package service

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"my-im/internal/model"
	"my-im/internal/repository"
)

type CacheScope string

const (
	CacheScopeFriends    CacheScope = "friends"
	CacheScopeBlacklists CacheScope = "blacklists"
	CacheScopeGroups     CacheScope = "groups"
	CacheScopeAll        CacheScope = "all"
)

func (scope CacheScope) Valid() bool {
	switch scope {
	case CacheScopeFriends, CacheScopeBlacklists, CacheScopeGroups, CacheScopeAll:
		return true
	default:
		return false
	}
}

var ErrCacheWarmTimeout = errors.New("relationship cache warm-up timed out")

// CacheTruthError preserves whether a failure came from the durable truth or
// Redis. Callers must return an error instead of interpreting a Redis failure as
// “not friends” / “not blocked” / “not a group member”.
type CacheTruthError struct {
	Layer      string
	Operation  string
	Resource   repository.CacheResource
	ResourceID int64
	Cause      error
}

func (e *CacheTruthError) Error() string {
	return fmt.Sprintf("%s %s %s:%d: %v", e.Layer, e.Operation, e.Resource, e.ResourceID, e.Cause)
}

func (e *CacheTruthError) Unwrap() error { return e.Cause }

type CacheTruthOptions struct {
	LockTTL      time.Duration
	WaitTimeout  time.Duration
	PollInterval time.Duration
	PageSize     int
}

type CacheTruthService struct {
	truth        repository.CacheTruthRepository
	cache        repository.RelationshipCacheRepository
	lockTTL      time.Duration
	waitTimeout  time.Duration
	pollInterval time.Duration
	pageSize     int
}

func NewCacheTruthService(
	truth repository.CacheTruthRepository,
	cache repository.RelationshipCacheRepository,
	options CacheTruthOptions,
) *CacheTruthService {
	// Keep construction simple for cmd/server wiring, but fail immediately with a
	// useful message instead of a distant nil-interface panic on the first request.
	if truth == nil {
		panic("NewCacheTruthService: nil MySQL truth repository")
	}
	if cache == nil {
		panic("NewCacheTruthService: nil Redis relationship cache repository")
	}
	if options.LockTTL <= 0 {
		options.LockTTL = 5 * time.Second
	}
	if options.WaitTimeout <= 0 {
		options.WaitTimeout = 2 * time.Second
	}
	if options.PollInterval <= 0 {
		options.PollInterval = 20 * time.Millisecond
	}
	if options.PageSize <= 0 {
		options.PageSize = 500
	}
	return &CacheTruthService{
		truth: truth, cache: cache,
		lockTTL: options.LockTTL, waitTimeout: options.WaitTimeout,
		pollInterval: options.PollInterval, pageSize: options.PageSize,
	}
}

// EnsurePrivateAccess prepares every key read by private_msg_check.lua. It must
// run before that Lua script; clearing Redis then simply causes a MySQL reload.
func (s *CacheTruthService) EnsurePrivateAccess(ctx context.Context, senderID, receiverID int64) error {
	if senderID <= 0 || receiverID <= 0 {
		return errors.New("ensure private access: user IDs must be positive")
	}
	for _, ownerID := range []int64{senderID, receiverID} {
		if err := s.ensureLoaded(ctx, repository.CacheResourceFriends, ownerID); err != nil {
			return err
		}
		if err := s.ensureLoaded(ctx, repository.CacheResourceBlacklist, ownerID); err != nil {
			return err
		}
	}
	return nil
}

// EnsureGroupAccess prepares group membership and mute metadata before the
// group-message Lua check.
func (s *CacheTruthService) EnsureGroupAccess(ctx context.Context, groupID int64) error {
	if groupID <= 0 {
		return errors.New("ensure group access: group ID must be positive")
	}
	return s.ensureLoaded(ctx, repository.CacheResourceGroupMembers, groupID)
}

func (s *CacheTruthService) ensureLoaded(ctx context.Context, resource repository.CacheResource, resourceID int64) error {
	loaded, err := s.isLoaded(ctx, resource, resourceID)
	if err != nil {
		return err
	}
	if loaded {
		return nil
	}

	return s.withResourceLock(ctx, resource, resourceID, "wait for cache warm-up", func() error {
		return s.loadIfStillMissing(ctx, resource, resourceID)
	})
}

func (s *CacheTruthService) loadIfStillMissing(ctx context.Context, resource repository.CacheResource, resourceID int64) error {
	loaded, err := s.isLoaded(ctx, resource, resourceID)
	if err != nil || loaded {
		return err
	}
	return s.reconcileCurrent(ctx, resource, resourceID)
}

