//go:build linux

package deploycli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// This is an opt-in LOCAL fixture, not official signature or systemd
// qualification. PostgreSQL, its parent CAS/session/socket, capsule files,
// stopped PID, package/archive bytes, intent, installed engine, hook, and
// startup seal are real. Only Sigstore and provider/systemd inventories are
// simulated. The fixed installed engine is confined to a private mount.
func TestMerchantStoreOrdinaryBindActualFilesAndParent(t *testing.T) {
	if os.Getenv("LMM_TEST_ORDINARY_BIND") != "1" {
		t.Skip("set LMM_TEST_ORDINARY_BIND=1 and LMM_TEST_POSTGRES=1 for isolated local bind")
	}
	if os.Geteuid() == 0 {
		t.Skip("outer fixture must own initdb as an ordinary user")
	}
	for _, tool := range []string{"sudo", "bwrap", "bsdtar", "psql"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("local fixture requires %s", tool)
		}
	}
	if _, err := os.Lstat(deployEngineInstalledPath); !errors.Is(err, os.ErrNotExist) {
		t.Skip("fixture requires the host fixed engine to be absent; never modifies an existing host engine")
	}
	db := newMerchantOrdinaryPostgresFixture(t)
	root, err := os.MkdirTemp("/tmp", "lmm-ob-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	engineDir := filepath.Join(root, "engine")
	if err := os.Mkdir(engineDir, 0700); err != nil {
		t.Fatal(err)
	}
	input, _ := json.Marshal(struct {
		Values map[string]string
		Writer *productionMerchantStoreWriterContract
	}{db.values, db.contract})
	if err := os.WriteFile(filepath.Join(root, "database.json"), input, 0600); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/usr/bin/sudo", "-n", "/usr/bin/bwrap", "--ro-bind", "/", "/", "--bind", root, root, "--bind", engineDir, filepath.Dir(deployEngineInstalledPath), "--proc", "/proc", "--dev-bind", "/dev", "/dev", "--setenv", "LMM_BIND_PRIVATE_ROOT", root, "--setenv", "LMM_BIND_OUTER_UID", strconv.Itoa(os.Getuid()), "--", executable, "-test.run=^TestMerchantStoreOrdinaryBindIsolatedChild$", "-test.v", "-test.timeout=140s")
	cmd.Env = []string{"PATH=/usr/bin:/bin", "LANG=C", "LC_ALL=C", "LMM_BIND_PRIVATE_ROOT=" + root, "LMM_BIND_OUTER_UID=" + strconv.Itoa(os.Getuid())}
	out, err := cmd.CombinedOutput()
	if _, hostErr := os.Lstat(deployEngineInstalledPath); !errors.Is(hostErr, os.ErrNotExist) {
		t.Fatal("private bind fixture changed host installed engine")
	}
	if err != nil || !bytes.Contains(out, []byte("actual_native_bind=true")) {
		t.Fatalf("isolated full bind: %v\n%s", err, out)
	}
	t.Log(string(out))
}

type ordinaryBindIntegrationRunner struct {
	loaded     string
	service    string
	holder     string
	stoppedPID int
	invocation string
	holderINV  string
	journal    []byte
	drop       string
	newPath    string
	newSHA     string
	oldPath    string
	oldSHA     string
	reloads    int
	signatures int
	starting   bool
	commands   []productionCommand
}

