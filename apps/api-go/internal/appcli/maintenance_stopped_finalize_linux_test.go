//go:build linux

package appcli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The runner handles only local fixture reads. Any service, package or database
// mutation is a test failure instead of falling through to a real command.
type stoppedFinalizeTestRunner struct {
	base               *fakeProductionRunner
	bounded            []byte
	whole              []byte
	auditJSON          []byte
	loss               []byte
	unit               map[string]string
	guardianUnit       string
	guardianPID        int
	guardianInvocation string
	admissionBody      string
	commands           []productionCommand
}

func (r *stoppedFinalizeTestRunner) Run(ctx context.Context, c productionCommand) ([]byte, error) {
	r.commands = append(r.commands, c)
	switch c.Name {
	case commandSystemctl:
		if len(c.Args) < 2 || c.Args[0] != "show" {
			r.base.t.Fatalf("finalize attempted a service mutation: %s %v", c.Name, c.Args)
		}
		if c.Args[1] == r.guardianUnit {
			return []byte(fmt.Sprintf("LoadState=loaded\nActiveState=active\nSubState=running\nMainPID=%d\nExecMainPID=%d\nExecMainCode=0\nExecMainStatus=0\nResult=success\nControlGroup=/fixture/guardian\nRestart=no\nInvocationID=%s\n", r.guardianPID, r.guardianPID, r.guardianInvocation)), nil
		}
		var out strings.Builder
		for _, key := range []string{"MainPID", "ExecMainPID", "ExecMainCode", "ExecMainStatus", "ActiveState", "SubState", "Result", "ControlGroup", "Restart", "InvocationID"} {
			fmt.Fprintf(&out, "%s=%s\n", key, r.unit[key])
		}
		return []byte(out.String()), nil
	case commandJournalctl:
		if slices.Contains(c.Args, "systemd-journald.service") {
			return bytes.Clone(r.loss), nil
		}
		if slices.Contains(c.Args, "--output=json") {
			return bytes.Clone(r.auditJSON), nil
		}
		if slices.Contains(c.Args, "--since") && slices.Contains(c.Args, "--output=cat") {
			return bytes.Clone(r.bounded), nil
		}
		if slices.Contains(c.Args, "--output=cat") {
			return bytes.Clone(r.whole), nil
		}
		return nil, errors.New("unexpected journal fixture query")
	case commandPacman:
		if len(c.Args) == 0 || (c.Args[0] != "-Q" && c.Args[0] != "-Qkk" && c.Args[0] != "-Qqo" && c.Args[0] != "-Qo") {
			r.base.t.Fatalf("finalize attempted a package mutation: %s %v", c.Name, c.Args)
		}
		return r.base.Run(ctx, c)
	case commandRunuser:
		if len(c.Args) < 5 || !reflect.DeepEqual(c.Args[:4], []string{"--user", "root", "--", r.base.installedBinary}) || c.Args[4] != "request" {
			r.base.t.Fatalf("finalize attempted a business command: %s %v", c.Name, c.Args)
		}
		index := slices.Index(c.Args, "--status-file")
		if index < 0 || index+1 >= len(c.Args) {
			r.base.t.Fatal("read-only admission probe omitted its temporary output")
		}
		if err := os.WriteFile(c.Args[index+1], []byte("503"), 0600); err != nil {
			return nil, err
		}
		return []byte(r.admissionBody), nil
	case r.base.installedBinary:
		if len(c.Args) != 1 || c.Args[0] != "version" {
			r.base.t.Fatalf("finalize attempted a business command: %v", c.Args)
		}
		return r.base.Run(ctx, c)
	default:
		r.base.t.Fatalf("finalize attempted an unexpected command: %s %v", c.Name, c.Args)
		return nil, errors.New("unexpected finalize command")
	}
}

type stoppedFinalizeFixture struct {
	productionFixture
	facts          maintenanceStoppedFinalizeFacts
	lock           *os.File
	finalizeRunner *stoppedFinalizeTestRunner
	manifest       productionManifest
	root           string
}

