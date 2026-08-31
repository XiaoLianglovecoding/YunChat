package api

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"my-im/internal/apperror"
)

const (
	CodeSuccess = int(apperror.CodeSuccess)

	CodeInternalFailure = int(apperror.CodeInternalFailure)
	CodeMissingParam    = int(apperror.CodeMissingParam)
	CodeInvalidParam    = int(apperror.CodeInvalidParam)
	CodeUnauthorized    = int(apperror.CodeUnauthorized)

	CodeUsernameTooShort = int(apperror.CodeUsernameTooShort)
	CodePasswordTooShort = int(apperror.CodePasswordTooShort)
	CodeUsernameTaken    = int(apperror.CodeUsernameTaken)
	CodeUserNotFound     = int(apperror.CodeUserNotFound)
	CodeWrongPassword    = int(apperror.CodeWrongPassword)
	CodeInvalidToken     = int(apperror.CodeInvalidToken)

	CodeSelfRequest      = int(apperror.CodeSelfRequest)
	CodeAlreadyFriends   = int(apperror.CodeAlreadyFriends)
	CodeFriendBlocked    = int(apperror.CodeFriendBlocked)
	CodeDuplicateRequest = int(apperror.CodeDuplicateRequest)
	CodeRequestNotFound  = int(apperror.CodeRequestNotFound)
	CodeNotRequestTarget = int(apperror.CodeNotRequestTarget)
	CodeAlreadyBlocked   = int(apperror.CodeAlreadyBlocked)

	CodeNotOwnerOrAdmin     = int(apperror.CodeNotOwnerOrAdmin)
	CodeGroupNotFound       = int(apperror.CodeGroupNotFound)
	CodeAlreadyMember       = int(apperror.CodeAlreadyMember)
	CodeGroupFull           = int(apperror.CodeGroupFull)
	CodeCannotRemoveOwner   = int(apperror.CodeCannotRemoveOwner)
	CodeCannotLeaveAsOwner  = int(apperror.CodeCannotLeaveAsOwner)
	CodeInvalidRole         = int(apperror.CodeInvalidRole)
	CodeMemberNotFriend     = int(apperror.CodeMemberNotFriend)
	CodeCannotRemovePeer    = int(apperror.CodeCannotRemovePeer)
	CodeGroupMemberNotFound = int(apperror.CodeGroupMemberNotFound)

	CodeMsgNotRevocable   = int(apperror.CodeMsgNotRevocable)
	CodeMsgRevokeNotOwner = int(apperror.CodeMsgRevokeNotOwner)
	CodeMsgDeleteFailed   = int(apperror.CodeMsgDeleteFailed)

	CodeMomentContentEmpty = int(apperror.CodeMomentContentEmpty)
	CodeMomentNotFound     = int(apperror.CodeMomentNotFound)
	CodeNotCommentOwner    = int(apperror.CodeNotCommentOwner)
	CodeInvalidVisibility  = int(apperror.CodeInvalidVisibility)
	CodeCommentNotFound    = int(apperror.CodeCommentNotFound)
	CodeNotMomentOwner     = int(apperror.CodeNotMomentOwner)

	CodeSettingsNotFound = int(apperror.CodeSettingsNotFound)
	CodeMuteConvExists   = int(apperror.CodeMuteConvExists)
	CodeMuteConvNotFound = int(apperror.CodeMuteConvNotFound)

	CodePrivateNotFriend = int(apperror.CodePrivateNotFriend)
	CodePrivateBlocked   = int(apperror.CodePrivateBlocked)
	CodePrivateDuplicate = int(apperror.CodePrivateDuplicate)
	CodeGroupNotMember   = int(apperror.CodeGroupNotMember)
	CodeGroupMuted       = int(apperror.CodeGroupMuted)
	CodeGroupDuplicate   = int(apperror.CodeGroupDuplicate)

	// 1900 仅用于骨架期；完成对应任务后必须移除该端点的 TODO 响应。
	CodeNotImplemented = int(apperror.CodeNotImplemented)
)

// Response 与已复制前端的固定响应信封保持一致。
type Response struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

type TodoData struct {
	TaskID  string `json:"task_id"`
	Feature string `json:"feature"`
}

func WriteError(c *gin.Context, err error) {
	appErr := apperror.From(err)
	c.JSON(appErr.HTTPStatus, Response{Code: int(appErr.Code), Message: appErr.Message})
}

func WriteSuccess(c *gin.Context, status int, data interface{}) {
	c.JSON(status, Response{Code: int(apperror.CodeSuccess), Message: "ok", Data: data})
}

func TODO(c *gin.Context, taskID, feature string) {
	err := apperror.WithMessage(apperror.CodeNotImplemented, "TODO["+taskID+"]: "+feature)
	c.JSON(http.StatusNotImplemented, Response{
		Code:    int(err.Code),
		Message: err.Message,
		Data: TodoData{
			TaskID:  taskID,
			Feature: feature,
		},
	})
}
