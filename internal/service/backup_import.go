package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/haierkeys/fast-note-sync-service/internal/domain"
	"github.com/haierkeys/fast-note-sync-service/internal/dto"
	"github.com/haierkeys/fast-note-sync-service/pkg/code"
	"github.com/haierkeys/fast-note-sync-service/pkg/syncgate"
	"github.com/haierkeys/fast-note-sync-service/pkg/timex"
	"github.com/haierkeys/fast-note-sync-service/pkg/util"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// Import task phases.
// 导入任务阶段。
const (
	importPhaseValidating   = "validating"
	importPhaseAwaitPartial = "await_partial"
	importPhaseImporting    = "importing"
	importPhaseSuccess      = "success"
	importPhaseFailed       = "failed"
	importPhaseCancelled    = "cancelled"
)

const (
	// backupImportTaskRetention how long finished tasks stay memory for polling.
	// backupImportTaskRetention 已完成任务在内存中保留供轮询的时长。
	backupImportTaskRetention = time.Hour
	// backupImportFIDPagination page size for FID/history listing batches.
	// backupImportFIDPagination 分页遍历时的每页大小。
	backupImportFIDPagination = 1000
	// backupImportProgressLogInterval log an info line every N imported items.
	// backupImportProgressLogInterval 每导入 N 条输出一次进度日志。
	backupImportProgressLogInterval = 200
)

// backupImportAwaitPartialTimeout is the time a task waits in await_partial for
// the user's confirm before cancelling itself (var so tests can shorten it).
// backupImportAwaitPartialTimeout await_partial 等待确认的超时时间（变量便于测试缩短）。
var backupImportAwaitPartialTimeout = 15 * time.Minute

// backupImportTask is the in-memory state of one async import task.
// backupImportTask 单个异步导入任务的内存状态。
type backupImportTask struct {
	taskID    string
	uid       int64
	mu        sync.Mutex
	phase     string
	zipPath   string
	workDir   string
	req       dto.BackupImportRequest
	available dto.BackupImportAvailabilityDTO
	missing   []string
	totals    dto.BackupImportTotalsDTO
	progress  dto.BackupImportProgressDTO
	result    *dto.BackupImportResultDTO
	errCode   int
	errMsg    string
	finishedAt time.Time
	confirmCh chan bool
}

func (t *backupImportTask) snapshot() *dto.BackupImportTaskStatusDTO {
	t.mu.Lock()
	defer t.mu.Unlock()
	return &dto.BackupImportTaskStatusDTO{
		TaskID:    t.taskID,
		Phase:     t.phase,
		Totals:    t.totals,
		Progress:  t.progress,
		Available: copyAvailability(t.available),
		Missing:   append([]string(nil), t.missing...),
		Result:    t.result,
		Error:     t.errorDTO(),
	}
}

func (t *backupImportTask) errorDTO() *dto.BackupImportTaskErrorDTO {
	if t.errCode == 0 {
		return nil
	}
	return &dto.BackupImportTaskErrorDTO{Code: t.errCode, Message: t.errMsg}
}

func copyAvailability(a dto.BackupImportAvailabilityDTO) *dto.BackupImportAvailabilityDTO {
	// Keep nil for zero struct so JSON omits before phase fills it
	// 零值时保持 nil，保证 JSON 在该阶段前省略该字段
	if !a.Notes && !a.VaultConfig && !a.NoteHistory {
		return nil
	}
	c := a
	return &c
}

// SubmitImportBackup validates the upload and starts the async import.
// 校验上传文件并启动异步导入任务。
func (s *backupService) SubmitImportBackup(ctx context.Context, uid int64, req *dto.BackupImportRequest, zipPath string) (*dto.BackupImportTaskDTO, error) {
	// Remove the upload on every early-error path; once the task owns it, the
	// goroutine performs removal when it finishes.
	// 所有错误路径清理上传文件；任务接管后由 goroutine 在结束时删除。
	owned := false
	defer func() {
		if !owned && zipPath != "" {
			_ = os.Remove(zipPath)
		}
	}()

	if zipPath == "" {
		return nil, code.ErrorBackupImportFileEmpty
	}
	if req == nil || req.Vault == "" {
		return nil, code.ErrorBackupVaultRequired
	}
	if !req.IncludeNotes && !req.IncludeVaultConfig && !req.IncludeNoteHistory {
		return nil, code.ErrorBackupImportOptionInvalid
	}
	if req.IncludeNoteHistory && !req.IncludeNotes {
		return nil, code.ErrorBackupImportOptionInvalid
	}

	// Authoritative size check (layer 3; handler already checked Content-Length
	// and multipart header earlier).
	// 权威大小校验（第三层；handler 已在前两处检查 Content-Length 与 multipart 头）。
	if s.importLimitOn {
		info, err := os.Stat(zipPath)
		if err != nil {
			return nil, code.ErrorBackupImportFileEmpty.WithDetails(err.Error())
		}
		if info.Size() > s.importLimitBytes {
			return nil, code.ErrorBackupImportFileTooLarge.
				WithDetails(fmt.Sprintf("%d > %d bytes", info.Size(), s.importLimitBytes))
		}
	}

	// Early vault existence check for fast feedback.
	// 预先校验仓库存在，快速反馈。
	vault, err := s.vaultRepo.GetByName(ctx, req.Vault, uid)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, code.ErrorVaultNotFound
		}
		return nil, err
	}
	if vault == nil {
		return nil, code.ErrorVaultNotFound
	}

	taskID := util.GetRandomString(16)
	task := &backupImportTask{
		taskID:    taskID,
		uid:       uid,
		phase:     importPhaseValidating,
		zipPath:   zipPath,
		req:       *req,
		confirmCh: make(chan bool, 1),
	}
	s.importTasks.Store(taskID, task)
	owned = true
	s.sweepFinishedImportTasksLocked()

	s.wg.Add(1)
	go s.runImportTask(task)

	return &dto.BackupImportTaskDTO{TaskID: taskID, Phase: importPhaseValidating}, nil
}

