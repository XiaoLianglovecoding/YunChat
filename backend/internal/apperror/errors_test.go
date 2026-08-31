package apperror

import (
	"errors"
	"net/http"
	"testing"
)

func TestEveryFrozenCodeHasDefinition(t *testing.T) {
	codes := []Code{
		1000, 1001, 1002, 1003, 1101, 1102, 1103, 1104, 1105, 1106,
		1201, 1202, 1203, 1204, 1205, 1206, 1207,
		1301, 1302, 1303, 1304, 1305, 1306, 1307, 1308, 1309, 1310,
		1401, 1402, 1403, 1501, 1502, 1503, 1504, 1505, 1506,
		1701, 1702, 1703, 1900, 4001, 4002, 4003, 5001, 5002, 5003,
	}
	for _, code := range codes {
		definition, ok := DefinitionFor(code)
		if !ok {
			t.Errorf("code %d is missing from catalog", code)
			continue
		}
		if definition.HTTPStatus < 400 || definition.HTTPStatus > 599 {
			t.Errorf("code %d has invalid HTTP status %d", code, definition.HTTPStatus)
		}
		if definition.Message == "" {
			t.Errorf("code %d has empty safe message", code)
		}
	}
}

func TestBusinessCodeAndHTTPStatusAreIndependent(t *testing.T) {
	err := New(CodeUsernameTaken)
	if err.Code != 1103 {
		t.Fatalf("business code = %d", err.Code)
	}
	if err.HTTPStatus != http.StatusConflict {
		t.Fatalf("HTTP status = %d", err.HTTPStatus)
	}
}

func TestUnknownErrorBecomesSafeInternalError(t *testing.T) {
	cause := errors.New("database password must never be returned")
	err := From(cause)
	if err.Code != CodeInternalFailure || err.Message != "internal server error" {
		t.Fatalf("unexpected mapping: %+v", err)
	}
	if !errors.Is(err, cause) {
		t.Fatal("diagnostic cause should remain available for logs")
	}
}
