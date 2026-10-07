//go:build linux

package appcli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// This recovery is deliberately bound to the single ordinary Arch writer whose
// bounded stop was committed before maintenanceStop rejected its whole history.
// It is a separate operator entry point, never a business/Dispatch action.
type maintenanceStoppedFinalizeLock struct {
	Path          string
	Device, Inode uint64
}

type maintenanceStoppedFinalizeFacts struct {
	DeploymentID, ManifestSHA256, StatusSHA256       string
	PID                                              int
	InvocationID                                     string
	StopStartedUTC                                   time.Time
	BoundedJournalSHA256, WholeJournalSHA256         string
	HandoffPath, HandoffSHA256                       string
	AllAdmissionClosedPath, AllAdmissionClosedSHA256 string
	GuardianUnit, GuardianUnitSHA256                 string
	GuardianPID                                      int
	GuardianInvocationID                             string
	Locks                                            [3]maintenanceStoppedFinalizeLock
}

func archMaintenanceStoppedFinalizeFacts() maintenanceStoppedFinalizeFacts {
	const handoffSHA = "a86394656da914b372c11e7609d25f4a26d9cb58910333a2368261a14e6d8205"
	return maintenanceStoppedFinalizeFacts{
		DeploymentID:   "credit-financial-20261006-arch-capture",
		ManifestSHA256: "d2bd45ba76275dadb902012e7e8b8c41e1f669dda5d44e7e18c16f68262abc1c",
		StatusSHA256:   "3f18d2374d902d8221c191a1dd43b9c4d205798f8f2f85266a084b7445d93aa1",
		PID:            707877, InvocationID: "9d7b5b339f6b4daead5136c55de0b157",
		StopStartedUTC:       time.Date(2026, 10, 6, 0, 31, 50, 148351603, time.UTC),
		BoundedJournalSHA256: "12c15f37e8bf69b6d813c474ebf2d00d154002f35cce1497f870ce6ea701b2d0",
		WholeJournalSHA256:   "319f6e623941df5a1f99ad32db4fb54dc95bddf40510084e60f07e9fef1e2fc2",
		HandoffPath:          "/var/lib/lmm-api-go-deploy/handoffs/" + handoffSHA + ".json", HandoffSHA256: handoffSHA,
		AllAdmissionClosedPath:   "/var/lib/lmm-credit-transition/credit-financial-20261006/all-admission-closed.json",
		AllAdmissionClosedSHA256: "5322fc58992aae88e27184c617f1efca76510752921eb0c5fed6a1f4596ad7cf",
		GuardianUnit:             "lmm-credit-financial-20261006-arch.service",
		GuardianUnitSHA256:       "2c94a7abffd515235602f2870569de22dfd0e49e2039866f68ed989ae33e7a93",
		GuardianPID:              745014, GuardianInvocationID: "c89ffc89bedc49d5b5103b461d17a322",
		Locks: [3]maintenanceStoppedFinalizeLock{
			{Path: "/run/lock/lmm-api-go-deploy.lock", Device: 28, Inode: 2627},
			{Path: "/var/lib/lmm-api-deploy-systemd/lock", Device: 65025, Inode: 288211},
			{Path: "/srv/lmm-api-frontend/.release.lock", Device: 65025, Inode: 3490},
		},
	}
}

// RunMaintenanceStoppedFinalize is called only by the private recovery operator.
// The caller cannot replace the incident identities, paths, or evidence hashes.
func RunMaintenanceStoppedFinalize(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("operator production maintenance finalize-stopped", flag.ContinueOnError)
	flags.SetOutput(stderr)
	execute := flags.Bool("execute", false, "finalize the exactly bound stopped Arch owner")
	confirm := flags.String("confirm", "", "required execution confirmation: api.lmm.best")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return ExitOK
		}
		return ExitUsage
	}
	if flags.NArg() != 0 || (*execute && *confirm != "api.lmm.best") || (!*execute && *confirm != "") {
		fmt.Fprintln(stderr, "finalize-stopped: --execute requires --confirm api.lmm.best; default is precheck")
		return ExitUsage
	}
	runtime := defaultProductionRuntime()
	status, err := runtime.runMaintenanceStoppedFinalize(context.Background(), archMaintenanceStoppedFinalizeFacts(), *execute)
	if err != nil {
		fmt.Fprintf(stderr, "finalize-stopped: %v\n", err)
		return ExitError
	}
	mode := "precheck"
	if *execute {
		mode = "executed"
	}
	if err := json.NewEncoder(stdout).Encode(struct {
		Mode   string           `json:"mode"`
		Status productionStatus `json:"status"`
	}{mode, status}); err != nil {
		return ExitError
	}
	return ExitOK
}

