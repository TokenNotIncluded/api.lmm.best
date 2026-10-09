package appcli

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const merchantStoreFenceProtocol = "lmm-merchant-deployment-fence-v1"

type merchantStoreFenceRequest struct {
	Protocol       string `json:"protocol"`
	Operation      string `json:"operation"`
	PlanSHA256     string `json:"plan_sha256"`
	ContractSHA256 string `json:"contract_sha256"`
	Nonce          string `json:"nonce"`
}

type merchantStoreFenceReply struct {
	Protocol string `json:"protocol"`
	Owner    string `json:"owner"`
	Held     bool   `json:"held"`
	Released bool   `json:"released"`
}

func merchantStoreFenceContractSHA(contract *productionMerchantStoreWriterContract) string {
	body, _ := json.Marshal(contract)
	return startupContentSHA256(body)
}

func merchantStoreFenceOwnerPath(workspace productionWorkspace) string {
	return filepath.Join(workspace.stateDir, "merchant-fence-owner.json")
}

func (runtime *productionRuntime) readMerchantStoreFenceOwner(workspace productionWorkspace, manifest productionManifest) (productionMerchantStoreFenceOwner, string, error) {
	path := merchantStoreFenceOwnerPath(workspace)
	body, err := runtime.readExistingSchemaSealedFile(path, true)
	if err != nil {
		return productionMerchantStoreFenceOwner{}, "", errors.New("merchant deployment durable owner receipt is missing or unsafe")
	}
	owner, err := parseMerchantStoreFenceOwner(body)
	if err != nil || owner.DeploymentID != workspace.id || owner.PlanSHA256 != manifest.SchemaPlanSHA256 ||
		owner.ContractSHA256 != merchantStoreFenceContractSHA(manifest.MerchantStoreWriter) || owner.ProviderSHA256 != manifest.MerchantStoreWriter.Candidate.PayloadSHA256 ||
		owner.SystemIdentifier != manifest.MerchantStoreWriter.SystemIdentifier || owner.Database != manifest.MerchantStoreWriter.Database ||
		owner.DatabaseOID != manifest.MerchantStoreWriter.DatabaseOID || owner.Schema != manifest.MerchantStoreWriter.Schema || owner.SchemaOID != manifest.MerchantStoreWriter.SchemaOID || owner.Role != manifest.MerchantStoreWriter.Role {
		return productionMerchantStoreFenceOwner{}, "", errors.New("merchant deployment durable owner differs from the immutable plan, provider or database")
	}
	host, err := runtime.hostname()
	if err != nil || host != owner.Host {
		return productionMerchantStoreFenceOwner{}, "", errors.New("merchant deployment owner is bound to another host")
	}
	return owner, string(body), nil
}

