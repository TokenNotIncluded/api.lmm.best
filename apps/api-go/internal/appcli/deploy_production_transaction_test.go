package appcli

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

type fakeProductionRunner struct {
	t *testing.T

	goCandidate, goRollback                                     string
	webCandidate, webRollback                                   string
	probeBinary, installedBinary                                string
	frontendRoot                                                string
	oldWebIndex, newWebIndex                                    string
	oldVersion, newVersion                                      string
	oldRevision, newRevision                                    string
	contractRevision, webContractRevision                       string
	operatorUID                                                 string
	installedGoVersion, installedWebVersion                     string
	installedGoRevision, installedWebRevision                   string
	goRevisionFile, webRevisionFile                             string
	goContractFile, webContractFile                             string
	serviceActive, timerActive                                  bool
	migrationFailure, rollbackMigrationFailure, failTimerEnable bool
	commands                                                    []productionCommand
}

func (runner *fakeProductionRunner) Run(_ context.Context, command productionCommand) ([]byte, error) {
	runner.commands = append(runner.commands, command)
	if command.Name == runner.probeBinary || command.Name == runner.installedBinary {
		if len(command.Args) == 0 {
			return nil, errors.New("missing native CLI command")
		}
		switch command.Args[0] {
		case "version":
			if command.Name == runner.probeBinary {
				return []byte(runner.newVersion + "\n"), nil
			}
			return []byte(runner.installedGoVersion + "\n"), nil
		case "migrate":
			if runner.migrationFailure && command.Name == runner.probeBinary && command.Args[1] == "--apply" {
				return nil, errors.New("injected migration failure")
			}
			if runner.rollbackMigrationFailure && command.Name == runner.installedBinary {
				return nil, errors.New("injected rollback compatibility failure")
			}
			return nil, nil
		case "request":
			return runner.nativeRequest(command.Args)
		}
	}
	switch command.Name {
	case "/usr/bin/id":
		if len(command.Args) != 2 {
			return nil, errors.New("bad id")
		}
		if command.Args[0] == "-u" {
			uid := runner.operatorUID
			if uid == "" {
				uid = "1000"
			}
			return []byte(uid + "\n"), nil
		}
		return []byte(strconv.Itoa(os.Getgid()) + "\n"), nil
	case "/usr/bin/runuser":
		return runner.runuser(command.Args)
	}
	switch filepath.Base(command.Name) {
	case "bsdtar":
		return runner.bsdtar(command.Args)
	case "pg_restore":
		return []byte("archive ok\n"), nil
	case "pg_dump":
		for _, arg := range command.Args {
			if strings.HasPrefix(arg, "--file=") {
				return nil, os.WriteFile(strings.TrimPrefix(arg, "--file="), []byte("postgresql-custom-backup"), 0o600)
			}
		}
		return nil, errors.New("pg_dump output is missing")
	case "psql":
		if strings.Contains(strings.Join(command.Args, " "), "current_schema") {
			return []byte("public\n"), nil
		}
		return []byte("abcdefghijklmnopqrstuvwxyz123456\n"), nil
	case "vercmp":
		return []byte("-1\n"), nil
	case "pacman":
		return runner.pacman(command.Args)
	case "systemctl":
		return runner.systemctl(command.Args)
	case "journalctl":
		return nil, nil
	case "age":
		output, decrypt := "", false
		for index, arg := range command.Args {
			if arg == "--decrypt" {
				decrypt = true
			}
			if arg == "--output" && index+1 < len(command.Args) {
				output = command.Args[index+1]
			}
		}
		content, err := os.ReadFile(command.Args[len(command.Args)-1])
		if err != nil {
			return nil, err
		}
		if decrypt {
			content = bytes.TrimPrefix(content, []byte("AGE-ENCRYPTED\n"))
		} else {
			content = append([]byte("AGE-ENCRYPTED\n"), content...)
		}
		return nil, os.WriteFile(output, content, 0o600)
	default:
		return nil, fmt.Errorf("unexpected command: %s %s", command.Name, strings.Join(command.Args, " "))
	}
}

