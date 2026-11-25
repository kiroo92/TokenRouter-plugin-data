//go:build integration

package billing_test

import (
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/batchimage"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/billing/postgres"
)

// subscription_reset_card_scenario_integration_test.go covers paid issuance,
// redemption, admission and settlement through subscription_service.go,
// subscription_reset_cards.go and settlement.go on migrated PostgreSQL.
// Assertions distinguish temporary welfare funding from all original counters
// and verify that one-time card identity survives replay and week expiry.

// TestResetCardRestoresWeekWithoutConsumingFutureQuota buys a card-bearing plan
// and restores spending only on its originating paid grant. Welfare funding
// remains usable despite exhausted original quotas, preserves refresh anchors,
// and falls back to balance after the capped benefit is consumed or expires.
func TestResetCardRestoresWeekWithoutConsumingFutureQuota(t *testing.T) {
	ctx := t.Context()
	client := committedEntitlementClient(t)
	now := time.Now().UTC().Truncate(time.Second)
	user, err := client.User.Create().SetEmail("reset-card@example.com").SetPasswordHash("hash").SetBalance(100).Save(ctx)
	require.NoError(t, err)
	plan, err := client.SubscriptionPlan.Create().SetName("Light").SetPrice(10).SetValidityDays(30).
		SetDailyLimitUsd(20).SetWeeklyLimitUsd(100).SetMonthlyLimitUsd(300).SetResetCardCount(2).SetResetCardLimitUsd(100).Save(ctx)
	require.NoError(t, err)
	store := postgres.NewUserSubscriptionRepository(client)
	clock := billing.DateRuntime{Now: func() time.Time { return now.Add(-72 * time.Hour) }}
	service := billing.NewSubscriptionService(nil, store, postgres.NewSubscriptionMutations(client), clock)
	orderID := int64(7001)
	// A historical order captured a smaller manual cap before automatic weekly
	// caps. Fulfillment must retain that promise after the live plan is normalized.
	input := &billing.AssignSubscriptionInput{UserID: user.ID, PlanID: plan.ID, SourceOrderID: &orderID, ResetCardCount: 2, ResetCardLimitUSD: 50}
	source, err := service.AssignSubscription(ctx, input)
	require.NoError(t, err)
	duplicate, err := service.AssignSubscription(ctx, input)
	require.NoError(t, err)
	require.Equal(t, source.ID, duplicate.ID)
	cards, err := service.ListResetCards(ctx, user.ID)
	require.NoError(t, err)
	require.Len(t, cards, 2)
	require.Equal(t, source.ExpiresAt, cards[0].ExpiresAt)
	target := source
	sibling, err := service.AssignSubscription(ctx, &billing.AssignSubscriptionInput{UserID: user.ID, PlanID: plan.ID})
	require.NoError(t, err)
	_, err = service.UseResetCard(ctx, user.ID, cards[0].ID, sibling.ID)
	require.ErrorIs(t, err, billing.ResetCardError)
	week := now.Add(-48 * time.Hour)
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	require.NoError(t, client.UserSubscription.UpdateOneID(target.ID).SetWeeklyWindowStart(week).SetDailyWindowStart(day).
		SetMonthlyWindowStart(week).SetDailyUsageUsd(20).SetWeeklyUsageUsd(80).SetMonthlyUsageUsd(300).Exec(ctx))
	used, err := service.UseResetCard(ctx, user.ID, cards[0].ID, target.ID)
	require.NoError(t, err)
	require.Equal(t, float64(50), used.CreditedUSD)
	require.Equal(t, week.Add(7*24*time.Hour), *used.BenefitExpiresAt)
	// Concurrent retries share the payer lock and reuse the saved result;
	// they must not append more benefit capacity to the already consumed card.
	var retries sync.WaitGroup
	results := make([]*billing.SubscriptionResetCard, 2)
	errors := make([]error, 2)
	for i := range results {
		retries.Add(1)
		go func(index int) {
			defer retries.Done()
			results[index], errors[index] = service.UseResetCard(ctx, user.ID, cards[0].ID, target.ID)
		}(i)
	}
	retries.Wait()
	for i := range results {
		require.NoError(t, errors[i])
		require.Equal(t, float64(50), results[i].CreditedUSD)
	}
	replay, err := service.UseResetCard(ctx, user.ID, cards[0].ID, target.ID)
	require.NoError(t, err)
	require.Equal(t, used.CreditedUSD, replay.CreditedUSD)
	_, err = service.UseResetCard(ctx, user.ID, cards[0].ID, sibling.ID)
	require.ErrorIs(t, err, billing.ResetCardError)
	view, err := service.GetByID(ctx, target.ID)
	require.NoError(t, err)
	require.NoError(t, billing.CheckSubscriptionUsageLimits(view, 0))
	require.Equal(t, float64(50), view.AvailableQuotaUSD())
	key, err := client.APIKey.Create().SetUserID(user.ID).SetKey("reset-" + uuid.NewString()).SetName("reset benefit").Save(ctx)
	require.NoError(t, err)
	command := &billing.UsageBillingCommand{
		RequestID: uuid.NewString(), APIKeyID: key.ID, UserID: user.ID,
		APIKeyBillingMode: billing.APIKeyBillingModeSubscription, PreferredSubscriptionID: &target.ID, BillableAmountUSD: 60,
	}
	settlement := newSettlementFixture(integrationDB)
	result, err := settlement.Apply(ctx, command)
	require.NoError(t, err)
	require.InDelta(t, 50, result.SubscriptionAmountUSD, 1e-8)
	require.InDelta(t, 10, result.BalanceAmountUSD, 1e-8)
	require.NotNil(t, result.BillingAllocations[0].ResetCardID)
	view, err = store.GetByID(ctx, target.ID)
	require.NoError(t, err)
	require.Equal(t, float64(20), view.DailyUsageUSD)
	require.Equal(t, float64(80), view.WeeklyUsageUSD)
	require.Equal(t, float64(300), view.MonthlyUsageUSD)
	require.Equal(t, week, *view.WeeklyWindowStart)
	require.Equal(t, day, *view.DailyWindowStart)
	require.Equal(t, target.ExpiresAt, view.ExpiresAt)
	replayed, err := settlement.Apply(ctx, command)
	require.NoError(t, err)
	require.False(t, replayed.Applied)
	// Redeem another entitled card, then advance only its benefit deadline.
	// Original monthly quota remains exhausted, exposing any accidental carryover.
	_, err = service.UseResetCard(ctx, user.ID, cards[1].ID, target.ID)
	require.NoError(t, err)
	_, err = integrationDB.ExecContext(ctx, `UPDATE subscription_reset_cards SET benefit_expires_at=NOW()-INTERVAL '1 second' WHERE id=$1`, cards[1].ID)
	require.NoError(t, err)
	view, err = service.GetByID(ctx, target.ID)
	require.NoError(t, err)
	require.Zero(t, billing.AvailableResetCredit(view.ResetCredits, time.Now()))
	require.Error(t, billing.CheckSubscriptionUsageLimits(view, 0))
}

