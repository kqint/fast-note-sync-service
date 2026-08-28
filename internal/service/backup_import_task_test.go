package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/haierkeys/fast-note-sync-service/internal/config"
	"github.com/haierkeys/fast-note-sync-service/internal/domain"
	"github.com/haierkeys/fast-note-sync-service/internal/domain/mocks"
	"github.com/haierkeys/fast-note-sync-service/internal/dto"
	"github.com/haierkeys/fast-note-sync-service/pkg/code"
	"github.com/haierkeys/fast-note-sync-service/pkg/util"
	"github.com/stretchr/testify/mock"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// newImportTestService builds a backupService wired with mock repositories for
// task-lifecycle tests. zshaped: notes-only archive → straight importing.
// 构造用于任务生命周期单测的 backupService（mock 仓储）。
func newImportTestService(t *testing.T) (*backupService, *mocks.MockNoteRepository, *mocks.MockVaultRepository, *mocks.MockFolderRepository, *mocks.MockFileRepository, *mocks.MockSettingRepository, *mocks.MockNoteHistoryRepository) {
	vaultRepo := new(mocks.MockVaultRepository)
	noteRepo := new(mocks.MockNoteRepository)
	folderRepo := new(mocks.MockFolderRepository)
	fileRepo := new(mocks.MockFileRepository)
	settingRepo := new(mocks.MockSettingRepository)
	historyRepo := new(mocks.MockNoteHistoryRepository)

	vaultRepo.On("GetByName", mock.Anything, "vault1", int64(12)).Return(&domain.Vault{ID: 1, Name: "vault1"}, nil)
	vaultRepo.On("UpdateNoteCountSize", mock.Anything, mock.Anything, mock.Anything, mock.Anything, int64(12)).Return(nil)
	vaultRepo.On("UpdateFileCountSize", mock.Anything, mock.Anything, mock.Anything, mock.Anything, int64(12)).Return(nil)

	// Defaults: no existing notes/files → create path
	noteRepo.On("GetAllByPathHash", mock.Anything, mock.Anything, int64(1), int64(12)).Return(nil, gorm.ErrRecordNotFound)
	noteRepo.On("Create", mock.Anything, mock.Anything, int64(12)).Return(&domain.Note{ID: 1}, nil)
	noteRepo.On("List", mock.Anything, int64(1), mock.Anything, mock.Anything, int64(12), mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return([]*domain.Note{}, nil)
	noteRepo.On("CountSizeSum", mock.Anything, int64(1), int64(12)).Return(&domain.CountSizeResult{}, nil)
	noteRepo.On("UpdateFID", mock.Anything, mock.Anything, mock.Anything, int64(12)).Return(nil)

	fileRepo.On("GetByPathHash", mock.Anything, mock.Anything, int64(1), int64(12)).Return(nil, gorm.ErrRecordNotFound)
	fileRepo.On("List", mock.Anything, int64(1), mock.Anything, mock.Anything, int64(12), mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return([]*domain.File{}, nil)
	fileRepo.On("CountSizeSum", mock.Anything, int64(1), int64(12)).Return(&domain.CountSizeResult{}, nil)
	fileRepo.On("UpdateFID", mock.Anything, mock.Anything, mock.Anything, int64(12)).Return(nil)

	settingRepo.On("GetByPathHash", mock.Anything, mock.Anything, int64(1), int64(12)).Return(nil, gorm.ErrRecordNotFound)
	settingRepo.On("Create", mock.Anything, mock.Anything, int64(12)).Return(&domain.Setting{ID: 1}, nil)

	historyRepo.On("Create", mock.Anything, mock.Anything, int64(12)).Return(&domain.NoteHistory{ID: 1}, nil)
	historyRepo.On("DeleteOldVersions", mock.Anything, mock.Anything, mock.Anything, mock.Anything, int64(12)).Return(nil)

	folderRepo.On("GetByPathHash", mock.Anything, mock.Anything, int64(1), int64(12)).Return(nil, gorm.ErrRecordNotFound)
	folderRepo.On("Create", mock.Anything, mock.Anything, int64(12)).Return(&domain.Folder{ID: 1}, nil)

	backupRepo := new(mocks.MockBackupRepository)

	s := &backupService{
		backupRepo:      backupRepo,
		noteRepo:        noteRepo,
		folderRepo:      folderRepo,
		fileRepo:        fileRepo,
		vaultRepo:       vaultRepo,
		settingRepo:     settingRepo,
		noteHistoryRepo: historyRepo,
		tempPath:        t.TempDir(),
		logger:          zap.NewNop(),
		syncTimers:      make(map[int64]*time.Timer),
		runningTasks:    make(map[int64]context.CancelFunc),
		ctx:             context.Background(),
		cancel:          func() {},
	}
	return s, noteRepo, vaultRepo, folderRepo, fileRepo, settingRepo, historyRepo
}

// buildNotesOnlyZip creates a valid zip containing a single note.
// 构造仅含单个笔记的合法 zip。
func buildNotesOnlyZip(t *testing.T, dir string) string {
	t.Helper()
	srcDir := filepath.Join(dir, "src")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "a.md"), []byte("# hi"), 0644); err != nil {
		t.Fatal(err)
	}
	zipPath := filepath.Join(dir, "notes.zip")
	if err := util.Zip(srcDir, zipPath); err != nil {
		t.Fatal(err)
	}
	return zipPath
}

