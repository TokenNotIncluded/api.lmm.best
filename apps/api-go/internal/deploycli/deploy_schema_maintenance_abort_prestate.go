package deploycli

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/internal/deploymentfence"
	"github.com/jackc/pgx/v5"
)

// The controller separately freezes its original NEVER_CLAIMED daemon and
// observes both old stopped/startable origins. This read-only gate neither
// infers that history from a missing owner file nor clears a reserved owner.
// Its claim lock is released before the ordinary old per-start hook runs.
func (runtime *productionRuntime) checkSchemaMaintenanceAbortPreState(ctx context.Context, plan productionSchemaMaintenancePlan, preSHA string, old productionMerchantStoreCapsule, oldSHA string) error {
	return runtime.schemaMaintenanceAbortPreState(ctx, plan, preSHA, old, oldSHA, false)
}

// Only the initial preparation requires a quiesced database. Later abort
// start/ready observations allow the already authenticated old Go processes'
// normal connection pools, while still rejecting every reserved owner.
func (runtime *productionRuntime) checkSchemaMaintenanceAbortQuiescedPreState(ctx context.Context, plan productionSchemaMaintenancePlan, preSHA string, old productionMerchantStoreCapsule, oldSHA string) error {
	return runtime.schemaMaintenanceAbortPreState(ctx, plan, preSHA, old, oldSHA, true)
}

func (runtime *productionRuntime) schemaMaintenanceAbortPreState(ctx context.Context, plan productionSchemaMaintenancePlan, preSHA string, old productionMerchantStoreCapsule, oldSHA string, quiesced bool) error {
	raw, err := canonicalProductionSchemaMaintenancePlan(plan, runtime.paths)
	if err != nil || startupContentSHA256(raw) != preSHA || old.Format != 1 || old.StartupPolicy != "per-start" || old.ExistingSchemaContract == nil || old.Writer == nil {
		return errors.New("schema abort lacks its exact pre-migration authority")
	}
	found := false
	for _, origin := range plan.Origins {
		if origin.Host == old.Host && origin.OldCapsule.Path == filepath.Join(old.Root, "capsule.json") && origin.OldCapsule.SHA256 == oldSHA {
			found = true
		}
	}
	expected := *old.ExistingSchemaContract
	expected.StartupSHA256, expected.SignedUnitSHA256 = "", ""
	if !found || expected != plan.PreSchema || old.Writer.RequiredCapability > 7 || old.Writer.Role != plan.Role {
		return errors.New("schema abort changed its retained provider or original catalog")
	}
	loaded, err := runtime.loadMerchantStoreCapsule(filepath.Join(old.Root, "capsule.json"), oldSHA)
	if err != nil {
		return errors.New("schema abort original capsule is unavailable")
	}
	want, wantErr := canonicalMerchantStoreCapsule(old)
	got, gotErr := canonicalMerchantStoreCapsule(loaded)
	if wantErr != nil || gotErr != nil || !bytes.Equal(want, got) {
		return errors.New("schema abort original capsule changed")
	}
	child, err := runtime.merchantStoreCapsuleEnvironment(ctx, old, oldSHA, "")
	if err != nil {
		return err
	}
	values := map[string]string{}
	for _, assignment := range child {
		key, value, valid := strings.Cut(assignment, "=")
		if !valid {
			return errors.New("schema abort original environment is malformed")
		}
		if _, duplicate := values[key]; duplicate {
			return errors.New("schema abort original environment is duplicated")
		}
		values[key] = value
	}
	lease, err := openMerchantStoreDeploymentFence(ctx, values, old.Writer, false)
	if err != nil {
		return err
	}
	defer lease.Close()
	return lease.schemaMaintenanceAbortPreState(ctx, plan.PreSchema, quiesced)
}

func (lease *productionMerchantStoreFence) checkSchemaMaintenanceAbortPreState(ctx context.Context, expected productionExistingSchemaContract) error {
	return lease.schemaMaintenanceAbortPreState(ctx, expected, false)
}

