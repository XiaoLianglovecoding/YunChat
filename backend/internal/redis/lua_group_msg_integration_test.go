package redis

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	goredis "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

// Run with Docker Redis:
//
//	MYIM_INTEGRATION=1 go test ./internal/redis -run TestGroupMuteExpiresWithoutCacheRewrite -v
func TestGroupMuteExpiresWithoutCacheRewrite(t *testing.T) {
	if os.Getenv("MYIM_INTEGRATION") != "1" {
		t.Skip("set MYIM_INTEGRATION=1 with Docker Redis running")
	}

	ctx := context.Background()
	client := goredis.NewClient(&goredis.Options{Addr: "127.0.0.1:16379"})
	require.NoError(t, client.Ping(ctx).Err())
	t.Cleanup(func() { require.NoError(t, client.Close()) })

	stamp := time.Now().UnixNano()
	groupID := stamp
	senderID := stamp + 1
	clientMsgID := fmt.Sprintf("mute-expiry-%d", stamp)
	missingInfoClientMsgID := clientMsgID + "-missing-info"
	memberKey := fmt.Sprintf("group_members:%d", groupID)
	infoKey := fmt.Sprintf("group_member_info:%d", groupID)
	dedupKey := fmt.Sprintf("msg_dedup:%d:%s", senderID, clientMsgID)
	groupSequenceKey := fmt.Sprintf("group_seq:%d", groupID)
	t.Cleanup(func() {
		_ = client.Del(context.Background(), memberKey, infoKey, dedupKey,
			fmt.Sprintf("msg_dedup:%d:%s", senderID, missingInfoClientMsgID), groupSequenceKey).Err()
	})

	redisNow, err := client.Time(ctx).Result()
	require.NoError(t, err)
	mutedUntil := redisNow.Add(900 * time.Millisecond).UnixMilli()
	require.NoError(t, client.SAdd(ctx, memberKey, senderID).Err())
	require.NoError(t, client.HSet(ctx, infoKey, senderID,
		fmt.Sprintf(`{"role":0,"muted_until":%d}`, mutedUntil),
	).Err())

	beforeExpiry, err := ExecGroupMsgCheck(client, ctx, groupID, senderID, clientMsgID)
	require.NoError(t, err)
	require.Equal(t, GMErrMuted, beforeExpiry.ErrCode)
	dedupExists, err := client.Exists(ctx, dedupKey).Result()
	require.NoError(t, err)
	require.Zero(t, dedupExists, "a rejected muted message must not consume its client_msg_id")
	groupSequenceExists, err := client.Exists(ctx, groupSequenceKey).Result()
	require.NoError(t, err)
	require.Zero(t, groupSequenceExists, "a rejected muted message must not allocate group_seq")

	// Do not rewrite group_member_info. The Lua script must observe Redis TIME
	// passing the cached deadline and allow the same member automatically.
	require.Eventually(t, func() bool {
		afterExpiry, execErr := ExecGroupMsgCheck(client, ctx, groupID, senderID, clientMsgID)
		return execErr == nil && afterExpiry.ErrCode == GMErrOK
	}, 3*time.Second, 50*time.Millisecond)

	stored, err := client.HGet(ctx, infoKey, fmt.Sprintf("%d", senderID)).Result()
	require.NoError(t, err)
	require.Contains(t, stored, fmt.Sprintf(`"muted_until":%d`, mutedUntil))
	dedupExists, err = client.Exists(ctx, dedupKey).Result()
	require.NoError(t, err)
	require.EqualValues(t, 1, dedupExists, "the same client_msg_id is accepted after mute expiry")
	groupSequence, err := client.Get(ctx, groupSequenceKey).Int64()
	require.NoError(t, err)
	require.EqualValues(t, 1, groupSequence)

	// A Hash field can be evicted independently after EnsureGroupAccess returns.
	// The authorization Lua is the final guard and must fail closed before it
	// writes dedup/sequence state.
	require.NoError(t, client.HDel(ctx, infoKey, fmt.Sprint(senderID)).Err())
	missingInfo, err := ExecGroupMsgCheck(client, ctx, groupID, senderID, missingInfoClientMsgID)
	require.NoError(t, err)
	require.Equal(t, GMErrNotMember, missingInfo.ErrCode)
	missingInfoDedupExists, err := client.Exists(ctx,
		fmt.Sprintf("msg_dedup:%d:%s", senderID, missingInfoClientMsgID)).Result()
	require.NoError(t, err)
	require.Zero(t, missingInfoDedupExists)
	groupSequence, err = client.Get(ctx, groupSequenceKey).Int64()
	require.NoError(t, err)
	require.EqualValues(t, 1, groupSequence)
}