// The receipt only locates the owner. Each request proves its current systemd
// invocation, actual executable and Unix peer, and the holder itself proves
// the physical PG session/shared lock and exact ACTIVE row before replying.
func (runtime *productionRuntime) requestMerchantStoreFence(ctx context.Context, workspace productionWorkspace, manifest productionManifest, release bool) error {
	if runtime.merchantStoreAuthority != nil {
		return runtime.merchantStoreAuthority.Request(ctx, workspace, manifest, release)
	}
	if !manifest.Go.Changed && manifest.MerchantStoreWriter == nil {
		return nil
	}
	if err := validateMerchantStoreWriterContract(manifest.MerchantStoreWriter); err != nil {
		return err
	}
	owner, value, err := runtime.readMerchantStoreFenceOwner(workspace, manifest)
	if err != nil {
		return err
	}
	unit, err := runtime.billingUnitState(ctx, owner.HolderUnit)
	if err != nil || unit["ActiveState"] != "active" || unit["SubState"] != "running" || unit["Restart"] != "no" ||
		unit["MainPID"] != strconv.Itoa(owner.HolderPID) || unit["ExecMainPID"] != strconv.Itoa(owner.HolderPID) || unit["InvocationID"] != owner.HolderInvocationID {
		return errors.New("merchant deployment holder actual systemd generation was lost")
	}
	hasher := runtime.billingExecutableSHA256
	if hasher == nil {
		hasher = func(pid int) (string, error) { return sha256File(filepath.Join("/proc", strconv.Itoa(pid), "exe")) }
	}
	if digest, err := hasher(owner.HolderPID); err != nil || digest != owner.ProviderSHA256 {
		return errors.New("merchant deployment holder executable differs from the qualified signed provider")
	}
	socket, err := runtime.merchantStoreSocketPath(workspace.root, owner, false)
	if err != nil {
		return err
	}
	info, err := os.Lstat(socket)
	if err != nil || info == nil {
		return errors.New("merchant deployment holder socket is unavailable")
	}
	uid, _, valid := deploymentFileOwnership(info)
	if info.Mode()&os.ModeSocket == 0 || info.Mode().Perm() != 0600 || !valid || uid != runtime.requiredOwnerUID {
		return errors.New("merchant deployment holder socket is unsafe")
	}
	connection, err := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "unix", socket)
	if err != nil {
		return errors.New("merchant deployment holder cannot be reached; retained ACTIVE owner requires review")
	}
	defer connection.Close()
	if err := merchantStoreFencePeer(connection, runtime.requiredOwnerUID, owner.HolderPID); err != nil {
		return err
	}
	deadline := time.Now().Add(90 * time.Second)
	if limit, ok := ctx.Deadline(); ok && limit.Before(deadline) {
		deadline = limit
	}
	_ = connection.SetDeadline(deadline)
	stopCancellation := context.AfterFunc(ctx, func() { _ = connection.Close() })
	defer stopCancellation()
	operation := "check"
	if release {
		operation = "release"
	}
	request := merchantStoreFenceRequest{Protocol: merchantStoreFenceProtocol, Operation: operation, PlanSHA256: owner.PlanSHA256, ContractSHA256: owner.ContractSHA256, Nonce: owner.Nonce}
	if err := json.NewEncoder(connection).Encode(request); err != nil {
		return errors.New("merchant deployment holder request failed")
	}
	var reply merchantStoreFenceReply
	decoder := json.NewDecoder(io.LimitReader(connection, 20000))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&reply); err != nil || decoder.Decode(&struct{}{}) != io.EOF || reply.Protocol != merchantStoreFenceProtocol || reply.Owner != value ||
		(!release && (!reply.Held || reply.Released)) || (release && (reply.Held || !reply.Released)) {
		return errors.New("merchant deployment holder live session/owner proof failed")
	}
	return nil
}

func merchantStoreFenceRequestValid(request merchantStoreFenceRequest, owner productionMerchantStoreFenceOwner) bool {
	return request.Protocol == merchantStoreFenceProtocol && (request.Operation == "check" || request.Operation == "release") &&
		request.PlanSHA256 == owner.PlanSHA256 && request.ContractSHA256 == owner.ContractSHA256 && request.Nonce == owner.Nonce
}

func readMerchantStoreFenceRequest(connection net.Conn, owner productionMerchantStoreFenceOwner) (merchantStoreFenceRequest, error) {
	var request merchantStoreFenceRequest
	reader := io.LimitReader(connection, 20000)
	// One canonical JSON line prevents duplicate/escaped duplicate fields and
	// trailing requests; the client does not need to half-close its stream.
	var body []byte
	buffer := make([]byte, 1)
	for len(body) < 16384 {
		if _, err := io.ReadFull(reader, buffer); err != nil {
			return request, errors.New("merchant deployment holder request is incomplete")
		}
		if buffer[0] == '\n' {
			break
		}
		body = append(body, buffer[0])
	}
	if len(body) == 0 || len(body) >= 16384 || json.Unmarshal(body, &request) != nil || !merchantStoreFenceRequestValid(request, owner) {
		return request, errors.New("merchant deployment holder request differs from its sealed owner")
	}
	canonical, _ := json.Marshal(request)
	if !bytes.Equal(canonical, body) {
		return request, errors.New("merchant deployment holder request is ambiguous or noncanonical")
	}
	return request, nil
}

