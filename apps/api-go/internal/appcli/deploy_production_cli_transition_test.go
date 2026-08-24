package appcli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVerifyTransitionCLIEnforcesT0AndT1CommandSets(t *testing.T) {
	root := t.TempDir()
	paths := defaultProductionPaths()
	paths.InstalledBinary = filepath.Join(root, "usr", "bin", "lmm-api")
	paths.LegacyGoBinary = filepath.Join(root, "usr", "bin", "lmm-api-go")
	paths.LegacyDeployBinary = filepath.Join(root, "usr", "bin", "lmm-api-deploy")
	if err := os.MkdirAll(filepath.Dir(paths.InstalledBinary), 0o755); err != nil {
		t.Fatal(err)
	}
	runtime := &productionRuntime{paths: paths}
	t1 := productionPackageTransition{
		CandidatePackageName: productionAURPackageName,
		CandidateIdentity:    productionAURPackageName + " 0.1.59-1",
	}
	if err := runtime.verifyTransitionCLI(t1, false); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("lmm-api", paths.LegacyGoBinary); err != nil {
		t.Fatal(err)
	}
	if err := runtime.verifyTransitionCLI(t1, false); err == nil || !strings.Contains(err.Error(), "legacy CLI path remains") {
		t.Fatalf("T1 legacy-path error=%v", err)
	}
	if err := os.Remove(paths.LegacyGoBinary); err != nil {
		t.Fatal(err)
	}
	t0 := productionPackageTransition{
		CandidatePackageName: productionAURPackageName,
		CandidateIdentity:    productionAURPackageName + " 0.1.58-1",
	}
	if err := runtime.verifyTransitionCLI(t0, false); err == nil || !strings.Contains(err.Error(), "lacks its compatibility") {
		t.Fatalf("T0 missing-link error=%v", err)
	}
	if err := os.Symlink("lmm-api", paths.LegacyGoBinary); err != nil {
		t.Fatal(err)
	}
	if err := runtime.verifyTransitionCLI(t0, false); err != nil {
		t.Fatal(err)
	}
}
