package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
	"golang.org/x/crypto/bcrypt"

	"my-im/internal/apperror"
	authtoken "my-im/internal/auth"
	"my-im/internal/model"
	"my-im/internal/repository"
)

const serviceTestSecret = "0123456789abcdef0123456789abcdef"

type fakeAuthUsers struct {
	mu      sync.Mutex
	nextID  int64
	byID    map[int64]*model.User
	byName  map[string]int64
	forceID int64
}

func newFakeAuthUsers() *fakeAuthUsers {
	return &fakeAuthUsers{nextID: 1, byID: make(map[int64]*model.User), byName: make(map[string]int64)}
}

func (f *fakeAuthUsers) GetUserByID(_ context.Context, id int64) (*model.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return cloneUser(f.byID[id]), nil
}

func (f *fakeAuthUsers) GetUserByUsername(_ context.Context, name string) (*model.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return cloneUser(f.byID[f.byName[name]]), nil
}

func (f *fakeAuthUsers) CreateUser(_ context.Context, user *model.User) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, exists := f.byName[user.Username]; exists {
		return repository.ErrConflict
	}
	user.ID = f.nextID
	f.nextID++
	f.byID[user.ID] = cloneUser(user)
	f.byName[user.Username] = user.ID
	return nil
}

func (f *fakeAuthUsers) UpdateUsername(_ context.Context, id int64, oldName, newName string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if existing := f.byName[newName]; existing != 0 && existing != id {
		return repository.ErrConflict
	}
	user := f.byID[id]
	if user == nil {
		return errors.New("missing user")
	}
	delete(f.byName, oldName)
	user.Username = newName
	if user.Nickname == oldName {
		user.Nickname = newName
	}
	f.byName[newName] = id
	return nil
}

func (f *fakeAuthUsers) UpdatePasswordHash(_ context.Context, id int64, hash string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.byID[id] == nil {
		return errors.New("missing user")
	}
	f.byID[id].PasswordHash = hash
	return nil
}

func cloneUser(user *model.User) *model.User {
	if user == nil {
		return nil
	}
	copy := *user
	return &copy
}

type fakeRefreshSessions struct {
	mu        sync.Mutex
	sessions  map[string]repository.RefreshSession
	revoked   int
	revokeErr error
}

func newFakeRefreshSessions() *fakeRefreshSessions {
	return &fakeRefreshSessions{sessions: make(map[string]repository.RefreshSession)}
}

func (f *fakeRefreshSessions) StoreRefreshSession(_ context.Context, session repository.RefreshSession, _ time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sessions[session.JTI] = session
	return nil
}

func (f *fakeRefreshSessions) RotateRefreshSession(_ context.Context, oldJTI string, next repository.RefreshSession, _ time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	old, ok := f.sessions[oldJTI]
	if !ok || old.UserID != next.UserID || old.FamilyID != next.FamilyID {
		return repository.ErrNotFound
	}
	delete(f.sessions, oldJTI)
	f.sessions[next.JTI] = next
	return nil
}

func (f *fakeRefreshSessions) RevokeUserRefreshSessions(_ context.Context, userID int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.revokeErr != nil {
		return f.revokeErr
	}
	for jti, session := range f.sessions {
		if session.UserID == userID {
			delete(f.sessions, jti)
		}
	}
	f.revoked++
	return nil
}

func TestUpdatePasswordDoesNotChangeHashWhenSessionRevocationFails(t *testing.T) {
	service, users, sessions, _ := newTestAuthService(t, nil)
	registerAlice(t, service)
	before, _ := users.GetUserByID(context.Background(), 1)
	sessions.revokeErr = errors.New("redis unavailable")
	err := service.UpdatePassword(context.Background(), 1, "secret1", "new-secret")
	assertAppCode(t, err, apperror.CodeInternalFailure)
	after, _ := users.GetUserByID(context.Background(), 1)
	if after.PasswordHash != before.PasswordHash {
		t.Fatal("password hash changed even though refresh revocation failed")
	}
}

