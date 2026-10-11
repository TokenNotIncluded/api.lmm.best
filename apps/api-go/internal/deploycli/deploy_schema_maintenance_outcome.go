package deploycli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/internal/deploymentfence"
)

const schemaMaintenanceOutcomeFormat = "lmm-schema-maintenance-outcome-v1"
const schemaMaintenanceOutcomeMaxBytes = 16 << 20

// This append-only result is produced by the same native physical owner after
// the worker returned. It contains actual read-only catalog/data projections,
// not a caller-supplied statement that a migration or backup succeeded.
type productionSchemaMaintenanceOutcome struct {
	Format                  string                                  `json:"format"`
	TransitionID            string                                  `json:"transition_id"`
	PrePlanSHA256           string                                  `json:"pre_plan_sha256"`
	OwnerSHA256             string                                  `json:"owner_sha256"`
	WorkerResult            productionReleaseFilePlan               `json:"worker_result"`
	BackupSet               productionReleaseFilePlan               `json:"backup_set"`
	BackupProof             productionReleaseFilePlan               `json:"backup_proof"`
	PreSchema               productionExistingSchemaContract        `json:"pre_schema"`
	PostSchema              productionExistingSchemaContract        `json:"post_schema"`
	PostSource              productionSchemaMaintenanceBackupSource `json:"post_source"`
	ProjectedSource         productionSchemaMaintenanceBackupSource `json:"projected_source"`
	QualifiedProviderSHA256 string                                  `json:"qualified_provider_sha256"`
	CapturedUTC             time.Time                               `json:"captured_utc"`
}

func (runtime *productionRuntime) schemaMaintenanceOutcomeBackup(plan productionSchemaMaintenancePlan, preSHA string, observedAt time.Time) (productionSchemaMaintenanceBackupSet, productionSchemaMaintenanceBackupProof, productionReleaseFilePlan, productionReleaseFilePlan, error) {
	var set productionSchemaMaintenanceBackupSet
	var proof productionSchemaMaintenanceBackupProof
	workspace := schemaMaintenanceHostWorkspace(runtime.paths, plan.TransitionID)
	setRef := productionReleaseFilePlan{Path: filepath.Join(workspace, "state", "schema-backup-set.json")}
	proofRef := productionReleaseFilePlan{Path: filepath.Join(workspace, "state", schemaMaintenanceBackupProofName)}
	fail := func() (productionSchemaMaintenanceBackupSet, productionSchemaMaintenanceBackupProof, productionReleaseFilePlan, productionReleaseFilePlan, error) {
		return set, proof, setRef, proofRef, errors.New("schema outcome lacks its exact signed quiesced backup source")
	}
	raw, err := runtime.readExistingSchemaSealedFile(proofRef.Path, true)
	if err != nil || len(raw) > controllerBackupReceiptMaxBytes {
		return fail()
	}
	proofRef.SHA256 = startupContentSHA256(raw)
	proof, err = schemaMaintenanceBackupDecodeProof(raw, plan.BackupPolicy.ControllerPublicKey)
	now := observedAt
	if now.IsZero() {
		return fail()
	}
	if err != nil || validateSchemaMaintenanceBackupProof(proof, plan, preSHA, proof.StoppedOrigins, now) != nil {
		return fail()
	}
	raw, err = runtime.readExistingSchemaSealedFileBounded(setRef.Path, true, schemaMaintenanceOutcomeMaxBytes)
	if err != nil || len(raw) > schemaMaintenanceOutcomeMaxBytes || startupContentSHA256(raw) != proof.BackupSetSHA256 || decodeControllerBackupJSON(raw, &set) != nil || validateSchemaMaintenanceBackupSet(set, plan, preSHA, proof.StoppedOrigins, now) != nil {
		return fail()
	}
	setRef.SHA256 = startupContentSHA256(raw)
	sourceRaw, err := json.Marshal(set.SourceBefore)
	if err != nil || startupContentSHA256(sourceRaw) != proof.SourceSHA256 || schemaMaintenanceBackupLogicalSHA(set.SourceBefore) != proof.SourceLogicalSHA256 || proof.Archive != set.Archive {
		return fail()
	}
	return set, proof, setRef, proofRef, nil
}

