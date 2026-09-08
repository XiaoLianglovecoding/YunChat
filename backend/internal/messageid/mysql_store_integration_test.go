package messageid_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"my-im/internal/config"
	"my-im/internal/infra"
	"my-im/internal/messageid"
	"my-im/internal/migrate"
)

// Run with Docker MySQL:
//
//	MYIM_INTEGRATION=1 go test ./internal/messageid -run TestMySQLStoreDockerIntegration -v
func TestMySQLStoreDockerIntegration(t *testing.T) {
	if os.Getenv("MYIM_INTEGRATION") != "1" {
		t.Skip("set MYIM_INTEGRATION=1 with Docker MySQL running")
	}
	ctx := context.Background()
	dbA := openMessageIDTestDB(t, ctx)
	dbB := openMessageIDTestDB(t, ctx)
	require.NoError(t, migrate.New(dbA, "../../scripts/migrations").Up(ctx))

	t.Run("migration initializes after every historical reference", func(t *testing.T) {
		suffix := fmt.Sprintf("%d", time.Now().UnixNano()%10_000_000_000)
		tables := map[string]string{
			"message_id_allocators": "it_mid_alloc_" + suffix,
			"private_messages":      "it_mid_private_" + suffix,
			"group_messages":        "it_mid_group_" + suffix,
			"msg_revoked":           "it_mid_revoked_" + suffix,
			"message_user_states":   "it_mid_states_" + suffix,
		}
		t.Cleanup(func() {
			_, cleanupErr := dbA.ExecContext(context.Background(), fmt.Sprintf(
				"DROP TABLE IF EXISTS `%s`,`%s`,`%s`,`%s`,`%s`",
				tables["message_id_allocators"], tables["private_messages"], tables["group_messages"],
				tables["msg_revoked"], tables["message_user_states"],
			))
			require.NoError(t, cleanupErr)
		})
		_, err := dbA.ExecContext(ctx, fmt.Sprintf(`
CREATE TABLE %[1]s(id BIGINT PRIMARY KEY);
CREATE TABLE %[2]s(id BIGINT PRIMARY KEY);
CREATE TABLE %[3]s(msg_id BIGINT NOT NULL);
CREATE TABLE %[4]s(msg_id BIGINT NOT NULL);
INSERT INTO %[1]s(id) VALUES(11);
INSERT INTO %[2]s(id) VALUES(22);
INSERT INTO %[3]s(msg_id) VALUES(33);
INSERT INTO %[4]s(msg_id) VALUES(44);`,
			tables["private_messages"], tables["group_messages"], tables["msg_revoked"], tables["message_user_states"]))
		require.NoError(t, err)

		migrationSQL, err := os.ReadFile("../../scripts/migrations/014_message_id_allocator.sql")
		require.NoError(t, err)
		shadowSQL := string(migrationSQL)
		for original, shadow := range tables {
			shadowSQL = strings.ReplaceAll(shadowSQL, original, shadow)
		}
		shadowSQL = strings.ReplaceAll(shadowSQL, "chk_message_id_next", "chk_mid_next_"+suffix)
		shadowSQL = strings.ReplaceAll(shadowSQL, "chk_private_message_id", "chk_mid_private_"+suffix)
		shadowSQL = strings.ReplaceAll(shadowSQL, "chk_group_message_id", "chk_mid_group_"+suffix)
		_, err = dbA.ExecContext(ctx, shadowSQL)
		require.NoError(t, err)

		var next int64
		require.NoError(t, dbA.QueryRowContext(ctx, fmt.Sprintf(
			"SELECT next_id FROM `%s` WHERE namespace='message'", tables["message_id_allocators"])).Scan(&next))
		require.EqualValues(t, 45, next)
		_, err = dbA.ExecContext(ctx, fmt.Sprintf(
			"INSERT INTO `%s`(namespace,next_id) VALUES('overflow',9007199254740993)", tables["message_id_allocators"]))
		require.Error(t, err, "the migration CHECK must reject IDs above the browser-safe exhausted sentinel")
	})

	t.Run("independent database pools reserve disjoint segments", func(t *testing.T) {
		namespace := createAllocatorNamespace(t, ctx, dbA, 1)
		storeA, err := messageid.NewMySQLStore(dbA, namespace)
		require.NoError(t, err)
		storeB, err := messageid.NewMySQLStore(dbB, namespace)
		require.NoError(t, err)
		generatorA, err := messageid.New(storeA, messageid.Options{SegmentSize: 257})
		require.NoError(t, err)
		generatorB, err := messageid.New(storeB, messageid.Options{SegmentSize: 257})
		require.NoError(t, err)

		const perInstance = 20_000
		ids := make(chan int64, 2*perInstance)
		errs := make(chan error, 2)
		var workers sync.WaitGroup
		for _, generator := range []*messageid.Generator{generatorA, generatorB} {
			workers.Add(1)
			go func(generator *messageid.Generator) {
				defer workers.Done()
				for range perInstance {
					id, nextErr := generator.Next(ctx)
					if nextErr != nil {
						errs <- nextErr
						return
					}
					ids <- id
				}
			}(generator)
		}
		workers.Wait()
		close(ids)
		close(errs)
		for err := range errs {
			require.NoError(t, err)
		}
		seen := make(map[int64]struct{}, 2*perInstance)
		for id := range ids {
			_, duplicate := seen[id]
			require.Falsef(t, duplicate, "duplicate message ID %d", id)
			seen[id] = struct{}{}
		}
		require.Len(t, seen, 2*perInstance)
	})

	t.Run("restart abandons rather than reuses IDs", func(t *testing.T) {
		namespace := createAllocatorNamespace(t, ctx, dbA, 1)
		store, err := messageid.NewMySQLStore(dbA, namespace)
		require.NoError(t, err)
		beforeRestart, err := messageid.New(store, messageid.Options{SegmentSize: 100})
		require.NoError(t, err)
		first, err := beforeRestart.Next(ctx)
		require.NoError(t, err)
		require.EqualValues(t, 1, first)

		afterRestartStore, err := messageid.NewMySQLStore(dbB, namespace)
		require.NoError(t, err)
		afterRestart, err := messageid.New(afterRestartStore, messageid.Options{SegmentSize: 100})
		require.NoError(t, err)
		next, err := afterRestart.Next(ctx)
		require.NoError(t, err)
		require.EqualValues(t, 101, next)
	})

	t.Run("browser safe boundary fails closed", func(t *testing.T) {
		namespace := createAllocatorNamespace(t, ctx, dbA, messageid.MaxID-1)
		store, err := messageid.NewMySQLStore(dbA, namespace)
		require.NoError(t, err)
		generator, err := messageid.New(store, messageid.Options{SegmentSize: messageid.DefaultSegmentSize})
		require.NoError(t, err)
		first, err := generator.Next(ctx)
		require.NoError(t, err)
		second, err := generator.Next(ctx)
		require.NoError(t, err)
		require.EqualValues(t, messageid.MaxID-1, first)
		require.EqualValues(t, messageid.MaxID, second)
		_, err = generator.Next(ctx)
		require.ErrorIs(t, err, messageid.ErrExhausted)
	})

	t.Run("missing namespace and canceled context fail closed", func(t *testing.T) {
		missing := fmt.Sprintf("it_missing_%d", time.Now().UnixNano())
		store, err := messageid.NewMySQLStore(dbA, missing)
		require.NoError(t, err)
		_, err = store.Reserve(ctx, 1)
		require.Error(t, err)

		namespace := createAllocatorNamespace(t, ctx, dbA, 50)
		store, err = messageid.NewMySQLStore(dbA, namespace)
		require.NoError(t, err)
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		_, err = store.Reserve(canceled, 1)
		require.True(t, errors.Is(err, context.Canceled), err)
		var next int64
		require.NoError(t, dbA.QueryRowContext(ctx,
			`SELECT next_id FROM message_id_allocators WHERE namespace = ?`, namespace).Scan(&next))
		require.EqualValues(t, 50, next)
	})
}

func openMessageIDTestDB(t *testing.T, ctx context.Context) *sql.DB {
	t.Helper()
	db, err := infra.OpenMySQL(ctx, config.MySQLConfig{
		Host: "127.0.0.1", Port: 13306, User: "my_im", Password: "my_im123", DBName: "my_im",
		ConnectTimeoutMS: 3000, QueryTimeoutMS: 3000, MaxOpenConns: 8, MaxIdleConns: 2,
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	return db
}

func createAllocatorNamespace(t *testing.T, ctx context.Context, db *sql.DB, next int64) string {
	t.Helper()
	namespace := fmt.Sprintf("it_%d", time.Now().UnixNano())
	_, err := db.ExecContext(ctx,
		`INSERT INTO message_id_allocators(namespace,next_id) VALUES(?,?)`, namespace, next)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, cleanupErr := db.ExecContext(context.Background(),
			`DELETE FROM message_id_allocators WHERE namespace = ?`, namespace)
		require.NoError(t, cleanupErr)
	})
	return namespace
}