// GetImportTaskStatus returns the status snapshot of a uid-owned task.
// 返回当前用户导入任务的状态快照。
func (s *backupService) GetImportTaskStatus(ctx context.Context, uid int64, taskID string) (*dto.BackupImportTaskStatusDTO, error) {
	task, err := s.loadImportTask(uid, taskID)
	if err != nil {
		return nil, err
	}
	return task.snapshot(), nil
}

// ConfirmImportTask confirms or cancels an await_partial import task.
// 确认或取消处于 await_partial 的导入任务。
func (s *backupService) ConfirmImportTask(ctx context.Context, uid int64, taskID string, allowPartial bool) (*dto.BackupImportTaskStatusDTO, error) {
	task, err := s.loadImportTask(uid, taskID)
	if err != nil {
		return nil, err
	}
	task.mu.Lock()
	phase := task.phase
	task.mu.Unlock()
	if phase != importPhaseAwaitPartial {
		return task.snapshot(), code.ErrorBackupImportTaskNotFound
	}

	select {
	case task.confirmCh <- allowPartial:
	default:
		return task.snapshot(), code.ErrorBackupImportTaskNotFound
	}
	return task.snapshot(), nil
}

// loadImportTask resolves task by id, enforcing uid ownership, retention and
// unknown-task semantics (549).
// 按 ID 解析任务，校验 uid 归属、保留期与未知任务语义（549）。
func (s *backupService) loadImportTask(uid int64, taskID string) (*backupImportTask, error) {
	v, ok := s.importTasks.Load(taskID)
	if !ok {
		return nil, code.ErrorBackupImportTaskNotFound
	}
	task := v.(*backupImportTask)
	if task.uid != uid {
		return nil, code.ErrorBackupImportTaskNotFound
	}

	task.mu.Lock()
	defer task.mu.Unlock()
	if !task.finishedAt.IsZero() && time.Since(task.finishedAt) > backupImportTaskRetention {
		// Expired finished task: drop it and report as missing.
		// 超期完成态任务：移除并视为不存在。
		s.importTasks.Delete(taskID)
		return nil, code.ErrorBackupImportTaskNotFound
	}
	return task, nil
}

// sweepFinishedImportTasksLocked opportunistically removes expired tasks.
// 惰性清理已过期任务。
func (s *backupService) sweepFinishedImportTasksLocked() {
	now := time.Now()
	s.importTasks.Range(func(key, value any) bool {
		task := value.(*backupImportTask)
		task.mu.Lock()
		expired := !task.finishedAt.IsZero() && now.Sub(task.finishedAt) > backupImportTaskRetention
		task.mu.Unlock()
		if expired {
			s.importTasks.Delete(key)
		}
		return true
	})
}

// runImportTask is the background execution of one import task.
// 后台执行单个导入任务。
func (s *backupService) runImportTask(task *backupImportTask) {
	defer s.wg.Done()

	// Any outcome removes staged files; the task entry itself stays in memory
	// for retention polling.
	// 任何终态都清理暂存文件；任务条目保留在内存中供轮询。
	defer func() {
		_ = os.Remove(task.zipPath)
		if task.workDir != "" {
			_ = os.RemoveAll(task.workDir)
		}
	}()

	if err := s.runImportTaskInner(task); err != nil {
		codeErr, ok := err.(*code.Code)
		if !ok {
			codeErr = code.ErrorServerInternal.WithDetails(err.Error())
		}
		s.finishImportTask(task, codeErr.Code(), codeErr.Error())
	}
}

