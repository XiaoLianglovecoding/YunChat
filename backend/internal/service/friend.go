package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"my-im/internal/apperror"
	"my-im/internal/model"
	"my-im/internal/protocol"
	"my-im/internal/repository"
)

const (
	FriendEventApply    = "friendApply"
	FriendEventAccepted = "friendAccepted"

	defaultFriendPageSize = 20
	maxFriendPageSize     = 100
)

// FriendCacheWriter mirrors the four write-through operations used by the
// friend service. RedisRepoImpl satisfies this interface directly.
type FriendCacheWriter interface {
	SetFriendCache(context.Context, int64, int64) error
	DeleteFriendCache(context.Context, int64, int64) error
	SetBlacklistMember(context.Context, int64, int64) error
	DeleteBlacklistMember(context.Context, int64, int64) error
}

type FriendEventNotifier interface {
	NotifyFriendEvent(context.Context, int64, string, any) error
}

type PresenceReader interface {
	GetOnlineStates(context.Context, []int64) (map[int64]bool, error)
}

type FriendServiceOption func(*FriendServiceImpl) error

// WithFriendCache enables a best-effort immediate write after commit. Durable
// cache reconciliation is already enqueued through FriendRepository inside the
// MySQL transaction, so a Redis error here can safely be retried by the worker.
func WithFriendCache(cache FriendCacheWriter) FriendServiceOption {
	return func(service *FriendServiceImpl) error {
		if cache == nil {
			return errors.New("friend cache must not be nil")
		}
		service.cache = cache
		return nil
	}
}

func WithFriendEventNotifier(notifier FriendEventNotifier) FriendServiceOption {
	return func(service *FriendServiceImpl) error {
		if notifier == nil {
			return errors.New("friend event notifier must not be nil")
		}
		service.notifier = notifier
		return nil
	}
}

func WithPresenceReader(presence PresenceReader) FriendServiceOption {
	return func(service *FriendServiceImpl) error {
		if presence == nil {
			return errors.New("presence reader must not be nil")
		}
		service.presence = presence
		return nil
	}
}

type FriendServiceImpl struct {
	repository repository.FriendRepository
	cache      FriendCacheWriter
	notifier   FriendEventNotifier
	presence   PresenceReader
	now        func() time.Time
}

func NewFriendService(friendRepository repository.FriendRepository, options ...FriendServiceOption) (*FriendServiceImpl, error) {
	if friendRepository == nil {
		return nil, errors.New("friend repository must not be nil")
	}
	service := &FriendServiceImpl{repository: friendRepository, now: time.Now}
	for _, option := range options {
		if option == nil {
			continue
		}
		if err := option(service); err != nil {
			return nil, err
		}
	}
	return service, nil
}

