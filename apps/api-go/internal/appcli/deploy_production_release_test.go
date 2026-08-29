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

func TestParseProductionReleasePlanConstrainsObservationWindowAndRemovesAutomaticRollbackFlags(t *testing.T) {
	tests := []struct {
		name  string
		flags []string
		want  string
	}{
		{name: "short observation", flags: []string{"--observation-seconds", "119"}, want: "between 120 and 360"},
		{name: "long observation", flags: []string{"--observation-seconds", "361"}, want: "between 120 and 360"},
		{name: "rollback seconds removed", flags: []string{"--rollback-seconds", "600"}, want: "flag provided but not defined"},
		{name: "manual confirm removed because confirmation is always manual", flags: []string{"--manual-confirm"}, want: "flag provided but not defined"},
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
		"--preserve-edge-policy",
	)
	options, err := parseProductionReleasePlanOptions(arguments, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if options.ObservationSeconds != 240 || !options.PreserveEdgePolicy {
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
		return []byte(productionAURPackageName + " 0.2.0-1\n"), nil
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
		case member == "usr/bin/lmm-api-go":
			return []byte("provider-cli"), nil
		case member == "usr/bin/lmm-api":
			runner.requestedLegacy = true
			return nil, errors.New("generic member must not be read")
		}
	}
	return nil, errors.New("unexpected command")
}

func TestPackageMetadataReadsProviderExecutableWithoutGenericFallback(t *testing.T) {
	runner := &productionCanonicalPackageMetadataRunner{}
	runtime := &productionRuntime{runner: runner}
	metadata, err := runtime.packageMetadata(context.Background(), "/safe/lmm-api-go-bin-0.2.0-1-x86_64.pkg.tar.zst", productionAURPackageName)
	if err != nil {
		t.Fatal(err)
	}
	if runner.requestedLegacy {
		t.Fatal("package metadata attempted the removed generic CLI payload")
	}
	if metadata.ReleaseAssetSHA256 != strings.Repeat("c", 64) || !productionSHA256Pattern.MatchString(metadata.BinarySHA256) {
		t.Fatalf("metadata=%#v", metadata)
	}
}

