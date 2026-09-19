package service

import (
	"os"
	"path/filepath"
	"testing"
)

func TestClassifyArchiveFiles(t *testing.T) {
	dir := t.TempDir()
	write := func(rel string, content string) {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}

	write("notes/todo.md", "# todo")
	write("img.png", "pngdata")
	write(".obsidian/app.json", "{}")
	write(".obsidian/themes/x.css", "body{}")
	write(BackupNoteHistoryFileName, "[]")

	notes, files, settings := classifyArchiveFiles(dir)

	if len(notes) != 1 || notes[0] != "notes/todo.md" {
		t.Fatalf("unexpected notes bucket: %v", notes)
	}
	if len(files) != 1 || files[0] != "img.png" {
		t.Fatalf("unexpected files bucket: %v", files)
	}
	if len(settings) != 2 {
		t.Fatalf("unexpected settings bucket: %v", settings)
	}
	for _, s := range settings {
		if filepath.ToSlash(s)[:len(VaultConfigDirPrefix)] != VaultConfigDirPrefix {
			t.Fatalf("setting outside .obsidian/: %v", s)
		}
	}
}

func TestClassifyArchiveFiles_SkipsFnsDir(t *testing.T) {
	dir := t.TempDir()
	write := func(rel string, content string) {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}

	write("note.md", "# n")
	write(BackupNoteHistoryFileName, "[]")
	// 用户自定义文件也不能利用 .fns 命名空间逃逸
	write(".fns/other.txt", "meta")

	notes, files, settings := classifyArchiveFiles(dir)
	if len(notes) != 1 || len(files) != 0 || len(settings) != 0 {
		t.Fatalf("unexpected buckets: notes=%v files=%v settings=%v", notes, files, settings)
	}
}

func TestCountBackupHistoryEntries(t *testing.T) {
	dir := t.TempDir()
	if got := countBackupHistoryEntries(dir); got != 0 {
		t.Fatalf("expected 0 entries for missing file, got %d", got)
	}

	metaDir := filepath.Join(dir, filepath.FromSlash(BackupMetaDirPrefix))
	if err := os.MkdirAll(metaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	data := []byte(`[{"path":"a.md","version":1,"content":"x"},{"path":"a.md","version":2,"content":"y"}]`)
	if err := os.WriteFile(filepath.Join(metaDir, "note_history.json"), data, 0644); err != nil {
		t.Fatal(err)
	}
	if got := countBackupHistoryEntries(dir); got != 2 {
		t.Fatalf("expected 2 entries, got %d", got)
	}

	if err := os.WriteFile(filepath.Join(metaDir, "note_history.json"), []byte(`not-json`), 0644); err != nil {
		t.Fatal(err)
	}
	if got := countBackupHistoryEntries(dir); got != 0 {
		t.Fatalf("expected 0 entries for invalid json, got %d", got)
	}
}
