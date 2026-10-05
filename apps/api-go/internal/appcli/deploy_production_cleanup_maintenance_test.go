//go:build !windows

package appcli

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFinancialCleanupReceiptRequiresFullArchiveAndExactDatabaseProvenance(t *testing.T) {
	for _, change := range []string{"", "full", "ownership", "intent", "provider", "database", "oid", "schema-oid", "unknown-target", "archive-hash", "size", "receipt-hash"} {
		t.Run(change, func(t *testing.T) {
			runtime, workspace, h := maintenanceBindingFixture(t, "post")
			archivePath := filepath.Join(workspace.root, "full.dump")
			body := []byte("PGDMP frozen full-database fixture")
			if err := os.WriteFile(archivePath, body, 0600); err != nil {
				t.Fatal(err)
			}
			archiveSHA, _ := sha256File(archivePath)
			target := map[string]any{"system_identifier": "123", "database": "fixture", "database_oid": 123, "schema": "public", "schema_oid": 2200}
			receipt := map[string]any{"format": "lmm-credit-financial-backup-v1", "transition_id": h.TransitionID, "transition_intent_sha256": h.TransitionIntentSHA256, "provider_sha256": h.ProviderSHA256, "source_sha": strings.Repeat("a", 64), "target": target, "full_database": true, "archive_format": "custom", "preserve_ownership": true, "backup_sha256": archiveSHA, "size_bytes": len(body), "frozen_guardian_bindings_sha256": strings.Repeat("b", 64)}
			switch change {
			case "full":
				receipt["full_database"] = false
			case "ownership":
				receipt["preserve_ownership"] = false
			case "intent":
				receipt["transition_intent_sha256"] = strings.Repeat("c", 64)
			case "provider":
				receipt["provider_sha256"] = strings.Repeat("c", 64)
			case "database":
				target["database"] = "other"
			case "oid":
				target["database_oid"] = 124
			case "schema-oid":
				target["schema_oid"] = 1.5
			case "unknown-target":
				target["extra"] = "unbound"
			case "archive-hash":
				receipt["backup_sha256"] = strings.Repeat("c", 64)
			case "size":
				receipt["size_bytes"] = len(body) + 1
			}
			path := filepath.Join(workspace.root, "full-receipt.json")
			content, _ := json.Marshal(receipt)
			os.WriteFile(path, content, 0600)
			digest, _ := sha256File(path)
			if change == "receipt-hash" {
				digest = strings.Repeat("c", 64)
			}
			err := runtime.validateMaintenanceFinancialReceipt(productionWorkspaceCleanupOptions{FinancialBackup: archivePath, FinancialBackupSHA256: archiveSHA, FinancialBackupReceipt: path, FinancialBackupReceiptSHA256: digest})
			if change == "" && err != nil {
				t.Fatal(err)
			}
			if change != "" && err == nil {
				t.Fatalf("accepted %s", change)
			}
		})
	}
}

func TestSupersededCleanupCannotSkipRetentionOrReplacementProofs(t *testing.T) {
	runtime, _ := newCleanupFixture(t)
	for _, options := range []productionWorkspaceCleanupOptions{
		{OlderThan: 24 * time.Hour, SupersededBy: "/current"},
		{OlderThan: time.Hour, SupersededBy: "/current", RetainRollback: "/bridge"},
		{OlderThan: 24 * time.Hour, SupersededBy: "/current", RetainRollback: "/bridge"},
	} {
		if _, err := runtime.cleanupWorkspaces(context.Background(), options); err == nil {
			t.Fatal("unproved superseded cleanup accepted")
		}
	}
}

