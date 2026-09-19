package util

import (
	"compress/bzip2"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/klauspost/compress/zstd"
	"github.com/yeka/zip"
)

// archiveDecompressorsOnce guards the one-time global decompressor registration.
// archiveDecompressorsOnce 保证一次性全局解压器注册。
var archiveDecompressorsOnce sync.Once

// entryTimesExtraID is the private ZIP extra-field id carrying exact per-entry
// timestamps (the ASCII bytes "FN", for Fast Note).
//
// ZIP's standard headers only hold a 2-second-resolution modification time and
// have no creation-time field at all, so both are stored here instead of in a
// separate archive member. Readers skip unknown extra ids, so archives stay
// fully compatible with standard unzip tools.
//
// It deliberately does not reuse 0x5455 (Info-ZIP extended timestamp): that
// field holds 32-bit Unix seconds, which both drops milliseconds and overflows
// in 2038.
//
// entryTimesExtraID 是携带精确逐条目时间的私有 ZIP extra 字段 id（ASCII "FN"，
// 取自 Fast Note）。
//
// ZIP 标准头只有 2 秒精度的修改时间、且完全没有创建时间字段，因此两者都存在这里，
// 而不是额外增加一个归档成员。读取器会跳过未知的 extra id，归档与标准 unzip 工具
// 保持完全兼容。
//
// 这里刻意不复用 0x5455（Info-ZIP 扩展时间戳）：该字段存的是 32 位 Unix 秒，
// 既丢毫秒，又会在 2038 年溢出。
const entryTimesExtraID = 0x464E

// entryTimesPayloadLen is the fixed payload size: version + ctime + mtime.
// entryTimesPayloadLen 固定载荷长度：版本 + ctime + mtime。
const entryTimesPayloadLen = 17

// entryTimesVersion is the payload version written by encodeEntryTimes. It lets
// the layout evolve without a reader ever misreading a future format.
// entryTimesVersion 是 encodeEntryTimes 写入的载荷版本，使布局可以演进，
// 而读取端永远不会误读未来的格式。
const entryTimesVersion = 1

// EntryTimes carries the exact timestamps preserved for a single archive entry.
// Both values are millisecond epochs; 0 means "unknown".
// EntryTimes 记录单个归档条目保留的精确时间戳。两者都是毫秒时间戳，0 表示未知。
type EntryTimes struct {
	Ctime int64
	Mtime int64
}

// encodeEntryTimes builds the private extra-field block for one entry, ready to
// be appended to FileHeader.Extra.
// encodeEntryTimes 构造单个条目的私有 extra 字段块，可直接追加到 FileHeader.Extra。
func encodeEntryTimes(t EntryTimes) []byte {
	payload := make([]byte, entryTimesPayloadLen)
	payload[0] = entryTimesVersion
	binary.LittleEndian.PutUint64(payload[1:9], uint64(t.Ctime))
	binary.LittleEndian.PutUint64(payload[9:17], uint64(t.Mtime))

	block := make([]byte, 4+len(payload))
	binary.LittleEndian.PutUint16(block[0:2], entryTimesExtraID)
	binary.LittleEndian.PutUint16(block[2:4], uint16(len(payload)))
	copy(block[4:], payload)
	return block
}

// decodeEntryTimes scans an entry's raw Extra bytes for the private timestamp
// block. It returns ok=false when the block is absent, truncated, or not
// written by this package — so a third-party field that happens to reuse the id
// is ignored rather than misread.
// decodeEntryTimes 在条目的原始 Extra 字节中扫描私有时间块。块缺失、被截断或不是
// 本包写入时返回 ok=false——这样恰好复用了同一 id 的第三方字段会被忽略而不是误读。
func decodeEntryTimes(extra []byte) (EntryTimes, bool) {
	for len(extra) >= 4 {
		id := binary.LittleEndian.Uint16(extra[0:2])
		size := int(binary.LittleEndian.Uint16(extra[2:4]))
		if size > len(extra)-4 {
			return EntryTimes{}, false
		}
		if id == entryTimesExtraID {
			data := extra[4 : 4+size]
			if size == entryTimesPayloadLen && data[0] == entryTimesVersion {
				return EntryTimes{
					Ctime: int64(binary.LittleEndian.Uint64(data[1:9])),
					Mtime: int64(binary.LittleEndian.Uint64(data[9:17])),
				}, true
			}
			// Same id but not our layout — a collision or corruption. Keep
			// scanning instead of trusting it, and instead of giving up on any
			// valid block that might follow.
			// id 相同但布局不是我们的——属于碰撞或损坏。继续扫描，既不采信它，
			// 也不放弃后面可能存在的有效块。
		}
		extra = extra[4+size:]
	}
	return EntryTimes{}, false
}

// ZipOption customises archive writing.
// ZipOption 定制归档写入。
type ZipOption func(*zipOptions)

// zipOptions holds the resolved ZipOption values.
// zipOptions 保存解析后的 ZipOption 取值。
type zipOptions struct {
	entryTimes map[string]EntryTimes
}

