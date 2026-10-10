//go:build linux

package deploycli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Real apply produces FAILED_PREARM before admission with its original
// transaction lock released. Package/database/systemd reads are fixtures;
// release executes the existing holder terminal verifier, including real ELF.
func failedPrearmRecoveryFixture(t *testing.T) (productionFixture, *merchantStoreTerminalRecoveryAuthority) {
	t.Helper()
	f, plan, base := existingSchemaProductionFixture(t)
	f.runtime.paths.EdgeAssetRoot = t.TempDir()
	unitPath := filepath.Join(f.runtime.paths.ConfigDir, "lmm-api.service")
	unitContent := []byte("[Service]\nEnvironment=LMM_DB_MIGRATION_MODE=verify\nExecStart=" + f.runtime.paths.InstalledBinary + " serve\n")
	if err := os.WriteFile(unitPath, unitContent, 0644); err != nil {
		t.Fatal(err)
	}
	started := f.clock.Add(-time.Hour).UTC().Format("Mon 2006-01-02 15:04:05 MST")
	f.runtime.runner = existingSchemaTestRunner{run: func(command productionCommand) ([]byte, error) {
		if command.Name == commandBsdtar && slices.Contains(command.Args, "usr/lib/systemd/system/lmm-api.service") {
			return unitContent, nil
		}
		if command.Name == commandSystemctl && strings.Contains(strings.Join(command.Args, " "), "--property=ExecMainStartTimestamp") {
			return []byte(started), nil
		}
		if command.Name == commandSystemctl && slices.Contains(command.Args, "--property="+existingSchemaUnitProperties) {
			start := "{ path=" + f.runtime.paths.InstalledBinary + " ; argv[]=" + f.runtime.paths.InstalledBinary + " serve ; ignore_errors=no ; }"
			return []byte(fmt.Sprintf("Environment=LMM_DB_MIGRATION_MODE=verify\nEnvironmentFiles=%s (ignore_errors=yes)\nMainPID=%d\nInvocationID=%s\nActiveState=active\nFragmentPath=%s\nExecStart=%s\n", base.configPath, os.Getpid(), strings.Repeat("1", 32), unitPath, start)), nil
		}
		out, err := base.Run(context.Background(), command)
		return []byte(strings.ReplaceAll(string(out), "2147483600", strconv.Itoa(os.Getpid()))), err
	}}
	if err := f.runtime.sealExistingSchemaStartup(context.Background(), plan.ExistingSchemaContract); err != nil {
		t.Fatal(err)
	}
	contract := testMerchantWriterContract()
	schema := plan.ExistingSchemaContract
	contract.SystemIdentifier, contract.Database, contract.DatabaseOID = schema.SystemIdentifier, schema.Database, schema.DatabaseOID
	contract.Schema, contract.SchemaOID = schema.Schema, schema.SchemaOID
	contract.StartupSHA256, contract.SignedUnitSHA256 = schema.StartupSHA256, schema.SignedUnitSHA256
	plan.GoRollback.PayloadSHA256 = mustHashFile(t, "/proc/self/exe")
	for _, pair := range []struct {
		p *productionReleasePackagePlan
		t *productionMerchantStoreWriterTarget
	}{{&plan.GoCandidate, &contract.Candidate}, {&plan.GoRollback, &contract.Rollback}} {
		pair.p.MerchantStoreWriterCapability = pair.t.Capability
		pair.t.PackageSHA256, pair.t.PayloadSHA256 = pair.p.PackageSHA256, pair.p.PayloadSHA256
		pair.t.SourceRevision, pair.t.ReleaseAssetSHA256 = pair.p.GitRevision, pair.p.ReleaseAssetSHA256
	}
	plan.MerchantStoreWriter = contract
	canonical, err := canonicalProductionReleasePlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	f.options.StagedPlanSHA256 = startupContentSHA256(canonical)
	if err := os.WriteFile(f.options.StagedPlanPath, canonical, 0600); err != nil {
		t.Fatal(err)
	}
	marker := fmt.Sprintf("format=1\ndeployment_id=%s\nrole=target\ncreated_at_utc=%s\n", f.workspace.id, f.clock.Add(-time.Minute).Format(time.RFC3339))
	if err := os.WriteFile(filepath.Join(f.workspace.root, productionWorkspaceMarker), []byte(marker), 0600); err != nil {
		t.Fatal(err)
	}
	authority := &merchantStoreTerminalRecoveryAuthority{merchantStoreHookAuthorityFixture: &merchantStoreHookAuthorityFixture{runner: f.runner, failEnsure: true}, runtime: f.runtime}
	f.runtime.merchantStoreAuthority = authority
	f.runtime.billingExecutableSHA256 = func(int) (string, error) { return contract.Rollback.PayloadSHA256, nil }
	if _, err := f.runtime.apply(context.Background(), f.workspace, f.options); err == nil {
		t.Fatal("holder qualification unexpectedly succeeded")
	}
	status, err := f.runtime.readStatus(f.workspace)
	if err != nil || status.Phase != "FAILED_PREARM" || !strings.Contains(status.Failure, "holder did not establish") {
		t.Fatalf("not an actual failed prearm: %+v %v", status, err)
	}
	manifest, err := f.runtime.readManifestForRollback(f.workspace)
	if err != nil || manifest.BillingGate != nil || !f.runner.serviceActive {
		t.Fatalf("prearm already mutated writer/admission: %+v %v", manifest.BillingGate, err)
	}
	if _, err := os.Stat(f.runtime.paths.TransactionLock); !os.IsNotExist(err) {
		t.Fatal("apply did not release its prearm lock")
	}
	authority.expected = manifest
	f.runner.events = nil
	return f, authority
}

