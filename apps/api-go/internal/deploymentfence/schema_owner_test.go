package deploymentfence

import (
	"encoding/json"
	"strings"
	"testing"
)

func schemaOwnerFixture() OwnerBinding {
	return OwnerBinding{Format: 1, State: "ACTIVE", DeploymentID: "schema-test", Host: "arch-dmit",
		PlanSHA256: strings.Repeat("a", 64), ContractSHA256: strings.Repeat("b", 64), ProviderSHA256: strings.Repeat("c", 64),
		Nonce: strings.Repeat("d", 32), HolderPID: 123, HolderUnit: "lmm-schema-maintenance-schema-test.service",
		HolderInvocationID: strings.Repeat("e", 32), BackendPID: 456, SystemIdentifier: "7648633982160478129",
		Database: "test_db", DatabaseOID: 10, Schema: "app", SchemaOID: 20, Role: "test_role", Purpose: "schema-maintenance", Service: "lmm-api.service"}
}

func TestSchemaOwnerRequiresExactNativeCanonicalPurposeAndIdentity(t *testing.T) {
	good := schemaOwnerFixture()
	body, err := json.Marshal(good)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := ParseSchemaMaintenanceOwner(body)
	if err != nil || actual != good {
		t.Fatalf("canonical native owner rejected: %v", err)
	}
	for name, edit := range map[string]func(*OwnerBinding){
		"ordinary deploy": func(o *OwnerBinding) { o.Purpose = "portable-deploy" },
		"start owner":     func(o *OwnerBinding) { o.Purpose = "start" },
		"stale owner":     func(o *OwnerBinding) { o.State = "RELEASED" },
		"other unit":      func(o *OwnerBinding) { o.HolderUnit = "lmm-api.service" },
		"old invocation":  func(o *OwnerBinding) { o.StartInvocationID = strings.Repeat("1", 32) },
		"invalid cluster": func(o *OwnerBinding) { o.SystemIdentifier = "07648633982160478129" },
		"unsafe schema":   func(o *OwnerBinding) { o.Schema = "app;select" },
		"system schema":   func(o *OwnerBinding) { o.Schema = "pg_catalog" },
		"role control":    func(o *OwnerBinding) { o.Role = "role\n" },
		"no PG holder":    func(o *OwnerBinding) { o.BackendPID = 0 },
		"no holder":       func(o *OwnerBinding) { o.HolderPID = 0 },
	} {
		t.Run(name, func(t *testing.T) {
			bad := good
			edit(&bad)
			body, _ := json.Marshal(bad)
			if _, err := ParseSchemaMaintenanceOwner(body); err == nil {
				t.Fatal("unsafe schema owner accepted")
			}
		})
	}
	for _, bad := range [][]byte{
		append(append([]byte(nil), body...), '\n'),
		[]byte(strings.Replace(string(body), `"format":1`, `"format":1,"format":1`, 1)),
		[]byte(strings.Replace(string(body), `"format":1`, `"Format":1`, 1)),
		[]byte(strings.Replace(string(body), `"format":1`, `"unknown":1,"format":1`, 1)),
	} {
		if _, err := ParseSchemaMaintenanceOwner(bad); err == nil {
			t.Fatal("noncanonical or ambiguous schema owner accepted")
		}
	}
}

func TestSchemaOwnerReadRejectsInvalidDigestBeforeOpening(t *testing.T) {
	for _, digest := range []string{"", "a", strings.Repeat("A", 64)} {
		if _, _, err := ReadSchemaMaintenanceOwner("/not-a-file", digest); err == nil {
			t.Fatal("invalid owner digest accepted")
		}
	}
}
