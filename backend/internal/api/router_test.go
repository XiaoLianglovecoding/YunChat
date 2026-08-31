package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
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
	if body["mode"] != "skeleton" {
		t.Fatalf("mode = %v, want skeleton", body["mode"])
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

func TestProtectedRouteIsClosedUntilJWTIsImplemented(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/friend/list", nil)

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
