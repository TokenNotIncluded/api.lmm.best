package appcli

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const (
	defaultProductionEnvFile       = "/etc/lmm-api-go/lmm-api-go.env"
	defaultProductionDropInDir     = "/etc/systemd/system/lmm-api.service.d"
	defaultPackagedMemoryDropInDir = "/usr/lib/systemd/system/lmm-api.service.d"
	productionMemoryFileName       = "20-memory.conf"
	legacyMemoryGuardFile          = "50-memory-guard.conf"
	legacyProductionMemoryFile     = "80-production-memory.conf"
	legacyEmergencyMemoryFile      = "99-emergency-memory-safety.conf"
	productionMemoryHigh           = "320M"
	productionMemoryMax            = "384M"
	productionMemorySwapMax        = "256M"
	productionGoMemoryLimit        = "256MiB"
)

type productionHardenOptions struct {
	EnvFile           string
	DropInDir         string
	OverrideDropInDir string
}

func runProductionDeploy(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		writeProductionDeployUsage(stderr)
		return ExitUsage
	}
	switch args[0] {
	case "plan":
		return runProductionReleasePlan(args[1:], stdout, stderr)
	case "stage":
		return runProductionReleaseStage(args[1:], stdout, stderr)
	case "promote":
		return runProductionReleasePromote(args[1:], stdout, stderr)
	case "package":
		return runProductionPackage(args[1:], stdout, stderr)
	case "workspace":
		return runProductionWorkspace(args[1:], stdout, stderr)
	case "backup":
		return runProductionBackup(args[1:], stdout, stderr)
	case "harden":
		options, err := parseProductionHardenOptions(args[1:], stderr)
		if errors.Is(err, flag.ErrHelp) {
			return ExitOK
		}
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "%s deploy production harden: %v\n", ProgramName, err)
			return ExitUsage
		}
		if err := hardenProductionConfiguration(options); err != nil {
			_, _ = fmt.Fprintf(stderr, "%s deploy production harden: %v\n", ProgramName, err)
			return ExitError
		}
		_, _ = fmt.Fprintln(stdout, "configuration=hardened")
		_, _ = fmt.Fprintln(stdout, "systemd_reload_required=true")
		return ExitOK
	case "edge-policy":
		return runProductionEdgePolicy(args[1:], stdout, stderr)
	case "dispatch-evidence":
		return runProductionDispatchEvidence(args[1:], stdout, stderr)
	case "apply":
		return runProductionTransaction(args[0], args[1:], stdout, stderr)
	case "status", "confirm", "rollback":
		if productionControllerPlanMode(args[1:]) {
			return runProductionReleaseControllerAction(args[0], args[1:], stdout, stderr)
		}
		return runProductionTransaction(args[0], args[1:], stdout, stderr)
	case "help", "--help", "-h":
		writeProductionDeployUsage(stdout)
		return ExitOK
	default:
		_, _ = fmt.Fprintf(stderr, "%s deploy production: unknown action %q\n", ProgramName, args[0])
		writeProductionDeployUsage(stderr)
		return ExitUsage
	}
}

func productionControllerPlanMode(args []string) bool {
	for _, argument := range args {
		if argument == "--plan" || strings.HasPrefix(argument, "--plan=") {
			return true
		}
	}
	return false
}

func writeProductionDeployUsage(output io.Writer) {
	_, _ = fmt.Fprintf(output, `Usage:
  %s deploy production plan --repo DIR --workspace DIR --deployment-id ID \\
       --go-package FILE --go-release-asset FILE --go-release-bundle FILE \\
       --go-rollback-package FILE --go-rollback-release-asset FILE --go-rollback-release-bundle FILE \\
       --web-package FILE --web-release-asset FILE --web-release-bundle FILE \\
       --web-rollback-package FILE --web-rollback-release-asset FILE --web-rollback-release-bundle FILE \\
       --probe-binary FILE [--operator-binary FILE] [--with-backups --age-recipient-file FILE] [--manual-confirm]
  %s deploy production stage|promote|status|confirm|rollback \\
       --plan FILE --plan-sha256 HEX --confirm api.lmm.best

Target-only recovery commands (normally invoked by the controller):
  %s deploy production workspace create --deployment-id ID
  %s deploy production apply --workspace DIR --operator-user USER \\
       --go-package FILE --go-package-sha256 HEX --go-rollback-package FILE --go-rollback-sha256 HEX \\
       --web-package FILE --web-package-sha256 HEX --web-rollback-package FILE --web-rollback-sha256 HEX \\
       --probe-binary FILE --probe-binary-sha256 HEX --operator-binary FILE --operator-binary-sha256 HEX \\
       --expected-version VERSION [--go-changed] [--web-changed] [--with-backups --backup-dir DIR] [--manual-confirm]
  %s deploy production status|confirm|rollback --workspace DIR

Production Go changes require --with-backups and the verified target, controller, and off-host copies.
Web-only releases may omit backups.
`, ProgramName, ProgramName, ProgramName, ProgramName, ProgramName)
}

