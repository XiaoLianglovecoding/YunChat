package api

import (
	"context"
	"net/http"
	"testing"

	"my-im/internal/apperror"
)

func TestGroupDisbandHandlerUsesAuthenticatedOwner(t *testing.T) {
	t.Parallel()
	stub := completeGroupStub()
	stub.disband = func(_ context.Context, groupID, operatorID int64) error {
		if groupID != 9 || operatorID != 7 {
			t.Fatalf("unexpected disband command: group=%d operator=%d", groupID, operatorID)
		}
		return nil
	}
	router, token := groupTestRouter(t, stub)

	// Request data cannot choose the operator. Identity only comes from JWT.
	response := serveGroupRequest(router, token, http.MethodDelete, "/api/v1/group/9", `{"operator_id":999}`)
	assertVoidGroupSuccess(t, response)
}

func TestGroupDisbandHandlerRejectsInvalidGroupID(t *testing.T) {
	t.Parallel()
	stub := completeGroupStub()
	stub.disband = func(context.Context, int64, int64) error {
		t.Fatal("disband service must not run for invalid group ID")
		return nil
	}
	router, token := groupTestRouter(t, stub)

	for _, path := range []string{"/api/v1/group/nope", "/api/v1/group/0", "/api/v1/group/-1"} {
		response := serveGroupRequest(router, token, http.MethodDelete, path, "")
		assertGroupHandlerError(t, response, http.StatusBadRequest, apperror.CodeInvalidParam)
	}
}

func TestGroupDisbandHandlerMapsPermissionError(t *testing.T) {
	t.Parallel()
	stub := completeGroupStub()
	stub.disband = func(context.Context, int64, int64) error {
		return apperror.New(apperror.CodeNotOwnerOrAdmin)
	}
	router, token := groupTestRouter(t, stub)

	response := serveGroupRequest(router, token, http.MethodDelete, "/api/v1/group/9", "")
	assertGroupHandlerError(t, response, http.StatusForbidden, apperror.CodeNotOwnerOrAdmin)
}
