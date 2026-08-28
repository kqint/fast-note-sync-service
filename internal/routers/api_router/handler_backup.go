package api_router

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/gin-gonic/gin"
	"github.com/haierkeys/fast-note-sync-service/internal/app"
	"github.com/haierkeys/fast-note-sync-service/internal/dto"
	"github.com/haierkeys/fast-note-sync-service/internal/middleware"
	pkgapp "github.com/haierkeys/fast-note-sync-service/pkg/app"
	"github.com/haierkeys/fast-note-sync-service/pkg/code"
	apperrors "github.com/haierkeys/fast-note-sync-service/pkg/errors"
	"github.com/haierkeys/fast-note-sync-service/pkg/util"
	"go.uber.org/zap"
)

// BackupHandler backup API router handler
type BackupHandler struct {
	*Handler
}

// NewBackupHandler creates BackupHandler instance
func NewBackupHandler(a *app.App) *BackupHandler {
	return &BackupHandler{
		Handler: NewHandler(a),
	}
}

// GetConfigs gets backup configurations
// @Summary Get backup configurations
// @Tags Backup
// @Security UserAuthToken
// @Produce json
// @Success 200 {object} pkgapp.Res{data=[]dto.BackupConfigDTO} "Success"
// @Failure 401 {object} pkgapp.Res "Token Required"
// @Failure 500 {object} pkgapp.Res "Internal Server Error"
// @Router /api/backup/configs [get]
func (h *BackupHandler) GetConfigs(c *gin.Context) {
	response := pkgapp.NewResponse(c)

	uid := pkgapp.GetUID(c)
	if uid == 0 {
		response.ToResponse(code.ErrorNotUserAuthToken)
		return
	}

	configs, err := h.App.BackupService.GetConfigs(c.Request.Context(), uid)
	if err != nil {
		h.logError(c.Request.Context(), "BackupHandler.GetConfigs", err)
		apperrors.ErrorResponse(c, err)
		return
	}

	response.ToResponse(code.Success.WithData(configs))
}

// UpdateConfig updates backup configuration
// @Summary Update backup configuration
// @Tags Backup
// @Security UserAuthToken
// @Accept json
// @Produce json
// @Param params body dto.BackupConfigRequest true "Backup Parameters"
// @Success 200 {object} pkgapp.Res{data=dto.BackupConfigDTO} "Success"
// @Failure 400 {object} pkgapp.Res "Invalid Params"
// @Failure 401 {object} pkgapp.Res "Token Required"
// @Failure 500 {object} pkgapp.Res "Internal Server Error"
// @Router /api/backup/config [post]
func (h *BackupHandler) UpdateConfig(c *gin.Context) {
	response := pkgapp.NewResponse(c)
	params := &dto.BackupConfigRequest{}

	if valid, errs := pkgapp.BindAndValid(c, params); !valid {
		response.ToResponse(code.ErrorInvalidParams.WithDetails(errs.ErrorsToString()).WithData(errs.MapsToString()))
		return
	}

	uid := pkgapp.GetUID(c)
	if uid == 0 {
		response.ToResponse(code.ErrorNotUserAuthToken)
		return
	}

	config, err := h.App.BackupService.UpdateConfig(c.Request.Context(), uid, params)
	if err != nil {
		h.logError(c.Request.Context(), "BackupHandler.UpdateConfig", err)
		apperrors.ErrorResponse(c, err)
		return
	}

	response.ToResponse(code.SuccessUpdate.WithData(config))
}

