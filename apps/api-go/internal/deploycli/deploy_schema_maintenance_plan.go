package deploycli

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const schemaMaintenancePlanFormat = "lmm-schema-maintenance-plan-v1"
const schemaMaintenanceRecoveryBindingFormat = "lmm-schema-maintenance-recovery-binding-v1"
const schemaMaintenanceWorkerKind = "single-tx-schema-only-v1"

// These are canonical data contracts, not live permission. The dedicated
// controller must separately verify signatures, actual host/PG generations,
// physical owner/session, quiesced backup and the worker result at every phase.
// No own SHA or post-execution digest can be embedded in the pre-change plan.
type productionSchemaMaintenancePlan struct {
	Format             string                                  `json:"format"`
	TransitionID       string                                  `json:"transition_id"`
	PrimaryAlias       string                                  `json:"primary_alias"`
	PrimaryHost        string                                  `json:"primary_host"`
	PreSchema          productionExistingSchemaContract        `json:"pre_schema"`
	Role               string                                  `json:"role"`
	RequiredCapability int                                     `json:"required_capability"`
	Origins            []productionSchemaMaintenanceOriginPlan `json:"origins"`
	Worker             productionSchemaMaintenanceWorkerPlan   `json:"worker"`
	BackupPolicy       productionSchemaMaintenanceBackupPolicy `json:"backup_policy"`
}

type productionSchemaMaintenanceOriginPlan struct {
	Alias           string                                    `json:"alias"`
	Host            string                                    `json:"host"`
	InstallKind     string                                    `json:"install_kind"`
	Workspace       string                                    `json:"workspace"`
	OldCapsule      productionReleaseFilePlan                 `json:"old_capsule"`
	ArtifactCapsule productionReleaseFilePlan                 `json:"artifact_capsule"`
	Guardian        productionSchemaMaintenanceGuardianRecipe `json:"guardian"`
	Recovery        productionSchemaMaintenanceRecoveryRecipe `json:"recovery"`
}

// Binding SHA is deliberately external: the realized guardian binding refers
// to the plan SHA, so putting its digest here would create a cycle.
type productionSchemaMaintenanceGuardianRecipe struct {
	Protocol    string   `json:"protocol"`
	BindingPath string   `json:"binding_path"`
	Socket      string   `json:"socket"`
	Locks       []string `json:"locks"`
}

type productionSchemaMaintenanceWorkerPlan struct {
	Kind          string                       `json:"kind"`
	Provider      productionReleasePackagePlan `json:"provider"`
	RWEnvironment productionReleaseFilePlan    `json:"rw_environment"`
}

type productionSchemaMaintenanceBackupPolicy struct {
	Kind                string `json:"kind"`
	ControllerDirectory string `json:"controller_directory"`
	ControllerPublicKey string `json:"controller_public_key"`
	RecipientSHA256     string `json:"recipient_sha256"`
	RestoreProofPath    string `json:"restore_proof_path"`
}

// Only identities and fixed future paths are known before DDL. A new ordinary
// no-DDL recovery plan/capsule is derived from the actual resulting catalog.
type productionSchemaMaintenanceRecoveryRecipe struct {
	DeploymentID        string                       `json:"deployment_id"`
	ControllerWorkspace string                       `json:"controller_workspace"`
	PlanPath            string                       `json:"plan_path"`
	CapsulePath         string                       `json:"capsule_path"`
	Candidate           productionReleasePackagePlan `json:"candidate"`
	Rollback            productionReleasePackagePlan `json:"rollback"`
}

// This append-only record is created AFTER the separately recorded actual DDL
// outcome and recovery plan/capsules. It cannot authorize the earlier mutation.
type productionSchemaMaintenanceRecoveryBinding struct {
	Format        string                                      `json:"format"`
	TransitionID  string                                      `json:"transition_id"`
	PrePlanSHA256 string                                      `json:"pre_plan_sha256"`
	OwnerSHA256   string                                      `json:"owner_sha256"`
	Outcome       productionReleaseFilePlan                   `json:"outcome"`
	WorkerResult  productionReleaseFilePlan                   `json:"worker_result"`
	PostSchema    productionExistingSchemaContract            `json:"post_schema"`
	Origins       []productionSchemaMaintenanceRecoveryOrigin `json:"origins"`
}

