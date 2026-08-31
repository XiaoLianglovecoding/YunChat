package redis

import (
	"context"
	"strconv"

	goredis "github.com/redis/go-redis/v9"
)

// MomentLikeResult 保存点赞/取消赞 Lua 脚本的结果。
type MomentLikeResult struct {
	Changed bool  // 状态是否真的发生变化（true=本次新增赞/取消赞；false=幂等无操作）
	Count   int64 // 该动态当前的点赞数
}

// luaMomentLike 原子点赞：
//   - SADD 到点赞用户集合；若为新增（返回 1）则 INCR 点赞数计数器。
//   - 若用户已在集合中（重复点赞），只读取当前计数返回，不重复计数（天然幂等）。
//
// KEYS[1]=点赞集合 setKey, KEYS[2]=计数 countKey；ARGV[1]=userID。
// 返回 {changed(0/1), count}。
const luaMomentLike = `
local added = redis.call('SADD', KEYS[1], ARGV[1])
if added == 1 then
    local c = redis.call('INCR', KEYS[2])
    return {1, c}
end
return {0, tonumber(redis.call('GET', KEYS[2]) or '0')}
`

// luaMomentUnlike 原子取消赞：
//   - SREM 出集合；若确实移除（返回 1）则 DECR 计数，并将计数夹到不小于 0。
//   - 若用户本就不在集合中，只读取当前计数返回（幂等）。
//
// KEYS[1]=点赞集合 setKey, KEYS[2]=计数 countKey；ARGV[1]=userID。
// 返回 {changed(0/1), count}。
const luaMomentUnlike = `
local removed = redis.call('SREM', KEYS[1], ARGV[1])
if removed == 1 then
    local c = redis.call('DECR', KEYS[2])
    if c < 0 then
        redis.call('SET', KEYS[2], '0')
        c = 0
    end
    return {1, c}
end
return {0, tonumber(redis.call('GET', KEYS[2]) or '0')}
`

// ExecMomentLike 原子执行点赞（判重 + 计数），避免竞态。
func ExecMomentLike(rdb *goredis.Client, ctx context.Context, setKey, countKey string, userID int64) (*MomentLikeResult, error) {
	return execMomentLikeScript(rdb, ctx, "moment_like", setKey, countKey, userID)
}

// ExecMomentUnlike 原子执行取消赞（判重 + 计数）。
func ExecMomentUnlike(rdb *goredis.Client, ctx context.Context, setKey, countKey string, userID int64) (*MomentLikeResult, error) {
	return execMomentLikeScript(rdb, ctx, "moment_unlike", setKey, countKey, userID)
}

func execMomentLikeScript(rdb *goredis.Client, ctx context.Context, scriptName, setKey, countKey string, userID int64) (*MomentLikeResult, error) {
	keys := []string{setKey, countKey}
	res, err := runScript(scriptName, rdb, ctx, keys, strconv.FormatInt(userID, 10)).Slice()
	if err != nil {
		return nil, err
	}
	out := &MomentLikeResult{}
	if len(res) >= 1 {
		if v, ok := res[0].(int64); ok {
			out.Changed = v == 1
		}
	}
	if len(res) >= 2 {
		if v, ok := res[1].(int64); ok {
			out.Count = v
		}
	}
	return out, nil
}
