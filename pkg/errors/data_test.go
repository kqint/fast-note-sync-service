package errors

import (
	"encoding/json"
	"testing"

	"github.com/haierkeys/fast-note-sync-service/pkg/code"
)

// TestErrorResponseCarriesData guards against dropping code.Code.WithData payloads
// on the error response path (regression: backup import 545 partial-validation data).
// TestErrorResponseCarriesData 防止错误响应路径丢失 code.Code.WithData 数据
// （回归：备份导入 545 部分导入校验数据）。
func TestErrorResponseCarriesData(t *testing.T) {
	data := map[string]interface{}{
		"available": map[string]bool{"notes": true, "vaultConfig": false, "noteHistory": false},
		"missing":   []string{"vaultConfig"},
	}
	codeErr := code.ErrorBackupImportSectionMissing.WithData(data)

	appErr := NewAppError(codeErr, nil)

	if appErr.Data == nil {
		t.Fatal("expected data passthrough, got nil")
	}
	raw, jsonErr := json.Marshal(appErr)
	if jsonErr != nil {
		t.Fatal(jsonErr)
	}
	var decoded map[string]interface{}
	if jsonErr := json.Unmarshal(raw, &decoded); jsonErr != nil {
		t.Fatal(jsonErr)
	}
	if _, ok := decoded["data"]; !ok {
		t.Fatalf("expected serialized 'data' field, got %s", string(raw))
	}
	if decoded["code"] != float64(545) {
		t.Fatalf("expected code 545, got %v", decoded["code"])
	}
}

// TestErrorResponseWithoutData ensures plain errors still serialize without a data field.
// TestErrorResponseWithoutData 确保普通错误仍不携带 data 字段。
func TestErrorResponseWithoutData(t *testing.T) {
	appErr := NewAppError(code.ErrorVaultNotFound, nil)
	if appErr.Data != nil {
		t.Fatal("expected nil data for code without data")
	}
}
