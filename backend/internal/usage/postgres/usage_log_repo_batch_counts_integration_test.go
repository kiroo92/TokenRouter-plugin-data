//go:build integration

package postgres

import (
	"time"

	"github.com/google/uuid"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/usage"
)

// TestBatchCountsIncludeZeroChargeAndRespectEndBoundary records billable, free, and out-of-range actor requests.
// Counts include the free request, exclude the exact end boundary, and preserve a zero row for an actor without retained usage.
func (s *UsageLogRepoSuite) TestBatchCountsIncludeZeroChargeAndRespectEndBoundary() {
	user := mustCreateUser(s.T(), s.client, &identity.User{Email: "plugin-counts@test.local"})
	other := mustCreateUser(s.T(), s.client, &identity.User{Email: "plugin-counts-empty@test.local"})
	key := mustCreateApiKey(s.T(), s.client, &apikey.APIKey{UserID: user.ID, Key: "plugin-counts-key", Name: "fixture"})
	upstream := mustCreateProvider(s.T(), s.client, &provider.Record{Name: "plugin-counts-provider"})
	start := time.Now().Add(-time.Hour)
	end := start.Add(30 * time.Minute)
	s.createUsageLog(user, key, upstream, 10, 20, 0.5, start)
	s.createUsageLog(user, key, upstream, 5, 7, 0, start.Add(time.Minute))
	s.createUsageLog(user, key, upstream, 100, 200, 1, end)
	result, err := s.repo.GetBatchUserUsageCounts(s.ctx, []int64{user.ID, other.ID}, start, end)
	s.Require().NoError(err)
	s.Require().Equal(int64(2), result[user.ID].Requests)
	s.Require().Equal(int64(42), result[user.ID].Tokens)
	s.Require().Zero(result[other.ID].Requests)
	s.Require().Equal("0.50000000", result[user.ID].BalanceCost)
	s.Require().Equal("0", result[other.ID].BalanceCost)
}

// TestBatchCountsSeparatesWalletFromSubscriptionSpending records wallet-only,
// subscription-only, mixed, legacy, and out-of-range charges. Reward spending
// includes mixed wallet allocations and historical wallet charges while excluding
// subscription amounts, and decimal truncation keeps fractional subunits below
// the next monetary threshold.
func (s *UsageLogRepoSuite) TestBatchCountsSeparatesWalletFromSubscriptionSpending() {
	user := mustCreateUser(s.T(), s.client, &identity.User{Email: "plugin-wallet-cost@test.local"})
	key := mustCreateApiKey(s.T(), s.client, &apikey.APIKey{UserID: user.ID, Key: "plugin-wallet-cost-key", Name: "fixture"})
	upstream := mustCreateProvider(s.T(), s.client, &provider.Record{Name: "plugin-wallet-cost-provider"})
	start := time.Now().Add(-time.Hour)
	end := start.Add(30 * time.Minute)
	for _, entry := range []struct {
		billingType  int8      // stored request funding classification
		actual       float64   // total user charge
		subscription float64   // subscription-funded charge
		balance      float64   // wallet-funded charge
		created      time.Time // request timestamp within reporting range
	}{
		{usage.BillingTypeBalance, 2, 0, 2, start},
		{usage.BillingTypeSubscription, 100, 100, 0, start},
		{usage.BillingTypeSubscription, 53, 50, 3, start},
		{usage.BillingTypeBalance, 1.9999999999, 0, 0, start},
		{usage.BillingTypeSubscription, 200, 0, 0, start},
		{usage.BillingTypeBalance, 1000, 0, 1000, end},
	} {
		_, err := s.client.UsageLog.Create().
			SetUserID(user.ID).
			SetAPIKeyID(key.ID).
			SetProviderID(upstream.ID).
			SetRequestID(uuid.NewString()).
			SetModel("wallet-cost-fixture").
			SetBillingType(entry.billingType).
			SetActualCost(entry.actual).
			SetSubscriptionAmountUsd(entry.subscription).
			SetBalanceAmountUsd(entry.balance).
			SetCreatedAt(entry.created).
			Save(s.ctx)
		s.Require().NoError(err)
	}
	result, err := s.repo.GetBatchUserUsageCounts(s.ctx, []int64{user.ID}, start, end)
	s.Require().NoError(err)
	s.Require().Equal(int64(5), result[user.ID].Requests)
	s.Require().Equal("6.99999999", result[user.ID].BalanceCost)
}
