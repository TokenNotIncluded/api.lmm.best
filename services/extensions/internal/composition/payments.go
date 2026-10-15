// Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later.
package composition

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/BurntSushi/toml"
	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/coreclient"
	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules/payments"
	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules/payments/rustbridge"
)

type paymentDocument struct {
	Version  int              `toml:"version"`
	Provider string           `toml:"provider"`
	Channels []paymentChannel `toml:"channels"`
}
type paymentChannel struct {
	ID         int64  `toml:"id"`
	Merchant   string `toml:"merchant"`
	Enabled    bool   `toml:"enabled"`
	APIKeyFile string `toml:"api_key_file"`
	APIVersion string `toml:"api_version"`
	SuccessURL string `toml:"success_url"`
	CancelURL  string `toml:"cancel_url"`
}

func paymentConfigured(cfg Config) bool {
	return cfg.PaymentTokenFile != "" || cfg.Payments.URLFile != "" || cfg.Payments.Database != "" || cfg.PaymentConfigFile != ""
}

// Reject ignored financial configuration before New's empty-selection return.
func validatePaymentSelection(cfg Config) error {
	selected := false
	for _, name := range cfg.Selected {
		selected = selected || name == "payments"
	}
	if !selected {
		if paymentConfigured(cfg) {
			return errors.New("payments configuration requires explicit module selection")
		}
		return nil
	}
	if cfg.PaymentTokenFile == "" || cfg.PaymentConfigFile == "" {
		return errors.New("payments requires independent RPC and provider configuration")
	}
	if cfg.Payments.Component != "payments" || cfg.Payments.validate() != nil {
		return errors.New("payments requires dedicated storage")
	}
	return nil
}

// Only the Core's existing Stripe test/USD policy is supported. This private
// document has no webhook secret, arbitrary endpoint or exchange-rate field.
func loadPaymentAdapters(path string) ([]payments.Adapter, error) {
	if !cleanPaymentPath(path) {
		return nil, errors.New("invalid private payment configuration path")
	}
	raw, err := coreclient.ReadPrivateFile(path, 64<<10)
	if err != nil || !utf8.Valid(raw) {
		return nil, errors.New("private payment configuration unavailable")
	}
	var cfg paymentDocument
	meta, err := toml.NewDecoder(bytes.NewReader(raw)).Decode(&cfg)
	if err != nil || len(meta.Undecoded()) != 0 {
		return nil, errors.New("invalid private payment configuration")
	}
	allowed := map[string]bool{"version": true, "provider": true, "channels": true, "channels.id": true, "channels.merchant": true, "channels.enabled": true, "channels.api_key_file": true, "channels.api_version": true, "channels.success_url": true, "channels.cancel_url": true}
	for _, key := range meta.Keys() {
		if !allowed[key.String()] {
			return nil, errors.New("unknown private payment configuration field")
		}
	}
	if cfg.Version != 1 || cfg.Provider != "stripe-test" || !meta.IsDefined("channels") || len(cfg.Channels) > 16 {
		return nil, errors.New("payments require explicit bounded Stripe test channels")
	}
	adapters := make([]payments.Adapter, 0, len(cfg.Channels))
	ids, merchants := map[int64]bool{}, map[string]bool{}
	for _, c := range cfg.Channels {
		if !cleanPaymentPath(c.APIKeyFile) || ids[c.ID] || merchants[c.Merchant] {
			return nil, errors.New("invalid or duplicate payment channel")
		}
		rawKey, err := coreclient.ReadPrivateFile(c.APIKeyFile, 4096)
		if err != nil {
			return nil, errors.New("private payment API credential unavailable")
		}
		key := strings.TrimSpace(string(rawKey))
		if len(key) < 16 || len(key) > 4096 {
			return nil, errors.New("invalid private payment API credential")
		}
		for _, b := range []byte(key) {
			if b < 33 || b > 126 {
				return nil, errors.New("invalid private payment API credential")
			}
		}
		adapter, err := payments.NewStripe(payments.StripeConfig{Channel: payments.Channel{ID: c.ID, Provider: "stripe", Merchant: c.Merchant, Environment: "test", Currency: "USD", Enabled: c.Enabled}, APIKey: key, APIVersion: c.APIVersion, SuccessURL: c.SuccessURL, CancelURL: c.CancelURL})
		if err != nil {
			return nil, errors.New("invalid Stripe test channel configuration")
		}
		ids[c.ID], merchants[c.Merchant] = true, true
		adapters = append(adapters, adapter)
	}
	return adapters, nil
}
func cleanPaymentPath(path string) bool {
	return path != "" && filepath.IsAbs(path) && filepath.Clean(path) == path && !strings.ContainsAny(path, "\x00\r\n")
}

// The bytes checked here are the bytes passed to the actual clients. Each
// financial credential is read once, so atomic key-file rotation cannot swap
// a different credential between the isolation check and client construction.
func loadPaymentCredential(cfg Config, identityToken, commerceToken []byte) ([]byte, error) {
	paymentToken, err := coreclient.ReadTokenFile(cfg.PaymentTokenFile)
	if err != nil {
		return nil, errors.New("private payment RPC credential unavailable")
	}
	if len(cfg.HostCredential) < 32 {
		return nil, errors.New("payment RPC requires an independent loaded host credential")
	}
	if err := independentRuntimeCredentials(cfg.HostCredential, identityToken, commerceToken, paymentToken); err != nil {
		return nil, err
	}
	return paymentToken, nil
}

// Empty optional roles were not loaded and grant no authority. For actual app
// startup, Run always supplies its already validated host credential. Legacy
// internal composition callers may omit it when payments are not selected.
func independentRuntimeCredentials(credentials ...[]byte) error {
	for i, token := range credentials {
		if len(token) == 0 {
			continue
		}
		for _, other := range credentials[i+1:] {
			if len(other) != 0 && bytes.Equal(token, other) {
				return errors.New("host and core RPC credentials must be independent")
			}
		}
	}
	return nil
}
func (r *Runtime) composePayments(ctx context.Context, cfg Config, paymentToken []byte) error {
	adapters, err := loadPaymentAdapters(cfg.PaymentConfigFile)
	if err != nil {
		return err
	}
	r.PaymentClient, err = coreclient.New(cfg.Socket, paymentToken)
	if err != nil {
		return err
	}
	r.close = append(r.close, r.PaymentClient.Close)
	db, err := openStorage(ctx, cfg.Payments)
	if err != nil {
		return err
	}
	r.close = append(r.close, db.Close)
	bounded, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	repo, err := payments.NewPostgres(bounded, db, cfg.Payments.Database)
	if err != nil {
		return errors.New("payment storage contract unavailable")
	}
	bridge, err := rustbridge.New(r.PaymentClient)
	if err != nil {
		return err
	}
	r.Payments, err = payments.New(repo, bridge, adapters...)
	return err
}
