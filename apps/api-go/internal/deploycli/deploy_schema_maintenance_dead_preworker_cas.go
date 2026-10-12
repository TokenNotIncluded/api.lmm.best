package deploycli

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/internal/deploymentfence"
	"github.com/jackc/pgx/v5"
)

const schemaMaintenanceDeadPreWorkerExactOwner = "EXACT_ORIGINAL_OWNER"
const schemaMaintenanceDeadPreWorkerZeroOwner = "ZERO_OWNER"

type productionSchemaMaintenanceDeadPreWorkerCASAuthority struct {
	Plan                    productionSchemaMaintenancePlan
	PrePlanSHA256           string
	OldCapsule              productionMerchantStoreCapsule
	OldCapsuleSHA256        string
	OriginalDaemon          schemaMaintenanceRPCBinding
	OwnerMode               string
	OriginalOwnerRaw        []byte
	PreExecRecovery         *productionReleaseFilePlan
	RecoveryEngineSHA256    string
	CheckLocalDeadAuthority func(context.Context) error
}

type productionSchemaMaintenanceDeadPreExecData struct {
	Before           productionSchemaMaintenanceBackupSource
	OriginalOwnerRaw []byte
}

type productionSchemaMaintenanceDeadPreWorkerCASResult struct {
	Format                string                           `json:"format"`
	TransitionID          string                           `json:"transition_id"`
	PrePlanSHA256         string                           `json:"pre_plan_sha256"`
	Mode                  string                           `json:"mode"`
	OriginalDaemon        schemaMaintenanceRPCBinding      `json:"original_daemon"`
	OriginalOwnerSHA256   string                           `json:"original_owner_sha256"`
	OldCapsule            productionReleaseFilePlan        `json:"old_capsule"`
	PreSchema             productionExistingSchemaContract `json:"pre_schema"`
	ProviderSHA256        string                           `json:"provider_sha256"`
	RecovererEXESHA256    string                           `json:"recoverer_exe_sha256"`
	RecovererPID          int                              `json:"recoverer_pid"`
	RecovererInvocationID string                           `json:"recoverer_invocation_id"`
	IntentSHA256          string                           `json:"intent_sha256"`
	StartedUTC            time.Time                        `json:"started_utc"`
	FinishedUTC           time.Time                        `json:"finished_utc"`
	Outcome               string                           `json:"outcome"`
}

var errSchemaMaintenanceDeadCommitUncertain = errors.New("dead-preworker exact CAS commit is uncertain; only read-only reconciliation is permitted")

