package advancedcustom

import (
	"encoding/json"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/stretchr/testify/require"
)

func TestAdaptorBuildBalanceQueryKeepsDefaultGETAndSupportsJSONPOST(t *testing.T) {
	body := `{"credential":"{api_key}"}`
	for _, config := range []*dto.AdvancedCustomBalanceConfig{nil, {Method: "POST"}, {Method: "POST", BodyTemplate: &body}} {
		info := advancedCustomRelayInfo(&dto.AdvancedCustomConfig{Routes: []dto.AdvancedCustomRoute{
			{IncomingPath: dto.AdvancedCustomBalancePath, UpstreamPath: "https://balance.example/account", Balance: config},
		}})
		query, err := (&Adaptor{}).BuildBalanceQuery(info)
		require.NoError(t, err)
		require.Equal(t, "https://balance.example/account", query.URL)
		require.Equal(t, "Bearer sk-test", query.Header.Get("Authorization"))
		if config == nil {
			require.Equal(t, "GET", query.Method)
			require.Nil(t, query.Body)
		} else {
			require.Equal(t, "POST", query.Method)
			if config.BodyTemplate == nil {
				require.Nil(t, query.Body)
				continue
			}
			require.Equal(t, "application/json", query.Header.Get("Content-Type"))
			var value map[string]string
			require.NoError(t, json.Unmarshal(query.Body, &value))
			require.Equal(t, map[string]string{"credential": "sk-test"}, value)
		}
	}
}
