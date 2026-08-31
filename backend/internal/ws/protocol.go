// Package ws 保留原项目的 WebSocket 模块边界。
// TODO[WS-001]: 实现升级、JWT 鉴权、单用户连接替换、心跳和消息分发。
package ws

import (
	"github.com/example/my-im/internal/model"
	"github.com/example/my-im/internal/protocol"
)

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
