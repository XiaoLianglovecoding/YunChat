package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"my-im/internal/apperror"
)

func TestWriteErrorSeparatesTransportAndBusinessCode(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	WriteError(c, apperror.New(apperror.CodeUsernameTaken))
	if recorder.Code != http.StatusConflict {
		t.Fatalf("HTTP status = %d", recorder.Code)
	}
	var response Response
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Code != 1103 {
		t.Fatalf("business code = %d", response.Code)
	}
}

func TestReadyRequiresEveryDependency(t *testing.T) {
	router := NewRouter(RouterOptions{ServiceName: "my-im-test", Readiness: func(context.Context) map[string]error {
		return map[string]error{"mysql": nil, "redis": nil, "rabbitmq": nil}
	}})
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/ready", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("ready status = %d", recorder.Code)
	}

	router = NewRouter(RouterOptions{ServiceName: "my-im-test", Readiness: func(context.Context) map[string]error {
		return map[string]error{"mysql": nil, "redis": errors.New("down"), "rabbitmq": nil}
	}})
	recorder = httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/ready", nil))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("not-ready status = %d", recorder.Code)
	}
}
