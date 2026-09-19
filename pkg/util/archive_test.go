package util

import (
	"archive/zip"
	"encoding/base64"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
)

func TestZipUnzipRoundTripWithPassword(t *testing.T) {
	dir := t.TempDir()
	srcDir := filepath.Join(dir, "src")
	if err := os.MkdirAll(filepath.Join(srcDir, ".obsidian"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "note.md"), []byte("# hello\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, ".obsidian", "app.json"), []byte("{\"a\":1}"), 0644); err != nil {
		t.Fatal(err)
	}

	zipPath := filepath.Join(dir, "backup.zip")
	password := "secret123"
	if err := ZipWithPassword(srcDir, zipPath, password); err != nil {
		t.Fatalf("ZipWithPassword() error = %v", err)
	}

	outDir := filepath.Join(dir, "out")
	if err := UnzipWithPassword(zipPath, outDir, password); err != nil {
		t.Fatalf("UnzipWithPassword() error = %v", err)
	}

	got, err := os.ReadFile(filepath.Join(outDir, "note.md"))
	if err != nil {
		t.Fatalf("read note.md: %v", err)
	}
	if string(got) != "# hello\n" {
		t.Fatalf("content mismatch: %q", string(got))
	}

	gotCfg, err := os.ReadFile(filepath.Join(outDir, ".obsidian", "app.json"))
	if err != nil {
		t.Fatalf("read .obsidian/app.json: %v", err)
	}
	if string(gotCfg) != "{\"a\":1}" {
		t.Fatalf("config content mismatch: %q", string(gotCfg))
	}
}

// An archive must carry the source file's modification time rather than the
// time it was packed: full/incremental backups are only useful for restore when
// the original timestamps survive the round trip.
// 归档必须携带源文件的修改时间而非打包时刻：只有原始时间戳能往返，全量/增量备份
// 对恢复才有意义。
func TestZipUnzipRoundTripPreservesModTime(t *testing.T) {
	dir := t.TempDir()
	srcDir := filepath.Join(dir, "src")
	if err := os.MkdirAll(filepath.Join(srcDir, "attachments"), 0o755); err != nil {
		t.Fatal(err)
	}

	// A fixed instant in the past, truncated to an even second because the ZIP
	// MS-DOS timestamp format only has 2s resolution.
	// 一个固定的过去时刻，截断到偶数秒——ZIP 的 MS-DOS 时间格式只有 2 秒精度。
	want := time.Date(2021, 3, 4, 5, 6, 8, 0, time.UTC)

	relPaths := []string{"note.md", filepath.Join("attachments", "a.bin")}
	for _, rel := range relPaths {
		p := filepath.Join(srcDir, rel)
		if err := os.WriteFile(p, []byte("payload"), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, want, want); err != nil {
			t.Fatal(err)
		}
	}

	zipPath := filepath.Join(dir, "backup.zip")
	if err := ZipWithPassword(srcDir, zipPath, ""); err != nil {
		t.Fatalf("ZipWithPassword() error = %v", err)
	}

	outDir := filepath.Join(dir, "out")
	if err := UnzipWithPassword(zipPath, outDir, ""); err != nil {
		t.Fatalf("UnzipWithPassword() error = %v", err)
	}

	for _, rel := range relPaths {
		info, err := os.Stat(filepath.Join(outDir, rel))
		if err != nil {
			t.Fatalf("stat %s: %v", rel, err)
		}
		if diff := info.ModTime().Sub(want); diff > 2*time.Second || diff < -2*time.Second {
			t.Errorf("%s mtime = %v, want ~%v (diff %v)",
				rel, info.ModTime().UTC(), want, diff)
		}
	}
}

func TestUnzipWithPassword_WrongPassword(t *testing.T) {
	dir := t.TempDir()
	srcDir := filepath.Join(dir, "src")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "data.txt"), []byte("top secret"), 0644); err != nil {
		t.Fatal(err)
	}

	zipPath := filepath.Join(dir, "enc.zip")
	if err := ZipWithPassword(srcDir, zipPath, "right"); err != nil {
		t.Fatal(err)
	}

	outDir := filepath.Join(dir, "out")
	if err := UnzipWithPassword(zipPath, outDir, "wrong"); err == nil {
		t.Fatal("expected error with wrong password, got nil")
	}
}

func TestUnzipWithPassword_ZipSlipRejected(t *testing.T) {
	dir := t.TempDir()
	zipPath := filepath.Join(dir, "evil.zip")

	// Hand-craft an archive containing a traversal entry
	if err := buildEvilZip(zipPath, "../evil.txt"); err != nil {
		t.Fatal(err)
	}

	outDir := filepath.Join(dir, "out")
	if err := UnzipWithPassword(zipPath, outDir, ""); err == nil {
		t.Fatal("expected error for traversal entry, got nil")
	}
	if _, err := os.Stat(filepath.Join(dir, "evil.txt")); err == nil {
		t.Fatal("traversal file escaped target directory")
	}
}

