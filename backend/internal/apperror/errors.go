// Package apperror 冻结 HTTP 与 WebSocket 共用的业务错误码契约。
package apperror

import (
	"errors"
	"fmt"
	"net/http"
)

type Code int

const (
	CodeSuccess             Code = 0
	CodeInternalFailure     Code = 1000
	CodeMissingParam        Code = 1001
	CodeInvalidParam        Code = 1002
	CodeUnauthorized        Code = 1003
	CodeUsernameTooShort    Code = 1101
	CodePasswordTooShort    Code = 1102
	CodeUsernameTaken       Code = 1103
	CodeUserNotFound        Code = 1104
	CodeWrongPassword       Code = 1105
	CodeInvalidToken        Code = 1106
	CodeSelfRequest         Code = 1201
	CodeAlreadyFriends      Code = 1202
	CodeFriendBlocked       Code = 1203
	CodeDuplicateRequest    Code = 1204
	CodeRequestNotFound     Code = 1205
	CodeNotRequestTarget    Code = 1206
	CodeAlreadyBlocked      Code = 1207
	CodeNotOwnerOrAdmin     Code = 1301
	CodeGroupNotFound       Code = 1302
	CodeAlreadyMember       Code = 1303
	CodeGroupFull           Code = 1304
	CodeCannotRemoveOwner   Code = 1305
	CodeCannotLeaveAsOwner  Code = 1306
	CodeInvalidRole         Code = 1307
	CodeMemberNotFriend     Code = 1308
	CodeCannotRemovePeer    Code = 1309
	CodeGroupMemberNotFound Code = 1310
	CodeMsgNotRevocable     Code = 1401
	CodeMsgRevokeNotOwner   Code = 1402
	CodeMsgDeleteFailed     Code = 1403
	CodeMomentContentEmpty  Code = 1501
	CodeMomentNotFound      Code = 1502
	CodeNotCommentOwner     Code = 1503
	CodeInvalidVisibility   Code = 1504
	CodeCommentNotFound     Code = 1505
	CodeNotMomentOwner      Code = 1506
	CodeSettingsNotFound    Code = 1701
	CodeMuteConvExists      Code = 1702
	CodeMuteConvNotFound    Code = 1703
	CodeNotImplemented      Code = 1900
	CodePrivateNotFriend    Code = 4001
	CodePrivateBlocked      Code = 4002
	CodePrivateDuplicate    Code = 4003
	CodeGroupNotMember      Code = 5001
	CodeGroupMuted          Code = 5002
	CodeGroupDuplicate      Code = 5003
)

type Definition struct {
	HTTPStatus int
	Message    string
}

var catalog = map[Code]Definition{
	CodeInternalFailure: {500, "internal server error"}, CodeMissingParam: {400, "missing parameter"}, CodeInvalidParam: {400, "invalid parameter"}, CodeUnauthorized: {401, "unauthorized"},
	CodeUsernameTooShort: {400, "username is too short"}, CodePasswordTooShort: {400, "password is too short"}, CodeUsernameTaken: {409, "username already exists"}, CodeUserNotFound: {404, "user not found"}, CodeWrongPassword: {401, "wrong username or password"}, CodeInvalidToken: {401, "invalid or expired token"},
	CodeSelfRequest: {400, "cannot add yourself"}, CodeAlreadyFriends: {409, "already friends"}, CodeFriendBlocked: {403, "friend operation is blocked"}, CodeDuplicateRequest: {409, "friend request already exists"}, CodeRequestNotFound: {404, "friend request not found"}, CodeNotRequestTarget: {403, "not the request target"}, CodeAlreadyBlocked: {409, "user already blocked"},
	CodeNotOwnerOrAdmin: {403, "group permission denied"}, CodeGroupNotFound: {404, "group not found"}, CodeAlreadyMember: {409, "already a group member"}, CodeGroupFull: {409, "group is full"}, CodeCannotRemoveOwner: {403, "cannot remove group owner"}, CodeCannotLeaveAsOwner: {409, "owner must transfer ownership first"}, CodeInvalidRole: {400, "invalid group role"}, CodeMemberNotFriend: {403, "group member must be a friend"}, CodeCannotRemovePeer: {403, "cannot remove a peer administrator"}, CodeGroupMemberNotFound: {404, "group member not found"},
	CodeMsgNotRevocable: {409, "message cannot be revoked"}, CodeMsgRevokeNotOwner: {403, "only the sender can revoke this message"}, CodeMsgDeleteFailed: {500, "message deletion failed"},
	CodeMomentContentEmpty: {400, "moment content is empty"}, CodeMomentNotFound: {404, "moment not found"}, CodeNotCommentOwner: {403, "not the comment owner"}, CodeInvalidVisibility: {400, "invalid moment visibility"}, CodeCommentNotFound: {404, "comment not found"}, CodeNotMomentOwner: {403, "not the moment owner"},
	CodeSettingsNotFound: {404, "settings not found"}, CodeMuteConvExists: {409, "conversation is already muted"}, CodeMuteConvNotFound: {404, "muted conversation not found"},
	CodeNotImplemented:   {501, "feature not implemented"},
	CodePrivateNotFriend: {403, "private messages require friendship"}, CodePrivateBlocked: {403, "private message blocked"}, CodePrivateDuplicate: {409, "duplicate private message"},
	CodeGroupNotMember: {403, "not a group member"}, CodeGroupMuted: {403, "group member is muted"}, CodeGroupDuplicate: {409, "duplicate group message"},
}

type Error struct {
	Code       Code
	HTTPStatus int
	Message    string
	Cause      error
}

func (e *Error) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("code %d: %s: %v", e.Code, e.Message, e.Cause)
	}
	return fmt.Sprintf("code %d: %s", e.Code, e.Message)
}
func (e *Error) Unwrap() error { return e.Cause }

func New(code Code) *Error { return Wrap(code, nil) }
func Wrap(code Code, cause error) *Error {
	definition, ok := catalog[code]
	if !ok {
		definition = catalog[CodeInternalFailure]
		code = CodeInternalFailure
	}
	return &Error{Code: code, HTTPStatus: definition.HTTPStatus, Message: definition.Message, Cause: cause}
}
func WithMessage(code Code, safeMessage string) *Error {
	e := New(code)
	if safeMessage != "" {
		e.Message = safeMessage
	}
	return e
}
func From(err error) *Error {
	if err == nil {
		return nil
	}
	var appErr *Error
	if errors.As(err, &appErr) {
		return appErr
	}
	return Wrap(CodeInternalFailure, err)
}
func DefinitionFor(code Code) (Definition, bool) {
	definition, ok := catalog[code]
	return definition, ok
}
func CatalogSize() int { return len(catalog) }

var _ = http.StatusOK // 让状态码来源在文档跳转中明确为 net/http；具体值被冻结在 catalog。