func validateSchemaMaintenancePostAdditions(before, post productionSchemaMaintenanceBackupSource) error {
	oldTables := map[string]bool{}
	for _, table := range before.Tables {
		oldTables[table.Name] = true
	}
	for _, table := range post.Tables {
		if !oldTables[table.Name] && table.Rows != 0 {
			return errors.New("schema outcome contains application rows in an added table")
		}
	}
	oldSequences := map[string]bool{}
	for _, sequence := range before.Sequences {
		oldSequences[sequence.Name] = true
	}
	for _, sequence := range post.Sequences {
		if !oldSequences[sequence.Name] && sequence.Called {
			return errors.New("schema outcome consumed an added application sequence")
		}
	}
	return nil
}

func validateSchemaMaintenanceOutcome(out productionSchemaMaintenanceOutcome, plan productionSchemaMaintenancePlan, preSHA, ownerSHA string, before productionSchemaMaintenanceBackupSource, paths productionPaths) error {
	raw, err := canonicalProductionSchemaMaintenancePlan(plan, paths)
	workspace := schemaMaintenanceHostWorkspace(paths, plan.TransitionID)
	if err != nil || startupContentSHA256(raw) != preSHA || out.Format != schemaMaintenanceOutcomeFormat || out.TransitionID != plan.TransitionID || out.PrePlanSHA256 != preSHA || out.OwnerSHA256 != ownerSHA || !productionSHA256Pattern.MatchString(ownerSHA) || out.PreSchema != plan.PreSchema || out.QualifiedProviderSHA256 != plan.Worker.Provider.PayloadSHA256 || out.CapturedUTC.IsZero() || out.CapturedUTC.Location() != time.UTC {
		return errors.New("schema outcome changed its exact immutable execution authority")
	}
	for _, ref := range []productionReleaseFilePlan{out.WorkerResult, out.BackupSet, out.BackupProof} {
		if validateSchemaMaintenanceReference(ref) != nil {
			return errors.New("schema outcome lost an exact execution evidence digest")
		}
	}
	if out.WorkerResult.Path != filepath.Join(workspace, "state", "schema-worker-result.json") || out.BackupSet.Path != filepath.Join(workspace, "state", "schema-backup-set.json") || out.BackupProof.Path != filepath.Join(workspace, "state", schemaMaintenanceBackupProofName) {
		return errors.New("schema outcome redirected its fixed evidence source")
	}
	pre, post := plan.PreSchema, out.PostSchema
	pre.MetadataSHA256, post.MetadataSHA256 = "", ""
	if validateProductionExistingSchemaContract(&out.PostSchema) != nil || pre != post || schemaMaintenanceBackupSourceIdentity(out.PostSource, out.PostSchema) != nil || schemaMaintenanceBackupSourceIdentity(out.ProjectedSource, out.PostSchema) != nil {
		return errors.New("schema outcome changed physical PostgreSQL identity")
	}
	if err := validateSchemaMaintenanceBackupProjectedEqual(before, out.ProjectedSource); err != nil {
		return err
	}
	return validateSchemaMaintenancePostAdditions(before, out.PostSource)
}

