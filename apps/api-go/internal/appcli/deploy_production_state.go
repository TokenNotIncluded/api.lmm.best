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
	"time"
)

const (
	productionServiceName                     = "lmm-api.service"
	productionExpectedHost                    = "arch-dmit"
	productionDefaultObservation              = 3 * time.Minute
	productionObservationInterval             = 10 * time.Second
	productionCommandTimeout                  = 2 * time.Minute
	productionProbeTimeout                    = 8 * time.Second
	productionProbeAttempts                   = 45
	productionTransactionFormat               = 8
	productionExistingSchemaTransactionFormat = 9
	productionStatusFormat                    = 2
	productionFrontendReleaseKeep             = 3
	productionTransactionMarker               = "deployment.env"
	productionWorkspaceMarker                 = ".lmm-deploy-workspace"
	productionCandidateLinkName               = "lmm-api"
	productionManifestFilename                = "deployment.json"
	productionStatusFilename                  = "status.json"
	// pi-lens-ignore: go-hardcoded-secrets
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
	Name        string
	Args        []string
	Env         []string
	Dir         string
	Timeout     time.Duration
	Sensitive   bool
	OutputLimit int
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
	portableTool, handled, err := productionSystemToolPath(command.Name)
	if err != nil {
		return nil, err
	}
	if handled {
		process = exec.CommandContext(ctx, portableTool, command.Args...)
	} else {
		switch command.Name {
		case commandAge:
			process = exec.CommandContext(ctx, "/usr/bin/age", command.Args...)
		case commandBsdtar:
			process = exec.CommandContext(ctx, "/usr/bin/bsdtar", command.Args...)
		case commandBun:
			process = exec.CommandContext(ctx, "/usr/bin/bun", command.Args...)
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
		case commandPacman:
			process = exec.CommandContext(ctx, "/usr/bin/pacman", command.Args...)
		case commandPGDump:
			process = exec.CommandContext(ctx, "/usr/bin/pg_dump", command.Args...)
		case commandPGRestore:
			process = exec.CommandContext(ctx, "/usr/bin/pg_restore", command.Args...)
		case commandPSQL:
			process = exec.CommandContext(ctx, "/usr/bin/psql", command.Args...)
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
			process = exec.CommandContext(ctx, "/usr/bin/lmm-api", command.Args...)
		default:
			return nil, fmt.Errorf("command executable is not allowlisted: %q", command.Name)
		}
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
	if command.OutputLimit > 0 {
		process.Stdout = &boundedBillingOutput{buffer: &stdout, limit: command.OutputLimit}
		process.Stderr = &boundedBillingOutput{buffer: &stderr, limit: command.OutputLimit}
	}
	err = process.Run()
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

// Linux distributions install these native tools in different system
// directories. Only these exact root-owned paths are admitted; PATH and an
// operator-provided executable are never used. An unsafe existing first choice
// is an error, rather than an excuse to select a different executable.
func productionSystemToolPath(name string) (string, bool, error) {
	var candidates []string
	switch name {
	case commandCosign:
		candidates = []string{"/usr/bin/cosign", "/usr/local/bin/cosign"}
	case commandNginx:
		candidates = []string{"/usr/bin/nginx", "/usr/sbin/nginx"}
	case commandRunuser:
		candidates = []string{"/usr/bin/runuser", "/usr/sbin/runuser"}
	case "/usr/bin/curl":
		candidates = []string{"/usr/bin/curl"}
	case "systemd-run", "/usr/bin/systemd-run":
		candidates = []string{"/usr/bin/systemd-run"}
	default:
		return "", false, nil
	}
	for _, candidate := range candidates {
		if _, err := os.Lstat(candidate); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return "", true, fmt.Errorf("inspect native system tool %s: %w", candidate, err)
		}
		resolved, err := filepath.EvalSymlinks(candidate)
		if err != nil || !productionSystemToolTargetAllowed(resolved, candidates) {
			return "", true, fmt.Errorf("native system tool %s has an unsafe target", candidate)
		}
		for _, spelling := range []string{candidate, resolved} {
			for path := spelling; ; path = filepath.Dir(path) {
				info, err := os.Lstat(path)
				if err != nil {
					return "", true, fmt.Errorf("native system tool %s has an unavailable ancestor", candidate)
				}
				uid, _, ownershipOK := deploymentFileOwnership(info)
				if !ownershipOK || uid != 0 || info.Mode()&os.ModeSymlink == 0 && info.Mode().Perm()&0022 != 0 {
					return "", true, fmt.Errorf("native system tool %s has unsafe ownership or permissions", candidate)
				}
				if path == "/" {
					break
				}
			}
		}
		info, err := os.Stat(resolved)
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0100 == 0 {
			return "", true, fmt.Errorf("native system tool %s is not an executable regular file", candidate)
		}
		return resolved, true, nil
	}
	return "", true, fmt.Errorf("required native system tool %s is unavailable", name)
}

func productionSystemToolTargetAllowed(resolved string, candidates []string) bool {
	return filepath.Clean(resolved) == resolved && slices.Contains(candidates, resolved)
}

// pi-lens-ignore: go-bare-error
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
	maintenanceHandoff   *productionMaintenanceHandoff
	guardianLease        io.Closer
	guardianAdoptAll     bool
	guardianExtraLocks   []*os.File
	maintenanceReleasing bool

	billingAdmissionClosed        bool
	billingRollback               bool
	billingConnections            func() (int, error)
	billingExecutableSHA256       func(int) (string, error)
	maintenanceProcessEnvironment func(int) ([]byte, error)
	merchantStoreLastWriterCheck  productionMerchantStoreWriterTarget
	merchantStoreAuthority        productionMerchantStoreAuthority
	merchantStoreSocketDirectory  string                 // Test-only path injection; production always uses the fixed /run directory.
	startupMonotonicUS            func() (uint64, error) // Test clock; production uses CLOCK_MONOTONIC.
	startupWait                   func(context.Context, time.Duration) error
	cleanupProcessReferences      func(string) (bool, error)
	paths                         productionPaths
	runner                        productionCommandRunner
	now                           func() time.Time
	sleep                         func(time.Duration)
	effectiveUID                  func() int
	hostname                      func() (string, error)
	probeAttempts                 int
	requiredOwnerUID              uint32
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
	MerchantStoreWriter      *productionMerchantStoreWriterContract
	SchemaMode               string
	ExistingSchemaContract   *productionExistingSchemaContract
	StagedPlanPath           string
	StagedPlanSHA256         string
	MaintenanceHandoffPath   string
	MaintenanceHandoffSHA256 string
	GlobalConfirmationPath   string
	AllAdmissionClosedPath   string
	AllAdmissionClosedSHA256 string
	GlobalConfirmationSHA256 string

	Action               string
	Workspace            string
	OperatorUser         string
	GoPackage            string
	GoPackageSHA256      string
	GoRollbackPackage    string
	GoRollbackSHA256     string
	WebPackage           string
	WebPackageSHA256     string
	WebRollbackPackage   string
	WebRollbackSHA256    string
	GoChanged            bool
	WebChanged           bool
	ProbeBinary          string
	ProbeBinarySHA256    string
	OperatorBinary       string
	OperatorBinarySHA256 string
	ExpectedVersion      string
	BackupDir            string
	WithBackups          bool
	ControllerBackup     controllerBackupBinding
	ObservationWindow    time.Duration
	PreserveEdgePolicy   bool
	Reason               string
}

