package dto

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
)

func TestAdvancedCustomBalanceConfigurationIsBalanceOnlyAndBounded(t *testing.T) {
	body := `{"key":"{api_key}"}`
	empty := ""
	scale := 0.01
	badScale := math.Inf(1)
	for _, tc := range []struct {
		name   string
		config AdvancedCustomBalanceConfig
		valid  bool
	}{
		{"legacy GET", AdvancedCustomBalanceConfig{}, true},
		{"POST without body", AdvancedCustomBalanceConfig{Method: "POST"}, true},
		{"POST JSON", AdvancedCustomBalanceConfig{Method: " post ", BodyTemplate: &body, JSONPointer: "/account/remaining", Scale: &scale}, true},
		{"PUT", AdvancedCustomBalanceConfig{Method: "PUT"}, false},
		{"GET body", AdvancedCustomBalanceConfig{BodyTemplate: &body}, false},
		{"present empty body", AdvancedCustomBalanceConfig{Method: "POST", BodyTemplate: &empty}, false},
		{"pointer expression", AdvancedCustomBalanceConfig{JSONPointer: "$.balance"}, false},
		{"bad escape", AdvancedCustomBalanceConfig{JSONPointer: "/a~2b"}, false},
		{"pointer too large", AdvancedCustomBalanceConfig{JSONPointer: "/" + strings.Repeat("x", MaxAdvancedCustomBalancePointerBytes)}, false},
		{"pointer too deep", AdvancedCustomBalanceConfig{JSONPointer: strings.Repeat("/a", MaxAdvancedCustomBalancePointerDepth+1)}, false},
		{"scale without pointer", AdvancedCustomBalanceConfig{Scale: &scale}, false},
		{"nonfinite scale", AdvancedCustomBalanceConfig{JSONPointer: "/balance", Scale: &badScale}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.config.Validate(); (err == nil) != tc.valid {
				t.Fatalf("Validate() = %v, valid = %v", err, tc.valid)
			}
		})
	}
	for _, path := range []string{AdvancedCustomModelListPath, "/v1/chat/completions"} {
		config := AdvancedCustomConfig{Routes: []AdvancedCustomRoute{{IncomingPath: path, UpstreamPath: path, Balance: &AdvancedCustomBalanceConfig{}}}}
		if err := config.Validate(); err == nil {
			t.Fatalf("balance configuration accepted on %s", path)
		}
	}
}

func TestAdvancedCustomBalanceTemplateEscapesCredentialAndBoundsRenderedBody(t *testing.T) {
	atLimit := `"` + strings.Repeat("x", MaxAdvancedCustomBalanceBodyBytes-2) + `"`
	atLimitConfig := &AdvancedCustomBalanceConfig{Method: "POST", BodyTemplate: &atLimit}
	atLimitBody, err := atLimitConfig.RenderBody("secret")
	if err != nil || len(atLimitBody) != MaxAdvancedCustomBalanceBodyBytes {
		t.Fatalf("valid template at the exact byte limit failed: %d %v", len(atLimitBody), err)
	}
	template := `{"key":"prefix-{api_key}","fixed":true}`
	key := "credential\"},\"injected\":true,\"tail\":\"\\\n雪"
	config := &AdvancedCustomBalanceConfig{Method: "POST", BodyTemplate: &template}
	body, err := config.RenderBody(key)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded) != 2 || decoded["key"] != "prefix-"+key || decoded["fixed"] != true {
		t.Fatalf("credential changed the JSON structure: %#v", decoded)
	}
	if _, err := config.RenderBody(strings.Repeat("x", MaxAdvancedCustomBalanceBodyBytes)); err == nil {
		t.Fatal("accepted oversized rendered body")
	}
	tooLarge := `"` + strings.Repeat("x", MaxAdvancedCustomBalanceBodyBytes) + `"`
	config.BodyTemplate = &tooLarge
	if _, err := config.RenderBody("secret"); err == nil {
		t.Fatal("accepted oversized template")
	}
	outside := `{"key":{api_key}}`
	config.BodyTemplate = &outside
	if _, err := config.RenderBody("secret"); err == nil {
		t.Fatal("accepted placeholder outside a JSON string")
	}
	tokens, err := AdvancedCustomBalancePointerTokens("/a~1b/m~0n/~01")
	if err != nil || strings.Join(tokens, "|") != "a/b|m~n|~1" {
		t.Fatalf("incorrect RFC 6901 decoding: %v %v", tokens, err)
	}
}
