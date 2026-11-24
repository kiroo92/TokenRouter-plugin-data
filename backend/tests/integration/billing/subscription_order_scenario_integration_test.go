//go:build integration

package billing_test

import (
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/billing/postgres"
)

// subscription_order_scenario_integration_test.go covers subscription_service.go,
// subscription_order.go, and postgres/settlement.go against migrated PostgreSQL.
// Repeated purchases, atomic rank changes and cross-grant allocation share real
// user locks and transactions so assertions observe persisted financial state.

// TestIndependentSubscriptionsSpendInSavedOrder buys the same plan twice and
// moves the second grant first. Settlement exhausts that grant before spilling
// into the first, replay charges nothing, and revocation keeps sibling dates.
func TestIndependentSubscriptionsSpendInSavedOrder(t *testing.T) {
	ctx := t.Context()
	client := committedEntitlementClient(t)
	user, err := client.User.Create().SetEmail("subscription-order@example.com").SetPasswordHash("hash").Save(ctx)
	require.NoError(t, err)
	plan, err := client.SubscriptionPlan.Create().SetName("independent plan").SetPrice(1).SetValidityDays(1).SetDailyLimitUsd(1).Save(ctx)
	require.NoError(t, err)
	store := postgres.NewUserSubscriptionRepository(client)
	service := billing.NewSubscriptionService(nil, store, postgres.NewSubscriptionMutations(client))
	first, err := service.AssignSubscription(ctx, &billing.AssignSubscriptionInput{UserID: user.ID, PlanID: plan.ID})
	require.NoError(t, err)
	second, err := service.AssignSubscription(ctx, &billing.AssignSubscriptionInput{UserID: user.ID, PlanID: plan.ID})
	require.NoError(t, err)
	require.NotEqual(t, first.ID, second.ID)
	require.True(t, first.Independent)
	require.True(t, second.Independent)
	require.Equal(t, billing.SubscriptionStatusActive, second.Status)
	require.True(t, second.StartsAt.Before(first.ExpiresAt), "repeat purchases overlap rather than queue")
	require.NoError(t, service.ReorderSubscriptions(ctx, user.ID, []int64{second.ID, first.ID}))

	key, err := client.APIKey.Create().SetUserID(user.ID).SetKey("subscription-order-" + uuid.NewString()).SetName("automatic").Save(ctx)
	require.NoError(t, err)
	command := &billing.UsageBillingCommand{
		RequestID: uuid.NewString(), APIKeyID: key.ID, UserID: user.ID,
		APIKeyBillingMode: billing.APIKeyBillingModeAuto, BillableAmountUSD: 1.5,
	}
	settlement := newSettlementFixture(integrationDB)
	result, err := settlement.Apply(ctx, command)
	require.NoError(t, err)
	require.True(t, result.Applied)
	require.Zero(t, result.BalanceAmountUSD)
	secondAfter, err := store.GetByID(ctx, second.ID)
	require.NoError(t, err)
	firstAfter, err := store.GetByID(ctx, first.ID)
	require.NoError(t, err)
	require.InDelta(t, 1, secondAfter.DailyUsageUSD, 1e-8)
	require.InDelta(t, 0.5, firstAfter.DailyUsageUSD, 1e-8)
	replayed, err := settlement.Apply(ctx, command)
	require.NoError(t, err)
	require.False(t, replayed.Applied)

	// Pin the exhausted grant before revoking it, so this assertion verifies
	// that the remaining same-plan grant receives the existing key binding.
	require.NoError(t, client.APIKey.UpdateOneID(key.ID).SetBillingMode(apikey.APIKeyBillingModeSubscription).SetPreferredSubscriptionID(second.ID).Exec(ctx))
	_, err = service.RevokeOwnExhaustedSubscription(ctx, user.ID, second.ID)
	require.NoError(t, err)
	firstAfter, err = store.GetByID(ctx, first.ID)
	require.NoError(t, err)
	require.Equal(t, first.StartsAt, firstAfter.StartsAt)
	require.Equal(t, first.ExpiresAt, firstAfter.ExpiresAt)
	keyAfter, err := client.APIKey.Get(ctx, key.ID)
	require.NoError(t, err)
	require.NotNil(t, keyAfter.PreferredSubscriptionID)
	require.Equal(t, first.ID, *keyAfter.PreferredSubscriptionID)
}

// TestSubscriptionOrderRollsBackPartialWrites forces the second rank update to
// fail in PostgreSQL. The first rank must roll back too, while an unavailable grant
// request is rejected before modifying any of the payer's ranks.
func TestSubscriptionOrderRollsBackPartialWrites(t *testing.T) {
	ctx := t.Context()
	client := committedEntitlementClient(t)
	user, err := client.User.Create().SetEmail("order-rollback@example.com").SetPasswordHash("hash").Save(ctx)
	require.NoError(t, err)
	plan, err := client.SubscriptionPlan.Create().SetName("order rollback").SetPrice(1).SetValidityDays(1).Save(ctx)
	require.NoError(t, err)
	store := postgres.NewUserSubscriptionRepository(client)
	service := billing.NewSubscriptionService(nil, store, postgres.NewSubscriptionMutations(client))
	first, err := service.AssignSubscription(ctx, &billing.AssignSubscriptionInput{UserID: user.ID, PlanID: plan.ID})
	require.NoError(t, err)
	second, err := service.AssignSubscription(ctx, &billing.AssignSubscriptionInput{UserID: user.ID, PlanID: plan.ID})
	require.NoError(t, err)
	// Reject the second rank update after the first succeeds, exposing whether
	// the enclosing transaction restores that earlier write on failure.
	constraint := fmt.Sprintf("order_failure_%d", first.ID)
	_, err = integrationDB.ExecContext(ctx, fmt.Sprintf("ALTER TABLE user_subscriptions ADD CONSTRAINT %s CHECK (id <> %d OR consumption_priority <> 1)", constraint, first.ID))
	require.NoError(t, err)
	// Remove the temporary constraint before shared fixture cleanup so later
	// cases keep the migrated schema even when the expected mutation fails.
	t.Cleanup(func() {
		_, dropErr := integrationDB.Exec("ALTER TABLE user_subscriptions DROP CONSTRAINT " + constraint)
		require.NoError(t, dropErr)
	})
	require.Error(t, service.ReorderSubscriptions(ctx, user.ID, []int64{second.ID, first.ID}))
	after, err := store.GetByID(ctx, second.ID)
	require.NoError(t, err)
	require.Equal(t, second.ConsumptionPriority, after.ConsumptionPriority)
	require.ErrorIs(t, service.ReorderSubscriptions(ctx, user.ID, []int64{first.ID, 999999}), billing.ErrSubscriptionOrderInvalid)
}