// WithEntryTimes embeds exact per-entry timestamps into the archive, keyed by
// the entry's slash-separated path relative to the source directory. Use it to
// preserve what the ZIP headers cannot express: creation time, and a
// modification time with better than 2-second resolution. ReadEntryTimes reads
// them back.
//
// WithEntryTimes 将精确的逐条目时间戳写入归档，键为条目相对源目录、使用正斜杠的路径。
// 用于保留 ZIP 头无法表达的信息：创建时间，以及精度优于 2 秒的修改时间。
// 可用 ReadEntryTimes 读回。
func WithEntryTimes(times map[string]EntryTimes) ZipOption {
	return func(o *zipOptions) { o.entryTimes = times }
}

// ReadEntryTimes reads the exact timestamps embedded by WithEntryTimes from an
// archive's central directory, keyed by entry name.
//
// Only the central directory is read — no entry is decompressed — so this is
// cheap even for large archives. Entries without the private block, and archives
// written without WithEntryTimes, simply do not appear in the result.
//
// ReadEntryTimes 从归档的中央目录读取由 WithEntryTimes 写入的精确时间戳，键为条目名。
//
// 只读中央目录、不解压任何条目，因此即使归档很大开销也很低。没有该私有块的条目，
// 以及未使用 WithEntryTimes 写入的归档，都不会出现在结果中。
func ReadEntryTimes(source string) (map[string]EntryTimes, error) {
	reader, err := zip.OpenReader(source)
	if err != nil {
		return nil, err
	}
	defer reader.Close()

	times := make(map[string]EntryTimes)
	for _, f := range reader.File {
		if t, ok := decodeEntryTimes(f.Extra); ok {
			times[f.Name] = t
		}
	}
	return times, nil
}

// Zip compresses files or directories into a zip file
// source: path to file or directory
// target: path to output zip file
func Zip(source, target string) error {
	return ZipWithPassword(source, target, "")
}

// ZipWithPassword compresses files or directories into a zip file with password
func ZipWithPassword(source, target, password string, opts ...ZipOption) error {
	var cfg zipOptions
	for _, opt := range opts {
		opt(&cfg)
	}

	zipFile, err := os.Create(target)
	if err != nil {
		return err
	}
	defer zipFile.Close()

	archive := zip.NewWriter(zipFile)
	defer archive.Close()

	if _, err := os.Stat(source); err != nil {
		return err
	}

	return filepath.Walk(source, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Get relative path from source
		// 获取相对于 source 的路径
		relPath, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}

		// Skip root directory itself
		// 跳过根目录本身
		if relPath == "." {
			return nil
		}

		// Use / as separator in zip file (Standard requirements)
		// ZIP 文件内必须使用 / 作为路径分隔符
		relPath = filepath.ToSlash(relPath)

		header, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}

		// Use relative path as filename in zip
		// 使用相对路径作为压缩包内的文件名
		header.Name = relPath
		if info.IsDir() {
			header.Name += "/"
		} else {
			header.Method = zip.Deflate
		}

		// Embed the exact timestamps the ZIP headers cannot carry. The writer
		// appends its own blocks (Zip64, WinZip AES) to Extra, so appending here
		// is safe and both end up in the entry.
		// 嵌入 ZIP 头无法携带的精确时间戳。写入端会往 Extra 追加自己的块
		// （Zip64、WinZip AES），因此这里追加是安全的，两者都会写进条目。
		if t, ok := cfg.entryTimes[relPath]; ok && !info.IsDir() {
			header.Extra = append(header.Extra, encodeEntryTimes(t)...)
		}

		var writer io.Writer
		if password != "" && !info.IsDir() {
			// Set password and encryption method
			// 设置密码和加密方法
			header.SetPassword(password)
			// StandardEncryption is more compatible with Windows built-in ZIP tool
			// StandardEncryption 对 Windows 自带 ZIP 工具兼容性更好
			header.SetEncryptionMethod(zip.StandardEncryption)
			writer, err = archive.CreateHeader(header)
		} else {
			writer, err = archive.CreateHeader(header)
		}

		if err != nil {
			return err
		}

		if info.IsDir() {
			return nil
		}

		file, err := os.Open(path)
		if err != nil {
			return err
		}
		defer file.Close()

		_, err = io.Copy(writer, file)
		return err
	})
}

