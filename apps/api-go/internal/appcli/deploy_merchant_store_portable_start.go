package appcli

import (
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
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/internal/deploymentfence"
)

func merchantStorePortableOwnerPath(c productionMerchantStoreCapsule, invocation string) string {
	directory := filepath.Join(c.Root, "state")
	if invocation != "" {
		directory = filepath.Join(directory, "start-"+invocation)
	}
	return filepath.Join(directory, "portable-owner.json")
}
func (runtime *productionRuntime) portableOwner(ctx context.Context, c productionMerchantStoreCapsule, digest, invocation string) (productionMerchantStoreFenceOwner, []byte, error) {
	path := merchantStorePortableOwnerPath(c, invocation)
	body, err := runtime.readExistingSchemaSealedFile(path, true)
	if err != nil {
		return productionMerchantStoreFenceOwner{}, nil, merchantStorePortableProofError{stage: "receipt-missing-or-unsafe"}
	}
	owner, err := parseMerchantStoreFenceOwner(body)
	purpose, unit := "portable-deploy", merchantStorePortableUnit(c.DeploymentID)
	if invocation != "" {
		purpose, unit = "start", merchantStoreStartUnit(c.DeploymentID, invocation)
	}
	if err != nil || owner.Purpose != purpose || owner.Service != c.Service || owner.StartInvocationID != invocation || owner.DeploymentID != c.DeploymentID || owner.Host != c.Host ||
		owner.PlanSHA256 != digest || owner.ContractSHA256 != merchantStoreFenceContractSHA(c.Writer) || owner.ProviderSHA256 != c.Writer.Candidate.PayloadSHA256 || owner.HolderUnit != unit ||
		owner.SystemIdentifier != c.Writer.SystemIdentifier || owner.Database != c.Writer.Database || owner.DatabaseOID != c.Writer.DatabaseOID || owner.Schema != c.Writer.Schema || owner.SchemaOID != c.Writer.SchemaOID || owner.Role != c.Writer.Role {
		return owner, nil, merchantStorePortableProofError{stage: "immutable-owner-binding"}
	}
	actual, err := runtime.billingUnitState(ctx, owner.HolderUnit)
	if err != nil || actual["ActiveState"] != "active" || actual["SubState"] != "running" || actual["Restart"] != "no" || actual["MainPID"] != strconv.Itoa(owner.HolderPID) || actual["ExecMainPID"] != strconv.Itoa(owner.HolderPID) || actual["InvocationID"] != owner.HolderInvocationID || sha256MustEqual(filepath.Join("/proc", strconv.Itoa(owner.HolderPID), "exe"), owner.ProviderSHA256) != nil {
		return owner, nil, merchantStorePortableProofError{stage: "holder-generation"}
	}
	return owner, body, nil
}
func (runtime *productionRuntime) requestMerchantStorePortableFence(ctx context.Context, c productionMerchantStoreCapsule, digest, invocation string, release bool) error {
	owner, body, err := runtime.portableOwner(ctx, c, digest, invocation)
	if err != nil {
		return err
	}
	socket, err := runtime.merchantStoreSocketPath(c.Root, owner, false)
	if err != nil {
		return err
	}
	info, err := os.Lstat(socket)
	if err != nil {
		return merchantStorePortableProofError{stage: "socket-missing"}
	}
	uid, _, ok := deploymentFileOwnership(info)
	if !ok || uid != runtime.requiredOwnerUID || info.Mode()&os.ModeSocket == 0 || info.Mode().Perm() != 0600 {
		return merchantStorePortableProofError{stage: "socket-ownership"}
	}
	conn, err := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "unix", socket)
	if err != nil {
		return merchantStorePortableProofError{stage: "socket-connect"}
	}
	defer conn.Close()
	if err := merchantStoreFencePeer(conn, runtime.requiredOwnerUID, owner.HolderPID); err != nil {
		return merchantStorePortableProofError{stage: "physical-peer"}
	}
	stopCancel := merchantStoreBoundConnection(ctx, conn)
	defer stopCancel()
	operation := "check"
	if release {
		operation = "release"
	}
	request := merchantStoreFenceRequest{Protocol: merchantStoreFenceProtocol, Operation: operation, PlanSHA256: digest, ContractSHA256: owner.ContractSHA256, Nonce: owner.Nonce}
	if json.NewEncoder(conn).Encode(request) != nil {
		return merchantStorePortableProofError{stage: "request-write"}
	}
	var reply merchantStoreFenceReply
	decoder := json.NewDecoder(io.LimitReader(conn, 20000))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&reply) != nil || decoder.Decode(&struct{}{}) != io.EOF || reply.Protocol != merchantStoreFenceProtocol || reply.Owner != string(body) ||
		!release && (!reply.Held || reply.Released) || release && (reply.Held || !reply.Released) {
		return merchantStorePortableProofError{stage: "physical-session-exact-owner"}
	}
	return nil
}
func (lease *productionMerchantStoreFence) requireNoDurableOwner(ctx context.Context) error {
	if err := lease.Check(ctx, &lease.identity); err != nil {
		return err
	}
	lease.mu.Lock()
	defer lease.mu.Unlock()
	var exists bool
	if lease.connection.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM "`+lease.identity.Schema+`".options WHERE `+deploymentfence.PostgreSQLPresencePredicate+`)`).Scan(&exists) != nil || exists {
		return errors.New("reserved ACTIVE or unknown deployment owner remains; automatic startup cannot clear it")
	}
	return nil
}
func (runtime *productionRuntime) portableStartControlPID(ctx context.Context, c productionMerchantStoreCapsule, invocation string) error {
	loaded, err := runtime.loadedExistingSchemaUnit(ctx)
	if err != nil || loaded["InvocationID"] != invocation || loaded["ActiveState"] != "activating" || loaded["MainPID"] != "0" {
		return errors.New("per-start entry is not the actual activating service invocation")
	}
	output, err := runtime.runner.Run(ctx, productionCommand{Name: commandSystemctl, Args: []string{"show", c.Service, "--all", "--property=ControlPID", "--value"}, Timeout: 15 * time.Second})
	if err != nil || strings.TrimSpace(string(output)) != strconv.Itoa(os.Getpid()) {
		return errors.New("per-start entry is not the actual systemd ExecStartPre control process")
	}
	return nil
}
func (runtime *productionRuntime) verifyPortableRunningWriter(ctx context.Context, c productionMerchantStoreCapsule, digest, invocation string, rollback bool) error {
	if c.Format == 2 && rollback {
		return errors.New("startup baseline cannot authorize a rollback provider")
	}
	target := c.Writer.Candidate
	if rollback {
		target = c.Writer.Rollback
	}
	state, err := runtime.billingUnitState(ctx, c.Service)
	pid, parseErr := strconv.Atoi(state["MainPID"])
	if err != nil || parseErr != nil || pid <= 1 || state["ExecMainPID"] != state["MainPID"] || state["ActiveState"] != "active" || state["SubState"] != "running" || !existingSchemaInvocationPattern.MatchString(state["InvocationID"]) || invocation != "" && state["InvocationID"] != invocation {
		return errors.New("portable actual running service generation is unavailable")
	}
	if runtime.verifyMerchantStorePortableInstalled(c, target.PayloadSHA256) != nil || sha256MustEqual(filepath.Join("/proc", strconv.Itoa(pid), "exe"), target.PayloadSHA256) != nil {
		return errors.New("portable installed/running writer differs from the explicit qualified target")
	}
	child, err := runtime.merchantStoreCapsuleEnvironment(ctx, c, digest, state["InvocationID"])
	if err != nil {
		return err
	}
	output, callErr := runVerifiedBinary(ctx, runtime.runner, c.Binary, []string{"merchant-store-writer-gate", "status"}, child, c.Root, 35*time.Second, true)
	if err := qualifyMerchantStoreWriterStatus(output, callErr, c.Writer, target); err != nil {
		return err
	}
	// No proxy/environment/config-file/body support: this is the existing fixed
	// loopback GET. The generation is checked again after readiness and schema.
	if _, err := runtime.runner.Run(ctx, productionCommand{Name: "/usr/bin/curl", Args: strings.Fields(strings.TrimPrefix(existingSchemaReadinessCurl, "/usr/bin/curl ")), Env: []string{"PATH=/usr/bin:/bin", "LANG=C", "LC_ALL=C"}, Timeout: 50 * time.Second}); err != nil {
		return errors.New("portable new writer loopback readiness failed")
	}
	if err := runtime.verifyMerchantStoreCapsuleSchema(ctx, c, child); err != nil {
		return err
	}
	after, err := runtime.billingUnitState(ctx, c.Service)
	if err != nil || after["MainPID"] != state["MainPID"] || after["ExecMainPID"] != state["ExecMainPID"] || after["InvocationID"] != state["InvocationID"] || after["ActiveState"] != "active" || after["SubState"] != "running" || sha256MustEqual(filepath.Join("/proc", strconv.Itoa(pid), "exe"), target.PayloadSHA256) != nil {
		return errors.New("portable running writer changed during terminal proof")
	}
	return nil
}

func (runtime *productionRuntime) verifyMerchantStorePortableInstalled(c productionMerchantStoreCapsule, digest string) error {
	link, err := os.Lstat(c.Binary)
	if err != nil || link.Mode()&os.ModeSymlink == 0 {
		return errors.New("portable installed entry is not the canonical Go provider link")
	}
	owner, _, ok := deploymentFileOwnership(link)
	target, err := os.Readlink(c.Binary)
	if !ok || owner != runtime.requiredOwnerUID || err != nil || target != backendGoName {
		return errors.New("portable installed entry selects an unqualified provider")
	}
	provider := filepath.Join(filepath.Dir(c.Binary), backendGoName)
	if runtime.requireOwnedSafePath(provider, false) != nil || sha256MustEqual(provider, digest) != nil {
		return errors.New("portable installed provider ownership/payload is unsafe")
	}
	resolved, err := filepath.EvalSymlinks(provider)
	if err != nil || resolved != provider {
		return errors.New("portable installed provider ancestor contains a symlink")
	}
	for parent := filepath.Dir(provider); ; parent = filepath.Dir(parent) {
		info, err := os.Lstat(parent)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("portable installed provider ancestor is unsafe")
		}
		owner, _, ok := deploymentFileOwnership(info)
		if !ok || owner != 0 && owner != runtime.requiredOwnerUID || info.Mode().Perm()&0022 != 0 && !(owner == 0 && info.Mode()&os.ModeSticky != 0) {
			return errors.New("portable installed provider can be replaced by another user")
		}
		if parent == filepath.Dir(parent) {
			break
		}
	}
	return nil
}
func (runtime *productionRuntime) portableTerminal(ctx context.Context, c productionMerchantStoreCapsule, digest string) error {
	if c.Format == 2 {
		return errors.New("startup baseline is not ordinary transaction authority")
	}
	// The ordinary standalone wrapper's existing root-owned transaction state
	// is authority for its phase, not a new self-issued controller approval.
	path := filepath.Join(runtime.portableTransactionRoot(), c.DeploymentID, "state.json")
	raw, err := runtime.readExistingSchemaSealedFile(path, true)
	if err != nil {
		return errors.New("portable ordinary transaction state is unavailable")
	}
	var state struct {
		Release     string `json:"release"`
		Phase       string `json:"phase"`
		SHA         string `json:"sha256"`
		PreviousSHA string `json:"previous_sha256"`
		Migrate     bool   `json:"migrate"`
	}
	if !merchantStoreUniqueJSON(raw) || json.Unmarshal(raw, &state) != nil || state.Release != c.DeploymentID || state.Migrate || state.SHA != c.Writer.Candidate.PayloadSHA256 || state.PreviousSHA != c.Writer.Rollback.PayloadSHA256 || state.Phase != "CONFIRMED" && state.Phase != "ROLLED_BACK" {
		return errors.New("portable release requires completed same-schema ordinary owner state")
	}
	if err := runtime.verifyPortableRunningWriter(ctx, c, digest, "", state.Phase == "ROLLED_BACK"); err != nil {
		return err
	}
	after, err := runtime.readExistingSchemaSealedFile(path, true)
	if err != nil || !bytesEqual(raw, after) {
		return errors.New("portable terminal ordinary state changed")
	}
	return nil
}
func bytesEqual(a, b []byte) bool { return string(a) == string(b) }

func merchantStoreUniqueJSON(raw []byte) bool {
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	var walk func(int) bool
	walk = func(depth int) bool {
		if depth > 32 {
			return false
		}
		token, err := decoder.Token()
		if err != nil {
			return false
		}
		delim, ok := token.(json.Delim)
		if !ok {
			return true
		}
		switch delim {
		case '{':
			keys := map[string]bool{}
			for decoder.More() {
				key, err := decoder.Token()
				name, ok := key.(string)
				if err != nil || !ok || keys[name] {
					return false
				}
				keys[name] = true
				if !walk(depth + 1) {
					return false
				}
			}
			end, err := decoder.Token()
			return err == nil && end == json.Delim('}')
		case '[':
			for decoder.More() {
				if !walk(depth + 1) {
					return false
				}
			}
			end, err := decoder.Token()
			return err == nil && end == json.Delim(']')
		default:
			return false
		}
	}
	if len(raw) == 0 || len(raw) > 1<<20 || !walk(0) {
		return false
	}
	_, err := decoder.Token()
	return err == io.EOF
}

func (runtime *productionRuntime) holdMerchantStorePortableFence(ctx context.Context, c productionMerchantStoreCapsule, digest, startInvocation string) error {
	if c.Format == 2 && startInvocation == "" {
		return errors.New("startup baseline requires an actual per-start invocation")
	}
	if startInvocation != "" {
		if err := runtime.merchantStoreStartupGuard(ctx, c, startInvocation, true); err != nil {
			return err
		}
	}
	if err := runtime.qualifyMerchantStoreCapsule(ctx, c, digest, startInvocation, false); err != nil {
		return err
	}
	ownInvocation := os.Getenv("INVOCATION_ID")
	if !existingSchemaInvocationPattern.MatchString(ownInvocation) || sha256MustEqual("/proc/self/exe", c.Writer.Candidate.PayloadSHA256) != nil {
		return errors.New("portable holder is not the actual qualified candidate systemd process")
	}
	child, err := runtime.merchantStoreCapsuleEnvironment(ctx, c, digest, startInvocation)
	if err != nil {
		return err
	}
	values := map[string]string{}
	for _, row := range child {
		key, value, _ := strings.Cut(row, "=")
		values[key] = value
	}
	var lease *productionMerchantStoreFence
	if c.Format == 2 {
		lease, err = acquireMerchantStartupBaselineFence(ctx, values, c.Baseline, c.Host)
	} else {
		lease, err = acquireMerchantStoreDeploymentFence(ctx, values, c.Writer)
	}
	if err != nil {
		return err
	}
	defer lease.Close()
	// New automatic starts refuse all stale/malformed/unknown owners. There is
	// no expiry, PID-based sweeping, overwrite or default installed permission.
	if err := lease.requireNoDurableOwner(ctx); err != nil {
		return err
	}
	if err := runtime.qualifyMerchantStoreCapsule(ctx, c, digest, startInvocation, true); err != nil {
		return err
	}
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return err
	}
	purpose, unit := "portable-deploy", merchantStorePortableUnit(c.DeploymentID)
	if startInvocation != "" {
		purpose, unit = "start", merchantStoreStartUnit(c.DeploymentID, startInvocation)
	}
	owner := productionMerchantStoreFenceOwner{Format: 1, State: "ACTIVE", DeploymentID: c.DeploymentID, Host: c.Host, PlanSHA256: digest, ContractSHA256: merchantStoreFenceContractSHA(c.Writer), ProviderSHA256: c.Writer.Candidate.PayloadSHA256,
		Nonce: hex.EncodeToString(nonce), HolderPID: os.Getpid(), HolderUnit: unit, HolderInvocationID: ownInvocation, BackendPID: lease.backendPID, SystemIdentifier: c.Writer.SystemIdentifier, Database: c.Writer.Database, DatabaseOID: c.Writer.DatabaseOID, Schema: c.Writer.Schema, SchemaOID: c.Writer.SchemaOID, Role: c.Writer.Role,
		Purpose: purpose, Service: c.Service, StartInvocationID: startInvocation}
	actual, err := runtime.billingUnitState(ctx, unit)
	if err != nil || actual["MainPID"] != strconv.Itoa(owner.HolderPID) || actual["ExecMainPID"] != strconv.Itoa(owner.HolderPID) || actual["InvocationID"] != ownInvocation || actual["Restart"] != "no" || actual["ActiveState"] != "active" {
		return errors.New("portable holder does not own the actual non-restarting unit generation")
	}
	ownerPath := merchantStorePortableOwnerPath(c, startInvocation)
	socketPath, err := runtime.merchantStoreSocketPath(c.Root, owner, true)
	if err != nil {
		return err
	}
	directory := filepath.Dir(ownerPath)
	if runtime.merchantStorePrivateDirectory(directory, true) != nil {
		return errors.New("portable holder state directory is unsafe")
	}
	for _, path := range []string{ownerPath, socketPath} {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			return errors.New("portable holder state already exists; retained evidence requires review")
		}
	}
	claim := func(ctx context.Context, guard func(context.Context) error) error {
		return lease.claimOwner(ctx, owner, guard)
	}
	if startInvocation != "" {
		guard := func(ctx context.Context) error {
			return runtime.merchantStoreStartupGuard(ctx, c, startInvocation, true)
		}
		err = merchantStoreGuardedStartupClaim(ctx, guard, claim)
	} else {
		err = lease.ClaimOwner(ctx, owner)
	}
	if err != nil {
		return err
	}
	ownerJSON, _ := canonicalMerchantStoreFenceOwner(owner)
	if err := writeAtomicRegularFile(ownerPath, ownerJSON, 0600); err != nil {
		return errors.New("portable receipt failed; ACTIVE owner remains")
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socketPath, Net: "unix"})
	if err != nil {
		return fmt.Errorf("portable listener failed; ACTIVE owner remains: %w", err)
	}
	listener.SetUnlinkOnClose(false)
	defer listener.Close()
	stopListenerCancel := context.AfterFunc(ctx, func() { _ = listener.Close() })
	defer stopListenerCancel()
	if err := os.Chmod(socketPath, 0600); err != nil {
		return err
	}
	for {
		if startInvocation != "" {
			if err := runtime.merchantStoreStartupGuard(ctx, c, startInvocation, false); err != nil {
				return fmt.Errorf("%w; ACTIVE owner remains permanently fenced", err)
			}
		}
		if err := lease.CheckOwner(ctx); err != nil {
			return err
		}
		if startInvocation != "" {
			state, err := runtime.billingUnitState(ctx, c.Service)
			if err != nil || state["InvocationID"] != startInvocation || state["ActiveState"] != "activating" && state["ActiveState"] != "active" {
				return errors.New("per-start service failed or timed out; ACTIVE owner remains permanently fenced")
			}
			if state["ActiveState"] == "active" && state["SubState"] == "running" {
				rollback := false
				installed, err := sha256File(c.Binary)
				if err != nil {
					return err
				}
				if installed != c.Writer.Candidate.PayloadSHA256 {
					if c.Format == 2 {
						return errors.New("startup baseline installed provider changed")
					}
					if installed != c.Writer.Rollback.PayloadSHA256 {
						return errors.New("per-start installed provider is outside explicit capsule set")
					}
					rollback = true
				}
				if err := runtime.verifyPortableRunningWriter(ctx, c, digest, startInvocation, rollback); err != nil {
					return err
				}
				if err := runtime.merchantStoreStartupGuard(ctx, c, startInvocation, false); err != nil {
					return err
				}
				if err := lease.ReleaseOwner(ctx); err != nil {
					return err
				}
				if err := runtime.recordMerchantStartupSucceeded(ctx, c, digest, owner); err != nil {
					return err
				}
				// Only a completed same-session exact CAS makes these owned transient
				// communication files disposable. Failure evidence is never removed.
				_ = listener.Close()
				if err := os.Remove(socketPath); err != nil {
					return err
				}
				if err := os.Remove(ownerPath); err != nil {
					return err
				}
				return nil
			}
		}
		_ = listener.SetDeadline(time.Now().Add(250 * time.Millisecond))
		conn, err := listener.AcceptUnix()
		if err != nil {
			if timeout, ok := err.(net.Error); ok && timeout.Timeout() && ctx.Err() == nil {
				continue
			}
			return errors.New("portable holder stopped; ACTIVE owner remains")
		}
		released := runtime.handleMerchantStorePortableConnection(ctx, c, digest, startInvocation, owner, ownerJSON, lease, conn)
		if released {
			return nil
		}
	}
}
func (runtime *productionRuntime) handleMerchantStorePortableConnection(ctx context.Context, c productionMerchantStoreCapsule, digest, invocation string, owner productionMerchantStoreFenceOwner, ownerJSON []byte, lease *productionMerchantStoreFence, conn net.Conn) bool {
	defer conn.Close()
	stopCancel := merchantStoreBoundConnection(ctx, conn)
	defer stopCancel()
	if merchantStoreFencePeer(conn, runtime.requiredOwnerUID, 0) != nil {
		return false
	}
	request, err := readMerchantStoreFenceRequest(conn, owner)
	if err != nil || lease.CheckOwner(ctx) != nil {
		return false
	}
	if request.Operation == "check" {
		if invocation != "" && runtime.merchantStoreStartupGuard(ctx, c, invocation, true) != nil {
			return false
		}
		if json.NewEncoder(conn).Encode(merchantStoreFenceReply{Protocol: merchantStoreFenceProtocol, Owner: string(ownerJSON), Held: true}) == nil && invocation != "" {
			budget := ctx.Value(merchantStoreStartupBudgetKey{}).(merchantStoreStartupBudget)
			*budget.handoff = true
		}
		return false
	}
	// A startup holder only self-releases after actual new PID/readiness. No
	// external caller can turn an early startup into a released permission.
	if invocation != "" || runtime.portableTerminal(ctx, c, digest) != nil || lease.ReleaseOwner(ctx) != nil {
		return false
	}
	// A completed exact CAS is the only path which removes this holder's
	// communication files. Keep the original owner value as terminal evidence.
	ownerPath := merchantStorePortableOwnerPath(c, invocation)
	socketPath, err := runtime.merchantStoreSocketPath(c.Root, owner, false)
	if err != nil {
		return true
	}
	if writeAtomicRegularFile(filepath.Join(filepath.Dir(ownerPath), "portable-released-owner.json"), ownerJSON, 0600) != nil {
		return true
	}
	actual, err := runtime.readExistingSchemaSealedFile(ownerPath, true)
	if err != nil || !bytesEqual(actual, ownerJSON) {
		return true
	}
	if os.Remove(socketPath) != nil || os.Remove(ownerPath) != nil {
		return true
	}
	_ = json.NewEncoder(conn).Encode(merchantStoreFenceReply{Protocol: merchantStoreFenceProtocol, Owner: string(ownerJSON), Released: true})
	return true
}
func (runtime *productionRuntime) ensureMerchantStorePortableFence(ctx context.Context, c productionMerchantStoreCapsule, digest, invocation string) error {
	if c.Format == 2 && invocation == "" {
		return errors.New("startup baseline cannot create an ordinary deployment holder")
	}
	ownerPath := merchantStorePortableOwnerPath(c, invocation)
	if _, err := os.Lstat(ownerPath); err == nil {
		return runtime.requestMerchantStorePortableFence(ctx, c, digest, invocation, false)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	unit := merchantStorePortableUnit(c.DeploymentID)
	args := []string{"operator", "production", "writer-capsule", "hold", "--capsule", filepath.Join(c.Root, "capsule.json"), "--capsule-sha256", digest}
	if invocation != "" {
		unit = merchantStoreStartUnit(c.DeploymentID, invocation)
		budget, ok := ctx.Value(merchantStoreStartupBudgetKey{}).(merchantStoreStartupBudget)
		if !ok || budget.holder == nil {
			return errMerchantStartupBudget
		}
		if err := runtime.merchantStoreStartupGuard(ctx, c, invocation, true); err != nil {
			return err
		}
		args = []string{"operator", "production", "writer-start", "hold", "--capsule", filepath.Join(c.Root, "capsule.json"), "--capsule-sha256", digest, "--start-invocation", invocation, "--start-deadline-monotonic-usec", strconv.FormatUint(budget.deadlineUS, 10), "--start-control-pid", strconv.Itoa(budget.controlPID)}
	}
	provider := filepath.Join(c.Root, "tmp", "migrations", "merchant-store-candidate", productionCandidateLinkName)
	if target, err := os.Readlink(provider); err != nil || target != backendGoName || runtime.merchantStoreCapsuleFile(filepath.Join(filepath.Dir(provider), backendGoName), 0700) != nil || sha256MustEqual(provider, c.Writer.Candidate.PayloadSHA256) != nil {
		return errors.New("portable holder candidate has not actually been qualified")
	}
	holderType := "simple"
	if invocation != "" {
		// Only return after exec, so the parent can bind the actual ELF and argv
		// before entering its wait. Ordinary deployment holders retain their type.
		holderType = "exec"
	}
	command := []string{"--quiet", "--collect", "--unit", unit, "--property=Type=" + holderType, "--property=User=root", "--property=Restart=no", "--property=KillMode=control-group", "--", provider}
	command = append(command, args...)
	_, startErr := runtime.runner.Run(ctx, productionCommand{Name: "/usr/bin/systemd-run", Args: command, Timeout: 30 * time.Second})
	if invocation != "" {
		budget := ctx.Value(merchantStoreStartupBudgetKey{}).(merchantStoreStartupBudget)
		// A canceled systemd-run may already have created its child. Capture
		// only our exact argv/ELF/generation so failure cleanup can stop it.
		captureCtx, captureCancel := context.WithTimeout(context.Background(), 5*time.Second)
		captureErr := runtime.captureMerchantStoreStartupHolder(captureCtx, c, unit, provider, args, budget.holder)
		captureCancel()
		if startErr != nil {
			return errors.New("per-start supervision: holder creation failed or was canceled; exact child cleanup required")
		}
		if captureErr != nil {
			return captureErr
		}
		return runtime.waitMerchantStoreStartupHolder(ctx, c, invocation, func(ctx context.Context) error {
			return runtime.requestMerchantStorePortableFence(ctx, c, digest, invocation, false)
		})
	}
	if startErr != nil {
		return errors.New("portable holder systemd unit could not be created")
	}
	return runtime.awaitMerchantStoreFence(ctx, func(ctx context.Context) error {
		return runtime.requestMerchantStorePortableFence(ctx, c, digest, invocation, false)
	})
}
func (runtime *productionRuntime) merchantStorePortableStart(ctx context.Context, c productionMerchantStoreCapsule, digest string) (resultErr error) {
	invocation := os.Getenv("INVOCATION_ID")
	if c.StartupPolicy != "per-start" || !existingSchemaInvocationPattern.MatchString(invocation) {
		return errors.New("portable per-start hook requires its actual service invocation")
	}
	bounded, cancel, err := runtime.bindMerchantStoreStartupBudget(ctx, c, invocation, 0, os.Getpid())
	if err != nil {
		return err
	}
	ctx = bounded
	defer func() {
		if resultErr == nil {
			resultErr = runtime.merchantStoreStartupGuard(ctx, c, invocation, true)
		}
		cancel()
		if resultErr != nil {
			budget := ctx.Value(merchantStoreStartupBudgetKey{}).(merchantStoreStartupBudget)
			if err := runtime.cancelMerchantStoreStartupHolder(c, invocation, budget.holder); err != nil {
				resultErr = fmt.Errorf("%w; %v", resultErr, err)
			}
		}
	}()
	if err := runtime.portableStartControlPID(ctx, c, invocation); err != nil {
		return err
	}
	installed, err := sha256File(c.Binary)
	allowed := installed == c.Writer.Candidate.PayloadSHA256 || c.Format == 1 && installed == c.Writer.Rollback.PayloadSHA256
	if err != nil || !allowed || runtime.verifyMerchantStorePortableInstalled(c, installed) != nil || sha256MustEqual("/proc/self/exe", installed) != nil {
		return errors.New("portable startup checker is not the explicit installed signed provider")
	}
	if err := runtime.qualifyMerchantStoreCapsule(ctx, c, digest, invocation, false); err != nil {
		return err
	}
	parent := merchantStorePortableOwnerPath(c, "")
	if _, err := os.Lstat(parent); err == nil {
		if c.Format == 2 {
			return errors.New("startup baseline cannot reuse an ordinary parent holder")
		}
		// Explicitly prove this same capsule's live parent. A terminal/stale
		// receipt or default installed-candidate inference is never permission.
		return runtime.requestMerchantStorePortableFence(ctx, c, digest, "", false)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := runtime.ensureMerchantStorePortableFence(ctx, c, digest, invocation); err != nil {
		return err
	}
	return runtime.portableStartControlPID(ctx, c, invocation)
}
func runProductionMerchantStorePortableStart(args []string, stdout, stderr io.Writer) int {
	hold := len(args) > 0 && args[0] == "hold"
	if hold {
		args = args[1:]
	}
	flags := flag.NewFlagSet("production writer-start", flag.ContinueOnError)
	flags.SetOutput(stderr)
	path := flags.String("capsule", "", "root-private immutable portable capsule")
	digest := flags.String("capsule-sha256", "", "exact immutable capsule digest")
	invocation := flags.String("start-invocation", "", "holder-only actual service invocation")
	deadline := flags.String("start-deadline-monotonic-usec", "", "holder-only nonrenewable startup deadline")
	controlPID := flags.Int("start-control-pid", 0, "holder-only actual ExecStartPre PID")
	if flags.Parse(args) != nil || flags.NArg() != 0 || *path == "" || *digest == "" || hold && (!existingSchemaInvocationPattern.MatchString(*invocation) || *deadline == "" || *controlPID <= 1) || !hold && (*invocation != "" || *deadline != "" || *controlPID != 0) {
		return ExitUsage
	}
	runtime := defaultProductionRuntime()
	if runtime.effectiveUID() != 0 {
		return ExitError
	}
	c, err := runtime.loadMerchantStoreCapsule(*path, *digest)
	ctx, signalCancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer signalCancel()
	if err == nil {
		if hold {
			deadlineUS, parseErr := strconv.ParseUint(*deadline, 10, 64)
			if parseErr != nil || deadlineUS == 0 || strings.Trim(*deadline, "0123456789") != "" {
				return ExitUsage
			}
			var cancel context.CancelFunc
			ctx, cancel, err = runtime.bindMerchantStoreStartupBudget(ctx, c, *invocation, deadlineUS, *controlPID)
			if err == nil {
				defer cancel()
				err = runtime.holdMerchantStorePortableFence(ctx, c, *digest, *invocation)
			}
		} else {
			err = runtime.merchantStorePortableStart(ctx, c, *digest)
		}
	}
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s production writer-start: %v\n", DeployProgramName, err)
		return ExitError
	}
	_, _ = fmt.Fprintln(stdout, "merchant_store_start=qualified")
	return ExitOK
}