type productionPackageTransition struct {
	CandidatePackageName                      string `json:"candidate_package_name"`
	RollbackPackageName                       string `json:"rollback_package_name"`
	Changed                                   bool   `json:"changed"`
	CandidatePath                             string `json:"candidate_path"`
	RollbackPath                              string `json:"rollback_path"`
	CandidateIdentity                         string `json:"candidate_identity"`
	RollbackIdentity                          string `json:"rollback_identity"`
	CandidateSHA256                           string `json:"candidate_sha256"`
	RollbackSHA256                            string `json:"rollback_sha256"`
	CandidateGitRevision                      string `json:"candidate_git_revision"`
	RollbackGitRevision                       string `json:"rollback_git_revision"`
	CandidateContractRevision                 string `json:"candidate_contract_revision"`
	RollbackContractRevision                  string `json:"rollback_contract_revision"`
	RollbackOAuthManagedTokenIsolation        bool   `json:"rollback_oauth_managed_token_isolation"`
	RollbackManagedBillingSettlementIsolation bool   `json:"rollback_managed_billing_settlement_isolation"`
}

type productionFrontendTransition struct {
	OldTarget      string `json:"old_target"`
	NewTarget      string `json:"new_target"`
	OldIndexSHA256 string `json:"old_index_sha256"`
	NewIndexSHA256 string `json:"new_index_sha256"`
}

type productionManifest struct {
	MerchantStoreWriter    *productionMerchantStoreWriterContract `json:"merchant_store_writer,omitempty"`
	SchemaMode             string                                 `json:"schema_mode,omitempty"`
	ExistingSchemaContract *productionExistingSchemaContract      `json:"existing_schema_contract,omitempty"`
	SchemaPlanSHA256       string                                 `json:"schema_plan_sha256,omitempty"`
	MaintenanceCapture     *productionMaintenanceCapture          `json:"maintenance_capture,omitempty"`

	MaintenanceHandoff *productionMaintenanceHandoff `json:"maintenance_handoff,omitempty"`

	BillingGate              *productionBillingGate       `json:"billing_gate,omitempty"`
	Format                   int                          `json:"format"`
	DeploymentID             string                       `json:"deployment_id"`
	OperatorUser             string                       `json:"operator_user"`
	Go                       productionPackageTransition  `json:"go"`
	Web                      productionPackageTransition  `json:"web"`
	Frontend                 productionFrontendTransition `json:"frontend"`
	ProbeBinary              string                       `json:"probe_binary"`
	ProbeBinarySHA256        string                       `json:"probe_binary_sha256"`
	OperatorBinary           string                       `json:"operator_binary,omitempty"`
	OperatorBinarySHA256     string                       `json:"operator_binary_sha256,omitempty"`
	ExpectedVersion          string                       `json:"expected_version"`
	OldVersion               string                       `json:"old_version"`
	PreviousProviderTarget   string                       `json:"previous_provider_target,omitempty"`
	NewProviderTarget        string                       `json:"new_provider_target,omitempty"`
	BackupDir                string                       `json:"backup_dir,omitempty"`
	BackupsEnabled           bool                         `json:"backups_enabled"`
	BackupEvidenceFormat     int                          `json:"backup_evidence_format,omitempty"`
	ControllerOnlyBackup     *controllerBackupBinding     `json:"controller_only_backup,omitempty"`
	DatabaseBackupSHA256     string                       `json:"database_backup_sha256,omitempty"`
	TargetBackupSHA256       string                       `json:"target_backup_sha256,omitempty"`
	ControllerBackupSHA256   string                       `json:"controller_backup_sha256,omitempty"`
	OffhostBackupSHA256      string                       `json:"offhost_backup_sha256,omitempty"`
	DatabaseSchema           string                       `json:"database_schema"`
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
	MerchantStoreWriterCapability     int
	Name                              string
	Version                           string
	Identity                          string
	GitRevision                       string
	ContractRevision                  string
	IndexSHA256                       string
	BinarySHA256                      string
	ReleaseAssetSHA256                string
	OAuthManagedTokenIsolation        bool
	ManagedBillingSettlementIsolation bool
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
	return runtime.packageMetadataWithIdentity(ctx, packagePath, identityOutput, packageNames...)
}

// A single-provider startup baseline is explicitly portable. Ordinary package
// selection above still requires the real pacman query and never falls back.
func (runtime *productionRuntime) startupBaselinePackageMetadata(ctx context.Context, packagePath string) (productionPackageMetadata, error) {
	release := productionReleaseRuntime{runner: runtime.runner}
	headers, err := release.archiveMembers(ctx, packagePath)
	if err != nil {
		return productionPackageMetadata{}, err
	}
	header, found := headers[".PKGINFO"]
	if !found || header.Type != "file" || header.Mode != 0644 || header.UID != 0 || header.GID != 0 || header.Link != "" {
		return productionPackageMetadata{}, errors.New("startup baseline package .PKGINFO header is unsafe")
	}
	content, err := runtime.runner.Run(ctx, productionCommand{Name: commandBsdtar, Args: []string{"-xOf", packagePath, ".PKGINFO"}, Timeout: 15 * time.Second, OutputLimit: 1 << 20})
	if err != nil {
		return productionPackageMetadata{}, fmt.Errorf("read startup baseline package identity: %w", err)
	}
	fields, err := parsePackageInfoContent(content)
	if err != nil {
		return productionPackageMetadata{}, err
	}
	if len(fields["pkgname"]) != 1 || fields["pkgname"][0] != productionAURPackageName || len(fields["pkgver"]) != 1 {
		return productionPackageMetadata{}, errors.New("startup baseline package identity is ambiguous or not the official Go package")
	}
	identity := []byte(fields["pkgname"][0] + " " + fields["pkgver"][0])
	return runtime.packageMetadataWithIdentity(ctx, packagePath, identity, productionAURPackageName)
}

