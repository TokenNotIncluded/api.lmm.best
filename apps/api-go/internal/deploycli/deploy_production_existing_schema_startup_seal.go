package deploycli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type existingSchemaEnvironmentFile struct {
	Path         string `json:"path"`
	IgnoreErrors bool   `json:"ignore_errors"`
	SHA256       string `json:"sha256"`
}

type existingSchemaStartupSnapshot struct {
	Environment  map[string]string               `json:"environment"`
	Files        []existingSchemaEnvironmentFile `json:"environment_files"`
	FragmentPath string                          `json:"fragment_path"`
	UnitSHA256   string                          `json:"unit_sha256"`
	Commands     map[string]string               `json:"commands"`
}

var existingSchemaEnvironmentFilePattern = regexp.MustCompile(`^(/[^\x00\r\n ]+) \(ignore_errors=(yes|no)\)$`)
var existingSchemaCommandPattern = regexp.MustCompile(`^\{ path=([^ ;\r\n]+) ; argv\[\]=([^;\r\n]+) ; ignore_errors=(yes|no) ;(?:[^\r\n]* )?\}$`)

// These are the exact existing read-only readiness commands. Their arguments
// and order are also covered by the root-sealed startup digest. Shells, curl
// config files, alternate URLs, request bodies and output files are refused.
const existingSchemaReadinessCurl = "/usr/bin/curl --fail --silent --show-error --retry 20 --retry-connrefused --retry-delay 1 --max-time 2 --noproxy 127.0.0.1 --output /dev/null http://127.0.0.1:3000/api/livez"
const existingSchemaReadinessSleep = "/usr/bin/sleep 2"

func existingSchemaCommandSemantics(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	match := existingSchemaCommandPattern.FindStringSubmatch(value)
	if match == nil || strings.Count(value, "{ path=") != 1 || strings.Count(value, "argv[]=") != 1 {
		return "", errors.New("loaded lifecycle command has an unsafe representation")
	}
	argv := strings.TrimSpace(match[2])
	words := strings.Fields(argv)
	if len(words) == 0 || strings.Join(words, " ") != argv || words[0] != match[1] {
		return "", errors.New("loaded lifecycle command arguments are ambiguous")
	}
	return match[1] + "\x00" + argv + "\x00" + match[3], nil
}

func verifyExistingSchemaSealedCommands(loaded map[string]string, binary string) (map[string]string, error) {
	commands := map[string]string{}
	start, err := existingSchemaCommandSemantics(loaded["ExecStart"])
	if err != nil || start != binary+"\x00"+binary+" serve\x00no" {
		return nil, errors.New("loaded production unit does not start only the verified serve command")
	}
	commands["ExecStart"] = start
	pre, err := merchantStoreSealedStartCommand(loaded["ExecStartPre"], binary)
	if err != nil {
		return nil, err
	}
	commands["ExecStartPre"] = pre
	if pre != "" && !strings.HasPrefix(pre, binary+"\x00") {
		if err := merchantStorePrivilegedStartCommand(pre, loaded["ExecStartPreEx"]); err != nil {
			return nil, err
		}
		commands["ExecStartPreEx"] = "privileged"
	}
	for _, key := range []string{"ExecCondition", "ExecStop", "ExecStopPost"} {
		if loaded[key] != "" {
			return nil, errors.New("loaded production unit has an unchecked lifecycle command")
		}
		commands[key] = ""
	}
	hooks := loaded["ExecStartPost"]
	if hooks == "" {
		commands["ExecStartPost"] = ""
		return commands, nil
	}
	rows := strings.Split(hooks, "\n")
	if len(rows) != 2 {
		return nil, errors.New("readiness hooks differ from the canonical read-only pair")
	}
	expected := []string{"/usr/bin/curl\x00" + existingSchemaReadinessCurl + "\x00no", "/usr/bin/sleep\x00" + existingSchemaReadinessSleep + "\x00no"}
	normalized := make([]string, 2)
	for i, row := range rows {
		item, err := existingSchemaCommandSemantics(row)
		if err != nil || item != expected[i] {
			return nil, errors.New("readiness hook is not a canonical read-only command")
		}
		normalized[i] = item
	}
	commands["ExecStartPost"] = strings.Join(normalized, "\n")
	return commands, nil
}

