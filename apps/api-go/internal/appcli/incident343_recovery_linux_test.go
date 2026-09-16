//go:build linux

package appcli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// All commands and clocks in these tests are local fakes; production is never used.
type incident343Runner struct {
	*fakeProductionRunner
	relationCount string
	backupFail    bool
	started       bool
}

func (r *incident343Runner) Run(ctx context.Context, c productionCommand) ([]byte, error) {
	if c.Name == commandPSQL {
		q := strings.Join(c.Args, " ")
		if strings.Contains(q, "pg_database_size") {
			return []byte("1024\n"), nil
		}
		if strings.Contains(q, "pg_catalog.pg_class") {
			v := r.relationCount
			if v == "" {
				v = "0"
			}
			return []byte(v + "\n"), nil
		}
	}
	if c.Name == commandPGDump && r.backupFail {
		return nil, errors.New("injected backup failure")
	}
	if c.Name == commandSystemctl && len(c.Args) > 0 && c.Args[0] == "start" {
		r.commands = append(r.commands, c)
		r.serviceActive = true
		r.started = true
		return nil, nil
	}
	return r.fakeProductionRunner.Run(ctx, c)
}

func incident343Fixture(t *testing.T) (productionFixture, productionManifest, *incident343Runner) {
	t.Helper()
	f := newProductionFixture(t)
	f.options.WebChanged = false
	f.options.WebPackage = f.options.WebRollbackPackage
	f.options.WebPackageSHA256 = f.options.WebRollbackSHA256
	f.runner.restartOnEnable = true
	if _, err := f.runtime.apply(context.Background(), f.workspace, f.options); err == nil || !strings.Contains(err.Error(), "restart baseline hard stop") {
		t.Fatalf("expected installed candidate failure, got %v", err)
	}
	m, err := f.runtime.readManifest(f.workspace)
	if err != nil {
		t.Fatal(err)
	}
	f.runner.serviceActive = false
	r := &incident343Runner{fakeProductionRunner: f.runner}
	f.runtime.runner = r
	if err := validateInstalledSchemaRecovery(m, mustIncidentStatus(t, f)); err != nil {
		t.Fatalf("fixture: %v %+v", err, m.BillingGate)
	}
	return f, m, r
}