type productionSchemaMaintenanceRecoveryOrigin struct {
	Alias            string                    `json:"alias"`
	Host             string                    `json:"host"`
	RecoveryPlan     productionReleaseFilePlan `json:"recovery_plan"`
	Capsule          productionReleaseFilePlan `json:"capsule"`
	StartupSHA256    string                    `json:"startup_sha256"`
	SignedUnitSHA256 string                    `json:"signed_unit_sha256"`
}

func schemaMaintenanceNativeLocks() []string {
	return []string{"/run/lock/lmm-api-go-deploy.lock", "/var/lib/lmm-api-deploy-systemd/lock", "/srv/lmm-api-frontend/.release.lock"}
}

func validateSchemaMaintenanceReference(ref productionReleaseFilePlan) error {
	if !filepath.IsAbs(ref.Path) || filepath.Clean(ref.Path) != ref.Path || ref.Path == "/" || strings.ContainsAny(ref.Path, "\x00\r\n") || len(ref.Path) > 4096 || !productionSHA256Pattern.MatchString(ref.SHA256) {
		return errors.New("schema maintenance file reference is not exact and canonical")
	}
	return nil
}

func validateSchemaMaintenanceProvider(p productionReleasePackagePlan, role string, floor int) error {
	version, err := packageReleaseVersion(p.Version)
	if err != nil || p.Name != productionAURPackageName || p.Identity != p.Name+" "+p.Version || p.ReleaseTag != "go-v"+version || p.Workflow != "release-go.yml" ||
		!productionRevisionPattern.MatchString(p.GitRevision) || !productionContractPattern.MatchString(p.ContractRevision) ||
		!productionSHA256Pattern.MatchString(p.PackageSHA256) || !productionSHA256Pattern.MatchString(p.PayloadSHA256) || !productionSHA256Pattern.MatchString(p.DeployEngineSHA256) ||
		!productionSHA256Pattern.MatchString(p.ReleaseAssetSHA256) || !productionSHA256Pattern.MatchString(p.SignatureBundleSHA256) ||
		p.PackagePath != merchantStoreCapsuleAsset(role, "pkg.tar.zst") || p.ReleaseAsset != merchantStoreCapsuleAsset(role, "release.tar.gz") || p.SignatureBundle != merchantStoreCapsuleAsset(role, "sigstore.json") ||
		merchantStoreWriterTargetAllowed(floor, p.MerchantStoreWriterCapability) != nil {
		return errors.New("schema maintenance provider tuple is not a complete supported modern official release identity")
	}
	return nil
}

