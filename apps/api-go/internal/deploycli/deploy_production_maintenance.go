package deploycli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"os"
	"os/user"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
)

func (runtime *productionRuntime) maintenanceUndispatchedStatus(ctx context.Context, workspace productionWorkspace, options productionTransactionOptions) (productionStatus, error) {
	if runtime.guardianLease == nil || options.StagedPlanPath != filepath.Join(workspace.stagingDir, productionReleasePlanFilename) || !productionSHA256Pattern.MatchString(options.StagedPlanSHA256) {
		return productionStatus{}, errors.New("absent maintenance status requires the exact staged plan and guardian lease")
	}
	if err := runtime.validateMaintenanceStatusTransaction(workspace); err != nil {
		return productionStatus{}, err
	}
	evidence, err := runtime.productionDispatchEvidence(ctx, workspace.root, productionActivationUnit(workspace.id))
	if err != nil {
		return productionStatus{}, err
	}
	if productionDispatchHasEvidence(evidence) {
		return productionStatus{}, errors.New("maintenance activation has evidence but no readable owner status; owner repair is required")
	}
	entries, err := os.ReadDir(workspace.stateDir)
	if err != nil {
		return productionStatus{}, err
	}
	for _, entry := range entries {
		if entry.Name() != "maintenance-transfer.json" {
			return productionStatus{}, errors.New("undispatched workspace retains owner mutation or failed-attempt evidence")
		}
		if runtime.maintenanceHandoff.StoppedWriter == nil {
			return productionStatus{}, errors.New("unstopped staging intent retains a transaction transfer")
		}
		path := filepath.Join(workspace.stateDir, entry.Name())
		if err := runtime.requireOwnedSafePath(path, false); err != nil {
			return productionStatus{}, err
		}
		body, err := readPrivateRegularFile(path, 16<<10)
		if err != nil {
			return productionStatus{}, err
		}
		var transfer struct {
			Format   string `json:"format"`
			Previous string `json:"previous_deployment_id"`
			Next     string `json:"deployment_id"`
			SHA256   string `json:"handoff_sha256"`
		}
		decoder := json.NewDecoder(bytes.NewReader(body))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&transfer) != nil || decoder.Decode(new(any)) != io.EOF || transfer.Format != "lmm-maintenance-transaction-transfer-v1" || transfer.Previous != runtime.maintenanceHandoff.PreviousDeploymentID || transfer.Next != workspace.id || transfer.SHA256 != runtime.maintenanceHandoff.SHA256 {
			return productionStatus{}, errors.New("undispatched workspace transaction transfer differs from its actual stopped handoff")
		}
	}
	content, err := readPrivateRegularFile(options.StagedPlanPath, 2<<20)
	if err != nil {
		return productionStatus{}, err
	}
	digest := sha256.Sum256(content)
	if hex.EncodeToString(digest[:]) != options.StagedPlanSHA256 {
		return productionStatus{}, errors.New("staged maintenance plan changed")
	}
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	var plan productionReleasePlan
	if err := decoder.Decode(&plan); err != nil {
		return productionStatus{}, err
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return productionStatus{}, errors.New("staged maintenance plan has trailing JSON")
	}
	if err := validateProductionReleasePlan(plan); err != nil {
		return productionStatus{}, err
	}
	canonical, err := canonicalProductionReleasePlan(plan)
	if err != nil || !bytes.Equal(canonical, content) {
		return productionStatus{}, errors.New("staged maintenance plan is not canonical")
	}
	h := runtime.maintenanceHandoff
	if plan.DeploymentID != workspace.id || plan.MaintenanceHandoff == nil || plan.MaintenanceHandoff.TransitionID != h.TransitionID || plan.MaintenanceHandoff.TransitionIntentSHA256 != h.TransitionIntentSHA256 || plan.ProbeBinary.SHA256 != h.ProviderSHA256 {
		return productionStatus{}, errors.New("staged maintenance plan differs from the frozen handoff")
	}
	refined := plan.MaintenanceHandoff.SHA256 != h.SHA256
	if refined {
		if err := runtime.verifyStoppedHandoffRefinement(workspace, plan.MaintenanceHandoff, h); err != nil {
			return productionStatus{}, err
		}
	}
	for _, artifact := range []productionReleasePackagePlan{plan.GoCandidate, plan.GoRollback, plan.WebCandidate, plan.WebRollback} {
		if err := runtime.validateStagedFile(workspace, filepath.Join(workspace.stagingDir, filepath.Base(artifact.PackagePath)), artifact.PackageSHA256, "undispatched package"); err != nil {
			return productionStatus{}, err
		}
	}
	if _, err := runtime.validateCandidateEntrypoint(workspace, filepath.Join(workspace.stagingDir, backendGoName), h.ProviderSHA256); err != nil {
		return productionStatus{}, err
	}
	if h.StoppedWriter != nil {
		if err := runtime.validateStoppedMaintenanceWriter(ctx); err != nil {
			return productionStatus{}, err
		}
		if h.Stage == "post" {
			_, previous, err := runtime.maintenancePreviousWorkspace()
			if err != nil {
				return productionStatus{}, err
			}
			if previous.Go.CandidateSHA256 != plan.GoCandidate.PackageSHA256 || previous.ProbeBinarySHA256 != h.ProviderSHA256 {
				return productionStatus{}, errors.New("post staged N-1 is not the actual installed confirmed bridge")
			}
		}
	}
	return productionStatus{Format: productionStatusFormat, DeploymentID: workspace.id, Phase: "NOT_DISPATCHED", PlanSHA256: options.StagedPlanSHA256, HandoffSHA256: h.SHA256, HandoffRefinementVerified: refined, DispatchVerifiedAbsent: true, MaintenanceStage: h.Stage, TransitionID: h.TransitionID, TransitionIntentSHA256: h.TransitionIntentSHA256, ProviderSHA256: h.ProviderSHA256, Version: plan.ExpectedVersion}, nil
}

func (runtime *productionRuntime) verifyStoppedHandoffRefinement(workspace productionWorkspace, base, next *productionMaintenanceHandoff) error {
	if base == nil || next == nil || base.StoppedWriter != nil || next.StoppedWriter == nil || base.Stage != next.Stage || base.TransitionID != next.TransitionID || base.TransitionIntentSHA256 != next.TransitionIntentSHA256 || base.ProviderSHA256 != next.ProviderSHA256 || base.PrepareConfigPath != next.PrepareConfigPath || base.PrepareConfigSHA256 != next.PrepareConfigSHA256 || base.GuardianSocket != next.GuardianSocket || base.DeploymentTool != next.DeploymentTool || base.PublicBaseURL != next.PublicBaseURL || base.ProbeTokenPath != next.ProbeTokenPath || base.ProbeTokenSHA256 != next.ProbeTokenSHA256 || (base.PreviousDeploymentID != "" && base.PreviousDeploymentID != next.PreviousDeploymentID) {
		return errors.New("dynamic handoff is not a stopped-only refinement of the immutable stage intent")
	}
	// Normal stage preserves the original root-owned handoff bytes alongside the
	// package plan. Re-read that base instead of trusting caller-supplied fields.
	path := filepath.Join(workspace.stagingDir, filepath.Base(base.Path))
	loaded, err := loadProductionMaintenanceHandoff(path, base.SHA256, runtime.requiredOwnerUID)
	if err != nil {
		loaded, err = loadProductionMaintenanceHandoff(productionRemoteHandoffPath(*base), base.SHA256, runtime.requiredOwnerUID)
	}
	if err != nil {
		return fmt.Errorf("verify original staged handoff before stopped refinement: %w", err)
	}
	left, right := *loaded, *base
	left.Path = ""
	left.SHA256 = ""
	right.Path = ""
	right.SHA256 = ""
	if !reflect.DeepEqual(left, right) {
		return errors.New("staged base handoff fields differ from the immutable release plan")
	}
	return nil
}