func (lease *productionMerchantStoreFence) checkSchemaMaintenanceAbortQuiescedPreState(ctx context.Context, expected productionExistingSchemaContract) error {
	return lease.schemaMaintenanceAbortPreState(ctx, expected, true)
}

func (lease *productionMerchantStoreFence) schemaMaintenanceAbortPreState(ctx context.Context, expected productionExistingSchemaContract, quiesced bool) error {
	if expected.StartupSHA256 != "" || expected.SignedUnitSHA256 != "" || validateProductionExistingSchemaContract(&expected) != nil {
		return errors.New("schema abort pre-catalog is not a physical-only contract")
	}
	if err := lease.Check(ctx, &lease.identity); err != nil {
		return err
	}
	lease.mu.Lock()
	defer lease.mu.Unlock()
	if lease.closed || lease.connection == nil || lease.connection.IsClosed() || lease.ownerKey != "" || lease.ownerValue != "" {
		return errors.New("schema abort cannot use a claimed or closed physical session")
	}
	// READ COMMITTED is mandatory even if the role defaults to repeatable read:
	// a waiting claim may COMMIT before we obtain ...0003.
	tx, err := lease.connection.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted, AccessMode: pgx.ReadOnly})
	if err != nil {
		return errors.New("schema abort read-only transaction is unavailable")
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err := tx.Exec(ctx, "SELECT pg_catalog.pg_advisory_xact_lock($1)", merchantStoreDeploymentOwnerClaimKey); err != nil {
		return errors.New("schema abort owner visibility could not be serialized")
	}
	checkAbsent := func() error {
		if _, err := tx.Exec(ctx, "SELECT pg_catalog.pg_stat_clear_snapshot()"); err != nil {
			return errors.New("schema abort client visibility is unavailable")
		}
		var ownerExists bool
		var others int
		if tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM "`+lease.identity.Schema+`".options WHERE `+deploymentfence.PostgreSQLPresencePredicate+`),
(SELECT count(*)::int FROM pg_catalog.pg_stat_activity WHERE datid=(SELECT oid FROM pg_catalog.pg_database WHERE datname=pg_catalog.current_database()) AND (backend_type='client backend' OR backend_type IS NULL) AND pid<>pg_catalog.pg_backend_pid())`).Scan(&ownerExists, &others) != nil {
			return errors.New("schema abort owner and client visibility is unavailable")
		}
		if ownerExists {
			return errors.New("schema abort still has a reserved owner")
		}
		if quiesced && others != 0 {
			return errors.New("schema abort quiesced preparation still has another database client")
		}
		return nil
	}
	if err := checkAbsent(); err != nil {
		return err
	}
	identityQuery := strings.TrimSuffix(strings.TrimPrefix(merchantStoreWriterIdentityQuery(lease.identity.Schema), "BEGIN READ ONLY;\n"), "\nROLLBACK;")
	var identity []byte
	if tx.QueryRow(ctx, identityQuery).Scan(&identity) != nil || verifyMerchantStoreWriterIdentity(identity, &lease.identity) != nil {
		return errors.New("schema abort physical role or writer floor changed")
	}
	if _, err := tx.Exec(ctx, "SET LOCAL search_path TO pg_catalog"); err != nil {
		return errors.New("schema abort catalog path is unavailable")
	}
	catalogQuery := strings.TrimSuffix(strings.TrimPrefix(existingSchemaMetadataQuery(expected.Schema), "BEGIN READ ONLY;\nSET LOCAL search_path TO pg_catalog;\n"), "\nROLLBACK;")
	for range 2 {
		var snapshot []byte
		if err := tx.QueryRow(ctx, catalogQuery).Scan(&snapshot); err != nil {
			return errors.New("schema abort original catalog is unavailable")
		}
		actual, err := decodeExistingSchemaSnapshot(snapshot)
		if err != nil || actual != expected {
			return errors.New("schema abort actual catalog differs from its original seal")
		}
		if err := checkAbsent(); err != nil {
			return err
		}
	}
	if err := tx.Rollback(ctx); err != nil {
		return errors.New("schema abort read-only gate did not finish cleanly")
	}
	return nil
}
