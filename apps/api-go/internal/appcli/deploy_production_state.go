package appcli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

const (
	productionServiceName          = "lmm-api.service"
	productionExpectedHost         = "arch-dmit"
	productionDefaultRollback      = 10 * time.Minute
	productionDefaultObservation   = 3 * time.Minute
	productionObservationInterval  = 10 * time.Second
	productionConfirmationMargin   = 30 * time.Second
	productionCommandTimeout       = 2 * time.Minute
	productionProbeTimeout         = 8 * time.Second
	productionProbeAttempts        = 45
	productionTransactionFormat    = 5
	productionStatusFormat         = 1
	productionFrontendReleaseKeep  = 3
	productionTransactionMarker    = "deployment.env"
	productionWorkspaceMarker      = ".lmm-deploy-workspace"
	productionManifestFilename     = "deployment.json"
	productionStatusFilename       = "status.json"
	productionProbeTokenFilename   = "probe-token"
	productionConfigRestoreDirname = "config-restore"
	productionSourcePackageName    = "lmm-api-go"
	productionAURPackageName       = "lmm-api-go-bin"
	productionWebPackageName       = "lmm-api-web-bin"
	productionOperatorPackageName  = productionAURPackageName
	productionOperatorUser         = "lmm-api-deploy"
	productionOperatorBinary       = "/usr/bin/lmm-api"
	legacyContractRevision         = "legacy"
	legacyContractlessGoVersion    = "0.1.34.r1146.gde02fda27-1"
	legacyContractlessWebVersion   = "0.1.30-1"
	commandAge                     = "/usr/bin/age"
	commandBsdtar                  = "/usr/bin/bsdtar"
	commandBun                     = "/usr/bin/bun"
	commandCosign                  = "/usr/bin/cosign"
	commandFile                    = "/usr/bin/file"
	commandGit                     = "/usr/bin/git"
	commandGo                      = "/usr/bin/go"
	commandID                      = "/usr/bin/id"
	commandJournalctl              = "/usr/bin/journalctl"
	commandMakepkg                 = "/usr/bin/makepkg"
	commandNginx                   = "/usr/bin/nginx"
	commandPacman                  = "/usr/bin/pacman"
	commandPGDump                  = "/usr/bin/pg_dump"
	commandPGRestore               = "/usr/bin/pg_restore"
	commandPSQL                    = "/usr/bin/psql"
	commandRunuser                 = "/usr/bin/runuser"
	commandSCP                     = "/usr/bin/scp"
	commandSSH                     = "/usr/bin/ssh"
	commandSudo                    = "/usr/bin/sudo"
	commandSystemctl               = "/usr/bin/systemctl"
	commandVercmp                  = "/usr/bin/vercmp"
)

var (
	productionIDPattern              = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,79}$`)
	productionVersionPattern         = regexp.MustCompile(`^[0-9][0-9A-Za-z._+]*$`)
	productionSHA256Pattern          = regexp.MustCompile(`^[0-9a-f]{64}$`)
	productionReasonPattern          = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
	productionPkgrelPattern          = regexp.MustCompile(`^[1-9][0-9]*(?:\.[0-9]+)?$`)
	productionUserPattern            = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)
	productionRevisionPattern        = regexp.MustCompile(`^[0-9a-f]{40,64}$`)
	productionContractPattern        = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]{0,127}$`)
	productionPackageFilenamePattern = regexp.MustCompile(`^lmm-api-(?:go|web)-bin-[A-Za-z0-9][A-Za-z0-9._+@~-]*\.pkg\.tar\.(?:zst|xz|gz|bz2|lz4|lrz|lzo|Z)$`)
)

type productionPaths struct {
	WorkRoot              string
	BackupRoot            string
	GlobalLock            string
	TransactionLock       string
	FrontendRoot          string
	SystemdUnitRoot       string
	ConfigDir             string
	DropInDir             string
	PackagedDropInDir     string
	NginxRoot             string
	EdgeAssetRoot         string
	InstalledBinary       string
	OperatorBinary        string
	LegacyGoBinary        string
	LegacyDeployBinary    string
	RunuserBinary         string
	ParuBinary            string
	GoRevisionFile        string
	GoContractFile        string
	GoSourceRevisionFile  string
	GoSourceContractFile  string
	WebRevisionFile       string
	WebContractFile       string
	PackagedFrontend      string
	ReleasePackages       string
	LegacyReleasePackages string
	PackageCache          string
	RemovedPaths          []string
	Service               string
	ExpectedHost          string
	PublicBaseURL         string
	LocalBaseURL          string
	JournalUnits          []string
}

func defaultProductionPaths() productionPaths {
	return productionPaths{
		WorkRoot:              "/var/lib/lmm-api-go-deploy/work",
		BackupRoot:            "/var/lib/lmm-api-go-deploy/backups",
		GlobalLock:            "/run/lock/lmm-api-go-deploy.lock",
		TransactionLock:       "/var/lib/lmm-api-go-deploy/transaction.lock",
		FrontendRoot:          defaultFrontendRoot,
		SystemdUnitRoot:       "/etc/systemd/system",
		ConfigDir:             "/etc/lmm-api-go",
		DropInDir:             defaultProductionDropInDir,
		PackagedDropInDir:     defaultPackagedMemoryDropInDir,
		NginxRoot:             defaultNginxRoot,
		EdgeAssetRoot:         defaultEdgeAssetRoot,
		InstalledBinary:       "/usr/bin/lmm-api",
		OperatorBinary:        productionOperatorBinary,
		LegacyGoBinary:        "/usr/bin/lmm-api-go",
		LegacyDeployBinary:    "/usr/bin/lmm-api-deploy",
		RunuserBinary:         "/usr/bin/runuser",
		ParuBinary:            "/usr/bin/paru",
		GoRevisionFile:        "/usr/share/doc/lmm-api-go-bin/REVISION",
		GoContractFile:        "/usr/share/doc/lmm-api-go-bin/API_ROUTE_CONTRACT_REVISION",
		GoSourceRevisionFile:  "/usr/share/doc/lmm-api-go/REVISION",
		GoSourceContractFile:  "/usr/share/doc/lmm-api-go/API_ROUTE_CONTRACT_REVISION",
		WebRevisionFile:       "/usr/share/doc/lmm-api-web-bin/REVISION",
		WebContractFile:       "/usr/share/doc/lmm-api-web-bin/API_ROUTE_CONTRACT_REVISION",
		PackagedFrontend:      "/usr/share/lmm-api-web/frontend-dist",
		ReleasePackages:       "/var/lib/lmm-api-go-deploy/release-packages",
		LegacyReleasePackages: "/var/lib/lmm-api-go/release-packages",
		PackageCache:          "/var/cache/pacman/pkg",
		RemovedPaths: []string{
			"/usr/bin/lmm-api-select",
			"/usr/lib/lmm-api",
			"/usr/lib/systemd/system/lmm-api-go.service",
		},
		Service:       productionServiceName,
		ExpectedHost:  productionExpectedHost,
		PublicBaseURL: "https://api.lmm.best",
		LocalBaseURL:  "http://127.0.0.1:3000",
		JournalUnits:  []string{productionServiceName, "nginx.service"},
	}
}

