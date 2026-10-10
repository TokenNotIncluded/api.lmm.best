package setting

import (
	"strings"
	"testing"
)

func TestAssistantToolPolicyInheritanceAndCanonicalization(t *testing.T) {
	canonical, policy, err := NormalizeAssistantToolPolicy(`{"tools":{"calculate_math":false,"search_web":true},"groups":{"service_help":false},"version":1}`)
	if err != nil {
		t.Fatal(err)
	}
	if canonical != `{"version":1,"groups":{"service_help":false},"tools":{"calculate_math":false,"search_web":true}}` {
		t.Fatalf("unstable policy: %s", canonical)
	}
	if policy.Enabled("search_web") || policy.Enabled("get_service_facts") || policy.Enabled("calculate_math") {
		t.Fatal("an individual enable cannot override a disabled group")
	}
	if !policy.Enabled("get_account_access") || policy.Enabled("not_a_tool") {
		t.Fatal("unchanged tools inherit enabled; unknown tools fail closed")
	}
	policy.Groups["service_help"] = true
	if !policy.Enabled("search_web") || !policy.Enabled("get_service_facts") || policy.Enabled("calculate_math") {
		t.Fatal("enabling a group must preserve individual overrides")
	}
	for _, raw := range []string{"", " \n", `{"version":1}`} {
		canonical, _, err := NormalizeAssistantToolPolicy(raw)
		if err != nil || canonical != DefaultAssistantToolPolicy {
			t.Fatalf("legacy/default policy %q: %s %v", raw, canonical, err)
		}
	}
}

func TestAssistantToolPolicyRejectsAmbiguousAndUnknownSettings(t *testing.T) {
	for _, raw := range []string{
		`{}`, `null`, `[]`, `{"version":2}`, `{"version":null}`, `{"version":1,"extra":true}`,
		`{"version":1,"groups":{"missing":false}}`, `{"version":1,"tools":{"prepare_l1_recommendation":true}}`,
		`{"version":1,"groups":[]}`, `{"version":1,"tools":null}`, `{"version":1,"tools":{"search_web":null}}`,
		`{"version":1,"tools":{"search_web":0}}`, `{"version":1,"tools":{"search_web":"false"}}`,
		`{"version":1,"version":1}`, `{"version":1,"tools":{"search_web":false,"search_web":true}}`,
		`{"version":1} {"version":1}`, strings.Repeat(" ", AssistantToolPolicyMaxBytes+1),
	} {
		t.Run(raw[:min(len(raw), 60)], func(t *testing.T) {
			if _, _, err := NormalizeAssistantToolPolicy(raw); err == nil {
				t.Fatal("ambiguous policy was accepted")
			}
			if err := ValidateAssistantOption(AssistantToolPolicyOptionKey, raw); err == nil {
				t.Fatal("option validation must reject before persisting")
			}
		})
	}
}

func TestAssistantToolPolicyUpdateIsAtomicAndCatalogueDetached(t *testing.T) {
	before := GetAssistantSettings().ToolPolicy
	t.Cleanup(func() { _ = UpdateAssistantToolPolicy(before) })
	if err := UpdateAssistantToolPolicy(`{"version":1,"tools":{"calculate_math":false}}`); err != nil {
		t.Fatal(err)
	}
	if err := UpdateAssistantToolPolicy(`{"version":1,"tools":{"calculate_math":"true"}}`); err == nil {
		t.Fatal("invalid update must fail")
	}
	if AssistantToolEnabled("calculate_math") || !AssistantToolEnabled("get_service_facts") {
		t.Fatal("failed update must preserve prior settings")
	}
	groups := AssistantToolCatalogue()
	firstTool := groups[0].Tools[0].Name
	groups[0].ID = "corrupt"
	groups[0].Tools[0].Name = "corrupt"
	if AssistantToolCatalogue()[0].ID != "service_help" || AssistantToolCatalogue()[0].Tools[0].Name != firstTool {
		t.Fatal("catalogue caller changed the registered definitions")
	}
}
