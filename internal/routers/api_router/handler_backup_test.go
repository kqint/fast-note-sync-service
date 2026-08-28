package api_router

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/haierkeys/fast-note-sync-service/internal/app"
	"github.com/haierkeys/fast-note-sync-service/internal/dto"
	svcmocks "github.com/haierkeys/fast-note-sync-service/internal/service/mocks"
	pkgapp "github.com/haierkeys/fast-note-sync-service/pkg/app"
	"github.com/haierkeys/fast-note-sync-service/pkg/code"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func newBackupTestContext(method, url, body string, uid int64) (*gin.Context, *httptest.ResponseRecorder) {
	w := httptest.NewRecorder()
	var req *http.Request
	if body != "" {
		req = httptest.NewRequest(method, url, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	} else {
		req = httptest.NewRequest(method, url, nil)
	}

	c, _ := gin.CreateTestContext(w)
	c.Request = req
	if uid > 0 {
		c.Set("user_token", &pkgapp.UserEntity{UID: uid})
	}
	return c, w
}

func newTestBackupHandler(backupSvc *svcmocks.MockBackupService) *BackupHandler {
	testApp := app.NewTestApp(&app.Services{
		BackupService: backupSvc,
	})
	return NewBackupHandler(testApp)
}

// TestBackupHandler_GetConfigs_Success verifies successful retrieval of backup configs
func TestBackupHandler_GetConfigs_Success(t *testing.T) {
	mockSvc := new(svcmocks.MockBackupService)

	mockData := []*dto.BackupConfigDTO{
		{ID: 1, Vault: "main"},
	}

	mockSvc.On("GetConfigs", mock.Anything, int64(1)).Return(mockData, nil)

	handler := newTestBackupHandler(mockSvc)
	c, w := newBackupTestContext("GET", "/api/backup/configs", "", 1)

	handler.GetConfigs(c)

	assert.Equal(t, http.StatusOK, w.Code)
	assertResponseCode(t, w, code.Success.Code())
	mockSvc.AssertExpectations(t)
}

// TestBackupHandler_UpdateConfig_Success verifies successful backup config update
func TestBackupHandler_UpdateConfig_Success(t *testing.T) {
	mockSvc := new(svcmocks.MockBackupService)

	mockData := &dto.BackupConfigDTO{
		ID:    1,
		Vault: "main",
	}

	mockSvc.On("UpdateConfig", mock.Anything, int64(1), mock.AnythingOfType("*dto.BackupConfigRequest")).
		Return(mockData, nil)

	handler := newTestBackupHandler(mockSvc)
	body := `{"id":1, "vault":"main", "type":"sync", "storageIds":"[1]", "cronStrategy":"daily"}`
	c, w := newBackupTestContext("POST", "/api/backup/config", body, 1)

	handler.UpdateConfig(c)

	assert.Equal(t, http.StatusOK, w.Code)
	assertResponseCode(t, w, code.SuccessUpdate.Code())
	mockSvc.AssertExpectations(t)
}

// TestBackupHandler_DeleteConfig_Success verifies successful backup config deletion
func TestBackupHandler_DeleteConfig_Success(t *testing.T) {
	mockSvc := new(svcmocks.MockBackupService)

	mockSvc.On("DeleteConfig", mock.Anything, int64(1), int64(1)).Return(nil)

	handler := newTestBackupHandler(mockSvc)
	// Delete request with body since c.ShouldBind parses body instead of query
	body := `{"id":1}`
	c, w := newBackupTestContext("DELETE", "/api/backup/config", body, 1)

	handler.DeleteConfig(c)

	assert.Equal(t, http.StatusOK, w.Code)
	assertResponseCode(t, w, code.Success.Code())
	mockSvc.AssertExpectations(t)
}

// TestBackupHandler_ListHistory_Success verifies successful history fetch
func TestBackupHandler_ListHistory_Success(t *testing.T) {
	mockSvc := new(svcmocks.MockBackupService)

	mockData := []*dto.BackupHistoryDTO{
		{ID: 10, ConfigID: 1},
	}

	mockSvc.On("ListHistory", mock.Anything, int64(1), int64(1), mock.AnythingOfType("*app.Pager")).
		Return(mockData, int64(1), nil)

	handler := newTestBackupHandler(mockSvc)
	c, w := newBackupTestContext("GET", "/api/backup/histories?configId=1", "", 1)

	handler.ListHistory(c)

	assert.Equal(t, http.StatusOK, w.Code)
	assertResponseCode(t, w, code.Success.Code())
	mockSvc.AssertExpectations(t)
}

// TestBackupHandler_Execute_Success verifies manual trigger backup execution
func TestBackupHandler_Execute_Success(t *testing.T) {
	mockSvc := new(svcmocks.MockBackupService)

	mockSvc.On("ExecuteUserBackup", mock.Anything, int64(1), int64(1)).Return(nil)

	handler := newTestBackupHandler(mockSvc)
	body := `{"id":1}`
	c, w := newBackupTestContext("POST", "/api/backup/execute", body, 1)

	handler.Execute(c)

	assert.Equal(t, http.StatusOK, w.Code)
	assertResponseCode(t, w, code.Success.Code())
	mockSvc.AssertExpectations(t)
}

// TestBackupHandler_ImportStatus_Success verifies task status polling
func TestBackupHandler_ImportStatus_Success(t *testing.T) {
	mockSvc := new(svcmocks.MockBackupService)

	mockData := &dto.BackupImportTaskStatusDTO{
		TaskID: "abc123",
		Phase:  "importing",
		Totals: dto.BackupImportTotalsDTO{Notes: 10},
		Progress: dto.BackupImportProgressDTO{Notes: 3},
	}
	mockSvc.On("GetImportTaskStatus", mock.Anything, int64(1), "abc123").Return(mockData, nil)

	handler := newTestBackupHandler(mockSvc)
	c, w := newBackupTestContext("GET", "/api/backup/import/status?taskId=abc123", "", 1)

	handler.ImportStatus(c)

	assert.Equal(t, http.StatusOK, w.Code)
	assertResponseCode(t, w, code.Success.Code())
	mockSvc.AssertExpectations(t)
}

// TestBackupHandler_ImportConfirm_Success verifies await_partial confirm flow
func TestBackupHandler_ImportConfirm_Success(t *testing.T) {
	mockSvc := new(svcmocks.MockBackupService)

	mockData := &dto.BackupImportTaskStatusDTO{
		TaskID: "abc123",
		Phase:  "importing",
	}
	mockSvc.On("ConfirmImportTask", mock.Anything, int64(1), "abc123", true).Return(mockData, nil)

	handler := newTestBackupHandler(mockSvc)
	body := `{"taskId":"abc123","allowPartial":true}`
	c, w := newBackupTestContext("POST", "/api/backup/import/confirm", body, 1)

	handler.ImportConfirm(c)

	assert.Equal(t, http.StatusOK, w.Code)
	assertResponseCode(t, w, code.Success.Code())
	mockSvc.AssertExpectations(t)
}

// TestBackupHandler_Import_Success verifies the async submit endpoint returns a task
// (multipart body with a valid zip file is staged then passed to the mock service).
func TestBackupHandler_Import_Success(t *testing.T) {
	mockSvc := new(svcmocks.MockBackupService)

	mockTask := &dto.BackupImportTaskDTO{TaskID: "abc123", Phase: "validating"}
	mockSvc.On("SubmitImportBackup", mock.Anything, int64(1), mock.AnythingOfType("*dto.BackupImportRequest"), mock.AnythingOfType("string")).
		Return(mockTask, nil)

	handler := newTestBackupHandler(mockSvc)

	// Build a tiny real zip file and multipart body
	zipBytes := []byte("PK\x03\x04") // magic-only header is enough for IsZipFile
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("file", "b.zip")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(zipBytes); err != nil {
		t.Fatal(err)
	}
	if err := writer.WriteField("vault", "main"); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	w := httptest.NewRecorder()
	req, err := http.NewRequest("POST", "/api/backup/import", body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	c, _ := gin.CreateTestContext(w)
	c.Request = req
	c.Set("user_token", &pkgapp.UserEntity{UID: 1})

	handler.Import(c)

	assert.Equal(t, http.StatusOK, w.Code)
	assertResponseCode(t, w, code.SuccessCreate.Code())
	mockSvc.AssertExpectations(t)
}

// TestBackupHandler_Import_NotZip rejects non-ZIP uploads with 547 before calling the service.
func TestBackupHandler_Import_NotZip(t *testing.T) {
	mockSvc := new(svcmocks.MockBackupService)

	handler := newTestBackupHandler(mockSvc)

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, _ := writer.CreateFormFile("file", "fake.exe")
	_, _ = part.Write([]byte("MZ\x90\x00not a zip"))
	_ = writer.WriteField("vault", "main")
	_ = writer.Close()

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/backup/import", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	c, _ := gin.CreateTestContext(w)
	c.Request = req
	c.Set("user_token", &pkgapp.UserEntity{UID: 1})

	handler.Import(c)

	assert.Equal(t, http.StatusOK, w.Code)
	assertResponseCode(t, w, code.ErrorBackupImportNotZip.Code())
	mockSvc.AssertNotCalled(t, "SubmitImportBackup", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}
