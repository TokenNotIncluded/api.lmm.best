//go:build linux

package appcli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const incident343PostStopFailure = "failed service cgroup is not verifiably empty"
const incident343CompletedBackupSHA = "c0dbfe8128fd27637203d3c66817c754917c737d7bb405ff0736c4ae0799bbf3"

// This is a new, bounded attempt from the observed full-backup/pre-DDL failure.
// The completed backup and both older audit checkpoints remain untouched.
func (runtime *productionRuntime) prepareIncident343ForwardAudit(ctx context.Context, workspace productionWorkspace, manifest productionManifest, status productionStatus) (string, error) {
	if status.Reason != "incident-343-forward-recovery-failure" || status.Failure != incident343PostStopFailure {
		return runtime.prepareIncident343Audit(workspace, manifest, status)
	}
	if err := validateIncident343Identity(manifest); err != nil {
		return "", err
	}
	if err := validateInstalledSchemaRecovery(manifest, status); err != nil {
		return "", err
	}
	original := filepath.Join(workspace.stateDir, "incident-343-schema-recovery")
	retry := filepath.Join(original, "backup-retry-1")
	if err := runtime.requireIncident343Entries(original, []string{"before-manifest.json", "before-status.json", "before-schema.database.dump", "backup-retry-1"}); err != nil {
		return "", err
	}
	if err := runtime.requireIncident343Entries(retry, []string{"before-manifest.json", "before-status.json", "before-schema.database.dump", "before-schema.database.dump.stderr", "database.sha256", "resume.json"}); err != nil {
		return "", err
	}
	current, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return "", err
	}
	current = append(current, '\n')
	for _, dir := range []string{original, retry} {
		data, err := readPrivateRegularFile(filepath.Join(dir, "before-manifest.json"), 1<<20)
		if err != nil || !bytes.Equal(data, current) {
			return "", errors.New("post-stop manifest no longer matches the recorded transaction")
		}
	}
	previous, err := readPrivateRegularFile(filepath.Join(original, "before-status.json"), 1<<20)
	if err != nil {
		return "", err
	}
	retryStatus, err := readPrivateRegularFile(filepath.Join(retry, "before-status.json"), 1<<20)
	if err != nil || !bytes.Equal(previous, retryStatus) {
		return "", errors.New("post-stop prior status evidence changed")
	}
	var before productionStatus
	if json.Unmarshal(previous, &before) != nil || validateInstalledSchemaRecovery(manifest, before) != nil {
		return "", errors.New("invalid pre-backup native state")
	}
	empty, err := readPrivateRegularFile(filepath.Join(original, "before-schema.database.dump"), 1)
	if err != nil || len(empty) != 0 {
		return "", errors.New("original failed backup changed")
	}
	stderr, err := readPrivateRegularFile(filepath.Join(retry, "before-schema.database.dump.stderr"), 1)
	if err != nil || len(stderr) != 0 {
		return "", errors.New("completed backup stderr changed")
	}
	receipt, err := readPrivateRegularFile(filepath.Join(retry, "resume.json"), 4096)
	if err != nil || string(receipt) != "{\"prior_checkpoint\":\"empty-backup-before-mutation\",\"prior_evidence_preserved\":true,\"fresh_complete_backup_required\":true}\n" {
		return "", errors.New("backup retry receipt changed")
	}
	backup := filepath.Join(retry, "before-schema.database.dump")
	info, err := os.Lstat(backup)
	if err != nil || info.Size() != 14029147 {
		return "", errors.New("recorded complete backup size changed")
	}
	digest, err := sha256File(backup)
	if err != nil || digest != incident343CompletedBackupSHA {
		return "", errors.New("recorded complete backup digest changed")
	}
	checksum, err := readPrivateRegularFile(filepath.Join(retry, "database.sha256"), 128)
	if err != nil || string(checksum) != digest+"\n" {
		return "", errors.New("recorded complete backup checksum changed")
	}
	if _, err := runtime.runner.Run(ctx, productionCommand{Name: commandPGRestore, Args: []string{"--list", backup}, Sensitive: true, Timeout: 20 * time.Second, OutputLimit: 4 << 20}); err != nil {
		return "", errors.New("recorded complete backup catalog is invalid")
	}
	// Consume this exact checkpoint once; do not reuse its dump for a new DDL.
	next := filepath.Join(original, "post-stop-retry-1")
	if err := os.Mkdir(next, 0700); err != nil {
		return "", errors.New("post-stop retry already exists; inspect instead of replaying")
	}
	for name, value := range map[string]any{"before-manifest.json": manifest, "before-status.json": status} {
		data, err := json.MarshalIndent(value, "", "  ")
		if err != nil {
			return "", err
		}
		if err := writeAtomicRegularFile(filepath.Join(next, name), append(data, '\n'), 0600); err != nil {
			return "", err
		}
	}
	receipt = []byte("{\"prior_checkpoint\":\"complete-backup-before-ddl\",\"prior_evidence_preserved\":true,\"fresh_complete_backup_required\":true}\n")
	if err := writeAtomicRegularFile(filepath.Join(next, "resume.json"), receipt, 0600); err != nil {
		return "", err
	}
	return next, nil
}

