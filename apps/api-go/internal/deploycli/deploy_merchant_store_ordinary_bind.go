package deploycli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/internal/deploymentfence"
)

type merchantStoreOrdinaryStopped struct {
	Format                string `json:"format"`
	Release               string `json:"release"`
	PreviousSHA256        string `json:"previous_sha256"`
	PID                   int    `json:"pid"`
	InvocationID          string `json:"invocation_id"`
	ShutdownJournalSHA256 string `json:"shutdown_journal_sha256"`
}

type merchantStoreOrdinaryBindIntent struct {
	Format          string                       `json:"format"`
	Capsule         productionReleaseFilePlan    `json:"capsule"`
	Predecessor     productionReleaseFilePlan    `json:"predecessor"`
	Stopped         merchantStoreOrdinaryStopped `json:"stopped"`
	OldHookSHA256   string                       `json:"old_hook_sha256"`
	NewHookSHA256   string                       `json:"new_hook_sha256"`
	OldEngineSHA256 string                       `json:"old_engine_sha256"`
	NewEngineSHA256 string                       `json:"new_engine_sha256"`
	StartedUTC      time.Time                    `json:"started_utc"`
}

func merchantStoreOrdinaryNewIntent(path string) error {
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		return errors.New("ordinary startup binding already has intent or uncertain state; inspect without replay")
	}
	return nil
}

func merchantStoreOrdinaryOwnerMatches(owner productionMerchantStoreFenceOwner, c productionMerchantStoreCapsule, planSHA, purpose string) bool {
	return validateMerchantStoreFenceOwner(owner) == nil && owner.DeploymentID == c.DeploymentID && owner.Host == c.Host &&
		owner.PlanSHA256 == planSHA && owner.ContractSHA256 == merchantStoreFenceContractSHA(c.Writer) && owner.ProviderSHA256 == c.Writer.Candidate.PayloadSHA256 &&
		owner.SystemIdentifier == c.Writer.SystemIdentifier && owner.Database == c.Writer.Database && owner.DatabaseOID == c.Writer.DatabaseOID &&
		owner.Schema == c.Writer.Schema && owner.SchemaOID == c.Writer.SchemaOID && owner.Role == c.Writer.Role && owner.Purpose == purpose
}

func merchantStoreOrdinaryManifestMatches(manifest productionManifest, c productionMerchantStoreCapsule) bool {
	return c.Writer != nil && c.ExistingSchemaContract != nil && manifest.DeploymentID == c.DeploymentID && manifest.SchemaPlanSHA256 == c.ControllerPlanSHA256 && manifest.MaintenanceHandoff == nil && manifest.MaintenanceCapture == nil &&
		manifest.Go.Changed && manifest.SchemaMode == productionSchemaModeVerifyExisting && manifest.MerchantStoreWriter != nil &&
		*manifest.MerchantStoreWriter == *c.Writer && manifest.ExistingSchemaContract != nil && *manifest.ExistingSchemaContract == *c.ExistingSchemaContract && manifest.DeployEngineSHA256 == c.Candidate.DeployEngineSHA256
}

// Never turn a failed parent RPC into permission for a second owner. The
// complete reserved-row set is observed before and after the live RPC.
func (runtime *productionRuntime) requestMerchantStoreOrdinaryParent(ctx context.Context, c productionMerchantStoreCapsule, digest string) (bool, error) {
	if c.Format != 1 || c.StartupPolicy != "per-start" || c.Writer == nil || c.ExistingSchemaContract == nil {
		return false, errors.New("ordinary parent requires an ordinary per-start capsule")
	}
	child, err := runtime.merchantStoreCapsuleEnvironment(ctx, c, digest, os.Getenv("INVOCATION_ID"))
	if err != nil {
		return false, err
	}
	values := map[string]string{}
	for _, assignment := range child {
		key, value, found := strings.Cut(assignment, "=")
		if !found {
			return false, errors.New("ordinary parent child environment is malformed")
		}
		if _, exists := values[key]; exists {
			return false, errors.New("ordinary parent child environment is duplicated")
		}
		values[key] = value
	}
	return runtime.requestMerchantStoreOrdinaryParentValues(ctx, c, digest, values)
}