// runImportTaskInner performs the phases before success; returns nil on success.
// 执行各阶段，成功返回 nil。
func (s *backupService) runImportTaskInner(task *backupImportTask) error {
	uid := task.uid
	req := &task.req
	ctx := s.ctx

	// ---- validating: unzip + scan ----
	// ---- 校验阶段：解压 + 零信任扫描 ----
	if err := os.MkdirAll(s.backupStagingDir(), 0o755); err != nil {
		return err
	}
	workDir, err := os.MkdirTemp(s.backupStagingDir(), fmt.Sprintf("import_%d_", uid))
	if err != nil {
		return err
	}
	task.workDir = workDir
	extractDir := filepath.Join(workDir, "extract")

	if err := util.UnzipWithPassword(task.zipPath, extractDir, req.Password); err != nil {
		s.logger.Warn("Failed to unzip backup import file",
			zap.Int64("uid", uid), zap.String("taskId", task.taskID), zap.Error(err))
		return code.ErrorBackupImportZipFailed.WithDetails(err.Error())
	}

	notePaths, filePaths, settingPaths := classifyArchiveFiles(extractDir)
	historyCount := countBackupHistoryEntries(extractDir)
	available := dto.BackupImportAvailabilityDTO{
		Notes:       len(notePaths)+len(filePaths) > 0,
		VaultConfig: len(settingPaths) > 0,
		NoteHistory: historyCount > 0,
	}
	task.mu.Lock()
	task.available = available
	task.totals = dto.BackupImportTotalsDTO{
		Notes:       int64(len(notePaths)),
		Files:       int64(len(filePaths)),
		Settings:    int64(len(settingPaths)),
		NoteHistory: int64(historyCount),
	}
	task.mu.Unlock()

	var missing []string
	if req.IncludeNotes && !available.Notes {
		missing = append(missing, "notes")
	}
	if req.IncludeVaultConfig && !available.VaultConfig {
		missing = append(missing, "vaultConfig")
	}
	if req.IncludeNoteHistory && !available.NoteHistory {
		missing = append(missing, "noteHistory")
	}

	// ---- await_partial: wait for the user's confirmation ----
	// ---- 等待用户对部分导入的确认 ----
	if len(missing) > 0 && !req.AllowPartial {
		task.mu.Lock()
		task.missing = missing
		task.phase = importPhaseAwaitPartial
		task.mu.Unlock()

		select {
		case v := <-task.confirmCh:
			if !v {
				task.mu.Lock()
				task.phase = importPhaseCancelled
				task.finishedAt = time.Now()
				task.mu.Unlock()
				return nil
			}
		case <-time.After(backupImportAwaitPartialTimeout):
			task.mu.Lock()
			task.phase = importPhaseCancelled
			task.finishedAt = time.Now()
			task.mu.Unlock()
			return nil
		case <-ctx.Done():
			task.mu.Lock()
			task.phase = importPhaseCancelled
			task.finishedAt = time.Now()
			task.mu.Unlock()
			return nil
		}

		if !available.Notes && !available.VaultConfig && !available.NoteHistory {
			return code.ErrorBackupImportInvalidArchive.WithDetails("archive contains no importable content")
		}
		req.IncludeNotes = req.IncludeNotes && available.Notes
		req.IncludeVaultConfig = req.IncludeVaultConfig && available.VaultConfig
		req.IncludeNoteHistory = req.IncludeNoteHistory && available.NoteHistory
		if !req.IncludeNotes && !req.IncludeVaultConfig && !req.IncludeNoteHistory {
			return code.ErrorBackupImportOptionInvalid
		}
	} else {
		// No missing sections or partial allowed up front: nothing to shrink.
		// If archive has no content at all, reject early.
		if !available.Notes && !available.VaultConfig && !available.NoteHistory {
			return code.ErrorBackupImportInvalidArchive.WithDetails("archive contains no importable content")
		}
	}

	// ---- importing ----
	// ---- 导入阶段 ----
	vault, err := s.vaultRepo.GetByName(ctx, req.Vault, uid)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return code.ErrorVaultNotFound
		}
		return err
	}
	if vault == nil {
		return code.ErrorVaultNotFound
	}

	task.mu.Lock()
	task.phase = importPhaseImporting
	task.mu.Unlock()

	// Pause sync for the target vault while importing; resume on exit.
	// 导入期间暂停目标仓库同步，退出时恢复。
	syncgate.Pause(uid, vault.Name)
	defer syncgate.Resume(uid, vault.Name)

	result := &dto.BackupImportResultDTO{Vault: vault.Name}

	if req.IncludeNotes {
		result.Notes, result.Files, err = s.importNotesAndAttachments(ctx, uid, vault.ID, extractDir, notePaths, filePaths, task)
		if err != nil {
			return err
		}
	}
	if req.IncludeVaultConfig {
		result.Settings, err = s.importVaultSettings(ctx, uid, vault.ID, extractDir, settingPaths, task)
		if err != nil {
			return err
		}
	}
	if req.IncludeNoteHistory {
		result.NoteHistory, result.SkippedHistory, err = s.importNoteHistory(ctx, uid, vault.ID, extractDir, task)
		if err != nil {
			return err
		}
	}

	task.mu.Lock()
	task.result = result
	task.phase = importPhaseSuccess
	task.finishedAt = time.Now()
	task.mu.Unlock()

	s.logger.Info("Backup import task completed",
		zap.Int64("uid", uid),
		zap.String("taskId", task.taskID),
		zap.Bool("includeNotes", req.IncludeNotes),
		zap.Bool("includeVaultConfig", req.IncludeVaultConfig),
		zap.Bool("includeNoteHistory", req.IncludeNoteHistory),
		zap.Int64("notes", result.Notes),
		zap.Int64("files", result.Files),
		zap.Int64("settings", result.Settings),
		zap.Int64("noteHistory", result.NoteHistory),
		zap.Int64("skippedHistory", result.SkippedHistory),
	)

	// Trigger debounced sync/backup tasks so external mirrors pick up imported data.
	// 触发防抖同步/备份任务。
	s.NotifyUpdated(uid)
	return nil
}