func (runtime *productionRuntime) requireIncident343Entries(directory string, names []string) error {
	if err := runtime.requireOwnedSafePath(directory, true); err != nil {
		return err
	}
	info, err := os.Lstat(directory)
	if err != nil || info.Mode().Perm() != 0700 {
		return errors.New("post-stop audit directory is not private")
	}
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != len(names) {
		return errors.New("post-stop audit has unexpected progress or files")
	}
	allowed := make(map[string]bool, len(names))
	for _, name := range names {
		allowed[name] = true
	}
	for _, entry := range entries {
		if !allowed[entry.Name()] {
			return errors.New("post-stop checkpoint entry mismatch")
		}
		path := filepath.Join(directory, entry.Name())
		isDir := entry.Name() == "backup-retry-1"
		if err := runtime.requireOwnedSafePath(path, isDir); err != nil {
			return err
		}
		info, err := os.Lstat(path)
		if err != nil || (!isDir && info.Mode().Perm() != 0600) {
			return errors.New("unsafe post-stop checkpoint entry")
		}
	}
	return nil
}

type incident343StoppedVerifier interface {
	VerifyIncident343Stopped(context.Context, string) error
}

func (runtime *productionRuntime) verifyIncident343Stopped(ctx context.Context) error {
	if verifier, ok := runtime.runner.(incident343StoppedVerifier); ok {
		return verifier.VerifyIncident343Stopped(ctx, runtime.paths.Service)
	}
	// Existing injected runners retain the stricter normal-exit fixture contract.
	state, err := runtime.billingUnitState(ctx, runtime.paths.Service)
	if err != nil || state["MainPID"] != "0" || state["ActiveState"] != "inactive" || state["ControlGroup"] != "" {
		return errors.New(incident343PostStopFailure)
	}
	return nil
}

type incident343UnitReader interface {
	ReadIncident343Unit(context.Context, string) (map[string]string, error)
}

func (runtime *productionRuntime) incident343RecoveryUnitState(ctx context.Context) (map[string]string, error) {
	if reader, ok := runtime.runner.(incident343UnitReader); ok {
		return reader.ReadIncident343Unit(ctx, runtime.paths.Service)
	}
	return runtime.billingUnitState(ctx, runtime.paths.Service)
}

func parseIncident343UnitProperties(raw []byte) (map[string]string, error) {
	if len(raw) == 0 || len(raw) > 8192 {
		return nil, errors.New("invalid stopped-unit evidence size")
	}
	state := make(map[string]string)
	for _, line := range strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return nil, errors.New("malformed stopped-unit evidence")
		}
		if _, exists := state[key]; exists {
			return nil, errors.New("duplicate stopped-unit evidence")
		}
		state[key] = value
	}
	for _, key := range []string{"LoadState", "MainPID", "ControlPID", "ActiveState", "SubState", "ControlGroup"} {
		if _, ok := state[key]; !ok {
			return nil, errors.New("missing stopped-unit evidence")
		}
	}
	if len(state) != 6 {
		return nil, errors.New("unexpected stopped-unit properties")
	}
	return state, nil
}

func parseIncident343StoppedState(raw []byte) error {
	state, err := parseIncident343UnitProperties(raw)
	if err != nil {
		return err
	}
	return validateIncident343StoppedState(state)
}

func validateIncident343StoppedState(state map[string]string) error {
	if state["LoadState"] != "loaded" || state["MainPID"] != "0" || state["ControlPID"] != "0" || state["ControlGroup"] != "" {
		return errors.New(incident343PostStopFailure)
	}
	inactive := state["ActiveState"] == "inactive" && state["SubState"] == "dead"
	failed := state["ActiveState"] == "failed" && state["SubState"] == "failed"
	if !inactive && !failed {
		return errors.New("candidate is not in a stopped terminal state")
	}
	return nil
}

func (runner osProductionCommandRunner) ReadIncident343Unit(ctx context.Context, unit string) (map[string]string, error) {
	if unit != "lmm-api.service" {
		return nil, errors.New("unexpected incident service")
	}
	output, err := runner.Run(ctx, productionCommand{Name: commandSystemctl, Args: []string{"show", unit, "--all", "--property=LoadState,MainPID,ControlPID,ActiveState,SubState,ControlGroup"}, Timeout: 15 * time.Second, OutputLimit: 8192})
	if err != nil {
		return nil, errors.New("cannot read explicit stopped-unit properties")
	}
	return parseIncident343UnitProperties(output)
}

func (runner osProductionCommandRunner) VerifyIncident343Stopped(ctx context.Context, unit string) error {
	state, err := runner.ReadIncident343Unit(ctx, unit)
	if err != nil {
		return err
	}
	if err := validateIncident343StoppedState(state); err != nil {
		return err
	}
	// On this incident's cgroup-v2 host, an absent unit cgroup plus explicit zero
	// main/control PIDs proves that a retained failed-start result is not a writer.
	var fs syscall.Statfs_t
	if err := syscall.Statfs("/sys/fs/cgroup", &fs); err != nil || fs.Type != 0x63677270 {
		return errors.New("expected cgroup-v2 filesystem is unavailable")
	}
	parent, err := os.Lstat("/sys/fs/cgroup/system.slice")
	if err != nil || !parent.IsDir() || parent.Mode()&os.ModeSymlink != 0 {
		return errors.New("system cgroup parent is unavailable")
	}
	if _, err := os.Lstat("/sys/fs/cgroup/system.slice/lmm-api.service"); !errors.Is(err, os.ErrNotExist) {
		return errors.New("candidate cgroup has not been removed after stop")
	}
	return nil
}
