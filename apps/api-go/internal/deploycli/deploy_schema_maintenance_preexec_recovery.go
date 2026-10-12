package deploycli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
)

const schemaMaintenancePreExecRecoveryFormat = "lmm-schema-maintenance-preexec-recovery-v1"
const schemaMaintenancePreExecRejectedMode = "PREEXEC_REJECTED"

// The old plan and receipts remain immutable. This separate authority names a
// signed newer recovery engine, never a replacement for the old worker tuple.
type productionSchemaMaintenancePreExecRecoveryPlan struct {
	Format        string                                             `json:"format"`
	TransitionID  string                                             `json:"transition_id"`
	PrePlanSHA256 string                                             `json:"pre_plan_sha256"`
	Origins       []productionSchemaMaintenancePreExecRecoveryOrigin `json:"origins"`
}

type productionSchemaMaintenancePreExecRecoveryOrigin struct {
	Host                    string                                             `json:"host"`
	RecoveryArtifactCapsule productionReleaseFilePlan                          `json:"recovery_artifact_capsule"`
	Evidence                productionSchemaMaintenancePreExecRejectedEvidence `json:"evidence"`
}

func schemaMaintenancePreExecRecoveryArtifactID(plan productionSchemaMaintenancePlan) string {
	return plan.TransitionID + "-preexec-recoverer"
}

func canonicalSchemaMaintenancePreExecRecovery(plan productionSchemaMaintenancePreExecRecoveryPlan, original productionSchemaMaintenancePlan, preSHA string, paths productionPaths) ([]byte, error) {
	originalRaw, err := canonicalProductionSchemaMaintenancePlan(original, paths)
	if err != nil || startupContentSHA256(originalRaw) != preSHA || plan.Format != schemaMaintenancePreExecRecoveryFormat || plan.TransitionID != original.TransitionID || plan.PrePlanSHA256 != preSHA || len(plan.Origins) != len(original.Origins) || !productionIDPattern.MatchString(schemaMaintenancePreExecRecoveryArtifactID(original)) || original.Worker.Provider != schemaMaintenancePreExecRejected117Provider() {
		return nil, errors.New("pre-exec recovery authority changed its exact original official 117 plan")
	}
	for i, origin := range original.Origins {
		actual := plan.Origins[i]
		workspace := origin.Workspace
		if actual.Host != origin.Host || validateSchemaMaintenanceReference(actual.RecoveryArtifactCapsule) != nil || actual.RecoveryArtifactCapsule.Path != filepath.Join(merchantStoreCapsuleRoot(paths, schemaMaintenancePreExecRecoveryArtifactID(original)), "capsule.json") || actual.Evidence.Plan != (productionReleaseFilePlan{Path: filepath.Join(workspace, "schema-maintenance-plan.json"), SHA256: preSHA}) || validateSchemaMaintenanceReference(actual.Evidence.OriginalDaemon) != nil || actual.Evidence.OriginalDaemon.Path != filepath.Join(workspace, "state", "daemon-binding.json") {
			return nil, errors.New("pre-exec recovery origin changed its fixed artifact or historical evidence path")
		}
		refs := []*productionReleaseFilePlan{actual.Evidence.OriginalOwner, actual.Evidence.WorkerIntent, actual.Evidence.WorkerResult}
		if origin.Host == original.PrimaryHost {
			for j, name := range []string{"schema-owner.json", "schema-worker-intent.json", "schema-worker-result.json"} {
				if refs[j] == nil || validateSchemaMaintenanceReference(*refs[j]) != nil || refs[j].Path != filepath.Join(workspace, "state", name) {
					return nil, errors.New("pre-exec primary recovery lacks its exact preserved worker invocation")
				}
			}
		} else if refs[0] != nil || refs[1] != nil || refs[2] != nil {
			return nil, errors.New("pre-exec secondary cannot acquire primary worker or owner authority")
		}
	}
	raw, err := json.MarshalIndent(plan, "", "  ")
	return append(raw, '\n'), err
}

