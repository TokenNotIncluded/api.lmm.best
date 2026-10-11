package deploycli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/internal/deploymentfence"
)

// The immutable preplan fixes the two retained providers. A post-DDL binding
// adds the actual catalog and actual new capsule hashes without rewriting it.
func validateSchemaMaintenanceRecoveryCapsule(c productionMerchantStoreCapsule, host productionSchemaMaintenanceOriginPlan, observed productionSchemaMaintenanceRecoveryOrigin, plan productionSchemaMaintenancePlan, binding productionSchemaMaintenanceRecoveryBinding, paths productionPaths) error {
	if err := validateMerchantStoreCapsule(c, paths); err != nil {
		return err
	}
	if c.Format != 1 || c.StartupPolicy != "per-start" || c.DeploymentID != host.Recovery.DeploymentID || c.Host != host.Host || c.ControllerPlanSHA256 != observed.RecoveryPlan.SHA256 || c.Root != filepath.Dir(observed.Capsule.Path) || c.Candidate != host.Recovery.Candidate || c.Rollback != host.Recovery.Rollback || c.Writer.RequiredCapability != plan.RequiredCapability || c.Writer.RequiredCapability > 7 || c.Writer.Role != plan.Role {
		return errors.New("schema maintenance recovery capsule changed its explicit signed N-1 provider pair")
	}
	actual, expected := *c.ExistingSchemaContract, binding.PostSchema
	actual.StartupSHA256, actual.SignedUnitSHA256 = "", ""
	if actual != expected || c.Writer.StartupSHA256 != observed.StartupSHA256 || c.Writer.SignedUnitSHA256 != observed.SignedUnitSHA256 {
		return errors.New("schema maintenance recovery capsule differs from the actual post catalog and host startup binding")
	}
	return nil
}