type productionCommand struct {
	Name      string
	Args      []string
	Env       []string
	Dir       string
	Timeout   time.Duration
	Sensitive bool
}

type productionCommandRunner interface {
	Run(context.Context, productionCommand) ([]byte, error)
}

type osProductionCommandRunner struct{}

func (osProductionCommandRunner) Run(parent context.Context, command productionCommand) ([]byte, error) {
	timeout := command.Timeout
	if timeout <= 0 {
		timeout = productionCommandTimeout
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	var process *exec.Cmd
	switch command.Name {
	case commandAge:
		process = exec.CommandContext(ctx, "/usr/bin/age", command.Args...)
	case commandBsdtar:
		process = exec.CommandContext(ctx, "/usr/bin/bsdtar", command.Args...)
	case commandBun:
		process = exec.CommandContext(ctx, "/usr/bin/bun", command.Args...)
	case commandCosign:
		process = exec.CommandContext(ctx, "/usr/bin/cosign", command.Args...)
	case commandFile:
		process = exec.CommandContext(ctx, "/usr/bin/file", command.Args...)
	case commandGit:
		process = exec.CommandContext(ctx, "/usr/bin/git", command.Args...)
	case commandGo:
		process = exec.CommandContext(ctx, "/usr/bin/go", command.Args...)
	case commandID:
		process = exec.CommandContext(ctx, "/usr/bin/id", command.Args...)
	case commandJournalctl:
		process = exec.CommandContext(ctx, "/usr/bin/journalctl", command.Args...)
	case commandMakepkg:
		process = exec.CommandContext(ctx, "/usr/bin/makepkg", command.Args...)
	case commandNginx:
		process = exec.CommandContext(ctx, "/usr/bin/nginx", command.Args...)
	case commandPacman:
		process = exec.CommandContext(ctx, "/usr/bin/pacman", command.Args...)
	case commandPGDump:
		process = exec.CommandContext(ctx, "/usr/bin/pg_dump", command.Args...)
	case commandPGRestore:
		process = exec.CommandContext(ctx, "/usr/bin/pg_restore", command.Args...)
	case commandPSQL:
		process = exec.CommandContext(ctx, "/usr/bin/psql", command.Args...)
	case commandRunuser:
		process = exec.CommandContext(ctx, "/usr/bin/runuser", command.Args...)
	case commandSCP:
		process = exec.CommandContext(ctx, "/usr/bin/scp", command.Args...)
	case commandSSH:
		process = exec.CommandContext(ctx, "/usr/bin/ssh", command.Args...)
	case commandSudo:
		process = exec.CommandContext(ctx, "/usr/bin/sudo", command.Args...)
	case commandSystemctl:
		process = exec.CommandContext(ctx, "/usr/bin/systemctl", command.Args...)
	case commandVercmp:
		process = exec.CommandContext(ctx, "/usr/bin/vercmp", command.Args...)
	case productionOperatorBinary:
		process = exec.CommandContext(ctx, productionOperatorBinary, command.Args...)
	default:
		return nil, fmt.Errorf("command executable is not allowlisted: %q", command.Name)
	}
	if command.Dir != "" {
		process.Dir = command.Dir
	}
	if command.Env != nil {
		process.Env = command.Env
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	process.Stdout = &stdout
	process.Stderr = &stderr
	err := process.Run()
	if err == nil {
		return stdout.Bytes(), nil
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return nil, fmt.Errorf("command %s timed out", filepath.Base(command.Name))
	}
	if command.Sensitive {
		return nil, fmt.Errorf("command %s failed: %w", filepath.Base(command.Name), err)
	}
	detail := strings.TrimSpace(stderr.String())
	if len(detail) > 1024 {
		detail = detail[:1024] + "..."
	}
	if detail == "" {
		return nil, fmt.Errorf("command %s failed: %w", filepath.Base(command.Name), err)
	}
	return nil, fmt.Errorf("command %s failed: %w: %s", filepath.Base(command.Name), err, detail)
}

func runVerifiedBinary(ctx context.Context, runner productionCommandRunner, binary string, args []string, environment []string, directory string, timeout time.Duration, sensitive bool) ([]byte, error) {
	if !filepath.IsAbs(binary) {
		return nil, errors.New("verified binary path must be absolute")
	}
	runuserArgs := append([]string{"--user", "root", "--", binary}, args...)
	return runner.Run(ctx, productionCommand{
		Name: commandRunuser, Args: runuserArgs, Env: environment, Dir: directory, Timeout: timeout, Sensitive: sensitive,
	})
}

type productionRuntime struct {
	paths            productionPaths
	runner           productionCommandRunner
	now              func() time.Time
	sleep            func(time.Duration)
	effectiveUID     func() int
	hostname         func() (string, error)
	probeAttempts    int
	requiredOwnerUID uint32
}

func defaultProductionRuntime() *productionRuntime {
	return &productionRuntime{
		paths:            defaultProductionPaths(),
		runner:           osProductionCommandRunner{},
		now:              time.Now,
		sleep:            time.Sleep,
		effectiveUID:     os.Geteuid,
		hostname:         os.Hostname,
		probeAttempts:    productionProbeAttempts,
		requiredOwnerUID: 0,
	}
}

type productionTransactionOptions struct {
	Action             string
	Workspace          string
	OperatorUser       string
	GoPackage          string
	GoPackageSHA256    string
	GoRollbackPackage  string
	GoRollbackSHA256   string
	WebPackage         string
	WebPackageSHA256   string
	WebRollbackPackage string
	WebRollbackSHA256  string
	GoChanged          bool
	WebChanged         bool
	ProbeBinary        string
	ProbeBinarySHA256  string
	ExpectedVersion    string
	BackupDir          string
	WithBackups        bool
	RollbackWindow     time.Duration
	ObservationWindow  time.Duration
	ManualConfirm      bool
	PreserveEdgePolicy bool
	Reason             string
}

type productionPackageTransition struct {
	CandidatePackageName      string `json:"candidate_package_name"`
	RollbackPackageName       string `json:"rollback_package_name"`
	Changed                   bool   `json:"changed"`
	CandidatePath             string `json:"candidate_path"`
	RollbackPath              string `json:"rollback_path"`
	CandidateIdentity         string `json:"candidate_identity"`
	RollbackIdentity          string `json:"rollback_identity"`
	CandidateSHA256           string `json:"candidate_sha256"`
	RollbackSHA256            string `json:"rollback_sha256"`
	CandidateGitRevision      string `json:"candidate_git_revision"`
	RollbackGitRevision       string `json:"rollback_git_revision"`
	CandidateContractRevision string `json:"candidate_contract_revision"`
	RollbackContractRevision  string `json:"rollback_contract_revision"`
}

type productionFrontendTransition struct {
	OldTarget      string `json:"old_target"`
	NewTarget      string `json:"new_target"`
	OldIndexSHA256 string `json:"old_index_sha256"`
	NewIndexSHA256 string `json:"new_index_sha256"`
}

type productionManifest struct {
	Format                   int                          `json:"format"`
	DeploymentID             string                       `json:"deployment_id"`
	OperatorUser             string                       `json:"operator_user"`
	Go                       productionPackageTransition  `json:"go"`
	Web                      productionPackageTransition  `json:"web"`
	Frontend                 productionFrontendTransition `json:"frontend"`
	ProbeBinary              string                       `json:"probe_binary"`
	ProbeBinarySHA256        string                       `json:"probe_binary_sha256"`
	ExpectedVersion          string                       `json:"expected_version"`
	OldVersion               string                       `json:"old_version"`
	BackupDir                string                       `json:"backup_dir,omitempty"`
	BackupsEnabled           bool                         `json:"backups_enabled"`
	DatabaseBackupSHA256     string                       `json:"database_backup_sha256,omitempty"`
	DatabaseSchema           string                       `json:"database_schema"`
	ArmedUTC                 time.Time                    `json:"armed_utc"`
	DeadlineUTC              time.Time                    `json:"deadline_utc"`
	ObservationStartedUTC    time.Time                    `json:"observation_started_utc,omitempty"`
	ObservationSeconds       int64                        `json:"observation_seconds"`
	ServiceRestartBaseline   int64                        `json:"service_restart_baseline"`
	ConfigRestorePath        string                       `json:"config_restore_path"`
	EnvironmentRestoreSHA256 string                       `json:"environment_restore_sha256"`
	NginxEdgeRestoreSHA256   string                       `json:"nginx_edge_restore_sha256,omitempty"`
	PreserveEdgePolicy       bool                         `json:"preserve_edge_policy,omitempty"`
}

func parseProductionPackageIdentity(output []byte) (name, version, identity string, err error) {
	fields := strings.Fields(string(output))
	if len(fields) != 2 {
		return "", "", "", errors.New("invalid package identity")
	}
	name, version = fields[0], fields[1]
	if name != productionAURPackageName && name != productionSourcePackageName {
		return "", "", "", fmt.Errorf("unsupported Go package %q", name)
	}
	separator := strings.LastIndexByte(version, '-')
	if separator <= 0 || !productionVersionPattern.MatchString(version[:separator]) ||
		!productionPkgrelPattern.MatchString(version[separator+1:]) {
		return "", "", "", errors.New("invalid Go package version")
	}
	return name, version, name + " " + version, nil
}

func productionPackageMatches(version, release string) bool {
	separator := strings.LastIndexByte(version, '-')
	return separator > 0 && version[:separator] == release &&
		productionPkgrelPattern.MatchString(version[separator+1:])
}

func (runtime *productionRuntime) installedGoPackage(ctx context.Context) (name, identity string, err error) {
	seen := make(map[string]struct{}, 1)
	for _, candidate := range []string{productionAURPackageName, productionSourcePackageName} {
		output, queryErr := runtime.runner.Run(ctx, productionCommand{Name: commandPacman, Args: []string{"-Q", candidate}})
		if queryErr != nil {
			continue
		}
		parsedName, _, parsedIdentity, parseErr := parseProductionPackageIdentity(output)
		if parseErr != nil {
			return "", "", errors.New("installed Go package identity is invalid")
		}
		if _, duplicate := seen[parsedIdentity]; duplicate {
			continue
		}
		seen[parsedIdentity] = struct{}{}
		if identity != "" {
			return "", "", errors.New("multiple Go packages are installed")
		}
		name, identity = parsedName, parsedIdentity
	}
	if identity == "" {
		return "", "", errors.New("installed Go package was not found")
	}
	return name, identity, nil
}

func (runtime *productionRuntime) verifyInstalledGoPackage(ctx context.Context, name, identity string) error {
	installed, err := runtime.runner.Run(ctx, productionCommand{Name: commandPacman, Args: []string{"-Q", name}})
	if err != nil || strings.TrimSpace(string(installed)) != identity {
		return errors.New("installed Go package identity mismatch")
	}
	integrity, err := runtime.runner.Run(ctx, productionCommand{Name: commandPacman, Args: []string{"-Qkk", name}, Env: append(os.Environ(), "LC_ALL=C")})
	if err != nil || !packageIntegrityClean(integrity, name) {
		return errors.New("installed Go package integrity check failed")
	}
	return nil
}

func packageIntegrityClean(output []byte, name string) bool {
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	backupPrefix := "backup file: " + name + ": "
	summaryPrefix := name + ": "
	summarySuffix := " total files, 0 altered files"
	summaryFound := false
	for _, line := range lines {
		if line == "" || strings.ContainsRune(line, '\r') || summaryFound {
			return false
		}
		if strings.HasPrefix(line, backupPrefix) && len(line) > len(backupPrefix) {
			continue
		}
		if len(line) < len(summaryPrefix)+len(summarySuffix) ||
			!strings.HasPrefix(line, summaryPrefix) || !strings.HasSuffix(line, summarySuffix) {
			return false
		}
		total := line[len(summaryPrefix) : len(line)-len(summarySuffix)]
		if _, err := strconv.ParseUint(total, 10, 64); err != nil {
			return false
		}
		summaryFound = true
	}
	return summaryFound
}

type productionPackageMetadata struct {
	Name               string
	Version            string
	Identity           string
	GitRevision        string
	ContractRevision   string
	IndexSHA256        string
	BinarySHA256       string
	ReleaseAssetSHA256 string
}

func parseNamedPackageIdentity(output []byte, expected string) (productionPackageMetadata, error) {
	fields := strings.Fields(string(output))
	if len(fields) != 2 || fields[0] != expected {
		return productionPackageMetadata{}, fmt.Errorf("expected package %s", expected)
	}
	separator := strings.LastIndexByte(fields[1], '-')
	if separator <= 0 || !productionVersionPattern.MatchString(fields[1][:separator]) ||
		!productionPkgrelPattern.MatchString(fields[1][separator+1:]) {
		return productionPackageMetadata{}, errors.New("invalid package version")
	}
	return productionPackageMetadata{Name: expected, Version: fields[1], Identity: expected + " " + fields[1]}, nil
}

func (runtime *productionRuntime) packageMetadata(ctx context.Context, packagePath string, packageNames ...string) (productionPackageMetadata, error) {
	identityOutput, err := runtime.runner.Run(ctx, productionCommand{Name: commandPacman, Args: []string{"-Qp", packagePath}})
	if err != nil {
		return productionPackageMetadata{}, fmt.Errorf("query package identity: %w", err)
	}
	fields := strings.Fields(string(identityOutput))
	if len(fields) != 2 || !slices.Contains(packageNames, fields[0]) {
		return productionPackageMetadata{}, fmt.Errorf("package identity is not one of %v", packageNames)
	}
	packageName := fields[0]
	metadata, err := parseNamedPackageIdentity(identityOutput, packageName)
	if err != nil {
		return productionPackageMetadata{}, err
	}
	docRoot := "usr/share/doc/" + packageName + "/"
	const contractName = "API_ROUTE_CONTRACT_REVISION"
	readMember := func(member string) (string, error) {
		output, err := runtime.runner.Run(ctx, productionCommand{Name: commandBsdtar, Args: []string{"-xOf", packagePath, docRoot + member}})
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(output)), nil
	}
	metadata.GitRevision, err = readMember("REVISION")
	if err != nil || !productionRevisionPattern.MatchString(metadata.GitRevision) {
		return productionPackageMetadata{}, fmt.Errorf("%s package Git revision is invalid", packageName)
	}
	metadata.ContractRevision, err = readMember(contractName)
	if err != nil {
		if !isContractlessLegacyPackage(packageName, metadata.Version) {
			return productionPackageMetadata{}, fmt.Errorf("%s package contract revision is invalid", packageName)
		}
		metadata.ContractRevision = legacyContractRevision
	} else if !productionContractPattern.MatchString(metadata.ContractRevision) {
		return productionPackageMetadata{}, fmt.Errorf("%s package contract revision is invalid", packageName)
	}
	if assetDigest, assetErr := readMember("RELEASE_ASSET_SHA256"); assetErr == nil {
		if !productionSHA256Pattern.MatchString(assetDigest) {
			return productionPackageMetadata{}, fmt.Errorf("%s package release-asset digest is invalid", packageName)
		}
		metadata.ReleaseAssetSHA256 = assetDigest
	}
	if packageName == productionWebPackageName {
		index, err := runtime.runner.Run(ctx, productionCommand{Name: commandBsdtar, Args: []string{"-xOf", packagePath, "usr/share/lmm-api-web/frontend-dist/index.html"}})
		if err != nil || len(index) == 0 {
			return productionPackageMetadata{}, errors.New("Web package frontend index is missing")
		}
		digest := sha256.Sum256(index)
		metadata.IndexSHA256 = hex.EncodeToString(digest[:])
	} else {
		binary, err := runtime.runner.Run(ctx, productionCommand{Name: commandBsdtar, Args: []string{"-xOf", packagePath, "usr/bin/lmm-api"}})
		if err != nil || len(binary) == 0 {
			binary, err = runtime.runner.Run(ctx, productionCommand{Name: commandBsdtar, Args: []string{"-xOf", packagePath, "usr/bin/lmm-api-go"}})
		}
		if err != nil || len(binary) == 0 {
			return productionPackageMetadata{}, errors.New("Go package service binary is missing")
		}
		digest := sha256.Sum256(binary)
		metadata.BinarySHA256 = hex.EncodeToString(digest[:])
	}
	return metadata, nil
}

func (runtime *productionRuntime) verifyCanonicalOperator(ctx context.Context) error {
	output, err := runtime.runner.Run(ctx, productionCommand{Name: commandPacman, Args: []string{"-Qo", productionOperatorBinary}, Env: append(os.Environ(), "LC_ALL=C")})
	prefix := productionOperatorBinary + " is owned by "
	if err != nil || !strings.HasPrefix(strings.TrimSpace(string(output)), prefix) {
		return errors.New("canonical deployment operator is not package-owned")
	}
	identity := strings.TrimPrefix(strings.TrimSpace(string(output)), prefix)
	if _, err := parseNamedPackageIdentity([]byte(identity), productionOperatorPackageName); err != nil {
		return errors.New("canonical deployment operator package identity is invalid")
	}
	integrity, err := runtime.runner.Run(ctx, productionCommand{Name: commandPacman, Args: []string{"-Qkk", productionOperatorPackageName}, Env: append(os.Environ(), "LC_ALL=C")})
	if err != nil || !packageIntegrityClean(integrity, productionOperatorPackageName) {
		return errors.New("canonical deployment operator package integrity check failed")
	}
	return nil
}

func (runtime *productionRuntime) verifyMemoryPackageOwner(ctx context.Context, identity string) error {
	path := filepath.Join(runtime.paths.PackagedDropInDir, productionMemoryFileName)
	metadata, parseErr := parseNamedPackageIdentity([]byte(identity), productionAURPackageName)
	if parseErr == nil && isContractlessLegacyPackage(productionAURPackageName, metadata.Version) {
		return ensureProductionMemoryDropIn(path)
	}
	output, err := runtime.runner.Run(ctx, productionCommand{Name: commandPacman, Args: []string{"-Qo", path}, Env: append(os.Environ(), "LC_ALL=C")})
	if err != nil || strings.TrimSpace(string(output)) != path+" is owned by "+identity {
		return errors.New("production memory drop-in is not owned by the expected Go package")
	}
	return nil
}

func (runtime *productionRuntime) retireContractlessMemoryDropInForUpgrade(ctx context.Context, rollbackIdentity string) error {
	metadata, err := parseNamedPackageIdentity([]byte(rollbackIdentity), productionAURPackageName)
	if err != nil || !isContractlessLegacyPackage(productionAURPackageName, metadata.Version) {
		return nil
	}
	path := filepath.Join(runtime.paths.PackagedDropInDir, productionMemoryFileName)
	output, ownerErr := runtime.runner.Run(ctx, productionCommand{Name: commandPacman, Args: []string{"-Qo", path}, Env: append(os.Environ(), "LC_ALL=C")})
	if ownerErr == nil {
		if strings.TrimSpace(string(output)) != path+" is owned by "+rollbackIdentity {
			return errors.New("legacy production memory drop-in has an unexpected package owner")
		}
		return nil
	}
	if err := verifyProductionMemoryDropIn(path); err != nil {
		return fmt.Errorf("refuse to retire unowned legacy production memory drop-in: %w", err)
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("retire unowned legacy production memory drop-in before package adoption: %w", err)
	}
	return nil
}

func (runtime *productionRuntime) verifyInstalledPackage(ctx context.Context, name, identity string) error {
	installed, err := runtime.runner.Run(ctx, productionCommand{Name: commandPacman, Args: []string{"-Q", name}})
	if err != nil || strings.TrimSpace(string(installed)) != identity {
		return fmt.Errorf("installed %s identity mismatch", name)
	}
	integrity, err := runtime.runner.Run(ctx, productionCommand{Name: commandPacman, Args: []string{"-Qkk", name}, Env: append(os.Environ(), "LC_ALL=C")})
	if err != nil || !packageIntegrityClean(integrity, name) {
		return fmt.Errorf("installed %s integrity check failed", name)
	}
	return nil
}

func isContractlessLegacyPackage(name, version string) bool {
	return (name == productionAURPackageName && version == legacyContractlessGoVersion) ||
		(name == productionWebPackageName && version == legacyContractlessWebVersion)
}

func (runtime *productionRuntime) readInstalledReleaseMetadata(name, identity string) (string, string, error) {
	revisionPath, contractPath := runtime.paths.GoRevisionFile, runtime.paths.GoContractFile
	switch name {
	case productionSourcePackageName:
		revisionPath, contractPath = runtime.paths.GoSourceRevisionFile, runtime.paths.GoSourceContractFile
	case productionWebPackageName:
		revisionPath, contractPath = runtime.paths.WebRevisionFile, runtime.paths.WebContractFile
	}
	read := func(path string) (string, error) {
		content, err := readSafeRegularFile(path, 4<<10)
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(content)), nil
	}
	revision, err := read(revisionPath)
	if err != nil || !productionRevisionPattern.MatchString(revision) {
		return "", "", fmt.Errorf("installed %s Git revision is invalid", name)
	}
	contract, err := read(contractPath)
	if err != nil {
		metadata, parseErr := parseNamedPackageIdentity([]byte(identity), name)
		if parseErr != nil || !isContractlessLegacyPackage(name, metadata.Version) {
			return "", "", fmt.Errorf("installed %s contract revision is invalid", name)
		}
		contract = legacyContractRevision
	} else if !productionContractPattern.MatchString(contract) {
		return "", "", fmt.Errorf("installed %s contract revision is invalid", name)
	}
	return revision, contract, nil
}

