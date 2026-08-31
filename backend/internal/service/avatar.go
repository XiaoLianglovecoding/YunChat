package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"my-im/internal/apperror"
	"my-im/internal/model"
	"my-im/internal/repository"
)

type avatarUserRepository interface {
	GetUserByID(context.Context, int64) (*model.User, error)
	UpdateAvatarURL(context.Context, int64, string) error
}

type AvatarService struct {
	users       avatarUserRepository
	uploadRoot  string
	maxSize     int64
	allowedExts map[string]struct{}
}

var avatarMIMEs = map[string]string{
	"jpg": "image/jpeg", "jpeg": "image/jpeg", "png": "image/png",
	"gif": "image/gif", "webp": "image/webp",
}

func NewAvatarService(users avatarUserRepository, uploadRoot string, maxSizeMB int, allowedExts []string) (*AvatarService, error) {
	if users == nil {
		return nil, errors.New("avatar user repository must not be nil")
	}
	if maxSizeMB <= 0 {
		maxSizeMB = 10
	}
	root, err := filepath.Abs(uploadRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve upload root: %w", err)
	}
	allowed := make(map[string]struct{})
	for _, ext := range allowedExts {
		ext = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(ext), "."))
		if _, isImage := avatarMIMEs[ext]; isImage {
			allowed[ext] = struct{}{}
		}
	}
	if len(allowed) == 0 {
		for _, ext := range []string{"jpg", "jpeg", "png", "gif", "webp"} {
			allowed[ext] = struct{}{}
		}
	}
	return &AvatarService{users: users, uploadRoot: filepath.Clean(root), maxSize: int64(maxSizeMB) * 1024 * 1024, allowedExts: allowed}, nil
}

func (s *AvatarService) GetAvatar(ctx context.Context, userID int64) (AvatarProfile, error) {
	if userID <= 0 {
		return AvatarProfile{}, apperror.New(apperror.CodeInvalidParam)
	}
	user, err := s.users.GetUserByID(ctx, userID)
	if err != nil {
		return AvatarProfile{}, apperror.Wrap(apperror.CodeInternalFailure, err)
	}
	if user == nil {
		return AvatarProfile{}, apperror.New(apperror.CodeUserNotFound)
	}
	return AvatarProfile{Username: user.Username, AvatarURL: user.AvatarURL}, nil
}

func (s *AvatarService) UploadAvatar(ctx context.Context, userID int64, originalName string, source io.Reader) (UploadResult, error) {
	if userID <= 0 || source == nil {
		return UploadResult{}, apperror.New(apperror.CodeInvalidParam)
	}
	user, err := s.users.GetUserByID(ctx, userID)
	if err != nil {
		return UploadResult{}, apperror.Wrap(apperror.CodeInternalFailure, err)
	}
	if user == nil {
		return UploadResult{}, apperror.New(apperror.CodeUserNotFound)
	}
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(filepath.Base(originalName)), "."))
	if _, ok := s.allowedExts[ext]; !ok {
		return UploadResult{}, apperror.WithMessage(apperror.CodeInvalidParam, "unsupported avatar file extension")
	}

	// 先读取最多 512 字节做内容嗅探，后续直接流式写临时文件，避免把 50 MiB 全放进内存。
	header := make([]byte, 512)
	headerSize, readErr := io.ReadFull(source, header)
	if readErr != nil && !errors.Is(readErr, io.EOF) && !errors.Is(readErr, io.ErrUnexpectedEOF) {
		return UploadResult{}, apperror.Wrap(apperror.CodeInternalFailure, readErr)
	}
	if headerSize == 0 {
		return UploadResult{}, apperror.WithMessage(apperror.CodeInvalidParam, "avatar file is empty")
	}
	detected := strings.TrimSpace(strings.Split(http.DetectContentType(header[:headerSize]), ";")[0])
	if detected != avatarMIMEs[ext] {
		return UploadResult{}, apperror.WithMessage(apperror.CodeInvalidParam, "avatar content does not match its extension")
	}

	randomName, err := secureFilename(ext)
	if err != nil {
		return UploadResult{}, apperror.Wrap(apperror.CodeInternalFailure, err)
	}
	avatarDir, err := safeJoin(s.uploadRoot, "avatars")
	if err != nil {
		return UploadResult{}, apperror.Wrap(apperror.CodeInternalFailure, err)
	}
	if err := os.MkdirAll(avatarDir, 0o755); err != nil {
		return UploadResult{}, apperror.Wrap(apperror.CodeInternalFailure, err)
	}
	finalPath, err := safeJoin(avatarDir, randomName)
	if err != nil {
		return UploadResult{}, apperror.Wrap(apperror.CodeInternalFailure, err)
	}

	// 临时文件与最终文件在同一目录，Rename 在同一文件系统内是原子的。
	temporary, err := os.CreateTemp(avatarDir, ".avatar-*.tmp")
	if err != nil {
		return UploadResult{}, apperror.Wrap(apperror.CodeInternalFailure, err)
	}
	temporaryPath := temporary.Name()
	committed := false
	defer func() {
		_ = temporary.Close()
		if !committed {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o644); err != nil {
		return UploadResult{}, apperror.Wrap(apperror.CodeInternalFailure, err)
	}
	written, err := temporary.Write(header[:headerSize])
	if err != nil {
		return UploadResult{}, apperror.Wrap(apperror.CodeInternalFailure, err)
	}
	copied, err := io.Copy(temporary, io.LimitReader(source, s.maxSize-int64(headerSize)+1))
	if err != nil {
		return UploadResult{}, apperror.Wrap(apperror.CodeInternalFailure, err)
	}
	totalSize := int64(written) + copied
	if totalSize > s.maxSize {
		return UploadResult{}, apperror.WithMessage(apperror.CodeInvalidParam, "avatar file is too large")
	}
	if err := temporary.Sync(); err != nil {
		return UploadResult{}, apperror.Wrap(apperror.CodeInternalFailure, err)
	}
	if err := temporary.Close(); err != nil {
		return UploadResult{}, apperror.Wrap(apperror.CodeInternalFailure, err)
	}
	if err := os.Rename(temporaryPath, finalPath); err != nil {
		return UploadResult{}, apperror.Wrap(apperror.CodeInternalFailure, err)
	}
	committed = true

	url := "/uploads/avatars/" + randomName
	if err := s.users.UpdateAvatarURL(ctx, userID, url); err != nil {
		_ = os.Remove(finalPath)
		if errors.Is(err, repository.ErrNotFound) {
			return UploadResult{}, apperror.New(apperror.CodeUserNotFound)
		}
		return UploadResult{}, apperror.Wrap(apperror.CodeInternalFailure, err)
	}
	return UploadResult{URL: url, FilePath: filepath.ToSlash(filepath.Join("avatars", randomName)), Size: totalSize}, nil
}

func secureFilename(ext string) (string, error) {
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(random[:]) + "." + ext, nil
}

func safeJoin(root, child string) (string, error) {
	joined := filepath.Join(root, child)
	relative, err := filepath.Rel(root, joined)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return "", errors.New("path escapes upload root")
	}
	return joined, nil
}
