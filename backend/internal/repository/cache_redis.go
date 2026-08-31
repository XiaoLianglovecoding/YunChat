package repository

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"my-im/internal/model"
)

var (
	replaceFriendOwnerScript       = goredis.NewScript(replaceFriendOwnerLua)
	replaceGroupMembersOwnerScript = goredis.NewScript(replaceGroupMembersOwnerLua)
	releaseCacheWarmLockScript     = goredis.NewScript(`
if redis.call('GET', KEYS[1]) == ARGV[1] then
    return redis.call('DEL', KEYS[1])
end
return 0
`)
)

// The friend owner index makes replacement proportional to one user's friend
// count. ARGV[1] is ownerID, ARGV[2] is the number of legacy IDs passed by Go,
// followed by those legacy IDs and then the desired friend IDs.
const replaceFriendOwnerLua = `
local ownerID = ARGV[1]
local legacyCount = tonumber(ARGV[2])
local oldFriendIDs = redis.call('SMEMBERS', KEYS[2])
for _, friendID in ipairs(oldFriendIDs) do
    redis.call('DEL', 'friend:' .. ownerID .. ':' .. friendID)
end
for i = 1, legacyCount do
    redis.call('DEL', 'friend:' .. ownerID .. ':' .. ARGV[2 + i])
end
redis.call('DEL', KEYS[2])
for i = 3 + legacyCount, #ARGV do
    local friendID = ARGV[i]
    redis.call('SET', 'friend:' .. ownerID .. ':' .. friendID, '1')
    redis.call('SADD', KEYS[2], friendID)
end
redis.call('SET', KEYS[1], '1')
redis.call('SET', KEYS[3], '1')
return #oldFriendIDs + legacyCount
`

// The reverse-owner index plus the old forward member set form the complete
// cleanup set for normal writes. ARGV[2] is a one-time legacy reverse-owner
// count, followed by those IDs and then the desired (userID, infoJSON) pairs.
// This keeps replacement atomic and avoids a blocking KEYS lookup.
const replaceGroupMembersOwnerLua = `
local groupID = ARGV[1]
local legacyCount = tonumber(ARGV[2])
local oldMembers = redis.call('SMEMBERS', KEYS[1])
local oldReverseOwners = redis.call('SMEMBERS', KEYS[4])
for _, userID in ipairs(oldMembers) do
    redis.call('SREM', 'user_groups:' .. userID, groupID)
end
for _, userID in ipairs(oldReverseOwners) do
    redis.call('SREM', 'user_groups:' .. userID, groupID)
end
for i = 1, legacyCount do
    redis.call('SREM', 'user_groups:' .. ARGV[2 + i], groupID)
end
redis.call('DEL', KEYS[1], KEYS[2], KEYS[4])
for i = 3 + legacyCount, #ARGV, 2 do
    local userID = ARGV[i]
    local infoJSON = ARGV[i + 1]
    redis.call('SADD', KEYS[1], userID)
    redis.call('HSET', KEYS[2], userID, infoJSON)
    redis.call('SADD', 'user_groups:' .. userID, groupID)
    redis.call('SADD', KEYS[4], userID)
end
redis.call('SET', KEYS[3], '1')
redis.call('SET', KEYS[5], '1')
return #oldMembers + #oldReverseOwners + legacyCount
`

func friendLoadedKey(userID int64) string {
	return fmt.Sprintf("friend_loaded:%d", userID)
}

func blacklistLoadedKey(userID int64) string {
	return fmt.Sprintf("blacklist_loaded:%d", userID)
}

func groupMemberLoadedKey(groupID int64) string {
	return fmt.Sprintf("group_member_loaded:%d", groupID)
}

func friendOwnerIndexKey(userID int64) string {
	return fmt.Sprintf("friend_owner_index:%d", userID)
}

func friendOwnerIndexLoadedKey(userID int64) string {
	return fmt.Sprintf("friend_owner_index_loaded:%d", userID)
}

