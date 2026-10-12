//go:build !windows

package deploycli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/internal/deploymentfence"
	"github.com/jackc/pgx/v5"
)

type merchantOrdinaryPostgresFixture struct {
	ctx      context.Context
	admin    *pgx.Conn
	values   map[string]string
	contract *productionMerchantStoreWriterContract
}

func ordinaryParentTarget(p productionReleasePackagePlan, status string) productionMerchantStoreWriterTarget {
	return productionMerchantStoreWriterTarget{Capability: p.MerchantStoreWriterCapability, PackageSHA256: p.PackageSHA256, PayloadSHA256: p.PayloadSHA256, SourceRevision: p.GitRevision, ReleaseAssetSHA256: p.ReleaseAssetSHA256, StatusSHA256: status}
}

func ordinaryParentCapsulePhysical(c *productionMerchantStoreCapsule, pg *merchantOrdinaryPostgresFixture) {
	w := *pg.contract
	c.Writer = &w
	c.ExistingSchemaContract.SystemIdentifier, c.ExistingSchemaContract.Database, c.ExistingSchemaContract.DatabaseOID = w.SystemIdentifier, w.Database, w.DatabaseOID
	c.ExistingSchemaContract.Schema, c.ExistingSchemaContract.SchemaOID = w.Schema, w.SchemaOID
	c.ExistingSchemaContract.StartupSHA256, c.ExistingSchemaContract.SignedUnitSHA256 = w.StartupSHA256, w.SignedUnitSHA256
	for _, p := range []*productionReleasePackagePlan{&c.Candidate, &c.Rollback} {
		target := w.Candidate
		if p == &c.Rollback {
			target = w.Rollback
		}
		p.PackageSHA256, p.PayloadSHA256, p.GitRevision, p.ReleaseAssetSHA256 = target.PackageSHA256, target.PayloadSHA256, target.SourceRevision, target.ReleaseAssetSHA256
		p.MerchantStoreWriterCapability = target.Capability
	}
	c.Candidate.DeployEngineSHA256 = pg.contract.Candidate.PayloadSHA256
	c.Writer.StartupPredecessorPath = filepath.Join(filepath.Dir(c.Root), "ordinary-predecessor", "capsule.json")
	c.Writer.StartupPredecessorSHA256 = strings.Repeat("b", 64)
	c.Writer.StartupTargetPath = filepath.Join(c.Root, "capsule.json")
}

