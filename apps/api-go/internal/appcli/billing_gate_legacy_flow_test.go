//go:build !windows

package appcli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLegacyRiskAcknowledgementPreservesShutdownGates(t *testing.T) {
	for _, failure := range []string{"", "abnormal exit", "shutdown error"} {
		t.Run(failure, func(t *testing.T) {
			f := newProductionFixture(t)
			f.runner.refundIntent = true
			f.runner.missingStartup = true
			f.runner.journalLoss = true
			f.runner.badWriterStop = failure == "abnormal exit"
			f.runner.shutdownJournalFailure = failure == "shutdown error"
			now := f.runtime.now().UTC()
			manifest := productionManifest{DeploymentID: f.workspace.id, ConfigRestorePath: f.workspace.configRestore, OldVersion: "0.2.17", ExpectedVersion: f.options.ExpectedVersion, Go: productionPackageTransition{RollbackPackageName: productionAURPackageName, RollbackIdentity: "lmm-api-go-bin 0.2.17-1", RollbackSHA256: mustHashFile(t, f.options.GoRollbackPackage)}}
			reference := controllerBackupReference{SourceHost: "ArchDmit", SourceDatabase: "lmm_api", Purpose: "controlled upgrade", CapturedUTC: now.Add(-time.Minute), Files: map[string]controllerBackupReferenceFile{}}
			for _, name := range []string{"database.dump.age", "roles.sql.age", "environment.tar.age"} {
				reference.Files[name] = controllerBackupReferenceFile{Bytes: 100, SHA256: strings.Repeat("a", 64), Verified: true}
			}
			referenceBytes, _ := json.Marshal(reference)
			if err := os.MkdirAll(filepath.Join(f.workspace.root, "state"), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(f.workspace.root, "state", "controller-backup-reference.json"), referenceBytes, 0600); err != nil {
				t.Fatal(err)
			}
			ack := legacyRefundRiskAcknowledgement{Format: 1, Purpose: "user-accepted-legacy-refund-risk", DeploymentID: f.workspace.id, OldPackageIdentity: manifest.Go.RollbackIdentity, OldPackageSHA256: manifest.Go.RollbackSHA256, WriterPID: 2147483600, WriterInvocationID: strings.Repeat("1", 32), BackupManifestSHA256: fmt.Sprintf("%x", sha256Bytes(referenceBytes)), BackupCapturedUTC: reference.CapturedUTC, AcceptedUTC: now, Outcome: "unknown"}
			ackBytes, _ := json.Marshal(ack)
			if err := os.WriteFile(filepath.Join(f.workspace.root, "state", "legacy-refund-risk.json"), ackBytes, 0600); err != nil {
				t.Fatal(err)
			}
			if err := f.runtime.preflightBillingWriter(context.Background(), &manifest); err != nil {
				t.Fatal(err)
			}
			manifest.BillingGate = &productionBillingGate{AdmissionClosed: true}
			f.runtime.billingAdmissionClosed = true
			err := f.runtime.stopBillingWriter(context.Background(), f.workspace, &manifest)
			if failure == "" {
				if err != nil || !manifest.BillingGate.StopVerified {
					t.Fatalf("stop=%v gate=%+v", err, manifest.BillingGate)
				}
				if manifest.BillingGate.InvocationJournalSHA256 != "" {
					t.Fatal("legacy acceptance fabricated history proof")
				}
			} else if err == nil {
				t.Fatal("risk acknowledgement bypassed a shutdown failure")
			}
		})
	}
}
