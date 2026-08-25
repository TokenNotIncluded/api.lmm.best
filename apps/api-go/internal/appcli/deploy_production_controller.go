package appcli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	productionReleasePhaseWorkspaceCreated     = "WORKSPACE_CREATED"
	productionReleasePhaseStaged               = "STAGED"
	productionReleasePhaseBackupsReady         = "BACKUPS_READY"
	productionReleasePhaseActivationDispatched = "ACTIVATION_DISPATCHED"
)

type productionReleaseControllerOptions struct {
	Plan            string
	PlanSHA256      string
	Confirm         string
	AgeIdentityFile string
	Reason          string
}

type productionReleaseControllerState struct {
	Format           int       `json:"format"`
	DeploymentID     string    `json:"deployment_id"`
	PlanSHA256       string    `json:"plan_sha256"`
	Phase            string    `json:"phase"`
	RemoteWorkspace  string    `json:"remote_workspace"`
	TargetBackup     string    `json:"target_backup,omitempty"`
	ControllerBackup string    `json:"controller_backup,omitempty"`
	OffhostBackup    string    `json:"offhost_backup,omitempty"`
	Version          string    `json:"version,omitempty"`
	RollbackTimer    string    `json:"rollback_timer,omitempty"`
	ActivationUnit   string    `json:"activation_unit,omitempty"`
	DispatchAttempts int       `json:"dispatch_attempts,omitempty"`
	DispatchObserved bool      `json:"dispatch_observed,omitempty"`
	UpdatedUTC       time.Time `json:"updated_utc"`
}

type productionReleaseControllerResult struct {
	DeploymentID     string `json:"deployment_id"`
	PlanSHA256       string `json:"plan_sha256"`
	Version          string `json:"version"`
	Revision         string `json:"revision"`
	Status           string `json:"status"`
	TargetBackup     string `json:"target_backup,omitempty"`
	ControllerBackup string `json:"controller_backup,omitempty"`
	OffhostBackup    string `json:"offhost_backup,omitempty"`
	RollbackTimer    string `json:"rollback_timer,omitempty"`
	ActivationUnit   string `json:"activation_unit,omitempty"`
	DispatchAttempts int    `json:"dispatch_attempts,omitempty"`
	Workspace        string `json:"workspace"`
}

func runProductionReleaseStage(args []string, stdout, stderr io.Writer) int {
	options, err := parseProductionReleaseControllerOptions("stage", args, stderr)
	if errors.Is(err, flag.ErrHelp) {
		return ExitOK
	}
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s deploy production stage: %v\n", ProgramName, err)
		return ExitUsage
	}
	runtime := &productionReleaseRuntime{runner: osProductionCommandRunner{}, now: time.Now}
	result, err := runtime.stage(context.Background(), options)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s deploy production stage: %v\n", ProgramName, err)
		return ExitError
	}
	return writeJSONCommandResult(result, stdout, stderr, "production release stage")
}

func runProductionReleasePromote(args []string, stdout, stderr io.Writer) int {
	options, err := parseProductionReleaseControllerOptions("promote", args, stderr)
	if errors.Is(err, flag.ErrHelp) {
		return ExitOK
	}
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s deploy production promote: %v\n", ProgramName, err)
		return ExitUsage
	}
	runtime := &productionReleaseRuntime{runner: osProductionCommandRunner{}, now: time.Now}
	result, err := runtime.promote(context.Background(), options)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s deploy production promote: %v\n", ProgramName, err)
		return ExitError
	}
	return writeJSONCommandResult(result, stdout, stderr, "production release promote")
}

func runProductionReleaseControllerAction(action string, args []string, stdout, stderr io.Writer) int {
	options, err := parseProductionReleaseControllerOptions(action, args, stderr)
	if errors.Is(err, flag.ErrHelp) {
		return ExitOK
	}
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s deploy production %s: %v\n", ProgramName, action, err)
		return ExitUsage
	}
	runtime := &productionReleaseRuntime{runner: osProductionCommandRunner{}, now: time.Now}
	result, err := runtime.control(context.Background(), action, options)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s deploy production %s: %v\n", ProgramName, action, err)
		return ExitError
	}
	return writeJSONCommandResult(result, stdout, stderr, "production release "+action)
}