// finishImportTask records a terminal failed/cancelled phase.
// 记录失败终态。
func (s *backupService) finishImportTask(task *backupImportTask, errCode int, errMsg string) {
	task.mu.Lock()
	defer task.mu.Unlock()
	if task.phase != importPhaseFailed {
		task.errCode = errCode
		task.errMsg = errMsg
		task.phase = importPhaseFailed
		task.finishedAt = time.Now()
	}
	s.logger.Warn("Backup import task failed",
		zap.Int64("uid", task.uid),
		zap.String("taskId", task.taskID),
		zap.Int("code", errCode),
		zap.String("message", errMsg))
}

// importProgress tracks task progress across the import subroutines.
// 跟踪任务处理进度的辅助结构。
type importProgress struct {
	service *backupService
	task    *backupImportTask
}

func (p *importProgress) add(notes, files, settings, history, skipped int64) {
	task := p.task
	task.mu.Lock()
	task.progress.Notes += notes
	task.progress.Files += files
	task.progress.Settings += settings
	task.progress.NoteHistory += history
	task.progress.SkippedHistory += skipped
	total := task.progress.Notes + task.progress.Files + task.progress.Settings + task.progress.NoteHistory
	shouldLog := total > 0 && total%backupImportProgressLogInterval == 0
	task.mu.Unlock()
	if shouldLog {
		p.service.logger.Info("Backup import progress",
			zap.Int64("uid", task.uid),
			zap.String("taskId", task.taskID),
			zap.Int64("done", total))
	}
}

// Note: archives carry no manifest, and import validation derives section
// availability by scanning the extracted files directly (classifyArchiveFiles).
// 说明：归档不携带清单文件，导入端通过直接扫描解压文件确定各部分可用性（见 classifyArchiveFiles）。

// countBackupHistoryEntries streams the archived note history data file
// (.fns/note_history.json) once to count entries without loading full payloads.
// 流式统计备份包中笔记历史数据文件 (.fns/note_history.json) 的条目数，
// 避免一次性载入全部内容。
func countBackupHistoryEntries(extractDir string) int {
	count, err := streamBackupHistory(extractDir, nil)
	if err != nil {
		return 0
	}
	return count
}

// streamBackupHistory decodes the archived history json with a streaming
// decoder: first pass counts entries, second pass (only when fn is set)
// invokes fn per entry for insertion, keeping memory flat.
// 流式两遍解析历史 JSON：第一遍计数；fn 非空时第二遍逐条回调插入，保持内存平稳。
func streamBackupHistory(extractDir string, fn func(*BackupNoteHistoryEntry) error) (int, error) {
	path := filepath.Join(extractDir, filepath.FromSlash(BackupNoteHistoryFileName))
	open := func() (*os.File, error) {
		f, err := os.Open(path)
		if os.IsNotExist(err) {
			return nil, nil
		}
		return f, err
	}

	count := 0
	first, err := open()
	if err != nil {
		return 0, err
	}
	if first == nil {
		return 0, nil
	}
	dec := json.NewDecoder(first)
	if _, err := dec.Token(); err != nil {
		first.Close()
		return 0, err
	}
	for dec.More() {
		var e BackupNoteHistoryEntry
		if err := dec.Decode(&e); err != nil {
			first.Close()
			return count, err
		}
		count++
	}
	first.Close()

	if fn != nil {
		second, err := open()
		if err != nil {
			return count, err
		}
		if second == nil {
			return count, nil
		}
		dec2 := json.NewDecoder(second)
		if _, err := dec2.Token(); err != nil {
			second.Close()
			return count, err
		}
		for dec2.More() {
			var e BackupNoteHistoryEntry
			if err := dec2.Decode(&e); err != nil {
				second.Close()
				return count, err
			}
			if err := fn(&e); err != nil {
				second.Close()
				return count, err
			}
		}
		second.Close()
	}
	return count, nil
}

