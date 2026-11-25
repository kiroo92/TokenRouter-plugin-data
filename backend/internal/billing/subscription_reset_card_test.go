package billing

import (
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// subscription_reset_card_test.go checks paid-benefit policy validation and
// expiry boundaries without persistence. PostgreSQL scenarios separately verify
// purchase snapshots, one-time redemption and financial transaction recovery.

// TestValidateResetCardPolicy accepts configured Light and Premium caps while
// rejecting invalid quantities, nonfinite money and unlimited-week templates.
// Disabled policies remain valid for plans without a finite weekly quota.
func TestValidateResetCardPolicy(t *testing.T) {
	weekly := 2000.0
	require.NoError(t, ValidateResetCardPolicy(1, 585, &weekly))
	require.NoError(t, ValidateResetCardPolicy(1, 1162.5, &weekly))
	require.NoError(t, ValidateResetCardPolicy(0, 0, nil))
	for _, test := range []struct {
		count  int      // included purchase card quantity
		cap    float64  // USD per-card benefit cap
		weekly *float64 // finite weekly template limit
	}{{-1, 585, &weekly}, {101, 585, &weekly}, {1, 0, &weekly}, {1, 1e-9, &weekly}, {1, math.Inf(1), &weekly}, {1, math.NaN(), &weekly}, {1, 585, nil}} {
		require.Error(t, ValidateResetCardPolicy(test.count, test.cap, test.weekly))
	}
}

// TestAvailableResetCreditUsesExclusiveExpiry includes one live card alongside
// expired and exhausted balances. Equality at the week boundary provides no
// funding, preventing an unused benefit from carrying into the next week.
func TestAvailableResetCreditUsesExclusiveExpiry(t *testing.T) {
	now := time.Now()
	credits := []WeeklyResetCredit{
		{ID: 1, RemainingUSD: 585, ExpiresAt: now.Add(time.Second)},
		{ID: 2, RemainingUSD: 1162.5, ExpiresAt: now},
		{ID: 3, RemainingUSD: 0, ExpiresAt: now.Add(time.Hour)},
	}
	require.Equal(t, 585.0, AvailableResetCredit(credits, now))
	require.Zero(t, AvailableResetCredit(credits, now.Add(time.Second)))
}