func readSafeRegularFile(path string, maximum int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Mode().Perm()&0o022 != 0 || info.Size() > maximum {
		return nil, errors.New("path is not a safe regular file")
	}
	return os.ReadFile(path)
}

type productionStatus struct {
	Format         int       `json:"format"`
	DeploymentID   string    `json:"deployment_id"`
	Phase          string    `json:"phase"`
	Version        string    `json:"version,omitempty"`
	Previous       string    `json:"previous_version,omitempty"`
	Reason         string    `json:"reason,omitempty"`
	RollbackTimer  string    `json:"rollback_timer,omitempty"`
	DeadlineUTC    time.Time `json:"deadline_utc,omitempty"`
	UpdatedUTC     time.Time `json:"updated_utc"`
	AutoConfirm    bool      `json:"auto_confirm,omitempty"`
	ObservationSec int64     `json:"observation_seconds,omitempty"`
}

type productionWorkspace struct {
	root          string
	id            string
	stateDir      string
	stagingDir    string
	manifestPath  string
	statusPath    string
	probeToken    string
	timerUnit     string
	rollbackUnit  string
	timerPath     string
	rollbackPath  string
	configRestore string
}

type productionObservationError struct{ err error }

func (err *productionObservationError) Error() string { return err.err.Error() }
func (err *productionObservationError) Unwrap() error { return err.err }

