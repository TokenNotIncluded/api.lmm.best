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

func newCleanupFixture(t *testing.T) (*productionRuntime, time.Time) {
	t.Helper()
	root := t.TempDir()
	paths := productionPaths{
		WorkRoot:        filepath.Join(root, "work"),
		BackupRoot:      filepath.Join(root, "backups"),
		GlobalLock:      filepath.Join(root, "run", "deploy.lock"),
		TransactionLock: filepath.Join(root, "transaction.lock"),
		FrontendRoot:    filepath.Join(root, "frontend"),
		ExpectedHost:    productionExpectedHost,
	}
	for _, path := range []string{paths.WorkRoot, paths.BackupRoot, filepath.Dir(paths.GlobalLock), filepath.Join(paths.FrontendRoot, "releases")} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	current := "0.1.0.r300.gabcdef123"
	if err := os.Mkdir(filepath.Join(paths.FrontendRoot, "releases", current), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join("releases", current), filepath.Join(paths.FrontendRoot, "current")); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 12, 4, 0, 0, 0, time.UTC)
	runtime := &productionRuntime{
		paths: paths, now: func() time.Time { return now }, sleep: func(time.Duration) {},
		effectiveUID: func() int { return 0 }, hostname: func() (string, error) { return productionExpectedHost, nil }, requiredOwnerUID: uint32(os.Getuid()),
	}
	return runtime, now
}

