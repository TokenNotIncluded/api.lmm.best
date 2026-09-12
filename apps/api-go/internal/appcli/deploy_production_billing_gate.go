package appcli

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Generated from live checks, never accepted as an operator-supplied receipt.
type productionBillingGate struct {
	StartedUTC              time.Time `json:"started_utc"`
	StopStartedUTC          time.Time `json:"stop_started_utc"`
	Sequence                int       `json:"sequence"`
	OriginalSHA256          string    `json:"original_sha256"`
	AdmissionClosed         bool      `json:"admission_closed"`
	GoPID                   int       `json:"go_pid"`
	GoInvocationID          string    `json:"go_invocation_id"`
	StopVerified            bool      `json:"stop_verified"`
	ShutdownJournalSHA256   string    `json:"shutdown_journal_sha256,omitempty"`
	InvocationJournalSHA256 string    `json:"invocation_journal_sha256,omitempty"`
	AdmissionReopened       bool      `json:"admission_reopened"`
}

func (runtime *productionRuntime) billingUnitState(ctx context.Context, unit string) (map[string]string, error) {
	out, err := runtime.runner.Run(ctx, productionCommand{Name: commandSystemctl, Args: []string{"show", unit, "--property=MainPID,ExecMainPID,ExecMainCode,ExecMainStatus,ActiveState,SubState,Result,ControlGroup,Restart,InvocationID"}})
	if err != nil {
		return nil, err
	}
	state := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return nil, errors.New("invalid drain unit evidence")
		}
		state[key] = value
	}
	for _, key := range []string{"MainPID", "ExecMainPID", "ExecMainCode", "ExecMainStatus", "ActiveState", "SubState", "Result", "ControlGroup", "Restart"} {
		if _, ok := state[key]; !ok {
			return nil, fmt.Errorf("missing drain evidence: %s", key)
		}
	}
	return state, nil
}

func cleanBillingUnitExit(state map[string]string, pid int) error {
	if pid <= 1 || state["MainPID"] != "0" || state["ExecMainPID"] != strconv.Itoa(pid) || state["ActiveState"] != "inactive" || state["SubState"] != "dead" || state["Result"] != "success" || state["ExecMainCode"] != "1" || state["ExecMainStatus"] != "0" || state["ControlGroup"] != "" {
		return errors.New("billing drain requires normal exit and an empty service cgroup")
	}
	if _, err := os.Stat(filepath.Join("/proc", strconv.Itoa(pid))); !errors.Is(err, os.ErrNotExist) {
		return errors.New("old billing writer PID has not verifiably exited")
	}
	return nil
}

func billingBarrier(original []byte, id string) ([]byte, error) {
	const anchor = "location @lmm_api_backend {"
	if !productionIDPattern.MatchString(id) || strings.Count(string(original), anchor) != 1 || strings.Contains(string(original), "lmm-billing-drain:") {
		return nil, errors.New("unrecognized LMM-only nginx locations")
	}
	return append([]byte("return 503 'lmm-billing-drain:"+id+"';\n"), original...), nil
}

