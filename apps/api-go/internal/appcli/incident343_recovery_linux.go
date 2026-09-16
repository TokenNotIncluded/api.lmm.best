//go:build linux

package appcli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const incident343Deployment = "release-go-v0.2.51-35116594330-attempt-1"
const incident343Revision = "13b22e03ea2c88fa118814c34f780aa6e77af723"

// Incident343SchemaRepair is supplied only by the reviewed, separately built
// recovery executable. It must atomically create the three absent RedPacket
// models under MigrationAdvisoryLockKey, without altering existing relations.
// No callback or arbitrary command is accepted by the installed HTTP service.
type Incident343SchemaRepair func(context.Context, string, string) error

// RunIncident343Recovery resumes the already installed, signed candidate. It
// does not install a package, change billing data, downgrade, or auto-confirm.
func RunIncident343Recovery(args []string, stdout, stderr io.Writer, repair Incident343SchemaRepair) int {
	if len(args) != 2 || args[0] != "--confirm" || args[1] != "api.lmm.best" || repair == nil {
		_, _ = fmt.Fprintln(stderr, "usage: incident343-recovery --confirm api.lmm.best")
		return ExitUsage
	}
	runtime := defaultProductionRuntime()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	result, err := runtime.recoverIncident343(ctx, repair)
	if err != nil {
		// The ops transport retains this output in its private server-side log.
		_, _ = fmt.Fprintf(stderr, "incident343-recovery: %v\n", err)
		return ExitError
	}
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return ExitError
	}
	_, _ = stdout.Write(append(data, '\n'))
	return ExitOK
}

func validateIncident343Identity(manifest productionManifest) error {
	if manifest.DeploymentID != incident343Deployment || manifest.ExpectedVersion != "0.2.51" ||
		manifest.Go.CandidateIdentity != "lmm-api-go-bin 0.2.51-1" || manifest.Go.CandidateGitRevision != incident343Revision ||
		manifest.Web.CandidateIdentity != "lmm-api-web-bin 0.1.71-2" {
		return errors.New("recovery is restricted to the recorded incident 343 signed package pair")
	}
	return nil
}

func validateInstalledSchemaRecovery(manifest productionManifest, status productionStatus) error {
	if status.Phase != "ROLLBACK_REQUIRED" || status.DeploymentID != manifest.DeploymentID ||
		!manifest.Go.Changed || manifest.Web.Changed || !manifest.ObservationStartedUTC.IsZero() {
		return errors.New("recovery requires a failed pre-observation Go-only activation")
	}
	if manifest.ObservationSeconds < 120 || manifest.ObservationSeconds > 360 {
		return errors.New("original observation window must remain between 120 and 360 seconds")
	}
	g := manifest.BillingGate
	if g == nil || !g.AdmissionClosed || g.AdmissionReopened || !g.StopVerified || g.GoPID <= 1 ||
		g.Sequence < 1 || len(g.GoInvocationID) != 32 || g.StopStartedUTC.IsZero() || !productionSHA256Pattern.MatchString(g.ShutdownJournalSHA256) ||
		!productionSHA256Pattern.MatchString(g.OriginalSHA256) {
		return errors.New("original billing admission and verified old-writer shutdown evidence are required")
	}
	if manifest.Frontend.OldTarget != manifest.Frontend.NewTarget || manifest.Frontend.OldIndexSHA256 != manifest.Frontend.NewIndexSHA256 {
		return errors.New("recovery cannot switch the frontend")
	}
	return nil
}

func (runtime *productionRuntime) recoverIncident343(ctx context.Context, repair Incident343SchemaRepair) (productionStatus, error) {
	if runtime.effectiveUID() != 0 {
		return productionStatus{}, errors.New("must run as root")
	}
	host, err := runtime.hostname()
	if err != nil || host != runtime.paths.ExpectedHost {
		return productionStatus{}, errors.New("production host identity mismatch")
	}
	workspace, err := runtime.openWorkspace(filepath.Join(runtime.paths.WorkRoot, incident343Deployment))
	if err != nil {
		return productionStatus{}, err
	}
	lock, err := runtime.acquireGlobalLock(ctx)
	if err != nil {
		return productionStatus{}, err
	}
	defer func() { _ = unlockDeploymentFile(lock); _ = lock.Close() }()
	manifest, err := runtime.readManifest(workspace)
	if err != nil {
		return productionStatus{}, err
	}
	if err := validateIncident343Identity(manifest); err != nil {
		return productionStatus{}, err
	}
	return runtime.recoverInstalledSchema(ctx, workspace, manifest, repair)
}

