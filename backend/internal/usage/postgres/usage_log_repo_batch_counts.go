package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/lib/pq"

	"github.com/TokenFlux/TokenRouter/internal/usage"
)

// usage_log_repo_batch_counts.go reports retained actor counters and wallet
// charges within an explicit time interval. Rewards use wallet spending while
// ordinary usage reports retain total charges across funding sources. Recorded
// allocations separate the wallet portion of a mixed request from its
// subscription portion, so filtering by a request's billing type would lose
// eligible wallet spending.
//
// Older balance requests can lack allocation fields. Only records classified
// as balance-funded, with no subscription identity or allocation, use their
// actual cost as a historical wallet amount. Subscription records with missing
// splits contribute zero. Decimal aggregation and truncation keep fractional
// subunits from advancing a reward threshold.

// GetBatchUserUsageCounts aggregates one bounded interval for distinct positive actor IDs.
// Missing usage remains a zero-valued requested entry. Empty normalized input
// returns immediately, and nonempty queries reject absent or reversed time bounds.
// Wallet costs use recorded balance allocations, or historical balance charges
// when both allocation amounts and the subscription identity are absent. A mixed
// request contributes its wallet amount even when its billing type is subscription.
func (r *Store) GetBatchUserUsageCounts(ctx context.Context, ids []int64, start, end time.Time) (map[int64]usage.BatchUserUsageCounts, error) {
	selected := normalizePositiveInt64IDs(ids)
	result := make(map[int64]usage.BatchUserUsageCounts, len(selected))
	if len(selected) == 0 {
		return result, nil
	}
	if start.IsZero() || !end.After(start) {
		return nil, fmt.Errorf("batch user counters require an ordered reporting interval")
	}
	for _, id := range selected {
		result[id] = usage.BatchUserUsageCounts{UserID: id, BalanceCost: "0"}
	}
	// Historical wallet rows predate allocation columns. Require balance funding and
	// no subscription evidence before using actual_cost, keeping incomplete subscription
	// records from becoming reward-eligible wallet spending. Sum eligible charges
	// before truncating subunits so reward comparisons use the plugin's monetary
	// precision while retaining amounts accumulated across separate requests.
	rows, err := r.sql.QueryContext(ctx, `SELECT user_id,COUNT(*),
COALESCE(SUM(input_tokens + output_tokens + cache_creation_tokens + cache_read_tokens),0),
TRUNC(COALESCE(SUM(CASE
    WHEN balance_amount_usd > 0 THEN balance_amount_usd
    WHEN billing_type = 0 AND subscription_id IS NULL AND subscription_amount_usd = 0
        THEN GREATEST(actual_cost, 0)
    ELSE 0
END), 0), 8)::text
FROM usage_logs WHERE user_id=ANY($1) AND created_at >= $2 AND created_at < $3 GROUP BY user_id`, pq.Array(selected), start, end)
	if err != nil {
		return nil, err
	}
	// Release query resources after every scan path; deferred cleanup leaves scan and
	// iteration errors as the primary failure returned to the plugin reader.
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var value usage.BatchUserUsageCounts
		if err := rows.Scan(&value.UserID, &value.Requests, &value.Tokens, &value.BalanceCost); err != nil {
			return nil, err
		}
		result[value.UserID] = value
	}
	return result, rows.Err()
}