func parseProductionReleaseControllerOptions(action string, args []string, stderr io.Writer) (productionReleaseControllerOptions, error) {
	options := productionReleaseControllerOptions{Reason: "operator-request"}
	flags := flag.NewFlagSet("deploy production "+action, flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(&options.Plan, "plan", "", "immutable controller release plan")
	flags.StringVar(&options.PlanSHA256, "plan-sha256", "", "exact immutable release-plan SHA-256")
	flags.StringVar(&options.Confirm, "confirm", "", "must equal api.lmm.best")
	if action == "promote" {
		flags.StringVar(&options.AgeIdentityFile, "age-identity-file", "", "owner-protected age or SSH private identity for backup verification")
	}
	if action == "rollback" {
		flags.StringVar(&options.Reason, "reason", options.Reason, "audit-safe rollback reason")
	}
	flags.Usage = func() { writeProductionDeployUsage(stderr) }
	if err := flags.Parse(args); err != nil {
		return productionReleaseControllerOptions{}, err
	}
	if flags.NArg() != 0 {
		return productionReleaseControllerOptions{}, errors.New("unexpected positional arguments")
	}
	if options.Confirm != "api.lmm.best" {
		return productionReleaseControllerOptions{}, errors.New("--confirm must equal api.lmm.best")
	}
	if options.Plan == "" {
		return productionReleaseControllerOptions{}, errors.New("--plan is required")
	}
	clean, err := cleanAbsoluteNonRoot(options.Plan)
	if err != nil {
		return productionReleaseControllerOptions{}, fmt.Errorf("invalid --plan: %w", err)
	}
	options.Plan = clean
	if !productionSHA256Pattern.MatchString(options.PlanSHA256) {
		return productionReleaseControllerOptions{}, errors.New("--plan-sha256 must be 64 lowercase hexadecimal characters")
	}
	if options.AgeIdentityFile != "" {
		clean, err := cleanAbsoluteNonRoot(options.AgeIdentityFile)
		if err != nil {
			return productionReleaseControllerOptions{}, fmt.Errorf("invalid --age-identity-file: %w", err)
		}
		options.AgeIdentityFile = clean
	}
	if action == "rollback" && !productionReasonPattern.MatchString(options.Reason) {
		return productionReleaseControllerOptions{}, errors.New("--reason must contain only audit-safe letters, digits, dot, underscore, colon, or dash")
	}
	return options, nil
}

func (runtime *productionReleaseRuntime) stage(ctx context.Context, options productionReleaseControllerOptions) (productionReleaseControllerResult, error) {
	plan, err := loadProductionReleasePlan(options.Plan, options.PlanSHA256)
	if err != nil {
		return productionReleaseControllerResult{}, err
	}
	if err := validateProductionReleasePlanArtifacts(ctx, runtime, plan); err != nil {
		return productionReleaseControllerResult{}, fmt.Errorf("revalidate immutable release evidence: %w", err)
	}
	if err := runtime.assertRemoteHost(ctx, plan.TargetAlias, plan.ExpectedHost); err != nil {
		return productionReleaseControllerResult{}, err
	}
	state, exists, err := loadProductionReleaseControllerState(plan, options.PlanSHA256)
	if err != nil {
		return productionReleaseControllerResult{}, err
	}
	if !exists {
		state = productionReleaseControllerState{
			Format:       productionReleaseStateFormat,
			DeploymentID: plan.DeploymentID,
			PlanSHA256:   options.PlanSHA256,
			Version:      plan.ExpectedVersion,
		}
	}
	if state.RemoteWorkspace == "" {
		output, err := runtime.ssh(ctx, plan.TargetAlias, 2*time.Minute,
			productionOperatorBinary, "deploy", "production", "workspace", "create", "--deployment-id", plan.DeploymentID)
		if err != nil {
			return productionReleaseControllerResult{}, fmt.Errorf("create target deployment workspace: %w", err)
		}
		var workspace productionWorkspaceResult
		if err := json.Unmarshal(output, &workspace); err != nil || workspace.DeploymentID != plan.DeploymentID || !workspace.TransactionSet {
			return productionReleaseControllerResult{}, errors.New("target workspace response is invalid")
		}
		expected := filepath.Join(defaultProductionPaths().WorkRoot, plan.DeploymentID)
		if workspace.Workspace != expected {
			return productionReleaseControllerResult{}, errors.New("target workspace path is not canonical")
		}
		state.RemoteWorkspace = workspace.Workspace
		state.Phase = productionReleasePhaseWorkspaceCreated
		state.UpdatedUTC = utcSecond(runtime.now())
		if err := writeProductionReleaseControllerState(plan, state); err != nil {
			return productionReleaseControllerResult{}, fmt.Errorf("persist target workspace state: %w", err)
		}
	}
	if state.RemoteWorkspace != filepath.Join(defaultProductionPaths().WorkRoot, plan.DeploymentID) {
		return productionReleaseControllerResult{}, errors.New("controller state target workspace is invalid")
	}
	files, err := productionReleaseStageFiles(plan, options.Plan)
	if err != nil {
		return productionReleaseControllerResult{}, err
	}
	for _, file := range files {
		remote := filepath.Join(state.RemoteWorkspace, "staging", filepath.Base(file.Path))
		if err := runtime.stageRemoteFile(ctx, plan.TargetAlias, file.Path, remote, file.SHA256, file.Executable); err != nil {
			return productionReleaseControllerResult{}, err
		}
	}
	if err := runtime.verifyRemoteStagedRelease(ctx, plan, state); err != nil {
		return productionReleaseControllerResult{}, err
	}
	if state.Phase == productionReleasePhaseWorkspaceCreated || state.Phase == "" {
		state.Phase = productionReleasePhaseStaged
		state.UpdatedUTC = utcSecond(runtime.now())
		if err := writeProductionReleaseControllerState(plan, state); err != nil {
			return productionReleaseControllerResult{}, err
		}
	}
	return releaseControllerResult(plan, state), nil
}

func (runtime *productionReleaseRuntime) promote(ctx context.Context, options productionReleaseControllerOptions) (productionReleaseControllerResult, error) {
	plan, err := loadProductionReleasePlan(options.Plan, options.PlanSHA256)
	if err != nil {
		return productionReleaseControllerResult{}, err
	}
	state, exists, err := loadProductionReleaseControllerState(plan, options.PlanSHA256)
	if err != nil {
		return productionReleaseControllerResult{}, err
	}
	if !exists || state.RemoteWorkspace == "" {
		return productionReleaseControllerResult{}, errors.New("release has not been staged")
	}
	if err := runtime.assertRemoteHost(ctx, plan.TargetAlias, plan.ExpectedHost); err != nil {
		return productionReleaseControllerResult{}, err
	}
	if err := runtime.verifyRemoteStagedRelease(ctx, plan, state); err != nil {
		return productionReleaseControllerResult{}, err
	}
	if plan.WithBackups && state.TargetBackup == "" {
		if options.AgeIdentityFile == "" {
			return productionReleaseControllerResult{}, errors.New("--age-identity-file is required by this backup-enabled plan")
		}
		if err := validateAgeIdentity(options.AgeIdentityFile); err != nil {
			return productionReleaseControllerResult{}, err
		}
		backups, err := runtime.prepareControllerBackups(ctx, plan, state, options.AgeIdentityFile)
		if err != nil {
			return productionReleaseControllerResult{}, err
		}
		state.TargetBackup = backups.TargetBackup
		state.ControllerBackup = backups.ControllerBackup
		state.OffhostBackup = backups.OffhostBackup
		state.Phase = productionReleasePhaseBackupsReady
		state.UpdatedUTC = utcSecond(runtime.now())
		if err := writeProductionReleaseControllerState(plan, state); err != nil {
			return productionReleaseControllerResult{}, err
		}
	}
	if state.Phase == productionReleasePhaseStaged ||
		state.Phase == productionReleasePhaseBackupsReady ||
		state.Phase == productionReleasePhaseActivationDispatched {
		if err := runtime.dispatchProductionActivation(ctx, plan, &state); err != nil {
			return productionReleaseControllerResult{}, err
		}
	}
	status, err := runtime.awaitRemoteReleaseStatus(ctx, plan, &state)
	if err != nil {
		return productionReleaseControllerResult{}, err
	}
	if err := persistRemoteReleaseControllerStatus(plan, &state, status, runtime.now()); err != nil {
		return productionReleaseControllerResult{}, err
	}
	expected := "CONFIRMED"
	if plan.ManualConfirm {
		expected = "AWAITING_CONFIRMATION"
	}
	if status.Phase != expected {
		return productionReleaseControllerResult{}, fmt.Errorf("production release did not finish in %s: phase=%s", expected, status.Phase)
	}
	return releaseControllerResult(plan, state), nil
}

func (runtime *productionReleaseRuntime) control(ctx context.Context, action string, options productionReleaseControllerOptions) (productionReleaseControllerResult, error) {
	plan, err := loadProductionReleasePlan(options.Plan, options.PlanSHA256)
	if err != nil {
		return productionReleaseControllerResult{}, err
	}
	state, exists, err := loadProductionReleaseControllerState(plan, options.PlanSHA256)
	if err != nil {
		return productionReleaseControllerResult{}, err
	}
	if !exists || state.RemoteWorkspace == "" {
		return productionReleaseControllerResult{}, errors.New("release has not been staged")
	}
	if err := runtime.assertRemoteHost(ctx, plan.TargetAlias, plan.ExpectedHost); err != nil {
		return productionReleaseControllerResult{}, err
	}
	if action != "status" {
		arguments := []string{"deploy", "production", action, "--workspace", state.RemoteWorkspace}
		if action == "rollback" {
			arguments = append(arguments, "--reason", options.Reason)
		}
		remoteOperator := productionRemoteOperatorPath(plan, state)
		output, err := runtime.ssh(ctx, plan.TargetAlias, 12*time.Minute, append([]string{remoteOperator}, arguments...)...)
		if err != nil {
			return productionReleaseControllerResult{}, fmt.Errorf("production %s failed or became transport-ambiguous: %w", action, err)
		}
		var status productionStatus
		if err := json.Unmarshal(output, &status); err != nil {
			return productionReleaseControllerResult{}, errors.New("production action returned invalid status JSON")
		}
	}
	status, err := runtime.readRemoteReleaseStatus(ctx, plan, state)
	if err != nil {
		return productionReleaseControllerResult{}, err
	}
	if err := persistRemoteReleaseControllerStatus(plan, &state, status, runtime.now()); err != nil {
		return productionReleaseControllerResult{}, err
	}
	return releaseControllerResult(plan, state), nil
}

func productionRemoteOperatorPath(plan productionReleasePlan, state productionReleaseControllerState) string {
	operator := plan.OperatorBinary.Path
	if operator == "" {
		operator = plan.ProbeBinary.Path
	}
	return filepath.Join(state.RemoteWorkspace, "staging", filepath.Base(operator))
}

func persistRemoteReleaseControllerStatus(plan productionReleasePlan, state *productionReleaseControllerState, status productionStatus, now time.Time) error {
	state.Phase = status.Phase
	state.Version = status.Version
	state.RollbackTimer = status.RollbackTimer
	state.UpdatedUTC = utcSecond(now)
	return writeProductionReleaseControllerState(plan, *state)
}

func (runtime *productionReleaseRuntime) productionApplyArguments(plan productionReleasePlan, state productionReleaseControllerState) []string {
	remoteStage := filepath.Join(state.RemoteWorkspace, "staging")
	remoteOperator := productionRemoteOperatorPath(plan, state)
	remoteProbe := filepath.Join(remoteStage, filepath.Base(plan.ProbeBinary.Path))
	arguments := []string{
		"systemd-run", "--quiet", "--wait", "--collect", "--unit", productionActivationUnit(plan.DeploymentID),
		"--property=Type=oneshot", "--property=TimeoutStartSec=18min",
		remoteOperator, "deploy", "production", "apply",
		"--workspace", state.RemoteWorkspace,
		"--operator-user", plan.OperatorUser,
		"--go-package", filepath.Join(remoteStage, filepath.Base(plan.GoCandidate.PackagePath)),
		"--go-package-sha256", plan.GoCandidate.PackageSHA256,
		"--go-rollback-package", filepath.Join(remoteStage, filepath.Base(plan.GoRollback.PackagePath)),
		"--go-rollback-sha256", plan.GoRollback.PackageSHA256,
		"--web-package", filepath.Join(remoteStage, filepath.Base(plan.WebCandidate.PackagePath)),
		"--web-package-sha256", plan.WebCandidate.PackageSHA256,
		"--web-rollback-package", filepath.Join(remoteStage, filepath.Base(plan.WebRollback.PackagePath)),
		"--web-rollback-sha256", plan.WebRollback.PackageSHA256,
		"--probe-binary", remoteProbe,
		"--probe-binary-sha256", plan.ProbeBinary.SHA256,
		"--operator-binary", remoteOperator,
		"--operator-binary-sha256", plan.OperatorBinary.SHA256,
		"--expected-version", plan.ExpectedVersion,
		"--rollback-seconds", strconv.Itoa(plan.RollbackSeconds),
		"--observation-seconds", strconv.Itoa(plan.ObservationSeconds),
	}
	if plan.GoChanged {
		arguments = append(arguments, "--go-changed")
	}
	if plan.WebChanged {
		arguments = append(arguments, "--web-changed")
	}
	if plan.WithBackups {
		arguments = append(arguments, "--with-backups", "--backup-dir", state.TargetBackup)
	}
	if plan.ManualConfirm {
		arguments = append(arguments, "--manual-confirm")
	}
	if plan.PreserveEdgePolicy {
		arguments = append(arguments, "--preserve-edge-policy")
	}
	return arguments
}

func (runtime *productionReleaseRuntime) remoteDispatchEvidence(ctx context.Context, plan productionReleasePlan, state productionReleaseControllerState) (productionDispatchEvidence, error) {
	remoteOperator := productionRemoteOperatorPath(plan, state)
	output, err := runtime.ssh(ctx, plan.TargetAlias, 30*time.Second,
		remoteOperator, "deploy", "production", "dispatch-evidence",
		"--workspace", state.RemoteWorkspace, "--unit", state.ActivationUnit)
	if err != nil {
		return productionDispatchEvidence{}, fmt.Errorf("reconcile production activation dispatch: %w", err)
	}
	var evidence productionDispatchEvidence
	if err := json.Unmarshal(output, &evidence); err != nil ||
		evidence.Format != 1 || evidence.DeploymentID != plan.DeploymentID ||
		evidence.Unit != state.ActivationUnit || evidence.UnitLoadState == "" {
		return productionDispatchEvidence{}, errors.New("production activation dispatch evidence is invalid")
	}
	return evidence, nil
}

func productionDispatchHasEvidence(evidence productionDispatchEvidence) bool {
	return evidence.UnitPresent || evidence.ManifestPresent || evidence.StatusPresent
}

func (runtime *productionReleaseRuntime) dispatchProductionActivation(ctx context.Context, plan productionReleasePlan, state *productionReleaseControllerState) error {
	expectedUnit := productionActivationUnit(plan.DeploymentID)
	if state.Phase == productionReleasePhaseActivationDispatched {
		if state.ActivationUnit != expectedUnit || state.DispatchAttempts < 1 || state.DispatchAttempts > 2 {
			return errors.New("persisted activation dispatch identity is incomplete")
		}
		evidence, err := runtime.remoteDispatchEvidence(ctx, plan, *state)
		if err != nil {
			return err
		}
		if productionDispatchHasEvidence(evidence) {
			state.DispatchObserved = true
			state.UpdatedUTC = utcSecond(runtime.now())
			return writeProductionReleaseControllerState(plan, *state)
		}
		if state.DispatchObserved || state.DispatchAttempts >= 2 {
			return errors.New("activation dispatch has no remote evidence and its single redispatch is exhausted")
		}
	}
	for state.DispatchAttempts < 2 {
		state.Phase = productionReleasePhaseActivationDispatched
		state.ActivationUnit = expectedUnit
		state.DispatchAttempts++
		state.UpdatedUTC = utcSecond(runtime.now())
		if err := writeProductionReleaseControllerState(plan, *state); err != nil {
			return err
		}
		applyArgs := runtime.productionApplyArguments(plan, *state)
		if _, err := runtime.ssh(ctx, plan.TargetAlias, 20*time.Minute, applyArgs...); err == nil {
			state.DispatchObserved = true
			state.UpdatedUTC = utcSecond(runtime.now())
			return writeProductionReleaseControllerState(plan, *state)
		} else {
			evidence, reconcileErr := runtime.remoteDispatchEvidence(ctx, plan, *state)
			if reconcileErr != nil {
				return fmt.Errorf("production activation became transport-ambiguous and reconciliation failed: dispatch=%v reconcile=%w", err, reconcileErr)
			}
			if productionDispatchHasEvidence(evidence) {
				state.DispatchObserved = true
				state.UpdatedUTC = utcSecond(runtime.now())
				if writeErr := writeProductionReleaseControllerState(plan, *state); writeErr != nil {
					return writeErr
				}
				return nil
			}
			if state.DispatchAttempts >= 2 {
				return fmt.Errorf("production activation failed before remote acceptance after the single safe redispatch: %w", err)
			}
		}
	}
	return errors.New("activation dispatch retry invariant failed")
}

func (runtime *productionReleaseRuntime) awaitRemoteReleaseStatus(ctx context.Context, plan productionReleasePlan, state *productionReleaseControllerState) (productionStatus, error) {
	waitCtx, cancel := context.WithTimeout(ctx, 20*time.Minute)
	defer cancel()
	for {
		status, err := runtime.readRemoteReleaseStatus(waitCtx, plan, *state)
		if err == nil && productionActivationStatusTerminalForPlan(plan, status) {
			return status, nil
		}
		if err == nil {
			state.DispatchObserved = true
			state.UpdatedUTC = utcSecond(runtime.now())
			if writeErr := writeProductionReleaseControllerState(plan, *state); writeErr != nil {
				return productionStatus{}, writeErr
			}
			if waitErr := runtime.waitForDispatchObservation(waitCtx, 2*time.Second); waitErr != nil {
				return productionStatus{}, fmt.Errorf("wait for terminal production activation status: %w", waitErr)
			}
			continue
		}
		evidence, reconcileErr := runtime.remoteDispatchEvidence(waitCtx, plan, *state)
		if reconcileErr != nil {
			return productionStatus{}, fmt.Errorf("observe production activation: status=%v evidence=%w", err, reconcileErr)
		}
		if !productionDispatchHasEvidence(evidence) {
			return productionStatus{}, errors.New("accepted production activation lost its unit, manifest, and status evidence")
		}
		state.DispatchObserved = true
		state.UpdatedUTC = utcSecond(runtime.now())
		if writeErr := writeProductionReleaseControllerState(plan, *state); writeErr != nil {
			return productionStatus{}, writeErr
		}
		if waitErr := runtime.waitForDispatchObservation(waitCtx, 2*time.Second); waitErr != nil {
			return productionStatus{}, fmt.Errorf("wait for production activation status: %w", waitErr)
		}
	}
}

func productionActivationStatusTerminalForPlan(plan productionReleasePlan, status productionStatus) bool {
	if status.Phase == "AWAITING_CONFIRMATION" && !plan.ManualConfirm && status.AutoConfirm {
		return false
	}
	return productionActivationStatusTerminal(status.Phase)
}

func productionActivationStatusTerminal(phase string) bool {
	switch phase {
	case "AWAITING_CONFIRMATION", "CONFIRMED", "ROLLED_BACK", "FAILED_PREARM", "ROLLBACK_FAILED", "ABORTED":
		return true
	default:
		return false
	}
}

func (runtime *productionReleaseRuntime) waitForDispatchObservation(ctx context.Context, delay time.Duration) error {
	if runtime.wait != nil {
		return runtime.wait(ctx, delay)
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (runtime *productionReleaseRuntime) readRemoteReleaseStatus(ctx context.Context, plan productionReleasePlan, state productionReleaseControllerState) (productionStatus, error) {
	remoteOperator := productionRemoteOperatorPath(plan, state)
	output, err := runtime.ssh(ctx, plan.TargetAlias, 2*time.Minute,
		remoteOperator, "deploy", "production", "status", "--workspace", state.RemoteWorkspace)
	if err != nil {
		return productionStatus{}, fmt.Errorf("read production release status: %w", err)
	}
	var status productionStatus
	if err := json.Unmarshal(output, &status); err != nil || status.Phase == "" {
		return productionStatus{}, errors.New("production release status is invalid")
	}
	return status, nil
}

type productionReleaseStageFile struct {
	Path       string
	SHA256     string
	Executable bool
}

func productionReleaseStageFiles(plan productionReleasePlan, planPath string) ([]productionReleaseStageFile, error) {
	planSHA256, err := sha256File(planPath)
	if err != nil {
		return nil, fmt.Errorf("hash release plan: %w", err)
	}
	digestPath := filepath.Join(plan.ControllerWorkspace, productionReleasePlanHashFilename)
	digestSHA256, err := sha256File(digestPath)
	if err != nil {
		return nil, fmt.Errorf("hash release plan digest file: %w", err)
	}
	operator := plan.OperatorBinary
	if operator.Path == "" {
		operator = plan.ProbeBinary
	}
	files := []productionReleaseStageFile{
		{plan.GoCandidate.PackagePath, plan.GoCandidate.PackageSHA256, false},
		{plan.GoRollback.PackagePath, plan.GoRollback.PackageSHA256, false},
		{plan.WebCandidate.PackagePath, plan.WebCandidate.PackageSHA256, false},
		{plan.WebRollback.PackagePath, plan.WebRollback.PackageSHA256, false},
		{plan.ProbeBinary.Path, plan.ProbeBinary.SHA256, true},
		{operator.Path, operator.SHA256, true},
		{planPath, planSHA256, false},
		{digestPath, digestSHA256, false},
	}
	if plan.WithBackups {
		files = append(files, productionReleaseStageFile{plan.AgeRecipient.Path, plan.AgeRecipient.SHA256, false})
	}
	unique := make([]productionReleaseStageFile, 0, len(files))
	seen := make(map[string]productionReleaseStageFile)
	for _, file := range files {
		base := filepath.Base(file.Path)
		if current, ok := seen[base]; ok {
			if current.SHA256 != file.SHA256 || current.Executable != file.Executable {
				return nil, fmt.Errorf("staging basename collision: %s", base)
			}
			continue
		}
		seen[base] = file
		unique = append(unique, file)
	}
	return unique, nil
}

func (runtime *productionReleaseRuntime) stageRemoteFile(ctx context.Context, alias, local, remote, expectedSHA256 string, executable bool) error {
	if digest, err := runtime.remoteFileSHA256(ctx, alias, remote); err == nil {
		if digest != expectedSHA256 {
			return fmt.Errorf("remote staging file already exists with a different digest: %s", remote)
		}
		return nil
	}
	if _, err := runtime.ssh(ctx, alias, 2*time.Minute, "test", "!", "-e", remote); err != nil {
		return fmt.Errorf("remote staging destination is occupied or could not be inspected: %s", remote)
	}
	if err := runtime.scpTo(ctx, local, alias, remote); err != nil {
		return fmt.Errorf("stage %s: %w", filepath.Base(local), err)
	}
	mode := "0600"
	if executable {
		mode = "0700"
	}
	if _, err := runtime.ssh(ctx, alias, 2*time.Minute, "chmod", mode, "--", remote); err != nil {
		return err
	}
	digest, err := runtime.remoteFileSHA256(ctx, alias, remote)
	if err != nil || digest != expectedSHA256 {
		return fmt.Errorf("remote staging digest mismatch: %s", remote)
	}
	return nil
}

func (runtime *productionReleaseRuntime) remoteFileSHA256(ctx context.Context, alias, path string) (string, error) {
	output, err := runtime.ssh(ctx, alias, 2*time.Minute, "sha256sum", "--", path)
	if err != nil {
		return "", err
	}
	fields := strings.Fields(string(output))
	if len(fields) != 2 || fields[1] != path || !productionSHA256Pattern.MatchString(fields[0]) {
		return "", errors.New("remote SHA-256 response is invalid")
	}
	return fields[0], nil
}

func (runtime *productionReleaseRuntime) verifyRemoteStagedRelease(ctx context.Context, plan productionReleasePlan, state productionReleaseControllerState) error {
	files := []productionReleaseFilePlan{
		{Path: plan.GoCandidate.PackagePath, SHA256: plan.GoCandidate.PackageSHA256},
		{Path: plan.GoRollback.PackagePath, SHA256: plan.GoRollback.PackageSHA256},
		{Path: plan.WebCandidate.PackagePath, SHA256: plan.WebCandidate.PackageSHA256},
		{Path: plan.WebRollback.PackagePath, SHA256: plan.WebRollback.PackageSHA256},
		plan.ProbeBinary,
	}
	if plan.WithBackups {
		files = append(files, plan.AgeRecipient)
	}
	seen := make(map[string]string)
	for _, file := range files {
		base := filepath.Base(file.Path)
		if digest, ok := seen[base]; ok {
			if digest != file.SHA256 {
				return errors.New("remote staging plan has a basename collision")
			}
			continue
		}
		seen[base] = file.SHA256
		remote := filepath.Join(state.RemoteWorkspace, "staging", base)
		digest, err := runtime.remoteFileSHA256(ctx, plan.TargetAlias, remote)
		if err != nil || digest != file.SHA256 {
			return fmt.Errorf("remote staged artifact failed digest verification: %s", base)
		}
	}
	return nil
}

type productionPreparedBackups struct {
	TargetBackup     string
	ControllerBackup string
	OffhostBackup    string
}

func (runtime *productionReleaseRuntime) prepareControllerBackups(ctx context.Context, plan productionReleasePlan, state productionReleaseControllerState, ageIdentityFile string) (productionPreparedBackups, error) {
	if err := runtime.assertRemoteHost(ctx, productionOffhostAlias, productionOffhostExpectedHost); err != nil {
		return productionPreparedBackups{}, err
	}
	remoteStage := filepath.Join(state.RemoteWorkspace, "staging")
	remoteProbe := filepath.Join(remoteStage, filepath.Base(plan.ProbeBinary.Path))
	remoteRollback := filepath.Join(remoteStage, filepath.Base(plan.GoRollback.PackagePath))
	remoteRecipient := filepath.Join(remoteStage, filepath.Base(plan.AgeRecipient.Path))
	targetBackup := filepath.Join(defaultProductionPaths().BackupRoot, plan.DeploymentID)
	exists, err := runtime.remoteDirectoryExists(ctx, plan.TargetAlias, targetBackup)
	if err != nil {
		return productionPreparedBackups{}, err
	}
	if !exists {
		if _, err := runtime.ssh(ctx, plan.TargetAlias, 12*time.Minute,
			remoteProbe, "deploy", "production", "backup", "create",
			"--workspace", state.RemoteWorkspace,
			"--rollback-package", remoteRollback,
			"--rollback-sha256", plan.GoRollback.PackageSHA256,
			"--candidate-sha256", plan.GoCandidate.PackageSHA256,
			"--expected-version", plan.ExpectedVersion,
			"--git-revision", plan.GoCandidate.GitRevision,
		); err != nil {
			return productionPreparedBackups{}, fmt.Errorf("create target production backup: %w", err)
		}
	}
	remoteControllerCopy := filepath.Join(remoteStage, "controller-copy")
	remoteOffhostCopy := filepath.Join(remoteStage, "offhost-copy")
	for role, output := range map[string]string{"controller": remoteControllerCopy, "off-host": remoteOffhostCopy} {
		exists, err := runtime.remoteDirectoryExists(ctx, plan.TargetAlias, output)
		if err != nil {
			return productionPreparedBackups{}, err
		}
		if exists {
			continue
		}
		if _, err := runtime.ssh(ctx, plan.TargetAlias, 12*time.Minute,
			remoteProbe, "deploy", "production", "backup", "export",
			"--workspace", state.RemoteWorkspace, "--role", role, "--output", output,
			"--age-recipient-file", remoteRecipient,
		); err != nil {
			return productionPreparedBackups{}, fmt.Errorf("create %s backup copy: %w", role, err)
		}
	}
	backupRoot := filepath.Join(plan.ControllerWorkspace, "backups")
	for _, directory := range []string{backupRoot, filepath.Join(backupRoot, "target-proof"), filepath.Join(backupRoot, "offhost")} {
		if err := ensureRealDirectory(directory, 0o700); err != nil {
			return productionPreparedBackups{}, err
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return productionPreparedBackups{}, fmt.Errorf("resolve controller home: %w", err)
	}
	durableControllerRoot := filepath.Join(home, "backup", "lmm-api", plan.ExpectedHost)
	for _, directory := range []string{home, filepath.Join(home, "backup"), filepath.Join(home, "backup", "lmm-api"), durableControllerRoot} {
		if directory != home {
			if err := ensureRealDirectory(directory, 0o700); err != nil {
				return productionPreparedBackups{}, err
			}
		}
		if err := requireRealDirectory(directory); err != nil {
			return productionPreparedBackups{}, fmt.Errorf("controller backup path is unsafe: %w", err)
		}
	}
	targetProof := filepath.Join(backupRoot, "target-proof", plan.DeploymentID)
	if err := ensureRealDirectory(targetProof, 0o700); err != nil {
		return productionPreparedBackups{}, err
	}
	for _, name := range []string{"manifest.env", "SHA256SUMS"} {
		local := filepath.Join(targetProof, name)
		if _, err := os.Lstat(local); errors.Is(err, os.ErrNotExist) {
			if err := runtime.scpFrom(ctx, plan.TargetAlias, filepath.Join(targetBackup, name), local); err != nil {
				return productionPreparedBackups{}, fmt.Errorf("retrieve target backup proof %s: %w", name, err)
			}
		} else if err != nil {
			return productionPreparedBackups{}, err
		}
	}
	controllerBackup := filepath.Join(durableControllerRoot, plan.DeploymentID)
	offhostMirror := filepath.Join(backupRoot, "offhost", plan.DeploymentID)
	transfers := []struct {
		remote string
		local  string
	}{
		{remoteControllerCopy, controllerBackup},
		{remoteOffhostCopy, offhostMirror},
	}
	existing := 0
	for _, transfer := range transfers {
		if _, err := os.Lstat(transfer.local); err == nil {
			existing++
		} else if !errors.Is(err, os.ErrNotExist) {
			return productionPreparedBackups{}, err
		}
	}
	if existing != 0 && existing != len(transfers) {
		return productionPreparedBackups{}, errors.New("partial encrypted backup mirrors already exist; refusing to overwrite")
	}
	if existing == 0 {
		for _, transfer := range transfers {
			if err := runtime.scpFrom(ctx, plan.TargetAlias, transfer.remote, transfer.local); err != nil {
				return productionPreparedBackups{}, fmt.Errorf("retrieve encrypted backup %s: %w", filepath.Base(transfer.local), err)
			}
		}
	}
	verificationRuntime := &productionRuntime{runner: runtime.runner, now: runtime.now, effectiveUID: os.Geteuid}
	verification, err := verificationRuntime.verifyExternalBackups(ctx, productionBackupVerifyOptions{
		Workspace:       plan.ControllerWorkspace,
		Target:          targetProof,
		Controller:      controllerBackup,
		Offhost:         offhostMirror,
		AgeIdentityFile: ageIdentityFile,
	})
	if err != nil {
		return productionPreparedBackups{}, fmt.Errorf("verify three production backup copies: %w", err)
	}
	if verification.DeploymentID != plan.DeploymentID {
		return productionPreparedBackups{}, errors.New("verified backup deployment identity mismatch")
	}
	if _, err := runtime.ssh(ctx, productionOffhostAlias, 2*time.Minute, "install", "-d", "-m0700", productionOffhostRoot); err != nil {
		return productionPreparedBackups{}, fmt.Errorf("prepare off-host backup root: %w", err)
	}
	offhostBackup := filepath.Join(productionOffhostRoot, plan.DeploymentID)
	offhostExists, err := runtime.remoteDirectoryExists(ctx, productionOffhostAlias, offhostBackup)
	if err != nil {
		return productionPreparedBackups{}, err
	}
	if !offhostExists {
		if err := runtime.scpToRecursive(ctx, offhostMirror, productionOffhostAlias, offhostBackup); err != nil {
			return productionPreparedBackups{}, fmt.Errorf("publish off-host backup: %w", err)
		}
	}
	offhostDigestOutput, err := runtime.ssh(ctx, productionOffhostAlias, 2*time.Minute, "sha256sum", filepath.Join(offhostBackup, "SHA256SUMS"))
	offhostDigestFields := strings.Fields(string(offhostDigestOutput))
	if err != nil || len(offhostDigestFields) == 0 || offhostDigestFields[0] != verification.OffhostDigest {
		return productionPreparedBackups{}, errors.New("off-host backup differs from verified controller copy")
	}
	if _, err := runtime.ssh(ctx, plan.TargetAlias, 2*time.Minute,
		remoteProbe, "deploy", "production", "backup", "attest", "--workspace", state.RemoteWorkspace,
		"--controller-digest", verification.ControllerDigest, "--offhost-digest", verification.OffhostDigest,
	); err != nil {
		return productionPreparedBackups{}, fmt.Errorf("attest verified external backup copies: %w", err)
	}
	return productionPreparedBackups{TargetBackup: targetBackup, ControllerBackup: controllerBackup, OffhostBackup: offhostBackup}, nil
}

func validateAgeIdentity(path string) error {
	if err := validateControllerArtifact(path, "age identity", false); err != nil {
		return err
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.Mode().Perm()&0o077 != 0 {
		return errors.New("age identity must not grant group or other access")
	}
	return nil
}

func (runtime *productionReleaseRuntime) remoteDirectoryExists(ctx context.Context, alias, path string) (bool, error) {
	if _, err := runtime.ssh(ctx, alias, 2*time.Minute, "test", "-d", path); err == nil {
		return true, nil
	}
	if _, err := runtime.ssh(ctx, alias, 2*time.Minute, "test", "!", "-e", path); err == nil {
		return false, nil
	}
	return false, fmt.Errorf("remote directory is unsafe or could not be inspected: %s", path)
}

func loadProductionReleaseControllerState(plan productionReleasePlan, planSHA256 string) (productionReleaseControllerState, bool, error) {
	path := filepath.Join(plan.ControllerWorkspace, productionReleaseStateFilename)
	raw, err := readPrivateRegularFile(path, 1<<20)
	if errors.Is(err, os.ErrNotExist) {
		return productionReleaseControllerState{}, false, nil
	}
	if err != nil {
		return productionReleaseControllerState{}, false, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var state productionReleaseControllerState
	if err := decoder.Decode(&state); err != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return productionReleaseControllerState{}, false, errors.New("controller release state is invalid")
	}
	canonical, err := canonicalProductionReleaseControllerState(state)
	if err != nil || !bytes.Equal(canonical, raw) {
		return productionReleaseControllerState{}, false, errors.New("controller release state is not canonical JSON")
	}
	migrated := false
	if state.Format == 1 {
		if state.ActivationUnit != "" || state.DispatchAttempts != 0 || state.DispatchObserved {
			return productionReleaseControllerState{}, false, errors.New("legacy controller release state contains unsupported dispatch fields")
		}
		state.Format = productionReleaseStateFormat
		if state.Phase == productionReleasePhaseActivationDispatched {
			state.ActivationUnit = productionActivationUnit(plan.DeploymentID)
			state.DispatchAttempts = 1
		}
		migrated = true
	}
	if err := validateProductionReleaseControllerState(plan, planSHA256, state); err != nil {
		return productionReleaseControllerState{}, false, err
	}
	if migrated {
		if err := writeProductionReleaseControllerState(plan, state); err != nil {
			return productionReleaseControllerState{}, false, fmt.Errorf("migrate controller release state: %w", err)
		}
	}
	return state, true, nil
}

func writeProductionReleaseControllerState(plan productionReleasePlan, state productionReleaseControllerState) error {
	if err := validateProductionReleaseControllerState(plan, state.PlanSHA256, state); err != nil {
		return err
	}
	encoded, err := canonicalProductionReleaseControllerState(state)
	if err != nil {
		return err
	}
	return writeAtomicRegularFile(filepath.Join(plan.ControllerWorkspace, productionReleaseStateFilename), encoded, 0o600)
}

func canonicalProductionReleaseControllerState(state productionReleaseControllerState) ([]byte, error) {
	encoded, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(encoded, '\n'), nil
}

func validateProductionReleaseControllerState(plan productionReleasePlan, planSHA256 string, state productionReleaseControllerState) error {
	if state.Format != productionReleaseStateFormat || state.DeploymentID != plan.DeploymentID || state.PlanSHA256 != planSHA256 || !productionSHA256Pattern.MatchString(state.PlanSHA256) {
		return errors.New("controller release state identity is invalid")
	}
	phases := map[string]bool{
		productionReleasePhaseWorkspaceCreated:     true,
		productionReleasePhaseStaged:               true,
		productionReleasePhaseBackupsReady:         true,
		productionReleasePhaseActivationDispatched: true,
		"PREPARING":             true,
		"ARMING":                true,
		"ARMED":                 true,
		"MIGRATING":             true,
		"DEPLOYING_GO":          true,
		"DEPLOYING_WEB":         true,
		"AWAITING_CONFIRMATION": true,
		"CONFIRMED":             true,
		"ROLLED_BACK":           true,
		"FAILED_PREARM":         true,
		"ROLLBACK_FAILED":       true,
		"ABORTED":               true,
	}
	if !phases[state.Phase] {
		return errors.New("controller release state phase is invalid")
	}
	expectedWorkspace := filepath.Join(defaultProductionPaths().WorkRoot, plan.DeploymentID)
	if state.RemoteWorkspace != expectedWorkspace {
		return errors.New("controller release state workspace is invalid")
	}
	if state.UpdatedUTC.IsZero() || state.UpdatedUTC.Location() != time.UTC || state.UpdatedUTC.Nanosecond() != 0 {
		return errors.New("controller release state timestamp is invalid")
	}
	if state.DispatchAttempts < 0 || state.DispatchAttempts > 2 ||
		(state.DispatchObserved && state.DispatchAttempts == 0) {
		return errors.New("controller release state dispatch attempts are invalid")
	}
	if state.ActivationUnit != "" && state.ActivationUnit != productionActivationUnit(plan.DeploymentID) {
		return errors.New("controller release state activation unit is invalid")
	}
	if (state.DispatchAttempts > 0) != (state.ActivationUnit != "") {
		return errors.New("controller release state dispatch identity is incomplete")
	}
	if state.Phase == productionReleasePhaseActivationDispatched && state.DispatchAttempts == 0 {
		return errors.New("controller release state activation phase lacks a dispatch attempt")
	}
	for _, path := range []string{state.TargetBackup, state.ControllerBackup, state.OffhostBackup} {
		if path != "" && !filepath.IsAbs(path) {
			return errors.New("controller release state contains a non-absolute backup path")
		}
	}
	return nil
}

func releaseControllerResult(plan productionReleasePlan, state productionReleaseControllerState) productionReleaseControllerResult {
	return productionReleaseControllerResult{
		DeploymentID:     plan.DeploymentID,
		PlanSHA256:       state.PlanSHA256,
		Version:          plan.ExpectedVersion,
		Revision:         plan.GoCandidate.GitRevision,
		Status:           state.Phase,
		TargetBackup:     state.TargetBackup,
		ControllerBackup: state.ControllerBackup,
		OffhostBackup:    state.OffhostBackup,
		RollbackTimer:    state.RollbackTimer,
		ActivationUnit:   state.ActivationUnit,
		DispatchAttempts: state.DispatchAttempts,
		Workspace:        state.RemoteWorkspace,
	}
}

func (runtime *productionReleaseRuntime) remoteGoPackage(ctx context.Context) (string, error) {
	installed := ""
	for _, name := range []string{productionAURPackageName, productionSourcePackageName} {
		output, err := runtime.ssh(ctx, productionTargetAlias, 2*time.Minute, "pacman", "-Q", name)
		if err != nil {
			continue
		}
		_, _, identity, err := parseProductionPackageIdentity(output)
		if err != nil {
			return "", errors.New("production Go package identity is invalid")
		}
		if installed != "" {
			if installed == identity {
				continue
			}
			return "", errors.New("multiple production Go packages are installed")
		}
		installed = identity
	}
	if installed == "" {
		return "", errors.New("production Go package was not found")
	}
	return installed, nil
}

func (runtime *productionReleaseRuntime) assertRemoteHost(ctx context.Context, alias, expected string) error {
	output, err := runtime.ssh(ctx, alias, 2*time.Minute, "hostnamectl", "--static")
	if err != nil {
		return fmt.Errorf("verify %s host identity: %w", alias, err)
	}
	if strings.TrimSpace(string(output)) != expected {
		return fmt.Errorf("%s host identity mismatch: got %q", alias, strings.TrimSpace(string(output)))
	}
	return nil
}

func (runtime *productionReleaseRuntime) ssh(ctx context.Context, alias string, timeout time.Duration, arguments ...string) ([]byte, error) {
	args := []string{"-o", "BatchMode=yes", alias}
	args = append(args, arguments...)
	return runtime.runner.Run(ctx, productionCommand{Name: commandSSH, Args: args, Timeout: timeout})
}

func (runtime *productionReleaseRuntime) scpTo(ctx context.Context, local, alias, remote string) error {
	_, err := runtime.runner.Run(ctx, productionCommand{Name: commandSCP, Args: []string{"-q", "-p", "--", local, alias + ":" + remote}, Timeout: 10 * time.Minute})
	return err
}

func (runtime *productionReleaseRuntime) scpToRecursive(ctx context.Context, local, alias, remote string) error {
	_, err := runtime.runner.Run(ctx, productionCommand{Name: commandSCP, Args: []string{"-q", "-p", "-r", "--", local, alias + ":" + remote}, Timeout: 10 * time.Minute})
	return err
}

func (runtime *productionReleaseRuntime) scpFrom(ctx context.Context, alias, remote, local string) error {
	if _, err := os.Lstat(local); !errors.Is(err, os.ErrNotExist) {
		return errors.New("local transfer destination already exists or is unsafe")
	}
	if err := ensureRealDirectory(filepath.Dir(local), 0o700); err != nil {
		return err
	}
	_, err := runtime.runner.Run(ctx, productionCommand{Name: commandSCP, Args: []string{"-q", "-p", "-r", "--", alias + ":" + remote, local}, Timeout: 10 * time.Minute})
	return err
}
