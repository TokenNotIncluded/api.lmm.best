package deploycli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

const schemaWorkerExecTestSecret = "postgresql://fixture:private-fixture-password@invalid/never-connect"

// This test executable acts as a harmless provider. It does not connect to a
// database or load any real worker configuration. Only the exact fixture child
// invocation enters this branch; ordinary test execution continues below.
func init() {
	if os.Getenv("LMM_SCHEMA_WORKER_EXEC_FIXTURE") != "1" || len(os.Args) < 2 || os.Args[1] != "migrate" {
		return
	}
	expected := []string{"migrate", "--schema-only", "--maintenance-owner", os.Getenv("LMM_SCHEMA_WORKER_EXEC_OWNER"), "--maintenance-owner-sha256", strings.Repeat("a", 64)}
	if os.Geteuid() != 0 || !reflect.DeepEqual(os.Args[1:], expected) || os.Getenv("LMM_SCHEMA_WORKER_EXEC_SECRET") != schemaWorkerExecTestSecret || os.Getenv("LMM_SCHEMA_WORKER_PARENT_ONLY") != "" {
		os.Exit(73)
	}
	if err := os.WriteFile(os.Getenv("LMM_SCHEMA_WORKER_EXEC_STARTED"), []byte("started\n"), 0600); err != nil {
		os.Exit(74)
	}
	switch os.Getenv("LMM_SCHEMA_WORKER_EXEC_MODE") {
	case "success":
		_, _ = os.Stdout.WriteString("PostgreSQL schema-only maintenance transaction committed; application data migrations were not run\n")
	case "failure":
		_, _ = os.Stderr.WriteString(schemaWorkerExecTestSecret)
		os.Exit(23)
	case "stdout-limit":
		_, _ = os.Stdout.Write(bytes.Repeat([]byte("x"), (1<<20)+1))
	case "stderr-limit":
		_, _ = os.Stderr.Write(bytes.Repeat([]byte("x"), (1<<20)+1))
	default:
		os.Exit(75)
	}
	os.Exit(0)
}

type schemaWorkerExecCaptureRunner struct {
	command productionCommand
	calls   int
}

func (runner *schemaWorkerExecCaptureRunner) Run(_ context.Context, command productionCommand) ([]byte, error) {
	runner.command, runner.calls = command, runner.calls+1
	return nil, nil
}

func TestSchemaMaintenanceWorkerProviderPreservesCommandConstraints(t *testing.T) {
	runner := &schemaWorkerExecCaptureRunner{}
	runtime := &productionRuntime{runner: runner}
	child := []string{"SYNTHETIC_SECRET=private"}
	_, err := runtime.runSchemaMaintenanceWorkerProvider(context.Background(), "/qualified/provider", "/qualified/owner.json", strings.Repeat("a", 64), child)
	if err != nil {
		t.Fatal(err)
	}
	expected := productionCommand{Name: commandRunuser, Args: []string{"--user", "root", "--", "/qualified/provider", "migrate", "--schema-only", "--maintenance-owner", "/qualified/owner.json", "--maintenance-owner-sha256", strings.Repeat("a", 64)}, Env: child, Timeout: 10 * time.Minute, Sensitive: true, OutputLimit: 1 << 20}
	if !reflect.DeepEqual(runner.command, expected) {
		t.Fatal("worker lost its fixed wrapper, exact argv/environment, timeout, private errors, or output limit")
	}
	if _, err := runtime.runSchemaMaintenanceWorkerProvider(context.Background(), "relative-provider", "/qualified/owner.json", strings.Repeat("a", 64), child); err == nil || runner.calls != 1 {
		t.Fatal("relative provider reached the command runner")
	}
	if _, err := runVerifiedBinary(context.Background(), runner, "/qualified/provider", nil, child, "/qualified", time.Second, true); err != nil || runner.command.OutputLimit != 0 || runner.command.Dir != "/qualified" {
		t.Fatal("existing verified-binary callers changed behavior")
	}
}

func TestSchemaMaintenanceWorkerProviderActualOSRunner(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("actual fixed runuser --user root execution requires an isolated local root test process")
	}
	provider, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	runner := osProductionCommandRunner{}
	// The fix must not admit arbitrary artifact paths as command executables.
	if _, err := runner.Run(context.Background(), productionCommand{Name: provider, Sensitive: true}); err == nil || !strings.Contains(err.Error(), "not allowlisted") {
		t.Fatal("default runner admitted a dynamic executable directly")
	}
	runtime := &productionRuntime{runner: runner}
	t.Setenv("LMM_SCHEMA_WORKER_PARENT_ONLY", "must-not-leak")
	for _, mode := range []string{"success", "failure", "stdout-limit", "stderr-limit", "cancelled-before-start"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			ownerPath := filepath.Join(root, "synthetic-owner.json")
			started := filepath.Join(root, "provider-started")
			child := []string{"PATH=/usr/bin:/bin", "LMM_SCHEMA_WORKER_EXEC_FIXTURE=1", "LMM_SCHEMA_WORKER_EXEC_MODE=" + mode, "LMM_SCHEMA_WORKER_EXEC_OWNER=" + ownerPath, "LMM_SCHEMA_WORKER_EXEC_STARTED=" + started, "LMM_SCHEMA_WORKER_EXEC_SECRET=" + schemaWorkerExecTestSecret}
			ctx := context.Background()
			if mode == "cancelled-before-start" {
				cancelled, cancel := context.WithCancel(ctx)
				cancel()
				ctx = cancelled
			}
			output, runErr := runtime.runSchemaMaintenanceWorkerProvider(ctx, provider, ownerPath, strings.Repeat("a", 64), child)
			if mode == "success" {
				if runErr != nil || string(output) != "PostgreSQL schema-only maintenance transaction committed; application data migrations were not run\n" {
					t.Fatalf("verified provider did not execute successfully: %v", runErr)
				}
			} else if runErr == nil || len(output) != 0 || strings.Contains(runErr.Error(), schemaWorkerExecTestSecret) || strings.Contains(runErr.Error(), "private-fixture-password") {
				t.Fatal("failed, oversized, or cancelled provider lost bounded private failure handling")
			}
			_, statErr := os.Stat(started)
			if mode == "cancelled-before-start" {
				if !os.IsNotExist(statErr) {
					t.Fatal("cancelled operation started a provider")
				}
			} else if statErr != nil {
				t.Fatal("test provider did not actually execute")
			}
		})
	}
	// Mid-flight timeout remains uncertain. These tests do not claim that
	// cancelling runuser proves the absence of every descendant process.
}