func runProductionTransaction(action string, args []string, stdout, stderr io.Writer) int {
	options, err := parseProductionTransactionOptions(action, args, stderr)
	if errors.Is(err, flag.ErrHelp) {
		return ExitOK
	}
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s deploy production %s: %v\n", ProgramName, action, err)
		return ExitUsage
	}
	runtime := defaultProductionRuntime()
	status, err := runtime.executeTransaction(context.Background(), options)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s deploy production %s: %v\n", ProgramName, action, err)
		return ExitError
	}
	encoded, err := json.MarshalIndent(status, "", "  ")
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s deploy production %s: encode status: %v\n", ProgramName, action, err)
		return ExitError
	}
	_, _ = stdout.Write(append(encoded, '\n'))
	return ExitOK
}

func parseProductionTransactionOptions(action string, args []string, stderr io.Writer) (productionTransactionOptions, error) {
	options := productionTransactionOptions{
		Action: action, RollbackWindow: productionDefaultRollback,
		ObservationWindow: productionDefaultObservation, Reason: "operator-request",
	}
	flags := flag.NewFlagSet("deploy production "+action, flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(&options.Workspace, "workspace", "", "marker-owned target deployment workspace")
	if action == "apply" {
		flags.StringVar(&options.OperatorUser, "operator-user", "", "validated unprivileged paru operator")
		flags.StringVar(&options.GoPackage, "go-package", "", "candidate lmm-api-go-bin package")
		flags.StringVar(&options.GoPackageSHA256, "go-package-sha256", "", "candidate Go package SHA-256")
		flags.StringVar(&options.GoRollbackPackage, "go-rollback-package", "", "rollback Go package")
		flags.StringVar(&options.GoRollbackSHA256, "go-rollback-sha256", "", "rollback Go package SHA-256")
		flags.StringVar(&options.WebPackage, "web-package", "", "candidate lmm-api-web-bin package")
		flags.StringVar(&options.WebPackageSHA256, "web-package-sha256", "", "candidate Web package SHA-256")
		flags.StringVar(&options.WebRollbackPackage, "web-rollback-package", "", "rollback Web package")
		flags.StringVar(&options.WebRollbackSHA256, "web-rollback-sha256", "", "rollback Web package SHA-256")
		flags.BoolVar(&options.GoChanged, "go-changed", false, "install the candidate Go package")
		flags.BoolVar(&options.WebChanged, "web-changed", false, "install the candidate Web package")
		flags.StringVar(&options.ProbeBinary, "probe-binary", "", "candidate Go binary used for migrations and probes")
		flags.StringVar(&options.ProbeBinarySHA256, "probe-binary-sha256", "", "probe binary SHA-256")
		flags.StringVar(&options.ExpectedVersion, "expected-version", "", "candidate service version")
		flags.StringVar(&options.BackupDir, "backup-dir", "", "current-turn-authorized verified target business backup directory")
		flags.BoolVar(&options.WithBackups, "with-backups", false, "bind an explicitly authorized optional business backup")
		rollbackSeconds := int(options.RollbackWindow / time.Second)
		observationSeconds := int(options.ObservationWindow / time.Second)
		flags.IntVar(&rollbackSeconds, "rollback-seconds", rollbackSeconds, "fixed automatic rollback window (must be 600)")
		flags.IntVar(&observationSeconds, "observation-seconds", observationSeconds, "stability observation window (120-360)")
		flags.BoolVar(&options.ManualConfirm, "manual-confirm", false, "leave a healthy release awaiting explicit confirmation")
		flags.BoolVar(&options.PreserveEdgePolicy, "preserve-edge-policy", false, "preserve the active nginx edge policy")
		if err := flags.Parse(args); err != nil {
			return productionTransactionOptions{}, err
		}
		options.RollbackWindow = time.Duration(rollbackSeconds) * time.Second
		options.ObservationWindow = time.Duration(observationSeconds) * time.Second
	} else if action == "rollback" {
		flags.StringVar(&options.Reason, "reason", options.Reason, "audit-safe rollback reason")
		if err := flags.Parse(args); err != nil {
			return productionTransactionOptions{}, err
		}
	} else if action == "status" || action == "confirm" {
		if err := flags.Parse(args); err != nil {
			return productionTransactionOptions{}, err
		}
	} else {
		return productionTransactionOptions{}, fmt.Errorf("unsupported action %q", action)
	}
	flags.Usage = func() { writeProductionDeployUsage(stderr) }
	if flags.NArg() != 0 {
		return productionTransactionOptions{}, errors.New("unexpected positional arguments")
	}
	if options.Workspace == "" {
		return productionTransactionOptions{}, errors.New("--workspace is required")
	}
	workspace, err := cleanAbsoluteNonRoot(options.Workspace)
	if err != nil {
		return productionTransactionOptions{}, fmt.Errorf("invalid --workspace: %w", err)
	}
	options.Workspace = workspace
	if action == "apply" {
		required := map[string]string{
			"--operator-user": options.OperatorUser,
			"--go-package":    options.GoPackage, "--go-package-sha256": options.GoPackageSHA256,
			"--go-rollback-package": options.GoRollbackPackage, "--go-rollback-sha256": options.GoRollbackSHA256,
			"--web-package": options.WebPackage, "--web-package-sha256": options.WebPackageSHA256,
			"--web-rollback-package": options.WebRollbackPackage, "--web-rollback-sha256": options.WebRollbackSHA256,
			"--probe-binary": options.ProbeBinary, "--probe-binary-sha256": options.ProbeBinarySHA256,
			"--expected-version": options.ExpectedVersion,
		}
		for label, value := range required {
			if value == "" {
				return productionTransactionOptions{}, fmt.Errorf("%s is required", label)
			}
		}
		for _, digest := range []string{options.GoPackageSHA256, options.GoRollbackSHA256, options.WebPackageSHA256, options.WebRollbackSHA256, options.ProbeBinarySHA256} {
			if !productionSHA256Pattern.MatchString(digest) {
				return productionTransactionOptions{}, errors.New("all SHA-256 values must be 64 lowercase hexadecimal characters")
			}
		}
		if options.OperatorUser != productionOperatorUser {
			return productionTransactionOptions{}, fmt.Errorf("--operator-user must be the package-owned %s account", productionOperatorUser)
		}
		if !productionVersionPattern.MatchString(options.ExpectedVersion) {
			return productionTransactionOptions{}, errors.New("invalid --expected-version")
		}
		if options.RollbackWindow != productionDefaultRollback {
			return productionTransactionOptions{}, errors.New("--rollback-seconds must be exactly 600")
		}
		if options.ObservationWindow < 2*time.Minute || options.ObservationWindow > 6*time.Minute {
			return productionTransactionOptions{}, errors.New("--observation-seconds must be between 120 and 360")
		}
		paths := map[string]*string{
			"--go-package": &options.GoPackage, "--go-rollback-package": &options.GoRollbackPackage,
			"--web-package": &options.WebPackage, "--web-rollback-package": &options.WebRollbackPackage,
			"--probe-binary": &options.ProbeBinary,
		}
		for label, value := range paths {
			clean, err := cleanAbsoluteNonRoot(*value)
			if err != nil {
				return productionTransactionOptions{}, fmt.Errorf("invalid %s: %w", label, err)
			}
			*value = clean
		}
		if options.WithBackups != (options.BackupDir != "") {
			return productionTransactionOptions{}, errors.New("--with-backups and --backup-dir must be supplied together")
		}
		if options.BackupDir != "" {
			clean, err := cleanAbsoluteNonRoot(options.BackupDir)
			if err != nil {
				return productionTransactionOptions{}, fmt.Errorf("invalid --backup-dir: %w", err)
			}
			options.BackupDir = clean
		}
	}
	if action == "rollback" && !productionReasonPattern.MatchString(options.Reason) {
		return productionTransactionOptions{}, errors.New("--reason must contain only audit-safe letters, digits, dot, underscore, colon, or dash")
	}
	return options, nil
}

func (runtime *productionRuntime) executeTransaction(ctx context.Context, options productionTransactionOptions) (productionStatus, error) {
	workspace, err := runtime.openWorkspace(options.Workspace)
	if err != nil {
		return productionStatus{}, err
	}
	if options.Action != "status" {
		if runtime.effectiveUID() != 0 {
			return productionStatus{}, errors.New("must run as root")
		}
		hostname, err := runtime.hostname()
		if err != nil {
			return productionStatus{}, fmt.Errorf("read production host identity: %w", err)
		}
		if hostname != runtime.paths.ExpectedHost {
			return productionStatus{}, fmt.Errorf("production host identity mismatch: got %q", hostname)
		}
	}
	lock, err := runtime.acquireGlobalLock(ctx)
	if err != nil {
		return productionStatus{}, err
	}
	defer func() {
		_ = unix.Flock(int(lock.Fd()), unix.LOCK_UN)
		_ = lock.Close()
	}()

	switch options.Action {
	case "apply":
		return runtime.apply(ctx, workspace, options)
	case "status":
		return runtime.readStatus(workspace)
	case "confirm":
		return runtime.confirm(ctx, workspace)
	case "rollback":
		return runtime.rollback(ctx, workspace, options.Reason)
	default:
		return productionStatus{}, fmt.Errorf("unsupported action %q", options.Action)
	}
}

func (runtime *productionRuntime) openWorkspace(root string) (productionWorkspace, error) {
	if filepath.Dir(root) != filepath.Clean(runtime.paths.WorkRoot) {
		return productionWorkspace{}, errors.New("workspace must be one direct child of the production work root")
	}
	id := filepath.Base(root)
	if !productionIDPattern.MatchString(id) {
		return productionWorkspace{}, errors.New("invalid deployment ID")
	}
	if err := runtime.requireOwnedSafePath(root, true); err != nil {
		return productionWorkspace{}, errors.New("workspace must be a root-owned real directory")
	}
	if err := runtime.requireOwnedSafePath(runtime.paths.WorkRoot, true); err != nil {
		return productionWorkspace{}, errors.New("production work root must be a real directory")
	}
	canonicalWorkRoot, err := filepath.EvalSymlinks(runtime.paths.WorkRoot)
	if err != nil || filepath.Clean(canonicalWorkRoot) != filepath.Clean(runtime.paths.WorkRoot) {
		return productionWorkspace{}, errors.New("production work root must not contain symlink components")
	}
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil || filepath.Clean(canonical) != filepath.Clean(root) || filepath.Dir(filepath.Clean(canonical)) != filepath.Clean(canonicalWorkRoot) {
		return productionWorkspace{}, errors.New("workspace must be a symlink-free direct child of the production work root")
	}
	marker := filepath.Join(root, productionWorkspaceMarker)
	if err := runtime.requireOwnedSafePath(marker, false); err != nil {
		return productionWorkspace{}, errors.New("workspace marker must be root-owned and safe")
	}
	markerContent, err := readPrivateRegularFile(marker, 16<<10)
	if err != nil {
		return productionWorkspace{}, fmt.Errorf("read workspace marker: %w", err)
	}
	markerValues, err := parseSimpleManifest(markerContent)
	if err != nil || markerValues["deployment_id"] != id {
		return productionWorkspace{}, errors.New("workspace marker does not own this deployment ID")
	}
	stateDir := filepath.Join(root, "state")
	if err := ensureRealDirectory(stateDir, 0o700); err != nil {
		return productionWorkspace{}, fmt.Errorf("prepare deployment state: %w", err)
	}
	if err := runtime.requireOwnedSafePath(stateDir, true); err != nil {
		return productionWorkspace{}, errors.New("deployment state must be root-owned and safe")
	}
	stateInfo, err := os.Lstat(stateDir)
	if err != nil {
		return productionWorkspace{}, fmt.Errorf("inspect deployment state: %w", err)
	}
	if stateInfo.Mode().Perm() != 0o700 {
		return productionWorkspace{}, errors.New("deployment state must remain root-only")
	}
	stagingDir := filepath.Join(root, "staging")
	if err := runtime.requireOwnedSafePath(stagingDir, true); err != nil {
		return productionWorkspace{}, fmt.Errorf("validate deployment staging: %w", err)
	}
	return productionWorkspace{
		root:          root,
		id:            id,
		stateDir:      stateDir,
		stagingDir:    stagingDir,
		manifestPath:  filepath.Join(stateDir, productionManifestFilename),
		statusPath:    filepath.Join(stateDir, productionStatusFilename),
		probeToken:    filepath.Join(stateDir, productionProbeTokenFilename),
		timerUnit:     "lmm-api-go-rollback-" + id + ".timer",
		rollbackUnit:  "lmm-api-go-rollback-" + id + ".service",
		timerPath:     filepath.Join(runtime.paths.SystemdUnitRoot, "lmm-api-go-rollback-"+id+".timer"),
		rollbackPath:  filepath.Join(runtime.paths.SystemdUnitRoot, "lmm-api-go-rollback-"+id+".service"),
		configRestore: filepath.Join(stateDir, productionConfigRestoreDirname),
	}, nil
}

func (runtime *productionRuntime) acquireGlobalLock(ctx context.Context) (*os.File, error) {
	parent := filepath.Dir(runtime.paths.GlobalLock)
	if err := ensureRealDirectory(parent, 0o755); err != nil {
		return nil, fmt.Errorf("prepare deployment lock: %w", err)
	}
	lock, err := os.OpenFile(runtime.paths.GlobalLock, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open deployment lock: %w", err)
	}
	if err := lock.Chmod(0o600); err != nil {
		_ = lock.Close()
		return nil, fmt.Errorf("protect deployment lock: %w", err)
	}
	deadline := runtime.now().Add(2 * time.Minute)
	for {
		if err := unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); err == nil {
			return lock, nil
		} else if !errors.Is(err, unix.EWOULDBLOCK) && !errors.Is(err, unix.EAGAIN) {
			_ = lock.Close()
			return nil, fmt.Errorf("lock production deployment: %w", err)
		}
		if runtime.now().After(deadline) {
			_ = lock.Close()
			return nil, errors.New("another production deployment holds the global lock")
		}
		select {
		case <-ctx.Done():
			_ = lock.Close()
			return nil, ctx.Err()
		default:
			runtime.sleep(250 * time.Millisecond)
		}
	}
}

