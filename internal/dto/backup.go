package dto

import "github.com/haierkeys/fast-note-sync-service/pkg/timex"

// BackupConfigRequest backup configuration request
// BackupConfigRequest 备份配置请求
type BackupConfigRequest struct {
	ID               int64  `json:"id" form:"id" example:"1"`                                                                              // ID // ID
	Vault            string `json:"vault" form:"vault" example:"test"`                                                                     // Vault name // 仓库名称
	Type             string `json:"type" form:"type" binding:"required,oneof=full incremental sync" example:"sync"`                        // Backup type // 备份类型
	StorageIds       string `json:"storageIds" form:"storageIds" binding:"required" example:"[1, 2]"`                                      // Storage IDs // 存储 ID 列表
	IsEnabled        bool   `json:"isEnabled" form:"isEnabled" example:"true"`                                                             // Is enabled // 是否启用
	CronStrategy     string `json:"cronStrategy" form:"cronStrategy" binding:"required,oneof=daily weekly monthly custom" example:"daily"` // Cron strategy // 定时策略
	CronExpression   string `json:"cronExpression" form:"cronExpression" example:"0 0 * * *"`                                              // Cron expression // Cron 表达式
	RetentionDays    int    `json:"retentionDays" form:"retentionDays" binding:"min=-1" example:"7"`                                       // Retention days // 保留天数
	IncludeVaultName bool   `json:"includeVaultName" form:"includeVaultName" example:"false"`                                              // Include vault name // 同步路径是否包含仓库名
	PasswordMode     int    `json:"passwordMode" form:"passwordMode" example:"0"`                                                          // Password mode (0:None, 1:Fixed, 2:Random) // 密码模式 (0:无密码, 1:固定密码, 2:随机密码)
	PasswordValue    string `json:"passwordValue" form:"passwordValue" example:"123456"`                                                   // Password value for fixed mode // 固定密码值
}

// BackupExecuteRequest backup execution request
// BackupExecuteRequest 备份执行请求
type BackupExecuteRequest struct {
	ID int64 `json:"id" form:"id" example:"1"` // ID // ID
}

// BackupHistoryListRequest backup history list request
// BackupHistoryListRequest 备份历史列表请求
type BackupHistoryListRequest struct {
	ConfigID int64 `json:"configId" form:"configId" binding:"required" example:"1"` // Config ID // 配置 ID
	Page     int   `json:"page" form:"page" example:"1"`                            // Page number // 页码
	PageSize int   `json:"pageSize" form:"pageSize" example:"10"`                   // Page size // 每页大小
}

// BackupImportRequest 备份包导入请求 (multipart/form-data)
// BackupImportRequest backup archive import request (multipart/form-data)
type BackupImportRequest struct {
	Vault              string `json:"vault" form:"vault" binding:"required" example:"test"`  // Target vault name // 目标仓库名称
	Password           string `json:"password" form:"password" example:"123456"`             // Archive password (optional) // 压缩包密码 (可选)
	IncludeNotes       bool   `json:"includeNotes" form:"includeNotes" example:"true"`       // Import notes & attachments // 导入笔记和附件
	IncludeVaultConfig bool   `json:"includeVaultConfig" form:"includeVaultConfig" example:"false"` // Import vault config (.obsidian) // 导入仓库配置 (.obsidian)
	IncludeNoteHistory bool   `json:"includeNoteHistory" form:"includeNoteHistory" example:"false"` // Import note history (requires IncludeNotes) // 导入笔记历史 (需勾选笔记和附件)
	AllowPartial       bool   `json:"allowPartial" form:"allowPartial" example:"false"`      // Allow importing only the sections actually present // 允许仅导入备份包中实际存在的部分
	// FileName carries the original upload name for the history record. It is
	// filled by the handler from the multipart header, never bound from the form.
	// FileName 记录上传时的原始文件名，仅供导入历史使用；由处理层从 multipart 头填入，不从表单绑定。
	FileName string `json:"fileName" form:"-"`
}

// BackupImportAvailabilityDTO 备份包中各部分的存在情况
// BackupImportAvailabilityDTO availability of each section inside the archive
type BackupImportAvailabilityDTO struct {
	Notes       bool `json:"notes"`       // Notes & attachments present // 存在笔记和附件
	VaultConfig bool `json:"vaultConfig"` // Vault config present // 存在仓库配置
	NoteHistory bool `json:"noteHistory"` // Note history present // 存在笔记历史
}

// BackupImportValidateDTO 备份包校验结果（所选内容缺失时返回）
// BackupImportValidateDTO archive validation result (returned when requested sections are missing)
type BackupImportValidateDTO struct {
	Available BackupImportAvailabilityDTO `json:"available"` // Sections present in the archive // 备份包中存在的部分
	Missing   []string                    `json:"missing"`   // Requested but missing sections: notes | vaultConfig | noteHistory // 已勾选但缺失的部分
}