func (runner *fakeProductionRunner) nativeRequest(args []string) ([]byte, error) {
	value := func(flag string) string {
		for i := range args {
			if args[i] == flag && i+1 < len(args) {
				return args[i+1]
			}
		}
		return ""
	}
	switch value("--path") {
	case "/api/status":
		return []byte(fmt.Sprintf(`{"success":true,"ready":true,"data":{"version":%q}}`, runner.installedGoVersion)), nil
	case "/api/livez":
		if !runner.serviceActive {
			return nil, errors.New("service stopped")
		}
		return []byte(`{"success":true,"live":true}`), nil
	case "/v1/models":
		return []byte(`{"data":[]}`), nil
	case "/":
		return os.ReadFile(filepath.Join(runner.frontendRoot, "current", "index.html"))
	default:
		return nil, errors.New("unexpected request path")
	}
}

func (runner *fakeProductionRunner) packageData(path string) (name, version, revision, contract, index string, ok bool) {
	switch path {
	case runner.goCandidate:
		return productionAURPackageName, runner.newVersion + "-1", runner.newRevision, runner.contractRevision, "", true
	case runner.goRollback:
		return productionAURPackageName, runner.oldVersion + "-1", runner.oldRevision, runner.contractRevision, "", true
	case runner.webCandidate:
		contract := runner.webContractRevision
		if contract == "" {
			contract = runner.contractRevision
		}
		return productionWebPackageName, runner.newVersion + "-1", runner.newRevision, contract, runner.newWebIndex, true
	case runner.webRollback:
		contract := runner.webContractRevision
		if contract == "" {
			contract = runner.contractRevision
		}
		return productionWebPackageName, runner.oldVersion + "-1", runner.oldRevision, contract, runner.oldWebIndex, true
	default:
		return "", "", "", "", "", false
	}
}

func (runner *fakeProductionRunner) bsdtar(args []string) ([]byte, error) {
	if len(args) != 3 || args[0] != "-xOf" {
		return nil, fmt.Errorf("unexpected bsdtar arguments: %v", args)
	}
	name, _, revision, contract, index, ok := runner.packageData(args[1])
	if !ok {
		return nil, errors.New("unknown archive")
	}
	member := args[2]
	switch {
	case strings.HasSuffix(member, "/REVISION"):
		return []byte(revision + "\n"), nil
	case strings.HasSuffix(member, "/API_CONTRACT_REVISION"), strings.HasSuffix(member, "/ROUTE_CONTRACT_REVISION"):
		return []byte(contract + "\n"), nil
	case name == productionWebPackageName && strings.HasSuffix(member, "/index.html"):
		return os.ReadFile(index)
	case name == productionAURPackageName && member == "usr/bin/lmm-api":
		if args[1] == runner.goCandidate {
			return os.ReadFile(runner.probeBinary)
		}
		return []byte("rollback-binary"), nil
	default:
		return nil, errors.New("unknown member")
	}
}

func (runner *fakeProductionRunner) pacman(args []string) ([]byte, error) {
	if len(args) < 2 {
		return nil, errors.New("invalid pacman arguments")
	}
	switch args[0] {
	case "-Q":
		switch args[1] {
		case productionAURPackageName:
			return []byte(productionAURPackageName + " " + runner.installedGoVersion + "-1\n"), nil
		case productionWebPackageName:
			return []byte(productionWebPackageName + " " + runner.installedWebVersion + "-1\n"), nil
		default:
			return nil, errors.New("package not found")
		}
	case "-Qp":
		name, version, _, _, _, ok := runner.packageData(args[1])
		if !ok {
			return nil, errors.New("unknown package")
		}
		return []byte(name + " " + version + "\n"), nil
	case "-Qkk":
		return []byte(args[1] + ": 42 total files, 0 altered files\n"), nil
	case "-Qo":
		if args[1] == productionOperatorBinary {
			return []byte(productionOperatorBinary + " is owned by " + productionOperatorPackageName + " 1.0.0-1\n"), nil
		}
		return []byte(args[1] + " is owned by " + productionAURPackageName + " " + runner.installedGoVersion + "-1\n"), nil
	case "-Qi":
		return []byte("Name : " + productionAURPackageName + "\nVersion : " + runner.installedGoVersion + "-1\n"), nil
	case "-U":
		return nil, errors.New("direct pacman -U is forbidden")
	}
	return nil, fmt.Errorf("unexpected pacman arguments: %v", args)
}

