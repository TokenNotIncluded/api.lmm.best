package appcli

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestSyncDirectory(t *testing.T) {
	if err := syncDirectory(t.TempDir()); err != nil {
		t.Fatalf("syncDirectory() error = %v", err)
	}
}

func TestSyncDirectoryRejectsMissingPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing")
	if err := syncDirectory(path); err == nil {
		t.Fatal("syncDirectory() accepted a missing path")
	}
}

func TestPrepareLegacyProviderRollbackRemovesActiveProviderLink(t *testing.T) {
	root := t.TempDir()
	installedBinary := filepath.Join(root, backendCanonicalName)
	if err := os.Symlink(backendGoName, installedBinary); err != nil {
		t.Fatal(err)
	}
	runtime := &productionRuntime{
		paths: productionPaths{InstalledBinary: installedBinary},
	}
	manifest := productionManifest{
		PreviousProviderTarget: "legacy-regular",
		NewProviderTarget:      backendGoName,
		Go: productionPackageTransition{
			RollbackPackageName: productionAURPackageName,
			RollbackIdentity:    productionAURPackageName + " 0.1.69-1",
		},
	}

	if err := runtime.prepareLegacyProviderRollback(manifest); err != nil {
		t.Fatalf("prepareLegacyProviderRollback() error = %v", err)
	}
	if _, err := os.Lstat(installedBinary); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("provider link still exists: %v", err)
	}
}
