package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/constant"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNativeVoiceChannelTestUsesSessionProtocol(t *testing.T) {
	for _, tt := range []struct {
		modelName string
		endpoint  constant.EndpointType
		path      string
	}{
		{"gpt-live-1", constant.EndpointTypeLive, "/v1/live/sessions"},
		{"gpt-live-transcribe", constant.EndpointTypeRealtimeTranscription, "/v1/realtime?intent=transcription"},
		{"gpt-realtime-whisper", constant.EndpointTypeRealtimeTranscription, "/v1/realtime?intent=transcription"},
		{"gpt-realtime-translate", constant.EndpointTypeRealtimeTranslation, "/v1/realtime/translations"},
	} {
		for _, channelType := range []int{constant.ChannelTypeOpenAI, constant.ChannelTypeNewAPI} {
			mapping, err := json.Marshal(map[string]string{"voice-alias": "intermediate", "intermediate": tt.modelName})
			require.NoError(t, err)
			mappingJSON := string(mapping)
			channel := &model.Channel{Type: channelType, ModelMapping: &mappingJSON}
			for _, modelName := range []string{tt.modelName, "voice-alias"} {
				assert.Equal(t, string(tt.endpoint), normalizeChannelTestModelEndpoint(channel, modelName, ""))
				assert.Equal(t, tt.path, resolveChannelTestRequestPath(channel, modelName, ""))
				assert.Nil(t, buildTestRequest(modelName, "", channel, true), "session protocols must never build a chat probe")
			}
		}
	}
}

func TestNativeVoiceChannelTestsSkipWithoutUpstream(t *testing.T) {
	for _, modelName := range []string{"gpt-live-1", "gpt-live-transcribe", "gpt-realtime-whisper", "gpt-realtime-translate"} {
		t.Run(modelName, func(t *testing.T) {
			db := setupModelListControllerTestDB(t)
			previousMemory := common.MemoryCacheEnabled
			previousDisable, previousEnable := common.AutomaticDisableChannelEnabled, common.AutomaticEnableChannelEnabled
			common.MemoryCacheEnabled, common.AutomaticDisableChannelEnabled, common.AutomaticEnableChannelEnabled = false, true, true
			t.Cleanup(func() {
				common.MemoryCacheEnabled = previousMemory
				common.AutomaticDisableChannelEnabled, common.AutomaticEnableChannelEnabled = previousDisable, previousEnable
			})
			var called atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				called.Add(1)
				w.WriteHeader(http.StatusBadGateway)
			}))
			defer server.Close()
			mapping, err := json.Marshal(map[string]string{"voice-alias": modelName})
			require.NoError(t, err)
			mappingJSON := string(mapping)
			autoBan := 1
			channel := model.Channel{Type: constant.ChannelTypeOpenAI, Name: "native voice", Models: "voice-alias", Key: "test-key", BaseURL: &server.URL, ModelMapping: &mappingJSON, Status: common.ChannelStatusEnabled, ResponseTime: 321, AutoBan: &autoBan}
			require.NoError(t, db.Create(&channel).Error)
			for _, endpoint := range []string{"", string(constant.EndpointTypeOpenAI)} {
				result := testChannel(context.Background(), &channel, 999, "", endpoint, true)
				require.True(t, result.skipped)
				require.ErrorContains(t, result.localErr, "实时会话客户端")
				assert.Nil(t, result.newAPIError, "unsupported probes must not be classified as provider failures")
			}
			summary := testChannelForHealthCheck(context.Background(), &channel, 999, true, 0)
			assert.Equal(t, channelTestSummary{}, summary)
			var stored model.Channel
			require.NoError(t, db.First(&stored, channel.Id).Error)
			assert.Equal(t, common.ChannelStatusEnabled, stored.Status)
			assert.Equal(t, 321, stored.ResponseTime)
			channel.ChannelInfo = model.ChannelInfo{IsMultiKey: true, MultiKeySize: 1, MultiKeyStatusList: map[int]int{0: common.ChannelStatusAutoDisabled}}
			recovery := recoverChannelKeys(context.Background(), &channel, 999)
			assert.Equal(t, channelTestSummary{}, recovery)
			assert.Equal(t, common.ChannelStatusAutoDisabled, channel.ChannelInfo.MultiKeyStatusList[0])
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Params = gin.Params{{Key: "id", Value: fmt.Sprint(channel.Id)}}
			c.Set("id", 999)
			c.Request = httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/channel/test/%d?stream=true", channel.Id), nil)
			TestChannel(c)
			require.Equal(t, http.StatusOK, recorder.Code)
			var response struct {
				Success bool   `json:"success"`
				Skipped bool   `json:"skipped"`
				Message string `json:"message"`
			}
			require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
			assert.False(t, response.Success)
			assert.True(t, response.Skipped)
			assert.Contains(t, response.Message, "实时会话客户端")
			assert.Zero(t, called.Load(), "channel probes must not open or charge a voice session")
		})
	}
}
