package deploycli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

const schemaMaintenanceRecoveryProfileFormat = "lmm-schema-maintenance-recovery-profile-v1"

// The native host computes this profile from its actual, still-loaded old hook.
// Only the future hook path is changed. Its digest is normalized by the same
// narrowly scoped rule as ordinary immutable per-start capsules.
type productionSchemaMaintenanceRecoveryProfile struct {
	Format                string                           `json:"format"`
	TransitionID          string                           `json:"transition_id"`
	PrePlanSHA256         string                           `json:"pre_plan_sha256"`
	Host                  string                           `json:"host"`
	OwnerSHA256           string                           `json:"owner_sha256"`
	OutcomeSHA256         string                           `json:"outcome_sha256"`
	PostSchema            productionExistingSchemaContract `json:"post_schema"`
	OldCapsuleSHA256      string                           `json:"old_capsule_sha256"`
	StoppedCaptureSHA256  string                           `json:"stopped_capture_sha256"`
	OldHookSHA256         string                           `json:"old_hook_sha256"`
	ProspectiveHookSHA256 string                           `json:"prospective_hook_sha256"`
	CapsulePath           string                           `json:"capsule_path"`
	ProviderSHA256        string                           `json:"provider_sha256"`
	DeployEngineSHA256    string                           `json:"deploy_engine_sha256"`
	StartupSHA256         string                           `json:"startup_sha256"`
	SignedUnitSHA256      string                           `json:"signed_unit_sha256"`
}

func (h *productionSchemaMaintenanceHost) recoveryPlanOrigin(plan productionSchemaMaintenancePlan, preSHA string) (productionSchemaMaintenanceOriginPlan, error) {
	var origin productionSchemaMaintenanceOriginPlan
	if h == nil || h.runtime == nil {
		return origin, errors.New("schema recovery has no native host")
	}
	raw, err := canonicalProductionSchemaMaintenancePlan(plan, h.runtime.paths)
	if err != nil || startupContentSHA256(raw) != preSHA || h.binding.TransitionID != plan.TransitionID || h.binding.PlanSHA256 != preSHA {
		return origin, errors.New("schema recovery changed its exact original maintenance plan")
	}
	for _, item := range plan.Origins {
		if item.Host == h.binding.Host && item.Workspace == h.binding.Workspace && item.OldCapsule.Path == h.binding.CapsulePath && item.OldCapsule.SHA256 == h.binding.CapsuleSHA256 {
			return item, nil
		}
	}
	return origin, errors.New("schema recovery omitted the actual local retained capsule")
}

// This does not fabricate a loaded systemd property or write a pending hook.
// The existing actual hook is independently verified before replacing only
// its already-normalized semantic command in the prospective snapshot.
func (runtime *productionRuntime) prospectiveSchemaRecoveryStartup(ctx context.Context, old productionMerchantStoreCapsule, oldSHA, newPath string, loaded map[string]string) (string, string, string, string, error) {
	newID := filepath.Base(filepath.Dir(newPath))
	if old.Format != 1 || old.StartupPolicy != "per-start" || !productionIDPattern.MatchString(newID) || newPath != filepath.Join(merchantStoreCapsuleRoot(runtime.paths, newID), "capsule.json") || newPath == filepath.Join(old.Root, "capsule.json") {
		return "", "", "", "", errors.New("schema recovery must derive a distinct ordinary per-start hook")
	}
	if _, err := runtime.merchantStoreCapsuleStartup(ctx, old, oldSHA, loaded); err != nil {
		return "", "", "", "", err
	}
	commands, err := runtime.merchantStorePortableStartupCommands(loaded, old.Binary, filepath.Join(old.Root, "capsule.json"), oldSHA)
	if err != nil {
		return "", "", "", "", err
	}
	operator := deploymentInstalledCommand(old.Binary, old.Candidate)
	commands["ExecStartPre"] = operator + "\x00" + operator + " operator production writer-start --capsule " + newPath + " --capsule-sha256 " + merchantStoreCapsuleHashPlaceholder + "\x00no\x00privileged"
	commands["PersistentPortableHookSHA256"] = startupContentSHA256(merchantStorePortableHookBytes(operator, newPath, merchantStoreCapsuleHashPlaceholder))
	values, err := parseExistingSchemaLoadedEnvironment(loaded["Environment"])
	if err != nil {
		return "", "", "", "", err
	}
	_, startup, unit, err := runtime.existingSchemaSealedStartupCommands(ctx, loaded, values, commands)
	if err != nil {
		return "", "", "", "", err
	}
	oldHook, err := runtime.readExistingSchemaSealedFile(filepath.Join(runtime.paths.DropInDir, "90-merchant-startup-baseline.conf"), false)
	if err != nil {
		return "", "", "", "", err
	}
	return startup, unit, startupContentSHA256(oldHook), commands["PersistentPortableHookSHA256"], nil
}

