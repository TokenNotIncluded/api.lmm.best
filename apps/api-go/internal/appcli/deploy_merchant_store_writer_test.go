package appcli

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func merchantWriterStatusJSON(floor, capability int) []byte {
	body, _ := json.Marshal(productionMerchantStoreWriterStatus{RequiredCapability: floor, WriterCapability: capability,
		NewWritesAllowed: capability >= floor, SupportsWriterGate: true, SupportsVariants: capability >= 2})
	return append(body, '\n')
}

func TestProductionMerchantStoreWriterStatusStrictProtocol(t *testing.T) {
	for floor := 1; floor <= 6; floor++ {
		for capability := 1; capability <= 6; capability++ {
			status, err := parseMerchantStoreWriterStatus(merchantWriterStatusJSON(floor, capability))
			if err != nil || status.RequiredCapability != floor || status.WriterCapability != capability {
				t.Fatalf("floor=%d capability=%d: status=%+v error=%v", floor, capability, status, err)
			}
		}
	}
	good := string(merchantWriterStatusJSON(4, 4))
	cases := map[string]string{
		"empty": "", "array": "[]", "null": "null", "trailing": good + "{}",
		"missing":               strings.Replace(good, `,"supports_variants":true`, "", 1),
		"unknown":               strings.Replace(good, "}", `,"supports_refunds":true}`, 1),
		"duplicate":             strings.Replace(good, "}", `,"writer_capability":4}`, 1),
		"escaped duplicate":     strings.Replace(good, "}", `,"writer_\u0063apability":4}`, 1),
		"null value":            strings.Replace(good, `"new_writes_allowed":true`, `"new_writes_allowed":null`, 1),
		"wrong type":            strings.Replace(good, `"writer_capability":4`, `"writer_capability":"4"`, 1),
		"fraction":              strings.Replace(good, `"writer_capability":4`, `"writer_capability":4.0`, 1),
		"cap zero":              strings.Replace(good, `"writer_capability":4`, `"writer_capability":0`, 1),
		"future cap":            strings.Replace(good, `"writer_capability":4`, `"writer_capability":7`, 1),
		"future floor":          strings.Replace(good, `"required_capability":4`, `"required_capability":7`, 1),
		"missing gate":          strings.Replace(good, `"supports_writer_gate":true`, `"supports_writer_gate":false`, 1),
		"variant contradiction": strings.Replace(good, `"supports_variants":true`, `"supports_variants":false`, 1),
		"write contradiction":   strings.Replace(good, `"new_writes_allowed":true`, `"new_writes_allowed":false`, 1),
		"oversize":              strings.Repeat(" ", 8193) + good,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := parseMerchantStoreWriterStatus([]byte(body)); err == nil {
				t.Fatalf("accepted ambiguous/unsupported status %s", name)
			}
		})
	}
}

func TestProductionMerchantStoreWriterTargetsNeverAllowLegacyOrBelowFloor(t *testing.T) {
	for floor := 0; floor <= 6; floor++ {
		for capability := 0; capability <= 6; capability++ {
			err := merchantStoreWriterTargetAllowed(floor, capability)
			wantAllowed := validMerchantStoreCapability(floor) && validMerchantStoreCapability(capability) && capability >= floor
			if (err == nil) != wantAllowed {
				t.Fatalf("floor=%d capability=%d allowed=%t: %v", floor, capability, err == nil, err)
			}
		}
	}
	// A valid frozen status is diagnostic evidence, not a callback/readiness
	// authorization. In particular a cap3 refund callback cannot run at floor4.
	for _, pair := range [][2]int{{2, 1}, {3, 1}, {3, 2}, {4, 1}, {4, 2}, {4, 3}} {
		if _, err := parseMerchantStoreWriterStatus(merchantWriterStatusJSON(pair[0], pair[1])); err != nil {
			t.Fatal(err)
		}
		if merchantStoreWriterTargetAllowed(pair[0], pair[1]) == nil {
			t.Fatalf("frozen-only diagnostic bypassed callback capability floor: %v", pair)
		}
	}
}

type merchantCapabilityInventoryRunner struct {
	listing []byte
	marker  []byte
	listErr error
	readErr error
	reads   int
}

func (runner *merchantCapabilityInventoryRunner) Run(_ context.Context, command productionCommand) ([]byte, error) {
	if command.Name != commandBsdtar {
		return nil, errors.New("unexpected command")
	}
	if command.Args[0] == "-tf" {
		return runner.listing, runner.listErr
	}
	runner.reads++
	return runner.marker, runner.readErr
}

