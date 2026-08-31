package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"my-im/internal/apperror"
	authtoken "my-im/internal/auth"
)

func newTestRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	return NewRouter(RouterOptions{ServiceName: "my-im-test", WSPath: "/ws"})
}

func TestHealth(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/health", nil)

	newTestRouter().ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	var body map[string]interface{}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body["service"] != "my-im-test" {
		t.Fatalf("service = %v, want my-im-test", body["service"])
	}
}

func TestPublicBusinessRouteReturnsTodo(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/register", nil)

	newTestRouter().ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusNotImplemented)
	}
	var body Response
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Code != CodeNotImplemented {
		t.Fatalf("code = %d, want %d", body.Code, CodeNotImplemented)
	}
}

func TestOriginalBusinessRouteCountIsPreserved(t *testing.T) {
	if got, want := len(BusinessRoutes()), 42; got != want {
		t.Fatalf("business route count = %d, want %d", got, want)
	}
}

func TestEveryBusinessRouteIsRegistered(t *testing.T) {
	registered := make(map[string]struct{})
	for _, route := range newTestRouter().Routes() {
		registered[route.Method+" "+route.Path] = struct{}{}
	}

	for _, route := range BusinessRoutes() {
		key := route.Method + " /api/v1" + route.Path
		if _, ok := registered[key]; !ok {
			t.Errorf("missing route %s", key)
		}
	}
}

func TestProtectedRouteStaysClosedWithoutVerifier(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/friend/list", nil)

	newTestRouter().ServeHTTP(recorder, request)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusUnauthorized)
	}
	var body Response
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Code != int(apperror.CodeUnauthorized) {
		t.Fatalf("code = %d, want %d", body.Code, apperror.CodeUnauthorized)
	}
}

func TestEveryProtectedRouteRequiresAuthorization(t *testing.T) {
	manager, err := authtoken.NewManager("0123456789abcdef0123456789abcdef", "my-im-test", time.Hour, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	router := NewRouter(RouterOptions{ServiceName: "my-im-test", TokenVerifier: manager})
	replacer := strings.NewReplacer(":friendID", "2", ":groupID", "3", ":memberID", "4", ":momentID", "5", ":commentID", "6", ":msgID", "7", ":convID", "p_1_2")
	for _, route := range protectedRoutes {
		path := "/api/v1" + replacer.Replace(route.Path)
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(route.Method, path, nil)
		router.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusUnauthorized {
			t.Errorf("%s %s status = %d, want 401; body=%s", route.Method, path, recorder.Code, recorder.Body.String())
		}
	}
}
