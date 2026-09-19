//go:build !windows

package appcli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func preStopRecoveryFixture(t *testing.T) productionFixture {
	t.Helper()
	fixture := newProductionFixture(t)
	fixture.runtime.paths.EdgeAssetRoot = t.TempDir()
	if err := os.WriteFile(filepath.Join(fixture.runtime.paths.NginxRoot, "lmm-api-http-map.conf"), []byte("old-map\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fixture.runner.journalLossAfterAdmission = true
	if _, err := fixture.runtime.apply(context.Background(), fixture.workspace, fixture.options); err == nil {
		t.Fatal("apply unexpectedly succeeded")
	}
	status, err := fixture.runtime.readStatus(fixture.workspace)
	if err != nil || status.Phase != "ROLLBACK_REQUIRED" {
		t.Fatalf("status=%+v err=%v", status, err)
	}
	if !fixture.runner.serviceActive {
		t.Fatal("writer stopped before pre-stop recovery failure")
	}
	return fixture
}

func TestProductionRollbackBeforeWriterStopRecoversWithoutSecondMutation(t *testing.T) {
	fixture := preStopRecoveryFixture(t)
	before := len(fixture.runner.events)
	status, err := fixture.runtime.rollback(context.Background(), fixture.workspace, "pre-stop-recovery")
	if err != nil || status.Phase != "ROLLED_BACK" {
		t.Fatalf("status=%+v err=%v", status, err)
	}
	if status.Format != productionStatusFormat || status.DeploymentID != fixture.workspace.id || status.UpdatedUTC.IsZero() {
		t.Fatalf("recovery response lacks persisted identity: %+v", status)
	}
	for _, event := range fixture.runner.events[before:] {
		if event == "systemd-stop" || strings.HasPrefix(event, "paru-") || strings.HasPrefix(event, "migrate:") {
			t.Fatalf("recovery performed a second mutation: %s", event)
		}
	}
	if _, err := os.Stat(fixture.runtime.paths.TransactionLock); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("transaction lock was not released: %v", err)
	}
}

func TestProductionRollbackUsesFullDrainForChangedWriterIdentity(t *testing.T) {
	fixture := preStopRecoveryFixture(t)
	manifest, err := fixture.runtime.readManifest(fixture.workspace)
	if err != nil {
		t.Fatal(err)
	}
	manifest.BillingGate.GoPID++
	if err := fixture.runtime.writeManifest(fixture.workspace, manifest); err != nil {
		t.Fatal(err)
	}
	before := len(fixture.runner.events)
	fixture.runner.journalLossAfterAdmission = false
	status, err := fixture.runtime.rollback(context.Background(), fixture.workspace, "pre-stop-identity")
	if err != nil || status.Phase != "ROLLED_BACK" {
		t.Fatalf("changed writer identity did not use complete rollback: status=%+v err=%v", status, err)
	}
	for _, event := range fixture.runner.events[before:] {
		if event == "systemd-stop" {
			return
		}
	}
	t.Fatalf("complete rollback did not drain and stop the writer: events=%v", fixture.runner.events[before:])
}

func TestProductionRollbackBeforeWriterStopRejectsChangedEvidence(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*testing.T, *productionFixture, productionManifest)
	}{
		{name: "invocation", mutate: func(t *testing.T, f *productionFixture, m productionManifest) {
			m.BillingGate.GoInvocationID = strings.Repeat("2", 32)
			if err := f.runtime.writeManifest(f.workspace, m); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "edge configuration", mutate: func(t *testing.T, f *productionFixture, _ productionManifest) {
			if err := os.WriteFile(filepath.Join(f.runtime.paths.NginxRoot, "lmm-api-http-map.conf"), []byte("changed-map\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "installed package", mutate: func(_ *testing.T, f *productionFixture, _ productionManifest) {
			f.runner.installedGoVersion = f.runner.newVersion
		}},
		{name: "environment", mutate: func(t *testing.T, f *productionFixture, _ productionManifest) {
			if err := os.WriteFile(filepath.Join(f.runtime.paths.ConfigDir, "lmm-api-go.env"), []byte("changed\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "unknown drop-in", mutate: func(t *testing.T, f *productionFixture, _ productionManifest) {
			if err := os.WriteFile(filepath.Join(f.runtime.paths.DropInDir, "99-unknown.conf"), []byte("[Service]\nMemoryMax=1G\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "archive", mutate: func(t *testing.T, f *productionFixture, _ productionManifest) {
			if err := os.WriteFile(f.options.GoRollbackPackage, []byte("damaged"), 0o700); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "lock", mutate: func(t *testing.T, f *productionFixture, _ productionManifest) {
			if err := os.WriteFile(filepath.Join(f.runtime.paths.TransactionLock, productionTransactionMarker), []byte("format=1\ndeployment_id=wrong\nstatus=ACTIVE\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "probe", mutate: func(_ *testing.T, f *productionFixture, _ productionManifest) {
			f.runner.preStopProbeFailure = true
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			f := preStopRecoveryFixture(t)
			manifest, err := f.runtime.readManifest(f.workspace)
			if err != nil {
				t.Fatal(err)
			}
			test.mutate(t, &f, manifest)
			before := len(f.runner.events)
			if _, err := f.runtime.rollback(context.Background(), f.workspace, "pre-stop-evidence"); err == nil {
				t.Fatal("changed evidence was accepted")
			}
			for _, event := range f.runner.events[before:] {
				if event == "systemd-stop" || strings.HasPrefix(event, "paru-") || strings.HasPrefix(event, "migrate:") {
					t.Fatalf("evidence rejection performed mutation: %s", event)
				}
			}
			if _, err := os.Stat(f.runtime.paths.TransactionLock); err != nil {
				t.Fatalf("recovery lock was released after rejection: %v", err)
			}
		})
	}
}

type admissionTimestampRunner struct {
	productionCommandRunner
	started string
}

func (r admissionTimestampRunner) Run(ctx context.Context, command productionCommand) ([]byte, error) {
	if command.Name == commandSystemctl && strings.Contains(strings.Join(command.Args, " "), "--property=ExecMainStartTimestamp") {
		return []byte(r.started), nil
	}
	return r.productionCommandRunner.Run(ctx, command)
}
func TestProductionAdmissionTimeoutRecoveryRequiresPredatingWriter(t *testing.T) {
	for _, kind := range []string{"original", "replacement", "unknown"} {
		t.Run(kind, func(t *testing.T) {
			f := preStopRecoveryFixture(t)
			m, err := f.runtime.readManifest(f.workspace)
			if err != nil {
				t.Fatal(err)
			}
			m.BillingGate.GoPID = 0
			m.BillingGate.GoInvocationID = ""
			m.BillingGate.AdmissionClosed = false
			m.BillingGate.StopStartedUTC = time.Time{}
			if err := f.runtime.writeManifest(f.workspace, m); err != nil {
				t.Fatal(err)
			}
			started := m.BillingGate.StartedUTC.Add(-time.Hour).UTC().Format("Mon 2006-01-02 15:04:05 MST")
			if kind == "replacement" {
				started = m.BillingGate.StartedUTC.Add(time.Minute).UTC().Format("Mon 2006-01-02 15:04:05 MST")
			}
			if kind == "unknown" {
				started = "n/a"
			}
			f.runtime.runner = admissionTimestampRunner{f.runner, started}
			before := len(f.runner.events)
			status, err := f.runtime.rollback(context.Background(), f.workspace, "admission-timeout-recovery")
			if kind == "original" && (err != nil || status.Phase != "ROLLED_BACK") {
				t.Fatalf("status=%+v error=%v", status, err)
			}
			if kind != "original" && err == nil {
				t.Fatal("unverified writer accepted")
			}
			for _, event := range f.runner.events[before:] {
				if event == "systemd-stop" || strings.HasPrefix(event, "paru-") || strings.HasPrefix(event, "migrate:") {
					t.Fatalf("unexpected mutation: %s", event)
				}
			}
		})
	}
}
