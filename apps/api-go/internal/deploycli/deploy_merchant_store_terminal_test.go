//go:build linux

package deploycli

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Package/provider qualification remains a component fixture, while release
// executes the actual terminal proof used by the database holder.
type merchantStoreTerminalRecoveryAuthority struct {
	*merchantStoreHookAuthorityFixture
	runtime       *productionRuntime
	expected      productionManifest
	beforeRelease func()
	released      bool
}

func (authority *merchantStoreTerminalRecoveryAuthority) Request(ctx context.Context, workspace productionWorkspace, manifest productionManifest, release bool) error {
	if release {
		if authority.beforeRelease != nil {
			authority.beforeRelease()
		}
		if err := authority.runtime.verifyMerchantStoreFenceTerminal(ctx, workspace, authority.expected); err != nil {
			return err
		}
	}
	if err := authority.merchantStoreHookAuthorityFixture.Request(ctx, workspace, manifest, release); err != nil {
		return err
	}
	if release {
		authority.released = true
	}
	return nil
}

func TestProductionMerchantStoreAdmissionTimeoutRecoveryReleasesOnlyUnchangedWriter(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*testing.T, productionFixture, *string)
		error  string
		retry  bool
	}{
		{name: "unchanged writer"},
		{name: "replacement after reopen", error: "cannot prove unchanged writer", change: func(t *testing.T, f productionFixture, started *string) {
			m, err := f.runtime.readManifestForRollback(f.workspace)
			if err != nil {
				t.Fatal(err)
			}
			*started = m.BillingGate.StartedUTC.Add(time.Minute).UTC().Format("Mon 2006-01-02 15:04:05 MST")
		}},
		{name: "restart after reopen", error: "service restart count changed", change: func(_ *testing.T, f productionFixture, _ *string) {
			f.runner.restartCounter++
		}},
		{name: "stop evidence after reopen", error: "writer mutation evidence", change: func(t *testing.T, f productionFixture, _ *string) {
			m, err := f.runtime.readManifestForRollback(f.workspace)
			if err != nil {
				t.Fatal(err)
			}
			m.BillingGate.StopVerified = true
			if err := f.runtime.writeManifest(f.workspace, m); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "entry drift after reopen", error: "admission entry changed", change: func(t *testing.T, f productionFixture, _ *string) {
			if err := os.WriteFile(filepath.Join(f.runtime.paths.NginxRoot, "lmm-api-locations.conf"), []byte("return 503;\n"), 0644); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "same owner release retry", error: "fixture same-owner CAS release failed", retry: true},
		{name: "generic terminal label", error: "reopened admission evidence", change: func(t *testing.T, f productionFixture, _ *string) {
			status, err := f.runtime.readStatus(f.workspace)
			if err != nil {
				t.Fatal(err)
			}
			status.Reason = "unchanged-writer-restored"
			if err := f.runtime.writeStatus(f.workspace, status); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newProductionFixture(t)
			f.runtime.paths.EdgeAssetRoot = t.TempDir()
			f.runner.nginxDrainFailure = true
			started := f.clock.Add(-time.Hour).UTC().Format("Mon 2006-01-02 15:04:05 MST")
			authority := &merchantStoreTerminalRecoveryAuthority{
				merchantStoreHookAuthorityFixture: &merchantStoreHookAuthorityFixture{runner: f.runner}, runtime: f.runtime,
			}
			f.runtime.merchantStoreAuthority = authority
			f.runtime.runner = existingSchemaTestRunner{run: func(command productionCommand) ([]byte, error) {
				if command.Name == commandSystemctl && strings.Contains(strings.Join(command.Args, " "), "--property=ExecMainStartTimestamp") {
					return []byte(started), nil
				}
				out, err := f.runner.Run(context.Background(), command)
				return []byte(strings.ReplaceAll(string(out), "2147483600", strconv.Itoa(os.Getpid()))), err
			}}
			if _, err := f.runtime.apply(context.Background(), f.workspace, f.options); err == nil {
				t.Fatal("admission timeout unexpectedly applied candidate")
			}
			status, err := f.runtime.readStatus(f.workspace)
			if err != nil || status.Phase != "ROLLBACK_REQUIRED" || !strings.Contains(status.Failure, "admission or upstream drain timed out") {
				t.Fatalf("actual timeout did not preserve recovery state: %+v, %v", status, err)
			}
			m, err := f.runtime.readManifestForRollback(f.workspace)
			if err != nil {
				t.Fatal(err)
			}
			if m.BillingGate == nil || m.BillingGate.AdmissionClosed || m.BillingGate.AdmissionReopened || m.BillingGate.StopVerified || m.BillingGate.GoPID != 0 || m.BillingGate.GoInvocationID != "" || !m.BillingGate.StopStartedUTC.IsZero() || !f.runner.serviceActive {
				t.Fatal("fixture did not produce a real pre-stop admission timeout")
			}
			m.MerchantStoreWriter = testMerchantWriterContract()
			m.MerchantStoreWriter.Rollback.PayloadSHA256 = mustHashFile(t, filepath.Join("/proc", "self", "exe"))
			if err := f.runtime.writeManifest(f.workspace, m); err != nil {
				t.Fatal(err)
			}
			authority.expected = m
			if test.change != nil {
				authority.beforeRelease = func() { test.change(t, f, &started) }
			}
			authority.failRelease = test.retry
			f.runner.nginxDrainFailure = false
			f.runner.events = nil
			status, err = f.runtime.rollback(context.Background(), f.workspace, "admission-timeout-recovery")
			if test.error == "" {
				if err != nil || status.Phase != "ROLLED_BACK" || status.Reason != productionUnchangedAdmissionRecoveryReason || !authority.released {
					t.Fatalf("native timeout recovery failed terminal release: %+v, %v", status, err)
				}
				if _, err := os.Stat(f.runtime.paths.TransactionLock); !os.IsNotExist(err) {
					t.Fatal("completed recovery retained its transaction lock")
				}
			} else if err == nil || !strings.Contains(err.Error(), test.error) || authority.released {
				t.Fatalf("terminal release did not reject %q: %v", test.error, err)
			}
			if test.retry {
				status, err = f.runtime.readStatus(f.workspace)
				if err != nil || status.Phase != "ROLLBACK_REQUIRED" {
					t.Fatalf("refused release did not preserve explicit recovery state: %+v, %v", status, err)
				}
				authority.failRelease = false
				status, err = f.runtime.rollback(context.Background(), f.workspace, "admission-release-retry")
				if err != nil || status.Phase != "ROLLED_BACK" || status.Reason != productionUnchangedAdmissionRecoveryReason || !authority.released {
					t.Fatalf("same owner release retry lost native recovery proof: %+v, %v", status, err)
				}
				if _, err := os.Stat(f.runtime.paths.TransactionLock); !os.IsNotExist(err) {
					t.Fatal("retried terminal release retained its transaction lock")
				}
			}
			m, err = f.runtime.readManifestForRollback(f.workspace)
			if err != nil || m.BillingGate.AdmissionClosed || !m.BillingGate.AdmissionReopened {
				t.Fatalf("recovery rewrote the drain fact or failed to reopen: %+v, %v", m.BillingGate, err)
			}
			if f.runtime.billingRollback {
				t.Fatal("early recovery leaked its rollback qualification mode")
			}
			for _, event := range f.runner.events {
				if event == "systemd-stop" || event == "systemd-start" || event == "merchant-qualify:candidate-installed" || strings.HasPrefix(event, "paru-") || strings.HasPrefix(event, "migrate:") {
					t.Fatalf("unchanged writer recovery replaced or stopped its N-1: %s", event)
				}
			}
		})
	}
}

// The existing transaction fixture generates the actual close/reopen receipt.
// Package/database qualification and systemd inventory are component fixtures;
// the final running-target check hashes this test process's real executable.
func merchantStoreTerminalFixture(t *testing.T, phase string) (productionFixture, productionManifest) {
	t.Helper()
	f := newProductionFixture(t)
	if _, err := f.runtime.apply(context.Background(), f.workspace, f.options); err != nil {
		t.Fatal(err)
	}
	m, err := f.runtime.readManifest(f.workspace)
	if err != nil {
		t.Fatal(err)
	}
	if m.BillingGate == nil || !m.BillingGate.AdmissionClosed || !m.BillingGate.AdmissionReopened {
		t.Fatal("transaction did not generate closed and reopened admission evidence")
	}
	m.MerchantStoreWriter = testMerchantWriterContract()
	digest := mustHashFile(t, filepath.Join("/proc", "self", "exe"))
	if phase == "ROLLED_BACK" {
		m.MerchantStoreWriter.Rollback.PayloadSHA256 = digest
	} else {
		m.MerchantStoreWriter.Candidate.PayloadSHA256 = digest
	}
	f.runtime.merchantStoreAuthority = &merchantStoreHookAuthorityFixture{runner: f.runner}
	f.runner.events = nil
	f.runtime.runner = existingSchemaTestRunner{run: func(command productionCommand) ([]byte, error) {
		out, err := f.runner.Run(context.Background(), command)
		return []byte(strings.ReplaceAll(string(out), "2147483600", strconv.Itoa(os.Getpid()))), err
	}}
	if err := f.runtime.writeStatus(f.workspace, productionStatus{Phase: phase}); err != nil {
		t.Fatal(err)
	}
	return f, m
}

func TestProductionMerchantStoreFenceTerminalAdmissionEvidence(t *testing.T) {
	for _, phase := range []string{"CONFIRMED", "ROLLED_BACK"} {
		t.Run(phase, func(t *testing.T) {
			for _, test := range []struct {
				name        string
				change      func(*testing.T, productionFixture, *productionManifest)
				wantError   string
				wantQualify bool
			}{
				{name: "restored admission", wantQualify: true},
				{name: "missing gate", wantError: "reopened admission evidence", change: func(_ *testing.T, _ productionFixture, m *productionManifest) {
					m.BillingGate = nil
				}},
				{name: "not reopened", wantError: "reopened admission evidence", change: func(_ *testing.T, _ productionFixture, m *productionManifest) {
					m.BillingGate.AdmissionReopened = false
				}},
				{name: "missing prior closure", wantError: "reopened admission evidence", change: func(_ *testing.T, _ productionFixture, m *productionManifest) {
					m.BillingGate.AdmissionClosed = false
				}},
				{name: "entry drift", wantError: "admission entry changed", change: func(t *testing.T, f productionFixture, _ *productionManifest) {
					if err := os.WriteFile(filepath.Join(f.runtime.paths.NginxRoot, "lmm-api-locations.conf"), []byte("return 503;\n"), 0644); err != nil {
						t.Fatal(err)
					}
				}},
				{name: "wrong running target", wantError: "terminal running writer", wantQualify: true, change: func(_ *testing.T, _ productionFixture, m *productionManifest) {
					m.MerchantStoreWriter.Candidate.PayloadSHA256 = strings.Repeat("f", 64)
					m.MerchantStoreWriter.Rollback.PayloadSHA256 = strings.Repeat("f", 64)
				}},
			} {
				t.Run(test.name, func(t *testing.T) {
					f, m := merchantStoreTerminalFixture(t, phase)
					if test.change != nil {
						test.change(t, f, &m)
					}
					if err := f.runtime.writeManifest(f.workspace, m); err != nil {
						t.Fatal(err)
					}
					err := f.runtime.verifyMerchantStoreFenceTerminal(context.Background(), f.workspace, m)
					if test.wantError == "" && err != nil || test.wantError != "" && (err == nil || !strings.Contains(err.Error(), test.wantError)) {
						t.Fatalf("expected error %q, got %v", test.wantError, err)
					}
					wantEvent := "merchant-qualify:candidate-installed"
					if phase == "ROLLED_BACK" {
						wantEvent = "merchant-qualify:rollback-installed"
					}
					if test.wantQualify && (len(f.runner.events) != 1 || f.runner.events[0] != wantEvent) || !test.wantQualify && len(f.runner.events) != 0 {
						t.Fatalf("unexpected terminal writer qualification: %v", f.runner.events)
					}
				})
			}
		})
	}
}

func TestProductionMerchantStoreFenceTerminalManagedEntry(t *testing.T) {
	for _, phase := range []string{"CONFIRMED", "ROLLED_BACK"} {
		t.Run(phase, func(t *testing.T) {
			f, m := merchantStoreTerminalFixture(t, phase)
			backup := filepath.Join(f.workspace.configRestore, "nginx-edge")
			digest, err := f.runtime.captureEdgePolicyBackup(backup)
			if err != nil {
				t.Fatal(err)
			}
			m.NginxEdgeRestoreSHA256 = digest
			locations := filepath.Join(f.runtime.paths.NginxRoot, "lmm-api-locations.conf")
			if phase == "CONFIRMED" {
				f.runtime.paths.EdgeAssetRoot = t.TempDir()
				for _, entry := range testEdgePolicyTarEntries("") {
					path := filepath.Join(f.runtime.paths.EdgeAssetRoot, entry.name)
					if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(path, []byte(entry.body), os.FileMode(entry.mode)); err != nil {
						t.Fatal(err)
					}
				}
				for _, asset := range f.runtime.edgePolicyAssets() {
					if err := os.MkdirAll(filepath.Dir(asset.Target), 0755); err != nil {
						t.Fatal(err)
					}
					if err := atomicInstallRegularFile(filepath.Join(f.runtime.paths.EdgeAssetRoot, asset.Source), asset.Target, asset.Mode); err != nil {
						t.Fatal(err)
					}
				}
			} else {
				// Rollback reopens its second gate, then restores the pre-upgrade
				// edge snapshot. Its gate's original entry may be the candidate.
				m.BillingGate.OriginalSHA256 = strings.Repeat("f", 64)
			}
			if err := f.runtime.writeManifest(f.workspace, m); err != nil {
				t.Fatal(err)
			}
			if err := f.runtime.verifyMerchantStoreFenceTerminal(context.Background(), f.workspace, m); err != nil {
				t.Fatalf("authorized managed admission entry rejected: %v", err)
			}
			if err := os.WriteFile(locations, []byte("return 503;\n"), 0644); err != nil {
				t.Fatal(err)
			}
			if err := f.runtime.verifyMerchantStoreFenceTerminal(context.Background(), f.workspace, m); err == nil {
				t.Fatal("managed admission entry drift released terminal owner")
			}
		})
	}
}
