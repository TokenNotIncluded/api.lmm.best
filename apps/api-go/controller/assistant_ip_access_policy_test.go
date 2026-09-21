// Copyright (C) 2026 LIghtJUNction
// SPDX-License-Identifier: AGPL-3.0-or-later

package controller

import (
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/setting"
)

func TestAssistantIPAccessSettingsAreNotWritable(t *testing.T) {
	// The current routing option must stay protected together with retired
	// whitelist options. A support message is not an IP access authorization.
	keys := []string{
		setting.IPAccessRoutingRulesOptionKey,
		"GlobalIPWhitelistEnabled",
		"GlobalIPWhitelistCIDRs",
		"RegionAccessPolicyEnabled",
		"RegionBlockedCountryCodes",
	}
	for _, key := range keys {
		t.Run(key, func(t *testing.T) {
			if _, allowed := assistantAdminConfigLabel(key); allowed {
				t.Fatalf("IP access setting %q must not be writable by the assistant", key)
			}
		})
	}
}

func TestAssistantIPAccessPolicyKeepsUnrelatedConfigAvailable(t *testing.T) {
	for _, key := range []string{"SystemName", "AssistantMaxSteps", "EmailDomainWhitelist"} {
		t.Run(key, func(t *testing.T) {
			if _, allowed := assistantAdminConfigLabel(key); !allowed {
				t.Fatalf("unrelated configuration %q should retain its existing confirmation flow", key)
			}
		})
	}
}