func (r *ordinaryBindIntegrationRunner) Run(ctx context.Context, c productionCommand) ([]byte, error) {
	r.commands = append(r.commands, c)
	if c.Name == commandBsdtar || c.Name == commandPSQL {
		// Real unsigned archive layout/extraction and real READ ONLY PostgreSQL.
		return (osProductionCommandRunner{}).Run(ctx, c)
	}
	if c.Name == commandCosign && len(c.Args) == 8 && c.Args[0] == "verify-blob" && c.Args[1] == "--bundle" && c.Args[3] == "--certificate-identity" && c.Args[5] == "--certificate-oidc-issuer" && c.Args[6] == productionReleaseOIDCIssuer {
		r.signatures++ // explicitly simulated signature verifier
		return nil, nil
	}
	if c.Name == commandSystemctl {
		if slices.Equal(c.Args, []string{"daemon-reload"}) {
			hook, err := os.ReadFile(r.drop)
			if err != nil || !bytes.Equal(hook, merchantStorePortableHookBytes(deployEngineInstalledPath, r.newPath, r.newSHA)) {
				return nil, errors.New("fixture reload did not find exact new hook")
			}
			r.loaded = strings.ReplaceAll(strings.ReplaceAll(r.loaded, r.oldPath, r.newPath), r.oldSHA, r.newSHA)
			r.reloads++
			return nil, nil
		}
		if len(c.Args) >= 2 && c.Args[0] == "show" {
			if c.Args[1] == r.service && slices.Contains(c.Args, "--property=ControlPID") {
				return []byte(strconv.Itoa(os.Getpid()) + "\n"), nil
			}
			if c.Args[1] == r.service && slices.Contains(c.Args, "--property=InvocationID,ActiveState,SubState,MainPID,ControlPID,TimeoutStartUSec,InactiveExitTimestampMonotonic") {
				return []byte(fmt.Sprintf("InvocationID=%s\nActiveState=activating\nSubState=start-pre\nMainPID=0\nControlPID=%d\nTimeoutStartUSec=4min\nInactiveExitTimestampMonotonic=1000000\n", strings.Repeat("9", 32), os.Getpid())), nil
			}
			if c.Args[1] == r.service && slices.Contains(c.Args, "--all") {
				return []byte(r.loaded), nil
			}
			if c.Args[1] == r.service {
				return []byte(fmt.Sprintf("MainPID=0\nExecMainPID=%d\nExecMainCode=1\nExecMainStatus=0\nActiveState=inactive\nSubState=dead\nResult=success\nControlGroup=\nRestart=no\nInvocationID=%s\n", r.stoppedPID, r.invocation)), nil
			}
			if c.Args[1] == r.holder {
				return []byte(fmt.Sprintf("MainPID=%d\nExecMainPID=%d\nExecMainCode=0\nExecMainStatus=0\nActiveState=active\nSubState=running\nResult=success\nControlGroup=\nRestart=no\nInvocationID=%s\n", os.Getpid(), os.Getpid(), r.holderINV)), nil
			}
		}
	}
	if c.Name == commandJournalctl && slices.Equal(c.Args, []string{"--no-pager", "-o", "cat", "_SYSTEMD_INVOCATION_ID=" + r.invocation}) {
		return r.journal, nil
	}
	if c.Name == commandRunuser && len(c.Args) >= 4 && slices.Equal(c.Args[:3], []string{"--user", "root", "--"}) && filepath.Base(c.Args[3]) == productionCandidateLinkName {
		if slices.Equal(c.Args[4:], []string{"merchant-store-writer-gate", "status"}) {
			return merchantWriterStatusJSON(4, 4), nil
		}
		if slices.Equal(c.Args[4:], []string{"migrate", "--verify"}) {
			return nil, nil
		}
	}
	return nil, fmt.Errorf("isolated fixture rejects command %q %q", c.Name, c.Args)
}