// DeleteConfig deletes backup configuration
// @Summary Delete backup configuration
// @Tags Backup
// @Security UserAuthToken
// @Produce json
// @Param params query dto.BackupExecuteRequest true "Config ID"
// @Success 200 {object} pkgapp.Res "Success"
// @Failure 400 {object} pkgapp.Res "Invalid Params"
// @Failure 401 {object} pkgapp.Res "Token Required"
// @Failure 500 {object} pkgapp.Res "Internal Server Error"
// @Router /api/backup/config [delete]
func (h *BackupHandler) DeleteConfig(c *gin.Context) {

	response := pkgapp.NewResponse(c)
	params := &dto.BackupExecuteRequest{} // Reusing ID struct
	if valid, errs := pkgapp.BindAndValid(c, params); !valid {
		response.ToResponse(code.ErrorInvalidParams.WithDetails(errs.ErrorsToString()).WithData(errs.MapsToString()))
		return
	}

	uid := pkgapp.GetUID(c)
	if uid == 0 {
		response.ToResponse(code.ErrorNotUserAuthToken)
		return
	}

	err := h.App.BackupService.DeleteConfig(c.Request.Context(), uid, params.ID)
	if err != nil {
		h.logError(c.Request.Context(), "BackupHandler.DeleteConfig", err)
		apperrors.ErrorResponse(c, err)
		return
	}

	response.ToResponse(code.Success)
}

// ListHistory gets backup history list
// @Summary Get backup history list
// @Tags Backup
// @Security UserAuthToken
// @Produce json
// @Param params query dto.BackupHistoryListRequest true "Backup History List Parameters"
// @Success 200 {object} pkgapp.Res{data=pkgapp.ListRes{list=[]dto.BackupHistoryDTO}} "Success"
// @Failure 400 {object} pkgapp.Res "Invalid Params"
// @Failure 401 {object} pkgapp.Res "Token Required"
// @Failure 500 {object} pkgapp.Res "Internal Server Error"
// @Router /api/backup/historys [get]
func (h *BackupHandler) ListHistory(c *gin.Context) {
	response := pkgapp.NewResponse(c)
	params := &dto.BackupHistoryListRequest{}
	pager := pkgapp.NewPager(c)

	if valid, errs := pkgapp.BindAndValid(c, params); !valid {
		response.ToResponse(code.ErrorInvalidParams.WithDetails(errs.ErrorsToString()).WithData(errs.MapsToString()))
		return
	}

	// Override pager with params if provided (though NewPager usually handles page/pageSize from query)
	// But BindAndValid parses them into params.
	// Let's sync them or just rely on params.
	// Actually pkgapp.NewPager extracts page and page_size from context query params.
	// Since we bind them to struct as well, we can just ensure consistency.
	// The service uses pager.Page and pager.PageSize.
	// Let's just use pager as is, because BindAndValid also reads from query.

	uid := pkgapp.GetUID(c)
	if uid == 0 {
		response.ToResponse(code.ErrorNotUserAuthToken)
		return
	}

	list, total, err := h.App.BackupService.ListHistory(c.Request.Context(), uid, params.ConfigID, pager)
	if err != nil {
		h.logError(c.Request.Context(), "BackupHandler.ListHistory", err)
		apperrors.ErrorResponse(c, err)
		return
	}

	response.ToResponseList(code.Success, list, int(total))
}

// Execute triggers a backup manually
// @Summary Trigger a backup manually
// @Tags Backup
// @Security UserAuthToken
// @Produce json
// @Param params body dto.BackupExecuteRequest true "Backup Execute Parameters"
// @Success 200 {object} pkgapp.Res "Success"
// @Failure 400 {object} pkgapp.Res "Invalid Params"
// @Failure 401 {object} pkgapp.Res "Token Required"
// @Failure 500 {object} pkgapp.Res "Internal Server Error"
// @Router /api/backup/execute [post]
func (h *BackupHandler) Execute(c *gin.Context) {

	response := pkgapp.NewResponse(c)
	params := &dto.BackupExecuteRequest{}
	if valid, errs := pkgapp.BindAndValid(c, params); !valid {
		response.ToResponse(code.ErrorInvalidParams.WithDetails(errs.ErrorsToString()).WithData(errs.MapsToString()))
		return
	}

	uid := pkgapp.GetUID(c)
	if uid == 0 {
		response.ToResponse(code.ErrorNotUserAuthToken)
		return
	}

	err := h.App.BackupService.ExecuteUserBackup(c.Request.Context(), uid, params.ID)
	if err != nil {
		h.logError(c.Request.Context(), "BackupHandler.Execute", err)
		apperrors.ErrorResponse(c, err)
		return
	}

	response.ToResponse(code.Success.WithDetails("Backup task completed, check history for details"))
}