func TestProductionFailedPrearmRecoveryReleasesOriginalHolderWithoutWriterMutation(t *testing.T) {
	f, authority := failedPrearmRecoveryFixture(t)
	status, err := f.runtime.rollback(context.Background(), f.workspace, "late-holder-recovery")
	if err != nil || status.Phase != "ROLLED_BACK" || status.Reason != productionUnchangedAdmissionRecoveryReason || !authority.released {
		t.Fatalf("original holder did not accept native recovery: %+v %v", status, err)
	}
	manifest, err := f.runtime.readManifestForRollback(f.workspace)
	if err != nil || manifest.BillingGate.AdmissionClosed || !manifest.BillingGate.AdmissionReopened {
		t.Fatalf("recovery invented a drain or lost open admission: %+v %v", manifest.BillingGate, err)
	}
	if _, err := os.Stat(f.runtime.paths.TransactionLock); !os.IsNotExist(err) {
		t.Fatal("native finalization retained transaction lock")
	}
	assertPrearmDidNotMutateWriter(t, f)
}

func TestProductionFailedPrearmRecoveryRefusesDriftAndRetainsOriginalPhase(t *testing.T) {
	for _, kind := range []string{"environment", "edge", "running payload", "restart", "other transaction", "owner lost", "missing original time", "late generation"} {
		t.Run(kind, func(t *testing.T) {
			f, authority := failedPrearmRecoveryFixture(t)
			switch kind {
			case "environment":
				if err := os.WriteFile(filepath.Join(f.runtime.paths.ConfigDir, "lmm-api-go.env"), []byte("changed\n"), 0600); err != nil {
					t.Fatal(err)
				}
			case "edge":
				if err := os.WriteFile(filepath.Join(f.runtime.paths.NginxRoot, "lmm-api-locations.conf"), []byte("changed\n"), 0644); err != nil {
					t.Fatal(err)
				}
			case "running payload":
				f.runtime.billingExecutableSHA256 = func(int) (string, error) { return strings.Repeat("f", 64), nil }
			case "restart":
				f.runner.restartCounter++
			case "other transaction":
				if err := os.Mkdir(f.runtime.paths.TransactionLock, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(f.runtime.paths.TransactionLock, productionTransactionMarker), []byte("format=1\ndeployment_id=other\nstatus=ACTIVE\n"), 0600); err != nil {
					t.Fatal(err)
				}
			case "owner lost":
				authority.failRequest = authority.requests + 1
			case "missing original time":
				if err := os.WriteFile(filepath.Join(f.workspace.root, productionWorkspaceMarker), []byte("format=1\ndeployment_id="+f.workspace.id+"\nrole=target\n"), 0600); err != nil {
					t.Fatal(err)
				}
			case "late generation":
				base := f.runtime.runner
				changed := false
				f.runtime.runner = existingSchemaTestRunner{run: func(command productionCommand) ([]byte, error) {
					out, err := base.Run(context.Background(), command)
					if changed && command.Name == commandSystemctl {
						out = []byte(strings.ReplaceAll(string(out), "MainPID="+strconv.Itoa(os.Getpid()), "MainPID="+strconv.Itoa(os.Getpid()+1)))
					}
					if slices.Contains(command.Args, "--token-file") {
						changed = true
					}
					return out, err
				}}
			}
			if _, err := f.runtime.rollback(context.Background(), f.workspace, "prearm-drift"); err == nil || authority.released {
				t.Fatal("changed evidence released original holder")
			}
			status, err := f.runtime.readStatus(f.workspace)
			if err != nil || status.Phase != "FAILED_PREARM" {
				t.Fatalf("refusal lost original phase: %+v %v", status, err)
			}
			assertPrearmDidNotMutateWriter(t, f)
		})
	}
}