// TestResetCardPlanPolicySynchronizesWeeklyEdits changes only quota fields on
// an enabled plan. Storage must derive the new cap, reject removing monthly
// eligibility, and serialize simultaneous weekly and monthly replacements.
func TestResetCardPlanPolicySynchronizesWeeklyEdits(t *testing.T) {
	ctx := t.Context()
	client := committedEntitlementClient(t)
	plans := billing.NewPlans(postgres.NewPlanStore(client), nil, nil)
	weekly, monthly := 585.0, 2340.0
	plan, err := plans.CreatePlan(ctx, billing.CreatePlanRequest{
		Name: "automatic card cap", Price: 10, ValidityDays: 30, ValidityUnit: "day",
		WeeklyLimitUSD: &weekly, MonthlyLimitUSD: &monthly, ResetCardCount: 1,
	})
	require.NoError(t, err)
	require.Equal(t, weekly, plan.ResetCardLimitUSD)
	weekly = 1162.5
	staleCap := 0.0
	plan, err = plans.UpdatePlan(ctx, plan.ID, billing.UpdatePlanRequest{
		WeeklyLimitUSD: billing.NullableFloat64Patch{Present: true, Value: &weekly}, ResetCardLimitUSD: &staleCap,
	})
	require.NoError(t, err)
	require.Equal(t, weekly, plan.ResetCardLimitUSD)
	_, err = plans.UpdatePlan(ctx, plan.ID, billing.UpdatePlanRequest{MonthlyLimitUSD: billing.NullableFloat64Patch{Present: true}})
	require.Error(t, err)
	stored, err := plans.GetPlan(ctx, plan.ID)
	require.NoError(t, err)
	require.Equal(t, monthly, *stored.MonthlyLimitUSD)

	weekly, monthly = 700, 3000
	patches := []billing.UpdatePlanRequest{
		{WeeklyLimitUSD: billing.NullableFloat64Patch{Present: true, Value: &weekly}},
		{MonthlyLimitUSD: billing.NullableFloat64Patch{Present: true, Value: &monthly}},
	}
	var workers sync.WaitGroup
	errors := make([]error, len(patches))
	// Distinct partial edits share one row lock through commit, so the later
	// request must derive its cap from the earlier committed weekly replacement.
	for i := range patches {
		workers.Go(func() {
			_, errors[i] = plans.UpdatePlan(ctx, plan.ID, patches[i])
		})
	}
	workers.Wait()
	for _, err := range errors {
		require.NoError(t, err)
	}
	stored, err = plans.GetPlan(ctx, plan.ID)
	require.NoError(t, err)
	require.Equal(t, weekly, stored.ResetCardLimitUSD)
	require.Equal(t, weekly, *stored.WeeklyLimitUSD)
	require.Equal(t, monthly, *stored.MonthlyLimitUSD)
	_, err = integrationDB.ExecContext(ctx, `UPDATE subscription_plans SET reset_card_limit_usd=0 WHERE id=$1`, plan.ID)
	require.Error(t, err)
	count := 0
	stored, err = plans.UpdatePlan(ctx, plan.ID, billing.UpdatePlanRequest{
		ResetCardCount: &count, MonthlyLimitUSD: billing.NullableFloat64Patch{Present: true},
	})
	require.NoError(t, err)
	require.Zero(t, stored.ResetCardCount)
	require.Zero(t, stored.ResetCardLimitUSD)
	require.Nil(t, stored.MonthlyLimitUSD)
}