func newStoppedFinalizeFixture(t *testing.T) stoppedFinalizeFixture {
	t.Helper()
	// Sealed bound files reject world-writable ancestors, including /tmp.
	t.Setenv("TMPDIR", maintenanceFixtureCache(t))
	f := newProductionFixture(t)
	f.runtime.effectiveUID = os.Geteuid
	root := filepath.Dir(f.runtime.paths.WorkRoot)
	context := context.Background()
	metadata := func(path, name string) productionPackageMetadata {
		t.Helper()
		value, err := f.runtime.packageMetadata(context, path, name)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	goCandidate := metadata(f.options.GoPackage, productionAURPackageName)
	goRollback := metadata(f.options.GoRollbackPackage, productionAURPackageName)
	web := metadata(f.options.WebRollbackPackage, productionWebPackageName)
	environmentSHA, err := f.runtime.saveRestoreState(f.workspace, f.environment)
	if err != nil {
		t.Fatal(err)
	}
	frontendTarget, err := os.Readlink(filepath.Join(f.runtime.paths.FrontendRoot, "current"))
	if err != nil {
		t.Fatal(err)
	}
	pid, invocation := 2147483600, strings.Repeat("1", 32)
	stopTime := f.clock.Add(-2 * time.Second).Add(148351603 * time.Nanosecond)
	bounded := []byte("received signal: terminated\nbatch update finished\nserver exited\n")
	whole := []byte("LMM API " + f.runner.oldVersion + " started\nready in 12 ms\nquota dashboard flush: persisted=12 failed=0 dropped=0\nquota dashboard flush: persisted=9 failed=0 dropped=0\n" + string(bounded))
	auditJSON := stoppedFinalizeJournal(t, f.runner.oldVersion, pid, invocation, f.clock.Add(-time.Hour),
		"quota dashboard flush: persisted=12 failed=0 dropped=0", "quota dashboard flush: persisted=9 failed=0 dropped=0",
		"received signal: terminated", "batch update finished", "server exited")
	original, err := os.ReadFile(filepath.Join(f.runtime.paths.NginxRoot, "lmm-api-locations.conf"))
	if err != nil {
		t.Fatal(err)
	}
	processPath := filepath.Join(f.workspace.stateDir, "maintenance-process-"+invocation+".environment")
	process := []byte("SQL_DSN=postgres://user:password@127.0.0.1/lmm\x00")
	if err := os.WriteFile(processPath, process, 0600); err != nil {
		t.Fatal(err)
	}
	m := productionManifest{
		Format: productionTransactionFormat, DeploymentID: f.workspace.id, OperatorUser: productionOperatorUser,
		Go:          transitionFromMetadata(true, f.options.GoPackage, f.options.GoRollbackPackage, f.options.GoPackageSHA256, f.options.GoRollbackSHA256, goCandidate, goRollback),
		Web:         transitionFromMetadata(false, f.options.WebRollbackPackage, f.options.WebRollbackPackage, f.options.WebRollbackSHA256, f.options.WebRollbackSHA256, web, web),
		Frontend:    productionFrontendTransition{OldTarget: frontendTarget, NewTarget: frontendTarget, OldIndexSHA256: web.IndexSHA256, NewIndexSHA256: web.IndexSHA256},
		ProbeBinary: f.options.ProbeBinary, ProbeBinarySHA256: f.options.ProbeBinarySHA256,
		OperatorBinary: f.options.OperatorBinary, OperatorBinarySHA256: f.options.OperatorBinarySHA256,
		ExpectedVersion: f.options.ExpectedVersion, OldVersion: f.runner.oldVersion,
		PreviousProviderTarget: backendGoName, NewProviderTarget: backendGoName,
		DatabaseSchema: "public", ObservationSeconds: 120,
		ConfigRestorePath: f.workspace.configRestore, EnvironmentRestoreSHA256: environmentSHA,
		BillingGate: &productionBillingGate{StartedUTC: f.clock.Add(-time.Minute), StopStartedUTC: stopTime, Sequence: 1,
			OriginalSHA256: fmt.Sprintf("%x", sha256Bytes(original)), AdmissionClosed: true, GoPID: pid, GoInvocationID: invocation,
			StopVerified: true, ShutdownJournalSHA256: fmt.Sprintf("%x", sha256Bytes(bounded)), InvocationJournalSHA256: fmt.Sprintf("%x", sha256Bytes(auditJSON))},
		MaintenanceCapture: &productionMaintenanceCapture{Format: "lmm-credit-maintenance-capture-v1", TransitionID: "stopped-finalize-fixture",
			TransitionIntentSHA256: strings.Repeat("a", 64), ProviderSHA256: mustHashFile(t, f.runtime.paths.InstalledBinary),
			Version: f.runner.oldVersion, PID: pid, InvocationID: invocation,
			ArchivedEnvironmentPath: filepath.Join(f.workspace.configRestore, "lmm-api-go.env"), ArchivedEnvironmentSHA256: environmentSHA,
			DatabaseSchema: "public", FrontendTarget: frontendTarget, FrontendSHA256: web.IndexSHA256,
			ProcessEnvironmentPath: processPath, ProcessEnvironmentSHA256: fmt.Sprintf("%x", sha256Bytes(process))},
	}
	if err := os.WriteFile(filepath.Join(f.workspace.root, "billing-locations.1"), original, 0600); err != nil {
		t.Fatal(err)
	}
	prepared := productionMaintenancePrepareConfig{Format: "lmm-credit-transition-prepare-v1", TransitionID: m.MaintenanceCapture.TransitionID,
		TransitionIntentSHA256: m.MaintenanceCapture.TransitionIntentSHA256, ProviderSHA256: f.options.ProbeBinarySHA256,
		TargetCreditsPerUSD: 500000,
		Database:            map[string]any{"system_identifier": "123", "database": "fixture", "database_oid": float64(123), "schema": "public", "server_version_num": float64(170000), "database_user": "fixture"},
		Options:             map[string]string{"CreditsPerUSD": "400000", "LegacyPricingQuotaPerUnit": "400000", "QuotaPerUnit": "400000", "PublicCreditsPerUSD": "400000", "USDExchangeRate": "7.1"}}
	writeJSON := func(path string, value any) {
		t.Helper()
		body, err := json.MarshalIndent(value, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, append(body, '\n'), 0600); err != nil {
			t.Fatal(err)
		}
	}
	preparePath := filepath.Join(f.workspace.stagingDir, "maintenance-prepare.json")
	writeJSON(preparePath, prepared)
	socketRoot, err := os.MkdirTemp(maintenanceFixtureCache(t), "sf-peer-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(socketRoot) })
	socketPath := filepath.Join(socketRoot, "s")
	// The receipt is native output; the manifest capture itself keeps its empty
	// Phase because persistMaintenanceCapture sets the phase only in its copy.
	if _, err := f.runtime.persistMaintenanceCapture(f.workspace, m, "ADMISSION_CLOSED"); err != nil {
		t.Fatal(err)
	}
	status, err := f.runtime.readStatus(f.workspace)
	if err != nil {
		t.Fatal(err)
	}
	h := productionMaintenanceHandoff{Format: productionMaintenanceHandoffFormat, Stage: "prebridge", DeploymentTool: "native",
		TransitionID: prepared.TransitionID, TransitionIntentSHA256: prepared.TransitionIntentSHA256, ProviderSHA256: prepared.ProviderSHA256,
		PrepareConfigPath: preparePath, PrepareConfigSHA256: mustHashFile(t, preparePath), GuardianSocket: socketPath,
		CaptureReceiptPath: status.CaptureReceiptPath, CaptureReceiptSHA256: status.CaptureReceiptSHA256}
	handoffPath := filepath.Join(f.workspace.stagingDir, "maintenance-handoff.json")
	writeJSON(handoffPath, h)
	loaded, err := loadProductionMaintenanceHandoff(handoffPath, mustHashFile(t, handoffPath), uint32(os.Geteuid()))
	if err != nil {
		t.Fatal(err)
	}
	f.runtime.maintenanceHandoff = loaded
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socketPath, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	lease, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: socketPath, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { lease.Close() })
	peer, err := listener.AcceptUnix()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { peer.Close() })
	f.runtime.guardianLease = lease
	f.runtime.guardianAdoptAll = true
	m.MaintenanceHandoff = loaded
	barrier, err := f.runtime.billingBarrier(original, f.workspace.id)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.runtime.paths.NginxRoot, "lmm-api-locations.conf"), barrier, 0644); err != nil {
		t.Fatal(err)
	}
	if err := f.runtime.writeManifest(f.workspace, m); err != nil {
		t.Fatal(err)
	}
	if _, err := f.runtime.persistMaintenanceCapture(f.workspace, m, "ADMISSION_CLOSED"); err != nil {
		t.Fatal(err)
	}
	allClosedPath := filepath.Join(root, "all-admission-closed.json")
	writeJSON(allClosedPath, map[string]any{"format": "lmm-credit-all-admission-closed-v1", "transition_id": loaded.TransitionID,
		"transition_intent_sha256": loaded.TransitionIntentSHA256, "all_origins_closed": true})
	guardianUnit := "stopped-finalize-fixture.service"
	guardianUnitPath := filepath.Join(f.runtime.paths.SystemdUnitRoot, guardianUnit)
	if err := os.WriteFile(guardianUnitPath, []byte("[Service]\nExecStart=/fixture/guardian\n"), 0600); err != nil {
		t.Fatal(err)
	}
	facts := maintenanceStoppedFinalizeFacts{DeploymentID: f.workspace.id, ManifestSHA256: mustHashFile(t, f.workspace.manifestPath),
		StatusSHA256: mustHashFile(t, f.workspace.statusPath), PID: pid, InvocationID: invocation, StopStartedUTC: stopTime,
		BoundedJournalSHA256: m.BillingGate.ShutdownJournalSHA256, WholeJournalSHA256: fmt.Sprintf("%x", sha256Bytes(whole)),
		HandoffPath: loaded.Path, HandoffSHA256: loaded.SHA256, AllAdmissionClosedPath: allClosedPath, AllAdmissionClosedSHA256: mustHashFile(t, allClosedPath),
		GuardianUnit: guardianUnit, GuardianUnitSHA256: mustHashFile(t, guardianUnitPath), GuardianPID: os.Getpid(), GuardianInvocationID: strings.Repeat("2", 32)}
	lockPaths := [3]string{f.runtime.paths.GlobalLock, filepath.Join(root, "systemd.lock"), filepath.Join(f.runtime.paths.FrontendRoot, ".release.lock")}
	var locks [3]*os.File
	for index, path := range lockPaths {
		file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
		if err != nil {
			t.Fatal(err)
		}
		if err := file.Chmod(0600); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { file.Close() })
		info, err := file.Stat()
		if err != nil {
			t.Fatal(err)
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok {
			t.Fatal("fixture lock stat unavailable")
		}
		facts.Locks[index] = maintenanceStoppedFinalizeLock{Path: path, Device: uint64(stat.Dev), Inode: stat.Ino}
		locks[index] = file
	}
	f.runtime.guardianExtraLocks = []*os.File{locks[1], locks[2]}
	runner := &stoppedFinalizeTestRunner{base: f.runner, bounded: bounded, whole: whole, auditJSON: auditJSON,
		guardianUnit: guardianUnit, guardianPID: facts.GuardianPID, guardianInvocation: facts.GuardianInvocationID,
		admissionBody: "lmm-credit-transition:" + loaded.TransitionID,
		unit: map[string]string{"MainPID": "0", "ExecMainPID": strconv.Itoa(pid), "ExecMainCode": "1", "ExecMainStatus": "0",
			"ActiveState": "inactive", "SubState": "dead", "Result": "success", "ControlGroup": "", "Restart": "no", "InvocationID": invocation}}
	f.runtime.runner = runner
	f.runner.serviceActive = false
	return stoppedFinalizeFixture{productionFixture: f, facts: facts, lock: locks[0], finalizeRunner: runner, manifest: m, root: root}
}