func (runtime *productionRuntime) readSchemaMaintenanceOutcome(plan productionSchemaMaintenancePlan, preSHA, ownerSHA string) (productionSchemaMaintenanceOutcome, string, error) {
	var out productionSchemaMaintenanceOutcome
	preRaw, err := canonicalProductionSchemaMaintenancePlan(plan, runtime.paths)
	if err != nil || startupContentSHA256(preRaw) != preSHA {
		return out, "", errors.New("schema outcome reader lacks its original canonical preplan")
	}
	path := filepath.Join(schemaMaintenanceHostWorkspace(runtime.paths, plan.TransitionID), "state", "schema-outcome.json")
	raw, err := runtime.readExistingSchemaSealedFileBounded(path, true, schemaMaintenanceOutcomeMaxBytes)
	if err != nil || len(raw) > schemaMaintenanceOutcomeMaxBytes {
		return out, "", errors.New("schema outcome native receipt is unavailable")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&out) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return out, "", errors.New("schema outcome native receipt is malformed")
	}
	canonical, err := json.MarshalIndent(out, "", "  ")
	if err != nil || !bytes.Equal(append(canonical, '\n'), raw) {
		return out, "", errors.New("schema outcome native receipt is not canonical")
	}
	owner, _, err := deploymentfence.ReadSchemaMaintenanceOwner(filepath.Join(schemaMaintenanceHostWorkspace(runtime.paths, plan.TransitionID), "state", "schema-owner.json"), ownerSHA)
	if err != nil || owner.DeploymentID != plan.TransitionID || owner.PlanSHA256 != preSHA || owner.ProviderSHA256 != plan.Worker.Provider.PayloadSHA256 || owner.Host != plan.PrimaryHost || owner.SystemIdentifier != plan.PreSchema.SystemIdentifier || owner.Database != plan.PreSchema.Database || owner.DatabaseOID != plan.PreSchema.DatabaseOID || owner.Schema != plan.PreSchema.Schema || owner.SchemaOID != plan.PreSchema.SchemaOID || owner.Role != plan.Role {
		return out, "", errors.New("schema outcome original native owner changed")
	}
	worker, err := runtime.readSchemaMaintenanceWorkerResult(schemaMaintenanceHostWorkspace(runtime.paths, plan.TransitionID), owner, ownerSHA)
	if err != nil {
		return out, "", err
	}
	// Freshness was required before dispatch. After COMMIT retain that original
	// authorization instant; elapsed maintenance time cannot expire its outcome.
	set, proof, setRef, proofRef, err := runtime.schemaMaintenanceOutcomeBackup(plan, preSHA, worker.StartedUTC)
	if err != nil {
		return out, "", err
	}
	if out.BackupSet != setRef || out.BackupProof != proofRef || proof.VerifiedUTC.After(worker.StartedUTC) || validateSchemaMaintenanceOutcome(out, plan, preSHA, ownerSHA, set.SourceBefore, runtime.paths) != nil {
		return out, "", errors.New("schema outcome does not bind the signed backup and preserved old data")
	}
	workerRaw, err := runtime.readExistingSchemaSealedFile(out.WorkerResult.Path, true)
	expectedWorker, encodeErr := json.MarshalIndent(worker, "", "  ")
	now := time.Now()
	if runtime.now != nil {
		now = runtime.now()
	}
	if err != nil || encodeErr != nil || !bytes.Equal(workerRaw, append(expectedWorker, '\n')) || startupContentSHA256(workerRaw) != out.WorkerResult.SHA256 || out.CapturedUTC.Before(worker.FinishedUTC) || out.CapturedUTC.After(now.Add(30*time.Second)) {
		return out, "", errors.New("schema outcome worker execution evidence changed")
	}
	return out, startupContentSHA256(raw), nil
}

