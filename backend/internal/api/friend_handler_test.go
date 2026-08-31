package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	authtoken "my-im/internal/auth"
	"my-im/internal/model"
	"my-im/internal/service"
)

type friendServiceStub struct {
	send         func(context.Context, int64, int64, string) (*model.FriendRequest, error)
	listRequests func(context.Context, int64, int, int) (service.Page[model.FriendRequest], error)
	accept       func(context.Context, int64, int64) (service.AcceptFriendResult, error)
	reject       func(context.Context, int64, int64) error
	listFriends  func(context.Context, int64, int, int) (service.Page[model.Friendship], error)
	deleteFriend func(context.Context, int64, int64) error
	block        func(context.Context, int64, int64) error
	unblock      func(context.Context, int64, int64) error
}

func (s friendServiceStub) SendRequest(ctx context.Context, from, to int64, message string) (*model.FriendRequest, error) {
	return s.send(ctx, from, to, message)
}
func (s friendServiceStub) ListRequests(ctx context.Context, id int64, limit, offset int) (service.Page[model.FriendRequest], error) {
	return s.listRequests(ctx, id, limit, offset)
}
func (s friendServiceStub) AcceptRequest(ctx context.Context, id, requestID int64) (service.AcceptFriendResult, error) {
	return s.accept(ctx, id, requestID)
}
func (s friendServiceStub) RejectRequest(ctx context.Context, id, requestID int64) error {
	return s.reject(ctx, id, requestID)
}
func (s friendServiceStub) ListFriends(ctx context.Context, id int64, limit, offset int) (service.Page[model.Friendship], error) {
	return s.listFriends(ctx, id, limit, offset)
}
func (s friendServiceStub) DeleteFriend(ctx context.Context, id, friendID int64) error {
	return s.deleteFriend(ctx, id, friendID)
}
func (s friendServiceStub) Block(ctx context.Context, id, blockedID int64) error {
	return s.block(ctx, id, blockedID)
}
func (s friendServiceStub) Unblock(ctx context.Context, id, blockedID int64) error {
	return s.unblock(ctx, id, blockedID)
}

func TestFriendSendMapsDatabaseIDToRequestID(t *testing.T) {
	stub := completeFriendStub()
	stub.send = func(_ context.Context, from, to int64, message string) (*model.FriendRequest, error) {
		if from != 7 || to != 9 || message != "你好" {
			t.Fatalf("unexpected command: from=%d to=%d message=%q", from, to, message)
		}
		return &model.FriendRequest{ID: 42, FromUserID: from, ToUserID: to, Status: 0}, nil
	}
	router, token := friendTestRouter(t, stub)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/friend/request", strings.NewReader(`{"to_user_id":9,"message":"你好"}`))
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Code int                `json:"code"`
		Data SendFriendResponse `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Code != 0 || response.Data.RequestID != 42 || response.Data.FromUserID != 7 || response.Data.ToUserID != 9 {
		t.Fatalf("unexpected response: %+v", response)
	}
}

func TestFriendListReturnsFrozenPaginationEnvelope(t *testing.T) {
	stub := completeFriendStub()
	stub.listFriends = func(_ context.Context, userID int64, limit, offset int) (service.Page[model.Friendship], error) {
		if userID != 7 || limit != 2 || offset != 1 {
			t.Fatalf("unexpected page command: user=%d limit=%d offset=%d", userID, limit, offset)
		}
		return service.Page[model.Friendship]{
			Items: []model.Friendship{{ID: 1, UserID: 7, FriendID: 9}}, Total: 4, Limit: limit, Offset: offset,
		}, nil
	}
	router, token := friendTestRouter(t, stub)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/friend/list?limit=2&offset=1", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Data PageResponse[model.Friendship] `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Data.Items) != 1 || response.Data.Pagination.Total != 4 || !response.Data.Pagination.HasMore {
		t.Fatalf("unexpected page: %+v", response.Data)
	}
}

func TestFriendPaginationRejectsInvalidBounds(t *testing.T) {
	router, token := friendTestRouter(t, completeFriendStub())
	for _, query := range []string{"limit=0", "limit=101", "limit=x", "offset=-1"} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/api/v1/friend/list?"+query, nil)
		request.Header.Set("Authorization", "Bearer "+token)
		router.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusBadRequest {
			t.Errorf("query=%s status=%d body=%s", query, recorder.Code, recorder.Body.String())
		}
	}
}

func friendTestRouter(t *testing.T, friends service.FriendService) (*gin.Engine, string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	manager, err := authtoken.NewManager("0123456789abcdef0123456789abcdef", "friend-handler-test", time.Hour, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	pair, err := manager.IssuePair(7, "alice", "")
	if err != nil {
		t.Fatal(err)
	}
	return NewRouter(RouterOptions{ServiceName: "friend-handler-test", TokenVerifier: manager, Friend: friends}), pair.AccessToken
}

func completeFriendStub() friendServiceStub {
	return friendServiceStub{
		send: func(context.Context, int64, int64, string) (*model.FriendRequest, error) {
			return &model.FriendRequest{}, nil
		},
		listRequests: func(context.Context, int64, int, int) (service.Page[model.FriendRequest], error) {
			return service.Page[model.FriendRequest]{}, nil
		},
		accept: func(context.Context, int64, int64) (service.AcceptFriendResult, error) {
			return service.AcceptFriendResult{}, nil
		},
		reject: func(context.Context, int64, int64) error { return nil },
		listFriends: func(context.Context, int64, int, int) (service.Page[model.Friendship], error) {
			return service.Page[model.Friendship]{}, nil
		},
		deleteFriend: func(context.Context, int64, int64) error { return nil },
		block:        func(context.Context, int64, int64) error { return nil },
		unblock:      func(context.Context, int64, int64) error { return nil },
	}
}
