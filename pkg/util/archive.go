package util

import (
	"compress/bzip2"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/klauspost/compress/zstd"
	"github.com/yeka/zip"
)

// archiveDecompressorsOnce guards the one-time global decompressor registration.
// archiveDecompressorsOnce 保证一次性全局解压器注册。
var archiveDecompressorsOnce sync.Once

// Zip compresses files or directories into a zip file
// source: path to file or directory
// target: path to output zip file
func Zip(source, target string) error {
	return ZipWithPassword(source, target, "")
}

// ZipWithPassword compresses files or directories into a zip file with password
func ZipWithPassword(source, target, password string) error {
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
// UnzipWithPassword 将 zip 压缩包解压到目标目录，支持加密条目（ZipCrypto 与 AES）。
// 压缩包内的路径会被清理以防止 Zip Slip 攻击。
// 支持的压缩算法：Store、Deflate、Bzip2 与 Zstandard。
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