// TestResetCardEligibilityKeepsUnusedCard rejects frozen, foreign, other owned
// subscriptions and bound grants without settled weekly spending. Every rejected use
// must leave the purchase card available, with no credited welfare amount.
func TestResetCardEligibilityKeepsUnusedCard(t *testing.T) {
	ctx := t.Context()
	client := committedEntitlementClient(t)
	user, err := client.User.Create().SetEmail("reset-eligibility@example.com").SetPasswordHash("hash").Save(ctx)
	require.NoError(t, err)
	other, err := client.User.Create().SetEmail("reset-foreign@example.com").SetPasswordHash("hash").Save(ctx)
	require.NoError(t, err)
	plan, err := client.SubscriptionPlan.Create().SetName("Premium").SetPrice(10).SetValidityDays(30).SetWeeklyLimitUsd(100).Save(ctx)
	require.NoError(t, err)
	store := postgres.NewUserSubscriptionRepository(client)
	service := billing.NewSubscriptionService(nil, store, postgres.NewSubscriptionMutations(client))
	orderID := int64(7002)
	sub, err := service.AssignSubscription(ctx, &billing.AssignSubscriptionInput{UserID: user.ID, PlanID: plan.ID, SourceOrderID: &orderID, ResetCardCount: 1, ResetCardLimitUSD: 80})
	require.NoError(t, err)
	cards, err := service.ListResetCards(ctx, user.ID)
	require.NoError(t, err)
	require.Len(t, cards, 1)
	_, err = service.UseResetCard(ctx, user.ID, cards[0].ID, sub.ID)
	require.ErrorIs(t, err, billing.ResetCardError)
	require.NoError(t, client.UserSubscription.UpdateOneID(sub.ID).SetStatus(billing.SubscriptionStatusSuspended).Exec(ctx))
	_, err = service.UseResetCard(ctx, user.ID, cards[0].ID, sub.ID)
	require.ErrorIs(t, err, billing.ResetCardError)
	_, err = service.UseResetCard(ctx, other.ID, cards[0].ID, sub.ID)
	require.ErrorIs(t, err, billing.ResetCardError)
	require.NoError(t, client.UserSubscription.UpdateOneID(sub.ID).SetStatus(billing.SubscriptionStatusActive).Exec(ctx))
	otherPlan, err := client.SubscriptionPlan.Create().SetName("different plan").SetPrice(1).SetValidityDays(30).SetWeeklyLimitUsd(100).Save(ctx)
	require.NoError(t, err)
	foreignPlanTarget, err := service.AssignSubscription(ctx, &billing.AssignSubscriptionInput{UserID: user.ID, PlanID: otherPlan.ID})
	require.NoError(t, err)
	_, err = service.UseResetCard(ctx, user.ID, cards[0].ID, foreignPlanTarget.ID)
	require.ErrorIs(t, err, billing.ResetCardError)
	frozenTarget, err := service.AssignSubscription(ctx, &billing.AssignSubscriptionInput{UserID: user.ID, PlanID: plan.ID})
	require.NoError(t, err)
	require.NoError(t, client.UserSubscription.UpdateOneID(frozenTarget.ID).SetStatus(billing.SubscriptionStatusSuspended).
		SetWeeklyWindowStart(time.Now().Add(-time.Hour)).SetWeeklyUsageUsd(20).Exec(ctx))
	_, err = service.UseResetCard(ctx, user.ID, cards[0].ID, frozenTarget.ID)
	require.ErrorIs(t, err, billing.ResetCardError)
	require.NoError(t, client.UserSubscription.UpdateOneID(frozenTarget.ID).SetStatus(billing.SubscriptionStatusActive).
		SetExpiresAt(time.Now().Add(-time.Second)).Exec(ctx))
	_, err = service.UseResetCard(ctx, user.ID, cards[0].ID, frozenTarget.ID)
	require.ErrorIs(t, err, billing.ResetCardError)
	rows, err := integrationDB.QueryContext(ctx, `SELECT used_at,credited_usd FROM subscription_reset_cards WHERE id=$1`, cards[0].ID)
	require.NoError(t, err)
	// Close the case-owned cursor so shared fixture cleanup can obtain its
	// connection after the invalid-redemption assertions finish.
	defer func() { _ = rows.Close() }()
	require.True(t, rows.Next())
	var usedAt *time.Time
	var credited float64
	require.NoError(t, rows.Scan(&usedAt, &credited))
	require.Nil(t, usedAt)
	require.Zero(t, credited)
}

