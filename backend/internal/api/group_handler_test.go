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

	"my-im/internal/apperror"
	authtoken "my-im/internal/auth"
	"my-im/internal/model"
	"my-im/internal/service"
)

type groupServiceStub struct {
	create       func(context.Context, int64, string, string) (int64, error)
	listByUser   func(context.Context, int64) ([]model.Group, error)
	get          func(context.Context, int64, int64) (*model.Group, error)
	update       func(context.Context, int64, int64, string, string) error
	addMember    func(context.Context, int64, int64, int64) error
	removeMember func(context.Context, int64, int64, int64) error
	listMembers  func(context.Context, int64, int64, int, int) (service.Page[service.GroupMemberListItem], error)
	updateRole   func(context.Context, int64, int64, int64, int) error
	muteMember   func(context.Context, int64, int64, int64, *time.Time) error
	transfer     func(context.Context, int64, int64, int64) error
	leave        func(context.Context, int64, int64) error
	disband      func(context.Context, int64, int64) error
}

func (s groupServiceStub) Create(ctx context.Context, ownerID int64, name, notice string) (int64, error) {
	return s.create(ctx, ownerID, name, notice)
}

func (s groupServiceStub) ListByUser(ctx context.Context, userID int64) ([]model.Group, error) {
	return s.listByUser(ctx, userID)
}

func (s groupServiceStub) Get(ctx context.Context, userID, groupID int64) (*model.Group, error) {
	return s.get(ctx, userID, groupID)
}

func (s groupServiceStub) Update(ctx context.Context, userID, groupID int64, name, notice string) error {
	return s.update(ctx, userID, groupID, name, notice)
}

func (s groupServiceStub) AddMember(ctx context.Context, groupID, operatorID, memberID int64) error {
	return s.addMember(ctx, groupID, operatorID, memberID)
}

func (s groupServiceStub) RemoveMember(ctx context.Context, groupID, operatorID, memberID int64) error {
	return s.removeMember(ctx, groupID, operatorID, memberID)
}

func (s groupServiceStub) ListMembers(ctx context.Context, groupID, viewerID int64, limit, offset int) (service.Page[service.GroupMemberListItem], error) {
	return s.listMembers(ctx, groupID, viewerID, limit, offset)
}

func (s groupServiceStub) UpdateRole(ctx context.Context, groupID, operatorID, memberID int64, role int) error {
	return s.updateRole(ctx, groupID, operatorID, memberID, role)
}

func (s groupServiceStub) MuteMember(ctx context.Context, groupID, operatorID, memberID int64, mutedUntil *time.Time) error {
	return s.muteMember(ctx, groupID, operatorID, memberID, mutedUntil)
}

func (s groupServiceStub) TransferOwnership(ctx context.Context, groupID, operatorID, newOwnerID int64) error {
	return s.transfer(ctx, groupID, operatorID, newOwnerID)
}

func (s groupServiceStub) Leave(ctx context.Context, groupID, userID int64) error {
	return s.leave(ctx, groupID, userID)
}

func (s groupServiceStub) Disband(ctx context.Context, groupID, operatorID int64) error {
	return s.disband(ctx, groupID, operatorID)
}

func TestGroupCreateReturnsCreatedGroupID(t *testing.T) {
	stub := completeGroupStub()
	stub.create = func(_ context.Context, ownerID int64, name, notice string) (int64, error) {
		if ownerID != 7 || name != "Project Team" || notice != "Weekly sync" {
			t.Fatalf("unexpected create command: owner=%d name=%q notice=%q", ownerID, name, notice)
		}
		return 42, nil
	}
	router, token := groupTestRouter(t, stub)

	recorder := serveGroupRequest(router, token, http.MethodPost, "/api/v1/group",
		`{"name":"Project Team","notice":"Weekly sync"}`)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Code    int                 `json:"code"`
		Message string              `json:"message"`
		Data    CreateGroupResponse `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Code != CodeSuccess || response.Message != "ok" || response.Data.GroupID != 42 {
		t.Fatalf("unexpected create response: %+v", response)
	}
}

func TestGroupListReturnsEmptyJSONArray(t *testing.T) {
	stub := completeGroupStub()
	stub.listByUser = func(_ context.Context, userID int64) ([]model.Group, error) {
		if userID != 7 {
			t.Fatalf("unexpected list user: %d", userID)
		}
		return make([]model.Group, 0), nil
	}
	router, token := groupTestRouter(t, stub)

	recorder := serveGroupRequest(router, token, http.MethodGet, "/api/v1/group/list", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Code int             `json:"code"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Code != CodeSuccess || string(response.Data) != "[]" {
		t.Fatalf("empty group list must be data=[]; response=%s", recorder.Body.String())
	}
}

