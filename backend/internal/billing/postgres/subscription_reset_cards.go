package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"time"

	"github.com/lib/pq"

	"github.com/TokenFlux/TokenRouter/internal/billing"
)

// subscription_reset_cards.go stores purchase cards and their expiring weekly
// benefit balances. Issuance joins the subscription grant transaction. Redemption
// locks the payer before reading the card and target so settlement, refunds and
// simultaneous clicks observe one serialized state. The original subscription
// counters and period anchors are never rewritten by redemption.
//
// Source ownership, lifetime and refund holds remain authoritative after a card
// has been used. Benefit reads exclude unavailable sources and expired weeks.
// Outstanding task holds are subtracted from original weekly usage before a
// credit is calculated, because reservations have not yet become actual spend.
// Financial settlement records the card ID and refunds that balance separately.

// resetCreditPredicate requires a live source for both card redemption and
// future benefit spending, so revoked or refund-held purchases stop funding.
const resetCreditPredicate = `source.deleted_at IS NULL
 AND source.refund_hold_operation_id IS NULL AND source.status IN ('active','pending')
 AND source.starts_at <= NOW() AND source.expires_at > NOW()`

// resetCreditList decodes only SQL-generated benefit JSON into quota snapshots.
type resetCreditList []billing.WeeklyResetCredit

// Scan decodes one database JSON value and propagates malformed data to the
// caller. Both text and byte representations are accepted; unsupported driver
// values fail before a funding decision can fall back to wallet balance.
func (credits *resetCreditList) Scan(value any) error {
	var raw []byte
	switch typed := value.(type) {
	case []byte:
		raw = typed
	case string:
		raw = []byte(typed)
	case nil:
		*credits = nil
		return nil
	default:
		return fmt.Errorf("invalid reset credit JSON type %T", value)
	}
	return json.Unmarshal(raw, credits)
}

// resetCreditJSON returns a correlated SQL expression for active benefit
// balances on a trusted internal subscription alias. Callers supply fixed SQL
// aliases; user input never becomes part of the returned query text.
func resetCreditJSON(alias string) string {
	return `COALESCE((SELECT jsonb_agg(jsonb_build_object('id',card.id,
 'remaining_usd',card.credited_usd-card.used_usd,
 'expires_at',LEAST(card.benefit_expires_at,card.expires_at,source.expires_at)) ORDER BY card.benefit_expires_at,card.id)
 FROM subscription_reset_cards card JOIN user_subscriptions source ON source.id=card.source_subscription_id
 WHERE card.target_subscription_id=` + alias + `.id AND card.source_subscription_id=` + alias + `.id AND card.used_at IS NOT NULL
 AND card.credited_usd>card.used_usd AND card.benefit_expires_at>NOW()
 AND card.expires_at>NOW() AND ` + resetCreditPredicate + `),'[]'::jsonb)`
}

// IssueResetCards inserts the paid grant's captured benefit quantity once.
// It requires the caller's existing Ent transaction and positive finite terms;
// the source/ordinal uniqueness key also prevents duplicate issuance on replay.
func (r *SubscriptionStore) IssueResetCards(ctx context.Context, sub *billing.UserSubscription, count int, cap float64) error {
	if err := billing.ValidateResetCardPolicy(count, cap, sub.WeeklyLimitUSD); err != nil {
		return err
	}
	client := clientFromContext(ctx, r.client)
	_, err := client.ExecContext(ctx, `INSERT INTO subscription_reset_cards
 (user_id,plan_id,source_subscription_id,ordinal,limit_usd,expires_at)
 SELECT $1,$2,$3,n,$4,$5 FROM generate_series(1,$6) AS n
 ON CONFLICT (source_subscription_id,ordinal) DO NOTHING`, sub.UserID, sub.PlanID, sub.ID, cap, sub.ExpiresAt, count)
	return err
}

