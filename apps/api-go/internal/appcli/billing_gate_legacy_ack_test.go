package appcli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAcceptedLegacyRefundRiskRequiresExactBoundedAcknowledgement(t *testing.T) {
	root := filepath.Join(t.TempDir(), "ack-test-20260913")
	now := time.Date(2026, 9, 13, 0, 0, 0, 0, time.UTC)
	manifest := &productionManifest{DeploymentID: "ack-test-20260913", ConfigRestorePath: filepath.Join(root, "state", "config-restore"), Go: productionPackageTransition{RollbackPackageName: productionAURPackageName, RollbackIdentity: "lmm-api-go-bin 0.2.17-1", RollbackSHA256: strings.Repeat("a", 64)}, ControllerBackupSHA256: strings.Repeat("b", 64), BillingGate: &productionBillingGate{GoPID: 42, GoInvocationID: strings.Repeat("1", 32)}}
	state := filepath.Join(root, "state")
	if err := os.MkdirAll(state, 0o700); err != nil {
		t.Fatal(err)
	}
	write := func(value any) {
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(state, "legacy-refund-risk.json"), raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeRaw := func(raw []byte) {
		if err := os.WriteFile(filepath.Join(state, "legacy-refund-risk.json"), raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	reference := controllerBackupReference{SourceHost: "ArchDmit", SourceDatabase: "lmm_api", Purpose: "controlled upgrade", CapturedUTC: now.Add(-time.Hour), Files: map[string]controllerBackupReferenceFile{}}
	for _, name := range []string{"database.dump.age", "roles.sql.age", "environment.tar.age"} {
		reference.Files[name] = controllerBackupReferenceFile{Bytes: 10, SHA256: strings.Repeat("c", 64), Verified: true}
	}
	referenceBytes, _ := json.Marshal(reference)
	if err := os.WriteFile(filepath.Join(state, "controller-backup-reference.json"), referenceBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	valid := legacyRefundRiskAcknowledgement{1, "user-accepted-legacy-refund-risk", manifest.DeploymentID, "lmm-api-go-bin 0.2.17-1", strings.Repeat("a", 64), 42, strings.Repeat("1", 32), strings.Repeat("b", 64), now.Add(-time.Hour), now.Add(-time.Minute), "unknown"}
	valid.BackupManifestSHA256 = fmt.Sprintf("%x", sha256Bytes(referenceBytes))
	runtime := &productionRuntime{requiredOwnerUID: uint32(os.Getuid()), now: func() time.Time { return now }}
	write(valid)
	ok, err := runtime.acceptedLegacyRefundRisk(manifest)
	if err != nil || !ok {
		t.Fatalf("valid acknowledgement: %v %v", ok, err)
	}
	for _, mutate := range []func(*legacyRefundRiskAcknowledgement){func(a *legacyRefundRiskAcknowledgement) { a.OldPackageSHA256 = strings.Repeat("c", 64) }, func(a *legacyRefundRiskAcknowledgement) { a.WriterPID++ }, func(a *legacyRefundRiskAcknowledgement) { a.AcceptedUTC = now.Add(-25 * time.Hour) }, func(a *legacyRefundRiskAcknowledgement) { a.Outcome = "accepted" }} {
		candidate := valid
		mutate(&candidate)
		write(candidate)
		if ok, err := runtime.acceptedLegacyRefundRisk(manifest); ok || err == nil {
			t.Fatalf("invalid acknowledgement accepted: %v %v", ok, err)
		}
	}
	if err := os.Chmod(filepath.Join(state, "legacy-refund-risk.json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if ok, err := runtime.acceptedLegacyRefundRisk(manifest); ok || err == nil {
		t.Fatal("world-readable acknowledgement accepted")
	}
	if err := os.Chmod(filepath.Join(state, "legacy-refund-risk.json"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeRaw([]byte(`{"format":1,"purpose":"user-accepted-legacy-refund-risk","deployment_id":"ack-test-20260913","old_package_identity":"lmm-api-go-bin 0.2.17-1","old_package_sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","writer_pid":42,"writer_invocation_id":"11111111111111111111111111111111","backup_manifest_sha256":"bad","backup_captured_utc":"2026-09-12T23:00:00Z","accepted_utc":"2026-09-12T23:59:00Z","outcome":"unknown","extra":true}`))
	if ok, err := runtime.acceptedLegacyRefundRisk(manifest); ok || err == nil {
		t.Fatal("unknown acknowledgement field accepted")
	}
	writeRaw([]byte(`{"format":1,"format":1}`))
	if ok, err := runtime.acceptedLegacyRefundRisk(manifest); ok || err == nil {
		t.Fatal("duplicate acknowledgement field accepted")
	}
	rawValid, _ := json.Marshal(valid)
	validPath := filepath.Join(state, "valid-ack.json")
	if err := os.WriteFile(validPath, rawValid, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(state, "legacy-refund-risk.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(validPath, filepath.Join(state, "legacy-refund-risk.json")); err != nil {
		t.Fatal(err)
	}
	if ok, err := runtime.acceptedLegacyRefundRisk(manifest); ok || err == nil {
		t.Fatal("symlink acknowledgement accepted")
	}
	if err := os.Remove(filepath.Join(state, "legacy-refund-risk.json")); err != nil {
		t.Fatal(err)
	}
	if ok, err := runtime.acceptedLegacyRefundRisk(manifest); err != nil || ok {
		t.Fatalf("missing acknowledgement: %v %v", ok, err)
	}
}
