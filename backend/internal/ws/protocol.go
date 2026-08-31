// Package ws 实现 WebSocket 鉴权、连接租约、心跳和在线事件分发。
// 聊天消息的入站分派仍属于后续 WS-001/MSG 任务。
package ws

import (
	"my-im/internal/model"
	"my-im/internal/protocol"
)

// Keep these aliases at the WebSocket package boundary so connection code can
// publish typed events without duplicating the wire contract.
type FriendApplyPayload = protocol.FriendApplyPayload
type FriendAcceptedPayload = protocol.FriendAcceptedPayload
type PresencePayload = protocol.PresencePayload

const (
	TypeMsg            = protocol.TypeMsg
	TypeServerAck      = protocol.TypeServerAck
	TypeDeliverAck     = protocol.TypeDeliverAck
	TypeReadAck        = protocol.TypeReadAck
	TypeSyncReq        = protocol.TypeSyncReq
	TypeSyncBatch      = protocol.TypeSyncBatch
	TypeConvSync       = protocol.TypeConvSync
	TypeRevokeMsg      = protocol.TypeRevokeMsg
	TypeMsgRevoked     = protocol.TypeMsgRevoked
	TypeKick           = protocol.TypeKick
	TypeFriendApply    = protocol.TypeFriendApply
	TypeFriendAccepted = protocol.TypeFriendAccepted
	TypePresence       = protocol.TypePresence
	TypeGroupAdded     = protocol.TypeGroupAdded
	TypeGroupRemoved   = protocol.TypeGroupRemoved
	TypeError          = protocol.TypeError
	TypePing           = protocol.TypePing
	TypePong           = protocol.TypePong
)

func EncodeMsg(msgType string, data interface{}) ([]byte, error) {
	return protocol.EncodeMsg(msgType, data)
}

func DecodeMsg(raw []byte) (*model.WsMessage, error) {
	return protocol.DecodeMsg(raw)
}
