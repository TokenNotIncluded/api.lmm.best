//go:build linux

package appcli

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

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