// classifyArchiveFiles splits extracted files into notes (*.md), attachments and
// vault config (.obsidian/) buckets by their archive-relative path. Files under
// the reserved ".fns/" metadata namespace are skipped entirely.
// 根据压缩包内相对路径将解压文件分为笔记 (*.md)、附件与仓库配置 (.obsidian/) 三类。
// 保留的 ".fns/" 元数据命名空间内的文件会被整体跳过。
func classifyArchiveFiles(extractDir string) (notes, files []string, settings []string) {
	_ = filepath.Walk(extractDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(extractDir, path)
		if relErr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		switch {
		case strings.HasPrefix(rel, BackupMetaDirPrefix):
			// Archive metadata (.fns/) is not user data
			// 归档元数据 (.fns/) 不属于用户数据
			return nil
		case strings.HasPrefix(rel, VaultConfigDirPrefix):
			settings = append(settings, rel)
		case strings.HasSuffix(strings.ToLower(rel), ".md"):
			notes = append(notes, rel)
		default:
			files = append(files, rel)
		}
		return nil
	})
	return
}

// importNotesAndAttachments upserts notes (*.md) and attachments with an
// overwrite strategy, then rebuilds folder FIDs and vault statistics.
// 以覆盖策略更新或创建笔记 (*.md) 与附件，随后重建文件夹 FID 与仓库统计。
func (s *backupService) importNotesAndAttachments(ctx context.Context, uid, vaultID int64, extractDir string, notePaths, filePaths []string, task *backupImportTask) (int64, int64, error) {
	nowMs := timex.Now().UnixMilli()
	var importedNotes, importedFiles int64
	progress := &importProgress{service: s, task: task}

	for _, relPath := range notePaths {
		if ctx.Err() != nil {
			return importedNotes, importedFiles, ctx.Err()
		}
		content, err := os.ReadFile(filepath.Join(extractDir, filepath.FromSlash(relPath)))
		if err != nil {
			s.logger.Warn("Skipping unreadable note in backup import",
				zap.String("path", relPath), zap.Error(err))
			continue
		}

		pathHash := util.EncodeHash32(relPath)
		contentStr := string(content)
		existing, getErr := s.noteRepo.GetAllByPathHash(ctx, pathHash, vaultID, uid)

		switch {
		case getErr == nil && existing != nil:
			action := domain.NoteActionModify
			if existing.Action == domain.NoteActionDelete {
				action = domain.NoteActionCreate
			}
			existing.VaultID = vaultID
			existing.Path = relPath
			existing.Content = contentStr
			existing.ContentHash = util.EncodeHash32(contentStr)
			existing.Size = int64(len(content))
			existing.Mtime = nowMs
			existing.Action = action
			existing.Rename = 0
			existing.Version++
			if _, err := s.noteRepo.Update(ctx, existing, uid); err != nil {
				return importedNotes, importedFiles, code.ErrorDBQuery.WithDetails(err.Error())
			}
		case getErr == nil:
			fallthrough
		case errors.Is(getErr, gorm.ErrRecordNotFound):
			newNote := &domain.Note{
				VaultID:     vaultID,
				Path:        relPath,
				PathHash:    pathHash,
				Content:     contentStr,
				ContentHash: util.EncodeHash32(contentStr),
				Size:        int64(len(content)),
				Mtime:       nowMs,
				Ctime:       nowMs,
				Action:      domain.NoteActionCreate,
			}
			if _, err := s.noteRepo.Create(ctx, newNote, uid); err != nil {
				return importedNotes, importedFiles, code.ErrorDBQuery.WithDetails(err.Error())
			}
		default:
			return importedNotes, importedFiles, code.ErrorDBQuery.WithDetails(getErr.Error())
		}
		importedNotes++
		progress.add(1, 0, 0, 0, 0)
	}

	// Mirror the DAO content-folder scheme (storage/vault/u_{uid}/file) so the
	// staged temp file lands on the same volume as its final file.dat destination
	// 与 DAO 内容目录方案保持一致 (storage/vault/u_{uid}/file)，确保暂存临时文件
	// 与最终 file.dat 目标位于同一卷，便于仓储层重命名落位
	fileStorageDir := filepath.Join("storage", "vault", fmt.Sprintf("u_%d", uid), "file")
	for _, relPath := range filePaths {
		if ctx.Err() != nil {
			return importedNotes, importedFiles, ctx.Err()
		}
		srcPath := filepath.Join(extractDir, filepath.FromSlash(relPath))
		info, statErr := os.Stat(srcPath)
		if statErr != nil {
			s.logger.Warn("Skipping unreadable attachment in backup import",
				zap.String("path", relPath), zap.Error(statErr))
			continue
		}

		tempPath, err := stageAttachmentFile(fileStorageDir, srcPath)
		if err != nil {
			return importedNotes, importedFiles, code.ErrorFileSaveFailed.WithDetails(err.Error())
		}

		contentHash, hashErr := util.HashFileContent32(srcPath)
		if hashErr != nil {
			_ = os.Remove(tempPath)
			s.logger.Warn("Failed to hash attachment in backup import",
				zap.String("path", relPath), zap.Error(hashErr))
			continue
		}

		pathHash := util.EncodeHash32(relPath)
		existing, getErr := s.fileRepo.GetByPathHash(ctx, pathHash, vaultID, uid)

		switch {
		case getErr == nil && existing != nil:
			action := domain.FileActionModify
			if existing.Action == domain.FileActionDelete {
				action = domain.FileActionCreate
			}
			existing.VaultID = vaultID
			existing.Path = relPath
			existing.ContentHash = contentHash
			existing.SavePath = tempPath
			existing.Size = info.Size()
			existing.Mtime = nowMs
			existing.Action = action
			existing.Rename = 0
			if _, err := s.fileRepo.Update(ctx, existing, uid); err != nil {
				_ = os.Remove(tempPath)
				return importedNotes, importedFiles, code.ErrorDBQuery.WithDetails(err.Error())
			}
		case getErr == nil:
			fallthrough
		case errors.Is(getErr, gorm.ErrRecordNotFound):
			newFile := &domain.File{
				VaultID:     vaultID,
				Path:        relPath,
				PathHash:    pathHash,
				ContentHash: contentHash,
				SavePath:    tempPath,
				Size:        info.Size(),
				Mtime:       nowMs,
				Ctime:       nowMs,
				Action:      domain.FileActionCreate,
			}
			if _, err := s.fileRepo.Create(ctx, newFile, uid); err != nil {
				_ = os.Remove(tempPath)
				return importedNotes, importedFiles, code.ErrorDBQuery.WithDetails(err.Error())
			}
		default:
			_ = os.Remove(tempPath)
			return importedNotes, importedFiles, code.ErrorDBQuery.WithDetails(getErr.Error())
		}
		importedFiles++
		progress.add(0, 1, 0, 0, 0)
	}

	// Rebuild folder records/FIDs for imported resources
	// 为导入的资源重建文件夹记录/FID
	if err := s.rebuildResourceFIDs(ctx, uid, vaultID); err != nil {
		s.logger.Warn("Failed to rebuild folder FIDs after import",
			zap.Int64("uid", uid), zap.Int64("vaultId", vaultID), zap.Error(err))
	}

	// Refresh vault statistics from authoritative per-repo aggregates
	// 以仓储聚合结果刷新仓库统计
	if noteStat, err := s.noteRepo.CountSizeSum(ctx, vaultID, uid); err == nil && noteStat != nil {
		_ = s.vaultRepo.UpdateNoteCountSize(ctx, noteStat.Size, noteStat.Count, vaultID, uid)
	}
	if fileStat, err := s.fileRepo.CountSizeSum(ctx, vaultID, uid); err == nil && fileStat != nil {
		_ = s.vaultRepo.UpdateFileCountSize(ctx, fileStat.Size, fileStat.Count, vaultID, uid)
	}

	return importedNotes, importedFiles, nil
}

