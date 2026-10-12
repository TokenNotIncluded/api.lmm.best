package deploycli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/internal/deploymentfence"
)

// The result records the actual outcome of one child invocation. A nonzero or
// lost result never authorizes a retry or owner release: COMMIT may have crossed
// the PostgreSQL write boundary even when the controller lost its reply.
type productionSchemaMaintenanceWorkerResult struct {
	Format         string    `json:"format"`
	TransitionID   string    `json:"transition_id"`
	PlanSHA256     string    `json:"plan_sha256"`
	OwnerSHA256    string    `json:"owner_sha256"`
	ProviderSHA256 string    `json:"provider_sha256"`
	Phase          string    `json:"phase"`
	OutputSHA256   string    `json:"output_sha256"`
	StartedUTC     time.Time `json:"started_utc"`
	FinishedUTC    time.Time `json:"finished_utc"`
}

func (runtime *productionRuntime) runSchemaMaintenanceWorker(ctx context.Context, fence *productionSchemaMaintenanceFence) (productionSchemaMaintenanceWorkerResult, error) {
	var result productionSchemaMaintenanceWorkerResult
	if fence == nil || fence.runtime != runtime || fence.authority.Artifacts.Host != fence.owner.Host {
		return result, errors.New("schema worker has no actual same-process native owner")
	}
	if err := fence.Check(ctx); err != nil {
		return result, err
	}
	a := fence.authority
	if a.CheckBackup == nil {
		return result, errors.New("schema worker lacks the independent quiesced backup verifier")
	}
	if err := a.CheckBackup(ctx); err != nil {
		return result, err
	}
	ownerPath := filepath.Join(a.Workspace, "state", "schema-owner.json")
	ownerSHA := startupContentSHA256(fence.ownerRaw)
	owner, ownerRaw, err := deploymentfence.ReadSchemaMaintenanceOwner(ownerPath, ownerSHA)
	if err != nil || owner != fence.owner || !bytes.Equal(ownerRaw, fence.ownerRaw) {
		return result, errors.New("schema worker lacks the exact owner bytes of this live holder")
	}
	provider, err := runtime.verifyMerchantStoreCapsuleArtifact(ctx, a.Artifacts, false)
	if err != nil {
		return result, err
	}
	child, err := runtime.schemaMaintenanceWorkerEnvironment(ctx, a.Artifacts, a.OldCapsule, a.OldCapsuleSHA256)
	if err != nil {
		return result, err
	}
	plan, err := runtime.loadSchemaMaintenancePlan(filepath.Join(a.Workspace, "schema-maintenance-plan.json"), a.PlanSHA256)
	if err != nil || plan.Worker.Provider != a.Artifacts.Candidate {
		return result, errors.New("schema worker differs from its actual canonical maintenance plan")
	}
	sealedEnvironment, err := runtime.readExistingSchemaSealedFile(plan.Worker.RWEnvironment.Path, true)
	expectedEnvironment, encodeErr := canonicalSchemaMaintenanceWorkerEnvironment(child)
	if err != nil || encodeErr != nil || startupContentSHA256(sealedEnvironment) != plan.Worker.RWEnvironment.SHA256 || !bytes.Equal(sealedEnvironment, expectedEnvironment) {
		return result, errors.New("schema worker child environment differs from the exact pre-change secret projection")
	}
	if sha256MustEqual(provider, fence.owner.ProviderSHA256) != nil {
		return result, errors.New("schema worker executable changed after official artifact verification")
	}
	if err := fence.Check(ctx); err != nil {
		return result, err
	}
	result = productionSchemaMaintenanceWorkerResult{Format: "lmm-schema-maintenance-worker-v1", TransitionID: a.TransitionID, PlanSHA256: a.PlanSHA256, OwnerSHA256: ownerSHA, ProviderSHA256: fence.owner.ProviderSHA256, Phase: "WORKER_INTENT", StartedUTC: runtime.now().UTC()}
	raw, _ := json.MarshalIndent(result, "", "  ")
	if err := runtime.writeSchemaMaintenanceRawNewFile(filepath.Join(a.Workspace, "state", "schema-worker-intent.json"), append(raw, '\n')); err != nil {
		return result, errors.New("schema worker has an existing or uncertain invocation; reconcile without dispatching again")
	}
	// Nothing invokes the ordinary --apply path. The signed child independently
	// verifies its executable, exact owner, holder PG session, sole-client rule
	// and the final transaction state before committing its additive DDL.
	output, runErr := runtime.runSchemaMaintenanceWorkerProvider(ctx, provider, ownerPath, ownerSHA, child)
	result.FinishedUTC, result.OutputSHA256 = runtime.now().UTC(), startupContentSHA256(output)
	result.Phase = "WORKER_RESULT_UNCERTAIN"
	if runErr == nil && bytes.Equal(output, []byte("PostgreSQL schema-only maintenance transaction committed; application data migrations were not run\n")) && sha256MustEqual(provider, fence.owner.ProviderSHA256) == nil {
		if checkErr := fence.Check(ctx); checkErr == nil {
			if logErr := runtime.writeSchemaMaintenanceRawNewFile(filepath.Join(a.Workspace, "state", "schema-worker-output.log"), output); logErr == nil {
				result.Phase = "WORKER_SUCCEEDED"
			} else {
				runErr = logErr
			}
		} else {
			runErr = checkErr
		}
	}
	// Save only bounded child output hashes; database credentials and raw
	// subprocess errors never become the controller's public JSON response.
	raw, _ = json.MarshalIndent(result, "", "  ")
	receiptErr := runtime.writeSchemaMaintenanceRawNewFile(filepath.Join(a.Workspace, "state", "schema-worker-result.json"), append(raw, '\n'))
	if receiptErr != nil {
		return result, errors.New("schema worker invocation finished but its result receipt failed; physical reconciliation is required")
	}
	if result.Phase != "WORKER_SUCCEEDED" || runErr != nil {
		return result, errors.New("schema worker result is uncertain; retain closed admission and the exact durable owner")
	}
	return result, nil
}