func (runner *fakeProductionRunner) runuser(args []string) ([]byte, error) {
	if len(args) < 7 || args[0] != "--user" || args[2] != "--" || args[3] != "/usr/bin/paru" || args[4] != "-U" || args[5] != "--noconfirm" {
		return nil, fmt.Errorf("unsafe runuser invocation: %v", args)
	}
	for _, path := range args[6:] {
		name, version, revision, _, _, ok := runner.packageData(path)
		if !ok {
			return nil, errors.New("unknown paru package")
		}
		version = strings.TrimSuffix(version, "-1")
		switch name {
		case productionAURPackageName:
			runner.installedGoVersion, runner.installedGoRevision = version, revision
			if err := os.WriteFile(runner.goRevisionFile, []byte(revision+"\n"), 0o644); err != nil {
				return nil, err
			}
		case productionWebPackageName:
			runner.installedWebVersion, runner.installedWebRevision = version, revision
			if err := os.WriteFile(runner.webRevisionFile, []byte(revision+"\n"), 0o644); err != nil {
				return nil, err
			}
			release := version + "-1.g" + revision[:12]
			if err := executeFrontendDeploy(frontendDeployOptions{Action: "rollback", Root: runner.frontendRoot, Release: release, Keep: 3}); err != nil {
				return nil, err
			}
		}
	}
	return nil, nil
}

func (runner *fakeProductionRunner) systemctl(args []string) ([]byte, error) {
	if len(args) == 0 {
		return nil, errors.New("missing systemctl action")
	}
	switch args[0] {
	case "is-active":
		unit := args[len(args)-1]
		if strings.HasSuffix(unit, ".timer") {
			if runner.timerActive {
				return []byte("active\n"), nil
			}
			return nil, errors.New("inactive")
		}
		if strings.Contains(unit, "rollback-") {
			return nil, errors.New("inactive")
		}
		if runner.serviceActive {
			return []byte("active\n"), nil
		}
		return nil, errors.New("inactive")
	case "is-enabled":
		return []byte("enabled\n"), nil
	case "stop":
		if !strings.Contains(args[len(args)-1], "rollback-") {
			runner.serviceActive = false
		}
		return nil, nil
	case "enable":
		if strings.HasSuffix(args[len(args)-1], ".timer") {
			runner.timerActive = true
			if runner.failTimerEnable {
				return nil, errors.New("injected ambiguous timer enable failure")
			}
		} else {
			runner.serviceActive = true
		}
		return nil, nil
	case "disable":
		runner.timerActive = false
		return nil, nil
	case "daemon-reload", "reset-failed":
		return nil, nil
	case "show":
		property := ""
		for _, arg := range args {
			if strings.HasPrefix(arg, "--property=") {
				property = strings.TrimPrefix(arg, "--property=")
			}
		}
		switch property {
		case "NRestarts":
			return []byte("0\n"), nil
		case "MemoryCurrent":
			return []byte("67108864\n"), nil
		case "MemoryHigh":
			return []byte("335544320\n"), nil
		case "MemoryMax":
			return []byte("402653184\n"), nil
		case "MemorySwapMax":
			return []byte("268435456\n"), nil
		}
		return []byte("LoadState=loaded\nActiveState=active\nSubState=running\nUnitFileState=enabled\n"), nil
	}
	return nil, fmt.Errorf("unexpected systemctl args: %v", args)
}

type productionFixture struct {
	runtime     *productionRuntime
	runner      *fakeProductionRunner
	workspace   productionWorkspace
	options     productionTransactionOptions
	environment []byte
	clock       *time.Time
}

