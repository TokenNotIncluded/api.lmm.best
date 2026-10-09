package controller

import (
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/setting"
)

func TestAssistantOpenUIContract(t *testing.T) {
	if strings.TrimSpace(assistantOpenUISystemPrompt) == "" {
		t.Fatal("generated OpenUI prompt must not be empty")
	}
	for _, required := range []string{"Stack", "Metric", "BarChart", "DataTable", "ConsoleLink", "display-only", "live tool results", "confirmation cards"} {
		if !strings.Contains(assistantOpenUISystemPrompt, required) {
			t.Errorf("OpenUI contract is missing %q", required)
		}
	}
	prompt := buildAssistantSystemPrompt(setting.GetAssistantSettings())
	if strings.Count(prompt, assistantOpenUISystemPrompt) != 1 {
		t.Fatal("the generated contract must appear exactly once")
	}
	if !strings.HasSuffix(prompt, assistantSystemRules) {
		t.Fatal("existing non-overridable rules must remain last")
	}
}
