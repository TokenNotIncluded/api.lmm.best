package appcli

import (
	"archive/tar"
	"bufio"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

var productionEnvironmentKeyPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func (runtime *productionRuntime) validateTransactionLock(workspace productionWorkspace) error {
	if err := runtime.requireOwnedSafePath(runtime.paths.TransactionLock, true); err != nil {
		return fmt.Errorf("deployment transaction lock is missing or unsafe: %w", err)
	}
	lockInfo, err := os.Lstat(runtime.paths.TransactionLock)
	if err != nil {
		return fmt.Errorf("inspect deployment transaction lock: %w", err)
	}
	if lockInfo.Mode().Perm() != 0o700 {
		return errors.New("deployment transaction lock must remain root-only")
	}
	markerPath := filepath.Join(runtime.paths.TransactionLock, productionTransactionMarker)
	if err := runtime.requireOwnedSafePath(markerPath, false); err != nil {
		return fmt.Errorf("deployment transaction marker is unsafe: %w", err)
	}
	content, err := readPrivateRegularFile(markerPath, 16<<10)
	if err != nil {
		return fmt.Errorf("read deployment transaction lock: %w", err)
	}
	values, err := parseSimpleManifest(content)
	if err != nil {
		return fmt.Errorf("parse deployment transaction lock: %w", err)
	}
	if values["deployment_id"] != workspace.id || values["status"] != "ACTIVE" {
		return errors.New("deployment transaction lock is owned by another or inactive deployment")
	}
	return nil
}

func (runtime *productionRuntime) releaseTransactionLock(workspace productionWorkspace) error {
	if _, err := os.Lstat(runtime.paths.TransactionLock); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return fmt.Errorf("inspect deployment transaction lock: %w", err)
	}
	if err := runtime.validateTransactionLock(workspace); err != nil {
		return err
	}
	markerPath := filepath.Join(runtime.paths.TransactionLock, productionTransactionMarker)
	if err := os.Remove(markerPath); err != nil {
		return fmt.Errorf("remove deployment transaction marker: %w", err)
	}
	if err := os.Remove(runtime.paths.TransactionLock); err != nil {
		return fmt.Errorf("remove empty deployment transaction lock: %w", err)
	}
	return nil
}

func (runtime *productionRuntime) validateStagedFile(workspace productionWorkspace, path, expectedSHA256, label string) error {
	if !pathWithinRoot(workspace.stagingDir, path) || filepath.Dir(path) != workspace.stagingDir {
		return fmt.Errorf("%s must be a direct child of deployment staging", label)
	}
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() == 0 || info.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("%s is missing, empty, writable, or unsafe", label)
	}
	_, linkCount, ok := deploymentFileOwnership(info)
	if !ok || linkCount != 1 {
		return fmt.Errorf("%s must not be hard-linked", label)
	}
	if !productionSHA256Pattern.MatchString(expectedSHA256) {
		return fmt.Errorf("%s SHA-256 is invalid", label)
	}
	actual, err := sha256File(path)
	if err != nil {
		return fmt.Errorf("hash %s: %w", label, err)
	}
	if actual != expectedSHA256 {
		return fmt.Errorf("%s SHA-256 mismatch", label)
	}
	return nil
}

func (runtime *productionRuntime) validateBackupSet(ctx context.Context, workspace productionWorkspace, backupDir string) ([]byte, error) {
	expected := filepath.Join(runtime.paths.BackupRoot, workspace.id)
	if backupDir != expected {
		return nil, errors.New("backup directory must be the release-scoped target backup")
	}
	if err := requireRealDirectory(backupDir); err != nil {
		return nil, fmt.Errorf("verified target backup is missing or unsafe: %w", err)
	}
	required := []string{
		"application.archive",
		"frontend.archive",
		"configuration.archive",
		"database.archive",
		"manifest.env",
		"SHA256SUMS",
		"rollback.package",
	}
	for _, name := range required {
		path := filepath.Join(backupDir, name)
		info, err := os.Lstat(path)
		if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() == 0 {
			return nil, fmt.Errorf("backup entry %s is missing, empty, or unsafe", name)
		}
	}
	if err := verifyBackupChecksums(backupDir); err != nil {
		return nil, err
	}
	if _, err := runtime.runner.Run(ctx, productionCommand{Name: commandPGRestore, Args: []string{"--list", filepath.Join(backupDir, "database.archive")},
		Timeout: productionCommandTimeout}); err != nil {
		return nil, fmt.Errorf("validate PostgreSQL backup: %w", err)
	}
	environment, err := environmentFromConfigurationArchive(filepath.Join(backupDir, "configuration.archive"))
	if err != nil {
		return nil, err
	}
	return environment, nil
}

