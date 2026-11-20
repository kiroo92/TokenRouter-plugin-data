package usage

import (
	"context"
	"errors"
	"time"
)

// dashboard_user_counts.go exposes bounded-period actor request counts, token
// counts, and wallet-funded costs alongside the existing batch charge report.
// Counters include retained rows with zero charge. Wallet totals identify the
// portion eligible for rewards that depend on spending an account balance.

// BatchUserUsageCounts contains retained actor counters and wallet-funded spending totals.
type BatchUserUsageCounts struct {
	UserID   int64 // queried actor account identifier
	Requests int64 // retained period request count
	Tokens   int64 // retained period token count
	// BalanceCost is a decimal wallet charge truncated to eight fractional digits.
	// It includes mixed-funding wallet portions and uses "0" for missing usage.
	BalanceCost string
}

// BatchUserUsageCountReader retrieves actor counters over an explicit exclusive-end interval.
type BatchUserUsageCountReader interface {
	// GetBatchUserUsageCounts returns zero-valued entries for requested actors without usage.
	// Empty normalized IDs produce an empty map. Nonempty requests require explicit
	// ordered time bounds, and counters include zero-charge retained usage records.
	// Wallet costs exclude subscription allocations and retain decimal text.
	GetBatchUserUsageCounts(context.Context, []int64, time.Time, time.Time) (map[int64]BatchUserUsageCounts, error)
}

// GetBatchUserUsageCounts retrieves actor counters and decimal wallet spending.
// The configured repository implements the dedicated count capability. A missing
// implementation returns an error because the charge report lacks these counters
// and cannot determine the wallet-funded portion of a request.
func (s *DashboardService) GetBatchUserUsageCounts(ctx context.Context, ids []int64, start, end time.Time) (map[int64]BatchUserUsageCounts, error) {
	reader, ok := s.usageRepo.(BatchUserUsageCountReader)
	if !ok {
		return nil, errors.New("batch user usage counters are unavailable")
	}
	return reader.GetBatchUserUsageCounts(ctx, ids, start, end)
}
