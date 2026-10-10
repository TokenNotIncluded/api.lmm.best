package service

import (
	"encoding/json"
	"math"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/LIghtJUNction/api.lmm.best/setting/ratio_setting"
	hosttypes "github.com/LIghtJUNction/api.lmm.best/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func realtimeBillingFixture(t *testing.T, price hosttypes.PriceData) (*gorm.DB, *gin.Context, *relaycommon.RelayInfo) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	db := setupBillingSessionWalletCacheTest(t)
	require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.Log{}))
	oldLogDB, oldLogEnabled, oldBatch := model.LOG_DB, common.LogConsumeEnabled, common.BatchUpdateEnabled
	model.LOG_DB, common.LogConsumeEnabled, common.BatchUpdateEnabled = db, true, false
	t.Cleanup(func() {
		model.LOG_DB, common.LogConsumeEnabled, common.BatchUpdateEnabled = oldLogDB, oldLogEnabled, oldBatch
	})
	user := model.User{Username: t.Name(), Password: "password", Status: common.UserStatusEnabled, Quota: 1000}
	require.NoError(t, db.Create(&user).Error)
	token := model.Token{UserId: user.Id, Key: t.Name(), Name: "realtime test", RemainQuota: 1000}
	require.NoError(t, db.Create(&token).Error)
	cacheBillingTokenForTest(t, token)
	channel := model.Channel{Name: "realtime test"}
	require.NoError(t, db.Create(&channel).Error)
	info := &relaycommon.RelayInfo{UserId: user.Id, TokenId: token.Id, TokenKey: token.Key,
		OriginModelName: "gpt-realtime", UsingGroup: "default", UserGroup: "default", ForcePreConsume: true,
		PriceData: price, StartTime: time.Now(), ChannelMeta: &relaycommon.ChannelMeta{ChannelId: channel.Id},
		UserSetting: dto.UserSetting{BillingPreference: "wallet_only"}}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("GET", "/v1/realtime?model=gpt-realtime", nil)
	require.Nil(t, PreConsumeBilling(c, 1, info))
	return db, c, info
}

func assertRealtimeBalances(t *testing.T, db *gorm.DB, info *relaycommon.RelayInfo, actual int) {
	t.Helper()
	var user model.User
	var token model.Token
	require.NoError(t, db.First(&user, info.UserId).Error)
	require.NoError(t, db.First(&token, info.TokenId).Error)
	require.Equal(t, 1000-actual, user.Quota)
	require.Equal(t, 1000-actual, token.RemainQuota)
	require.Equal(t, actual, token.UsedQuota)
}

func TestRealtimeQuotaCumulativeReservationSettlesOnce(t *testing.T) {
	price := hosttypes.PriceData{ModelRatio: 1, CompletionRatio: 2, AudioRatio: 4,
		AudioCompletionRatio: 3, CacheRatio: .25, GroupRatioInfo: hosttypes.GroupRatioInfo{GroupRatio: 1}}
	db, c, info := realtimeBillingFixture(t, price)
	usage := &dto.RealtimeUsage{TotalTokens: 10, InputTokens: 10,
		InputTokenDetails: dto.InputTokenDetails{TextTokens: 10}}
	require.NoError(t, PreWssConsumeQuota(c, info, usage))
	require.Equal(t, 10, info.Billing.GetPreConsumedQuota())
	assertRealtimeBalances(t, db, info, 10)
	usage.TotalTokens, usage.InputTokens = 30, 30
	usage.InputTokenDetails.TextTokens = 30
	require.NoError(t, PreWssConsumeQuota(c, info, usage))
	require.NoError(t, PreWssConsumeQuota(c, info, usage)) // duplicate cumulative checkpoint
	require.Equal(t, 30, info.Billing.GetPreConsumedQuota())
	assertRealtimeBalances(t, db, info, 30)
	PostWssConsumeQuota(c, info, info.OriginModelName, usage, "")
	assertRealtimeBalances(t, db, info, 30)
	var log model.Log
	require.NoError(t, db.Where("type = ?", model.LogTypeConsume).First(&log).Error)
	require.Equal(t, 30, log.Quota)
	require.Equal(t, 30, log.PromptTokens)
}