func buildEvilZip(path string, entryName string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	w := zip.NewWriter(f)
	entry, err := w.Create(entryName)
	if err != nil {
		return err
	}
	if _, err := entry.Write([]byte("evil")); err != nil {
		return err
	}
	return w.Close()
}

// TestUnzipWithPassword_Bzip2Zstd verifies that archives compressed with Bzip2 (12)
// and Zstandard (93) can be extracted after the extra decompressors are registered.
// The Bzip2 fixture is a real archive generated once by Python's bz2-backend zipfile
// (no Go bzip2 writer is available in the stdlib); Zstandard uses klauspost.
// TestUnzipWithPassword_Bzip2Zstd 校验注册补充解压器后 Bzip2(12) 与 Zstandard(93)
// 压缩的归档可正常解压。Bzip2 样本由 Python bz2 后端 zipfile 生成一次并内嵌
// （Go 标准库没有 bzip2 写入器）；Zstandard 使用 klauspost 库生成。
func TestUnzipWithPassword_Bzip2Zstd(t *testing.T) {
	dir := t.TempDir()

	// Bzip2 fixture entry: data.txt -> "hello bzip-ish"
	// Bzip2 样本条目：data.txt -> "hello bzip-ish"
	bzip2Zip := filepath.Join(dir, "bzip2.zip")
	fixture, err := base64.StdEncoding.DecodeString("UEsDBC4AAAAMACKCG11Qq7L2NAAAAA4AAAAIAAAAZGF0YS50eHRCWmg5MUFZJlNZVwLepQAAAhGAQAISZMgQIAAhoA00IBppo0xg5YUEF4u5IpwoSCuBb1KAUEsBAi4ALgAAAAwAIoIbXVCrsvY0AAAADgAAAAgAAAAAAAAAAAAAAIABAAAAAGRhdGEudHh0UEsFBgAAAAABAAEANgAAAFoAAAAAAA==")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bzip2Zip, fixture, 0644); err != nil {
		t.Fatal(err)
	}

	// Zstandard archive built with klauspost/compress/zstd (stdlib archive/zip
	// cannot write method 93); register it as a writer-side compressor.
	// 使用 klauspost/compress/zstd 构造 Zstandard 归档（stdlib archive/zip 无法写入
	// 方法 93），将其注册为写入侧压缩器。
	zstdZip := filepath.Join(dir, "zstd.zip")
	{
		zip.RegisterCompressor(93, func(w io.Writer) (io.WriteCloser, error) {
			return zstd.NewWriter(w)
		})
		f, err := os.Create(zstdZip)
		if err != nil {
			t.Fatal(err)
		}
		w := zip.NewWriter(f)
		h := &zip.FileHeader{Name: "data.txt", Method: 93}
		h.SetMode(0644)
		entry, err := w.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte("hello bzip-ish")); err != nil {
			t.Fatal(err)
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		f.Close()
	}

	cases := []struct {
		name string
		path string
	}{
		{"bzip2", bzip2Zip},
		{"zstd", zstdZip},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			outDir := filepath.Join(dir, "out-"+tc.name)
			if err := UnzipWithPassword(tc.path, outDir, ""); err != nil {
				t.Fatalf("UnzipWithPassword(%s) error = %v", tc.name, err)
			}
			got, err := os.ReadFile(filepath.Join(outDir, "data.txt"))
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != "hello bzip-ish" {
				t.Fatalf("content mismatch: %q", string(got))
			}
		})
	}
}

