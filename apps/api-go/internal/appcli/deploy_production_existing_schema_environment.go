package appcli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var existingSchemaFinancialEnvironment = []string{"LMM_CREDIT_TRANSITION_PLAN", "LMM_CREDIT_TRANSITION_SHA256"}
var existingSchemaInvocationPattern = regexp.MustCompile(`^[a-f0-9]{32}$`)

const existingSchemaUnitProperties = "Environment,EnvironmentFiles,PassEnvironment,UnsetEnvironment,MainPID,InvocationID,ActiveState,ExecStart,ExecStartPre,ExecStartPreEx,ExecStartPost,ExecCondition,ExecStop,ExecStopPost,FragmentPath,DropInPaths"

// Only the disposable verification child receives a read-only connection. The
// running service must remain able to serve ordinary business transactions.
func (runtime *productionRuntime) existingSchemaMigrationEnvironment(environment []byte, schema string) ([]string, error) {
	if !isDatabaseSchema(schema) {
		return nil, errors.New("verify-existing schema is unsafe")
	}
	values, err := parseProductionEnvironment(environment)
	if err != nil {
		return nil, err
	}
	return runtime.existingSchemaMigrationValues(values, schema)
}

// Accept the already inspected effective ordered systemd environment without
// serializing parsed secrets back through a shell/env-file quoting grammar.
func (runtime *productionRuntime) existingSchemaMigrationValues(effective map[string]string, schema string) ([]string, error) {
	if !isDatabaseSchema(schema) {
		return nil, errors.New("verify-existing schema is unsafe")
	}
	values := make(map[string]string, len(effective))
	for key, value := range effective {
		values[key] = value
	}
	databaseURL, err := productionDatabaseURL(values)
	if err != nil {
		return nil, err
	}
	if err := verifyExistingSchemaLogDatabase(values, databaseURL); err != nil {
		return nil, err
	}
	parsed, err := url.Parse(databaseURL)
	if err != nil || parsed.Fragment != "" {
		return nil, errors.New("verify-existing database URL is invalid")
	}
	query, err := url.ParseQuery(parsed.RawQuery)
	if err != nil {
		return nil, errors.New("verify-existing database parameters are invalid")
	}
	// URI options override PGOPTIONS in libpq, while pgx can also accept direct
	// runtime parameters. Seal both paths instead of relying on an environment
	// override that an existing DSN could undo.
	readOnlyOptions := "-c search_path=" + schema + " -c default_transaction_read_only=on"
	query.Set("options", readOnlyOptions)
	query.Set("search_path", schema)
	query.Set("default_transaction_read_only", "on")
	parsed.RawQuery = query.Encode()
	for _, key := range []string{"SQL_DSN", "DATABASE_URL", "LOG_SQL_DSN"} {
		if values[key] != "" {
			values[key] = parsed.String()
		}
	}
	child := productionChildEnvironment(values, map[string]string{
		"GIN_MODE": "release", "PGOPTIONS": readOnlyOptions, "LMM_DB_MIGRATION_MODE": "verify",
	})
	filtered := make([]string, 0, len(child))
	for _, assignment := range child {
		key, _, _ := strings.Cut(assignment, "=")
		if key != existingSchemaFinancialEnvironment[0] && key != existingSchemaFinancialEnvironment[1] &&
			!((key == "SQL_DSN" || key == "DATABASE_URL" || key == "LOG_SQL_DSN") && values[key] == "") {
			filtered = append(filtered, assignment)
		}
	}
	return filtered, nil
}

func verifyExistingSchemaLogDatabase(values map[string]string, mainDSN string) error {
	if logDSN := values["LOG_SQL_DSN"]; logDSN != "" && logDSN != mainDSN {
		return errors.New("verify-existing requires the log database to use the same sealed PostgreSQL connection")
	}
	return nil
}

func verifyExistingSchemaStartupEnvironment(values map[string]string, required bool) error {
	mode, present := values["LMM_DB_MIGRATION_MODE"]
	if (required && (!present || mode != "verify")) || (present && mode != "verify") {
		return errors.New("verify-existing requires service startup in verify mode")
	}
	for _, key := range existingSchemaFinancialEnvironment {
		if values[key] != "" {
			return errors.New("verify-existing cannot start with financial transition environment")
		}
	}
	return nil
}