func (runtime *productionRuntime) verifyClosedRecoveryAdmission(workspace productionWorkspace, manifest productionManifest) error {
	if err := runtime.validateTransactionLock(workspace); err != nil {
		return err
	}
	original, err := readPrivateRegularFile(filepath.Join(workspace.root, fmt.Sprintf("billing-locations.%d", manifest.BillingGate.Sequence)), 1<<20)
	if err != nil || fmt.Sprintf("%x", sha256Bytes(original)) != manifest.BillingGate.OriginalSHA256 {
		return errors.New("original billing admission evidence mismatch")
	}
	barrier, err := billingBarrier(original, workspace.id)
	if err != nil {
		return err
	}
	current, err := readSafeRegularFile(filepath.Join(runtime.paths.NginxRoot, "lmm-api-locations.conf"), 1<<20)
	if err != nil || !bytes.Equal(current, barrier) {
		return errors.New("exact transaction billing barrier is not installed")
	}
	return nil
}

func (runtime *productionRuntime) recoveryDatabase(ctx context.Context, workspace productionWorkspace, manifest productionManifest) (string, string, []string, error) {
	live, err := readPrivateRegularFile(filepath.Join(runtime.paths.ConfigDir, "lmm-api-go.env"), 1<<20)
	if err != nil {
		return "", "", nil, err
	}
	archived, err := readPrivateRegularFile(filepath.Join(workspace.configRestore, "lmm-api-go.env"), 1<<20)
	if err != nil || fmt.Sprintf("%x", sha256Bytes(archived)) != manifest.EnvironmentRestoreSHA256 {
		return "", "", nil, errors.New("archived environment identity mismatch")
	}
	values, err := parseProductionEnvironment(live)
	if err != nil {
		return "", "", nil, errors.New("live environment is not safely parseable")
	}
	before, err := parseProductionEnvironment(archived)
	if err != nil {
		return "", "", nil, errors.New("archived environment is not safely parseable")
	}
	dsn, err := productionDatabaseURL(values)
	if err != nil {
		return "", "", nil, err
	}
	oldDSN, err := productionDatabaseURL(before)
	if err != nil || dsn != oldDSN {
		return "", "", nil, errors.New("database connection changed since activation")
	}
	if !isDatabaseSchema(manifest.DatabaseSchema) {
		return "", "", nil, errors.New("invalid recorded database schema")
	}
	databaseURL, environment, err := productionDatabaseCommand(values)
	if err != nil {
		return "", "", nil, err
	}
	out, err := runtime.runner.Run(ctx, productionCommand{Name: commandPSQL,
		Args: []string{"-X", "--no-password", "-At", "-v", "ON_ERROR_STOP=1", "-c", "SELECT pg_catalog.current_schema()", databaseURL},
		Env:  environment, Sensitive: true, Timeout: 15 * time.Second, OutputLimit: 4096})
	if err != nil || strings.TrimSpace(string(out)) != manifest.DatabaseSchema {
		return "", "", nil, errors.New("live database search path does not match recorded schema")
	}
	return dsn, databaseURL, environment, nil
}

func (runtime *productionRuntime) requireAbsentRedPacketTables(ctx context.Context, databaseURL string, environment []string, schema string) error {
	// schema has already passed isDatabaseSchema. Query catalog metadata only.
	query := fmt.Sprintf(`SELECT count(*) FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='%s' AND c.relname IN ('red_packets','red_packet_items','red_packet_claims')`, schema)
	out, err := runtime.runner.Run(ctx, productionCommand{Name: commandPSQL,
		Args: []string{"-X", "--no-password", "-At", "-v", "ON_ERROR_STOP=1", "-c", query, databaseURL},
		Env:  environment, Sensitive: true, Timeout: 15 * time.Second, OutputLimit: 4096})
	if err != nil || strings.TrimSpace(string(out)) != "0" {
		return errors.New("all three red-packet relations must be absent; no existing table will be altered")
	}
	return nil
}