// TestResetCardTaskFundingExcludesHolds reserves original quota before card
// redemption, then captures a smaller welfare-funded task. The reward excludes
// outstanding holds and refunds only its card balance, preserving base counters.
func TestResetCardTaskFundingExcludesHolds(t *testing.T) {
	ctx := t.Context()
	client := committedEntitlementClient(t)
	now := time.Now().UTC()
	user, err := client.User.Create().SetEmail("reset-task@example.com").SetPasswordHash("hash").SetBalance(100).Save(ctx)
	require.NoError(t, err)
	plan, err := client.SubscriptionPlan.Create().SetName("task reset").SetPrice(1).SetValidityDays(30).SetWeeklyLimitUsd(100).Save(ctx)
	require.NoError(t, err)
	store := postgres.NewUserSubscriptionRepository(client)
	service := billing.NewSubscriptionService(nil, store, postgres.NewSubscriptionMutations(client))
	orderID := int64(7003)
	sub, err := service.AssignSubscription(ctx, &billing.AssignSubscriptionInput{UserID: user.ID, PlanID: plan.ID, SourceOrderID: &orderID, ResetCardCount: 1, ResetCardLimitUSD: 100})
	require.NoError(t, err)
	week := now.Add(-time.Hour)
	require.NoError(t, client.UserSubscription.UpdateOneID(sub.ID).SetWeeklyWindowStart(week).SetWeeklyUsageUsd(80).Exec(ctx))
	key, err := client.APIKey.Create().SetUserID(user.ID).SetKey("reset-task-" + uuid.NewString()).SetName("task").Save(ctx)
	require.NoError(t, err)
	funds := newTaskFundsFixture(integrationDB)
	batchID := "imgbatch_" + uuid.NewString()
	insertBatchImageAllowanceTestJob(t, batchID, user.ID, user.ID, key.ID, nil, now)
	original := &billing.TaskFundsCommand{
		RequestID: batchimage.BatchImageHoldRequestID(batchID), APIKeyID: key.ID,
		UserID: user.ID, ActorUserID: user.ID, HoldAmount: 10, ReservedAt: now, Task: batchimage.FundingReference(batchID),
	}
	held, err := funds.Reserve(ctx, original)
	require.NoError(t, err)
	cards, err := service.ListResetCards(ctx, user.ID)
	require.NoError(t, err)
	used, err := service.UseResetCard(ctx, user.ID, cards[0].ID, sub.ID)
	require.NoError(t, err)
	require.Equal(t, float64(80), used.CreditedUSD, "only the settled 80 is rewarded, excluding the additional 10 held")
	// Release the original hold before a new task spends restored welfare.
	// Its quota refund must leave the independently credited benefit unchanged.
	release := *original
	release.RequestID = batchimage.BatchImageReleaseRequestID(batchID)
	release.RequestFingerprint = ""
	release.SubscriptionHoldAllocations = held.BillingAllocations
	release.BalanceHoldAmount = held.BalanceAmountUSD
	release.AllowanceReserved = true
	_, err = funds.Release(ctx, &release)
	require.NoError(t, err)
	secondID := "imgbatch_" + uuid.NewString()
	insertBatchImageAllowanceTestJob(t, secondID, user.ID, user.ID, key.ID, nil, now)
	second := &billing.TaskFundsCommand{
		RequestID: batchimage.BatchImageHoldRequestID(secondID), APIKeyID: key.ID,
		UserID: user.ID, ActorUserID: user.ID, HoldAmount: 50, ReservedAt: now, Task: batchimage.FundingReference(secondID),
	}
	benefitHold, err := funds.Reserve(ctx, second)
	require.NoError(t, err)
	require.NotNil(t, benefitHold.BillingAllocations[0].ResetCardID)
	capture := *second
	capture.RequestID = batchimage.BatchImageCaptureRequestID(secondID)
	capture.RequestFingerprint = ""
	capture.SubscriptionHoldAllocations = benefitHold.BillingAllocations
	capture.BalanceHoldAmount = benefitHold.BalanceAmountUSD
	capture.ActualAmount = 20
	capture.AllowanceReserved = true
	_, err = funds.Capture(ctx, &capture)
	require.NoError(t, err)
	view, err := service.GetByID(ctx, sub.ID)
	require.NoError(t, err)
	require.InDelta(t, 80, view.WeeklyUsageUSD, 1e-8)
	require.InDelta(t, 60, billing.AvailableResetCredit(view.ResetCredits, time.Now()), 1e-8)
	thirdID := "imgbatch_" + uuid.NewString()
	insertBatchImageAllowanceTestJob(t, thirdID, user.ID, user.ID, key.ID, nil, now)
	third := &billing.TaskFundsCommand{
		RequestID: batchimage.BatchImageHoldRequestID(thirdID), APIKeyID: key.ID,
		UserID: user.ID, ActorUserID: user.ID, HoldAmount: 10, ReservedAt: now, Task: batchimage.FundingReference(thirdID),
	}
	thirdHold, err := funds.Reserve(ctx, third)
	require.NoError(t, err)
	// Expire the benefit before retrying a completed capture. Neither the
	// idempotent retry nor expired inventory can supply capacity to another week.
	_, err = integrationDB.ExecContext(ctx, `UPDATE subscription_reset_cards SET benefit_expires_at=NOW()-INTERVAL '1 second' WHERE id=$1`, cards[0].ID)
	require.NoError(t, err)
	// A late task cancellation refunds its historical card balance only.
	// Expiry still excludes that returned balance from the next week's funding.
	lateRelease := *third
	lateRelease.RequestID = batchimage.BatchImageReleaseRequestID(thirdID)
	lateRelease.RequestFingerprint = ""
	lateRelease.SubscriptionHoldAllocations = thirdHold.BillingAllocations
	lateRelease.BalanceHoldAmount = thirdHold.BalanceAmountUSD
	lateRelease.AllowanceReserved = true
	_, err = funds.Release(ctx, &lateRelease)
	require.NoError(t, err)
	replayed, err := funds.Capture(ctx, &capture)
	require.NoError(t, err)
	require.False(t, replayed.Applied)
	view, err = service.GetByID(ctx, sub.ID)
	require.NoError(t, err)
	require.Zero(t, billing.AvailableResetCredit(view.ResetCredits, time.Now()))
	require.InDelta(t, 80, view.WeeklyUsageUSD, 1e-8)
}

