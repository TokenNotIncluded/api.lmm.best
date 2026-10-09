package marketprovider

import "testing"

func TestManualPublicationCannotBypassProviderPolicy(t *testing.T) {
	for _, p := range Presets() {
		pricing := &Pricing{Provider: p.ID, Multiplier: "1.2"}
		if ValidateTool(p.Endpoint, p.ExecuteTool, pricing) != nil {
			t.Fatal("valid preset rejected")
		}
		for _, tool := range append(p.ReadTools, p.ExecuteTool) {
			if ValidateTool(p.Endpoint, tool, nil) == nil && tool == p.ExecuteTool {
				t.Fatal("unpriced execution accepted")
			}
		}
		for _, name := range []string{"account", "agentkey_account", "monid_runs", "delete_key"} {
			if ValidateTool(p.Endpoint, name, nil) == nil {
				t.Errorf("allowed shared-account tool %s", name)
			}
		}
	}
	for _, endpoint := range []string{"https://API.AGENTKEY.APP/v1/mcp", "https://api.agentkey.app:443/v1/mcp", "https://api.agentkey.app./v1/mcp", "https://mcp.monid.ai/other"} {
		if ValidateTool(endpoint, "execute_tool", nil) == nil {
			t.Errorf("endpoint spelling bypass: %s", endpoint)
		}
	}
	if ValidateTool("https://custom.example/mcp", "custom_tool", nil) != nil {
		t.Fatal("custom services blocked")
	}
}

func TestRunIDCannotBecomeAnArbitraryURL(t *testing.T) {
	for _, bad := range []string{"", "../account", "a/b", "a?key=x", "a#fragment", "https://evil.example", "a%2fb", "a\n"} {
		if ValidRunID(bad) {
			t.Errorf("accepted run ID %q", bad)
		}
	}
	if !ValidRunID("01HXYZ1234567890ABCDEF") {
		t.Fatal("documented ID rejected")
	}
}
