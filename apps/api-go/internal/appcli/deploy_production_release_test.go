package appcli

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

type productionCanonicalPackageMetadataRunner struct {
	requestedLegacy bool
}

func (runner *productionCanonicalPackageMetadataRunner) Run(_ context.Context, command productionCommand) ([]byte, error) {
	switch command.Name {
	case commandPacman:
		return []byte(productionAURPackageName + " 0.1.58-1\n"), nil
	case commandBsdtar:
		if len(command.Args) != 3 || command.Args[0] != "-xOf" {
			return nil, errors.New("unexpected bsdtar arguments")
		}
		member := command.Args[2]
		switch {
		case strings.HasSuffix(member, "/REVISION"):
			return []byte(strings.Repeat("a", 40) + "\n"), nil
		case strings.HasSuffix(member, "/API_ROUTE_CONTRACT_REVISION"):
			return []byte(strings.Repeat("b", 64) + "\n"), nil
		case strings.HasSuffix(member, "/RELEASE_ASSET_SHA256"):
			return []byte(strings.Repeat("c", 64) + "\n"), nil
		case member == "usr/bin/lmm-api":
			return []byte("canonical-cli"), nil
		case member == "usr/bin/lmm-api-go":
			runner.requestedLegacy = true
			return nil, errors.New("legacy member must not be read")
		}
	}
	return nil, errors.New("unexpected command")
}

func TestPackageMetadataReadsCanonicalCLIWithoutFollowingCompatibilitySymlink(t *testing.T) {
	runner := &productionCanonicalPackageMetadataRunner{}
	runtime := &productionRuntime{runner: runner}
	metadata, err := runtime.packageMetadata(context.Background(), "/safe/lmm-api-go-bin-0.1.58-1-x86_64.pkg.tar.zst", productionAURPackageName)
	if err != nil {
		t.Fatal(err)
	}
	if runner.requestedLegacy {
		t.Fatal("package metadata read the compatibility symlink after finding the canonical CLI")
	}
	if metadata.ReleaseAssetSHA256 != strings.Repeat("c", 64) || !productionSHA256Pattern.MatchString(metadata.BinarySHA256) {
		t.Fatalf("metadata=%#v", metadata)
	}
}

func testProductionReleasePackage(root, name, version, digest, payload string) productionReleasePackagePlan {
	prefix, workflow := "go-v", "release-go.yml"
	if name == productionWebPackageName {
		prefix, workflow = "web-v", "release-web.yml"
	}
	releaseVersion, err := packageReleaseVersion(version)
	if err != nil {
		panic(err)
	}
	return productionReleasePackagePlan{
		PackagePath:           filepath.Join(root, name+"-"+version+".pkg.tar.zst"),
		PackageSHA256:         digest,
		Name:                  name,
		Version:               version,
		Identity:              name + " " + version,
		GitRevision:           strings.Repeat("a", 40),
		ContractRevision:      strings.Repeat("b", 64),
		PayloadSHA256:         payload,
		ReleaseAsset:          filepath.Join(root, name+"-"+releaseVersion+".tar.gz"),
		ReleaseAssetSHA256:    strings.Repeat("d", 64),
		SignatureBundle:       filepath.Join(root, name+"-"+releaseVersion+".sigstore.json"),
		SignatureBundleSHA256: strings.Repeat("e", 64),
		ReleaseTag:            prefix + releaseVersion,
		Workflow:              workflow,
	}
}

func testProductionReleasePlan(root string) productionReleasePlan {
	goCandidate := testProductionReleasePackage(root, productionAURPackageName, "0.1.58-1", strings.Repeat("1", 64), strings.Repeat("2", 64))
	goRollback := testProductionReleasePackage(root, productionAURPackageName, "0.1.57-1", strings.Repeat("3", 64), strings.Repeat("4", 64))
	web := testProductionReleasePackage(root, productionWebPackageName, "0.1.41-1", strings.Repeat("5", 64), strings.Repeat("6", 64))
	return productionReleasePlan{
		Format:              productionReleasePlanFormat,
		DeploymentID:        "release-0.1.58-test",
		CreatedUTC:          time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC),
		ControllerWorkspace: root,
		Repository:          filepath.Join(root, "repo"),
		TargetAlias:         productionTargetAlias,
		ExpectedHost:        productionExpectedHost,
		OperatorUser:        productionOperatorUser,
		ExpectedVersion:     "0.1.58",
		GoCandidate:         goCandidate,
		GoRollback:          goRollback,
		WebCandidate:        web,
		WebRollback:         web,
		ProbeBinary:         productionReleaseFilePlan{Path: filepath.Join(root, "lmm-api"), SHA256: goCandidate.PayloadSHA256},
		GoChanged:           true,
		ObservationSeconds:  180,
		RollbackSeconds:     600,
	}
}