// TestResetCardRedemptionRollsBack injects a database rejection during benefit
// crediting. The surrounding transaction must preserve the unused card and
// original weekly usage, allowing the user to retry after the failure is fixed.
func TestResetCardRedemptionRollsBack(t *testing.T) {
	ctx := t.Context()
	client := committedEntitlementClient(t)
	user, err := client.User.Create().SetEmail("reset-rollback@example.com").SetPasswordHash("hash").Save(ctx)
	require.NoError(t, err)
	plan, err := client.SubscriptionPlan.Create().SetName("reset rollback").SetPrice(1).SetValidityDays(30).SetWeeklyLimitUsd(100).Save(ctx)
	require.NoError(t, err)
	store := postgres.NewUserSubscriptionRepository(client)
	service := billing.NewSubscriptionService(nil, store, postgres.NewSubscriptionMutations(client))
	orderID := int64(7004)
	sub, err := service.AssignSubscription(ctx, &billing.AssignSubscriptionInput{UserID: user.ID, PlanID: plan.ID, SourceOrderID: &orderID, ResetCardCount: 1, ResetCardLimitUSD: 585})
	require.NoError(t, err)
	require.NoError(t, client.UserSubscription.UpdateOneID(sub.ID).SetWeeklyWindowStart(time.Now().Add(-time.Hour)).SetWeeklyUsageUsd(80).Exec(ctx))
	cards, err := service.ListResetCards(ctx, user.ID)
	require.NoError(t, err)
	// Reject positive credits after validation to exercise transaction rollback
	// rather than an early application-level eligibility failure.
	_, err = integrationDB.ExecContext(ctx, `ALTER TABLE subscription_reset_cards ADD CONSTRAINT reset_credit_failure CHECK (credited_usd=0)`)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, dropErr := integrationDB.Exec(`ALTER TABLE subscription_reset_cards DROP CONSTRAINT reset_credit_failure`)
		require.NoError(t, dropErr)
	})
	_, err = service.UseResetCard(ctx, user.ID, cards[0].ID, sub.ID)
	require.Error(t, err)
	cards, err = service.ListResetCards(ctx, user.ID)
	require.NoError(t, err)
	require.Nil(t, cards[0].UsedAt)
	require.Zero(t, cards[0].CreditedUSD)
	view, err := service.GetByID(ctx, sub.ID)
	require.NoError(t, err)
	require.Equal(t, float64(80), view.WeeklyUsageUSD)
	require.Empty(t, view.ResetCredits)
}

