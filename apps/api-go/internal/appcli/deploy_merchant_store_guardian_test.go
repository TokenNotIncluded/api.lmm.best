package appcli

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestProductionMerchantStoreGuardianRequestBindsExactOwner(t *testing.T) {
	owner := productionMerchantStoreFenceOwner{PlanSHA256: strings.Repeat("a", 64), ContractSHA256: strings.Repeat("b", 64), Nonce: strings.Repeat("c", 32)}
	request := merchantStoreFenceRequest{Protocol: merchantStoreFenceProtocol, Operation: "check", PlanSHA256: owner.PlanSHA256, ContractSHA256: owner.ContractSHA256, Nonce: owner.Nonce}
	good, _ := json.Marshal(request)
	for name, body := range map[string]string{
		"check": string(good), "release": strings.Replace(string(good), `"check"`, `"release"`, 1),
		"bad protocol": strings.Replace(string(good), merchantStoreFenceProtocol, "old-financial-guardian", 1),
		"auto sweep":   strings.Replace(string(good), `"check"`, `"sweep"`, 1),
		"wrong plan":   strings.Replace(string(good), owner.PlanSHA256, strings.Repeat("d", 64), 1),
		"wrong nonce":  strings.Replace(string(good), owner.Nonce, strings.Repeat("e", 32), 1),
		"duplicate":    strings.TrimSuffix(string(good), "}") + `,"operation":"release"}`,
		"unknown":      strings.TrimSuffix(string(good), "}") + `,"ignore_floor":true}`,
		"whitespace":   " " + string(good), "null": "null",
	} {
		t.Run(name, func(t *testing.T) {
			client, server := net.Pipe()
			defer server.Close()
			go func() { defer client.Close(); _, _ = client.Write([]byte(body + "\n")) }()
			actual, err := readMerchantStoreFenceRequest(server, owner)
			allowed := name == "check" || name == "release"
			if (err == nil) != allowed || allowed && !merchantStoreFenceRequestValid(actual, owner) {
				t.Fatalf("allowed=%t error=%v request=%+v", allowed, err, actual)
			}
		})
	}
}

func TestProductionMerchantStoreQualificationNeedsSuccessfulExactActualStatus(t *testing.T) {
	contract := testMerchantWriterContract()
	body := merchantWriterStatusJSON(4, 4)
	target := contract.Candidate
	target.StatusSHA256 = startupContentSHA256(body)
	if err := qualifyMerchantStoreWriterStatus(body, nil, contract, target); err != nil {
		t.Fatal(err)
	}
	for name, check := range map[string]func() error{
		"exit64 with fabricated good stdout": func() error {
			return qualifyMerchantStoreWriterStatus(body, errors.New("exit status 64"), contract, target)
		},
		"empty zero exit": func() error { return qualifyMerchantStoreWriterStatus(nil, nil, contract, target) },
		"wrong raw bytes": func() error { return qualifyMerchantStoreWriterStatus(append(body, ' '), nil, contract, target) },
		"below floor": func() error {
			return qualifyMerchantStoreWriterStatus(merchantWriterStatusJSON(4, 3), nil, contract, target)
		},
		"nil contract": func() error { return qualifyMerchantStoreWriterStatus(body, nil, nil, target) },
	} {
		t.Run(name, func(t *testing.T) {
			if check() == nil {
				t.Fatal("unqualified actual status authorized writer mutation")
			}
		})
	}
}

func TestProductionMerchantStoreRetainedProvidersActualUnsupported(t *testing.T) {
	if os.Getenv("LMM_TEST_RETAINED_MERCHANT_PROVIDERS") != "1" {
		t.Skip("requires explicitly selected local retained official ELF files; never downloads or accesses production")
	}
	for _, provider := range []struct{ label, path, digest string }{
		{"Go86", "/home/lightjunction/.cache/go86-web126-final-20261006/official-download/go-unpacked/lmm-api-go", "f07b2e205a0f8524aed986786f9b14a1e0ae83dd1d0441bbcbeef2f92e6e0466"},
		{"Go87", "/home/lightjunction/.cache/finance87-web127-official-20261007/go/unpacked/lmm-api-go-0.2.87-linux-amd64/lmm-api-go", "37f514091dab4e30df7c6b627305850dcce4a81f19f22fbf0ef1498195f73870"},
	} {
		t.Run(provider.label, func(t *testing.T) {
			if err := sha256MustEqual(provider.path, provider.digest); err != nil {
				t.Fatal("retained official ELF hash changed")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, provider.path, "merchant-store-writer-gate", "status")
			command.Env = []string{"PATH=/usr/bin:/bin", "LANG=C", "TZ=UTC"}
			output, err := command.Output()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != ExitUsage || len(output) != 0 {
				t.Fatalf("retained unsupported protocol: exit=%v stdout_bytes=%d", err, len(output))
			}
			contract := testMerchantWriterContract()
			if qualifyMerchantStoreWriterStatus(output, err, contract, contract.Candidate) == nil {
				t.Fatal("unsupported retained CLI authorized mutation")
			}
		})
	}
}

