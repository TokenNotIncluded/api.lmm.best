package app

import (
	"strings"
	"testing"
)

func TestEnvironmentRejectsLegacyAndCoreDatabaseSettings(t *testing.T) {
	for _, name := range []string{"SQL_DSN", "LOG_SQL_DSN", "DATABASE_URL", "LMM_CORE_DATABASE_URL", "LMM_CORE_DATABASE_URL_FILE", "LMM_DB_MIGRATION_MODE"} {
		t.Run(name, func(t *testing.T) {
			const secret = "private-connection-value-do-not-log"
			err := validateEnvironment(func(key string) string {
				if key == name {
					return secret
				}
				return ""
			})
			if err == nil || !strings.Contains(err.Error(), name) {
				t.Fatalf("expected a named configuration error, got %v", err)
			}
			if strings.Contains(err.Error(), secret) {
				t.Fatal("configuration error exposes a secret")
			}
		})
	}
}

func TestEnvironmentAllowsExtensionAndRPCSettings(t *testing.T) {
	env := map[string]string{
		"LMM_EXTENSION_LISTEN":           "127.0.0.1:8081",
		"LMM_EXTENSION_TOKEN_FILE":       "/run/secrets/extension",
		"LMM_CORE_RPC_SOCKET":            "/run/lmm-core/control.sock",
		"LMM_CORE_RPC_TOKEN_FILE":        "/run/secrets/rpc",
		"LMM_EXTENSION_EXAMPLE_DATABASE": "extension-owned-storage",
	}
	if err := validateEnvironment(func(key string) string { return env[key] }); err != nil {
		t.Fatal(err)
	}
}

func TestEnvironmentAllowsEmptyLegacySettings(t *testing.T) {
	if err := validateEnvironment(func(string) string { return "" }); err != nil {
		t.Fatal(err)
	}
}