func (runtime *productionRuntime) captureSchemaMaintenanceOutcome(ctx context.Context, fence *productionSchemaMaintenanceFence, plan productionSchemaMaintenancePlan, preSHA string, stopped []productionSchemaMaintenanceHostCapture) (productionSchemaMaintenanceOutcome, error) {
	var out productionSchemaMaintenanceOutcome
	preRaw, err := canonicalProductionSchemaMaintenancePlan(plan, runtime.paths)
	if err != nil || startupContentSHA256(preRaw) != preSHA {
		return out, errors.New("schema outcome lacks its original canonical preplan")
	}
	if fence == nil || fence.runtime != runtime || fence.owner.Host != plan.PrimaryHost || fence.owner.DeploymentID != plan.TransitionID || fence.owner.PlanSHA256 != preSHA || fence.authority.Workspace != schemaMaintenanceHostWorkspace(runtime.paths, plan.TransitionID) {
		return out, errors.New("schema outcome has no actual same-process primary owner")
	}
	if fence.authority.Artifacts.Candidate != plan.Worker.Provider || filepath.Join(fence.authority.OldCapsule.Root, "capsule.json") != plan.Origins[0].OldCapsule.Path || fence.authority.OldCapsuleSHA256 != plan.Origins[0].OldCapsule.SHA256 || fence.owner.SystemIdentifier != plan.PreSchema.SystemIdentifier || fence.owner.Database != plan.PreSchema.Database || fence.owner.DatabaseOID != plan.PreSchema.DatabaseOID || fence.owner.Schema != plan.PreSchema.Schema || fence.owner.SchemaOID != plan.PreSchema.SchemaOID || fence.owner.Role != plan.Role {
		return out, errors.New("schema outcome changed its original provider, retained seal or physical owner")
	}
	if err := fence.Check(ctx); err != nil {
		return out, err
	}
	ownerSHA := startupContentSHA256(fence.ownerRaw)
	worker, err := runtime.readSchemaMaintenanceWorkerResult(fence.authority.Workspace, fence.owner, ownerSHA)
	if err != nil {
		return out, err
	}
	set, proof, setRef, proofRef, err := runtime.schemaMaintenanceOutcomeBackup(plan, preSHA, worker.StartedUTC)
	if err != nil {
		return out, err
	}
	stops, err := schemaMaintenanceBackupStopBindings(plan, preSHA, stopped)
	if err != nil || !reflect.DeepEqual(stops, proof.StoppedOrigins) || proof.VerifiedUTC.After(worker.StartedUTC) {
		return out, errors.New("schema outcome backup differs from its original quiesced writer generations")
	}
	for _, capture := range stopped {
		if proof.CapturedUTC.Before(capture.StopStartedUTC) {
			return out, errors.New("schema outcome backup predates the original stopped generation")
		}
	}
	child, err := runtime.merchantStoreCapsuleEnvironment(ctx, fence.authority.OldCapsule, fence.authority.OldCapsuleSHA256, "")
	if err != nil {
		return out, err
	}
	values := map[string]string{}
	for _, row := range child {
		key, value, ok := strings.Cut(row, "=")
		if !ok {
			return out, errors.New("schema outcome read-only child is malformed")
		}
		if _, dup := values[key]; dup {
			return out, errors.New("schema outcome read-only child is duplicated")
		}
		values[key] = value
	}
	dsn, environment, err := productionSealedDatabaseCommand(values)
	if err != nil {
		return out, err
	}
	query := func(sql string, limit int) ([]byte, error) {
		if err := fence.CheckPhysical(ctx); err != nil {
			return nil, err
		}
		guarded, err := schemaMaintenanceOutcomeGuardQuery(sql, int(fence.owner.BackendPID))
		if err != nil {
			return nil, err
		}
		body, err := runtime.runner.Run(ctx, productionCommand{Name: commandPSQL, Args: []string{"-X", "-q", "-v", "ON_ERROR_STOP=1", "--no-align", "--tuples-only", "--command", guarded, "--dbname", dsn}, Env: environment, Sensitive: true, Timeout: 5 * time.Minute, OutputLimit: limit})
		if err != nil {
			return nil, errors.New("schema outcome actual read-only PostgreSQL capture failed")
		}
		if err := fence.CheckPhysical(ctx); err != nil {
			return nil, err
		}
		return body, nil
	}
	provider, err := runtime.verifyMerchantStoreCapsuleArtifact(ctx, fence.authority.Artifacts, false)
	if err != nil {
		return out, err
	}
	if err := runtime.verifyMerchantStoreWriterProvider(ctx, merchantStoreCapsuleWorkspace(fence.authority.Artifacts), provider, child, "candidate"); err != nil {
		return out, errors.New("schema outcome actual signed candidate schema verification failed")
	}
	postRaw, err := query(existingSchemaMetadataQuery(plan.PreSchema.Schema), 8<<20)
	if err != nil {
		return out, err
	}
	out.PostSchema, err = decodeExistingSchemaSnapshot(postRaw)
	if err != nil {
		return out, err
	}
	fullQuery, err := schemaMaintenanceBackupSourceQuery(plan.PreSchema.Schema)
	if err != nil {
		return out, err
	}
	fullRaw, err := query(fullQuery, 8<<20)
	if err != nil {
		return out, err
	}
	out.PostSource, err = decodeSchemaMaintenanceBackupSource(bytes.TrimSpace(fullRaw))
	if err != nil {
		return out, err
	}
	projectionQuery, err := schemaMaintenanceBackupProjectedSourceQuery(plan.PreSchema.Schema, set.SourceBefore, fence.ownerRaw)
	if err != nil {
		return out, err
	}
	projectedRaw, err := query(projectionQuery, 8<<20)
	if err != nil {
		return out, err
	}
	out.ProjectedSource, err = decodeSchemaMaintenanceBackupProjectedSource(bytes.TrimSpace(projectedRaw))
	if err != nil {
		return out, err
	}
	workerRaw, err := runtime.readExistingSchemaSealedFile(filepath.Join(fence.authority.Workspace, "state", "schema-worker-result.json"), true)
	expectedWorker, encodeErr := json.MarshalIndent(worker, "", "  ")
	if err != nil || encodeErr != nil || !bytes.Equal(workerRaw, append(expectedWorker, '\n')) {
		return out, errors.New("schema outcome worker changed after its validated successful invocation")
	}
	out.Format, out.TransitionID, out.PrePlanSHA256, out.OwnerSHA256 = schemaMaintenanceOutcomeFormat, plan.TransitionID, preSHA, ownerSHA
	out.PreSchema, out.BackupSet, out.BackupProof = plan.PreSchema, setRef, proofRef
	out.WorkerResult = productionReleaseFilePlan{Path: filepath.Join(fence.authority.Workspace, "state", "schema-worker-result.json"), SHA256: startupContentSHA256(workerRaw)}
	out.QualifiedProviderSHA256, out.CapturedUTC = worker.ProviderSHA256, runtime.now().UTC()
	if out.CapturedUTC.Before(worker.FinishedUTC) {
		return out, errors.New("schema outcome capture predates its actual completed worker")
	}
	if err := validateSchemaMaintenanceOutcome(out, plan, preSHA, ownerSHA, set.SourceBefore, runtime.paths); err != nil {
		return out, err
	}
	afterRaw, err := query(existingSchemaMetadataQuery(plan.PreSchema.Schema), 8<<20)
	if err != nil {
		return out, err
	}
	after, err := decodeExistingSchemaSnapshot(afterRaw)
	if err != nil || after != out.PostSchema {
		return out, errors.New("schema outcome catalog changed during old-data projection")
	}
	if err := fence.lease.checkSchemaMaintenanceRecoveryBusiness(ctx); err != nil {
		return out, err
	}
	if err := fence.Check(ctx); err != nil {
		return out, err
	}
	if err := fence.requireSchemaMaintenanceNoOtherClients(ctx); err != nil {
		return out, err
	}
	raw, err := json.MarshalIndent(out, "", "  ")
	if err != nil || len(raw)+1 > schemaMaintenanceOutcomeMaxBytes {
		return out, errors.New("schema outcome receipt exceeds its bounded evidence budget")
	}
	if err := runtime.writeSchemaMaintenanceRawNewFile(filepath.Join(fence.authority.Workspace, "state", "schema-outcome.json"), append(raw, '\n')); err != nil {
		return out, err
	}
	return out, nil
}

