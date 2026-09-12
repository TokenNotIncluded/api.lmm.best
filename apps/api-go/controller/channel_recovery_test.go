package controller

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/constant"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/LIghtJUNction/api.lmm.best/setting/ratio_setting"
	"github.com/stretchr/testify/require"
)

func TestChannelRecoveryHTTP(t *testing.T) {
	for _, tc := range []struct {
		name     string
		statuses map[int]int
		success  map[string]bool
		cancel   bool
	}{
		{"all-disabled-one-success", map[int]int{0: 3, 1: 3}, map[string]bool{"a": true}, false},
		{"all-success", map[int]int{0: 3, 1: 3}, map[string]bool{"a": true, "b": true}, false},
		{"all-failed", map[int]int{0: 3, 1: 3}, nil, false},
		{"manual", map[int]int{0: 2, 1: 3}, map[string]bool{"b": true}, false},
		{"partial", map[int]int{1: 3}, map[string]bool{"b": true}, false},
		{"cancel", map[int]int{0: 3, 1: 3}, map[string]bool{"a": true}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := setupModelListControllerTestDB(t)
			oldEnable, oldMemory, oldInterval := common.AutomaticEnableChannelEnabled, common.MemoryCacheEnabled, common.RequestInterval
			common.AutomaticEnableChannelEnabled, common.MemoryCacheEnabled, common.RequestInterval = true, false, 0
			t.Cleanup(func() {
				common.AutomaticEnableChannelEnabled, common.MemoryCacheEnabled, common.RequestInterval = oldEnable, oldMemory, oldInterval
			})
			ratios := ratio_setting.ModelRatio2JSONString()
			require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"bge-reranker-v2-m3":1}`))
			t.Cleanup(func() { require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(ratios)) })
			user := model.User{Username: "probe", Group: "default", Status: common.UserStatusEnabled, Quota: 1000000}
			require.NoError(t, db.Create(&user).Error)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var called []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				key := r.Header.Get("Authorization")[7:]
				called = append(called, key)
				require.Equal(t, "/v1/rerank", r.URL.Path)
				var body map[string]any
				require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
				require.NotEmpty(t, body["query"])
				require.Len(t, body["documents"], 2)
				var stored model.Channel
				require.NoError(t, db.First(&stored).Error)
				_, index, err := stored.GetNextEnabledKey()
				if err == nil {
					require.NotEqual(t, common.ChannelStatusAutoDisabled, stored.ChannelInfo.MultiKeyStatusList[index])
				}
				if tc.cancel {
					cancel()
				}
				w.Header().Set("Content-Type", "application/json")
				if !tc.success[key] {
					w.WriteHeader(401)
					_, _ = w.Write([]byte(`{"error":{"message":"bad key","type":"invalid_api_key"}}`))
					return
				}
				_, _ = w.Write([]byte(`{"results":[{"index":0,"relevance_score":0.9}],"usage":{"total_tokens":2}}`))
			}))
			defer server.Close()
			service.InitHttpClient()
			channel := model.Channel{Type: constant.ChannelTypeOpenAI, Name: "probe", Key: "a\nb", Models: "bge-reranker-v2-m3", BaseURL: &server.URL, Status: common.ChannelStatusAutoDisabled, ChannelInfo: model.ChannelInfo{IsMultiKey: true, MultiKeySize: 2, MultiKeyStatusList: tc.statuses}}
			if tc.name == "partial" {
				channel.Status = common.ChannelStatusEnabled
			}
			require.NoError(t, db.Create(&channel).Error)
			summary := testChannelForHealthCheck(ctx, &channel, user.Id, false, 100000)
			var stored model.Channel
			require.NoError(t, db.First(&stored, channel.Id).Error)
			expectedCalls, recovered := 0, 0
			for i, key := range []string{"a", "b"} {
				if tc.statuses[i] == common.ChannelStatusAutoDisabled {
					expectedCalls++
				}
				if tc.statuses[i] == common.ChannelStatusAutoDisabled && tc.success[key] && !tc.cancel {
					recovered++
					require.Zero(t, stored.ChannelInfo.MultiKeyStatusList[i])
				} else {
					require.Equal(t, tc.statuses[i], stored.ChannelInfo.MultiKeyStatusList[i])
				}
			}
			if tc.cancel {
				expectedCalls = 1
			}
			if tc.name == "partial" {
				expectedCalls++ // The enabled key still receives its ordinary health check.
			}
			require.Len(t, called, expectedCalls)
			require.Equal(t, recovered, summary.Enabled)
			if recovered > 0 || tc.name == "partial" {
				require.Equal(t, common.ChannelStatusEnabled, stored.Status)
			} else {
				require.Equal(t, common.ChannelStatusAutoDisabled, stored.Status)
			}
		})
	}
}

func TestRecoveryWritebackRejectsChangedState(t *testing.T) {
	db := setupModelListControllerTestDB(t)
	for _, tc := range []struct {
		name      string
		status    int
		key       string
		keyStatus int
	}{
		{"manual-channel", common.ChannelStatusManuallyDisabled, "a", common.ChannelStatusAutoDisabled},
		{"manual-key", common.ChannelStatusEnabled, "a", common.ChannelStatusManuallyDisabled},
		{"rotated-key", common.ChannelStatusAutoDisabled, "replacement", common.ChannelStatusAutoDisabled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			channel := model.Channel{Key: tc.key, Status: tc.status, ChannelInfo: model.ChannelInfo{IsMultiKey: true, MultiKeyStatusList: map[int]int{0: tc.keyStatus}}}
			require.NoError(t, db.Create(&channel).Error)
			require.False(t, model.RecoverChannelKey(channel.Id, 0, "a"))
			var stored model.Channel
			require.NoError(t, db.First(&stored, channel.Id).Error)
			require.Equal(t, tc.status, stored.Status)
			require.Equal(t, tc.keyStatus, stored.ChannelInfo.MultiKeyStatusList[0])
		})
	}
}
