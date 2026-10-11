package deploycli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/internal/deploymentfence"
)

func testSchemaMaintenanceOwner() productionMerchantStoreFenceOwner {
	return productionMerchantStoreFenceOwner{Format: 1, State: "ACTIVE", DeploymentID: "schema-owner-test", Host: "arch-dmit", PlanSHA256: strings.Repeat("a", 64), ContractSHA256: strings.Repeat("b", 64), ProviderSHA256: strings.Repeat("c", 64), Nonce: strings.Repeat("d", 32), HolderPID: 123, HolderUnit: schemaMaintenanceHolderUnit("schema-owner-test"), HolderInvocationID: strings.Repeat("e", 32), BackendPID: 321, SystemIdentifier: "7648633982160478129", Database: "postgres", DatabaseOID: 1, Schema: "merchant_fence", SchemaOID: 2, Role: "lmm_fence_business", Purpose: "schema-maintenance", Service: "lmm-api.service"}
}

func TestSchemaMaintenanceOwnerPurposeAndWorkerWireAreExact(t *testing.T) {
	owner := testSchemaMaintenanceOwner()
	raw, err := canonicalMerchantStoreFenceOwner(owner)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := deploymentfence.ParseSchemaMaintenanceOwner(raw)
	if err != nil || actual != owner {
		t.Fatalf("native worker wire mismatch: %v", err)
	}
	for name, change := range map[string]func(*productionMerchantStoreFenceOwner){
		"ordinary holder":  func(o *productionMerchantStoreFenceOwner) { o.HolderUnit = merchantStoreFenceUnit(o.DeploymentID) },
		"portable holder":  func(o *productionMerchantStoreFenceOwner) { o.HolderUnit = merchantStorePortableUnit(o.DeploymentID) },
		"start invocation": func(o *productionMerchantStoreFenceOwner) { o.StartInvocationID = strings.Repeat("f", 32) },
		"wrong service":    func(o *productionMerchantStoreFenceOwner) { o.Service = "other.service" },
		"unknown purpose":  func(o *productionMerchantStoreFenceOwner) { o.Purpose = "schema-maintenance-approved" },
	} {
		t.Run(name, func(t *testing.T) {
			o := owner
			change(&o)
			if validateMerchantStoreFenceOwner(o) == nil {
				t.Fatal("invalid schema purpose accepted")
			}
		})
	}
	for _, bad := range [][]byte{append([]byte(" "), raw...), append(append([]byte{}, raw...), '\n')} {
		if _, err := deploymentfence.ParseSchemaMaintenanceOwner(bad); err == nil {
			t.Fatal("worker accepted nonexact owner bytes")
		}
	}
}

func TestSchemaMaintenanceOwnerReceiptPreservesExactCompactBytes(t *testing.T) {
	root := t.TempDir()
	state := filepath.Join(root, "state")
	if err := os.Mkdir(state, 0700); err != nil {
		t.Fatal(err)
	}
	r := productionRuntime{requiredOwnerUID: uint32(os.Geteuid())}
	raw, err := canonicalMerchantStoreFenceOwner(testSchemaMaintenanceOwner())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(state, "schema-owner.json")
	if err := r.writeSchemaMaintenanceRawNewFile(path, raw); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, raw) {
		t.Fatal("receipt changed exact DB owner wire")
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("receipt lost private permissions")
	}
	if err := r.writeSchemaMaintenanceRawNewFile(path, []byte("replacement")); err == nil {
		t.Fatal("owner receipt was overwritten")
	}
	if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, raw) {
		t.Fatal("retry changed original owner receipt")
	}
}

func TestSchemaMaintenanceOwnerMissingAuthorityCannotClaim(t *testing.T) {
	ctx := context.Background()
	r := productionRuntime{effectiveUID: func() int { return 0 }, hostname: func() (string, error) { return "arch-dmit", nil }, paths: defaultProductionPaths()}
	if _, err := r.claimSchemaMaintenanceFence(ctx, productionSchemaMaintenanceAuthority{}, nil); err == nil {
		t.Fatal("empty authority claimed schema maintenance")
	}
	if (*productionSchemaMaintenanceFence)(nil).Check(ctx) == nil {
		t.Fatal("nil fence checked as live")
	}
	if err := (*productionSchemaMaintenanceFence)(nil).Close(); err != nil {
		t.Fatal(err)
	}
}
