// Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later.
package composition

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func paymentPrivate(t *testing.T, name, raw string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}
func TestPaymentProviderDocument(t *testing.T) {
	empty := "version=1\nprovider='stripe-test'\nchannels=[]\n"
	adapters, err := loadPaymentAdapters(paymentPrivate(t, "payment.toml", empty))
	if err != nil || len(adapters) != 0 {
		t.Fatal(adapters, err)
	}
	key := paymentPrivate(t, "stripe-key", "sk_test_synthetic_host_key_0000001")
	channel := "[[channels]]\nid=1\nmerchant='acct_synthetic_host'\nenabled=true\napi_key_file='" + key + "'\napi_version='2025-02-24.acacia'\nsuccess_url='https://example.test/success'\ncancel_url='https://example.test/cancel'\n"
	base := "version=1\nprovider='stripe-test'\n"
	adapters, err = loadPaymentAdapters(paymentPrivate(t, "payment.toml", base+channel))
	if err != nil || len(adapters) != 1 || adapters[0].Channel().Environment != "test" || adapters[0].Channel().Currency != "USD" || !adapters[0].Channel().Enabled {
		t.Fatal(adapters, err)
	}
	for _, raw := range []string{
		strings.Replace(empty, "version=1", "version=2", 1),
		strings.Replace(empty, "stripe-test", "stripe-live", 1),
		"version=1\nprovider='stripe-test'\n",
		strings.Replace(empty, "provider=", "Provider=", 1),
		base + channel + "webhook_secret='whsec_private_should_never_reach_go'\n",
		base + channel + "environment='live'\n",
		base + channel + "endpoint='http://127.0.0.1'\n",
		base + channel + channel,
		base + strings.Replace(channel, "api_key_file=", "APIKeyFile=", 1),
		base + strings.Replace(channel, "id=1", "id=0", 1),
		base + strings.Replace(channel, "https://example.test/success", "http://127.0.0.1/success", 1),
		base + strings.Replace(channel, key, "relative-key", 1),
	} {
		if _, err := loadPaymentAdapters(paymentPrivate(t, "payment.toml", raw)); err == nil || strings.Contains(err.Error(), "whsec_private") {
			t.Fatal("invalid payment document accepted or secret leaked", err)
		}
	}
	for _, raw := range []string{"sk_live_synthetic_host_key_0000001", "rk_live_synthetic_host_key_0000001", "sk_test_key\nembedded_secret"} {
		badKey := paymentPrivate(t, "bad-key", raw)
		if _, err := loadPaymentAdapters(paymentPrivate(t, "payment.toml", base+strings.Replace(channel, key, badKey, 1))); err == nil {
			t.Fatal("unsafe API credential accepted")
		}
	}
	link := filepath.Join(t.TempDir(), "link.toml")
	if err := os.Symlink(paymentPrivate(t, "payment.toml", empty), link); err != nil {
		t.Fatal(err)
	}
	if _, err := loadPaymentAdapters(link); err == nil {
		t.Fatal("symlink payment document accepted")
	}
	unsafe := paymentPrivate(t, "payment.toml", empty)
	if err := os.Chmod(unsafe, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadPaymentAdapters(unsafe); err == nil {
		t.Fatal("public payment document accepted")
	}
}
func TestPaymentConfigurationRequiresExplicitSelection(t *testing.T) {
	for _, cfg := range []Config{
		{PaymentTokenFile: "/nonexistent-payment"},
		{PaymentConfigFile: "/nonexistent-provider"},
		{Payments: StorageConfig{Component: "payments", Database: "lmm_payments_test"}},
		{Selected: []string{"payments"}},
	} {
		runtime, err := New(context.Background(), cfg)
		if err == nil || runtime != nil {
			t.Fatal("incomplete or unselected payment configuration accepted", runtime, err)
		}
	}
}
func TestPaymentRPCUsesIndependentCredential(t *testing.T) {
	token := "synthetic_payment_principal_0000000001"
	payment := paymentPrivate(t, "payment-token", token+"\n")
	cfg := Config{PaymentTokenFile: payment, HostCredential: []byte("synthetic_other_host_principal_0000001")}
	identity := []byte("synthetic_other_identity_principal_0001")
	for _, pair := range []struct{ identity, commerce, host []byte }{
		{[]byte(token), nil, cfg.HostCredential},
		{identity, []byte(token), cfg.HostCredential},
		{identity, nil, []byte(token)},
		{identity, nil, nil},
	} {
		cfg.HostCredential = pair.host
		if _, err := loadPaymentCredential(cfg, pair.identity, pair.commerce); err == nil || !strings.Contains(err.Error(), "independent") {
			t.Fatal("shared or absent host credential accepted", err)
		}
	}
	cfg.HostCredential = []byte("synthetic_other_host_principal_0000001")
	loaded, err := loadPaymentCredential(cfg, identity, nil)
	if err != nil || string(loaded) != token {
		t.Fatal("independent payment credential rejected", err)
	}
	// Later file rotation does not change the checked bytes passed to the client.
	if err := os.WriteFile(payment, identity, 0600); err != nil {
		t.Fatal(err)
	}
	if string(loaded) != token {
		t.Fatal("loaded payment credential changed during rotation")
	}
}

func TestLoadedRuntimeCredentialsArePairwiseIndependent(t *testing.T) {
	original := [][]byte{
		[]byte("synthetic_loaded_host_credential_000001"),
		[]byte("synthetic_loaded_identity_credential_1"),
		[]byte("synthetic_loaded_commerce_credential_1"),
		[]byte("synthetic_loaded_payment_credential_01"),
	}
	if err := independentRuntimeCredentials(original...); err != nil {
		t.Fatal(err)
	}
	for i := range original {
		for j := i + 1; j < len(original); j++ {
			tokens := append([][]byte(nil), original...)
			tokens[j] = tokens[i]
			if err := independentRuntimeCredentials(tokens...); err == nil {
				t.Fatalf("credential roles %d and %d were allowed to share actual bytes", i, j)
			}
		}
	}
	var absent []byte
	for _, tokens := range [][][]byte{{}, {absent, []byte{}}, {original[0], original[1], absent, []byte{}}, {absent, original[1], original[2]}} {
		if err := independentRuntimeCredentials(tokens...); err != nil {
			t.Fatal("absent optional role treated as shared authority", err)
		}
	}
}

func TestHostCredentialCannotShareIdentityOrCommerceWithoutPayments(t *testing.T) {
	identity := "synthetic_identity_actual_token_000001"
	commerce := "synthetic_commerce_actual_token_000001"
	for _, scenario := range []struct {
		name string
		cfg  Config
	}{
		{"identity", Config{Selected: []string{"identity"}, Socket: "/tmp/synthetic-payment-credentials.sock", TokenFile: paymentPrivate(t, "identity", identity), HostCredential: []byte(identity)}},
		{"commerce", Config{Selected: []string{"store"}, Socket: "/tmp/synthetic-payment-credentials.sock", TokenFile: paymentPrivate(t, "identity", identity), CommerceTokenFile: paymentPrivate(t, "commerce", commerce), HostCredential: []byte(commerce)}},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			runtime, err := New(context.Background(), scenario.cfg)
			if runtime != nil || err == nil || !strings.Contains(err.Error(), "independent") {
				t.Fatal("host credential shared an actual RPC token", runtime, err)
			}
		})
	}
	for _, selected := range [][]string{nil, {}} {
		runtime, err := New(context.Background(), Config{Selected: selected})
		if err != nil || runtime == nil || runtime.Client != nil || runtime.PaymentClient != nil || runtime.CommerceClient != nil {
			t.Fatal("empty module selection opened or required RPC authority", runtime, err)
		}
		if err := runtime.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
