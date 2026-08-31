package model

import (
	"encoding/json"
	"fmt"
)

// 消息类型常量
const (
	MsgTypeText    = 1
	MsgTypeImage   = 2
	MsgTypeVideo   = 3
	MsgTypeSystem  = 5
	MsgTypeRevoked = 6

	ConvTypePrivate = 1
	ConvTypeGroup   = 2

	RoleMember = 0
	RoleAdmin  = 1
	RoleOwner  = 2
)

// BuildConvID 生成会话 ID：
//
//	private: p_{较小ID}_{较大ID}
//	group:   g_{群组ID}
func BuildConvID(convType int, id1, id2 int64) string {
	if convType == ConvTypeGroup {
		return fmt.Sprintf("g_%d", id1)
	}
	if id1 > id2 {
		id1, id2 = id2, id1
	}
	return fmt.Sprintf("p_%d_%d", id1, id2)
}

// WsMessage WebSocket 通用消息封装
type WsMessage struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

// WsError 通过 WebSocket 发送的错误负载
type WsError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}
