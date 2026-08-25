package appcli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type productionCLIRemovalRunner struct {
	legacyPath string
	goVersion  string
	legacy     bool
	removed    bool
}

func (runner *productionCLIRemovalRunner) Run(_ context.Context, command productionCommand) ([]byte, error) {
	if command.Name != commandPacman || len(command.Args) == 0 {
		return nil, errors.New("unexpected command")
	}
	switch command.Args[0] {
	case "-Q":
		if len(command.Args) == 2 && command.Args[1] == productionAURPackageName {
			return []byte(productionAURPackageName + " " + runner.goVersion + "\n"), nil
		}
		return nil, errors.New("package not installed")
	case "-Qq":
		output := productionAURPackageName + "\n"
		if runner.legacy {
			output += "lmm-api-deploy-bin\n"
		}
		return []byte(output), nil
	case "-Qo":
		if len(command.Args) != 2 {
			return nil, errors.New("invalid ownership query")
		}
		if command.Args[1] == runner.legacyPath {
			return []byte(runner.legacyPath + " is owned by lmm-api-deploy-bin 0.1.57-1\n"), nil
		}
		return []byte(command.Args[1] + " is owned by " + productionAURPackageName + " " + runner.goVersion + "\n"), nil
	case "-Qkk":
		return []byte("lmm-api-deploy-bin: 7 total files, 0 altered files\n"), nil
	case "--remove":
		if len(command.Args) != 4 || command.Args[1] != "--noconfirm" || command.Args[2] != "--" || command.Args[3] != "lmm-api-deploy-bin" {
			return nil, errors.New("unsafe removal arguments")
		}
		if err := os.Remove(runner.legacyPath); err != nil {
			return nil, err
		}
		runner.legacy = false
		runner.removed = true
		return nil, nil
	default:
		return nil, errors.New("unexpected pacman arguments")
	}
}

func TestRemoveLegacyDeployPackageRequiresIntegratedT0(t *testing.T) {
	root := t.TempDir()
	legacy := filepath.Join(root, "lmm-api-deploy")
	if err := os.WriteFile(legacy, []byte("legacy"), 0o755); err != nil {
		t.Fatal(err)
	}
	runner := &productionCLIRemovalRunner{legacyPath: legacy, goVersion: "0.1.59-1", legacy: true}
	runtime := &productionRuntime{
		paths:  productionPaths{LegacyDeployBinary: legacy},
		runner: runner,
	}
	candidate := productionPackageMetadata{Name: productionAURPackageName, Version: "0.1.60-1"}
	if err := runtime.removeLegacyDeployPackageForT1(context.Background(), candidate); err != nil {
		t.Fatal(err)
	}
	if !runner.removed {
		t.Fatal("T1 transition did not remove the legacy deployment package")
	}

	if err := os.WriteFile(legacy, []byte("legacy"), 0o755); err != nil {
		t.Fatal(err)
	}
	runner = &productionCLIRemovalRunner{legacyPath: legacy, goVersion: "0.1.58-1", legacy: true}
	runtime.runner = runner
	if err := runtime.removeLegacyDeployPackageForT1(context.Background(), candidate); err == nil || !strings.Contains(err.Error(), "requires a confirmed") {
		t.Fatalf("pre-T0 removal error=%v", err)
	}
	if runner.removed {
		t.Fatal("pre-T0 guard removed the legacy package")
	}
}

func TestPackageCLITransitionPhaseRequiresSignedMetadataAtNewBoundary(t *testing.T) {
	for _, test := range []struct {
		version, explicit, want string
		wantError               string
	}{
		{version: "0.1.59-1", explicit: productionCLIPhaseT1, wantError: "historical"},
		{version: "0.1.62-1", explicit: productionCLIPhaseT0, wantError: "historical"},
		{version: "0.1.63-1", wantError: "signed CLI_TRANSITION_PHASE"},
		{version: "0.1.63-1", explicit: productionCLIPhaseT0, want: productionCLIPhaseT0},
		{version: "0.1.63-1", explicit: productionCLIPhaseT1, want: productionCLIPhaseT1},
	} {
		got, err := packageCLITransitionPhase(productionAURPackageName, test.version, test.explicit)
		if test.wantError != "" {
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("version=%s explicit=%s error=%v", test.version, test.explicit, err)
			}
			continue
		}
		if err != nil || got != test.want {
			t.Fatalf("version=%s explicit=%s phase=%s error=%v", test.version, test.explicit, got, err)
		}
	}
}

func TestExplicitHighVersionT0DoesNotRemoveLegacyDeployPackage(t *testing.T) {
	runtime := &productionRuntime{runner: &productionCLIRemovalRunner{}}
	candidate := productionPackageMetadata{Name: productionAURPackageName, Version: "0.1.63-1", CLITransitionPhase: productionCLIPhaseT0}
	if err := runtime.removeLegacyDeployPackageForT1(context.Background(), candidate); err != nil {
		t.Fatal(err)
	}
}

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
		CandidateIdentity:    productionAURPackageName + " 0.1.60-1",
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
		CandidateIdentity:    productionAURPackageName + " 0.1.59-1",
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

	explicitT0 := productionPackageTransition{
		CandidatePackageName: productionAURPackageName,
		CandidateIdentity:    productionAURPackageName + " 0.1.63-1",
		CandidateCLIPhase:    productionCLIPhaseT0,
	}
	if err := runtime.verifyTransitionCLI(explicitT0, false); err != nil {
		t.Fatal(err)
	}
	explicitT0.CandidateCLIPhase = productionCLIPhaseT1
	if err := runtime.verifyTransitionCLI(explicitT0, false); err == nil || !strings.Contains(err.Error(), "legacy CLI path remains") {
		t.Fatalf("explicit T1 with compatibility link error=%v", err)
	}
}
