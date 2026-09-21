package middleware

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/constant"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAuthenticatedDistributorFiltersEndpointCapabilities(t *testing.T) {
	gin.SetMode(gin.TestMode)
	previousDB := model.DB
	previousSQLitePath := common.SQLitePath
	previousRedisEnabled := common.RedisEnabled
	previousMemoryCacheEnabled := common.MemoryCacheEnabled
	common.RedisEnabled = false
	common.MemoryCacheEnabled = false
	common.SQLitePath = filepath.Join(t.TempDir(), "relay-capability.db")
	t.Setenv("SQL_DSN", "")
	require.NoError(t, model.InitDB())
	db := model.DB
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.Token{}, &model.Channel{}, &model.Ability{}, &model.UserRankingRevision{}, &model.PublicRelayPreference{}))
	require.NoError(t, model.EnsureUserRankingRevisionState(db))
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
		model.DB = previousDB
		common.SQLitePath = previousSQLitePath
		common.RedisEnabled = previousRedisEnabled
		common.MemoryCacheEnabled = previousMemoryCacheEnabled
	})

	require.NoError(t, db.Create(&model.User{
		Id:       1,
		Username: "relay-capability-user",
		Password: "not-used-password",
		Role:     common.RoleRootUser,
		Status:   common.UserStatusEnabled,
		Group:    "default",
		Quota:    1_000_000,
	}).Error)
	require.NoError(t, db.Create(&model.Token{
		Id:             1,
		UserId:         1,
		Key:            "relaycapabilitytoken",
		Status:         common.TokenStatusEnabled,
		Name:           "relay-capability",
		ExpiredTime:    -1,
		UnlimitedQuota: true,
		Group:          "default",
	}).Error)

	highPriority := int64(10)
	lowPriority := int64(0)
	unsupported := &model.Channel{Id: 11, Type: constant.ChannelTypeBaidu, Key: "key", Status: common.ChannelStatusEnabled, Name: "baidu", Models: "rerank-model", Group: "default", Priority: &highPriority}
	compatible := &model.Channel{Id: 12, Type: constant.ChannelTypeOpenAI, Key: "key", Status: common.ChannelStatusEnabled, Name: "openai", Models: "rerank-model", Group: "default", Priority: &lowPriority}
	require.NoError(t, db.Create(unsupported).Error)
	require.NoError(t, db.Create(compatible).Error)
	require.NoError(t, db.Create(&model.Ability{Group: "default", Model: "rerank-model", ChannelId: unsupported.Id, Enabled: true, Priority: &highPriority, Weight: 1}).Error)
	require.NoError(t, db.Create(&model.Ability{Group: "default", Model: "rerank-model", ChannelId: compatible.Id, Enabled: true, Priority: &lowPriority, Weight: 1}).Error)

	for _, tt := range []struct {
		name       string
		channelID  int
		wantStatus int
		wantCode   string
		wantCalled bool
	}{
		{name: "unsupported channel rejected", channelID: unsupported.Id, wantStatus: http.StatusBadRequest, wantCode: "channel:unsupported_endpoint"},
		{name: "compatible channel routes", channelID: compatible.Id, wantStatus: http.StatusNoContent, wantCalled: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			called := false
			router := gin.New()
			router.POST("/v1/rerank",
				TokenAuth(),
				func(c *gin.Context) {
					common.SetContextKey(c, constant.ContextKeyTokenSpecificChannelId, strconv.Itoa(tt.channelID))
					c.Next()
				},
				Distribute(),
				func(c *gin.Context) {
					called = true
					c.Status(http.StatusNoContent)
				},
			)
			request := httptest.NewRequest(http.MethodPost, "/v1/rerank", strings.NewReader(`{"model":"rerank-model","query":"q","documents":["d"]}`))
			request.Header.Set("Authorization", "Bearer sk-relaycapabilitytoken")
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()

			router.ServeHTTP(response, request)

			require.Equal(t, tt.wantStatus, response.Code, response.Body.String())
			require.Equal(t, tt.wantCalled, called)
			if tt.wantCode != "" {
				require.Contains(t, response.Body.String(), tt.wantCode)
				require.NotEqual(t, "null", strings.TrimSpace(response.Body.String()))
			}
		})
	}

	t.Run("selector skips higher-priority unsupported channel", func(t *testing.T) {
		called := false
		router := gin.New()
		router.POST("/v1/rerank", TokenAuth(), Distribute(), func(c *gin.Context) {
			called = true
			require.Equal(t, compatible.Id, common.GetContextKeyInt(c, constant.ContextKeyChannelId))
			c.Status(http.StatusNoContent)
		})
		request := httptest.NewRequest(http.MethodPost, "/v1/rerank", strings.NewReader(`{"model":"rerank-model","query":"q","documents":["d"]}`))
		request.Header.Set("Authorization", "Bearer sk-relaycapabilitytoken")
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()

		router.ServeHTTP(response, request)

		require.Equal(t, http.StatusNoContent, response.Code, response.Body.String())
		require.True(t, called)
	})

	t.Run("selector returns deterministic error when every channel is unsupported", func(t *testing.T) {
		require.NoError(t, db.Model(&model.Ability{}).Where("channel_id = ?", compatible.Id).Update("enabled", false).Error)
		t.Cleanup(func() {
			_ = db.Model(&model.Ability{}).Where("channel_id = ?", compatible.Id).Update("enabled", true).Error
		})
		router := gin.New()
		router.POST("/v1/rerank", TokenAuth(), Distribute(), func(c *gin.Context) {
			t.Fatal("unsupported selector unexpectedly reached handler")
		})
		request := httptest.NewRequest(http.MethodPost, "/v1/rerank", strings.NewReader(`{"model":"rerank-model","query":"q","documents":["d"]}`))
		request.Header.Set("Authorization", "Bearer sk-relaycapabilitytoken")
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()

		router.ServeHTTP(response, request)

		require.Equal(t, http.StatusBadRequest, response.Code, response.Body.String())
		require.Contains(t, response.Body.String(), "channel:unsupported_endpoint")
		require.NotEqual(t, "null", strings.TrimSpace(response.Body.String()))
	})
}

func TestChannelSupportsRequestPathDistinguishesTencentDispatch(t *testing.T) {
	native := &model.Channel{Type: constant.ChannelTypeTencent, Key: "secret-id|secret-key"}
	tokenHub := &model.Channel{Type: constant.ChannelTypeTencent, Key: "token-hub-key"}
	xunfei := &model.Channel{Type: constant.ChannelTypeXunfei, Key: "xunfei-key"}

	require.False(t, channelSupportsRequestPath(native, "/v1/messages", "model"))
	require.False(t, channelSupportsRequestPath(native, "/v1/rerank", "model"))
	require.True(t, channelSupportsRequestPath(tokenHub, "/v1/messages", "model"))
	require.True(t, channelSupportsRequestPath(tokenHub, "/v1/rerank", "model"))
	require.False(t, channelSupportsRequestPath(xunfei, "/v1/messages", "model"))
	require.False(t, channelSupportsRequestPath(xunfei, "/v1/rerank", "model"))
	require.True(t, channelSupportsRequestPath(xunfei, "/v1/chat/completions", "model"))
}
