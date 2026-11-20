package provider

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/plugin"
	"github.com/TokenFlux/TokenRouter/plugin-sdk/go/pluginpb/v1"
)

// usageFundingFixture supplies separate decimal total and wallet funding amounts.
type usageFundingFixture struct {
	plugin.UsageReader // unused usage operations
}

// Stats returns a wallet amount below a threshold and larger total charges.
func (usageFundingFixture) Stats(context.Context, []int64, time.Time, time.Time) ([]plugin.UserUsageView, error) {
	return []plugin.UserUsageView{{UserID: 7, Requests: 2, Tokens: 24, Cost: "100", BalanceCost: "9.99999999"}}, nil
}

// TestGetUsageStatsPreservesWalletCost maps funding observations through the
// authorized host RPC. The exact wallet string reaches the protocol result
// independently of total charges and the actor's request and token counters.
func TestGetUsageStatsPreservesWalletCost(t *testing.T) {
	queries := plugin.NewQueries(plugin.QueryReaders{Usage: usageFundingFixture{}}, nil)
	server := &hostService{
		services: &HostServices{capabilities: HostCapabilities{Queries: queries}},
		binding:  plugin.Binding{PluginID: "test.wallet-usage", Permissions: []string{"usage.read"}},
	}
	response, err := server.GetUsageStats(t.Context(), &pluginpb.GetUsageStatsRequest{UserIds: []int64{7}, StartUnix: 100, EndUnix: 200})
	require.NoError(t, err)
	require.Len(t, response.Stats, 1)
	require.Equal(t, "100", response.Stats[0].Cost)
	require.Equal(t, "9.99999999", response.Stats[0].BalanceCost)
}