func TestProductionMerchantStoreOwnerRequiresActualHolderAndCanonicalPhysicalIdentity(t *testing.T) {
	owner := productionMerchantStoreFenceOwner{Format: 1, State: "ACTIVE", DeploymentID: "release-owner-test", Host: "arch-dmit", PlanSHA256: strings.Repeat("a", 64), ContractSHA256: strings.Repeat("b", 64), ProviderSHA256: strings.Repeat("c", 64), Nonce: strings.Repeat("d", 32), HolderPID: 123, HolderUnit: merchantStoreFenceUnit("release-owner-test"), HolderInvocationID: strings.Repeat("e", 32), BackendPID: 321, SystemIdentifier: "7648633982160478129", Database: "lmm_api", DatabaseOID: 1, Schema: "lmm_prod", SchemaOID: 2, Role: "lmm_api"}
	if err := validateMerchantStoreFenceOwner(owner); err != nil {
		t.Fatal(err)
	}
	for name, modify := range map[string]func(*productionMerchantStoreFenceOwner){
		"wrong unit":               func(o *productionMerchantStoreFenceOwner) { o.HolderUnit = "lmm-financial-guardian.service" },
		"other release unit":       func(o *productionMerchantStoreFenceOwner) { o.HolderUnit = merchantStoreFenceUnit("other-release") },
		"zero physical ID":         func(o *productionMerchantStoreFenceOwner) { o.SystemIdentifier = "0" },
		"noncanonical physical ID": func(o *productionMerchantStoreFenceOwner) { o.SystemIdentifier = "07648633982160478129" },
		"negative physical ID":     func(o *productionMerchantStoreFenceOwner) { o.SystemIdentifier = "-1" },
		"invalid host":             func(o *productionMerchantStoreFenceOwner) { o.Host = "arch-dmit\n" },
		"released":                 func(o *productionMerchantStoreFenceOwner) { o.State = "RELEASED" },
	} {
		t.Run(name, func(t *testing.T) {
			copy := owner
			modify(&copy)
			if validateMerchantStoreFenceOwner(copy) == nil {
				t.Fatal("invalid durable owner accepted")
			}
		})
	}
}

func TestProductionMerchantStoreContractFileDigestCannotBeRebound(t *testing.T) {
	contract := testMerchantWriterContract()
	body, _ := json.MarshalIndent(contract, "", "  ")
	body = append(body, '\n')
	path := filepath.Join(t.TempDir(), "writer-contract.json")
	if err := os.WriteFile(path, body, 0600); err != nil {
		t.Fatal(err)
	}
	digest := startupContentSHA256(body)
	if _, err := loadMerchantStoreWriterContract(path, digest); err != nil {
		t.Fatal(err)
	}
	contract.Rollback.StatusSHA256 = strings.Repeat("2", 64)
	modified, _ := json.MarshalIndent(contract, "", "  ")
	modified = append(modified, '\n')
	if err := os.WriteFile(path, modified, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadMerchantStoreWriterContract(path, digest); err == nil {
		t.Fatal("new raw-status seal was accepted under old canonical digest")
	}
}

type merchantStoreVerificationCommandFixture struct {
	commands []productionCommand
	err      error
}

func (fixture *merchantStoreVerificationCommandFixture) Run(_ context.Context, command productionCommand) ([]byte, error) {
	fixture.commands = append(fixture.commands, command)
	return nil, fixture.err
}

func TestProductionMerchantStoreBothProviderVerificationUsesSealedReadOnlyChild(t *testing.T) {
	t.Setenv(existingSchemaFinancialEnvironment[0], "unsealed-ambient-financial-intent")
	for _, role := range []string{"candidate", "rollback"} {
		t.Run(role, func(t *testing.T) {
			runner := &merchantStoreVerificationCommandFixture{}
			runtime := &productionRuntime{runner: runner}
			workspace := productionWorkspace{root: t.TempDir()}
			child := []string{"SQL_DSN=postgres://business@127.0.0.1/test?default_transaction_read_only=on", "LMM_DB_MIGRATION_MODE=verify", "PGOPTIONS=-c search_path=lmm_prod -c default_transaction_read_only=on"}
			if err := runtime.verifyMerchantStoreWriterProvider(context.Background(), workspace, "/qualified/retained-provider", child, role); err != nil {
				t.Fatal(err)
			}
			if len(runner.commands) != 1 || runner.commands[0].Name != commandRunuser || strings.Join(runner.commands[0].Args, " ") != "--user root -- /qualified/retained-provider migrate --verify" {
				t.Fatal("provider verification executed another mode/target")
			}
			if strings.Join(runner.commands[0].Env, "\n") != strings.Join(child, "\n") {
				t.Fatal("provider verification inherited an unsealed environment")
			}
			for _, invalid := range []string{"LMM_DB_MIGRATION_MODE=apply", existingSchemaFinancialEnvironment[0] + "=financial-intent", "PGOPTIONS=-c search_path=lmm_prod -c default_transaction_read_only=on -c default_transaction_read_only=off"} {
				bad := append(append([]string(nil), child...), invalid)
				before := len(runner.commands)
				if err := runtime.verifyMerchantStoreWriterProvider(context.Background(), workspace, "/qualified/retained-provider", bad, role); err == nil || len(runner.commands) != before {
					t.Fatal("unsafe provider verification reached a subprocess")
				}
			}
			runner.err = errors.New("command runuser failed: exit status 1")
			err := runtime.verifyMerchantStoreWriterProvider(context.Background(), workspace, "/qualified/private-provider", child, role)
			if !errors.Is(err, runner.err) || !strings.Contains(err.Error(), role+" migrate --verify failed") {
				t.Fatalf("verification failure lost provider role, step or cause: %v", err)
			}
			if strings.Contains(err.Error(), "postgres://") || strings.Contains(err.Error(), "private-provider") || !runner.commands[len(runner.commands)-1].Sensitive {
				t.Fatal("verification diagnostic exposed private input or disabled subprocess redaction")
			}
		})
	}
}