// Only the identical copy of the controller's new ordinary plan is staged.
// Its original controller path is retained in the bytes, not mistaken for a
// new target-side release authority or fed into the ordinary plan loader.
func (h *productionSchemaMaintenanceHost) loadRecoveryReleasePlan(host productionSchemaMaintenanceOriginPlan, origin productionSchemaMaintenanceRecoveryOrigin, c productionMerchantStoreCapsule) error {
	raw, err := h.runtime.readExistingSchemaSealedFile(filepath.Join(h.binding.Workspace, "state", "recovery-release-plan.json"), true)
	if err != nil || len(raw) > 2<<20 || startupContentSHA256(raw) != origin.RecoveryPlan.SHA256 {
		return errors.New("schema maintenance exact recovery release plan copy is unavailable")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var plan productionReleasePlan
	if decoder.Decode(&plan) != nil || decoder.Decode(&struct{}{}) != io.EOF || validateProductionReleasePlan(plan) != nil {
		return errors.New("schema maintenance recovery release plan is invalid")
	}
	canonical, err := canonicalProductionReleasePlan(plan)
	if err != nil || !bytes.Equal(canonical, raw) || plan.DeploymentID != host.Recovery.DeploymentID || plan.ControllerWorkspace != host.Recovery.ControllerWorkspace || filepath.Join(plan.ControllerWorkspace, productionReleasePlanFilename) != host.Recovery.PlanPath || plan.SchemaMode != productionSchemaModeVerifyExisting || plan.ExistingSchemaContract == nil || plan.MerchantStoreWriter == nil || !plan.GoChanged || plan.MaintenanceHandoff != nil || merchantStoreCapsuleProjection(plan.GoCandidate, "candidate") != c.Candidate || merchantStoreCapsuleProjection(plan.GoRollback, "rollback") != c.Rollback {
		return errors.New("schema maintenance recovery capsule is not the projection of its exact new ordinary no-DDL plan")
	}
	schema, expected := *plan.ExistingSchemaContract, *c.ExistingSchemaContract
	schema.StartupSHA256, expected.StartupSHA256 = "", ""
	if schema != expected || plan.MerchantStoreWriter.RequiredCapability != c.Writer.RequiredCapability || plan.MerchantStoreWriter.Candidate != c.Writer.Candidate || plan.MerchantStoreWriter.Rollback != c.Writer.Rollback {
		return errors.New("schema maintenance recovery release plan does not bind the qualified post catalog and writers")
	}
	return nil
}

func (h *productionSchemaMaintenanceHost) recoveryCapsule(ctx context.Context, plan productionSchemaMaintenancePlan, preSHA string, binding productionSchemaMaintenanceRecoveryBinding, readOnlyFinished bool) (productionMerchantStoreCapsule, productionSchemaMaintenanceRecoveryOrigin, error) {
	var c productionMerchantStoreCapsule
	var origin productionSchemaMaintenanceRecoveryOrigin
	if h == nil || h.runtime == nil || h.binding.TransitionID != plan.TransitionID || h.binding.PlanSHA256 != preSHA || validateProductionSchemaMaintenanceRecoveryBinding(binding, plan, preSHA, h.runtime.paths) != nil {
		return c, origin, errors.New("schema maintenance recovery lacks its exact immutable pre/post authority")
	}
	if readOnlyFinished {
		if _, err := h.checkRecoveryReadyAuthority(ctx, plan, preSHA, binding); err != nil {
			return c, origin, err
		}
	} else if err := h.check(ctx); err != nil {
		return c, origin, err
	}
	var host productionSchemaMaintenanceOriginPlan
	for i, o := range plan.Origins {
		if o.Host == h.binding.Host {
			host = o
			origin = binding.Origins[i]
		}
	}
	if host.Host == "" || host.OldCapsule.Path != h.binding.CapsulePath || host.OldCapsule.SHA256 != h.binding.CapsuleSHA256 {
		return c, origin, errors.New("schema maintenance recovery changed the original local retained capsule")
	}
	for _, ref := range []productionReleaseFilePlan{binding.WorkerResult, binding.Outcome} {
		raw, err := h.runtime.readExistingSchemaSealedFile(ref.Path, true)
		if err != nil || len(raw) > 8<<20 || startupContentSHA256(raw) != ref.SHA256 {
			return c, origin, errors.New("schema maintenance recovery execution evidence changed or disappeared")
		}
	}
	owner, _, err := deploymentfence.ReadSchemaMaintenanceOwner(filepath.Join(h.binding.Workspace, "state", "schema-owner.json"), binding.OwnerSHA256)
	if err != nil || owner.DeploymentID != plan.TransitionID || owner.PlanSHA256 != preSHA || owner.Host != plan.PrimaryHost || owner.ProviderSHA256 != plan.Worker.Provider.PayloadSHA256 || owner.SystemIdentifier != plan.PreSchema.SystemIdentifier || owner.Database != plan.PreSchema.Database || owner.DatabaseOID != plan.PreSchema.DatabaseOID || owner.Schema != plan.PreSchema.Schema || owner.SchemaOID != plan.PreSchema.SchemaOID || owner.Role != plan.Role {
		return c, origin, errors.New("schema maintenance recovery original native owner differs from its execution binding")
	}
	if _, err := h.runtime.readSchemaMaintenanceWorkerResult(h.binding.Workspace, owner, binding.OwnerSHA256); err != nil {
		return c, origin, err
	}
	c, err = h.runtime.loadMerchantStoreCapsule(origin.Capsule.Path, origin.Capsule.SHA256)
	if err != nil {
		return c, origin, err
	}
	if err := validateSchemaMaintenanceRecoveryCapsule(c, host, origin, plan, binding, h.runtime.paths); err != nil {
		return c, origin, err
	}
	if err := h.loadRecoveryReleasePlan(host, origin, c); err != nil {
		return c, origin, err
	}
	return c, origin, nil
}

// captureRecovery deliberately verifies the new loaded hook. Calling the old
// captureStopped after replacing that hook would reject the intended change;
// silently weakening its old-startup check would lose the original evidence.
func (h *productionSchemaMaintenanceHost) captureRecovery(ctx context.Context, plan productionSchemaMaintenancePlan, preSHA string, binding productionSchemaMaintenanceRecoveryBinding) (productionSchemaMaintenanceHostCapture, error) {
	capture, err := h.readCapture("writer-stopped.json")
	if err != nil {
		return capture, err
	}
	c, origin, err := h.recoveryCapsule(ctx, plan, preSHA, binding, false)
	if err != nil {
		return capture, err
	}
	closeIntent, err := h.readCapture("close-intent.json")
	if err != nil || closeIntent.Phase != "CLOSE_INTENT" || capture.Phase != "WRITER_STOPPED" || !sameSchemaMaintenanceWriterCapture(closeIntent, capture) {
		return capture, errors.New("schema maintenance recovery lacks its original sealed closed/stopped generation")
	}
	stopIntent, err := h.readCapture("stop-intent.json")
	if err != nil || stopIntent.Phase != "STOP_INTENT" || !sameSchemaMaintenanceWriterCapture(stopIntent, capture) || !stopIntent.StopStartedUTC.Equal(capture.StopStartedUTC) {
		return capture, errors.New("schema maintenance recovery changed its original shutdown intent")
	}
	journal, err := h.shutdownJournal(ctx, capture)
	if err != nil || startupContentSHA256(journal) != capture.ShutdownJournalSHA256 || validateSchemaMaintenanceShutdown(journal) != nil {
		return capture, errors.New("schema maintenance recovery original refund/flush journal is unavailable")
	}
	state, err := h.runtime.billingUnitState(ctx, h.runtime.paths.Service)
	if err != nil || state["InvocationID"] != capture.InvocationID || cleanBillingUnitExit(state, capture.PID) != nil || capture.ProviderSHA256 != c.Candidate.PayloadSHA256 || h.runtime.verifyMerchantStorePortableInstalled(c, c.Candidate.PayloadSHA256) != nil {
		return capture, errors.New("schema maintenance recovery no longer has the original stopped retained provider")
	}
	if err := h.captureAdmission(ctx, &capture); err != nil {
		return capture, err
	}
	if err := h.runtime.qualifyMerchantStoreCapsule(ctx, c, origin.Capsule.SHA256, "", true); err != nil {
		return capture, err
	}
	after, err := h.runtime.billingUnitState(ctx, h.runtime.paths.Service)
	if err != nil || after["InvocationID"] != capture.InvocationID || cleanBillingUnitExit(after, capture.PID) != nil {
		return capture, errors.New("schema maintenance writer changed during recovery qualification")
	}
	capture.Phase = "RECOVERY_READY"
	capture.ConfirmedCapsuleSHA256 = origin.Capsule.SHA256
	capture.ConfirmedProviderSHA256 = c.Candidate.PayloadSHA256
	return capture, h.check(ctx)
}

// This is schema transition completion on the retained provider. A later
// ordinary latest-release transaction has its own independent deployment proof.
func (h *productionSchemaMaintenanceHost) captureReady(ctx context.Context, plan productionSchemaMaintenancePlan, preSHA string, binding productionSchemaMaintenanceRecoveryBinding) (productionSchemaMaintenanceHostCapture, error) {
	capture, err := h.readCapture("close-intent.json")
	if err != nil {
		return capture, err
	}
	c, origin, err := h.recoveryCapsule(ctx, plan, preSHA, binding, true)
	if err != nil {
		return capture, err
	}
	before, err := h.runtime.billingUnitState(ctx, c.Service)
	if err != nil {
		return capture, err
	}
	if err := h.captureRecoveryRunningAdmission(ctx, &capture, c, origin.Capsule); err != nil {
		return capture, err
	}
	if err := h.runtime.qualifyMerchantStoreCapsule(ctx, c, origin.Capsule.SHA256, "", true); err != nil {
		return capture, err
	}
	if err := h.verifyRecoveryOwnersReleased(ctx, c, origin.Capsule.SHA256); err != nil {
		return capture, err
	}
	if err := h.runtime.verifyPortableRunningWriter(ctx, c, origin.Capsule.SHA256, "", false); err != nil {
		return capture, err
	}
	if err := h.verifyRecoveryOwnersReleased(ctx, c, origin.Capsule.SHA256); err != nil {
		return capture, err
	}
	state, err := h.runtime.billingUnitState(ctx, c.Service)
	pid, parseErr := strconv.Atoi(state["MainPID"])
	if err != nil || parseErr != nil || pid <= 1 || !existingSchemaInvocationPattern.MatchString(state["InvocationID"]) || state["MainPID"] != before["MainPID"] || state["ExecMainPID"] != state["MainPID"] || state["InvocationID"] != before["InvocationID"] || state["ActiveState"] != "active" || state["SubState"] != "running" || sha256MustEqual(filepath.Join("/proc", strconv.Itoa(pid), "exe"), c.Candidate.PayloadSHA256) != nil {
		return capture, errors.New("schema maintenance retained running generation is unavailable")
	}
	if err := h.captureRecoveryRunningAdmission(ctx, &capture, c, origin.Capsule); err != nil {
		return capture, err
	}
	if capture.Phase != "ADMISSION_RESTORED" {
		capture.Phase = "RECOVERY_RUNNING"
	}
	capture.ConfirmedCapsuleSHA256 = origin.Capsule.SHA256
	capture.ConfirmedProviderSHA256 = c.Candidate.PayloadSHA256
	capture.ConfirmedPID, capture.ConfirmedInvocationID = pid, state["InvocationID"]
	finished, err := h.checkRecoveryReadyAuthority(ctx, plan, preSHA, binding)
	if err != nil {
		return capture, err
	}
	if finished != nil && (capture.Phase != "ADMISSION_RESTORED" || finished.PID != pid || finished.InvocationID != state["InvocationID"]) {
		return capture, errors.New("schema maintenance finished host changed its actual restored writer generation")
	}
	return capture, nil
}

// A peer may be checked while the first origin is being reopened. Require the
// exact own reopen intent AND actual original bytes/direct-origin readiness;
// the intent by itself is never an admission or process observation.
func (h *productionSchemaMaintenanceHost) captureRecoveryRunningAdmission(ctx context.Context, capture *productionSchemaMaintenanceHostCapture, c productionMerchantStoreCapsule, ref productionReleaseFilePlan) error {
	path := filepath.Join(h.runtime.paths.NginxRoot, "lmm-api-locations.conf")
	raw, err := h.runtime.readExistingSchemaSealedFile(path, false)
	if err != nil {
		return err
	}
	if startupContentSHA256(raw) == capture.ClosedAdmissionSHA256 {
		if err := h.captureAdmission(ctx, capture); err != nil {
			return err
		}
		capture.Phase = "RECOVERY_RUNNING"
		return nil
	}
	original, err := h.runtime.readExistingSchemaSealedFile(filepath.Join(h.binding.Workspace, "state", "admission-original.conf"), true)
	if err != nil || startupContentSHA256(original) != capture.OriginalAdmissionSHA256 || !bytes.Equal(raw, original) {
		return errors.New("schema maintenance running peer has unknown admission bytes")
	}
	expected := struct {
		Format        int    `json:"format"`
		TransitionID  string `json:"transition_id"`
		PlanSHA256    string `json:"plan_sha256"`
		Host          string `json:"host"`
		CapsulePath   string `json:"capsule_path"`
		CapsuleSHA256 string `json:"capsule_sha256"`
	}{1, h.binding.TransitionID, h.binding.PlanSHA256, h.binding.Host, ref.Path, ref.SHA256}
	expectedRaw, _ := json.MarshalIndent(expected, "", "  ")
	intent, err := h.runtime.readExistingSchemaSealedFile(filepath.Join(h.binding.Workspace, "state", "reopen-intent.json"), true)
	if err != nil || !bytes.Equal(intent, append(expectedRaw, '\n')) {
		return errors.New("schema maintenance running peer lacks its exact own reopen intent")
	}
	before, err := h.runtime.billingUnitState(ctx, "nginx.service")
	pid, parseErr := strconv.Atoi(before["MainPID"])
	if err != nil || parseErr != nil || pid <= 1 || before["ActiveState"] != "active" || before["SubState"] != "running" || !existingSchemaInvocationPattern.MatchString(before["InvocationID"]) {
		return errors.New("schema maintenance reopened nginx generation unavailable")
	}
	childrenPath := filepath.Join("/proc", strconv.Itoa(pid), "task", strconv.Itoa(pid), "children")
	children, err := os.ReadFile(childrenPath)
	if err != nil || len(children) > 16384 || len(bytes.Fields(children)) == 0 {
		return errors.New("schema maintenance reopened nginx workers unavailable")
	}
	if err := h.probeRestoredAdmission(ctx, c); err != nil {
		return err
	}
	after, err := h.runtime.billingUnitState(ctx, "nginx.service")
	afterChildren, childErr := os.ReadFile(childrenPath)
	afterRaw, fileErr := h.runtime.readExistingSchemaSealedFile(path, false)
	if err != nil || childErr != nil || fileErr != nil || before["MainPID"] != after["MainPID"] || before["InvocationID"] != after["InvocationID"] || after["ActiveState"] != "active" || after["SubState"] != "running" || !bytes.Equal(children, afterChildren) || !bytes.Equal(original, afterRaw) {
		return errors.New("schema maintenance reopened admission changed during observation")
	}
	capture.NginxPID, capture.NginxInvocationID, capture.NginxGenerationSHA256 = pid, before["InvocationID"], startupContentSHA256(children)
	capture.Phase = "ADMISSION_RESTORED"
	return nil
}

// This receipt enables only fresh, read-only captureReady observations after
// native finish released this host's three locks. Every mutating operation and
// captureRecovery continues to require the original live guardian lease.
type productionSchemaMaintenanceHostFinished struct {
	Format                string    `json:"format"`
	TransitionID          string    `json:"transition_id"`
	PlanSHA256            string    `json:"plan_sha256"`
	Host                  string    `json:"host"`
	GuardianBindingSHA256 string    `json:"guardian_binding_sha256"`
	RecoveryBindingSHA256 string    `json:"recovery_binding_sha256"`
	CapsuleSHA256         string    `json:"capsule_sha256"`
	ProviderSHA256        string    `json:"provider_sha256"`
	PID                   int       `json:"pid"`
	InvocationID          string    `json:"invocation_id"`
	FinishedUTC           time.Time `json:"finished_utc"`
}

func (h *productionSchemaMaintenanceHost) checkRecoveryReadyAuthority(ctx context.Context, plan productionSchemaMaintenancePlan, preSHA string, binding productionSchemaMaintenanceRecoveryBinding) (*productionSchemaMaintenanceHostFinished, error) {
	if err := h.check(ctx); err == nil {
		return nil, nil
	}
	if h == nil || h.runtime == nil || h.validate == nil {
		return nil, errors.New("schema maintenance finished observation has no original native authority")
	}
	if err := h.validate(ctx); err != nil {
		return nil, err
	}
	guardian, err := h.runtime.readExistingSchemaSealedFile(h.binding.GuardianBindingPath, true)
	if err != nil || startupContentSHA256(guardian) != h.binding.GuardianBindingSHA256 {
		return nil, errors.New("schema maintenance finished observation changed its original guardian binding")
	}
	bindingRaw, err := canonicalProductionSchemaMaintenanceRecoveryBinding(binding, plan, preSHA, h.runtime.paths)
	if err != nil {
		return nil, err
	}
	actualBinding, err := h.runtime.readExistingSchemaSealedFile(filepath.Join(h.binding.Workspace, "state", "recovery-binding.json"), true)
	if err != nil || !bytes.Equal(actualBinding, bindingRaw) {
		return nil, errors.New("schema maintenance finished observation changed its original recovery binding")
	}
	raw, err := h.runtime.readExistingSchemaSealedFile(filepath.Join(h.binding.Workspace, "state", "host-finished.json"), true)
	if err != nil || len(raw) > 16384 {
		return nil, errors.New("schema maintenance host has neither a live lease nor exact native finish receipt")
	}
	var finished productionSchemaMaintenanceHostFinished
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&finished) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return nil, errors.New("schema maintenance finish receipt is malformed")
	}
	canonical, err := json.MarshalIndent(finished, "", "  ")
	var origin productionSchemaMaintenanceRecoveryOrigin
	var provider string
	for i, o := range plan.Origins {
		if o.Host == h.binding.Host {
			origin = binding.Origins[i]
			provider = o.Recovery.Candidate.PayloadSHA256
		}
	}
	if err != nil || !bytes.Equal(append(canonical, '\n'), raw) || finished.Format != "lmm-schema-maintenance-host-finished-v1" || finished.TransitionID != h.binding.TransitionID || finished.PlanSHA256 != h.binding.PlanSHA256 || finished.PlanSHA256 != preSHA || finished.Host != h.binding.Host || finished.GuardianBindingSHA256 != h.binding.GuardianBindingSHA256 || finished.RecoveryBindingSHA256 != startupContentSHA256(bindingRaw) || finished.CapsuleSHA256 != origin.Capsule.SHA256 || finished.ProviderSHA256 != provider || provider == "" || finished.PID <= 1 || !existingSchemaInvocationPattern.MatchString(finished.InvocationID) || finished.FinishedUTC.IsZero() {
		return nil, errors.New("schema maintenance finish receipt changed its exact guardian/recovery/provider authority")
	}
	restored, err := h.readCapture("admission-restored.json")
	if err != nil || restored.Phase != "ADMISSION_RESTORED" || restored.ConfirmedCapsuleSHA256 != finished.CapsuleSHA256 || restored.ConfirmedProviderSHA256 != finished.ProviderSHA256 || restored.ConfirmedPID != finished.PID || restored.ConfirmedInvocationID != finished.InvocationID {
		return nil, errors.New("schema maintenance finish receipt lacks its native restored generation")
	}
	return &finished, nil
}