// The native runtime seals a one-shot intent before calling this primitive.
// Guard must only inspect local immutable authority, dead process generations,
// original transferred FDs and absence of worker files. It must not call this
// lease, another DB client, migrate --verify, or remote full qualification.
func (lease *productionMerchantStoreFence) recoverSchemaMaintenanceDeadPreWorkerOwner(ctx context.Context, expected productionExistingSchemaContract, ownerRaw []byte, mode string, guard func(context.Context) error, preExecData ...productionSchemaMaintenanceDeadPreExecData) error {
	if expected.StartupSHA256 != "" || expected.SignedUnitSHA256 != "" || validateProductionExistingSchemaContract(&expected) != nil || guard == nil {
		return errors.New("dead-preworker CAS lacks a physical pre-catalog or local recovery authority")
	}
	if len(preExecData) > 1 || len(preExecData) == 1 && (mode != schemaMaintenanceDeadPreWorkerExactOwner || !bytes.Equal(preExecData[0].OriginalOwnerRaw, ownerRaw) || schemaMaintenanceBackupSourceIdentity(preExecData[0].Before, expected) != nil) {
		return errors.New("dead-preworker pre-execution data authority differs from its exact original owner or catalog")
	}
	var owner productionMerchantStoreFenceOwner
	if mode == schemaMaintenanceDeadPreWorkerExactOwner {
		var err error
		owner, err = deploymentfence.ParseSchemaMaintenanceOwner(ownerRaw)
		if err != nil {
			return err
		}
	} else if mode != schemaMaintenanceDeadPreWorkerZeroOwner || len(ownerRaw) != 0 {
		return errors.New("dead-preworker owner mode must be explicit and cannot fall back")
	}
	if err := lease.Check(ctx, &lease.identity); err != nil {
		return err
	}
	if err := guard(ctx); err != nil {
		return err
	}
	lease.mu.Lock()
	defer lease.mu.Unlock()
	if lease.closed || lease.connection == nil || lease.connection.IsClosed() || lease.ownerKey != "" || lease.ownerValue != "" {
		return errors.New("dead-preworker recovery session is closed or already owns another claim")
	}
	tx, err := lease.connection.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted, AccessMode: pgx.ReadWrite})
	if err != nil {
		return errors.New("dead-preworker CAS transaction is unavailable")
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	for _, key := range []int64{merchantStoreDeploymentOwnerClaimKey, 0x4c4d4d4150490001} {
		if _, err := tx.Exec(ctx, "SELECT pg_catalog.pg_advisory_xact_lock($1)", key); err != nil {
			return errors.New("dead-preworker CAS serialization is unavailable")
		}
	}
	if err := checkSchemaMaintenanceDeadPreWorkerTransaction(ctx, tx, lease.identity, expected, owner, ownerRaw, mode, true); err != nil {
		return err
	}
	if len(preExecData) == 1 {
		if err := checkSchemaMaintenanceDeadPreExecData(ctx, tx, preExecData[0]); err != nil {
			return err
		}
	}
	if err := guard(ctx); err != nil {
		return err
	}
	if len(preExecData) == 1 {
		var deleteEffects int
		if err := tx.QueryRow(ctx, `SELECT
 (SELECT count(*) FROM pg_catalog.pg_trigger WHERE tgrelid=pg_catalog.to_regclass($1) AND NOT tgisinternal AND tgenabled<>'D' AND (tgtype & 8)<>0)
 +(SELECT count(*) FROM pg_catalog.pg_rewrite WHERE ev_class=pg_catalog.to_regclass($1) AND ev_type='4' AND ev_enabled<>'D')`, schemaMaintenanceQuoteIdentifier(lease.identity.Schema)+`.options`).Scan(&deleteEffects); err != nil || deleteEffects != 0 {
			return errors.New("dead-preworker pre-execution owner DELETE has active user DELETE triggers or rules")
		}
	}
	if mode == schemaMaintenanceDeadPreWorkerExactOwner {
		var returned string
		if err := tx.QueryRow(ctx, `DELETE FROM "`+lease.identity.Schema+`".options WHERE key=$1 AND value=$2 RETURNING value`, merchantStoreFenceOwnerKey(owner), string(ownerRaw)).Scan(&returned); err != nil || returned != string(ownerRaw) {
			return errors.New("dead-preworker CAS did not return its sole exact original owner")
		}
	}
	if len(preExecData) == 1 {
		// Drain deferred DELETE triggers before the final catalog/data checks;
		// otherwise a trigger could change old money only during COMMIT.
		if _, err := tx.Exec(ctx, "SET CONSTRAINTS ALL IMMEDIATE"); err != nil {
			return errors.New("dead-preworker pre-execution deferred constraints did not finish before data qualification")
		}
	}
	if err := guard(ctx); err != nil {
		return err
	}
	if err := checkSchemaMaintenanceDeadPreWorkerTransaction(ctx, tx, lease.identity, expected, owner, nil, schemaMaintenanceDeadPreWorkerZeroOwner, true); err != nil {
		return err
	}
	if len(preExecData) == 1 {
		if err := checkSchemaMaintenanceDeadPreExecData(ctx, tx, preExecData[0]); err != nil {
			return err
		}
	}
	if err := guard(ctx); err != nil {
		return err
	}
	if err := checkSchemaMaintenanceDeadPreWorkerClients(ctx, tx, owner.BackendPID); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return errSchemaMaintenanceDeadCommitUncertain
	}
	return nil
}