func verifyBackupChecksums(root string) error {
	return verifyNamedChecksums(root, []string{
		"application.archive", "frontend.archive", "configuration.archive", "database.archive", "rollback.package",
	})
}

func readNamedChecksums(root string, names []string) (map[string]string, error) {
	content, err := readPrivateRegularFile(filepath.Join(root, "SHA256SUMS"), 1<<20)
	if err != nil {
		return nil, fmt.Errorf("read backup checksums: %w", err)
	}
	required := make(map[string]string, len(names))
	for _, name := range names {
		if name == "" || filepath.Base(name) != name {
			return nil, errors.New("checksum requirement contains an unsafe filename")
		}
		required[name] = ""
	}
	scanner := bufio.NewScanner(strings.NewReader(string(content)))
	for scanner.Scan() {
		line := scanner.Text()
		if len(line) < 67 {
			return nil, errors.New("backup checksum line is malformed")
		}
		digest := line[:64]
		name := strings.TrimPrefix(strings.TrimSpace(line[64:]), "*")
		if !productionSHA256Pattern.MatchString(digest) {
			return nil, errors.New("backup checksum digest is malformed")
		}
		if current, recognized := required[name]; !recognized || current != "" {
			return nil, fmt.Errorf("backup checksum contains an unexpected or duplicate entry: %s", name)
		}
		required[name] = digest
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan backup checksums: %w", err)
	}
	for name, digest := range required {
		if digest == "" {
			return nil, fmt.Errorf("backup checksum is missing: %s", name)
		}
	}
	return required, nil
}

func verifyNamedChecksums(root string, names []string) error {
	required, err := readNamedChecksums(root, names)
	if err != nil {
		return err
	}
	for name, digest := range required {
		actual, err := sha256File(filepath.Join(root, name))
		if err != nil {
			return fmt.Errorf("hash backup entry %s: %w", name, err)
		}
		if actual != digest {
			return fmt.Errorf("backup checksum mismatch: %s", name)
		}
	}
	return nil
}

func environmentFromConfigurationArchive(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open configuration backup: %w", err)
	}
	defer file.Close()
	reader := tar.NewReader(file)
	var environment []byte
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read configuration backup: %w", err)
		}
		name := filepath.ToSlash(filepath.Clean(strings.TrimPrefix(header.Name, "./")))
		if name == "." || name == "" {
			continue
		}
		if strings.HasPrefix(name, "/") || name == ".." || strings.HasPrefix(name, "../") {
			return nil, fmt.Errorf("configuration backup contains unsafe path %q", header.Name)
		}
		switch header.Typeflag {
		case tar.TypeDir:
			continue
		case tar.TypeReg:
		default:
			return nil, fmt.Errorf("configuration backup contains unsupported entry %q", header.Name)
		}
		if name != "lmm-api-go/lmm-api-go.env" {
			continue
		}
		if environment != nil {
			return nil, errors.New("configuration backup contains duplicate Go environment files")
		}
		if header.Size <= 0 || header.Size > 1<<20 {
			return nil, errors.New("configuration backup Go environment has an unsafe size")
		}
		environment = make([]byte, header.Size)
		if _, err := io.ReadFull(reader, environment); err != nil {
			return nil, fmt.Errorf("read backed-up Go environment: %w", err)
		}
	}
	if environment == nil {
		return nil, errors.New("configuration backup lacks lmm-api-go/lmm-api-go.env")
	}
	return environment, nil
}