func parseProductionHardenOptions(args []string, stderr io.Writer) (productionHardenOptions, error) {
	options := productionHardenOptions{
		EnvFile:           defaultProductionEnvFile,
		DropInDir:         defaultPackagedMemoryDropInDir,
		OverrideDropInDir: defaultProductionDropInDir,
	}
	flags := flag.NewFlagSet("deploy production harden", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(&options.EnvFile, "env-file", options.EnvFile, "production environment file")
	flags.StringVar(&options.DropInDir, "drop-in-dir", options.DropInDir, "package-owned systemd service drop-in directory")
	flags.StringVar(&options.OverrideDropInDir, "override-drop-in-dir", options.OverrideDropInDir, "legacy /etc systemd override directory")
	flags.Usage = func() { writeProductionDeployUsage(stderr) }
	if err := flags.Parse(args); err != nil {
		return productionHardenOptions{}, err
	}
	if flags.NArg() != 0 {
		return productionHardenOptions{}, errors.New("unexpected positional arguments")
	}
	var err error
	options.EnvFile, err = cleanAbsoluteNonRoot(options.EnvFile)
	if err != nil {
		return productionHardenOptions{}, fmt.Errorf("invalid --env-file: %w", err)
	}
	options.DropInDir, err = cleanAbsoluteNonRoot(options.DropInDir)
	if err != nil {
		return productionHardenOptions{}, fmt.Errorf("invalid --drop-in-dir: %w", err)
	}
	options.OverrideDropInDir, err = cleanAbsoluteNonRoot(options.OverrideDropInDir)
	if err != nil {
		return productionHardenOptions{}, fmt.Errorf("invalid --override-drop-in-dir: %w", err)
	}
	return options, nil
}

func hardenProductionConfiguration(options productionHardenOptions) error {
	info, err := os.Lstat(options.EnvFile)
	if err != nil {
		return fmt.Errorf("inspect environment file: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return errors.New("environment file must be a real regular file")
	}
	content, err := os.ReadFile(options.EnvFile)
	if err != nil {
		return fmt.Errorf("read environment file: %w", err)
	}
	hardened := hardenProductionEnvironment(content)
	if err := writeAtomicRegularFile(options.EnvFile, hardened, 0o600); err != nil {
		return fmt.Errorf("write hardened environment: %w", err)
	}

	if err := ensureProductionMemoryDropIn(filepath.Join(options.DropInDir, productionMemoryFileName)); err != nil {
		return err
	}
	return retireKnownMemoryOverrides(options.OverrideDropInDir)
}

func productionMemoryConfig() []byte {
	return []byte(fmt.Sprintf(`[Service]
MemoryAccounting=yes
MemoryHigh=%s
MemoryMax=%s
MemorySwapMax=%s
Environment=GOMEMLIMIT=%s
`, productionMemoryHigh, productionMemoryMax, productionMemorySwapMax, productionGoMemoryLimit))
}

func legacyProductionMemoryConfig() []byte {
	return []byte(fmt.Sprintf(`[Service]
MemoryAccounting=yes
MemoryHigh=%s
MemoryMax=%s
MemorySwapMax=%s
`, productionMemoryHigh, productionMemoryMax, productionMemorySwapMax))
}

func ensureProductionMemoryDropIn(path string) error {
	if err := verifyProductionMemoryDropIn(path); err == nil {
		return nil
	} else if _, statErr := os.Lstat(path); !os.IsNotExist(statErr) {
		return err
	}

	directory := filepath.Dir(path)
	info, err := os.Lstat(directory)
	if os.IsNotExist(err) {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			return fmt.Errorf("create package-owned production memory drop-in directory: %w", err)
		}
	} else if err != nil {
		return fmt.Errorf("inspect package-owned production memory drop-in directory: %w", err)
	} else if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() || info.Mode().Perm()&0o022 != 0 {
		return errors.New("package-owned production memory drop-in directory is unsafe")
	}

	if err := writeAtomicRegularFile(path, productionMemoryConfig(), 0o644); err != nil {
		return fmt.Errorf("create package-owned production memory drop-in: %w", err)
	}
	return verifyProductionMemoryDropIn(path)
}

func verifyProductionMemoryDropIn(path string) error {
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Mode().Perm()&0o022 != 0 {
		return errors.New("package-owned production memory drop-in is missing or unsafe")
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read package-owned production memory drop-in: %w", err)
	}
	if !bytes.Equal(content, productionMemoryConfig()) {
		return errors.New("package-owned production memory drop-in does not exactly set 320M/384M/256M limits")
	}
	return nil
}

func retireKnownMemoryOverrides(root string) error {
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect legacy override directory: %w", err)
	}
	remove := make([]string, 0, 3)
	emergencyConfig := []byte("[Service]\nMemoryHigh=256M\nMemoryMax=288M\nMemorySwapMax=64M\n")
	legacyProductionConfig := legacyProductionMemoryConfig()
	for _, entry := range entries {
		path := filepath.Join(root, entry.Name())
		info, err := os.Lstat(path)
		if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return fmt.Errorf("unknown or unsafe systemd override: %s", path)
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		text := string(content)
		if !strings.Contains(text, "MemoryHigh=") && !strings.Contains(text, "MemoryMax=") &&
			!strings.Contains(text, "MemorySwapMax=") && !strings.Contains(text, "GOMEMLIMIT=") {
			continue
		}
		switch entry.Name() {
		case legacyMemoryGuardFile, legacyProductionMemoryFile:
			if !bytes.Equal(content, productionMemoryConfig()) && !bytes.Equal(content, legacyProductionConfig) {
				return fmt.Errorf("refusing to remove unknown memory override: %s", path)
			}
		case legacyEmergencyMemoryFile:
			if !bytes.Equal(content, emergencyConfig) && !bytes.Equal(content, legacyProductionConfig) {
				return fmt.Errorf("refusing to remove unknown memory override: %s", path)
			}
		default:
			return fmt.Errorf("unknown memory override blocks deployment: %s", path)
		}
		remove = append(remove, path)
	}
	for _, path := range remove {
		if err := os.Remove(path); err != nil {
			return fmt.Errorf("remove recognized legacy memory override: %w", err)
		}
	}
	return nil
}

