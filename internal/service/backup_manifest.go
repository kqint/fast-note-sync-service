package service

import "time"

// Backup archive layout constants. The note history data file lives at
// ".fns/note_history.json" inside the zip; notes/attachments keep their
// vault-relative paths and vault config (.obsidian) files are exported under
// their original ".obsidian/" paths. The ".fns/" namespace is reserved for
// archive metadata and is skipped during import.
// 备份压缩包布局常量。笔记历史数据文件位于压缩包内 ".fns/note_history.json"；
// 笔记/附件保持仓库相对路径，仓库配置 (.obsidian) 按原始 ".obsidian/" 路径导出。
// ".fns/" 命名空间为归档元数据保留，导入时会被跳过。
const (
	// BackupMetaDirPrefix marks the reserved metadata directory inside the archive.
	// BackupMetaDirPrefix 标识压缩包内保留的元数据目录。
	BackupMetaDirPrefix = ".fns/"

	// BackupNoteHistoryFileName is the note history data file path at the zip root metadata dir.
	// BackupNoteHistoryFileName 压缩包内笔记历史数据文件路径。
	BackupNoteHistoryFileName = BackupMetaDirPrefix + "note_history.json"

	// VaultConfigDirPrefix marks vault config files inside the archive.
	// VaultConfigDirPrefix 标识压缩包内的仓库配置文件。
	VaultConfigDirPrefix = ".obsidian/"
)

// BackupNoteHistoryEntry is one note history version serialized into the archive.
// BackupNoteHistoryEntry 序列化进备份包的单条笔记历史版本。
type BackupNoteHistoryEntry struct {
	Path          string    `json:"path"`                   // Owning note path (normalized with .md) // 所属笔记路径 (规范化为 .md)
	Version       int64     `json:"version"`                // History version // 历史版本号
	ContentHash   string    `json:"contentHash,omitempty"`  // Content hash // 内容哈希
	DiffPatch     string    `json:"diffPatch,omitempty"`    // Diff patch against base snapshot // 相对基础快照的差异补丁
	Content       string    `json:"content"`                // Full snapshot content // 完整快照内容
	ClientName    string    `json:"clientName,omitempty"`   // Client name // 客户端名称
	ClientType    string    `json:"clientType,omitempty"`   // Client type // 客户端类型
	ClientVersion string    `json:"clientVersion,omitempty"` // Client version // 客户端版本
	CreatedAt     time.Time `json:"createdAt"`              // Record creation time // 记录创建时间
}