// Check the loaded unit before each start, and the actual process generation
// while active. An on-disk package unit alone does not prove loaded behavior.
func (runtime *productionRuntime) verifyExistingSchemaStartupMode(ctx context.Context, manifest productionManifest) error {
	if manifest.SchemaMode != productionSchemaModeVerifyExisting {
		return errors.New("startup schema invariant requires verify-existing mode")
	}
	configPath := filepath.Join(runtime.paths.ConfigDir, "lmm-api-go.env")
	configuration, err := readPrivateRegularFile(configPath, 1<<20)
	if err != nil {
		return errors.New("verify-existing startup configuration cannot be read")
	}
	configured, err := parseProductionEnvironment(configuration)
	if err != nil || verifyExistingSchemaStartupEnvironment(configured, false) != nil {
		return errors.New("verify-existing startup configuration enables migration or financial preparation")
	}
	configuredDSN, err := productionDatabaseURL(configured)
	if err != nil {
		return err
	}
	if err := verifyExistingSchemaLogDatabase(configured, configuredDSN); err != nil {
		return err
	}
	loaded, err := runtime.loadedExistingSchemaUnit(ctx)
	if err != nil {
		return err
	}
	values, err := parseExistingSchemaLoadedEnvironment(loaded["Environment"])
	if err != nil || verifyExistingSchemaStartupEnvironment(values, true) != nil {
		return errors.New("loaded production unit does not enforce verify-only startup")
	}
	// No later EnvironmentFile or UnsetEnvironment can silently override the
	// checked mode or introduce financial preparation into the next process.
	files := loaded["EnvironmentFiles"]
	sealed := manifest.ExistingSchemaContract != nil && manifest.ExistingSchemaContract.StartupSHA256 != ""
	if sealed {
		effective, digest, unitDigest, err := runtime.existingSchemaSealedStartup(ctx, loaded)
		if err != nil || digest != manifest.ExistingSchemaContract.StartupSHA256 || unitDigest != manifest.ExistingSchemaContract.SignedUnitSHA256 {
			return errors.New("effective production startup differs from its immutable seal")
		}
		values = effective
	} else {
		if files != configPath+" (ignore_errors=yes)" && files != configPath+" (ignore_errors=no)" {
			return errors.New("loaded production unit has unexpected unsealed environment files")
		}
		for key, value := range configured {
			values[key] = value
		}
	}
	if loaded["PassEnvironment"] != "" || loaded["UnsetEnvironment"] != "" {
		return errors.New("loaded production unit has unchecked environment overrides")
	}
	if !sealed {
		if err := verifyExistingSchemaLoadedCommands(loaded, runtime.paths.InstalledBinary); err != nil {
			return err
		}
	}
	if err := validateProductionExistingSchemaContract(manifest.ExistingSchemaContract); err != nil {
		return err
	}
	futureDSN, err := productionDatabaseURL(values)
	if err != nil || futureDSN != configuredDSN {
		return errors.New("effective startup database differs from canonical configuration")
	}
	if err := runtime.verifyExistingSchemaEffectiveSearchPath(ctx, values, manifest.ExistingSchemaContract); err != nil {
		return err
	}
	if loaded["ActiveState"] == "inactive" && loaded["MainPID"] == "0" {
		return nil
	}
	pid, err := strconv.Atoi(loaded["MainPID"])
	if err != nil || pid <= 1 || loaded["ActiveState"] != "active" || !existingSchemaInvocationPattern.MatchString(loaded["InvocationID"]) {
		return errors.New("verify-existing production process generation is unavailable")
	}
	reader := runtime.maintenanceProcessEnvironment
	if reader == nil {
		reader = func(pid int) ([]byte, error) {
			return os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "environ"))
		}
	}
	process, err := reader(pid)
	if err != nil || len(process) == 0 || len(process) > 1<<20 {
		return errors.New("verify-existing production process environment is unavailable")
	}
	processValues := map[string]string{}
	for _, entry := range bytes.Split(process, []byte{0}) {
		if len(entry) == 0 {
			continue
		}
		key, value, found := strings.Cut(string(entry), "=")
		if !found || !productionEnvironmentKeyPattern.MatchString(key) {
			return errors.New("verify-existing process environment is malformed")
		}
		if _, duplicate := processValues[key]; duplicate {
			return errors.New("verify-existing process environment contains duplicate keys")
		}
		processValues[key] = value
	}
	if err := verifyExistingSchemaStartupEnvironment(processValues, true); err != nil {
		return err
	}
	processDSN, err := productionDatabaseURL(processValues)
	if err != nil || configuredDSN != processDSN {
		return errors.New("verify-existing production process database differs from checked configuration")
	}
	if err := verifyExistingSchemaLogDatabase(processValues, processDSN); err != nil {
		return err
	}
	if err := runtime.verifyExistingSchemaEffectiveSearchPath(ctx, processValues, manifest.ExistingSchemaContract); err != nil {
		return err
	}
	current, err := runtime.loadedExistingSchemaUnit(ctx)
	if err != nil || current["MainPID"] != loaded["MainPID"] || current["InvocationID"] != loaded["InvocationID"] || current["ActiveState"] != "active" ||
		current["Environment"] != loaded["Environment"] || current["EnvironmentFiles"] != files || current["PassEnvironment"] != "" || current["UnsetEnvironment"] != "" {
		return errors.New("verify-existing loaded unit or process generation changed during inspection")
	}
	if sealed {
		_, digest, unitDigest, err := runtime.existingSchemaSealedStartup(ctx, current)
		if err != nil || digest != manifest.ExistingSchemaContract.StartupSHA256 || unitDigest != manifest.ExistingSchemaContract.SignedUnitSHA256 {
			return errors.New("verify-existing sealed startup changed during inspection")
		}
	} else if err := verifyExistingSchemaLoadedCommands(current, runtime.paths.InstalledBinary); err != nil || current["ExecStart"] != loaded["ExecStart"] {
		return errors.New("verify-existing loaded startup commands changed during inspection")
	}
	return nil
}

