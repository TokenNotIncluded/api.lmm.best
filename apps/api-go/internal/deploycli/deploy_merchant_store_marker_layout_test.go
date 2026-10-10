package deploycli

import (
	"context"
	"path/filepath"
	"testing"
)

func TestMerchantStoreSignedMarkerPackageContractPreservesExactCanonicalBytes(t *testing.T) {
	for _, scenario := range []struct {
		name, signed, packaged string
		signedMode             int64
		omit, duplicate        bool
		wantPass               bool
	}{
		{name: "exact", signed: "4\n", packaged: "4\n", wantPass: true},
		{name: "refund sync capability", signed: "8\n", packaged: "8\n", wantPass: true},
		{name: "unsigned change", signed: "4\n", packaged: "3\n"},
		{name: "missing package member", signed: "4\n", omit: true},
		{name: "duplicate package member", signed: "4\n", packaged: "4\n", duplicate: true},
		{name: "missing newline", signed: "4", packaged: "4"},
		{name: "unknown capability", signed: "9\n", packaged: "9\n"},
		{name: "extra newline", signed: "4\n\n", packaged: "4\n\n"},
		{name: "null byte", signed: "4\x00", packaged: "4\x00"},
		{name: "signed missing newline", signed: "4", packaged: "4\n"},
		{name: "signed unknown capability", signed: "9\n", packaged: "4\n"},
		{name: "signed noncanonical mode", signed: "4\n", packaged: "4\n", signedMode: 0640},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			workspace := t.TempDir()
			asset := filepath.Join(workspace, "signed-fixture.tar.gz")
			prefix := "lmm-api-go-0.2.0-linux-amd64/"
			signedMode := int64(0644)
			if scenario.signedMode != 0 {
				signedMode = scenario.signedMode
			}
			entries := []testTarEntry{
				{name: prefix + "lmm-api-go", body: "provider-fixture", mode: 0755},
				{name: prefix + "lmm-api-go.env", body: "safe-env\n", mode: 0640},
				{name: prefix + "lmm-api-operator.sudoers", body: "safe-sudoers\n", mode: 0644},
				{name: prefix + "LICENSE", body: "license\n", mode: 0644},
				{name: prefix + merchantStoreCapabilityMember, body: scenario.signed, mode: signedMode},
			}
			writeTestTarGzip(t, asset, entries)
			assetSHA, err := sha256File(asset)
			if err != nil {
				t.Fatal(err)
			}
			packageEntries := []testTarEntry{
				{name: ".PKGINFO", body: testProductionPackageInfo(t, productionAURPackageName, "0.2.0-1"), mode: 0644},
				{name: ".MTREE", body: testPackageMtree(t, true), mode: 0644},
				{name: "usr/bin/lmm-api-go", body: "provider-fixture", mode: 0755},
				{name: "etc/lmm-api-go/lmm-api-go.env", body: "safe-env\n", mode: 0600},
				{name: "etc/sudoers.d/", mode: 0750, directory: true},
				{name: "etc/sudoers.d/lmm-api-operator", body: "safe-sudoers\n", mode: 0440},
				{name: "usr/share/licenses/" + productionAURPackageName + "/LICENSE", body: "license\n", mode: 0644},
				{name: "usr/share/doc/" + productionAURPackageName + "/RELEASE_ASSET_SHA256", body: assetSHA + "\n", mode: 0644},
			}
			marker := testTarEntry{name: "usr/share/doc/" + productionAURPackageName + "/" + merchantStoreCapabilityMember, body: scenario.packaged, mode: 0644}
			if !scenario.omit {
				packageEntries = append(packageEntries, marker)
			}
			if scenario.duplicate {
				packageEntries = append(packageEntries, marker)
			}
			pkg := filepath.Join(workspace, "package-fixture.tar.gz")
			writeTestTarGzip(t, pkg, packageEntries)
			runtime := productionReleaseRuntime{runner: osProductionCommandRunner{}}
			// Native qualification uses the raw inventory and signed layout
			// together. A tar-to-mtree conversion alone folds duplicate names.
			inventory := productionRuntime{runner: runtime.runner}
			_, err = inventory.merchantStorePackageCapability(context.Background(), pkg, productionAURPackageName)
			if err == nil {
				err = runtime.verifySignedPackageLayout(context.Background(), workspace, productionAURPackageName, "0.2.0-1", pkg, asset, assetSHA, false)
			}
			if (err == nil) != scenario.wantPass {
				t.Fatalf("signed fixture layout pass=%v error=%v", scenario.wantPass, err)
			}
		})
	}
}
