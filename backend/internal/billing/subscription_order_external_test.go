package billing_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/billing/testkit"
)

// subscription_order_external_test.go verifies owner-scoped order validation
// and admission selection using in-memory grants. PostgreSQL transaction
// rollback and real settlement are covered by the integration scenarios.

// subscriptionOrderRepository records rank-only updates to owned fixture grants.
type subscriptionOrderRepository struct {
	*testkit.SubscriptionRepository     // shared in-memory grant fixture
	writes                          int // rank update invocation count
}

// subscriptionOrderTransactions runs validation fixtures without a database or
// rollback simulation; PostgreSQL integration tests verify actual lock ownership.
type subscriptionOrderTransactions struct {
	// SubscriptionTransactions leaves unused fixture operations unconfigured.
	// These tests invoke only Within and LockGrantUser, which are overridden below.
	billing.SubscriptionTransactions // unused transaction fixture operations
}

// Within executes the fixture callback directly and returns its result.
// It supplies no rollback or locking; these unit cases inspect validation
// before writes, while integration tests exercise persistence failure recovery.
func (subscriptionOrderTransactions) Within(ctx context.Context, callback func(context.Context) error) error {
	return callback(ctx)
}

// LockGrantUser accepts fixture identity without acquiring a persistence lock.
func (subscriptionOrderTransactions) LockGrantUser(context.Context, int64) error {
	return nil
}

// SetConsumptionPriority changes the matching fixture grant's rank and counts
// the write. Tests inspect that invalid requests finish validation before any
// update; this fixture does not simulate transactional rollback.
func (r *subscriptionOrderRepository) SetConsumptionPriority(_ context.Context, userID, id int64, priority int) error {
	sub := r.ByID[id]
	if sub == nil || sub.UserID != userID {
		return billing.ErrSubscriptionNotFound
	}
	sub.ConsumptionPriority = priority
	r.writes++
	return nil
}

// TestReorderSubscriptionsValidatesBeforeWrites mixes a valid owned grant with
// duplicate, foreign and expired IDs. Every invalid request must preserve ranks
// before a successful partial order appends an omitted owned subscription.
func TestReorderSubscriptionsValidatesBeforeWrites(t *testing.T) {
	repo := &subscriptionOrderRepository{SubscriptionRepository: testkit.NewSubscriptionRepository()}
	now := time.Now()
	for _, sub := range []billing.UserSubscription{
		{ID: 1, UserID: 7, StartsAt: now.Add(-time.Hour), ExpiresAt: now.Add(time.Hour), ConsumptionPriority: 5},
		{ID: 2, UserID: 7, StartsAt: now.Add(-time.Hour), ExpiresAt: now.Add(2 * time.Hour), ConsumptionPriority: 6},
		{ID: 3, UserID: 8, StartsAt: now.Add(-time.Hour), ExpiresAt: now.Add(time.Hour)},
		{ID: 4, UserID: 7, StartsAt: now.Add(-2 * time.Hour), ExpiresAt: now.Add(-time.Hour)},
	} {
		repo.Seed(&sub)
	}
	service := billing.NewSubscriptionService(nil, repo, subscriptionOrderTransactions{})
	for _, ids := range [][]int64{{1, 1}, {1, 3}, {1, 4}, {0}} {
		require.ErrorIs(t, service.ReorderSubscriptions(t.Context(), 7, ids), billing.ErrSubscriptionOrderInvalid)
		require.Zero(t, repo.writes)
	}
	require.NoError(t, service.ReorderSubscriptions(t.Context(), 7, []int64{2}))
	require.Equal(t, 0, repo.ByID[2].ConsumptionPriority)
	require.Equal(t, 1, repo.ByID[1].ConsumptionPriority)
	require.Equal(t, 2, repo.writes)
}

// TestSubscriptionOrderControlsAdmission changes priority against expiry order.
// Admission selects the preferred grant, skips it after its quota is consumed,
// and then uses the remaining eligible subscription automatically.
func TestSubscriptionOrderControlsAdmission(t *testing.T) {
	repo := testkit.NewSubscriptionRepository()
	now := time.Now()
	limit := 10.0
	repo.Seed(&billing.UserSubscription{
		ID: 1, UserID: 7, StartsAt: now.Add(-time.Hour), ExpiresAt: now.Add(time.Hour),
		Status: billing.SubscriptionStatusActive, ConsumptionPriority: 2, DailyLimitUSD: &limit,
	})
	repo.Seed(&billing.UserSubscription{
		ID: 2, UserID: 7, StartsAt: now.Add(-time.Hour), ExpiresAt: now.Add(2 * time.Hour),
		Status: billing.SubscriptionStatusActive, ConsumptionPriority: 1, DailyLimitUSD: &limit,
	})
	service := billing.NewSubscriptionService(nil, repo, subscriptionOrderTransactions{})
	sub, _, err := service.GetUsableSubscription(t.Context(), 7)
	require.NoError(t, err)
	require.Equal(t, int64(2), sub.ID)
	repo.ByID[2].DailyUsageUSD = limit
	repo.ByID[2].DailyWindowStart = &now
	sub, _, err = service.GetUsableSubscription(t.Context(), 7)
	require.NoError(t, err)
	require.Equal(t, int64(1), sub.ID)
}