func TestNativeMaintenanceStoppedFinalizePrecheckDoesNotWrite(t *testing.T) {
	f := newStoppedFinalizeFixture(t)
	before := snapshotStoppedFinalizeTree(t, f.root)
	status, err := f.runtime.maintenanceStoppedFinalize(context.Background(), f.facts, false, f.lock)
	if err != nil {
		t.Fatal(err)
	}
	if status.Phase != "ADMISSION_CLOSED" {
		t.Fatalf("precheck changed phase to %q", status.Phase)
	}
	assertStoppedFinalizeUnchanged(t, f.root, before)
}

func TestNativeMaintenanceStoppedFinalizeCLIRejectsUnboundExecution(t *testing.T) {
	for _, args := range [][]string{
		{"--execute"}, {"--execute", "--confirm", "other"}, {"--confirm", "api.lmm.best"},
		{"--execute", "--confirm", "api.lmm.best", "--pid", "1"},
		{"--execute", "--confirm", "api.lmm.best", "--handoff", "/fixture/other"}, {"force"},
	} {
		var stdout, stderr bytes.Buffer
		if code := RunMaintenanceStoppedFinalize(args, &stdout, &stderr); code != ExitUsage {
			t.Fatalf("unbound args accepted: %v, exit=%d", args, code)
		}
		if stdout.Len() != 0 || stderr.Len() == 0 {
			t.Fatal("invalid args reached recovery")
		}
	}
}