func (s *FriendServiceImpl) SendRequest(ctx context.Context, fromUserID, toUserID int64, rawMessage string) (*model.FriendRequest, error) {
	if fromUserID <= 0 || toUserID <= 0 {
		return nil, apperror.New(apperror.CodeInvalidParam)
	}
	if fromUserID == toUserID {
		return nil, apperror.New(apperror.CodeSelfRequest)
	}
	message, err := normalizeFriendMessage(rawMessage)
	if err != nil {
		return nil, err
	}
	now := s.now()
	request := &model.FriendRequest{
		FromUserID: fromUserID,
		ToUserID:   toUserID,
		Message:    message,
		Status:     model.FriendRequestPending,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	err = s.repository.WithinFriendTransaction(ctx, func(txCtx context.Context, tx repository.FriendRepository) error {
		locked, err := tx.LockFriendUsers(txCtx, fromUserID, toUserID)
		if err != nil {
			return err
		}
		if locked != 2 {
			return apperror.New(apperror.CodeUserNotFound)
		}
		blocked, err := tx.IsEitherBlocked(txCtx, fromUserID, toUserID)
		if err != nil {
			return err
		}
		if blocked {
			return apperror.New(apperror.CodeFriendBlocked)
		}
		friends, err := tx.IsFriendPair(txCtx, fromUserID, toUserID)
		if err != nil {
			return err
		}
		if friends {
			return apperror.New(apperror.CodeAlreadyFriends)
		}
		pending, err := tx.FindPendingFriendRequest(txCtx, fromUserID, toUserID)
		if err != nil {
			return err
		}
		if pending != nil {
			return apperror.New(apperror.CodeDuplicateRequest)
		}
		// SaveFriendRequest replaces an old terminal request with a new row and
		// a new ID. The pair lock makes the checks and replacement atomic, while
		// the new ID prevents a delayed action on an old request from affecting
		// this new application generation.
		if err := tx.SaveFriendRequest(txCtx, request); err != nil {
			return err
		}
		profile, err := tx.GetFriendUserProfile(txCtx, fromUserID)
		if err != nil {
			return err
		}
		if profile == nil {
			return apperror.New(apperror.CodeUserNotFound)
		}
		request.Username = profile.Username
		request.AvatarURL = profile.AvatarURL
		return nil
	})
	if err != nil {
		if errors.Is(err, repository.ErrConflict) {
			return nil, apperror.New(apperror.CodeDuplicateRequest)
		}
		return nil, friendServiceError(err)
	}
	s.notify(context.WithoutCancel(ctx), toUserID, FriendEventApply, protocol.FriendApplyPayload{
		RequestID: request.ID, FromUserID: request.FromUserID, Username: request.Username,
		AvatarURL: request.AvatarURL, Message: request.Message, CreatedAt: request.CreatedAt,
	})
	return request, nil
}

func (s *FriendServiceImpl) ListRequests(ctx context.Context, userID int64, limit, offset int) (Page[model.FriendRequest], error) {
	if userID <= 0 {
		return Page[model.FriendRequest]{}, apperror.New(apperror.CodeInvalidParam)
	}
	limit, offset, err := normalizeFriendPage(limit, offset)
	if err != nil {
		return Page[model.FriendRequest]{}, err
	}
	items, err := s.repository.ListIncomingFriendRequests(ctx, userID, limit, offset)
	if err != nil {
		return Page[model.FriendRequest]{}, friendServiceError(err)
	}
	total, err := s.repository.CountIncomingFriendRequests(ctx, userID)
	if err != nil {
		return Page[model.FriendRequest]{}, friendServiceError(err)
	}
	if items == nil {
		items = make([]model.FriendRequest, 0)
	}
	return Page[model.FriendRequest]{Items: items, Total: total, Limit: limit, Offset: offset}, nil
}

func (s *FriendServiceImpl) AcceptRequest(ctx context.Context, userID, requestID int64) (AcceptFriendResult, error) {
	if userID <= 0 || requestID <= 0 {
		return AcceptFriendResult{}, apperror.New(apperror.CodeInvalidParam)
	}
	preview, err := s.repository.GetFriendRequest(ctx, requestID)
	if err != nil {
		return AcceptFriendResult{}, friendServiceError(err)
	}
	if preview == nil {
		return AcceptFriendResult{}, apperror.New(apperror.CodeRequestNotFound)
	}
	if preview.ToUserID != userID {
		return AcceptFriendResult{}, apperror.New(apperror.CodeNotRequestTarget)
	}
	result := AcceptFriendResult{UserID: userID, FriendID: preview.FromUserID}
	newlyAccepted := false
	var acceptedProfile *model.User
	err = s.repository.WithinFriendTransaction(ctx, func(txCtx context.Context, tx repository.FriendRepository) error {
		locked, err := tx.LockFriendUsers(txCtx, preview.FromUserID, preview.ToUserID)
		if err != nil {
			return err
		}
		if locked != 2 {
			return apperror.New(apperror.CodeRequestNotFound)
		}
		request, err := tx.GetFriendRequestForUpdate(txCtx, requestID)
		if err != nil {
			return err
		}
		if request == nil {
			return apperror.New(apperror.CodeRequestNotFound)
		}
		if request.ToUserID != userID {
			return apperror.New(apperror.CodeNotRequestTarget)
		}
		result.FriendID = request.FromUserID
		switch request.Status {
		case model.FriendRequestAccepted:
			// A retry is successful and also repairs either missing direction.
			if err := tx.EnsureFriendshipPair(txCtx, request.FromUserID, request.ToUserID); err != nil {
				return err
			}
			return enqueueFriendPairReconcile(txCtx, tx, request.FromUserID, request.ToUserID)
		case model.FriendRequestRejected:
			// A rejected request is deliberately exposed as no longer actionable;
			// no new public error code is needed for this terminal state.
			return apperror.New(apperror.CodeRequestNotFound)
		case model.FriendRequestPending:
			blocked, err := tx.IsEitherBlocked(txCtx, request.FromUserID, request.ToUserID)
			if err != nil {
				return err
			}
			if blocked {
				return apperror.New(apperror.CodeFriendBlocked)
			}
			if err := tx.SetFriendRequestStatus(txCtx, request.ID, model.FriendRequestAccepted); err != nil {
				return err
			}
			if err := tx.EnsureFriendshipPair(txCtx, request.FromUserID, request.ToUserID); err != nil {
				return err
			}
			acceptedProfile, err = tx.GetFriendUserProfile(txCtx, request.ToUserID)
			if err != nil {
				return err
			}
			if acceptedProfile == nil {
				return apperror.New(apperror.CodeUserNotFound)
			}
			if err := enqueueFriendPairReconcile(txCtx, tx, request.FromUserID, request.ToUserID); err != nil {
				return err
			}
			newlyAccepted = true
			return nil
		default:
			return apperror.New(apperror.CodeRequestNotFound)
		}
	})
	if err != nil {
		return AcceptFriendResult{}, friendServiceError(err)
	}
	postCtx := context.WithoutCancel(ctx)
	s.writeCache(func() error { return s.cache.SetFriendCache(postCtx, result.UserID, result.FriendID) })
	if newlyAccepted {
		s.notify(postCtx, result.FriendID, FriendEventAccepted, protocol.FriendAcceptedPayload{
			RequestID: requestID, UserID: result.FriendID, FriendID: result.UserID,
			Username: acceptedProfile.Username, AvatarURL: acceptedProfile.AvatarURL,
		})
	}
	return result, nil
}

func (s *FriendServiceImpl) RejectRequest(ctx context.Context, userID, requestID int64) error {
	if userID <= 0 || requestID <= 0 {
		return apperror.New(apperror.CodeInvalidParam)
	}
	preview, err := s.repository.GetFriendRequest(ctx, requestID)
	if err != nil {
		return friendServiceError(err)
	}
	if preview == nil {
		return apperror.New(apperror.CodeRequestNotFound)
	}
	if preview.ToUserID != userID {
		return apperror.New(apperror.CodeNotRequestTarget)
	}
	err = s.repository.WithinFriendTransaction(ctx, func(txCtx context.Context, tx repository.FriendRepository) error {
		locked, err := tx.LockFriendUsers(txCtx, preview.FromUserID, preview.ToUserID)
		if err != nil {
			return err
		}
		if locked != 2 {
			return apperror.New(apperror.CodeRequestNotFound)
		}
		request, err := tx.GetFriendRequestForUpdate(txCtx, requestID)
		if err != nil {
			return err
		}
		if request == nil {
			return apperror.New(apperror.CodeRequestNotFound)
		}
		if request.ToUserID != userID {
			return apperror.New(apperror.CodeNotRequestTarget)
		}
		switch request.Status {
		case model.FriendRequestRejected:
			return nil // retry is idempotent
		case model.FriendRequestAccepted:
			return apperror.New(apperror.CodeRequestNotFound)
		case model.FriendRequestPending:
			return tx.SetFriendRequestStatus(txCtx, request.ID, model.FriendRequestRejected)
		default:
			return apperror.New(apperror.CodeRequestNotFound)
		}
	})
	return friendServiceError(err)
}

func (s *FriendServiceImpl) ListFriends(ctx context.Context, userID int64, limit, offset int) (Page[model.Friendship], error) {
	if userID <= 0 {
		return Page[model.Friendship]{}, apperror.New(apperror.CodeInvalidParam)
	}
	limit, offset, err := normalizeFriendPage(limit, offset)
	if err != nil {
		return Page[model.Friendship]{}, err
	}
	items, err := s.repository.ListFriendshipsPage(ctx, userID, limit, offset)
	if err != nil {
		return Page[model.Friendship]{}, friendServiceError(err)
	}
	total, err := s.repository.CountFriendships(ctx, userID)
	if err != nil {
		return Page[model.Friendship]{}, friendServiceError(err)
	}
	if items == nil {
		items = make([]model.Friendship, 0)
	}
	if s.presence != nil && len(items) > 0 {
		friendIDs := make([]int64, len(items))
		for index := range items {
			items[index].Online = false
			friendIDs[index] = items[index].FriendID
		}
		states, err := s.presence.GetOnlineStates(ctx, friendIDs)
		if err == nil {
			for index := range items {
				items[index].Online = states[items[index].FriendID]
			}
		}
		// Presence is an ephemeral Redis projection. On failure keep every
		// authoritative MySQL friendship and conservatively report offline.
	}
	return Page[model.Friendship]{Items: items, Total: total, Limit: limit, Offset: offset}, nil
}

func (s *FriendServiceImpl) DeleteFriend(ctx context.Context, userID, friendID int64) error {
	if userID <= 0 || friendID <= 0 {
		return apperror.New(apperror.CodeInvalidParam)
	}
	if userID == friendID {
		return apperror.New(apperror.CodeSelfRequest)
	}
	err := s.repository.WithinFriendTransaction(ctx, func(txCtx context.Context, tx repository.FriendRepository) error {
		locked, err := tx.LockFriendUsers(txCtx, userID, friendID)
		if err != nil {
			return err
		}
		if locked != 2 {
			return apperror.New(apperror.CodeUserNotFound)
		}
		if err := tx.DeleteFriendshipPair(txCtx, userID, friendID); err != nil {
			return err
		}
		if err := tx.InvalidateAcceptedFriendRequests(txCtx, userID, friendID); err != nil {
			return err
		}
		return enqueueFriendPairReconcile(txCtx, tx, userID, friendID)
	})
	if err != nil {
		return friendServiceError(err)
	}
	postCtx := context.WithoutCancel(ctx)
	s.writeCache(func() error { return s.cache.DeleteFriendCache(postCtx, userID, friendID) })
	return nil
}

func (s *FriendServiceImpl) Block(ctx context.Context, userID, blockedID int64) error {
	if userID <= 0 || blockedID <= 0 {
		return apperror.New(apperror.CodeInvalidParam)
	}
	if userID == blockedID {
		return apperror.New(apperror.CodeSelfRequest)
	}
	entry := &model.Blacklist{UserID: userID, BlockedID: blockedID, CreatedAt: s.now()}
	err := s.repository.WithinFriendTransaction(ctx, func(txCtx context.Context, tx repository.FriendRepository) error {
		locked, err := tx.LockFriendUsers(txCtx, userID, blockedID)
		if err != nil {
			return err
		}
		if locked != 2 {
			return apperror.New(apperror.CodeUserNotFound)
		}
		// Product policy: blocking does not delete the friendship. It only adds
		// a communication/request barrier, so unblocking can restore messaging.
		if err := tx.AddBlacklistEntry(txCtx, entry); err != nil {
			return err
		}
		if err := tx.RejectPendingFriendRequests(txCtx, userID, blockedID); err != nil {
			return err
		}
		return tx.EnqueueCacheReconcile(txCtx, repository.CacheResourceBlacklist, userID)
	})
	if err != nil {
		if errors.Is(err, repository.ErrConflict) {
			return apperror.New(apperror.CodeAlreadyBlocked)
		}
		return friendServiceError(err)
	}
	postCtx := context.WithoutCancel(ctx)
	s.writeCache(func() error { return s.cache.SetBlacklistMember(postCtx, userID, blockedID) })
	return nil
}

func (s *FriendServiceImpl) Unblock(ctx context.Context, userID, blockedID int64) error {
	if userID <= 0 || blockedID <= 0 {
		return apperror.New(apperror.CodeInvalidParam)
	}
	if userID == blockedID {
		return apperror.New(apperror.CodeSelfRequest)
	}
	err := s.repository.WithinFriendTransaction(ctx, func(txCtx context.Context, tx repository.FriendRepository) error {
		locked, err := tx.LockFriendUsers(txCtx, userID, blockedID)
		if err != nil {
			return err
		}
		if locked != 2 {
			return apperror.New(apperror.CodeUserNotFound)
		}
		// DELETE of a missing row succeeds, making unblock safe to retry.
		if err := tx.RemoveBlacklistEntry(txCtx, userID, blockedID); err != nil {
			return err
		}
		return tx.EnqueueCacheReconcile(txCtx, repository.CacheResourceBlacklist, userID)
	})
	if err != nil {
		return friendServiceError(err)
	}
	postCtx := context.WithoutCancel(ctx)
	s.writeCache(func() error { return s.cache.DeleteBlacklistMember(postCtx, userID, blockedID) })
	return nil
}

func (s *FriendServiceImpl) writeCache(write func() error) {
	if s.cache == nil {
		return
	}
	// The durable reconciliation command was committed with MySQL. Never
	// pretend the business transaction rolled back if this fast path fails.
	_ = write()
}

func enqueueFriendPairReconcile(ctx context.Context, tx repository.FriendRepository, userID, friendID int64) error {
	if err := tx.EnqueueCacheReconcile(ctx, repository.CacheResourceFriends, userID); err != nil {
		return err
	}
	return tx.EnqueueCacheReconcile(ctx, repository.CacheResourceFriends, friendID)
}

func (s *FriendServiceImpl) notify(ctx context.Context, userID int64, eventType string, payload any) {
	if s.notifier != nil {
		// Critical state lives in MySQL; WebSocket delivery is an acceleration.
		_ = s.notifier.NotifyFriendEvent(ctx, userID, eventType, payload)
	}
}

func normalizeFriendMessage(raw string) (string, error) {
	for _, character := range raw {
		if unicode.IsControl(character) {
			return "", apperror.WithMessage(apperror.CodeInvalidParam, "friend request message must not contain control characters")
		}
	}
	message := strings.TrimSpace(raw)
	if utf8.RuneCountInString(message) > 200 {
		return "", apperror.WithMessage(apperror.CodeInvalidParam, "friend request message must not exceed 200 characters")
	}
	return message, nil
}

func normalizeFriendPage(limit, offset int) (int, int, error) {
	if offset < 0 {
		return 0, 0, apperror.WithMessage(apperror.CodeInvalidParam, "offset must not be negative")
	}
	if limit <= 0 {
		limit = defaultFriendPageSize
	}
	if limit > maxFriendPageSize {
		return 0, 0, apperror.WithMessage(apperror.CodeInvalidParam, "limit must not exceed 100")
	}
	return limit, offset, nil
}

func friendServiceError(err error) error {
	if err == nil {
		return nil
	}
	var applicationError *apperror.Error
	if errors.As(err, &applicationError) {
		return applicationError
	}
	return apperror.Wrap(apperror.CodeInternalFailure, fmt.Errorf("friend service: %w", err))
}

var _ FriendService = (*FriendServiceImpl)(nil)
