package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"my-im/internal/model"
)

var ErrNotFound = errors.New("repository item not found")

type LuaRuleError struct{ Code int }

func (e *LuaRuleError) Error() string {
	return fmt.Sprintf("redis lua rule rejected request with code %d", e.Code)
}

func refreshKey(jti string) string       { return "refresh:" + jti }
func refreshUserKey(userID int64) string { return fmt.Sprintf("refresh_user:%d", userID) }

func (r *RedisRepoImpl) StoreRefreshSession(ctx context.Context, session RefreshSession, ttl time.Duration) error {
	if ttl <= 0 {
		ttl = time.Until(session.ExpiresAt)
	}
	if ttl <= 0 {
		return errors.New("refresh session is already expired")
	}
	pipe := r.rdb.TxPipeline()
	pipe.HSet(ctx, refreshKey(session.JTI), "user_id", session.UserID, "family_id", session.FamilyID, "expires_at", session.ExpiresAt.Unix())
	pipe.Expire(ctx, refreshKey(session.JTI), ttl)
	pipe.SAdd(ctx, refreshUserKey(session.UserID), session.JTI)
	pipe.Expire(ctx, refreshUserKey(session.UserID), ttl)
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("store refresh session: %w", err)
	}
	return nil
}

func (r *RedisRepoImpl) RotateRefreshSession(ctx context.Context, oldJTI string, next RefreshSession, ttl time.Duration) error {
	if ttl <= 0 {
		ttl = time.Until(next.ExpiresAt)
	}
	if ttl <= 0 {
		return errors.New("replacement refresh session is already expired")
	}
	err := r.rdb.Watch(ctx, func(tx *goredis.Tx) error {
		oldKey := refreshKey(oldJTI)
		values, err := tx.HGetAll(ctx, oldKey).Result()
		if err != nil {
			return fmt.Errorf("read old refresh session: %w", err)
		}
		if len(values) == 0 {
			return ErrNotFound
		}
		oldUserID, err := strconv.ParseInt(values["user_id"], 10, 64)
		if err != nil {
			return fmt.Errorf("invalid refresh session user: %w", err)
		}
		if oldUserID != next.UserID || values["family_id"] == "" || values["family_id"] != next.FamilyID {
			return ErrNotFound
		}
		_, err = tx.TxPipelined(ctx, func(pipe goredis.Pipeliner) error {
			pipe.Del(ctx, oldKey)
			pipe.SRem(ctx, refreshUserKey(oldUserID), oldJTI)
			pipe.HSet(ctx, refreshKey(next.JTI), "user_id", next.UserID, "family_id", next.FamilyID, "expires_at", next.ExpiresAt.Unix())
			pipe.Expire(ctx, refreshKey(next.JTI), ttl)
			pipe.SAdd(ctx, refreshUserKey(next.UserID), next.JTI)
			pipe.Expire(ctx, refreshUserKey(next.UserID), ttl)
			return nil
		})
		return err
	}, refreshKey(oldJTI))
	// 两个并发请求同时刷新时，只有一个事务能提交；另一个按“令牌已使用”处理。
	if errors.Is(err, goredis.TxFailedErr) {
		return ErrNotFound
	}
	return err
}

func (r *RedisRepoImpl) RevokeUserRefreshSessions(ctx context.Context, userID int64) error {
	indexKey := refreshUserKey(userID)
	jtis, err := r.rdb.SMembers(ctx, indexKey).Result()
	if err != nil {
		return fmt.Errorf("read refresh session index: %w", err)
	}
	keys := make([]string, 0, len(jtis)+1)
	keys = append(keys, indexKey)
	for _, jti := range jtis {
		keys = append(keys, refreshKey(jti))
	}
	if err := r.rdb.Del(ctx, keys...).Err(); err != nil {
		return fmt.Errorf("revoke refresh sessions: %w", err)
	}
	return nil
}

func (r *RedisRepoImpl) ReplaceGroupMembers(ctx context.Context, groupID int64, userIDs []int64) error {
	members := make([]any, len(userIDs))
	for i, id := range userIDs {
		members[i] = id
	}
	return replaceSet(ctx, r.rdb, fmt.Sprintf("group_members:%d", groupID), members)
}

