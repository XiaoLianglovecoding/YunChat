package redis

import (
	"context"
	"strconv"

	goredis "github.com/redis/go-redis/v9"
)

// private_msg_check.lua 返回的错误码
const (
	PMErrOK        = 0
	PMErrNotFriend = 1
	PMErrBlocked   = 2
	PMErrDuplicate = 3
)

// 从 Lua 错误码映射的面向客户端的错误码。
// 避免将原始 Lua 整数值（1,2,3）与 HTTP 风格的状态码（400,500,403）混用。
const (
	CodePMNotFriend = 4001
	CodePMBlocked   = 4002
	CodePMDuplicate = 4003
)

// MapLuaErrToClientCode 将私信 Lua 错误码转换为面向客户端的错误码。
// 对于 OK 返回 0，对于无法识别的 Lua 错误码返回原始值（它们会作为"未知错误"传递）。
func MapLuaErrToClientCode(luaErrCode int) int {
	switch luaErrCode {
	case PMErrNotFriend:
		return CodePMNotFriend
	case PMErrBlocked:
		return CodePMBlocked
	case PMErrDuplicate:
		return CodePMDuplicate
	default:
		return luaErrCode
	}
}

// PrivateMsgCheckResult 保存私信检查 Lua 脚本的结果。
type PrivateMsgCheckResult struct {
	ErrCode   int   // 0=正常, 1=不是好友, 2=被拉黑, 3=重复消息
	MsgID     int64 // 分配的全局消息 ID（出错时为 0）
	Timestamp int64 // Redis 服务器接收消息时的 Unix 毫秒
	IsOnline  bool  // 接收者在线状态
	IsFriend  bool  // 好友关系存在
	IsBlocked bool  // 任意一方拉黑了另一方
}

const luaPrivateMsgCheck = `
local senderID = KEYS[1]
local receiverID = KEYS[2]
local clientMsgID = KEYS[3]
local msgID = tonumber(ARGV[1])

-- Message IDs come from the durable MySQL segment generator. Redis must not
-- maintain a second allocator because a restored snapshot could reuse IDs.
if not msgID or msgID < 1 or msgID > 9007199254740991 or msgID ~= math.floor(msgID) then
    return redis.error_reply('invalid externally allocated message ID')
end

-- 1. 好友关系检查（双向）
local friend1 = redis.call('EXISTS', 'friend:' .. senderID .. ':' .. receiverID)
local friend2 = redis.call('EXISTS', 'friend:' .. receiverID .. ':' .. senderID)
if friend1 == 0 or friend2 == 0 then
    return {1, 0, 0, 0, 0, 0}
end

-- 2. 黑名单检查（任意方向拉黑都禁止私信）
local blockedByReceiver = redis.call('SISMEMBER', 'blacklist:' .. receiverID, senderID)
local blockedBySender = redis.call('SISMEMBER', 'blacklist:' .. senderID, receiverID)
if blockedByReceiver == 1 or blockedBySender == 1 then
    return {2, 0, 0, 0, 1, 1}
end

-- 3. 消息去重
local dedupKey = 'msg_dedup:' .. senderID .. ':' .. clientMsgID
local dedup = redis.call('SET', dedupKey, '1', 'EX', 300, 'NX')
if dedup == false then
    return {3, 0, 0, 0, 0, 0}
end

-- 4. 在线状态检查
local isOnline = redis.call('EXISTS', 'online:' .. receiverID)

-- 5. Timestamp is independent metadata; a message ID no longer encodes time.
local redisTime = redis.call('TIME')
local milliseconds = redisTime[1] * 1000 + math.floor(redisTime[2] / 1000)

return {0, msgID, milliseconds, isOnline, 1, 0}
`

// ExecPrivateMsgCheck 原子性地检查好友关系、黑名单、消息去重、
// 接收外部分配的消息 ID 并检查在线状态——所有这些操作都在单次
// Redis Lua 脚本执行中完成，以避免竞态条件。
func ExecPrivateMsgCheck(rdb *goredis.Client, ctx context.Context, senderID, receiverID int64, clientMsgID string, messageID int64) (*PrivateMsgCheckResult, error) {
	keys := []string{
		strconv.FormatInt(senderID, 10),
		strconv.FormatInt(receiverID, 10),
		clientMsgID,
	}
	result, err := runScript("private_msg_check", rdb, ctx, keys, messageID).Slice()
	if err != nil {
		return nil, err
	}

	res := &PrivateMsgCheckResult{}
	res.ErrCode = int(result[0].(int64))
	res.MsgID = result[1].(int64)
	res.Timestamp = result[2].(int64)
	res.IsOnline = result[3].(int64) == 1
	res.IsFriend = result[4].(int64) == 1
	res.IsBlocked = result[5].(int64) == 1

	return res, nil
}