func newTestAuthService(t *testing.T, logger *zap.Logger) (*AuthServiceImpl, *fakeAuthUsers, *fakeRefreshSessions, *authtoken.Manager) {
	t.Helper()
	users := newFakeAuthUsers()
	sessions := newFakeRefreshSessions()
	tokens, err := authtoken.NewManager(serviceTestSecret, "my-im-test", time.Hour, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewAuthService(users, sessions, tokens, logger)
	if err != nil {
		t.Fatal(err)
	}
	return service, users, sessions, tokens
}

func registerAlice(t *testing.T, service *AuthServiceImpl) RegisterResult {
	t.Helper()
	result, err := service.Register(context.Background(), RegisterCommand{Username: "alice", Password: "secret1"})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestRegisterHashesPasswordAndMapsConcurrentDuplicate(t *testing.T) {
	service, users, _, _ := newTestAuthService(t, nil)
	result := registerAlice(t, service)
	stored, _ := users.GetUserByID(context.Background(), result.UserID)
	if stored.PasswordHash == "secret1" || bcrypt.CompareHashAndPassword([]byte(stored.PasswordHash), []byte("secret1")) != nil {
		t.Fatal("password was not stored as a bcrypt hash")
	}
	_, err := service.Register(context.Background(), RegisterCommand{Username: "alice", Password: "secret2"})
	assertAppCode(t, err, apperror.CodeUsernameTaken)
}

func TestLoginDoesNotRevealWhetherUserExists(t *testing.T) {
	service, _, sessions, tokens := newTestAuthService(t, nil)
	registerAlice(t, service)
	_, missingErr := service.Login(context.Background(), LoginCommand{Username: "nobody", Password: "wrong-password"})
	_, wrongErr := service.Login(context.Background(), LoginCommand{Username: "alice", Password: "wrong-password"})
	assertAppCode(t, missingErr, apperror.CodeWrongPassword)
	assertAppCode(t, wrongErr, apperror.CodeWrongPassword)

	pair, err := service.Login(context.Background(), LoginCommand{Username: "alice", Password: "secret1"})
	if err != nil {
		t.Fatal(err)
	}
	claims, err := tokens.ParseAccess(pair.AccessToken)
	if err != nil || claims.Username != "alice" || claims.UserID != 1 {
		t.Fatalf("unexpected access token: claims=%+v err=%v", claims, err)
	}
	refreshClaims, _ := tokens.ParseRefresh(pair.RefreshToken)
	if _, ok := sessions.sessions[refreshClaims.ID]; !ok {
		t.Fatal("refresh session was not stored")
	}
}

func TestRefreshIsOneTimeAndRejectsConcurrentReplay(t *testing.T) {
	service, _, _, _ := newTestAuthService(t, nil)
	registerAlice(t, service)
	pair, _ := service.Login(context.Background(), LoginCommand{Username: "alice", Password: "secret1"})

	start := make(chan struct{})
	errorsSeen := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			<-start
			_, err := service.Refresh(context.Background(), pair.RefreshToken)
			errorsSeen <- err
		}()
	}
	close(start)
	successes, rejected := 0, 0
	for i := 0; i < 2; i++ {
		err := <-errorsSeen
		if err == nil {
			successes++
		} else if apperror.From(err).Code == apperror.CodeInvalidToken {
			rejected++
		}
	}
	if successes != 1 || rejected != 1 {
		t.Fatalf("refresh results: successes=%d rejected=%d", successes, rejected)
	}
}

func TestUpdateUsernameRevokesOldRefreshAndResignsIdentity(t *testing.T) {
	service, _, sessions, tokens := newTestAuthService(t, nil)
	registerAlice(t, service)
	oldPair, _ := service.Login(context.Background(), LoginCommand{Username: "alice", Password: "secret1"})
	newPair, err := service.UpdateUsername(context.Background(), 1, "alice_new")
	if err != nil {
		t.Fatal(err)
	}
	claims, err := tokens.ParseAccess(newPair.AccessToken)
	if err != nil || claims.Username != "alice_new" {
		t.Fatalf("new token username = %+v, err=%v", claims, err)
	}
	if sessions.revoked != 1 {
		t.Fatalf("revoke count = %d", sessions.revoked)
	}
	_, err = service.Refresh(context.Background(), oldPair.RefreshToken)
	assertAppCode(t, err, apperror.CodeInvalidToken)
}

func TestUpdatePasswordRevokesSessionsAndWritesAuditEvent(t *testing.T) {
	core, observed := observer.New(zap.InfoLevel)
	service, _, sessions, _ := newTestAuthService(t, zap.New(core))
	registerAlice(t, service)
	oldPair, _ := service.Login(context.Background(), LoginCommand{Username: "alice", Password: "secret1"})
	if err := service.UpdatePassword(context.Background(), 1, "secret1", "new-secret"); err != nil {
		t.Fatal(err)
	}
	if sessions.revoked != 1 {
		t.Fatalf("revoke count = %d", sessions.revoked)
	}
	_, err := service.Refresh(context.Background(), oldPair.RefreshToken)
	assertAppCode(t, err, apperror.CodeInvalidToken)
	if _, err := service.Login(context.Background(), LoginCommand{Username: "alice", Password: "secret1"}); apperror.From(err).Code != apperror.CodeWrongPassword {
		t.Fatalf("old password still works: %v", err)
	}
	if _, err := service.Login(context.Background(), LoginCommand{Username: "alice", Password: "new-secret"}); err != nil {
		t.Fatalf("new password login failed: %v", err)
	}
	entries := observed.FilterMessage("security_event").All()
	if len(entries) != 1 || entries[0].ContextMap()["event"] != "password_changed" || entries[0].ContextMap()["user_id"] != int64(1) {
		t.Fatalf("unexpected audit entries: %+v", entries)
	}
}

func assertAppCode(t *testing.T, err error, want apperror.Code) {
	t.Helper()
	if err == nil {
		t.Fatalf("error is nil, want code=%d", want)
	}
	if got := apperror.From(err).Code; got != want {
		t.Fatalf("error = %v, code=%d, want=%d", err, got, want)
	}
}
