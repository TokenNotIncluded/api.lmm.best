package deploycli

import (
	"context"
	"errors"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func requireSchemaBackupStage(t *testing.T, err error, stage string) *schemaMaintenanceBackupFailure {
	t.Helper()
	safe, ok := err.(*schemaMaintenanceBackupFailure)
	if !ok || safe.stage != stage || errors.Unwrap(err) != nil || strings.Contains(err.Error(), "SECRET") || strings.Contains(err.Error(), "postgresql://") {
		t.Fatalf("backup failure is not the required redacted %s: %v", stage, err)
	}
	return safe
}

type schemaBackupSecretWriter struct{}

func (schemaBackupSecretWriter) Write([]byte) (int, error) {
	return 0, errors.New("SECRET row payload postgresql://user:password@private/db")
}

func TestSchemaMaintenanceBackupPipeFixedDiagnostics(t *testing.T) {
	for _, mode := range []string{"age-start", "dump-start", "partial-dump-exit", "dump-signal", "age-exit", "writer-error", "bad-header", "timeout", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			dump := schemaBackupCollectProcess(ctx, "dump")
			age := exec.CommandContext(ctx, "/usr/bin/cat") // accepting fixture sink, not an encryption claim
			var destination io.Writer = io.Discard
			stage := "PIPE_WAIT"
			switch mode {
			case "age-start":
				age = exec.CommandContext(ctx, "/unavailable/SECRET-age")
				stage = "PIPE_AGE_START"
			case "dump-start":
				dump = exec.CommandContext(ctx, "/unavailable/SECRET-dump")
				stage = "PIPE_DUMP_START"
			case "partial-dump-exit":
				dump = schemaBackupCollectProcess(ctx, "partial-dump-error")
			case "dump-signal":
				dump = schemaBackupCollectProcess(ctx, "signal")
			case "age-exit":
				dump = schemaBackupCollectProcess(ctx, "blocked-dump")
				age = schemaBackupCollectProcess(ctx, "encrypt-error")
			case "writer-error":
				destination = schemaBackupSecretWriter{}
			case "bad-header":
				dump = schemaBackupCollectProcess(ctx, "bad-magic")
				stage = "PIPE_ARCHIVE_HEADER"
			case "timeout":
				cancel()
				ctx, cancel = context.WithTimeout(context.Background(), 100*time.Millisecond)
				defer cancel()
				dump = schemaBackupCollectProcess(ctx, "blocked-dump")
				age = schemaBackupCollectProcess(ctx, "stall-encrypt")
				stage = "PIPE_TIMEOUT"
			case "canceled":
				cancel()
				stage = "PIPE_CANCELED"
			}
			_, _, err := runSchemaMaintenanceBackupPipe(ctx, dump, age, destination)
			safe := requireSchemaBackupStage(t, err, stage)
			switch mode {
			case "age-start":
				if safe.dump != "not_run" || safe.copy != "not_run" || safe.age != "failed" {
					t.Fatal("unstarted dump was reported successful")
				}
			case "dump-start":
				if safe.dump != "failed" || safe.copy != "not_run" {
					t.Fatal("unstarted dump/copy result is inaccurate")
				}
			case "partial-dump-exit":
				if safe.dump != "exit_23" || safe.age != "ok" || safe.copy != "ok" {
					t.Fatal("complete accepting stream hid failed producer exit")
				}
			case "dump-signal":
				if safe.dump != "signal" {
					t.Fatal("producer signal not distinguished")
				}
			case "age-exit":
				if safe.age != "exit_24" {
					t.Fatal("encryption exit not preserved")
				}
			case "timeout":
				if safe.context != "timeout" {
					t.Fatal("timeout not distinguished from child termination")
				}
			case "canceled":
				if safe.context != "canceled" || safe.dump != "not_run" || safe.copy != "not_run" {
					t.Fatal("pre-canceled pipeline reported unstarted work as successful")
				}
			}
			preserved := schemaMaintenanceBackupSafeFailure(err, "STREAM_EXECUTE")
			if preserved.Error() != err.Error() {
				t.Fatal("native collect lost the safe pipeline diagnostic")
			}
		})
	}
}

func TestSchemaMaintenanceBackupFailureFiniteVocabulary(t *testing.T) {
	secret := "SECRET postgresql://user:password@private/db"
	for _, err := range []error{
		errors.New(secret),
		&schemaMaintenanceBackupFailure{stage: secret, dump: secret, age: secret, copy: secret, context: secret},
	} {
		safe := schemaMaintenanceBackupSafeFailure(err, "STREAM_EXECUTE")
		if strings.Contains(safe.Error(), "SECRET") || strings.Contains(safe.Error(), "private") || errors.Unwrap(safe) != nil {
			t.Fatal("arbitrary child/writer error escaped the finite diagnostic vocabulary")
		}
	}
	for _, stage := range []string{"SOURCE_BEFORE", "SOURCE_AFTER", "SOURCE_COMPARE", "CIPHER_VERIFY", "SET_CHRONOLOGY", "SET_SIZE", "SET_WRITE"} {
		err := schemaMaintenanceBackupFail(stage)
		if schemaMaintenanceBackupParseFailure(err.Error(), "UNCLASSIFIED").Error() != err.Error() {
			t.Fatalf("poststream diagnostic %s did not survive safe transport", stage)
		}
	}
	if got := schemaMaintenanceBackupClass("exit_256"); got != "failed" {
		t.Fatal("out-of-range process status accepted")
	}
}

func TestSchemaMaintenanceBackupCollectTransportPreservesOnlyFixedNativeDiagnostics(t *testing.T) {
	const prefix = "command ssh failed: exit status 1: schema-maintenance rpc: schema maintenance RPC backup-export failed without retry: "
	safe := schemaMaintenanceBackupFail("SOURCE_AFTER").Error()
	for _, tc := range []struct{ name, input, stage string }{
		{"native-code", prefix + safe, "SOURCE_AFTER"},
		{"secret-suffix", prefix + safe + " SECRET_CONNECTION_PASSWORD", "CONTROLLER_EXPORT_TRANSPORT"},
		{"other-ssh-exit", "command ssh failed: exit status 255: SECRET", "CONTROLLER_EXPORT_TRANSPORT"},
		{"other-cli", "command ssh failed: exit status 1: SECRET", "CONTROLLER_EXPORT_TRANSPORT"},
		{"invalid-stage", prefix + strings.Replace(safe, "SOURCE_AFTER", "SECRET", 1), "CONTROLLER_EXPORT_TRANSPORT"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runtime, plan, sha, captures, runner := schemaBackupCollectImportFixture(t)
			runner.sshErr = errors.New(tc.input)
			_, err := runtime.collectSchemaMaintenanceBackup(context.Background(), plan, sha, captures)
			requireSchemaBackupStage(t, err, tc.stage)
			if _, err := runtime.collectSchemaMaintenanceBackup(context.Background(), plan, sha, captures); err == nil || len(runner.commands) != 1 {
				t.Fatal("diagnostic allowed an existing export intent to be replayed")
			}
			if _, err := controllerBackupReadPrivate(filepath.Join(plan.BackupPolicy.ControllerDirectory, ".complete"), 1000, 65); err == nil {
				t.Fatal("failed export was marked imported")
			}
		})
	}
}

func TestSchemaMaintenanceBackupCollectAuthorityStage(t *testing.T) {
	var controller *productionSchemaMaintenanceController
	_, err := controller.collectBackup(context.Background())
	requireSchemaBackupStage(t, err, "AUTHORITY")
}