func (runtime *productionRuntime) verifyMerchantStoreFenceTerminal(ctx context.Context, workspace productionWorkspace, expected productionManifest) error {
	status, err := runtime.readStatus(workspace)
	if err != nil || status.Phase != "CONFIRMED" && status.Phase != "ROLLED_BACK" {
		return errors.New("merchant deployment fence release requires actual confirmed or completed rollback state")
	}
	var manifest productionManifest
	if status.Phase == "ROLLED_BACK" {
		manifest, err = runtime.readManifestForRollback(workspace)
	} else {
		manifest, err = runtime.readManifest(workspace)
	}
	if err != nil || manifest.SchemaPlanSHA256 != expected.SchemaPlanSHA256 || manifest.MerchantStoreWriter == nil || *manifest.MerchantStoreWriter != *expected.MerchantStoreWriter {
		return errors.New("merchant deployment holder terminal manifest changed")
	}
	// AdmissionClosed records the completed drain; reopening preserves it.
	rollback := status.Phase == "ROLLED_BACK"
	if manifest.BillingGate == nil || !manifest.BillingGate.AdmissionReopened {
		return errors.New("merchant deployment fence release requires reopened admission evidence")
	}
	incompleteAdmission := !manifest.BillingGate.AdmissionClosed
	if incompleteAdmission && (!rollback || status.Reason != productionUnchangedAdmissionRecoveryReason) {
		return errors.New("merchant deployment fence release requires reopened admission evidence")
	}
	if !rollback && manifest.NginxEdgeRestoreSHA256 != "" && !manifest.PreserveEdgePolicy {
		// Promotion can install its qualified edge policy after reopening.
		if err := runtime.verifyEdgePolicy(ctx, runtime.paths.EdgeAssetRoot); err != nil {
			return err
		}
	} else {
		expectedAdmissionSHA256 := manifest.BillingGate.OriginalSHA256
		if rollback && manifest.NginxEdgeRestoreSHA256 != "" {
			// Rollback restores the pre-upgrade snapshot after reopening its gate.
			root := filepath.Join(workspace.configRestore, "nginx-edge")
			if err := runtime.validateEdgePolicyBackup(root, manifest.NginxEdgeRestoreSHA256); err != nil {
				return err
			}
			expectedAdmissionSHA256, err = sha256File(filepath.Join(root, "locations"))
			if err != nil {
				return errors.New("merchant deployment terminal admission restore evidence is unavailable")
			}
		}
		if digest, err := sha256File(filepath.Join(runtime.paths.NginxRoot, "lmm-api-locations.conf")); err != nil || digest != expectedAdmissionSHA256 {
			return errors.New("merchant deployment terminal admission entry changed after reopening")
		}
	}
	if err := runtime.checkMerchantStoreWriterLifecycle(ctx, workspace, manifest, true, rollback); err != nil {
		return err
	}
	if err := runtime.verifyExistingSchemaLifecycle(ctx, manifest); err != nil {
		return err
	}
	expectedPayload := manifest.MerchantStoreWriter.Candidate.PayloadSHA256
	if rollback {
		expectedPayload = manifest.MerchantStoreWriter.Rollback.PayloadSHA256
	}
	unit, err := runtime.billingUnitState(ctx, runtime.paths.Service)
	pid, parseErr := strconv.Atoi(unit["MainPID"])
	if err != nil || parseErr != nil || pid <= 1 || unit["ActiveState"] != "active" || !existingSchemaInvocationPattern.MatchString(unit["InvocationID"]) {
		return errors.New("merchant deployment terminal writer generation is unavailable")
	}
	if incompleteAdmission {
		// Only the native pre-stop recovery path writes this reason. Recheck its
		// live unchanged-writer proof, rather than trusting the terminal label.
		if err := runtime.verifyUnchangedAdmissionWriter(ctx, manifest, unit); err != nil {
			return err
		}
	}
	if digest, err := sha256File(filepath.Join("/proc", strconv.Itoa(pid), "exe")); err != nil || digest != expectedPayload {
		return errors.New("merchant deployment terminal running writer is not its qualified target")
	}
	return nil
}