func (h *productionSchemaMaintenanceHost) verifyRecoveryOwnersReleased(ctx context.Context, c productionMerchantStoreCapsule, digest string) error {
	child, err := h.runtime.merchantStoreCapsuleEnvironment(ctx, c, digest, "")
	if err != nil {
		return err
	}
	values := map[string]string{}
	for _, row := range child {
		key, value, ok := strings.Cut(row, "=")
		if !ok {
			return errors.New("schema maintenance recovery child environment is malformed")
		}
		if _, dup := values[key]; dup {
			return errors.New("schema maintenance recovery child environment is duplicated")
		}
		values[key] = value
	}
	dsn, env, err := productionSealedDatabaseCommand(values)
	if err != nil {
		return err
	}
	query := `SELECT EXISTS(SELECT 1 FROM "` + c.Writer.Schema + `".options WHERE ` + deploymentfence.PostgreSQLPresencePredicate + `)`
	out, err := h.runtime.runner.Run(ctx, productionCommand{Name: commandPSQL, Args: []string{"-X", "-q", "-v", "ON_ERROR_STOP=1", "--no-align", "--tuples-only", "--command", query, "--dbname", dsn}, Env: env, Sensitive: true, Timeout: 20 * time.Second, OutputLimit: 4096})
	if err != nil || string(out) != "f\n" {
		return errors.New("schema maintenance recovery still has reserved deployment or startup ownership")
	}
	return nil
}