func (h *productionSchemaMaintenanceHost) captureRecoveryProfile(ctx context.Context, plan productionSchemaMaintenancePlan, preSHA string, checkActiveOwner func(context.Context) error) (productionSchemaMaintenanceRecoveryProfile, error) {
	var profile productionSchemaMaintenanceRecoveryProfile
	origin, err := h.recoveryPlanOrigin(plan, preSHA)
	if err != nil {
		return profile, err
	}
	if checkActiveOwner == nil {
		return profile, errors.New("schema recovery profile requires the actual original physical owner")
	}
	if err := h.check(ctx); err != nil {
		return profile, err
	}
	if err := checkActiveOwner(ctx); err != nil {
		return profile, err
	}
	ownerRaw, err := h.runtime.readExistingSchemaSealedFile(filepath.Join(h.binding.Workspace, "state", "schema-owner.json"), true)
	if err != nil {
		return profile, err
	}
	ownerSHA := startupContentSHA256(ownerRaw)
	outcome, outcomeSHA, err := h.runtime.readSchemaMaintenanceOutcome(plan, preSHA, ownerSHA)
	if err != nil {
		return profile, err
	}
	capture, err := h.captureStopped(ctx)
	if err != nil || capture.Phase != "WRITER_STOPPED" {
		return profile, errors.New("schema recovery profile requires the sealed original stopped writer")
	}
	stoppedRaw, err := h.runtime.readExistingSchemaSealedFile(filepath.Join(h.binding.Workspace, "state", "writer-stopped.json"), true)
	if err != nil {
		return profile, err
	}
	old, err := h.runtime.loadMerchantStoreCapsule(h.binding.CapsulePath, h.binding.CapsuleSHA256)
	if err != nil || old.Candidate != origin.Recovery.Candidate || capture.ProviderSHA256 != origin.Recovery.Candidate.PayloadSHA256 {
		return profile, errors.New("schema recovery profile changed the actual retained signed provider")
	}
	loaded, err := h.runtime.loadedExistingSchemaUnit(ctx)
	if err != nil || loaded["MainPID"] != "0" || loaded["ActiveState"] != "inactive" || loaded["InvocationID"] != capture.InvocationID {
		return profile, errors.New("schema recovery profile lost its original stopped invocation")
	}
	startup, unit, oldHook, hook, err := h.runtime.prospectiveSchemaRecoveryStartup(ctx, old, h.binding.CapsuleSHA256, origin.Recovery.CapsulePath, loaded)
	if err != nil {
		return profile, err
	}
	profile = productionSchemaMaintenanceRecoveryProfile{Format: schemaMaintenanceRecoveryProfileFormat, TransitionID: plan.TransitionID, PrePlanSHA256: preSHA, Host: h.binding.Host,
		OwnerSHA256: ownerSHA, OutcomeSHA256: outcomeSHA, PostSchema: outcome.PostSchema, OldCapsuleSHA256: h.binding.CapsuleSHA256, StoppedCaptureSHA256: startupContentSHA256(stoppedRaw),
		OldHookSHA256: oldHook, ProspectiveHookSHA256: hook, CapsulePath: origin.Recovery.CapsulePath, ProviderSHA256: old.Candidate.PayloadSHA256,
		DeployEngineSHA256: old.Candidate.DeployEngineSHA256, StartupSHA256: startup, SignedUnitSHA256: unit}
	if _, err := h.captureStopped(ctx); err != nil {
		return profile, err
	}
	if err := checkActiveOwner(ctx); err != nil {
		return profile, err
	}
	raw, err := json.MarshalIndent(profile, "", "  ")
	if err != nil {
		return profile, err
	}
	raw = append(raw, '\n')
	path := filepath.Join(h.binding.Workspace, "state", "recovery-startup-profile.json")
	if _, err := os.Lstat(path); err == nil {
		previous, err := h.runtime.readExistingSchemaSealedFile(path, true)
		if err != nil || !bytes.Equal(previous, raw) {
			return profile, errors.New("schema recovery immutable startup profile changed")
		}
		return profile, h.check(ctx)
	} else if !errors.Is(err, os.ErrNotExist) {
		return profile, err
	}
	return profile, h.writeNew("recovery-startup-profile.json", raw)
}