func validateProductionSchemaMaintenancePlan(plan productionSchemaMaintenancePlan, paths productionPaths) error {
	if plan.Format != schemaMaintenancePlanFormat || !productionIDPattern.MatchString(plan.TransitionID) || len(plan.Origins) != len(merchantStartupOrigins) ||
		plan.PrimaryAlias != merchantStartupOrigins[0].Alias || plan.PrimaryHost != merchantStartupOrigins[0].Host ||
		plan.Role == "" || len(plan.Role) > 63 || strings.ContainsAny(plan.Role, "\x00\r\n") || !validMerchantStoreCapability(plan.RequiredCapability) || plan.RequiredCapability > 7 {
		return errors.New("schema maintenance plan identity, primary or fixed origin closure is incomplete")
	}
	if err := validateProductionExistingSchemaContract(&plan.PreSchema); err != nil {
		return err
	}
	if plan.PreSchema.StartupSHA256 != "" || plan.PreSchema.SignedUnitSHA256 != "" {
		return errors.New("schema maintenance shared physical catalog cannot contain one host's startup seal")
	}
	workspace := schemaMaintenanceHostWorkspace(paths, plan.TransitionID)
	locks := schemaMaintenanceNativeLocks()
	for index, origin := range plan.Origins {
		registered := merchantStartupOrigins[index]
		kind := "package"
		if index == 1 {
			kind = "standalone"
		}
		if origin.Alias != registered.Alias || origin.Host != registered.Host || origin.InstallKind != kind || origin.Workspace != workspace {
			return errors.New("schema maintenance omitted, reordered or changed a fixed origin/installation owner")
		}
		if err := validateSchemaMaintenanceReference(origin.OldCapsule); err != nil {
			return err
		}
		oldID := filepath.Base(filepath.Dir(origin.OldCapsule.Path))
		if !productionIDPattern.MatchString(oldID) || oldID == plan.TransitionID || origin.OldCapsule.Path != filepath.Join(merchantStoreCapsuleRoot(paths, oldID), "capsule.json") {
			return errors.New("schema maintenance old capsule is outside its distinct canonical retained root")
		}
		if err := validateSchemaMaintenanceReference(origin.ArtifactCapsule); err != nil {
			return err
		}
		if origin.ArtifactCapsule.Path != filepath.Join(merchantStoreCapsuleRoot(paths, plan.TransitionID), "capsule.json") {
			return errors.New("schema maintenance artifact capsule is outside its exact held artifact root")
		}
		g := origin.Guardian
		if g.Protocol != schemaMaintenanceGuardianProtocol || g.BindingPath != filepath.Join(workspace, "state", "guardian-binding.json") || g.Socket != filepath.Join(workspace, "state", "guardian.sock") || len(g.Locks) != len(locks) {
			return errors.New("schema maintenance guardian recipe is not the fixed three-lock protocol")
		}
		for i, path := range locks {
			if g.Locks[i] != path {
				return errors.New("schema maintenance guardian omitted or reordered a native owner lock")
			}
		}
		r := origin.Recovery
		if !productionIDPattern.MatchString(r.DeploymentID) || r.DeploymentID == plan.TransitionID || r.DeploymentID == oldID || r.ControllerWorkspace != filepath.Join(workspace, "recovery", "controller-workspace") ||
			r.PlanPath != filepath.Join(r.ControllerWorkspace, productionReleasePlanFilename) || r.CapsulePath != filepath.Join(merchantStoreCapsuleRoot(paths, r.DeploymentID), "capsule.json") {
			return errors.New("schema maintenance recovery recipe reused a sealed root or escaped its fixed future paths")
		}
		for role, provider := range map[string]productionReleasePackagePlan{"candidate": r.Candidate, "rollback": r.Rollback} {
			if err := validateSchemaMaintenanceProvider(provider, role, plan.RequiredCapability); err != nil {
				return err
			}
		}
		if r.Candidate.ContractRevision != r.Rollback.ContractRevision || r.Candidate.PackageSHA256 == r.Rollback.PackageSHA256 || r.Candidate.PayloadSHA256 == r.Rollback.PayloadSHA256 {
			return errors.New("schema maintenance recovery requires two distinct same-route supported providers")
		}
		if index > 0 && (r != plan.Origins[0].Recovery) {
			return errors.New("schema maintenance origins do not share the exact same recovery recipe")
		}
	}
	if plan.Worker.Kind != schemaMaintenanceWorkerKind || validateSchemaMaintenanceProvider(plan.Worker.Provider, "candidate", plan.RequiredCapability) != nil ||
		validateSchemaMaintenanceReference(plan.Worker.RWEnvironment) != nil || plan.Worker.RWEnvironment.Path != filepath.Join(workspace, "state", "schema-worker.env") ||
		plan.Worker.Provider.PayloadSHA256 == plan.Origins[0].Recovery.Candidate.PayloadSHA256 || plan.Worker.Provider.PayloadSHA256 == plan.Origins[0].Recovery.Rollback.PayloadSHA256 ||
		plan.Worker.Provider.ContractRevision != plan.Origins[0].Recovery.Candidate.ContractRevision {
		return errors.New("schema maintenance worker lacks its fixed schema-only operation, new official payload or explicit RW environment")
	}
	b := plan.BackupPolicy
	_, keyErr := controllerBackupVerificationKey(b.ControllerPublicKey)
	if b.Kind != "controller-only-encrypted-quiesced-v1" || keyErr != nil || !productionSHA256Pattern.MatchString(b.ControllerPublicKey) || !filepath.IsAbs(b.ControllerDirectory) || filepath.Clean(b.ControllerDirectory) != b.ControllerDirectory || len(b.ControllerDirectory) > 4096 || strings.ContainsAny(b.ControllerDirectory, "\x00\r\n") ||
		!pathWithinRoot("/home/lightjunction/Backups", b.ControllerDirectory) || b.ControllerDirectory == "/home/lightjunction/Backups" || !productionSHA256Pattern.MatchString(b.RecipientSHA256) ||
		b.RestoreProofPath != filepath.Join(b.ControllerDirectory, "restore-proof.json") {
		return errors.New("schema maintenance backup policy lacks its controller-only protected backup/restore recipe")
	}
	return nil
}