type maintenanceStoppedFinalizeEvidence struct {
	workspace                                                    productionWorkspace
	manifest                                                     productionManifest
	status                                                       productionStatus
	manifestRaw, statusRaw, captureRaw, handoffRaw, allClosedRaw []byte
	boundedRaw, wholeRaw                                         []byte
	lineagePath                                                  string
}

func (runtime *productionRuntime) runMaintenanceStoppedFinalize(ctx context.Context, facts maintenanceStoppedFinalizeFacts, execute bool) (productionStatus, error) {
	if err := runtime.maintenanceStoppedFinalizeAuthority(facts); err != nil {
		return productionStatus{}, err
	}
	if err := runtime.setMaintenanceHandoff(facts.HandoffPath, facts.HandoffSHA256); err != nil {
		return productionStatus{}, err
	}
	if err := runtime.maintenanceStoppedFinalizeGuardian(ctx, facts); err != nil {
		return productionStatus{}, err
	}
	runtime.guardianAdoptAll = true
	lock, err := runtime.acquireGlobalLock(ctx)
	if err != nil {
		return productionStatus{}, err
	}
	// Adopted OFDs must only be closed; the original guardian retains its locks.
	defer runtime.releaseGlobalLock(lock)
	return runtime.maintenanceStoppedFinalize(ctx, facts, execute, lock)
}

// The private facts parameter allows isolated fixtures, never operator overrides.
func (runtime *productionRuntime) maintenanceStoppedFinalize(ctx context.Context, facts maintenanceStoppedFinalizeFacts, execute bool, lock *os.File) (status productionStatus, resultErr error) {
	originalRunner := runtime.runner
	runtime.runner = maintenanceStoppedFinalizeRunner{runner: originalRunner, facts: facts, service: runtime.paths.Service}
	defer func() { runtime.runner = originalRunner }()
	evidence, err := runtime.maintenanceStoppedFinalizePrecheck(ctx, facts, lock)
	if err != nil {
		return productionStatus{}, err
	}
	if !execute {
		return evidence.status, nil
	}
	if err := runtime.maintenanceStoppedFinalizeCAS(ctx, facts, lock, evidence); err != nil {
		return productionStatus{}, err
	}
	if err := os.Mkdir(evidence.lineagePath, 0700); err != nil {
		return productionStatus{}, fmt.Errorf("claim exclusive finalize lineage: %w", err)
	}
	defer func() {
		if resultErr != nil {
			// A claimed lineage always remains visible and refuses blind replay.
			body, _ := json.Marshal(struct {
				Format  string `json:"format"`
				Failure string `json:"failure"`
			}{"lmm-maintenance-stopped-finalize-failure-v1", resultErr.Error()})
			_ = maintenanceStoppedFinalizeExclusiveFile(filepath.Join(evidence.lineagePath, "failure.json"), append(body, '\n'))
		}
	}()
	if err := syncDirectory(evidence.workspace.root); err != nil {
		return productionStatus{}, err
	}
	intent, err := json.MarshalIndent(struct {
		Format               string `json:"format"`
		DeploymentID         string `json:"deployment_id"`
		ManifestSHA256       string `json:"manifest_sha256"`
		StatusSHA256         string `json:"status_sha256"`
		BoundedJournalSHA256 string `json:"bounded_journal_sha256"`
		WholeJournalSHA256   string `json:"whole_journal_sha256"`
	}{"lmm-maintenance-stopped-finalize-intent-v1", facts.DeploymentID, facts.ManifestSHA256, facts.StatusSHA256, facts.BoundedJournalSHA256, facts.WholeJournalSHA256}, "", "  ")
	if err != nil {
		return productionStatus{}, err
	}
	if err := runtime.maintenanceStoppedFinalizeCAS(ctx, facts, lock, evidence); err != nil {
		return productionStatus{}, err
	}
	if err := maintenanceStoppedFinalizeExclusiveFile(filepath.Join(evidence.lineagePath, "intent.json"), append(intent, '\n')); err != nil {
		return productionStatus{}, err
	}
	archives := []struct {
		name string
		body []byte
	}{
		{"manifest.before.json", evidence.manifestRaw}, {"status.before.json", evidence.statusRaw},
		{"capture.before.json", evidence.captureRaw}, {"handoff.original.json", evidence.handoffRaw},
		{"all-admission-closed.original.json", evidence.allClosedRaw},
		{"whole-invocation.original.log", evidence.wholeRaw}, {"bounded-shutdown.original.log", evidence.boundedRaw},
	}
	for _, archive := range archives {
		if err := runtime.maintenanceStoppedFinalizeCAS(ctx, facts, lock, evidence); err != nil {
			return productionStatus{}, err
		}
		if err := maintenanceStoppedFinalizeExclusiveFile(filepath.Join(evidence.lineagePath, archive.name), archive.body); err != nil {
			return productionStatus{}, err
		}
	}

	// Repeat the original stop verification, including refund/package ownership
	// rules. Its serialization must preserve the already committed manifest bytes.
	if err := runtime.maintenanceStoppedFinalizeCAS(ctx, facts, lock, evidence); err != nil {
		return productionStatus{}, err
	}
	verifyErr := runtime.verifyBillingStopJournal(ctx, evidence.workspace, &evidence.manifest)
	if verifyErr != nil {
		return productionStatus{}, verifyErr
	}
	if err := runtime.maintenanceStoppedFinalizeCAS(ctx, facts, lock, evidence); err != nil {
		return productionStatus{}, err
	}
	if err := maintenanceStoppedFinalizeExclusiveFile(filepath.Join(evidence.workspace.root, "maintenance-shutdown.log"), evidence.boundedRaw); err != nil {
		return productionStatus{}, err
	}
	if err := runtime.maintenanceStoppedFinalizeCAS(ctx, facts, lock, evidence); err != nil {
		return productionStatus{}, err
	}
	status, err = runtime.persistMaintenanceCapture(evidence.workspace, evidence.manifest, "FROZEN")
	if err != nil {
		return productionStatus{}, err
	}
	if err := runtime.maintenanceStoppedFinalizeCompletion(ctx, facts, lock, evidence, status); err != nil {
		return productionStatus{}, err
	}
	return status, nil
}

