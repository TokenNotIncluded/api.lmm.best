package appcli

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

type productionPackageQueryRunner struct {
	identities map[string]string
}

func (runner productionPackageQueryRunner) Run(_ context.Context, command productionCommand) ([]byte, error) {
	if len(command.Args) == 0 {
		return nil, errors.New("missing package query")
	}
	name := command.Args[len(command.Args)-1]
	identity, ok := runner.identities[name]
	if !ok {
		return nil, errors.New("package not installed")
	}
	return []byte(identity + "\n"), nil
}

func validProductionReleaseArguments(root string) []string {
	return []string{
		"--repo", filepath.Join(root, "repo"),
		"--workspace", filepath.Join(root, "workspace"),
		"--confirm", "api.lmm.best",
	}
}

func TestParseProductionReleaseRequiresExplicitProductionConfirmation(t *testing.T) {
	arguments := validProductionReleaseArguments(t.TempDir())
	arguments[len(arguments)-1] = "wrong-host"
	_, err := parseProductionReleaseOptions(arguments, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "--confirm must equal api.lmm.best") {
		t.Fatalf("confirmation error=%v", err)
	}
}

func TestParseProductionReleaseConstrainsRollbackAndObservationWindows(t *testing.T) {
	tests := []struct {
		name  string
		flags []string
		want  string
	}{
		{name: "short observation", flags: []string{"--observation-seconds", "119"}, want: "between 120 and 360"},
		{name: "long observation", flags: []string{"--observation-seconds", "361"}, want: "between 120 and 360"},
		{name: "short rollback", flags: []string{"--rollback-seconds", "599"}, want: "must be at least"},
		{name: "long rollback", flags: []string{"--rollback-seconds", "3601"}, want: "at most 3600"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			arguments := append(validProductionReleaseArguments(t.TempDir()), test.flags...)
			_, err := parseProductionReleaseOptions(arguments, &bytes.Buffer{})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("window error=%v", err)
			}
		})
	}
}

func TestParseProductionReleaseAcceptsSafeAbsoluteInputs(t *testing.T) {
	root := t.TempDir()
	arguments := append(validProductionReleaseArguments(root),
		"--rollback-package", filepath.Join(root, "rollback.pkg.tar.zst"),
		"--observation-seconds", "240",
		"--rollback-seconds", "3300",
		"--manual-confirm",
		"--preserve-edge-policy",
	)
	options, err := parseProductionReleaseOptions(arguments, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if options.ObservationSeconds != 240 || options.RollbackSeconds != 3300 || options.Confirm != "api.lmm.best" ||
		!options.ManualConfirm || !options.PreserveEdgePolicy {
		t.Fatalf("options=%#v", options)
	}
}

func TestParseProductionReleaseRequiresAgeFilesOnlyWithBackups(t *testing.T) {
	arguments := append(validProductionReleaseArguments(t.TempDir()), "--with-backups")
	_, err := parseProductionReleaseOptions(arguments, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "required with --with-backups") {
		t.Fatalf("backup input error=%v", err)
	}
}

func TestRemoteGoPackageDeduplicatesPacmanProviderResolution(t *testing.T) {
	identity := productionAURPackageName + " 0.1.19-1"
	runtime := &productionReleaseRuntime{runner: productionPackageQueryRunner{identities: map[string]string{
		productionAURPackageName:    identity,
		productionSourcePackageName: identity,
	}}}

	got, err := runtime.remoteGoPackage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got != identity {
		t.Fatalf("remoteGoPackage()=%q, want %q", got, identity)
	}
}

func TestRemoteGoPackageRejectsDistinctInstalledPackages(t *testing.T) {
	runtime := &productionReleaseRuntime{runner: productionPackageQueryRunner{identities: map[string]string{
		productionAURPackageName:    productionAURPackageName + " 0.1.19-1",
		productionSourcePackageName: productionSourcePackageName + " 0.1.19-1",
	}}}

	_, err := runtime.remoteGoPackage(context.Background())
	if err == nil || !strings.Contains(err.Error(), "multiple production Go packages") {
		t.Fatalf("remoteGoPackage error=%v", err)
	}
}
