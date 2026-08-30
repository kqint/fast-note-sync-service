package domain

import (
	"context"
	"time"
)

const (
	BackupStatusIdle     = 0
	BackupStatusRunning  = 1
	BackupStatusSuccess  = 2
	BackupStatusFailed   = 3
	BackupStatusStopped  = 4
	BackupStatusNoUpdate = 5
)

// Backup import history statuses. Deliberately separate from the export/sync
// statuses above: imports have no "idle"/"no update" state but add "cancelled".
// 备份导入历史状态。与上面的导出/同步状态分开定义：导入没有 idle / 无更新态，
// 但多了"已取消"这一终态。
const (
	BackupImportStatusRunning   = 0
	BackupImportStatusSuccess   = 1
	BackupImportStatusFailed    = 2
	BackupImportStatusCancelled = 3
	BackupImportStatusStopped   = 4
)

// BackupConfig 备份配置领域模型
type BackupConfig struct {
	ID               int64
	UID              int64
	VaultID          int64     // 关联库 ID (0 表示所有库)
	Type             string    // full, incremental, sync
	StorageIds       string    // JSON 数组，如 "[1, 2]"
	IsEnabled        bool      // 是否启用
	CronStrategy     string    // daily, weekly, monthly, custom
	CronExpression   string    // Cron 表达式
	IncludeVaultName bool      // 同步路径是否包含仓库名前缀
	RetentionDays    int       // 保留天数
	LastRunTime      time.Time // 上次运行时间
	NextRunTime      time.Time // 下次运行时间
	PasswordMode     int       // 密码模式 (0: 无密码, 1: 固定密码, 2: 随机密码)
	PasswordValue    string    // 固定密码值
	LastStatus       int       // 上次状态 (0: Idle, 1: Running, 2: Success, 3: Failed, 4: Stopped, 5: SuccessNoUpdate)
	LastMessage      string    // 上次运行结果消息
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// BackupHistory 备份历史领域模型
type BackupHistory struct {
	ID        int64
	UID       int64
	ConfigID  int64
	StorageID int64
	Type      string // full, incremental, sync
	StartTime time.Time
	EndTime   time.Time
	Status    int // 0: Idle, 1: Running, 2: Success, 3: Failed, 4: Stopped, 5: SuccessNoUpdate
	FileSize  int64
	FileCount int64
	Message   string
	FilePath  string
	Password  string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// BackupImportHistory 备份包导入历史领域模型
// 一条记录对应一次「提交导入」请求，从提交即落库（running），
// 到任务进入终态（success / failed / cancelled / stopped）时回填结果。
type BackupImportHistory struct {
	ID                  int64
	UID                 int64
	TaskID              string    // 内存态导入任务 ID，用于日志与数据库互相定位
	VaultName           string    // 目标仓库名称
	FileName            string    // 上传的原始备份包名
	FileSize            int64     // 备份包字节数
	Status              int       // 0: Running, 1: Success, 2: Failed, 3: Cancelled, 4: Stopped
	StartTime           time.Time // 提交时间
	EndTime             time.Time // 进入终态的时间
	DurationMs          int64     // EndTime - StartTime（毫秒）
	IncludeNotes        bool      // 提交时勾选：导入笔记和附件
	IncludeVaultConfig  bool      // 提交时勾选：导入仓库配置 (.obsidian)
	IncludeNoteHistory  bool      // 提交时勾选：导入笔记历史
	AllowPartial        bool      // 提交时勾选：允许仅导入备份包中实际存在的部分
	NotesCount          int64     // 导入(覆盖)的笔记数
	FilesCount          int64     // 导入(覆盖)的附件数
	SettingsCount       int64     // 导入(覆盖)的仓库配置数
	NoteHistoryCount    int64     // 导入的笔记历史条数
	SkippedHistoryCount int64     // 跳过的笔记历史条数（对应笔记不存在）
	Message             string    // 失败时的错误信息
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

// BackupRepository 备份仓储接口
type BackupRepository interface {
	// ListConfigs 获取用户的备份配置列表
	ListConfigs(ctx context.Context, uid int64) ([]*BackupConfig, error)
	// GetByID 根据ID获取备份配置
	GetByID(ctx context.Context, id, uid int64) (*BackupConfig, error)
	// DeleteConfig 删除备份配置
	DeleteConfig(ctx context.Context, id, uid int64) error
	// SaveConfig 保存备份配置
	SaveConfig(ctx context.Context, config *BackupConfig, uid int64) (*BackupConfig, error)
	// ListEnabledConfigs 获取所有已启用的备份配置
	ListEnabledConfigs(ctx context.Context) ([]*BackupConfig, error)
	// UpdateNextRunTime 更新下次执行时间
	UpdateNextRunTime(ctx context.Context, id, uid int64, nextRun time.Time) error

	// CreateHistory 创建备份历史记录
	CreateHistory(ctx context.Context, history *BackupHistory, uid int64) (*BackupHistory, error)
	// ListHistory 分页获取备份历史记录
	ListHistory(ctx context.Context, uid int64, configID int64, page, pageSize int) ([]*BackupHistory, int64, error)
	// ListOldHistory List old history records created before cutoffTime
	// 获取早于 cutoffTime 的历史记录
	ListOldHistory(ctx context.Context, uid int64, configID int64, cutoffTime time.Time) ([]*BackupHistory, error)
	// DeleteOldHistory Delete old history records created before cutoffTime
	// 删除早于 cutoffTime 的历史记录
	DeleteOldHistory(ctx context.Context, uid int64, configID int64, cutoffTime time.Time) error

	// DisableByVaultID 禁用仓库下的备份任务
	DisableByVaultID(ctx context.Context, vaultID, uid int64) error

	// CreateImportHistory 创建一条导入历史记录（提交即落库，状态为 running）
	CreateImportHistory(ctx context.Context, history *BackupImportHistory, uid int64) (*BackupImportHistory, error)
	// SaveImportHistory 更新一条已存在的导入历史记录（回填终态与统计）
	SaveImportHistory(ctx context.Context, history *BackupImportHistory, uid int64) error
	// ListImportHistory 分页获取导入历史记录
	ListImportHistory(ctx context.Context, uid int64, page, pageSize int) ([]*BackupImportHistory, int64, error)
	// StopOrphanImportHistories 将所有用户的 running 记录置为 stopped（服务重启后收尾）
	StopOrphanImportHistories(ctx context.Context) error
}