func (r *RedisRepoImpl) ReplaceUserGroups(ctx context.Context, userID int64, groupIDs []int64) error {
	members := make([]any, len(groupIDs))
	for i, id := range groupIDs {
		members[i] = id
	}
	return replaceSet(ctx, r.rdb, fmt.Sprintf("user_groups:%d", userID), members)
}

func replaceSet(ctx context.Context, client *goredis.Client, key string, members []any) error {
	pipe := client.TxPipeline()
	pipe.Del(ctx, key)
	if len(members) > 0 {
		pipe.SAdd(ctx, key, members...)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("replace redis set %s: %w", key, err)
	}
	return nil
}

func (r *RedisRepoImpl) ReplaceFriendCache(ctx context.Context, userID int64, friendIDs []int64) error {
	pattern := fmt.Sprintf("friend:%d:*", userID)
	var cursor uint64
	oldFriends := make([]int64, 0)
	for {
		keys, next, err := r.rdb.Scan(ctx, cursor, pattern, 100).Result()
		if err != nil {
			return err
		}
		for _, key := range keys {
			var id int64
			if _, err := fmt.Sscanf(key, "friend:"+strconv.FormatInt(userID, 10)+":%d", &id); err == nil {
				oldFriends = append(oldFriends, id)
			}
		}
		cursor = next
		if cursor == 0 {
			break
		}
	}
	pipe := r.rdb.TxPipeline()
	for _, id := range oldFriends {
		pipe.Del(ctx, fmt.Sprintf("friend:%d:%d", userID, id), fmt.Sprintf("friend:%d:%d", id, userID))
	}
	for _, id := range friendIDs {
		pipe.Set(ctx, fmt.Sprintf("friend:%d:%d", userID, id), "1", 0)
		pipe.Set(ctx, fmt.Sprintf("friend:%d:%d", id, userID), "1", 0)
	}
	_, err := pipe.Exec(ctx)
	return err
}

func (r *RedisRepoImpl) SetBlacklistMember(ctx context.Context, userID, blockedID int64) error {
	return r.rdb.SAdd(ctx, fmt.Sprintf("blacklist:%d", userID), blockedID).Err()
}
func (r *RedisRepoImpl) DeleteBlacklistMember(ctx context.Context, userID, blockedID int64) error {
	return r.rdb.SRem(ctx, fmt.Sprintf("blacklist:%d", userID), blockedID).Err()
}
func (r *RedisRepoImpl) ReplaceBlacklist(ctx context.Context, userID int64, blockedIDs []int64) error {
	members := make([]any, len(blockedIDs))
	for i, id := range blockedIDs {
		members[i] = id
	}
	return replaceSet(ctx, r.rdb, fmt.Sprintf("blacklist:%d", userID), members)
}

func (r *RedisRepoImpl) SetGroupMemberInfo(ctx context.Context, groupID, userID int64, role int, mutedUntil *time.Time) error {
	value, err := json.Marshal(map[string]any{"role": role, "muted": mutedUntil != nil && mutedUntil.After(time.Now()), "muted_until": mutedUntil})
	if err != nil {
		return err
	}
	return r.rdb.HSet(ctx, fmt.Sprintf("group_member_info:%d", groupID), userID, value).Err()
}
func (r *RedisRepoImpl) DeleteGroupMemberInfo(ctx context.Context, groupID, userID int64) error {
	return r.rdb.HDel(ctx, fmt.Sprintf("group_member_info:%d", groupID), strconv.FormatInt(userID, 10)).Err()
}
func (r *RedisRepoImpl) ReplaceGroupMemberInfo(ctx context.Context, groupID int64, members []model.GroupMember) error {
	key := fmt.Sprintf("group_member_info:%d", groupID)
	pipe := r.rdb.TxPipeline()
	pipe.Del(ctx, key)
	for _, member := range members {
		value, err := json.Marshal(map[string]any{"role": member.Role, "muted": member.MutedUntil != nil && member.MutedUntil.After(time.Now()), "muted_until": member.MutedUntil})
		if err != nil {
			return err
		}
		pipe.HSet(ctx, key, member.UserID, value)
	}
	_, err := pipe.Exec(ctx)
	return err
}

var _ RedisRepository = (*RedisRepoImpl)(nil)