func (runtime *productionRuntime) loadSchemaMaintenancePreExecRecovery(original productionSchemaMaintenancePlan, preSHA string, ref productionReleaseFilePlan) (productionSchemaMaintenancePreExecRecoveryPlan, error) {
	var plan productionSchemaMaintenancePreExecRecoveryPlan
	if validateSchemaMaintenanceReference(ref) != nil || ref.Path != filepath.Join(schemaMaintenanceHostWorkspace(runtime.paths, original.TransitionID), "state", "preexec-recovery-plan.json") {
		return plan, errors.New("pre-exec recovery plan is outside its fixed original workspace")
	}
	raw, err := runtime.readExistingSchemaSealedFileBounded(ref.Path, true, 65536)
	if err != nil || startupContentSHA256(raw) != ref.SHA256 {
		return plan, errors.New("pre-exec recovery plan bytes are unsafe or changed")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&plan) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return plan, errors.New("pre-exec recovery plan JSON is ambiguous")
	}
	canonical, err := canonicalSchemaMaintenancePreExecRecovery(plan, original, preSHA, runtime.paths)
	if err != nil || !bytes.Equal(raw, canonical) {
		return plan, errors.New("pre-exec recovery plan is not the exact canonical authority")
	}
	return plan, nil
}

func (runtime *productionRuntime) schemaMaintenancePreExecRecoveryLocal(original productionSchemaMaintenancePlan, preSHA string, ref productionReleaseFilePlan) (productionSchemaMaintenancePreExecRecoveryOrigin, schemaMaintenanceRPCBinding, []byte, error) {
	var empty productionSchemaMaintenancePreExecRecoveryOrigin
	var daemon schemaMaintenanceRPCBinding
	plan, err := runtime.loadSchemaMaintenancePreExecRecovery(original, preSHA, ref)
	if err != nil {
		return empty, daemon, nil, err
	}
	host, err := runtime.hostname()
	if err != nil {
		return empty, daemon, nil, err
	}
	for _, origin := range plan.Origins {
		if origin.Host != host {
			continue
		}
		raw, err := runtime.readExistingSchemaSealedFileBounded(origin.Evidence.OriginalDaemon.Path, true, 16384)
		if err != nil || startupContentSHA256(raw) != origin.Evidence.OriginalDaemon.SHA256 || decodeControllerBackupJSON(raw, &daemon) != nil {
			return empty, daemon, nil, errors.New("pre-exec recovery original daemon receipt changed")
		}
		var ownerRaw []byte
		if origin.Evidence.OriginalOwner != nil {
			ownerRaw, err = runtime.readExistingSchemaSealedFileBounded(origin.Evidence.OriginalOwner.Path, true, 16384)
			if err != nil || startupContentSHA256(ownerRaw) != origin.Evidence.OriginalOwner.SHA256 {
				return empty, daemon, nil, errors.New("pre-exec recovery original owner receipt changed")
			}
		}
		return origin, daemon, ownerRaw, nil
	}
	return empty, daemon, nil, errors.New("pre-exec recovery plan has no registered local origin")
}

