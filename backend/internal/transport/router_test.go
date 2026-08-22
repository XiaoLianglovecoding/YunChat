package transport

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/XiaoLianglovecoding/linknest-im/backend/internal/application"
	"github.com/XiaoLianglovecoding/linknest-im/backend/internal/config"
	"github.com/XiaoLianglovecoding/linknest-im/backend/internal/realtime"
	"github.com/XiaoLianglovecoding/linknest-im/backend/pkg/token"
	"go.uber.org/zap"
)

func TestHealthEndpoint(t *testing.T) {
	cfg := config.Default()
	cfg.JWT.Secret = "01234567890123456789012345678901"
	tokens, err := token.NewManager(cfg.JWT.Issuer, cfg.JWT.Secret, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	router := NewRouter(cfg, zap.NewNop(), tokens, nil, nil, application.NewTODO(), realtime.NewHub())

	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", response.Code, response.Body.String())
	}
	if response.Header().Get("X-Request-ID") == "" {
		t.Fatal("missing request id header")
	}
}

func TestRegisterRejectsAnEmptyBody(t *testing.T) {
	cfg := config.Default()
	cfg.JWT.Secret = "01234567890123456789012345678901"
	tokens, _ := token.NewManager(cfg.JWT.Issuer, cfg.JWT.Secret, time.Hour)
	router := NewRouter(cfg, zap.NewNop(), tokens, nil, nil, application.NewTODO(), realtime.NewHub())

	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/register", nil)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", response.Code, response.Body.String())
	}
}