func testProductionReleasePackage(t *testing.T, root, name, version, digest, payload string) productionReleasePackagePlan {
	t.Helper()
	prefix, workflow := "go-v", "release-go.yml"
	if name == productionWebPackageName {
		prefix, workflow = "web-v", "release-web.yml"
	}
	releaseVersion, err := packageReleaseVersion(version)
	if err != nil {
		t.Fatal(err)
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

func testProductionReleasePlan(t *testing.T, root string) productionReleasePlan {
	t.Helper()
	goCandidate := testProductionReleasePackage(t, root, productionAURPackageName, "0.2.0-1", strings.Repeat("1", 64), strings.Repeat("2", 64))
	goRollback := testProductionReleasePackage(t, root, productionAURPackageName, "0.1.69-1", strings.Repeat("3", 64), strings.Repeat("4", 64))
	web := testProductionReleasePackage(t, root, productionWebPackageName, "0.1.41-1", strings.Repeat("5", 64), strings.Repeat("6", 64))
	return productionReleasePlan{
		Format:              productionReleasePlanFormat,
		DeploymentID:        "release-0.2.0-test",
		CreatedUTC:          time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC),
		ControllerWorkspace: root,
		Repository:          filepath.Join(root, "repo"),
		TargetAlias:         productionTargetAlias,
		ExpectedHost:        productionExpectedHost,
		OperatorUser:        productionOperatorUser,
		ExpectedVersion:     "0.2.0",
		GoCandidate:         goCandidate,
		GoRollback:          goRollback,
		WebCandidate:        web,
		WebRollback:         web,
		ProbeBinary:         productionReleaseFilePlan{Path: filepath.Join(root, "lmm-api"), SHA256: goCandidate.PayloadSHA256},
		OperatorBinary:      productionReleaseFilePlan{Path: filepath.Join(root, "lmm-api"), SHA256: goCandidate.PayloadSHA256},
		GoChanged:           true,
		ObservationSeconds:  180,
		WithBackups:         true,
		AgeRecipient:        productionReleaseFilePlan{Path: filepath.Join(root, "age-recipient.txt"), SHA256: strings.Repeat("f", 64)},
	}
}

func TestLoadProductionReleasePlanRequiresExactCanonicalBytes(t *testing.T) {
	root := t.TempDir()
	plan := testProductionReleasePlan(t, root)
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
	plan := testProductionReleasePlan(t, t.TempDir())
	plan.WebCandidate.ContractRevision = strings.Repeat("c", 64)
	if err := validateProductionReleasePlan(plan); err == nil || !strings.Contains(err.Error(), "route-contract") {
		t.Fatalf("contract mismatch error=%v", err)
	}
}

func TestValidateProductionReleasePlanRequiresBackupsForGoChanges(t *testing.T) {
	plan := testProductionReleasePlan(t, t.TempDir())
	plan.WithBackups = false
	plan.AgeRecipient = productionReleaseFilePlan{}
	if err := validateProductionReleasePlan(plan); err == nil || !strings.Contains(err.Error(), "Go changes require verified three-copy backups") {
		t.Fatalf("Go backup requirement error=%v", err)
	}
}

func TestValidateProductionReleasePlanAllowsWebOnlyWithoutBackups(t *testing.T) {
	plan := testProductionReleasePlan(t, t.TempDir())
	plan.GoCandidate = plan.GoRollback
	plan.GoChanged = false
	plan.ExpectedVersion = "0.1.69"
	plan.ProbeBinary.SHA256 = plan.GoCandidate.PayloadSHA256
	plan.OperatorBinary.SHA256 = plan.GoCandidate.PayloadSHA256
	plan.WebCandidate = testProductionReleasePackage(t, plan.ControllerWorkspace, productionWebPackageName, "0.1.42-1", strings.Repeat("7", 64), strings.Repeat("8", 64))
	plan.WebChanged = true
	plan.WithBackups = false
	plan.AgeRecipient = productionReleaseFilePlan{}
	if err := validateProductionReleasePlan(plan); err != nil {
		t.Fatalf("Web-only plan without backups rejected: %v", err)
	}
}

func TestValidateProductionReleasePlanRequiresCandidateOperatorBinary(t *testing.T) {
	plan := testProductionReleasePlan(t, t.TempDir())
	plan.OperatorBinary.SHA256 = strings.Repeat("9", 64)
	if err := validateProductionReleasePlan(plan); err == nil || !strings.Contains(err.Error(), "operator") {
		t.Fatalf("operator provenance error=%v", err)
	}
}

func testProductionPackageInfo(t *testing.T, name, version string) string {
	t.Helper()
	releaseVersion, err := packageReleaseVersion(version)
	if err != nil {
		t.Fatal(err)
	}
	lines := []string{
		"pkgname = " + name,
		"pkgbase = " + name,
		"pkgver = " + version,
		"license = AGPL-3.0-only",
	}
	if name == productionAURPackageName {
		lines = append(lines, "arch = x86_64", "backup = etc/lmm-api-go/lmm-api-go.env")
		conflicts := []string{"lmm-api-go", "lmm-api-go-git"}
		provides := []string{"lmm-api-go=" + releaseVersion, "lmm-api-provider"}
		if version == "0.1.69-1" {
			conflicts = []string{"lmm-api", "lmm-api-bin", "lmm-api-git", "lmm-api-go", "lmm-api-go-git"}
			provides = []string{"lmm-api=" + releaseVersion, "lmm-api-go=" + releaseVersion}
		}
		for _, value := range conflicts {
			lines = append(lines, "conflict = "+value)
		}
		for _, value := range provides {
			lines = append(lines, "provides = "+value)
		}
		for _, value := range []string{"ca-certificates", "coreutils", "libarchive", "pacman", "paru", "sudo", "systemd", "tzdata", "util-linux"} {
			lines = append(lines, "depend = "+value)
		}
	} else {
		lines = append(lines,
			"arch = any",
			"conflict = lmm-api-web",
			"provides = lmm-api-web="+releaseVersion,
			"depend = lmm-api-provider",
			"depend = nginx",
		)
	}
	return strings.Join(lines, "\n") + "\n"
}

func testPackageMtree(t *testing.T, integrated bool, sudoersDirectoryMode ...string) string {
	t.Helper()
	directoryMode := "750"
	if len(sudoersDirectoryMode) == 1 {
		directoryMode = sudoersDirectoryMode[0]
	} else if len(sudoersDirectoryMode) > 1 {
		t.Fatal("test package mtree accepts at most one sudoers directory mode")
	}
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	if _, err := writer.Write([]byte("#mtree\n/set type=file uid=0 gid=0 mode=644\n")); err != nil {
		t.Fatal(err)
	}
	if integrated {
		if _, err := writer.Write([]byte("./etc/sudoers.d type=dir mode=" + directoryMode + "\n./etc/sudoers.d/lmm-api-operator mode=440\n")); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return compressed.String()
}

type testTarEntry struct {
	name      string
	body      string
	mode      int64
	uid       int
	gid       int
	linkTo    string
	directory bool
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
		header := &tar.Header{Name: entry.name, Mode: entry.mode, Uid: entry.uid, Gid: entry.gid, Size: int64(len(entry.body)), Typeflag: tar.TypeReg}
		if entry.directory {
			header.Typeflag = tar.TypeDir
			header.Size = 0
		} else if entry.linkTo != "" {
			header.Typeflag = tar.TypeSymlink
			header.Linkname = entry.linkTo
			header.Size = 0
		}
		if err := archive.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if entry.linkTo == "" && !entry.directory {
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

func testEdgePolicyTarEntries(prefix string) []testTarEntry {
	return []testTarEntry{
		{name: prefix + "nginx/http-map.conf", body: "geoip2 /var/lib/geoip2/DBIP-Country-Lite.mmdb {\n}\n", mode: 0o644},
		{name: prefix + "nginx/new-api.conf", body: "include /etc/nginx/lmm-api-region-policy.conf;\n", mode: 0o644},
		{name: prefix + "nginx/lmm-api-locations.conf", body: "error_page 418 = @lmm_api_cors_preflight;\nlocation @lmm_api_cors_preflight {\nauth_request off;\n}\nset $lmm_access_policy_original_uri $uri;\nif ($request_method = OPTIONS) { return 418; }\nadd_header Access-Control-Allow-Methods $http_access_control_request_method always;\nadd_header Access-Control-Allow-Headers $http_access_control_request_headers always;\nadd_header Vary \"Origin, Access-Control-Request-Method, Access-Control-Request-Headers\" always;\n", mode: 0o644},
		{name: prefix + "nginx/lmm-api-region-policy.conf", body: "auth_request /internal/access-ip-policy;\nproxy_set_header X-LMM-Original-URI $lmm_access_policy_original_uri;\nproxy_set_header X-LMM-Original-Accept $http_accept;\n", mode: 0o644},
		{name: prefix + "nginx/mime.types", body: "types {}\n", mode: 0o644},
		{name: prefix + "geoip2-country-update.service", body: "[Service]\n", mode: 0o644},
		{name: prefix + "geoip2-country-update.timer", body: "[Timer]\n", mode: 0o644},
	}
}

func TestVerifySignedPackageLayoutAcceptsOnlyProviderLayoutAndExactLegacyRollback(t *testing.T) {
	workspace := t.TempDir()
	runtime := &productionReleaseRuntime{runner: osProductionCommandRunner{}}

	newAsset := filepath.Join(workspace, "release-0.2.0.tar.gz")
	newPrefix := "lmm-api-go-0.2.0-linux-amd64/"
	newReleaseEntries := []testTarEntry{
		{name: newPrefix + "lmm-api-go", body: "provider-binary", mode: 0o755},
		{name: newPrefix + "lmm-api-go.env", body: "safe-env\n", mode: 0o640},
		{name: newPrefix + "lmm-api-operator.sudoers", body: "safe-sudoers\n", mode: 0o644},
		{name: newPrefix + "LICENSE", body: "license\n", mode: 0o644},
	}
	newReleaseEntries = append(newReleaseEntries, testEdgePolicyTarEntries(newPrefix+"edge-policy/")...)
	writeTestTarGzip(t, newAsset, newReleaseEntries)
	newAssetSHA256, err := sha256File(newAsset)
	if err != nil {
		t.Fatal(err)
	}
	newPackageEntries := func(sudoers string) []testTarEntry {
		entries := []testTarEntry{
			{name: ".PKGINFO", body: testProductionPackageInfo(t, productionAURPackageName, "0.2.0-1"), mode: 0o644},
			{name: ".MTREE", body: testPackageMtree(t, true), mode: 0o644},
			{name: "usr/bin/lmm-api-go", body: "provider-binary", mode: 0o755},
			{name: "etc/lmm-api-go/lmm-api-go.env", body: "safe-env\n", mode: 0o600},
			{name: "etc/sudoers.d/", mode: 0o750, directory: true},
			{name: "etc/sudoers.d/lmm-api-operator", body: sudoers, mode: 0o440},
			{name: "usr/share/licenses/lmm-api-go-bin/LICENSE", body: "license\n", mode: 0o644},
			{name: "usr/share/doc/lmm-api-go-bin/RELEASE_ASSET_SHA256", body: newAssetSHA256 + "\n", mode: 0o644},
		}
		return append(entries, testEdgePolicyTarEntries("usr/share/lmm-api-go/edge-policy/")...)
	}
	newPackage := filepath.Join(workspace, "new-provider.tar.gz")
	writeTestTarGzip(t, newPackage, newPackageEntries("safe-sudoers\n"))
	if err := runtime.verifySignedPackageLayout(context.Background(), workspace, productionAURPackageName, "0.2.0-1", newPackage, newAsset, newAssetSHA256, true); err != nil {
		t.Fatal(err)
	}

	genericPayload := filepath.Join(workspace, "new-generic-payload.tar.gz")
	genericEntries := newPackageEntries("safe-sudoers\n")
	genericEntries = append(genericEntries, testTarEntry{name: "usr/bin/lmm-api", body: "generic", mode: 0o755})
	writeTestTarGzip(t, genericPayload, genericEntries)
	if err := runtime.verifySignedPackageLayout(context.Background(), workspace, productionAURPackageName, "0.2.0-1", genericPayload, newAsset, newAssetSHA256, true); err == nil {
		t.Fatal("new generic CLI payload was accepted")
	}

	reverseLink := filepath.Join(workspace, "new-reverse-link.tar.gz")
	reverseEntries := newPackageEntries("safe-sudoers\n")
	reverseEntries = append(reverseEntries, testTarEntry{name: "usr/bin/lmm-api", mode: 0o777, linkTo: "lmm-api-go"})
	writeTestTarGzip(t, reverseLink, reverseEntries)
	if err := runtime.verifySignedPackageLayout(context.Background(), workspace, productionAURPackageName, "0.2.0-1", reverseLink, newAsset, newAssetSHA256, true); err == nil || !strings.Contains(err.Error(), "unexpected symlink") {
		t.Fatalf("new reverse-link error=%v", err)
	}

	tampered := filepath.Join(workspace, "tampered-provider.tar.gz")
	writeTestTarGzip(t, tampered, newPackageEntries("unsafe-sudoers\n"))
	if err := runtime.verifySignedPackageLayout(context.Background(), workspace, productionAURPackageName, "0.2.0-1", tampered, newAsset, newAssetSHA256, true); err == nil || !strings.Contains(err.Error(), "differs from signed release") {
		t.Fatalf("tampered package error=%v", err)
	}

	legacyAsset := filepath.Join(workspace, "release-0.1.69.tar.gz")
	legacyPrefix := "lmm-api-go-0.1.69-linux-amd64/"
	legacyReleaseEntries := []testTarEntry{
		{name: legacyPrefix + "lmm-api", body: "legacy-binary", mode: 0o755},
		{name: legacyPrefix + "lmm-api-go.env", body: "safe-env\n", mode: 0o640},
		{name: legacyPrefix + "lmm-api-operator.sudoers", body: "safe-sudoers\n", mode: 0o644},
	}
	legacyReleaseEntries = append(legacyReleaseEntries, testEdgePolicyTarEntries(legacyPrefix+"edge-policy/")...)
	writeTestTarGzip(t, legacyAsset, legacyReleaseEntries)
	legacyAssetSHA256, err := sha256File(legacyAsset)
	if err != nil {
		t.Fatal(err)
	}
	legacyEntries := []testTarEntry{
		{name: ".PKGINFO", body: testProductionPackageInfo(t, productionAURPackageName, "0.1.69-1"), mode: 0o644},
		{name: ".MTREE", body: testPackageMtree(t, true), mode: 0o644},
		{name: "usr/bin/lmm-api", body: "legacy-binary", mode: 0o755},
		{name: "usr/bin/lmm-api-go", mode: 0o777, linkTo: "lmm-api"},
		{name: "etc/lmm-api-go/lmm-api-go.env", body: "safe-env\n", mode: 0o600},
		{name: "etc/sudoers.d/", mode: 0o750, directory: true},
		{name: "etc/sudoers.d/lmm-api-operator", body: "safe-sudoers\n", mode: 0o440},
		{name: "usr/share/doc/lmm-api-go-bin/RELEASE_ASSET_SHA256", body: legacyAssetSHA256 + "\n", mode: 0o644},
	}
	legacyEntries = append(legacyEntries, testEdgePolicyTarEntries("usr/share/lmm-api-go/edge-policy/")...)
	legacyPackage := filepath.Join(workspace, "legacy-0.1.69.tar.gz")
	writeTestTarGzip(t, legacyPackage, legacyEntries)
	if err := runtime.verifySignedPackageLayout(context.Background(), workspace, productionAURPackageName, "0.1.69-1", legacyPackage, legacyAsset, legacyAssetSHA256, true); err != nil {
		t.Fatalf("verified legacy rollback rejected: %v", err)
	}

	legacyWrongDirection := filepath.Join(workspace, "legacy-wrong-direction.tar.gz")
	for index := range legacyEntries {
		if legacyEntries[index].name == "usr/bin/lmm-api" {
			legacyEntries[index] = testTarEntry{name: "usr/bin/lmm-api", mode: 0o777, linkTo: "lmm-api-go"}
		}
		if legacyEntries[index].name == "usr/bin/lmm-api-go" {
			legacyEntries[index] = testTarEntry{name: "usr/bin/lmm-api-go", body: "legacy-binary", mode: 0o755}
		}
	}
	writeTestTarGzip(t, legacyWrongDirection, legacyEntries)
	if err := runtime.verifySignedPackageLayout(context.Background(), workspace, productionAURPackageName, "0.1.69-1", legacyWrongDirection, legacyAsset, legacyAssetSHA256, true); err == nil {
		t.Fatal("legacy reverse compatibility direction was accepted")
	}
}

func TestVerifySignedWebPackageLayoutRequiresNativeCLIActivationHook(t *testing.T) {
	installHook, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "packaging", "common", "lmm-api", "lmm-api-web.install"))
	if err != nil {
		t.Fatal(err)
	}
	verify := func(name string, packageHook []byte, includeSignedHook, includeShellPublisher bool, wantError string) {
		t.Helper()
		caseRoot := t.TempDir()
		asset := filepath.Join(caseRoot, "web-0.1.52.tar.gz")
		assetEntries := []testTarEntry{{name: "dist/index.html", body: "<!doctype html>\n", mode: 0o644}}
		packageEntries := []testTarEntry{
			{name: ".PKGINFO", body: testProductionPackageInfo(t, productionWebPackageName, "0.1.52-1"), mode: 0o644},
			{name: ".INSTALL", body: string(packageHook), mode: 0o644},
			{name: "usr/share/lmm-api-web/frontend-dist/index.html", body: "<!doctype html>\n", mode: 0o644},
		}
		if includeSignedHook {
			assetEntries = append(assetEntries, testTarEntry{name: "lmm-api-web.install", body: string(installHook), mode: 0o644})
		}
		if includeShellPublisher {
			assetEntries = append(assetEntries, testTarEntry{name: "frontend-release.sh", body: "#!/bin/sh\nexit 0\n", mode: 0o755})
			packageEntries = append(packageEntries, testTarEntry{name: "usr/lib/lmm-api-web/frontend-release.sh", body: "#!/bin/sh\nexit 0\n", mode: 0o755})
		}
		writeTestTarGzip(t, asset, assetEntries)
		assetSHA256, err := sha256File(asset)
		if err != nil {
			t.Fatal(err)
		}
		packageEntries = append(packageEntries, testTarEntry{name: "usr/share/doc/lmm-api-web-bin/RELEASE_ASSET_SHA256", body: assetSHA256 + "\n", mode: 0o644})
		packagePath := filepath.Join(caseRoot, name+".tar.gz")
		writeTestTarGzip(t, packagePath, packageEntries)
		runtime := &productionReleaseRuntime{runner: osProductionCommandRunner{}}
		err = runtime.verifySignedPackageLayout(context.Background(), caseRoot, productionWebPackageName, "0.1.52-1", packagePath, asset, assetSHA256, false)
		if wantError == "" {
			if err != nil {
				t.Fatal(err)
			}
			return
		}
		if err == nil || !strings.Contains(err.Error(), wantError) {
			t.Fatalf("%s error=%v want %q", name, err, wantError)
		}
	}
	verify("native", installHook, true, false, "")
	verify("tampered-hook", []byte("post_install() { /bin/false; }\n"), true, false, "install hook")
	verify("unsigned-hook", installHook, false, false, "lacks lmm-api-web.install")
	verify("shell-publisher", installHook, true, true, "unmapped payload")
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
