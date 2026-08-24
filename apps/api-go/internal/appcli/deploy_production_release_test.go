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

func validProductionReleasePlanArguments(root string) []string {
	path := func(name string) string { return filepath.Join(root, name) }
	return []string{
		"--repo", path("repo"),
		"--workspace", path("workspace"),
		"--deployment-id", "release-0.1.58-test",
		"--go-package", path("go.pkg.tar.zst"),
		"--go-release-asset", path("go.tar.gz"),
		"--go-release-bundle", path("go.tar.gz.bundle"),
		"--go-rollback-package", path("go-old.pkg.tar.zst"),
		"--go-rollback-release-asset", path("go-old.tar.gz"),
		"--go-rollback-release-bundle", path("go-old.tar.gz.bundle"),
		"--web-package", path("web.pkg.tar.zst"),
		"--web-release-asset", path("web.tar.gz"),
		"--web-release-bundle", path("web.tar.gz.bundle"),
		"--web-rollback-package", path("web-old.pkg.tar.zst"),
		"--web-rollback-release-asset", path("web-old.tar.gz"),
		"--web-rollback-release-bundle", path("web-old.tar.gz.bundle"),
		"--probe-binary", path("lmm-api"),
	}
}

func TestParseProductionReleasePlanConstrainsRollbackAndObservationWindows(t *testing.T) {
	tests := []struct {
		name  string
		flags []string
		want  string
	}{
		{name: "short observation", flags: []string{"--observation-seconds", "119"}, want: "between 120 and 360"},
		{name: "long observation", flags: []string{"--observation-seconds", "361"}, want: "between 120 and 360"},
		{name: "short rollback", flags: []string{"--rollback-seconds", "599"}, want: "exactly 600"},
		{name: "long rollback", flags: []string{"--rollback-seconds", "601"}, want: "exactly 600"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			arguments := append(validProductionReleasePlanArguments(t.TempDir()), test.flags...)
			_, err := parseProductionReleasePlanOptions(arguments, &bytes.Buffer{})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("window error=%v", err)
			}
		})
	}
}

func TestParseProductionReleasePlanAcceptsSafeAbsoluteInputs(t *testing.T) {
	arguments := append(validProductionReleasePlanArguments(t.TempDir()),
		"--observation-seconds", "240",
		"--rollback-seconds", "600",
		"--manual-confirm",
		"--preserve-edge-policy",
	)
	options, err := parseProductionReleasePlanOptions(arguments, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if options.ObservationSeconds != 240 || options.RollbackSeconds != 600 ||
		!options.ManualConfirm || !options.PreserveEdgePolicy {
		t.Fatalf("options=%#v", options)
	}
}

func TestParseProductionReleasePlanRequiresAgeRecipientWithBackups(t *testing.T) {
	arguments := append(validProductionReleasePlanArguments(t.TempDir()), "--with-backups")
	_, err := parseProductionReleasePlanOptions(arguments, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "--age-recipient-file is required") {
		t.Fatalf("backup input error=%v", err)
	}
}

func TestParseProductionReleaseControllerRequiresExactPlanDigestAndConfirmation(t *testing.T) {
	root := t.TempDir()
	arguments := []string{
		"--plan", filepath.Join(root, productionReleasePlanFilename),
		"--plan-sha256", strings.Repeat("a", 64),
		"--confirm", "wrong-host",
	}
	_, err := parseProductionReleaseControllerOptions("stage", arguments, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "--confirm must equal api.lmm.best") {
		t.Fatalf("confirmation error=%v", err)
	}
	arguments[len(arguments)-1] = "api.lmm.best"
	options, err := parseProductionReleaseControllerOptions("stage", arguments, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if options.PlanSHA256 != strings.Repeat("a", 64) {
		t.Fatalf("options=%#v", options)
	}
}

func TestPackageReleaseVersionRejectsMissingPkgrel(t *testing.T) {
	if _, err := packageReleaseVersion("0.1.58"); err == nil {
		t.Fatal("packageReleaseVersion accepted a version without pkgrel")
	}
	got, err := packageReleaseVersion("0.1.58-2")
	if err != nil || got != "0.1.58" {
		t.Fatalf("packageReleaseVersion()=(%q, %v)", got, err)
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
