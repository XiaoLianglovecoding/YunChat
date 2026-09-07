package api

import (
	"context"
	"net/http"
	"testing"

	"my-im/internal/apperror"
)

func TestGroupLifecycleHandlersPassAuthenticatedActor(t *testing.T) {
	t.Run("transfer passes group actor and target", func(t *testing.T) {
		stub := completeGroupStub()
		stub.transfer = func(_ context.Context, groupID, operatorID, newOwnerID int64) error {
			if groupID != 9 || operatorID != 7 || newOwnerID != 8 {
				t.Fatalf("unexpected transfer command: group=%d operator=%d target=%d", groupID, operatorID, newOwnerID)
			}
			return nil
		}
		router, token := groupTestRouter(t, stub)
		assertVoidGroupSuccess(t, serveGroupRequest(router, token, http.MethodPut,
			"/api/v1/group/9/owner", `{"new_owner_id":8}`))
	})

	t.Run("leave uses authenticated user rather than request data", func(t *testing.T) {
		stub := completeGroupStub()
		stub.leave = func(_ context.Context, groupID, userID int64) error {
			if groupID != 9 || userID != 7 {
				t.Fatalf("unexpected leave command: group=%d user=%d", groupID, userID)
			}
			return nil
		}
		router, token := groupTestRouter(t, stub)
		assertVoidGroupSuccess(t, serveGroupRequest(router, token, http.MethodPost,
			"/api/v1/group/9/leave", `{"user_id":999}`))
	})
}

func TestGroupLifecycleHandlersRejectInvalidTransportInput(t *testing.T) {
	stub := completeGroupStub()
	stub.transfer = func(context.Context, int64, int64, int64) error {
		t.Fatal("transfer service must not run for invalid transport input")
		return nil
	}
	stub.leave = func(context.Context, int64, int64) error {
		t.Fatal("leave service must not run for invalid transport input")
		return nil
	}
	router, token := groupTestRouter(t, stub)

	for _, test := range []struct {
		name, method, path, body string
	}{
		{name: "transfer invalid group", method: http.MethodPut, path: "/api/v1/group/nope/owner", body: `{"new_owner_id":8}`},
		{name: "transfer malformed json", method: http.MethodPut, path: "/api/v1/group/9/owner", body: `{"new_owner_id":`},
		{name: "transfer missing target", method: http.MethodPut, path: "/api/v1/group/9/owner", body: `{}`},
		{name: "transfer null target", method: http.MethodPut, path: "/api/v1/group/9/owner", body: `{"new_owner_id":null}`},
		{name: "transfer zero target", method: http.MethodPut, path: "/api/v1/group/9/owner", body: `{"new_owner_id":0}`},
		{name: "transfer negative target", method: http.MethodPut, path: "/api/v1/group/9/owner", body: `{"new_owner_id":-8}`},
		{name: "leave invalid group", method: http.MethodPost, path: "/api/v1/group/0/leave"},
	} {
		t.Run(test.name, func(t *testing.T) {
			recorder := serveGroupRequest(router, token, test.method, test.path, test.body)
			assertGroupHandlerError(t, recorder, http.StatusBadRequest, apperror.CodeInvalidParam)
		})
	}
}

func TestGroupLifecycleHandlersMapBusinessErrors(t *testing.T) {
	tests := []struct {
		name      string
		method    string
		path      string
		body      string
		code      apperror.Code
		configure func(*groupServiceStub, error)
	}{
		{
			name: "only real owner can transfer", method: http.MethodPut, path: "/api/v1/group/9/owner",
			body: `{"new_owner_id":8}`, code: apperror.CodeNotOwnerOrAdmin,
			configure: func(stub *groupServiceStub, err error) {
				stub.transfer = func(context.Context, int64, int64, int64) error { return err }
			},
		},
		{
			name: "transfer target is not a member", method: http.MethodPut, path: "/api/v1/group/9/owner",
			body: `{"new_owner_id":8}`, code: apperror.CodeGroupMemberNotFound,
			configure: func(stub *groupServiceStub, err error) {
				stub.transfer = func(context.Context, int64, int64, int64) error { return err }
			},
		},
		{
			name: "owner must transfer before leaving", method: http.MethodPost, path: "/api/v1/group/9/leave",
			code: apperror.CodeCannotLeaveAsOwner,
			configure: func(stub *groupServiceStub, err error) {
				stub.leave = func(context.Context, int64, int64) error { return err }
			},
		},
		{
			name: "outsider cannot leave", method: http.MethodPost, path: "/api/v1/group/9/leave",
			code: apperror.CodeGroupNotMember,
			configure: func(stub *groupServiceStub, err error) {
				stub.leave = func(context.Context, int64, int64) error { return err }
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