func TestProductionFailedPrearmRecoveryCASRefusalKeepsExplicitRecoveryEvidence(t *testing.T) {
	f, authority := failedPrearmRecoveryFixture(t)
	authority.failRelease = true
	if _, err := f.runtime.rollback(context.Background(), f.workspace, "late-holder-recovery"); err == nil || authority.released {
		t.Fatal("CAS failure was finalized")
	}
	status, err := f.runtime.readStatus(f.workspace)
	if err != nil || status.Phase != "ROLLBACK_REQUIRED" {
		t.Fatalf("CAS failure lost recovery state: %+v %v", status, err)
	}
	if err := f.runtime.validateTransactionLock(f.workspace); err != nil {
		t.Fatal(err)
	}
	assertPrearmDidNotMutateWriter(t, f)
	authority.failRelease = false
	status, err = f.runtime.rollback(context.Background(), f.workspace, "same-owner-release-retry")
	if err != nil || status.Phase != "ROLLED_BACK" || !authority.released {
		t.Fatalf("explicit original-owner retry failed: %+v %v", status, err)
	}
	if f.runtime.billingRollback {
		t.Fatal("recovery leaked rollback qualification mode")
	}
	for _, event := range f.runner.events {
		if event == "merchant-qualify:candidate-installed" {
			t.Fatal("unchanged recovery qualified an uninstalled candidate")
		}
	}
	assertPrearmDidNotMutateWriter(t, f)
}

func assertPrearmDidNotMutateWriter(t *testing.T, f productionFixture) {
	t.Helper()
	for _, event := range f.runner.events {
		if event == "systemd-stop" || event == "systemd-start" || strings.HasPrefix(event, "paru-") || strings.HasPrefix(event, "migrate:") {
			t.Fatalf("unchanged prearm recovery mutated writer: %s", event)
		}
	}
}

func TestProductionMerchantStoreHolderReadinessWaitsForSlowOriginalAndHonorsCancellation(t *testing.T) {
	f := newProductionFixture(t)
	start := f.runtime.now()
	checks := 0
	err := f.runtime.awaitMerchantStoreFence(context.Background(), func(context.Context) error {
		checks++
		if f.runtime.now().Sub(start) >= 15*time.Second {
			return nil
		}
		return errors.New("not ready")
	})
	if err != nil || checks != 31 {
		t.Fatalf("slow original holder was rejected: checks=%d %v", checks, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	checks = 0
	err = f.runtime.awaitMerchantStoreFence(ctx, func(context.Context) error { checks++; cancel(); return errors.New("not ready") })
	if !errors.Is(err, context.Canceled) || checks != 1 {
		t.Fatalf("readiness ignored cancellation: checks=%d %v", checks, err)
	}
	checks = 0
	start = f.runtime.now()
	err = f.runtime.awaitMerchantStoreFence(context.Background(), func(context.Context) error { checks++; return errors.New("not ready") })
	if err == nil || f.runtime.now().Sub(start) != 2*time.Minute || checks != 240 {
		t.Fatalf("readiness is not bounded: checks=%d elapsed=%s err=%v", checks, f.runtime.now().Sub(start), err)
	}
}

func TestProductionMerchantStoreHolderReadinessRejectsSuccessAfterItsBudget(t *testing.T) {
	f := newProductionFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err := f.runtime.awaitMerchantStoreFence(ctx, func(context.Context) error {
		cancel()
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled readiness probe authorized mutation: %v", err)
	}
	err = f.runtime.awaitMerchantStoreFence(context.Background(), func(context.Context) error {
		f.runtime.sleep(2 * time.Minute)
		return nil
	})
	if err == nil {
		t.Fatal("readiness probe that crossed the deadline authorized mutation")
	}
}