func validateSchemaRecoveryProfileBinding(profile productionSchemaMaintenanceRecoveryProfile, binding productionSchemaMaintenanceRecoveryBinding, origin productionSchemaMaintenanceRecoveryOrigin, oldSHA string) error {
	if profile.Format != schemaMaintenanceRecoveryProfileFormat || profile.TransitionID != binding.TransitionID || profile.PrePlanSHA256 != binding.PrePlanSHA256 || profile.Host != origin.Host ||
		profile.OwnerSHA256 != binding.OwnerSHA256 || profile.OutcomeSHA256 != binding.Outcome.SHA256 || profile.PostSchema != binding.PostSchema || profile.OldCapsuleSHA256 != oldSHA ||
		profile.CapsulePath != origin.Capsule.Path || profile.StartupSHA256 != origin.StartupSHA256 || profile.SignedUnitSHA256 != origin.SignedUnitSHA256 {
		return errors.New("schema recovery capsule binding differs from the native prospective startup profile")
	}
	for _, sha := range []string{profile.OwnerSHA256, profile.OutcomeSHA256, profile.OldCapsuleSHA256, profile.StoppedCaptureSHA256, profile.OldHookSHA256, profile.ProspectiveHookSHA256, profile.ProviderSHA256, profile.DeployEngineSHA256, profile.StartupSHA256, profile.SignedUnitSHA256} {
		if !productionSHA256Pattern.MatchString(sha) {
			return errors.New("schema recovery native startup profile is incomplete")
		}
	}
	return nil
}

// Qualification before binding uses the actual old sealed read-only startup
// environment, while comparing the new full post catalog. The subsequent
// normal capsule qualifier independently checks the actually loaded new hook.
func (h *productionSchemaMaintenanceHost) qualifyRecoveryBeforeBind(ctx context.Context, c productionMerchantStoreCapsule) error {
	old, err := h.runtime.loadMerchantStoreCapsule(h.binding.CapsulePath, h.binding.CapsuleSHA256)
	if err != nil {
		return err
	}
	child, err := h.runtime.merchantStoreCapsuleEnvironment(ctx, old, h.binding.CapsuleSHA256, "")
	if err != nil {
		return err
	}
	for _, source := range c.SourceFiles {
		raw, err := h.runtime.readExistingSchemaSealedFile(filepath.Join(c.Root, "source", source.Path), true)
		if err != nil || startupContentSHA256(raw) != source.SHA256 {
			return errors.New("schema recovery official wrapper source changed before binding")
		}
	}
	for index, target := range []productionMerchantStoreWriterTarget{c.Writer.Candidate, c.Writer.Rollback} {
		role := "candidate"
		if index == 1 {
			role = "rollback"
		}
		provider, err := h.runtime.verifyMerchantStoreCapsuleArtifact(ctx, c, index == 1)
		if err != nil {
			return err
		}
		out, callErr := runVerifiedBinary(ctx, h.runtime.runner, provider, []string{"merchant-store-writer-gate", "status"}, child, filepath.Dir(provider), 35*time.Second, true)
		if err := qualifyMerchantStoreWriterStatus(out, callErr, c.Writer, target); err != nil {
			return err
		}
		if err := h.runtime.verifyMerchantStoreWriterProvider(ctx, merchantStoreCapsuleWorkspace(c), provider, child, role); err != nil {
			return err
		}
	}
	return h.runtime.verifyMerchantStoreCapsuleSchema(ctx, c, child)
}

type productionSchemaMaintenanceRecoveryHookIntent struct {
	Format        string                    `json:"format"`
	TransitionID  string                    `json:"transition_id"`
	PrePlanSHA256 string                    `json:"pre_plan_sha256"`
	Host          string                    `json:"host"`
	OwnerSHA256   string                    `json:"owner_sha256"`
	ProfileSHA256 string                    `json:"profile_sha256"`
	Capsule       productionReleaseFilePlan `json:"capsule"`
	HookSHA256    string                    `json:"hook_sha256"`
}

func (h *productionSchemaMaintenanceHost) writeRecoveryIntent(name string, value any) error {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	// h.writeNew synchronizes both the EXCL file and its directory before a
	// command can change the external service state.
	return h.writeNew(name, append(raw, '\n'))
}

