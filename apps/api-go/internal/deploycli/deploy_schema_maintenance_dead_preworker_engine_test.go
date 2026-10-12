//go:build !windows

package deploycli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// These exercise local authority dispatch and executable identity only. They
// do not authorize a recovery, qualify signatures, or connect to PostgreSQL.
func TestSchemaMaintenanceDeadRuntimeLegacyEngineRemainsExact(t *testing.T) {
	f := newSchemaMaintenanceDeadRuntimeFixture(t)
	raw, err := os.ReadFile("/proc/self/exe")
	if err != nil {
		t.Fatal(err)
	}
	engineSHA := startupContentSHA256(raw)
	f.authority.Plan.Worker.Provider.DeployEngineSHA256 = engineSHA
	commands := 0
	f.runtime.runner = existingSchemaTestRunner{run: func(productionCommand) ([]byte, error) {
		commands++
		return nil, errors.New("unexpected command")
	}}
	got, err := f.runtime.schemaMaintenanceDeadRecoveryEngine(context.Background(), f.authority)
	if err != nil || got != engineSHA || commands != 0 {
		t.Fatalf("legacy exact current executable/absence failed: sha=%s commands=%d err=%v", got, commands, err)
	}
	for _, declared := range []string{engineSHA, strings.Repeat("a", 64), "invalid"} {
		a := f.authority
		a.RecoveryEngineSHA256 = declared
		if got, err := f.runtime.schemaMaintenanceDeadRecoveryEngine(context.Background(), a); err == nil || got != "" {
			t.Fatal("legacy recovery accepted an explicit engine override")
		}
	}
	a := f.authority
	a.Plan.Worker.Provider.DeployEngineSHA256 = strings.Repeat("a", 64)
	if got, err := f.runtime.schemaMaintenanceDeadRecoveryEngine(context.Background(), a); err == nil || got != "" {
		t.Fatal("legacy recovery accepted a different actual executable")
	}
	if commands != 0 {
		t.Fatal("local executable dispatch ran a qualification command")
	}
}

func TestSchemaMaintenanceDeadRuntimeLegacyEngineStillRejectsWorkerEvidence(t *testing.T) {
	for _, name := range []string{"schema-worker-intent.json", "schema-worker-result.json", "schema-worker-output.log", "schema-outcome.json", "recovery-binding.json", "host-finished.json"} {
		t.Run(name, func(t *testing.T) {
			f := newSchemaMaintenanceDeadRuntimeFixture(t)
			raw, err := os.ReadFile("/proc/self/exe")
			if err != nil {
				t.Fatal(err)
			}
			f.authority.Plan.Worker.Provider.DeployEngineSHA256 = startupContentSHA256(raw)
			workspace := schemaMaintenanceHostWorkspace(f.runtime.paths, f.authority.Plan.TransitionID)
			schemaMaintenanceRecoveryWriteFile(t, filepath.Join(workspace, "state", name), nil)
			if got, err := f.runtime.schemaMaintenanceDeadRecoveryEngine(context.Background(), f.authority); err == nil || got != "" {
				t.Fatal("legacy engine dispatch bypassed exact worker/recovery absence")
			}
		})
	}
}

func TestSchemaMaintenanceDeadRuntimePreExecEngineCannotFallBack(t *testing.T) {
	for _, digest := range []string{"", "invalid", strings.Repeat("a", 64)} {
		t.Run(digest, func(t *testing.T) {
			f := newSchemaMaintenanceDeadRuntimeFixture(t)
			raw, err := os.ReadFile("/proc/self/exe")
			if err != nil {
				t.Fatal(err)
			}
			// Even an otherwise usable legacy executable and empty worker
			// workspace cannot replace missing typed pre-execution evidence.
			f.authority.Plan.Worker.Provider.DeployEngineSHA256 = startupContentSHA256(raw)
			f.authority.PreExecRecovery = &productionReleaseFilePlan{Path: filepath.Join(t.TempDir(), "absent-authority.json"), SHA256: strings.Repeat("b", 64)}
			f.authority.RecoveryEngineSHA256 = digest
			commands := 0
			f.runtime.runner = existingSchemaTestRunner{run: func(productionCommand) ([]byte, error) {
				commands++
				return nil, errors.New("unexpected command")
			}}
			if got, err := f.runtime.schemaMaintenanceDeadRecoveryEngine(context.Background(), f.authority); err == nil || got != "" {
				t.Fatal("missing typed recovery authority fell back to the legacy lane")
			}
			if commands != 0 {
				t.Fatal("invalid local recovery authority reached qualification commands")
			}
		})
	}
}

func TestSchemaMaintenanceDeadRuntimeResultBindsValidatedEffectiveEngine(t *testing.T) {
	f := newSchemaMaintenanceDeadRuntimeFixture(t)
	now := time.Date(2026, 10, 12, 15, 4, 5, 123, time.UTC)
	validatedEngine := strings.Repeat("a", 64)
	f.authority.RecoveryEngineSHA256 = strings.Repeat("b", 64)
	expected := newSchemaMaintenanceDeadCASResult(f.authority, now)
	expected.RecovererEXESHA256 = validatedEngine
	got := newSchemaMaintenanceDeadCASResultForEngine(f.authority, now, validatedEngine)
	if got != expected {
		t.Fatal("recoverer result changed original authority or used the unvalidated declared engine")
	}
}