func (runtime *productionRuntime) verifyExistingSchemaEffectiveSearchPath(ctx context.Context, values map[string]string, expected *productionExistingSchemaContract) error {
	if err := validateProductionExistingSchemaContract(expected); err != nil {
		return err
	}
	// The adapter must not promote the operator's ambient PGOPTIONS into URI
	// options before the process-environment filtering below can remove them.
	commandValues := make(map[string]string, len(values)+1)
	for key, value := range values {
		commandValues[key] = value
	}
	commandValues["PGOPTIONS"] = values["PGOPTIONS"]
	databaseURL, environment, err := productionDatabaseCommand(commandValues)
	if err != nil {
		return err
	}
	rawDSN, err := productionDatabaseURL(values)
	if err != nil {
		return err
	}
	if err := verifyExistingSchemaLogDatabase(values, rawDSN); err != nil {
		return err
	}
	// Reproduce the inspected process's PostgreSQL variables, not the native
	// operator's ambient defaults. The extracted URI password stays private.
	filtered := make([]string, 0, len(environment)+1)
	for _, assignment := range environment {
		key, _, _ := strings.Cut(assignment, "=")
		if key == "PGOPTIONS" {
			continue
		}
		if strings.HasPrefix(key, "PG") && key != "PGPASSWORD" {
			if _, present := values[key]; !present {
				continue
			}
		}
		if key == existingSchemaFinancialEnvironment[0] || key == existingSchemaFinancialEnvironment[1] {
			continue
		}
		filtered = append(filtered, assignment)
	}
	filtered = append(filtered, "PGOPTIONS="+values["PGOPTIONS"])
	output, err := runtime.runner.Run(ctx, productionCommand{Name: commandPSQL,
		Args: []string{"-X", "-q", "-v", "ON_ERROR_STOP=1", "--no-align", "--tuples-only", "--command", existingSchemaStartupIdentityQuery, databaseURL},
		Env:  filtered, Sensitive: true, Timeout: 15 * time.Second, OutputLimit: 4096})
	if err != nil {
		return errors.New("verify-existing effective startup database identity is unavailable")
	}
	var identity struct {
		SystemIdentifier    string `json:"system_identifier"`
		Database            string `json:"database"`
		DatabaseOID         int64  `json:"database_oid"`
		Schema              string `json:"schema"`
		SchemaOID           int64  `json:"schema_oid"`
		TransactionReadOnly string `json:"transaction_read_only"`
	}
	decoder := json.NewDecoder(bytes.NewReader(output))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&identity) != nil || decoder.Decode(&struct{}{}) != io.EOF || identity.TransactionReadOnly != "on" ||
		identity.SystemIdentifier != expected.SystemIdentifier || identity.Database != expected.Database || identity.DatabaseOID != expected.DatabaseOID ||
		identity.Schema != expected.Schema || identity.SchemaOID != expected.SchemaOID {
		return errors.New("verify-existing process or loaded startup uses a different effective database identity or schema")
	}
	return nil
}