func (s *CacheTruthService) isLoaded(ctx context.Context, resource repository.CacheResource, resourceID int64) (bool, error) {
	var (
		loaded bool
		err    error
	)
	switch resource {
	case repository.CacheResourceFriends:
		loaded, err = s.cache.FriendsLoaded(ctx, resourceID)
	case repository.CacheResourceBlacklist:
		loaded, err = s.cache.BlacklistLoaded(ctx, resourceID)
	case repository.CacheResourceGroupMembers:
		loaded, err = s.cache.GroupMembersLoaded(ctx, resourceID)
	default:
		return false, fmt.Errorf("unknown cache resource %q", resource)
	}
	if err != nil {
		return false, s.redisError("read loaded marker", resource, resourceID, err)
	}
	return loaded, nil
}

// ReconcileNow rereads current MySQL state and overwrites the Redis projection.
// Rebuild workers and post-commit fast paths share the same resource lock. This
// prevents an older snapshot, whose MySQL read started first, from landing in
// Redis after a newer post-commit refresh.
func (s *CacheTruthService) ReconcileNow(ctx context.Context, resource repository.CacheResource, resourceID int64) error {
	if !resource.Valid() || resourceID <= 0 {
		return fmt.Errorf("reconcile cache: invalid resource %q:%d", resource, resourceID)
	}
	return s.withResourceLock(ctx, resource, resourceID, "wait for cache reconciliation", func() error {
		return s.reconcileCurrent(ctx, resource, resourceID)
	})
}

func (s *CacheTruthService) reconcileCurrent(ctx context.Context, resource repository.CacheResource, resourceID int64) error {
	err := s.truth.WithinCacheSnapshot(ctx, resource, resourceID,
		func(snapshotCtx context.Context, snapshot repository.CacheSnapshotRepository) error {
			switch resource {
			case repository.CacheResourceFriends:
				ids, err := snapshot.ListFriendIDsForCache(snapshotCtx, resourceID)
				if err != nil {
					return s.mysqlError("read authoritative snapshot", resource, resourceID, err)
				}
				if err := s.cache.ReplaceFriendOwner(snapshotCtx, resourceID, ids); err != nil {
					return s.redisError("replace projection", resource, resourceID, err)
				}
			case repository.CacheResourceBlacklist:
				ids, err := snapshot.ListBlockedIDsForCache(snapshotCtx, resourceID)
				if err != nil {
					return s.mysqlError("read authoritative snapshot", resource, resourceID, err)
				}
				if err := s.cache.ReplaceBlacklistOwner(snapshotCtx, resourceID, ids); err != nil {
					return s.redisError("replace projection", resource, resourceID, err)
				}
			case repository.CacheResourceGroupMembers:
				members, err := snapshot.ListGroupMembersForCache(snapshotCtx, resourceID)
				if err != nil {
					return s.mysqlError("read authoritative snapshot", resource, resourceID, err)
				}
				if err := s.cache.ReplaceGroupMembersOwner(snapshotCtx, resourceID, members); err != nil {
					return s.redisError("replace projection", resource, resourceID, err)
				}
			}
			return nil
		})
	if err != nil {
		var cacheErr *CacheTruthError
		if errors.As(err, &cacheErr) {
			return err
		}
		return s.mysqlError("serialize authoritative snapshot", resource, resourceID, err)
	}
	return nil
}

func (s *CacheTruthService) withResourceLock(
	ctx context.Context,
	resource repository.CacheResource,
	resourceID int64,
	waitOperation string,
	operation func() error,
) error {
	deadline := time.NewTimer(s.waitTimeout)
	defer deadline.Stop()
	ticker := time.NewTicker(s.pollInterval)
	defer ticker.Stop()

	for {
		token, acquired, err := s.cache.TryCacheWarmLock(ctx, resource, resourceID, s.lockTTL)
		if err != nil {
			return s.redisError("acquire resource lock", resource, resourceID, err)
		}
		if acquired {
			operationErr := operation()
			releaseErr := s.cache.ReleaseCacheWarmLock(ctx, resource, resourceID, token)
			if releaseErr != nil {
				releaseErr = s.redisError("release resource lock", resource, resourceID, releaseErr)
			}
			return errors.Join(operationErr, releaseErr)
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return &CacheTruthError{
				Layer: "redis", Operation: waitOperation, Resource: resource,
				ResourceID: resourceID, Cause: ErrCacheWarmTimeout,
			}
		case <-ticker.C:
		}
	}
}

