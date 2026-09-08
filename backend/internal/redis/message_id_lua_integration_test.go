package redis_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	goredis "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"

	"my-im/internal/messageid"
	redisscripts "my-im/internal/redis"
	"my-im/internal/repository"
)

// Run with Docker Redis:
//
//	MYIM_INTEGRATION=1 go test ./internal/redis -run TestMessageLuaEchoesBrowserSafeIDAndIndependentTime -v
func TestMessageLuaEchoesBrowserSafeIDAndIndependentTime(t *testing.T) {
	if os.Getenv("MYIM_INTEGRATION") != "1" {
		t.Skip("set MYIM_INTEGRATION=1 with Docker Redis running")
	}

	ctx := context.Background()
	client := goredis.NewClient(&goredis.Options{Addr: "127.0.0.1:16379"})
	require.NoError(t, client.Ping(ctx).Err())
	t.Cleanup(func() { require.NoError(t, client.Close()) })

	stamp := time.Now().UnixNano()
	senderID := stamp
	receiverID := stamp + 1
	groupID := stamp + 2
	privateClientID := fmt.Sprintf("message-id-private-%d", stamp)
	groupClientID := fmt.Sprintf("message-id-group-%d", stamp)
	keys := []string{
		fmt.Sprintf("friend:%d:%d", senderID, receiverID),
		fmt.Sprintf("friend:%d:%d", receiverID, senderID),
		fmt.Sprintf("msg_dedup:%d:%s", senderID, privateClientID),
		fmt.Sprintf("msg_dedup:%d:%s", senderID, groupClientID),
		fmt.Sprintf("group_members:%d", groupID),
		fmt.Sprintf("group_member_info:%d", groupID),
		fmt.Sprintf("group_seq:%d", groupID),
	}
	t.Cleanup(func() { _ = client.Del(context.Background(), keys...).Err() })
	require.NoError(t, client.Set(ctx, keys[0], "1", 0).Err())
	require.NoError(t, client.Set(ctx, keys[1], "1", 0).Err())
	require.NoError(t, client.SAdd(ctx, keys[4], senderID).Err())
	require.NoError(t, client.HSet(ctx, keys[5], senderID, `{"role":0}`).Err())

	const privateID int64 = 9_007_199_254_740_991
	before, err := client.Time(ctx).Result()
	require.NoError(t, err)
	privateResult, err := redisscripts.ExecPrivateMsgCheck(client, ctx, senderID, receiverID, privateClientID, privateID)
	require.NoError(t, err)
	after, err := client.Time(ctx).Result()
	require.NoError(t, err)
	require.Equal(t, privateID, privateResult.MsgID)
	requireTimestampBetween(t, privateResult.Timestamp, before, after)
	require.NotEqual(t, privateResult.MsgID/1000, privateResult.Timestamp,
		"timestamp must not be derived from the opaque message ID")

	const groupMessageID int64 = privateID - 1
	before, err = client.Time(ctx).Result()
	require.NoError(t, err)
	groupResult, err := redisscripts.ExecGroupMsgCheck(client, ctx, groupID, senderID, groupClientID, groupMessageID)
	require.NoError(t, err)
	after, err = client.Time(ctx).Result()
	require.NoError(t, err)
	require.Equal(t, groupMessageID, groupResult.MsgID)
	require.EqualValues(t, 1, groupResult.GroupSeq)
	requireTimestampBetween(t, groupResult.Timestamp, before, after)

	for index, invalidID := range []int64{0, messageid.MaxID + 1} {
		invalidPrivateClientID := fmt.Sprintf("message-id-invalid-private-%d-%d", stamp, index)
		_, invalidErr := redisscripts.ExecPrivateMsgCheck(
			client, ctx, senderID, receiverID, invalidPrivateClientID, invalidID)
		require.Error(t, invalidErr)
		require.Zero(t, client.Exists(ctx,
			fmt.Sprintf("msg_dedup:%d:%s", senderID, invalidPrivateClientID)).Val())

		invalidGroupClientID := fmt.Sprintf("message-id-invalid-group-%d-%d", stamp, index)
		_, invalidErr = redisscripts.ExecGroupMsgCheck(
			client, ctx, groupID, senderID, invalidGroupClientID, invalidID)
		require.Error(t, invalidErr)
		require.Zero(t, client.Exists(ctx,
			fmt.Sprintf("msg_dedup:%d:%s", senderID, invalidGroupClientID)).Val())
		sequence, sequenceErr := client.Get(ctx, keys[6]).Int64()
		require.NoError(t, sequenceErr)
		require.EqualValues(t, 1, sequence)
	}

	const adapterMessageID int64 = privateID - 2
	adapterClientID := fmt.Sprintf("message-id-adapter-%d", stamp)
	keys = append(keys, fmt.Sprintf("msg_dedup:%d:%s", senderID, adapterClientID))
	repo := repository.NewRedisRepo(client,
		repository.WithMessageIDGenerator(fixedMessageIDs{id: adapterMessageID}))
	before, err = client.Time(ctx).Result()
	require.NoError(t, err)
	adapterResult, err := repo.ExecPrivateMsgCheck(ctx, senderID, receiverID, adapterClientID)
	require.NoError(t, err)
	after, err = client.Time(ctx).Result()
	require.NoError(t, err)
	require.Equal(t, adapterMessageID, adapterResult.MessageID)
	requireTimestampBetween(t, adapterResult.Timestamp, before, after)
}

type fixedMessageIDs struct{ id int64 }

func (g fixedMessageIDs) Next(ctx context.Context) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	return g.id, nil
}

func requireTimestampBetween(t *testing.T, timestamp int64, before, after time.Time) {
	t.Helper()
	require.GreaterOrEqual(t, timestamp, before.UnixMilli())
	require.LessOrEqual(t, timestamp, after.UnixMilli())
}