func (runtime *productionRuntime) runSchemaMaintenanceWorkerProvider(ctx context.Context, provider, ownerPath, ownerSHA string, child []string) ([]byte, error) {
	// The default runner admits fixed native tools, not dynamic artifact paths.
	// Reuse the verified-binary entry point without losing the worker's bounded
	// output, private environment, or operation budget. Failure remains uncertain.
	return runVerifiedBinaryWithOutputLimit(ctx, runtime.runner, provider, []string{"migrate", "--schema-only", "--maintenance-owner", ownerPath, "--maintenance-owner-sha256", ownerSHA}, child, "", 10*time.Minute, true, 1<<20)
}

func canonicalSchemaMaintenanceWorkerEnvironment(child []string) ([]byte, error) {
	// This private JSON array is an explicit execve environment record, never a
	// systemd EnvironmentFile and never a shell program.
	if len(child) == 0 {
		return nil, errors.New("schema worker child environment is empty")
	}
	raw, err := json.MarshalIndent(child, "", "  ")
	return append(raw, '\n'), err
}

func (runtime *productionRuntime) readSchemaMaintenanceWorkerResult(workspace string, owner productionMerchantStoreFenceOwner, ownerSHA string) (productionSchemaMaintenanceWorkerResult, error) {
	var result productionSchemaMaintenanceWorkerResult
	path := filepath.Join(workspace, "state", "schema-worker-result.json")
	raw, err := runtime.readExistingSchemaSealedFile(path, true)
	if err != nil || len(raw) > 16384 {
		return result, errors.New("schema worker result receipt unavailable")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&result) != nil {
		return result, errors.New("schema worker result JSON is invalid")
	}
	canonical, _ := json.MarshalIndent(result, "", "  ")
	if !bytes.Equal(append(canonical, '\n'), raw) || result.Format != "lmm-schema-maintenance-worker-v1" || result.TransitionID != owner.DeploymentID || result.PlanSHA256 != owner.PlanSHA256 || result.OwnerSHA256 != ownerSHA || result.ProviderSHA256 != owner.ProviderSHA256 || result.Phase != "WORKER_SUCCEEDED" || !productionSHA256Pattern.MatchString(result.OutputSHA256) || result.StartedUTC.IsZero() || result.FinishedUTC.Before(result.StartedUTC) {
		return result, errors.New("schema worker result differs from the original exact invocation")
	}
	// Compare the complete original one-shot intent, rather than accepting an
	// unrelated file with the expected basename as evidence of dispatch.
	intentRaw, err := runtime.readExistingSchemaSealedFile(filepath.Join(workspace, "state", "schema-worker-intent.json"), true)
	if err != nil || len(intentRaw) > 16384 {
		return result, errors.New("schema worker result lost its original invocation intent")
	}
	intent := result
	intent.Phase, intent.OutputSHA256, intent.FinishedUTC = "WORKER_INTENT", "", time.Time{}
	expectedIntent, _ := json.MarshalIndent(intent, "", "  ")
	if !bytes.Equal(append(expectedIntent, '\n'), intentRaw) {
		return result, errors.New("schema worker result differs from its complete original invocation intent")
	}
	output, err := runtime.readExistingSchemaSealedFile(filepath.Join(workspace, "state", "schema-worker-output.log"), true)
	if err != nil || startupContentSHA256(output) != result.OutputSHA256 || !bytes.Equal(output, []byte("PostgreSQL schema-only maintenance transaction committed; application data migrations were not run\n")) {
		return result, errors.New("schema worker result lost its exact successful child output")
	}
	return result, nil
}