func TestFinancialCleanupProofStreamsLargePGDMPAndRejectsChangedArchive(t *testing.T) {
	root, err := os.MkdirTemp(maintenanceFixtureCache(t), "cleanup-archive-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	path := filepath.Join(root, "full.dump")
	content := []byte("PGDMP" + strings.Repeat("custom archive fixture", 65536))
	if err := os.WriteFile(path, content, 0600); err != nil {
		t.Fatal(err)
	}
	digest, err := sha256File(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateMaintenanceFinancialBackup(path, digest, uint32(os.Getuid())); err != nil {
		t.Fatalf("large sealed archive: %v", err)
	}
	if err := validateMaintenanceFinancialBackup(path, strings.Repeat("a", 64), uint32(os.Getuid())); err == nil {
		t.Fatal("changed financial archive hash accepted")
	}
	if err := os.Link(path, filepath.Join(root, "linked.dump")); err != nil {
		t.Fatal(err)
	}
	if err := validateMaintenanceFinancialBackup(path, digest, uint32(os.Getuid())); err == nil {
		t.Fatal("multi-linked financial archive accepted")
	}
	os.Remove(filepath.Join(root, "linked.dump"))
	if err := os.WriteFile(path, []byte("schema-only SQL"), 0600); err != nil {
		t.Fatal(err)
	}
	digest, _ = sha256File(path)
	if err := validateMaintenanceFinancialBackup(path, digest, uint32(os.Getuid())); err == nil {
		t.Fatal("schema dump substituted for financial backup")
	}
}

func TestSupersededCleanupPayloadWhitelistPreservesFinancialAndOwnerEvidence(t *testing.T) {
	runtime, now := newCleanupFixture(t)
	root := addCleanupWorkspace(t, runtime, "historical", "CONFIRMED", "old", now.Add(-72*time.Hour))
	for _, name := range []string{"tmp", "cache", "caches", filepath.Join("state", productionConfigRestoreDirname)} {
		if err := os.MkdirAll(filepath.Join(root, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	evidence := []string{filepath.Join("state", productionConfigRestoreDirname, "lmm-api-go.env"), filepath.Join("state", "manifest.json"), "shutdown.log", "database.dump"}
	for _, name := range evidence {
		if err := os.WriteFile(filepath.Join(root, name), []byte("preserve"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	backup := filepath.Join(runtime.paths.BackupRoot, "financial.dump")
	if err := os.WriteFile(backup, []byte("PGDMP full backup"), 0600); err != nil {
		t.Fatal(err)
	}
	removed, _, err := removeWorkspaceChildren(productionWorkspace{root: root}, []string{"staging", "tmp", "cache", "caches"})
	if err != nil || len(removed) != 4 {
		t.Fatalf("removed=%v err=%v", removed, err)
	}
	for _, name := range evidence {
		if content, err := os.ReadFile(filepath.Join(root, name)); err != nil || string(content) != "preserve" {
			t.Fatalf("evidence %s changed", name)
		}
	}
	if _, err := os.Stat(backup); err != nil {
		t.Fatal("financial backup was removed")
	}
	removed, bytes, err := removeWorkspaceChildren(productionWorkspace{root: root}, []string{"staging", "tmp", "cache", "caches"})
	if err != nil || len(removed) != 0 || bytes != 0 {
		t.Fatalf("not idempotent: %v %d %v", removed, bytes, err)
	}
}

func TestSupersededCleanupHonorsOtherOwnerPayloadReferences(t *testing.T) {
	runtime, now := newCleanupFixture(t)
	target := addCleanupWorkspace(t, runtime, "historical", "CONFIRMED", "old", now.Add(-72*time.Hour))
	other := addCleanupWorkspace(t, runtime, "retained", "CONFIRMED", "current", now.Add(-72*time.Hour))
	workspace, err := runtime.openWorkspaceForInspection(target)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(other, "state", productionManifestFilename)
	if err := os.WriteFile(path, []byte(`{"rollback_path":"`+filepath.Join(target, "staging", "package.pkg.tar.zst")+`"}`), 0600); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(runtime.paths.WorkRoot)
	if referenced, err := runtime.cleanupWorkspaceReferenced(workspace, entries); err != nil || !referenced {
		t.Fatalf("lost live payload reference: %v %v", referenced, err)
	}
	if err := os.WriteFile(path, []byte(`{"archived_environment_path":"`+filepath.Join(target, "state", productionConfigRestoreDirname, "lmm-api-go.env")+`"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if referenced, err := runtime.cleanupWorkspaceReferenced(workspace, entries); err != nil || referenced {
		t.Fatalf("retained config incorrectly blocks disposable cleanup: %v %v", referenced, err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if referenced, err := runtime.cleanupWorkspaceReferenced(workspace, entries); err != nil || !referenced {
		t.Fatal("unknown owner references accepted")
	}
	if _, err := os.Stat(filepath.Join(target, "staging")); errors.Is(err, os.ErrNotExist) {
		t.Fatal("read-only reference inspection removed payload")
	}
}
