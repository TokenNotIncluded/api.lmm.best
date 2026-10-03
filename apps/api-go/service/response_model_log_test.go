package service

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestResponseModelConsumeLogContract(t *testing.T) {
	for _, tt := range []struct {
		name, requested, selected, returned string
		useful                              bool
	}{
		{"no declaration", "gpt-4o", "gpt-4o", "", false},
		{"ordinary exact", "gpt-4o", "gpt-4o", "gpt-4o", false},
		{"provider alias", "gpt-4o", "gpt-4o", "openai/gpt-4o-2024-08-06", true},
		{"mapped selected", "client-model", "gpt-4o", "gpt-4o", true},
		{"mapped requested", "client-model", "gpt-4o", "client-model", true},
		{"real replacement", "gpt-4o", "gpt-4o", "gpt-4o-mini", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			info := &relaycommon.RelayInfo{OriginModelName: tt.requested, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: tt.selected}}
			info.ObserveResponseModel(tt.returned)
			other := GenerateTextOtherInfo(c, info, 2, 3, 4, 5, 0.1, 6, 7)
			_, present := other["response_model"]
			require.Equal(t, tt.useful, present)
			require.Equal(t, float64(2), other["model_ratio"])
			require.Equal(t, float64(6), other["model_price"])
			if !tt.useful {
				return
			}
			data, err := json.Marshal(other["response_model"])
			require.NoError(t, err)
			var names map[string]string
			require.NoError(t, json.Unmarshal(data, &names))
			require.Equal(t, map[string]string{"requested_model": tt.requested, "upstream_model": tt.selected, "returned_model": tt.returned}, names)
			info.ResponseModel.ReturnedModel = "later-mutated-observation"
			snapshot, err := json.Marshal(other["response_model"])
			require.NoError(t, err)
			require.Equal(t, string(data), string(snapshot))
		})
	}
}