func parseProductionEnvironment(content []byte) (map[string]string, error) {
	values := make(map[string]string)
	for lineNumber, rawLine := range strings.Split(strings.ReplaceAll(string(content), "\r\n", "\n"), "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		key, rawValue, found := strings.Cut(line, "=")
		key = strings.TrimSpace(key)
		if !found || !productionEnvironmentKeyPattern.MatchString(key) {
			return nil, fmt.Errorf("environment line %d is not a safe assignment", lineNumber+1)
		}
		if _, exists := values[key]; exists {
			return nil, fmt.Errorf("environment key %s is duplicated", key)
		}
		value, err := parseProductionEnvironmentValue(strings.TrimSpace(rawValue))
		if err != nil {
			return nil, fmt.Errorf("environment key %s: %w", key, err)
		}
		values[key] = value
	}
	return values, nil
}

func parseProductionEnvironmentValue(value string) (string, error) {
	if strings.Contains(value, "\x00") || strings.Contains(value, "\n") || strings.Contains(value, "\r") {
		return "", errors.New("value contains a control character")
	}
	if value == "" {
		return "", nil
	}
	if value[0] == '\'' || value[0] == '"' {
		quote := value[0]
		if len(value) < 2 || value[len(value)-1] != quote {
			return "", errors.New("quoted value is not terminated")
		}
		inner := value[1 : len(value)-1]
		if strings.ContainsRune(inner, rune(quote)) || strings.Contains(inner, "`") || strings.Contains(inner, "$(") {
			return "", errors.New("quoted value contains executable or ambiguous syntax")
		}
		if quote == '"' && strings.ContainsAny(inner, `\$`) {
			return "", errors.New("double-quoted value contains unsupported expansion or escaping")
		}
		return inner, nil
	}
	if strings.ContainsAny(value, " \t`;") || strings.Contains(value, "$(") {
		return "", errors.New("unquoted value contains unsafe syntax")
	}
	return value, nil
}

func productionDatabaseURL(values map[string]string) (string, error) {
	found := make([]string, 0, 2)
	for _, key := range []string{"SQL_DSN", "DATABASE_URL"} {
		if value, ok := values[key]; ok && value != "" {
			found = append(found, value)
		}
	}
	if len(found) != 1 {
		return "", errors.New("environment must contain exactly one SQL_DSN or DATABASE_URL")
	}
	value := found[0]
	if !strings.HasPrefix(value, "postgres://") && !strings.HasPrefix(value, "postgresql://") {
		return "", errors.New("production database URL must use PostgreSQL")
	}
	return value, nil
}

func productionDatabaseCommand(values map[string]string) (string, []string, error) {
	databaseURL, err := productionDatabaseURL(values)
	if err != nil {
		return "", nil, err
	}
	parsed, err := url.Parse(databaseURL)
	if err != nil {
		return "", nil, errors.New("production database URL is invalid")
	}
	overrides := make(map[string]string)
	if parsed.User != nil {
		if password, present := parsed.User.Password(); present {
			overrides["PGPASSWORD"] = password
			parsed.User = url.User(parsed.User.Username())
		}
	}
	return parsed.String(), productionChildEnvironment(values, overrides), nil
}

func productionChildEnvironment(values map[string]string, overrides map[string]string) []string {
	merged := make(map[string]string)
	for _, assignment := range os.Environ() {
		key, value, found := strings.Cut(assignment, "=")
		if found {
			merged[key] = value
		}
	}
	for key, value := range values {
		merged[key] = value
	}
	for key, value := range overrides {
		merged[key] = value
	}
	keys := make([]string, 0, len(merged))
	for key := range merged {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]string, 0, len(keys))
	for _, key := range keys {
		result = append(result, key+"="+merged[key])
	}
	return result
}

