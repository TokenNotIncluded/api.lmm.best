package deploycli

import (
	"context"
	"errors"
	"net/url"
	"sort"
	"strings"
)

// This child-only projection is the dedicated schema maintenance authority. It
// changes neither the service EnvFile nor its loaded startup environment. The
// worker independently proves the exact physical cluster/role/owner before DDL.
func (runtime *productionRuntime) schemaMaintenanceWorkerEnvironment(ctx context.Context, artifacts, oldCapsule productionMerchantStoreCapsule, oldSHA string) ([]string, error) {
	if err := validateMerchantStoreCapsule(artifacts, runtime.paths); err != nil {
		return nil, err
	}
	if artifacts.Format != 1 || artifacts.StartupPolicy != "held" || artifacts.Writer.RequiredCapability > 7 || oldCapsule.Format != 1 || oldCapsule.StartupPolicy != "per-start" || artifacts.Rollback.PayloadSHA256 != oldCapsule.Candidate.PayloadSHA256 {
		return nil, errors.New("schema maintenance worker environment lacks its actual retained-provider authority")
	}
	old, err := runtime.loadMerchantStoreCapsule(oldCapsule.Root+"/capsule.json", oldSHA)
	if err != nil {
		return nil, err
	}
	expected, _ := canonicalMerchantStoreCapsule(oldCapsule)
	actual, _ := canonicalMerchantStoreCapsule(old)
	if string(expected) != string(actual) {
		return nil, errors.New("schema maintenance retained startup capsule changed")
	}
	child, err := runtime.merchantStoreCapsuleEnvironment(ctx, oldCapsule, oldSHA, "")
	if err != nil {
		return nil, err
	}
	return schemaMaintenanceWorkerValues(child, artifacts.Writer.Schema)
}

func schemaMaintenanceWorkerValues(readOnlyChild []string, schema string) ([]string, error) {
	if !isDatabaseSchema(schema) {
		return nil, errors.New("schema maintenance worker schema is unsafe")
	}
	values := map[string]string{}
	for _, entry := range readOnlyChild {
		key, value, ok := strings.Cut(entry, "=")
		if !ok || !productionEnvironmentKeyPattern.MatchString(key) || strings.ContainsRune(value, '\x00') {
			return nil, errors.New("schema maintenance inspected environment is malformed")
		}
		if _, exists := values[key]; exists {
			return nil, errors.New("schema maintenance inspected environment is duplicated")
		}
		values[key] = value
	}
	if err := verifyExistingSchemaStartupEnvironment(values, true); err != nil {
		return nil, err
	}
	dsn, err := productionDatabaseURL(values)
	if err != nil {
		return nil, err
	}
	if err := verifyExistingSchemaLogDatabase(values, dsn); err != nil {
		return nil, err
	}
	parsed, err := url.Parse(dsn)
	if err != nil || parsed.Fragment != "" {
		return nil, errors.New("schema maintenance database URL is invalid")
	}
	query, err := url.ParseQuery(parsed.RawQuery)
	if err != nil {
		return nil, errors.New("schema maintenance database parameters are invalid")
	}
	for _, key := range []string{"options", "search_path", "default_transaction_read_only"} {
		if len(query[key]) != 1 {
			return nil, errors.New("schema maintenance database lacks the exact inspected read-only projection")
		}
	}
	oldOptions := "-c search_path=" + schema + " -c default_transaction_read_only=on"
	if query.Get("options") != oldOptions || query.Get("search_path") != schema || query.Get("default_transaction_read_only") != "on" || values["PGOPTIONS"] != oldOptions {
		return nil, errors.New("schema maintenance database was not projected by the original verify-only authority")
	}
	for _, key := range []string{"service", "servicefile", "passfile"} {
		if query.Has(key) {
			return nil, errors.New("schema maintenance cannot use an external database service or credentials file")
		}
	}
	for _, key := range []string{"PGSERVICE", "PGSERVICEFILE", "PGPASSFILE"} {
		if values[key] != "" {
			return nil, errors.New("schema maintenance cannot use an external PostgreSQL file")
		}
	}
	options := "-c search_path=" + schema + " -c default_transaction_read_only=off"
	query.Set("options", options)
	query.Set("search_path", schema)
	query.Set("default_transaction_read_only", "off")
	parsed.RawQuery = query.Encode()
	// The schema-only API reads SQL_DSN exclusively and never opens a log pool.
	// No Redis, session secrets, financial-transition or general application
	// settings are inherited from either the holder or the operator's shell.
	result := map[string]string{"SQL_DSN": parsed.String(), "PGOPTIONS": options, "PATH": "/usr/bin:/bin", "LANG": "C", "LC_ALL": "C", "GIN_MODE": "release", "LMM_DB_MIGRATION_MODE": "schema-only"}
	for _, key := range []string{"OAUTH_SERVER_ENABLED", "LMM_OIDC_ENABLED"} {
		if value, exists := values[key]; exists {
			result[key] = value
		}
	}
	switch result["OAUTH_SERVER_ENABLED"] {
	case "", "false", "true":
	default:
		return nil, errors.New("schema maintenance OAuth storage opt-in is invalid")
	}
	for _, key := range []string{"PGHOST", "PGHOSTADDR", "PGPORT", "PGDATABASE", "PGUSER", "PGPASSWORD", "PGSSLMODE", "PGSSLCERT", "PGSSLKEY", "PGSSLROOTCERT", "PGSSLCRL", "PGSSLCRLDIR", "PGSSLNEGOTIATION", "PGCHANNELBINDING", "PGTARGETSESSIONATTRS", "PGGSSENCMODE", "PGKRBSRVNAME", "PGGSSLIB"} {
		if value, exists := values[key]; exists {
			result[key] = value
		}
	}
	keys := make([]string, 0, len(result))
	for key := range result {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	child := make([]string, 0, len(keys))
	for _, key := range keys {
		child = append(child, key+"="+result[key])
	}
	return child, nil
}
