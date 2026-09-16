package controller

import "testing"

// IP access decisions require the dedicated human-operated administration
// surface. The shared assistant configuration catalog must not expose them,
// including to root administrators or through retired option names.
func TestAssistantIPAccessConfigurationBoundary(t *testing.T) {
	blockedKeys := []string{
		"IPAccessRoutingRules",
		"IPAccessRoutingRulesVersion",
		"GlobalIPWhitelistEnabled",
		"GlobalIPWhitelistCIDRs",
		"RegionAccessPolicyEnabled",
		"RegionBlockedCountryCodes",
		"PersonalAccessIPWhitelistEnabled",
		"PersonalAccessIPWhitelistTTLSeconds",
		"PersonalAccessIPWhitelistMaxEntriesPerUser",
		"PersonalAccessIPWhitelistMaxDailyRequests",
		"PersonalAccessIPWhitelistRequireBYOK",
		"PersonalAccessIPWhitelistAllowRiskBypass",
	}
	catalog := assistantAdminAvailableConfigLabels()
	for _, key := range blockedKeys {
		t.Run(key, func(t *testing.T) {
			if _, allowed := assistantAdminConfigLabel(key); allowed {
				t.Errorf("IP access setting %q must not be writable through the assistant", key)
			}
			if _, advertised := catalog[key]; advertised {
				t.Errorf("IP access setting %q must not be advertised as an assistant configuration capability", key)
			}
		})
	}
	// Avoid a vacuous pass caused by disabling the entire configuration tool.
	if _, allowed := assistantAdminConfigLabel("Notice"); !allowed {
		t.Fatal("ordinary supported configuration must remain available")
	}
}