func groupReverseOwnerIndexKey(groupID int64) string {
	return fmt.Sprintf("group_reverse_owner_index:%d", groupID)
}

func groupReverseOwnerIndexLoadedKey(groupID int64) string {
	return fmt.Sprintf("group_reverse_owner_index_loaded:%d", groupID)
}

func cacheWarmLockKey(resource CacheResource, resourceID int64) string {
	return fmt.Sprintf("cache_warm_lock:%s:%d", resource, resourceID)
}

func (r *RedisRepoImpl) FriendsLoaded(ctx context.Context, userID int64) (bool, error) {
	return r.cacheLoaded(ctx, friendLoadedKey(userID))
}

func (r *RedisRepoImpl) BlacklistLoaded(ctx context.Context, userID int64) (bool, error) {
	return r.cacheLoaded(ctx, blacklistLoadedKey(userID))
}

func (r *RedisRepoImpl) GroupMembersLoaded(ctx context.Context, groupID int64) (bool, error) {
	return r.cacheLoaded(ctx, groupMemberLoadedKey(groupID))
}

func (r *RedisRepoImpl) cacheLoaded(ctx context.Context, key string) (bool, error) {
	exists, err := r.rdb.Exists(ctx, key).Result()
	if err != nil {
		return false, fmt.Errorf("read relationship cache marker %s: %w", key, err)
	}
	return exists == 1, nil
}

// InvalidateProjectionMarkers is used only by the explicit management
// rebuild. Normal request and worker paths keep the owner-index marker and stay
// proportional to one owner's relationship count. Clearing the index marker
// here deliberately enables the incremental SCAN compatibility path in the
// next Replace* call, which also removes keys that an operator inserted without
// updating the bounded owner index.
func (r *RedisRepoImpl) InvalidateProjectionMarkers(
	ctx context.Context,
	resource CacheResource,
	resourceID int64,
) error {
	var keys []string
	switch resource {
	case CacheResourceFriends:
		keys = []string{friendLoadedKey(resourceID), friendOwnerIndexLoadedKey(resourceID)}
	case CacheResourceBlacklist:
		keys = []string{blacklistLoadedKey(resourceID)}
	case CacheResourceGroupMembers:
		keys = []string{groupMemberLoadedKey(resourceID), groupReverseOwnerIndexLoadedKey(resourceID)}
	default:
		return fmt.Errorf("invalidate projection markers: invalid resource %q", resource)
	}
	if err := r.rdb.Del(ctx, keys...).Err(); err != nil {
		return fmt.Errorf("invalidate projection markers %s:%d: %w", resource, resourceID, err)
	}
	return nil
}

func (r *RedisRepoImpl) TryCacheWarmLock(
	ctx context.Context,
	resource CacheResource,
	resourceID int64,
	ttl time.Duration,
) (string, bool, error) {
	if !resource.Valid() {
		return "", false, fmt.Errorf("cache warm lock: invalid resource %q", resource)
	}
	if ttl <= 0 {
		ttl = 5 * time.Second
	}
	token, err := randomCacheToken()
	if err != nil {
		return "", false, err
	}
	acquired, err := r.rdb.SetNX(ctx, cacheWarmLockKey(resource, resourceID), token, ttl).Result()
	if err != nil {
		return "", false, fmt.Errorf("acquire cache warm lock %s:%d: %w", resource, resourceID, err)
	}
	return token, acquired, nil
}

func (r *RedisRepoImpl) ReleaseCacheWarmLock(
	ctx context.Context,
	resource CacheResource,
	resourceID int64,
	token string,
) error {
	if token == "" {
		return nil
	}
	if _, err := releaseCacheWarmLockScript.Run(
		ctx,
		r.rdb,
		[]string{cacheWarmLockKey(resource, resourceID)},
		token,
	).Result(); err != nil {
		return fmt.Errorf("release cache warm lock %s:%d: %w", resource, resourceID, err)
	}
	return nil
}