// BackupImportResultDTO 备份包导入结果
// BackupImportResultDTO backup import result
type BackupImportResultDTO struct {
	Notes          int64 `json:"notes"`                    // Imported/updated notes count // 导入(覆盖)的笔记数
	Files          int64 `json:"files"`                    // Imported/updated attachments count // 导入(覆盖)的附件数
	Settings       int64 `json:"settings"`                 // Imported/updated vault config count // 导入(覆盖)的仓库配置数
	NoteHistory    int64 `json:"noteHistory"`              // Imported note history entries count // 导入的笔记历史条数
	SkippedHistory int64 `json:"skippedHistory,omitempty"` // History entries skipped (note missing) // 跳过的历史条数 (对应笔记不存在)
	Vault          string `json:"vault"`                   // Target vault name // 目标仓库名称
}

// BackupImportTaskDTO 导入任务提交结果
// BackupImportTaskDTO backup import task submission result
type BackupImportTaskDTO struct {
	TaskID string `json:"taskId"` // Task ID // 任务 ID
	Phase  string `json:"phase"`  // Task phase: validating | await_partial | importing | success | failed | cancelled // 任务阶段
}

// BackupImportTotalsDTO 导入任务扫描总数
// BackupImportTotalsDTO backup import task scanned totals
type BackupImportTotalsDTO struct {
	Notes       int64 `json:"notes"`       // Note file count // 笔记文件数
	Files       int64 `json:"files"`       // Attachment count // 附件数
	Settings    int64 `json:"settings"`    // Vault config count // 仓库配置数
	NoteHistory int64 `json:"noteHistory"` // Note history entries // 笔记历史条数
}

// BackupImportProgressDTO 导入任务处理进度
// BackupImportProgressDTO backup import task processing progress
type BackupImportProgressDTO struct {
	Notes          int64 `json:"notes"`          // Imported notes // 已导入笔记
	Files          int64 `json:"files"`          // Imported attachments // 已导入附件
	Settings       int64 `json:"settings"`       // Imported vault config // 已导入仓库配置
	NoteHistory    int64 `json:"noteHistory"`    // Imported note history // 已导入笔记历史
	SkippedHistory int64 `json:"skippedHistory"` // Skipped history entries // 跳过的历史条数
}

// BackupImportTaskErrorDTO 导入任务错误信息
// BackupImportTaskErrorDTO backup import task error info
type BackupImportTaskErrorDTO struct {
	Code    int    `json:"code"`    // Error code // 错误码
	Message string `json:"message"` // Error message // 错误消息
}

// BackupImportTaskStatusDTO 导入任务状态快照
// BackupImportTaskStatusDTO backup import task status snapshot
type BackupImportTaskStatusDTO struct {
	TaskID    string                      `json:"taskId"`    // Task ID // 任务 ID
	Phase     string                      `json:"phase"`     // Task phase // 任务阶段
	Totals    BackupImportTotalsDTO       `json:"totals"`    // Scanned totals (after scan) // 扫描总数（扫描完成后填充）
	Progress  BackupImportProgressDTO     `json:"progress"`  // Processing progress // 处理进度
	Available *BackupImportAvailabilityDTO `json:"available,omitempty"` // Available sections (await_partial) // 备份包中存在的部分
	Missing   []string                    `json:"missing,omitempty"`   // Requested but missing sections // 已勾选但缺失的部分
	Result    *BackupImportResultDTO      `json:"result,omitempty"`    // Final result (success) // 最终结果（成功时）
	Error     *BackupImportTaskErrorDTO   `json:"error,omitempty"`     // Error (failed) // 错误信息（失败时）
}

// BackupImportConfirmRequest 导入任务确认请求
// BackupImportConfirmRequest backup import task confirm request
type BackupImportConfirmRequest struct {
	TaskID       string `json:"taskId" binding:"required" example:"6f0c..."` // Task ID // 任务 ID
	AllowPartial bool   `json:"allowPartial" example:"true"`                 // true: import available sections only; false: cancel // true: 仅导入已有部分; false: 放弃本次导入
}

// BackupImportTaskQuery 导入任务状态查询
// BackupImportTaskQuery backup import task status query
type BackupImportTaskQuery struct {
	TaskID string `json:"taskId" form:"taskId" binding:"required" example:"6f0c..."` // Task ID // 任务 ID
}

// BackupImportHistoryListRequest 导入历史列表请求
// BackupImportHistoryListRequest backup import history list request
type BackupImportHistoryListRequest struct {
	Page     int `json:"page" form:"page" example:"1"`         // Page number // 页码
	PageSize int `json:"pageSize" form:"pageSize" example:"10"` // Page size // 每页大小
}

