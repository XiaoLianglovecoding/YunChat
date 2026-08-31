package protocol

import (
	"encoding/json"

	"github.com/example/my-im/internal/model"
)

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
