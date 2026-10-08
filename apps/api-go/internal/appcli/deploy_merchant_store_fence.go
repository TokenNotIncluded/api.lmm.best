package appcli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/internal/deploymentfence"
	"github.com/jackc/pgx/v5"
)

// Activation's model owner uses the same independent key exclusively, before
// MigrationAdvisoryLockKey (...0001). Holding that migration key here would
// deadlock the candidate/N-1's actual migrate --verify command.
const merchantStoreDeploymentFenceKey = deploymentfence.AdvisoryKey

// Owner INSERTs serialize independently of activation and migrate --verify.
// Always acquire the already-held shared fence before this transaction key.
const merchantStoreDeploymentOwnerClaimKey int64 = 0x4c4d4d4150490003

type productionMerchantStoreFenceOwner struct {
	Format             int    `json:"format"`
	State              string `json:"state"`
	DeploymentID       string `json:"deployment_id"`
	Host               string `json:"host"`
	PlanSHA256         string `json:"plan_sha256"`
	ContractSHA256     string `json:"contract_sha256"`
	ProviderSHA256     string `json:"provider_sha256"`
	Nonce              string `json:"nonce"`
	HolderPID          int    `json:"holder_pid"`
	HolderUnit         string `json:"holder_unit"`
	HolderInvocationID string `json:"holder_invocation_id"`
	BackendPID         int32  `json:"backend_pid"`
	SystemIdentifier   string `json:"system_identifier"`
	Database           string `json:"database"`
	DatabaseOID        int64  `json:"database_oid"`
	Schema             string `json:"schema"`
	SchemaOID          int64  `json:"schema_oid"`
	Role               string `json:"role"`
	Purpose            string `json:"purpose,omitempty"`
	Service            string `json:"service,omitempty"`
	StartInvocationID  string `json:"start_invocation_id,omitempty"`
}

var merchantStoreFenceHostPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,62}$`)

func validateMerchantStoreFenceOwner(owner productionMerchantStoreFenceOwner) error {
	if owner.Format != 1 || owner.State != "ACTIVE" || !productionIDPattern.MatchString(owner.DeploymentID) || !merchantStoreFenceHostPattern.MatchString(owner.Host) ||
		!productionSHA256Pattern.MatchString(owner.PlanSHA256) || !productionSHA256Pattern.MatchString(owner.ContractSHA256) || !productionSHA256Pattern.MatchString(owner.ProviderSHA256) ||
		!existingSchemaInvocationPattern.MatchString(owner.Nonce) || !existingSchemaInvocationPattern.MatchString(owner.HolderInvocationID) || owner.HolderPID <= 1 || owner.BackendPID <= 1 ||
		owner.Database == "" || len(owner.Database) > 63 || owner.DatabaseOID <= 0 || !isDatabaseSchema(owner.Schema) || owner.SchemaOID <= 0 || owner.Role == "" || len(owner.Role) > 63 ||
		strings.ContainsAny(owner.Database+owner.Role, "\x00\r\n") {
		return errors.New("merchant deployment fence owner binding is incomplete")
	}
	switch owner.Purpose {
	case "":
		if owner.HolderUnit != merchantStoreFenceUnit(owner.DeploymentID) || owner.Service != "" || owner.StartInvocationID != "" {
			return errors.New("merchant deployment owner legacy unit binding differs")
		}
	case "portable-deploy":
		if owner.HolderUnit != merchantStorePortableUnit(owner.DeploymentID) || owner.Service != "lmm-api.service" || owner.StartInvocationID != "" {
			return errors.New("portable deployment owner unit binding differs")
		}
	case "start":
		if !existingSchemaInvocationPattern.MatchString(owner.StartInvocationID) || owner.HolderUnit != merchantStoreStartUnit(owner.DeploymentID, owner.StartInvocationID) || owner.Service != "lmm-api.service" {
			return errors.New("merchant per-start owner generation binding differs")
		}
	default:
		return errors.New("merchant deployment owner purpose is unknown")
	}
	identifier, err := strconv.ParseUint(owner.SystemIdentifier, 10, 64)
	if err != nil || identifier == 0 || strconv.FormatUint(identifier, 10) != owner.SystemIdentifier {
		return errors.New("merchant deployment fence owner physical system identifier is invalid")
	}
	return nil
}

func merchantStoreFenceUnit(deploymentID string) string {
	return "lmm-merchant-fence-" + deploymentID + ".service"
}

func merchantStoreFenceOwnerKey(owner productionMerchantStoreFenceOwner) string {
	if owner.Purpose == "start" {
		return deploymentfence.OptionPrefix + "start:" + owner.DeploymentID + ":" + owner.Host + ":" + owner.StartInvocationID
	}
	return deploymentfence.OptionPrefix + owner.DeploymentID + ":" + owner.Host
}

func canonicalMerchantStoreFenceOwner(owner productionMerchantStoreFenceOwner) ([]byte, error) {
	if err := validateMerchantStoreFenceOwner(owner); err != nil {
		return nil, err
	}
	return json.Marshal(owner)
}

func parseMerchantStoreFenceOwner(body []byte) (productionMerchantStoreFenceOwner, error) {
	var owner productionMerchantStoreFenceOwner
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if len(body) == 0 || len(body) > 16384 || decoder.Decode(&owner) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return owner, errors.New("merchant deployment fence owner JSON is invalid")
	}
	canonical, err := canonicalMerchantStoreFenceOwner(owner)
	if err != nil || !bytes.Equal(canonical, body) {
		return owner, errors.New("merchant deployment fence owner value is noncanonical or invalid")
	}
	return owner, nil
}

type productionMerchantStoreFenceLease interface {
	Check(context.Context, *productionMerchantStoreWriterContract) error
	Close() error
}

// A dedicated connection owns the session lock. It never reconnects, never
// borrows a business connection pool, and never treats a receipt/PID alone as
// ownership. The ordinary durable holder must keep this lease through final
// confirmation or completed rollback; an individual CLI invocation is not it.
type productionMerchantStoreFence struct {
	mu           sync.Mutex
	connection   *pgx.Conn
	ownerContext context.Context
	identity     productionMerchantStoreWriterContract
	backendPID   int32
	closed       bool
	ownerKey     string
	ownerValue   string
	startupOnly  bool
}

func acquireMerchantStoreDeploymentFence(ctx context.Context, sealedValues map[string]string, expected *productionMerchantStoreWriterContract) (*productionMerchantStoreFence, error) {
	if err := validateMerchantStoreWriterContract(expected); err != nil {
		return nil, err
	}
	return openMerchantStoreDeploymentFence(ctx, sealedValues, expected, false)
}

func acquireMerchantStartupBaselineFence(ctx context.Context, sealedValues map[string]string, baseline *productionMerchantStartupBaseline, host string) (*productionMerchantStoreFence, error) {
	if err := validateMerchantStartupBaseline(baseline); err != nil {
		return nil, err
	}
	identity := baseline.writerIdentity(host)
	if err := validateMerchantStoreWriterHostIdentity(identity); err != nil {
		return nil, err
	}
	return openMerchantStoreDeploymentFence(ctx, sealedValues, identity, true)
}

// Both authorities use this one physical-session implementation. The ordinary
// entry above still requires its complete, distinct two-provider contract.
func openMerchantStoreDeploymentFence(ctx context.Context, sealedValues map[string]string, expected *productionMerchantStoreWriterContract, startupOnly bool) (*productionMerchantStoreFence, error) {
	dsn, err := productionDatabaseURL(sealedValues)
	if err != nil {
		return nil, err
	}
	// pgx reads PG* defaults while parsing. Only the exact inspected child
	// environment may supply them; reject a parent-shell override rather than
	// mutating global environment around a concurrent connection attempt.
	if err := rejectMerchantStoreAmbientPGEnvironment(sealedValues); err != nil {
		return nil, err
	}
	configuration, err := pgx.ParseConfig(dsn)
	if err != nil {
		return nil, errors.New("merchant deployment fence PostgreSQL configuration is invalid")
	}
	configuration.ConnectTimeout = 10 * time.Second
	configuration.RuntimeParams["default_transaction_read_only"] = "on"
	configuration.RuntimeParams["search_path"] = expected.Schema
	configuration.RuntimeParams["statement_timeout"] = "10000"
	configuration.RuntimeParams["application_name"] = "lmm-merchant-deployment-fence"
	connection, err := pgx.ConnectConfig(ctx, configuration)
	if err != nil {
		return nil, errors.New("merchant deployment fence dedicated connection is unavailable")
	}
	lease := &productionMerchantStoreFence{connection: connection, ownerContext: ctx, identity: *expected, backendPID: int32(connection.PgConn().PID()), startupOnly: startupOnly}
	var locked bool
	if err := connection.QueryRow(ctx, "SELECT pg_catalog.pg_try_advisory_lock_shared($1)", merchantStoreDeploymentFenceKey).Scan(&locked); err != nil || !locked {
		_ = lease.Close()
		return nil, errors.New("merchant deployment fence is held by activation or could not be acquired")
	}
	if err := lease.Check(ctx, expected); err != nil {
		_ = lease.Close()
		return nil, err
	}
	return lease, nil
}

func rejectMerchantStoreAmbientPGEnvironment(sealedValues map[string]string) error {
	for _, assignment := range os.Environ() {
		key, value, found := strings.Cut(assignment, "=")
		if found && strings.HasPrefix(key, "PG") && value != "" && sealedValues[key] != value {
			return errors.New("merchant deployment fence cannot use unsealed ambient PostgreSQL environment")
		}
	}
	for _, key := range []string{"PGSERVICE", "PGSERVICEFILE", "PGPASSFILE"} {
		if sealedValues[key] != "" {
			return errors.New("merchant deployment fence cannot use an unsealed external PostgreSQL credentials/configuration file")
		}
	}
	return nil
}

func (lease *productionMerchantStoreFence) Check(ctx context.Context, expected *productionMerchantStoreWriterContract) error {
	lease.mu.Lock()
	defer lease.mu.Unlock()
	if lease.closed || lease.connection == nil || lease.connection.IsClosed() || lease.ownerContext.Err() != nil || expected == nil || lease.identity != *expected {
		return errors.New("merchant deployment fence session or immutable owner binding was lost")
	}
	checkContext, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	var pid int32
	var locked bool
	err := lease.connection.QueryRow(checkContext, `SELECT pg_catalog.pg_backend_pid(), EXISTS (
 SELECT 1 FROM pg_catalog.pg_locks
 WHERE pid OPERATOR(pg_catalog.=) pg_catalog.pg_backend_pid()
 AND locktype OPERATOR(pg_catalog.=) 'advisory' AND mode OPERATOR(pg_catalog.=) 'ShareLock' AND granted
 AND classid::pg_catalog.int8 OPERATOR(pg_catalog.=) $1::pg_catalog.int8
 AND objid::pg_catalog.int8 OPERATOR(pg_catalog.=) $2::pg_catalog.int8 AND objsubid OPERATOR(pg_catalog.=) 1)`,
		int64(uint64(merchantStoreDeploymentFenceKey)>>32), int64(uint64(merchantStoreDeploymentFenceKey)&0xffffffff)).Scan(&pid, &locked)
	if err != nil || !locked || pid != lease.backendPID {
		return errors.New("merchant deployment fence actual PostgreSQL lock/session proof was lost")
	}
	transaction, err := lease.connection.BeginTx(checkContext, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return errors.New("merchant deployment fence read-only identity transaction failed")
	}
	defer func() { _ = transaction.Rollback(context.Background()) }()
	var identity []byte
	query := merchantStoreWriterIdentityQuery(expected.Schema)
	query = strings.TrimPrefix(query, "BEGIN READ ONLY;\n")
	query = strings.TrimSuffix(query, "\nROLLBACK;")
	if err := transaction.QueryRow(checkContext, query).Scan(&identity); err != nil {
		return errors.New("merchant deployment fence actual database/role/floor proof failed")
	}
	if !json.Valid(identity) || verifyMerchantStoreWriterIdentity(identity, expected) != nil {
		return errors.New("merchant deployment fence actual physical database, role, schema or floor changed")
	}
	return transaction.Rollback(checkContext)
}

// Claim only after acquiring the live shared lock. An existing key is never
// overwritten, and a crash after this INSERT deliberately leaves a blocker.
func (lease *productionMerchantStoreFence) ClaimOwner(ctx context.Context, owner productionMerchantStoreFenceOwner) error {
	return lease.claimOwner(ctx, owner, nil)
}

func (lease *productionMerchantStoreFence) claimOwner(ctx context.Context, owner productionMerchantStoreFenceOwner, guard func(context.Context) error) error {
	if lease.startupOnly && owner.Purpose != "start" {
		return errors.New("startup baseline cannot claim an ordinary deployment owner")
	}
	if err := lease.Check(ctx, &lease.identity); err != nil {
		return err
	}
	canonical, err := canonicalMerchantStoreFenceOwner(owner)
	if err != nil {
		return err
	}
	if owner.HolderPID != os.Getpid() || owner.BackendPID != lease.backendPID || owner.SystemIdentifier != lease.identity.SystemIdentifier ||
		owner.Database != lease.identity.Database || owner.DatabaseOID != lease.identity.DatabaseOID || owner.Schema != lease.identity.Schema || owner.SchemaOID != lease.identity.SchemaOID || owner.Role != lease.identity.Role ||
		owner.ContractSHA256 != merchantStoreFenceContractSHA(&lease.identity) || owner.ProviderSHA256 != lease.identity.Candidate.PayloadSHA256 {
		return errors.New("merchant deployment owner differs from the actual holder/session/database")
	}
	if err := sha256MustEqual("/proc/self/exe", owner.ProviderSHA256); err != nil {
		return errors.New("merchant deployment owner actual executable differs from the qualified candidate payload")
	}
	lease.mu.Lock()
	defer lease.mu.Unlock()
	if lease.closed || lease.ownerKey != "" {
		return errors.New("merchant deployment fence already claimed or closed")
	}
	// A waiter must take a fresh snapshot after the claim lock, even when the
	// business role's default isolation is repeatable-read/serializable.
	transaction, err := lease.connection.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted, AccessMode: pgx.ReadWrite})
	if err != nil {
		return errors.New("merchant deployment owner claim transaction failed")
	}
	defer func() { _ = transaction.Rollback(context.Background()) }()
	if _, err := transaction.Exec(ctx, "SELECT pg_catalog.pg_advisory_xact_lock($1)", merchantStoreDeploymentOwnerClaimKey); err != nil {
		return errors.New("merchant deployment owner claim serialization failed")
	}
	if owner.Purpose == "start" || owner.Purpose == "portable-deploy" {
		var exists bool
		if transaction.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM "`+lease.identity.Schema+`".options WHERE `+deploymentfence.PostgreSQLPresencePredicate+`)`).Scan(&exists) != nil || exists {
			return errors.New("portable owner claim found an ACTIVE/unknown owner under its serialized fence")
		}
	}
	key := merchantStoreFenceOwnerKey(owner)
	if guard != nil {
		if err := guard(ctx); err != nil {
			return err
		}
	}
	if _, err := transaction.Exec(ctx, `INSERT INTO "`+lease.identity.Schema+`".options(key,value) VALUES($1,$2)`, key, string(canonical)); err != nil {
		return errors.New("merchant deployment owner already exists or could not be claimed")
	}
	if guard != nil {
		if err := guard(ctx); err != nil {
			return err
		}
	}
	if err := transaction.Commit(ctx); err != nil {
		return errors.New("merchant deployment owner claim commit failed; do not retry or clear the reserved key")
	}
	lease.ownerKey, lease.ownerValue = key, string(canonical)
	return nil
}