func randomCacheToken() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("create cache lock token: %w", err)
	}
	return hex.EncodeToString(raw), nil
}

// ReplaceFriendOwner atomically replaces only friend:{owner}:* keys. It must
// never touch friend:{other}:{owner}; that direction belongs to another MySQL row.
// A per-owner Set records the exact keys owned by this projection, so rebuilding
// one user never scans the Redis database.
func (r *RedisRepoImpl) ReplaceFriendOwner(ctx context.Context, ownerID int64, friendIDs []int64) error {
	legacyIDs := make([]int64, 0)
	indexLoaded, err := r.cacheLoaded(ctx, friendOwnerIndexLoadedKey(ownerID))
	if err != nil {
		return fmt.Errorf("read friend owner index marker %d: %w", ownerID, err)
	}
	if !indexLoaded {
		// One-time compatibility path for caches written before the owner index
		// existed. SCAN is incremental and runs outside Lua; after this rebuild
		// the dedicated marker makes later replacements use only the index Set.
		legacyIDs, err = r.scanNumericSuffixes(ctx, fmt.Sprintf("friend:%d:", ownerID))
		if err != nil {
			return fmt.Errorf("bootstrap friend owner index %d: %w", ownerID, err)
		}
	}
	desiredIDs := sortedUniquePositive(friendIDs)
	args := make([]any, 0, len(legacyIDs)+len(desiredIDs)+2)
	args = append(args, strconv.FormatInt(ownerID, 10), len(legacyIDs))
	for _, friendID := range legacyIDs {
		args = append(args, strconv.FormatInt(friendID, 10))
	}
	for _, friendID := range desiredIDs {
		args = append(args, strconv.FormatInt(friendID, 10))
	}
	if _, err := replaceFriendOwnerScript.Run(ctx, r.rdb, []string{
		friendLoadedKey(ownerID),
		friendOwnerIndexKey(ownerID),
		friendOwnerIndexLoadedKey(ownerID),
	}, args...).Result(); err != nil {
		return fmt.Errorf("replace friend cache owner %d: %w", ownerID, err)
	}
	return nil
}

func (r *RedisRepoImpl) ReplaceBlacklistOwner(ctx context.Context, ownerID int64, blockedIDs []int64) error {
	key := fmt.Sprintf("blacklist:%d", ownerID)
	pipe := r.rdb.TxPipeline()
	pipe.Del(ctx, key)
	ids := sortedUniquePositive(blockedIDs)
	if len(ids) > 0 {
		members := make([]any, len(ids))
		for i, id := range ids {
			members[i] = id
		}
		pipe.SAdd(ctx, key, members...)
	}
	// The marker is written even for an empty set: empty is valid loaded truth.
	pipe.Set(ctx, blacklistLoadedKey(ownerID), "1", 0)
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("replace blacklist cache owner %d: %w", ownerID, err)
	}
	return nil
}

type cachedGroupMemberInfo struct {
	Role       int   `json:"role"`
	MutedUntil int64 `json:"muted_until,omitempty"` // Unix milliseconds; 0 means not muted.
}

func newCachedGroupMemberInfo(role int, mutedUntil *time.Time) cachedGroupMemberInfo {
	info := cachedGroupMemberInfo{Role: role}
	if mutedUntil != nil {
		info.MutedUntil = mutedUntil.UnixMilli()
	}
	return info
}

func (i cachedGroupMemberInfo) modelMutedUntil() *time.Time {
	if i.MutedUntil <= 0 {
		return nil
	}
	mutedUntil := time.UnixMilli(i.MutedUntil).UTC()
	return &mutedUntil
}

