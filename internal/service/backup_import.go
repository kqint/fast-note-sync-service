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
	"time"

	"github.com/haierkeys/fast-note-sync-service/internal/domain"
	"github.com/haierkeys/fast-note-sync-service/internal/dto"
	"github.com/haierkeys/fast-note-sync-service/pkg/code"
	"github.com/haierkeys/fast-note-sync-service/pkg/timex"
	"github.com/haierkeys/fast-note-sync-service/pkg/util"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// ImportBackup imports a backup archive into the target vault.
// Current strategy: full overwrite — existing items with the same path are
// replaced by archive content, missing items are created; unrelated current
// data is left untouched. Note history is restored per note: existing versions
// of notes present in the archive are cleared before inserting archived ones.
//
// 导入备份包到目标仓库。
// 当前策略：全部覆盖 —— 同名路径的现有数据被备份包内容替换，缺失的条目被创建；
// 与备份无关的现有数据保持不变。笔记历史按笔记恢复：先清除存在于备份包中的
// 笔记的现有历史版本，再写入归档的历史版本。
func (s *backupService) ImportBackup(ctx context.Context, uid int64, req *dto.BackupImportRequest, zipPath string) (*dto.BackupImportResultDTO, error) {
	defer os.Remove(zipPath)

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

	// Stage the extraction under the app temp path so it can be swept on startup
	// 将解压目录置于应用临时路径下，便于启动时清理
	if err := os.MkdirAll(s.backupStagingDir(), 0o755); err != nil {
		return nil, err
	}
	workDir, err := os.MkdirTemp(s.backupStagingDir(), fmt.Sprintf("import_%d_", uid))
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(workDir)

	extractDir := filepath.Join(workDir, "extract")
	if err := util.UnzipWithPassword(zipPath, extractDir, req.Password); err != nil {
		s.logger.Warn("Failed to unzip backup import file",
			zap.Int64("uid", uid),
			zap.String("vault", vault.Name),
			zap.Error(err))
		return nil, code.ErrorBackupImportZipFailed.WithDetails(err.Error())
	}

	manifest := s.readBackupManifest(extractDir)
	if manifest != nil && manifest.Version > BackupManifestVersion {
		return nil, code.ErrorBackupImportUnsupported
	}

	notePaths, filePaths, settingPaths := classifyArchiveFiles(extractDir)

	result := &dto.BackupImportResultDTO{Vault: vault.Name}

	// 1. Notes & attachments (option 1)
	// 1. 笔记和附件 (选项 1)
	if req.IncludeNotes {
		result.Notes, result.Files, err = s.importNotesAndAttachments(ctx, uid, vault.ID, extractDir, notePaths, filePaths)
		if err != nil {
			return nil, err
		}
	}

	// 2. Vault config (.obsidian) (option 2)
	// 2. 仓库配置 (.obsidian) (选项 2)
	if req.IncludeVaultConfig {
		result.Settings, err = s.importVaultSettings(ctx, uid, vault.ID, extractDir, settingPaths)
		if err != nil {
			return nil, err
		}
	}

	// 3. Note history (option 3, depends on option 1)
	// 3. 笔记历史 (选项 3，依赖选项 1)
	if req.IncludeNoteHistory {
		result.NoteHistory, result.SkippedHistory, err = s.importNoteHistory(ctx, uid, vault.ID, extractDir)
		if err != nil {
			return nil, err
		}
	}

	s.logger.Info("Backup import completed",
		zap.Int64("uid", uid),
		zap.String("vault", vault.Name),
		zap.Bool("includeNotes", req.IncludeNotes),
		zap.Bool("includeVaultConfig", req.IncludeVaultConfig),
		zap.Bool("includeNoteHistory", req.IncludeNoteHistory),
		zap.Int64("notes", result.Notes),
		zap.Int64("files", result.Files),
		zap.Int64("settings", result.Settings),
		zap.Int64("noteHistory", result.NoteHistory),
		zap.Int64("skippedHistory", result.SkippedHistory),
	)

	// Trigger debounced sync/backup tasks so external mirrors pick up imported data
	// 触发防抖的同步/备份任务，让外部镜像获取导入的数据
	s.NotifyUpdated(uid)

	return result, nil
}

