package redis

import (
	"context"
	"strconv"

	goredis "github.com/redis/go-redis/v9"
)

const luaInboxMarkRead = `
local userID = KEYS[1]
local convID = KEYS[2]
local unreadKey = 'unread:' .. userID
local unread = tonumber(redis.call('HGET', unreadKey, convID) or '0')
redis.call('HSET', unreadKey, convID, 0)
return unread
`

// ExecInboxMarkRead 原子清零会话未读计数并返回清零前的数量。
// 不重写收件箱 JSON，避免 Redis Lua/cjson 将 16 位 int64 消息 ID 转成科学计数法。
func ExecInboxMarkRead(rdb *goredis.Client, ctx context.Context, userID int64, convID string) (int64, error) {
	keys := []string{
		strconv.FormatInt(userID, 10),
		convID,
	}
	result, err := runScript("inbox_mark_read", rdb, ctx, keys).Int64()
	if err != nil {
		return 0, err
	}
	return result, nil
}