func (runtime *productionRuntime) requestMerchantStoreOrdinaryParentValues(ctx context.Context, c productionMerchantStoreCapsule, digest string, values map[string]string) (bool, error) {
	if c.Format != 1 || c.StartupPolicy != "per-start" || validateMerchantStoreCapsule(c, runtime.paths) != nil || !productionSHA256Pattern.MatchString(digest) {
		return false, errors.New("ordinary parent capsule authority is invalid")
	}
	lease, err := acquireMerchantStoreDeploymentFence(ctx, values, c.Writer)
	if err != nil {
		return false, err
	}
	defer lease.Close()
	readRows := func() ([][2]string, error) {
		if err := lease.Check(ctx, c.Writer); err != nil {
			return nil, err
		}
		rows, err := lease.connection.Query(ctx, `SELECT key,value FROM "`+c.Writer.Schema+`".options WHERE `+deploymentfence.PostgreSQLPresencePredicate+` ORDER BY key,value LIMIT 2`)
		if err != nil {
			return nil, errors.New("ordinary parent reserved owner observation failed")
		}
		defer rows.Close()
		var found [][2]string
		for rows.Next() {
			var row [2]string
			if err := rows.Scan(&row[0], &row[1]); err != nil {
				return nil, err
			}
			found = append(found, row)
		}
		return found, rows.Err()
	}
	rows, err := readRows()
	if err != nil {
		return false, err
	}
	if len(rows) > 1 {
		return false, errors.New("ordinary startup refuses multiple reserved owners")
	}
	archRoot := filepath.Join(runtime.paths.WorkRoot, c.DeploymentID)
	_, archErr := os.Lstat(archRoot)
	if archErr != nil && !errors.Is(archErr, os.ErrNotExist) {
		return false, archErr
	}
	portablePath := merchantStorePortableOwnerPath(c, "")
	_, portableErr := os.Lstat(portablePath)
	if portableErr != nil && !errors.Is(portableErr, os.ErrNotExist) {
		return false, portableErr
	}
	if archErr == nil && portableErr == nil {
		return false, errors.New("ordinary startup has ambiguous Arch and portable parent evidence")
	}
	if archErr == nil {
		workspace, err := runtime.openWorkspaceForInspection(archRoot)
		if err != nil {
			return false, err
		}
		manifest, err := runtime.readManifestSchema(workspace)
		if err != nil || !merchantStoreOrdinaryManifestMatches(manifest, c) {
			return false, errors.New("ordinary Arch parent differs from the exact capsule plan")
		}
		owner, raw, err := runtime.readMerchantStoreFenceOwner(workspace, manifest)
		if err != nil || !merchantStoreOrdinaryOwnerMatches(owner, c, c.ControllerPlanSHA256, "") {
			return false, errors.New("ordinary Arch parent owner is not its exact original plan")
		}
		if len(rows) == 1 {
			if rows[0] != [2]string{merchantStoreFenceOwnerKey(owner), raw} {
				return false, errors.New("ordinary Arch startup found a foreign reserved owner")
			}
			if err := runtime.requestMerchantStoreFence(ctx, workspace, manifest, false); err != nil {
				return false, err
			}
			after, err := readRows()
			if err != nil || len(after) != 1 || after[0] != rows[0] {
				return false, errors.New("ordinary Arch live parent changed during startup")
			}
			return true, nil
		}
		status, err := runtime.readStatus(workspace)
		if err != nil || status.Phase != "CONFIRMED" {
			return false, errors.New("ordinary Arch parent absence lacks exact confirmed terminal state")
		}
		unit, unitErr := runtime.billingUnitState(ctx, owner.HolderUnit)
		clean := unitErr == nil && unit["MainPID"] == "0" && unit["ExecMainPID"] == strconv.Itoa(owner.HolderPID) && unit["InvocationID"] == owner.HolderInvocationID && unit["Restart"] == "no" && unit["ActiveState"] == "inactive" && unit["SubState"] == "dead" && unit["Result"] == "success" && unit["ExecMainCode"] == "1" && unit["ExecMainStatus"] == "0"
		if !clean {
			// --collect legitimately removes the original transient unit. Only
			// the explicit manager observation counts; an RPC/proc error does not.
			out, err := runtime.runner.Run(ctx, productionCommand{Name: commandSystemctl, Args: []string{"show", owner.HolderUnit, "--property=LoadState", "--value"}, Timeout: 15 * time.Second, OutputLimit: 128})
			if err != nil || strings.TrimSpace(string(out)) != "not-found" {
				return false, errors.New("ordinary Arch original holder exit is uncertain")
			}
		}
		if _, err := os.Lstat(filepath.Join("/proc", strconv.Itoa(owner.HolderPID))); !errors.Is(err, os.ErrNotExist) {
			return false, errors.New("ordinary Arch original holder PID is not verifiably gone")
		}
		socket, err := runtime.merchantStoreSocketPath(workspace.root, owner, false)
		if err != nil {
			return false, err
		}
		if _, err := os.Lstat(socket); !errors.Is(err, os.ErrNotExist) {
			return false, errors.New("ordinary Arch original holder socket still exists or is unknown")
		}
	} else if portableErr == nil {
		owner, raw, err := runtime.portableOwner(ctx, c, digest, "")
		if err != nil || !merchantStoreOrdinaryOwnerMatches(owner, c, digest, "portable-deploy") {
			return false, errors.New("ordinary portable parent is not its exact live capsule")
		}
		if len(rows) != 1 || rows[0] != [2]string{merchantStoreFenceOwnerKey(owner), string(raw)} {
			return false, errors.New("ordinary portable startup has a foreign or absent parent owner")
		}
		if err := runtime.requestMerchantStorePortableFence(ctx, c, digest, "", false); err != nil {
			return false, err
		}
		after, err := readRows()
		if err != nil || len(after) != 1 || after[0] != rows[0] {
			return false, errors.New("ordinary portable live parent changed during startup")
		}
		return true, nil
	} else {
		if len(rows) != 0 {
			return false, errors.New("ordinary startup has an unrecognized reserved owner")
		}
		if c.Writer.StartupPredecessorPath != "" {
			// New ordinary forward capsules have a real wrapper transaction;
			// absence of a parent receipt alone does not establish completion.
			work := filepath.Join(runtime.portableTransactionRoot(), c.DeploymentID)
			raw, err := runtime.ordinaryStandaloneFile(work, "state.json")
			var state struct {
				Release        string `json:"release"`
				Phase          string `json:"phase"`
				SHA256         string `json:"sha256"`
				PreviousSHA256 string `json:"previous_sha256"`
				Migrate        bool   `json:"migrate"`
			}
			if err != nil || !merchantStoreUniqueJSON(raw) || json.Unmarshal(raw, &state) != nil || state.Release != c.DeploymentID || state.Phase != "CONFIRMED" || state.Migrate || state.SHA256 != c.Writer.Candidate.PayloadSHA256 || state.PreviousSHA256 != c.Writer.Rollback.PayloadSHA256 {
				return false, errors.New("ordinary portable parent absence lacks its exact confirmed transaction")
			}
		}
	}
	// This is observation only. The existing per-start atomic claim checks the
	// reserved set again under its own owner-claim advisory transaction lock.
	after, err := readRows()
	if err != nil || len(after) != 0 {
		return false, errors.New("ordinary startup owner absence changed")
	}
	return false, nil
}