func (h *productionSchemaMaintenanceHost) installRecoveryHook(ctx context.Context, plan productionSchemaMaintenancePlan, preSHA string, binding productionSchemaMaintenanceRecoveryBinding, checkActiveOwner func(context.Context) error) (productionSchemaMaintenanceHostCapture, error) {
	var capture productionSchemaMaintenanceHostCapture
	c, origin, err := h.recoveryCapsule(ctx, plan, preSHA, binding, false)
	if err != nil {
		return capture, err
	}
	profile, err := h.captureRecoveryProfile(ctx, plan, preSHA, checkActiveOwner)
	if err != nil {
		return capture, err
	}
	if err := validateSchemaRecoveryProfileBinding(profile, binding, origin, h.binding.CapsuleSHA256); err != nil {
		return capture, err
	}
	if err := h.qualifyRecoveryBeforeBind(ctx, c); err != nil {
		return capture, err
	}
	profileRaw, err := h.runtime.readExistingSchemaSealedFile(filepath.Join(h.binding.Workspace, "state", "recovery-startup-profile.json"), true)
	if err != nil {
		return capture, err
	}
	hook := merchantStorePortableHookBytes(deploymentInstalledCommand(c.Binary, c.Candidate), origin.Capsule.Path, origin.Capsule.SHA256)
	intent := productionSchemaMaintenanceRecoveryHookIntent{"lmm-schema-maintenance-recovery-hook-intent-v1", plan.TransitionID, preSHA, h.binding.Host, binding.OwnerSHA256, startupContentSHA256(profileRaw), origin.Capsule, startupContentSHA256(hook)}
	if err := h.writeRecoveryIntent("recovery-hook-intent.json", intent); err != nil {
		return capture, errors.New("schema recovery hook already has an intent or could not seal it; inspect without replay")
	}
	if _, err := h.captureStopped(ctx); err != nil {
		return capture, err
	}
	if err := checkActiveOwner(ctx); err != nil {
		return capture, err
	}
	drop := filepath.Join(h.runtime.paths.DropInDir, "90-merchant-startup-baseline.conf")
	oldHook, err := h.runtime.readExistingSchemaSealedFile(drop, false)
	if err != nil || startupContentSHA256(oldHook) != profile.OldHookSHA256 {
		return capture, errors.New("schema recovery actual old hook changed before binding")
	}
	if err := writeAtomicRegularFile(drop, hook, 0644); err != nil {
		return capture, fmt.Errorf("schema recovery hook binding is uncertain; preserve intent: %w", err)
	}
	if _, err := h.runtime.runner.Run(ctx, productionCommand{Name: commandSystemctl, Args: []string{"daemon-reload"}, Timeout: 15 * time.Second}); err != nil {
		return capture, errors.New("schema recovery hook reload is uncertain; preserve intent and inspect loaded hook")
	}
	capture, err = h.captureRecovery(ctx, plan, preSHA, binding)
	if err != nil {
		return capture, err
	}
	if err := checkActiveOwner(ctx); err != nil {
		return capture, err
	}
	return capture, h.writeCapture("recovery-hook-bound.json", capture)
}

type productionSchemaMaintenanceOwnerReleased struct {
	Format       string    `json:"format"`
	TransitionID string    `json:"transition_id"`
	PlanSHA256   string    `json:"plan_sha256"`
	OwnerSHA256  string    `json:"owner_sha256"`
	FinishedUTC  time.Time `json:"finished_utc"`
}

func (h *productionSchemaMaintenanceHost) readRecoveryOwnerReleased(plan productionSchemaMaintenancePlan, preSHA string, binding productionSchemaMaintenanceRecoveryBinding) (productionSchemaMaintenanceOwnerReleased, string, error) {
	var released productionSchemaMaintenanceOwnerReleased
	raw, err := h.runtime.readExistingSchemaSealedFile(filepath.Join(h.binding.Workspace, "state", "owner-released.json"), true)
	if err != nil || len(raw) > 16384 {
		return released, "", errors.New("schema recovery exact original owner release receipt is unavailable")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&released) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return released, "", errors.New("schema recovery original owner release receipt is malformed")
	}
	canonical, _ := json.MarshalIndent(released, "", "  ")
	if !bytes.Equal(append(canonical, '\n'), raw) || released.Format != "lmm-schema-maintenance-owner-released-v1" || released.TransitionID != plan.TransitionID || released.PlanSHA256 != preSHA || released.OwnerSHA256 != binding.OwnerSHA256 || released.FinishedUTC.IsZero() || released.FinishedUTC.Location() != time.UTC || h.runtime.now == nil || released.FinishedUTC.After(h.runtime.now().UTC()) {
		return released, "", errors.New("schema recovery original owner release receipt changed its exact execution binding")
	}
	return released, startupContentSHA256(raw), nil
}