// ListResetCards reads the owner's cards and derives their current status.
// Issued expiry is capped by the source's current expiry; used cards retain
// their historical credit even when their remaining benefit is no longer usable.
func (r *SubscriptionStore) ListResetCards(ctx context.Context, userID int64) ([]billing.SubscriptionResetCard, error) {
	client := clientFromContext(ctx, r.client)
	rows, err := client.QueryContext(ctx, `SELECT card.id,card.plan_id,card.source_subscription_id,
 plan.name,card.limit_usd,LEAST(card.expires_at,source.expires_at),card.used_at,
 card.target_subscription_id,card.credited_usd,
 CASE WHEN card.benefit_expires_at>NOW() AND card.expires_at>NOW() AND `+resetCreditPredicate+`
 THEN card.credited_usd-card.used_usd ELSE 0 END,card.benefit_expires_at,
 CASE WHEN card.used_at IS NOT NULL THEN 'used'
 WHEN LEAST(card.expires_at,source.expires_at)<=NOW() THEN 'expired'
 WHEN `+resetCreditPredicate+` THEN 'available' ELSE 'unavailable' END
 FROM subscription_reset_cards card JOIN user_subscriptions source ON source.id=card.source_subscription_id
 JOIN subscription_plans plan ON plan.id=card.plan_id WHERE card.user_id=$1 ORDER BY card.id`, userID)
	if err != nil {
		return nil, err
	}
	// Close the inventory cursor before returning, including partial read failures.
	// Its connection belongs to the selected Ent client or enclosing transaction.
	defer func() { _ = rows.Close() }()
	result := make([]billing.SubscriptionResetCard, 0)
	for rows.Next() {
		var card billing.SubscriptionResetCard
		if err := rows.Scan(&card.ID, &card.PlanID, &card.SourceSubscriptionID, &card.PlanName, &card.LimitUSD,
			&card.ExpiresAt, &card.UsedAt, &card.TargetSubscriptionID, &card.CreditedUSD, &card.RemainingUSD, &card.BenefitExpiresAt, &card.Status); err != nil {
			return nil, err
		}
		result = append(result, card)
	}
	return result, rows.Err()
}

