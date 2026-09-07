package api

import (
	"context"
	"net/http"
	"testing"
	"time"

	"my-im/internal/apperror"
)

func TestGroupManagementHandlersPassAuthenticatedActor(t *testing.T) {
	t.Run("update role accepts zero as an explicit role", func(t *testing.T) {
		stub := completeGroupStub()
		stub.updateRole = func(_ context.Context, groupID, operatorID, memberID int64, role int) error {
			if groupID != 9 || operatorID != 7 || memberID != 8 || role != 0 {
				t.Fatalf("unexpected role command: group=%d operator=%d member=%d role=%d", groupID, operatorID, memberID, role)
			}
			return nil
		}
		router, token := groupTestRouter(t, stub)
		assertVoidGroupSuccess(t, serveGroupRequest(router, token, http.MethodPut,
			"/api/v1/group/9/member/8/role", `{"role":0}`))
	})

	t.Run("mute parses an RFC3339 deadline", func(t *testing.T) {
		want := time.Date(2099, 1, 2, 3, 4, 5, 0, time.FixedZone("request", 8*60*60))
		stub := completeGroupStub()
		stub.muteMember = func(_ context.Context, groupID, operatorID, memberID int64, deadline *time.Time) error {
			if groupID != 9 || operatorID != 7 || memberID != 8 || deadline == nil || !deadline.Equal(want) {
				t.Fatalf("unexpected mute command: group=%d operator=%d member=%d deadline=%v", groupID, operatorID, memberID, deadline)
			}
			return nil
		}
		router, token := groupTestRouter(t, stub)
		assertVoidGroupSuccess(t, serveGroupRequest(router, token, http.MethodPut,
			"/api/v1/group/9/member/8/mute", `{"muted_until":"2099-01-02T03:04:05+08:00"}`))
	})

	t.Run("delete mute passes nil to unmute", func(t *testing.T) {
		stub := completeGroupStub()
		stub.muteMember = func(_ context.Context, groupID, operatorID, memberID int64, deadline *time.Time) error {
			if groupID != 9 || operatorID != 7 || memberID != 8 || deadline != nil {
				t.Fatalf("unexpected unmute command: group=%d operator=%d member=%d deadline=%v", groupID, operatorID, memberID, deadline)
			}
			return nil
		}
		router, token := groupTestRouter(t, stub)
		assertVoidGroupSuccess(t, serveGroupRequest(router, token, http.MethodDelete,
			"/api/v1/group/9/member/8/mute", ""))
	})
}

func TestGroupManagementHandlersRejectInvalidTransportInput(t *testing.T) {
	stub := completeGroupStub()
	stub.updateRole = func(context.Context, int64, int64, int64, int) error {
		t.Fatal("role service must not run for invalid transport input")
		return nil
	}
	stub.muteMember = func(context.Context, int64, int64, int64, *time.Time) error {
		t.Fatal("mute service must not run for invalid transport input")
		return nil
	}
	router, token := groupTestRouter(t, stub)

	for _, test := range []struct {
		name, method, path, body string
	}{
		{name: "role invalid group", method: http.MethodPut, path: "/api/v1/group/nope/member/8/role", body: `{"role":1}`},
		{name: "role invalid member", method: http.MethodPut, path: "/api/v1/group/9/member/nope/role", body: `{"role":1}`},
		{name: "role malformed JSON", method: http.MethodPut, path: "/api/v1/group/9/member/8/role", body: `{"role":`},
		{name: "role missing field", method: http.MethodPut, path: "/api/v1/group/9/member/8/role", body: `{}`},
		{name: "role null field", method: http.MethodPut, path: "/api/v1/group/9/member/8/role", body: `{"role":null}`},
		{name: "mute invalid group", method: http.MethodPut, path: "/api/v1/group/0/member/8/mute", body: `{"muted_until":"2099-01-01T00:00:00Z"}`},
		{name: "mute invalid member", method: http.MethodDelete, path: "/api/v1/group/9/member/0/mute"},
		{name: "mute malformed JSON", method: http.MethodPut, path: "/api/v1/group/9/member/8/mute", body: `{"muted_until":`},
		{name: "mute missing field", method: http.MethodPut, path: "/api/v1/group/9/member/8/mute", body: `{}`},
		{name: "mute null field", method: http.MethodPut, path: "/api/v1/group/9/member/8/mute", body: `{"muted_until":null}`},
		{name: "mute invalid RFC3339", method: http.MethodPut, path: "/api/v1/group/9/member/8/mute", body: `{"muted_until":"tomorrow"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			recorder := serveGroupRequest(router, token, test.method, test.path, test.body)
			assertGroupHandlerError(t, recorder, http.StatusBadRequest, apperror.CodeInvalidParam)
		})
	}
}

func TestGroupManagementHandlersMapBusinessErrors(t *testing.T) {
	tests := []struct {
		name, method, path, body string
		code                     apperror.Code
		configure                func(*groupServiceStub, error)
	}{
		{
			name: "invalid target role", method: http.MethodPut, path: "/api/v1/group/9/member/8/role", body: `{"role":2}`,
			code: apperror.CodeInvalidRole,
			configure: func(stub *groupServiceStub, err error) {
				stub.updateRole = func(context.Context, int64, int64, int64, int) error { return err }
			},
		},
		{
			name: "administrator cannot mute peer", method: http.MethodPut, path: "/api/v1/group/9/member/8/mute", body: `{"muted_until":"2099-01-01T00:00:00Z"}`,
			code: apperror.CodeNotOwnerOrAdmin,
			configure: func(stub *groupServiceStub, err error) {
				stub.muteMember = func(context.Context, int64, int64, int64, *time.Time) error { return err }
			},
		},
		{
			name: "unmute target is missing", method: http.MethodDelete, path: "/api/v1/group/9/member/8/mute",
			code: apperror.CodeGroupMemberNotFound,
			configure: func(stub *groupServiceStub, err error) {
				stub.muteMember = func(context.Context, int64, int64, int64, *time.Time) error { return err }
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