func canonicalProductionSchemaMaintenancePlan(plan productionSchemaMaintenancePlan, paths productionPaths) ([]byte, error) {
	if err := validateProductionSchemaMaintenancePlan(plan, paths); err != nil {
		return nil, err
	}
	body, err := json.MarshalIndent(plan, "", "  ")
	return append(body, '\n'), err
}

func (runtime *productionRuntime) createSchemaMaintenancePlan(path string, plan productionSchemaMaintenancePlan) (string, error) {
	raw, err := canonicalProductionSchemaMaintenancePlan(plan, runtime.paths)
	if err != nil || path != filepath.Join(schemaMaintenanceHostWorkspace(runtime.paths, plan.TransitionID), "schema-maintenance-plan.json") {
		return "", errors.New("schema maintenance plan creation requires its exact canonical private workspace")
	}
	return runtime.writeSchemaMaintenanceDataNew(path, raw)
}

func (runtime *productionRuntime) loadSchemaMaintenancePlan(path, digest string) (productionSchemaMaintenancePlan, error) {
	var plan productionSchemaMaintenancePlan
	raw, err := runtime.readSchemaMaintenanceData(path, digest, &plan)
	if err != nil {
		return plan, err
	}
	canonical, err := canonicalProductionSchemaMaintenancePlan(plan, runtime.paths)
	if err != nil || !bytes.Equal(canonical, raw) || path != filepath.Join(schemaMaintenanceHostWorkspace(runtime.paths, plan.TransitionID), "schema-maintenance-plan.json") {
		return plan, errors.New("schema maintenance plan bytes or location are not canonical")
	}
	return plan, nil
}

func validateProductionSchemaMaintenanceRecoveryBinding(binding productionSchemaMaintenanceRecoveryBinding, plan productionSchemaMaintenancePlan, planSHA string, paths productionPaths) error {
	canonical, err := canonicalProductionSchemaMaintenancePlan(plan, paths)
	if err != nil || startupContentSHA256(canonical) != planSHA || binding.Format != schemaMaintenanceRecoveryBindingFormat || binding.TransitionID != plan.TransitionID || binding.PrePlanSHA256 != planSHA ||
		!productionSHA256Pattern.MatchString(binding.OwnerSHA256) || len(binding.Origins) != len(plan.Origins) {
		return errors.New("schema maintenance recovery binding does not reference its exact original plan and owner")
	}
	if err := validateSchemaMaintenanceReference(binding.Outcome); err != nil {
		return err
	}
	if binding.Outcome.Path != filepath.Join(schemaMaintenanceHostWorkspace(paths, plan.TransitionID), "state", "schema-outcome.json") {
		return errors.New("schema maintenance recovery binding selected another execution outcome")
	}
	if validateSchemaMaintenanceReference(binding.WorkerResult) != nil || binding.WorkerResult.Path != filepath.Join(schemaMaintenanceHostWorkspace(paths, plan.TransitionID), "state", "schema-worker-result.json") {
		return errors.New("schema maintenance recovery binding omitted its exact schema worker result")
	}
	if err := validateProductionExistingSchemaContract(&binding.PostSchema); err != nil {
		return err
	}
	pre, post := plan.PreSchema, binding.PostSchema
	pre.MetadataSHA256, post.MetadataSHA256 = "", ""
	if pre != post {
		return errors.New("schema maintenance post catalog changed physical database identity or embedded a host startup seal")
	}
	for i, origin := range binding.Origins {
		expected := plan.Origins[i]
		if origin.Alias != expected.Alias || origin.Host != expected.Host || origin.RecoveryPlan.Path != expected.Recovery.PlanPath || origin.Capsule.Path != expected.Recovery.CapsulePath ||
			validateSchemaMaintenanceReference(origin.RecoveryPlan) != nil || validateSchemaMaintenanceReference(origin.Capsule) != nil ||
			!productionSHA256Pattern.MatchString(origin.StartupSHA256) || !productionSHA256Pattern.MatchString(origin.SignedUnitSHA256) ||
			i > 0 && origin.SignedUnitSHA256 != binding.Origins[0].SignedUnitSHA256 {
			return errors.New("schema maintenance actual recovery closure omitted or changed a fixed origin/plan/capsule/startup profile")
		}
	}
	return nil
}