func (runtime *productionRuntime) packageMetadataWithIdentity(ctx context.Context, packagePath string, identityOutput []byte, packageNames ...string) (productionPackageMetadata, error) {
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
	if packageName == productionAURPackageName {
		capability, capabilityErr := readMember("OAUTH_MANAGED_TOKEN_CAPABILITY")
		metadata.OAuthManagedTokenIsolation = capabilityErr == nil && capability == "v1"
		billingCapability, billingCapabilityErr := readMember("MANAGED_BILLING_SETTLEMENT_CAPABILITY")
		metadata.ManagedBillingSettlementIsolation = billingCapabilityErr == nil && billingCapability == "v1"
	}
	if packageName == productionAURPackageName || packageName == productionSourcePackageName {
		metadata.MerchantStoreWriterCapability, err = runtime.merchantStorePackageCapability(ctx, packagePath, packageName)
		if err != nil {
			return productionPackageMetadata{}, err
		}
	}
	if packageName == productionWebPackageName {
		index, err := runtime.runner.Run(ctx, productionCommand{Name: commandBsdtar, Args: []string{"-xOf", packagePath, "usr/share/lmm-api-web/frontend-dist/index.html"}})
		if err != nil || len(index) == 0 {
			return productionPackageMetadata{}, errors.New("Web package frontend index is missing")
		}
		digest := sha256.Sum256(index)
		metadata.IndexSHA256 = hex.EncodeToString(digest[:])
	} else {
		providerTarget, providerErr := providerTargetForPackage(packageName)
		if providerErr != nil {
			return productionPackageMetadata{}, providerErr
		}
		binaryMember := "usr/bin/" + providerTarget
		if packageName == productionAURPackageName && metadata.Version == "0.1.69-1" {
			binaryMember = "usr/bin/lmm-api"
		}
		binary, err := runtime.runner.Run(ctx, productionCommand{Name: commandBsdtar, Args: []string{"-xOf", packagePath, binaryMember}})
		if err != nil || len(binary) == 0 {
			return productionPackageMetadata{}, errors.New("backend package provider binary is missing")
		}
		digest := sha256.Sum256(binary)
		metadata.BinarySHA256 = hex.EncodeToString(digest[:])
	}
	return metadata, nil
}

func (runtime *productionRuntime) verifyCanonicalOperator(ctx context.Context) error {
	currentTarget, err := providerLinkState(runtime.paths.InstalledBinary)
	if err != nil {
		return fmt.Errorf("canonical deployment operator link is invalid: %w", err)
	}
	if currentTarget == "legacy-regular" {
		return runtime.verifyLegacyCanonicalOperator(ctx)
	}
	selector := backendRuntime{
		paths: backendPaths{
			Canonical: runtime.paths.InstalledBinary,
			Go:        runtime.paths.LegacyGoBinary,
			Rust:      filepath.Join(filepath.Dir(runtime.paths.InstalledBinary), backendRustName),
		},
		owner:       productionBackendOwner{ctx: ctx, runner: runtime.runner},
		effectiveID: runtime.effectiveUID,
		requiredUID: runtime.requiredOwnerUID,
	}
	provider, err := selector.status()
	if err != nil {
		return fmt.Errorf("canonical deployment operator link is invalid: %w", err)
	}
	identity, err := runtime.runner.Run(ctx, productionCommand{Name: commandPacman, Args: []string{"-Q", provider.Package}, Env: append(os.Environ(), "LC_ALL=C")})
	if err != nil {
		return errors.New("canonical deployment operator package identity is unavailable")
	}
	if _, err := parseNamedPackageIdentity(identity, provider.Package); err != nil {
		return errors.New("canonical deployment operator package identity is invalid")
	}
	integrity, err := runtime.runner.Run(ctx, productionCommand{Name: commandPacman, Args: []string{"-Qkk", provider.Package}, Env: append(os.Environ(), "LC_ALL=C")})
	if err != nil || !packageIntegrityClean(integrity, provider.Package) {
		return errors.New("canonical deployment operator package integrity check failed")
	}
	return nil
}

func (runtime *productionRuntime) verifyLegacyCanonicalOperator(ctx context.Context) error {
	canonical, canonicalErr := os.Lstat(runtime.paths.InstalledBinary)
	provider, providerErr := os.Lstat(runtime.paths.LegacyGoBinary)
	target, targetErr := os.Readlink(runtime.paths.LegacyGoBinary)
	if canonicalErr != nil || !canonical.Mode().IsRegular() || canonical.Mode()&0o111 == 0 || canonical.Mode().Perm()&0o022 != 0 ||
		providerErr != nil || provider.Mode()&os.ModeSymlink == 0 || targetErr != nil || target != filepath.Base(runtime.paths.InstalledBinary) {
		return errors.New("canonical deployment operator legacy layout is invalid")
	}
	identity, err := runtime.runner.Run(ctx, productionCommand{Name: commandPacman, Args: []string{"-Q", productionAURPackageName}, Env: append(os.Environ(), "LC_ALL=C")})
	if err != nil {
		return errors.New("canonical deployment operator package identity is unavailable")
	}
	metadata, err := parseNamedPackageIdentity(identity, productionAURPackageName)
	if err != nil || metadata.Version != "0.1.69-1" {
		return errors.New("canonical deployment operator legacy package identity is invalid")
	}
	integrity, err := runtime.runner.Run(ctx, productionCommand{Name: commandPacman, Args: []string{"-Qkk", productionAURPackageName}, Env: append(os.Environ(), "LC_ALL=C")})
	if err != nil || !packageIntegrityClean(integrity, productionAURPackageName) {
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

// pi-lens-ignore: go-bare-error
func readSafeRegularFile(path string, maximum int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Mode().Perm()&0o022 != 0 || info.Size() > maximum {
		return nil, errors.New("path is not a safe regular file")
	}
	return os.ReadFile(path)
}

type productionStatus struct {
	HandoffSHA256             string `json:"handoff_sha256,omitempty"`
	HandoffRefinementVerified bool   `json:"handoff_refinement_verified,omitempty"`
	PlanSHA256                string `json:"plan_sha256,omitempty"`
	DispatchVerifiedAbsent    bool   `json:"dispatch_verified_absent,omitempty"`
	ProviderSHA256            string `json:"provider_sha256,omitempty"`
	MaintenanceStage          string `json:"maintenance_stage,omitempty"`
	TransitionID              string `json:"transition_id,omitempty"`
	TransitionIntentSHA256    string `json:"transition_intent_sha256,omitempty"`
	CaptureReceiptPath        string `json:"capture_receipt_path,omitempty"`
	CaptureReceiptSHA256      string `json:"capture_receipt_sha256,omitempty"`

	MaintenanceConfirmation      bool `json:"maintenance_confirmation,omitempty"`
	MaintenanceAdmissionReopened bool `json:"maintenance_admission_reopened,omitempty"`

	Format         int       `json:"format"`
	DeploymentID   string    `json:"deployment_id"`
	Phase          string    `json:"phase"`
	Version        string    `json:"version,omitempty"`
	Previous       string    `json:"previous_version,omitempty"`
	Reason         string    `json:"reason,omitempty"`
	Failure        string    `json:"failure,omitempty"`
	UpdatedUTC     time.Time `json:"updated_utc"`
	ObservationSec int64     `json:"observation_seconds,omitempty"`
}

type productionWorkspace struct {
	root          string
	id            string
	stateDir      string
	stagingDir    string
	candidateLink string
	manifestPath  string
	statusPath    string
	probeToken    string
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
		_, _ = fmt.Fprintf(stderr, "%s production %s: %v\n", DeployProgramName, action, err)
		return ExitUsage
	}
	runtime := defaultProductionRuntime()
	status, err := runtime.executeTransaction(context.Background(), options)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s production %s: %v\n", DeployProgramName, action, err)
		return ExitError
	}
	encoded, err := json.MarshalIndent(status, "", "  ")
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s production %s: encode status: %v\n", DeployProgramName, action, err)
		return ExitError
	}
	_, _ = stdout.Write(append(encoded, '\n'))
	return ExitOK
}

