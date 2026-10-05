package model

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/internal/credittransition"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestCreditPreparationPostgresPreservesEveryExistingValue(t *testing.T) {
	dsn := os.Getenv("CREDIT_TRANSITION_TEST_DSN")
	if dsn == "" {
		t.Skip("CREDIT_TRANSITION_TEST_DSN requires an isolated fixture database")
	}
	parsed, err := url.Parse(dsn)
	require.NoError(t, err)
	require.Equal(t, "/credit_transition_test", parsed.Path, "never run this fixture against an application database")
	require.True(t, parsed.Hostname() == "127.0.0.1" || parsed.Hostname() == "localhost" || (parsed.Hostname() == "" && strings.HasPrefix(parsed.Query().Get("host"), "/")), "fixture must use a local isolated PostgreSQL server")
	admin, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	schema := fmt.Sprintf("credit_prepare_%d", os.Getpid())
	require.NoError(t, admin.Exec(`CREATE SCHEMA `+schema).Error)
	t.Cleanup(func() {
		require.NoError(t, admin.Exec(`DROP SCHEMA `+schema+` CASCADE`).Error)
		pool, _ := admin.DB()
		if pool != nil {
			_ = pool.Close()
		}
	})
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	db, err := gorm.Open(postgres.Open(parsed.String()), &gorm.Config{})
	require.NoError(t, err)
	pool, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = pool.Close() })
	for _, statement := range []string{
		`CREATE TABLE options (key text PRIMARY KEY, value text NOT NULL)`,
		`CREATE TABLE users (id bigint PRIMARY KEY, quota bigint, used_quota bigint)`,
		`CREATE TABLE top_ups (id bigint PRIMARY KEY, credited_quota bigint, refunded_quota bigint, money numeric)`,
		`CREATE TABLE user_subscriptions (id bigint PRIMARY KEY, amount_total bigint, amount_used bigint)`,
		`INSERT INTO users VALUES (1,100000000,5000000)`,
		`INSERT INTO top_ups VALUES (1,100000000,3000000,20)`,
		`INSERT INTO user_subscriptions VALUES (1,100000000,4000000)`,
	} {
		require.NoError(t, db.Exec(statement).Error)
	}
	config := credittransition.Config{Format: credittransition.Format, TransitionID: "fixture-credit", FinancialPlanSHA256: strings.Repeat("a", 64), ProviderSHA256: strings.Repeat("b", 64), TargetCreditsPerUSD: common.FixedCreditsPerUSD,
		Options: map[string]string{"CreditsPerUSD": "3359744", "LegacyPricingQuotaPerUnit": "500000", "QuotaPerUnit": "500000", "PublicCreditsPerUSD": "100000", "USDExchangeRate": "6.710363"}}
	for key, value := range config.Options {
		require.NoError(t, db.Exec(`INSERT INTO options VALUES (?,?)`, key, value).Error)
	}
	require.NoError(t, db.Raw(`SELECT c.system_identifier::text AS system_identifier,current_database() AS database,
		(SELECT oid::bigint FROM pg_database WHERE datname=current_database()) AS database_oid,current_schema() AS schema,
		current_setting('server_version_num')::integer AS server_version_num,CURRENT_USER AS database_user FROM pg_control_system() c`).Scan(&config.Database).Error)
	baseline := creditPreparationExistingFixture(t, db)
	require.ErrorContains(t, VerifyCreditPreparation(t.Context(), db, config), "missing preparation column")
	require.NoError(t, ApplyCreditPreparationSchema(t.Context(), db, config))
	require.NoError(t, VerifyCreditPreparation(t.Context(), db, config))
	require.NoError(t, ApplyCreditPreparationSchema(t.Context(), db, config), "repeat preparation must be schema-only and idempotent")
	require.Equal(t, baseline, creditPreparationExistingFixture(t, db))
	var added int64
	require.NoError(t, db.Raw(`SELECT count(*) FROM information_schema.columns WHERE table_schema=? AND column_name IN
		('pending_credit_rebase_key','pending_credit_rebase_original_quota','pending_credit_rebase_effective_quota','reset_amount','renewal_amount')`, schema).Scan(&added).Error)
	require.Equal(t, int64(5), added)
	wrong := config
	wrong.Database.DatabaseOID++
	require.ErrorContains(t, ApplyCreditPreparationSchema(t.Context(), db, wrong), "identity mismatch")
	require.NoError(t, db.Exec(`UPDATE options SET value='6.7103630' WHERE key='USDExchangeRate'`).Error)
	require.ErrorContains(t, VerifyCreditPreparation(t.Context(), db, config), "USDExchangeRate changed", "even numerically equal FX must retain its sealed string")
	require.NoError(t, db.Exec(`UPDATE options SET value='6.710363' WHERE key='USDExchangeRate'`).Error)
	require.NoError(t, db.Exec(`UPDATE user_subscriptions SET reset_amount=100 WHERE id=1`).Error)
	require.ErrorContains(t, VerifyCreditPreparation(t.Context(), db, config), "no longer dormant")
	require.NoError(t, db.Exec(`UPDATE user_subscriptions SET reset_amount=NULL WHERE id=1`).Error)
	require.NoError(t, db.Exec(`CREATE TABLE wallet_credit_rebases (id bigint); INSERT INTO wallet_credit_rebases VALUES (1)`).Error)
	require.ErrorContains(t, VerifyCreditPreparation(t.Context(), db, config), "financial audit exists")
	require.Equal(t, baseline, creditPreparationExistingFixture(t, db))
	t.Setenv("SQL_DSN", parsed.String())
	opened, err := OpenCreditPreparationDB(context.Background(), config)
	require.NoError(t, err)
	openedPool, err := opened.DB()
	require.NoError(t, err)
	require.NoError(t, openedPool.Close())
	_, err = common.CreditsPerUSD()
	require.ErrorIs(t, err, common.ErrCreditUnitsUnavailable, "preparation must not install the legacy anchor")
}

func creditPreparationExistingFixture(t *testing.T, db *gorm.DB) string {
	t.Helper()
	var value string
	require.NoError(t, db.Raw(`SELECT jsonb_build_object(
		'options',(SELECT jsonb_agg(to_jsonb(o) ORDER BY key) FROM (SELECT key,value FROM options) o),
		'users',(SELECT jsonb_agg(to_jsonb(u) ORDER BY id) FROM (SELECT id,quota,used_quota FROM users) u),
		'topups',(SELECT jsonb_agg(to_jsonb(p) ORDER BY id) FROM (SELECT id,credited_quota,refunded_quota,money FROM top_ups) p),
		'subscriptions',(SELECT jsonb_agg(to_jsonb(s) ORDER BY id) FROM (SELECT id,amount_total,amount_used FROM user_subscriptions) s)
		)::text`).Scan(&value).Error)
	return value
}
