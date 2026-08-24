package service

import (
	"os"
	"path/filepath"
	"testing"

	"go.uber.org/zap"
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
	write(BackupManifestFileName, "{}")
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

func TestReadBackupManifest(t *testing.T) {
	dir := t.TempDir()

	s := &backupService{logger: zap.NewNop()}
	if m := s.readBackupManifest(dir); m != nil {
		t.Fatalf("expected nil manifest for missing file, got %+v", m)
	}

	data := []byte(`{"version":1,"type":"full","vault":"myVault","includes":{"notes":true,"attachments":true,"vaultConfig":true,"noteHistory":true},"settingsCount":3,"historyCount":7}`)
	if err := os.WriteFile(filepath.Join(dir, BackupManifestFileName), data, 0644); err != nil {
		t.Fatal(err)
	}

	m := s.readBackupManifest(dir)
	if m == nil {
		t.Fatal("expected manifest, got nil")
	}
	if m.Version != BackupManifestVersion || m.Vault != "myVault" || !m.Includes.NoteHistory || m.HistoryCount != 7 {
		t.Fatalf("manifest mismatch: %+v", m)
	}
}