// Import validates the upload quickly and submits an asynchronous backup import task
// @Summary Submit a backup import task
// @Description Quick-checks the upload (size limit, ZIP magic), then runs the import in the background. Poll GET /api/backup/import/status with the returned taskId
// @Tags Backup
// @Security UserAuthToken
// @Accept multipart/form-data
// @Produce json
// @Param file formData file true "Backup archive (.zip)"
// @Param vault formData string true "Target vault name"
// @Param password formData string false "Archive password"
// @Param includeNotes formData boolean false "Import notes & attachments"
// @Param includeVaultConfig formData boolean false "Import vault config (.obsidian)"
// @Param includeNoteHistory formData boolean false "Import note history (requires includeNotes)"
// @Success 200 {object} pkgapp.Res{data=dto.BackupImportTaskDTO} "Success"
// @Failure 400 {object} pkgapp.Res "Invalid Params"
// @Failure 401 {object} pkgapp.Res "Token Required"
// @Failure 500 {object} pkgapp.Res "Internal Server Error"
// @Router /api/backup/import [post]
func (h *BackupHandler) Import(c *gin.Context) {
	response := pkgapp.NewResponse(c)

	uid := pkgapp.GetUID(c)
	if uid == 0 {
		response.ToResponse(code.ErrorNotUserAuthToken)
		return
	}

	params := &dto.BackupImportRequest{
		Vault:              c.PostForm("vault"),
		Password:           c.PostForm("password"),
		IncludeNotes:       c.PostForm("includeNotes") == "true",
		IncludeVaultConfig: c.PostForm("includeVaultConfig") == "true",
		IncludeNoteHistory: c.PostForm("includeNoteHistory") == "true",
		AllowPartial:       c.PostForm("allowPartial") == "true",
	}
	if params.Vault == "" {
		response.ToResponse(code.ErrorBackupVaultRequired)
		return
	}

	fileHeader, err := c.FormFile("file")
	if err != nil {
		response.ToResponse(code.ErrorBackupImportFileEmpty.WithDetails(err.Error()))
		return
	}

	// Size limit checks, layer 1 & 2: reject before streaming the whole body.
	// 大小限制·第 1、2 层：在流式转储前即拒绝。
	limitEnabled, limitBytes := h.App.Config().GetBackupImportSizeLimit()
	if limitEnabled {
		if c.Request.ContentLength > limitBytes+1<<20 {
			response.ToResponse(code.ErrorBackupImportFileTooLarge.
				WithDetails(fmt.Sprintf("content-length %d exceeds limit %d", c.Request.ContentLength, limitBytes)))
			return
		}
		if fileHeader.Size > limitBytes {
			response.ToResponse(code.ErrorBackupImportFileTooLarge.
				WithDetails(fmt.Sprintf("%d exceeds limit %d", fileHeader.Size, limitBytes)))
			return
		}
	}

	// Stage the upload outside the service so the request stream is fully
	// consumed here; the task takes ownership of the temp file afterwards.
	// 先在处理层暂存上传文件以完整消费请求流；随后由导入任务接管该临时文件。
	tmpFile, err := os.CreateTemp("", "fns-backup-import-*.zip")
	if err != nil {
		response.ToResponse(code.ErrorServerInternal.WithDetails(err.Error()))
		return
	}
	tmpPath := tmpFile.Name()

	if err := func() error {
		defer tmpFile.Close()
		src, err := fileHeader.Open()
		if err != nil {
			return err
		}
		defer src.Close()
		_, err = io.Copy(tmpFile, src)
		return err
	}(); err != nil {
		os.Remove(tmpPath)
		h.logError(c.Request.Context(), "BackupHandler.Import.SaveUpload", err)
		response.ToResponse(code.ErrorServerInternal.WithDetails(err.Error()))
		return
	}

	// ZIP magic check: reject non-ZIP uploads (e.g. renamed executables) early.
	// ZIP 魔数校验：提前拦截非 ZIP 上传（如改名的可执行文件）。
	if !util.IsZipFile(tmpPath) {
		os.Remove(tmpPath)
		response.ToResponse(code.ErrorBackupImportNotZip)
		return
	}

	task, err := h.App.BackupService.SubmitImportBackup(c.Request.Context(), uid, params, tmpPath)
	if err != nil {
		h.logError(c.Request.Context(), "BackupHandler.Import", err)
		apperrors.ErrorResponse(c, err)
		return
	}

	response.ToResponse(code.SuccessCreate.WithData(task))
}

