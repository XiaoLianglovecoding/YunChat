package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

const (
	CodeSuccess = 0

	CodeInternalFailure = 1000
	CodeMissingParam    = 1001
	CodeInvalidParam    = 1002
	CodeUnauthorized    = 1003

	CodeUsernameTooShort = 1101
	CodePasswordTooShort = 1102
	CodeUsernameTaken    = 1103
	CodeUserNotFound     = 1104
	CodeWrongPassword    = 1105
	CodeInvalidToken     = 1106

	CodeSelfRequest      = 1201
	CodeAlreadyFriends   = 1202
	CodeFriendBlocked    = 1203
	CodeDuplicateRequest = 1204
	CodeRequestNotFound  = 1205
	CodeNotRequestTarget = 1206
	CodeAlreadyBlocked   = 1207

	CodeNotOwnerOrAdmin     = 1301
	CodeGroupNotFound       = 1302
	CodeAlreadyMember       = 1303
	CodeGroupFull           = 1304
	CodeCannotRemoveOwner   = 1305
	CodeCannotLeaveAsOwner  = 1306
	CodeInvalidRole         = 1307
	CodeMemberNotFriend     = 1308
	CodeCannotRemovePeer    = 1309
	CodeGroupMemberNotFound = 1310

	CodeMsgNotRevocable   = 1401
	CodeMsgRevokeNotOwner = 1402
	CodeMsgDeleteFailed   = 1403

	CodeMomentContentEmpty = 1501
	CodeMomentNotFound     = 1502
	CodeNotCommentOwner    = 1503
	CodeInvalidVisibility  = 1504
	CodeCommentNotFound    = 1505
	CodeNotMomentOwner     = 1506

	CodeSettingsNotFound = 1701
	CodeMuteConvExists   = 1702
	CodeMuteConvNotFound = 1703

	CodePrivateNotFriend = 4001
	CodePrivateBlocked   = 4002
	CodePrivateDuplicate = 4003
	CodeGroupNotMember   = 5001
	CodeGroupMuted       = 5002
	CodeGroupDuplicate   = 5003

	// 1900 仅用于骨架期；完成对应任务后必须移除该端点的 TODO 响应。
	CodeNotImplemented = 1900
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

// TODO[CONTRACT-001]: 为每个 Service 错误定义稳定的 HTTP 状态与业务错误码映射。

func TODO(c *gin.Context, taskID, feature string) {
	c.JSON(http.StatusNotImplemented, Response{
		Code:    CodeNotImplemented,
		Message: "TODO[" + taskID + "]: " + feature,
		Data: TodoData{
			TaskID:  taskID,
			Feature: feature,
		},
	})
}