// Full manifest and immutable staged plan are loaded through the native
// readers. Package installation and systemd inventory remain component
// fixtures; the PostgreSQL owner/session and Unix peer below are real.
func newOrdinaryArchParentFixture(t *testing.T, pg *merchantOrdinaryPostgresFixture) (*productionRuntime, productionWorkspace, productionManifest, productionMerchantStoreCapsule, string) {
	t.Helper()
	f, plan, _ := existingSchemaProductionFixture(t)
	if _, err := f.runtime.apply(context.Background(), f.workspace, f.options); err != nil {
		t.Fatal(err)
	}
	m, err := f.runtime.readManifestSchema(f.workspace)
	if err != nil {
		t.Fatal(err)
	}
	c := testMerchantCapsule(t, f.runtime.paths)
	c.DeploymentID, c.Root, c.Host = f.workspace.id, merchantStoreCapsuleRoot(f.runtime.paths, f.workspace.id), productionExpectedHost
	ordinaryParentCapsulePhysical(&c, pg)
	plan.GoCandidate.DeployEngineSHA256 = pg.contract.Candidate.PayloadSHA256
	plan.GoCandidate.MerchantStoreWriterCapability, plan.GoRollback.MerchantStoreWriterCapability = c.Writer.Candidate.Capability, c.Writer.Rollback.Capability
	c.Writer.Candidate = ordinaryParentTarget(plan.GoCandidate, c.Writer.Candidate.StatusSHA256)
	c.Writer.Rollback = ordinaryParentTarget(plan.GoRollback, c.Writer.Rollback.StatusSHA256)
	c.Candidate, c.Rollback = merchantStoreCapsuleProjection(plan.GoCandidate, "candidate"), merchantStoreCapsuleProjection(plan.GoRollback, "rollback")
	plan.MerchantStoreWriter, plan.ExistingSchemaContract = c.Writer, c.ExistingSchemaContract
	plan.OperatorBinary = productionReleaseFilePlan{Path: filepath.Join(f.workspace.stagingDir, deployEngineName), SHA256: c.Candidate.DeployEngineSHA256}
	raw, err := canonicalProductionReleasePlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	c.ControllerPlanSHA256 = startupContentSHA256(raw)
	if err := os.WriteFile(f.options.StagedPlanPath, raw, 0600); err != nil {
		t.Fatal(err)
	}
	m.MerchantStoreWriter, m.ExistingSchemaContract = c.Writer, c.ExistingSchemaContract
	m.SchemaPlanSHA256, m.DatabaseSchema = c.ControllerPlanSHA256, c.Writer.Schema
	m.DeployEngineSHA256, m.OperatorBinarySHA256, m.OperatorBinary = c.Candidate.DeployEngineSHA256, plan.OperatorBinary.SHA256, plan.OperatorBinary.Path
	m.ProbeBinarySHA256 = plan.ProbeBinary.SHA256
	if err := f.runtime.writeManifest(f.workspace, m); err != nil {
		t.Fatal(err)
	}
	m, err = f.runtime.readManifestSchema(f.workspace)
	if err != nil {
		t.Fatal(err)
	}
	f.runtime.merchantStoreAuthority = nil
	if err := os.MkdirAll(filepath.Join(c.Root, "state"), 0700); err != nil {
		t.Fatal(err)
	}
	canonical, err := canonicalMerchantStoreCapsule(c)
	if err != nil || validateMerchantStoreCapsule(c, f.runtime.paths) != nil {
		t.Fatalf("ordinary Arch capsule invalid: %v %v", err, validateMerchantStoreCapsule(c, f.runtime.paths))
	}
	return f.runtime, f.workspace, m, c, startupContentSHA256(canonical)
}

type merchantOrdinaryHolderConfig struct {
	Capsule       productionMerchantStoreCapsule `json:"capsule"`
	Digest        string                         `json:"digest"`
	Manifest      productionManifest             `json:"manifest"`
	WorkspaceRoot string                         `json:"workspace_root"`
	StateDir      string                         `json:"state_dir"`
	SocketDir     string                         `json:"socket_dir"`
	Values        map[string]string              `json:"values"`
	Portable      bool                           `json:"portable"`
}