// The historical Python wrapper writes these two non-secret files with the
// process umask. Preserve their actual mode; their enclosing work is private.
func (runtime *productionRuntime) ordinaryStandaloneFile(work, name string) ([]byte, error) {
	if name != "state.json" && name != "shutdown.log" {
		return nil, errors.New("ordinary standalone file is not fixed")
	}
	if err := runtime.merchantStorePrivateDirectory(work, false); err != nil {
		return nil, err
	}
	path := filepath.Join(work, name)
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 && info.Mode().Perm() != 0644 {
		return nil, errors.New("ordinary standalone file mode is unsafe")
	}
	limit := int64(1 << 20)
	if name == "shutdown.log" {
		limit = 8 << 20
	}
	return runtime.readExistingSchemaSealedFileBounded(path, info.Mode().Perm() == 0600, limit)
}

func merchantStoreOrdinaryShutdownJournal(actual, saved []byte) error {
	// deploy-systemd.run strips the full invocation output before saving it.
	// Audit the real shutdown suffix, not unrelated earlier periodic flushes.
	if !bytes.Equal(bytes.TrimSpace(actual), bytes.TrimSpace(saved)) {
		return errors.New("ordinary invocation journal differs from its actual saved bytes")
	}
	rows := bytes.Split(actual, []byte{'\n'})
	start := -1
	for index, row := range rows {
		if bytes.Contains(bytes.ToLower(row), []byte("received signal:")) {
			start = index
		}
	}
	if start < 0 {
		return errors.New("ordinary invocation journal lacks a shutdown signal")
	}
	bounded := bytes.Join(rows[start:], []byte{'\n'})
	if validateBillingShutdownJournal(bounded) != nil || bytes.Count(bounded, []byte("refund_tasks execution_complete=")) != 1 {
		return errors.New("ordinary invocation shutdown/refund completion is incomplete")
	}
	return nil
}