func waitForPhase(t *testing.T, s *backupService, uid int64, taskID string, target string) *dto.BackupImportTaskStatusDTO {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		st, err := s.GetImportTaskStatus(context.Background(), uid, taskID)
		if err != nil {
			t.Fatalf("GetImportTaskStatus error: %v", err)
		}
		if st.Phase == target {
			return st
		}
		if st.Phase == "failed" || st.Phase == "cancelled" {
			return st
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("phase %q not reached within timeout", target)
	return nil
}

// TestImportTaskLifecycle_Success submits a notes-only archive and expects a
// successful import with progress counts populated.
// 提交流程：仅笔记归档 → 成功，进度与结果计数正确。
func TestImportTaskLifecycle_Success(t *testing.T) {
	s, _, _, _, _, _, _ := newImportTestService(t)
	zipPath := buildNotesOnlyZip(t, t.TempDir())

	task, err := s.SubmitImportBackup(context.Background(), 12, &dto.BackupImportRequest{
		Vault:        "vault1",
		IncludeNotes: true,
	}, zipPath)
	if err != nil {
		t.Fatalf("SubmitImportBackup error: %v", err)
	}
	if task.Phase != importPhaseValidating {
		t.Fatalf("expected validating phase, got %s", task.Phase)
	}

	st := waitForPhase(t, s, 12, task.TaskID, importPhaseSuccess)
	if st.Result == nil {
		t.Fatal("expected result on success")
	}
	if st.Result.Notes != 1 {
		t.Fatalf("expected 1 imported note, got %d", st.Result.Notes)
	}
	if st.Totals.Notes != 1 {
		t.Fatalf("expected totals.notes=1, got %d", st.Totals.Notes)
	}
	if st.Progress.Notes != 1 {
		t.Fatalf("expected progress.notes=1, got %d", st.Progress.Notes)
	}
}

// TestImportTaskLifecycle_AwaitPartialConfirm submits an archive missing the
// requested vault config section, confirms partial import and expects success.
// 缺少所选部分 → await_partial；confirm(true) → 仅导入已有部分并成功。
func TestImportTaskLifecycle_AwaitPartialConfirm(t *testing.T) {
	s, _, _, _, _, _, _ := newImportTestService(t)
	zipPath := buildNotesOnlyZip(t, t.TempDir())

	_, err := s.SubmitImportBackup(context.Background(), 12, &dto.BackupImportRequest{
		Vault:              "vault1",
		IncludeNotes:       true,
		IncludeVaultConfig: true,
	}, zipPath)
	if err != nil {
		t.Fatal(err)
	}
	// taskID lost in this shortcut: use the Sync.Map directly via a helper is
	// overkill; instead re-submit and read phase list via status by id retrieval.
	tasks := []interface{}{}
	s.importTasks.Range(func(k, v any) bool { tasks = append(tasks, k); return false })
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task registered, got %d", len(tasks))
	}
	taskID := tasks[0].(string)

	st := waitForPhase(t, s, 12, taskID, importPhaseAwaitPartial)
	if st.Missing == nil || len(st.Missing) != 1 || st.Missing[0] != "vaultConfig" {
		t.Fatalf("expected missing vaultConfig, got %v", st.Missing)
	}
	if st.Available == nil || !st.Available.Notes {
		t.Fatalf("expected available.notes=true, got %+v", st.Available)
	}

	if _, err := s.ConfirmImportTask(context.Background(), 12, taskID, true); err != nil {
		t.Fatalf("ConfirmImportTask error: %v", err)
	}

	st = waitForPhase(t, s, 12, taskID, importPhaseSuccess)
	if st.Result == nil || st.Result.Notes != 1 || st.Result.Settings != 0 {
		t.Fatalf("unexpected result: %+v", st.Result)
	}
}

