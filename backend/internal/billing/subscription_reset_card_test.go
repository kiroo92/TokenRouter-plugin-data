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
// Current sale eligibility is tested separately from captured historical purchase terms.

// TestValidateResetCardPolicy accepts historical purchased caps independently of
// current plan policy. Issuance must retain valid promised benefits while
// rejecting invalid quantities, nonfinite money and unlimited-week templates.
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

// TestResetCardPlanLimit checks the current purchase policy across missing,
// unlimited and invalid quota combinations. Only plans with finite positive
// weekly and monthly quotas can enable cards, whose cap follows weekly quota.
func TestResetCardPlanLimit(t *testing.T) {
	weekly, monthly, zero := 585.0, 2340.0, 0.0
	cap, err := ResetCardPlanLimit(1, &weekly, &monthly)
	require.NoError(t, err)
	require.Equal(t, weekly, cap)
	cap, err = ResetCardPlanLimit(0, nil, nil)
	require.NoError(t, err)
	require.Zero(t, cap)
	infinite, tiny := math.Inf(1), 1e-9
	for _, terms := range []struct {
		count   int      // included purchase card quantity
		weekly  *float64 // configured weekly quota USD
		monthly *float64 // configured monthly quota USD
	}{
		{1, nil, &monthly},
		{1, &weekly, nil},
		{1, &zero, &monthly},
		{1, &weekly, &zero},
		{1, &infinite, &monthly},
		{1, &weekly, &infinite},
		{1, &tiny, &monthly},
		{-1, &weekly, &monthly},
		{101, &weekly, &monthly},
	} {
		_, err := ResetCardPlanLimit(terms.count, terms.weekly, terms.monthly)
		require.Error(t, err)
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
