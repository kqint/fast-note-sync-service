package service

import "time"

// Backup archive layout constants. The manifest and note-history data files live
// at the zip root; notes/attachments keep their vault-relative paths and vault
// config (.obsidian) files are exported under their original ".obsidian/" paths.
// 备份压缩包布局常量。清单与笔记历史数据文件位于压缩包根目录；
// 笔记/附件保持仓库相对路径，仓库配置 (.obsidian) 按原始 ".obsidian/" 路径导出。
const (
	// BackupManifestVersion is the current backup manifest schema version.
	// BackupManifestVersion 当前备份清单格式版本。
	BackupManifestVersion = 1

	// BackupManifestFileName is the manifest file name at the zip root.
	// BackupManifestFileName 压缩包根目录的清单文件名。
	BackupManifestFileName = "fns-backup.manifest.json"

	// BackupNoteHistoryFileName is the note history data file name at the zip root.
	// BackupNoteHistoryFileName 压缩包根目录的笔记历史数据文件名。
	BackupNoteHistoryFileName = "_fns_note_history.json"

	// VaultConfigDirPrefix marks vault config files inside the archive.
	// VaultConfigDirPrefix 标识压缩包内的仓库配置文件。
	VaultConfigDirPrefix = ".obsidian/"
)

// BackupManifest describes the content of a backup archive.
// BackupManifest 描述备份压缩包的内容。
type BackupManifest struct {
	Version       int           `json:"version"`                 // Manifest schema version // 清单格式版本
	Type          string        `json:"type"`                    // full | incremental // 全量 | 增量
	Vault         string        `json:"vault"`                   // Source vault name // 来源仓库名称
	CreatedAt     time.Time     `json:"createdAt"`               // Archive creation time // 打包时间
	Includes      BackupInclude `json:"includes"`                // Sections present in the archive // 压缩包包含的部分
	SettingsCount int64         `json:"settingsCount,omitempty"` // Vault config file count // 仓库配置文件数量
	HistoryCount  int64         `json:"historyCount,omitempty"`  // Note history entry count // 笔记历史条目数
}

// BackupInclude flags for the sections contained in a backup archive.
// BackupInclude 备份压缩包所含部分的标记。
type BackupInclude struct {
	Notes       bool `json:"notes"`                 // Notes included // 包含笔记
	Attachments bool `json:"attachments"`           // Attachments included // 包含附件
	VaultConfig bool `json:"vaultConfig"`           // .obsidian config included // 包含仓库配置
	NoteHistory bool `json:"noteHistory,omitempty"` // Note history included // 包含笔记历史
}

// BackupNoteHistoryEntry is one note history version serialized into the archive.
// BackupNoteHistoryEntry 序列化进备份包的单条笔记历史版本。
type BackupNoteHistoryEntry struct {
	Path          string    `json:"path"`                    // Owning note path (normalized with .md) // 所属笔记路径 (规范化为 .md)
	Version       int64     `json:"version"`                 // History version // 历史版本号
	ContentHash   string    `json:"contentHash,omitempty"`   // Content hash // 内容哈希
	DiffPatch     string    `json:"diffPatch,omitempty"`     // Diff patch against base snapshot // 相对基础快照的差异补丁
	Content       string    `json:"content"`                 // Full snapshot content // 完整快照内容
	ClientName    string    `json:"clientName,omitempty"`    // Client name // 客户端名称
	ClientType    string    `json:"clientType,omitempty"`    // Client type // 客户端类型
	ClientVersion string    `json:"clientVersion,omitempty"` // Client version // 客户端版本
	CreatedAt     time.Time `json:"createdAt"`               // Record creation time // 记录创建时间
}

// backupTypeOf maps the incremental flag to its backup type label
// backupTypeOf 将增量标记映射为备份类型标识
func backupTypeOf(incremental bool) string {
	if incremental {
		return "incremental"
	}
	return "full"
}