func (runtime *productionRuntime) maintenanceStoppedFinalizeAuthority(facts maintenanceStoppedFinalizeFacts) error {
	if runtime.effectiveUID() != 0 || runtime.requiredOwnerUID != 0 {
		return errors.New("finalize-stopped requires root authority")
	}
	host, err := runtime.hostname()
	if err != nil || host != runtime.paths.ExpectedHost {
		return errors.New("finalize-stopped production host identity mismatch")
	}
	if runtime.paths.PublicBaseURL != "https://api.lmm.best" || runtime.paths.LocalBaseURL != "http://127.0.0.1:3000" || facts != archMaintenanceStoppedFinalizeFacts() {
		return errors.New("finalize-stopped production facts differ from the sealed Arch incident")
	}
	return nil
}

func (runtime *productionRuntime) maintenanceStoppedFinalizeGuardian(ctx context.Context, facts maintenanceStoppedFinalizeFacts) error {
	path := filepath.Join(runtime.paths.SystemdUnitRoot, facts.GuardianUnit)
	if err := runtime.requireOwnedSafePath(path, false); err != nil {
		return fmt.Errorf("finalize guardian unit file: %w", err)
	}
	hash, err := sha256File(path)
	if err != nil || hash != facts.GuardianUnitSHA256 {
		return errors.New("finalize guardian unit bytes changed")
	}
	state, err := runtime.billingUnitState(ctx, facts.GuardianUnit)
	if err != nil || state["MainPID"] != strconv.Itoa(facts.GuardianPID) || state["InvocationID"] != facts.GuardianInvocationID || state["ActiveState"] != "active" || state["SubState"] != "running" || state["Result"] != "success" {
		return errors.New("finalize guardian generation changed")
	}
	return nil
}