func addCleanupWorkspace(t *testing.T, runtime *productionRuntime, id, phase, version string, updated time.Time) string {
	t.Helper()
	root := filepath.Join(runtime.paths.WorkRoot, id)
	for _, name := range []string{"staging", "state"} {
		if err := os.MkdirAll(filepath.Join(root, name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	marker := "format=1\ndeployment_id=" + id + "\nrole=target\n"
	if err := os.WriteFile(filepath.Join(root, productionWorkspaceMarker), []byte(marker), 0o600); err != nil {
		t.Fatal(err)
	}
	status, err := json.Marshal(productionStatus{
		Format: productionStatusFormat, DeploymentID: id, Phase: phase, Version: version, UpdatedUTC: updated,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "state", productionStatusFilename), append(status, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "staging", "payload.bin"), []byte(strings.Repeat("x", 128)), 0o600); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestProductionWorkspaceCleanupDryRunProtectsCurrentAndFallback(t *testing.T) {
	runtime, now := newCleanupFixture(t)
	current := addCleanupWorkspace(t, runtime, "go-current", "CONFIRMED", "0.1.0.r300.gabcdef123", now.Add(-72*time.Hour))
	fallback := addCleanupWorkspace(t, runtime, "go-rollback", "ROLLED_BACK", "0.1.0.r282.g546910cef", now.Add(-72*time.Hour))
	failed := addCleanupWorkspace(t, runtime, "go-failed", "FAILED_PREARM", "0.1.0.r299.gdeadbeef0", now.Add(-72*time.Hour))
	if err := writeTestBackupSet(filepath.Join(runtime.paths.BackupRoot, "go-rollback"), []byte("env")); err != nil {
		t.Fatal(err)
	}
	result, err := runtime.cleanupWorkspaces(context.Background(), productionWorkspaceCleanupOptions{OlderThan: 24 * time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	if !result.DryRun || result.RemovedBytes != 0 {
		t.Fatalf("unexpected dry-run result: %#v", result)
	}
	for _, path := range []string{filepath.Join(current, "staging"), filepath.Join(fallback, "staging"), filepath.Join(failed, "staging")} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("dry-run removed %s: %v", path, err)
		}
	}
	for _, id := range []string{"go-current", "go-rollback"} {
		for _, entry := range result.Entries {
			if entry.DeploymentID == id && !entry.Protected {
				t.Fatalf("%s was not protected: %#v", id, entry)
			}
		}
	}
}

func TestProductionWorkspaceCleanupExecuteRemovesOnlyDisposableChildren(t *testing.T) {
	runtime, now := newCleanupFixture(t)
	stale := addCleanupWorkspace(t, runtime, "go-failed", "FAILED_PREARM", "0.1.0.r299.gdeadbeef0", now.Add(-72*time.Hour))
	if err := os.Mkdir(filepath.Join(stale, "tmp"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(stale, "cache"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(stale, "state", productionConfigRestoreDirname), 0o700); err != nil {
		t.Fatal(err)
	}
	result, err := runtime.cleanupWorkspaces(context.Background(), productionWorkspaceCleanupOptions{OlderThan: 24 * time.Hour, Execute: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.DryRun || result.RemovedBytes == 0 {
		t.Fatalf("unexpected execute result: %#v", result)
	}
	for _, path := range []string{filepath.Join(stale, "staging"), filepath.Join(stale, "tmp"), filepath.Join(stale, "cache"), filepath.Join(stale, "state", productionConfigRestoreDirname)} {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("disposable path %s remains: %v", path, err)
		}
	}
	for _, path := range []string{filepath.Join(stale, productionWorkspaceMarker), filepath.Join(stale, "state", productionStatusFilename)} {
		if _, err := os.Lstat(path); err != nil {
			t.Fatalf("audit path %s was removed: %v", path, err)
		}
	}

	second, err := runtime.cleanupWorkspaces(context.Background(), productionWorkspaceCleanupOptions{OlderThan: 24 * time.Hour, Execute: true})
	if err != nil {
		t.Fatal(err)
	}
	if second.RemovedBytes != 0 || len(second.Entries) != 1 || second.Entries[0].Phase != "FAILED_PREARM" {
		t.Fatalf("cleanup was not idempotent after staging removal: %#v", second)
	}
}

func TestProductionWorkspaceInspectionDoesNotRequireStaging(t *testing.T) {
	runtime, now := newCleanupFixture(t)
	root := addCleanupWorkspace(t, runtime, "go-cleaned", "CONFIRMED", "0.1.0.r299.gdeadbeef0", now.Add(-72*time.Hour))
	if err := os.RemoveAll(filepath.Join(root, "staging")); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.openWorkspaceForInspection(root); err != nil {
		t.Fatalf("inspect cleaned terminal workspace: %v", err)
	}
	if _, err := runtime.openWorkspace(root); err == nil {
		t.Fatal("mutation path accepted a workspace without staging")
	}
	if err := os.Symlink("/etc", filepath.Join(root, "staging")); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.openWorkspaceForInspection(root); err == nil {
		t.Fatal("inspection path accepted an unsafe staging symlink")
	}
}

func TestProductionWorkspaceCleanupRefusesInvalidOrActiveLock(t *testing.T) {
	runtime, now := newCleanupFixture(t)
	active := addCleanupWorkspace(t, runtime, "go-active", "PREPARING", "0.1.0.r300.gabcdef123", now.Add(-72*time.Hour))
	if err := os.Mkdir(runtime.paths.TransactionLock, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runtime.paths.TransactionLock, productionTransactionMarker), []byte("format=1\ndeployment_id=go-active\nstatus=ACTIVE\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := runtime.cleanupWorkspaces(context.Background(), productionWorkspaceCleanupOptions{OlderThan: time.Hour, Execute: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Entries) == 0 {
		t.Fatal("cleanup result did not report the active workspace")
	}
	if _, err := os.Stat(filepath.Join(active, "staging")); err != nil {
		t.Fatalf("active staging was removed: %v", err)
	}

	if err := os.Remove(filepath.Join(runtime.paths.TransactionLock, productionTransactionMarker)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runtime.paths.TransactionLock, productionTransactionMarker), []byte("format=1\ndeployment_id=unknown\nstatus=STALE\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.cleanupWorkspaces(context.Background(), productionWorkspaceCleanupOptions{OlderThan: time.Hour, Execute: true}); err == nil {
		t.Fatal("invalid transaction lock was not rejected")
	}
}