func canonicalProductionSchemaMaintenanceRecoveryBinding(binding productionSchemaMaintenanceRecoveryBinding, plan productionSchemaMaintenancePlan, planSHA string, paths productionPaths) ([]byte, error) {
	if err := validateProductionSchemaMaintenanceRecoveryBinding(binding, plan, planSHA, paths); err != nil {
		return nil, err
	}
	body, err := json.MarshalIndent(binding, "", "  ")
	return append(body, '\n'), err
}

func (runtime *productionRuntime) createSchemaMaintenanceRecoveryBinding(path string, binding productionSchemaMaintenanceRecoveryBinding, plan productionSchemaMaintenancePlan, planSHA string) (string, error) {
	raw, err := canonicalProductionSchemaMaintenanceRecoveryBinding(binding, plan, planSHA, runtime.paths)
	if err != nil || path != filepath.Join(schemaMaintenanceHostWorkspace(runtime.paths, plan.TransitionID), "state", "recovery-binding.json") {
		return "", errors.New("schema maintenance recovery binding requires its exact canonical private workspace")
	}
	return runtime.writeSchemaMaintenanceDataNew(path, raw)
}

func (runtime *productionRuntime) loadSchemaMaintenanceRecoveryBinding(path, digest string, plan productionSchemaMaintenancePlan, planSHA string) (productionSchemaMaintenanceRecoveryBinding, error) {
	var binding productionSchemaMaintenanceRecoveryBinding
	raw, err := runtime.readSchemaMaintenanceData(path, digest, &binding)
	if err != nil {
		return binding, err
	}
	canonical, err := canonicalProductionSchemaMaintenanceRecoveryBinding(binding, plan, planSHA, runtime.paths)
	if err != nil || !bytes.Equal(canonical, raw) || path != filepath.Join(schemaMaintenanceHostWorkspace(runtime.paths, plan.TransitionID), "state", "recovery-binding.json") {
		return binding, errors.New("schema maintenance recovery binding bytes or location are not canonical")
	}
	return binding, nil
}

func (runtime *productionRuntime) readSchemaMaintenanceData(path, digest string, value any) ([]byte, error) {
	if !productionSHA256Pattern.MatchString(digest) {
		return nil, errors.New("schema maintenance data requires its exact external digest")
	}
	raw, err := runtime.readExistingSchemaSealedFile(path, true)
	if err != nil || startupContentSHA256(raw) != digest {
		return nil, errors.New("schema maintenance private data file changed or is unsafe")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(value) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return nil, errors.New("schema maintenance data wire has unknown or trailing fields")
	}
	return raw, nil
}

func (runtime *productionRuntime) writeSchemaMaintenanceDataNew(path string, raw []byte) (string, error) {
	if len(raw) == 0 || len(raw) > 1<<20 {
		return "", errors.New("schema maintenance canonical data exceeds its bounded immutable wire size")
	}
	if err := runtime.merchantStorePrivateDirectory(filepath.Dir(path), false); err != nil {
		return "", err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return "", err
	}
	_, err = file.Write(raw)
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil || closeErr != nil {
		return "", errors.New("schema maintenance immutable data write failed; reconcile original file without replacing it")
	}
	if err := syncDirectory(filepath.Dir(path)); err != nil {
		return "", err
	}
	return startupContentSHA256(raw), nil
}