func (runtime *productionRuntime) captureDatabaseAccess(ctx context.Context, workspace productionWorkspace, environment []byte) (string, error) {
	values, err := parseProductionEnvironment(environment)
	if err != nil {
		return "", fmt.Errorf("parse production environment: %w", err)
	}
	databaseURL, childEnvironment, err := productionDatabaseCommand(values)
	if err != nil {
		return "", err
	}
	schemaOutput, err := runtime.runner.Run(ctx, productionCommand{Name: commandPSQL, Args: []string{"-X", "-v", "ON_ERROR_STOP=1", "--no-align", "--tuples-only", "--command", "SELECT pg_catalog.current_schema()", databaseURL},
		Env: childEnvironment, Sensitive: true})
	if err != nil {
		return "", fmt.Errorf("discover production database schema: %w", err)
	}
	schema := strings.TrimSpace(string(schemaOutput))
	if !isDatabaseSchema(schema) {
		return "", errors.New("production database schema is unsafe")
	}
	const tokenQuery = `SELECT tokens.key
FROM tokens
JOIN users ON users.id = tokens.user_id
WHERE tokens.deleted_at IS NULL
  AND tokens.status = 1
  AND users.status = 1
  AND users.role >= 10
  AND (tokens.expired_time = -1 OR tokens.expired_time > EXTRACT(EPOCH FROM NOW()))
  AND (tokens.unlimited_quota OR tokens.remain_quota > 0)
  AND COALESCE(LENGTH(BTRIM(tokens.allow_ips)), 0) = 0
ORDER BY tokens.unlimited_quota DESC, tokens.remain_quota DESC, tokens.id DESC
LIMIT 1`
	tokenOutput, err := runtime.runner.Run(ctx, productionCommand{Name: commandPSQL, Args: []string{"-X", "-v", "ON_ERROR_STOP=1", "--no-align", "--tuples-only", "--command", tokenQuery, databaseURL},
		Env: childEnvironment, Sensitive: true})
	if err != nil {
		return "", fmt.Errorf("select safe production probe token: %w", err)
	}
	rawToken := strings.TrimSpace(string(tokenOutput))
	if matched, _ := regexp.MatchString(`^[A-Za-z0-9_-]{16,128}$`, rawToken); !matched {
		return "", errors.New("no safe production probe token is available")
	}
	if _, err := os.Lstat(workspace.probeToken); !errors.Is(err, os.ErrNotExist) {
		return "", errors.New("probe token path already exists or is unsafe")
	}
	if err := writeAtomicRegularFile(workspace.probeToken, []byte("sk-"+rawToken+"\n"), 0o600); err != nil {
		return "", fmt.Errorf("write protected probe token: %w", err)
	}
	return schema, nil
}

func isDatabaseSchema(schema string) bool {
	if schema == "information_schema" || strings.HasPrefix(schema, "pg_") {
		return false
	}
	matched, _ := regexp.MatchString(`^[a-z_][a-z0-9_]{0,62}$`, schema)
	return matched
}

func (runtime *productionRuntime) saveRestoreState(workspace productionWorkspace, archivedEnvironment []byte) (string, error) {
	if err := ensureRealDirectory(workspace.configRestore, 0o700); err != nil {
		return "", fmt.Errorf("prepare configuration restore state: %w", err)
	}
	liveEnvironment := filepath.Join(runtime.paths.ConfigDir, "lmm-api-go.env")
	live, err := readPrivateRegularFile(liveEnvironment, 1<<20)
	if err != nil {
		return "", fmt.Errorf("read live Go environment: %w", err)
	}
	if !equalSHA256(live, archivedEnvironment) {
		return "", errors.New("verified backup environment does not match the live production environment")
	}
	environmentRestorePath := filepath.Join(workspace.configRestore, "lmm-api-go.env")
	if err := writeAtomicRegularFile(environmentRestorePath, archivedEnvironment, 0o600); err != nil {
		return "", fmt.Errorf("save environment restore state: %w", err)
	}
	return sha256File(environmentRestorePath)
}