const existingSchemaStartupIdentityQuery = `BEGIN READ ONLY;
/* lmm-existing-startup-identity */
SELECT pg_catalog.jsonb_build_object(
 'system_identifier',(SELECT system_identifier::pg_catalog.text FROM pg_catalog.pg_control_system()),
 'database',pg_catalog.current_database(),
 'database_oid',(SELECT oid::pg_catalog.int8 FROM pg_catalog.pg_database WHERE datname OPERATOR(pg_catalog.=) pg_catalog.current_database()),
 'schema',pg_catalog.current_schema(),
 'schema_oid',(SELECT oid::pg_catalog.int8 FROM pg_catalog.pg_namespace WHERE nspname OPERATOR(pg_catalog.=) pg_catalog.current_schema()),
 'transaction_read_only',pg_catalog.current_setting('transaction_read_only'));
ROLLBACK;`

func verifyExistingSchemaLoadedCommands(loaded map[string]string, binary string) error {
	prefix := "{ path=" + binary + " ; argv[]=" + binary + " serve ; ignore_errors=no ;"
	start := loaded["ExecStart"]
	if binary == "" || !strings.HasPrefix(start, prefix) || !strings.HasSuffix(start, " }") ||
		strings.Count(start, "{ path=") != 1 || strings.Count(start, "argv[]=") != 1 {
		return errors.New("loaded production unit does not start only the verified serve command")
	}
	for _, key := range []string{"ExecStartPre", "ExecStartPost", "ExecCondition", "ExecStop", "ExecStopPost"} {
		if loaded[key] != "" {
			return errors.New("loaded production unit has unchecked lifecycle commands")
		}
	}
	return nil
}

func (runtime *productionRuntime) loadedExistingSchemaUnit(ctx context.Context) (map[string]string, error) {
	output, err := runtime.runner.Run(ctx, productionCommand{Name: commandSystemctl,
		Args:      []string{"show", runtime.paths.Service, "--all", "--property=" + existingSchemaUnitProperties},
		Sensitive: true, Timeout: 15 * time.Second, OutputLimit: 1 << 20})
	if err != nil {
		return nil, errors.New("cannot inspect loaded verify-existing startup unit")
	}
	values := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		key, value, found := strings.Cut(line, "=")
		if !found {
			return nil, errors.New("loaded startup unit evidence is malformed")
		}
		if !strings.Contains(","+existingSchemaUnitProperties+",", ","+key+",") {
			return nil, errors.New("loaded startup unit evidence contains an unexpected property")
		}
		if previous, duplicate := values[key]; duplicate {
			if (key != "EnvironmentFiles" && key != "ExecStartPost") || previous == "" || value == "" {
				return nil, errors.New("loaded startup unit evidence contains ambiguous duplicate properties")
			}
			values[key] = previous + "\n" + value
		} else {
			values[key] = value
		}
	}
	for _, key := range strings.Split(existingSchemaUnitProperties, ",") {
		if _, present := values[key]; !present {
			switch key {
			case "PassEnvironment", "UnsetEnvironment", "ExecStartPre", "ExecStartPreEx", "ExecStartPost", "ExecCondition", "ExecStop", "ExecStopPost", "FragmentPath", "DropInPaths":
				// systemctl omits unset array properties, including with --all.
				values[key] = ""
			default:
				return nil, fmt.Errorf("loaded startup unit evidence is missing %s", key)
			}
		}
	}
	return values, nil
}

// systemctl show quotes assignments containing spaces. Accept plain and
// quoted tokens; refuse backslash escapes instead of guessing their meaning.
func parseExistingSchemaLoadedEnvironment(value string) (map[string]string, error) {
	values := map[string]string{}
	var word strings.Builder
	var quote byte
	finish := func() error {
		if word.Len() == 0 {
			return nil
		}
		key, value, found := strings.Cut(word.String(), "=")
		if !found || !productionEnvironmentKeyPattern.MatchString(key) {
			return errors.New("loaded service environment is malformed")
		}
		if _, duplicate := values[key]; duplicate {
			return errors.New("loaded service environment contains duplicate keys")
		}
		values[key] = value
		word.Reset()
		return nil
	}
	for index := 0; index < len(value); index++ {
		character := value[index]
		if character == '\\' || character < 32 {
			return nil, errors.New("loaded service environment contains unsupported escapes")
		}
		if quote != 0 {
			if character == quote {
				quote = 0
			} else {
				word.WriteByte(character)
			}
		} else if character == '"' || character == '\'' {
			quote = character
		} else if character == ' ' {
			if err := finish(); err != nil {
				return nil, err
			}
		} else {
			word.WriteByte(character)
		}
	}
	if quote != 0 {
		return nil, errors.New("loaded service environment quote is unfinished")
	}
	return values, finish()
}
