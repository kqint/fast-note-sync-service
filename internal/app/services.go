package app

import (
	"github.com/haierkeys/fast-note-sync-service/internal/service"
	"go.uber.org/zap"
)

// Services encapsulates all business service instances
type Services struct {
	VaultService       service.VaultService
	NoteService        service.NoteService
	UserService        service.UserService
	TokenService       service.TokenService
	FileService        service.FileService
	SettingService     service.SettingService
	NoteHistoryService service.NoteHistoryService
	ConflictService    service.ConflictService
	ShareService       service.ShareService
	NoteLinkService    service.NoteLinkService
	FolderService      service.FolderService
	StorageService     service.StorageService
	BackupService      service.BackupService
	GitSyncService     service.GitSyncService
	CloudflareService  service.CloudflareService
	SyncLogService     service.SyncLogService
	OIDCService        service.OIDCService
}

// initServices initializes all services
func initServices(cfg *AppConfig, infra *Infra, repos *Repositories, logger *zap.Logger) *Services {
	svcConfig := &service.ServiceConfig{
		User: service.UserServiceConfig{
			RegisterIsEnable: cfg.User.RegisterIsEnable,
			AdminUID:         cfg.User.AdminUID,
		},
		Token: service.TokenServiceConfig{
			WebGUILoginTokenExpiry: cfg.Security.WebGUILoginTokenExpiry,
			WebGUILoginTokenBindIP: *cfg.Security.WebGUILoginTokenBindIP,
		},
		App: service.AppServiceConfig{
			SoftDeleteRetentionTime: cfg.App.SoftDeleteRetentionTime,
			HistoryKeepVersions:     cfg.App.HistoryKeepVersions,
			HistorySaveDelay:        cfg.App.HistorySaveDelay,
			ShareTokenExpiry:        cfg.Security.ShareTokenExpiry,
			ShortLink: service.ShortLinkServiceConfig{
				BaseURL:  cfg.ShortLink.BaseURL,
				APIKey:   cfg.ShortLink.APIKey,
				Password: cfg.ShortLink.Password,
				Cloaking: cfg.ShortLink.Cloaking,
			},
		},
	}

	s := &Services{}
	s.VaultService = service.NewVaultService(
		repos.VaultRepo,
		repos.NoteRepo,
		repos.FileRepo,
		repos.FolderRepo,
		repos.SyncLogRepo,
		repos.NoteHistoryRepo,
		repos.NoteLinkRepo,
		repos.SettingRepo,
		repos.NoteFTSRepo,
		repos.ShareRepo,
		repos.GitSyncRepo,
		repos.BackupRepo,
		logger,
	)
	s.StorageService = service.NewStorageService(repos.StorageRepo, &cfg.Storage)
	// Pass a live handle to AppSettings (not a snapshot): the backup import size limit is
	// resolved per submission via appSettings.BackupImportSizeLimitResolved(), so changes
	// made in system settings take effect immediately without restart.
	// 传入 AppSettings 实时句柄（非构造期快照）：备份导入大小限制在每次提交时经
	// appSettings.BackupImportSizeLimitResolved() 解析，系统设置修改后立即生效、无需重启。
	s.BackupService = service.NewBackupService(repos.BackupRepo, repos.NoteRepo, repos.FolderRepo, repos.FileRepo, repos.VaultRepo, repos.SettingRepo, repos.NoteHistoryRepo, s.StorageService, &cfg.Storage, cfg.App.TempPath, logger, &cfg.App)
	s.GitSyncService = service.NewGitSyncService(repos.GitSyncRepo, repos.NoteRepo, repos.FolderRepo, repos.FileRepo, repos.VaultRepo, repos.SettingRepo, &cfg.Git, logger)

	// Initialize SyncLogService first, as NoteService/FileService/SettingService depend on it
	// SyncLogService 必须最先初始化，因为其他服务依赖它
	s.SyncLogService = service.NewSyncLogService(repos.SyncLogRepo, logger)

	s.FolderService = service.NewFolderService(repos.FolderRepo, repos.NoteRepo, repos.FileRepo, s.VaultService, s.BackupService, s.GitSyncService, s.SyncLogService, infra.workerPool)
	s.NoteService = service.NewNoteService(repos.UserRepo, repos.NoteRepo, repos.NoteLinkRepo, repos.FileRepo, repos.ShareRepo, repos.NoteHistoryRepo, s.VaultService, s.FolderService, s.BackupService, s.GitSyncService, s.SyncLogService, svcConfig)
	s.TokenService = service.NewTokenService(repos.AuthTokenRepo, repos.AuthTokenLogRepo, infra.TokenManager, logger, svcConfig.Token)
	s.UserService = service.NewUserService(repos.UserRepo, infra.TokenManager, s.TokenService, logger, svcConfig)
	s.OIDCService = service.NewOIDCService(repos.UserRepo, repos.OIDCIdentityRepo, s.TokenService)
	s.FileService = service.NewFileService(repos.UserRepo, repos.FileRepo, repos.NoteRepo, s.VaultService, s.FolderService, s.BackupService, s.GitSyncService, s.SyncLogService, svcConfig)
	s.SettingService = service.NewSettingService(repos.SettingRepo, s.VaultService, s.SyncLogService, svcConfig)
	s.NoteHistoryService = service.NewNoteHistoryService(repos.NoteHistoryRepo, repos.NoteRepo, repos.UserRepo, s.VaultService, s.FolderService, s.NoteService, s.BackupService, s.GitSyncService, logger, &svcConfig.App)
	s.ConflictService = service.NewConflictService(repos.NoteRepo, s.VaultService, logger)
	s.ShareService = service.NewShareService(repos.ShareRepo, infra.TokenManager, repos.NoteRepo, repos.FileRepo, repos.VaultRepo, logger, svcConfig)
	s.NoteLinkService = service.NewNoteLinkService(repos.NoteLinkRepo, repos.NoteRepo, s.VaultService)
	s.CloudflareService = service.NewCloudflareService(logger)

	return s
}