func (runtime *productionRuntime) maintenanceStagingIntent() bool {
	return runtime.maintenancePost() && runtime.maintenanceHandoff.StoppedWriter == nil
}

func (runtime *productionRuntime) refuseUnstoppedPostMutation() error {
	if runtime.maintenanceStagingIntent() {
		return errors.New("post maintenance staging intent cannot activate before an official stopped bridge handoff")
	}
	return nil
}

func (runtime *productionRuntime) maintenanceWorkspaceStagingIntent(workspace productionWorkspace) (bool, error) {
	content, err := readPrivateRegularFile(filepath.Join(workspace.root, productionWorkspaceMarker), 16<<10)
	if err != nil {
		return false, err
	}
	values, err := parseSimpleManifest(content)
	if err != nil {
		return false, err
	}
	return values["deployment_id"] == workspace.id && values["role"] == "staging-intent", nil
}

func (runtime *productionRuntime) validateMaintenanceStatusTransaction(workspace productionWorkspace) error {
	intent, err := runtime.maintenanceWorkspaceStagingIntent(workspace)
	if err != nil || !intent {
		return runtime.validateTransactionLock(workspace)
	}
	if !runtime.maintenancePost() || runtime.guardianLease == nil {
		return errors.New("post staging workspace requires its bound guardian")
	}
	if runtime.maintenanceStopped() {
		previous, _, err := runtime.maintenancePreviousWorkspace()
		if err != nil {
			return err
		}
		if err := runtime.validateTransactionLock(previous); err == nil {
			return nil
		}
		// A dispatched post apply may already own the transaction; missing status
		// remains independently guarded by the absence-of-mutation checks.
		return runtime.validateTransactionLock(workspace)
	}
	return nil // This workspace has never claimed the transaction.
}

func (runtime *productionRuntime) activateMaintenanceStagingIntent(ctx context.Context, workspace productionWorkspace) error {
	intent, err := runtime.maintenanceWorkspaceStagingIntent(workspace)
	if err != nil || !intent {
		return err
	}
	if !runtime.maintenancePost() || !runtime.maintenanceStopped() {
		return errors.New("post staging intent activation requires the stopped bridge")
	}
	if err := runtime.validateTransactionLock(workspace); err == nil {
		return nil
	}
	return runtime.transferMaintenanceTransaction(ctx, workspace.id)
}

const productionMaintenanceHandoffFormat = "lmm-credit-maintenance-handoff-v1"
const productionMaintenanceLockProtocol = "lmm-maintenance-deploy-lock-v1"
const productionMaintenanceConfirmedPhase = "MAINTENANCE_CONFIRMED"
const productionMaintenanceDropIn = "90-credit-transition-prepare.conf"
const productionMaintenanceReaderGroup = "lmm-credit-transition"

func (runtime *productionRuntime) readTerminalMaintenanceStatus(ctx context.Context, workspace productionWorkspace) (productionStatus, bool, error) {
	status, err := runtime.readStatus(workspace)
	if errors.Is(err, os.ErrNotExist) {
		return productionStatus{}, false, nil
	}
	if err != nil {
		return productionStatus{}, false, err
	}
	if status.Phase != "CONFIRMED" || !status.MaintenanceAdmissionReopened {
		return productionStatus{}, false, nil
	}
	h := runtime.maintenanceHandoff
	manifest, err := runtime.readManifest(workspace)
	if err != nil {
		return productionStatus{}, true, err
	}
	if h == nil || h.Stage != "post" || manifest.MaintenanceHandoff == nil || manifest.MaintenanceHandoff.SHA256 != h.SHA256 || status.TransitionID != h.TransitionID || status.TransitionIntentSHA256 != h.TransitionIntentSHA256 || status.ProviderSHA256 != h.ProviderSHA256 {
		return productionStatus{}, true, errors.New("terminal owner status differs from its exact post handoff")
	}
	installed, err := sha256File(runtime.paths.InstalledBinary)
	if err != nil || installed != h.ProviderSHA256 {
		return productionStatus{}, true, errors.New("terminal confirmed provider identity changed")
	}
	if err := runtime.verifyManifestInstalled(ctx, manifest, false); err != nil {
		return productionStatus{}, true, err
	}
	if err := verifyFrontendIdentity(runtime.paths.FrontendRoot, manifest.Frontend.NewTarget, manifest.Frontend.NewIndexSHA256); err != nil {
		return productionStatus{}, true, err
	}
	if _, err := runtime.probeStatus(ctx, runtime.paths.InstalledBinary, runtime.paths.LocalBaseURL, manifest.ExpectedVersion); err != nil {
		return productionStatus{}, true, err
	}
	if err := runtime.probeLive(ctx, runtime.paths.InstalledBinary); err != nil {
		return productionStatus{}, true, err
	}
	return status, true, nil
}

// DynamicUser cannot read a root-only plan. Require an explicitly provisioned
// static reader group instead of changing the runner's sealed file permissions.
func validateMaintenanceServiceReader(path string) error {
	group, err := user.LookupGroup(productionMaintenanceReaderGroup)
	if err != nil {
		return errors.New("prepare service reader group lmm-credit-transition must be provisioned")
	}
	gid, err := strconv.ParseUint(group.Gid, 10, 32)
	if err != nil || gid == 0 {
		return errors.New("prepare reader group must have a non-root gid")
	}
	return validateMaintenanceServiceReaderGID(path, uint32(gid))
}

func validateMaintenanceServiceReaderGID(path string, gid uint32) error {
	return validateMaintenanceServiceReaderGIDToRoot(path, gid, string(filepath.Separator))
}

func validateMaintenanceServiceReaderGIDToRoot(path string, gid uint32, boundary string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	_, fileGID, ok := maintenanceFileIDs(info)
	if !ok || info.Mode().Perm() != 0640 || fileGID != gid || gid == 0 {
		return errors.New("prepare plan must be root: lmm-credit-transition mode 0640 for the service")
	}
	for dir := filepath.Dir(path); ; dir = filepath.Dir(dir) {
		info, err := os.Lstat(dir)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("unsafe prepare reader directory")
		}
		_, directoryGID, ok := maintenanceFileIDs(info)
		if !ok || info.Mode().Perm()&0022 != 0 || (info.Mode().Perm()&0001 == 0 && (directoryGID != gid || info.Mode().Perm()&0010 == 0)) {
			return errors.New("prepare reader group cannot traverse a sealed plan directory")
		}
		if dir == boundary {
			break
		}
	}
	return nil
}