func (runtime *productionRuntime) ordinaryStoppedWriter(ctx context.Context, c, old productionMerchantStoreCapsule) (merchantStoreOrdinaryStopped, error) {
	var stopped merchantStoreOrdinaryStopped
	archRoot := filepath.Join(runtime.paths.WorkRoot, c.DeploymentID)
	if _, err := os.Lstat(archRoot); err == nil {
		workspace, err := runtime.openWorkspaceForInspection(archRoot)
		if err != nil {
			return stopped, err
		}
		manifest, err := runtime.readManifestSchema(workspace)
		if err != nil || !merchantStoreOrdinaryManifestMatches(manifest, c) || manifest.BillingGate == nil {
			return stopped, errors.New("ordinary bind lacks its original Arch manifest/billing stop")
		}
		gate := manifest.BillingGate
		if !gate.AdmissionClosed || gate.AdmissionReopened || !gate.StopVerified || gate.GoPID <= 1 || !existingSchemaInvocationPattern.MatchString(gate.GoInvocationID) || !productionSHA256Pattern.MatchString(gate.ShutdownJournalSHA256) {
			return stopped, errors.New("ordinary bind Arch writer was not verifiably stopped behind admission")
		}
		unit, err := runtime.billingUnitState(ctx, c.Service)
		if err != nil || unit["InvocationID"] != gate.GoInvocationID || cleanBillingUnitExit(unit, gate.GoPID) != nil {
			return stopped, errors.New("ordinary bind lost the exact original Arch stopped invocation")
		}
		previousSHA := gate.ShutdownJournalSHA256
		if err := runtime.verifyBillingStopJournal(ctx, workspace, &manifest); err != nil || manifest.BillingGate.ShutdownJournalSHA256 != previousSHA {
			return stopped, errors.New("ordinary bind Arch shutdown journal changed")
		}
		return merchantStoreOrdinaryStopped{"lmm-ordinary-writer-stopped-v1", c.DeploymentID, old.Candidate.PayloadSHA256, gate.GoPID, gate.GoInvocationID, previousSHA}, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return stopped, err
	}
	work := filepath.Join(runtime.portableTransactionRoot(), c.DeploymentID)
	raw, err := runtime.readExistingSchemaSealedFile(filepath.Join(work, "ordinary-writer-stopped.json"), true)
	if err != nil || decodeControllerBackupJSON(raw, &stopped) != nil || stopped.Format != "lmm-ordinary-writer-stopped-v1" || stopped.Release != c.DeploymentID || stopped.PreviousSHA256 != old.Candidate.PayloadSHA256 || stopped.PID <= 1 || !existingSchemaInvocationPattern.MatchString(stopped.InvocationID) || !productionSHA256Pattern.MatchString(stopped.ShutdownJournalSHA256) {
		return stopped, errors.New("ordinary bind lacks the actual standalone stop receipt")
	}
	stateRaw, err := runtime.ordinaryStandaloneFile(work, "state.json")
	var state struct {
		Release            string `json:"release"`
		Phase              string `json:"phase"`
		SHA256             string `json:"sha256"`
		PreviousSHA256     string `json:"previous_sha256"`
		DeployEngineSHA256 string `json:"deploy_engine_sha256"`
		Migrate            bool   `json:"migrate"`
	}
	if err != nil || !merchantStoreUniqueJSON(stateRaw) || json.Unmarshal(stateRaw, &state) != nil || state.Release != c.DeploymentID || state.Phase != "MUTATION_PENDING" || state.Migrate || state.SHA256 != c.Candidate.PayloadSHA256 || state.PreviousSHA256 != old.Candidate.PayloadSHA256 || state.DeployEngineSHA256 != c.Candidate.DeployEngineSHA256 {
		return stopped, errors.New("ordinary bind standalone state differs from the current exact ordinary transaction")
	}
	unit, err := runtime.billingUnitState(ctx, c.Service)
	if err != nil || unit["InvocationID"] != stopped.InvocationID || cleanBillingUnitExit(unit, stopped.PID) != nil {
		return stopped, errors.New("ordinary bind lost the original standalone stopped invocation")
	}
	saved, err := runtime.ordinaryStandaloneFile(work, "shutdown.log")
	if err != nil || startupContentSHA256(saved) != stopped.ShutdownJournalSHA256 {
		return stopped, errors.New("ordinary bind standalone shutdown file changed")
	}
	actual, err := runtime.runner.Run(ctx, productionCommand{Name: commandJournalctl, Args: []string{"--no-pager", "-o", "cat", "_SYSTEMD_INVOCATION_ID=" + stopped.InvocationID}, Timeout: 30 * time.Second, OutputLimit: 8 << 20})
	if err != nil || merchantStoreOrdinaryShutdownJournal(actual, saved) != nil {
		return stopped, errors.New("ordinary bind standalone exact invocation shutdown audit failed")
	}
	return stopped, nil
}