func (runtime *productionRuntime) snapshotRecoveryDatabase(ctx context.Context, audit, databaseURL string, environment []string) error {
	out, err := runtime.runner.Run(ctx, productionCommand{Name: commandPSQL,
		Args: []string{"-X", "--no-password", "-At", "-v", "ON_ERROR_STOP=1", "-c", "SELECT pg_catalog.pg_database_size(pg_catalog.current_database())", databaseURL},
		Env:  environment, Sensitive: true, Timeout: 15 * time.Second, OutputLimit: 4096})
	if err != nil {
		return errors.New("database snapshot size preflight failed")
	}
	size, err := strconv.ParseUint(strings.TrimSpace(string(out)), 10, 64)
	if err != nil || size == 0 || size > 2<<30 {
		return errors.New("database size exceeds bounded incident backup policy")
	}
	var disk syscall.Statfs_t
	if err := syscall.Statfs(audit, &disk); err != nil {
		return err
	}
	free := disk.Bavail * uint64(disk.Bsize)
	if free < size*2+(1<<30) {
		return errors.New("insufficient space for a fresh incident database snapshot")
	}
	backup := filepath.Join(audit, "before-schema.database.dump")
	if _, err := os.Lstat(backup); !errors.Is(err, os.ErrNotExist) {
		return errors.New("incident database snapshot already exists")
	}
	if _, err := runtime.runner.Run(ctx, productionCommand{Name: commandPGDump,
		Args: []string{"--no-password", "--format=custom", "--file=" + backup, databaseURL}, Env: environment,
		Sensitive: true, Timeout: 2 * time.Minute}); err != nil {
		return errors.New("fresh incident database backup failed")
	}
	if err := os.Chmod(backup, 0600); err != nil {
		return err
	}
	info, err := os.Lstat(backup)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() <= 0 || info.Size() > 2<<30 || info.Mode().Perm() != 0600 {
		return errors.New("incident database snapshot is unsafe")
	}
	owner, links, ok := deploymentFileOwnership(info)
	if !ok || owner != runtime.requiredOwnerUID || links != 1 {
		return errors.New("incident database snapshot ownership mismatch")
	}
	if _, err := runtime.runner.Run(ctx, productionCommand{Name: commandPGRestore, Args: []string{"--list", backup},
		Sensitive: true, Timeout: 20 * time.Second, OutputLimit: 4 << 20}); err != nil {
		return errors.New("incident database snapshot cannot be inspected")
	}
	digest, err := sha256File(backup)
	if err != nil {
		return err
	}
	return writeAtomicRegularFile(filepath.Join(audit, "database.sha256"), []byte(digest+"\n"), 0600)
}