type productionStoppedWriter struct {
	PID                   int    `json:"pid"`
	InvocationID          string `json:"invocation_id"`
	ShutdownJournalPath   string `json:"shutdown_journal_path"`
	ShutdownJournalSHA256 string `json:"shutdown_journal_sha256"`
}

type productionMaintenanceHandoff struct {
	CaptureReceiptPath   string `json:"capture_receipt_path,omitempty"`
	CaptureReceiptSHA256 string `json:"capture_receipt_sha256,omitempty"`

	PublicBaseURL             string                   `json:"public_base_url,omitempty"`
	ProbeTokenPath            string                   `json:"probe_token_path,omitempty"`
	ProbeTokenSHA256          string                   `json:"probe_token_sha256,omitempty"`
	Format                    string                   `json:"format"`
	TransitionID              string                   `json:"transition_id"`
	TransitionIntentSHA256    string                   `json:"transition_intent_sha256"`
	ProviderSHA256            string                   `json:"provider_sha256"`
	PrepareConfigPath         string                   `json:"prepare_config_path"`
	PrepareConfigSHA256       string                   `json:"prepare_config_sha256"`
	GuardianSocket            string                   `json:"guardian_socket"`
	DeploymentTool            string                   `json:"deployment_tool"`
	Stage                     string                   `json:"stage"`
	PreviousDeploymentID      string                   `json:"previous_deployment_id,omitempty"`
	ArchivedEnvironmentPath   string                   `json:"archived_environment_path,omitempty"`
	ArchivedEnvironmentSHA256 string                   `json:"archived_environment_sha256,omitempty"`
	StoppedWriter             *productionStoppedWriter `json:"stopped_writer,omitempty"`
	Path                      string                   `json:"path,omitempty"`
	SHA256                    string                   `json:"sha256,omitempty"`
}

type productionMaintenancePrepareConfig struct {
	Format                 string            `json:"format"`
	TransitionID           string            `json:"transition_id"`
	TransitionIntentSHA256 string            `json:"transition_intent_sha256"`
	ProviderSHA256         string            `json:"provider_sha256"`
	TargetCreditsPerUSD    int               `json:"target_credits_per_usd"`
	Database               map[string]any    `json:"database"`
	Options                map[string]string `json:"options"`
}

func readMaintenanceBoundFile(path, expected string, owner uint32) ([]byte, error) {
	clean, err := cleanAbsoluteNonRoot(path)
	if err != nil || clean != path || !productionSHA256Pattern.MatchString(expected) {
		return nil, errors.New("invalid maintenance bound file path or digest")
	}
	for current := path; ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err != nil {
			return nil, err
		}
		uid, links, ok := deploymentFileOwnership(info)
		if !ok || uid != owner && uid != 0 || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o022 != 0 {
			return nil, errors.New("maintenance bound file ownership or ancestor permissions are unsafe")
		}
		if current == path && (uid != owner || !info.Mode().IsRegular() || links != 1 || (info.Mode().Perm() != 0o600 && info.Mode().Perm() != 0o640) || info.Size() > 1<<20) {
			return nil, errors.New("maintenance bound file must be private, regular and single-linked")
		}
		if current == string(filepath.Separator) {
			break
		}
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if fmt.Sprintf("%x", sha256Bytes(content)) != expected {
		return nil, errors.New("maintenance bound file SHA-256 mismatch")
	}
	return content, nil
}

func loadProductionMaintenanceHandoff(path, digest string, owner uint32) (*productionMaintenanceHandoff, error) {
	if path == "" && digest == "" {
		return nil, nil
	}
	content, err := readMaintenanceBoundFile(path, digest, owner)
	if err != nil {
		return nil, err
	}
	var handoff productionMaintenanceHandoff
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&handoff); err != nil {
		return nil, err
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return nil, errors.New("maintenance handoff contains trailing JSON")
	}
	if handoff.Format != productionMaintenanceHandoffFormat || !productionIDPattern.MatchString(handoff.TransitionID) || (handoff.Stage != "prebridge" && handoff.Stage != "post") || handoff.DeploymentTool != "native" && handoff.DeploymentTool != "systemd" {
		return nil, errors.New("unsupported maintenance handoff")
	}
	for _, d := range []string{handoff.TransitionIntentSHA256, handoff.ProviderSHA256, handoff.PrepareConfigSHA256} {
		if !productionSHA256Pattern.MatchString(d) {
			return nil, errors.New("invalid maintenance handoff identity digest")
		}
	}
	if clean, err := cleanAbsoluteNonRoot(handoff.GuardianSocket); err != nil || clean != handoff.GuardianSocket {
		return nil, errors.New("invalid guardian socket path")
	}
	prepared, err := readMaintenanceBoundFile(handoff.PrepareConfigPath, handoff.PrepareConfigSHA256, owner)
	if err != nil {
		return nil, fmt.Errorf("maintenance prepare binding: %w", err)
	}
	var config productionMaintenancePrepareConfig
	if err := json.Unmarshal(prepared, &config); err != nil {
		return nil, err
	}
	if config.Format != "lmm-credit-transition-prepare-v1" || config.TransitionID != handoff.TransitionID || config.TransitionIntentSHA256 != handoff.TransitionIntentSHA256 || config.ProviderSHA256 != handoff.ProviderSHA256 || config.TargetCreditsPerUSD != 500000 || len(config.Database) != 6 || len(config.Options) != 5 {
		return nil, errors.New("maintenance prepare identity differs from the handoff")
	}
	keys := []string{"CreditsPerUSD", "LegacyPricingQuotaPerUnit", "QuotaPerUnit", "PublicCreditsPerUSD", "USDExchangeRate"}
	for _, key := range keys {
		number, ok := new(big.Rat).SetString(config.Options[key])
		if !ok || number.Sign() <= 0 {
			return nil, errors.New("invalid exact maintenance option snapshot")
		}
	}
	if handoff.StoppedWriter != nil {
		if handoff.StoppedWriter == nil || handoff.StoppedWriter.PID <= 1 || len(handoff.StoppedWriter.InvocationID) != 32 || !productionIDPattern.MatchString(handoff.PreviousDeploymentID) {
			return nil, errors.New("post maintenance handoff requires bound stopped writer and previous deployment")
		}
		if _, err := readMaintenanceBoundFile(handoff.ArchivedEnvironmentPath, handoff.ArchivedEnvironmentSHA256, owner); err != nil {
			return nil, fmt.Errorf("stopped writer environment: %w", err)
		}
		journal, err := readMaintenanceBoundFile(handoff.StoppedWriter.ShutdownJournalPath, handoff.StoppedWriter.ShutdownJournalSHA256, owner)
		if err != nil {
			return nil, err
		}
		if handoff.Stage == "post" {
			if err := validateMaintenanceShutdownJournal(journal); err != nil {
				return nil, err
			}
		} else if err := validateBillingShutdownJournal(journal); err != nil {
			return nil, err
		}
	}
	if handoff.Stage == "post" && handoff.StoppedWriter == nil && (handoff.PreviousDeploymentID != "" || handoff.CaptureReceiptPath != "" || handoff.CaptureReceiptSHA256 != "" || handoff.ArchivedEnvironmentPath != "" || handoff.ArchivedEnvironmentSHA256 != "") {
		return nil, errors.New("unstopped post staging intent must not claim previous writer evidence")
	}
	if handoff.StoppedWriter != nil {
		receipt, err := readMaintenanceBoundFile(handoff.CaptureReceiptPath, handoff.CaptureReceiptSHA256, owner)
		if err != nil {
			return nil, err
		}
		var captured productionMaintenanceCapture
		if json.Unmarshal(receipt, &captured) != nil || captured.Format != "lmm-credit-maintenance-capture-v1" || captured.Phase != "FROZEN" || captured.TransitionID != handoff.TransitionID || captured.TransitionIntentSHA256 != handoff.TransitionIntentSHA256 || captured.PID != handoff.StoppedWriter.PID || captured.InvocationID != handoff.StoppedWriter.InvocationID || captured.ArchivedEnvironmentPath != handoff.ArchivedEnvironmentPath || captured.ArchivedEnvironmentSHA256 != handoff.ArchivedEnvironmentSHA256 || captured.ShutdownJournalPath != handoff.StoppedWriter.ShutdownJournalPath || captured.ShutdownJournalSHA256 != handoff.StoppedWriter.ShutdownJournalSHA256 || handoff.Stage == "post" && (!captured.WasMaintenanceConfirmed || captured.ProviderSHA256 != handoff.ProviderSHA256) {
			return nil, errors.New("stopped handoff differs from normal owner frozen capture receipt")
		}
	}
	handoff.Path, handoff.SHA256 = path, digest
	return &handoff, nil
}