// Hold is a separate non-restarting systemd unit, not a borrowed transaction
// process or a detached goroutine that disappears when promote returns.
func (runtime *productionRuntime) holdMerchantStoreFence(ctx context.Context, workspace productionWorkspace, planPath, planSHA string) error {
	manifest, err := runtime.merchantStorePreviewManifest(workspace, planPath, planSHA)
	if err != nil {
		return err
	}
	for _, rollback := range []bool{false, true} {
		if err := runtime.checkMerchantStoreWriterTarget(ctx, workspace, manifest, rollback); err != nil {
			return err
		}
	}
	invocation := os.Getenv("INVOCATION_ID")
	if !existingSchemaInvocationPattern.MatchString(invocation) {
		return errors.New("merchant deployment holder requires a real systemd invocation")
	}
	digest, err := sha256File(filepath.Join("/proc", "self", "exe"))
	if err != nil || digest != manifest.MerchantStoreWriter.Candidate.PayloadSHA256 {
		return errors.New("merchant deployment holder is not the signed candidate payload")
	}
	host, err := runtime.hostname()
	if err != nil || !merchantStoreFenceHostPattern.MatchString(host) {
		return errors.New("merchant deployment holder hostname is invalid")
	}
	child, err := runtime.merchantStoreWriterEnvironment(ctx, manifest)
	if err != nil {
		return err
	}
	values := map[string]string{}
	for _, entry := range child {
		key, value, _ := strings.Cut(entry, "=")
		values[key] = value
	}
	lease, err := acquireMerchantStoreDeploymentFence(ctx, values, manifest.MerchantStoreWriter)
	if err != nil {
		return err
	}
	defer lease.Close()
	// Qualification before the shared lock is diagnostic only. Repeat both
	// actual status and migrate --verify under this independent fence; never
	// hold the migration advisory key while either provider acquires it.
	for _, rollback := range []bool{false, true} {
		if err := runtime.checkMerchantStoreWriterTarget(ctx, workspace, manifest, rollback); err != nil {
			return err
		}
		role := "candidate"
		if rollback {
			role = "rollback"
		}
		provider := filepath.Join(workspace.root, "tmp", "migrations", "merchant-store-"+role, productionCandidateLinkName)
		child, err := runtime.merchantStoreWriterEnvironment(ctx, manifest)
		if err != nil {
			return err
		}
		if err := runtime.verifyMerchantStoreWriterProvider(ctx, workspace, provider, child, role); err != nil {
			return errors.New("merchant deployment holder actual candidate/N-1 migrate verification failed")
		}
	}
	if err := runtime.verifyExistingSchemaContract(ctx, manifest.ExistingSchemaContract); err != nil {
		return err
	}
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return errors.New("merchant deployment holder nonce is unavailable")
	}
	owner := productionMerchantStoreFenceOwner{Format: 1, State: "ACTIVE", DeploymentID: workspace.id, Host: host,
		PlanSHA256: planSHA, ContractSHA256: merchantStoreFenceContractSHA(manifest.MerchantStoreWriter), ProviderSHA256: digest,
		Nonce: hex.EncodeToString(nonce), HolderPID: os.Getpid(), HolderUnit: merchantStoreFenceUnit(workspace.id), HolderInvocationID: invocation, BackendPID: lease.backendPID,
		SystemIdentifier: manifest.MerchantStoreWriter.SystemIdentifier, Database: manifest.MerchantStoreWriter.Database, DatabaseOID: manifest.MerchantStoreWriter.DatabaseOID,
		Schema: manifest.MerchantStoreWriter.Schema, SchemaOID: manifest.MerchantStoreWriter.SchemaOID, Role: manifest.MerchantStoreWriter.Role}
	unit, err := runtime.billingUnitState(ctx, owner.HolderUnit)
	if err != nil || unit["MainPID"] != strconv.Itoa(owner.HolderPID) || unit["ExecMainPID"] != strconv.Itoa(owner.HolderPID) || unit["InvocationID"] != invocation || unit["Restart"] != "no" || unit["ActiveState"] != "active" {
		return errors.New("merchant deployment holder does not own the actual non-restarting systemd invocation")
	}
	ownerPath := merchantStoreFenceOwnerPath(workspace)
	socketPath, err := runtime.merchantStoreSocketPath(workspace.root, owner, true)
	if err != nil {
		return err
	}
	for _, path := range []string{ownerPath, socketPath} {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			return errors.New("merchant deployment holder state already exists; explicit recovery review is required")
		}
	}
	if err := lease.ClaimOwner(ctx, owner); err != nil {
		return err
	}
	ownerJSON, _ := canonicalMerchantStoreFenceOwner(owner)
	if err := writeAtomicRegularFile(ownerPath, ownerJSON, 0600); err != nil {
		return errors.New("merchant deployment owner receipt failed; ACTIVE database owner remains blocked")
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socketPath, Net: "unix"})
	if err != nil {
		return fmt.Errorf("merchant deployment holder listener failed; ACTIVE database owner remains blocked: %w", err)
	}
	listener.SetUnlinkOnClose(false)
	defer listener.Close()
	if err := os.Chmod(socketPath, 0600); err != nil {
		return err
	}
	for {
		if err := lease.CheckOwner(ctx); err != nil {
			return err
		}
		_ = listener.SetDeadline(time.Now().Add(2 * time.Second))
		connection, err := listener.AcceptUnix()
		if err != nil {
			if timeout, ok := err.(net.Error); ok && timeout.Timeout() && ctx.Err() == nil {
				continue
			}
			return errors.New("merchant deployment holder stopped; ACTIVE database owner remains blocked")
		}
		released := runtime.handleMerchantStoreFenceConnection(ctx, workspace, manifest, owner, ownerJSON, lease, connection)
		if released {
			_ = listener.Close()
			return os.Remove(socketPath)
		}
	}
}