func (r *RedisRepoImpl) ReplaceGroupMembersOwner(ctx context.Context, groupID int64, members []model.GroupMember) error {
	memberKey := fmt.Sprintf("group_members:%d", groupID)
	infoKey := fmt.Sprintf("group_member_info:%d", groupID)
	legacyReverseIDs := make([]int64, 0)
	indexLoaded, err := r.cacheLoaded(ctx, groupReverseOwnerIndexLoadedKey(groupID))
	if err != nil {
		return fmt.Errorf("read group reverse owner index marker %d: %w", groupID, err)
	}
	if !indexLoaded {
		// Compatibility repair for caches created before the bounded reverse
		// owner index existed. SCAN is incremental and outside Lua; the marker
		// makes this a one-time cost for each group.
		legacyReverseIDs, err = r.readReverseGroupMembers(ctx, groupID)
		if err != nil {
			return fmt.Errorf("bootstrap group reverse owner index %d: %w", groupID, err)
		}
	}
	args := make([]any, 0, len(legacyReverseIDs)+len(members)*2+2)
	args = append(args, strconv.FormatInt(groupID, 10), len(legacyReverseIDs))
	for _, userID := range legacyReverseIDs {
		args = append(args, strconv.FormatInt(userID, 10))
	}
	seen := make(map[int64]struct{}, len(members))
	for _, member := range members {
		if member.UserID <= 0 {
			continue
		}
		if _, duplicate := seen[member.UserID]; duplicate {
			continue
		}
		seen[member.UserID] = struct{}{}
		info := newCachedGroupMemberInfo(member.Role, member.MutedUntil)
		encoded, err := json.Marshal(info)
		if err != nil {
			return fmt.Errorf("encode group %d member %d cache: %w", groupID, member.UserID, err)
		}
		args = append(args, strconv.FormatInt(member.UserID, 10), string(encoded))
	}
	if _, err := replaceGroupMembersOwnerScript.Run(
		ctx,
		r.rdb,
		[]string{
			memberKey,
			infoKey,
			groupMemberLoadedKey(groupID),
			groupReverseOwnerIndexKey(groupID),
			groupReverseOwnerIndexLoadedKey(groupID),
		},
		args...,
	).Result(); err != nil {
		return fmt.Errorf("replace group member cache owner %d: %w", groupID, err)
	}
	return nil
}

func (r *RedisRepoImpl) ReadFriendSnapshot(ctx context.Context, userID int64) (RelationshipCacheSnapshot, error) {
	loaded, err := r.FriendsLoaded(ctx, userID)
	if err != nil {
		return RelationshipCacheSnapshot{}, err
	}
	prefix := fmt.Sprintf("friend:%d:", userID)
	ids, err := r.scanNumericSuffixes(ctx, prefix)
	if err != nil {
		return RelationshipCacheSnapshot{}, fmt.Errorf("read friend cache snapshot for user %d: %w", userID, err)
	}
	return RelationshipCacheSnapshot{Loaded: loaded, IDs: ids}, nil
}

func (r *RedisRepoImpl) ReadBlacklistSnapshot(ctx context.Context, userID int64) (RelationshipCacheSnapshot, error) {
	loaded, err := r.BlacklistLoaded(ctx, userID)
	if err != nil {
		return RelationshipCacheSnapshot{}, err
	}
	raw, err := r.rdb.SMembers(ctx, fmt.Sprintf("blacklist:%d", userID)).Result()
	if err != nil {
		return RelationshipCacheSnapshot{}, fmt.Errorf("read blacklist cache snapshot for user %d: %w", userID, err)
	}
	ids, err := parseNumericMembers(raw)
	if err != nil {
		return RelationshipCacheSnapshot{}, fmt.Errorf("read blacklist cache snapshot for user %d: %w", userID, err)
	}
	return RelationshipCacheSnapshot{Loaded: loaded, IDs: ids}, nil
}