func TestNativeMaintenanceStoppedFinalizePreservesWholeHistoryAndWritesFrozenReceipt(t *testing.T) {
	f := newStoppedFinalizeFixture(t)
	if err := validateBillingShutdownJournal(f.finalizeRunner.whole); err == nil || !strings.Contains(err.Error(), "duplicated") {
		t.Fatal("fixture does not reproduce historical periodic quota duplicate rejection")
	}
	if bytes.Contains(f.finalizeRunner.bounded, []byte("quota dashboard flush")) {
		t.Fatal("fixture must cover valid shutdown without a final quota report")
	}
	precheck, err := f.runtime.maintenanceStoppedFinalizePrecheck(context.Background(), f.facts, f.lock)
	if err != nil {
		t.Fatal(err)
	}
	before := snapshotStoppedFinalizeTree(t, f.root)
	status, err := f.runtime.maintenanceStoppedFinalize(context.Background(), f.facts, true, f.lock)
	if err != nil {
		t.Fatal(err)
	}
	if status.Phase != "FROZEN" || status.CaptureReceiptPath != filepath.Join(f.workspace.stateDir, "maintenance-capture.FROZEN.json") {
		t.Fatalf("native frozen status was not produced: %+v", status)
	}
	if status.CaptureReceiptSHA256 != mustHashFile(t, status.CaptureReceiptPath) {
		t.Fatal("native receipt SHA does not match its actual bytes")
	}
	read := func(path string) []byte {
		t.Helper()
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return body
	}
	var receipt productionMaintenanceCapture
	if err := json.Unmarshal(read(status.CaptureReceiptPath), &receipt); err != nil {
		t.Fatal(err)
	}
	if receipt.Phase != "FROZEN" || receipt.PID != f.facts.PID || receipt.InvocationID != f.facts.InvocationID || receipt.ShutdownJournalSHA256 != f.facts.BoundedJournalSHA256 {
		t.Fatalf("native frozen receipt lost captured identity: %+v", receipt)
	}
	if receipt.ShutdownJournalPath != filepath.Join(f.workspace.root, "maintenance-shutdown.log") || !bytes.Equal(read(receipt.ShutdownJournalPath), f.finalizeRunner.bounded) {
		t.Fatal("formal shutdown evidence differs from the original bounded bytes")
	}
	if receipt.ShutdownJournalSHA256 != mustHashFile(t, receipt.ShutdownJournalPath) {
		t.Fatal("formal shutdown hash is not the original bounded SHA")
	}
	archives := map[string][]byte{
		"manifest.before.json": precheck.manifestRaw, "status.before.json": precheck.statusRaw,
		"capture.before.json": precheck.captureRaw, "handoff.original.json": precheck.handoffRaw,
		"all-admission-closed.original.json": precheck.allClosedRaw,
		"whole-invocation.original.log":      f.finalizeRunner.whole, "bounded-shutdown.original.log": f.finalizeRunner.bounded,
	}
	for name, body := range archives {
		path := filepath.Join(precheck.lineagePath, name)
		if !bytes.Equal(read(path), body) {
			t.Fatalf("archived evidence changed: %s", name)
		}
		if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0600 {
			t.Fatalf("archive is not private: %s %v", name, err)
		}
	}
	whole := read(filepath.Join(precheck.lineagePath, "whole-invocation.original.log"))
	if bytes.Count(whole, []byte("quota dashboard flush:")) != 2 || fmt.Sprintf("%x", sha256Bytes(whole)) != f.facts.WholeJournalSHA256 {
		t.Fatal("whole history lost its original periodic quota reports")
	}
	if mustHashFile(t, f.workspace.manifestPath) != f.facts.ManifestSHA256 {
		t.Fatal("original stop verification rewrote the manifest evidence")
	}
	wholeReads, boundedReads := 0, 0
	for _, command := range f.finalizeRunner.commands {
		if command.Name != commandJournalctl || !slices.Contains(command.Args, "--output=cat") || slices.Contains(command.Args, "systemd-journald.service") {
			continue
		}
		if !slices.Contains(command.Args, "_PID="+strconv.Itoa(f.facts.PID)) || !slices.Contains(command.Args, "_SYSTEMD_INVOCATION_ID="+f.facts.InvocationID) {
			t.Fatal("journal query omitted the captured writer generation")
		}
		if index := slices.Index(command.Args, "--since"); index >= 0 {
			boundedReads++
			wantSince := fmt.Sprintf("@%d.%06d", f.facts.StopStartedUTC.Unix(), f.facts.StopStartedUTC.Nanosecond()/1000)
			if index+1 >= len(command.Args) || command.Args[index+1] != wantSince || !slices.Contains(command.Args, f.runtime.paths.Service) {
				t.Fatal("bounded shutdown query lost the original microsecond boundary or unit")
			}
		} else {
			wholeReads++
		}
	}
	if wholeReads == 0 || boundedReads == 0 {
		t.Fatal("whole and bounded journals were not read independently")
	}
	// Only the lineage, formal bounded log, native receipt/status and cosmetic
	// public phase can change. All packages, locks, ingress and sealed inputs stay.
	after := snapshotStoppedFinalizeTree(t, f.root)
	allowed := map[string]bool{}
	for _, path := range []string{f.workspace.statusPath, status.CaptureReceiptPath, receipt.ShutdownJournalPath,
		filepath.Join(f.runtime.paths.FrontendRoot, publicServiceStatusFilename)} {
		relative, err := filepath.Rel(f.root, path)
		if err != nil {
			t.Fatal(err)
		}
		allowed[relative] = true
	}
	lineageRelative, err := filepath.Rel(f.root, precheck.lineagePath)
	if err != nil {
		t.Fatal(err)
	}
	for path, old := range before {
		if old.Mode.IsDir() || allowed[path] {
			continue
		}
		got, ok := after[path]
		if !ok || got.Mode != old.Mode || got.Data != old.Data {
			t.Errorf("finalize changed protected path: %s", path)
		}
	}
	for path := range after {
		if _, existed := before[path]; !existed && !allowed[path] && path != lineageRelative && !strings.HasPrefix(path, lineageRelative+string(filepath.Separator)) {
			t.Errorf("finalize created an unexpected path: %s", path)
		}
	}
	beforeReplay := snapshotStoppedFinalizeTree(t, f.root)
	if _, err := f.runtime.maintenanceStoppedFinalize(context.Background(), f.facts, true, f.lock); err == nil {
		t.Fatal("completed finalize replay accepted")
	}
	assertStoppedFinalizeUnchanged(t, f.root, beforeReplay)
}