// The four methods below implement FriendCacheWriter. They intentionally do
// not apply Redis deltas; each one reloads MySQL truth through ReconcileNow so
// the immediate path and the durable worker have identical ordering semantics.
func (s *CacheTruthService) SetFriendCache(ctx context.Context, firstID, secondID int64) error {
	return s.reconcileOwners(ctx, repository.CacheResourceFriends, firstID, secondID)
}

func (s *CacheTruthService) DeleteFriendCache(ctx context.Context, firstID, secondID int64) error {
	return s.reconcileOwners(ctx, repository.CacheResourceFriends, firstID, secondID)
}

func (s *CacheTruthService) SetBlacklistMember(ctx context.Context, ownerID, _ int64) error {
	return s.ReconcileNow(ctx, repository.CacheResourceBlacklist, ownerID)
}

func (s *CacheTruthService) DeleteBlacklistMember(ctx context.Context, ownerID, _ int64) error {
	return s.ReconcileNow(ctx, repository.CacheResourceBlacklist, ownerID)
}

func (s *CacheTruthService) reconcileOwners(
	ctx context.Context,
	resource repository.CacheResource,
	ownerIDs ...int64,
) error {
	seen := make(map[int64]struct{}, len(ownerIDs))
	var failures []error
	for _, ownerID := range ownerIDs {
		if ownerID <= 0 {
			failures = append(failures, fmt.Errorf("reconcile %s: invalid owner ID %d", resource, ownerID))
			continue
		}
		if _, duplicate := seen[ownerID]; duplicate {
			continue
		}
		seen[ownerID] = struct{}{}
		if err := s.ReconcileNow(ctx, resource, ownerID); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

var _ FriendCacheWriter = (*CacheTruthService)(nil)

func (s *CacheTruthService) mysqlError(operation string, resource repository.CacheResource, resourceID int64, err error) error {
	return &CacheTruthError{Layer: "mysql", Operation: operation, Resource: resource, ResourceID: resourceID, Cause: err}
}

func (s *CacheTruthService) redisError(operation string, resource repository.CacheResource, resourceID int64, err error) error {
	return &CacheTruthError{Layer: "redis", Operation: operation, Resource: resource, ResourceID: resourceID, Cause: err}
}

type CacheRebuildReport struct {
	Scope     CacheScope `json:"scope"`
	Users     int        `json:"users"`
	Groups    int        `json:"groups"`
	Resources int        `json:"resources"`
}

// Warm rewrites every known MySQL owner through the bounded indexes used by
// normal online reconciliation. It is appropriate at application startup.
func (s *CacheTruthService) Warm(ctx context.Context, scope CacheScope) (CacheRebuildReport, error) {
	return s.rebuild(ctx, scope, false)
}

// Rebuild is the strict operator path used by cachectl. Besides replacing the
// projection from MySQL, it invalidates owner-index migration markers while
// holding the same resource lock. The repository then performs an incremental
// SCAN and can remove an orphan key that was manually created outside the
// normal atomic write path.
func (s *CacheTruthService) Rebuild(ctx context.Context, scope CacheScope) (CacheRebuildReport, error) {
	return s.rebuild(ctx, scope, true)
}

func (s *CacheTruthService) rebuild(ctx context.Context, scope CacheScope, strict bool) (CacheRebuildReport, error) {
	report := CacheRebuildReport{Scope: scope}
	if !scope.Valid() {
		return report, fmt.Errorf("invalid cache rebuild scope %q", scope)
	}
	if scope == CacheScopeFriends || scope == CacheScopeBlacklists || scope == CacheScopeAll {
		err := s.forEachUser(ctx, func(userID int64) error {
			report.Users++
			if scope == CacheScopeFriends || scope == CacheScopeAll {
				if err := s.reconcileForBulkOperation(ctx, repository.CacheResourceFriends, userID, strict); err != nil {
					return err
				}
				report.Resources++
			}
			if scope == CacheScopeBlacklists || scope == CacheScopeAll {
				if err := s.reconcileForBulkOperation(ctx, repository.CacheResourceBlacklist, userID, strict); err != nil {
					return err
				}
				report.Resources++
			}
			return nil
		})
		if err != nil {
			return report, err
		}
	}
	if scope == CacheScopeGroups || scope == CacheScopeAll {
		err := s.forEachGroup(ctx, func(groupID int64) error {
			report.Groups++
			if err := s.reconcileForBulkOperation(ctx, repository.CacheResourceGroupMembers, groupID, strict); err != nil {
				return err
			}
			report.Resources++
			return nil
		})
		if err != nil {
			return report, err
		}
	}
	return report, nil
}

func (s *CacheTruthService) reconcileForBulkOperation(
	ctx context.Context,
	resource repository.CacheResource,
	resourceID int64,
	strict bool,
) error {
	if !strict {
		return s.ReconcileNow(ctx, resource, resourceID)
	}
	return s.withResourceLock(ctx, resource, resourceID, "wait for strict cache rebuild", func() error {
		if err := s.cache.InvalidateProjectionMarkers(ctx, resource, resourceID); err != nil {
			return s.redisError("prepare strict rebuild", resource, resourceID, err)
		}
		return s.reconcileCurrent(ctx, resource, resourceID)
	})
}

type CacheAuditIssue struct {
	Resource             repository.CacheResource `json:"resource"`
	OwnerID              int64                    `json:"owner_id"`
	Reason               string                   `json:"reason"`
	MySQLIDs             []int64                  `json:"mysql_ids,omitempty"`
	RedisIDs             []int64                  `json:"redis_ids,omitempty"`
	MissingSetIDs        []int64                  `json:"missing_set_ids,omitempty"`
	MissingInfoIDs       []int64                  `json:"missing_info_ids,omitempty"`
	MissingReverseIDs    []int64                  `json:"missing_reverse_ids,omitempty"`
	UnexpectedReverseIDs []int64                  `json:"unexpected_reverse_ids,omitempty"`
}

type CacheAuditReport struct {
	Scope      CacheScope        `json:"scope"`
	Checked    int               `json:"checked"`
	Mismatches int               `json:"mismatches"`
	Issues     []CacheAuditIssue `json:"issues"`
}

func (s *CacheTruthService) Audit(ctx context.Context, scope CacheScope) (CacheAuditReport, error) {
	report := CacheAuditReport{Scope: scope, Issues: make([]CacheAuditIssue, 0)}
	if !scope.Valid() {
		return report, fmt.Errorf("invalid cache audit scope %q", scope)
	}
	if scope == CacheScopeFriends || scope == CacheScopeBlacklists || scope == CacheScopeAll {
		err := s.forEachUser(ctx, func(userID int64) error {
			if scope == CacheScopeFriends || scope == CacheScopeAll {
				issue, err := s.auditRelationship(ctx, repository.CacheResourceFriends, userID)
				if err != nil {
					return err
				}
				report.add(issue)
			}
			if scope == CacheScopeBlacklists || scope == CacheScopeAll {
				issue, err := s.auditRelationship(ctx, repository.CacheResourceBlacklist, userID)
				if err != nil {
					return err
				}
				report.add(issue)
			}
			return nil
		})
		if err != nil {
			return report, err
		}
	}
	if scope == CacheScopeGroups || scope == CacheScopeAll {
		err := s.forEachGroup(ctx, func(groupID int64) error {
			issue, err := s.auditGroup(ctx, groupID)
			if err != nil {
				return err
			}
			report.add(issue)
			return nil
		})
		if err != nil {
			return report, err
		}
	}
	return report, nil
}

func (report *CacheAuditReport) add(issue *CacheAuditIssue) {
	report.Checked++
	if issue != nil {
		report.Mismatches++
		report.Issues = append(report.Issues, *issue)
	}
}

func (s *CacheTruthService) auditRelationship(
	ctx context.Context,
	resource repository.CacheResource,
	ownerID int64,
) (*CacheAuditIssue, error) {
	var (
		mysqlIDs []int64
		snapshot repository.RelationshipCacheSnapshot
		err      error
	)
	if resource == repository.CacheResourceFriends {
		mysqlIDs, err = s.truth.ListFriendIDsForCache(ctx, ownerID)
		if err == nil {
			snapshot, err = s.cache.ReadFriendSnapshot(ctx, ownerID)
		}
	} else {
		mysqlIDs, err = s.truth.ListBlockedIDsForCache(ctx, ownerID)
		if err == nil {
			snapshot, err = s.cache.ReadBlacklistSnapshot(ctx, ownerID)
		}
	}
	if err != nil {
		return nil, fmt.Errorf("audit %s:%d: %w", resource, ownerID, err)
	}
	mysqlIDs = canonicalIDs(mysqlIDs)
	redisIDs := canonicalIDs(snapshot.IDs)
	reasons := make([]string, 0, 2)
	if !snapshot.Loaded {
		reasons = append(reasons, "loaded marker missing")
	}
	if !sameIDs(mysqlIDs, redisIDs) {
		reasons = append(reasons, "member IDs differ")
	}
	if len(reasons) == 0 {
		return nil, nil
	}
	return &CacheAuditIssue{
		Resource: resource, OwnerID: ownerID, Reason: strings.Join(reasons, "; "),
		MySQLIDs: mysqlIDs, RedisIDs: redisIDs,
	}, nil
}

func (s *CacheTruthService) auditGroup(ctx context.Context, groupID int64) (*CacheAuditIssue, error) {
	mysqlMembers, err := s.truth.ListGroupMembersForCache(ctx, groupID)
	if err != nil {
		return nil, fmt.Errorf("audit group_members:%d mysql: %w", groupID, err)
	}
	snapshot, err := s.cache.ReadGroupMemberSnapshot(ctx, groupID)
	if err != nil {
		return nil, fmt.Errorf("audit group_members:%d redis: %w", groupID, err)
	}
	mysqlIDs := groupMemberIDs(mysqlMembers)
	redisIDs := groupMemberIDs(snapshot.Members)
	reasons := make([]string, 0, 3)
	if !snapshot.Loaded {
		reasons = append(reasons, "loaded marker missing")
	}
	if !sameIDs(mysqlIDs, redisIDs) {
		reasons = append(reasons, "member IDs differ")
	} else if !sameGroupMemberDetails(mysqlMembers, snapshot.Members) {
		reasons = append(reasons, "role or mute metadata differs")
	}
	if len(snapshot.MissingSetIDs) > 0 {
		reasons = append(reasons, "group_members Set entry missing")
	}
	if len(snapshot.MissingInfoIDs) > 0 {
		reasons = append(reasons, "group_member_info Hash entry missing")
	}
	if len(snapshot.MissingReverseIDs) > 0 {
		reasons = append(reasons, "user_groups reverse membership missing")
	}
	if len(snapshot.UnexpectedReverseIDs) > 0 {
		reasons = append(reasons, "user_groups contains non-member")
	}
	if len(reasons) == 0 {
		return nil, nil
	}
	return &CacheAuditIssue{
		Resource: repository.CacheResourceGroupMembers, OwnerID: groupID,
		Reason: strings.Join(reasons, "; "), MySQLIDs: mysqlIDs, RedisIDs: redisIDs,
		MissingSetIDs:        canonicalIDs(snapshot.MissingSetIDs),
		MissingInfoIDs:       canonicalIDs(snapshot.MissingInfoIDs),
		MissingReverseIDs:    canonicalIDs(snapshot.MissingReverseIDs),
		UnexpectedReverseIDs: canonicalIDs(snapshot.UnexpectedReverseIDs),
	}, nil
}

func (s *CacheTruthService) forEachUser(ctx context.Context, visit func(int64) error) error {
	var afterID int64
	for {
		ids, err := s.truth.ListUserIDs(ctx, afterID, s.pageSize)
		if err != nil {
			return fmt.Errorf("list users for cache operation: %w", err)
		}
		if len(ids) == 0 {
			return nil
		}
		for _, id := range ids {
			if err := visit(id); err != nil {
				return err
			}
		}
		afterID = ids[len(ids)-1]
		if len(ids) < s.pageSize {
			return nil
		}
	}
}

func (s *CacheTruthService) forEachGroup(ctx context.Context, visit func(int64) error) error {
	var afterID int64
	for {
		ids, err := s.truth.ListGroupIDs(ctx, afterID, s.pageSize)
		if err != nil {
			return fmt.Errorf("list groups for cache operation: %w", err)
		}
		if len(ids) == 0 {
			return nil
		}
		for _, id := range ids {
			if err := visit(id); err != nil {
				return err
			}
		}
		afterID = ids[len(ids)-1]
		if len(ids) < s.pageSize {
			return nil
		}
	}
}

func canonicalIDs(ids []int64) []int64 {
	seen := make(map[int64]struct{}, len(ids))
	for _, id := range ids {
		if id > 0 {
			seen[id] = struct{}{}
		}
	}
	result := make([]int64, 0, len(seen))
	for id := range seen {
		result = append(result, id)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}

func sameIDs(left, right []int64) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func groupMemberIDs(members []model.GroupMember) []int64 {
	ids := make([]int64, 0, len(members))
	for _, member := range members {
		ids = append(ids, member.UserID)
	}
	return canonicalIDs(ids)
}

func sameGroupMemberDetails(left, right []model.GroupMember) bool {
	leftByID := make(map[int64]model.GroupMember, len(left))
	for _, member := range left {
		leftByID[member.UserID] = member
	}
	rightByID := make(map[int64]model.GroupMember, len(right))
	for _, member := range right {
		rightByID[member.UserID] = member
	}
	if len(leftByID) != len(rightByID) {
		return false
	}
	for id, expected := range leftByID {
		actual, ok := rightByID[id]
		if !ok || actual.Role != expected.Role || !sameOptionalTime(actual.MutedUntil, expected.MutedUntil) {
			return false
		}
	}
	return true
}

func sameOptionalTime(left, right *time.Time) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.Equal(*right)
}

type CacheReconcilerOptions struct {
	BatchSize  int
	Lease      time.Duration
	IdleDelay  time.Duration
	RetryBase  time.Duration
	MaxBackoff time.Duration
	OnError    func(error)
}

type CacheReconciler struct {
	truth      repository.CacheTruthRepository
	service    *CacheTruthService
	workerID   string
	batchSize  int
	lease      time.Duration
	idleDelay  time.Duration
	retryBase  time.Duration
	maxBackoff time.Duration
	onError    func(error)
}

func NewCacheReconciler(
	truth repository.CacheTruthRepository,
	service *CacheTruthService,
	workerID string,
	options CacheReconcilerOptions,
) *CacheReconciler {
	if options.BatchSize <= 0 {
		options.BatchSize = 100
	}
	if options.Lease <= 0 {
		options.Lease = 30 * time.Second
	}
	if options.IdleDelay <= 0 {
		options.IdleDelay = time.Second
	}
	if options.RetryBase <= 0 {
		options.RetryBase = time.Second
	}
	if options.MaxBackoff <= 0 {
		options.MaxBackoff = 5 * time.Minute
	}
	return &CacheReconciler{
		truth: truth, service: service, workerID: workerID,
		batchSize: options.BatchSize, lease: options.Lease, idleDelay: options.IdleDelay,
		retryBase: options.RetryBase, maxBackoff: options.MaxBackoff, onError: options.OnError,
	}
}

// RunOnce processes a claimed batch. Every event reloads current MySQL truth;
// event age/order therefore cannot resurrect a deleted relationship.
func (r *CacheReconciler) RunOnce(ctx context.Context) (int, error) {
	events, err := r.truth.ClaimCacheReconcileEvents(ctx, r.workerID, r.batchSize, r.lease)
	if err != nil {
		return 0, fmt.Errorf("claim cache reconciliation events: %w", err)
	}
	var failures []error
	for _, event := range events {
		reconcileErr := r.service.ReconcileNow(ctx, event.ResourceType, event.ResourceID)
		if reconcileErr == nil {
			if err := r.truth.MarkCacheReconcileSuccess(ctx, event.ID, event.LockToken); err != nil {
				failures = append(failures, fmt.Errorf("finish cache event %d: %w", event.ID, err))
			}
			continue
		}
		retryAfter := r.retryDelay(event.Attempts)
		if err := r.truth.MarkCacheReconcileFailure(ctx, event.ID, event.LockToken, reconcileErr, retryAfter); err != nil {
			failures = append(failures, errors.Join(reconcileErr, fmt.Errorf("reschedule cache event %d: %w", event.ID, err)))
		} else {
			failures = append(failures, fmt.Errorf("reconcile cache event %d: %w", event.ID, reconcileErr))
		}
	}
	return len(events), errors.Join(failures...)
}

func (r *CacheReconciler) retryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	delay := r.retryBase
	for i := 1; i < attempt && delay < r.maxBackoff; i++ {
		if delay > r.maxBackoff/2 {
			return r.maxBackoff
		}
		delay *= 2
	}
	if delay > r.maxBackoff {
		return r.maxBackoff
	}
	return delay
}

// Run is suitable for cmd/server: start it in a goroutine and cancel the
// server context during graceful shutdown.
func (r *CacheReconciler) Run(ctx context.Context) error {
	for {
		processed, err := r.RunOnce(ctx)
		if err != nil && r.onError != nil {
			r.onError(err)
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if processed > 0 {
			continue
		}
		timer := time.NewTimer(r.idleDelay)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return ctx.Err()
		case <-timer.C:
		}
	}
}