func TestProductionMerchantStoreSignedMarkerInventory(t *testing.T) {
	member := "usr/share/doc/" + productionAURPackageName + "/" + merchantStoreCapabilityMember
	for _, capability := range []string{"1\n", "2\n", "3\n", "4\n", "5\n", "6\n"} {
		runner := &merchantCapabilityInventoryRunner{listing: []byte("./" + member + "\n"), marker: []byte(capability)}
		runtime := productionRuntime{runner: runner}
		got, err := runtime.merchantStorePackageCapability(context.Background(), "/safe/package", productionAURPackageName)
		if err != nil || got != int(capability[0]-'0') || runner.reads != 1 {
			t.Fatalf("marker=%q cap=%d error=%v", capability, got, err)
		}
	}
	for _, name := range []string{"absent", "duplicate", "malformed", "unreadable", "unavailable inventory"} {
		t.Run(name, func(t *testing.T) {
			runner := &merchantCapabilityInventoryRunner{listing: []byte(member + "\n"), marker: []byte("4\n")}
			switch name {
			case "absent":
				runner.listing = []byte("usr/bin/lmm-api-go\n")
			case "duplicate":
				runner.listing = append(runner.listing, []byte("./"+member+"\n")...)
			case "malformed":
				runner.marker = []byte("4 \n")
			case "unreadable":
				runner.readErr = errors.New("failed extraction")
			case "unavailable inventory":
				runner.listErr = errors.New("failed inventory")
			}
			runtime := productionRuntime{runner: runner}
			capability, err := runtime.merchantStorePackageCapability(context.Background(), "/safe/package", productionAURPackageName)
			if name == "absent" {
				if err != nil || capability != 0 || runner.reads != 0 || merchantStoreWriterTargetAllowed(1, capability) == nil {
					t.Fatal("inventory-proved absence became ordinary writer qualification")
				}
			} else if err == nil {
				t.Fatalf("%s accepted", name)
			}
		})
	}
}

func testMerchantWriterContract() *productionMerchantStoreWriterContract {
	target := productionMerchantStoreWriterTarget{Capability: 4, PackageSHA256: strings.Repeat("a", 64), PayloadSHA256: strings.Repeat("b", 64),
		SourceRevision: strings.Repeat("c", 40), ReleaseAssetSHA256: strings.Repeat("d", 64), StatusSHA256: strings.Repeat("e", 64)}
	return &productionMerchantStoreWriterContract{Format: 1, RequiredCapability: 4, SystemIdentifier: "7648633982160478129", Database: "lmm_api",
		DatabaseOID: 59903, Schema: "lmm_prod", SchemaOID: 60578, Role: "lmm_api", StartupSHA256: strings.Repeat("f", 64),
		SignedUnitSHA256: strings.Repeat("1", 64), RecoveryPolicy: "same-floor-writable", Candidate: target, Rollback: target}
}

func TestProductionMerchantStoreContractCannotClaimUnqualifiedRecovery(t *testing.T) {
	if err := validateMerchantStoreWriterContract(testMerchantWriterContract()); err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(*productionMerchantStoreWriterContract){
		"missing marker":                func(c *productionMerchantStoreWriterContract) { c.Rollback.Capability = 0 },
		"frozen refund rollback":        func(c *productionMerchantStoreWriterContract) { c.Rollback.Capability = 3 },
		"legacy policy":                 func(c *productionMerchantStoreWriterContract) { c.RecoveryPolicy = "frozen-only" },
		"missing actual status":         func(c *productionMerchantStoreWriterContract) { c.Rollback.StatusSHA256 = "" },
		"missing source":                func(c *productionMerchantStoreWriterContract) { c.Candidate.SourceRevision = "" },
		"missing signed asset":          func(c *productionMerchantStoreWriterContract) { c.Candidate.ReleaseAssetSHA256 = "" },
		"missing environment":           func(c *productionMerchantStoreWriterContract) { c.StartupSHA256 = "" },
		"missing unit":                  func(c *productionMerchantStoreWriterContract) { c.SignedUnitSHA256 = "" },
		"missing role":                  func(c *productionMerchantStoreWriterContract) { c.Role = "" },
		"physical cluster noncanonical": func(c *productionMerchantStoreWriterContract) { c.SystemIdentifier = "07648633982160478129" },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			contract := testMerchantWriterContract()
			change(contract)
			if validateMerchantStoreWriterContract(contract) == nil {
				t.Fatal("unqualified contract accepted")
			}
		})
	}
}