func (runtime *productionRuntime) maintenanceStoppedFinalizeLocks(facts maintenanceStoppedFinalizeFacts, lock *os.File) error {
	if lock == nil || runtime.guardianLease == nil || len(runtime.guardianExtraLocks) != 2 || !runtime.guardianAdoptAll {
		return errors.New("finalize requires all three adopted guardian OFDs")
	}
	connection, ok := runtime.guardianLease.(*net.UnixConn)
	if !ok {
		return errors.New("finalize guardian lease is not the original Unix connection")
	}
	raw, err := connection.SyscallConn()
	if err != nil {
		return err
	}
	var peer *unix.Ucred
	var peerErr error
	if err := raw.Control(func(fd uintptr) { peer, peerErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED) }); err != nil || peerErr != nil || peer == nil || peer.Uid != runtime.requiredOwnerUID || int(peer.Pid) != facts.GuardianPID {
		return errors.New("finalize guardian Unix peer changed")
	}
	files := append([]*os.File{lock}, runtime.guardianExtraLocks...)
	for index, file := range files {
		if file == nil {
			return errors.New("finalize guardian descriptor missing")
		}
		info, err := file.Stat()
		if err != nil {
			return err
		}
		current, err := os.Lstat(facts.Locks[index].Path)
		if err != nil {
			return err
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		uid, links, owned := deploymentFileOwnership(info)
		if !ok || !owned || uid != runtime.requiredOwnerUID || links != 1 || !info.Mode().IsRegular() || current.Mode()&os.ModeSymlink != 0 || !os.SameFile(info, current) || uint64(stat.Dev) != facts.Locks[index].Device || stat.Ino != facts.Locks[index].Inode {
			return errors.New("finalize adopted lock inode differs from frozen facts")
		}
	}
	if facts.Locks[0].Path != runtime.paths.GlobalLock {
		return errors.New("finalize native lock path differs")
	}
	return nil
}

func (runtime *productionRuntime) maintenanceStoppedFinalizeStopped(ctx context.Context, facts maintenanceStoppedFinalizeFacts, manifest productionManifest) error {
	capture, gate := manifest.MaintenanceCapture, manifest.BillingGate
	if capture == nil || gate == nil || capture.WasMaintenanceConfirmed || capture.PID != facts.PID || capture.InvocationID != facts.InvocationID || gate.GoPID != facts.PID || gate.GoInvocationID != facts.InvocationID || !gate.StopVerified || !gate.AdmissionClosed || gate.AdmissionReopened || gate.Sequence <= 0 || !gate.StopStartedUTC.Equal(facts.StopStartedUTC) || gate.ShutdownJournalSHA256 != facts.BoundedJournalSHA256 {
		return errors.New("finalize captured stopped writer or closed admission facts differ")
	}
	state, err := runtime.billingUnitState(ctx, runtime.paths.Service)
	if err != nil {
		return err
	}
	if state["InvocationID"] != facts.InvocationID {
		return errors.New("finalize inactive writer invocation changed")
	}
	return cleanBillingUnitExit(state, facts.PID)
}

func maintenanceStoppedFinalizeAbsent(path string) error {
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		return errors.New("finalize destination or previous attempt already exists")
	}
	return nil
}