func (runtime *productionRuntime) closeBillingAdmission(ctx context.Context, workspace productionWorkspace, manifest *productionManifest) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	if runtime.paths.LocalBaseURL != "http://127.0.0.1:3000" {
		return errors.New("billing drain requires the verified loopback LMM upstream")
	}
	path := filepath.Join(runtime.paths.NginxRoot, "lmm-api-locations.conf")
	original, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("unsafe LMM locations file")
	}
	owner, links, ok := deploymentFileOwnership(info)
	if !ok || owner != runtime.requiredOwnerUID || links != 1 || info.Mode().Perm() != 0644 {
		return errors.New("LMM locations ownership/mode mismatch")
	}
	if manifest.BillingGate == nil || manifest.BillingGate.AdmissionReopened {
		seq := 1
		if manifest.BillingGate != nil {
			seq = manifest.BillingGate.Sequence + 1
		}
		g := &productionBillingGate{StartedUTC: runtime.now().UTC(), Sequence: seq, OriginalSHA256: fmt.Sprintf("%x", sha256Bytes(original))}
		backup := filepath.Join(workspace.root, fmt.Sprintf("billing-locations.%d", seq))
		if _, err := os.Lstat(backup); !errors.Is(err, os.ErrNotExist) {
			return errors.New("billing locations evidence already exists")
		}
		if err := writeAtomicRegularFile(backup, original, 0600); err != nil {
			return err
		}
		manifest.BillingGate = g
		if err := runtime.writeManifest(workspace, *manifest); err != nil {
			return err
		}
	}
	g := manifest.BillingGate
	original, err = readPrivateRegularFile(filepath.Join(workspace.root, fmt.Sprintf("billing-locations.%d", g.Sequence)), 1<<20)
	if err != nil || fmt.Sprintf("%x", sha256Bytes(original)) != g.OriginalSHA256 {
		return errors.New("billing locations backup mismatch")
	}
	barrier, err := billingBarrier(original, workspace.id)
	if err != nil {
		return err
	}
	current, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if string(current) != string(original) && string(current) != string(barrier) {
		return errors.New("LMM locations changed outside the transaction")
	}
	if err := writeAtomicRegularFile(path, barrier, 0644); err != nil {
		return err
	}
	if _, err := runtime.runner.Run(ctx, productionCommand{Name: commandNginx, Args: []string{"-t"}}); err != nil {
		return err
	}
	if _, err := runtime.runner.Run(ctx, productionCommand{Name: commandSystemctl, Args: []string{"reload", "nginx"}}); err != nil {
		return err
	}
	binary, err := runtime.validateCandidateEntrypoint(workspace, manifest.ProbeBinary, manifest.ProbeBinarySHA256)
	if err != nil {
		// Recovery may legitimately lack a candidate archive. Use only the
		// package/link/payload-verified installed endpoint, never a wrapper or
		// arbitrary binary; promotion keeps the signed candidate requirement.
		if !runtime.billingRollback {
			return err
		}
		verified := false
		for _, old := range []bool{false, true} {
			if runtime.verifyTransitionInstalled(ctx, manifest.Go, old, true) == nil && runtime.verifyTransitionCLI(manifest.Go, old) == nil {
				verified = true
				break
			}
		}
		if !verified {
			return errors.New("no verified billing recovery probe")
		}
		binary = runtime.paths.InstalledBinary
	}
	statusFile := filepath.Join(workspace.root, "billing-gate-probe.status")
	closed := false
	for attempt := 0; attempt < 300; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		out, probeErr := runVerifiedBinary(ctx, runtime.runner, binary, []string{"request", "--base-url", runtime.paths.PublicBaseURL, "--path", "/v1/models?lmm_billing_gate=" + workspace.id, "--no-follow", "--timeout", "5s", "--status-file", statusFile}, nil, "", 7*time.Second, false)
		status, readErr := os.ReadFile(statusFile)
		if probeErr == nil && readErr == nil && strings.TrimSpace(string(status)) == "503" && string(out) == "lmm-billing-drain:"+workspace.id {
			counter := countBillingConnections
			if runtime.billingConnections != nil {
				counter = runtime.billingConnections
			}
			count, err := counter()
			if err != nil {
				return err
			}
			if count == 0 {
				closed = true
				break
			}
		}
		runtime.sleep(time.Second)
	}
	if !closed {
		return errors.New("LMM admission or upstream drain timed out; backend left running")
	}
	g.AdmissionClosed = true
	runtime.billingAdmissionClosed = true
	return runtime.writeManifest(workspace, *manifest)
}

// Includes accepted/SYN/closing upstream sockets, but not listeners or TIME_WAIT.
// Production's package-owned backend binds only loopback port 3000.
func countBillingConnections() (int, error) {
	total := 0
	for _, path := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		data, err := os.ReadFile(path)
		if err != nil {
			return 0, err
		}
		for _, line := range strings.Split(string(data), "\n")[1:] {
			fields := strings.Fields(line)
			if len(fields) == 0 {
				continue
			}
			if len(fields) < 4 {
				return 0, errors.New("invalid TCP drain evidence")
			}
			if strings.HasSuffix(fields[1], ":0BB8") && fields[3] != "0A" && fields[3] != "06" {
				total++
			}
		}
	}
	return total, nil
}

func validateBillingShutdownJournal(out []byte) error {
	text := strings.ToLower(string(out))
	if !strings.Contains(text, "received signal:") || !strings.Contains(text, "server exited") {
		return errors.New("missing writer shutdown journal evidence")
	}
	if strings.Count(text, "batch update started") > strings.Count(text, "batch update finished") {
		return errors.New("batch completion missing from shutdown journal")
	}
	for _, failure := range []string{"error", "failed", "panic", "timed out", "deadline exceeded", "killed", "oom"} {
		if strings.Contains(text, failure) {
			return errors.New("writer shutdown/batch journal is not clean; reconciliation required")
		}
	}
	return nil
}