func checkSchemaMaintenanceDeadPreWorkerTransaction(ctx context.Context, tx pgx.Tx, writer productionMerchantStoreWriterContract, expected productionExistingSchemaContract, originalOwner productionMerchantStoreFenceOwner, ownerRaw []byte, mode string, writable bool) error {
	accessMode := "on"
	if writable {
		accessMode = "off"
	}
	if _, err := tx.Exec(ctx, `SET LOCAL search_path TO "`+writer.Schema+`"`); err != nil {
		return errors.New("dead-preworker business schema is unavailable")
	}
	identityQuery := strings.TrimSuffix(strings.TrimPrefix(merchantStoreWriterIdentityQuery(writer.Schema), "BEGIN READ ONLY;\n"), "\nROLLBACK;")
	var identity []byte
	if tx.QueryRow(ctx, identityQuery).Scan(&identity) != nil || verifyMerchantStoreWriterIdentityAccessMode(identity, &writer, accessMode) != nil {
		return errors.New("dead-preworker physical role or writer floor changed")
	}
	if _, err := tx.Exec(ctx, "SET LOCAL search_path TO pg_catalog"); err != nil {
		return errors.New("dead-preworker catalog path is unavailable")
	}
	if err := checkSchemaMaintenanceDeadPreWorkerClients(ctx, tx, originalOwner.BackendPID); err != nil {
		return err
	}
	catalogQuery := strings.TrimSuffix(strings.TrimPrefix(existingSchemaMetadataQuery(expected.Schema), "BEGIN READ ONLY;\nSET LOCAL search_path TO pg_catalog;\n"), "\nROLLBACK;")
	var raw []byte
	if tx.QueryRow(ctx, catalogQuery).Scan(&raw) != nil {
		return errors.New("dead-preworker complete catalog is unavailable")
	}
	actual, err := decodeExistingSchemaSnapshotAccessMode(raw, accessMode)
	if err != nil || actual != expected {
		return errors.New("dead-preworker actual catalog differs from its original seal")
	}
	var count int
	var key, value *string
	if tx.QueryRow(ctx, `SELECT count(*)::int,min(key),min(value) FROM "`+writer.Schema+`".options WHERE `+deploymentfence.PostgreSQLPresencePredicate).Scan(&count, &key, &value) != nil {
		return errors.New("dead-preworker reserved owner is unavailable")
	}
	if mode == schemaMaintenanceDeadPreWorkerZeroOwner {
		if count != 0 || len(ownerRaw) != 0 {
			return errors.New("dead-preworker zero-owner mode found a reserved owner")
		}
		return nil
	}
	if mode != schemaMaintenanceDeadPreWorkerExactOwner || count != 1 || key == nil || value == nil || *key != merchantStoreFenceOwnerKey(originalOwner) || !bytes.Equal([]byte(*value), ownerRaw) {
		return errors.New("dead-preworker expected owner differs from the sole exact canonical row")
	}
	return nil
}

// Refresh after catalog/owner inspection as well as before it. Restricted roles
// see some client rows with a NULL backend_type; those rows must still count.
func checkSchemaMaintenanceDeadPreWorkerClients(ctx context.Context, tx pgx.Tx, originalPID int32) error {
	if _, err := tx.Exec(ctx, "SELECT pg_catalog.pg_stat_clear_snapshot()"); err != nil {
		return errors.New("dead-preworker client visibility is unavailable")
	}
	var others int
	var originalExists bool
	if tx.QueryRow(ctx, `SELECT (SELECT count(*)::int FROM pg_catalog.pg_stat_activity WHERE datid=(SELECT oid FROM pg_catalog.pg_database WHERE datname=pg_catalog.current_database()) AND (backend_type='client backend' OR backend_type IS NULL) AND pid<>pg_catalog.pg_backend_pid()), EXISTS(SELECT 1 FROM pg_catalog.pg_stat_activity WHERE pid=$1)`, originalPID).Scan(&others, &originalExists) != nil || others != 0 || originalPID > 1 && originalExists {
		return errors.New("dead-preworker original backend is live/reused or another client is present")
	}
	return nil
}