// stageAttachmentFile copies an extracted attachment into the user's file storage
// directory (same volume as its final destination) so repository Create/Update can
// rename it into place as file.dat.
// 将解压出的附件复制到用户附件存储目录（与最终目标同卷），供仓储层 Create/Update
// 通过重命名落位为 file.dat。返回临时文件路径。
func stageAttachmentFile(fileStorageDir, srcPath string) (string, error) {
	if err := os.MkdirAll(fileStorageDir, 0o755); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(fileStorageDir, "import_*.dat")
	if err != nil {
		return "", err
	}
	tmpName := tmp.Name()

	src, err := os.Open(srcPath)
	if err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return "", err
	}
	defer src.Close()

	if _, err := io.Copy(tmp, src); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return "", err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return "", err
	}
	return tmpName, nil
}

// importVaultSettings upserts .obsidian config records with an overwrite strategy.
// 以覆盖策略更新或创建 .obsidian 配置记录。
func (s *backupService) importVaultSettings(ctx context.Context, uid, vaultID int64, extractDir string, settingPaths []string, task *backupImportTask) (int64, error) {
	nowMs := timex.Now().UnixMilli()
	var count int64
	progress := &importProgress{service: s, task: task}

	for _, relPath := range settingPaths {
		if ctx.Err() != nil {
			return count, ctx.Err()
		}
		content, err := os.ReadFile(filepath.Join(extractDir, filepath.FromSlash(relPath)))
		if err != nil {
			s.logger.Warn("Skipping unreadable vault config in backup import",
				zap.String("path", relPath), zap.Error(err))
			continue
		}

		pathHash := util.EncodeHash32(relPath)
		contentStr := string(content)
		existing, getErr := s.settingRepo.GetByPathHash(ctx, pathHash, vaultID, uid)

		switch {
		case getErr == nil && existing != nil:
			action := domain.SettingActionModify
			if existing.Action == domain.SettingActionDelete {
				action = domain.SettingActionCreate
			}
			existing.VaultID = vaultID
			existing.Action = action
			existing.Path = relPath
			existing.Content = contentStr
			existing.ContentHash = util.EncodeHash32(contentStr)
			existing.Size = int64(len(content))
			existing.Mtime = nowMs
			existing.Rename = 0
			if _, err := s.settingRepo.Update(ctx, existing, uid); err != nil {
				return count, code.ErrorDBQuery.WithDetails(err.Error())
			}
		case getErr == nil:
			fallthrough
		case errors.Is(getErr, gorm.ErrRecordNotFound):
			newSetting := &domain.Setting{
				VaultID:     vaultID,
				Action:      domain.SettingActionCreate,
				Path:        relPath,
				PathHash:    pathHash,
				Content:     contentStr,
				ContentHash: util.EncodeHash32(contentStr),
				Size:        int64(len(content)),
				Ctime:       nowMs,
				Mtime:       nowMs,
			}
			if _, err := s.settingRepo.Create(ctx, newSetting, uid); err != nil {
				return count, code.ErrorDBQuery.WithDetails(err.Error())
			}
		default:
			return count, code.ErrorDBQuery.WithDetails(getErr.Error())
		}
		count++
		progress.add(0, 0, 1, 0, 0)
	}
	return count, nil
}