func (runtime *productionRuntime) verifyMerchantStoreWriterProvider(ctx context.Context, workspace productionWorkspace, provider string, sealedChild []string, role string) error {
	if role != "candidate" && role != "rollback" {
		return errors.New("merchant verification provider role is invalid")
	}
	values := map[string]string{}
	for _, entry := range sealedChild {
		key, value, present := strings.Cut(entry, "=")
		if !present || key == "" {
			return errors.New("merchant verification environment is invalid")
		}
		if _, duplicate := values[key]; duplicate {
			return errors.New("merchant verification environment is duplicated")
		}
		values[key] = value
	}
	options := strings.Fields(values["PGOPTIONS"])
	if verifyExistingSchemaStartupEnvironment(values, true) != nil || len(options) != 4 || options[0] != "-c" ||
		!strings.HasPrefix(options[1], "search_path=") || !isDatabaseSchema(strings.TrimPrefix(options[1], "search_path=")) || options[2] != "-c" || options[3] != "default_transaction_read_only=on" {
		return errors.New("merchant verification cannot execute apply or financial preparation")
	}
	dsn, err := productionDatabaseURL(values)
	if err != nil || verifyExistingSchemaLogDatabase(values, dsn) != nil {
		return errors.New("merchant verification database policy is invalid")
	}
	directory, err := prepareMigrationDir(workspace, "merchant-fence-"+role+"-verify")
	if err != nil {
		return err
	}
	_, err = runVerifiedBinary(ctx, runtime.runner, provider, []string{"migrate", "--verify"}, sealedChild, directory, 5*time.Minute, true)
	if err != nil {
		return fmt.Errorf("merchant writer %s migrate --verify failed: %w", role, err)
	}
	return nil
}

func (runtime *productionRuntime) handleMerchantStoreFenceConnection(ctx context.Context, workspace productionWorkspace, manifest productionManifest, owner productionMerchantStoreFenceOwner, ownerJSON []byte, lease *productionMerchantStoreFence, connection net.Conn) bool {
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(90 * time.Second))
	if merchantStoreFencePeer(connection, runtime.requiredOwnerUID, 0) != nil {
		return false
	}
	request, err := readMerchantStoreFenceRequest(connection, owner)
	if err != nil || lease.CheckOwner(ctx) != nil {
		return false
	}
	if request.Operation == "check" {
		_ = json.NewEncoder(connection).Encode(merchantStoreFenceReply{Protocol: merchantStoreFenceProtocol, Owner: string(ownerJSON), Held: true})
		return false
	}
	if runtime.verifyMerchantStoreFenceTerminal(ctx, workspace, manifest) != nil || lease.ReleaseOwner(ctx) != nil {
		return false
	}
	_ = json.NewEncoder(connection).Encode(merchantStoreFenceReply{Protocol: merchantStoreFenceProtocol, Owner: string(ownerJSON), Released: true})
	return true
}

