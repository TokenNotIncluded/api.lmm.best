package controller

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMerchantStoreRefundAssistantContractsAreStrictAndDescribeActualMoney(t *testing.T) {
	for _, name := range []string{"RequestMerchantStoreRefund", "ProactivelyRefundMerchantStoreOrder", "DecideMerchantStoreRefund", "GetMerchantStorePickupRefunds", "RequestMerchantStorePickupRefund"} {
		c := assistantAdminOperationContract(name)
		require.Equal(t, "derived", c["contract_status"], name)
		body := c["body_schema"].(map[string]any)
		require.Equal(t, false, body["additionalProperties"])
		require.NotEmpty(t, body["required"])
		fields := body["properties"].(map[string]any)
		require.NotContains(t, fields, "buyer_id")
		require.NotContains(t, fields, "payout_account")
		require.NotContains(t, fields, "completed")
	}
	request := assistantAdminOperationContract("RequestMerchantStoreRefund")
	body := request["body_schema"].(map[string]any)
	fields := body["properties"].(map[string]any)
	for _, name := range []string{"quantity", "amount_quota", "amount_minor"} {
		require.Equal(t, "integer", fields[name].(map[string]any)["type"])
	}
	require.Equal(t, []any{"request_key", "reason", "mode"}, body["required"])
	pickup := assistantAdminOperationContract("RequestMerchantStorePickupRefund")["body_schema"].(map[string]any)
	require.Equal(t, []any{"order_id", "token", "input"}, pickup["required"])
}