func newProductionFixture(t *testing.T) productionFixture {
	t.Helper()
	root := t.TempDir()
	paths := defaultProductionPaths()
	paths.WorkRoot = filepath.Join(root, "work")
	paths.BackupRoot = filepath.Join(root, "backups")
	paths.GlobalLock = filepath.Join(root, "run", "deploy.lock")
	paths.TransactionLock = filepath.Join(root, "transaction.lock")
	paths.FrontendRoot = filepath.Join(root, "frontend")
	paths.EdgeAssetRoot = ""
	paths.SystemdUnitRoot = filepath.Join(root, "systemd-units")
	paths.ConfigDir = filepath.Join(root, "etc", "lmm-api-go")
	paths.DropInDir = filepath.Join(root, "etc", "systemd", "lmm-api.service.d")
	paths.PackagedDropInDir = filepath.Join(root, "usr", "lib", "systemd", "lmm-api.service.d")
	paths.InstalledBinary = filepath.Join(root, "usr", "bin", "lmm-api")
	paths.GoRevisionFile = filepath.Join(root, "usr", "share", "doc", "lmm-api-go-bin", "REVISION")
	paths.GoContractFile = filepath.Join(root, "usr", "share", "doc", "lmm-api-go-bin", "API_CONTRACT_REVISION")
	paths.WebRevisionFile = filepath.Join(root, "usr", "share", "doc", "lmm-api-web-bin", "REVISION")
	paths.WebContractFile = filepath.Join(root, "usr", "share", "doc", "lmm-api-web-bin", "ROUTE_CONTRACT_REVISION")
	paths.ReleasePackages = filepath.Join(root, "release-packages")
	paths.PackageCache = filepath.Join(root, "cache")
	paths.RemovedPaths = []string{filepath.Join(root, "removed")}
	paths.PublicBaseURL = "https://api.lmm.best"
	paths.LocalBaseURL = "http://127.0.0.1:3000"
	paths.JournalUnits = []string{productionServiceName, "nginx.service"}
	for _, dir := range []string{paths.WorkRoot, paths.BackupRoot, filepath.Dir(paths.GlobalLock), paths.SystemdUnitRoot, paths.ConfigDir, paths.DropInDir, paths.PackagedDropInDir, filepath.Dir(paths.InstalledBinary), filepath.Dir(paths.GoRevisionFile), filepath.Dir(paths.WebRevisionFile)} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(paths.BackupRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(paths.PackagedDropInDir, productionMemoryFileName), productionMemoryConfig(), 0o644); err != nil {
		t.Fatal(err)
	}
	oldVersion, newVersion := "0.1.0.r282.g546910cef", "0.1.1.r300.gabcdef123"
	oldRevision := strings.Repeat("1", 40)
	newRevision := strings.Repeat("2", 40)
	contract := "contract-2026-08"
	if err := os.WriteFile(paths.GoRevisionFile, []byte(oldRevision+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.GoContractFile, []byte(contract+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.WebRevisionFile, []byte(oldRevision+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.WebContractFile, []byte(contract+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	id := "go-test-20260810T000000Z"
	workspaceRoot := filepath.Join(paths.WorkRoot, id)
	staging := filepath.Join(workspaceRoot, "staging")
	if err := os.MkdirAll(staging, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspaceRoot, productionWorkspaceMarker), []byte("format=1\ndeployment_id="+id+"\nrole=target\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(paths.TransactionLock, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(paths.TransactionLock, productionTransactionMarker), []byte("format=1\ndeployment_id="+id+"\nstatus=ACTIVE\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	goCandidate := filepath.Join(staging, "lmm-api-go-bin-new.pkg.tar.zst")
	goRollback := filepath.Join(staging, "lmm-api-go-bin-old.pkg.tar.zst")
	webCandidate := filepath.Join(staging, "lmm-api-web-bin-new.pkg.tar.zst")
	webRollback := filepath.Join(staging, "lmm-api-web-bin-old.pkg.tar.zst")
	probe := filepath.Join(staging, "lmm-api-go")
	for path, body := range map[string]string{goCandidate: "go-new", goRollback: "go-old", webCandidate: "web-new", webRollback: "web-old", probe: "probe"} {
		if err := os.WriteFile(path, []byte(body), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	environment := []byte("SQL_DSN=postgres://user:password@127.0.0.1/lmm\nSESSION_COOKIE_SECURE=false\n")
	if err := os.WriteFile(filepath.Join(paths.ConfigDir, "lmm-api-go.env"), environment, 0o600); err != nil {
		t.Fatal(err)
	}
	oldFrontend := writeFrontendFixture(t, root, "old.111.js", "old")
	newFrontend := writeFrontendFixture(t, root, "new.222.js", "new")
	oldRelease := oldVersion + "-1.g" + oldRevision[:12]
	newRelease := newVersion + "-1.g" + newRevision[:12]
	if err := executeFrontendDeploy(frontendDeployOptions{Action: "publish", Root: paths.FrontendRoot, Source: oldFrontend, Release: oldRelease, Keep: 3}); err != nil {
		t.Fatal(err)
	}
	if err := executeFrontendDeploy(frontendDeployOptions{Action: "publish", Root: paths.FrontendRoot, Source: newFrontend, Release: newRelease, Keep: 3}); err != nil {
		t.Fatal(err)
	}
	if err := executeFrontendDeploy(frontendDeployOptions{Action: "rollback", Root: paths.FrontendRoot, Release: oldRelease, Keep: 3}); err != nil {
		t.Fatal(err)
	}
	backupDir := filepath.Join(paths.BackupRoot, id)
	if err := writeTestBackupSet(backupDir, environment); err != nil {
		t.Fatal(err)
	}
	runner := &fakeProductionRunner{t: t, goCandidate: goCandidate, goRollback: goRollback, webCandidate: webCandidate, webRollback: webRollback, probeBinary: probe, installedBinary: paths.InstalledBinary, frontendRoot: paths.FrontendRoot, oldWebIndex: filepath.Join(oldFrontend, "index.html"), newWebIndex: filepath.Join(newFrontend, "index.html"), oldVersion: oldVersion, newVersion: newVersion, oldRevision: oldRevision, newRevision: newRevision, contractRevision: contract, installedGoVersion: oldVersion, installedWebVersion: oldVersion, installedGoRevision: oldRevision, installedWebRevision: oldRevision, goRevisionFile: paths.GoRevisionFile, webRevisionFile: paths.WebRevisionFile, goContractFile: paths.GoContractFile, webContractFile: paths.WebContractFile, serviceActive: true}
	clockValue := time.Date(2026, 8, 10, 1, 0, 0, 0, time.UTC)
	runtime := &productionRuntime{paths: paths, runner: runner, now: func() time.Time { return clockValue }, sleep: func(d time.Duration) { clockValue = clockValue.Add(d) }, effectiveUID: func() int { return 0 }, hostname: func() (string, error) { return productionExpectedHost, nil }, probeAttempts: 1, requiredOwnerUID: uint32(os.Getuid())}
	workspace, err := runtime.openWorkspace(workspaceRoot)
	if err != nil {
		t.Fatal(err)
	}
	options := productionTransactionOptions{Action: "apply", Workspace: workspaceRoot, OperatorUser: "deploy", GoPackage: goCandidate, GoPackageSHA256: mustHashFile(t, goCandidate), GoRollbackPackage: goRollback, GoRollbackSHA256: mustHashFile(t, goRollback), WebPackage: webCandidate, WebPackageSHA256: mustHashFile(t, webCandidate), WebRollbackPackage: webRollback, WebRollbackSHA256: mustHashFile(t, webRollback), GoChanged: true, WebChanged: true, ProbeBinary: probe, ProbeBinarySHA256: mustHashFile(t, probe), ExpectedVersion: newVersion, BackupDir: backupDir, RollbackWindow: 10 * time.Minute, ObservationWindow: 2 * time.Minute, ManualConfirm: true}
	return productionFixture{runtime: runtime, runner: runner, workspace: workspace, options: options, environment: environment, clock: &clockValue}
}

func writeTestBackupSet(root string, environment []byte) error {
	if err := os.MkdirAll(root, 0o700); err != nil {
		return err
	}
	for _, name := range []string{"application.archive", "frontend.archive", "database.archive", "rollback.package"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(name), 0o600); err != nil {
			return err
		}
	}
	var archive bytes.Buffer
	writer := tar.NewWriter(&archive)
	if err := writer.WriteHeader(&tar.Header{Name: "lmm-api-go/", Typeflag: tar.TypeDir, Mode: 0o700}); err != nil {
		return err
	}
	if err := writer.WriteHeader(&tar.Header{Name: "lmm-api-go/lmm-api-go.env", Typeflag: tar.TypeReg, Mode: 0o600, Size: int64(len(environment))}); err != nil {
		return err
	}
	if _, err := writer.Write(environment); err != nil {
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(root, "configuration.archive"), archive.Bytes(), 0o600); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(root, "manifest.env"), []byte("format=1\n"), 0o600); err != nil {
		return err
	}
	attestation, err := json.Marshal(productionBackupAttestation{Format: 1, DeploymentID: filepath.Base(root), ControllerDigest: strings.Repeat("c", 64), OffhostDigest: strings.Repeat("d", 64), VerifiedUTC: time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC)})
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(root, productionBackupAttestationFilename), append(attestation, '\n'), 0o600); err != nil {
		return err
	}
	var sums strings.Builder
	for _, name := range []string{"application.archive", "frontend.archive", "configuration.archive", "database.archive", "rollback.package"} {
		digest, err := sha256File(filepath.Join(root, name))
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintf(&sums, "%s  %s\n", digest, name)
	}
	return os.WriteFile(filepath.Join(root, "SHA256SUMS"), []byte(sums.String()), 0o600)
}

func mustHashFile(t *testing.T, path string) string {
	t.Helper()
	digest, err := sha256File(path)
	if err != nil {
		t.Fatal(err)
	}
	return digest
}

func TestProductionDualPackageApplyUsesParuAndCanonicalWatchdog(t *testing.T) {
	fixture := newProductionFixture(t)
	status, err := fixture.runtime.apply(context.Background(), fixture.workspace, fixture.options)
	if err != nil {
		t.Fatal(err)
	}
	if status.Phase != "AWAITING_CONFIRMATION" {
		t.Fatalf("status=%#v", status)
	}
	manifest, err := fixture.runtime.readManifest(fixture.workspace)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Format != productionTransactionFormat || manifest.OperatorUser != "deploy" || !manifest.Go.Changed || !manifest.Web.Changed ||
		manifest.Go.CandidatePackageName != productionAURPackageName || manifest.Go.RollbackPackageName != productionAURPackageName ||
		manifest.Web.CandidatePackageName != productionWebPackageName || manifest.Web.RollbackPackageName != productionWebPackageName ||
		manifest.Go.CandidateContractRevision != manifest.Web.CandidateContractRevision {
		t.Fatalf("manifest=%#v", manifest)
	}
	seenParu := false
	for _, command := range fixture.runner.commands {
		if command.Name == "pacman" && len(command.Args) > 0 && command.Args[0] == "-U" {
			t.Fatalf("direct pacman -U used: %#v", command)
		}
		if command.Name == fixture.runtime.paths.RunuserBinary {
			seenParu = true
			if got := strings.Join(command.Args, " "); !strings.Contains(got, "-- /usr/bin/paru -U --noconfirm") || !strings.Contains(got, fixture.options.GoPackage) || !strings.Contains(got, fixture.options.WebPackage) {
				t.Fatalf("paru invocation=%q", got)
			}
		}
	}
	if !seenParu {
		t.Fatal("missing paru install")
	}
	for path, wantMode := range map[string]os.FileMode{
		filepath.Dir(fixture.runtime.paths.WorkRoot):                     0o710,
		fixture.workspace.root:                                           0o710,
		fixture.workspace.stagingDir:                                     0o750,
		filepath.Join(fixture.workspace.root, productionWorkspaceMarker): 0o640,
		fixture.workspace.stateDir:                                       0o700,
		fixture.options.GoPackage:                                        0o640,
	} {
		info, err := os.Lstat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != wantMode || info.Mode().Perm()&0o022 != 0 {
			t.Fatalf("operator workspace %s mode=%v want=%v", path, info.Mode().Perm(), wantMode)
		}
	}
	unit, err := os.ReadFile(fixture.workspace.rollbackPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(unit), "ExecStart=/usr/bin/lmm-api-deploy deploy production rollback") || strings.Contains(string(unit), fixture.options.ProbeBinary) {
		t.Fatalf("rollback unit=%s", unit)
	}
}

func TestProductionRollbackRestoresBothPackagesAndFrontend(t *testing.T) {
	fixture := newProductionFixture(t)
	if _, err := fixture.runtime.apply(context.Background(), fixture.workspace, fixture.options); err != nil {
		t.Fatal(err)
	}
	status, err := fixture.runtime.rollback(context.Background(), fixture.workspace, "test")
	if err != nil {
		t.Fatal(err)
	}
	if status.Phase != "ROLLED_BACK" || fixture.runner.installedGoVersion != fixture.runner.oldVersion || fixture.runner.installedWebVersion != fixture.runner.oldVersion {
		t.Fatalf("status=%#v", status)
	}
	manifest, _ := fixture.runtime.readManifest(fixture.workspace)
	if err := verifyFrontendIdentity(fixture.runtime.paths.FrontendRoot, manifest.Frontend.OldTarget, manifest.Frontend.OldIndexSHA256); err != nil {
		t.Fatal(err)
	}
}

func TestProductionAutoConfirmObservesForAtLeastTwoMinutes(t *testing.T) {
	fixture := newProductionFixture(t)
	fixture.options.ManualConfirm = false
	confirmed, err := fixture.runtime.apply(context.Background(), fixture.workspace, fixture.options)
	if err != nil {
		t.Fatal(err)
	}
	if confirmed.Phase != "CONFIRMED" || fixture.runner.timerActive {
		t.Fatalf("auto-confirm=%#v timer=%v", confirmed, fixture.runner.timerActive)
	}
}

func TestProductionConfirmRequiresObservationAndExactIdentities(t *testing.T) {
	fixture := newProductionFixture(t)
	if _, err := fixture.runtime.apply(context.Background(), fixture.workspace, fixture.options); err != nil {
		t.Fatal(err)
	}
	manifest, _ := fixture.runtime.readManifest(fixture.workspace)
	if _, err := fixture.runtime.confirmLoaded(context.Background(), fixture.workspace, manifest); err == nil || !strings.Contains(err.Error(), "120 seconds") {
		t.Fatalf("early confirm error=%v", err)
	}
	fixture.runtime.now = func() time.Time { return manifest.ObservationStartedUTC.Add(3 * time.Minute) }
	confirmed, err := fixture.runtime.confirmLoaded(context.Background(), fixture.workspace, manifest)
	if err != nil {
		t.Fatal(err)
	}
	if confirmed.Phase != "CONFIRMED" || fixture.runner.timerActive {
		t.Fatalf("confirmed=%#v timer=%v", confirmed, fixture.runner.timerActive)
	}
}

func TestProductionManifestTamperKeepsRollbackArmed(t *testing.T) {
	fixture := newProductionFixture(t)
	if _, err := fixture.runtime.apply(context.Background(), fixture.workspace, fixture.options); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(fixture.workspace.manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	raw = bytes.Replace(raw, []byte(fixture.options.GoPackageSHA256), []byte(strings.Repeat("0", 64)), 1)
	if err := os.WriteFile(fixture.workspace.manifestPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.runtime.executeTransaction(context.Background(), productionTransactionOptions{Action: "confirm", Workspace: fixture.workspace.root}); err == nil {
		t.Fatal("tampered manifest accepted")
	}
	if !fixture.runner.timerActive {
		t.Fatal("tamper disarmed rollback timer")
	}
}

func TestProductionWorkspaceRejectsWritablePayload(t *testing.T) {
	fixture := newProductionFixture(t)
	if err := os.Chmod(fixture.options.WebPackage, 0o662); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.runtime.apply(context.Background(), fixture.workspace, fixture.options); err == nil || !strings.Contains(err.Error(), "writable") {
		t.Fatalf("unsafe workspace error=%v", err)
	}
}

func TestProductionRejectsRootOperatorBeforeWatchdog(t *testing.T) {
	fixture := newProductionFixture(t)
	fixture.runner.operatorUID = "0"
	if _, err := fixture.runtime.apply(context.Background(), fixture.workspace, fixture.options); err == nil || !strings.Contains(err.Error(), "uid greater than zero") {
		t.Fatalf("root operator error=%v", err)
	}
	if fixture.runner.timerActive {
		t.Fatal("root operator armed watchdog")
	}
}

func TestProductionRejectsContractMismatchBeforeWatchdog(t *testing.T) {
	fixture := newProductionFixture(t)
	fixture.runner.webContractRevision = "other-contract"
	if _, err := fixture.runtime.apply(context.Background(), fixture.workspace, fixture.options); err == nil || !strings.Contains(err.Error(), "contract revisions differ") {
		t.Fatalf("contract mismatch error=%v", err)
	}
	if fixture.runner.timerActive {
		t.Fatal("contract mismatch armed watchdog")
	}
}

func TestPackageIntegritySummaryIsExact(t *testing.T) {
	name := productionAURPackageName
	for _, test := range []struct {
		output string
		clean  bool
	}{{name + ": 42 total files, 0 altered files\n", true}, {name + ": 42 total files, 10 altered files\n", false}, {"", false}} {
		if got := packageIntegrityClean([]byte(test.output), name); got != test.clean {
			t.Fatalf("clean=%v for %q", got, test.output)
		}
	}
}