func (runtime *productionRuntime) writeStatus(workspace productionWorkspace, status productionStatus) error {
	status.Format = productionStatusFormat
	status.DeploymentID = workspace.id
	status.UpdatedUTC = runtime.now().UTC().Truncate(time.Second)
	encoded, err := json.MarshalIndent(status, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomicRegularFile(workspace.statusPath, append(encoded, '\n'), 0o600)
}

func (runtime *productionRuntime) readStatus(workspace productionWorkspace) (productionStatus, error) {
	content, err := readPrivateRegularFile(workspace.statusPath, 64<<10)
	if err != nil {
		return productionStatus{}, fmt.Errorf("read deployment status: %w", err)
	}
	var status productionStatus
	if err := json.Unmarshal(content, &status); err != nil {
		return productionStatus{}, fmt.Errorf("decode deployment status: %w", err)
	}
	if status.Format != productionStatusFormat || status.DeploymentID != workspace.id || status.Phase == "" {
		return productionStatus{}, errors.New("deployment status identity is invalid")
	}
	return status, nil
}

func (runtime *productionRuntime) writeManifest(workspace productionWorkspace, manifest productionManifest) error {
	manifest.Format = productionTransactionFormat
	manifest.DeploymentID = workspace.id
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomicRegularFile(workspace.manifestPath, append(encoded, '\n'), 0o600)
}

func (runtime *productionRuntime) readManifest(workspace productionWorkspace) (productionManifest, error) {
	content, err := readPrivateRegularFile(workspace.manifestPath, 256<<10)
	if err != nil {
		return productionManifest{}, fmt.Errorf("read deployment manifest: %w", err)
	}
	var manifest productionManifest
	if err := json.Unmarshal(content, &manifest); err != nil {
		return productionManifest{}, fmt.Errorf("decode deployment manifest: %w", err)
	}
	if manifest.Format != productionTransactionFormat || manifest.DeploymentID != workspace.id {
		return productionManifest{}, errors.New("deployment manifest identity is invalid")
	}
	if err := runtime.validateManifest(workspace, manifest); err != nil {
		return productionManifest{}, err
	}
	return manifest, nil
}

func (runtime *productionRuntime) validateManifest(workspace productionWorkspace, manifest productionManifest) error {
	if !productionVersionPattern.MatchString(manifest.ExpectedVersion) || !productionVersionPattern.MatchString(manifest.OldVersion) ||
		manifest.OperatorUser != productionOperatorUser {
		return errors.New("deployment manifest contains invalid release or operator identity")
	}
	if manifest.Go.CandidatePackageName != productionAURPackageName ||
		(manifest.Go.RollbackPackageName != productionAURPackageName && manifest.Go.RollbackPackageName != productionSourcePackageName) ||
		manifest.Web.CandidatePackageName != productionWebPackageName || manifest.Web.RollbackPackageName != productionWebPackageName ||
		manifest.Go.CandidateContractRevision != manifest.Web.CandidateContractRevision ||
		manifest.Go.RollbackContractRevision != manifest.Web.RollbackContractRevision {
		return errors.New("deployment manifest Go/Web package or contract pair mismatch")
	}
	for _, transition := range []productionPackageTransition{manifest.Go, manifest.Web} {
		if !productionRevisionPattern.MatchString(transition.CandidateGitRevision) ||
			!productionRevisionPattern.MatchString(transition.RollbackGitRevision) ||
			!productionContractPattern.MatchString(transition.CandidateContractRevision) ||
			!productionContractPattern.MatchString(transition.RollbackContractRevision) {
			return errors.New("deployment manifest contains invalid package release metadata")
		}
		candidate, err := parseNamedPackageIdentity([]byte(transition.CandidateIdentity), transition.CandidatePackageName)
		if err != nil || candidate.Identity != transition.CandidateIdentity {
			return errors.New("deployment manifest contains invalid candidate package identity")
		}
		rollback, err := parseNamedPackageIdentity([]byte(transition.RollbackIdentity), transition.RollbackPackageName)
		if err != nil || rollback.Identity != transition.RollbackIdentity {
			return errors.New("deployment manifest contains invalid rollback package identity")
		}
		if !transition.Changed && (transition.CandidatePackageName != transition.RollbackPackageName || transition.CandidateIdentity != transition.RollbackIdentity ||
			transition.CandidateSHA256 != transition.RollbackSHA256 || transition.CandidateGitRevision != transition.RollbackGitRevision ||
			transition.CandidateContractRevision != transition.RollbackContractRevision) {
			return errors.New("unchanged package manifest identities differ")
		}
		for _, path := range []string{transition.CandidatePath, transition.RollbackPath} {
			if !pathWithinRoot(workspace.stagingDir, path) || filepath.Dir(path) != workspace.stagingDir {
				return errors.New("deployment manifest package path escapes staging")
			}
		}
	}
	for _, digest := range []string{
		manifest.Go.CandidateSHA256, manifest.Go.RollbackSHA256, manifest.Web.CandidateSHA256, manifest.Web.RollbackSHA256,
		manifest.ProbeBinarySHA256, manifest.Frontend.OldIndexSHA256, manifest.Frontend.NewIndexSHA256, manifest.EnvironmentRestoreSHA256,
	} {
		if !productionSHA256Pattern.MatchString(digest) {
			return errors.New("deployment manifest contains an invalid SHA-256")
		}
	}
	if !pathWithinRoot(workspace.stagingDir, manifest.ProbeBinary) || filepath.Dir(manifest.ProbeBinary) != workspace.stagingDir {
		return errors.New("deployment manifest probe binary escapes staging")
	}
	if manifest.ConfigRestorePath != workspace.configRestore {
		return errors.New("deployment manifest configuration rollback path escapes root-only state")
	}
	if manifest.BackupsEnabled {
		if manifest.BackupDir != filepath.Join(runtime.paths.BackupRoot, workspace.id) || !productionSHA256Pattern.MatchString(manifest.DatabaseBackupSHA256) {
			return errors.New("deployment manifest backup path or digest is not release-scoped")
		}
	} else if manifest.BackupDir != "" || manifest.DatabaseBackupSHA256 != "" {
		return errors.New("deployment manifest contains unauthorized optional backup state")
	}
	if manifest.ArmedUTC.IsZero() || !manifest.DeadlineUTC.Equal(manifest.ArmedUTC.Add(productionDefaultRollback)) || manifest.ObservationSeconds < 120 {
		return errors.New("deployment manifest fixed deadline or observation window is invalid")
	}
	webCandidate, candidateErr := parseNamedPackageIdentity([]byte(manifest.Web.CandidateIdentity), productionWebPackageName)
	webRollback, rollbackErr := parseNamedPackageIdentity([]byte(manifest.Web.RollbackIdentity), productionWebPackageName)
	if candidateErr != nil || rollbackErr != nil ||
		manifest.Frontend.NewTarget != frontendTargetFor(productionPackageMetadata{Version: webCandidate.Version, GitRevision: manifest.Web.CandidateGitRevision}) ||
		manifest.Frontend.OldTarget != frontendTargetFor(productionPackageMetadata{Version: webRollback.Version, GitRevision: manifest.Web.RollbackGitRevision}) {
		return errors.New("deployment manifest frontend targets do not match Web package identities")
	}
	for _, target := range []string{manifest.Frontend.OldTarget, manifest.Frontend.NewTarget} {
		if !strings.HasPrefix(target, "releases/") || !releaseIDPattern.MatchString(strings.TrimPrefix(target, "releases/")) {
			return errors.New("deployment manifest contains unsafe frontend target")
		}
	}
	if !isDatabaseSchema(manifest.DatabaseSchema) {
		return errors.New("deployment manifest contains unsafe schema data")
	}
	staged := []struct{ path, digest, label string }{
		{manifest.Go.CandidatePath, manifest.Go.CandidateSHA256, "candidate Go package"},
		{manifest.Go.RollbackPath, manifest.Go.RollbackSHA256, "rollback Go package"},
		{manifest.Web.CandidatePath, manifest.Web.CandidateSHA256, "candidate Web package"},
		{manifest.Web.RollbackPath, manifest.Web.RollbackSHA256, "rollback Web package"},
		{manifest.ProbeBinary, manifest.ProbeBinarySHA256, "probe binary"},
	}
	for _, file := range staged {
		if err := runtime.validateStagedFile(workspace, file.path, file.digest, file.label); err != nil {
			return fmt.Errorf("deployment manifest %s failed validation: %w", file.label, err)
		}
	}
	return nil
}

func (runtime *productionRuntime) requireOwnedSafePath(path string, directory bool) error {
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o022 != 0 || (directory && !info.IsDir()) || (!directory && !info.Mode().IsRegular()) {
		return errors.New("path is missing, writable, or unsafe")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != runtime.requiredOwnerUID || (!directory && stat.Nlink != 1) {
		return errors.New("path ownership or link count is unsafe")
	}
	return nil
}

func readPrivateRegularFile(path string, maximum int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, errors.New("path is not a real regular file")
	}
	if info.Size() > maximum {
		return nil, errors.New("file exceeds the safe size limit")
	}
	return os.ReadFile(path)
}

func requireRealDirectory(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return errors.New("path is not a real directory")
	}
	return nil
}

func ensureRealDirectory(path string, mode os.FileMode) error {
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return errors.New("path is not a real directory")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	} else if err := os.MkdirAll(path, mode); err != nil {
		return err
	}
	return os.Chmod(path, mode)
}

func parseSimpleManifest(content []byte) (map[string]string, error) {
	values := make(map[string]string)
	for _, rawLine := range strings.Split(strings.ReplaceAll(string(content), "\r\n", "\n"), "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, found := strings.Cut(line, "=")
		if !found || strings.TrimSpace(key) == "" {
			return nil, errors.New("invalid marker assignment")
		}
		key = strings.TrimSpace(key)
		if _, exists := values[key]; exists {
			return nil, fmt.Errorf("duplicate marker key %s", key)
		}
		values[key] = strings.TrimSpace(value)
	}
	return values, nil
}

func sha256File(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func parseSingleInt(output []byte, label string) (int64, error) {
	value := strings.TrimSpace(string(output))
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed < 0 {
		return 0, fmt.Errorf("invalid %s value %q", label, value)
	}
	return parsed, nil
}
