package protocol

import (
	"encoding/json"
	"time"

	"my-im/internal/model"
)

// FriendApplyPayload is sent to the target user after a friend request has
// been committed to MySQL.  The camelCase JSON tags are part of the public
// WebSocket contract shared with the TypeScript client.
type FriendApplyPayload struct {
	RequestID  int64     `json:"requestId"`
	FromUserID int64     `json:"fromUserId"`
	Username   string    `json:"username"`
	AvatarURL  string    `json:"avatarUrl,omitempty"`
	Message    string    `json:"message"`
	CreatedAt  time.Time `json:"createdAt"`
}

// FriendAcceptedPayload is sent after accepting a request. UserID and
// FriendID describe the two sides of the newly-created friendship; a client
// can pick the other side by comparing them with its current user ID.
type FriendAcceptedPayload struct {
	RequestID int64  `json:"requestId"`
	UserID    int64  `json:"userId"`
	FriendID  int64  `json:"friendId"`
	Username  string `json:"username"`
	AvatarURL string `json:"avatarUrl,omitempty"`
}

// PresencePayload reports the current online state of one friend.
type PresencePayload struct {
	UserID int64 `json:"userId"`
	Online bool  `json:"online"`
}

const (
	TypeMsg            = "msg"
	TypeServerAck      = "serverAck"
	TypeDeliverAck     = "deliverAck"
	TypeReadAck        = "readAck"
	TypeSyncReq        = "syncReq"
	TypeSyncBatch      = "syncBatch"
	TypeConvSync       = "convSync"
	TypeRevokeMsg      = "revokeMsg"
	TypeMsgRevoked     = "msgRevoked"
	TypeKick           = "kick"
	TypeFriendApply    = "friendApply"
	TypeFriendAccepted = "friendAccepted"
	TypePresence       = "presence"
	TypeGroupAdded     = "groupAdded"
	TypeGroupRemoved   = "groupRemoved"
	TypeError          = "error"
	TypePing           = "ping"
	TypePong           = "pong"
)

func EncodeMsg(msgType string, data interface{}) ([]byte, error) {
	dataBytes, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}
	return json.Marshal(model.WsMessage{Type: msgType, Data: dataBytes})
}

func DecodeMsg(raw []byte) (*model.WsMessage, error) {
	var message model.WsMessage
	if err := json.Unmarshal(raw, &message); err != nil {
		return nil, err
	}
	return &message, nil
}
