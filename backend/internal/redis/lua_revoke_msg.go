package redis

import (
	"context"
	"strconv"

	goredis "github.com/redis/go-redis/v9"
)

const luaRevokeMsg = `
local userID = KEYS[1]
local convID = KEYS[2]
local msgID = tonumber(KEYS[3])
local revokeMsgJSON = ARGV[1]
local nowTimestamp = tonumber(ARGV[2])

-- 判断是私聊还是群聊
local isGroup = false
if string.sub(convID, 1, 2) == 'g_' then
    isGroup = true
end

local function findMessage(zsetKey)
    local msgs = redis.call('ZRANGE', zsetKey, 0, -1)
    for _, msg in ipairs(msgs) do
        local decoded = cjson.decode(msg)
        if decoded.msgId == msgID then
            return msg, decoded
        end
    end
    return nil, nil
end

local function replaceWithRevoked(zsetKey, raw, original, replacement)
    -- Keep the original message metadata and timestamp.  The timestamp is also
    -- the sync cursor, so changing it would make the same message appear again
    -- as a new message after a client reconnects.
    original.msgType = replacement.msgType
    original.content = replacement.content
    redis.call('ZREM', zsetKey, raw)
    redis.call('ZADD', zsetKey, original.timestamp, cjson.encode(original))
end

local replacement = cjson.decode(revokeMsgJSON)

if isGroup then
    local groupID = string.sub(convID, 3)
    local zsetKey = 'outbox:' .. groupID
    local raw, original = findMessage(zsetKey)
    if not raw or original.fromId ~= tonumber(userID) then
        return 0
    end
    if nowTimestamp - original.timestamp > 120000 then
        return 0
    end
    replaceWithRevoked(zsetKey, raw, original, replacement)
    return 1
end

-- A private message is stored in both participants' inboxes.  Updating only
-- the sender's copy makes the receiver see the old content after a refresh.
-- Find and validate both copies before changing either one, then replace both
-- within this Lua invocation so the operation is atomic.
local senderKey = 'inbox:' .. userID
local senderRaw, senderMessage = findMessage(senderKey)
if not senderRaw or senderMessage.fromId ~= tonumber(userID) then
    return 0
end
if nowTimestamp - senderMessage.timestamp > 120000 then
    return 0
end

local receiverID = senderMessage.toId
if not receiverID or receiverID == tonumber(userID) then
    return 0
end
local receiverKey = 'inbox:' .. receiverID
local receiverRaw, receiverMessage = findMessage(receiverKey)
if not receiverRaw or receiverMessage.fromId ~= tonumber(userID) then
    return 0
end

replaceWithRevoked(senderKey, senderRaw, senderMessage, replacement)
replaceWithRevoked(receiverKey, receiverRaw, receiverMessage, replacement)
return 1
`

// ExecRevokeMsg 在 2 分钟内原子性地撤回消息。
// 它在收件箱/发件箱 ZSet 中根据 msgID 查找消息，检查
// 2 分钟时间戳窗口，并用撤回版本（msgType=6）替换原消息。
// 成功返回 true，未找到消息或超出 2 分钟窗口返回 false。
func ExecRevokeMsg(rdb *goredis.Client, ctx context.Context, userID int64, convID string, msgID int64, revokeMsgJSON string, nowTimestamp int64) (bool, error) {
	keys := []string{
		strconv.FormatInt(userID, 10),
		convID,
		strconv.FormatInt(msgID, 10),
	}
	args := []interface{}{
		revokeMsgJSON,
		strconv.FormatInt(nowTimestamp, 10),
	}
	result, err := runScript("revoke_msg", rdb, ctx, keys, args...).Int64()
	if err != nil {
		return false, err
	}
	return result == 1, nil
}
