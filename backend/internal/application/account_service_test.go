package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/XiaoLianglovecoding/linknest-im/backend/internal/domain"
	"github.com/XiaoLianglovecoding/linknest-im/backend/pkg/id"
	"github.com/XiaoLianglovecoding/linknest-im/backend/pkg/password"
	"github.com/XiaoLianglovecoding/linknest-im/backend/pkg/token"
)

type fakeAccountRepository struct {
	user            domain.User
	hash            string
	settings        domain.UserSettings
	session         domain.Session
	changedPassword bool
}

func (repo *fakeAccountRepository) CreateAccount(_ context.Context, user domain.User, hash string, settings domain.UserSettings) error {
	repo.user, repo.hash, repo.settings = user, hash, settings
	return nil
}
func (repo *fakeAccountRepository) CreateAccountAndSession(_ context.Context, user domain.User, hash string, settings domain.UserSettings, device domain.Device, session domain.Session) (domain.Session, error) {
	repo.user, repo.hash, repo.settings = user, hash, settings
	session.UserID, session.DeviceID = user.ID, device.ID
	repo.session = session
	return session, nil
}
func (repo *fakeAccountRepository) FindByID(context.Context, domain.UserID) (domain.User, error) {
	if repo.user.ID == 0 {
		return domain.User{}, domain.ErrNotFound
	}
	return repo.user, nil
}
func (repo *fakeAccountRepository) FindByLogin(context.Context, string) (domain.User, string, error) {
	if repo.user.ID == 0 {
		return domain.User{}, "", domain.ErrNotFound
	}
	return repo.user, repo.hash, nil
}
func (repo *fakeAccountRepository) FindPasswordHash(context.Context, domain.UserID) (string, error) {
	return repo.hash, nil
}
func (repo *fakeAccountRepository) UpdateProfile(_ context.Context, user domain.User) error {
	repo.user = user
	return nil
}
func (repo *fakeAccountRepository) GetSettings(context.Context, domain.UserID) (domain.UserSettings, error) {
	return repo.settings, nil
}
func (repo *fakeAccountRepository) UpdateSettings(_ context.Context, settings domain.UserSettings) error {
	repo.settings = settings
	return nil
}
func (repo *fakeAccountRepository) ChangePassword(_ context.Context, _ domain.UserID, hash string) error {
	repo.hash, repo.changedPassword = hash, true
	return nil
}
func (repo *fakeAccountRepository) StartSingleDeviceSession(_ context.Context, userID domain.UserID, device domain.Device, session domain.Session) (domain.Session, error) {
	session.UserID, session.DeviceID = userID, device.ID
	repo.session = session
	return session, nil
}
func (repo *fakeAccountRepository) RotateRefreshToken(_ context.Context, _ []byte, replacement domain.Session, _ time.Time) (domain.Session, error) {
	replacement.UserID, replacement.DeviceID, replacement.FamilyID = repo.session.UserID, repo.session.DeviceID, repo.session.FamilyID
	repo.session = replacement
	return replacement, nil
}
func (repo *fakeAccountRepository) RevokeDeviceSession(context.Context, domain.UserID, domain.DeviceID, time.Time) error {
	repo.session = domain.Session{}
	return nil
}
func (repo *fakeAccountRepository) IsSessionActive(context.Context, domain.UserID, domain.DeviceID, uint64, time.Time) (bool, error) {
	return repo.session.TokenID != 0, nil
}

func newTestAccountService(t *testing.T, repo *fakeAccountRepository) *accountService {
	t.Helper()
	ids, err := id.New(8)
	if err != nil {
		t.Fatal(err)
	}
	tokens, err := token.NewManager("linknest-test", "01234567890123456789012345678901", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	services, err := NewServices(Dependencies{Accounts: repo, IDs: ids, Tokens: tokens, RefreshTTL: 24 * time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	return services.Auth.(*accountService)
}

func TestRegisterCreatesDefaultsAndSession(t *testing.T) {
	repo := &fakeAccountRepository{}
	service := newTestAccountService(t, repo)
	session, err := service.Register(context.Background(), RegisterCommand{Username: "Alice_1", Nickname: "Alice", Email: "ALICE@example.com", Password: "correct-horse", DeviceKey: "browser-1"})
	if err != nil {
		t.Fatal(err)
	}
	if session.User.Username != "alice_1" || session.User.Email != "alice@example.com" {
		t.Fatalf("unexpected user: %+v", session.User)
	}
	if session.AccessToken == "" || session.RefreshToken == "" || repo.session.DeviceID == 0 {
		t.Fatal("session was not issued")
	}
	if repo.settings.Theme != "system" || !repo.settings.NotificationEnabled {
		t.Fatalf("unexpected defaults: %+v", repo.settings)
	}
}

func TestRegisterRejectsMalformedEmail(t *testing.T) {
	repo := &fakeAccountRepository{}
	service := newTestAccountService(t, repo)
	_, err := service.Register(context.Background(), RegisterCommand{Username: "alice_1", Nickname: "Alice", Email: "not-an-email", Password: "correct-horse"})
	if !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatalf("error = %v, want invalid argument", err)
	}
	if repo.user.ID != 0 {
		t.Fatal("invalid account was persisted")
	}
}

func TestLoginHidesCredentialFailure(t *testing.T) {
	hash, err := password.Hash("correct-password")
	if err != nil {
		t.Fatal(err)
	}
	repo := &fakeAccountRepository{user: domain.User{ID: 1, Status: domain.UserStatusActive}, hash: hash}
	service := newTestAccountService(t, repo)
	_, err = service.Login(context.Background(), LoginCommand{Login: "alice", Password: "wrong-password"})
	if !errors.Is(err, domain.ErrInvalidCredentials) {
		t.Fatalf("error = %v, want invalid credentials", err)
	}
}

func TestChangePasswordVerifiesCurrentPassword(t *testing.T) {
	hash, err := password.Hash("old-password")
	if err != nil {
		t.Fatal(err)
	}
	repo := &fakeAccountRepository{user: domain.User{ID: 1, Status: domain.UserStatusActive}, hash: hash}
	service := newTestAccountService(t, repo)
	if err := service.ChangePassword(context.Background(), 1, "old-password", "new-password"); err != nil {
		t.Fatal(err)
	}
	if !repo.changedPassword || !password.Verify(repo.hash, "new-password") {
		t.Fatal("password was not changed")
	}
}
