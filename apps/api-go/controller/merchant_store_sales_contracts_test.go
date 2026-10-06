package controller

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMerchantStoreSalesContractsRequireExplicitNullableCeilingAndBoolean(t *testing.T) {
	limit := assistantAdminOperationContract("SetMerchantStoreProductSaleLimit")
	require.Equal(t, "derived", limit["contract_status"])
	body := limit["body_schema"].(map[string]any)
	require.Equal(t, []any{"sale_limit"}, body["required"])
	property := body["properties"].(map[string]any)["sale_limit"].(map[string]any)
	branches := property["anyOf"].([]any)
	require.Equal(t, "integer", branches[0].(map[string]any)["type"])
	require.EqualValues(t, 0, branches[0].(map[string]any)["minimum"])
	require.EqualValues(t, 9007199254740991, branches[0].(map[string]any)["maximum"])
	require.Equal(t, "null", branches[1].(map[string]any)["type"])
	listing := assistantAdminOperationContract("SetMerchantStoreProductListed")
	require.Equal(t, "derived", listing["contract_status"])
	body = listing["body_schema"].(map[string]any)
	require.Equal(t, []any{"listed"}, body["required"])
	property = body["properties"].(map[string]any)["listed"].(map[string]any)
	require.Equal(t, "boolean", property["type"])
	require.NotContains(t, property, "anyOf")
}
