package util

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/yeka/zip"
)

func TestIsZipFile(t *testing.T) {
	dir := t.TempDir()

	buildFixtureZip := func(path string, content string) {
		f, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		w := zip.NewWriter(f)
		entry, err := w.Create("data.txt")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		f.Close()
	}
	zipPath := filepath.Join(dir, "real.zip")
	buildFixtureZip(zipPath, "hello")
	if !IsZipFile(zipPath) {
		t.Fatal("real zip should be detected as zip")
	}

	// Empty archive: only EOCD record (PK\x05\x06)
	emptyPath := filepath.Join(dir, "empty.zip")
	{
		f, err := os.Create(emptyPath)
		if err != nil {
			t.Fatal(err)
		}
		w := zip.NewWriter(f)
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		f.Close()
	}
	if !IsZipFile(emptyPath) {
		t.Fatal("empty zip should be detected as zip")
	}

	// Plain text file
	textPath := filepath.Join(dir, "notzip.txt")
	if err := os.WriteFile(textPath, []byte("not a zip file"), 0644); err != nil {
		t.Fatal(err)
	}
	if IsZipFile(textPath) {
		t.Fatal("text file should not be detected as zip")
	}

	// Fake exe with MZ magic
	exePath := filepath.Join(dir, "fake.exe")
	if err := os.WriteFile(exePath, []byte("MZ\x90\x00fake binary"), 0644); err != nil {
		t.Fatal(err)
	}
	if IsZipFile(exePath) {
		t.Fatal("MZ file should not be detected as zip")
	}

	// Empty file
	emptyPath2 := filepath.Join(dir, "zero.bin")
	if err := os.WriteFile(emptyPath2, nil, 0644); err != nil {
		t.Fatal(err)
	}
	if IsZipFile(emptyPath2) {
		t.Fatal("empty file should not be detected as zip")
	}

	// Missing file
	if IsZipFile(filepath.Join(dir, "missing.zip")) {
		t.Fatal("missing file should not be detected as zip")
	}
}

func TestParseSizeGBTB(t *testing.T) {
	cases := []struct {
		in   string
		want int64
	}{
		{"2GB", 2 * 1024 * 1024 * 1024},
		{"1TB", 1024 * 1024 * 1024 * 1024},
		{"512MB", 512 * 1024 * 1024},
		{"1KB", 1024},
		{"1024B", 1024},
		{"", 42},
		{"invalid", 42},
		{"0GB", 42},
	}
	for _, c := range cases {
		if got := ParseSize(c.in, 42); got != c.want {
			t.Fatalf("ParseSize(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}