func ordinaryBindFixturePackage(t *testing.T, root, role string, api, engine, unit []byte, revision string) (productionReleasePackagePlan, productionMerchantStoreWriterTarget) {
	t.Helper()
	version := "0.2.90-1"
	if role == "candidate" {
		version = "0.2.91-1"
	}
	name := "lmm-api-go-fixture"
	type member struct {
		source, target, body string
		mode                 int64
	}
	members := []member{
		{"REVISION", "usr/share/doc/" + productionAURPackageName + "/REVISION", revision + "\n", 0644},
		{"API_ROUTE_CONTRACT_REVISION", "usr/share/doc/" + productionAURPackageName + "/API_ROUTE_CONTRACT_REVISION", "contract-v1\n", 0644},
		{"MERCHANT_STORE_WRITER_CAPABILITY", "usr/share/doc/" + productionAURPackageName + "/MERCHANT_STORE_WRITER_CAPABILITY", "4\n", 0644},
		{backendGoName, "usr/bin/" + backendGoName, string(api), 0755},
		{deployEngineName, "usr/lib/lmm-api-deploy/engine", string(engine), 0755},
		{"lmm-api-deploy", "usr/bin/lmm-api-deploy", "#!/bin/sh\nexec /usr/lib/lmm-api-deploy/engine \"$@\"\n", 0755},
		{"lmm-api.service", "usr/lib/systemd/system/lmm-api.service", string(unit), 0644},
		{"lmm-api-operator.sudoers", "etc/sudoers.d/lmm-api-operator", "# isolated unsigned fixture\n", 0440},
	}
	asset := filepath.Join(root, merchantStoreCapsuleAsset(role, "release.tar.gz"))
	var release, pkg []testTarEntry
	release = append(release, testTarEntry{name: name + "/", mode: 0755, directory: true})
	for _, m := range members {
		mode := m.mode
		if m.source == "lmm-api-operator.sudoers" {
			mode = 0644
		}
		release = append(release, testTarEntry{name: name + "/" + m.source, body: m.body, mode: mode})
		pkg = append(pkg, testTarEntry{name: m.target, body: m.body, mode: m.mode})
	}
	writeTestTarGzip(t, asset, release)
	assetSHA, err := sha256File(asset)
	if err != nil {
		t.Fatal(err)
	}
	pkg = append(pkg, testTarEntry{name: ".PKGINFO", body: testProductionPackageInfo(t, productionAURPackageName, version), mode: 0644}, testTarEntry{name: ".MTREE", body: testPackageMtree(t, true), mode: 0644}, testTarEntry{name: "etc/sudoers.d/", mode: 0750, directory: true}, testTarEntry{name: "usr/share/doc/" + productionAURPackageName + "/RELEASE_ASSET_SHA256", body: assetSHA + "\n", mode: 0644})
	packagePath := filepath.Join(root, merchantStoreCapsuleAsset(role, "pkg.tar.zst"))
	writeTestTarGzip(t, packagePath, pkg)
	packageSHA, _ := sha256File(packagePath)
	bundle := []byte("unsigned local fixture signature bundle\n")
	if err := os.WriteFile(filepath.Join(root, merchantStoreCapsuleAsset(role, "sigstore.json")), bundle, 0600); err != nil {
		t.Fatal(err)
	}
	releaseVersion, _ := packageReleaseVersion(version)
	p := productionReleasePackagePlan{Name: productionAURPackageName, Version: version, Identity: productionAURPackageName + " " + version, ReleaseTag: "go-v" + releaseVersion, Workflow: "release-go.yml", PackageSHA256: packageSHA, PayloadSHA256: startupContentSHA256(api), DeployEngineSHA256: startupContentSHA256(engine), GitRevision: revision, ContractRevision: "contract-v1", ReleaseAssetSHA256: assetSHA, SignatureBundleSHA256: startupContentSHA256(bundle), MerchantStoreWriterCapability: 4}
	target := productionMerchantStoreWriterTarget{Capability: 4, PackageSHA256: packageSHA, PayloadSHA256: p.PayloadSHA256, SourceRevision: revision, ReleaseAssetSHA256: assetSHA, StatusSHA256: startupContentSHA256(merchantWriterStatusJSON(4, 4))}
	return merchantStoreCapsuleProjection(p, role), target
}

