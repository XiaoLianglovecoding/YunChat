package service

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"my-im/internal/apperror"
	"my-im/internal/model"
)

type fakeAvatarUsers struct {
	user      *model.User
	updateErr error
}

func (f *fakeAvatarUsers) GetUserByID(_ context.Context, id int64) (*model.User, error) {
	if f.user == nil || f.user.ID != id {
		return nil, nil
	}
	copy := *f.user
	return &copy, nil
}

func (f *fakeAvatarUsers) UpdateAvatarURL(_ context.Context, id int64, url string) error {
	if f.updateErr != nil {
		return f.updateErr
	}
	if f.user == nil || f.user.ID != id {
		return errors.New("missing user")
	}
	f.user.AvatarURL = url
	return nil
}

func TestAvatarUploadValidatesContentAndWritesAtomically(t *testing.T) {
	root := t.TempDir()
	users := &fakeAvatarUsers{user: &model.User{ID: 7, Username: "alice"}}
	service, err := NewAvatarService(users, root, 1, []string{"png"})
	if err != nil {
		t.Fatal(err)
	}
	png := []byte("\x89PNG\r\n\x1a\nminimal-test-content")
	result, err := service.UploadAvatar(context.Background(), 7, "../../portrait.png", bytes.NewReader(png))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(result.FilePath, "..") || !strings.HasPrefix(result.URL, "/uploads/avatars/") {
		t.Fatalf("unsafe result: %+v", result)
	}
	storedPath := filepath.Join(root, filepath.FromSlash(result.FilePath))
	stored, err := os.ReadFile(storedPath)
	if err != nil || !bytes.Equal(stored, png) {
		t.Fatalf("stored file mismatch: err=%v data=%q", err, stored)
	}
	entries, _ := os.ReadDir(filepath.Join(root, "avatars"))
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".tmp") {
			t.Fatalf("temporary file remained: %s", entry.Name())
		}
	}
	if users.user.AvatarURL != result.URL || filepath.IsAbs(result.FilePath) {
		t.Fatalf("profile/result contract mismatch: user=%+v result=%+v", users.user, result)
	}
}

func TestAvatarUploadRejectsExtensionMIMEAndSize(t *testing.T) {
	users := &fakeAvatarUsers{user: &model.User{ID: 7, Username: "alice"}}
	service, _ := NewAvatarService(users, t.TempDir(), 1, []string{"png"})
	_, err := service.UploadAvatar(context.Background(), 7, "avatar.jpg", bytes.NewReader([]byte("not an image")))
	assertAvatarCode(t, err, apperror.CodeInvalidParam)
	_, err = service.UploadAvatar(context.Background(), 7, "avatar.png", bytes.NewReader([]byte("not an image")))
	assertAvatarCode(t, err, apperror.CodeInvalidParam)
	tooLarge := append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{0}, 1024*1024)...)
	_, err = service.UploadAvatar(context.Background(), 7, "avatar.png", bytes.NewReader(tooLarge))
	assertAvatarCode(t, err, apperror.CodeInvalidParam)
}

func TestAvatarUploadRemovesFileWhenProfileUpdateFails(t *testing.T) {
	root := t.TempDir()
	users := &fakeAvatarUsers{user: &model.User{ID: 7}, updateErr: errors.New("database down")}
	service, _ := NewAvatarService(users, root, 1, []string{"png"})
	_, err := service.UploadAvatar(context.Background(), 7, "avatar.png", bytes.NewReader([]byte("\x89PNG\r\n\x1a\ncontent")))
	assertAvatarCode(t, err, apperror.CodeInternalFailure)
	entries, readErr := os.ReadDir(filepath.Join(root, "avatars"))
	if readErr != nil || len(entries) != 0 {
		t.Fatalf("orphan upload remained: entries=%v err=%v", entries, readErr)
	}
}

func TestGetAvatarUsesDatabaseProfile(t *testing.T) {
	users := &fakeAvatarUsers{user: &model.User{ID: 7, Username: "alice", AvatarURL: "/uploads/avatars/a.png"}}
	service, _ := NewAvatarService(users, t.TempDir(), 1, []string{"png"})
	profile, err := service.GetAvatar(context.Background(), 7)
	if err != nil || profile.Username != "alice" || profile.AvatarURL != users.user.AvatarURL {
		t.Fatalf("profile=%+v err=%v", profile, err)
	}
}

func assertAvatarCode(t *testing.T, err error, want apperror.Code) {
	t.Helper()
	if err == nil || apperror.From(err).Code != want {
		t.Fatalf("error=%v want code=%d", err, want)
	}
}