func (runtime *productionRuntime) stopBillingWriter(ctx context.Context, workspace productionWorkspace, manifest *productionManifest) error {
	gate := manifest.BillingGate
	if gate == nil || !gate.AdmissionClosed || !runtime.billingAdmissionClosed {
		return errors.New("billing admission is not closed")
	}
	state, err := runtime.billingUnitState(ctx, runtime.paths.Service)
	if err != nil {
		return err
	}
	if state["ActiveState"] == "inactive" {
		if gate.GoPID <= 1 || gate.GoInvocationID == "" {
			return errors.New("writer already stopped without verified drain evidence")
		}
		if err := cleanBillingUnitExit(state, gate.GoPID); err != nil {
			return err
		}
		return runtime.verifyBillingStopJournal(ctx, workspace, manifest)
	}
	pid, err := strconv.Atoi(state["MainPID"])
	if err != nil || pid <= 1 || state["ActiveState"] != "active" {
		return errors.New("invalid old writer PID")
	}
	gate.GoPID, gate.StopVerified = pid, false
	gate.StopStartedUTC = runtime.now().UTC()
	invocation, decodeErr := hex.DecodeString(state["InvocationID"])
	if decodeErr != nil || len(invocation) != 16 {
		return errors.New("missing writer invocation identity")
	}
	gate.GoInvocationID = state["InvocationID"]
	if err := runtime.writeManifest(workspace, *manifest); err != nil {
		return err
	}
	if err := runtime.verifyNoUntrackedRefunds(ctx, manifest); err != nil {
		return err
	}
	if _, err := runtime.runner.Run(ctx, productionCommand{Name: commandSystemctl, Args: []string{"stop", runtime.paths.Service}}); err != nil {
		return err
	}
	state, err = runtime.billingUnitState(ctx, runtime.paths.Service)
	if err != nil {
		return err
	}
	if err := cleanBillingUnitExit(state, pid); err != nil {
		return err
	}
	return runtime.verifyBillingStopJournal(ctx, workspace, manifest)
}

func (runtime *productionRuntime) verifyBillingStopJournal(ctx context.Context, workspace productionWorkspace, manifest *productionManifest) error {
	gate := manifest.BillingGate
	if err := runtime.verifyNoUntrackedRefunds(ctx, manifest); err != nil {
		return err
	}
	if gate.StopStartedUTC.IsZero() {
		return errors.New("missing stop journal boundary")
	}
	out, err := runtime.runner.Run(ctx, productionCommand{Name: commandJournalctl, Args: []string{"--no-pager", "--output=cat", "--since", "@" + strconv.FormatInt(gate.StopStartedUTC.Unix(), 10), "-u", runtime.paths.Service, "_PID=" + strconv.Itoa(gate.GoPID), "_SYSTEMD_INVOCATION_ID=" + gate.GoInvocationID}})
	if err != nil {
		return err
	}
	if err := validateBillingShutdownJournal(out); err != nil {
		return err
	}
	gate.StopVerified, gate.ShutdownJournalSHA256 = true, fmt.Sprintf("%x", sha256Bytes(out))
	return runtime.writeManifest(workspace, *manifest)
}

type boundedBillingOutput struct {
	buffer *bytes.Buffer
	limit  int
}

func (w *boundedBillingOutput) Write(p []byte) (int, error) {
	if len(p) > w.limit-w.buffer.Len() {
		return 0, errors.New("billing evidence exceeds bounded output limit")
	}
	return w.buffer.Write(p)
}