func (runtime *productionRuntime) recoverInstalledSchema(ctx context.Context, workspace productionWorkspace, manifest productionManifest, repair Incident343SchemaRepair) (result productionStatus, returnErr error) {
	status, err := runtime.readStatus(workspace)
	if err != nil {
		return productionStatus{}, err
	}
	if err := validateInstalledSchemaRecovery(manifest, status); err != nil {
		return productionStatus{}, err
	}
	if err := runtime.verifyClosedRecoveryAdmission(workspace, manifest); err != nil {
		return productionStatus{}, err
	}
	if err := runtime.verifyManifestArchives(ctx, workspace, manifest); err != nil {
		return productionStatus{}, err
	}
	if err := runtime.verifyManifestInstalled(ctx, manifest, false); err != nil {
		return productionStatus{}, err
	}
	if err := verifyFrontendIdentity(runtime.paths.FrontendRoot, manifest.Frontend.NewTarget, manifest.Frontend.NewIndexSHA256); err != nil {
		return productionStatus{}, err
	}
	if err := validateMemoryOverrides(runtime.paths.DropInDir); err != nil {
		return productionStatus{}, err
	}
	if err := waitIncident343Quiescent(ctx, func(readCtx context.Context) (map[string]string, error) {
		return runtime.billingUnitState(readCtx, runtime.paths.Service)
	}, sleepIncident343); err != nil {
		return productionStatus{}, err
	}
	dsn, databaseURL, environment, err := runtime.recoveryDatabase(ctx, workspace, manifest)
	if err != nil {
		return productionStatus{}, err
	}
	if err := runtime.requireAbsentRedPacketTables(ctx, databaseURL, environment, manifest.DatabaseSchema); err != nil {
		return productionStatus{}, err
	}
	audit := filepath.Join(workspace.stateDir, "incident-343-schema-recovery")
	if err := os.Mkdir(audit, 0700); err != nil {
		return productionStatus{}, errors.New("incident recovery evidence already exists; inspect it instead of replaying")
	}
	for name, data := range map[string]any{"before-manifest.json": manifest, "before-status.json": status} {
		encoded, err := json.MarshalIndent(data, "", "  ")
		if err != nil {
			return productionStatus{}, err
		}
		if err := writeAtomicRegularFile(filepath.Join(audit, name), append(encoded, '\n'), 0600); err != nil {
			return productionStatus{}, err
		}
	}
	if err := runtime.snapshotRecoveryDatabase(ctx, audit, databaseURL, environment); err != nil {
		return productionStatus{}, err
	}
	if err := runtime.verifyClosedRecoveryAdmission(workspace, manifest); err != nil {
		return productionStatus{}, err
	}
	// No forced kill, package downgrade, manual receipt, or admission removal.
	if _, err := runtime.runner.Run(ctx, productionCommand{Name: commandSystemctl, Args: []string{"stop", runtime.paths.Service}, Timeout: time.Minute}); err != nil {
		return productionStatus{}, errors.New("failed service could not be stopped normally")
	}
	defer func() {
		if returnErr != nil {
			failed := productionStatus{Phase: "ROLLBACK_REQUIRED", Version: manifest.ExpectedVersion, Previous: manifest.OldVersion, Reason: "incident-343-forward-recovery-failure", Failure: returnErr.Error()}
			if err := runtime.writeStatus(workspace, failed); err != nil {
				returnErr = errors.Join(returnErr, err)
			}
		}
	}()
	state, err := runtime.billingUnitState(ctx, runtime.paths.Service)
	if err != nil || state["MainPID"] != "0" || state["ActiveState"] != "inactive" || state["ControlGroup"] != "" {
		return productionStatus{}, errors.New("failed service cgroup is not verifiably empty")
	}
	if err := runtime.requireAbsentRedPacketTables(ctx, databaseURL, environment, manifest.DatabaseSchema); err != nil {
		return productionStatus{}, err
	}
	if err := repair(ctx, dsn, manifest.DatabaseSchema); err != nil {
		return productionStatus{}, fmt.Errorf("additive red-packet schema repair failed: %w", err)
	}
	if err := writeAtomicRegularFile(filepath.Join(audit, "schema-created.json"), []byte("{\"created\":[\"red_packets\",\"red_packet_items\",\"red_packet_claims\"],\"existing_data_modified\":false}\n"), 0600); err != nil {
		return productionStatus{}, err
	}
	if err := runtime.runMigration(ctx, workspace, manifest, migrationRun{name: "incident343-candidate-verify", binary: runtime.paths.InstalledBinary, mode: "verify"}); err != nil {
		return productionStatus{}, err
	}
	if _, err := runtime.runner.Run(ctx, productionCommand{Name: commandSystemctl, Args: []string{"reset-failed", runtime.paths.Service}}); err != nil {
		return productionStatus{}, err
	}
	if _, err := runtime.runner.Run(ctx, productionCommand{Name: commandSystemctl, Args: []string{"start", runtime.paths.Service}, Timeout: time.Minute}); err != nil {
		return productionStatus{}, errors.New("corrected candidate did not start")
	}
	baseline, err := runtime.readServiceRestarts(ctx)
	if err != nil || baseline != 0 {
		return productionStatus{}, errors.New("corrected candidate restarted during readiness")
	}
	manifest.ServiceRestartBaseline = 0
	if err := runtime.probeBackendLocalEventually(ctx, workspace, manifest, manifest.ExpectedVersion); err != nil {
		return productionStatus{}, err
	}
	if err := runtime.verifyServiceRestartBaseline(ctx, manifest); err != nil {
		return productionStatus{}, err
	}
	if err := runtime.verifyManifestArchives(ctx, workspace, manifest); err != nil {
		return productionStatus{}, err
	}
	if err := runtime.verifyManifestInstalled(ctx, manifest, false); err != nil {
		return productionStatus{}, err
	}
	if err := runtime.checkMemoryHeadroom(ctx); err != nil {
		return productionStatus{}, err
	}
	if err := runtime.verifyClosedRecoveryAdmission(workspace, manifest); err != nil {
		return productionStatus{}, err
	}
	// This is the original native admission restoration, after its signed
	// candidate is healthy and the original old-writer stop remains proved.
	if err := runtime.reopenBillingAdmission(ctx, workspace, &manifest); err != nil {
		return productionStatus{}, err
	}
	manifest.ObservationStartedUTC = utcSecond(runtime.now())
	if err := runtime.writeManifest(workspace, manifest); err != nil {
		return productionStatus{}, err
	}
	observing := productionStatus{Phase: "OBSERVING", Version: manifest.ExpectedVersion, Previous: manifest.OldVersion, Reason: "incident-343-additive-schema-recovery", ObservationSec: manifest.ObservationSeconds}
	if err := runtime.writeStatus(workspace, observing); err != nil {
		return productionStatus{}, err
	}
	if err := runtime.observe(ctx, workspace, manifest, time.Duration(manifest.ObservationSeconds)*time.Second); err != nil {
		return productionStatus{}, err
	}
	awaiting := observing
	awaiting.Phase = "AWAITING_CONFIRMATION"
	if err := runtime.writeStatus(workspace, awaiting); err != nil {
		return productionStatus{}, err
	}
	// Confirmation remains a separate normal native operation after evidence review.
	return runtime.readStatus(workspace)
}