// Only the native read-only startup checker is accepted as a pre-start hook.
// Its entire command (including the one manifest-owned workspace) enters the
// immutable startup digest. The checker cannot start without a live durable
// holder. Only the installed checker or this workspace's candidate checker is
// accepted; shells, arbitrary options and ignore-error hooks are refused.
func merchantStoreSealedStartCommand(value, binary string) (string, error) {
	if value == "" {
		return "", nil
	}
	semantics, err := existingSchemaCommandSemantics(value)
	if err != nil {
		return "", errors.New("merchant startup hook has an unsafe representation")
	}
	parts := strings.Split(semantics, "\x00")
	if len(parts) != 3 || parts[2] != "no" {
		return "", errors.New("merchant startup hook is not the canonical native checker")
	}
	words := strings.Fields(parts[1])
	if len(words) != 6 || words[0] != parts[0] || words[1] != "operator" || words[2] != "production" || words[3] != "writer-start-check" || words[4] != "--workspace" {
		return "", errors.New("merchant startup hook has unsupported commands or arguments")
	}
	workspace := words[5]
	if filepath.Clean(workspace) != workspace || filepath.Dir(workspace) != defaultProductionPaths().WorkRoot || !productionIDPattern.MatchString(filepath.Base(workspace)) {
		return "", errors.New("merchant startup hook workspace is not a canonical native deployment workspace")
	}
	if parts[0] != binary && parts[0] != merchantStoreHeldStartEntrypoint(workspace) && parts[0] != filepath.Join(filepath.Dir(merchantStoreHeldStartEntrypoint(workspace)), deployEngineName) {
		return "", errors.New("merchant startup hook is not its installed or workspace candidate checker")
	}
	return semantics, nil
}

func merchantStoreHeldStartEntrypoint(workspace string) string {
	return filepath.Join(workspace, "tmp", "migrations", "merchant-store-candidate", productionCandidateLinkName)
}

func merchantStorePrivilegedStartCommand(semantic, extended string) error {
	parts := strings.Split(semantic, "\x00")
	match := merchantStorePortableStartExPattern.FindStringSubmatch(extended)
	if len(parts) != 3 || match == nil || strings.Count(extended, "{ path=") != 1 || strings.Count(extended, "argv[]=") != 1 || strings.Count(extended, "flags=") != 1 || match[1] != parts[0] || match[2] != parts[1] {
		return errors.New("merchant startup hook lacks its exact privileged systemd command")
	}
	return nil
}

