package controller

import (
	"bytes"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/internal/assistantcontracts"
)

func TestAssistantAdminOperationContractsMatchSources(t *testing.T) {
	generated, err := assistantcontracts.Generate("..")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(generated, assistantAdminOperationContractsJSON) {
		t.Fatal("assistant request contracts are stale; run go generate ./controller from apps/api-go")
	}
}

func TestAssistantAdminOperationContractsCriticalPayloads(t *testing.T) {
	cases := []struct {
		handler string
		fields  []string
	}{
		{"UpdateOption", []string{"key", "value"}},
		{"UpdateOptionsBulk", []string{"values"}},
		{"UpdateChannel", []string{"id", "models", "model_mapping", "key_mode"}},
		{"AddChannel", []string{"mode", "channel"}},
		{"UpdateUser", []string{"id", "username", "admin_permissions"}},
		{"CreateModelMeta", []string{"model_name", "vendor_id"}},
		{"CreateVendorMeta", []string{"name"}},
		{"AddDiscountCode", []string{"code", "discount_percent"}},
		{"AddRedemption", []string{"quota", "count"}},
		{"AdminCreateSubscriptionPlan", []string{"plan"}},
		{"UpdateAdvancedSecuritySettings", []string{"enabled", "on_prompt", "action", "rules"}},
	}
	for _, tc := range cases {
		t.Run(tc.handler, func(t *testing.T) {
			contract := assistantAdminOperationContract(tc.handler)
			if contract["contract_status"] != "derived" {
				t.Fatalf("incomplete critical contract: %v", contract)
			}
			body, ok := contract["body_schema"].(map[string]any)
			if !ok {
				t.Fatal("missing body")
			}
			properties, ok := body["properties"].(map[string]any)
			if !ok {
				t.Fatal("missing properties")
			}
			for _, field := range tc.fields {
				if _, ok := properties[field]; !ok {
					t.Errorf("missing field %q", field)
				}
			}
		})
	}
}

func TestAssistantAdminOperationContractIsolationAndUnknown(t *testing.T) {
	first := assistantAdminOperationContract("UpdateOption")
	first["body_schema"].(map[string]any)["properties"].(map[string]any)["key"] = "corrupt"
	second := assistantAdminOperationContract("controller.UpdateOption")
	if _, ok := second["body_schema"].(map[string]any)["properties"].(map[string]any)["key"].(map[string]any); !ok {
		t.Fatal("caller mutated shared contract")
	}
	unknown := assistantAdminOperationContract("UnknownHandler")
	if unknown["contract_status"] != "unavailable" {
		t.Fatalf("unknown handler described as callable: %v", unknown)
	}
}

func TestAssistantAdminOperationContractsPreserveQueryAndMutationSemantics(t *testing.T) {
	users := assistantAdminOperationContract("GetAllUsers")
	query := users["query_schema"].(map[string]any)["properties"].(map[string]any)
	for _, key := range []string{"p", "page_size", "ps", "size", "sort_by", "sort_order"} {
		if _, ok := query[key]; !ok {
			t.Errorf("missing shared or direct query field %q", key)
		}
	}
	channel := assistantAdminOperationContract("UpdateChannel")
	if _, ok := channel["body_schema"].(map[string]any)["properties"].(map[string]any)["status"]; ok {
		t.Fatal("UpdateChannel rejects status; must use dedicated operation")
	}
	bulk := assistantAdminOperationContract("UpdateOptionsBulk")
	values := bulk["body_schema"].(map[string]any)["properties"].(map[string]any)["values"].(map[string]any)
	if values["additionalProperties"].(map[string]any)["type"] != "string" {
		t.Fatal("bulk values must be encoded strings")
	}
}