// The private extra field is how creation time survives at all: ZIP's standard
// headers cannot hold it, and truncate the modification time to 2 seconds. The
// timestamps here deliberately carry milliseconds so an exact match proves the
// extra field won over the MS-DOS fields.
//
// 私有 extra 字段是创建时间得以保留的唯一途径：ZIP 标准头装不下它，且会把修改时间
// 截断到 2 秒。这里的时间刻意带毫秒，精确匹配才能证明 extra 字段胜过 MS-DOS 字段。
func TestZipWithEntryTimesRoundTrip(t *testing.T) {
	dir := t.TempDir()
	srcDir := filepath.Join(dir, "src")
	if err := os.MkdirAll(filepath.Join(srcDir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	names := []string{"a.md", "sub/b.bin"}
	for _, rel := range names {
		if err := os.WriteFile(filepath.Join(srcDir, filepath.FromSlash(rel)), []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	ctime := time.Date(2019, 8, 7, 6, 5, 4, 321_000_000, time.UTC)
	mtime := time.Date(2022, 1, 2, 3, 4, 5, 678_000_000, time.UTC)
	want := make(map[string]EntryTimes, len(names))
	for _, name := range names {
		want[name] = EntryTimes{Ctime: ctime.UnixMilli(), Mtime: mtime.UnixMilli()}
	}

	zipPath := filepath.Join(dir, "backup.zip")
	if err := ZipWithPassword(srcDir, zipPath, "pw", WithEntryTimes(want)); err != nil {
		t.Fatalf("ZipWithPassword() error = %v", err)
	}

	got, err := ReadEntryTimes(zipPath)
	if err != nil {
		t.Fatalf("ReadEntryTimes() error = %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("ReadEntryTimes() returned %d entries, want %d", len(got), len(want))
	}
	for name, w := range want {
		if got[name] != w {
			t.Errorf("%s = %+v, want %+v", name, got[name], w)
		}
	}

	// Extraction must stamp the exact mtime onto the extracted file.
	// 解压必须把精确 mtime 打到解压出的文件上。
	outDir := filepath.Join(dir, "out")
	if err := UnzipWithPassword(zipPath, outDir, "pw"); err != nil {
		t.Fatalf("UnzipWithPassword() error = %v", err)
	}
	info, err := os.Stat(filepath.Join(outDir, "a.md"))
	if err != nil {
		t.Fatal(err)
	}
	if info.ModTime().UnixMilli() != mtime.UnixMilli() {
		t.Errorf("extracted mtime = %d, want %d", info.ModTime().UnixMilli(), mtime.UnixMilli())
	}

	// Unknown extra ids are skipped by standard readers, so the archive must stay
	// readable by the standard library.
	// 标准读取器会跳过未知 extra id，因此归档必须仍能被标准库读取。
	std, err := zip.OpenReader(zipPath)
	if err != nil {
		t.Fatalf("standard archive/zip cannot open the archive: %v", err)
	}
	defer std.Close()
	seen := make(map[string]bool)
	for _, f := range std.File {
		seen[f.Name] = true
	}
	for _, name := range names {
		if !seen[name] {
			t.Errorf("standard reader does not see entry %q", name)
		}
	}
}

// Archives written without WithEntryTimes must simply yield no timestamps rather
// than failing.
// 未使用 WithEntryTimes 写入的归档应只返回空结果，而不是报错。
func TestReadEntryTimes_EmptyWithoutOption(t *testing.T) {
	dir := t.TempDir()
	srcDir := filepath.Join(dir, "src")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "a.md"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}

	zipPath := filepath.Join(dir, "backup.zip")
	if err := ZipWithPassword(srcDir, zipPath, ""); err != nil {
		t.Fatalf("ZipWithPassword() error = %v", err)
	}

	got, err := ReadEntryTimes(zipPath)
	if err != nil {
		t.Fatalf("ReadEntryTimes() error = %v", err)
	}
	if len(got) != 0 {
		t.Errorf("ReadEntryTimes() = %v, want empty", got)
	}
}

// A third-party extra field that reuses our id with a different payload — or a
// truncated copy of ours — must be ignored, never misread as timestamps.
// 第三方字段若复用了我们的 id 但载荷不同，或我们的块被截断，都必须被忽略，
// 绝不能误读成时间戳。
func TestDecodeEntryTimes_RejectsForeignAndTruncated(t *testing.T) {
	// Same id, 8-byte payload instead of the expected 17.
	// id 相同，但载荷是 8 字节而非预期的 17 字节。
	foreign := []byte{0x4E, 0x46, 0x08, 0x00, 1, 2, 3, 4, 5, 6, 7, 8}
	if _, ok := decodeEntryTimes(foreign); ok {
		t.Error("decodeEntryTimes() accepted a foreign payload")
	}

	block := encodeEntryTimes(EntryTimes{Ctime: 1, Mtime: 2})
	if _, ok := decodeEntryTimes(block[:len(block)-1]); ok {
		t.Error("decodeEntryTimes() accepted a truncated block")
	}

	// An unrelated field followed by ours must still be found.
	// 无关字段在前、我们的块在后时仍应被找到。
	combined := append(append([]byte{}, foreign...), block...)
	got, ok := decodeEntryTimes(combined)
	if !ok {
		t.Fatal("decodeEntryTimes() did not find the block after a foreign one")
	}
	if got.Ctime != 1 || got.Mtime != 2 {
		t.Errorf("decodeEntryTimes() = %+v, want {Ctime:1 Mtime:2}", got)
	}
}