func (runtime *productionRuntime) maintenanceStoppedFinalizePrecheck(ctx context.Context, facts maintenanceStoppedFinalizeFacts, lock *os.File) (*maintenanceStoppedFinalizeEvidence, error) {
	if err := runtime.maintenanceStoppedFinalizeLocks(facts, lock); err != nil {
		return nil, err
	}
	if err := runtime.maintenanceStoppedFinalizeGuardian(ctx, facts); err != nil {
		return nil, err
	}
	if runtime.maintenanceHandoff == nil || runtime.maintenanceHandoff.Path != facts.HandoffPath || runtime.maintenanceHandoff.SHA256 != facts.HandoffSHA256 || runtime.maintenanceHandoff.Stage != "prebridge" || runtime.maintenanceHandoff.StoppedWriter != nil {
		return nil, errors.New("finalize requires the unchanged original ordinary prebridge handoff")
	}
	workspace, err := runtime.openWorkspaceForInspection(filepath.Join(runtime.paths.WorkRoot, facts.DeploymentID))
	if err != nil {
		return nil, err
	}
	e := &maintenanceStoppedFinalizeEvidence{workspace: workspace, lineagePath: filepath.Join(workspace.root, "maintenance-stopped-finalize-20261006")}
	for _, path := range []string{e.lineagePath, filepath.Join(workspace.root, "maintenance-shutdown.log"), filepath.Join(workspace.stateDir, "maintenance-capture.FROZEN.json")} {
		if err := maintenanceStoppedFinalizeAbsent(path); err != nil {
			return nil, err
		}
	}
	owner := runtime.requiredOwnerUID
	e.manifestRaw, err = readMaintenanceBoundFile(workspace.manifestPath, facts.ManifestSHA256, owner)
	if err != nil {
		return nil, fmt.Errorf("finalize original manifest: %w", err)
	}
	e.statusRaw, err = readMaintenanceBoundFile(workspace.statusPath, facts.StatusSHA256, owner)
	if err != nil {
		return nil, fmt.Errorf("finalize original status: %w", err)
	}
	e.manifest, err = runtime.readManifest(workspace)
	if err != nil {
		return nil, err
	}
	e.status, err = runtime.readStatus(workspace)
	if err != nil {
		return nil, err
	}
	h := runtime.maintenanceHandoff
	if e.status.Phase != "ADMISSION_CLOSED" || e.status.MaintenanceConfirmation || e.status.TransitionID != h.TransitionID || e.status.TransitionIntentSHA256 != h.TransitionIntentSHA256 || e.status.HandoffSHA256 != facts.HandoffSHA256 || e.status.MaintenanceStage != "prebridge" || e.manifest.MaintenanceHandoff == nil {
		return nil, errors.New("finalize original owner status is not the bound admission-closed capture")
	}
	left, right := *e.manifest.MaintenanceHandoff, *h
	left.Path, right.Path = "", ""
	if !reflect.DeepEqual(left, right) {
		return nil, errors.New("finalize manifest handoff fields changed")
	}
	if err := runtime.maintenanceStoppedFinalizeStopped(ctx, facts, e.manifest); err != nil {
		return nil, err
	}
	if err := runtime.validateTransactionLock(workspace); err != nil {
		return nil, err
	}
	e.handoffRaw, err = readMaintenanceBoundFile(facts.HandoffPath, facts.HandoffSHA256, owner)
	if err != nil {
		return nil, err
	}
	e.allClosedRaw, err = readMaintenanceBoundFile(facts.AllAdmissionClosedPath, facts.AllAdmissionClosedSHA256, owner)
	if err != nil {
		return nil, err
	}
	var all struct {
		Format, TransitionID, TransitionIntentSHA256 string
		AllOriginsClosed                             bool
	}
	// Receipt field spelling is the original maintenanceStop contract.
	var closure struct {
		Format                 string `json:"format"`
		TransitionID           string `json:"transition_id"`
		TransitionIntentSHA256 string `json:"transition_intent_sha256"`
		AllOriginsClosed       bool   `json:"all_origins_closed"`
	}
	_ = all
	if json.Unmarshal(e.allClosedRaw, &closure) != nil || closure.Format != "lmm-credit-all-admission-closed-v1" || closure.TransitionID != h.TransitionID || closure.TransitionIntentSHA256 != h.TransitionIntentSHA256 || !closure.AllOriginsClosed {
		return nil, errors.New("finalize all-origin admission closure changed")
	}
	if e.status.CaptureReceiptPath != filepath.Join(workspace.stateDir, "maintenance-capture.ADMISSION_CLOSED.json") {
		return nil, errors.New("finalize capture receipt path changed")
	}
	e.captureRaw, err = readMaintenanceBoundFile(e.status.CaptureReceiptPath, e.status.CaptureReceiptSHA256, owner)
	if err != nil {
		return nil, err
	}
	var saved productionMaintenanceCapture
	if err := json.Unmarshal(e.captureRaw, &saved); err != nil {
		return nil, err
	}
	expected := *e.manifest.MaintenanceCapture
	expected.Phase = "ADMISSION_CLOSED"
	if !reflect.DeepEqual(saved, expected) || saved.Format != "lmm-credit-maintenance-capture-v1" || saved.TransitionID != h.TransitionID || saved.TransitionIntentSHA256 != h.TransitionIntentSHA256 {
		return nil, errors.New("finalize ordinary capture receipt changed")
	}
	if err := runtime.maintenanceStoppedFinalizeEnvironment(workspace, e.manifest); err != nil {
		return nil, err
	}
	if saved.FrontendTarget != e.manifest.Frontend.OldTarget || saved.FrontendSHA256 != e.manifest.Frontend.OldIndexSHA256 {
		return nil, errors.New("finalize captured frontend differs from the original manifest")
	}
	if err := verifyFrontendIdentity(runtime.paths.FrontendRoot, saved.FrontendTarget, saved.FrontendSHA256); err != nil {
		return nil, err
	}
	if err := runtime.maintenanceStoppedFinalizeBarrier(workspace, e.manifest); err != nil {
		return nil, err
	}
	// The original probe helper writes only into a disposable root-private directory.
	probeRoot, err := os.MkdirTemp("", "lmm-maintenance-finalize-probe-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(probeRoot)
	if err := runtime.probeMaintenanceAdmission(ctx, productionWorkspace{root: probeRoot}, runtime.paths.InstalledBinary); err != nil {
		return nil, err
	}
	if err := runtime.verifyNoUntrackedRefunds(ctx, &e.manifest); err != nil {
		return nil, err
	}
	e.boundedRaw, err = runtime.runner.Run(ctx, maintenanceStoppedFinalizeBoundedCommand(runtime.paths.Service, facts))
	if err != nil {
		return nil, err
	}
	if err := validateBillingShutdownJournal(e.boundedRaw); err != nil {
		return nil, err
	}
	tracked, err := runtime.trackedRefundWriter(ctx, &e.manifest)
	if err != nil {
		return nil, err
	}
	if tracked && !bytes.Contains(bytes.ToLower(e.boundedRaw), []byte("refund_tasks execution_complete=")) {
		return nil, errors.New("tracked writer shutdown lacks its refund completion report")
	}
	e.wholeRaw, err = runtime.runner.Run(ctx, productionCommand{Name: commandJournalctl, Args: []string{"--no-pager", "--output=cat", "_PID=" + strconv.Itoa(facts.PID), "_SYSTEMD_INVOCATION_ID=" + facts.InvocationID}, Sensitive: true, OutputLimit: 8 << 20})
	if err != nil {
		return nil, err
	}
	if maintenanceStoppedFinalizeSHA(e.boundedRaw) != facts.BoundedJournalSHA256 || maintenanceStoppedFinalizeSHA(e.wholeRaw) != facts.WholeJournalSHA256 || !bytes.HasSuffix(e.wholeRaw, e.boundedRaw) {
		return nil, errors.New("finalize original whole or bounded shutdown bytes changed")
	}
	canonical, err := maintenanceStoppedFinalizeManifestBytes(workspace, e.manifest)
	if err != nil || !bytes.Equal(canonical, e.manifestRaw) {
		return nil, errors.New("original stop verifier would change sealed manifest serialization")
	}
	if err := runtime.maintenanceStoppedFinalizeCAS(ctx, facts, lock, e); err != nil {
		return nil, err
	}
	return e, nil
}

func maintenanceStoppedFinalizeSHA(content []byte) string {
	return fmt.Sprintf("%x", sha256Bytes(content))
}

func maintenanceStoppedFinalizeManifestBytes(workspace productionWorkspace, manifest productionManifest) ([]byte, error) {
	manifest.Format, manifest.DeploymentID = productionTransactionFormat, workspace.id
	content, err := json.MarshalIndent(manifest, "", "  ")
	return append(content, '\n'), err
}

func maintenanceStoppedFinalizeBoundedCommand(service string, facts maintenanceStoppedFinalizeFacts) productionCommand {
	return productionCommand{Name: commandJournalctl, Args: []string{"--no-pager", "--output=cat", "--since", fmt.Sprintf("@%d.%06d", facts.StopStartedUTC.Unix(), facts.StopStartedUTC.Nanosecond()/1000), "-u", service, "_PID=" + strconv.Itoa(facts.PID), "_SYSTEMD_INVOCATION_ID=" + facts.InvocationID}, Sensitive: true, OutputLimit: 8 << 20}
}

func (runtime *productionRuntime) maintenanceStoppedFinalizeEnvironment(workspace productionWorkspace, manifest productionManifest) error {
	c := manifest.MaintenanceCapture
	if c.ArchivedEnvironmentPath != filepath.Join(workspace.configRestore, "lmm-api-go.env") || c.ArchivedEnvironmentSHA256 != manifest.EnvironmentRestoreSHA256 || c.ProcessEnvironmentPath != filepath.Join(workspace.stateDir, "maintenance-process-"+c.InvocationID+".environment") {
		return errors.New("finalize captured environment binding changed")
	}
	archived, err := readMaintenanceBoundFile(c.ArchivedEnvironmentPath, c.ArchivedEnvironmentSHA256, runtime.requiredOwnerUID)
	if err != nil {
		return err
	}
	if _, err := readMaintenanceBoundFile(filepath.Join(runtime.paths.ConfigDir, "lmm-api-go.env"), c.ArchivedEnvironmentSHA256, runtime.requiredOwnerUID); err != nil {
		return err
	}
	process, err := readMaintenanceBoundFile(c.ProcessEnvironmentPath, c.ProcessEnvironmentSHA256, runtime.requiredOwnerUID)
	if err != nil {
		return err
	}
	configuration, err := parseProductionEnvironment(archived)
	if err != nil {
		return err
	}
	configuredDSN, err := productionDatabaseURL(configuration)
	if err != nil {
		return err
	}
	values := map[string]string{}
	for _, entry := range bytes.Split(process, []byte{0}) {
		if len(entry) == 0 {
			continue
		}
		key, value, ok := strings.Cut(string(entry), "=")
		if !ok || key == "" {
			return errors.New("finalize captured process environment malformed")
		}
		if _, duplicate := values[key]; duplicate {
			return errors.New("finalize captured process environment key duplicated")
		}
		values[key] = value
	}
	processDSN, err := productionDatabaseURL(values)
	if err != nil || processDSN != configuredDSN {
		return errors.New("finalize captured process database differs from archived configuration")
	}
	return nil
}

func (runtime *productionRuntime) maintenanceStoppedFinalizeBarrier(workspace productionWorkspace, manifest productionManifest) error {
	gate := manifest.BillingGate
	originalPath := filepath.Join(workspace.root, fmt.Sprintf("billing-locations.%d", gate.Sequence))
	original, err := readMaintenanceBoundFile(originalPath, gate.OriginalSHA256, runtime.requiredOwnerUID)
	if err != nil {
		return err
	}
	expected, err := runtime.billingBarrier(original, workspace.id)
	if err != nil {
		return err
	}
	path := filepath.Join(runtime.paths.NginxRoot, "lmm-api-locations.conf")
	if err := runtime.requireOwnedSafePath(path, false); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil || info.Mode().Perm() != 0644 {
		return errors.New("finalize ingress barrier mode changed")
	}
	current, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(current, expected) {
		return errors.New("finalize ingress barrier changed")
	}
	return nil
}

func (runtime *productionRuntime) maintenanceStoppedFinalizeCAS(ctx context.Context, facts maintenanceStoppedFinalizeFacts, lock *os.File, e *maintenanceStoppedFinalizeEvidence) error {
	if err := runtime.maintenanceStoppedFinalizeLocks(facts, lock); err != nil {
		return err
	}
	if err := runtime.maintenanceStoppedFinalizeGuardian(ctx, facts); err != nil {
		return err
	}
	if err := runtime.maintenanceStoppedFinalizeStopped(ctx, facts, e.manifest); err != nil {
		return err
	}
	for _, bound := range []struct{ path, hash string }{
		{e.workspace.manifestPath, facts.ManifestSHA256}, {e.workspace.statusPath, facts.StatusSHA256},
		{e.status.CaptureReceiptPath, e.status.CaptureReceiptSHA256},
		{facts.HandoffPath, facts.HandoffSHA256}, {facts.AllAdmissionClosedPath, facts.AllAdmissionClosedSHA256},
	} {
		if _, err := readMaintenanceBoundFile(bound.path, bound.hash, runtime.requiredOwnerUID); err != nil {
			return fmt.Errorf("finalize owner raw compare failed: %w", err)
		}
	}
	if err := runtime.validateTransactionLock(e.workspace); err != nil {
		return err
	}
	if err := runtime.maintenanceStoppedFinalizeEnvironment(e.workspace, e.manifest); err != nil {
		return err
	}
	if err := runtime.maintenanceStoppedFinalizeBarrier(e.workspace, e.manifest); err != nil {
		return err
	}
	return verifyFrontendIdentity(runtime.paths.FrontendRoot, e.manifest.MaintenanceCapture.FrontendTarget, e.manifest.MaintenanceCapture.FrontendSHA256)
}

func maintenanceStoppedFinalizeExclusiveFile(path string, content []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	if _, err = file.Write(content); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return syncDirectory(filepath.Dir(path))
}

type maintenanceStoppedFinalizeRunner struct {
	runner  productionCommandRunner
	facts   maintenanceStoppedFinalizeFacts
	service string
}

func (runner maintenanceStoppedFinalizeRunner) Run(ctx context.Context, command productionCommand) ([]byte, error) {
	if len(command.Args) == 0 {
		return nil, errors.New("finalize refuses an empty external command")
	}
	switch command.Name {
	case commandSystemctl:
		if command.Args[0] != "show" {
			return nil, errors.New("finalize forbids service mutations")
		}
	case commandPacman:
		if !strings.HasPrefix(command.Args[0], "-Q") {
			return nil, errors.New("finalize forbids package mutations")
		}
	case commandRunuser:
		if len(command.Args) < 5 || !reflect.DeepEqual(command.Args[:3], []string{"--user", "root", "--"}) || command.Args[4] != "request" {
			return nil, errors.New("finalize permits only read-only admission requests")
		}
	case commandJournalctl:
		command.Sensitive = true
		if command.OutputLimit <= 0 {
			command.OutputLimit = 8 << 20
		}
	default:
		return nil, errors.New("finalize forbids this external command")
	}
	output, err := runner.runner.Run(ctx, command)
	if err != nil {
		return nil, err
	}
	if command.Name == commandJournalctl {
		bounded := maintenanceStoppedFinalizeBoundedCommand(runner.service, runner.facts)
		whole := []string{"--no-pager", "--output=cat", "_PID=" + strconv.Itoa(runner.facts.PID), "_SYSTEMD_INVOCATION_ID=" + runner.facts.InvocationID}
		if reflect.DeepEqual(command.Args, bounded.Args) && maintenanceStoppedFinalizeSHA(output) != runner.facts.BoundedJournalSHA256 {
			return nil, errors.New("fresh original bounded journal SHA-256 changed")
		}
		if reflect.DeepEqual(command.Args, whole) && maintenanceStoppedFinalizeSHA(output) != runner.facts.WholeJournalSHA256 {
			return nil, errors.New("fresh original whole journal SHA-256 changed")
		}
	}
	return output, nil
}

func (runtime *productionRuntime) maintenanceStoppedFinalizeCompletion(ctx context.Context, facts maintenanceStoppedFinalizeFacts, lock *os.File, e *maintenanceStoppedFinalizeEvidence, status productionStatus) error {
	if err := runtime.maintenanceStoppedFinalizeLocks(facts, lock); err != nil {
		return err
	}
	if err := runtime.maintenanceStoppedFinalizeGuardian(ctx, facts); err != nil {
		return err
	}
	if err := runtime.maintenanceStoppedFinalizeStopped(ctx, facts, e.manifest); err != nil {
		return err
	}
	if _, err := readMaintenanceBoundFile(e.workspace.manifestPath, facts.ManifestSHA256, runtime.requiredOwnerUID); err != nil {
		return err
	}
	if status.Phase != "FROZEN" || status.CaptureReceiptPath != filepath.Join(e.workspace.stateDir, "maintenance-capture.FROZEN.json") {
		return errors.New("original persist did not return the official FROZEN receipt")
	}
	receipt, err := readMaintenanceBoundFile(status.CaptureReceiptPath, status.CaptureReceiptSHA256, runtime.requiredOwnerUID)
	if err != nil {
		return err
	}
	var captured productionMaintenanceCapture
	if err := json.Unmarshal(receipt, &captured); err != nil {
		return err
	}
	expected := *e.manifest.MaintenanceCapture
	expected.Phase, expected.ShutdownJournalPath, expected.ShutdownJournalSHA256 = "FROZEN", filepath.Join(e.workspace.root, "maintenance-shutdown.log"), facts.BoundedJournalSHA256
	if !reflect.DeepEqual(captured, expected) {
		return errors.New("official FROZEN capture differs from the original writer evidence")
	}
	if _, err := readMaintenanceBoundFile(expected.ShutdownJournalPath, facts.BoundedJournalSHA256, runtime.requiredOwnerUID); err != nil {
		return err
	}
	actualStatus, err := runtime.readStatus(e.workspace)
	if err != nil || !reflect.DeepEqual(actualStatus, status) {
		return errors.New("official FROZEN status changed")
	}
	statusRaw, err := readPrivateRegularFile(e.workspace.statusPath, 64<<10)
	if err != nil {
		return err
	}
	body, err := json.MarshalIndent(struct {
		Format               string `json:"format"`
		DeploymentID         string `json:"deployment_id"`
		Phase                string `json:"phase"`
		ManifestSHA256       string `json:"manifest_sha256"`
		StatusSHA256         string `json:"status_sha256"`
		CaptureReceiptSHA256 string `json:"capture_receipt_sha256"`
		BoundedJournalSHA256 string `json:"bounded_journal_sha256"`
		WholeJournalSHA256   string `json:"whole_journal_sha256"`
	}{"lmm-maintenance-stopped-finalize-completion-v1", facts.DeploymentID, "FROZEN", facts.ManifestSHA256, maintenanceStoppedFinalizeSHA(statusRaw), status.CaptureReceiptSHA256, facts.BoundedJournalSHA256, facts.WholeJournalSHA256}, "", "  ")
	if err != nil {
		return err
	}
	completedFacts := facts
	completedFacts.StatusSHA256 = maintenanceStoppedFinalizeSHA(statusRaw)
	if err := runtime.maintenanceStoppedFinalizeCAS(ctx, completedFacts, lock, e); err != nil {
		return err
	}
	return maintenanceStoppedFinalizeExclusiveFile(filepath.Join(e.lineagePath, "completion.json"), append(body, '\n'))
}
