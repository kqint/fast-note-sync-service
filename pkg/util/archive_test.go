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
