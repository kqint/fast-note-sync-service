package util

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"
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
