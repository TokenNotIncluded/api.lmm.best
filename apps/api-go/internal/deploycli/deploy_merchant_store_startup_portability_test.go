package deploycli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Only package extraction is real in this fixture. Rejection at the required
// signature step is deliberately injected; it is not official artifact proof.
type startupPortabilityRunner struct {
	pacmanCalls int
	cosignCalls int
	cosignArgs  []string
}

func (r *startupPortabilityRunner) Run(ctx context.Context, c productionCommand) ([]byte, error) {
	if c.Name == commandPacman {
		r.pacmanCalls++
		return nil, errors.New("fixture has no pacman")
	}
	if c.Name == commandCosign {
		r.cosignCalls++
		r.cosignArgs = append([]string(nil), c.Args...)
		return nil, errors.New("signature fixture refuses verification")
	}
	return (osProductionCommandRunner{}).Run(ctx, c)
}

func startupPortabilityPackage(t *testing.T, info testTarEntry, extra ...testTarEntry) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "owned-package.tar.gz")
	entries := []testTarEntry{
		info,
		{name: "usr/share/doc/lmm-api-go-bin/REVISION", body: strings.Repeat("a", 40) + "\n", mode: 0644},
		{name: "usr/share/doc/lmm-api-go-bin/API_ROUTE_CONTRACT_REVISION", body: strings.Repeat("b", 64) + "\n", mode: 0644},
		{name: "usr/share/doc/lmm-api-go-bin/MERCHANT_STORE_WRITER_CAPABILITY", body: "5\n", mode: 0644},
		{name: "usr/bin/lmm-api-go", body: "owned metadata fixture, not an official ELF", mode: 0755},
	}
	writeTestTarGzip(t, p, append(entries, extra...))
	return p
}