func (runtime *productionRuntime) ensureMerchantStoreFence(ctx context.Context, workspace productionWorkspace, manifest productionManifest) error {
	if runtime.merchantStoreAuthority != nil {
		return runtime.merchantStoreAuthority.Ensure(ctx, workspace, manifest)
	}
	if !manifest.Go.Changed && manifest.MerchantStoreWriter == nil {
		return nil
	}
	if err := validateMerchantStoreWriterContract(manifest.MerchantStoreWriter); err != nil || !productionSHA256Pattern.MatchString(manifest.SchemaPlanSHA256) {
		return errors.New("merchant deployment cannot create a durable holder without its immutable same-schema plan")
	}
	ownerPath := merchantStoreFenceOwnerPath(workspace)
	if _, err := os.Lstat(ownerPath); err == nil {
		return runtime.requestMerchantStoreFence(ctx, workspace, manifest, false)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	provider := filepath.Join(workspace.root, "tmp", "migrations", "merchant-store-candidate", productionCandidateLinkName)
	if target, err := filepath.EvalSymlinks(provider); err != nil || sha256MustEqual(target, manifest.MerchantStoreWriter.Candidate.PayloadSHA256) != nil {
		return errors.New("merchant deployment holder candidate was not actually qualified")
	}
	plan := filepath.Join(workspace.stagingDir, productionReleasePlanFilename)
	if _, err := runtime.runner.Run(ctx, productionCommand{Name: "systemd-run", Args: []string{"--quiet", "--collect", "--unit", merchantStoreFenceUnit(workspace.id),
		"--property=Type=simple", "--property=User=root", "--property=Restart=no", "--property=KillMode=control-group", "--", provider, "operator", "production", "writer-fence", "hold",
		"--workspace", workspace.root, "--release-plan", plan, "--release-plan-sha256", manifest.SchemaPlanSHA256}, Timeout: 30 * time.Second}); err != nil {
		return errors.New("merchant deployment durable holder unit could not be started")
	}
	return runtime.awaitMerchantStoreFence(ctx, func(ctx context.Context) error {
		return runtime.requestMerchantStoreFence(ctx, workspace, manifest, false)
	})
}

// Holder qualification reads both signed providers and the physical database.
// A slow host must retain its original holder rather than launch a replacement.
func (runtime *productionRuntime) awaitMerchantStoreFence(ctx context.Context, check func(context.Context) error) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	deadline := runtime.now().Add(2 * time.Minute)
	for runtime.now().Before(deadline) {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := check(ctx); err == nil {
			if err := ctx.Err(); err != nil {
				return err
			}
			if !runtime.now().Before(deadline) {
				break
			}
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		runtime.sleep(500 * time.Millisecond)
	}
	return errors.New("merchant deployment durable holder did not establish actual session ownership; no writer/package mutation is authorized")
}

func sha256MustEqual(path, expected string) error {
	if digest, err := sha256File(path); err != nil || digest != expected {
		return errors.New("payload digest mismatch")
	}
	return nil
}

func runProductionMerchantStoreFence(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || (args[0] != "hold" && args[0] != "check" && args[0] != "release") {
		return ExitUsage
	}
	flags := flag.NewFlagSet("production writer-fence "+args[0], flag.ContinueOnError)
	flags.SetOutput(stderr)
	workspacePath := flags.String("workspace", "", "manifest-owned root-private workspace")
	planPath := flags.String("release-plan", "", "exact staged immutable release plan for holder startup")
	planSHA := flags.String("release-plan-sha256", "", "exact staged immutable release plan SHA-256")
	if flags.Parse(args[1:]) != nil || flags.NArg() != 0 || *workspacePath == "" || args[0] == "hold" && (*planPath == "" || *planSHA == "") ||
		args[0] != "hold" && (*planPath != "" || *planSHA != "") {
		return ExitUsage
	}
	runtime := defaultProductionRuntime()
	if runtime.effectiveUID() != 0 {
		return ExitError
	}
	workspace, err := runtime.openWorkspace(*workspacePath)
	if err == nil {
		if args[0] == "hold" {
			err = runtime.holdMerchantStoreFence(context.Background(), workspace, *planPath, *planSHA)
		} else {
			var manifest productionManifest
			status, statusErr := runtime.readStatus(workspace)
			if statusErr == nil && (status.Phase == "ROLLED_BACK" || status.Phase == "ROLLING_BACK" || status.Phase == "ROLLBACK_REQUIRED") {
				manifest, err = runtime.readManifestForRollback(workspace)
			} else {
				manifest, err = runtime.readManifest(workspace)
			}
			if err == nil {
				err = runtime.requestMerchantStoreFence(context.Background(), workspace, manifest, args[0] == "release")
			}
		}
	}
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s production writer-fence: %v\n", DeployProgramName, err)
		return ExitError
	}
	_, _ = fmt.Fprintln(stdout, "merchant_store_fence=verified")
	return ExitOK
}