type productionSchemaMaintenanceRecoveryStartIntent struct {
	Format                string                                 `json:"format"`
	TransitionID          string                                 `json:"transition_id"`
	PrePlanSHA256         string                                 `json:"pre_plan_sha256"`
	Host                  string                                 `json:"host"`
	OwnerSHA256           string                                 `json:"owner_sha256"`
	OwnerReleasedSHA256   string                                 `json:"owner_released_sha256"`
	RecoveryBindingSHA256 string                                 `json:"recovery_binding_sha256"`
	Capsule               productionReleaseFilePlan              `json:"capsule"`
	Stopped               productionSchemaMaintenanceHostCapture `json:"stopped"`
	StartedUTC            time.Time                              `json:"started_utc"`
}

// A failed/uncertain systemctl response never authorizes another start. The
// existing captureReady operation is the read-only reconciliation path.
func (h *productionSchemaMaintenanceHost) startRecovery(ctx context.Context, plan productionSchemaMaintenancePlan, preSHA string, binding productionSchemaMaintenanceRecoveryBinding, startableOrigins func(context.Context) error) (productionSchemaMaintenanceHostCapture, error) {
	var capture productionSchemaMaintenanceHostCapture
	if startableOrigins == nil {
		return capture, errors.New("schema recovery start requires actual fixed two-origin recovery observations")
	}
	c, origin, err := h.recoveryCapsule(ctx, plan, preSHA, binding, false)
	if err != nil {
		return capture, err
	}
	_, releasedSHA, err := h.readRecoveryOwnerReleased(plan, preSHA, binding)
	if err != nil {
		return capture, err
	}
	capture, err = h.captureRecovery(ctx, plan, preSHA, binding)
	if err != nil || capture.Phase != "RECOVERY_READY" {
		return capture, errors.New("schema recovery start lacks its actual stopped new-hook recovery qualification")
	}
	if err := h.verifyRecoveryOwnersReleased(ctx, c, origin.Capsule.SHA256); err != nil {
		return capture, err
	}
	if err := startableOrigins(ctx); err != nil {
		return capture, err
	}
	bindingRaw, err := canonicalProductionSchemaMaintenanceRecoveryBinding(binding, plan, preSHA, h.runtime.paths)
	if err != nil || h.runtime.now == nil {
		return capture, errors.New("schema recovery start lost its exact binding or native clock")
	}
	intent := productionSchemaMaintenanceRecoveryStartIntent{"lmm-schema-maintenance-recovery-start-intent-v1", plan.TransitionID, preSHA, h.binding.Host, binding.OwnerSHA256, releasedSHA, startupContentSHA256(bindingRaw), origin.Capsule, capture, h.runtime.now().UTC()}
	if err := h.writeRecoveryIntent("recovery-start-intent.json", intent); err != nil {
		return capture, errors.New("schema recovery start already has an intent or could not seal it; inspect without replay")
	}
	if _, err := h.captureRecovery(ctx, plan, preSHA, binding); err != nil {
		return capture, err
	}
	if _, afterSHA, err := h.readRecoveryOwnerReleased(plan, preSHA, binding); err != nil || afterSHA != releasedSHA {
		return capture, errors.New("schema recovery original owner release evidence changed before start")
	}
	if err := h.verifyRecoveryOwnersReleased(ctx, c, origin.Capsule.SHA256); err != nil {
		return capture, err
	}
	if err := h.check(ctx); err != nil {
		return capture, err
	}
	if _, err := h.runtime.runner.Run(ctx, productionCommand{Name: commandSystemctl, Args: []string{"start", c.Service}, Timeout: merchantStoreStartupLimit + 30*time.Second}); err != nil {
		return capture, errors.New("schema recovery start result is uncertain; preserve intent and inspect the actual invocation without replay")
	}
	// The normal per-start holder releases its own exact owner asynchronously.
	// Poll only the actual read-only owner-absence check before full ready proof.
	waitCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	for attempt := 0; attempt < 60; attempt++ {
		if err := h.verifyRecoveryOwnersReleased(waitCtx, c, origin.Capsule.SHA256); err == nil {
			capture, err = h.captureReady(ctx, plan, preSHA, binding)
			if err != nil {
				return capture, err
			}
			if capture.Phase != "RECOVERY_RUNNING" || capture.ConfirmedInvocationID == intent.Stopped.InvocationID {
				return capture, errors.New("schema recovery start did not create a new running invocation under its original closed admission")
			}
			return capture, h.writeCapture("recovery-started.json", capture)
		}
		select {
		case <-waitCtx.Done():
			return capture, errors.New("schema recovery start owner release remains uncertain; preserve intent and reconcile the actual invocation")
		case <-time.After(500 * time.Millisecond):
		}
	}
	return capture, errors.New("schema recovery start owner did not verifiably release; preserve intent and reconcile the actual invocation")
}