func TestNativeMaintenanceStoppedFinalizeRejectsUnsafeFactsBeforeWrites(t *testing.T) {
	wantErrors := map[string]string{
		"gate-unverified":             "stopped writer or closed admission facts differ",
		"admission-reopened":          "stopped writer or closed admission facts differ",
		"stop-boundary-missing":       "stopped writer or closed admission facts differ",
		"stopped-invocation-mismatch": "inactive writer invocation changed",
		"live-captured-pid":           "PID has not verifiably exited",
		"existing-lineage":            "destination or previous attempt already exists",
		"manifest-sha":                "original manifest", "status-sha": "original status",
		"bounded-sha": "original bounded journal SHA-256 changed", "whole-sha": "original whole journal SHA-256 changed",
		"handoff-sha": "unchanged original ordinary prebridge handoff", "all-origin-sha": "SHA-256 mismatch",
		"guardian-generation": "guardian generation changed", "guardian-peer": "guardian Unix peer changed",
		"lock-inode": "lock inode differs", "transaction-owner": "owned by another or inactive deployment",
		"refund-intent": "untracked asynchronous refund intent", "refund-startup-missing": "writer startup missing",
		"journal-loss": "journal loss/suppression", "tracked-refund-report-missing": "lacks its refund completion report",
		"shutdown-quota-failed": "quota dashboard flush report is invalid", "shutdown-refund-incomplete": "refund task completion report is inconsistent",
	}
	for _, name := range []string{
		"gate-unverified", "admission-reopened", "stop-boundary-missing", "stopped-invocation-mismatch", "live-captured-pid",
		"existing-lineage", "manifest-sha", "status-sha", "bounded-sha", "whole-sha", "handoff-sha", "all-origin-sha",
		"guardian-generation", "guardian-peer", "lock-inode", "transaction-owner", "refund-intent", "refund-startup-missing", "journal-loss",
		"tracked-refund-report-missing", "shutdown-quota-failed", "shutdown-refund-incomplete",
	} {
		t.Run(name, func(t *testing.T) {
			f := newStoppedFinalizeFixture(t)
			rebindManifest := func() {
				t.Helper()
				if err := f.runtime.writeManifest(f.workspace, f.manifest); err != nil {
					t.Fatal(err)
				}
				f.facts.ManifestSHA256 = mustHashFile(t, f.workspace.manifestPath)
			}
			rebindBounded := func(body string) {
				f.finalizeRunner.bounded = []byte(body)
				f.facts.BoundedJournalSHA256 = fmt.Sprintf("%x", sha256Bytes(f.finalizeRunner.bounded))
				f.manifest.BillingGate.ShutdownJournalSHA256 = f.facts.BoundedJournalSHA256
				rebindManifest()
			}
			switch name {
			case "gate-unverified":
				f.manifest.BillingGate.StopVerified = false
				rebindManifest()
			case "admission-reopened":
				f.manifest.BillingGate.AdmissionReopened = true
				rebindManifest()
			case "stop-boundary-missing":
				f.manifest.BillingGate.StopStartedUTC = time.Time{}
				rebindManifest()
			case "stopped-invocation-mismatch":
				f.finalizeRunner.unit["InvocationID"] = strings.Repeat("3", 32)
			case "live-captured-pid":
				f.facts.PID = os.Getpid()
				f.manifest.BillingGate.GoPID = f.facts.PID
				f.manifest.MaintenanceCapture.PID = f.facts.PID
				f.finalizeRunner.unit["ExecMainPID"] = strconv.Itoa(f.facts.PID)
				// Keep the sealed native receipt and every identity consistent so
				// rejection must come from the still-existing /proc/PID guard.
				if _, err := f.runtime.persistMaintenanceCapture(f.workspace, f.manifest, "ADMISSION_CLOSED"); err != nil {
					t.Fatal(err)
				}
				current, err := f.runtime.readStatus(f.workspace)
				if err != nil {
					t.Fatal(err)
				}
				h := *f.runtime.maintenanceHandoff
				h.CaptureReceiptSHA256 = current.CaptureReceiptSHA256
				h.Path, h.SHA256 = "", ""
				body, err := json.MarshalIndent(h, "", "  ")
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(f.facts.HandoffPath, append(body, '\n'), 0600); err != nil {
					t.Fatal(err)
				}
				f.facts.HandoffSHA256 = mustHashFile(t, f.facts.HandoffPath)
				loaded, err := loadProductionMaintenanceHandoff(f.facts.HandoffPath, f.facts.HandoffSHA256, uint32(os.Geteuid()))
				if err != nil {
					t.Fatal(err)
				}
				f.runtime.maintenanceHandoff, f.manifest.MaintenanceHandoff = loaded, loaded
				rebindManifest()
				if _, err := f.runtime.persistMaintenanceCapture(f.workspace, f.manifest, "ADMISSION_CLOSED"); err != nil {
					t.Fatal(err)
				}
				f.facts.StatusSHA256 = mustHashFile(t, f.workspace.statusPath)
			case "existing-lineage":
				evidence, err := f.runtime.maintenanceStoppedFinalizePrecheck(context.Background(), f.facts, f.lock)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(evidence.lineagePath, 0700); err != nil {
					t.Fatal(err)
				}
			case "manifest-sha":
				f.facts.ManifestSHA256 = strings.Repeat("0", 64)
			case "status-sha":
				f.facts.StatusSHA256 = strings.Repeat("0", 64)
			case "bounded-sha":
				f.finalizeRunner.bounded = append(bytes.Clone(f.finalizeRunner.bounded), '\n')
			case "whole-sha":
				f.finalizeRunner.whole = append(bytes.Clone(f.finalizeRunner.whole), '\n')
			case "handoff-sha":
				f.facts.HandoffSHA256 = strings.Repeat("0", 64)
			case "all-origin-sha":
				f.facts.AllAdmissionClosedSHA256 = strings.Repeat("0", 64)
			case "guardian-generation":
				f.finalizeRunner.guardianInvocation = strings.Repeat("3", 32)
			case "guardian-peer":
				f.facts.GuardianPID++
				f.finalizeRunner.guardianPID = f.facts.GuardianPID
			case "lock-inode":
				f.facts.Locks[1].Inode++
			case "transaction-owner":
				if err := os.WriteFile(filepath.Join(f.runtime.paths.TransactionLock, productionTransactionMarker), []byte("format=1\ndeployment_id=other\nstatus=ACTIVE\n"), 0600); err != nil {
					t.Fatal(err)
				}
			case "refund-intent":
				f.finalizeRunner.auditJSON = stoppedFinalizeJournal(t, f.runner.oldVersion, f.facts.PID, f.facts.InvocationID, f.clock.Add(-time.Hour), "请求失败, 返还预扣费")
				f.manifest.BillingGate.InvocationJournalSHA256 = fmt.Sprintf("%x", sha256Bytes(f.finalizeRunner.auditJSON))
				rebindManifest()
			case "refund-startup-missing":
				f.finalizeRunner.auditJSON = bytes.ReplaceAll(f.finalizeRunner.auditJSON, []byte(" started"), []byte(" omitted"))
				f.manifest.BillingGate.InvocationJournalSHA256 = fmt.Sprintf("%x", sha256Bytes(f.finalizeRunner.auditJSON))
				rebindManifest()
			case "journal-loss":
				f.finalizeRunner.loss = []byte("Suppressed 10 messages\n")
			case "tracked-refund-report-missing":
				if err := os.WriteFile(filepath.Join(filepath.Dir(f.runtime.paths.GoRevisionFile), refundTaskDrainCapability), []byte("v1\n"), 0644); err != nil {
					t.Fatal(err)
				}
			case "shutdown-quota-failed":
				rebindBounded(string(f.finalizeRunner.bounded) + "quota dashboard flush: persisted=0 failed=1 dropped=0\n")
			case "shutdown-refund-incomplete":
				rebindBounded(string(f.finalizeRunner.bounded) + "refund_tasks execution_complete=true accepted=2 finished=1 active=0 failed=0 (execution completion is not financial success)\n")
			}
			before := snapshotStoppedFinalizeTree(t, f.root)
			if _, err := f.runtime.maintenanceStoppedFinalize(context.Background(), f.facts, true, f.lock); err == nil {
				t.Fatalf("unsafe %s accepted", name)
			} else if !strings.Contains(err.Error(), wantErrors[name]) {
				t.Fatalf("%s rejected by the wrong guard: got %v, want %q", name, err, wantErrors[name])
			}
			assertStoppedFinalizeUnchanged(t, f.root, before)
		})
	}
}