// A separate real process owns the PG session and Unix socket, so terminal
// checks observe actual original-PID disappearance rather than a supplied PID.
func TestMerchantOrdinaryParentHolderProcess(t *testing.T) {
	path := os.Getenv("LMM_ORDINARY_PARENT_HOLDER_CONFIG")
	if path == "" {
		t.Skip("owned helper process only")
	}
	raw, err := os.ReadFile(path)
	var f merchantOrdinaryHolderConfig
	if err != nil || json.Unmarshal(raw, &f) != nil {
		t.Fatal("holder fixture config invalid")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	lease, err := acquireMerchantStoreDeploymentFence(ctx, f.Values, f.Capsule.Writer)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	w := f.Capsule.Writer
	owner := productionMerchantStoreFenceOwner{Format: 1, State: "ACTIVE", DeploymentID: f.Capsule.DeploymentID, Host: f.Capsule.Host, PlanSHA256: f.Capsule.ControllerPlanSHA256, ContractSHA256: merchantStoreFenceContractSHA(w), ProviderSHA256: w.Candidate.PayloadSHA256, Nonce: strings.Repeat("d", 32), HolderPID: os.Getpid(), HolderUnit: merchantStoreFenceUnit(f.Capsule.DeploymentID), HolderInvocationID: strings.Repeat("e", 32), BackendPID: lease.backendPID, SystemIdentifier: w.SystemIdentifier, Database: w.Database, DatabaseOID: w.DatabaseOID, Schema: w.Schema, SchemaOID: w.SchemaOID, Role: w.Role}
	workspace := productionWorkspace{root: f.WorkspaceRoot, id: f.Capsule.DeploymentID, stateDir: f.StateDir}
	ownerPath := merchantStoreFenceOwnerPath(workspace)
	if f.Portable {
		owner.Purpose, owner.Service, owner.HolderUnit, owner.PlanSHA256 = "portable-deploy", f.Capsule.Service, merchantStorePortableUnit(f.Capsule.DeploymentID), f.Digest
		ownerPath = merchantStorePortableOwnerPath(f.Capsule, "")
	}
	if err := lease.claimOwner(ctx, owner, f.Capsule.deploymentSHA256(), nil); err != nil {
		t.Fatal(err)
	}
	canonical, err := canonicalMerchantStoreFenceOwner(owner)
	if err != nil || os.WriteFile(ownerPath, canonical, 0600) != nil {
		t.Fatal("holder fixture owner receipt failed")
	}
	runtime := &productionRuntime{requiredOwnerUID: uint32(os.Getuid()), merchantStoreSocketDirectory: f.SocketDir, hostname: func() (string, error) { return owner.Host, nil }}
	root := workspace.root
	if f.Portable {
		root = f.Capsule.Root
	}
	socket, err := runtime.merchantStoreSocketPath(root, owner, true)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	defer os.Remove(socket)
	if err := os.Chmod(socket, 0600); err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			conn, err := listener.AcceptUnix()
			if err != nil {
				return
			}
			if f.Portable {
				runtime.handleMerchantStorePortableConnection(ctx, f.Capsule, f.Digest, "", owner, canonical, lease, conn)
			} else {
				runtime.handleMerchantStoreFenceConnection(ctx, workspace, f.Manifest, owner, canonical, lease, conn)
			}
		}
	}()
	fmt.Println("READY")
	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() || scanner.Text() != "release" {
		t.Fatal("holder fixture release command missing")
	}
	if err := lease.ReleaseOwner(ctx); err != nil {
		t.Fatal(err)
	}
	// The PG exact CAS is real. The completed wrapper/status inventory is an
	// explicit fixture. Match the native portable terminal file convention.
	if f.Portable {
		if err := os.WriteFile(filepath.Join(filepath.Dir(ownerPath), "portable-released-owner.json"), canonical, 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(ownerPath); err != nil {
			t.Fatal(err)
		}
	}
}

func startMerchantOrdinaryHolder(t *testing.T, runtime *productionRuntime, cfg merchantOrdinaryHolderConfig) (productionMerchantStoreFenceOwner, []byte, func()) {
	t.Helper()
	socket, err := os.MkdirTemp("/tmp", "lmm-ordinary-rpc-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(socket) })
	cfg.SocketDir = socket
	runtime.merchantStoreSocketDirectory = socket
	raw, _ := json.Marshal(cfg)
	path := filepath.Join(t.TempDir(), "holder.json")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, "-test.run=^TestMerchantOrdinaryParentHolderProcess$", "-test.timeout=90s")
	cmd.Env = append(os.Environ(), "LMM_ORDINARY_PARENT_HOLDER_CONFIG="+path)
	input, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	finished := false
	t.Cleanup(func() {
		if !finished {
			_ = input.Close()
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	})
	scanner := bufio.NewScanner(output)
	if !scanner.Scan() || scanner.Text() != "READY" {
		t.Fatal("owned real holder failed before readiness")
	}
	ownerPath := filepath.Join(cfg.StateDir, "merchant-fence-owner.json")
	if cfg.Portable {
		ownerPath = merchantStorePortableOwnerPath(cfg.Capsule, "")
	}
	ownerRaw, err := os.ReadFile(ownerPath)
	owner, parseErr := parseMerchantStoreFenceOwner(ownerRaw)
	if err != nil || parseErr != nil || owner.HolderPID != cmd.Process.Pid {
		t.Fatalf("real holder receipt invalid: %v %v", err, parseErr)
	}
	runtime.runner = &merchantStoreHolderUnitFixture{owner: owner}
	release := func() {
		t.Helper()
		if _, err := io.WriteString(input, "release\n"); err != nil {
			t.Fatal(err)
		}
		_ = input.Close()
		if err := cmd.Wait(); err != nil {
			t.Fatal(err)
		}
		finished = true
		if _, err := os.Lstat(fmt.Sprintf("/proc/%d", owner.HolderPID)); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("original actual holder PID disappearance is unknown")
		}
	}
	return owner, ownerRaw, release
}

