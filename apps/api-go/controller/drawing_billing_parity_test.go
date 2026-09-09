package controller

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/constant"
	"github.com/LIghtJUNction/api.lmm.best/i18n"
	"github.com/LIghtJUNction/api.lmm.best/middleware"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/types"
	"github.com/LIghtJUNction/api.lmm.best/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

const drawingParityModel = "gpt-image-1-billing-parity"

type drawingParityFixture struct {
	db       *gorm.DB
	user     model.User
	token    model.Token
	channel  model.Channel
	upstream atomic.Int32
}

// This uses only the existing isolated SQL unit-test fixture and a loopback
// mock provider. It is not a production/strict-PostgreSQL acceptance rehearsal.
func newDrawingParityFixture(t *testing.T, quota int, upstreamStatus int) *drawingParityFixture {
	t.Helper()
	previousDB, previousLogDB := model.DB, model.LOG_DB
	previousMainType, previousLogType := common.MainDatabaseType(), common.LogDatabaseType()
	previousRedis, previousMemory := common.RedisEnabled, common.MemoryCacheEnabled
	previousDrawing, previousLogs := common.DrawingEnabled, common.LogConsumeEnabled
	previousPrices, err := json.Marshal(ratio_setting.GetModelPriceCopy())
	require.NoError(t, err)
	t.Cleanup(func() {
		model.DB, model.LOG_DB = previousDB, previousLogDB
		common.SetDatabaseTypes(previousMainType, previousLogType)
		common.RedisEnabled, common.MemoryCacheEnabled = previousRedis, previousMemory
		common.DrawingEnabled, common.LogConsumeEnabled = previousDrawing, previousLogs
		require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(string(previousPrices)))
		model.InvalidatePricingCache()
	})
	initModelListColumnNames(t)
	common.MemoryCacheEnabled = false
	common.DrawingEnabled, common.LogConsumeEnabled = true, true
	require.NoError(t, i18n.Init())
	db, user := createAssistantKeyFixture(t, "drawing-billing-owner")
	require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.Ability{}, &model.Model{}, &model.Vendor{}))
	setAssistantKeyOption(t, db, "UserUsableGroups", `{"default":"Default","image-2":"Drawing"}`)
	setAssistantKeyOption(t, db, "GroupRatio", `{"default":1,"image-2":1}`)
	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{"`+drawingParityModel+`":0.12}`))
	levelOne := model.TrustLevelMinUser + 1
	user.TrustLevelOverride = &levelOne
	user.Quota = quota
	user.Setting = `{"billing_preference":"wallet_only"}`
	require.NoError(t, db.Save(&user).Error)
	fixture := &drawingParityFixture{db: db, user: user}
	fixture.token = model.Token{
		UserId: user.Id, Key: strings.Repeat("b", 48), Name: "drawing billing parity",
		Group: "image-2", Status: common.TokenStatusEnabled, ExpiredTime: -1,
		RemainQuota: common.GetTrustQuota(), UnlimitedQuota: false,
	}
	require.NoError(t, db.Create(&fixture.token).Error)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fixture.upstream.Add(1)
		if r.URL.Path != "/v1/images/generations" {
			http.Error(w, "unexpected mock-provider route", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(upstreamStatus)
		if upstreamStatus != http.StatusOK {
			_, _ = w.Write([]byte(`{"error":{"message":"mock provider rejected request","type":"invalid_request_error","code":"invalid_request"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"created":1,"data":[{"b64_json":"cGFyaXR5LTE="},{"b64_json":"cGFyaXR5LTI="}]}`))
	}))
	t.Cleanup(upstream.Close)
	fixture.channel = model.Channel{
		Type: constant.ChannelTypeOpenAI, Status: common.ChannelStatusEnabled,
		Name: "mock drawing billing", Key: "mock-upstream-key", BaseURL: &upstream.URL,
		Models: drawingParityModel, Group: "image-2",
	}
	require.NoError(t, db.Create(&fixture.channel).Error)
	require.NoError(t, fixture.channel.AddAbilities(nil))
	model.InvalidatePricingCache()
	require.Eventually(t, func() bool {
		return assistantDrawingModelAllowed("default", "image-2", drawingParityModel)
	}, 3*time.Second, 10*time.Millisecond, "the isolated drawing model must be in the live catalog")
	return fixture
}

func (fixture *drawingParityFixture) request(t *testing.T, entry string) (int, string) {
	t.Helper()
	admission := middleware.RelayRequestAdmission()
	if entry == "mcp" {
		result, err := executeDrawingMCPRelay(context.Background(), newDrawingMCPRelayEngine(admission), fixture.user.ToBaseUser(), drawingMCPGenerateInput{
			Prompt: "a local billing fixture", Model: drawingParityModel, Group: "image-2", N: 2,
		})
		if err != nil {
			return http.StatusBadRequest, err.Error()
		}
		encoded, err := json.Marshal(result)
		require.NoError(t, err)
		return http.StatusOK, string(encoded)
	}
	engine := gin.New()
	engine.Use(middleware.BodyStorageCleanup())
	path := "/v1/images/generations"
	if entry == "web" {
		path = "/pg/images/generations"
		engine.POST(path, func(c *gin.Context) {
			// Browser session authentication is covered separately. Exercise its
			// resulting owner identity, then the real key auth and shared relay.
			c.Set("id", fixture.user.Id)
			c.Set("session_id", "drawing-billing-session")
			c.Set(common.RequestIdKey, common.NewRequestId())
			common.SetContextKey(c, constant.ContextKeyRequestStartTime, time.Now())
			c.Next()
		}, admission, PreparePlaygroundImageAuth, middleware.Distribute(), PlaygroundImage)
	} else {
		engine.POST(path, middleware.TokenAuth(), admission, middleware.Distribute(), func(c *gin.Context) {
			Relay(c, types.RelayFormatOpenAIImage)
		})
	}
	request := httptest.NewRequest(http.MethodPost, path+"?group=image-2", strings.NewReader(`{"model":"`+drawingParityModel+`","prompt":"a local billing fixture","n":2}`))
	request.Header.Set("Content-Type", "application/json")
	if entry == "api" {
		request.Header.Set("Authorization", "Bearer "+fixture.token.Key)
	}
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	return response.Code, response.Body.String()
}

func TestDrawingRelayBillingAndUsageLogsMatchAcrossWebMCPAndAPI(t *testing.T) {
	var expectedCharge int
	for _, entry := range []string{"web", "mcp", "api"} {
		t.Run(entry, func(t *testing.T) {
			fixture := newDrawingParityFixture(t, common.GetTrustQuota(), http.StatusOK)
			status, body := fixture.request(t, entry)
			require.Equal(t, http.StatusOK, status, body)
			require.EqualValues(t, 1, fixture.upstream.Load())
			var user model.User
			var token model.Token
			require.NoError(t, fixture.db.First(&user, fixture.user.Id).Error)
			require.NoError(t, fixture.db.First(&token, fixture.token.Id).Error)
			charge := fixture.user.Quota - user.Quota
			require.Positive(t, charge)
			if expectedCharge == 0 {
				expectedCharge = charge
			}
			require.Equal(t, expectedCharge, charge)
			require.Equal(t, charge, fixture.token.RemainQuota-token.RemainQuota)
			require.Equal(t, charge, token.UsedQuota)
			require.Equal(t, charge, user.UsedQuota)
			var logs []model.Log
			require.NoError(t, fixture.db.Where("user_id = ? AND type = ?", user.Id, model.LogTypeConsume).Find(&logs).Error)
			require.Len(t, logs, 1)
			require.Equal(t, charge, logs[0].Quota)
			require.Equal(t, token.Id, logs[0].TokenId)
			require.Equal(t, token.Name, logs[0].TokenName)
			require.Equal(t, "image-2", logs[0].Group)
			require.Equal(t, drawingParityModel, logs[0].ModelName)
			require.Equal(t, fixture.channel.Id, logs[0].ChannelId)
			require.NotContains(t, body, token.Key)
			require.NotContains(t, logs[0].Other, token.Key)
		})
	}
}

func TestDrawingRelayUpstreamFailureRefundsRealKeyAndWallet(t *testing.T) {
	for _, entry := range []string{"web", "mcp", "api"} {
		t.Run(entry, func(t *testing.T) {
			fixture := newDrawingParityFixture(t, common.GetTrustQuota(), http.StatusBadRequest)
			status, _ := fixture.request(t, entry)
			require.GreaterOrEqual(t, status, http.StatusBadRequest)
			require.EqualValues(t, 1, fixture.upstream.Load())
			// Refund runs in the existing bounded asynchronous billing path.
			require.Eventually(t, func() bool {
				var user model.User
				var token model.Token
				return fixture.db.First(&user, fixture.user.Id).Error == nil &&
					fixture.db.First(&token, fixture.token.Id).Error == nil &&
					user.Quota == fixture.user.Quota && token.RemainQuota == fixture.token.RemainQuota
			}, 3*time.Second, 10*time.Millisecond)
			var count int64
			require.NoError(t, fixture.db.Model(&model.Log{}).Where("user_id = ? AND type = ?", fixture.user.Id, model.LogTypeConsume).Count(&count).Error)
			require.Zero(t, count)
		})
	}
}
