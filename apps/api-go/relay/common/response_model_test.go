package common

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/constant"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestResponseModelSharedCompatibility(t *testing.T) {
	data, err := os.ReadFile("testdata/response_model_compatibility.json")
	require.NoError(t, err)
	var fixtures []struct {
		Name, Requested, Selected, Returned string
		Mismatch                            bool
	}
	require.NoError(t, json.Unmarshal(data, &fixtures))
	require.GreaterOrEqual(t, len(fixtures), 24)
	for _, fixture := range fixtures {
		t.Run(fixture.Name, func(t *testing.T) {
			observation := &ResponseModel{RequestedModel: fixture.Requested, UpstreamModel: fixture.Selected, ReturnedModel: fixture.Returned}
			require.Equal(t, fixture.Mismatch, observation.Mismatch())
		})
	}
}

func TestResponseModelStickyMismatchAndAliases(t *testing.T) {
	info := &RelayInfo{OriginModelName: "client-alias", ChannelMeta: &ChannelMeta{UpstreamModelName: "gpt-4o"}}
	info.ObserveResponseModel("")
	require.Nil(t, info.ResponseModel)
	info.ObserveResponseModel("gpt-4o")
	info.ObserveResponseModel("openai/gpt-4o-2024-08-06")
	info.ObserveResponseModel("gpt-4o")
	require.Equal(t, "openai/gpt-4o-2024-08-06", info.ResponseModel.ReturnedModel)
	require.False(t, info.ResponseModel.Mismatch())
	info.ObserveResponseModel("gpt-4o-mini")
	info.UpstreamModelName = "legacy-mutated-field"
	info.OriginModelName = "adapted-request"
	info.ObserveResponseModel("gpt-4o")
	info.ObserveResponseModel("")
	info.ObserveResponseModel("gpt-4.1")
	require.Equal(t, &ResponseModel{RequestedModel: "client-alias", UpstreamModel: "gpt-4o", ReturnedModel: "gpt-4o-mini"}, info.ResponseModel)
	require.True(t, info.ResponseModel.Mismatch())
}

func TestResponseModelChannelRetryResetsOnlyObservation(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	common.SetContextKey(c, constant.ContextKeyOriginalModel, "request-adapted-model")
	info := &RelayInfo{OriginModelName: "client-model", responseModelRequestedModel: "client-model"}
	info.InitChannelMeta(c)
	info.UpstreamModelName = "gpt-4o"
	info.ObserveResponseModel("gpt-4o-mini")
	info.OriginModelName = "request-adapted-model"
	info.InitChannelMeta(c)
	require.Nil(t, info.ResponseModel)
	info.UpstreamModelName = "gpt-4.1"
	info.ObserveResponseModel("gpt-4.1-2025-04-14")
	require.Equal(t, "client-model", info.ResponseModel.RequestedModel)
	require.Equal(t, "gpt-4.1", info.ResponseModel.UpstreamModel)
	require.False(t, info.ResponseModel.Mismatch())
}

func TestResponseModelEmptyDeclarationStillFreezesSelection(t *testing.T) {
	info := &RelayInfo{OriginModelName: "client-model", ChannelMeta: &ChannelMeta{UpstreamModelName: "selected-model"}}
	info.ObserveResponseModel("")
	require.Nil(t, info.ResponseModel)
	info.UpstreamModelName = ""
	info.ObserveResponseModel("returned-model")
	require.Equal(t, "selected-model", info.ResponseModel.UpstreamModel)
}