// TestResetCardExcludesHoldCreatedBeforeWeek creates a task before the current
// quota anchor but reserves its funds afterward. The saved allocation anchor
// must exclude that unsettled hold from credit even across the time boundary.
func TestResetCardExcludesHoldCreatedBeforeWeek(t *testing.T) {
	ctx := t.Context()
	client := committedEntitlementClient(t)
	user, err := client.User.Create().SetEmail("reset-boundary@example.com").SetPasswordHash("hash").SetBalance(100).Save(ctx)
	require.NoError(t, err)
	plan, err := client.SubscriptionPlan.Create().SetName("boundary").SetPrice(1).SetValidityDays(30).SetWeeklyLimitUsd(100).Save(ctx)
	require.NoError(t, err)
	store := postgres.NewUserSubscriptionRepository(client)
	service := billing.NewSubscriptionService(nil, store, postgres.NewSubscriptionMutations(client))
	orderID := int64(7005)
	sub, err := service.AssignSubscription(ctx, &billing.AssignSubscriptionInput{UserID: user.ID, PlanID: plan.ID, SourceOrderID: &orderID, ResetCardCount: 1, ResetCardLimitUSD: 100})
	require.NoError(t, err)
	week := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	require.NoError(t, client.UserSubscription.UpdateOneID(sub.ID).SetWeeklyWindowStart(week).SetWeeklyUsageUsd(80).Exec(ctx))
	key, err := client.APIKey.Create().SetUserID(user.ID).SetKey("boundary-" + uuid.NewString()).SetName("boundary").Save(ctx)
	require.NoError(t, err)
	createdAt := week.Add(-time.Minute)
	batchID := "imgbatch_" + uuid.NewString()
	insertBatchImageAllowanceTestJob(t, batchID, user.ID, user.ID, key.ID, nil, createdAt)
	held, err := newTaskFundsFixture(integrationDB).Reserve(ctx, &billing.TaskFundsCommand{
		RequestID: batchimage.BatchImageHoldRequestID(batchID), APIKeyID: key.ID, UserID: user.ID, ActorUserID: user.ID,
		HoldAmount: 10, ReservedAt: createdAt, Task: batchimage.FundingReference(batchID),
	})
	require.NoError(t, err)
	require.NotNil(t, held.BillingAllocations[0].WeeklyWindowStart)
	require.Equal(t, week, *held.BillingAllocations[0].WeeklyWindowStart)
	cards, err := service.ListResetCards(ctx, user.ID)
	require.NoError(t, err)
	used, err := service.UseResetCard(ctx, user.ID, cards[0].ID, sub.ID)
	require.NoError(t, err)
	require.Equal(t, float64(80), used.CreditedUSD)
}