func ordinaryParentReservedRows(t *testing.T, pg *merchantOrdinaryPostgresFixture) [][2]string {
	t.Helper()
	rows, err := pg.admin.Query(pg.ctx, `SELECT key,value FROM merchant_fence.options WHERE `+deploymentfence.PostgreSQLPresencePredicate+` ORDER BY key,value`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var found [][2]string
	for rows.Next() {
		var row [2]string
		if err := rows.Scan(&row[0], &row[1]); err != nil {
			t.Fatal(err)
		}
		found = append(found, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return found
}

func ordinaryParentAtomicRestart(t *testing.T, pg *merchantOrdinaryPostgresFixture, c productionMerchantStoreCapsule, digest string) {
	t.Helper()
	lease, err := acquireMerchantStoreDeploymentFence(pg.ctx, pg.values, c.Writer)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	w := c.Writer
	invocation := strings.Repeat("f", 32)
	owner := productionMerchantStoreFenceOwner{Format: 1, State: "ACTIVE", DeploymentID: c.DeploymentID, Host: c.Host, PlanSHA256: digest, ContractSHA256: merchantStoreFenceContractSHA(w), ProviderSHA256: w.Candidate.PayloadSHA256, Nonce: strings.Repeat("c", 32), HolderPID: os.Getpid(), HolderUnit: merchantStoreStartUnit(c.DeploymentID, invocation), HolderInvocationID: strings.Repeat("a", 32), BackendPID: lease.backendPID, SystemIdentifier: w.SystemIdentifier, Database: w.Database, DatabaseOID: w.DatabaseOID, Schema: w.Schema, SchemaOID: w.SchemaOID, Role: w.Role, Purpose: "start", Service: c.Service, StartInvocationID: invocation}
	// The real per-start holder uses this exact atomic primitive and a
	// separately qualified deployment executable, not the API payload hash.
	if err := lease.claimOwner(pg.ctx, owner, c.deploymentSHA256(), nil); err != nil {
		t.Fatal(err)
	}
	if err := lease.CheckOwner(pg.ctx); err != nil {
		t.Fatal(err)
	}
	canonical, _ := canonicalMerchantStoreFenceOwner(owner)
	rows := ordinaryParentReservedRows(t, pg)
	if len(rows) != 1 || rows[0] != [2]string{merchantStoreFenceOwnerKey(owner), string(canonical)} {
		t.Fatal("restart did not establish exactly its new actual session owner")
	}
	second, err := acquireMerchantStoreDeploymentFence(pg.ctx, pg.values, c.Writer)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	duplicate := owner
	duplicate.BackendPID = second.backendPID
	if err := second.claimOwner(pg.ctx, duplicate, c.deploymentSHA256(), nil); err == nil {
		t.Fatal("restart allowed a second actual owner")
	}
	if err := lease.ReleaseOwner(pg.ctx); err != nil {
		t.Fatal(err)
	}
	if len(ordinaryParentReservedRows(t, pg)) != 0 {
		t.Fatal("restart exact release retained an owner")
	}
	var floor string
	if err := pg.admin.QueryRow(pg.ctx, `SELECT value FROM merchant_fence.options WHERE key='MerchantStoreMinimumWriterCapability'`).Scan(&floor); err != nil || floor != "4" {
		t.Fatal("restart changed the writer floor")
	}
}

func TestMerchantStoreOrdinaryParentActualPostgres(t *testing.T) {
	pg := newMerchantOrdinaryPostgresFixture(t)
	t.Run("Arch live same plan and terminal restart", func(t *testing.T) {
		runtime, workspace, manifest, c, digest := newOrdinaryArchParentFixture(t, pg)
		owner, raw, release := startMerchantOrdinaryHolder(t, runtime, merchantOrdinaryHolderConfig{Capsule: c, Digest: digest, Manifest: manifest, WorkspaceRoot: workspace.root, StateDir: workspace.stateDir, Values: pg.values})
		key := merchantStoreFenceOwnerKey(owner)
		if live, err := runtime.requestMerchantStoreOrdinaryParentValues(pg.ctx, c, digest, pg.values); err != nil || !live {
			t.Fatalf("actual same-plan Arch parent was not reused: %t %v", live, err)
		}
		if rows := ordinaryParentReservedRows(t, pg); len(rows) != 1 || rows[0] != [2]string{key, string(raw)} {
			t.Fatal("live parent reuse claimed or changed the existing owner")
		}
		foreign := owner
		foreign.PlanSHA256 = strings.Repeat("1", 64)
		foreignRaw, _ := canonicalMerchantStoreFenceOwner(foreign)
		if _, err := pg.admin.Exec(pg.ctx, `UPDATE merchant_fence.options SET value=$1 WHERE key=$2`, string(foreignRaw), key); err != nil {
			t.Fatal(err)
		}
		if live, err := runtime.requestMerchantStoreOrdinaryParentValues(pg.ctx, c, digest, pg.values); err == nil || live {
			t.Fatal("foreign actual owner authorized Arch startup")
		}
		if rows := ordinaryParentReservedRows(t, pg); len(rows) != 1 || rows[0] != [2]string{key, string(foreignRaw)} {
			t.Fatal("foreign owner refusal mutated the database")
		}
		if _, err := pg.admin.Exec(pg.ctx, `UPDATE merchant_fence.options SET value=$1 WHERE key=$2`, string(raw), key); err != nil {
			t.Fatal(err)
		}
		unknownKey := deploymentfence.OptionPrefix + "unknown"
		if _, err := pg.admin.Exec(pg.ctx, `INSERT INTO merchant_fence.options VALUES($1,$2)`, unknownKey, "unknown-reserved-owner"); err != nil {
			t.Fatal(err)
		}
		if live, err := runtime.requestMerchantStoreOrdinaryParentValues(pg.ctx, c, digest, pg.values); err == nil || live {
			t.Fatal("multiple actual owners authorized startup")
		}
		if len(ordinaryParentReservedRows(t, pg)) != 2 {
			t.Fatal("multiple owner refusal changed the database")
		}
		if _, err := pg.admin.Exec(pg.ctx, `DELETE FROM merchant_fence.options WHERE key=$1`, unknownKey); err != nil {
			t.Fatal(err)
		}
		runtime.runner.(*merchantStoreHolderUnitFixture).changedInvocation = true
		if live, err := runtime.requestMerchantStoreOrdinaryParentValues(pg.ctx, c, digest, pg.values); err == nil || live {
			t.Fatal("stale manager generation authorized actual parent reuse")
		}
		runtime.runner.(*merchantStoreHolderUnitFixture).changedInvocation = false
		release()
		if len(ordinaryParentReservedRows(t, pg)) != 0 {
			t.Fatal("original holder did not perform actual exact release")
		}
		after, err := os.ReadFile(merchantStoreFenceOwnerPath(workspace))
		if err != nil || string(after) != string(raw) {
			t.Fatal("Arch terminal test lost the original native owner receipt")
		}
		if live, err := runtime.requestMerchantStoreOrdinaryParentValues(pg.ctx, c, digest, pg.values); err == nil || live {
			t.Fatal("owner absence without CONFIRMED authorized restart")
		}
		status, _ := json.Marshal(productionStatus{Format: productionStatusFormat, DeploymentID: workspace.id, Phase: "CONFIRMED"})
		if err := os.WriteFile(workspace.statusPath, status, 0600); err != nil {
			t.Fatal(err)
		}
		loadError := true
		runtime.runner = existingSchemaTestRunner{run: func(command productionCommand) ([]byte, error) {
			if command.Name != commandSystemctl || len(command.Args) < 3 || command.Args[0] != "show" || command.Args[1] != owner.HolderUnit {
				return nil, errors.New("terminal fixture rejects mutations and unknown commands")
			}
			if strings.Join(command.Args[2:], " ") == "--property=LoadState --value" {
				if loadError {
					return []byte("not-found\n"), errors.New("manager observation failed")
				}
				return []byte("not-found\n"), nil
			}
			return nil, errors.New("collected transient unit")
		}}
		if live, err := runtime.requestMerchantStoreOrdinaryParentValues(pg.ctx, c, digest, pg.values); err == nil || live {
			t.Fatal("error plus not-found stdout authorized terminal restart")
		}
		loadError = false
		if live, err := runtime.requestMerchantStoreOrdinaryParentValues(pg.ctx, c, digest, pg.values); err != nil || live {
			t.Fatalf("CONFIRMED released Arch parent blocked fresh startup: %t %v", live, err)
		}
		ordinaryParentAtomicRestart(t, pg, c, digest)
	})
	t.Run("portable released confirmed restart", func(t *testing.T) {
		root := t.TempDir()
		paths := defaultProductionPaths()
		paths.WorkRoot = filepath.Join(root, "arch-work")
		c := testMerchantCapsule(t, paths)
		ordinaryParentCapsulePhysical(&c, pg)
		if err := os.MkdirAll(filepath.Join(c.Root, "state"), 0700); err != nil {
			t.Fatal(err)
		}
		canonical, _ := canonicalMerchantStoreCapsule(c)
		digest := startupContentSHA256(canonical)
		runtime := &productionRuntime{paths: paths, requiredOwnerUID: uint32(os.Getuid()), portableWorkRoot: filepath.Join(root, "standalone"), hostname: func() (string, error) { return c.Host, nil }}
		_, raw, release := startMerchantOrdinaryHolder(t, runtime, merchantOrdinaryHolderConfig{Capsule: c, Digest: digest, Values: pg.values, Portable: true})
		if live, err := runtime.requestMerchantStoreOrdinaryParentValues(pg.ctx, c, digest, pg.values); err != nil || !live {
			t.Fatalf("actual same-plan portable parent not reused: %t %v", live, err)
		}
		release()
		if _, err := os.Lstat(merchantStorePortableOwnerPath(c, "")); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("released portable holder still has an active owner receipt")
		}
		releasedRaw, err := os.ReadFile(filepath.Join(c.Root, "state", "portable-released-owner.json"))
		if err != nil || string(releasedRaw) != string(raw) {
			t.Fatal("portable released owner bytes were not preserved")
		}
		work := filepath.Join(runtime.portableTransactionRoot(), c.DeploymentID)
		if err := os.MkdirAll(work, 0700); err != nil {
			t.Fatal(err)
		}
		state := map[string]any{"release": c.DeploymentID, "phase": "MUTATION_PENDING", "migrate": false, "sha256": c.Writer.Candidate.PayloadSHA256, "previous_sha256": c.Writer.Rollback.PayloadSHA256}
		stateRaw, _ := json.Marshal(state)
		if err := os.WriteFile(filepath.Join(work, "state.json"), stateRaw, 0644); err != nil {
			t.Fatal(err)
		}
		if live, err := runtime.requestMerchantStoreOrdinaryParentValues(pg.ctx, c, digest, pg.values); err == nil || live {
			t.Fatal("portable owner absence without CONFIRMED authorized restart")
		}
		state["phase"] = "CONFIRMED"
		stateRaw, _ = json.Marshal(state)
		if err := os.WriteFile(filepath.Join(work, "state.json"), stateRaw, 0644); err != nil {
			t.Fatal(err)
		}
		if live, err := runtime.requestMerchantStoreOrdinaryParentValues(pg.ctx, c, digest, pg.values); err != nil || live {
			t.Fatalf("CONFIRMED released portable parent blocked fresh startup: %t %v", live, err)
		}
		ordinaryParentAtomicRestart(t, pg, c, digest)
	})
}

// This server is owned by this test and has no TCP listener. Never use an
// application DSN or an already running database for deployment tests.
func newMerchantOrdinaryPostgresFixture(t *testing.T) *merchantOrdinaryPostgresFixture {
	t.Helper()
	if os.Getenv("LMM_TEST_POSTGRES") != "1" {
		t.Skip("set LMM_TEST_POSTGRES=1 for owned ordinary parent qualification")
	}
	if os.Geteuid() == 0 {
		t.Skip("initdb requires an unprivileged process")
	}
	for _, tool := range []string{"initdb", "pg_ctl"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip("owned PostgreSQL tools unavailable")
		}
	}
	for _, assignment := range os.Environ() {
		key, _, found := strings.Cut(assignment, "=")
		if found && strings.HasPrefix(key, "PG") {
			t.Setenv(key, "")
		}
	}
	root := t.TempDir()
	data := filepath.Join(root, "data")
	socket, err := os.MkdirTemp("/tmp", "lmm-ordinary-pg-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(socket) })
	run := func(binary string, args ...string) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		out, err := exec.CommandContext(ctx, binary, args...).CombinedOutput()
		if err != nil {
			t.Fatalf("owned %s: %v\n%s", binary, err, out)
		}
	}
	run("initdb", "-D", data, "--auth=trust", "--no-locale", "--encoding=UTF8", "--username=lmm_ordinary_test")
	run("pg_ctl", "-D", data, "-l", filepath.Join(root, "postgres.log"), "-w", "-t", "20", "-o", fmt.Sprintf("-c listen_addresses='' -k %s -p 5432", socket), "start")
	t.Cleanup(func() { run("pg_ctl", "-D", data, "-m", "fast", "-w", "-t", "20", "stop") })
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(cancel)
	dsn := "postgres://lmm_ordinary_test@/postgres?host=" + url.QueryEscape(socket) + "&port=5432&sslmode=disable"
	admin, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = admin.Close(context.Background()) })
	if _, err := admin.Exec(ctx, `CREATE SCHEMA merchant_fence;
CREATE TABLE merchant_fence.options(key text PRIMARY KEY,value text NOT NULL);
INSERT INTO merchant_fence.options VALUES('MerchantStoreMinimumWriterCapability','4');
CREATE ROLE lmm_ordinary_business LOGIN;
ALTER ROLE lmm_ordinary_business SET default_transaction_isolation='repeatable read';
GRANT USAGE ON SCHEMA merchant_fence TO lmm_ordinary_business;
GRANT SELECT,INSERT,DELETE ON merchant_fence.options TO lmm_ordinary_business;
REVOKE EXECUTE ON FUNCTION pg_catalog.pg_control_system() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION pg_catalog.pg_control_system() TO lmm_ordinary_business;`); err != nil {
		t.Fatal(err)
	}
	var listeners string
	if err := admin.QueryRow(ctx, "SHOW listen_addresses").Scan(&listeners); err != nil || listeners != "" {
		t.Fatalf("owned cluster exposed TCP: %q %v", listeners, err)
	}
	contract := testMerchantWriterContract()
	contract.Database, contract.Schema, contract.Role = "postgres", "merchant_fence", "lmm_ordinary_business"
	contract.Candidate.PayloadSHA256, err = sha256File("/proc/self/exe")
	if err != nil {
		t.Fatal(err)
	}
	if err := admin.QueryRow(ctx, `SELECT system_identifier::text,(SELECT oid::bigint FROM pg_database WHERE datname='postgres'),
(SELECT oid::bigint FROM pg_namespace WHERE nspname='merchant_fence') FROM pg_control_system()`).Scan(&contract.SystemIdentifier, &contract.DatabaseOID, &contract.SchemaOID); err != nil {
		t.Fatal(err)
	}
	return &merchantOrdinaryPostgresFixture{ctx: ctx, admin: admin, contract: contract, values: map[string]string{"SQL_DSN": "postgres://lmm_ordinary_business@/postgres?host=" + url.QueryEscape(socket) + "&port=5432&sslmode=disable"}}
}
