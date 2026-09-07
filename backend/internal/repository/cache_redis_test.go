package repository

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCachedGroupMemberInfoStoresMuteDeadlineAsUnixMilliseconds(t *testing.T) {
	deadline := time.Date(2026, time.August, 31, 12, 34, 56, 789_000_000, time.FixedZone("CST", 8*60*60))
	info := newCachedGroupMemberInfo(2, &deadline)

	require.Equal(t, deadline.UnixMilli(), info.MutedUntil)
	encoded, err := json.Marshal(info)
	require.NoError(t, err)
	require.JSONEq(t, `{"role":2,"muted_until":1788150896789}`, string(encoded))
	require.NotContains(t, string(encoded), `"muted":`)

	restored := info.modelMutedUntil()
	require.NotNil(t, restored)
	require.Equal(t, deadline.UnixMilli(), restored.UnixMilli())
}

func TestCachedGroupMemberInfoOmitsEmptyMuteDeadline(t *testing.T) {
	encoded, err := json.Marshal(newCachedGroupMemberInfo(0, nil))
	require.NoError(t, err)
	require.JSONEq(t, `{"role":0}`, string(encoded))
}

func TestRelationshipReplacementLuaNeverUsesRedisKeysCommand(t *testing.T) {
	scripts := map[string]string{
		"friend owner": replaceFriendOwnerLua,
		"group owner":  replaceGroupMembersOwnerLua,
	}
	for name, script := range scripts {
		t.Run(name, func(t *testing.T) {
			require.NotContains(t, strings.ToUpper(script), "REDIS.CALL('KEYS'")
		})
	}
}

func TestRelationshipReplacementLuaUsesBoundedOwnerSets(t *testing.T) {
	require.Contains(t, replaceFriendOwnerLua, "redis.call('SMEMBERS', KEYS[2])")
	require.Contains(t, replaceFriendOwnerLua, "redis.call('SADD', KEYS[2], friendID)")
	require.Contains(t, replaceGroupMembersOwnerLua, "redis.call('SMEMBERS', KEYS[1])")
	require.Contains(t, replaceGroupMembersOwnerLua, "redis.call('SMEMBERS', KEYS[4])")
	require.Contains(t, replaceGroupMembersOwnerLua, "redis.call('SADD', KEYS[4], userID)")
	require.Contains(t, replaceGroupMembersOwnerLua, "redis.call('SET', KEYS[3], desiredCount)")
	require.Contains(t, replaceGroupMembersOwnerLua, "redis.call('SET', KEYS[5], '1')")
}

func TestGroupMembersLoadedLuaValidatesBothAuthorizationStructures(t *testing.T) {
	require.Contains(t, groupMembersLoadedLua, "tonumber(redis.call('GET', KEYS[1]))")
	require.Contains(t, groupMembersLoadedLua, "redis.call('SCARD', KEYS[2]) ~= expectedCount")
	require.Contains(t, groupMembersLoadedLua, "redis.call('HLEN', KEYS[3]) ~= expectedCount")
}