// TestResetCardBindingAndSubscriptionExpiry preserves another active purchase
// of the same plan while the bound grant expires. Neither an unused card nor
// remaining credited welfare can transfer to that purchase or fund new usage.
func TestResetCardBindingAndSubscriptionExpiry(t *testing.T) {
	ctx := t.Context()
	client := committedEntitlementClient(t)
	user, err := client.User.Create().SetEmail("reset-bound-expiry@example.com").SetPasswordHash("hash").Save(ctx)
	require.NoError(t, err)
	plan, err := client.SubscriptionPlan.Create().SetName("bound plan").SetPrice(1).SetValidityDays(30).SetWeeklyLimitUsd(100).Save(ctx)
	require.NoError(t, err)
	store := postgres.NewUserSubscriptionRepository(client)
	service := billing.NewSubscriptionService(nil, store, postgres.NewSubscriptionMutations(client))
	orderID := int64(7006)
	bound, err := service.AssignSubscription(ctx, &billing.AssignSubscriptionInput{
		UserID: user.ID, PlanID: plan.ID, SourceOrderID: &orderID, ResetCardCount: 2, ResetCardLimitUSD: 585,
	})
	require.NoError(t, err)
	sibling, err := service.AssignSubscription(ctx, &billing.AssignSubscriptionInput{UserID: user.ID, PlanID: plan.ID})
	require.NoError(t, err)
	week := time.Now().UTC().Add(-time.Hour)
	for _, id := range []int64{bound.ID, sibling.ID} {
		require.NoError(t, client.UserSubscription.UpdateOneID(id).SetWeeklyWindowStart(week).SetWeeklyUsageUsd(80).Exec(ctx))
	}
	cards, err := service.ListResetCards(ctx, user.ID)
	require.NoError(t, err)
	require.Len(t, cards, 2)
	_, err = service.UseResetCard(ctx, user.ID, cards[0].ID, sibling.ID)
	require.ErrorIs(t, err, billing.ResetCardError)
	before, err := service.ListResetCards(ctx, user.ID)
	require.NoError(t, err)
	require.Nil(t, before[0].UsedAt)
	used, err := service.UseResetCard(ctx, user.ID, cards[0].ID, bound.ID)
	require.NoError(t, err)
	require.Equal(t, float64(80), used.CreditedUSD)
	// Verify the persistent invariant independently of API ownership checks.
	// Even a direct write cannot redirect a used card to a sibling subscription.
	_, err = integrationDB.ExecContext(ctx, `UPDATE subscription_reset_cards SET target_subscription_id=$1 WHERE id=$2`, sibling.ID, cards[0].ID)
	require.Error(t, err)
	view, err := service.GetByID(ctx, bound.ID)
	require.NoError(t, err)
	require.Equal(t, float64(80), billing.AvailableResetCredit(view.ResetCredits, time.Now()))
	require.NoError(t, client.UserSubscription.UpdateOneID(bound.ID).SetExpiresAt(time.Now().Add(-time.Second)).Exec(ctx))
	_, err = service.UseResetCard(ctx, user.ID, cards[1].ID, bound.ID)
	require.ErrorIs(t, err, billing.ResetCardError)
	_, err = service.UseResetCard(ctx, user.ID, cards[1].ID, sibling.ID)
	require.ErrorIs(t, err, billing.ResetCardError)
	after, err := service.ListResetCards(ctx, user.ID)
	require.NoError(t, err)
	require.Zero(t, after[0].RemainingUSD)
	require.Equal(t, "expired", after[1].Status)
	require.Nil(t, after[1].UsedAt)
	view, err = service.GetByID(ctx, bound.ID)
	require.NoError(t, err)
	require.Equal(t, billing.SubscriptionStatusExpired, view.Status)
	require.Empty(t, view.ResetCredits)
	other, err := service.GetByID(ctx, sibling.ID)
	require.NoError(t, err)
	require.Equal(t, billing.SubscriptionStatusActive, other.Status)
	require.Empty(t, other.ResetCredits)
}