// UnzipWithPassword extracts a zip archive into the target directory,
// optionally decrypting password-protected entries (ZipCrypto and AES).
// Paths inside the archive are cleaned to prevent Zip Slip attacks.
// Supported compression methods: Store, Deflate, Bzip2 and Zstandard.
// Each extracted file inherits the archive entry's modification time so that
// callers (e.g. backup import) can read back the original mtime.
// UnzipWithPassword 将 zip 压缩包解压到目标目录，支持加密条目（ZipCrypto 与 AES）。
// 压缩包内的路径会被清理以防止 Zip Slip 攻击。
// 支持的压缩算法：Store、Deflate、Bzip2 与 Zstandard。
// 解压出的文件会继承归档条目的修改时间，便于调用方（如备份导入）读回原始 mtime。
func UnzipWithPassword(source, targetDir, password string) error {
	registerArchiveDecompressors()

	reader, err := zip.OpenReader(source)
	if err != nil {
		return err
	}
	defer reader.Close()

	for _, f := range reader.File {
		// Clean the archive path and confine it within targetDir
		// 清理压缩包内路径并限制在 targetDir 内
		name := filepath.Clean(f.Name)
		if strings.HasPrefix(name, "..") || filepath.IsAbs(name) {
			return fmt.Errorf("illegal file path in zip: %s", f.Name)
		}
		destPath := filepath.Join(targetDir, name)

		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(destPath, 0o755); err != nil {
				return err
			}
			continue
		}

		if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
			return err
		}

		if f.IsEncrypted() {
			f.SetPassword(password)
		}

		src, err := f.Open()
		if err != nil {
			return err
		}

		dest, err := os.OpenFile(destPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
		if err != nil {
			src.Close()
			return err
		}

		_, err = io.Copy(dest, src)
		src.Close()
		closeErr := dest.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}

		// Carry over the entry's timestamp; without this the extracted file would
		// simply show the extraction time.
		//
		// Prefer the exact timestamp embedded by WithEntryTimes, falling back to
		// the MS-DOS date/time fields that ZipWithPassword always writes (2-second
		// resolution). Both fields are 0 when the archive carries no timestamp at
		// all, in which case there is nothing to restore.
		//
		// 沿用条目的时间戳；不还原的话解压出来的文件只会显示解压时刻。
		//
		// 优先使用 WithEntryTimes 嵌入的精确时间，回退到 ZipWithPassword 始终会写的
		// MS-DOS 日期/时间字段（2 秒精度）。归档完全未携带时间戳时这两个字段都是 0，
		// 此时无需处理。
		if t, ok := decodeEntryTimes(f.Extra); ok && t.Mtime > 0 {
			mt := time.UnixMilli(t.Mtime)
			_ = os.Chtimes(destPath, mt, mt)
		} else if f.ModifiedDate != 0 || f.ModifiedTime != 0 {
			mt := f.FileInfo().ModTime()
			_ = os.Chtimes(destPath, mt, mt)
		}
	}
	return nil
}

// IsZipFile reports whether the file starts with a ZIP magic signature:
// PK\x03\x04 (local file header), PK\x05\x06 (empty archive/EOCD) or
// PK\x07\x08 (split/spanned archive).
// IsZipFile 判断文件是否以 ZIP 魔数开头：
// PK\x03\x04 (本地文件头)、PK\x05\x06 (空归档/EOCD) 或 PK\x07\x08 (分卷归档)。
func IsZipFile(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()

	header := make([]byte, 4)
	if _, err := io.ReadFull(f, header); err != nil {
		return false
	}
	return header[0] == 'P' && header[1] == 'K' &&
		((header[2] == 0x03 && header[3] == 0x04) ||
			(header[2] == 0x05 && header[3] == 0x06) ||
			(header[2] == 0x07 && header[3] == 0x08))
}

// registerArchiveDecompressors registers decompressors beyond the yeka/zip
// builtins (Store/Deflate): Bzip2 (12, stdlib) and Zstandard (93, klauspost).
// The registration is process-global and done exactly once.
// registerArchiveDecompressors 在 yeka/zip 内置算法（Store/Deflate）之外补充注册
// Bzip2 (12, 标准库) 与 Zstandard (93, klauspost/compress)。进程级全局注册且仅一次。
func registerArchiveDecompressors() {
	archiveDecompressorsOnce.Do(func() {
		zip.RegisterDecompressor(12, func(r io.Reader) io.ReadCloser {
			return io.NopCloser(bzip2.NewReader(r))
		})
		zip.RegisterDecompressor(93, func(r io.Reader) io.ReadCloser {
			zr, err := zstd.NewReader(r)
			if err != nil {
				return &errReadCloser{err: err}
			}
			return &zstdReadCloser{Decoder: zr}
		})
	})
}

// zstdReadCloser adapts *zstd.Decoder to io.ReadCloser (Close returns error).
// zstdReadCloser 将 *zstd.Decoder 适配为 io.ReadCloser（Close 返回错误）。
type zstdReadCloser struct {
	*zstd.Decoder
}

func (z *zstdReadCloser) Close() error {
	z.Decoder.Close()
	return nil
}

// errReadCloser is a minimal io.ReadCloser that always fails with its error,
// used when a decompressor cannot be initialized.
// errReadCloser 是一个总是返回预设错误的 io.ReadCloser，用于解压器初始化失败场景。
type errReadCloser struct {
	err error
}

func (e *errReadCloser) Read([]byte) (int, error) { return 0, e.err }
func (e *errReadCloser) Close() error             { return nil }

// ZipBytes creates a zip archive from a map of filenames and their contents (bytes)
func ZipBytes(files map[string][]byte, target string) error {
	zipFile, err := os.Create(target)
	if err != nil {
		return err
	}
	defer zipFile.Close()

	archive := zip.NewWriter(zipFile)
	defer archive.Close()

	for name, content := range files {
		writer, err := archive.Create(name)
		if err != nil {
			return err
		}
		_, err = writer.Write(content)
		if err != nil {
			return err
		}
	}

	return nil
}
