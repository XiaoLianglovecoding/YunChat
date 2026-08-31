package redis

import (
	"context"
	"strconv"

	goredis "github.com/redis/go-redis/v9"
)

// group_msg_check.lua 返回的错误码
const (
	GMErrOK        = 0
	GMErrNotMember = 1
	GMErrMuted     = 2
	GMErrDuplicate = 3
)

// 从群聊 Lua 错误码映射的客户端错误码。
const (
	CodeGMNotMember = 5001
	CodeGMMuted     = 5002
	CodeGMDuplicate = 5003
)

// MapGroupLuaErrToClientCode 将群聊消息 Lua 错误码转换为客户端错误码。
// 成功时返回 0，无法识别的 Lua 错误码则返回原始值。
func MapGroupLuaErrToClientCode(luaErrCode int) int {
	switch luaErrCode {
	case GMErrNotMember:
		return CodeGMNotMember
	case GMErrMuted:
		return CodeGMMuted
	case GMErrDuplicate:
		return CodeGMDuplicate
	default:
		return luaErrCode
	}
}

// GroupMsgCheckResult 保存群聊消息检查 Lua 脚本的结果。
type GroupMsgCheckResult struct {
	ErrCode  int   // 0=正常, 1=非成员, 2=已禁言, 3=重复消息
	MsgID    int64 // 分配的全局消息 ID（出错时为 0）
	GroupSeq int64 // 分配的群聊序列号（出错时为 0）
	IsMember bool  // 成员状态
	IsMuted  bool  // 禁言状态
}

const luaGroupMsgCheck = `
local groupID = KEYS[1]
local senderID = KEYS[2]
local clientMsgID = KEYS[3]

-- 1. 成员身份检查
local isMember = redis.call('SISMEMBER', 'group_members:' .. groupID, senderID)
if isMember == 0 then
    return {1, 0, 0, 0, 0}
end

-- Redis server time is the shared clock for both mute expiry and message IDs.
-- muted_until is cached as a Unix-millisecond number, not a frozen boolean,
-- so the same cache entry automatically becomes writable when its deadline passes.
local redisTime = redis.call('TIME')
local milliseconds = tonumber(redisTime[1]) * 1000 + math.floor(tonumber(redisTime[2]) / 1000)

-- 2. Mute status check
local memberInfo = redis.call('HGET', 'group_member_info:' .. groupID, senderID)
if memberInfo then
    local info = cjson.decode(memberInfo)
    local mutedUntil = tonumber(info.muted_until)
    if mutedUntil and mutedUntil > milliseconds then
        return {2, 0, 0, 1, 1}
    end
    -- Rolling-upgrade safety for the old {muted:true, muted_until:"ISO"}
    -- format. It fails closed until startup rebuild writes the numeric deadline.
    if not mutedUntil and info.muted == true then
        return {2, 0, 0, 1, 1}
    end
end

-- 3. Message dedup
local dedupKey = 'msg_dedup:' .. senderID .. ':' .. clientMsgID
local dedup = redis.call('SET', dedupKey, '1', 'EX', 300, 'NX')
if dedup == false then
    return {3, 0, 0, 0, 0}
end

-- 4. Allocate a global message ID from Redis server time.
-- Format: Unix milliseconds * 1000 + per-millisecond sequence (1..999).
local sequenceKey = 'msg_id_seq:' .. milliseconds
local sequence = redis.call('INCR', sequenceKey)
if sequence == 1 then
    redis.call('EXPIRE', sequenceKey, 2)
end
if sequence > 999 then
    return redis.error_reply('message ID sequence overflow')
end
local msgID = milliseconds * 1000 + sequence

-- 5. Allocate group sequence number
local groupSeq = redis.call('INCR', 'group_seq:' .. groupID)

return {0, msgID, groupSeq, 1, 0}
`

// ExecGroupMsgCheck atomically checks group membership, mute status, dedup,
// allocates a message ID and group sequence number — all in a single
// Redis Lua script execution to avoid race conditions.
func ExecGroupMsgCheck(rdb *goredis.Client, ctx context.Context, groupID, senderID int64, clientMsgID string) (*GroupMsgCheckResult, error) {
	keys := []string{
		strconv.FormatInt(groupID, 10),
		strconv.FormatInt(senderID, 10),
		clientMsgID,
	}
	result, err := runScript("group_msg_check", rdb, ctx, keys).Slice()
	if err != nil {
		return nil, err
	}

	res := &GroupMsgCheckResult{}
	res.ErrCode = int(result[0].(int64))
	res.MsgID = result[1].(int64)
	res.GroupSeq = result[2].(int64)
	res.IsMember = result[3].(int64) == 1
	res.IsMuted = result[4].(int64) == 1

	return res, nil
}
