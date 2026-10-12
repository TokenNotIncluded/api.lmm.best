package deploycli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// These references describe original receipts, not a claim that an uncertain
// worker succeeded. Secondary origins must not acquire a primary owner copy.
type productionSchemaMaintenancePreExecRejectedEvidence struct {
	Plan           productionReleaseFilePlan  `json:"plan"`
	OriginalDaemon productionReleaseFilePlan  `json:"original_daemon"`
	OriginalOwner  *productionReleaseFilePlan `json:"original_owner,omitempty"`
	WorkerIntent   *productionReleaseFilePlan `json:"worker_intent,omitempty"`
	WorkerResult   *productionReleaseFilePlan `json:"worker_result,omitempty"`
}

// This is the complete immutable official 117 tuple. It intentionally does
// not follow the current engine's runner: a later engine may permit this path.
func schemaMaintenancePreExecRejected117Provider() productionReleasePackagePlan {
	return productionReleasePackagePlan{
		DeployEngineSHA256:            "a6a16a7de137feffb49a06e9b140ed97d2490f78220f6c58a7d0db7e2db46e47",
		MerchantStoreWriterCapability: 8,
		PackagePath:                   "assets/candidate.pkg.tar.zst", PackageSHA256: "412d8a4fc2e97cd070f230f97153423eff9580689c2b9aa47553ec526c1dab92",
		Name: "lmm-api-go-bin", Version: "0.2.117-1", Identity: "lmm-api-go-bin 0.2.117-1",
		GitRevision: "3adaf17b2444a73f435bef4865381d2538670179", ContractRevision: "59854984853104df5c353e2f681a15fc7924742f9a2e468c29af248dce45ce03",
		PayloadSHA256: "71621e28d4539d075c569efe9fa2fa572ff59fd52bf550077b5c383dadb39144",
		ReleaseAsset:  "assets/candidate.release.tar.gz", ReleaseAssetSHA256: "d1f5785468d42bcc98909a30a14f17f7833b98313c45fae9a8a47295c3321ba4",
		SignatureBundle: "assets/candidate.sigstore.json", SignatureBundleSHA256: "078f3c14943ce9348645db11923da496cd61d8ca9b34d9acfa1952f491e258b9",
		ReleaseTag: "go-v0.2.117", Workflow: "release-go.yml",
	}
}

// At source 3adaf17b, verifyMerchantStoreCapsuleArtifact returns this lmm-api
// entry (a link to lmm-api-go). worker.go passes it directly as command.Name.
// state.go:190-238 compares exact tool names, never basename or symlink target;
// no absolute merchant-capsule entry matches, so it returns nil,error before
// constructing the provider exec.Cmd or calling process.Run. The full old
// engine/source tuple above, not an empty output hash, establishes this fact.
func schemaMaintenance117RejectsWorkerBeforeExec(paths productionPaths, origin productionSchemaMaintenanceOriginPlan, provider productionReleasePackagePlan) bool {
	root := merchantStoreCapsuleRoot(paths, filepath.Base(filepath.Dir(origin.ArtifactCapsule.Path)))
	entry := filepath.Join(root, "tmp", "migrations", "merchant-store-candidate", "lmm-api")
	if provider != schemaMaintenancePreExecRejected117Provider() || origin.ArtifactCapsule.Path != filepath.Join(root, "capsule.json") ||
		!filepath.IsAbs(entry) || filepath.Clean(entry) != entry {
		return false
	}
	// Literal historical names: using current tool constants or dispatch here
	// would let a later allowlist change rewrite the old execution fact.
	switch entry {
	case "/usr/bin/cosign", "/usr/bin/nginx", "/usr/bin/runuser", "/usr/bin/curl", "systemd-run", "/usr/bin/systemd-run",
		"/usr/bin/age", "/usr/bin/bsdtar", "/usr/bin/bun", "/usr/bin/file", "/usr/bin/git", "/usr/bin/go", "/usr/bin/id",
		"/usr/bin/journalctl", "/usr/bin/makepkg", "/usr/bin/pacman", "/usr/bin/pg_dump", "/usr/bin/pg_restore", "/usr/bin/psql",
		"/usr/bin/scp", "/usr/bin/ssh", "/usr/bin/sudo", "/usr/bin/systemctl", "/usr/bin/vercmp", "/usr/bin/lmm-api":
		return false
	default:
		return true
	}
}