// TestImportTaskLifecycle_ConfirmCancel: awaiting confirm, user cancels.
// await_partial 状态下用户取消 → cancelled 终态。
func TestImportTaskLifecycle_ConfirmCancel(t *testing.T) {
	s, _, _, _, _, _, _ := newImportTestService(t)
	zipPath := buildNotesOnlyZip(t, t.TempDir())

	_, _ = s.SubmitImportBackup(context.Background(), 12, &dto.BackupImportRequest{
		Vault:              "vault1",
		IncludeNotes:       true,
		IncludeVaultConfig: true,
	}, zipPath)

	taskID := ""
	s.importTasks.Range(func(k, v any) bool { taskID = k.(string); return false })
	st := waitForPhase(t, s, 12, taskID, importPhaseAwaitPartial)
	if st.Phase != importPhaseAwaitPartial {
		t.Fatalf("expected await_partial, got %s", st.Phase)
	}

	if _, err := s.ConfirmImportTask(context.Background(), 12, taskID, false); err != nil {
		t.Fatalf("ConfirmImportTask error: %v", err)
	}
	st = waitForPhase(t, s, 12, taskID, importPhaseCancelled)
	if st.Phase != importPhaseCancelled {
		t.Fatalf("expected cancelled, got %s", st.Phase)
	}
}

// TestImportTaskLifecycle_AwaitTimeout: no confirm arrives within the (short,
// test-overridden) window → auto cancelled.
// 未确认超时 → 自动取消。
func TestImportTaskLifecycle_AwaitTimeout(t *testing.T) {
	old := backupImportAwaitPartialTimeout
	backupImportAwaitPartialTimeout = 40 * time.Millisecond
	defer func() { backupImportAwaitPartialTimeout = old }()

	s, _, _, _, _, _, _ := newImportTestService(t)
	zipPath := buildNotesOnlyZip(t, t.TempDir())

	_, _ = s.SubmitImportBackup(context.Background(), 12, &dto.BackupImportRequest{
		Vault:              "vault1",
		IncludeNotes:       true,
		IncludeVaultConfig: true,
	}, zipPath)

	taskID := ""
	s.importTasks.Range(func(k, v any) bool { taskID = k.(string); return false })
	st := waitForPhase(t, s, 12, taskID, importPhaseCancelled)
	if st.Phase != importPhaseCancelled {
		t.Fatalf("expected cancelled, got %s", st.Phase)
	}
}

// TestImportTask_UnknownTask: unknown task id returns 549.
// 未知任务 ID → 549。
func TestImportTask_UnknownTask(t *testing.T) {
	s, _, _, _, _, _, _ := newImportTestService(t)
	_, err := s.GetImportTaskStatus(context.Background(), 12, "nope")
	if err == nil {
		t.Fatal("expected error for unknown task")
	}
	if c, ok := err.(*code.Code); !ok || c.Code() != code.ErrorBackupImportTaskNotFound.Code() {
		t.Fatalf("expected 549 code, got %v", err)
	}
}

// TestImportTask_SizeLimit: oversized upload rejected synchronously with 548.
// 超大小限制 → 同步拒绝 548。
func TestImportTask_SizeLimit(t *testing.T) {
	s, _, _, _, _, _, _ := newImportTestService(t)
	enable := true
	s.appSettings = &config.AppSettings{
		BackupImportSizeLimitEnabled: &enable,
		BackupImportSizeLimit:        "4B",
	}

	zipPath := buildNotesOnlyZip(t, t.TempDir())
	info, _ := os.Stat(zipPath)
	if info.Size() <= 4 {
		t.Fatalf("fixture too small for test: %d", info.Size())
	}

	_, err := s.SubmitImportBackup(context.Background(), 12, &dto.BackupImportRequest{
		Vault:        "vault1",
		IncludeNotes: true,
	}, zipPath)
	if err == nil {
		t.Fatal("expected 548 for oversized upload")
	}
	if c, ok := err.(*code.Code); !ok || c.Code() != code.ErrorBackupImportFileTooLarge.Code() {
		t.Fatalf("expected 548 code, got %v", err)
	}
}