func validateMaintenanceShutdownJournal(journal []byte) error {
	text := string(journal)
	if !strings.Contains(text, "credit_transition_prepare shutdown_complete=true business_enabled=false") || !strings.Contains(text, "server exited") {
		return errors.New("bound maintenance writer shutdown evidence is incomplete")
	}
	for _, bad := range []string{"shutdown_complete=false", "panic:", "execution_complete=false"} {
		if strings.Contains(text, bad) {
			return errors.New("bound maintenance writer shutdown was not clean")
		}
	}
	return nil
}

func (runtime *productionRuntime) releaseGlobalLock(lock *os.File) {
	if runtime.guardianLease != nil {
		for _, extra := range runtime.guardianExtraLocks {
			_ = extra.Close()
		}
		runtime.guardianExtraLocks = nil
		_ = lock.Close()
		_ = runtime.guardianLease.Close()
		runtime.guardianLease = nil
		return
	}
	_ = unlockDeploymentFile(lock)
	_ = lock.Close()
}

func (runtime *productionRuntime) setMaintenanceHandoff(path, digest string) error {
	handoff, err := loadProductionMaintenanceHandoff(path, digest, uint32(runtime.effectiveUID()))
	if err != nil {
		return err
	}
	if handoff != nil && handoff.DeploymentTool != "native" {
		return errors.New("maintenance handoff belongs to a different deployment tool")
	}
	runtime.maintenanceHandoff = handoff
	return nil
}

func (runtime *productionRuntime) maintenancePost() bool {
	return runtime.maintenanceHandoff != nil && runtime.maintenanceHandoff.Stage == "post"
}

func (runtime *productionRuntime) validateStoppedMaintenanceWriter(ctx context.Context) error {
	if !runtime.maintenanceStopped() {
		return errors.New("stopped writer adoption requires a sealed maintenance binding")
	}
	state, err := runtime.billingUnitState(ctx, runtime.paths.Service)
	if err != nil {
		return err
	}
	h := runtime.maintenanceHandoff
	if state["InvocationID"] != h.StoppedWriter.InvocationID {
		return errors.New("stopped writer invocation changed")
	}
	return cleanBillingUnitExit(state, h.StoppedWriter.PID)
}

func (runtime *productionRuntime) maintenanceEnvironment() ([]byte, error) {
	h := runtime.maintenanceHandoff
	return readMaintenanceBoundFile(h.ArchivedEnvironmentPath, h.ArchivedEnvironmentSHA256, uint32(runtime.effectiveUID()))
}