// importNoteHistory restores archived note history versions. For every note that
// has entries in the archive, its current history versions are removed first
// (overwrite strategy), then archived entries are re-created with original
// metadata; sidecar content/patch files are written by the repository layer.
// Note paths are resolved against existing notes after the notes import phase.
//
// 恢复归档的笔记历史版本。对于备份包中存在历史条目的笔记，先删除其当前历史版本
// （覆盖策略），再按原始元数据重建归档条目；内容/补丁 sidecar 文件由仓储层写入。
// 笔记路径在笔记导入阶段之后解析到现有笔记。
func (s *backupService) importNoteHistory(ctx context.Context, uid, vaultID int64, extractDir string, task *backupImportTask) (int64, int64, error) {
	path := filepath.Join(extractDir, filepath.FromSlash(BackupNoteHistoryFileName))
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			// Archive contains no history section (incremental backup or none available)
			// 备份包不含历史部分（增量备份或不可用）
			return 0, 0, nil
		}
		return 0, 0, err
	}

	// First pass: count + collect note paths present in the archive.
	// 第一遍：计数并收集备份包涉及的笔记路径。
	pathSet := make(map[string]struct{})
	var err error
	_, err = streamBackupHistory(extractDir, func(e *BackupNoteHistoryEntry) error {
		pathSet[e.Path] = struct{}{}
		return nil
	})
	if err != nil {
		return 0, 0, code.ErrorBackupImportZipFailed.WithDetails("invalid note history data: " + err.Error())
	}
	if len(pathSet) == 0 {
		return 0, 0, nil
	}

	notes, err := s.listNotesPaginated(ctx, vaultID, uid)
	if err != nil {
		return 0, 0, code.ErrorDBQuery.WithDetails(err.Error())
	}
	idByPath := make(map[string]int64, len(notes))
	for _, n := range notes {
		p := n.Path
		if filepath.Ext(p) != ".md" {
			p += ".md"
		}
		idByPath[filepath.ToSlash(p)] = n.ID
	}

	// Clear existing history only for notes referenced by the archive.
	// 仅清空备份包引用的笔记的现有历史。
	affectedNotes := make(map[int64]struct{})
	for p := range pathSet {
		if id, ok := idByPath[p]; ok {
			affectedNotes[id] = struct{}{}
		}
	}
	cutoff := timex.Now().UnixMilli() + 60_000 // future cutoff removes everything for affected notes
	for noteID := range affectedNotes {
		if err := s.noteHistoryRepo.DeleteOldVersions(ctx, noteID, cutoff, 0, uid); err != nil {
			return 0, 0, code.ErrorDBQuery.WithDetails(err.Error())
		}
	}

	var imported, skipped int64
	progress := &importProgress{service: s, task: task}
	_, err = streamBackupHistory(extractDir, func(e *BackupNoteHistoryEntry) error {
		noteID, ok := idByPath[e.Path]
		if !ok {
			skipped++
			return nil
		}
		h := &domain.NoteHistory{
			NoteID:        noteID,
			VaultID:       vaultID,
			Path:          e.Path,
			DiffPatch:     e.DiffPatch,
			Content:       e.Content,
			ContentHash:   e.ContentHash,
			ClientName:    e.ClientName,
			ClientType:    e.ClientType,
			ClientVersion: e.ClientVersion,
			Version:       e.Version,
			CreatedAt:     e.CreatedAt,
			UpdatedAt:     e.CreatedAt,
		}
		if h.Version <= 0 {
			h.Version = 1
		}
		if h.CreatedAt.IsZero() {
			h.CreatedAt = time.Now()
			h.UpdatedAt = h.CreatedAt
		}
		if _, err := s.noteHistoryRepo.Create(ctx, h, uid); err != nil {
			s.logger.Warn("Failed to import note history entry",
				zap.Int64("uid", uid),
				zap.String("path", e.Path),
				zap.Int64("version", e.Version),
				zap.Error(err))
			skipped++
			return nil
		}
		imported++
		progress.add(0, 0, 0, 1, 0)
		return nil
	})
	if err != nil {
		return imported, skipped, err
	}
	return imported, skipped, nil
}