func parseProductionTransactionOptions(action string, args []string, stderr io.Writer) (productionTransactionOptions, error) {
	options := productionTransactionOptions{
		Action: action, ObservationWindow: productionDefaultObservation, Reason: "operator-request",
	}
	flags := flag.NewFlagSet(DeployProgramName+" production "+action, flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(&options.Workspace, "workspace", "", "marker-owned target deployment workspace")
	flags.StringVar(&options.MaintenanceHandoffPath, "maintenance-handoff", "", "root-owned immutable maintenance handoff")
	flags.StringVar(&options.MaintenanceHandoffSHA256, "maintenance-handoff-sha256", "", "exact maintenance handoff SHA-256")
	if action == "status" || action == "schema-verify" || action == "apply" {
		flags.StringVar(&options.StagedPlanPath, "staged-plan", "", "immutable plan staged inside the target workspace for absent-dispatch proof")
		flags.StringVar(&options.StagedPlanSHA256, "staged-plan-sha256", "", "immutable staged plan SHA-256")
	}
	if action == "maintenance-release" {
		flags.StringVar(&options.GlobalConfirmationPath, "global-confirmation", "", "root-owned global owner confirmation receipt")
		flags.StringVar(&options.GlobalConfirmationSHA256, "global-confirmation-sha256", "", "exact global confirmation SHA-256")
	}
	if action == "maintenance-stop" {
		flags.StringVar(&options.AllAdmissionClosedPath, "all-admission-closed", "", "sealed all-origin admission closure evidence")
		flags.StringVar(&options.AllAdmissionClosedSHA256, "all-admission-closed-sha256", "", "exact all-origin closure receipt digest")
	}
	if action == "apply" || action == "maintenance-capture" || action == "maintenance-retry" {
		flags.StringVar(&options.SchemaMode, "schema-mode", "", "immutable verify-existing schema policy from the staged release plan")
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
		flags.StringVar(&options.OperatorBinary, "operator-binary", "", "deployment operator binary")
		flags.StringVar(&options.OperatorBinarySHA256, "operator-binary-sha256", "", "operator binary SHA-256")
		flags.StringVar(&options.ExpectedVersion, "expected-version", "", "candidate service version")
		flags.StringVar(&options.BackupDir, "backup-dir", "", "verified target copy from the production three-copy backup set")
		flags.BoolVar(&options.WithBackups, "with-backups", false, "bind explicitly selected verified production backup evidence")
		flags.StringVar(&options.ControllerBackup.PublicKey, "controller-backup-public-key", "", "frozen controller verification public key")
		flags.StringVar(&options.ControllerBackup.PlanSHA256, "release-plan-sha256", "", "immutable controller release-plan SHA-256")
		flags.StringVar(&options.ControllerBackup.ReceiptPath, "controller-backup-receipt", "", "root-owned signed controller-only verification receipt")
		flags.StringVar(&options.ControllerBackup.ReceiptSHA256, "controller-backup-receipt-sha256", "", "signed controller verification receipt SHA-256")
		observationSeconds := int(options.ObservationWindow / time.Second)
		flags.IntVar(&observationSeconds, "observation-seconds", observationSeconds, "stability observation window (120-360)")
		flags.BoolVar(&options.PreserveEdgePolicy, "preserve-edge-policy", false, "preserve the active nginx edge policy")
		if err := flags.Parse(args); err != nil {
			return productionTransactionOptions{}, err
		}
		options.ObservationWindow = time.Duration(observationSeconds) * time.Second
	} else if action == "rollback" {
		flags.StringVar(&options.Reason, "reason", options.Reason, "audit-safe rollback reason")
		if err := flags.Parse(args); err != nil {
			return productionTransactionOptions{}, err
		}
	} else if action == "status" || action == "schema-verify" || action == "confirm" || action == "maintenance-release" || action == "maintenance-close" || action == "maintenance-stop" {
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
	if options.SchemaMode != "" && options.SchemaMode != productionSchemaModeVerifyExisting {
		return productionTransactionOptions{}, errors.New("--schema-mode must be verify-existing or omitted")
	}
	if options.SchemaMode == productionSchemaModeVerifyExisting || action == "schema-verify" {
		if (action != "apply" && action != "schema-verify") || options.MaintenanceHandoffPath != "" || options.MaintenanceHandoffSHA256 != "" ||
			options.StagedPlanPath != filepath.Join(options.Workspace, "staging", productionReleasePlanFilename) || !productionSHA256Pattern.MatchString(options.StagedPlanSHA256) {
			return productionTransactionOptions{}, errors.New("verify-existing requires the exact staged ordinary release plan")
		}
	} else if action == "apply" && (options.StagedPlanPath != "" || options.StagedPlanSHA256 != "") {
		return productionTransactionOptions{}, errors.New("staged schema policy requires --schema-mode verify-existing")
	}
	if (options.MaintenanceHandoffPath == "") != (options.MaintenanceHandoffSHA256 == "") {
		return productionTransactionOptions{}, errors.New("maintenance handoff path and SHA-256 must be supplied together")
	}
	if action == "maintenance-release" && (options.GlobalConfirmationPath == "" || !productionSHA256Pattern.MatchString(options.GlobalConfirmationSHA256)) {
		return productionTransactionOptions{}, errors.New("maintenance release requires exact global confirmation receipt")
	}
	if action == "apply" || action == "maintenance-capture" || action == "maintenance-retry" {
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
		if options.OperatorBinary == "" {
			options.OperatorBinary = options.ProbeBinary
		}
		if options.OperatorBinarySHA256 == "" {
			options.OperatorBinarySHA256 = options.ProbeBinarySHA256
		}
		for _, digest := range []string{options.GoPackageSHA256, options.GoRollbackSHA256, options.WebPackageSHA256, options.WebRollbackSHA256, options.ProbeBinarySHA256, options.OperatorBinarySHA256} {
			if !productionSHA256Pattern.MatchString(digest) {
				return productionTransactionOptions{}, errors.New("all SHA-256 values must be 64 lowercase hexadecimal characters")
			}
		}
		if _, err := cleanAbsoluteNonRoot(options.OperatorBinary); err != nil {
			return productionTransactionOptions{}, fmt.Errorf("invalid --operator-binary: %w", err)
		}
		if options.OperatorBinary == "" || options.ProbeBinary == "" {
			return productionTransactionOptions{}, errors.New("--operator-binary and --probe-binary are required")
		}
		if !productionSHA256Pattern.MatchString(options.OperatorBinarySHA256) {
			return productionTransactionOptions{}, errors.New("operator binary SHA-256 must be 64 lowercase hexadecimal characters")
		}
		if options.OperatorUser != productionOperatorUser {
			return productionTransactionOptions{}, fmt.Errorf("--operator-user must be the package-owned %s account", productionOperatorUser)
		}
		if !productionVersionPattern.MatchString(options.ExpectedVersion) {
			return productionTransactionOptions{}, errors.New("invalid --expected-version")
		}
		if options.ObservationWindow < 2*time.Minute || options.ObservationWindow > 6*time.Minute {
			return productionTransactionOptions{}, errors.New("--observation-seconds must be between 120 and 360")
		}
		paths := map[string]*string{
			"--go-package": &options.GoPackage, "--go-rollback-package": &options.GoRollbackPackage,
			"--web-package": &options.WebPackage, "--web-rollback-package": &options.WebRollbackPackage,
			"--probe-binary": &options.ProbeBinary, "--operator-binary": &options.OperatorBinary,
		}
		for label, value := range paths {
			clean, err := cleanAbsoluteNonRoot(*value)
			if err != nil {
				return productionTransactionOptions{}, fmt.Errorf("invalid %s: %w", label, err)
			}
			*value = clean
		}
		if err := validateControllerBackupTransactionOptions(options); err != nil {
			return productionTransactionOptions{}, err
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
	var workspace productionWorkspace
	var err error
	// Resolve the binding through inspection first. An unstopped post intent
	// cannot cause even a state-directory preparation before its refusal.
	workspace, err = runtime.openWorkspaceForInspection(options.Workspace)
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
	if options.MaintenanceHandoffPath != "" {
		if err := runtime.setMaintenanceHandoff(options.MaintenanceHandoffPath, options.MaintenanceHandoffSHA256); err != nil {
			return productionStatus{}, err
		}
	} else if options.Action != "apply" && options.Action != "maintenance-capture" && options.Action != "maintenance-retry" {
		if manifest, err := runtime.readManifestSchema(workspace); err == nil && manifest.MaintenanceHandoff != nil {
			if err := runtime.setMaintenanceHandoff(manifest.MaintenanceHandoff.Path, manifest.MaintenanceHandoff.SHA256); err != nil {
				return productionStatus{}, err
			}
		}
	}
	if options.Action != "status" {
		if err := runtime.refuseUnstoppedPostMutation(); err != nil {
			return productionStatus{}, err
		}
		if options.Action != "schema-verify" {
			workspace, err = runtime.openWorkspace(options.Workspace)
			if err != nil {
				return productionStatus{}, err
			}
		}
	}
	if options.Action == "status" && runtime.maintenanceHandoff != nil {
		if status, terminal, err := runtime.readTerminalMaintenanceStatus(ctx, workspace); terminal || err != nil {
			return status, err
		}
	}
	lock, err := runtime.acquireGlobalLock(ctx)
	if err != nil {
		return productionStatus{}, err
	}
	defer runtime.releaseGlobalLock(lock)

	switch options.Action {
	case "schema-verify":
		if runtime.maintenanceHandoff != nil {
			return productionStatus{}, errors.New("verify-existing cannot use a financial maintenance handoff")
		}
		plan, err := loadStagedProductionExistingSchemaPlan(workspace, options.StagedPlanPath, options.StagedPlanSHA256)
		if err != nil {
			return productionStatus{}, err
		}
		if err := runtime.verifyExistingSchemaLifecycle(ctx, productionManifest{SchemaMode: plan.SchemaMode, ExistingSchemaContract: plan.ExistingSchemaContract, DatabaseSchema: plan.ExistingSchemaContract.Schema}); err != nil {
			return productionStatus{}, err
		}
		return productionStatus{Format: productionStatusFormat, DeploymentID: workspace.id, PlanSHA256: options.StagedPlanSHA256, Version: plan.ExpectedVersion, Phase: "SCHEMA_VERIFIED"}, nil
	case "apply", "maintenance-capture", "maintenance-retry":
		return runtime.apply(ctx, workspace, options)
	case "maintenance-close":
		return runtime.maintenanceClose(ctx, workspace)
	case "maintenance-stop":
		return runtime.maintenanceStop(ctx, workspace, options.AllAdmissionClosedPath, options.AllAdmissionClosedSHA256)
	case "status":
		status, err := runtime.readStatus(workspace)
		if err == nil {
			return status, nil
		}
		if errors.Is(err, os.ErrNotExist) && runtime.maintenanceHandoff != nil {
			return runtime.maintenanceUndispatchedStatus(ctx, workspace, options)
		}
		return productionStatus{}, err
	case "confirm":
		return runtime.confirm(ctx, workspace)
	case "rollback":
		return runtime.rollback(ctx, workspace, options.Reason)
	case "maintenance-release":
		return runtime.maintenanceRelease(ctx, workspace, options.GlobalConfirmationPath, options.GlobalConfirmationSHA256)
	default:
		return productionStatus{}, fmt.Errorf("unsupported action %q", options.Action)
	}
}

func (runtime *productionRuntime) openWorkspace(root string) (productionWorkspace, error) {
	return runtime.openWorkspaceWithMode(root, true)
}

func (runtime *productionRuntime) openWorkspaceForInspection(root string) (productionWorkspace, error) {
	return runtime.openWorkspaceWithMode(root, false)
}

func (runtime *productionRuntime) openWorkspaceWithMode(root string, requireStaging bool) (productionWorkspace, error) {
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
	if requireStaging {
		if err := ensureRealDirectory(stateDir, 0o700); err != nil {
			return productionWorkspace{}, fmt.Errorf("prepare deployment state: %w", err)
		}
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
	if requireStaging {
		if err := runtime.requireOwnedSafePath(stagingDir, true); err != nil {
			return productionWorkspace{}, fmt.Errorf("validate deployment staging: %w", err)
		}
	} else if _, err := os.Lstat(stagingDir); err == nil {
		if err := runtime.requireOwnedSafePath(stagingDir, true); err != nil {
			return productionWorkspace{}, fmt.Errorf("validate deployment staging: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return productionWorkspace{}, fmt.Errorf("inspect deployment staging: %w", err)
	}
	return productionWorkspace{
		root:          root,
		id:            id,
		stateDir:      stateDir,
		stagingDir:    stagingDir,
		candidateLink: filepath.Join(stagingDir, productionCandidateLinkName),
		manifestPath:  filepath.Join(stateDir, productionManifestFilename),
		statusPath:    filepath.Join(stateDir, productionStatusFilename),
		probeToken:    filepath.Join(stateDir, productionProbeTokenFilename),
		configRestore: filepath.Join(stateDir, productionConfigRestoreDirname),
	}, nil
}

func (runtime *productionRuntime) acquireGlobalLock(ctx context.Context) (*os.File, error) {
	if runtime.maintenanceHandoff != nil {
		if runtime.guardianAdoptAll {
			files, lease, err := receiveMaintenanceAllLocks(ctx, runtime.maintenanceHandoff, runtime.paths.GlobalLock, uint32(runtime.effectiveUID()))
			if err != nil {
				return nil, err
			}
			runtime.guardianLease = lease
			runtime.guardianExtraLocks = files[1:]
			return files[0], nil
		}
		file, lease, err := receiveMaintenanceLock(ctx, runtime.maintenanceHandoff, runtime.paths.GlobalLock, uint32(runtime.effectiveUID()))
		if err != nil {
			return nil, err
		}
		runtime.guardianLease = lease
		return file, nil
	}
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
		acquired, err := tryDeploymentFileLock(lock)
		if err != nil {
			_ = lock.Close()
			return nil, fmt.Errorf("lock production deployment: %w", err)
		}
		if acquired {
			return lock, nil
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
	if runtime.maintenanceHandoff != nil {
		status.MaintenanceStage = runtime.maintenanceHandoff.Stage
		status.TransitionID = runtime.maintenanceHandoff.TransitionID
		status.TransitionIntentSHA256 = runtime.maintenanceHandoff.TransitionIntentSHA256
		status.ProviderSHA256 = runtime.maintenanceHandoff.ProviderSHA256
		status.HandoffSHA256 = runtime.maintenanceHandoff.SHA256
	}
	status.Format = productionStatusFormat
	status.DeploymentID = workspace.id
	status.UpdatedUTC = runtime.now().UTC().Truncate(time.Second)
	encoded, err := json.MarshalIndent(status, "", "  ")
	if err != nil {
		return err
	}
	if err := writeAtomicRegularFile(workspace.statusPath, append(encoded, '\n'), 0o600); err != nil {
		return err
	}
	// Public copy is cosmetic: a publication failure must never stop recovery.
	if err := runtime.publishServicePhase(workspace.id, status.Phase); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "public maintenance status unavailable: %v\n", err)
	}
	return nil
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
	if manifest.SchemaMode == productionSchemaModeVerifyExisting {
		manifest.Format = productionExistingSchemaTransactionFormat
	} else {
		if manifest.SchemaMode != "" || manifest.Format == productionExistingSchemaTransactionFormat || manifest.ExistingSchemaContract != nil || manifest.SchemaPlanSHA256 != "" {
			return errors.New("cannot write a downgraded or unsupported existing-schema policy")
		}
		manifest.Format = productionTransactionFormat
	}
	if err := validateProductionExistingSchemaManifest(manifest); err != nil {
		return err
	}
	manifest.DeploymentID = workspace.id
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomicRegularFile(workspace.manifestPath, append(encoded, '\n'), 0o600)
}

func (runtime *productionRuntime) readManifest(workspace productionWorkspace) (productionManifest, error) {
	manifest, err := runtime.readManifestSchema(workspace)
	if err != nil {
		return productionManifest{}, err
	}
	if err := runtime.validateManifestArtifacts(workspace, manifest); err != nil {
		return productionManifest{}, err
	}
	return manifest, nil
}

func (runtime *productionRuntime) readManifestForRollback(workspace productionWorkspace) (productionManifest, error) {
	manifest, err := runtime.readManifestSchema(workspace)
	if err != nil {
		return productionManifest{}, err
	}
	for _, file := range []struct {
		path, digest, label string
		changed             bool
	}{
		{manifest.Go.RollbackPath, manifest.Go.RollbackSHA256, "rollback Go package", manifest.Go.Changed},
		{manifest.Web.RollbackPath, manifest.Web.RollbackSHA256, "rollback Web package", manifest.Web.Changed},
	} {
		if !file.changed {
			continue
		}
		if err := runtime.validateStagedFile(workspace, file.path, file.digest, file.label); err != nil {
			return productionManifest{}, fmt.Errorf("deployment manifest %s failed validation: %w", file.label, err)
		}
	}
	return manifest, nil
}

func (runtime *productionRuntime) readManifestSchema(workspace productionWorkspace) (productionManifest, error) {
	content, err := readPrivateRegularFile(workspace.manifestPath, 256<<10)
	if err != nil {
		return productionManifest{}, fmt.Errorf("read deployment manifest: %w", err)
	}
	var manifest productionManifest
	if err := json.Unmarshal(content, &manifest); err != nil {
		return productionManifest{}, fmt.Errorf("decode deployment manifest: %w", err)
	}
	if manifest.Format == productionExistingSchemaTransactionFormat {
		if err := decodeControllerBackupJSON(content, &manifest); err != nil {
			return productionManifest{}, fmt.Errorf("decode existing-schema deployment manifest: %w", err)
		}
	}
	if (manifest.Format != productionTransactionFormat && manifest.Format != productionExistingSchemaTransactionFormat) || manifest.DeploymentID != workspace.id {
		return productionManifest{}, errors.New("deployment manifest identity is invalid")
	}
	if err := runtime.validateManifestSchema(workspace, manifest); err != nil {
		return productionManifest{}, err
	}
	if manifest.MerchantStoreWriter != nil {
		if err := validateMerchantStoreWriterContract(manifest.MerchantStoreWriter); err != nil {
			return productionManifest{}, err
		}
	}
	if err := validateProductionExistingSchemaManifestPlan(workspace, manifest); err != nil {
		return productionManifest{}, err
	}
	return manifest, nil
}

func providerTargetForPackage(name string) (string, error) {
	switch name {
	case "lmm-api-go", "lmm-api-go-bin", "lmm-api-go-git":
		return backendGoName, nil
	case "lmm-api-rs", "lmm-api-rs-bin", "lmm-api-rs-git":
		return backendRustName, nil
	default:
		return "", fmt.Errorf("unsupported backend provider package %q", name)
	}
}

func (runtime *productionRuntime) validateManifest(workspace productionWorkspace, manifest productionManifest) error {
	if err := runtime.validateManifestSchema(workspace, manifest); err != nil {
		return err
	}
	return runtime.validateManifestArtifacts(workspace, manifest)
}

func (runtime *productionRuntime) validateManifestSchema(workspace productionWorkspace, manifest productionManifest) error {
	if err := validateProductionExistingSchemaManifest(manifest); err != nil {
		return err
	}
	if !productionVersionPattern.MatchString(manifest.ExpectedVersion) || !productionVersionPattern.MatchString(manifest.OldVersion) ||
		manifest.OperatorUser != productionOperatorUser {
		return errors.New("deployment manifest contains invalid release or operator identity")
	}
	candidateProviderTarget, candidateProviderErr := providerTargetForPackage(manifest.Go.CandidatePackageName)
	rollbackProviderTarget, rollbackProviderErr := providerTargetForPackage(manifest.Go.RollbackPackageName)
	if candidateProviderErr != nil || rollbackProviderErr != nil ||
		manifest.Web.CandidatePackageName != productionWebPackageName || manifest.Web.RollbackPackageName != productionWebPackageName ||
		manifest.Go.CandidateContractRevision != manifest.Web.CandidateContractRevision ||
		manifest.Go.RollbackContractRevision != manifest.Web.RollbackContractRevision {
		return errors.New("deployment manifest backend/Web package or contract pair mismatch")
	}
	if manifest.NewProviderTarget != candidateProviderTarget {
		return errors.New("deployment manifest candidate provider target is invalid")
	}
	switch manifest.PreviousProviderTarget {
	case backendGoName, backendRustName:
		if manifest.PreviousProviderTarget != rollbackProviderTarget {
			return errors.New("deployment manifest rollback provider target is invalid")
		}
	case "legacy-regular":
		if manifest.Go.RollbackPackageName != productionAURPackageName || manifest.Go.RollbackIdentity != productionAURPackageName+" 0.1.69-1" {
			return errors.New("deployment manifest legacy provider evidence is invalid")
		}
	case "missing":
	default:
		return errors.New("deployment manifest previous provider target is invalid")
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
		manifest.ProbeBinarySHA256, manifest.OperatorBinarySHA256, manifest.Frontend.OldIndexSHA256, manifest.Frontend.NewIndexSHA256, manifest.EnvironmentRestoreSHA256,
	} {
		if !productionSHA256Pattern.MatchString(digest) {
			return errors.New("deployment manifest contains an invalid SHA-256")
		}
	}
	if manifest.ProbeBinary != manifest.OperatorBinary || manifest.ProbeBinarySHA256 != manifest.OperatorBinarySHA256 ||
		manifest.ProbeBinary != filepath.Join(workspace.stagingDir, backendGoName) {
		return errors.New("deployment manifest candidate entrypoint is invalid")
	}
	if manifest.ConfigRestorePath != workspace.configRestore {
		return errors.New("deployment manifest configuration rollback path escapes root-only state")
	}
	if manifest.BackupEvidenceFormat == controllerBackupEvidenceFormat {
		if err := validateControllerBackupBinding(workspace, manifest); err != nil {
			return err
		}
	} else if manifest.BackupsEnabled {
		if manifest.ControllerOnlyBackup != nil {
			return errors.New("legacy backup evidence cannot contain a controller-only binding")
		}
		if manifest.BackupDir != filepath.Join(runtime.paths.BackupRoot, workspace.id) || !productionSHA256Pattern.MatchString(manifest.DatabaseBackupSHA256) {
			return errors.New("deployment manifest backup path or digest is not release-scoped")
		}
		boundDigests := productionSHA256Pattern.MatchString(manifest.TargetBackupSHA256) &&
			productionSHA256Pattern.MatchString(manifest.ControllerBackupSHA256) &&
			productionSHA256Pattern.MatchString(manifest.OffhostBackupSHA256)
		legacyDigests := manifest.TargetBackupSHA256 == "" && manifest.ControllerBackupSHA256 == "" && manifest.OffhostBackupSHA256 == ""
		if (manifest.BackupEvidenceFormat == 2 && !boundDigests) ||
			(manifest.BackupEvidenceFormat == 0 && !legacyDigests) ||
			(manifest.BackupEvidenceFormat != 0 && manifest.BackupEvidenceFormat != 2) {
			return errors.New("deployment manifest external backup digests are incomplete")
		}
	} else if manifest.ControllerOnlyBackup != nil || manifest.BackupDir != "" || manifest.BackupEvidenceFormat != 0 || manifest.DatabaseBackupSHA256 != "" || manifest.TargetBackupSHA256 != "" || manifest.ControllerBackupSHA256 != "" || manifest.OffhostBackupSHA256 != "" {
		return errors.New("deployment manifest contains unauthorized optional backup state")
	}
	if manifest.ObservationSeconds < 120 || manifest.ObservationSeconds > 360 {
		return errors.New("deployment manifest observation window is invalid")
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
	return nil
}

func productionManifestSupportsControllerBackups(manifest productionManifest) bool {
	return manifest.Format == productionTransactionFormat || manifest.Format == productionExistingSchemaTransactionFormat
}

func validateProductionExistingSchemaManifest(manifest productionManifest) error {
	if manifest.Format != productionExistingSchemaTransactionFormat {
		if manifest.SchemaMode != "" || manifest.ExistingSchemaContract != nil || manifest.SchemaPlanSHA256 != "" {
			return errors.New("historical deployment manifests cannot contain an existing-schema policy")
		}
		return nil
	}
	if manifest.SchemaMode != productionSchemaModeVerifyExisting || !manifest.Go.Changed || manifest.MaintenanceHandoff != nil ||
		manifest.Go.CandidateContractRevision != manifest.Go.RollbackContractRevision || !productionSHA256Pattern.MatchString(manifest.SchemaPlanSHA256) {
		return errors.New("verify-existing deployment manifest policy is invalid")
	}
	if err := validateProductionExistingSchemaContract(manifest.ExistingSchemaContract); err != nil {
		return err
	}
	if manifest.DatabaseSchema != manifest.ExistingSchemaContract.Schema {
		return errors.New("verified existing schema does not match migration search path")
	}
	return nil
}

func loadStagedProductionExistingSchemaPlan(workspace productionWorkspace, path, expectedSHA256 string) (productionReleasePlan, error) {
	var plan productionReleasePlan
	if path != filepath.Join(workspace.stagingDir, productionReleasePlanFilename) || !productionSHA256Pattern.MatchString(expectedSHA256) {
		return productionReleasePlan{}, errors.New("existing-schema staged plan identity is invalid")
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o022 != 0 {
		return plan, errors.New("staged existing-schema plan is not a frozen regular file")
	}
	owner, links, ok := deploymentFileOwnership(info)
	if !ok || owner != uint32(os.Geteuid()) || links != 1 {
		return plan, errors.New("staged existing-schema plan ownership or hard links are unsafe")
	}
	raw, err := readPrivateRegularFile(path, 2<<20)
	if err != nil || fmt.Sprintf("%x", sha256Bytes(raw)) != expectedSHA256 {
		return productionReleasePlan{}, errors.New("existing-schema staged plan digest mismatch")
	}
	if err := decodeControllerBackupJSON(raw, &plan); err != nil {
		return productionReleasePlan{}, errors.New("existing-schema staged plan JSON is invalid")
	}
	if plan.DeploymentID != workspace.id || plan.Format != productionExistingSchemaPlanFormat {
		return productionReleasePlan{}, errors.New("existing-schema staged plan deployment differs")
	}
	if err := validateProductionReleasePlan(plan); err != nil {
		return productionReleasePlan{}, err
	}
	canonical, err := canonicalProductionReleasePlan(plan)
	if err != nil || !bytes.Equal(canonical, raw) {
		return productionReleasePlan{}, errors.New("existing-schema staged plan is not canonical")
	}
	return plan, nil
}

func validateProductionExistingSchemaManifestPlan(workspace productionWorkspace, manifest productionManifest) error {
	path := filepath.Join(workspace.stagingDir, productionReleasePlanFilename)
	if manifest.SchemaMode != productionSchemaModeVerifyExisting {
		// Historical transactions retain their original recovery behavior. A
		// staged new policy cannot silently become an historical transaction.
		raw, err := readPrivateRegularFile(path, 2<<20)
		if err != nil {
			// Legacy recovery never required a readable controller plan. The
			// new manifest carries its own mandatory plan seal below.
			return nil
		}
		var policy struct {
			Format     int    `json:"format"`
			SchemaMode string `json:"schema_mode"`
		}
		if err := json.Unmarshal(raw, &policy); err != nil {
			return nil
		}
		if policy.Format == productionExistingSchemaPlanFormat || policy.SchemaMode != "" {
			return errors.New("deployment manifest lost its immutable existing-schema policy")
		}
		return nil
	}
	plan, err := loadStagedProductionExistingSchemaPlan(workspace, path, manifest.SchemaPlanSHA256)
	if err != nil {
		return err
	}
	if (manifest.MerchantStoreWriter == nil) != (plan.MerchantStoreWriter == nil) ||
		(manifest.MerchantStoreWriter != nil && *manifest.MerchantStoreWriter != *plan.MerchantStoreWriter) {
		return errors.New("merchant writer manifest qualification differs from the immutable release plan")
	}
	if manifest.ExistingSchemaContract == nil || *manifest.ExistingSchemaContract != *plan.ExistingSchemaContract ||
		manifest.OperatorUser != plan.OperatorUser || manifest.ExpectedVersion != plan.ExpectedVersion ||
		manifest.Go.Changed != plan.GoChanged || manifest.Web.Changed != plan.WebChanged ||
		manifest.BackupsEnabled != plan.WithBackups || manifest.PreserveEdgePolicy != plan.PreserveEdgePolicy ||
		manifest.ObservationSeconds != int64(plan.ObservationSeconds) ||
		manifest.ProbeBinarySHA256 != plan.ProbeBinary.SHA256 || manifest.OperatorBinarySHA256 != plan.OperatorBinary.SHA256 {
		return errors.New("existing-schema manifest differs from the immutable release plan")
	}
	for _, pair := range []struct {
		transition          productionPackageTransition
		candidate, rollback productionReleasePackagePlan
	}{{manifest.Go, plan.GoCandidate, plan.GoRollback}, {manifest.Web, plan.WebCandidate, plan.WebRollback}} {
		if pair.transition.CandidatePath != filepath.Join(workspace.stagingDir, filepath.Base(pair.candidate.PackagePath)) ||
			pair.transition.RollbackPath != filepath.Join(workspace.stagingDir, filepath.Base(pair.rollback.PackagePath)) ||
			pair.transition.CandidatePackageName != pair.candidate.Name || pair.transition.RollbackPackageName != pair.rollback.Name ||
			pair.transition.CandidateIdentity != pair.candidate.Identity || pair.transition.RollbackIdentity != pair.rollback.Identity ||
			pair.transition.CandidateSHA256 != pair.candidate.PackageSHA256 || pair.transition.RollbackSHA256 != pair.rollback.PackageSHA256 ||
			pair.transition.CandidateGitRevision != pair.candidate.GitRevision || pair.transition.RollbackGitRevision != pair.rollback.GitRevision ||
			pair.transition.CandidateContractRevision != pair.candidate.ContractRevision || pair.transition.RollbackContractRevision != pair.rollback.ContractRevision {
			return errors.New("existing-schema manifest package differs from the immutable release plan")
		}
	}
	if plan.WithBackups && (manifest.ControllerOnlyBackup == nil || manifest.ControllerOnlyBackup.PublicKey != plan.ControllerBackupPublicKey ||
		manifest.ControllerOnlyBackup.PlanSHA256 != manifest.SchemaPlanSHA256) {
		return errors.New("existing-schema manifest backup differs from the immutable release plan")
	}
	return nil
}

func (runtime *productionRuntime) validateManifestArtifacts(workspace productionWorkspace, manifest productionManifest) error {
	staged := []struct{ path, digest, label string }{
		{manifest.Go.CandidatePath, manifest.Go.CandidateSHA256, "candidate Go package"},
		{manifest.Go.RollbackPath, manifest.Go.RollbackSHA256, "rollback Go package"},
		{manifest.Web.CandidatePath, manifest.Web.CandidateSHA256, "candidate Web package"},
		{manifest.Web.RollbackPath, manifest.Web.RollbackSHA256, "rollback Web package"},
	}
	for _, file := range staged {
		if err := runtime.validateStagedFile(workspace, file.path, file.digest, file.label); err != nil {
			return fmt.Errorf("deployment manifest %s failed validation: %w", file.label, err)
		}
	}
	if _, err := runtime.validateCandidateEntrypoint(workspace, manifest.ProbeBinary, manifest.ProbeBinarySHA256); err != nil {
		return fmt.Errorf("deployment manifest candidate entrypoint failed validation: %w", err)
	}
	return nil
}

func (runtime *productionRuntime) requireOwnedSafePath(path string, directory bool) error {
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o022 != 0 || (directory && !info.IsDir()) || (!directory && !info.Mode().IsRegular()) {
		return errors.New("path is missing, writable, or unsafe")
	}
	uid, linkCount, ok := deploymentFileOwnership(info)
	if !ok || uid != runtime.requiredOwnerUID || (!directory && linkCount != 1) {
		return errors.New("path ownership or link count is unsafe")
	}
	return nil
}

// pi-lens-ignore: go-bare-error
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

// pi-lens-ignore: go-bare-error
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

// pi-lens-ignore: go-bare-error
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
