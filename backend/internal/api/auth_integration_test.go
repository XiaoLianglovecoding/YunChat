package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"
	"time"

	"go.uber.org/zap"

	authtoken "my-im/internal/auth"
	"my-im/internal/config"
	"my-im/internal/infra"
	"my-im/internal/repository"
	"my-im/internal/service"
)

type integrationEnvelope[T any] struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    T      `json:"data"`
}

// 运行方式：先 docker compose up -d mysql redis，再执行
// MYIM_INTEGRATION=1 go test ./internal/api -run TestAuthHTTPIntegration -v
func TestAuthHTTPIntegration(t *testing.T) {
	if os.Getenv("MYIM_INTEGRATION") != "1" {
		t.Skip("set MYIM_INTEGRATION=1 to use local Docker MySQL and Redis")
	}
	ctx := context.Background()
	db, err := infra.OpenMySQL(ctx, config.MySQLConfig{
		Host: "127.0.0.1", Port: 13306, User: "my_im", Password: "my_im123", DBName: "my_im",
		ConnectTimeoutMS: 3000, QueryTimeoutMS: 3000, MaxOpenConns: 5, MaxIdleConns: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	redisClient, err := infra.OpenRedis(ctx, config.RedisConfig{Addr: "127.0.0.1:16379", DialTimeoutMS: 3000, ReadTimeoutMS: 2000, WriteTimeoutMS: 2000, PoolSize: 5, HealthTimeoutMS: 1000})
	if err != nil {
		t.Fatal(err)
	}
	defer redisClient.Close()

	users := repository.NewMySQLRepository(db, 3*time.Second, nil)
	sessions := repository.NewRedisRepo(redisClient)
	tokens, _ := authtoken.NewManager("0123456789abcdef0123456789abcdef", "my-im-integration", time.Hour, 24*time.Hour)
	authService, err := service.NewAuthService(users, sessions, tokens, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	uploadRoot := t.TempDir()
	avatarService, err := service.NewAvatarService(users, uploadRoot, 1, []string{"png"})
	if err != nil {
		t.Fatal(err)
	}
	router := NewRouter(RouterOptions{ServiceName: "my-im-test", UploadDir: uploadRoot, Auth: authService,
		TokenVerifier: tokens, Profile: avatarService, Upload: avatarService, FileMaxSizeMB: 1})

	username := "auth_it_" + strconv.FormatInt(time.Now().UnixNano(), 36)
	registerBody := fmt.Sprintf(`{"username":%q,"password":"secret1"}`, username)
	status, body := integrationJSONRequest(t, router, http.MethodPost, "/api/v1/auth/register", registerBody, "")
	if status != http.StatusCreated {
		t.Fatalf("register status=%d body=%s", status, body)
	}
	var registered integrationEnvelope[RegisterResponse]
	if err := json.Unmarshal(body, &registered); err != nil || registered.Code != 0 || registered.Data.UserID <= 0 {
		t.Fatalf("register response=%s err=%v", body, err)
	}
	userID := registered.Data.UserID
	defer func() {
		_ = sessions.RevokeUserRefreshSessions(context.Background(), userID)
		_, _ = db.ExecContext(context.Background(), `DELETE FROM users WHERE id=?`, userID)
	}()

	status, body = integrationJSONRequest(t, router, http.MethodPost, "/api/v1/auth/register", registerBody, "")
	assertIntegrationError(t, status, body, http.StatusConflict, 1103)
	status, body = integrationJSONRequest(t, router, http.MethodPost, "/api/v1/auth/login", `{"username":"missing-user","password":"wrong-password"}`, "")
	assertIntegrationError(t, status, body, http.StatusUnauthorized, 1105)
	status, body = integrationJSONRequest(t, router, http.MethodPost, "/api/v1/auth/login", fmt.Sprintf(`{"username":%q,"password":"wrong-password"}`, username), "")
	assertIntegrationError(t, status, body, http.StatusUnauthorized, 1105)

	status, body = integrationJSONRequest(t, router, http.MethodPost, "/api/v1/auth/login", fmt.Sprintf(`{"username":%q,"password":"secret1"}`, username), "")
	var login integrationEnvelope[LoginResponse]
	if status != http.StatusOK || json.Unmarshal(body, &login) != nil || login.Data.AccessToken == "" || login.Data.RefreshToken == "" {
		t.Fatalf("login failed with status=%d", status)
	}
	status, body = integrationJSONRequest(t, router, http.MethodGet, "/api/v1/friend/list", "", "")
	assertIntegrationError(t, status, body, http.StatusUnauthorized, 1003)
	status, _ = integrationJSONRequest(t, router, http.MethodGet, "/api/v1/friend/list", "", login.Data.AccessToken)
	if status != http.StatusNotImplemented {
		t.Fatalf("valid access token did not pass middleware: status=%d", status)
	}

	refreshRequest := fmt.Sprintf(`{"refresh_token":%q}`, login.Data.RefreshToken)
	status, body = integrationJSONRequest(t, router, http.MethodPost, "/api/v1/auth/refresh", refreshRequest, "")
	var refreshed integrationEnvelope[LoginResponse]
	if status != http.StatusOK || json.Unmarshal(body, &refreshed) != nil || refreshed.Data.RefreshToken == login.Data.RefreshToken {
		t.Fatalf("refresh rotation failed with status=%d", status)
	}
	status, body = integrationJSONRequest(t, router, http.MethodPost, "/api/v1/auth/refresh", refreshRequest, "")
	assertIntegrationError(t, status, body, http.StatusUnauthorized, 1106)

	newUsername := username + "_new"
	status, body = integrationJSONRequest(t, router, http.MethodPut, "/api/v1/account/username", fmt.Sprintf(`{"username":%q}`, newUsername), refreshed.Data.AccessToken)
	var renamed integrationEnvelope[LoginResponse]
	if status != http.StatusOK || json.Unmarshal(body, &renamed) != nil {
		t.Fatalf("rename failed with status=%d", status)
	}
	renamedClaims, err := tokens.ParseAccess(renamed.Data.AccessToken)
	if err != nil || renamedClaims.Username != newUsername {
		t.Fatalf("renamed claims=%+v err=%v", renamedClaims, err)
	}
	status, body = integrationJSONRequest(t, router, http.MethodPost, "/api/v1/auth/refresh", fmt.Sprintf(`{"refresh_token":%q}`, refreshed.Data.RefreshToken), "")
	assertIntegrationError(t, status, body, http.StatusUnauthorized, 1106)

	status, body = integrationJSONRequest(t, router, http.MethodPut, "/api/v1/account/password", `{"current_password":"secret1","new_password":"new-secret"}`, renamed.Data.AccessToken)
	if status != http.StatusOK {
		t.Fatalf("password update status=%d body=%s", status, body)
	}
	status, body = integrationJSONRequest(t, router, http.MethodPost, "/api/v1/auth/refresh", fmt.Sprintf(`{"refresh_token":%q}`, renamed.Data.RefreshToken), "")
	assertIntegrationError(t, status, body, http.StatusUnauthorized, 1106)
	status, body = integrationJSONRequest(t, router, http.MethodPost, "/api/v1/auth/login", fmt.Sprintf(`{"username":%q,"password":"new-secret"}`, newUsername), "")
	var finalLogin integrationEnvelope[LoginResponse]
	if status != http.StatusOK || json.Unmarshal(body, &finalLogin) != nil {
		t.Fatalf("new password login failed with status=%d", status)
	}

	png := []byte("\x89PNG\r\n\x1a\nhttp-integration-content")
	status, body = integrationUploadRequest(t, router, "/api/v1/upload/avatar", finalLogin.Data.AccessToken, "../../avatar.png", png)
	var uploaded integrationEnvelope[UploadAvatarResponse]
	if status != http.StatusOK || json.Unmarshal(body, &uploaded) != nil || uploaded.Data.URL == "" || uploaded.Data.FilePath == "" {
		t.Fatalf("upload status=%d body=%s", status, body)
	}
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/avatar/%d", userID), nil))
	if recorder.Code != http.StatusFound || recorder.Header().Get("Location") != uploaded.Data.URL {
		t.Fatalf("avatar redirect status=%d location=%q", recorder.Code, recorder.Header().Get("Location"))
	}
	recorder = httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, uploaded.Data.URL, nil))
	if recorder.Code != http.StatusOK || !bytes.Equal(recorder.Body.Bytes(), png) {
		t.Fatalf("static avatar status=%d", recorder.Code)
	}

	var storedHash string
	if err := db.QueryRowContext(ctx, `SELECT password_hash FROM users WHERE id=?`, userID).Scan(&storedHash); err != nil || storedHash == "new-secret" {
		t.Fatalf("password storage is unsafe: err=%v", err)
	}
}

func integrationJSONRequest(t *testing.T, handler http.Handler, method, path, body, accessToken string) (int, []byte) {
	t.Helper()
	request := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	if accessToken != "" {
		request.Header.Set("Authorization", "Bearer "+accessToken)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder.Code, recorder.Body.Bytes()
}

func integrationUploadRequest(t *testing.T, handler http.Handler, path, accessToken, filename string, content []byte) (int, []byte) {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, path, &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.Header.Set("Authorization", "Bearer "+accessToken)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder.Code, recorder.Body.Bytes()
}

func assertIntegrationError(t *testing.T, status int, body []byte, wantStatus, wantCode int) {
	t.Helper()
	var response integrationEnvelope[json.RawMessage]
	if err := json.Unmarshal(body, &response); err != nil || status != wantStatus || response.Code != wantCode {
		t.Fatalf("status=%d code=%d body=%s err=%v; want status=%d code=%d", status, response.Code, body, err, wantStatus, wantCode)
	}
}