func (r *RedisRepoImpl) ReadGroupMemberSnapshot(ctx context.Context, groupID int64) (GroupMemberCacheSnapshot, error) {
	loaded, err := r.GroupMembersLoaded(ctx, groupID)
	if err != nil {
		return GroupMemberCacheSnapshot{}, err
	}
	memberKey := fmt.Sprintf("group_members:%d", groupID)
	infoKey := fmt.Sprintf("group_member_info:%d", groupID)
	pipe := r.rdb.Pipeline()
	membersCommand := pipe.SMembers(ctx, memberKey)
	infoCommand := pipe.HGetAll(ctx, infoKey)
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, goredis.Nil) {
		return GroupMemberCacheSnapshot{}, fmt.Errorf("read group %d cache snapshot: %w", groupID, err)
	}
	rawMembers, err := membersCommand.Result()
	if err != nil {
		return GroupMemberCacheSnapshot{}, fmt.Errorf("read group %d member set: %w", groupID, err)
	}
	rawInfo, err := infoCommand.Result()
	if err != nil {
		return GroupMemberCacheSnapshot{}, fmt.Errorf("read group %d member info: %w", groupID, err)
	}

	byID := make(map[int64]model.GroupMember, len(rawMembers)+len(rawInfo))
	setIDs := make(map[int64]struct{}, len(rawMembers))
	infoIDs := make(map[int64]struct{}, len(rawInfo))
	for _, rawID := range rawMembers {
		userID, err := strconv.ParseInt(rawID, 10, 64)
		if err != nil || userID <= 0 {
			return GroupMemberCacheSnapshot{}, fmt.Errorf("invalid group member cache ID %q", rawID)
		}
		setIDs[userID] = struct{}{}
		byID[userID] = model.GroupMember{GroupID: groupID, UserID: userID}
	}
	for rawID, encoded := range rawInfo {
		userID, err := strconv.ParseInt(rawID, 10, 64)
		if err != nil || userID <= 0 {
			return GroupMemberCacheSnapshot{}, fmt.Errorf("invalid group member info cache ID %q", rawID)
		}
		var info cachedGroupMemberInfo
		if err := json.Unmarshal([]byte(encoded), &info); err != nil {
			return GroupMemberCacheSnapshot{}, fmt.Errorf("decode group %d member %d cache: %w", groupID, userID, err)
		}
		infoIDs[userID] = struct{}{}
		byID[userID] = model.GroupMember{GroupID: groupID, UserID: userID, Role: info.Role, MutedUntil: info.modelMutedUntil()}
	}
	missingSetIDs := make([]int64, 0)
	for userID := range infoIDs {
		if _, exists := setIDs[userID]; !exists {
			missingSetIDs = append(missingSetIDs, userID)
		}
	}
	missingInfoIDs := make([]int64, 0)
	for userID := range setIDs {
		if _, exists := infoIDs[userID]; !exists {
			missingInfoIDs = append(missingInfoIDs, userID)
		}
	}
	members := make([]model.GroupMember, 0, len(byID))
	for _, member := range byID {
		members = append(members, member)
	}
	sort.Slice(members, func(i, j int) bool { return members[i].UserID < members[j].UserID })

	reverseIDs, err := r.readReverseGroupMembers(ctx, groupID)
	if err != nil {
		return GroupMemberCacheSnapshot{}, err
	}
	memberIDs := make(map[int64]struct{}, len(members))
	for _, member := range members {
		memberIDs[member.UserID] = struct{}{}
	}
	reverseSet := make(map[int64]struct{}, len(reverseIDs))
	for _, userID := range reverseIDs {
		reverseSet[userID] = struct{}{}
	}
	missingReverse := make([]int64, 0)
	for userID := range memberIDs {
		if _, found := reverseSet[userID]; !found {
			missingReverse = append(missingReverse, userID)
		}
	}
	unexpectedReverse := make([]int64, 0)
	for userID := range reverseSet {
		if _, found := memberIDs[userID]; !found {
			unexpectedReverse = append(unexpectedReverse, userID)
		}
	}
	return GroupMemberCacheSnapshot{
		Loaded: loaded, Members: members,
		MissingSetIDs:        sortedUniquePositive(missingSetIDs),
		MissingInfoIDs:       sortedUniquePositive(missingInfoIDs),
		MissingReverseIDs:    sortedUniquePositive(missingReverse),
		UnexpectedReverseIDs: sortedUniquePositive(unexpectedReverse),
	}, nil
}