// This only validates original, local historical evidence. The caller still
// needs the original guardian/daemon generation, unchanged live catalog and PG
// owner, two-origin authority and a separately qualified recovery engine. It
// grants no owner release, replay, startup, DDL or recovery success authority.
func (runtime *productionRuntime) validateSchemaMaintenancePreExecRejected(plan productionSchemaMaintenancePlan, preSHA string, originalDaemon schemaMaintenanceRPCBinding, originalOwnerRaw []byte, evidence productionSchemaMaintenancePreExecRejectedEvidence) error {
	if runtime == nil || runtime.effectiveUID == nil || runtime.effectiveUID() != 0 || runtime.hostname == nil {
		return errors.New("pre-exec evidence requires native root scope")
	}
	canonicalPlan, err := canonicalProductionSchemaMaintenancePlan(plan, runtime.paths)
	if err != nil || startupContentSHA256(canonicalPlan) != preSHA || validateSchemaMaintenanceRPCBinding(originalDaemon) != nil ||
		originalDaemon.TransitionID != plan.TransitionID || originalDaemon.PlanSHA256 != preSHA ||
		plan.Worker.Provider != schemaMaintenancePreExecRejected117Provider() || originalDaemon.DaemonExeSHA256 != plan.Worker.Provider.DeployEngineSHA256 {
		return errors.New("pre-exec evidence is not the exact original official 117 plan and daemon")
	}
	host, err := runtime.hostname()
	if err != nil {
		return errors.New("pre-exec evidence cannot establish its local origin")
	}
	var origin productionSchemaMaintenanceOriginPlan
	for _, candidate := range plan.Origins {
		if candidate.Host == host {
			origin = candidate
		}
	}
	if origin.Host == "" || !schemaMaintenance117RejectsWorkerBeforeExec(runtime.paths, origin, plan.Worker.Provider) {
		return errors.New("pre-exec evidence is outside the known 117 worker path")
	}
	workspace := schemaMaintenanceHostWorkspace(runtime.paths, plan.TransitionID)
	if err := runtime.merchantStorePrivateDirectory(filepath.Join(workspace, "state"), false); err != nil {
		return err
	}
	type snapshot struct {
		ref   productionReleaseFilePlan
		raw   []byte
		limit int64
	}
	var snapshots []snapshot
	read := func(ref productionReleaseFilePlan, path string, limit int64) ([]byte, error) {
		if validateSchemaMaintenanceReference(ref) != nil || ref.Path != path {
			return nil, errors.New("pre-exec evidence reference changed its fixed original path")
		}
		raw, err := runtime.readExistingSchemaSealedFileBounded(path, true, limit)
		if err != nil || startupContentSHA256(raw) != ref.SHA256 {
			return nil, errors.New("pre-exec original receipt is unsafe or changed")
		}
		snapshots = append(snapshots, snapshot{ref, raw, limit})
		return raw, nil
	}
	rawPlan, err := read(evidence.Plan, filepath.Join(workspace, "schema-maintenance-plan.json"), 1<<20)
	if err != nil || evidence.Plan.SHA256 != preSHA || !bytes.Equal(rawPlan, canonicalPlan) {
		return errors.New("pre-exec original plan bytes changed")
	}
	daemonRaw, err := schemaMaintenanceRPCMarshal(originalDaemon)
	if err != nil {
		return err
	}
	raw, err := read(evidence.OriginalDaemon, filepath.Join(workspace, "state", "daemon-binding.json"), 16384)
	if err != nil || !bytes.Equal(raw, append(daemonRaw, '\n')) {
		return errors.New("pre-exec original daemon receipt changed")
	}
	// Loading is a bounded canonical-file check, not Sigstore verification,
	// extraction, a verifier subprocess, or a database connection.
	capsuleRaw, err := read(origin.ArtifactCapsule, origin.ArtifactCapsule.Path, 32768)
	if err != nil {
		return err
	}
	capsule, err := runtime.loadMerchantStoreCapsule(origin.ArtifactCapsule.Path, origin.ArtifactCapsule.SHA256)
	if err != nil || capsule.Format != 1 || capsule.StartupPolicy != "held" || capsule.DeploymentID != plan.TransitionID ||
		capsule.Host != host || capsule.Candidate != plan.Worker.Provider || capsule.Rollback != merchantStoreCapsuleProjection(origin.Recovery.Candidate, "rollback") ||
		capsule.Writer == nil || capsule.ExistingSchemaContract == nil || capsule.Writer.Role != plan.Role || capsule.Writer.RequiredCapability != plan.RequiredCapability {
		return errors.New("pre-exec original held artifact capsule changed")
	}
	physical := *capsule.ExistingSchemaContract
	physical.StartupSHA256, physical.SignedUnitSHA256 = "", ""
	if physical != plan.PreSchema {
		return errors.New("pre-exec original artifact physical catalog differs")
	}
	canonicalCapsule, _ := canonicalMerchantStoreCapsule(capsule)
	if !bytes.Equal(capsuleRaw, canonicalCapsule) {
		return errors.New("pre-exec artifact bytes changed while loading")
	}
	primary := host == plan.PrimaryHost
	if primary {
		if evidence.OriginalOwner == nil || evidence.WorkerIntent == nil || evidence.WorkerResult == nil || len(originalOwnerRaw) == 0 {
			return errors.New("pre-exec primary lacks the exact owner and worker receipt pair")
		}
		ownerRaw, err := read(*evidence.OriginalOwner, filepath.Join(workspace, "state", "schema-owner.json"), 16384)
		if err != nil || !bytes.Equal(ownerRaw, originalOwnerRaw) || validateSchemaMaintenanceDeadOriginalOwner(ownerRaw, plan, preSHA, originalDaemon, capsule, runtime.paths.Service) != nil {
			return errors.New("pre-exec primary owner differs from the original typed owner")
		}
		intentRaw, err := read(*evidence.WorkerIntent, filepath.Join(workspace, "state", "schema-worker-intent.json"), 16384)
		if err != nil {
			return err
		}
		resultRaw, err := read(*evidence.WorkerResult, filepath.Join(workspace, "state", "schema-worker-result.json"), 16384)
		if err != nil {
			return err
		}
		var result productionSchemaMaintenanceWorkerResult
		decoder := json.NewDecoder(bytes.NewReader(resultRaw))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&result) != nil || decoder.Decode(&struct{}{}) != io.EOF {
			return errors.New("pre-exec worker result JSON is ambiguous")
		}
		canonical, _ := json.MarshalIndent(result, "", "  ")
		if !bytes.Equal(resultRaw, append(canonical, '\n')) || result.Format != "lmm-schema-maintenance-worker-v1" ||
			result.TransitionID != plan.TransitionID || result.PlanSHA256 != preSHA || result.OwnerSHA256 != startupContentSHA256(ownerRaw) ||
			result.ProviderSHA256 != plan.Worker.Provider.PayloadSHA256 || result.Phase != "WORKER_RESULT_UNCERTAIN" || result.OutputSHA256 != startupContentSHA256(nil) ||
			result.StartedUTC.IsZero() || result.FinishedUTC.IsZero() || result.StartedUTC.Location() != time.UTC || result.FinishedUTC.Location() != time.UTC || result.FinishedUTC.Before(result.StartedUTC) {
			return errors.New("pre-exec result is not the complete original 117 rejected invocation")
		}
		result.Phase, result.OutputSHA256, result.FinishedUTC = "WORKER_INTENT", "", time.Time{}
		canonical, _ = json.MarshalIndent(result, "", "  ")
		if !bytes.Equal(intentRaw, append(canonical, '\n')) {
			return errors.New("pre-exec intent and result are not the exact original pair")
		}
	} else if evidence.OriginalOwner != nil || evidence.WorkerIntent != nil || evidence.WorkerResult != nil || len(originalOwnerRaw) != 0 {
		return errors.New("pre-exec secondary cannot inherit primary worker or owner evidence")
	}
	absent := func() error {
		for _, name := range []string{
			"schema-worker-intent.json", "schema-worker-result.json", "schema-worker-output.log", "schema-outcome.json", "owner-released.json",
			"recovery-binding.json", "recovery-startup-profile.json", "recovery-hook-intent.json", "recovery-hook-bound.json", "recovery-start-intent.json", "recovery-started.json",
			"reopen-intent.json", "admission-restored.json", "host-finished.json", "abort-prepared.json", "abort-binding.json", "abort-start-intent.json", "abort-started.json", "abort-finish-intent.json", "abort-host-finished.json",
		} {
			if primary && (name == "schema-worker-intent.json" || name == "schema-worker-result.json") {
				continue
			}
			if _, err := os.Lstat(filepath.Join(workspace, "state", name)); !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("pre-exec evidence found possible worker or downstream progress at %s", name)
			}
		}
		return nil
	}
	if err := absent(); err != nil {
		return err
	}
	// Long enclosing recovery guards must call this again. Even within this
	// check, reread every original byte and repeat exact absence at the end.
	for _, snapshot := range snapshots {
		raw, err := runtime.readExistingSchemaSealedFileBounded(snapshot.ref.Path, true, snapshot.limit)
		if err != nil || !bytes.Equal(raw, snapshot.raw) || startupContentSHA256(raw) != snapshot.ref.SHA256 {
			return errors.New("pre-exec original evidence changed during validation")
		}
	}
	return absent()
}
