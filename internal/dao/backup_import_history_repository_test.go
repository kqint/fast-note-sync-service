package dao

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/haierkeys/fast-note-sync-service/internal/config"
	"github.com/haierkeys/fast-note-sync-service/internal/domain"
	"github.com/haierkeys/fast-note-sync-service/pkg/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// setupImportHistoryRepoTestEnv builds a *Dao backed by a throwaway SQLite file so the
// backup import history repository can be exercised end to end (including AutoMigrate).
// setupImportHistoryRepoTestEnv 用一次性 SQLite 文件搭建 *Dao，端到端验证导入历史仓储（含 AutoMigrate）。
func setupImportHistoryRepoTestEnv(t *testing.T) (domain.BackupRepository, *Dao, func()) {
	t.Helper()

	tempDir, err := os.MkdirTemp("", "fast-note-sync-service-importhistory-test-*")
	require.NoError(t, err)

	origWd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(tempDir))
	require.NoError(t, os.MkdirAll(filepath.Join("storage", "database"), 0755))

	logger := zap.NewNop()
	dbPath := filepath.Join("storage", "database", "db.sqlite3")
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	require.NoError(t, err)

	dbCfg := &config.DatabaseConfig{
		Type:             "sqlite",
		Path:             dbPath,
		EnableWriteQueue: util.Ptr(false),
	}
	daoInst := New(db, context.Background(),
		WithConfig(dbCfg),
		WithUserDatabaseConfig(dbCfg),
		WithLogger(logger),
	)

	repo := NewBackupRepository(daoInst)
	cleanup := func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
		_ = os.Chdir(origWd)
		_ = os.RemoveAll(tempDir)
	}
	return repo, daoInst, cleanup
}

// seedTestUser inserts a user row so dao.GetAllUserUIDs (and therefore the orphan
// sweep) can discover the tenant database.
// 插入一条用户记录，使 dao.GetAllUserUIDs（进而孤儿清理）能够发现该租户库。
func seedTestUser(t *testing.T, daoInst *Dao, uid int64) {
	t.Helper()
	user, err := NewUserRepository(daoInst).Create(context.Background(), &domain.User{
		UID:      uid,
		Email:    "import-history-test@example.com",
		Username: "import-history-test",
	})
	require.NoError(t, err)
	require.NotNil(t, user)
}

// TestBackupImportHistory_CreateListFinalize covers the full lifecycle of one import:
// submit-time insert (running) → terminal update → paginated listing.
// 覆盖一次导入的完整生命周期：提交落库（running）→ 终态回填 → 分页查询。
func TestBackupImportHistory_CreateListFinalize(t *testing.T) {
	repo, _, cleanup := setupImportHistoryRepoTestEnv(t)
	defer cleanup()

	ctx := context.Background()
	const uid = int64(7)

	start := time.Now().Add(-30 * time.Second).Truncate(time.Second)
	created, err := repo.CreateImportHistory(ctx, &domain.BackupImportHistory{
		UID:                uid,
		TaskID:             "task-abc",
		VaultName:          "vault1",
		FileName:           "backup_full_vault1_20260830.zip",
		FileSize:           4096,
		Status:             domain.BackupImportStatusRunning,
		StartTime:          start,
		IncludeNotes:       true,
		IncludeNoteHistory: true,
	}, uid)
	require.NoError(t, err)
	require.NotNil(t, created)
	require.NotZero(t, created.ID)

	// Newest first, and the row is immediately visible while running.
	// 最新在前，且 running 状态的记录立即可见。
	list, total, err := repo.ListImportHistory(ctx, uid, 1, 10)
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	require.Len(t, list, 1)
	assert.Equal(t, domain.BackupImportStatusRunning, list[0].Status)
	assert.Equal(t, "backup_full_vault1_20260830.zip", list[0].FileName)
	assert.True(t, list[0].IncludeNotes)
	assert.True(t, list[0].IncludeNoteHistory)

	// Finalize the task the way the service does.
	// 按服务的方式回填终态。
	end := start.Add(30 * time.Second)
	created.Status = domain.BackupImportStatusSuccess
	created.EndTime = end
	created.DurationMs = end.Sub(start).Milliseconds()
	created.NotesCount = 12
	created.FilesCount = 3
	created.NoteHistoryCount = 40
	created.SkippedHistoryCount = 2
	require.NoError(t, repo.SaveImportHistory(ctx, created, uid))

	list, total, err = repo.ListImportHistory(ctx, uid, 1, 10)
	require.NoError(t, err)
	require.Equal(t, int64(1), total)
	require.Len(t, list, 1)
	got := list[0]
	assert.Equal(t, domain.BackupImportStatusSuccess, got.Status)
	assert.Equal(t, int64(30000), got.DurationMs)
	assert.Equal(t, int64(12), got.NotesCount)
	assert.Equal(t, int64(3), got.FilesCount)
	assert.Equal(t, int64(40), got.NoteHistoryCount)
	assert.Equal(t, int64(2), got.SkippedHistoryCount)
	assert.False(t, got.CreatedAt.IsZero(), "created_at must be preserved across the finalizing update")

	// History is per-user: another uid must not see this row.
	// 历史按用户隔离：其他 uid 不应看到该记录。
	_, otherTotal, err := repo.ListImportHistory(ctx, uid+1, 1, 10)
	require.NoError(t, err)
	assert.Equal(t, int64(0), otherTotal)
}

// TestBackupImportHistory_StopOrphanImportHistories verifies the startup sweep flips
// every leftover "running" row to "stopped" instead of leaving it in progress forever.
// 验证启动兜底会把残留的 running 记录统一置为 stopped，而不是永远停留在"进行中"。
func TestBackupImportHistory_StopOrphanImportHistories(t *testing.T) {
	repo, daoInst, cleanup := setupImportHistoryRepoTestEnv(t)
	defer cleanup()

	ctx := context.Background()
	const uid = int64(9)
	seedTestUser(t, daoInst, uid)

	for i := 0; i < 2; i++ {
		_, err := repo.CreateImportHistory(ctx, &domain.BackupImportHistory{
			UID:       uid,
			TaskID:    "orphan",
			VaultName: "vault1",
			Status:    domain.BackupImportStatusRunning,
			StartTime: time.Now(),
		}, uid)
		require.NoError(t, err)
	}
	// One already-finished row must be left untouched.
	// 已完成的记录不应被误改。
	done, err := repo.CreateImportHistory(ctx, &domain.BackupImportHistory{
		UID:       uid,
		TaskID:    "done",
		VaultName: "vault1",
		Status:    domain.BackupImportStatusSuccess,
		StartTime: time.Now(),
	}, uid)
	require.NoError(t, err)

	require.NoError(t, repo.StopOrphanImportHistories(ctx))

	list, _, err := repo.ListImportHistory(ctx, uid, 1, 10)
	require.NoError(t, err)
	require.Len(t, list, 3)

	stopped := 0
	for _, h := range list {
		if h.ID == done.ID {
			assert.Equal(t, domain.BackupImportStatusSuccess, h.Status)
			assert.True(t, h.EndTime.IsZero(), "finished rows must keep their original end time")
			continue
		}
		assert.Equal(t, domain.BackupImportStatusStopped, h.Status)
		assert.False(t, h.EndTime.IsZero())
		stopped++
	}
	assert.Equal(t, 2, stopped)
}
