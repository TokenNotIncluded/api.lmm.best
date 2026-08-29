//go:build !windows

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
	"slices"
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
	operatorUID, operatorGID, probeVersion                      string
	installedGoVersion, installedWebVersion                     string
	installedGoRevision, installedWebRevision                   string
	goRevisionFile, webRevisionFile                             string
	goContractFile, webContractFile                             string
	serviceActive, timerActive, rollbackServiceActive           bool
	migrationFailure, rollbackMigrationFailure, failTimerEnable bool
	invalidCandidateEdgePolicy, alteredCandidatePackage         bool
	sudoFailure, restartOnEnable, restartOnWebInstall           bool
	restartOnRequestAfterBaseline, restartBaselineRead          bool
	cancelOnStop                                                context.CancelFunc
	timerDeadline, timerLastTrigger                             time.Time
	restartCounter                                              int64
	onlineWriteCount                                            int
	onCandidateApply                                            func()
	commands                                                    []productionCommand
	events                                                      []string
}

func (runner *fakeProductionRunner) Run(ctx context.Context, command productionCommand) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	runner.commands = append(runner.commands, command)
	if command.Name == runner.probeBinary || command.Name == runner.installedBinary {
		return runner.runNativeBinary(command.Name, command.Args)
	}
	switch command.Name {
	case commandID:
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
		gid := runner.operatorGID
		if gid == "" {
			gid = strconv.Itoa(os.Getgid())
		}
		return []byte(gid + "\n"), nil
	case commandRunuser:
		return runner.runuser(command.Args)
	}
	switch filepath.Base(command.Name) {
	case "bsdtar":
		return runner.bsdtar(command.Args)
	case "pg_restore":
		if len(command.Args) == 2 && command.Args[0] == "--list" {
			return []byte("archive ok\n"), nil
		}
		return nil, errors.New("database restoration is forbidden in automatic deployment flow")
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

func (runner *fakeProductionRunner) runNativeBinary(binary string, args []string) ([]byte, error) {
	if len(args) == 0 {
		return nil, errors.New("missing native CLI command")
	}
	switch args[0] {
	case "version":
		if binary == runner.probeBinary {
			version := runner.probeVersion
			if version == "" {
				version = runner.newVersion
			}
			return []byte(version + "\n"), nil
		}
		return []byte(runner.installedGoVersion + "\n"), nil
	case "migrate":
		runner.events = append(runner.events, "migrate:"+args[1])
		if binary == runner.probeBinary && args[1] == "--apply" && runner.onCandidateApply != nil {
			runner.onCandidateApply()
		}
		if runner.migrationFailure && binary == runner.probeBinary && args[1] == "--apply" {
			return nil, errors.New("injected migration failure")
		}
		if binary == runner.probeBinary && args[1] == "--apply" {
			runner.onlineWriteCount++
		}
		if runner.rollbackMigrationFailure && binary == runner.installedBinary {
			return nil, errors.New("injected rollback compatibility failure")
		}
		return nil, nil
	case "request":
		return runner.nativeRequest(args)
	}
	return nil, errors.New("unexpected native CLI command")
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
	requestPath := value("--path")
	runner.events = append(runner.events, "request:"+requestPath)
	if runner.restartOnRequestAfterBaseline && runner.restartBaselineRead {
		runner.restartCounter++
		runner.restartOnRequestAfterBaseline = false
	}
	switch requestPath {
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
	name, version, revision, contract, index, ok := runner.packageData(args[1])
	if !ok {
		return nil, errors.New("unknown archive")
	}
	member := args[2]
	if strings.Contains(member, "usr/share/lmm-api-go/edge-policy/") {
		if name != productionAURPackageName {
			return nil, errors.New("edge-policy member requested from non-Go package")
		}
		if runner.invalidCandidateEdgePolicy && version == runner.newVersion+"-1" && strings.HasSuffix(member, "/nginx/lmm-api-locations.conf") {
			return []byte("invalid candidate edge policy\n"), nil
		}
		switch {
		case strings.HasSuffix(member, "/nginx/http-map.conf"):
			return []byte("geoip2 /var/lib/geoip2/DBIP-Country-Lite.mmdb {\n}\n"), nil
		case strings.HasSuffix(member, "/nginx/new-api.conf"):
			return []byte("include /etc/nginx/lmm-api-region-policy.conf;\n"), nil
		case strings.HasSuffix(member, "/nginx/lmm-api-locations.conf"):
			return []byte("error_page 418 = @lmm_api_cors_preflight;\nlocation @lmm_api_cors_preflight {\nauth_request off;\n}\nset $lmm_access_policy_original_uri $uri;\nif ($request_method = OPTIONS) { return 418; }\nadd_header Access-Control-Allow-Methods $http_access_control_request_method always;\nadd_header Access-Control-Allow-Headers $http_access_control_request_headers always;\nadd_header Vary \"Origin, Access-Control-Request-Method, Access-Control-Request-Headers\" always;\n"), nil
		case strings.HasSuffix(member, "/nginx/lmm-api-region-policy.conf"):
			return []byte("auth_request /internal/access-ip-policy;\nproxy_set_header X-LMM-Original-URI $lmm_access_policy_original_uri;\nproxy_set_header X-LMM-Original-Accept $http_accept;\n"), nil
		default:
			return []byte("fixture edge-policy asset\n"), nil
		}
	}
	switch {
	case strings.HasSuffix(member, "/REVISION"):
		return []byte(revision + "\n"), nil
	case strings.HasSuffix(member, "/API_ROUTE_CONTRACT_REVISION"):
		return []byte(contract + "\n"), nil
	case name == productionWebPackageName && strings.HasSuffix(member, "/index.html"):
		return os.ReadFile(index)
	case name == productionAURPackageName && member == "usr/bin/lmm-api-go":
		return os.ReadFile(runner.probeBinary)
	default:
		return nil, errors.New("unknown member")
	}
}

func (runner *fakeProductionRunner) pacman(args []string) ([]byte, error) {
	if len(args) == 0 {
		return nil, errors.New("invalid pacman arguments")
	}
	switch args[0] {
	case "-Q":
		if len(args) != 2 {
			return nil, errors.New("invalid pacman query arguments")
		}
		switch args[1] {
		case productionAURPackageName:
			return []byte(productionAURPackageName + " " + runner.installedGoVersion + "-1\n"), nil
		case productionWebPackageName:
			return []byte(productionWebPackageName + " " + runner.installedWebVersion + "-1\n"), nil
		default:
			return nil, errors.New("package not found")
		}
	case "-Qq":
		if len(args) != 1 {
			return nil, errors.New("invalid pacman package-list arguments")
		}
		return []byte(productionAURPackageName + "\n" + productionWebPackageName + "\n" + productionOperatorPackageName + "\n"), nil
	case "-Qqo":
		if len(args) != 3 || args[1] != "--" {
			return nil, errors.New("invalid pacman owner arguments")
		}
		return []byte(productionAURPackageName + "\n"), nil
	case "-Qp":
		name, version, _, _, _, ok := runner.packageData(args[1])
		if !ok {
			return nil, errors.New("unknown package")
		}
		return []byte(name + " " + version + "\n"), nil
	case "-Qkk":
		if runner.alteredCandidatePackage && args[1] == productionAURPackageName && runner.installedGoVersion == runner.newVersion {
			return []byte(args[1] + ": 42 total files, 1 altered file\n"), nil
		}
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
	if len(args) < 4 || args[0] != "--user" || args[2] != "--" {
		return nil, fmt.Errorf("unsafe runuser invocation: %v", args)
	}
	if args[1] == "root" {
		binary := args[3]
		if binary != runner.probeBinary && binary != runner.installedBinary {
			return nil, fmt.Errorf("unverified native binary: %s", binary)
		}
		return runner.runNativeBinary(binary, args[4:])
	}
	if args[1] != productionOperatorUser {
		return nil, errors.New("unexpected operator user")
	}
	if len(args) == 12 && args[3] == commandSudo && slices.Equal(args[4:11], []string{"-n", "-l", "--", commandPacman, "--upgrade", "--noconfirm", "--"}) {
		runner.events = append(runner.events, "sudo-preflight:"+filepath.Base(args[11]))
		if runner.sudoFailure {
			return nil, errors.New("sudo policy denied")
		}
		if _, _, _, _, _, ok := runner.packageData(args[11]); !ok {
			return nil, errors.New("sudo policy rejected non-package path")
		}
		return []byte(commandPacman + " --upgrade --noconfirm -- " + args[11] + "\n"), nil
	}
	if len(args) != 8 || args[3] != "/usr/bin/paru" || args[4] != "-U" || args[5] != "--noconfirm" || args[6] != "--" {
		return nil, fmt.Errorf("unsafe runuser invocation: %v", args)
	}
	path := args[7]
	name, version, revision, _, _, ok := runner.packageData(path)
	if !ok {
		return nil, errors.New("unknown paru package")
	}
	version = strings.TrimSuffix(version, "-1")
	switch name {
	case productionAURPackageName:
		runner.events = append(runner.events, "paru-go")
		runner.installedGoVersion, runner.installedGoRevision = version, revision
		providerPath := filepath.Join(filepath.Dir(runner.installedBinary), backendGoName)
		if err := os.RemoveAll(providerPath); err != nil {
			return nil, err
		}
		if err := os.WriteFile(providerPath, []byte("installed "+version+"\n"), 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(runner.goRevisionFile, []byte(revision+"\n"), 0o644); err != nil {
			return nil, err
		}
	case productionWebPackageName:
		runner.events = append(runner.events, "paru-web-hook")
		if runner.restartOnWebInstall {
			runner.restartCounter++
			runner.restartOnWebInstall = false
		}
		runner.installedWebVersion, runner.installedWebRevision = version, revision
		if err := os.WriteFile(runner.webRevisionFile, []byte(revision+"\n"), 0o644); err != nil {
			return nil, err
		}
		release := version + "-1.g" + revision[:12]
		if err := executeFrontendDeploy(frontendDeployOptions{Action: "rollback", Root: runner.frontendRoot, Release: release, Keep: 3}); err != nil {
			return nil, err
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
			if runner.rollbackServiceActive {
				return []byte("active\n"), nil
			}
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
			runner.events = append(runner.events, "systemd-stop")
			if runner.cancelOnStop != nil {
				runner.cancelOnStop()
			}
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
			runner.events = append(runner.events, "systemd-start")
			if runner.restartOnEnable {
				runner.restartCounter++
				runner.restartOnEnable = false
			}
		}
		return nil, nil
	case "disable":
		runner.timerActive = false
		return nil, nil
	case "daemon-reload":
		return nil, nil
	case "reset-failed":
		if slices.Contains(args[1:], productionServiceName) {
			runner.restartCounter = 0
			runner.restartBaselineRead = false
			runner.events = append(runner.events, "systemd-reset-failed")
		}
		return nil, nil
	case "show":
		if strings.HasSuffix(args[1], ".timer") && slices.Contains(args, "--property=NextElapseUSecRealtime") {
			active, sub := "inactive", "dead"
			if runner.timerActive {
				active, sub = "active", "waiting"
			}
			next, last := "n/a", "n/a"
			if !runner.timerDeadline.IsZero() {
				next = runner.timerDeadline.UTC().Format("Mon 2006-01-02 15:04:05 MST")
			}
			if !runner.timerLastTrigger.IsZero() {
				last = runner.timerLastTrigger.UTC().Format("Mon 2006-01-02 15:04:05 MST")
			}
			return []byte(fmt.Sprintf("LoadState=loaded\nActiveState=%s\nSubState=%s\nUnitFileState=enabled\nNextElapseUSecRealtime=%s\nLastTriggerUSec=%s\n", active, sub, next, last)), nil
		}
		property := ""
		for _, arg := range args {
			if strings.HasPrefix(arg, "--property=") {
				property = strings.TrimPrefix(arg, "--property=")
			}
		}
		switch property {
		case "NRestarts":
			restarts := runner.restartCounter
			runner.restartBaselineRead = true
			runner.events = append(runner.events, fmt.Sprintf("restart-read:%d", restarts))
			return []byte(strconv.FormatInt(restarts, 10) + "\n"), nil
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
	paths.LegacyGoBinary = filepath.Join(root, "usr", "bin", "lmm-api-go")
	paths.LegacyDeployBinary = filepath.Join(root, "usr", "bin", "lmm-api-deploy")
	paths.GoRevisionFile = filepath.Join(root, "usr", "share", "doc", "lmm-api-go-bin", "REVISION")
	paths.GoContractFile = filepath.Join(root, "usr", "share", "doc", "lmm-api-go-bin", "API_ROUTE_CONTRACT_REVISION")
	paths.WebRevisionFile = filepath.Join(root, "usr", "share", "doc", "lmm-api-web-bin", "REVISION")
	paths.WebContractFile = filepath.Join(root, "usr", "share", "doc", "lmm-api-web-bin", "API_ROUTE_CONTRACT_REVISION")
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
	contract := strings.Repeat("a", 64)
	if err := os.WriteFile(paths.LegacyGoBinary, []byte("installed "+oldVersion+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Base(paths.LegacyGoBinary), paths.InstalledBinary); err != nil {
		t.Fatal(err)
	}
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
	operator := filepath.Join(staging, "lmm-api-operator")
	for path, body := range map[string]string{goCandidate: "go-new", goRollback: "go-old", webCandidate: "web-new", webRollback: "web-old", probe: "probe", operator: "operator"} {
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
	clockValue := time.Date(2026, 8, 10, 1, 0, 0, 0, time.UTC)
	runner := &fakeProductionRunner{t: t, goCandidate: goCandidate, goRollback: goRollback, webCandidate: webCandidate, webRollback: webRollback, probeBinary: probe, installedBinary: paths.InstalledBinary, frontendRoot: paths.FrontendRoot, oldWebIndex: filepath.Join(oldFrontend, "index.html"), newWebIndex: filepath.Join(newFrontend, "index.html"), oldVersion: oldVersion, newVersion: newVersion, oldRevision: oldRevision, newRevision: newRevision, contractRevision: contract, installedGoVersion: oldVersion, installedWebVersion: oldVersion, installedGoRevision: oldRevision, installedWebRevision: oldRevision, goRevisionFile: paths.GoRevisionFile, webRevisionFile: paths.WebRevisionFile, goContractFile: paths.GoContractFile, webContractFile: paths.WebContractFile, serviceActive: true, timerDeadline: clockValue.Add(10 * time.Minute)}
	runtime := &productionRuntime{paths: paths, runner: runner, now: func() time.Time { return clockValue }, sleep: func(d time.Duration) { clockValue = clockValue.Add(d) }, effectiveUID: func() int { return 0 }, hostname: func() (string, error) { return productionExpectedHost, nil }, probeAttempts: 1, requiredOwnerUID: uint32(os.Getuid())}
	workspace, err := runtime.openWorkspace(workspaceRoot)
	if err != nil {
		t.Fatal(err)
	}
	options := productionTransactionOptions{Action: "apply", Workspace: workspaceRoot, OperatorUser: productionOperatorUser, GoPackage: goCandidate, GoPackageSHA256: mustHashFile(t, goCandidate), GoRollbackPackage: goRollback, GoRollbackSHA256: mustHashFile(t, goRollback), WebPackage: webCandidate, WebPackageSHA256: mustHashFile(t, webCandidate), WebRollbackPackage: webRollback, WebRollbackSHA256: mustHashFile(t, webRollback), GoChanged: true, WebChanged: true, ProbeBinary: probe, ProbeBinarySHA256: mustHashFile(t, probe), OperatorBinary: operator, OperatorBinarySHA256: mustHashFile(t, operator), ExpectedVersion: newVersion, BackupDir: backupDir, WithBackups: true, ObservationWindow: 2 * time.Minute}
	return productionFixture{runtime: runtime, runner: runner, workspace: workspace, options: options, environment: environment, clock: &clockValue}
}

func writeTestBackupSet(root string, environment []byte) error {
	if err := os.MkdirAll(root, 0o700); err != nil {
		return fmt.Errorf("create test backup root: %w", err)
	}
	for _, name := range []string{"application.archive", "frontend.archive", "database.archive", "rollback.package"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(name), 0o600); err != nil {
			return fmt.Errorf("write test backup %s: %w", name, err)
		}
	}
	var archive bytes.Buffer
	writer := tar.NewWriter(&archive)
	if err := writer.WriteHeader(&tar.Header{Name: "lmm-api-go/", Typeflag: tar.TypeDir, Mode: 0o700}); err != nil {
		return fmt.Errorf("write test configuration directory header: %w", err)
	}
	if err := writer.WriteHeader(&tar.Header{Name: "lmm-api-go/lmm-api-go.env", Typeflag: tar.TypeReg, Mode: 0o600, Size: int64(len(environment))}); err != nil {
		return fmt.Errorf("write test environment header: %w", err)
	}
	if _, err := writer.Write(environment); err != nil {
		return fmt.Errorf("write test environment: %w", err)
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("close test configuration archive: %w", err)
	}
	if err := os.WriteFile(filepath.Join(root, "configuration.archive"), archive.Bytes(), 0o600); err != nil {
		return fmt.Errorf("write test configuration archive: %w", err)
	}
	if err := os.WriteFile(filepath.Join(root, "manifest.env"), []byte("format=1\n"), 0o600); err != nil {
		return fmt.Errorf("write test backup manifest: %w", err)
	}
	attestation, err := json.Marshal(productionBackupAttestation{Format: 1, DeploymentID: filepath.Base(root), ControllerDigest: strings.Repeat("c", 64), OffhostDigest: strings.Repeat("d", 64), VerifiedUTC: time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC)})
	if err != nil {
		return fmt.Errorf("marshal test backup attestation: %w", err)
	}
	if err := os.WriteFile(filepath.Join(root, productionBackupAttestationFilename), append(attestation, '\n'), 0o600); err != nil {
		return fmt.Errorf("write test backup attestation: %w", err)
	}
	var sums strings.Builder
	for _, name := range []string{"application.archive", "frontend.archive", "configuration.archive", "database.archive", "rollback.package"} {
		digest, err := sha256File(filepath.Join(root, name))
		if err != nil {
			return fmt.Errorf("hash test backup %s: %w", name, err)
		}
		_, _ = fmt.Fprintf(&sums, "%s  %s\n", digest, name)
	}
	if err := os.WriteFile(filepath.Join(root, "SHA256SUMS"), []byte(sums.String()), 0o600); err != nil {
		return fmt.Errorf("write test backup checksums: %w", err)
	}
	return nil
}

func mustHashFile(t *testing.T, path string) string {
	t.Helper()
	digest, err := sha256File(path)
	if err != nil {
		t.Fatal(err)
	}
	return digest
}

func TestProductionDualPackageApplyUsesParuAndAwaitsExplicitConfirmationWithoutTimerUnits(t *testing.T) {
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
	if manifest.Format != productionTransactionFormat || manifest.OperatorUser != productionOperatorUser || !manifest.Go.Changed || !manifest.Web.Changed ||
		manifest.Go.CandidatePackageName != productionAURPackageName || manifest.Go.RollbackPackageName != productionAURPackageName ||
		manifest.Web.CandidatePackageName != productionWebPackageName || manifest.Web.RollbackPackageName != productionWebPackageName ||
		manifest.Go.CandidateContractRevision != manifest.Web.CandidateContractRevision || manifest.ServiceRestartBaseline != 0 || manifest.ObservationStartedUTC.IsZero() {
		t.Fatalf("manifest=%#v", manifest)
	}
	paruTransactions := 0
	for _, command := range fixture.runner.commands {
		if command.Name == commandPacman && len(command.Args) > 0 && command.Args[0] == "-U" {
			t.Fatalf("direct pacman -U used: %#v", command)
		}
		if command.Name == commandRunuser && len(command.Args) == 8 && command.Args[3] == "/usr/bin/paru" {
			paruTransactions++
			if got := strings.Join(command.Args, " "); !strings.Contains(got, "-- /usr/bin/paru -U --noconfirm --") {
				t.Fatalf("paru invocation=%q", got)
			}
		}
	}
	if paruTransactions != 2 {
		t.Fatalf("paru transactions=%d, want two split transactions", paruTransactions)
	}
	wantOrder := []string{"sudo-preflight:lmm-api-go-bin-new.pkg.tar.zst", "systemd-stop", "migrate:--apply", "paru-go", "systemd-reset-failed", "systemd-start", "restart-read:0", "request:/api/status", "request:/api/livez", "paru-web-hook"}
	cursor := 0
	for _, event := range fixture.runner.events {
		if cursor < len(wantOrder) && event == wantOrder[cursor] {
			cursor++
		}
	}
	if cursor != len(wantOrder) {
		t.Fatalf("events=%v do not contain ordered sequence %v", fixture.runner.events, wantOrder)
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
	for _, command := range fixture.runner.commands {
		if command.Name == commandSystemctl && len(command.Args) > 2 && (command.Args[0] == "enable" || command.Args[0] == "disable") && strings.HasPrefix(command.Args[2], "lmm-api-go-rollback-") {
			t.Fatalf("deployment managed a rollback timer/service: %#v", command)
		}
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

func TestProductionConfirmRequiresObservationAndExactIdentities(t *testing.T) {
	fixture := newProductionFixture(t)
	if _, err := fixture.runtime.apply(context.Background(), fixture.workspace, fixture.options); err != nil {
		t.Fatal(err)
	}
	manifest, _ := fixture.runtime.readManifest(fixture.workspace)
	*fixture.clock = manifest.ObservationStartedUTC.Add(119 * time.Second)
	if _, err := fixture.runtime.confirmLoaded(context.Background(), fixture.workspace, manifest); err == nil || !strings.Contains(err.Error(), "120 seconds") {
		t.Fatalf("early confirm error=%v", err)
	}
	fixture.runtime.now = func() time.Time { return manifest.ObservationStartedUTC.Add(3 * time.Minute) }
	confirmed, err := fixture.runtime.confirmLoaded(context.Background(), fixture.workspace, manifest)
	if err != nil {
		t.Fatal(err)
	}
	if confirmed.Phase != "CONFIRMED" {
		t.Fatalf("confirmed=%#v", confirmed)
	}
}

func TestProductionConfirmingStateRemainsManuallyRollbackEligible(t *testing.T) {
	fixture := newProductionFixture(t)
	if _, err := fixture.runtime.apply(context.Background(), fixture.workspace, fixture.options); err != nil {
		t.Fatal(err)
	}
	if err := fixture.runtime.writeStatus(fixture.workspace, productionStatus{
		Phase: "CONFIRMING", Version: fixture.runner.newVersion, Previous: fixture.runner.oldVersion,
	}); err != nil {
		t.Fatal(err)
	}
	status, err := fixture.runtime.rollback(context.Background(), fixture.workspace, "operator-confirm-failure")
	if err != nil {
		t.Fatal(err)
	}
	if status.Phase != "ROLLED_BACK" || fixture.runner.installedGoVersion != fixture.runner.oldVersion {
		t.Fatalf("manual rollback=%#v installed=%q", status, fixture.runner.installedGoVersion)
	}
}

func TestProductionManifestTamperRejectsConfirmationAndKeepsRecoveryEvidence(t *testing.T) {
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
	if _, err := os.Stat(fixture.runtime.paths.TransactionLock); err != nil {
		t.Fatalf("tampered confirmation released transaction evidence: %v", err)
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

func TestProductionRejectsRootOperatorBeforeMutation(t *testing.T) {
	fixture := newProductionFixture(t)
	fixture.runner.operatorUID = "0"
	if _, err := fixture.runtime.apply(context.Background(), fixture.workspace, fixture.options); err == nil || !strings.Contains(err.Error(), "uid greater than zero") {
		t.Fatalf("root operator error=%v", err)
	}
	if fixture.runner.timerActive {
		t.Fatal("root operator failure created a rollback timer")
	}
}

func TestProductionRejectsContractMismatchBeforeMutation(t *testing.T) {
	fixture := newProductionFixture(t)
	fixture.runner.webContractRevision = "other-contract"
	if _, err := fixture.runtime.apply(context.Background(), fixture.workspace, fixture.options); err == nil || !strings.Contains(err.Error(), "contract revisions differ") {
		t.Fatalf("contract mismatch error=%v", err)
	}
	if fixture.runner.timerActive {
		t.Fatal("contract mismatch created a rollback timer")
	}
}

func TestProductionActivationFailureRequiresExplicitRetryableRollback(t *testing.T) {
	fixture := newProductionFixture(t)
	fixture.runner.restartCounter = 9
	fixture.runner.restartOnEnable = true

	if _, err := fixture.runtime.apply(context.Background(), fixture.workspace, fixture.options); err == nil || !strings.Contains(err.Error(), "restart baseline hard stop") {
		t.Fatalf("startup restart error=%v", err)
	}
	status, err := fixture.runtime.readStatus(fixture.workspace)
	if err != nil {
		t.Fatal(err)
	}
	if status.Phase != "ROLLBACK_REQUIRED" || status.Failure == "" {
		t.Fatalf("activation failure status=%#v", status)
	}
	if fixture.runner.installedGoVersion != fixture.runner.newVersion {
		t.Fatalf("apply automatically rolled back candidate: installed=%q", fixture.runner.installedGoVersion)
	}
	if _, err := os.Stat(fixture.runtime.paths.TransactionLock); err != nil {
		t.Fatalf("activation failure released transaction lock: %v", err)
	}
	rolledBack, err := fixture.runtime.rollback(context.Background(), fixture.workspace, "operator-request")
	if err != nil {
		t.Fatal(err)
	}
	if rolledBack.Phase != "ROLLED_BACK" || fixture.runner.installedGoVersion != fixture.runner.oldVersion {
		t.Fatalf("manual rollback=%#v installed=%q", rolledBack, fixture.runner.installedGoVersion)
	}
}

func TestProductionWebOnlyRestartCannotBeSwallowed(t *testing.T) {
	tests := []struct {
		name                  string
		inject                func(*fakeProductionRunner)
		wantCandidateInstalls int
		wantError             string
	}{
		{
			name: "initial backend probe",
			inject: func(runner *fakeProductionRunner) {
				runner.restartOnRequestAfterBaseline = true
			},
			wantError: "candidate local backend health gate changed restart baseline",
		},
		{
			name: "Web installation",
			inject: func(runner *fakeProductionRunner) {
				runner.restartOnWebInstall = true
			},
			wantCandidateInstalls: 1,
			wantError:             "candidate Web installation changed restart baseline",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newProductionFixture(t)
			fixture.options.GoChanged = false
			fixture.options.GoPackage = fixture.options.GoRollbackPackage
			fixture.options.GoPackageSHA256 = fixture.options.GoRollbackSHA256
			fixture.options.ExpectedVersion = fixture.runner.oldVersion
			fixture.runner.probeVersion = fixture.runner.oldVersion
			fixture.runner.restartCounter = 7
			test.inject(fixture.runner)

			if _, err := fixture.runtime.apply(context.Background(), fixture.workspace, fixture.options); err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("Web-only restart error=%v", err)
			}
			manifest, err := fixture.runtime.readManifest(fixture.workspace)
			if err != nil {
				t.Fatal(err)
			}
			if manifest.ServiceRestartBaseline != 7 || manifest.ObservationStartedUTC.IsZero() {
				t.Fatalf("Web-only observation baseline=%d started=%s", manifest.ServiceRestartBaseline, manifest.ObservationStartedUTC)
			}
			candidateInstalls := 0
			for _, command := range fixture.runner.commands {
				if command.Name == commandRunuser && len(command.Args) == 8 && command.Args[7] == fixture.options.WebPackage {
					candidateInstalls++
				}
			}
			if candidateInstalls != test.wantCandidateInstalls {
				t.Fatalf("candidate Web installs=%d want=%d", candidateInstalls, test.wantCandidateInstalls)
			}
			if fixture.runner.restartCounter != 8 {
				t.Fatalf("restart counter=%d, want baseline change to remain visible", fixture.runner.restartCounter)
			}
		})
	}
}

func TestProductionWebOnlyDoesNotMutateGoService(t *testing.T) {
	fixture := newProductionFixture(t)
	fixture.options.GoChanged = false
	fixture.options.GoPackage = fixture.options.GoRollbackPackage
	fixture.options.GoPackageSHA256 = fixture.options.GoRollbackSHA256
	fixture.options.ExpectedVersion = fixture.runner.oldVersion
	fixture.runner.probeVersion = fixture.runner.oldVersion
	if _, err := fixture.runtime.apply(context.Background(), fixture.workspace, fixture.options); err != nil {
		t.Fatal(err)
	}
	for _, event := range fixture.runner.events {
		if event == "systemd-stop" || event == "systemd-start" || strings.HasPrefix(event, "migrate:") || event == "paru-go" {
			t.Fatalf("web-only deployment performed Go mutation: events=%v", fixture.runner.events)
		}
	}
	if !slices.Contains(fixture.runner.events, "paru-web-hook") {
		t.Fatalf("web-only deployment did not install Web package: events=%v", fixture.runner.events)
	}
}

func TestProductionGoOnlyDoesNotSwitchFrontend(t *testing.T) {
	fixture := newProductionFixture(t)
	fixture.options.WebChanged = false
	fixture.options.WebPackage = fixture.options.WebRollbackPackage
	fixture.options.WebPackageSHA256 = fixture.options.WebRollbackSHA256
	if _, err := fixture.runtime.apply(context.Background(), fixture.workspace, fixture.options); err != nil {
		t.Fatal(err)
	}
	if slices.Contains(fixture.runner.events, "paru-web-hook") {
		t.Fatalf("Go-only deployment switched frontend: events=%v", fixture.runner.events)
	}
}

func TestProductionSchemaIncompatibilityIsPreflightHardStop(t *testing.T) {
	fixture := newProductionFixture(t)
	fixture.runner.rollbackMigrationFailure = true
	if _, err := fixture.runtime.apply(context.Background(), fixture.workspace, fixture.options); err == nil || !strings.Contains(err.Error(), "N-1 schema preflight hard stop") {
		t.Fatalf("schema incompatibility error=%v", err)
	}
	if fixture.runner.timerActive {
		t.Fatal("schema incompatibility created a rollback timer instead of hard stopping")
	}
	for _, event := range fixture.runner.events {
		if event == "paru-go" || event == "paru-web-hook" || event == "systemd-stop" {
			t.Fatalf("schema preflight mutated production state: events=%v", fixture.runner.events)
		}
	}
}

func TestProductionRejectsInvalidCandidateEdgePolicyBeforeMutation(t *testing.T) {
	fixture := newProductionFixture(t)
	fixture.runner.invalidCandidateEdgePolicy = true
	if _, err := fixture.runtime.apply(context.Background(), fixture.workspace, fixture.options); err == nil || !strings.Contains(err.Error(), "candidate edge-policy preflight") {
		t.Fatalf("edge-policy preflight error=%v", err)
	}
	if fixture.runner.timerActive || !fixture.runner.serviceActive {
		t.Fatalf("invalid candidate changed runtime state: rollback_timer=%v service=%v", fixture.runner.timerActive, fixture.runner.serviceActive)
	}
	for _, event := range fixture.runner.events {
		if event == "systemd-stop" || strings.HasPrefix(event, "paru-") {
			t.Fatalf("invalid candidate mutated production state: events=%v", fixture.runner.events)
		}
	}
	status, err := fixture.runtime.readStatus(fixture.workspace)
	if err != nil {
		t.Fatal(err)
	}
	if status.Phase != "FAILED_PREARM" {
		t.Fatalf("status phase=%s want FAILED_PREARM", status.Phase)
	}
}

func TestProductionMutationFailureNeverAutomaticallyRollsBack(t *testing.T) {
	fixture := newProductionFixture(t)
	fixture.runner.alteredCandidatePackage = true
	_, err := fixture.runtime.apply(context.Background(), fixture.workspace, fixture.options)
	if err == nil || !strings.Contains(err.Error(), "integrity check failed") {
		t.Fatalf("candidate integrity error=%v", err)
	}
	status, statusErr := fixture.runtime.readStatus(fixture.workspace)
	if statusErr != nil {
		t.Fatal(statusErr)
	}
	if status.Phase != "ROLLBACK_REQUIRED" || status.Failure == "" {
		t.Fatalf("mutation failure state=%#v", status)
	}
	if _, err := os.Stat(fixture.runtime.paths.TransactionLock); err != nil {
		t.Fatalf("mutation failure released lock/evidence: %v", err)
	}
}

func TestPersistRollbackFailureReturnsStatusWriteError(t *testing.T) {
	fixture := newProductionFixture(t)
	workspace := fixture.workspace
	workspace.statusPath = filepath.Join(workspace.root, "missing-state", "status.json")
	operationErr := errors.New("rollback package install failed")
	err := fixture.runtime.persistRollbackFailure(workspace, productionStatus{Phase: "ROLLING_BACK"}, "test", operationErr)
	if !errors.Is(err, operationErr) || !strings.Contains(err.Error(), "persist ROLLBACK_REQUIRED status") {
		t.Fatalf("rollback failure error=%v", err)
	}
}

func TestProductionCancelledActivationRetainsManualRecovery(t *testing.T) {
	fixture := newProductionFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	fixture.runner.cancelOnStop = cancel
	_, err := fixture.runtime.apply(ctx, fixture.workspace, fixture.options)
	if err == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled activation error=%v", err)
	}
	status, statusErr := fixture.runtime.readStatus(fixture.workspace)
	if statusErr != nil {
		t.Fatal(statusErr)
	}
	if status.Phase != "ROLLBACK_REQUIRED" {
		t.Fatalf("cancel recovery state=%#v installed=%q", status, fixture.runner.installedGoVersion)
	}
	if _, rollbackErr := fixture.runtime.rollback(context.Background(), fixture.workspace, "operator-after-cancellation"); rollbackErr != nil {
		t.Fatal(rollbackErr)
	}
}

func TestProductionManualRollbackNeverRestoresDatabaseAndPreservesOnlineWrites(t *testing.T) {
	fixture := newProductionFixture(t)
	if _, err := fixture.runtime.apply(context.Background(), fixture.workspace, fixture.options); err != nil {
		t.Fatal(err)
	}
	writesAfterBackup := fixture.runner.onlineWriteCount
	if writesAfterBackup == 0 {
		t.Fatal("fixture did not simulate an online write after the optional backup")
	}
	commandsBeforeRollback := len(fixture.runner.commands)
	if _, err := fixture.runtime.rollback(context.Background(), fixture.workspace, "operator-request"); err != nil {
		t.Fatal(err)
	}
	if fixture.runner.onlineWriteCount != writesAfterBackup {
		t.Fatalf("manual rollback lost online writes: got=%d want=%d", fixture.runner.onlineWriteCount, writesAfterBackup)
	}
	for _, command := range fixture.runner.commands[commandsBeforeRollback:] {
		if filepath.Base(command.Name) == "pg_restore" {
			t.Fatalf("manual rollback invoked pg_restore: %#v", command)
		}
	}
}

func TestProductionBackupsMayBeOmittedOnlyForWebOnlyTransactions(t *testing.T) {
	t.Run("Web-only omitted", func(t *testing.T) {
		fixture := newProductionFixture(t)
		fixture.options.GoChanged = false
		fixture.options.GoPackage = fixture.options.GoRollbackPackage
		fixture.options.GoPackageSHA256 = fixture.options.GoRollbackSHA256
		fixture.options.ExpectedVersion = fixture.runner.oldVersion
		fixture.options.BackupDir = ""
		fixture.options.WithBackups = false
		fixture.runner.probeVersion = fixture.runner.oldVersion
		if _, err := fixture.runtime.apply(context.Background(), fixture.workspace, fixture.options); err != nil {
			t.Fatal(err)
		}
		manifest, err := fixture.runtime.readManifest(fixture.workspace)
		if err != nil {
			t.Fatal(err)
		}
		if manifest.BackupsEnabled || manifest.BackupDir != "" || manifest.DatabaseBackupSHA256 != "" {
			t.Fatalf("backup state persisted for Web-only release without backups: %#v", manifest)
		}
	})
	t.Run("Go change omitted", func(t *testing.T) {
		fixture := newProductionFixture(t)
		fixture.options.BackupDir = ""
		fixture.options.WithBackups = false
		if _, err := fixture.runtime.apply(context.Background(), fixture.workspace, fixture.options); err == nil || !strings.Contains(err.Error(), "Go transactions require verified three-copy backups") {
			t.Fatalf("Go backup requirement error=%v", err)
		}
		if _, err := os.Lstat(fixture.workspace.statusPath); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("rejected Go transaction wrote status: %v", err)
		}
		if _, err := os.Lstat(fixture.workspace.manifestPath); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("rejected Go transaction wrote manifest: %v", err)
		}
	})
	t.Run("authorized-empty-database", func(t *testing.T) {
		fixture := newProductionFixture(t)
		if err := os.Truncate(filepath.Join(fixture.options.BackupDir, "database.archive"), 0); err != nil {
			t.Fatal(err)
		}
		if _, err := fixture.runtime.apply(context.Background(), fixture.workspace, fixture.options); err == nil {
			t.Fatal("unsafe authorized backup was accepted")
		}
		if fixture.runner.timerActive {
			t.Fatal("unsafe authorized backup created a rollback timer")
		}
	})
}

func TestProductionRejectsMissingSudoPrivilegeBeforeMutation(t *testing.T) {
	fixture := newProductionFixture(t)
	fixture.runner.sudoFailure = true
	if _, err := fixture.runtime.apply(context.Background(), fixture.workspace, fixture.options); err == nil || !strings.Contains(err.Error(), "exact non-interactive pacman") {
		t.Fatalf("sudo preflight error=%v", err)
	}
	if fixture.runner.timerActive {
		t.Fatal("failed sudo preflight created a rollback timer")
	}
}

func TestParuPackageGateRejectsMaliciousAndNonCanonicalPaths(t *testing.T) {
	fixture := newProductionFixture(t)
	outside := filepath.Join(t.TempDir(), "lmm-api-go-bin-1-1-x86_64.pkg.tar.zst")
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	unsafeNames := []string{
		"lmm-api-go-bin-*.pkg.tar.zst",
		"lmm-api-go-bin-has space.pkg.tar.zst",
		"lmm-api-go-bin-safe.pkg.tar.zst.extra",
		"other-bin-1.pkg.tar.zst",
	}
	for _, name := range unsafeNames {
		path := filepath.Join(fixture.workspace.stagingDir, name)
		if err := os.WriteFile(path, []byte("unsafe"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := fixture.runtime.validateParuPackagePath(fixture.workspace, path); err == nil {
			t.Fatalf("unsafe package path accepted: %s", path)
		}
	}
	for _, path := range []string{outside, filepath.Join(fixture.workspace.stagingDir, "..", filepath.Base(fixture.options.GoPackage))} {
		if err := fixture.runtime.validateParuPackagePath(fixture.workspace, path); err == nil {
			t.Fatalf("escaping package path accepted: %s", path)
		}
	}
	if err := os.Chmod(fixture.options.GoPackage, 0o660); err != nil {
		t.Fatal(err)
	}
	if err := fixture.runtime.validateParuPackagePath(fixture.workspace, fixture.options.GoPackage); err == nil || !strings.Contains(err.Error(), "non-writable") {
		t.Fatalf("writable package error=%v", err)
	}
}

func TestParseProductionTransactionRejectsRemovedAutomaticRollbackFlags(t *testing.T) {
	fixture := newProductionFixture(t)
	base := []string{
		"--workspace", fixture.workspace.root, "--operator-user", productionOperatorUser,
		"--go-package", fixture.options.GoPackage, "--go-package-sha256", fixture.options.GoPackageSHA256,
		"--go-rollback-package", fixture.options.GoRollbackPackage, "--go-rollback-sha256", fixture.options.GoRollbackSHA256,
		"--web-package", fixture.options.WebPackage, "--web-package-sha256", fixture.options.WebPackageSHA256,
		"--web-rollback-package", fixture.options.WebRollbackPackage, "--web-rollback-sha256", fixture.options.WebRollbackSHA256,
		"--probe-binary", fixture.options.ProbeBinary, "--probe-binary-sha256", fixture.options.ProbeBinarySHA256,
		"--expected-version", fixture.options.ExpectedVersion, "--go-changed", "--web-changed",
		"--with-backups", "--backup-dir", fixture.options.BackupDir,
	}
	for _, removed := range [][]string{{"--rollback-seconds", "600"}, {"--manual-confirm"}} {
		arguments := append(slices.Clone(base), removed...)
		_, err := parseProductionTransactionOptions("apply", arguments, &bytes.Buffer{})
		if err == nil || !strings.Contains(err.Error(), "flag provided but not defined") {
			t.Fatalf("removed flags %v error=%v", removed, err)
		}
	}
}

func TestParseProductionTransactionRequiresBackupsForGoChanges(t *testing.T) {
	fixture := newProductionFixture(t)
	base := []string{
		"--workspace", fixture.workspace.root, "--operator-user", productionOperatorUser,
		"--go-package", fixture.options.GoPackage, "--go-package-sha256", fixture.options.GoPackageSHA256,
		"--go-rollback-package", fixture.options.GoRollbackPackage, "--go-rollback-sha256", fixture.options.GoRollbackSHA256,
		"--web-package", fixture.options.WebPackage, "--web-package-sha256", fixture.options.WebPackageSHA256,
		"--web-rollback-package", fixture.options.WebRollbackPackage, "--web-rollback-sha256", fixture.options.WebRollbackSHA256,
		"--probe-binary", fixture.options.ProbeBinary, "--probe-binary-sha256", fixture.options.ProbeBinarySHA256,
		"--expected-version", fixture.options.ExpectedVersion,
	}
	_, err := parseProductionTransactionOptions("apply", append(slices.Clone(base), "--go-changed"), &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "--go-changed requires verified three-copy backups") {
		t.Fatalf("Go backup parse error=%v", err)
	}

	webOnly, err := parseProductionTransactionOptions("apply", append(slices.Clone(base), "--web-changed"), &bytes.Buffer{})
	if err != nil {
		t.Fatalf("Web-only transaction without backups rejected: %v", err)
	}
	if webOnly.WithBackups || webOnly.BackupDir != "" {
		t.Fatalf("Web-only backup options=%#v", webOnly)
	}
}

func TestPrepareOperatorWorkspaceRejectsOversizedGroupID(t *testing.T) {
	fixture := newProductionFixture(t)
	fixture.runner.operatorGID = "4294967296"

	err := fixture.runtime.prepareOperatorWorkspace(
		context.Background(),
		fixture.workspace,
		productionOperatorUser,
		nil,
	)
	if err == nil || !strings.Contains(err.Error(), "primary group is invalid") {
		t.Fatalf("group ID error=%v", err)
	}
}

func TestPrepareOperatorWorkspaceRejectsHardlinkBeforePermissionMutation(t *testing.T) {
	fixture := newProductionFixture(t)
	external := filepath.Join(t.TempDir(), "external.pkg")
	if err := os.WriteFile(external, []byte("external"), 0o600); err != nil {
		t.Fatal(err)
	}
	linked := filepath.Join(fixture.workspace.stagingDir, "hardlinked.pkg")
	if err := os.Link(external, linked); err != nil {
		t.Fatal(err)
	}
	err := fixture.runtime.prepareOperatorWorkspace(context.Background(), fixture.workspace, productionOperatorUser, []productionStagedFile{{path: linked}})
	if err == nil || !strings.Contains(err.Error(), "link count") {
		t.Fatalf("hardlink error=%v", err)
	}
	info, statErr := os.Stat(external)
	if statErr != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("external file permissions changed before rejection: info=%v err=%v", info, statErr)
	}
}

func TestOSProductionCommandRunnerRejectsUnknownExecutable(t *testing.T) {
	_, err := (osProductionCommandRunner{}).Run(context.Background(), productionCommand{Name: "/tmp/untrusted-command"})
	if err == nil || !strings.Contains(err.Error(), "not allowlisted") {
		t.Fatalf("unknown executable error=%v", err)
	}
}

type unownedMemoryDropInRunner struct{}

func (unownedMemoryDropInRunner) Run(_ context.Context, command productionCommand) ([]byte, error) {
	if command.Name == commandPacman && len(command.Args) == 2 && command.Args[0] == "-Qo" {
		return nil, errors.New("no package owns the path")
	}
	return nil, fmt.Errorf("unexpected command: %s %v", command.Name, command.Args)
}

func TestRetireContractlessMemoryDropInForPackageAdoption(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, productionMemoryFileName)
	if err := os.WriteFile(path, productionMemoryConfig(), 0o644); err != nil {
		t.Fatal(err)
	}
	runtime := &productionRuntime{runner: unownedMemoryDropInRunner{}, paths: productionPaths{PackagedDropInDir: directory}}
	identity := productionAURPackageName + " 0.1.34.r1146.gde02fda27-1"
	if err := runtime.retireContractlessMemoryDropInForUpgrade(context.Background(), identity); err != nil {
		t.Fatalf("retireContractlessMemoryDropInForUpgrade() error = %v", err)
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("recognized unowned legacy drop-in remains: %v", err)
	}
	if err := os.WriteFile(path, []byte("[Service]\nMemoryMax=999M\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := runtime.retireContractlessMemoryDropInForUpgrade(context.Background(), identity); err == nil {
		t.Fatal("unknown unowned legacy drop-in was accepted")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("unknown drop-in was removed: %v", err)
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