func (runtime *productionRuntime) verifyNoUntrackedRefunds(ctx context.Context, manifest *productionManifest) error {
	g := manifest.BillingGate
	out, err := runtime.runner.Run(ctx, productionCommand{Name: commandJournalctl, Args: []string{"--no-pager", "--output=json", "--lines=20000", "-u", runtime.paths.Service, "_SYSTEMD_INVOCATION_ID=" + g.GoInvocationID}, Sensitive: true, OutputLimit: 16 << 20})
	if err != nil {
		return errors.New("whole writer invocation journal unavailable")
	}
	started, ready := false, false
	var earliest int64
	lines := bytes.Split(bytes.TrimSpace(out), []byte("\n"))
	if len(lines) >= 20000 {
		return errors.New("invocation journal truncated; cannot prove refund absence")
	}
	for _, line := range lines {
		var entry struct {
			Message   string `json:"MESSAGE"`
			Timestamp string `json:"__REALTIME_TIMESTAMP"`
			Cursor    string `json:"__CURSOR"`
		}
		if json.Unmarshal(line, &entry) != nil || entry.Cursor == "" {
			return errors.New("incomplete invocation journal evidence")
		}
		ts, e := strconv.ParseInt(entry.Timestamp, 10, 64)
		if e != nil || ts <= 0 {
			return errors.New("invalid invocation journal timestamp")
		}
		if earliest == 0 || ts < earliest {
			earliest = ts
		}
		if strings.Contains(entry.Message, "请求失败, 返还预扣费") {
			return errors.New("untracked asynchronous refund intent in writer invocation; completion cannot be proven, reconciliation required")
		}
		started = started || strings.Contains(entry.Message, " "+manifest.OldVersion+" started") || strings.Contains(entry.Message, " "+manifest.ExpectedVersion+" started")
		ready = ready || strings.Contains(entry.Message, "ready in ")
	}
	if !started || !ready || earliest == 0 {
		return errors.New("writer startup missing; cannot prove complete refund intent history")
	}
	// Suppression is logged by journald, not necessarily attributed to the
	// application invocation. Reject any known loss in the retained window.
	loss, err := runtime.runner.Run(ctx, productionCommand{Name: commandJournalctl, Args: []string{"--no-pager", "--output=cat", "--since", "@" + strconv.FormatInt(earliest/1000000, 10), "-u", "systemd-journald.service"}, Sensitive: true, OutputLimit: 1 << 20})
	if err != nil {
		return errors.New("journal loss audit unavailable")
	}
	for _, word := range []string{"suppressed", "dropped", "missed", "corrupt"} {
		if strings.Contains(strings.ToLower(string(loss)), word) {
			return errors.New("journal loss/suppression evidence prevents refund absence proof")
		}
	}
	g.InvocationJournalSHA256 = fmt.Sprintf("%x", sha256Bytes(out))
	return nil
}

func (runtime *productionRuntime) reopenBillingAdmission(ctx context.Context, workspace productionWorkspace, manifest *productionManifest) error {
	if manifest.BillingGate == nil {
		return nil
	}
	g := manifest.BillingGate
	original, err := readPrivateRegularFile(filepath.Join(workspace.root, fmt.Sprintf("billing-locations.%d", g.Sequence)), 1<<20)
	if err != nil || fmt.Sprintf("%x", sha256Bytes(original)) != g.OriginalSHA256 {
		return errors.New("billing restore evidence mismatch")
	}
	barrier, err := billingBarrier(original, workspace.id)
	if err != nil {
		return err
	}
	path := filepath.Join(runtime.paths.NginxRoot, "lmm-api-locations.conf")
	current, err := os.ReadFile(path)
	if err != nil || (string(current) != string(barrier) && string(current) != string(original)) {
		return errors.New("billing barrier changed before restore")
	}
	if err := writeAtomicRegularFile(path, original, 0644); err != nil {
		return err
	}
	if _, err := runtime.runner.Run(ctx, productionCommand{Name: commandNginx, Args: []string{"-t"}}); err != nil {
		return err
	}
	if _, err := runtime.runner.Run(ctx, productionCommand{Name: commandSystemctl, Args: []string{"reload", "nginx"}}); err != nil {
		return err
	}
	if digest, err := sha256File(path); err != nil || digest != g.OriginalSHA256 {
		return errors.New("billing restore hash mismatch")
	}
	manifest.BillingGate.AdmissionReopened = true
	runtime.billingAdmissionClosed = false
	return runtime.writeManifest(workspace, *manifest)
}

func (runtime *productionRuntime) refuseManagedBillingRollback(ctx context.Context, workspace productionWorkspace, manifest productionManifest) error {
	environment, err := readPrivateRegularFile(filepath.Join(workspace.configRestore, "lmm-api-go.env"), 1<<20)
	if err != nil {
		return err
	}
	values, err := parseProductionEnvironment(environment)
	if err != nil {
		return err
	}
	databaseURL, childEnvironment, err := productionDatabaseCommand(values)
	if err != nil {
		return err
	}
	if !isDatabaseSchema(manifest.DatabaseSchema) {
		return errors.New("invalid rollback billing schema")
	}
	// to_jsonb handles old tables without billing_managed. Missing tables,
	// malformed flags, denied access and unavailable DB all fail closed.
	query := `SELECT COUNT(*) FROM "` + manifest.DatabaseSchema + `".subscription_pre_consume_records AS r WHERE COALESCE((to_jsonb(r)->>'billing_managed')::boolean, false)`
	out, err := runtime.runner.Run(ctx, productionCommand{Name: commandPSQL, Args: []string{"-X", "-v", "ON_ERROR_STOP=1", "--no-align", "--tuples-only", "--command", query, databaseURL}, Env: childEnvironment, Sensitive: true})
	if err != nil {
		return errors.New("cannot verify managed billing rollback eligibility")
	}
	if strings.TrimSpace(string(out)) != "0" {
		return errors.New("managed billing records block old writer rollback; keep admission closed and reconcile with compatible code")
	}
	return nil
}