// readBackupManifest reads the optional backup manifest file
// 读取可选的备份清单文件
func (s *backupService) readBackupManifest(extractDir string) *BackupManifest {
	data, err := os.ReadFile(filepath.Join(extractDir, BackupManifestFileName))
	if err != nil {
		return nil
	}
	var manifest BackupManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil
	}
	return &manifest
}

// classifyArchiveFiles splits extracted files into notes (*.md), attachments and
// vault config (.obsidian/) buckets by their archive-relative path.
// 根据压缩包内相对路径将解压文件分为笔记 (*.md)、附件与仓库配置 (.obsidian/) 三类。
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
		case rel == BackupManifestFileName, rel == BackupNoteHistoryFileName:
			// Archive metadata files are not user data
			// 归档元数据文件不属于用户数据
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
func (s *backupService) importNotesAndAttachments(ctx context.Context, uid, vaultID int64, extractDir string, notePaths, filePaths []string) (int64, int64, error) {
	nowMs := timex.Now().UnixMilli()
	var importedNotes, importedFiles int64

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
func (s *backupService) importVaultSettings(ctx context.Context, uid, vaultID int64, extractDir string, settingPaths []string) (int64, error) {
	nowMs := timex.Now().UnixMilli()
	var count int64

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
	}
	return count, nil
}

// importNoteHistory restores archived note history versions. For every note that
// has entries in the archive, its current history versions are removed first
// (overwrite strategy), then archived entries are re-created with original
// metadata; sidecar content/patch files are written by the repository layer.
//
// 恢复归档的笔记历史版本。对于备份包中存在历史条目的笔记，先删除其当前历史版本
// （覆盖策略），再按原始元数据重建归档条目；内容/补丁 sidecar 文件由仓储层写入。
func (s *backupService) importNoteHistory(ctx context.Context, uid, vaultID int64, extractDir string) (int64, int64, error) {
	data, err := os.ReadFile(filepath.Join(extractDir, BackupNoteHistoryFileName))
	if err != nil {
		if os.IsNotExist(err) {
			// Archive contains no history section (legacy or incremental backup)
			// 备份包不含历史部分（旧格式或增量备份）
			return 0, 0, nil
		}
		return 0, 0, err
	}

	var entries []BackupNoteHistoryEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return 0, 0, code.ErrorBackupImportZipFailed.WithDetails("invalid note history data: " + err.Error())
	}
	if len(entries) == 0 {
		return 0, 0, nil
	}

	notes, err := s.noteRepo.List(ctx, vaultID, 1, 1000000, uid, "", false, "", false, "", "", nil)
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

	// Clear existing history only for notes referenced by the archive
	// 仅清空备份包引用的笔记的现有历史
	affectedNotes := make(map[int64]struct{})
	for _, e := range entries {
		if id, ok := idByPath[e.Path]; ok {
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
	for _, e := range entries {
		if ctx.Err() != nil {
			return imported, skipped, ctx.Err()
		}
		noteID, ok := idByPath[e.Path]
		if !ok {
			skipped++
			continue
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
			continue
		}
		imported++
	}
	return imported, skipped, nil
}

// rebuildResourceFIDs ensures folder records exist for all parent paths in the
// vault and assigns FIDs to notes/attachments (mirrors folder service logic).
// 确保仓库内所有父路径存在文件夹记录并为笔记/附件分配 FID (与文件夹服务逻辑一致)。
func (s *backupService) rebuildResourceFIDs(ctx context.Context, uid, vaultID int64) error {
	notes, err := s.noteRepo.List(ctx, vaultID, 1, 1000000, uid, "", false, "", false, "", "", nil)
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

	files, err := s.fileRepo.List(ctx, vaultID, 1, 1000000, uid, "", false, "", "")
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
