//go:build linux

package deploycli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// SSH sends remote arguments through a shell. Exercise that shell boundary;
// a runner that merely compares argument slices misses the split stat format.
type engineStatShellRunner struct{ t *testing.T }

func (r engineStatShellRunner) Run(_ context.Context, c productionCommand) ([]byte, error) {
	r.t.Helper()
	if c.Name != commandSSH || len(c.Args) < 4 {
		r.t.Fatalf("unexpected command: %+v", c)
	}
	remote := c.Args[3:]
	body, err := exec.Command("/bin/sh", "-c", strings.Join(remote, " ")).CombinedOutput()
	if err == nil && remote[0] == "stat" {
		fields := strings.Fields(string(body))
		if len(fields) != 5 || strings.Join(fields[:2], " ") != "regular file" {
			r.t.Fatalf("stat format did not survive the remote shell: %q", body)
		}
		// Ownership itself is fixture data; type/link/mode and quoting above
		// are read from the real local file without a root-only test setup.
		fields[2] = "0"
		body = []byte(strings.Join(fields, " ") + "\n")
	}
	return body, err
}

func TestSplitEngineRemoteStatSurvivesShell(t *testing.T) {
	path := filepath.Join(t.TempDir(), "engine")
	if err := os.WriteFile(path, []byte("signed tool fixture"), 0700); err != nil {
		t.Fatal(err)
	}
	rt := &productionReleaseRuntime{runner: engineStatShellRunner{t}}
	if err := rt.verifyRemoteDeployEngine(context.Background(), "ArchDmit", path, startupContentSHA256([]byte("signed tool fixture"))); err != nil {
		t.Fatal(err)
	}
}

// The unit inventory is a fixture, but the holder executable is the actual
// current process. An API digest must not stand in for a separate tool digest.
func TestSplitEnginePortableOwnerKeepsWriterIdentity(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "state"), 0700); err != nil {
		t.Fatal(err)
	}
	engine, err := sha256File("/proc/self/exe")
	if err != nil {
		t.Fatal(err)
	}
	w := testMerchantWriterContract()
	w.Candidate.PayloadSHA256 = strings.Repeat("8", 64)
	c := productionMerchantStoreCapsule{DeploymentID: "split-owner-test", Root: root, Host: "arch-dmit", Service: "lmm-api.service", Writer: w, Candidate: productionReleasePackagePlan{DeployEngineSHA256: engine}}
	digest := strings.Repeat("a", 64)
	o := productionMerchantStoreFenceOwner{Format: 1, State: "ACTIVE", DeploymentID: c.DeploymentID, Host: c.Host, PlanSHA256: digest, ContractSHA256: merchantStoreFenceContractSHA(w), ProviderSHA256: w.Candidate.PayloadSHA256, Nonce: strings.Repeat("b", 32), HolderPID: os.Getpid(), HolderUnit: merchantStorePortableUnit(c.DeploymentID), HolderInvocationID: strings.Repeat("c", 32), BackendPID: 123, SystemIdentifier: w.SystemIdentifier, Database: w.Database, DatabaseOID: w.DatabaseOID, Schema: w.Schema, SchemaOID: w.SchemaOID, Role: w.Role, Purpose: "portable-deploy", Service: c.Service}
	body, err := canonicalMerchantStoreFenceOwner(o)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(merchantStorePortableOwnerPath(c, ""), body, 0600); err != nil {
		t.Fatal(err)
	}
	rt := &productionRuntime{runner: &merchantStoreHolderUnitFixture{owner: o}, requiredOwnerUID: uint32(os.Getuid())}
	if _, _, err := rt.portableOwner(context.Background(), c, digest, ""); err != nil {
		t.Fatal(err)
	}
	c.Candidate.DeployEngineSHA256 = w.Candidate.PayloadSHA256
	if _, _, err := rt.portableOwner(context.Background(), c, digest, ""); err == nil {
		t.Fatal("API executable digest authorized the tool process")
	}
}