// BackupImportHistoryDTO 导入历史记录
// BackupImportHistoryDTO backup import history record
type BackupImportHistoryDTO struct {
	ID                  int64      `json:"id"`                  // Record ID // 记录 ID
	UID                 int64      `json:"uid"`                 // User UID // 用户 ID
	TaskID              string     `json:"taskId"`              // Import task ID // 导入任务 ID
	VaultName           string     `json:"vaultName"`           // Target vault name // 目标仓库名称
	FileName            string     `json:"fileName"`            // Original archive name // 上传时原始文件名
	FileSize            int64      `json:"fileSize"`            // Archive size in bytes // 备份包字节数
	Status              int        `json:"status"`              // 0: Running, 1: Success, 2: Failed, 3: Cancelled, 4: Stopped // 状态
	StartTime           timex.Time `json:"startTime"`           // Submit time // 提交时间
	EndTime             timex.Time `json:"endTime"`             // Terminal state time // 进入终态的时间
	DurationMs          int64      `json:"durationMs"`          // EndTime - StartTime in ms // 耗时（毫秒）
	IncludeNotes        bool       `json:"includeNotes"`        // Requested: notes & attachments // 勾选：笔记和附件
	IncludeVaultConfig  bool       `json:"includeVaultConfig"`  // Requested: vault config // 勾选：仓库配置
	IncludeNoteHistory  bool       `json:"includeNoteHistory"`  // Requested: note history // 勾选：笔记历史
	AllowPartial        bool       `json:"allowPartial"`        // Requested: allow partial import // 勾选：允许部分导入
	NotesCount          int64      `json:"notesCount"`          // Imported notes // 导入笔记数
	FilesCount          int64      `json:"filesCount"`          // Imported attachments // 导入附件数
	SettingsCount       int64      `json:"settingsCount"`       // Imported vault config entries // 导入仓库配置数
	NoteHistoryCount    int64      `json:"noteHistoryCount"`    // Imported note history entries // 导入笔记历史条数
	SkippedHistoryCount int64      `json:"skippedHistoryCount"` // Skipped history entries // 跳过的历史条数
	Message             string     `json:"message"`             // Error message (failed) // 错误信息（失败时）
	CreatedAt           timex.Time `json:"createdAt"`           // Created at // 创建时间
	UpdatedAt           timex.Time `json:"updatedAt"`           // Updated at // 更新时间
}

// BackupConfigDTO backup configuration DTO
// BackupConfigDTO 备份配置 DTO
type BackupConfigDTO struct {
	ID               int64      `json:"id"`               // Config ID // 配置ID
	UID              int64      `json:"uid"`              // User UID // 用户ID
	Vault            string     `json:"vault"`            // Associated vault name // 关联库名称
	Type             string     `json:"type"`             // Backup type (full, incremental, sync) // 备份类型 (full, incremental, sync)
	StorageIds       string     `json:"storageIds"`       // Storage ID list // 存储ID列表
	IsEnabled        bool       `json:"isEnabled"`        // Is enabled // 是否启用
	CronStrategy     string     `json:"cronStrategy"`     // Cron strategy // 定时策略
	CronExpression   string     `json:"cronExpression"`   // Cron expression // Cron表达式
	RetentionDays    int        `json:"retentionDays"`    // Retention days // 保留天数
	IncludeVaultName bool       `json:"includeVaultName"` // Whether sync path includes vault name // 同步路径是否包含仓库名
	PasswordMode     int        `json:"passwordMode"`     // Password mode (0:None, 1:Fixed, 2:Random) // 密码模式 (0:无密码, 1:固定密码, 2:随机密码)
	PasswordValue    string     `json:"passwordValue"`    // Password value for fixed mode // 固定密码值
	LastRunTime      timex.Time `json:"lastRunTime"`      // Last run time // 上次运行时间
	NextRunTime      timex.Time `json:"nextRunTime"`      // Next run time // 下次运行时间
	LastStatus       int        `json:"lastStatus"`       // Last status (0:Idle, 1:Running, 2:Success, 3:Failed, 4:Stopped) // 上次状态 (0:Idle, 1:Running, 2:Success, 3:Failed, 4:Stopped)
	LastMessage      string     `json:"lastMessage"`      // Last run result message // 上次运行结果消息
	CreatedAt        timex.Time `json:"createdAt"`        // Created at // 创建时间
	UpdatedAt        timex.Time `json:"updatedAt"`        // Updated at // 更新时间
}

// BackupHistoryDTO backup history DTO
// BackupHistoryDTO 备份历史 DTO
type BackupHistoryDTO struct {
	ID        int64      `json:"id"`        // History record ID // 历史记录ID
	UID       int64      `json:"uid"`       // User UID // 用户ID
	ConfigID  int64      `json:"configId"`  // Config ID // 配置ID
	StorageID int64      `json:"storageId"` // Storage ID // 存储ID
	Type      string     `json:"type"`      // Backup type // 备份类型
	StartTime timex.Time `json:"startTime"` // Start time // 开始时间
	EndTime   timex.Time `json:"endTime"`   // End time // 结束时间
	Status    int        `json:"status"`    // Status (0:Idle, 1:Running, 2:Success, 3:Failed, 4:Stopped) // 状态 (0:Idle, 1:Running, 2:Success, 3:Failed, 4:Stopped)
	FileSize  int64      `json:"fileSize"`  // File size // 文件大小
	FileCount int64      `json:"fileCount"` // File count // 文件数量
	Message   string     `json:"message"`   // Result message // 结果消息
	FilePath  string     `json:"filePath"`  // File path // 文件路径
	Password  string     `json:"password"`  // Password // 密码
	CreatedAt timex.Time `json:"createdAt"` // Created at // 创建时间
	UpdatedAt timex.Time `json:"updatedAt"` // Updated at // 更新时间
}