func TestLoadProductionReleasePlanRequiresExactCanonicalBytes(t *testing.T) {
	root := t.TempDir()
	plan := testProductionReleasePlan(root)
	encoded, err := canonicalProductionReleasePlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, productionReleasePlanFilename)
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(encoded)
	hexDigest := hex.EncodeToString(digest[:])
	loaded, err := loadProductionReleasePlan(path, hexDigest)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.DeploymentID != plan.DeploymentID {
		t.Fatalf("loaded plan=%#v", loaded)
	}
	tampered := append(append([]byte(nil), encoded...), '\n')
	if err := os.WriteFile(path, tampered, 0o600); err != nil {
		t.Fatal(err)
	}
	tamperedDigest := sha256.Sum256(tampered)
	_, err = loadProductionReleasePlan(path, hex.EncodeToString(tamperedDigest[:]))
	if err == nil || !strings.Contains(err.Error(), "not canonical JSON") {
		t.Fatalf("noncanonical plan error=%v", err)
	}
}

func TestValidateProductionReleasePlanRejectsCandidateContractMismatch(t *testing.T) {
	plan := testProductionReleasePlan(t.TempDir())
	plan.WebCandidate.ContractRevision = strings.Repeat("c", 64)
	if err := validateProductionReleasePlan(plan); err == nil || !strings.Contains(err.Error(), "route-contract") {
		t.Fatalf("contract mismatch error=%v", err)
	}
}

type testTarEntry struct {
	name   string
	body   string
	mode   int64
	linkTo string
}

func writeTestTarGzip(t *testing.T, path string, entries []testTarEntry) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	compressed := gzip.NewWriter(file)
	archive := tar.NewWriter(compressed)
	for _, entry := range entries {
		header := &tar.Header{Name: entry.name, Mode: entry.mode, Size: int64(len(entry.body)), Typeflag: tar.TypeReg}
		if entry.linkTo != "" {
			header.Typeflag = tar.TypeSymlink
			header.Linkname = entry.linkTo
			header.Size = 0
		}
		if err := archive.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if entry.linkTo == "" {
			if _, err := archive.Write([]byte(entry.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := compressed.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestVerifySignedPackageLayoutRejectsUnsignedOperatorMutation(t *testing.T) {
	workspace := t.TempDir()
	asset := filepath.Join(workspace, "release.tar.gz")
	writeTestTarGzip(t, asset, []testTarEntry{
		{name: "lmm-api-go-0.1.58-linux-amd64/lmm-api", body: "binary", mode: 0o755},
		{name: "lmm-api-go-0.1.58-linux-amd64/lmm-api-operator.sudoers", body: "safe-sudoers\n", mode: 0o644},
		{name: "lmm-api-go-0.1.58-linux-amd64/LICENSE", body: "license\n", mode: 0o644},
	})
	assetSHA256, err := sha256File(asset)
	if err != nil {
		t.Fatal(err)
	}
	packageEntries := func(sudoers string, legacyAlias bool) []testTarEntry {
		entries := []testTarEntry{
			{name: "usr/bin/lmm-api", body: "binary", mode: 0o755},
			{name: "etc/sudoers.d/lmm-api-operator", body: sudoers, mode: 0o440},
			{name: "usr/share/licenses/lmm-api-go-bin/LICENSE", body: "license\n", mode: 0o644},
			{name: "usr/share/doc/lmm-api-go-bin/RELEASE_ASSET_SHA256", body: assetSHA256 + "\n", mode: 0o644},
		}
		if legacyAlias {
			entries = append(entries, testTarEntry{name: "usr/bin/lmm-api-go", mode: 0o777, linkTo: "lmm-api"})
		}
		return entries
	}
	packagePath := filepath.Join(workspace, "package.tar.gz")
	writeTestTarGzip(t, packagePath, packageEntries("safe-sudoers\n", true))
	runtime := &productionReleaseRuntime{runner: osProductionCommandRunner{}}
	if err := runtime.verifySignedPackageLayout(context.Background(), workspace, productionAURPackageName, "0.1.58-1", packagePath, asset, assetSHA256); err != nil {
		t.Fatal(err)
	}
	tamperedPackage := filepath.Join(workspace, "tampered.tar.gz")
	writeTestTarGzip(t, tamperedPackage, packageEntries("unsafe-sudoers\n", true))
	if err := runtime.verifySignedPackageLayout(context.Background(), workspace, productionAURPackageName, "0.1.58-1", tamperedPackage, asset, assetSHA256); err == nil || !strings.Contains(err.Error(), "differs from signed release") {
		t.Fatalf("tampered package error=%v", err)
	}
	t1Package := filepath.Join(workspace, "t1.tar.gz")
	writeTestTarGzip(t, t1Package, packageEntries("safe-sudoers\n", false))
	if err := runtime.verifySignedPackageLayout(context.Background(), workspace, productionAURPackageName, "0.1.59-1", t1Package, asset, assetSHA256); err != nil {
		t.Fatal(err)
	}
	if err := runtime.verifySignedPackageLayout(context.Background(), workspace, productionAURPackageName, "0.1.59-1", packagePath, asset, assetSHA256); err == nil || !strings.Contains(err.Error(), "T1 package still exposes") {
		t.Fatalf("T1 compatibility-link error=%v", err)
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