// Pure local revalidation for transaction guards. Full Sigstore/package
// qualification is a separate pre-transaction operation below; this method
// never extracts, starts a subprocess, opens a database, or changes a receipt.
func (runtime *productionRuntime) checkSchemaMaintenancePreExecRecovery(ctx context.Context, original productionSchemaMaintenancePlan, preSHA string, ref productionReleaseFilePlan) (productionMerchantStoreCapsule, error) {
	var empty productionMerchantStoreCapsule
	if err := ctx.Err(); err != nil {
		return empty, err
	}
	origin, daemon, ownerRaw, err := runtime.schemaMaintenancePreExecRecoveryLocal(original, preSHA, ref)
	if err != nil {
		return empty, err
	}
	if err := runtime.validateSchemaMaintenancePreExecRejected(original, preSHA, daemon, ownerRaw, origin.Evidence); err != nil {
		return empty, err
	}
	c, err := runtime.loadMerchantStoreCapsule(origin.RecoveryArtifactCapsule.Path, origin.RecoveryArtifactCapsule.SHA256)
	if err != nil {
		return empty, err
	}
	var old productionSchemaMaintenanceOriginPlan
	for _, candidate := range original.Origins {
		if candidate.Host == origin.Host {
			old = candidate
		}
	}
	if c.Format != 1 || c.StartupPolicy != "held" || c.DeploymentID != schemaMaintenancePreExecRecoveryArtifactID(original) || c.Host != old.Host || c.Writer == nil || c.ExistingSchemaContract == nil || c.Candidate.Version != "0.2.118-1" || c.Candidate.ReleaseTag != "go-v0.2.118" || c.Candidate.Workflow != "release-go.yml" || c.Candidate.DeployEngineSHA256 == original.Worker.Provider.DeployEngineSHA256 || !productionSHA256Pattern.MatchString(c.Candidate.DeployEngineSHA256) || c.Rollback != merchantStoreCapsuleProjection(old.Recovery.Candidate, "rollback") || c.Writer.RequiredCapability != original.RequiredCapability || c.Writer.Role != original.Role || c.Writer.RequiredCapability > 7 {
		return empty, errors.New("pre-exec recoverer is not its separate official 118 artifact identity")
	}
	physical := *c.ExistingSchemaContract
	physical.StartupSHA256, physical.SignedUnitSHA256 = "", ""
	if physical != original.PreSchema {
		return empty, errors.New("pre-exec recoverer changed the original physical catalog contract")
	}
	for _, file := range []struct{ path, sha string }{{c.Candidate.PackagePath, c.Candidate.PackageSHA256}, {c.Candidate.ReleaseAsset, c.Candidate.ReleaseAssetSHA256}, {c.Candidate.SignatureBundle, c.Candidate.SignatureBundleSHA256}} {
		path := filepath.Join(c.Root, file.path)
		if runtime.merchantStoreCapsuleFile(path, 0600) != nil || sha256MustEqual(path, file.sha) != nil {
			return empty, errors.New("pre-exec recoverer official artifact bytes changed")
		}
	}
	engine := filepath.Join(c.Root, "tmp", "migrations", "merchant-store-candidate", deployEngineName)
	if runtime.merchantStoreCapsuleFile(engine, 0700) != nil || sha256MustEqual(engine, c.Candidate.DeployEngineSHA256) != nil || sha256MustEqual("/proc/self/exe", c.Candidate.DeployEngineSHA256) != nil {
		return empty, errors.New("pre-exec actual recoverer is not its sealed independent deployment engine")
	}
	return c, nil
}

func (runtime *productionRuntime) qualifySchemaMaintenancePreExecRecovery(ctx context.Context, original productionSchemaMaintenancePlan, preSHA string, ref productionReleaseFilePlan) (productionMerchantStoreCapsule, error) {
	c, err := runtime.checkSchemaMaintenancePreExecRecovery(ctx, original, preSHA, ref)
	if err != nil {
		return c, err
	}
	if _, err := runtime.verifyMerchantStoreCapsuleArtifact(ctx, c, false); err != nil {
		return productionMerchantStoreCapsule{}, err
	}
	return runtime.checkSchemaMaintenancePreExecRecovery(ctx, original, preSHA, ref)
}

// The signed restore proof supplies the actual pre-stop data inventory. This
// is consumed by the same CAS transaction, never as a caller-supplied boolean.
func (runtime *productionRuntime) schemaMaintenancePreExecRecoveryDataSource(original productionSchemaMaintenancePlan, preSHA string, ref productionReleaseFilePlan) (productionSchemaMaintenanceBackupSource, []byte, error) {
	var empty productionSchemaMaintenanceBackupSource
	origin, daemon, ownerRaw, err := runtime.schemaMaintenancePreExecRecoveryLocal(original, preSHA, ref)
	if err != nil || origin.Host != original.PrimaryHost || origin.Evidence.WorkerResult == nil {
		return empty, nil, errors.New("pre-exec data qualification requires original primary worker evidence")
	}
	if err := runtime.validateSchemaMaintenancePreExecRejected(original, preSHA, daemon, ownerRaw, origin.Evidence); err != nil {
		return empty, nil, err
	}
	raw, err := runtime.readExistingSchemaSealedFileBounded(origin.Evidence.WorkerResult.Path, true, 16384)
	var worker productionSchemaMaintenanceWorkerResult
	if err != nil || startupContentSHA256(raw) != origin.Evidence.WorkerResult.SHA256 || decodeControllerBackupJSON(raw, &worker) != nil {
		return empty, nil, errors.New("pre-exec data qualification lost its original worker observation time")
	}
	set, _, _, _, err := runtime.schemaMaintenanceOutcomeBackup(original, preSHA, worker.StartedUTC)
	if err != nil {
		return empty, nil, err
	}
	return set.SourceBefore, ownerRaw, nil
}