// LoadResetCredits enriches grant views with current card funding in one read.
// The supplied slice is mutated only after each row's JSON is decoded; callers
// discard the result on any error and retain original quota observations.
func (r *SubscriptionStore) LoadResetCredits(ctx context.Context, subs []billing.UserSubscription) error {
	if len(subs) == 0 {
		return nil
	}
	ids := make([]int64, len(subs))
	for i := range subs {
		ids[i] = subs[i].ID
		subs[i].ResetCredits = nil
	}
	client := clientFromContext(ctx, r.client)
	rows, err := client.QueryContext(ctx, `SELECT us.id,`+resetCreditJSON("us")+` FROM user_subscriptions us WHERE us.id=ANY($1)`, pq.Array(ids))
	if err != nil {
		return err
	}
	// Release the batch cursor on every path before another entitlement query;
	// malformed internally generated JSON remains a visible storage error.
	defer func() { _ = rows.Close() }()
	byID := make(map[int64][]billing.WeeklyResetCredit, len(subs))
	for rows.Next() {
		var id int64
		var raw []byte
		if err := rows.Scan(&id, &raw); err != nil {
			return err
		}
		var credits []billing.WeeklyResetCredit
		if err := json.Unmarshal(raw, &credits); err != nil {
			return err
		}
		byID[id] = credits
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for i := range subs {
		subs[i].ResetCredits = byID[subs[i].ID]
	}
	return nil
}

// UseResetCard atomically redeems a card or returns its original same-target
// result. Payer locking coordinates with existing grant and settlement locks.
// Each card is bound to its originating paid subscription. Frozen, expired,
// foreign or other subscription identities cannot consume that card;
// only settled original weekly spending contributes to the capped credit.
//
// Redemption writes the target, credit and original week end in one update.
// It leaves base usage and refresh anchors intact. A failed validation or write
// rolls back the entire transaction, preserving an unused card for a retry.
func (r *SubscriptionStore) UseResetCard(ctx context.Context, userID, cardID, targetID int64) (*billing.SubscriptionResetCard, error) {
	mutations := NewSubscriptionMutations(r.client)
	var result *billing.SubscriptionResetCard
	err := mutations.Within(ctx, func(txCtx context.Context) error {
		// Match settlement's payer-first lock order before card or grant reads,
		// preventing concurrent clicks from spending the same one-time entitlement.
		if err := mutations.LockGrantUser(txCtx, userID); err != nil {
			return err
		}
		cards, err := r.ListResetCards(txCtx, userID)
		if err != nil {
			return err
		}
		var selected *billing.SubscriptionResetCard
		for i := range cards {
			if cards[i].ID == cardID {
				selected = &cards[i]
				break
			}
		}
		if selected == nil || selected.SourceSubscriptionID != targetID {
			return billing.ResetCardError
		}
		if selected.UsedAt != nil {
			if selected.TargetSubscriptionID == nil || *selected.TargetSubscriptionID != targetID {
				return billing.ResetCardError
			}
			result = selected
			return nil
		}
		if selected.Status != "available" || !selected.ExpiresAt.After(time.Now()) {
			return billing.ResetCardError
		}
		target, err := r.GetByID(txCtx, targetID)
		if err != nil || target.UserID != userID || target.PlanID != selected.PlanID || !target.IsEffective() {
			return billing.ResetCardError
		}
		now := time.Now()
		// Normalize elapsed base windows before calculating the current week's
		// settled spending; this is an in-memory view and does not move refresh times.
		normalized := billing.NormalizeSettlementSubscription(billing.SettlementSubscription{
			StartsAt: target.StartsAt, ExpiresAt: target.ExpiresAt, DailyWindowStart: target.DailyWindowStart,
			WeeklyWindowStart: target.WeeklyWindowStart, MonthlyWindowStart: target.MonthlyWindowStart,
			DailyLimitUSD: target.DailyLimitUSD, WeeklyLimitUSD: target.WeeklyLimitUSD, MonthlyLimitUSD: target.MonthlyLimitUSD,
			DailyUsageUSD: target.DailyUsageUSD, WeeklyUsageUSD: target.WeeklyUsageUSD, MonthlyUsageUSD: target.MonthlyUsageUSD,
		}, now)
		if normalized.WeeklyWindowStart == nil || !billing.FinitePositiveSubscriptionLimit(target.WeeklyLimitUSD) {
			return billing.ResetCardError
		}
		target.WeeklyWindowStart = normalized.WeeklyWindowStart
		weekEnd := target.WeeklyResetTime()
		if weekEnd == nil || !weekEnd.After(now) {
			return billing.ResetCardError
		}
		client := clientFromContext(txCtx, r.client)
		// Task allowance flags are cleared in the same funding transaction as
		// capture or release. Exclude outstanding original-quota holds from rewards.
		rows, err := client.QueryContext(txCtx, `SELECT COALESCE(SUM((allocation->>'amount_usd')::numeric),0)
 FROM (SELECT subscription_hold_allocations,created_at FROM creative_runs WHERE user_id=$1 AND allowance_reserved=true
 UNION ALL SELECT subscription_hold_allocations,created_at FROM batch_image_jobs WHERE user_id=$1 AND allowance_reserved=true) task,
 LATERAL jsonb_array_elements(COALESCE(task.subscription_hold_allocations,'[]'::jsonb)) allocation
 WHERE (allocation->>'subscription_id')::bigint=$2 AND allocation->>'type'='subscription' AND allocation->>'reset_card_id' IS NULL
 AND (CASE WHEN allocation->>'weekly_window_start' IS NOT NULL THEN
 (allocation->>'weekly_window_start')::timestamptz=$3 ELSE task.created_at >= $3 END)`, userID, targetID, *normalized.WeeklyWindowStart)
		if err != nil {
			return err
		}
		reserved := 0.0
		if !rows.Next() {
			_ = rows.Close()
			return fmt.Errorf("read subscription reservation total")
		}
		err = rows.Scan(&reserved)
		// Close before the redemption write on this transaction's connection.
		// Its scan error remains primary if cleanup also reports a failure.
		_ = rows.Close()
		if err != nil {
			return err
		}
		credit := math.Min(selected.LimitUSD, math.Max(0, normalized.WeeklyUsageUSD-reserved))
		if credit <= 0 {
			return billing.ResetCardError
		}
		end := *weekEnd
		if selected.ExpiresAt.Before(end) {
			end = selected.ExpiresAt
		}
		updated, err := client.ExecContext(txCtx, `UPDATE subscription_reset_cards SET target_subscription_id=$1,used_at=$2,
 credited_usd=TRUNC($3::numeric,8),benefit_expires_at=$4 WHERE id=$5 AND user_id=$6 AND used_at IS NULL
 AND source_subscription_id=$1 AND TRUNC($3::numeric,8)>0 AND $4>statement_timestamp()`, targetID, now, credit, end, cardID, userID)
		if err != nil {
			return err
		}
		count, err := updated.RowsAffected()
		if err != nil {
			return err
		}
		if count != 1 {
			return billing.ResetCardError
		}
		// Reread the database-truncated credit before returning, so initial
		// redemption and an idempotent replay report the same eight-place amount.
		stored, err := r.ListResetCards(txCtx, userID)
		if err != nil {
			return err
		}
		for i := range stored {
			if stored[i].ID == cardID {
				result = &stored[i]
				break
			}
		}
		if result == nil {
			return billing.ResetCardError
		}
		return nil
	})
	return result, err
}