// listNotesPaginated lists all notes of a vault in batches to avoid loading
// everything at once for huge vaults.
// 分页列出仓库全部笔记，避免大仓库一次性载入。
func (s *backupService) listNotesPaginated(ctx context.Context, vaultID, uid int64) ([]*domain.Note, error) {
	var result []*domain.Note
	page := 1
	for {
		batch, err := s.noteRepo.List(ctx, vaultID, page, backupImportFIDPagination, uid, "", false, "", false, "", "", nil)
		if err != nil {
			return nil, err
		}
		result = append(result, batch...)
		if len(batch) < backupImportFIDPagination {
			break
		}
		page++
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
	}
	return result, nil
}

// listFilesPaginated lists all attachments of a vault in batches.
// 分页列出仓库全部附件。
func (s *backupService) listFilesPaginated(ctx context.Context, vaultID, uid int64) ([]*domain.File, error) {
	var result []*domain.File
	page := 1
	for {
		batch, err := s.fileRepo.List(ctx, vaultID, page, backupImportFIDPagination, uid, "", false, "", "")
		if err != nil {
			return nil, err
		}
		result = append(result, batch...)
		if len(batch) < backupImportFIDPagination {
			break
		}
		page++
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
	}
	return result, nil
}

// rebuildResourceFIDs ensures folder records exist for all parent paths in the
// vault and assigns FIDs to notes/attachments (mirrors folder service logic).
// 确保仓库内所有父路径存在文件夹记录并为笔记/附件分配 FID (与文件夹服务逻辑一致)。
func (s *backupService) rebuildResourceFIDs(ctx context.Context, uid, vaultID int64) error {
	notes, err := s.listNotesPaginated(ctx, vaultID, uid)
	if err != nil {
		return err
	}
	for _, n := range notes {
		if n.Action == domain.NoteActionDelete {
			continue
		}
		fid, err := s.ensureFolderFID(ctx, uid, vaultID, n.Path)
		if err != nil {
			return err
		}
		if fid != n.FID {
			_ = s.noteRepo.UpdateFID(ctx, n.ID, fid, uid)
		}
	}

	files, err := s.listFilesPaginated(ctx, vaultID, uid)
	if err != nil {
		return err
	}
	for _, f := range files {
		if f.Action == domain.FileActionDelete {
			continue
		}
		fid, err := s.ensureFolderFID(ctx, uid, vaultID, f.Path)
		if err != nil {
			return err
		}
		if fid != f.FID {
			_ = s.fileRepo.UpdateFID(ctx, f.ID, fid, uid)
		}
	}
	return nil
}

// ensureFolderFID walks each path segment and creates/restores folder records,
// returning the FID of the deepest folder (0 for root-level resources).
// 逐级遍历路径段并创建/恢复文件夹记录，返回最深文件夹的 FID (根级资源为 0)。
func (s *backupService) ensureFolderFID(ctx context.Context, uid, vaultID int64, resourcePath string) (int64, error) {
	path := strings.Trim(resourcePath, "/")
	idx := strings.LastIndex(path, "/")
	if idx <= 0 {
		return 0, nil
	}
	folderPath := path[:idx]

	parts := strings.Split(folderPath, "/")
	var currentFID int64
	nowMs := timex.Now().UnixMilli()
	for i := range parts {
		currentPath := strings.Join(parts[:i+1], "/")
		pathHash := util.EncodeHash32(currentPath)

		folder, err := s.folderRepo.GetByPathHash(ctx, pathHash, vaultID, uid)
		switch {
		case err == nil && folder != nil:
			if folder.Action == domain.FolderActionDelete {
				folder.Action = domain.FolderActionCreate
				folder.Ctime = nowMs
				folder.Mtime = nowMs
				folder, err = s.folderRepo.Update(ctx, folder, uid)
				if err != nil {
					return 0, err
				}
			}
			currentFID = folder.ID
		case errors.Is(err, gorm.ErrRecordNotFound):
			created, createErr := s.folderRepo.Create(ctx, &domain.Folder{
				VaultID:  vaultID,
				Action:   domain.FolderActionCreate,
				Path:     currentPath,
				PathHash: pathHash,
				Level:    int64(i + 1),
				FID:      currentFID,
				Ctime:    nowMs,
				Mtime:    nowMs,
			}, uid)
			if createErr != nil {
				return 0, createErr
			}
			currentFID = created.ID
		case err == nil:
			currentFID = 0
		default:
			return 0, err
		}
	}
	return currentFID, nil
}