type stoppedFinalizeFileSnapshot struct {
	Mode    fs.FileMode
	ModTime int64
	Data    string
}

func snapshotStoppedFinalizeTree(t *testing.T, root string) map[string]stoppedFinalizeFileSnapshot {
	t.Helper()
	result := map[string]stoppedFinalizeFileSnapshot{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		data := ""
		if info.Mode().IsRegular() {
			body, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			data = string(body)
		} else if info.Mode()&os.ModeSymlink != 0 {
			data, err = os.Readlink(path)
			if err != nil {
				return err
			}
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		result[relative] = stoppedFinalizeFileSnapshot{Mode: info.Mode(), ModTime: info.ModTime().UnixNano(), Data: data}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func assertStoppedFinalizeUnchanged(t *testing.T, root string, before map[string]stoppedFinalizeFileSnapshot) {
	t.Helper()
	after := snapshotStoppedFinalizeTree(t, root)
	if !reflect.DeepEqual(before, after) {
		for path, old := range before {
			if got, ok := after[path]; !ok || got != old {
				t.Errorf("persistent path changed: %s", path)
			}
		}
		for path := range after {
			if _, ok := before[path]; !ok {
				t.Errorf("persistent path created: %s", path)
			}
		}
		t.FailNow()
	}
}

func stoppedFinalizeJournal(t *testing.T, version string, pid int, invocation string, start time.Time, messages ...string) []byte {
	t.Helper()
	all := append([]string{"LMM API " + version + " started", "ready in 12 ms"}, messages...)
	var out bytes.Buffer
	for index, message := range all {
		line, err := json.Marshal(map[string]string{
			"MESSAGE": message, "__CURSOR": fmt.Sprintf("fixture-cursor-%d", index),
			"__REALTIME_TIMESTAMP": strconv.FormatInt(start.Add(time.Duration(index)*time.Second).UnixMicro(), 10),
			"_PID":                 strconv.Itoa(pid), "_SYSTEMD_INVOCATION_ID": invocation,
		})
		if err != nil {
			t.Fatal(err)
		}
		out.Write(line)
		out.WriteByte('\n')
	}
	return out.Bytes()
}
