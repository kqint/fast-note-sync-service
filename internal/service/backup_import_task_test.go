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

	// Import history persistence. One shared row stands in for the DB row:
	// CreateImportHistory copies the submitted fields onto it and stamps an ID,
	// mirroring the real repository, so tests can assert both the submit-time
	// snapshot and the terminal state written afterwards.
	// 导入历史落库。用一行共享记录代替数据库记录：CreateImportHistory 把提交的字段
	// 复制过去并打上 ID（与真实仓储一致），便于断言提交时快照与之后的终态。
	stubRow := &domain.BackupImportHistory{}
	backupRepo.On("CreateImportHistory", mock.Anything, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			*stubRow = *args.Get(1).(*domain.BackupImportHistory)
			stubRow.ID = 1
		}).Return(stubRow, nil)
	backupRepo.On("SaveImportHistory", mock.Anything, mock.Anything, mock.Anything).Return(nil)

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

// readImportHistory returns a snapshot of the task's history row, copied while
// holding the task mutex. It must copy: the service keeps mutating the same
// struct in finalizeImportHistory, so handing the caller the live pointer would
// let it read fields outside the lock and trip the race detector.
// 返回任务历史记录的快照副本（在任务锁内拷贝）。必须拷贝：服务会在
// finalizeImportHistory 中持续修改同一个结构体，若把真实指针交给调用方，
// 调用方就会在锁外读取字段，从而触发竞态检测。
func readImportHistory(t *testing.T, s *backupService, taskID string) *domain.BackupImportHistory {
	t.Helper()
	v, ok := s.importTasks.Load(taskID)
	if !ok {
		t.Fatalf("task %q not found", taskID)
	}
	task := v.(*backupImportTask)
	task.mu.Lock()
	defer task.mu.Unlock()
	if task.history == nil {
		return nil
	}
	snapshot := *task.history
	return &snapshot
}

// waitForTerminalHistory polls until the task's history row leaves the running
// status. finalizeImportHistory runs as a deferred call, i.e. slightly after the
// phase itself flips, so the phase check alone is not enough.
// 轮询直到历史记录离开 running 状态。终态回填是 defer 调用，略晚于阶段切换，
// 因此仅检查阶段还不够。
func waitForTerminalHistory(t *testing.T, s *backupService, taskID string) *domain.BackupImportHistory {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if h := readImportHistory(t, s, taskID); h != nil && h.Status != domain.BackupImportStatusRunning {
			return h
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("import history for task %q never reached a terminal status", taskID)
	return nil
}

// TestImportHistory_SuccessPersistsTerminalState covers the happy path: the row is
// created at submit time (running) and finalized to success with the import counts.
// 成功路径：提交时落库（running），终态回填为 success 并带上导入计数。
func TestImportHistory_SuccessPersistsTerminalState(t *testing.T) {
	s, _, _, _, _, _, _ := newImportTestService(t)
	zipPath := buildNotesOnlyZip(t, t.TempDir())

	task, err := s.SubmitImportBackup(context.Background(), 12, &dto.BackupImportRequest{
		Vault:        "vault1",
		IncludeNotes: true,
		FileName:     "notes.zip",
	}, zipPath)
	if err != nil {
		t.Fatalf("SubmitImportBackup error: %v", err)
	}

	// Submitted row must exist immediately, before the task has done any work.
	// 任务开始工作前，提交记录就必须已经存在。
	submitted := readImportHistory(t, s, task.TaskID)
	if submitted == nil {
		t.Fatal("expected an import history row at submit time")
	}
	if submitted.Status != domain.BackupImportStatusRunning {
		t.Fatalf("expected running at submit time, got %d", submitted.Status)
	}
	if submitted.FileName != "notes.zip" || submitted.VaultName != "vault1" {
		t.Fatalf("unexpected submitted row: %+v", submitted)
	}
	if submitted.FileSize <= 0 {
		t.Fatalf("expected file size to be recorded, got %d", submitted.FileSize)
	}

	waitForPhase(t, s, 12, task.TaskID, importPhaseSuccess)
	h := waitForTerminalHistory(t, s, task.TaskID)
	if h.Status != domain.BackupImportStatusSuccess {
		t.Fatalf("expected success status, got %d", h.Status)
	}
	if h.NotesCount != 1 {
		t.Fatalf("expected notesCount=1, got %d", h.NotesCount)
	}
	if h.EndTime.IsZero() || h.DurationMs <= 0 {
		t.Fatalf("expected end time and duration to be filled, got end=%v duration=%dms", h.EndTime, h.DurationMs)
	}
}

// TestImportHistory_CancelPersistsTerminalState verifies that a task cancelled inside
// runImportTaskInner (three early `return nil` branches) still gets its history
// finalized — those paths never reach finishImportTask.
// 验证在 runImportTaskInner 内部取消的任务也会回填历史——这三条分支不会经过 finishImportTask。
func TestImportHistory_CancelPersistsTerminalState(t *testing.T) {
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

	taskID := ""
	s.importTasks.Range(func(k, v any) bool { taskID = k.(string); return false })

	waitForPhase(t, s, 12, taskID, importPhaseAwaitPartial)
	if _, err := s.ConfirmImportTask(context.Background(), 12, taskID, false); err != nil {
		t.Fatalf("ConfirmImportTask error: %v", err)
	}

	waitForPhase(t, s, 12, taskID, importPhaseCancelled)
	h := waitForTerminalHistory(t, s, taskID)
	if h.Status != domain.BackupImportStatusCancelled {
		t.Fatalf("expected cancelled status, got %d", h.Status)
	}
	if h.EndTime.IsZero() {
		t.Fatal("expected end time to be filled on cancel")
	}
}

// TestImportHistory_FailedPersistsTerminalState verifies a failed task records the
// failure status and the error message.
// 验证失败任务会记录失败状态与错误信息。
func TestImportHistory_FailedPersistsTerminalState(t *testing.T) {
	s, _, _, _, _, _, _ := newImportTestService(t)

	// Not a zip: the task fails during the validating phase.
	// 非 zip 文件：任务在校验阶段失败。
	badPath := filepath.Join(t.TempDir(), "broken.zip")
	if err := os.WriteFile(badPath, []byte("definitely not a zip"), 0644); err != nil {
		t.Fatal(err)
	}

	task, err := s.SubmitImportBackup(context.Background(), 12, &dto.BackupImportRequest{
		Vault:        "vault1",
		IncludeNotes: true,
	}, badPath)
	if err != nil {
		t.Fatalf("SubmitImportBackup error: %v", err)
	}

	waitForPhase(t, s, 12, task.TaskID, importPhaseFailed)
	h := waitForTerminalHistory(t, s, task.TaskID)
	if h.Status != domain.BackupImportStatusFailed {
		t.Fatalf("expected failed status, got %d", h.Status)
	}
	if h.Message == "" {
		t.Fatal("expected the failure message to be persisted")
	}
}