// ImportStatus returns the snapshot of an async backup import task
// @Summary Get backup import task status
// @Description Poll the background import task by id; phases: validating | await_partial | importing | success | failed | cancelled
// @Tags Backup
// @Security UserAuthToken
// @Produce json
// @Param taskId query string true "Task ID"
// @Success 200 {object} pkgapp.Res{data=dto.BackupImportTaskStatusDTO} "Success"
// @Failure 400 {object} pkgapp.Res "Invalid Params"
// @Failure 401 {object} pkgapp.Res "Token Required"
// @Failure 500 {object} pkgapp.Res "Internal Server Error"
// @Router /api/backup/import/status [get]
func (h *BackupHandler) ImportStatus(c *gin.Context) {
	response := pkgapp.NewResponse(c)

	uid := pkgapp.GetUID(c)
	if uid == 0 {
		response.ToResponse(code.ErrorNotUserAuthToken)
		return
	}

	params := &dto.BackupImportTaskQuery{}
	if valid, errs := pkgapp.BindAndValid(c, params); !valid {
		response.ToResponse(code.ErrorInvalidParams.WithDetails(errs.ErrorsToString()).WithData(errs.MapsToString()))
		return
	}

	status, err := h.App.BackupService.GetImportTaskStatus(c.Request.Context(), uid, params.TaskID)
	if err != nil {
		h.logError(c.Request.Context(), "BackupHandler.ImportStatus", err)
		apperrors.ErrorResponse(c, err)
		return
	}

	response.ToResponse(code.Success.WithData(status))
}

// ImportConfirm confirms or cancels an await_partial backup import task
// @Summary Confirm backup import task
// @Description For tasks in await_partial: allowPartial=true imports the available sections only; false cancels the task
// @Tags Backup
// @Security UserAuthToken
// @Accept json
// @Produce json
// @Param params body dto.BackupImportConfirmRequest true "Confirm Parameters"
// @Success 200 {object} pkgapp.Res{data=dto.BackupImportTaskStatusDTO} "Success"
// @Failure 400 {object} pkgapp.Res "Invalid Params"
// @Failure 401 {object} pkgapp.Res "Token Required"
// @Failure 500 {object} pkgapp.Res "Internal Server Error"
// @Router /api/backup/import/confirm [post]
func (h *BackupHandler) ImportConfirm(c *gin.Context) {
	response := pkgapp.NewResponse(c)

	uid := pkgapp.GetUID(c)
	if uid == 0 {
		response.ToResponse(code.ErrorNotUserAuthToken)
		return
	}

	params := &dto.BackupImportConfirmRequest{}
	if valid, errs := pkgapp.BindAndValid(c, params); !valid {
		response.ToResponse(code.ErrorInvalidParams.WithDetails(errs.ErrorsToString()).WithData(errs.MapsToString()))
		return
	}

	status, err := h.App.BackupService.ConfirmImportTask(c.Request.Context(), uid, params.TaskID, params.AllowPartial)
	if err != nil {
		h.logError(c.Request.Context(), "BackupHandler.ImportConfirm", err)
		apperrors.ErrorResponse(c, err)
		return
	}

	response.ToResponse(code.Success.WithData(status))
}

func (h *BackupHandler) logError(ctx context.Context, method string, err error) {
	traceID := middleware.GetTraceID(ctx)
	h.App.Logger().Error(method,
		zap.Error(err),
		zap.String("traceId", traceID),
	)
}