func TestGroupGetReturnsDetails(t *testing.T) {
	createdAt := time.Date(2026, 8, 31, 9, 30, 0, 0, time.UTC)
	updatedAt := createdAt.Add(time.Hour)
	want := &model.Group{
		ID: 9, Name: "Project Team", Notice: "Weekly sync", OwnerID: 7,
		MaxMembers: 500, CreatedAt: createdAt, UpdatedAt: updatedAt,
	}
	stub := completeGroupStub()
	stub.get = func(_ context.Context, userID, groupID int64) (*model.Group, error) {
		if userID != 7 || groupID != 9 {
			t.Fatalf("unexpected get command: user=%d group=%d", userID, groupID)
		}
		return want, nil
	}
	router, token := groupTestRouter(t, stub)

	recorder := serveGroupRequest(router, token, http.MethodGet, "/api/v1/group/9", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Code int         `json:"code"`
		Data model.Group `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Code != CodeSuccess || response.Data.ID != want.ID || response.Data.Name != want.Name ||
		response.Data.Notice != want.Notice || response.Data.OwnerID != want.OwnerID ||
		response.Data.MaxMembers != want.MaxMembers || !response.Data.CreatedAt.Equal(createdAt) ||
		!response.Data.UpdatedAt.Equal(updatedAt) {
		t.Fatalf("unexpected group details: %+v", response.Data)
	}
}

func TestGroupUpdatePassesProfileAndReturnsSuccess(t *testing.T) {
	stub := completeGroupStub()
	stub.update = func(_ context.Context, userID, groupID int64, name, notice string) error {
		if userID != 7 || groupID != 9 || name != "New Name" || notice != "New notice" {
			t.Fatalf("unexpected update command: user=%d group=%d name=%q notice=%q", userID, groupID, name, notice)
		}
		return nil
	}
	router, token := groupTestRouter(t, stub)

	recorder := serveGroupRequest(router, token, http.MethodPut, "/api/v1/group/9",
		`{"name":"New Name","notice":"New notice"}`)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response map[string]json.RawMessage
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	var code int
	if err := json.Unmarshal(response["code"], &code); err != nil || code != CodeSuccess {
		t.Fatalf("unexpected update response: %s", recorder.Body.String())
	}
	if _, exists := response["data"]; exists {
		t.Fatalf("void update response must omit data: %s", recorder.Body.String())
	}
}

func TestGroupMemberHandlersPassAuthenticatedActorAndReturnContracts(t *testing.T) {
	t.Run("add member", func(t *testing.T) {
		stub := completeGroupStub()
		stub.addMember = func(_ context.Context, groupID, operatorID, memberID int64) error {
			if groupID != 9 || operatorID != 7 || memberID != 8 {
				t.Fatalf("unexpected add command: group=%d operator=%d member=%d", groupID, operatorID, memberID)
			}
			return nil
		}
		router, token := groupTestRouter(t, stub)
		recorder := serveGroupRequest(router, token, http.MethodPost, "/api/v1/group/9/member", `{"member_id":8}`)
		assertVoidGroupSuccess(t, recorder)
	})

	t.Run("remove member", func(t *testing.T) {
		stub := completeGroupStub()
		stub.removeMember = func(_ context.Context, groupID, operatorID, memberID int64) error {
			if groupID != 9 || operatorID != 7 || memberID != 8 {
				t.Fatalf("unexpected remove command: group=%d operator=%d member=%d", groupID, operatorID, memberID)
			}
			return nil
		}
		router, token := groupTestRouter(t, stub)
		recorder := serveGroupRequest(router, token, http.MethodDelete, "/api/v1/group/9/member/8", "")
		assertVoidGroupSuccess(t, recorder)
	})

	t.Run("member page", func(t *testing.T) {
		joinedAt := time.Date(2026, 8, 31, 10, 0, 0, 0, time.UTC)
		stub := completeGroupStub()
		stub.listMembers = func(_ context.Context, groupID, viewerID int64, limit, offset int) (service.Page[service.GroupMemberListItem], error) {
			if groupID != 9 || viewerID != 7 || limit != 2 || offset != 1 {
				t.Fatalf("unexpected list command: group=%d viewer=%d limit=%d offset=%d", groupID, viewerID, limit, offset)
			}
			return service.Page[service.GroupMemberListItem]{
				Items: []service.GroupMemberListItem{{
					GroupMember: model.GroupMember{ID: 11, GroupID: 9, UserID: 8, Role: model.GroupRoleMember, JoinedAt: joinedAt},
					Username:    "bob", AvatarURL: "/bob.png",
				}},
				Total: 4, Limit: limit, Offset: offset,
			}, nil
		}
		router, token := groupTestRouter(t, stub)
		recorder := serveGroupRequest(router, token, http.MethodGet, "/api/v1/group/9/members?limit=2&offset=1", "")
		if recorder.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
		}
		var response struct {
			Code int                                       `json:"code"`
			Data PageResponse[service.GroupMemberListItem] `json:"data"`
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		if response.Code != CodeSuccess || len(response.Data.Items) != 1 || response.Data.Items[0].UserID != 8 ||
			response.Data.Items[0].Username != "bob" || response.Data.Pagination.Total != 4 ||
			response.Data.Pagination.Limit != 2 || response.Data.Pagination.Offset != 1 || !response.Data.Pagination.HasMore {
			t.Fatalf("unexpected member page response: %+v", response)
		}
	})
}

func TestGroupHandlerRejectsInvalidIDsAndJSONBeforeService(t *testing.T) {
	stub := completeGroupStub()
	stub.create = func(context.Context, int64, string, string) (int64, error) {
		t.Fatal("create service must not run for invalid JSON")
		return 0, nil
	}
	stub.get = func(context.Context, int64, int64) (*model.Group, error) {
		t.Fatal("get service must not run for an invalid group ID")
		return nil, nil
	}
	stub.update = func(context.Context, int64, int64, string, string) error {
		t.Fatal("update service must not run for an invalid group ID or JSON")
		return nil
	}
	stub.addMember = func(context.Context, int64, int64, int64) error {
		t.Fatal("add service must not run for invalid IDs or JSON")
		return nil
	}
	stub.removeMember = func(context.Context, int64, int64, int64) error {
		t.Fatal("remove service must not run for invalid IDs")
		return nil
	}
	stub.listMembers = func(context.Context, int64, int64, int, int) (service.Page[service.GroupMemberListItem], error) {
		t.Fatal("list members service must not run for invalid IDs or pagination")
		return service.Page[service.GroupMemberListItem]{}, nil
	}
	router, token := groupTestRouter(t, stub)

	tests := []struct {
		name    string
		method  string
		path    string
		body    string
		message string
	}{
		{name: "get non-number ID", method: http.MethodGet, path: "/api/v1/group/not-a-number"},
		{name: "get zero ID", method: http.MethodGet, path: "/api/v1/group/0"},
		{name: "get negative ID", method: http.MethodGet, path: "/api/v1/group/-1"},
		{name: "get overflowing ID", method: http.MethodGet, path: "/api/v1/group/9223372036854775808"},
		{name: "update invalid ID", method: http.MethodPut, path: "/api/v1/group/nope", body: `{"name":"valid","notice":""}`},
		{name: "create malformed JSON", method: http.MethodPost, path: "/api/v1/group", body: `{"name":`},
		{name: "create missing name", method: http.MethodPost, path: "/api/v1/group", body: `{"notice":"hello"}`},
		{name: "update malformed JSON", method: http.MethodPut, path: "/api/v1/group/9", body: `{"name":`},
		{name: "update missing name", method: http.MethodPut, path: "/api/v1/group/9", body: `{"notice":"hello"}`},
		{name: "add invalid group ID", method: http.MethodPost, path: "/api/v1/group/nope/member", body: `{"member_id":8}`},
		{name: "add malformed JSON", method: http.MethodPost, path: "/api/v1/group/9/member", body: `{"member_id":`},
		{name: "add missing member", method: http.MethodPost, path: "/api/v1/group/9/member", body: `{}`},
		{name: "add zero member", method: http.MethodPost, path: "/api/v1/group/9/member", body: `{"member_id":0}`},
		{name: "remove non-number member", method: http.MethodDelete, path: "/api/v1/group/9/member/nope"},
		{name: "remove zero member", method: http.MethodDelete, path: "/api/v1/group/9/member/0"},
		{name: "members invalid limit", method: http.MethodGet, path: "/api/v1/group/9/members?limit=101", message: "limit must be between 1 and 100"},
		{name: "members zero limit", method: http.MethodGet, path: "/api/v1/group/9/members?limit=0", message: "limit must be between 1 and 100"},
		{name: "members negative offset", method: http.MethodGet, path: "/api/v1/group/9/members?offset=-1", message: "offset must be a non-negative integer"},
		{name: "members non-number offset", method: http.MethodGet, path: "/api/v1/group/9/members?offset=nope", message: "offset must be a non-negative integer"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := serveGroupRequest(router, token, test.method, test.path, test.body)
			if test.message != "" {
				assertGroupHandlerErrorMessage(t, recorder, http.StatusBadRequest, apperror.CodeInvalidParam, test.message)
				return
			}
			assertGroupHandlerError(t, recorder, http.StatusBadRequest, apperror.CodeInvalidParam)
		})
	}
}

func TestGroupHandlerMapsServiceBusinessErrors(t *testing.T) {
	tests := []struct {
		name      string
		method    string
		path      string
		body      string
		code      apperror.Code
		configure func(*groupServiceStub, error)
	}{
		{
			name: "creator no longer exists", method: http.MethodPost, path: "/api/v1/group",
			body: `{"name":"Project Team","notice":""}`, code: apperror.CodeUserNotFound,
			configure: func(stub *groupServiceStub, err error) {
				stub.create = func(context.Context, int64, string, string) (int64, error) { return 0, err }
			},
		},
		{
			name: "list storage failure", method: http.MethodGet, path: "/api/v1/group/list",
			code: apperror.CodeInternalFailure,
			configure: func(stub *groupServiceStub, err error) {
				stub.listByUser = func(context.Context, int64) ([]model.Group, error) { return nil, err }
			},
		},
		{
			name: "group not found", method: http.MethodGet, path: "/api/v1/group/99",
			code: apperror.CodeGroupNotFound,
			configure: func(stub *groupServiceStub, err error) {
				stub.get = func(context.Context, int64, int64) (*model.Group, error) { return nil, err }
			},
		},
		{
			name: "not a group member", method: http.MethodGet, path: "/api/v1/group/9",
			code: apperror.CodeGroupNotMember,
			configure: func(stub *groupServiceStub, err error) {
				stub.get = func(context.Context, int64, int64) (*model.Group, error) { return nil, err }
			},
		},
		{
			name: "update permission denied", method: http.MethodPut, path: "/api/v1/group/9",
			body: `{"name":"New Name","notice":""}`, code: apperror.CodeNotOwnerOrAdmin,
			configure: func(stub *groupServiceStub, err error) {
				stub.update = func(context.Context, int64, int64, string, string) error { return err }
			},
		},
		{
			name: "invitee is not friend", method: http.MethodPost, path: "/api/v1/group/9/member",
			body: `{"member_id":8}`, code: apperror.CodeMemberNotFriend,
			configure: func(stub *groupServiceStub, err error) {
				stub.addMember = func(context.Context, int64, int64, int64) error { return err }
			},
		},
		{
			name: "administrator cannot remove peer", method: http.MethodDelete, path: "/api/v1/group/9/member/8",
			code: apperror.CodeCannotRemovePeer,
			configure: func(stub *groupServiceStub, err error) {
				stub.removeMember = func(context.Context, int64, int64, int64) error { return err }
			},
		},
		{
			name: "outsider cannot list members", method: http.MethodGet, path: "/api/v1/group/9/members?limit=20&offset=0",
			code: apperror.CodeGroupNotMember,
			configure: func(stub *groupServiceStub, err error) {
				stub.listMembers = func(context.Context, int64, int64, int, int) (service.Page[service.GroupMemberListItem], error) {
					return service.Page[service.GroupMemberListItem]{}, err
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stub := completeGroupStub()
			appErr := apperror.New(test.code)
			test.configure(&stub, appErr)
			router, token := groupTestRouter(t, stub)
			recorder := serveGroupRequest(router, token, test.method, test.path, test.body)
			assertGroupHandlerError(t, recorder, appErr.HTTPStatus, test.code)
		})
	}
}

func groupTestRouter(t *testing.T, groups service.GroupCoreService) (*gin.Engine, string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	manager, err := authtoken.NewManager(
		"0123456789abcdef0123456789abcdef", "group-handler-test", time.Hour, 24*time.Hour,
	)
	if err != nil {
		t.Fatal(err)
	}
	pair, err := manager.IssuePair(7, "alice", "")
	if err != nil {
		t.Fatal(err)
	}
	return NewRouter(RouterOptions{
		ServiceName: "group-handler-test", TokenVerifier: manager, Group: groups,
	}), pair.AccessToken
}

func serveGroupRequest(router http.Handler, token, method, path, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+token)
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder
}

func assertGroupHandlerError(t *testing.T, recorder *httptest.ResponseRecorder, status int, code apperror.Code) {
	t.Helper()
	if recorder.Code != status {
		t.Fatalf("status=%d body=%s; want status=%d", recorder.Code, recorder.Body.String(), status)
	}
	var response struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	definition, ok := apperror.DefinitionFor(code)
	if !ok {
		t.Fatalf("missing error definition for code %d", code)
	}
	if response.Code != int(code) || response.Message != definition.Message {
		t.Fatalf("unexpected error response: %+v; want code=%d message=%q", response, code, definition.Message)
	}
}

func assertGroupHandlerErrorMessage(t *testing.T, recorder *httptest.ResponseRecorder, status int, code apperror.Code, message string) {
	t.Helper()
	if recorder.Code != status {
		t.Fatalf("status=%d body=%s; want status=%d", recorder.Code, recorder.Body.String(), status)
	}
	var response struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Code != int(code) || response.Message != message {
		t.Fatalf("unexpected error response: %+v; want code=%d message=%q", response, code, message)
	}
}

func assertVoidGroupSuccess(t *testing.T, recorder *httptest.ResponseRecorder) {
	t.Helper()
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response map[string]json.RawMessage
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	var code int
	if err := json.Unmarshal(response["code"], &code); err != nil || code != CodeSuccess {
		t.Fatalf("unexpected response: %s", recorder.Body.String())
	}
	if _, exists := response["data"]; exists {
		t.Fatalf("void success must omit data: %s", recorder.Body.String())
	}
}

func completeGroupStub() groupServiceStub {
	return groupServiceStub{
		create: func(context.Context, int64, string, string) (int64, error) { return 1, nil },
		listByUser: func(context.Context, int64) ([]model.Group, error) {
			return make([]model.Group, 0), nil
		},
		get: func(context.Context, int64, int64) (*model.Group, error) {
			return &model.Group{}, nil
		},
		update:       func(context.Context, int64, int64, string, string) error { return nil },
		addMember:    func(context.Context, int64, int64, int64) error { return nil },
		removeMember: func(context.Context, int64, int64, int64) error { return nil },
		listMembers: func(context.Context, int64, int64, int, int) (service.Page[service.GroupMemberListItem], error) {
			return service.Page[service.GroupMemberListItem]{Items: make([]service.GroupMemberListItem, 0)}, nil
		},
		updateRole: func(context.Context, int64, int64, int64, int) error { return nil },
		muteMember: func(context.Context, int64, int64, int64, *time.Time) error { return nil },
		transfer:   func(context.Context, int64, int64, int64) error { return nil },
		leave:      func(context.Context, int64, int64) error { return nil },
		disband:    func(context.Context, int64, int64) error { return nil },
	}
}

var _ service.GroupCoreService = groupServiceStub{}