// Binding is a one-shot ordinary same-schema transition. Neither a schema
// recovery override nor a supplied stop/approval boolean is accepted.
func (runtime *productionRuntime) bindMerchantStoreOrdinaryStartup(ctx context.Context, c productionMerchantStoreCapsule, digest string) error {
	if runtime.effectiveUID == nil || runtime.effectiveUID() != 0 || c.Format != 1 || c.StartupPolicy != "per-start" || c.Writer == nil || c.ExistingSchemaContract == nil || c.Writer.StartupTargetPath != filepath.Join(c.Root, "capsule.json") || validateMerchantStoreStartupTransitionPaths(c.Writer, runtime.paths) != nil || c.Writer.StartupPredecessorPath == "" {
		return errors.New("ordinary bind requires a native ordinary per-start target with its exact predecessor")
	}
	intentPath := filepath.Join(c.Root, "state", "ordinary-startup-bind-intent.json")
	if err := merchantStoreOrdinaryNewIntent(intentPath); err != nil {
		return err
	}
	actual, err := runtime.loadMerchantStoreCapsule(filepath.Join(c.Root, "capsule.json"), digest)
	want, _ := canonicalMerchantStoreCapsule(c)
	got, _ := canonicalMerchantStoreCapsule(actual)
	if err != nil || !bytes.Equal(want, got) {
		return errors.New("ordinary bind target capsule changed")
	}
	old, err := runtime.loadMerchantStoreCapsule(c.Writer.StartupPredecessorPath, c.Writer.StartupPredecessorSHA256)
	if err != nil || validateMerchantStoreStartupTransitionBinding(c.Writer, c.ExistingSchemaContract, old) != nil {
		return errors.New("ordinary bind predecessor is not the exact retained same-schema capsule")
	}
	stopped, err := runtime.ordinaryStoppedWriter(ctx, c, old)
	if err != nil {
		return err
	}
	if err := runtime.verifyMerchantStorePortableInstalled(old, old.Candidate.PayloadSHA256); err != nil {
		return err
	}
	loaded, err := runtime.loadedExistingSchemaUnit(ctx)
	if err != nil || loaded["MainPID"] != "0" || loaded["ActiveState"] != "inactive" || loaded["InvocationID"] != stopped.InvocationID {
		return errors.New("ordinary bind requires its exact original stopped loaded unit")
	}
	// Verify old literal hook and normalized snapshot directly. Installing the
	// new official engine must not require pretending the old engine persists.
	commands, err := runtime.merchantStorePortableStartupCommands(loaded, old.Binary, c.Writer.StartupPredecessorPath, c.Writer.StartupPredecessorSHA256)
	if err != nil {
		return err
	}
	values, err := parseExistingSchemaLoadedEnvironment(loaded["Environment"])
	if err != nil {
		return err
	}
	values, startup, unit, err := runtime.existingSchemaSealedStartupCommands(ctx, loaded, values, commands)
	if err != nil || startup != old.Writer.StartupSHA256 || unit != old.Writer.SignedUnitSHA256 {
		return errors.New("ordinary bind actual predecessor startup snapshot changed")
	}
	projected, unit, err := runtime.merchantStoreStartupTransitionSnapshot(ctx, loaded, commands, deploymentInstalledCommand(c.Binary, c.Candidate), c.Writer.StartupTargetPath)
	if err != nil || projected != c.Writer.StartupSHA256 || unit != c.Writer.SignedUnitSHA256 {
		return errors.New("ordinary bind prospective startup differs from its exact target seal")
	}
	child, err := runtime.merchantStoreWriterEnvironmentFromValues(ctx, c.Writer, values)
	if err != nil {
		return err
	}
	for _, source := range c.SourceFiles {
		raw, err := runtime.readExistingSchemaSealedFile(filepath.Join(c.Root, "source", source.Path), true)
		if err != nil || startupContentSHA256(raw) != source.SHA256 {
			return errors.New("ordinary bind official wrapper source changed")
		}
	}
	var engine string
	for index, target := range []productionMerchantStoreWriterTarget{c.Writer.Candidate, c.Writer.Rollback} {
		provider, err := runtime.verifyMerchantStoreCapsuleArtifact(ctx, c, index == 1)
		if err != nil {
			return err
		}
		out, callErr := runVerifiedBinary(ctx, runtime.runner, provider, []string{"merchant-store-writer-gate", "status"}, child, filepath.Dir(provider), 35*time.Second, true)
		if err := qualifyMerchantStoreWriterStatus(out, callErr, c.Writer, target); err != nil {
			return err
		}
		role := "candidate"
		if index == 1 {
			role = "rollback"
		}
		if err := runtime.verifyMerchantStoreWriterProvider(ctx, merchantStoreCapsuleWorkspace(c), provider, child, role); err != nil {
			return err
		}
		if index == 0 {
			engine = filepath.Join(filepath.Dir(provider), deployEngineName)
		}
	}
	if err := runtime.verifyMerchantStoreCapsuleSchema(ctx, c, child); err != nil {
		return err
	}
	if !productionSHA256Pattern.MatchString(c.Candidate.DeployEngineSHA256) || runtime.merchantStoreCapsuleFile(engine, 0700) != nil || sha256MustEqual(engine, c.Candidate.DeployEngineSHA256) != nil || sha256MustEqual("/proc/self/exe", c.Candidate.DeployEngineSHA256) != nil {
		return errors.New("ordinary bind is not the new signed artifact deployment engine")
	}
	live, err := runtime.requestMerchantStoreOrdinaryParentValues(ctx, c, digest, values)
	if err != nil || !live {
		return errors.New("ordinary bind requires its actual live ordinary parent")
	}
	drop := filepath.Join(runtime.paths.DropInDir, "90-merchant-startup-baseline.conf")
	oldHook, err := runtime.readExistingSchemaSealedFile(drop, false)
	if err != nil {
		return err
	}
	oldEngine, err := sha256File(deployEngineInstalledPath)
	if err != nil || runtime.requireOwnedSafePath(deployEngineInstalledPath, false) != nil || oldEngine != old.Candidate.DeployEngineSHA256 && oldEngine != c.Candidate.DeployEngineSHA256 {
		return errors.New("ordinary bind installed engine is outside the exact old/new signed pair")
	}
	hook := merchantStorePortableHookBytes(deploymentInstalledCommand(c.Binary, c.Candidate), c.Writer.StartupTargetPath, digest)
	intent := merchantStoreOrdinaryBindIntent{"lmm-ordinary-startup-bind-intent-v1", productionReleaseFilePlan{Path: c.Writer.StartupTargetPath, SHA256: digest}, productionReleaseFilePlan{Path: c.Writer.StartupPredecessorPath, SHA256: c.Writer.StartupPredecessorSHA256}, stopped, startupContentSHA256(oldHook), startupContentSHA256(hook), oldEngine, c.Candidate.DeployEngineSHA256, runtime.now().UTC()}
	intentRaw, _ := json.MarshalIndent(intent, "", "  ")
	intentRaw = append(intentRaw, '\n')
	if err := runtime.writeSchemaMaintenanceRawNewFile(intentPath, intentRaw); err != nil {
		return errors.New("ordinary bind intent is uncertain; inspect without replay")
	}
	check := func() error {
		sealed, err := runtime.readExistingSchemaSealedFile(intentPath, true)
		if err != nil || !bytes.Equal(sealed, intentRaw) {
			return errors.New("ordinary bind sealed intent changed")
		}
		fresh, err := runtime.ordinaryStoppedWriter(ctx, c, old)
		if err != nil || fresh != stopped {
			return errors.New("ordinary bind original stopped writer changed")
		}
		freshLoaded, err := runtime.loadedExistingSchemaUnit(ctx)
		if err != nil || !merchantStoreStartupLoadedEqual(loaded, freshLoaded) {
			return errors.New("ordinary bind loaded predecessor changed before mutation")
		}
		currentHook, err := runtime.readExistingSchemaSealedFile(drop, false)
		if err != nil || !bytes.Equal(currentHook, oldHook) {
			return errors.New("ordinary bind old hook CAS differs")
		}
		live, err := runtime.requestMerchantStoreOrdinaryParentValues(ctx, c, digest, values)
		if err != nil || !live {
			return errors.New("ordinary bind live parent changed before mutation")
		}
		return nil
	}
	if err := check(); err != nil {
		return err
	}
	if sha256MustEqual(deployEngineInstalledPath, oldEngine) != nil || sha256MustEqual(engine, c.Candidate.DeployEngineSHA256) != nil {
		return errors.New("ordinary bind old/new engine CAS differs")
	}
	if err := atomicInstallRegularFile(engine, deployEngineInstalledPath, 0755); err != nil {
		return fmt.Errorf("ordinary bind engine installation uncertain; preserve intent: %w", err)
	}
	if err := check(); err != nil {
		return err
	}
	if err := writeAtomicRegularFile(drop, hook, 0644); err != nil {
		return fmt.Errorf("ordinary bind hook installation uncertain; preserve intent: %w", err)
	}
	if _, err := runtime.runner.Run(ctx, productionCommand{Name: commandSystemctl, Args: []string{"daemon-reload"}, Timeout: 15 * time.Second}); err != nil {
		return errors.New("ordinary bind hook reload uncertain; preserve intent")
	}
	final, err := runtime.loadedExistingSchemaUnit(ctx)
	if err != nil || final["MainPID"] != "0" || final["ActiveState"] != "inactive" || final["InvocationID"] != stopped.InvocationID {
		return errors.New("ordinary bind original stopped generation changed after reload")
	}
	if _, err := runtime.merchantStoreCapsuleStartup(ctx, c, digest, final); err != nil {
		return err
	}
	if err := runtime.qualifyMerchantStoreCapsule(ctx, c, digest, "", true); err != nil {
		return err
	}
	live, err = runtime.requestMerchantStoreOrdinaryParentValues(ctx, c, digest, values)
	if err != nil || !live {
		return errors.New("ordinary bind parent changed after reload")
	}
	sealed, err := runtime.readExistingSchemaSealedFile(intentPath, true)
	if err != nil || !bytes.Equal(sealed, intentRaw) {
		return errors.New("ordinary bind intent changed after reload")
	}
	return runtime.writeSchemaMaintenanceRawNewFile(filepath.Join(c.Root, "state", "ordinary-startup-bound.json"), intentRaw)
}