// Each fixed read-only capture rejects an unexpected client both before and
// after its snapshot. pg_stat_clear_snapshot avoids trusting an older activity
// cache, including when a client arrives during a lengthy table digest.
func schemaMaintenanceOutcomeGuardQuery(query string, holderPID int) (string, error) {
	if holderPID <= 1 || strings.TrimSpace(query) == "" {
		return "", errors.New("schema outcome lacks its exact live holder backend")
	}
	const begin = "BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY;"
	body := strings.TrimSpace(query)
	if strings.HasPrefix(body, begin) {
		body = strings.TrimSpace(strings.TrimPrefix(body, begin))
		if !strings.HasSuffix(body, "COMMIT;") {
			return "", errors.New("schema outcome fixed read-only query is incomplete")
		}
		body = strings.TrimSpace(strings.TrimSuffix(body, "COMMIT;"))
	}
	guard := `DO $lmm_schema_outcome$ BEGIN
 PERFORM pg_catalog.pg_stat_clear_snapshot();
 IF EXISTS(SELECT 1 FROM pg_catalog.pg_stat_activity WHERE datid=(SELECT oid FROM pg_catalog.pg_database WHERE datname=pg_catalog.current_database()) AND backend_type='client backend' AND pid NOT IN (pg_catalog.pg_backend_pid(),` + strconv.Itoa(holderPID) + `)) THEN RAISE EXCEPTION 'schema maintenance outcome has another client'; END IF;
END $lmm_schema_outcome$;`
	return begin + "\n" + guard + "\n" + body + "\n" + guard + "\nCOMMIT;", nil
}

func (f *productionSchemaMaintenanceFence) requireSchemaMaintenanceNoOtherClients(ctx context.Context) error {
	if err := f.CheckPhysical(ctx); err != nil {
		return err
	}
	if _, err := f.lease.connection.Exec(ctx, "SELECT pg_catalog.pg_stat_clear_snapshot()"); err != nil {
		return errors.New("schema outcome client visibility is unavailable")
	}
	var others int
	err := f.lease.connection.QueryRow(ctx, `SELECT count(*)::int FROM pg_catalog.pg_stat_activity WHERE datid=(SELECT oid FROM pg_catalog.pg_database WHERE datname=current_database()) AND backend_type='client backend' AND pid<>pg_backend_pid()`).Scan(&others)
	if err != nil || others != 0 {
		return errors.New("schema outcome still has an unclassified database client")
	}
	return f.CheckPhysical(ctx)
}