func TestStartupBaselinePortableMetadataUsesActualArchiveWithoutPacman(t *testing.T) {
	info := testTarEntry{name: ".PKGINFO", body: testProductionPackageInfo(t, productionAURPackageName, "0.2.88-1"), mode: 0644}
	p := startupPortabilityPackage(t, info)
	runner := &startupPortabilityRunner{}
	runtime := productionRuntime{runner: runner}
	m, err := runtime.startupBaselinePackageMetadata(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	if runner.pacmanCalls != 0 || m.Name != productionAURPackageName || m.Version != "0.2.88-1" || m.MerchantStoreWriterCapability != 5 || m.BinarySHA256 != startupContentSHA256([]byte("owned metadata fixture, not an official ELF")) {
		t.Fatalf("portable archive metadata differs: %#v, pacman=%d", m, runner.pacmanCalls)
	}
}

func TestStartupBaselinePortableMetadataRejectsAmbiguousAndUnsafeArchiveIdentity(t *testing.T) {
	base := testProductionPackageInfo(t, productionAURPackageName, "0.2.88-1")
	tests := []struct {
		name  string
		info  testTarEntry
		extra []testTarEntry
	}{
		{name: "duplicate name", info: testTarEntry{name: ".PKGINFO", body: base + "pkgname = lmm-api-go-bin\n", mode: 0644}},
		{name: "duplicate version", info: testTarEntry{name: ".PKGINFO", body: base + "pkgver = 0.2.88-1\n", mode: 0644}},
		{name: "different provider", info: testTarEntry{name: ".PKGINFO", body: strings.ReplaceAll(base, "lmm-api-go-bin", "lmm-api-web-bin"), mode: 0644}},
		{name: "missing release", info: testTarEntry{name: ".PKGINFO", body: strings.Replace(base, "0.2.88-1", "0.2.88", 1), mode: 0644}},
		{name: "nul", info: testTarEntry{name: ".PKGINFO", body: base + "\x00", mode: 0644}},
		{name: "malformed field", info: testTarEntry{name: ".PKGINFO", body: base + "pkgver=0.2.88-1\n", mode: 0644}},
		{name: "writable", info: testTarEntry{name: ".PKGINFO", body: base, mode: 0664}},
		{name: "different owner", info: testTarEntry{name: ".PKGINFO", body: base, mode: 0644, uid: 1000}},
		{name: "different group", info: testTarEntry{name: ".PKGINFO", body: base, mode: 0644, gid: 1000}},
		{name: "directory", info: testTarEntry{name: ".PKGINFO", mode: 0644, directory: true}},
		{name: "symlink", info: testTarEntry{name: ".PKGINFO", mode: 0644, linkTo: "usr/bin/lmm-api-go"}},
		{name: "duplicate member", info: testTarEntry{name: ".PKGINFO", body: base, mode: 0644}, extra: []testTarEntry{{name: "./.PKGINFO", body: base, mode: 0644}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			runner := &startupPortabilityRunner{}
			r := productionRuntime{runner: runner}
			if _, err := r.startupBaselinePackageMetadata(context.Background(), startupPortabilityPackage(t, tc.info, tc.extra...)); err == nil {
				t.Fatal("unsafe or ambiguous package identity accepted")
			}
			if runner.pacmanCalls != 0 {
				t.Fatal("portable metadata tried an OS fallback")
			}
		})
	}
}

func TestStartupBaselinePortableEvidenceRequiresSameOfficialSignatureAndOrdinaryPacman(t *testing.T) {
	p := startupPortabilityPackage(t, testTarEntry{name: ".PKGINFO", body: testProductionPackageInfo(t, productionAURPackageName, "0.2.88-1"), mode: 0644})
	root := t.TempDir()
	asset, bundle := filepath.Join(root, "release.tar.gz"), filepath.Join(root, "signature.json")
	for _, path := range []string{asset, bundle} {
		if err := os.WriteFile(path, []byte("owned rejected signature fixture"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	runner := &startupPortabilityRunner{}
	r := productionRuntime{runner: runner}
	release := productionReleaseRuntime{runner: runner}
	if _, err := release.verifyStartupBaselinePackageEvidence(context.Background(), root, root, &r, p, asset, bundle); err == nil || !strings.Contains(err.Error(), "Sigstore") {
		t.Fatalf("portable evidence bypassed mandatory signature: %v", err)
	}
	args := strings.Join(runner.cosignArgs, " ")
	if runner.pacmanCalls != 0 || runner.cosignCalls != 1 || !strings.Contains(args, "release-go.yml@refs/tags/go-v0.2.88") || !strings.Contains(args, "--certificate-oidc-issuer https://token.actions.githubusercontent.com") || strings.Contains(args, "insecure") {
		t.Fatal("portable signature identity changed")
	}
	if _, err := release.verifyPackageEvidence(context.Background(), root, root, &r, productionAURPackageName, p, asset, bundle, false); err == nil || !strings.Contains(err.Error(), "query package identity") {
		t.Fatalf("ordinary evidence used portable fallback after pacman failure: %v", err)
	}
	if runner.pacmanCalls != 1 || runner.cosignCalls != 1 {
		t.Fatal("ordinary evidence did not fail before signature at real pacman query")
	}
}

func TestProductionDefaultRunnerExecutesFixedNativeToolsAndRejectsUnknownNames(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{name: "/usr/bin/curl", args: []string{"--version"}},
		{name: "/usr/bin/systemd-run", args: []string{"--version"}},
		{name: "systemd-run", args: []string{"--version"}},
		{name: commandCosign, args: []string{"version"}},
		{name: commandNginx, args: []string{"-v"}},
		{name: commandRunuser, args: []string{"--version"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, handled, err := productionSystemToolPath(tc.name)
			if err != nil || !handled || !strings.HasPrefix(p, "/usr/") {
				t.Fatalf("fixed system tool is unavailable: %s %v", p, err)
			}
			if _, err := (osProductionCommandRunner{}).Run(context.Background(), productionCommand{Name: tc.name, Args: tc.args, Env: []string{"PATH=" + t.TempDir()}}); err != nil {
				t.Fatal(err)
			}
		})
	}
	for _, name := range []string{"curl", "/tmp/curl", "/usr/local/bin/curl", "/usr/sbin/systemd-run", "sh", "/bin/sh"} {
		if _, err := (osProductionCommandRunner{}).Run(context.Background(), productionCommand{Name: name, Args: []string{"--version"}}); err == nil || !strings.Contains(err.Error(), "not allowlisted") {
			t.Fatalf("unknown executable accepted: %s %v", name, err)
		}
	}
}

func TestProductionSystemToolTargetsCannotChangeExecutableIdentity(t *testing.T) {
	allowed := []string{"/usr/bin/nginx", "/usr/sbin/nginx"}
	if !productionSystemToolTargetAllowed("/usr/sbin/nginx", allowed) {
		t.Fatal("canonical distro alias rejected")
	}
	for _, target := range []string{"/usr/bin/true", "/usr/local/bin/nginx", "/usr/bin/../sbin/nginx", "/tmp/nginx"} {
		if productionSystemToolTargetAllowed(target, allowed) {
			t.Fatal("symlink changed executable identity", target)
		}
	}
}