func equalSHA256(first, second []byte) bool {
	left := sha256Bytes(first)
	right := sha256Bytes(second)
	return left == right
}

func sha256Bytes(content []byte) [32]byte {
	// Kept local so backup comparisons never write secret environment data.
	return sha256.Sum256(content)
}

func (runtime *productionRuntime) restoreConfiguration(workspace productionWorkspace, manifest productionManifest) error {
	environment, err := readPrivateRegularFile(filepath.Join(workspace.configRestore, "lmm-api-go.env"), 1<<20)
	if err != nil {
		return fmt.Errorf("read environment restore state: %w", err)
	}
	if fmt.Sprintf("%x", sha256Bytes(environment)) != manifest.EnvironmentRestoreSHA256 {
		return errors.New("environment restore state changed after deployment was armed")
	}
	if err := ensureRealDirectory(runtime.paths.ConfigDir, 0o700); err != nil {
		return fmt.Errorf("prepare production configuration directory: %w", err)
	}
	if err := writeAtomicRegularFile(filepath.Join(runtime.paths.ConfigDir, "lmm-api-go.env"), environment, 0o600); err != nil {
		return fmt.Errorf("restore production environment: %w", err)
	}
	return nil
}

func (runtime *productionRuntime) migrationEnvironment(environment []byte, schema string) ([]string, error) {
	values, err := parseProductionEnvironment(environment)
	if err != nil {
		return nil, err
	}
	if _, err := productionDatabaseURL(values); err != nil {
		return nil, err
	}
	return productionChildEnvironment(values, map[string]string{
		"GIN_MODE":              "release",
		"PGOPTIONS":             "-c search_path=" + schema,
		"LMM_DB_MIGRATION_MODE": "",
	}), nil
}

type migrationRun struct {
	name   string
	binary string
	mode   string
}

func (runtime *productionRuntime) runMigration(
	ctx context.Context,
	workspace productionWorkspace,
	manifest productionManifest,
	run migrationRun,
) error {
	if run.mode != "apply" && run.mode != "verify" {
		return errors.New("migration mode must be apply or verify")
	}
	if !productionReasonPattern.MatchString(run.name) || run.binary == "" {
		return errors.New("migration run identity is invalid")
	}
	environment, err := readPrivateRegularFile(filepath.Join(runtime.paths.ConfigDir, "lmm-api-go.env"), 1<<20)
	if err != nil {
		return fmt.Errorf("read migration environment: %w", err)
	}
	childEnvironment, err := runtime.migrationEnvironment(environment, manifest.DatabaseSchema)
	if err != nil {
		return fmt.Errorf("prepare migration environment: %w", err)
	}
	for index, assignment := range childEnvironment {
		if strings.HasPrefix(assignment, "LMM_DB_MIGRATION_MODE=") {
			childEnvironment[index] = "LMM_DB_MIGRATION_MODE=" + run.mode
			break
		}
	}
	migrationWorkdir, err := prepareMigrationDir(workspace, run.name)
	if err != nil {
		return err
	}
	_, err = runVerifiedBinary(ctx, runtime.runner, run.binary, []string{"migrate", "--" + run.mode}, childEnvironment, migrationWorkdir, 5*time.Minute, true)
	if err != nil {
		return fmt.Errorf("migration %s failed: %w", run.name, err)
	}
	return nil
}

func prepareMigrationDir(workspace productionWorkspace, mode string) (string, error) {
	path := workspace.root
	for _, name := range []string{"tmp", "migrations", mode} {
		path = filepath.Join(path, name)
		if err := ensureRealDirectory(path, 0o700); err != nil {
			return "", fmt.Errorf("prepare migration directory %s: %w", name, err)
		}
	}
	return path, nil
}

// Keep this helper close to backup parsing: timestamps in manifests should be
// stable and never inherit a controller locale.
func utcSecond(value time.Time) time.Time { return value.UTC().Truncate(time.Second) }