// Read a stable, singly linked, root-owned file without following any symlink
// component. Private EnvFiles must be exactly 0600; the signed unit is 0644.
func (runtime *productionRuntime) readExistingSchemaSealedFile(path string, private bool) ([]byte, error) {
	clean, err := cleanAbsoluteNonRoot(path)
	if err != nil || clean != path {
		return nil, errors.New("startup seal path is not canonical")
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil || resolved != path {
		return nil, errors.New("startup seal path contains a symlink")
	}
	for parent := filepath.Dir(path); ; parent = filepath.Dir(parent) {
		info, err := os.Lstat(parent)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, errors.New("startup seal ancestor is not a real directory")
		}
		owner, _, ok := deploymentFileOwnership(info)
		trustedSticky := owner == 0 && info.Mode()&os.ModeSticky != 0
		if !ok || (owner != 0 && owner != runtime.requiredOwnerUID) || (info.Mode().Perm()&0022 != 0 && !trustedSticky) {
			return nil, errors.New("startup seal ancestor can be replaced by another user")
		}
		if parent == filepath.Dir(parent) {
			break
		}
	}
	before, err := os.Lstat(path)
	if err != nil || !before.Mode().IsRegular() || before.Size() <= 0 || before.Size() > 1<<20 {
		return nil, errors.New("startup seal file is not a bounded regular file")
	}
	owner, links, ok := deploymentFileOwnership(before)
	expectedMode := os.FileMode(0644)
	if private {
		expectedMode = 0600
	}
	if !ok || owner != runtime.requiredOwnerUID || links != 1 || before.Mode().Perm() != expectedMode {
		return nil, errors.New("startup seal file ownership, links or permissions are unsafe")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, errors.New("startup seal file cannot be opened")
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(before, opened) {
		return nil, errors.New("startup seal file changed while opening")
	}
	content, err := io.ReadAll(io.LimitReader(file, (1<<20)+1))
	after, statErr := os.Lstat(path)
	if statErr == nil {
		afterOwner, afterLinks, afterOK := deploymentFileOwnership(after)
		if !afterOK || afterOwner != runtime.requiredOwnerUID || afterLinks != 1 {
			return nil, errors.New("startup seal ownership or links changed during inspection")
		}
	}
	current, resolveErr := filepath.EvalSymlinks(path)
	if err != nil || statErr != nil || resolveErr != nil || current != path || !os.SameFile(before, after) || !after.Mode().IsRegular() || after.Size() != int64(len(content)) || after.ModTime() != before.ModTime() || after.Mode() != before.Mode() || len(content) > 1<<20 {
		return nil, errors.New("startup seal file changed during inspection")
	}
	return content, nil
}

func startupContentSHA256(content []byte) string {
	value := sha256.Sum256(content)
	return hex.EncodeToString(value[:])
}

func (runtime *productionRuntime) existingSchemaSealedStartup(ctx context.Context, loaded map[string]string) (map[string]string, string, string, error) {
	if loaded["PassEnvironment"] != "" || loaded["UnsetEnvironment"] != "" {
		return nil, "", "", errors.New("loaded production unit has unchecked environment overrides")
	}
	values, err := parseExistingSchemaLoadedEnvironment(loaded["Environment"])
	if err != nil || verifyExistingSchemaStartupEnvironment(values, true) != nil {
		return nil, "", "", errors.New("loaded production unit does not enforce verify-only startup")
	}
	commands, err := verifyExistingSchemaSealedCommands(loaded, runtime.paths.InstalledBinary)
	if err != nil {
		return nil, "", "", err
	}
	return runtime.existingSchemaSealedStartupCommands(ctx, loaded, values, commands)
}

// The portable startup owner supplies only its separately validated typed
// command. Environment-file ordering and file fences remain identical.
func (runtime *productionRuntime) existingSchemaSealedStartupCommands(ctx context.Context, loaded, values, commands map[string]string) (map[string]string, string, string, error) {
	if loaded["PassEnvironment"] != "" || loaded["UnsetEnvironment"] != "" {
		return nil, "", "", errors.New("loaded production unit has unchecked environment overrides")
	}
	if loaded["FragmentPath"] == "" {
		return nil, "", "", errors.New("loaded startup unit has no signed fragment")
	}
	unit, err := runtime.readExistingSchemaSealedFile(loaded["FragmentPath"], false)
	if err != nil {
		return nil, "", "", err
	}
	sealedUnitEnvironment := make(map[string]string, len(values))
	for key, value := range values {
		if key != "GOMEMLIMIT" {
			sealedUnitEnvironment[key] = value
		}
	}
	// GOMEMLIMIT is governed by the existing signed memory-drop-in owner and
	// recognized legacy override retirement, independently of database startup.
	snapshot := existingSchemaStartupSnapshot{Environment: sealedUnitEnvironment, FragmentPath: loaded["FragmentPath"], UnitSHA256: startupContentSHA256(unit), Commands: commands}
	rows := strings.Split(loaded["EnvironmentFiles"], "\n")
	if len(rows) == 0 || len(rows) > 16 {
		return nil, "", "", errors.New("loaded environment file count is unsafe")
	}
	seen := map[string]bool{}
	main := filepath.Join(runtime.paths.ConfigDir, "lmm-api-go.env")
	for i, row := range rows {
		match := existingSchemaEnvironmentFilePattern.FindStringSubmatch(row)
		if match == nil || seen[match[1]] || (i == 0 && match[1] != main) {
			return nil, "", "", errors.New("loaded environment files are duplicated or not canonical")
		}
		seen[match[1]] = true
		content, err := runtime.readExistingSchemaSealedFile(match[1], true)
		if err != nil {
			return nil, "", "", err
		}
		assignments, err := parseProductionEnvironment(content)
		if err != nil {
			return nil, "", "", errors.New("sealed environment file is malformed")
		}
		// EnvironmentFile assignments override unit Environment in systemd order.
		for key, value := range assignments {
			values[key] = value
		}
		snapshot.Files = append(snapshot.Files, existingSchemaEnvironmentFile{Path: match[1], IgnoreErrors: match[2] == "yes", SHA256: startupContentSHA256(content)})
	}
	if verifyExistingSchemaStartupEnvironment(values, true) != nil {
		return nil, "", "", errors.New("effective startup enables migration or financial preparation")
	}
	dsn, err := productionDatabaseURL(values)
	if err != nil {
		return nil, "", "", err
	}
	if err := verifyExistingSchemaLogDatabase(values, dsn); err != nil {
		return nil, "", "", err
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return nil, "", "", errors.New("startup snapshot cannot be sealed")
	}
	return values, startupContentSHA256(encoded), snapshot.UnitSHA256, nil
}

func (runtime *productionRuntime) sealExistingSchemaStartup(ctx context.Context, contract *productionExistingSchemaContract) error {
	if err := validateProductionExistingSchemaContract(contract); err != nil {
		return err
	}
	loaded, err := runtime.loadedExistingSchemaUnit(ctx)
	if err != nil {
		return err
	}
	_, startup, unit, err := runtime.existingSchemaSealedStartup(ctx, loaded)
	if err != nil {
		return err
	}
	contract.StartupSHA256, contract.SignedUnitSHA256 = startup, unit
	if err := runtime.verifyExistingSchemaStartupMode(ctx, productionManifest{SchemaMode: productionSchemaModeVerifyExisting, ExistingSchemaContract: contract}); err != nil {
		contract.StartupSHA256, contract.SignedUnitSHA256 = "", ""
		return err
	}
	return nil
}

// The controller has already verified each exact package against its signed
// release archive. Bind the live unit seal to BOTH package payloads before the
// plan can authorize any mutation, then repeat that binding on target apply.
func (runtime *productionRuntime) verifyExistingSchemaSignedUnitBinding(ctx context.Context, contract *productionExistingSchemaContract, candidate, rollback string) error {
	if contract == nil || contract.StartupSHA256 == "" {
		return nil
	}
	if err := validateProductionExistingSchemaContract(contract); err != nil {
		return err
	}
	for _, path := range []string{candidate, rollback} {
		if path == "" {
			return errors.New("startup seal is not bound to both signed backend packages")
		}
		content, err := runtime.runner.Run(ctx, productionCommand{Name: commandBsdtar, Args: []string{"-xOf", path, "usr/lib/systemd/system/lmm-api.service"}, Timeout: 15 * time.Second, OutputLimit: 1 << 20})
		if err != nil || len(content) == 0 || startupContentSHA256(content) != contract.SignedUnitSHA256 {
			return fmt.Errorf("sealed startup unit differs from signed backend package payload")
		}
	}
	return nil
}

// A sealed same-schema release preserves the already accepted private startup
// bytes. Replaying optional environment hardening would invalidate that seal
// during both upgrade and recovery. Historical/default transactions keep their
// exact previous hardening policy; memory ownership checks remain in all modes.
func (runtime *productionRuntime) hardenProductionTransactionConfiguration(manifest productionManifest) error {
	if manifest.SchemaMode == productionSchemaModeVerifyExisting && manifest.ExistingSchemaContract != nil && manifest.ExistingSchemaContract.StartupSHA256 != "" {
		if _, err := runtime.readExistingSchemaSealedFile(filepath.Join(runtime.paths.ConfigDir, "lmm-api-go.env"), true); err != nil {
			return err
		}
		if err := ensureProductionMemoryDropIn(filepath.Join(runtime.paths.PackagedDropInDir, productionMemoryFileName)); err != nil {
			return err
		}
		return retireKnownMemoryOverrides(runtime.paths.DropInDir)
	}
	return hardenProductionConfiguration(productionHardenOptions{EnvFile: filepath.Join(runtime.paths.ConfigDir, "lmm-api-go.env"), DropInDir: runtime.paths.PackagedDropInDir, OverrideDropInDir: runtime.paths.DropInDir})
}
