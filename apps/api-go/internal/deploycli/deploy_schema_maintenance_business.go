package deploycli

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
)

type productionSchemaMaintenanceRecoveryBusiness struct {
	RegistrationQuota    string
	NullReferralRewards  int64
	HeldToolCalls        int64
	PendingProviderCalls int64
	PendingBuiltinBills  int64
}

// These are N-1 business compatibility conditions, separate from a successful
// schema verifier. A new registration reward would make the old first-top-up
// path collide with its unique invitee reward and roll back the user's top-up.
func validateSchemaMaintenanceRecoveryBusiness(b productionSchemaMaintenanceRecoveryBusiness) error {
	quota, err := strconv.ParseInt(strings.TrimSpace(b.RegistrationQuota), 10, 64)
	if err != nil || quota != 0 || b.NullReferralRewards != 0 || b.HeldToolCalls != 0 || b.PendingProviderCalls != 0 || b.PendingBuiltinBills != 0 {
		return errors.New("schema maintenance N-1 business recovery requires registration rewards disabled and every outstanding tool bill resolved")
	}
	return nil
}

func schemaMaintenanceRecoveryBusinessQuery(schema string) string {
	return `SELECT
 COALESCE((SELECT value FROM "` + schema + `".options WHERE key='ReferralRegistrationRewardQuota'),'0'),
 (SELECT count(*) FROM "` + schema + `".referral_rewards WHERE top_up_id IS NULL),
 (SELECT count(*) FROM "` + schema + `".tool_market_calls WHERE settlement_status='held'),
 (SELECT count(*) FROM "` + schema + `".tool_market_calls c WHERE settlement_status='held' AND COALESCE(to_jsonb(c)->>'provider_run_id','')<>''),
 (SELECT count(*) FROM "` + schema + `".tool_market_results WHERE builtin_billing_pending)`
}

// Use only the already-held physical PG session. In particular a worker's
// active single transaction must never acquire an additional observer client.
func (lease *productionMerchantStoreFence) checkSchemaMaintenanceRecoveryBusiness(ctx context.Context) error {
	if lease == nil {
		return errors.New("schema maintenance recovery business owner is unavailable")
	}
	if err := lease.Check(ctx, &lease.identity); err != nil {
		return err
	}
	if lease.identity.RequiredCapability > 7 {
		return errors.New("schema maintenance N-1 writer floor is unsupported")
	}
	lease.mu.Lock()
	defer lease.mu.Unlock()
	if lease.closed || !isDatabaseSchema(lease.identity.Schema) {
		return errors.New("schema maintenance recovery physical session is unavailable")
	}
	tx, err := lease.connection.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly, IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return errors.New("schema maintenance recovery business read-only transaction unavailable")
	}
	defer tx.Rollback(context.Background())
	var b productionSchemaMaintenanceRecoveryBusiness
	if err := tx.QueryRow(ctx, schemaMaintenanceRecoveryBusinessQuery(lease.identity.Schema)).Scan(&b.RegistrationQuota, &b.NullReferralRewards, &b.HeldToolCalls, &b.PendingProviderCalls, &b.PendingBuiltinBills); err != nil {
		return errors.New("schema maintenance N-1 business recovery cannot be established")
	}
	if err := validateSchemaMaintenanceRecoveryBusiness(b); err != nil {
		return err
	}
	return tx.Rollback(ctx)
}