func (r *RedisRepoImpl) readReverseGroupMembers(ctx context.Context, groupID int64) ([]int64, error) {
	const prefix = "user_groups:"
	groupIDString := strconv.FormatInt(groupID, 10)
	reverseIDs := make([]int64, 0)
	var cursor uint64
	for {
		keys, next, err := r.rdb.Scan(ctx, cursor, prefix+"*", 200).Result()
		if err != nil {
			return nil, fmt.Errorf("scan group %d reverse membership keys: %w", groupID, err)
		}
		pipe := r.rdb.Pipeline()
		commands := make(map[int64]*goredis.BoolCmd, len(keys))
		for _, key := range keys {
			userID, err := strconv.ParseInt(strings.TrimPrefix(key, prefix), 10, 64)
			if err != nil || userID <= 0 {
				return nil, fmt.Errorf("invalid reverse group membership key %q", key)
			}
			commands[userID] = pipe.SIsMember(ctx, key, groupIDString)
		}
		if len(commands) > 0 {
			if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, goredis.Nil) {
				return nil, fmt.Errorf("read group %d reverse memberships: %w", groupID, err)
			}
			for userID, command := range commands {
				present, err := command.Result()
				if err != nil {
					return nil, fmt.Errorf("read user %d reverse group %d membership: %w", userID, groupID, err)
				}
				if present {
					reverseIDs = append(reverseIDs, userID)
				}
			}
		}
		cursor = next
		if cursor == 0 {
			break
		}
	}
	return sortedUniquePositive(reverseIDs), nil
}

func (r *RedisRepoImpl) GetOnlineStates(ctx context.Context, userIDs []int64) (map[int64]bool, error) {
	states := make(map[int64]bool, len(userIDs))
	if len(userIDs) == 0 {
		return states, nil
	}
	keys := make([]string, len(userIDs))
	for i, userID := range userIDs {
		keys[i] = fmt.Sprintf("online:%d", userID)
	}
	values, err := r.rdb.MGet(ctx, keys...).Result()
	if err != nil {
		return nil, fmt.Errorf("batch read online states: %w", err)
	}
	for i, userID := range userIDs {
		states[userID] = values[i] != nil
	}
	return states, nil
}

func (r *RedisRepoImpl) scanNumericSuffixes(ctx context.Context, prefix string) ([]int64, error) {
	ids := make([]int64, 0)
	var cursor uint64
	for {
		keys, next, err := r.rdb.Scan(ctx, cursor, prefix+"*", 200).Result()
		if err != nil {
			return nil, err
		}
		for _, key := range keys {
			id, err := strconv.ParseInt(strings.TrimPrefix(key, prefix), 10, 64)
			if err != nil || id <= 0 {
				return nil, fmt.Errorf("invalid numeric cache key %q", key)
			}
			ids = append(ids, id)
		}
		cursor = next
		if cursor == 0 {
			break
		}
	}
	return sortedUniquePositive(ids), nil
}

func parseNumericMembers(raw []string) ([]int64, error) {
	ids := make([]int64, 0, len(raw))
	for _, value := range raw {
		id, err := strconv.ParseInt(value, 10, 64)
		if err != nil || id <= 0 {
			return nil, fmt.Errorf("invalid numeric cache member %q", value)
		}
		ids = append(ids, id)
	}
	return sortedUniquePositive(ids), nil
}

func sortedUniquePositive(ids []int64) []int64 {
	unique := make(map[int64]struct{}, len(ids))
	for _, id := range ids {
		if id > 0 {
			unique[id] = struct{}{}
		}
	}
	result := make([]int64, 0, len(unique))
	for id := range unique {
		result = append(result, id)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}

var _ RelationshipCacheRepository = (*RedisRepoImpl)(nil)
