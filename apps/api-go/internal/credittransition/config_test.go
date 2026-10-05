package credittransition

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testConfig() Config {
	return Config{Format: Format, TransitionID: "credits-20261006", TransitionIntentSHA256: strings.Repeat("a", 64),
		ProviderSHA256: strings.Repeat("b", 64), TargetCreditsPerUSD: 500000,
		Database: DatabaseIdentity{SystemIdentifier: "1234567890", Database: "fixture", DatabaseOID: 42, Schema: "lmm_fixture", ServerVersionNum: 180000, DatabaseUser: "fixture_user"},
		Options:  map[string]string{"CreditsPerUSD": "3359744", "LegacyPricingQuotaPerUnit": "500000", "QuotaPerUnit": "500000", "PublicCreditsPerUSD": "100000", "USDExchangeRate": "6.710363"}}
}

func TestSealedPreparationNeverAuthorizesAnotherTarget(t *testing.T) {
	for _, mutate := range []func(*Config){
		func(c *Config) { c.TargetCreditsPerUSD = 3359744 },
		func(c *Config) { c.TransitionIntentSHA256 = "" },
		func(c *Config) { c.ProviderSHA256 = "" },
		func(c *Config) { c.Database.SystemIdentifier = "unknown" },
		func(c *Config) { c.Database.Schema = "unsafe;drop schema public" },
		func(c *Config) { c.Database.Schema = "pg_catalog" },
		func(c *Config) { c.Options["USDExchangeRate"] = "NaN" },
		func(c *Config) { delete(c.Options, "CreditsPerUSD") },
		func(c *Config) { c.Options["Price"] = "1" },
		func(c *Config) {
			for _, k := range OptionKeys {
				if k != "USDExchangeRate" {
					c.Options[k] = "500000"
				}
			}
		},
	} {
		config := testConfig()
		mutate(&config)
		if err := config.Validate(); err == nil {
			t.Fatalf("accepted invalid preparation: %#v", config)
		}
	}
	config := testConfig()
	if err := config.Validate(); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Decode(data); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range [][]byte{append(data, []byte(` {}`)...), []byte(strings.Replace(string(data), `"format":`, `"unknown":`, 1))} {
		if _, err := Decode(invalid); err == nil {
			t.Fatal("accepted ambiguous/unknown configuration")
		}
	}
}

func TestPreparationRequestFailsClosedForPartialEnvironment(t *testing.T) {
	t.Setenv(PlanEnvironment, "")
	t.Setenv(DigestEnvironment, "")
	if Requested() {
		t.Fatal("preparation enabled by default")
	}
	t.Setenv(DigestEnvironment, strings.Repeat("a", 64))
	if !Requested() {
		t.Fatal("partial preparation configuration fell through to business startup")
	}
	if _, err := ReadConfig("", os.Getenv(DigestEnvironment)); err == nil {
		t.Fatal("accepted missing sealed plan")
	}
}

func TestPreparationProviderMustMatchItsSealedHash(t *testing.T) {
	config := testConfig()
	path := filepath.Join(t.TempDir(), "provider")
	if err := os.WriteFile(path, []byte("unreviewed provider"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := config.VerifyProvider(path); err == nil {
		t.Fatal("accepted another binary")
	}
}
