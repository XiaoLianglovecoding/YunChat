package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	authtoken "my-im/internal/auth"
)

const middlewareSecret = "0123456789abcdef0123456789abcdef"

func TestRequireAuthContractAndStrongIdentity(t *testing.T) {
	gin.SetMode(gin.TestMode)
	manager, _ := authtoken.NewManager(middlewareSecret, "my-im-test", time.Hour, 24*time.Hour)
	pair, _ := manager.IssuePair(9, "alice", "")
	router := gin.New()
	router.GET("/protected", RequireAuth(manager), func(c *gin.Context) {
		userID, ok := CurrentUserID(c)
		if !ok {
			t.Fatal("strong user ID was not injected")
		}
		c.JSON(http.StatusOK, gin.H{"user_id": int64(userID)})
	})

	assertAuthResponse(t, router, "", http.StatusUnauthorized, 1003)
	assertAuthResponse(t, router, "Basic abc", http.StatusUnauthorized, 1106)
	assertAuthResponse(t, router, "Bearer "+pair.RefreshToken, http.StatusUnauthorized, 1106)

	request := httptest.NewRequest(http.MethodGet, "/protected", nil)
	request.Header.Set("Authorization", "Bearer "+pair.AccessToken)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("valid access status = %d, body=%s", recorder.Code, recorder.Body.String())
	}
}

func assertAuthResponse(t *testing.T, router http.Handler, header string, wantStatus, wantCode int) {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "/protected", nil)
	if header != "" {
		request.Header.Set("Authorization", header)
	}
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	if recorder.Code != wantStatus {
		t.Fatalf("status = %d, want %d", recorder.Code, wantStatus)
	}
	var response struct {
		Code int `json:"code"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Code != wantCode {
		t.Fatalf("code = %d, want %d", response.Code, wantCode)
	}
}