func TestRealtimeQuotaUsesFrozenPricesForReservationAndSettlement(t *testing.T) {
	oldModels, oldCompletion := ratio_setting.ModelRatio2JSONString(), ratio_setting.CompletionRatio2JSONString()
	oldAudio, oldAudioCompletion, oldCache := ratio_setting.AudioRatio2JSONString(), ratio_setting.AudioCompletionRatio2JSONString(), ratio_setting.CacheRatio2JSONString()
	t.Cleanup(func() {
		require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(oldModels))
		require.NoError(t, ratio_setting.UpdateCompletionRatioByJSONString(oldCompletion))
		require.NoError(t, ratio_setting.UpdateAudioRatioByJSONString(oldAudio))
		require.NoError(t, ratio_setting.UpdateAudioCompletionRatioByJSONString(oldAudioCompletion))
		require.NoError(t, ratio_setting.UpdateCacheRatioByJSONString(oldCache))
	})
	price := hosttypes.PriceData{ModelRatio: 1, CompletionRatio: 2, AudioRatio: 4,
		AudioCompletionRatio: 3, CacheRatio: .25, GroupRatioInfo: hosttypes.GroupRatioInfo{GroupRatio: 1}}
	db, c, info := realtimeBillingFixture(t, price)
	for _, update := range []func(string) error{ratio_setting.UpdateModelRatioByJSONString,
		ratio_setting.UpdateCompletionRatioByJSONString, ratio_setting.UpdateAudioRatioByJSONString,
		ratio_setting.UpdateAudioCompletionRatioByJSONString, ratio_setting.UpdateCacheRatioByJSONString} {
		require.NoError(t, update(`{"gpt-realtime":99}`))
	}
	usage := &dto.RealtimeUsage{TotalTokens: 40, InputTokens: 30, OutputTokens: 10,
		InputTokenDetails: dto.InputTokenDetails{TextTokens: 20, AudioTokens: 10, CachedTokens: 10,
			CachedTokensDetails: &dto.CachedTokenDetails{TextTokens: 8, AudioTokens: 2}},
		OutputTokenDetails: dto.OutputTokenDetails{TextTokens: 5, AudioTokens: 5}}
	// 20 text - 8*.75 text-cache discount + 10*4 audio + 5*2 + 5*4*3 = 124.
	// The audio-cache rate is not captured yet, so its 2 tokens stay full price.
	require.NoError(t, PreWssConsumeQuota(c, info, usage))
	require.Equal(t, 124, info.Billing.GetPreConsumedQuota())
	PostWssConsumeQuota(c, info, info.OriginModelName, usage, "")
	assertRealtimeBalances(t, db, info, 124)
	var log model.Log
	require.NoError(t, db.Where("type = ?", model.LogTypeConsume).First(&log).Error)
	var other map[string]any
	require.NoError(t, json.Unmarshal([]byte(log.Other), &other))
	require.Equal(t, true, other["unsupported_audio_cache_pricing"])
	require.EqualValues(t, 4, other["audio_ratio"])
}

