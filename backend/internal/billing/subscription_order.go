package billing

import (
	"context"
	"sort"

	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
)

// subscription_order.go persists user-selected subscription spending order and
// applies that order to admission views. Each subscription owns its quota and
// lifetime; ordering chooses which eligible grant funds an automatic request
// first. Expiry, start and record identity provide deterministic ordering when
// priorities match, including records created before custom ordering existed.
//
// Reordering acquires the payer lock shared by issuance and settlement before
// checking ownership or writing ranks. All submitted identities are validated
// before the first update, and the transaction rolls back partial writes.
// Concurrently added grants omitted by a stale client are appended in their
// existing order so saving an order cannot discard an entitlement.

// ErrSubscriptionOrderInvalid rejects repeated, missing or ineligible order IDs
// before any rank is written, allowing callers to refresh their subscription list.
var ErrSubscriptionOrderInvalid = apperror.BadRequest("SUBSCRIPTION_ORDER_INVALID", "subscription order contains unavailable or duplicate subscriptions")

// SubscriptionOrderWriter updates only spending rank under the service's payer
// lock. Its implementation joins the context transaction and checks ownership.
type SubscriptionOrderWriter interface {
	// SetConsumptionPriority updates the owned grant's rank without rewriting a
	// stale quota or lifetime snapshot. The caller supplies its transaction and
	// retains commit, rollback and payer-lock ownership.
	SetConsumptionPriority(context.Context, int64, int64, int) error
}

// SortSubscriptions orders a caller-owned slice for automatic consumption.
// Lower saved priorities precede earlier expiry, earlier start and smaller ID;
// sorting changes the outer slice while retaining the records' nested values.
func SortSubscriptions(subscriptions []UserSubscription) {
	sort.SliceStable(subscriptions, func(i, j int) bool {
		a, b := subscriptions[i], subscriptions[j]
		if a.ConsumptionPriority != b.ConsumptionPriority {
			return a.ConsumptionPriority < b.ConsumptionPriority
		}
		if !a.ExpiresAt.Equal(b.ExpiresAt) {
			return a.ExpiresAt.Before(b.ExpiresAt)
		}
		if !a.StartsAt.Equal(b.StartsAt) {
			return a.StartsAt.Before(b.StartsAt)
		}
		return a.ID < b.ID
	})
}

// ReorderSubscriptions saves the authenticated payer's requested spending order.
// IDs must identify unique active or pending grants owned by that payer;
// suspended, expired and deleted grants cannot be included. Omitted eligible
// grants follow the submitted IDs in their previous order.
//
// The payer lock serializes ordering with new purchases and locked settlement.
// Ownership and eligibility are checked on a fresh read before any write, then
// every rank is updated in the same transaction. A storage error aborts that
// transaction, preserving the previous complete order rather than partial ranks.
// Explicitly bound API Keys continue to use their selected subscription.
// @project-doc docs/domains/payments_and_entitlements.md#subscription_consumption_order
func (s *SubscriptionService) ReorderSubscriptions(ctx context.Context, userID int64, ids []int64) error {
	writer, ok := s.userSubRepo.(SubscriptionOrderWriter)
	if !ok {
		return apperror.InternalServer("SUBSCRIPTION_ORDER_UNAVAILABLE", "subscription ordering is unavailable")
	}
	return s.transactions.Within(ctx, func(txCtx context.Context) error {
		// Settlement and issuance lock the payer first. Share that lock before
		// reading eligibility so a concurrent purchase cannot be lost during ranking.
		if err := s.transactions.LockGrantUser(txCtx, userID); err != nil {
			return err
		}
		subscriptions, err := s.userSubRepo.ListByUserID(txCtx, userID)
		if err != nil {
			return err
		}
		SortSubscriptions(subscriptions)
		eligible := make(map[int64]bool, len(subscriptions))
		now := s.clock.now()
		for _, sub := range subscriptions {
			status := sub.EffectiveStatus(now)
			if sub.UserID == userID && (status == SubscriptionStatusActive || status == SubscriptionStatusPending) {
				eligible[sub.ID] = true
			}
		}
		order := make([]int64, 0, len(eligible))
		for _, id := range ids {
			if !eligible[id] {
				return ErrSubscriptionOrderInvalid
			}
			delete(eligible, id)
			order = append(order, id)
		}
		for _, sub := range subscriptions {
			if eligible[sub.ID] {
				order = append(order, sub.ID)
			}
		}
		// Persist only ranks after validating the entire request; counter and
		// lifetime updates remain owned by settlement and grant maintenance respectively.
		for priority, id := range order {
			if err := writer.SetConsumptionPriority(txCtx, userID, id, priority); err != nil {
				return err
			}
		}
		return nil
	})
}