func mustIncidentStatus(t *testing.T, f productionFixture) productionStatus {
	t.Helper()
	s, err := f.runtime.readStatus(f.workspace)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestIncident343RecoveryRequiresExactConfirmation(t *testing.T) {
	for _, args := range [][]string{nil, {"--confirm", "other"}, {"--confirm", "api.lmm.best", "--force"}} {
		var out, stderr bytes.Buffer
		if RunIncident343Recovery(args, &out, &stderr, func(context.Context, string, string) error { return nil }) != ExitUsage {
			t.Fatal("accepted invalid confirmation")
		}
	}
}

func TestIncident343RecoveryUsesExistingCandidateAndNativeObservation(t *testing.T) {
	f, m, r := incident343Fixture(t)
	before := *f.clock
	calls := 0
	got, err := f.runtime.recoverInstalledSchema(context.Background(), f.workspace, m, func(ctx context.Context, dsn, schema string) error {
		calls++
		if dsn != "postgres://user:password@127.0.0.1/lmm" || schema != "public" {
			t.Fatal("unexpected private schema input")
		}
		if r.serviceActive {
			t.Fatal("DDL ran with active writer")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Phase != "AWAITING_CONFIRMATION" || calls != 1 || !r.started {
		t.Fatalf("result=%+v calls=%d started=%v", got, calls, r.started)
	}
	if f.clock.Sub(before) < 2*time.Minute {
		t.Fatal("observation was shortened")
	}
	if r.installedGoVersion != r.newVersion || r.installedWebVersion != r.oldVersion {
		t.Fatal("package pair was changed")
	}
	after, err := f.runtime.readManifest(f.workspace)
	if err != nil {
		t.Fatal(err)
	}
	if after.ObservationStartedUTC.IsZero() || !after.BillingGate.AdmissionReopened {
		t.Fatal("native recovery was not recorded")
	}
	if _, err := os.Stat(f.runtime.paths.TransactionLock); err != nil {
		t.Fatal("confirmation lock lost", err)
	}
	if data, err := os.ReadFile(filepath.Join(f.runtime.paths.NginxRoot, "lmm-api-locations.conf")); err != nil || bytes.Contains(data, []byte("lmm-billing-drain:")) {
		t.Fatal("native admission did not reopen", err)
	}
	if _, err := f.runtime.recoverInstalledSchema(context.Background(), f.workspace, after, func(context.Context, string, string) error { t.Fatal("replay reached schema"); return nil }); err == nil {
		t.Fatal("replay accepted")
	}
}

func TestIncident343RecoveryRejectsUnsafePreconditionsWithoutSchemaChanges(t *testing.T) {
	for _, name := range []string{"active-writer", "existing-table", "no-old-stop", "admission-altered", "observed", "web-change", "database-changed", "backup-failure"} {
		t.Run(name, func(t *testing.T) {
			f, m, r := incident343Fixture(t)
			switch name {
			case "active-writer":
				r.serviceActive = true
			case "existing-table":
				r.relationCount = "1"
			case "no-old-stop":
				m.BillingGate.StopVerified = false
			case "admission-altered":
				if err := os.WriteFile(filepath.Join(f.runtime.paths.NginxRoot, "lmm-api-locations.conf"), []byte("different\n"), 0644); err != nil {
					t.Fatal(err)
				}
			case "observed":
				m.ObservationStartedUTC = *f.clock
			case "web-change":
				m.Web.Changed = true
			case "database-changed":
				if err := os.WriteFile(filepath.Join(f.runtime.paths.ConfigDir, "lmm-api-go.env"), []byte("SQL_DSN=postgres://other:password@127.0.0.1/elsewhere\n"), 0600); err != nil {
					t.Fatal(err)
				}
			case "backup-failure":
				r.backupFail = true
			}
			called := false
			if _, err := f.runtime.recoverInstalledSchema(context.Background(), f.workspace, m, func(context.Context, string, string) error { called = true; return nil }); err == nil {
				t.Fatal("unsafe precondition accepted")
			}
			if called || r.started {
				t.Fatal("unsafe precondition reached mutation")
			}
			if mustIncidentStatus(t, f).Phase != "ROLLBACK_REQUIRED" {
				t.Fatal("failed status was falsely changed")
			}
		})
	}
}

func TestIncident343SchemaFailureKeepsAdmissionClosed(t *testing.T) {
	f, m, r := incident343Fixture(t)
	_, err := f.runtime.recoverInstalledSchema(context.Background(), f.workspace, m, func(context.Context, string, string) error { return errors.New("synthetic transactional DDL failure") })
	if err == nil || r.started {
		t.Fatal("failed DDL accepted")
	}
	s := mustIncidentStatus(t, f)
	if s.Phase != "ROLLBACK_REQUIRED" || s.Reason != "incident-343-forward-recovery-failure" {
		t.Fatalf("status=%+v", s)
	}
	if err := f.runtime.verifyClosedRecoveryAdmission(f.workspace, m); err != nil {
		t.Fatal("admission changed on DDL failure", err)
	}
}

func TestIncident343IdentityRejectsDifferentPackagePair(t *testing.T) {
	m := productionManifest{DeploymentID: incident343Deployment, ExpectedVersion: "0.2.51"}
	m.Go.CandidateIdentity = "lmm-api-go-bin 0.2.51-1"
	m.Go.CandidateGitRevision = incident343Revision
	m.Web.CandidateIdentity = "lmm-api-web-bin 0.1.71-2"
	if err := validateIncident343Identity(m); err != nil {
		t.Fatal(err)
	}
	m.Go.CandidateGitRevision = strings.Repeat("a", 40)
	if err := validateIncident343Identity(m); err == nil {
		t.Fatal("different source accepted")
	}
}
