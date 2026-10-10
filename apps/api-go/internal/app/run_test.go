package app

import (
	"os"
	"strings"
	"testing"
)

func TestRunRejectsCoreConfigurationBeforeCredentialIO(t *testing.T) {
	args := os.Args
	os.Args = []string{"lmm-extensions"}
	t.Cleanup(func() { os.Args = args })
	for _, name := range []string{"SQL_DSN", "LOG_SQL_DSN", "DATABASE_URL", "LMM_CORE_DATABASE_URL", "LMM_CORE_DATABASE_URL_FILE", "LMM_DB_MIGRATION_MODE"} {
		t.Setenv(name, "")
	}
	const secret = "private-core-connection-do-not-log"
	t.Setenv("LMM_CORE_DATABASE_URL", secret)
	t.Setenv("LMM_EXTENSION_TOKEN_FILE", "/missing/extension-credential")
	err := Run()
	if err == nil || !strings.Contains(err.Error(), "LMM_CORE_DATABASE_URL") {
		t.Fatalf("must reject core configuration before credential I/O, got %v", err)
	}
	if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), "open extension") {
		t.Fatalf("unexpected startup error: %v", err)
	}
}

func TestRunRejectsRetiredCommandsBeforeCredentialIO(t *testing.T) {
	args := os.Args
	t.Cleanup(func() { os.Args = args })
	for _, command := range []string{"migrate", "serve", "deploy"} {
		t.Run(command, func(t *testing.T) {
			os.Args = []string{"lmm-extensions", command}
			err := Run()
			if err == nil || !strings.Contains(err.Error(), "accepts no subcommands") {
				t.Fatalf("must reject retired command, got %v", err)
			}
		})
	}
}
