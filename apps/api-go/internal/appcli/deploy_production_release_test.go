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
	cliPhase := ""
	if name == productionAURPackageName {
		cliPhase, err = packageCLITransitionPhase(name, version, "")
		if err != nil {
			t.Fatal(err)
		}
	}
	return productionReleasePackagePlan{
		PackagePath:           filepath.Join(root, name+"-"+version+".pkg.tar.zst"),
		PackageSHA256:         digest,
		Name:                  name,
		Version:               version,
		Identity:              name + " " + version,
		GitRevision:           strings.Repeat("a", 40),
		ContractRevision:      strings.Repeat("b", 64),
		CLITransitionPhase:    cliPhase,
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
	goCandidate := testProductionReleasePackage(t, root, productionAURPackageName, "0.1.59-1", strings.Repeat("1", 64), strings.Repeat("2", 64))
	goRollback := testProductionReleasePackage(t, root, productionAURPackageName, "0.1.57-1", strings.Repeat("3", 64), strings.Repeat("4", 64))
	web := testProductionReleasePackage(t, root, productionWebPackageName, "0.1.41-1", strings.Repeat("5", 64), strings.Repeat("6", 64))
	return productionReleasePlan{
		Format:              productionReleasePlanFormat,
		DeploymentID:        "release-0.1.59-test",
		CreatedUTC:          time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC),
		ControllerWorkspace: root,
		Repository:          filepath.Join(root, "repo"),
		TargetAlias:         productionTargetAlias,
		ExpectedHost:        productionExpectedHost,
		OperatorUser:        productionOperatorUser,
		ExpectedVersion:     "0.1.59",
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
	plan.ExpectedVersion = "0.1.57"
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

func testProductionPackageInfo(t *testing.T, name, version, phase string) string {
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
		conflicts := []string{"lmm-api", "lmm-api-bin", "lmm-api-git", "lmm-api-go", "lmm-api-go-git"}
		integrated, err := isIntegratedOperatorPackage(name, version)
		if err != nil {
			t.Fatal(err)
		}
		provides := []string{"lmm-api=" + releaseVersion}
		dependencies := []string{"ca-certificates", "systemd", "tzdata"}
		if integrated {
			dependencies = []string{"ca-certificates", "coreutils", "libarchive", "pacman", "paru", "sudo", "systemd", "tzdata", "util-linux"}
		}
		if phase == productionCLIPhaseT0 {
			if integrated {
				provides = append(provides, "lmm-api-go="+releaseVersion)
			} else {
				provides = []string{"lmm-api-go=" + releaseVersion}
			}
		} else {
			conflicts = append(conflicts, "lmm-api-deploy", "lmm-api-deploy-bin")
			lines = append(lines, "replaces = lmm-api-deploy-bin")
		}
		for _, value := range conflicts {
			lines = append(lines, "conflict = "+value)
		}
		for _, value := range provides {
			lines = append(lines, "provides = "+value)
		}
		for _, value := range dependencies {
			lines = append(lines, "depend = "+value)
		}
	} else {
		lines = append(lines,
			"arch = any",
			"conflict = lmm-api-web",
			"provides = lmm-api-web="+releaseVersion,
		)
		for _, value := range []string{"bash", "coreutils", "diffutils", "findutils", "gawk", "grep", "nginx", "sed", "systemd", "util-linux"} {
			lines = append(lines, "depend = "+value)
		}
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

func TestVerifySignedPackageLayoutRejectsUnsignedOperatorMutation(t *testing.T) {
	workspace := t.TempDir()
	asset := filepath.Join(workspace, "release.tar.gz")
	releasePrefix := "lmm-api-go-0.1.59-linux-amd64/"
	releaseEntries := []testTarEntry{
		{name: releasePrefix + "lmm-api", body: "binary", mode: 0o755},
		{name: releasePrefix + "lmm-api-go.env", body: "safe-env\n", mode: 0o640},
		{name: releasePrefix + "lmm-api-operator.sudoers", body: "safe-sudoers\n", mode: 0o644},
		{name: releasePrefix + "LICENSE", body: "license\n", mode: 0o644},
	}
	releaseEntries = append(releaseEntries, testEdgePolicyTarEntries(releasePrefix+"edge-policy/")...)
	writeTestTarGzip(t, asset, releaseEntries)
	assetSHA256, err := sha256File(asset)
	if err != nil {
		t.Fatal(err)
	}
	packageEntries := func(version, phase, sudoers string, legacyAlias bool, sudoersDirectoryMode int64) []testTarEntry {
		entries := []testTarEntry{
			{name: ".PKGINFO", body: testProductionPackageInfo(t, productionAURPackageName, version, phase), mode: 0o644},
			{name: ".MTREE", body: testPackageMtree(t, true), mode: 0o644},
			{name: "usr/bin/lmm-api", body: "binary", mode: 0o755},
			{name: "etc/lmm-api-go/lmm-api-go.env", body: "safe-env\n", mode: 0o600},
			{name: "etc/sudoers.d/", mode: sudoersDirectoryMode, directory: true},
			{name: "etc/sudoers.d/lmm-api-operator", body: sudoers, mode: 0o440},
			{name: "usr/share/licenses/lmm-api-go-bin/LICENSE", body: "license\n", mode: 0o644},
			{name: "usr/share/doc/lmm-api-go-bin/RELEASE_ASSET_SHA256", body: assetSHA256 + "\n", mode: 0o644},
		}
		entries = append(entries, testEdgePolicyTarEntries("usr/share/lmm-api-go/edge-policy/")...)
		if legacyAlias {
			entries = append(entries, testTarEntry{name: "usr/bin/lmm-api-go", mode: 0o777, linkTo: "lmm-api"})
		}
		return entries
	}
	packagePath := filepath.Join(workspace, "package.tar.gz")
	writeTestTarGzip(t, packagePath, packageEntries("0.1.59-1", productionCLIPhaseT0, "safe-sudoers\n", true, 0o750))
	runtime := &productionReleaseRuntime{runner: osProductionCommandRunner{}}
	if err := runtime.verifySignedPackageLayout(context.Background(), workspace, productionAURPackageName, "0.1.59-1", packagePath, asset, assetSHA256, true); err != nil {
		t.Fatal(err)
	}
	tamperedPackage := filepath.Join(workspace, "tampered.tar.gz")
	writeTestTarGzip(t, tamperedPackage, packageEntries("0.1.59-1", productionCLIPhaseT0, "unsafe-sudoers\n", true, 0o750))
	if err := runtime.verifySignedPackageLayout(context.Background(), workspace, productionAURPackageName, "0.1.59-1", tamperedPackage, asset, assetSHA256, true); err == nil || !strings.Contains(err.Error(), "differs from signed release") {
		t.Fatalf("tampered package error=%v", err)
	}
	t1Package := filepath.Join(workspace, "t1.tar.gz")
	writeTestTarGzip(t, t1Package, packageEntries("0.1.60-1", productionCLIPhaseT1, "safe-sudoers\n", false, 0o750))
	if err := runtime.verifySignedPackageLayout(context.Background(), workspace, productionAURPackageName, "0.1.60-1", t1Package, asset, assetSHA256, true); err != nil {
		t.Fatal(err)
	}
	t1AliasPackage := filepath.Join(workspace, "t1-alias.tar.gz")
	writeTestTarGzip(t, t1AliasPackage, packageEntries("0.1.60-1", productionCLIPhaseT1, "safe-sudoers\n", true, 0o750))
	if err := runtime.verifySignedPackageLayout(context.Background(), workspace, productionAURPackageName, "0.1.60-1", t1AliasPackage, asset, assetSHA256, true); err == nil || !strings.Contains(err.Error(), "T1 package still exposes") {
		t.Fatalf("T1 compatibility-link error=%v", err)
	}

	hookPackage := filepath.Join(workspace, "root-hook.tar.gz")
	hookEntries := packageEntries("0.1.59-1", productionCLIPhaseT0, "safe-sudoers\n", true, 0o750)
	hookEntries = append(hookEntries, testTarEntry{name: ".INSTALL", body: "#!/bin/sh\nexit 0\n", mode: 0o755})
	writeTestTarGzip(t, hookPackage, hookEntries)
	if err := runtime.verifySignedPackageLayout(context.Background(), workspace, productionAURPackageName, "0.1.59-1", hookPackage, asset, assetSHA256, true); err == nil || !strings.Contains(err.Error(), "root install hook") {
		t.Fatalf("Go install-hook error=%v", err)
	}

	unsafeModePackage := filepath.Join(workspace, "unsafe-sudoers-mode.tar.gz")
	writeTestTarGzip(t, unsafeModePackage, packageEntries("0.1.59-1", productionCLIPhaseT0, "safe-sudoers\n", true, 0o755))
	if err := runtime.verifySignedPackageLayout(context.Background(), workspace, productionAURPackageName, "0.1.59-1", unsafeModePackage, asset, assetSHA256, true); err == nil || !strings.Contains(err.Error(), "mode or type mismatch") {
		t.Fatalf("sudoers.d mode error=%v", err)
	}

	setuidPackage := filepath.Join(workspace, "setuid-package.tar.gz")
	setuidEntries := packageEntries("0.1.59-1", productionCLIPhaseT0, "safe-sudoers\n", true, 0o750)
	for index := range setuidEntries {
		if setuidEntries[index].name == "usr/bin/lmm-api" {
			setuidEntries[index].mode = 0o4755
		}
	}
	writeTestTarGzip(t, setuidPackage, setuidEntries)
	if err := runtime.verifySignedPackageLayout(context.Background(), workspace, productionAURPackageName, "0.1.59-1", setuidPackage, asset, assetSHA256, true); err == nil || !strings.Contains(err.Error(), "unsafe mode") {
		t.Fatalf("setuid package error=%v", err)
	}

	ownerPackage := filepath.Join(workspace, "nonroot-owner-package.tar.gz")
	ownerEntries := packageEntries("0.1.59-1", productionCLIPhaseT0, "safe-sudoers\n", true, 0o750)
	for index := range ownerEntries {
		if ownerEntries[index].name == "usr/bin/lmm-api" {
			ownerEntries[index].uid = 1000
		}
	}
	writeTestTarGzip(t, ownerPackage, ownerEntries)
	if err := runtime.verifySignedPackageLayout(context.Background(), workspace, productionAURPackageName, "0.1.59-1", ownerPackage, asset, assetSHA256, true); err == nil || !strings.Contains(err.Error(), "not root-owned") {
		t.Fatalf("nonroot package error=%v", err)
	}

	nonHardenedEnvPackage := filepath.Join(workspace, "non-hardened-env-package.tar.gz")
	nonHardenedEnvEntries := packageEntries("0.1.59-1", productionCLIPhaseT0, "safe-sudoers\n", true, 0o750)
	for index := range nonHardenedEnvEntries {
		if nonHardenedEnvEntries[index].name == "etc/lmm-api-go/lmm-api-go.env" {
			nonHardenedEnvEntries[index].mode = 0o640
		}
	}
	writeTestTarGzip(t, nonHardenedEnvPackage, nonHardenedEnvEntries)
	if err := runtime.verifySignedPackageLayout(context.Background(), workspace, productionAURPackageName, "0.1.59-1", nonHardenedEnvPackage, asset, assetSHA256, true); err == nil || !strings.Contains(err.Error(), "mode=0640 want=0600") {
		t.Fatalf("environment hardening mode error=%v", err)
	}

	mappedModePackage := filepath.Join(workspace, "mapped-mode-package.tar.gz")
	mappedModeEntries := packageEntries("0.1.59-1", productionCLIPhaseT0, "safe-sudoers\n", true, 0o750)
	for index := range mappedModeEntries {
		if mappedModeEntries[index].name == "usr/bin/lmm-api" {
			mappedModeEntries[index].mode = 0o750
		}
	}
	writeTestTarGzip(t, mappedModePackage, mappedModeEntries)
	if err := runtime.verifySignedPackageLayout(context.Background(), workspace, productionAURPackageName, "0.1.59-1", mappedModePackage, asset, assetSHA256, true); err == nil || !strings.Contains(err.Error(), "mode=0750 want=0755") {
		t.Fatalf("mapped package mode error=%v", err)
	}

	mtreeMismatchPackage := filepath.Join(workspace, "mtree-mismatch-package.tar.gz")
	mtreeMismatchEntries := packageEntries("0.1.59-1", productionCLIPhaseT0, "safe-sudoers\n", true, 0o750)
	for index := range mtreeMismatchEntries {
		if mtreeMismatchEntries[index].name == ".MTREE" {
			mtreeMismatchEntries[index].body = testPackageMtree(t, true, "755")
		}
	}
	writeTestTarGzip(t, mtreeMismatchPackage, mtreeMismatchEntries)
	if err := runtime.verifySignedPackageLayout(context.Background(), workspace, productionAURPackageName, "0.1.59-1", mtreeMismatchPackage, asset, assetSHA256, true); err == nil || !strings.Contains(err.Error(), "disagrees with its archive header") {
		t.Fatalf("mtree/header mismatch error=%v", err)
	}

	unsafeMetadataPackage := filepath.Join(workspace, "unsafe-metadata.tar.gz")
	unsafeMetadataEntries := packageEntries("0.1.59-1", productionCLIPhaseT0, "safe-sudoers\n", true, 0o750)
	for index := range unsafeMetadataEntries {
		if unsafeMetadataEntries[index].name == ".PKGINFO" {
			unsafeMetadataEntries[index].body += "replaces = lmm-api-deploy-bin\n"
		}
	}
	writeTestTarGzip(t, unsafeMetadataPackage, unsafeMetadataEntries)
	if err := runtime.verifySignedPackageLayout(context.Background(), workspace, productionAURPackageName, "0.1.59-1", unsafeMetadataPackage, asset, assetSHA256, true); err == nil || !strings.Contains(err.Error(), "replaces contract mismatch") {
		t.Fatalf("T0 package metadata error=%v", err)
	}

	legacyPackage := filepath.Join(workspace, "legacy-package.tar.gz")
	legacyEntries := []testTarEntry{
		{name: ".PKGINFO", body: testProductionPackageInfo(t, productionAURPackageName, "0.1.57-1", productionCLIPhaseT0), mode: 0o644},
		{name: ".MTREE", body: testPackageMtree(t, false), mode: 0o644},
		{name: "usr/bin/lmm-api-go", body: "binary", mode: 0o755},
		{name: "usr/bin/lmm-api", mode: 0o777, linkTo: "lmm-api-go"},
		{name: "etc/lmm-api-go/lmm-api-go.env", body: "safe-env\n", mode: 0o600},
		{name: "etc/sudoers.d/lmm-api-operator", body: "safe-sudoers\n", mode: 0o440},
		{name: "usr/share/licenses/lmm-api-go-bin/LICENSE", body: "license\n", mode: 0o644},
	}
	legacyEntries = append(legacyEntries, testEdgePolicyTarEntries("usr/share/lmm-api-go/edge-policy/")...)
	writeTestTarGzip(t, legacyPackage, legacyEntries)
	if err := runtime.verifySignedPackageLayout(context.Background(), workspace, productionAURPackageName, "0.1.57-1", legacyPackage, asset, assetSHA256, false); err != nil {
		t.Fatalf("pre-T0 compatibility-link error=%v", err)
	}
}

func TestVerifySignedPackageLayoutUsesSignedExplicitT0PhaseForNewRelease(t *testing.T) {
	workspace := t.TempDir()
	asset := filepath.Join(workspace, "release-0.1.63.tar.gz")
	releasePrefix := "lmm-api-go-0.1.63-linux-amd64/"
	releaseEntries := []testTarEntry{
		{name: releasePrefix + "lmm-api", body: "binary", mode: 0o755},
		{name: releasePrefix + "lmm-api-go.env", body: "safe-env\n", mode: 0o640},
		{name: releasePrefix + "lmm-api-operator.sudoers", body: "safe-sudoers\n", mode: 0o644},
		{name: releasePrefix + "CLI_TRANSITION_PHASE", body: "t0\n", mode: 0o644},
	}
	releaseEntries = append(releaseEntries, testEdgePolicyTarEntries(releasePrefix+"edge-policy/")...)
	writeTestTarGzip(t, asset, releaseEntries)
	assetSHA256, err := sha256File(asset)
	if err != nil {
		t.Fatal(err)
	}
	packageEntries := []testTarEntry{
		{name: ".PKGINFO", body: testProductionPackageInfo(t, productionAURPackageName, "0.1.63-1", productionCLIPhaseT0), mode: 0o644},
		{name: ".MTREE", body: testPackageMtree(t, true), mode: 0o644},
		{name: "usr/bin/lmm-api", body: "binary", mode: 0o755},
		{name: "usr/bin/lmm-api-go", mode: 0o777, linkTo: "lmm-api"},
		{name: "etc/lmm-api-go/lmm-api-go.env", body: "safe-env\n", mode: 0o600},
		{name: "etc/sudoers.d/", mode: 0o750, directory: true},
		{name: "etc/sudoers.d/lmm-api-operator", body: "safe-sudoers\n", mode: 0o440},
		{name: "usr/share/doc/lmm-api-go-bin/CLI_TRANSITION_PHASE", body: "t0\n", mode: 0o644},
		{name: "usr/share/doc/lmm-api-go-bin/RELEASE_ASSET_SHA256", body: assetSHA256 + "\n", mode: 0o644},
	}
	packageEntries = append(packageEntries, testEdgePolicyTarEntries("usr/share/lmm-api-go/edge-policy/")...)
	packagePath := filepath.Join(workspace, "package-0.1.63.tar.gz")
	writeTestTarGzip(t, packagePath, packageEntries)
	runtime := &productionReleaseRuntime{runner: osProductionCommandRunner{}}
	if err := runtime.verifySignedPackageLayout(context.Background(), workspace, productionAURPackageName, "0.1.63-1", packagePath, asset, assetSHA256, true); err != nil {
		t.Fatal(err)
	}

	missingPhaseAsset := filepath.Join(workspace, "release-missing-phase.tar.gz")
	missingPhaseEntries := []testTarEntry{{name: releasePrefix + "lmm-api", body: "binary", mode: 0o755}}
	missingPhaseEntries = append(missingPhaseEntries, testEdgePolicyTarEntries(releasePrefix+"edge-policy/")...)
	writeTestTarGzip(t, missingPhaseAsset, missingPhaseEntries)
	missingPhaseSHA256, err := sha256File(missingPhaseAsset)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.verifySignedPackageLayout(context.Background(), workspace, productionAURPackageName, "0.1.63-1", packagePath, missingPhaseAsset, missingPhaseSHA256, true); err == nil || !strings.Contains(err.Error(), "CLI_TRANSITION_PHASE") {
		t.Fatalf("missing explicit phase error=%v", err)
	}
}

func TestVerifySignedWebPackageLayoutMigratesActivationIntoBackendCLI(t *testing.T) {
	installHook, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "packaging", "aur", "lmm-api-web-bin", "lmm-api-web.install"))
	if err != nil {
		t.Fatal(err)
	}
	legacyInstallHook := []byte("post_install() {\n  /usr/lib/lmm-api-web/lmm-api-web-activate \"$1\"\n}\n\npost_upgrade() {\n  /usr/lib/lmm-api-web/lmm-api-web-activate \"$1\"\n}\n\npost_remove() {\n  printf '%s\\n' 'The active LMM frontend release was retained for safe rollback.'\n}\n")
	if digest := sha256.Sum256(legacyInstallHook); hex.EncodeToString(digest[:]) != productionLegacyWebInstallSHA256 {
		t.Fatal("legacy Web install-hook fixture no longer matches the pinned digest")
	}
	verify := func(version string, signedHook bool, packageHook []byte, legacyPublisher bool, wantError string) {
		t.Helper()
		caseRoot := t.TempDir()
		asset := filepath.Join(caseRoot, "web-"+version+".tar.gz")
		assetEntries := []testTarEntry{{name: "dist/index.html", body: "<!doctype html>\n", mode: 0o644}}
		packageEntries := []testTarEntry{
			{name: ".PKGINFO", body: testProductionPackageInfo(t, productionWebPackageName, version+"-1", ""), mode: 0o644},
			{name: ".INSTALL", body: string(packageHook), mode: 0o644},
			{name: "usr/share/lmm-api-web/frontend-dist/index.html", body: "<!doctype html>\n", mode: 0o644},
		}
		if legacyPublisher {
			assetEntries = append(assetEntries,
				testTarEntry{name: "lmm-api-web-activate", body: "#!/bin/sh\nexit 0\n", mode: 0o755},
				testTarEntry{name: "frontend-release.sh", body: "#!/bin/sh\nexit 0\n", mode: 0o755},
			)
			packageEntries = append(packageEntries,
				testTarEntry{name: "usr/lib/lmm-api-web/lmm-api-web-activate", body: "#!/bin/sh\nexit 0\n", mode: 0o755},
				testTarEntry{name: "usr/lib/lmm-api-web/frontend-release.sh", body: "#!/bin/sh\nexit 0\n", mode: 0o755},
			)
		}
		if signedHook {
			assetEntries = append(assetEntries, testTarEntry{name: "lmm-api-web.install", body: string(installHook), mode: 0o644})
		}
		writeTestTarGzip(t, asset, assetEntries)
		assetSHA256, err := sha256File(asset)
		if err != nil {
			t.Fatal(err)
		}
		packageEntries = append(packageEntries, testTarEntry{name: "usr/share/doc/lmm-api-web-bin/RELEASE_ASSET_SHA256", body: assetSHA256 + "\n", mode: 0o644})
		packagePath := filepath.Join(caseRoot, "web-package-"+version+".tar.gz")
		writeTestTarGzip(t, packagePath, packageEntries)
		runtime := &productionReleaseRuntime{runner: osProductionCommandRunner{}}
		err = runtime.verifySignedPackageLayout(context.Background(), caseRoot, productionWebPackageName, version+"-1", packagePath, asset, assetSHA256, false)
		if wantError == "" {
			if err != nil {
				t.Fatal(err)
			}
			return
		}
		if err == nil || !strings.Contains(err.Error(), wantError) {
			t.Fatalf("version=%s error=%v want %q", version, err, wantError)
		}
	}
	verify("0.1.42", false, legacyInstallHook, true, "")
	verify("0.1.42", false, []byte("#!/bin/sh\nexit 1\n"), true, "install hook")
	verify("0.1.52", true, installHook, false, "")
	verify("0.1.52", false, installHook, false, "lacks lmm-api-web.install")
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