func TestMerchantStoreOrdinaryBindIsolatedChild(t *testing.T) {
	root := os.Getenv("LMM_BIND_PRIVATE_ROOT")
	if root == "" {
		t.Skip("only the isolated outer fixture launches this child")
	}
	if os.Geteuid() != 0 || filepath.Dir(root) != "/tmp" || !strings.HasPrefix(filepath.Base(root), "lmm-ob-") {
		t.Fatal("invalid isolated child boundary")
	}
	// The actual bind mount, rather than a production path override, confines
	// the fixed engine. Refuse execution unless both names are the same inode.
	privateEngine, err := os.Stat(filepath.Join(root, "engine"))
	fixedEngine, fixedErr := os.Stat(filepath.Dir(deployEngineInstalledPath))
	if err != nil || fixedErr != nil || !os.SameFile(privateEngine, fixedEngine) {
		t.Fatal("fixed engine directory is not the private fixture bind mount")
	}
	outerUID, err := strconv.Atoi(os.Getenv("LMM_BIND_OUTER_UID"))
	if err != nil || outerUID <= 0 {
		t.Fatal("invalid outer fixture owner")
	}
	defer func() {
		_ = os.RemoveAll(filepath.Join(root, "fixture"))
		_ = os.RemoveAll(filepath.Join(root, "s"))
		_ = os.Remove(deployEngineInstalledPath)
		_ = os.Chown(root, outerUID, -1)
	}()
	if err := os.Chown(root, 0, 0); err != nil {
		t.Fatal(err)
	}
	fixtureRoot := filepath.Join(root, "fixture")
	if err := os.Mkdir(fixtureRoot, 0700); err != nil {
		t.Fatal(err)
	}
	var input struct {
		Values map[string]string
		Writer *productionMerchantStoreWriterContract
	}
	raw, err := os.ReadFile(filepath.Join(root, "database.json"))
	if err != nil || json.Unmarshal(raw, &input) != nil || input.Writer == nil {
		t.Fatal("owned PostgreSQL fixture input unavailable")
	}
	input.Values["SQL_DSN"] += "&options=-csearch_path%3Dmerchant_fence"
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	paths := defaultProductionPaths()
	paths.WorkRoot = filepath.Join(fixtureRoot, "work")
	paths.ConfigDir = filepath.Join(fixtureRoot, "config")
	paths.DropInDir = filepath.Join(fixtureRoot, "drop-ins")
	paths.InstalledBinary = filepath.Join(fixtureRoot, "bin", productionCandidateLinkName)
	for _, path := range []string{paths.ConfigDir, paths.DropInDir, filepath.Dir(paths.InstalledBinary)} {
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	write := func(path string, body []byte, mode os.FileMode) {
		t.Helper()
		if err := os.WriteFile(path, body, mode); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(paths.ConfigDir, "lmm-api-go.env"), []byte("SQL_DSN="+input.Values["SQL_DSN"]+"\nLMM_DB_MIGRATION_MODE=verify\n"), 0600)
	oldAPI, newAPI, oldEngine := []byte("old API unsigned fixture\n"), []byte("new API unsigned fixture\n"), []byte("old engine unsigned fixture\n")
	newEngine, err := os.ReadFile("/proc/self/exe")
	if err != nil {
		t.Fatal(err)
	}
	newAPI = newEngine
	write(filepath.Join(filepath.Dir(paths.InstalledBinary), backendGoName), oldAPI, 0755)
	if err := os.Symlink(backendGoName, paths.InstalledBinary); err != nil {
		t.Fatal(err)
	}
	write(deployEngineInstalledPath, oldEngine, 0755)
	unit := []byte("[Service]\nEnvironment=LMM_DB_MIGRATION_MODE=verify\nExecStart=" + paths.InstalledBinary + " serve\n")
	unitPath := filepath.Join(fixtureRoot, "lmm-api.service")
	write(unitPath, unit, 0644)
	process := exec.Command("/usr/bin/true")
	if err := process.Start(); err != nil {
		t.Fatal(err)
	}
	stoppedPID := process.Process.Pid
	if err := process.Wait(); err != nil {
		t.Fatal(err)
	}
	runner := &ordinaryBindIntegrationRunner{service: paths.Service, stoppedPID: stoppedPID, invocation: strings.Repeat("a", 32), holderINV: strings.Repeat("b", 32), drop: filepath.Join(paths.DropInDir, "90-merchant-startup-baseline.conf"), journal: []byte("received signal: terminated\nrefund_tasks execution_complete=true accepted=0 finished=0 active=0 failed=0 (execution completion is not financial success)\nserver exited\n")}
	runtime := &productionRuntime{paths: paths, runner: runner, requiredOwnerUID: 0, effectiveUID: os.Geteuid, hostname: func() (string, error) { return "dmit-ubuntu", nil }, now: time.Now, portableWorkRoot: filepath.Join(fixtureRoot, "standalone"), merchantStoreSocketDirectory: filepath.Join(root, "s")}
	schema, err := runtime.captureExistingSchemaContract(ctx, input.Writer.Schema)
	if err != nil {
		t.Fatal(err)
	}
	old := testMerchantCapsule(t, paths)
	old.DeploymentID, old.Root = "ordinary-old", merchantStoreCapsuleRoot(paths, "ordinary-old")
	old.Writer, old.ExistingSchemaContract = input.Writer, &schema
	next := cloneMerchantCapsule(old)
	next.DeploymentID, next.Root = "ordinary-next", merchantStoreCapsuleRoot(paths, "ordinary-next")
	for _, c := range []productionMerchantStoreCapsule{old, next} {
		if err := os.MkdirAll(filepath.Join(c.Root, "assets"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	old.Candidate, old.Writer.Candidate = ordinaryBindFixturePackage(t, old.Root, "candidate", oldAPI, oldEngine, unit, strings.Repeat("c", 40))
	old.Rollback, old.Writer.Rollback = ordinaryBindFixturePackage(t, old.Root, "rollback", oldAPI, oldEngine, unit, strings.Repeat("c", 40))
	next.Candidate, next.Writer.Candidate = ordinaryBindFixturePackage(t, next.Root, "candidate", newAPI, newEngine, unit, strings.Repeat("d", 40))
	// Retained tuples bind the predecessor's actual candidate bytes, including
	// its package digest; copy all three raw assets, never rearchive them.
	next.Rollback = merchantStoreCapsuleProjection(old.Candidate, "rollback")
	next.Writer.Rollback = old.Writer.Candidate
	for _, kind := range []string{"pkg.tar.zst", "release.tar.gz", "sigstore.json"} {
		body, err := os.ReadFile(filepath.Join(old.Root, merchantStoreCapsuleAsset("candidate", kind)))
		if err != nil {
			t.Fatal(err)
		}
		write(filepath.Join(next.Root, merchantStoreCapsuleAsset("rollback", kind)), body, 0600)
	}
	runner.oldPath, runner.oldSHA = filepath.Join(old.Root, "capsule.json"), strings.Repeat("e", 64)
	runner.loaded = "Environment=LMM_DB_MIGRATION_MODE=verify\nEnvironmentFiles=" + filepath.Join(paths.ConfigDir, "lmm-api-go.env") + " (ignore_errors=no)\nMainPID=0\nInvocationID=" + runner.invocation + "\nActiveState=inactive\nFragmentPath=" + unitPath + "\nExecStart={ path=" + paths.InstalledBinary + " ; argv[]=" + paths.InstalledBinary + " serve ; ignore_errors=no ; }\nDropInPaths=" + runner.drop + "\n"
	pre := "{ path=" + deployEngineInstalledPath + " ; argv[]=" + deployEngineInstalledPath + " operator production writer-start --capsule " + runner.oldPath + " --capsule-sha256 " + runner.oldSHA + " ; ignore_errors=no ; }"
	runner.loaded += "ExecStartPre=" + pre + "\nExecStartPreEx=" + testPortableStartEx(pre) + "\n"
	write(runner.drop, merchantStorePortableHookBytes(deployEngineInstalledPath, runner.oldPath, runner.oldSHA), 0644)
	seal, err := runtime.captureMerchantStorePortableStartup(ctx, runner.oldPath, runner.oldSHA)
	if err != nil {
		t.Fatal(err)
	}
	old.Writer.StartupSHA256, old.Writer.SignedUnitSHA256 = seal["startup_sha256"], seal["signed_unit_sha256"]
	old.ExistingSchemaContract.StartupSHA256, old.ExistingSchemaContract.SignedUnitSHA256 = old.Writer.StartupSHA256, old.Writer.SignedUnitSHA256
	oldRaw, _ := canonicalMerchantStoreCapsule(old)
	oldSHA := startupContentSHA256(oldRaw)
	runner.loaded = strings.ReplaceAll(runner.loaded, runner.oldSHA, oldSHA)
	runner.oldSHA = oldSHA
	write(runner.oldPath, oldRaw, 0600)
	write(runner.drop, merchantStorePortableHookBytes(deployEngineInstalledPath, runner.oldPath, oldSHA), 0644)
	loaded, err := runtime.loadedExistingSchemaUnit(ctx)
	if err != nil {
		t.Fatal(err)
	}
	commands, err := runtime.merchantStorePortableStartupCommands(loaded, old.Binary, runner.oldPath, oldSHA)
	if err != nil {
		t.Fatal(err)
	}
	runner.newPath = filepath.Join(next.Root, "capsule.json")
	projected, signedUnit, err := runtime.merchantStoreStartupTransitionSnapshot(ctx, loaded, commands, deployEngineInstalledPath, runner.newPath)
	if err != nil {
		t.Fatal(err)
	}
	next.Writer.StartupSHA256, next.Writer.SignedUnitSHA256 = projected, signedUnit
	next.ExistingSchemaContract.StartupSHA256, next.ExistingSchemaContract.SignedUnitSHA256 = projected, signedUnit
	next.Writer.StartupPredecessorPath, next.Writer.StartupPredecessorSHA256, next.Writer.StartupTargetPath = runner.oldPath, oldSHA, runner.newPath
	for i, path := range merchantStoreCapsuleSourcePaths {
		body := []byte("# unsigned isolated fixture source\n")
		next.SourceFiles[i].SHA256 = startupContentSHA256(body)
		if err := os.MkdirAll(filepath.Dir(filepath.Join(next.Root, "source", path)), 0700); err != nil {
			t.Fatal(err)
		}
		write(filepath.Join(next.Root, "source", path), body, 0600)
	}
	nextRaw, _ := canonicalMerchantStoreCapsule(next)
	runner.newSHA = startupContentSHA256(nextRaw)
	write(runner.newPath, nextRaw, 0600)
	if err := validateMerchantStoreCapsule(next, paths); err != nil {
		t.Fatal(err)
	}
	if err := runtime.merchantStoreCapsuleMutableDirectories(next); err != nil {
		t.Fatal(err)
	}
	work := filepath.Join(runtime.portableWorkRoot, next.DeploymentID)
	if err := os.MkdirAll(work, 0700); err != nil {
		t.Fatal(err)
	}
	savedJournal := []byte(strings.TrimSpace(string(runner.journal)))
	proof := merchantStoreOrdinaryStopped{"lmm-ordinary-writer-stopped-v1", next.DeploymentID, old.Candidate.PayloadSHA256, stoppedPID, runner.invocation, startupContentSHA256(savedJournal)}
	proofRaw, _ := json.Marshal(proof)
	write(filepath.Join(work, "ordinary-writer-stopped.json"), proofRaw, 0600)
	stateRaw, _ := json.Marshal(map[string]any{"release": next.DeploymentID, "phase": "MUTATION_PENDING", "sha256": next.Candidate.PayloadSHA256, "previous_sha256": old.Candidate.PayloadSHA256, "deploy_engine_sha256": next.Candidate.DeployEngineSHA256, "migrate": false})
	write(filepath.Join(work, "state.json"), stateRaw, 0644)
	write(filepath.Join(work, "shutdown.log"), savedJournal, 0644)
	lease, err := acquireMerchantStoreDeploymentFence(ctx, input.Values, next.Writer)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	owner := productionMerchantStoreFenceOwner{Format: 1, State: "ACTIVE", Purpose: "portable-deploy", Service: next.Service, DeploymentID: next.DeploymentID, Host: next.Host, PlanSHA256: runner.newSHA, ContractSHA256: merchantStoreFenceContractSHA(next.Writer), ProviderSHA256: next.Writer.Candidate.PayloadSHA256, Nonce: strings.Repeat("f", 32), HolderPID: os.Getpid(), HolderUnit: merchantStorePortableUnit(next.DeploymentID), HolderInvocationID: runner.holderINV, BackendPID: lease.backendPID, SystemIdentifier: next.Writer.SystemIdentifier, Database: next.Writer.Database, DatabaseOID: next.Writer.DatabaseOID, Schema: next.Writer.Schema, SchemaOID: next.Writer.SchemaOID, Role: next.Writer.Role}
	runner.holder = owner.HolderUnit
	if err := lease.claimOwner(ctx, owner, next.Candidate.DeployEngineSHA256, nil); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := lease.ReleaseOwner(context.Background()); err != nil {
			t.Errorf("owned fixture exact parent release: %v", err)
		}
	}()
	ownerRaw, _ := canonicalMerchantStoreFenceOwner(owner)
	write(merchantStorePortableOwnerPath(next, ""), ownerRaw, 0600)
	socket, err := runtime.merchantStoreSocketPath(next.Root, owner, true)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	if err := os.Chmod(socket, 0600); err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			conn, err := listener.AcceptUnix()
			if err != nil {
				return
			}
			runtime.handleMerchantStorePortableConnection(ctx, next, runner.newSHA, "", owner, ownerRaw, lease, conn)
		}
	}()
	for _, drift := range []string{"unsigned installed engine", "changed old hook"} {
		t.Run(drift, func(t *testing.T) {
			oldHook, _ := os.ReadFile(runner.drop)
			if drift == "unsigned installed engine" {
				write(deployEngineInstalledPath, []byte("unbound engine\n"), 0755)
			} else {
				write(runner.drop, append(append([]byte(nil), oldHook...), '#'), 0644)
			}
			beforeEngine, _ := os.ReadFile(deployEngineInstalledPath)
			beforeHook, _ := os.ReadFile(runner.drop)
			if err := runtime.bindMerchantStoreOrdinaryStartup(ctx, next, runner.newSHA); err == nil {
				t.Fatal("accepted drift before authority mutation")
			}
			afterEngine, _ := os.ReadFile(deployEngineInstalledPath)
			afterHook, _ := os.ReadFile(runner.drop)
			if !bytes.Equal(beforeEngine, afterEngine) || !bytes.Equal(beforeHook, afterHook) || runner.reloads != 0 {
				t.Fatal("rejected drift modified installed engine/hook")
			}
			if _, err := os.Lstat(filepath.Join(next.Root, "state", "ordinary-startup-bind-intent.json")); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("rejected prebind drift wrote an intent")
			}
			write(deployEngineInstalledPath, oldEngine, 0755)
			write(runner.drop, oldHook, 0644)
		})
	}
	if err := runtime.bindMerchantStoreOrdinaryStartup(ctx, next, runner.newSHA); err != nil {
		t.Fatal(err)
	}
	if runtime.verifyMerchantStorePortableInstalled(old, old.Candidate.PayloadSHA256) != nil || sha256MustEqual(deployEngineInstalledPath, next.Candidate.DeployEngineSHA256) != nil || runner.reloads != 1 || runner.signatures == 0 {
		t.Fatal("bind did not preserve old API while installing exact new engine/hook")
	}
	intent, err := os.ReadFile(filepath.Join(next.Root, "state", "ordinary-startup-bind-intent.json"))
	bound, boundErr := os.ReadFile(filepath.Join(next.Root, "state", "ordinary-startup-bound.json"))
	var decoded merchantStoreOrdinaryBindIntent
	if err != nil || boundErr != nil || !bytes.Equal(intent, bound) || json.Unmarshal(intent, &decoded) != nil || decoded.OldEngineSHA256 != old.Candidate.DeployEngineSHA256 || decoded.NewEngineSHA256 != next.Candidate.DeployEngineSHA256 || decoded.Stopped != proof {
		t.Fatal("full function did not preserve exact native intent/bound evidence")
	}
	// Simulate the wrapper's later package step with real provider bytes. No
	// start or fake success receipt is manufactured by this file-only fixture.
	write(filepath.Join(filepath.Dir(paths.InstalledBinary), backendGoName), newAPI, 0755)
	if err := runtime.verifyMerchantStorePortableInstalled(next, next.Candidate.PayloadSHA256); err != nil {
		t.Fatal(err)
	}
	finalLoaded, err := runtime.loadedExistingSchemaUnit(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.merchantStoreCapsuleStartup(ctx, next, runner.newSHA, finalLoaded); err != nil {
		t.Fatalf("actual new installed startup seal: %v", err)
	}
	// Run the actual new per-start entry while the original ordinary parent
	// remains live. Only the manager's activating inventory is a fixture;
	// ControlPID and executable are this real process, and parent RPC/PG are
	// native. Reusing it must never create a second start owner or holder.
	startINV := strings.Repeat("9", 32)
	t.Setenv("INVOCATION_ID", startINV)
	runner.loaded = strings.ReplaceAll(strings.ReplaceAll(runner.loaded, runner.invocation, startINV), "ActiveState=inactive", "ActiveState=activating")
	runtime.startupMonotonicUS = func() (uint64, error) { return 1000000, nil }
	if err := runtime.merchantStorePortableStart(ctx, next, runner.newSHA); err != nil {
		t.Fatalf("actual per-start entry did not reuse its original live parent: %v", err)
	}
	if _, err := os.Lstat(merchantStorePortableOwnerPath(next, startINV)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("ordinary parent reuse created a second per-start owner")
	}
	if err := lease.CheckOwner(ctx); err != nil {
		t.Fatal("bind lost its actual PostgreSQL parent")
	}
	t.Log("actual_postgres_parent=true actual_native_bind=true old_api_preserved_during_bind=true new_engine_and_hook_installed=true installed_new_api_actual_seal=true native_per_start_original_parent_reuse=true changed_engine_and_hook_rejected=true sigstore_and_systemd=fixture_only")
}
