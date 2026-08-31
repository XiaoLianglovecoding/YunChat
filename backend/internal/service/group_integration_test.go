package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"my-im/internal/apperror"
	"my-im/internal/config"
	"my-im/internal/infra"
	"my-im/internal/migrate"
	"my-im/internal/model"
	"my-im/internal/repository"
)

// Run with local Docker MySQL:
//
//	MYIM_INTEGRATION=1 go test ./internal/service -run TestGroupDockerIntegration -v
func TestGroupDockerIntegration(t *testing.T) {
	if os.Getenv("MYIM_INTEGRATION") != "1" {
		t.Skip("set MYIM_INTEGRATION=1 with Docker MySQL running")
	}

	ctx := context.Background()
	db, err := infra.OpenMySQL(ctx, config.MySQLConfig{
		Host: "127.0.0.1", Port: 13306, User: "my_im", Password: "my_im123", DBName: "my_im",
		ConnectTimeoutMS: 3000, QueryTimeoutMS: 3000, MaxOpenConns: 5, MaxIdleConns: 2,
	})
	require.NoError(t, err)
	defer db.Close()
	require.NoError(t, migrate.New(db, "../../scripts/migrations").Up(ctx))

	var ownerID, adminID, memberID, outsiderID int64
	var groupID int64
	var ownerOnlyGroupID int64
	defer func() {
		cleanupCtx := context.Background()
		_, _ = db.ExecContext(cleanupCtx, `DELETE FROM cache_reconcile_events
			WHERE resource_type = 'group_members' AND resource_id IN (?, ?)`, groupID, ownerOnlyGroupID)
		_, _ = db.ExecContext(cleanupCtx,
			`DELETE FROM group_members WHERE group_id IN (?, ?)`, groupID, ownerOnlyGroupID)
		_, _ = db.ExecContext(cleanupCtx,
			"DELETE FROM `groups` WHERE id IN (?, ?)", groupID, ownerOnlyGroupID)
		_, _ = db.ExecContext(cleanupCtx,
			`DELETE FROM users WHERE id IN (?, ?, ?, ?)`, ownerID, adminID, memberID, outsiderID)
	}()

	stamp := time.Now().UnixNano()
	ownerID = insertGroupIntegrationUser(t, ctx, db, fmt.Sprintf("group_it_owner_%d", stamp))
	adminID = insertGroupIntegrationUser(t, ctx, db, fmt.Sprintf("group_it_admin_%d", stamp))
	memberID = insertGroupIntegrationUser(t, ctx, db, fmt.Sprintf("group_it_member_%d", stamp))
	outsiderID = insertGroupIntegrationUser(t, ctx, db, fmt.Sprintf("group_it_out_%d", stamp))

	mysqlRepo := repository.NewMySQLRepo(db)
	groups, err := NewGroupService(mysqlRepo)
	require.NoError(t, err)

	groupID, err = groups.Create(ctx, ownerID, "  Integration Group  ", "  initial notice  ")
	require.NoError(t, err)
	require.Positive(t, groupID)

	// One transaction must commit the group, its role=owner membership, and the
	// durable cache-reconciliation request. None of these assertions uses Redis.
	var storedName, storedNotice string
	var storedOwnerID int64
	var maxMembers int
	require.NoError(t, db.QueryRowContext(ctx, "SELECT name, COALESCE(notice, ''), owner_id, max_members "+
		"FROM `groups` WHERE id = ?", groupID).Scan(
		&storedName, &storedNotice, &storedOwnerID, &maxMembers,
	))
	require.Equal(t, "Integration Group", storedName)
	require.Equal(t, "initial notice", storedNotice)
	require.Equal(t, ownerID, storedOwnerID)
	require.Equal(t, 500, maxMembers)

	var ownerRole int
	var memberRows int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT role FROM group_members
		WHERE group_id = ? AND user_id = ?`, groupID, ownerID).Scan(&ownerRole))
	require.Equal(t, model.GroupRoleOwner, ownerRole)
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM group_members WHERE group_id = ?`, groupID).Scan(&memberRows))
	require.Equal(t, 1, memberRows)

	var reconcileEvents int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM cache_reconcile_events
		WHERE resource_type = 'group_members' AND resource_id = ?`, groupID).Scan(&reconcileEvents))
	require.Equal(t, 1, reconcileEvents)

	// GROUP-002/003 are outside this test, so seed the two roles directly. The
	// GROUP-001 service must still enforce those roles when reading/updating.
	_, err = db.ExecContext(ctx, `INSERT INTO group_members(group_id,user_id,role)
		VALUES(?,?,?),(?,?,?)`,
		groupID, adminID, model.GroupRoleAdmin,
		groupID, memberID, model.GroupRoleMember,
	)
	require.NoError(t, err)

	// owner_id alone is not membership. This deliberately incomplete group must
	// never leak into the member's list because the list is driven by the join
	// through group_members rather than groups.owner_id or Redis user_groups.
	result, err := db.ExecContext(ctx,
		"INSERT INTO `groups`(name,notice,owner_id) VALUES(?,?,?)",
		fmt.Sprintf("owner-only-%d", stamp), "must stay hidden", memberID,
	)
	require.NoError(t, err)
	ownerOnlyGroupID, err = result.LastInsertId()
	require.NoError(t, err)

	memberGroups, err := groups.ListByUser(ctx, memberID)
	require.NoError(t, err)
	require.Len(t, memberGroups, 1)
	require.Equal(t, groupID, memberGroups[0].ID)
	for _, group := range memberGroups {
		require.NotEqual(t, ownerOnlyGroupID, group.ID)
	}

	detail, err := groups.Get(ctx, memberID, groupID)
	require.NoError(t, err)
	require.NotNil(t, detail)
	require.Equal(t, groupID, detail.ID)
	require.Equal(t, ownerID, detail.OwnerID)

	_, err = groups.Get(ctx, outsiderID, groupID)
	requireGroupIntegrationErrorCode(t, err, apperror.CodeGroupNotMember)

	err = groups.Update(ctx, memberID, groupID, "member overwrite", "not allowed")
	requireGroupIntegrationErrorCode(t, err, apperror.CodeNotOwnerOrAdmin)
	requireGroupIntegrationProfile(t, ctx, db, groupID, "Integration Group", "initial notice")

	require.NoError(t, groups.Update(ctx, adminID, groupID, "  Admin Updated  ", "  admin notice  "))
	requireGroupIntegrationProfile(t, ctx, db, groupID, "Admin Updated", "admin notice")

	// Authorization uses the real groups.owner_id together with the owner's
	// membership. The owner remains allowed after an administrator's update.
	require.NoError(t, groups.Update(ctx, ownerID, groupID, "  Owner Updated  ", "  owner notice  "))
	requireGroupIntegrationProfile(t, ctx, db, groupID, "Owner Updated", "owner notice")
}

func insertGroupIntegrationUser(t *testing.T, ctx context.Context, db *sql.DB, username string) int64 {
	t.Helper()
	result, err := db.ExecContext(ctx,
		`INSERT INTO users(username,password_hash,nickname) VALUES(?,?,?)`, username, "hash", username)
	require.NoError(t, err)
	userID, err := result.LastInsertId()
	require.NoError(t, err)
	return userID
}

func requireGroupIntegrationErrorCode(t *testing.T, err error, code apperror.Code) {
	t.Helper()
	require.Error(t, err)
	var applicationError *apperror.Error
	require.True(t, errors.As(err, &applicationError))
	require.Equal(t, code, applicationError.Code)
}

func requireGroupIntegrationProfile(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	groupID int64,
	wantName string,
	wantNotice string,
) {
	t.Helper()
	var name, notice string
	require.NoError(t, db.QueryRowContext(ctx,
		"SELECT name, COALESCE(notice, '') FROM `groups` WHERE id = ?", groupID,
	).Scan(&name, &notice))
	require.Equal(t, wantName, name)
	require.Equal(t, wantNotice, notice)
}