func (lease *productionMerchantStoreFence) CheckOwner(ctx context.Context) error {
	if err := lease.Check(ctx, &lease.identity); err != nil {
		return err
	}
	lease.mu.Lock()
	defer lease.mu.Unlock()
	if lease.closed || lease.ownerKey == "" || lease.ownerValue == "" {
		return errors.New("merchant deployment fence has no durable ACTIVE owner")
	}
	var actual string
	if err := lease.connection.QueryRow(ctx, `SELECT value FROM "`+lease.identity.Schema+`".options WHERE key=$1`, lease.ownerKey).Scan(&actual); err != nil || actual != lease.ownerValue {
		return errors.New("merchant deployment durable owner value changed or disappeared")
	}
	return nil
}

// Only a still-live shared session may finish its exact owner. No auto-expiry,
// upsert, sweep, unknown-owner deletion, or migration/floor mutation exists.
func (lease *productionMerchantStoreFence) ReleaseOwner(ctx context.Context) error {
	if err := lease.CheckOwner(ctx); err != nil {
		return err
	}
	lease.mu.Lock()
	defer lease.mu.Unlock()
	transaction, err := lease.connection.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadWrite})
	if err != nil {
		return errors.New("merchant deployment owner release transaction failed")
	}
	defer func() { _ = transaction.Rollback(context.Background()) }()
	var deleted string
	if err := transaction.QueryRow(ctx, `DELETE FROM "`+lease.identity.Schema+`".options WHERE key=$1 AND value=$2 RETURNING value`, lease.ownerKey, lease.ownerValue).Scan(&deleted); err != nil || deleted != lease.ownerValue {
		return errors.New("merchant deployment owner CAS release did not match its exact original ACTIVE value")
	}
	if err := transaction.Commit(ctx); err != nil {
		return errors.New("merchant deployment owner release commit failed; retained state requires review")
	}
	lease.ownerKey, lease.ownerValue = "", ""
	return nil
}

func (lease *productionMerchantStoreFence) Close() error {
	lease.mu.Lock()
	defer lease.mu.Unlock()
	if lease.closed {
		return nil
	}
	lease.closed = true
	if lease.connection == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// Closing the dedicated session releases the fence. Do not issue a generic
	// unlock-all command or touch another owner's migration/deployment locks.
	return lease.connection.Close(ctx)
}