func retireLegacyEmergencyMemoryDropIn(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect legacy emergency memory drop-in: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return errors.New("legacy emergency memory drop-in must be a real regular file")
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read legacy emergency memory drop-in: %w", err)
	}
	expected := []byte("[Service]\nMemoryHigh=256M\nMemoryMax=288M\nMemorySwapMax=64M\n")
	if !bytes.Equal(content, expected) {
		return errors.New("refusing to retire unknown legacy emergency memory drop-in")
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("remove recognized legacy emergency memory drop-in: %w", err)
	}
	return nil
}

func hardenProductionEnvironment(content []byte) []byte {
	blocked := map[string]struct{}{
		"SESSION_COOKIE_SECURE":      {},
		"SESSION_COOKIE_TRUSTED_URL": {},
		"TRUSTED_PROXIES":            {},
	}
	lines := strings.Split(strings.ReplaceAll(string(content), "\r\n", "\n"), "\n")
	// EnvironmentFile parsing is last-assignment-wins.  Normalize legacy files
	// to that deterministic representation before the transaction parser checks
	// for duplicates; otherwise a harmless historical duplicate blocks every
	// guarded production backup.
	lastAssignment := make(map[string]int)
	for index, line := range lines {
		trimmed := strings.TrimSpace(line)
		key, _, found := strings.Cut(trimmed, "=")
		if !found {
			continue
		}
		key = strings.TrimSpace(key)
		if _, remove := blocked[key]; remove {
			continue
		}
		lastAssignment[key] = index
	}
	kept := make([]string, 0, len(lines)+3)
	for index, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		key, _, found := strings.Cut(trimmed, "=")
		if found {
			key = strings.TrimSpace(key)
			if _, remove := blocked[key]; remove {
				continue
			}
			if lastAssignment[key] != index {
				continue
			}
		}
		kept = append(kept, line)
	}
	kept = append(kept,
		"SESSION_COOKIE_SECURE=true",
		"SESSION_COOKIE_TRUSTED_URL=https://api.lmm.best,https://lmm.best",
		"TRUSTED_PROXIES=127.0.0.1/32,::1/128",
	)
	return []byte(strings.Join(kept, "\n") + "\n")
}

func writeAtomicRegularFile(path string, content []byte, mode fs.FileMode) (returnErr error) {
	parent := filepath.Dir(path)
	temporary, err := os.CreateTemp(parent, "."+filepath.Base(path)+".*.new")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer func() {
		if returnErr != nil {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(mode); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(content); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return err
	}
	return syncDirectory(parent)
}
