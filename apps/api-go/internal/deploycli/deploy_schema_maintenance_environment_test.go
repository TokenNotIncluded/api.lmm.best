package deploycli

import (
	"net/url"
	"reflect"
	"strings"
	"testing"
)

func TestSchemaMaintenanceWorkerEnvironmentIsChildOnlyAndExplicitRW(t *testing.T) {
	options := "-c search_path=public -c default_transaction_read_only=on"
	raw := []string{"SQL_DSN=postgres://fixture:private@localhost/lmm?sslmode=require&options=" + url.QueryEscape(options) + "&search_path=public&default_transaction_read_only=on", "PGOPTIONS=" + options, "LMM_DB_MIGRATION_MODE=verify", "OAUTH_SERVER_ENABLED=true", "LMM_OIDC_ENABLED=true", "SESSION_SECRET=private-session", "REDIS_CONN_STRING=private-redis", "UNRELATED=keep-private"}
	original := append([]string{}, raw...)
	child, err := schemaMaintenanceWorkerValues(raw, "public")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(raw, original) {
		t.Fatal("maintenance changed the original inspected startup environment")
	}
	values := map[string]string{}
	for _, entry := range child {
		key, value, _ := strings.Cut(entry, "=")
		values[key] = value
	}
	parsed, err := url.Parse(values["SQL_DSN"])
	if err != nil || parsed.Query().Get("default_transaction_read_only") != "off" || parsed.Query().Get("search_path") != "public" || parsed.Query().Get("sslmode") != "require" || values["PGOPTIONS"] != parsed.Query().Get("options") {
		t.Fatal("maintenance connection did not bind explicit child-only RW and original schema/TLS")
	}
	for _, key := range []string{"DATABASE_URL", "LOG_SQL_DSN", "SESSION_SECRET", "REDIS_CONN_STRING", "UNRELATED", "LMM_CREDIT_TRANSITION_PLAN", "LMM_CREDIT_TRANSITION_SHA256"} {
		if _, exists := values[key]; exists {
			t.Fatal("maintenance inherited unrelated or financial authority")
		}
	}
	if values["OAUTH_SERVER_ENABLED"] != "true" || values["LMM_OIDC_ENABLED"] != "true" {
		t.Fatal("maintenance changed the actual enabled identity storage registry")
	}
	for name, extra := range map[string]string{"financial plan": "LMM_CREDIT_TRANSITION_PLAN=legacy", "external pgfile": "PGPASSFILE=/tmp/private", "invalid oauth": "OAUTH_SERVER_ENABLED=TRUE", "duplicate": "PGOPTIONS=" + options} {
		t.Run(name, func(t *testing.T) {
			bad := append(append([]string{}, raw...), extra)
			if _, err := schemaMaintenanceWorkerValues(bad, "public"); err == nil {
				t.Fatal("invalid maintenance authority accepted")
			}
		})
	}
	if _, err := schemaMaintenanceWorkerValues(raw, "other"); err == nil {
		t.Fatal("maintenance changed the inspected search path")
	}
	unsealed := append([]string{}, raw...)
	unsealed[1] = "PGOPTIONS="
	if _, err := schemaMaintenanceWorkerValues(unsealed, "public"); err == nil {
		t.Fatal("maintenance upgraded an unqualified environment")
	}
}