func TestRealtimeQuotaFixedPriceUsesSnapshotAndEmptyUsageRefunds(t *testing.T) {
	for _, tc := range []struct {
		name  string
		usage *dto.RealtimeUsage
		price float64
		want  int
	}{
		{"fixed with actual input", &dto.RealtimeUsage{InputTokens: 1}, 25 / common.QuotaPerUnit, 25},
		{"fixed with actual modality", &dto.RealtimeUsage{InputTokenDetails: dto.InputTokenDetails{AudioTokens: 1}}, 25 / common.QuotaPerUnit, 25},
		{"fixed without usage", &dto.RealtimeUsage{}, 25 / common.QuotaPerUnit, 0},
		{"explicit zero price", &dto.RealtimeUsage{InputTokens: 1}, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			price := hosttypes.PriceData{UsePrice: true, ModelPrice: tc.price, GroupRatioInfo: hosttypes.GroupRatioInfo{GroupRatio: 1}}
			db, c, info := realtimeBillingFixture(t, price)
			// RelayInfo.UsePrice is deliberately false: PriceData owns billing.
			require.NoError(t, PreWssConsumeQuota(c, info, tc.usage))
			PostWssConsumeQuota(c, info, info.OriginModelName, tc.usage, "")
			assertRealtimeBalances(t, db, info, tc.want)
		})
	}
}

func TestRealtimeQuotaReservationRejectsInvalidUsageAndMissingOwner(t *testing.T) {
	price := hosttypes.PriceData{ModelRatio: 1, CompletionRatio: 1, AudioRatio: 1,
		AudioCompletionRatio: 1, CacheRatio: .25, GroupRatioInfo: hosttypes.GroupRatioInfo{GroupRatio: 1}}
	for _, usage := range []*dto.RealtimeUsage{
		{InputTokens: -1},
		{InputTokens: 2, InputTokenDetails: dto.InputTokenDetails{AudioTokens: 3}},
		{InputTokens: math.MaxInt, OutputTokens: 1},
		{InputTokenDetails: dto.InputTokenDetails{TextTokens: math.MaxInt, AudioTokens: 1}},
		{InputTokens: 10, InputTokenDetails: dto.InputTokenDetails{TextTokens: 10, CachedTokens: 4, CachedTokensDetails: &dto.CachedTokenDetails{TextTokens: 5}}},
	} {
		info := &relaycommon.RelayInfo{PriceData: price}
		_, _, err := realtimeReservationQuota(info, usage)
		require.Error(t, err)
		require.Error(t, ValidateRealtimeUsage(usage))
	}
	info := &relaycommon.RelayInfo{PriceData: price}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	require.ErrorContains(t, PreWssConsumeQuota(c, info, &dto.RealtimeUsage{InputTokens: 1}), "billing session is missing")
	info.PriceData = hosttypes.PriceData{FreeModel: true}
	require.NoError(t, PreWssConsumeQuota(c, info, &dto.RealtimeUsage{InputTokens: 1}))
	info.PriceData = price
	info.PriceData.ModelRatio = math.MaxFloat64
	quota, _, err := realtimeReservationQuota(info, &dto.RealtimeUsage{InputTokens: 10})
	var clamp *common.QuotaClamp
	require.ErrorAs(t, err, &clamp)
	require.Equal(t, common.MaxQuota, quota, "settlement retains the capped measured cost")
	require.NotNil(t, info.QuotaClamp)
	info.PriceData.ModelRatio = math.NaN()
	_, _, err = realtimeReservationQuota(info, &dto.RealtimeUsage{InputTokens: 1})
	require.Error(t, err)
}

func TestRealtimeQuotaUnclassifiedCacheRetainsFullPrice(t *testing.T) {
	info := &relaycommon.RelayInfo{PriceData: hosttypes.PriceData{ModelRatio: 1, CacheRatio: .25,
		GroupRatioInfo: hosttypes.GroupRatioInfo{GroupRatio: 1}}}
	usage := &dto.RealtimeUsage{InputTokens: 10,
		InputTokenDetails: dto.InputTokenDetails{TextTokens: 10, CachedTokens: 8}}
	quota, _, err := realtimeReservationQuota(info, usage)
	require.NoError(t, err)
	require.Equal(t, 10, quota, "an aggregate cache count does not identify its modality")
	usage.InputTokenDetails.CachedTokensDetails = &dto.CachedTokenDetails{TextTokens: 8}
	quota, _, err = realtimeReservationQuota(info, usage)
	require.NoError(t, err)
	require.Equal(t, 4, quota, "only the measured text-cache subset receives the frozen discount")
}
