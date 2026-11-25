//go:build integration

package migrations_test

import (
	"context"
	"database/sql"
	"io/fs"
	"testing"
	"testing/fstest"
	"time"

	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/TokenFlux/TokenRouter/internal/infra/postgres"
	"github.com/TokenFlux/TokenRouter/migrations"
)

// subscription_reset_card_policy_integration_test.go verifies the transition
// from independently configured card caps to weekly-derived purchase terms.
// Real PostgreSQL checks normalize existing plans without changing issued
// benefits or order snapshots, while fresh installs and replays enforce the
// same week/month quota eligibility constraint.

// TestSubscriptionResetCardPolicyMigration upgrades seeded pre-316 plans and
// verifies captured cards and orders survive unchanged. It also applies the
// complete migration set to a fresh schema and replays the current policy SQL.
func TestSubscriptionResetCardPolicyMigration(t *testing.T) {
	ctx := t.Context()
	container, err := tcpostgres.Run(ctx, "postgres:18.1-alpine3.23", tcpostgres.WithDatabase("reset_policy"),
		tcpostgres.WithUsername("postgres"), tcpostgres.WithPassword("postgres"), tcpostgres.BasicWaitStrategies())
	// Register cleanup even after partial startup, with a fresh deadline that
	// remains usable after testing cancels the scenario's own context.
	if container != nil {
		t.Cleanup(func() {
			cleanup, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			require.NoError(t, container.Terminate(cleanup))
		})
	}
	require.NoError(t, err)
	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	// Close the pool before removing its container, including failures during
	// migration setup or assertions against the preserved purchase records.
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	prior := fstest.MapFS{}
	entries, err := fs.ReadDir(migrations.FS, ".")
	require.NoError(t, err)
	for _, entry := range entries {
		if entry.IsDir() || entry.Name() >= "316" {
			continue
		}
		data, err := migrations.FS.ReadFile(entry.Name())
		require.NoError(t, err)
		prior[entry.Name()] = &fstest.MapFile{Data: data}
	}
	require.NoError(t, postgres.ApplyMigrations(ctx, db, prior))
	_, err = db.ExecContext(ctx, `INSERT INTO subscription_plans(id,name,price,weekly_limit_usd,monthly_limit_usd,reset_card_count,reset_card_limit_usd)
 VALUES(901,'eligible',10,585,2340,1,50),(902,'weekly only',10,585,NULL,1,50),
 (903,'disabled',10,585,2340,0,50);
 INSERT INTO users(id,email,password_hash) VALUES(901,'policy@example.com','hash');
 INSERT INTO user_subscriptions(id,user_id,plan_id,starts_at,expires_at)
 VALUES(901,901,901,NOW(),NOW()+INTERVAL '30 days');
 INSERT INTO subscription_reset_cards(user_id,plan_id,source_subscription_id,ordinal,limit_usd,expires_at)
 VALUES(901,901,901,1,50,NOW()+INTERVAL '30 days');
 INSERT INTO payment_orders(user_id,amount,pay_amount,expires_at,plan_snapshot)
 VALUES(901,10,10,NOW()+INTERVAL '1 hour','{"reset_card_count":1,"reset_card_limit_usd":50}');`)
	require.NoError(t, err)
	require.NoError(t, postgres.ApplyMigrations(ctx, db, migrations.FS))
	var count int
	var cap float64
	for _, expected := range []struct {
		id    int     // seeded plan identity
		count int     // surviving purchase card count
		cap   float64 // derived future purchase cap USD
	}{{901, 1, 585}, {902, 0, 0}, {903, 0, 0}} {
		require.NoError(t, db.QueryRowContext(ctx, `SELECT reset_card_count,reset_card_limit_usd FROM subscription_plans WHERE id=$1`, expected.id).Scan(&count, &cap))
		require.Equal(t, expected.count, count)
		require.Equal(t, expected.cap, cap)
	}
	require.NoError(t, db.QueryRowContext(ctx, `SELECT limit_usd FROM subscription_reset_cards WHERE source_subscription_id=901`).Scan(&cap))
	require.Equal(t, 50.0, cap)
	require.NoError(t, db.QueryRowContext(ctx, `SELECT (plan_snapshot->>'reset_card_limit_usd')::numeric FROM payment_orders WHERE user_id=901`).Scan(&cap))
	require.Equal(t, 50.0, cap)
	_, err = db.ExecContext(ctx, `UPDATE subscription_plans SET monthly_limit_usd=NULL WHERE id=901`)
	require.Error(t, err)
	policy, err := migrations.FS.ReadFile("316_subscription_reset_card_plan_policy.sql")
	require.NoError(t, err)
	// Reexecuting policy SQL must retain derived caps and leave migration
	// bookkeeping intact; the runner separately recognizes already applied files.
	_, err = db.ExecContext(ctx, string(policy))
	require.NoError(t, err)
	require.NoError(t, postgres.ApplyMigrations(ctx, db, migrations.FS))
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE filename='316_subscription_reset_card_plan_policy.sql'`).Scan(&count))
	require.Equal(t, 1, count)
	// A fresh schema exercises every embedded migration rather than relying
	// solely on the upgrade fixture's previously populated tables and constraints.
	_, err = db.ExecContext(ctx, `DROP SCHEMA public CASCADE; CREATE SCHEMA public`)
	require.NoError(t, err)
	require.NoError(t, postgres.ApplyMigrations(ctx, db, migrations.FS))
}
