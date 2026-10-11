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

func TestEnvironmentAllowsProcessSettings(t *testing.T) {
	env := map[string]string{
		"PATH": "/usr/bin", "GOMAXPROCS": "2", "MISE_AUTO_INSTALL": "false",
	}
	if err := validateEnvironment(func(key string) string { return env[key] }); err != nil {
		t.Fatal(err)
	}
}

func TestEnvironmentRejectsRetiredRuntimeInputs(t *testing.T) {
	for _, name := range []string{"LMM_EXTENSION_TOKEN_FILE", "LMM_EXTENSION_LISTEN", "LMM_EXTENSION_MODULES", "LMM_EXTENSION_WEB_COMPAT", "LMM_CORE_RPC_SOCKET", "LMM_CORE_RPC_TOKEN_FILE", "LMM_CORE_COMMERCE_RPC_TOKEN_FILE", "LMM_STORE_DATABASE_URL_FILE", "LMM_STORE_DATABASE_NAME", "LMM_SUPPORT_DATABASE_URL_FILE", "LMM_SUPPORT_DATABASE_NAME", "LMM_LEGAL_CONFIG_FILE"} {
		err := validateEnvironment(func(key string) string {
			if key == name {
				return "private-value"
			}
			return ""
		})
		if err == nil || !strings.Contains(err.Error(), name) || strings.Contains(err.Error(), "private-value") {
			t.Fatalf("runtime input did not fail closed: %s %v", name, err)
		}
	}
}

func TestEnvironmentAllowsEmptyLegacySettings(t *testing.T) {
	if err := validateEnvironment(func(string) string { return "" }); err != nil {
		t.Fatal(err)
	}
}