func (runtime *productionRuntime) verifyMaintenanceDatabase(ctx context.Context, environment []byte, post bool) error {
	h := runtime.maintenanceHandoff
	body, err := readMaintenanceBoundFile(h.PrepareConfigPath, h.PrepareConfigSHA256, uint32(runtime.effectiveUID()))
	if err != nil {
		return err
	}
	var prepared productionMaintenancePrepareConfig
	if err := json.Unmarshal(body, &prepared); err != nil {
		return err
	}
	values, err := parseProductionEnvironment(environment)
	if err != nil {
		return err
	}
	databaseURL, child, err := productionDatabaseCommand(values)
	if err != nil {
		return err
	}
	query := `SELECT json_build_object('database',json_build_object('system_identifier',(SELECT system_identifier::text FROM pg_control_system()),'database',current_database(),'database_oid',(SELECT oid::bigint FROM pg_database WHERE datname=current_database()),'schema',current_schema(),'server_version_num',current_setting('server_version_num')::int,'database_user',current_user),'options',(SELECT json_object_agg(key,value) FROM options WHERE key IN ('CreditsPerUSD','LegacyPricingQuotaPerUnit','QuotaPerUnit','PublicCreditsPerUSD','USDExchangeRate')))`
	output, err := runtime.runner.Run(ctx, productionCommand{Name: commandPSQL, Args: []string{"-X", "-v", "ON_ERROR_STOP=1", "--no-align", "--tuples-only", "--command", query, databaseURL}, Env: child, Sensitive: true})
	if err != nil {
		return errors.New("maintenance database identity/options verification failed")
	}
	var current struct {
		Database map[string]any    `json:"database"`
		Options  map[string]string `json:"options"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(output), &current); err != nil || !reflect.DeepEqual(current.Database, prepared.Database) || len(current.Options) != 5 {
		return errors.New("maintenance database identity or exact option count changed")
	}
	for key, value := range prepared.Options {
		expected := value
		if post && key != "USDExchangeRate" {
			expected = "500000"
		}
		if current.Options[key] != expected {
			return fmt.Errorf("maintenance database option changed: %s", key)
		}
	}
	return nil
}

func (runtime *productionRuntime) configureMaintenanceService(workspace productionWorkspace, manifest productionManifest, rollback bool) error {
	if err := runtime.refuseUnstoppedPostMutation(); err != nil {
		return err
	}
	if manifest.MaintenanceHandoff == nil {
		return nil
	}
	h := manifest.MaintenanceHandoff
	path := filepath.Join(runtime.paths.DropInDir, productionMaintenanceDropIn)
	if h.Stage == "prebridge" && !rollback {
		if err := validateMaintenanceServiceReader(h.PrepareConfigPath); err != nil {
			return err
		}
		if err := ensureRealDirectory(runtime.paths.DropInDir, 0o755); err != nil {
			return err
		}
		// Paths are separately restricted before using them in a systemd assignment.
		for _, value := range []string{h.PrepareConfigPath, h.PrepareConfigSHA256} {
			if strings.ContainsAny(value, "\r\n\"%\\ ") {
				return errors.New("maintenance systemd binding contains unsafe syntax")
			}
		}
		return writeAtomicRegularFile(path, []byte("[Service]\nSupplementaryGroups="+productionMaintenanceReaderGroup+"\nEnvironment=LMM_CREDIT_TRANSITION_PLAN="+h.PrepareConfigPath+"\nEnvironment=LMM_CREDIT_TRANSITION_SHA256="+h.PrepareConfigSHA256+"\n"), 0o644)
	}
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("maintenance service drop-in is unsafe")
		}
		content, err := os.ReadFile(path)
		if err != nil || !strings.Contains(string(content), "LMM_CREDIT_TRANSITION_SHA256="+h.PrepareConfigSHA256+"\n") {
			return errors.New("maintenance service drop-in does not match frozen prepare identity")
		}
		return os.Remove(path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

type productionMaintenanceHealthResponse struct {
	Success         bool  `json:"success"`
	Ready           bool  `json:"ready"`
	Live            bool  `json:"live"`
	Maintenance     bool  `json:"maintenance"`
	BusinessEnabled *bool `json:"business_enabled"`
	Data            struct {
		Version          string `json:"version"`
		CreditTransition struct {
			Format                 string `json:"format"`
			TransitionID           string `json:"transition_id"`
			TransitionIntentSHA256 string `json:"transition_intent_sha256"`
			PrepareConfigSHA256    string `json:"prepare_config_sha256"`
			ProviderSHA256         string `json:"provider_sha256"`
			TargetCreditsPerUSD    int    `json:"target_credits_per_usd"`
		} `json:"credit_transition"`
	} `json:"data"`
}

func (runtime *productionRuntime) probeBoundMaintenanceLocal(ctx context.Context, binary, version string) error {
	h := runtime.maintenanceHandoff
	for _, route := range []string{"/api/status", "/api/livez"} {
		body, err := runtime.runNativeRequest(ctx, binary, runtime.paths.LocalBaseURL, route, "")
		if err != nil {
			return err
		}
		var response productionMaintenanceHealthResponse
		if err := json.Unmarshal(body, &response); err != nil {
			return err
		}
		binding := response.Data.CreditTransition
		if !response.Success || !response.Maintenance || response.BusinessEnabled == nil || *response.BusinessEnabled || response.Data.Version != version || binding.Format != "lmm-credit-transition-prepare-v1" || binding.TransitionID != h.TransitionID || binding.TransitionIntentSHA256 != h.TransitionIntentSHA256 || binding.PrepareConfigSHA256 != h.PrepareConfigSHA256 || binding.ProviderSHA256 != h.ProviderSHA256 || binding.TargetCreditsPerUSD != 500000 || route == "/api/status" && !response.Ready || route == "/api/livez" && !response.Live {
			return errors.New("maintenance health response differs from frozen handoff identity")
		}
	}
	return nil
}

func (runtime *productionRuntime) probeMaintenanceAdmission(ctx context.Context, workspace productionWorkspace, binary string) error {
	h := runtime.maintenanceHandoff
	expected := "lmm-credit-transition:" + h.TransitionID
	for _, route := range []string{"/api/status", "/v1/models", "/api/user/self"} {
		statusPath := filepath.Join(workspace.root, "maintenance-probe.status")
		body, err := runVerifiedBinary(ctx, runtime.runner, binary, []string{"request", "--base-url", runtime.paths.PublicBaseURL, "--path", route, "--no-follow", "--timeout", productionProbeTimeout.String(), "--status-file", statusPath}, nil, "", productionProbeTimeout, false)
		status, statusErr := os.ReadFile(statusPath)
		if err != nil || statusErr != nil || strings.TrimSpace(string(status)) != "503" || string(body) != expected {
			return errors.New("public maintenance admission is not the bound 503 barrier")
		}
	}
	return nil
}

func (runtime *productionRuntime) probeMaintenanceRelease(ctx context.Context, workspace productionWorkspace, binary, version string) error {
	h := runtime.maintenanceHandoff
	if h.Stage == "prebridge" && !runtime.billingRollback {
		if err := runtime.probeBoundMaintenanceLocal(ctx, binary, version); err != nil {
			return err
		}
	} else {
		if err := runtime.probeBackendLocalWithBinary(ctx, binary, version); err != nil {
			return err
		}
		// Strict business verification stays local while global ingress remains shut.
		if err := runtime.probeModelsAt(ctx, binary, runtime.paths.LocalBaseURL, workspace.probeToken); err != nil {
			return err
		}
	}
	return runtime.probeMaintenanceAdmission(ctx, workspace, binary)
}

func (runtime *productionRuntime) maintenanceRelease(ctx context.Context, workspace productionWorkspace, globalPath, globalSHA string) (productionStatus, error) {
	if err := runtime.refuseUnstoppedPostMutation(); err != nil {
		return productionStatus{}, err
	}
	manifest, err := runtime.readManifest(workspace)
	if err != nil {
		return productionStatus{}, err
	}
	status, err := runtime.readStatus(workspace)
	if err != nil {
		return productionStatus{}, err
	}
	if status.Phase != productionMaintenanceConfirmedPhase || !runtime.maintenancePost() || manifest.MaintenanceHandoff == nil {
		return productionStatus{}, errors.New("maintenance release requires a post maintenance confirmation")
	}
	release, err := readMaintenanceBoundFile(globalPath, globalSHA, uint32(runtime.effectiveUID()))
	if err != nil {
		return productionStatus{}, err
	}
	var receipt struct {
		Format                 string `json:"format"`
		TransitionID           string `json:"transition_id"`
		TransitionIntentSHA256 string `json:"transition_intent_sha256"`
		AllNodesConfirmed      bool   `json:"all_nodes_confirmed"`
	}
	if json.Unmarshal(release, &receipt) != nil || receipt.Format != "lmm-credit-maintenance-release-v1" || receipt.TransitionID != runtime.maintenanceHandoff.TransitionID || receipt.TransitionIntentSHA256 != runtime.maintenanceHandoff.TransitionIntentSHA256 || !receipt.AllNodesConfirmed {
		return productionStatus{}, errors.New("global owner confirmation receipt does not authorize ingress release")
	}
	if err := runtime.validateTransactionLock(workspace); err != nil {
		return productionStatus{}, err
	}
	if err := runtime.healthCheck(ctx, workspace, manifest); err != nil {
		return productionStatus{}, err
	}
	runtime.maintenanceReleasing = true
	defer func() { runtime.maintenanceReleasing = false }()
	if err := runtime.reopenBillingAdmission(ctx, workspace, &manifest); err != nil {
		return productionStatus{}, err
	}
	// After release, reinstate the ordinary public/local health contract before
	// reporting business confirmation. Any failure recloses ingress for recovery.
	runtime.maintenanceHandoff = nil
	probeErr := runtime.healthCheck(ctx, workspace, manifest)
	runtime.maintenanceHandoff = manifest.MaintenanceHandoff
	if probeErr != nil {
		barrierErr := runtime.closeBillingAdmission(ctx, workspace, &manifest)
		failure := productionStatus{Phase: "ROLLBACK_REQUIRED", Version: manifest.ExpectedVersion, Previous: manifest.OldVersion, Reason: "maintenance-admission-release-failed", Failure: probeErr.Error()}
		writeErr := runtime.writeStatus(workspace, failure)
		return productionStatus{}, errors.Join(probeErr, barrierErr, writeErr)
	}
	confirmed := productionStatus{Phase: "CONFIRMED", Version: manifest.ExpectedVersion, Previous: manifest.OldVersion, Reason: "global-owner-confirmation-and-public-health-gates-passed", MaintenanceAdmissionReopened: true}
	if err := runtime.writeStatus(workspace, confirmed); err != nil {
		return productionStatus{}, err
	}
	if err := runtime.finalizeTransactionFiles(workspace); err != nil {
		return productionStatus{}, err
	}
	return confirmed, nil
}

func (runtime *productionRuntime) maintenancePreviousWorkspace() (productionWorkspace, productionManifest, error) {
	h := runtime.maintenanceHandoff
	previous, err := runtime.openWorkspace(filepath.Join(runtime.paths.WorkRoot, h.PreviousDeploymentID))
	if err != nil {
		return productionWorkspace{}, productionManifest{}, err
	}
	manifest, err := runtime.readManifest(previous)
	if err != nil {
		return previous, manifest, err
	}
	status, err := runtime.readStatus(previous)
	if err != nil {
		return previous, manifest, err
	}
	prior := manifest.MaintenanceHandoff
	expectedPhase := productionMaintenanceConfirmedPhase
	if h.Stage == "prebridge" {
		expectedPhase = "FROZEN"
	}
	if status.Phase != expectedPhase && !(h.Stage == "post" && status.Phase == "FROZEN" && status.MaintenanceConfirmation) || h.Stage == "post" && !status.MaintenanceConfirmation || prior == nil || prior.Stage != "prebridge" || prior.TransitionID != h.TransitionID || prior.TransitionIntentSHA256 != h.TransitionIntentSHA256 || prior.ProviderSHA256 != h.ProviderSHA256 || prior.PrepareConfigSHA256 != h.PrepareConfigSHA256 {
		return previous, manifest, errors.New("maintenance predecessor is not the frozen confirmed bridge")
	}
	return previous, manifest, nil
}

func (runtime *productionRuntime) transferMaintenanceTransaction(ctx context.Context, newID string) error {
	if !runtime.maintenanceStopped() || runtime.guardianLease == nil {
		return errors.New("maintenance transaction transfer requires an adopted guardian lease")
	}
	previous, manifest, err := runtime.maintenancePreviousWorkspace()
	if err != nil {
		return err
	}
	if err := runtime.validateTransactionLock(previous); err != nil {
		return err
	}
	if err := runtime.validateStoppedMaintenanceWriter(ctx); err != nil {
		return err
	}
	environment, err := runtime.maintenanceEnvironment()
	if err != nil {
		return err
	}
	if err := runtime.verifyMaintenanceDatabase(ctx, environment, runtime.maintenancePost()); err != nil {
		return err
	}
	if manifest.Go.CandidateSHA256 == "" {
		return errors.New("maintenance bridge package identity missing")
	}
	transfer := struct {
		Format        string `json:"format"`
		Previous      string `json:"previous_deployment_id"`
		Next          string `json:"deployment_id"`
		HandoffSHA256 string `json:"handoff_sha256"`
	}{"lmm-maintenance-transaction-transfer-v1", previous.id, newID, runtime.maintenanceHandoff.SHA256}
	evidence, _ := json.MarshalIndent(transfer, "", "  ")
	destination := filepath.Join(runtime.paths.WorkRoot, newID, "state", "maintenance-transfer.json")
	if err := writeAtomicRegularFile(destination, append(evidence, '\n'), 0o600); err != nil {
		return err
	}
	marker := []byte("format=1\ndeployment_id=" + newID + "\nstatus=ACTIVE\n")
	return writeAtomicRegularFile(filepath.Join(runtime.paths.TransactionLock, productionTransactionMarker), marker, 0o600)
}

func (runtime *productionRuntime) adoptMaintenanceBarrier(workspace productionWorkspace, manifest *productionManifest) error {
	previous, prior, err := runtime.maintenancePreviousWorkspace()
	if err != nil {
		return err
	}
	if prior.BillingGate == nil || !prior.BillingGate.AdmissionClosed || prior.BillingGate.AdmissionReopened {
		return errors.New("maintenance predecessor lacks its closed ingress barrier")
	}
	gate := *prior.BillingGate
	original, err := readPrivateRegularFile(filepath.Join(previous.root, fmt.Sprintf("billing-locations.%d", gate.Sequence)), 1<<20)
	if err != nil || fmt.Sprintf("%x", sha256Bytes(original)) != gate.OriginalSHA256 {
		return errors.New("maintenance ingress restore evidence changed")
	}
	oldBarrier, err := runtime.billingBarrier(original, previous.id)
	if err != nil {
		return err
	}
	current, err := os.ReadFile(filepath.Join(runtime.paths.NginxRoot, "lmm-api-locations.conf"))
	if err != nil || !bytes.Equal(current, oldBarrier) {
		return errors.New("maintenance predecessor ingress barrier changed")
	}
	if err := writeAtomicRegularFile(filepath.Join(workspace.root, fmt.Sprintf("billing-locations.%d", gate.Sequence)), original, 0o600); err != nil {
		return err
	}
	gate.GoPID = runtime.maintenanceHandoff.StoppedWriter.PID
	gate.GoInvocationID = runtime.maintenanceHandoff.StoppedWriter.InvocationID
	gate.StopVerified = true
	gate.ShutdownJournalSHA256 = runtime.maintenanceHandoff.StoppedWriter.ShutdownJournalSHA256
	manifest.BillingGate = &gate
	return nil
}

func productionRemoteHandoffPath(h productionMaintenanceHandoff) string {
	return filepath.Join(filepath.Dir(defaultProductionPaths().WorkRoot), "handoffs", h.SHA256+".json")
}

func (runtime *productionRuntime) maintenanceStopped() bool {
	return runtime.maintenanceHandoff != nil && runtime.maintenanceHandoff.StoppedWriter != nil
}

type productionMaintenanceCapture struct {
	Format                    string `json:"format"`
	TransitionID              string `json:"transition_id"`
	TransitionIntentSHA256    string `json:"transition_intent_sha256"`
	ProviderSHA256            string `json:"provider_sha256"`
	Version                   string `json:"version"`
	PID                       int    `json:"pid"`
	InvocationID              string `json:"invocation_id"`
	ArchivedEnvironmentPath   string `json:"archived_environment_path"`
	ArchivedEnvironmentSHA256 string `json:"archived_environment_sha256"`
	DatabaseSchema            string `json:"database_schema"`
	FrontendTarget            string `json:"frontend_target"`
	FrontendSHA256            string `json:"frontend_sha256"`
	ProcessEnvironmentPath    string `json:"process_environment_path,omitempty"`
	ProcessEnvironmentSHA256  string `json:"process_environment_sha256,omitempty"`
	WasMaintenanceConfirmed   bool   `json:"was_maintenance_confirmed,omitempty"`
	Phase                     string `json:"phase"`
	ShutdownJournalPath       string `json:"shutdown_journal_path,omitempty"`
	ShutdownJournalSHA256     string `json:"shutdown_journal_sha256,omitempty"`
}

func (runtime *productionRuntime) captureMaintenanceProcessEnvironment(ctx context.Context, workspace productionWorkspace, capture *productionMaintenanceCapture, archived []byte) error {
	reader := runtime.maintenanceProcessEnvironment
	if reader == nil {
		reader = func(pid int) ([]byte, error) {
			return os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "environ"))
		}
	}
	content, err := reader(capture.PID)
	if err != nil || len(content) == 0 || len(content) > 1<<20 {
		return errors.New("captured writer process environment is unavailable")
	}
	values := map[string]string{}
	for _, entry := range bytes.Split(content, []byte{0}) {
		if len(entry) == 0 {
			continue
		}
		key, value, ok := strings.Cut(string(entry), "=")
		if !ok || key == "" {
			return errors.New("invalid captured process environment")
		}
		if _, exists := values[key]; exists {
			return errors.New("duplicate captured process environment key")
		}
		values[key] = value
	}
	configuration, err := parseProductionEnvironment(archived)
	if err != nil {
		return err
	}
	configuredDSN, err := productionDatabaseURL(configuration)
	if err != nil {
		return err
	}
	processDSN, err := productionDatabaseURL(values)
	if err != nil || processDSN != configuredDSN {
		return errors.New("captured process database differs from archived configuration")
	}
	hasher := runtime.billingExecutableSHA256
	if hasher == nil {
		hasher = func(pid int) (string, error) { return sha256File(filepath.Join("/proc", strconv.Itoa(pid), "exe")) }
	}
	executableSHA, err := hasher(capture.PID)
	if err != nil || executableSHA != capture.ProviderSHA256 {
		return errors.New("captured writer executable differs from the installed provider")
	}
	unit, err := runtime.billingUnitState(ctx, runtime.paths.Service)
	if err != nil || unit["MainPID"] != strconv.Itoa(capture.PID) || unit["InvocationID"] != capture.InvocationID || unit["ActiveState"] != "active" {
		return errors.New("captured writer generation changed while reading its environment")
	}
	path := filepath.Join(workspace.stateDir, "maintenance-process-"+capture.InvocationID+".environment")
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		return errors.New("writer process environment evidence already exists")
	}
	if err := writeAtomicRegularFile(path, content, 0600); err != nil {
		return err
	}
	capture.ProcessEnvironmentPath, capture.ProcessEnvironmentSHA256 = path, fmt.Sprintf("%x", sha256Bytes(content))
	return nil
}

func (runtime *productionRuntime) persistMaintenanceCapture(workspace productionWorkspace, manifest productionManifest, phase string) (productionStatus, error) {
	capture := *manifest.MaintenanceCapture
	capture.Phase = phase
	path := filepath.Join(workspace.stateDir, "maintenance-capture."+phase+".json")
	if phase == "FROZEN" {
		capture.ShutdownJournalPath = filepath.Join(workspace.root, "maintenance-shutdown.log")
		capture.ShutdownJournalSHA256 = manifest.BillingGate.ShutdownJournalSHA256
	}
	content, err := json.MarshalIndent(capture, "", "  ")
	if err != nil {
		return productionStatus{}, err
	}
	content = append(content, '\n')
	if err := writeAtomicRegularFile(path, content, 0o600); err != nil {
		return productionStatus{}, err
	}
	status := productionStatus{MaintenanceConfirmation: capture.WasMaintenanceConfirmed, Phase: phase, Version: manifest.OldVersion, Previous: manifest.ExpectedVersion, CaptureReceiptPath: path, CaptureReceiptSHA256: fmt.Sprintf("%x", sha256Bytes(content))}
	if err := runtime.writeStatus(workspace, status); err != nil {
		return productionStatus{}, err
	}
	status, err = runtime.readStatus(workspace)
	return status, err
}

func (runtime *productionRuntime) maintenanceClose(ctx context.Context, workspace productionWorkspace) (productionStatus, error) {
	if err := runtime.refuseUnstoppedPostMutation(); err != nil {
		return productionStatus{}, err
	}
	manifest, err := runtime.readManifest(workspace)
	if err != nil {
		return productionStatus{}, err
	}
	status, err := runtime.readStatus(workspace)
	if err != nil {
		return productionStatus{}, err
	}
	if status.Phase != "CAPTURED" || manifest.MaintenanceCapture == nil || runtime.maintenanceHandoff == nil {
		return productionStatus{}, errors.New("admission close requires a live captured ordinary owner")
	}
	if err := runtime.validateTransactionLock(workspace); err != nil {
		return productionStatus{}, err
	}
	unit, err := runtime.billingUnitState(ctx, runtime.paths.Service)
	if err != nil {
		return productionStatus{}, err
	}
	captured := manifest.MaintenanceCapture
	if unit["MainPID"] != strconv.Itoa(captured.PID) || unit["InvocationID"] != captured.InvocationID || unit["ActiveState"] != "active" {
		return productionStatus{}, errors.New("captured ordinary writer generation changed before admission close")
	}
	h := runtime.maintenanceHandoff
	runtime.maintenanceHandoff = nil
	probeErr := runtime.probeReleaseWithBinary(ctx, workspace, runtime.paths.InstalledBinary, manifest.OldVersion, manifest.Frontend.OldIndexSHA256)
	runtime.maintenanceHandoff = h
	if probeErr != nil {
		return productionStatus{}, probeErr
	}
	if err := runtime.closeBillingAdmission(ctx, workspace, &manifest); err != nil {
		return productionStatus{}, err
	}
	manifest.BillingGate.GoPID = captured.PID
	manifest.BillingGate.GoInvocationID = captured.InvocationID
	if err := runtime.writeManifest(workspace, manifest); err != nil {
		return productionStatus{}, err
	}
	return runtime.persistMaintenanceCapture(workspace, manifest, "ADMISSION_CLOSED")
}

func (runtime *productionRuntime) maintenanceStop(ctx context.Context, workspace productionWorkspace, allPath, allSHA string) (productionStatus, error) {
	if err := runtime.refuseUnstoppedPostMutation(); err != nil {
		return productionStatus{}, err
	}
	manifest, err := runtime.readManifest(workspace)
	if err != nil {
		return productionStatus{}, err
	}
	status, err := runtime.readStatus(workspace)
	if err != nil {
		return productionStatus{}, err
	}
	if (status.Phase != "ADMISSION_CLOSED" && status.Phase != productionMaintenanceConfirmedPhase) || runtime.maintenanceHandoff == nil {
		return productionStatus{}, errors.New("writer stop requires captured closed admission")
	}
	content, err := readMaintenanceBoundFile(allPath, allSHA, uint32(runtime.effectiveUID()))
	if err != nil {
		return productionStatus{}, err
	}
	var all struct {
		Format                 string `json:"format"`
		TransitionID           string `json:"transition_id"`
		TransitionIntentSHA256 string `json:"transition_intent_sha256"`
		AllOriginsClosed       bool   `json:"all_origins_closed"`
	}
	if json.Unmarshal(content, &all) != nil || all.Format != "lmm-credit-all-admission-closed-v1" || all.TransitionID != runtime.maintenanceHandoff.TransitionID || all.TransitionIntentSHA256 != runtime.maintenanceHandoff.TransitionIntentSHA256 || !all.AllOriginsClosed {
		return productionStatus{}, errors.New("all-origin closed admission receipt is not bound to this transition")
	}
	if status.Phase == productionMaintenanceConfirmedPhase {
		unit, err := runtime.billingUnitState(ctx, runtime.paths.Service)
		if err != nil {
			return productionStatus{}, err
		}
		pid, err := strconv.Atoi(unit["MainPID"])
		if err != nil || pid <= 1 {
			return productionStatus{}, errors.New("confirmed maintenance writer identity unavailable")
		}
		if err := runtime.probeBoundMaintenanceLocal(ctx, runtime.paths.InstalledBinary, manifest.ExpectedVersion); err != nil {
			return productionStatus{}, err
		}
		manifest.MaintenanceCapture = &productionMaintenanceCapture{Format: "lmm-credit-maintenance-capture-v1", TransitionID: runtime.maintenanceHandoff.TransitionID, TransitionIntentSHA256: runtime.maintenanceHandoff.TransitionIntentSHA256, ProviderSHA256: runtime.maintenanceHandoff.ProviderSHA256, Version: manifest.ExpectedVersion, PID: pid, InvocationID: unit["InvocationID"], ArchivedEnvironmentPath: filepath.Join(workspace.configRestore, "lmm-api-go.env"), ArchivedEnvironmentSHA256: manifest.EnvironmentRestoreSHA256, DatabaseSchema: manifest.DatabaseSchema, FrontendTarget: manifest.Frontend.NewTarget, FrontendSHA256: manifest.Frontend.NewIndexSHA256, WasMaintenanceConfirmed: true}
		environment, err := readPrivateRegularFile(filepath.Join(workspace.configRestore, "lmm-api-go.env"), 1<<20)
		if err != nil {
			return productionStatus{}, err
		}
		if err := runtime.captureMaintenanceProcessEnvironment(ctx, workspace, manifest.MaintenanceCapture, environment); err != nil {
			return productionStatus{}, err
		}
	}
	if err := runtime.validateTransactionLock(workspace); err != nil {
		return productionStatus{}, err
	}
	if err := runtime.probeMaintenanceAdmission(ctx, workspace, runtime.paths.InstalledBinary); err != nil {
		return productionStatus{}, err
	}
	unit, err := runtime.billingUnitState(ctx, runtime.paths.Service)
	if err != nil {
		return productionStatus{}, err
	}
	captured := manifest.MaintenanceCapture
	if unit["MainPID"] != strconv.Itoa(captured.PID) || unit["InvocationID"] != captured.InvocationID {
		return productionStatus{}, errors.New("captured writer generation changed before stop")
	}
	runtime.billingAdmissionClosed = true
	if err := runtime.stopBillingWriter(ctx, workspace, &manifest); err != nil {
		return productionStatus{}, err
	}
	journal, err := runtime.runner.Run(ctx, productionCommand{Name: commandJournalctl, Args: []string{"--no-pager", "--output=cat", "_PID=" + strconv.Itoa(captured.PID), "_SYSTEMD_INVOCATION_ID=" + captured.InvocationID}})
	if err != nil {
		return productionStatus{}, err
	}
	if captured.WasMaintenanceConfirmed {
		if err := validateMaintenanceShutdownJournal(journal); err != nil {
			return productionStatus{}, err
		}
	} else if err := validateBillingShutdownJournal(journal); err != nil {
		return productionStatus{}, err
	}
	if err := writeAtomicRegularFile(filepath.Join(workspace.root, "maintenance-shutdown.log"), journal, 0o600); err != nil {
		return productionStatus{}, err
	}
	manifest.BillingGate.ShutdownJournalSHA256 = fmt.Sprintf("%x", sha256Bytes(journal))
	if err := runtime.writeManifest(workspace, manifest); err != nil {
		return productionStatus{}, err
	}
	return runtime.persistMaintenanceCapture(workspace, manifest, "FROZEN")
}

func validateMaintenanceServiceDropIn(path string, content []byte) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	owner, links, ok := deploymentFileOwnership(info)
	if !ok || links != 1 || info.Mode().Perm() != 0o644 {
		return errors.New("maintenance service drop-in ownership/mode is unsafe")
	}
	lines := strings.Split(string(content), "\n")
	if len(lines) != 5 || lines[0] != "[Service]" || lines[1] != "SupplementaryGroups="+productionMaintenanceReaderGroup || !strings.HasPrefix(lines[2], "Environment=LMM_CREDIT_TRANSITION_PLAN=") || !strings.HasPrefix(lines[3], "Environment=LMM_CREDIT_TRANSITION_SHA256=") || lines[4] != "" {
		return errors.New("maintenance service drop-in has unrecognized directives")
	}
	configPath := strings.TrimPrefix(lines[2], "Environment=LMM_CREDIT_TRANSITION_PLAN=")
	digest := strings.TrimPrefix(lines[3], "Environment=LMM_CREDIT_TRANSITION_SHA256=")
	_, err = readMaintenanceBoundFile(configPath, digest, owner)
	return err
}

func (runtime *productionRuntime) archiveMaintenancePrearmFailure(ctx context.Context, workspace productionWorkspace) error {
	if err := runtime.refuseUnstoppedPostMutation(); err != nil {
		return err
	}
	if !runtime.maintenanceStopped() || runtime.guardianLease == nil {
		return errors.New("maintenance prearm retry requires the same stopped handoff and guardian lease")
	}
	status, err := runtime.readStatus(workspace)
	if err != nil {
		return err
	}
	if status.Phase != "MAINTENANCE_PREARM_FAILED" || status.TransitionID != runtime.maintenanceHandoff.TransitionID || status.TransitionIntentSHA256 != runtime.maintenanceHandoff.TransitionIntentSHA256 {
		return errors.New("maintenance retry cannot resume an activated or different transition")
	}
	if err := runtime.validateTransactionLock(workspace); err != nil {
		return err
	}
	if err := runtime.validateStoppedMaintenanceWriter(ctx); err != nil {
		return err
	}
	environment, err := runtime.maintenanceEnvironment()
	if err != nil {
		return err
	}
	if err := runtime.verifyMaintenanceDatabase(ctx, environment, runtime.maintenancePost()); err != nil {
		return err
	}
	if manifest, err := runtime.readManifestSchema(workspace); err == nil {
		if manifest.MaintenanceHandoff == nil || manifest.MaintenanceHandoff.SHA256 != runtime.maintenanceHandoff.SHA256 {
			return errors.New("failed manifest maintenance identity changed")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	archive, err := os.MkdirTemp(workspace.stateDir, "maintenance-failed-attempt-")
	if err != nil {
		return err
	}
	for _, path := range []string{workspace.manifestPath, workspace.statusPath} {
		if _, err := os.Lstat(path); err == nil {
			if err := os.Rename(path, filepath.Join(archive, filepath.Base(path))); err != nil {
				return err
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}
