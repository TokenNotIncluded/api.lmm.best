package controller

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/constant"
	"github.com/LIghtJUNction/api.lmm.best/model"
	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/types"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestGetChannelRetrySkipsUnsupportedEndpointCandidates(t *testing.T) {
	gin.SetMode(gin.TestMode)
	previousDB := model.DB
	previousSQLitePath := common.SQLitePath
	previousRedisEnabled := common.RedisEnabled
	previousMemoryCacheEnabled := common.MemoryCacheEnabled
	common.RedisEnabled = false
	common.MemoryCacheEnabled = false
	common.SQLitePath = filepath.Join(t.TempDir(), "relay-retry-endpoint.db")
	t.Setenv("SQL_DSN", "")
	require.NoError(t, model.InitDB())
	db := model.DB
	require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.Ability{}, &model.PublicRelayPreference{}))
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
		model.DB = previousDB
		common.SQLitePath = previousSQLitePath
		common.RedisEnabled = previousRedisEnabled
		common.MemoryCacheEnabled = previousMemoryCacheEnabled
	})

	highPriority := int64(10)
	lowPriority := int64(0)
	unsupported := &model.Channel{
		Id:       31,
		Type:     constant.ChannelTypeBaidu,
		Key:      "unsupported-key",
		Status:   common.ChannelStatusEnabled,
		Name:     "unsupported-claude-retry",
		Models:   "retry-model",
		Group:    "default",
		Priority: &highPriority,
	}
	compatible := &model.Channel{
		Id:       32,
		Type:     constant.ChannelTypeOpenAI,
		Key:      "compatible-key",
		Status:   common.ChannelStatusEnabled,
		Name:     "compatible-claude-retry",
		Models:   "retry-model",
		Group:    "default",
		Priority: &lowPriority,
	}
	require.NoError(t, db.Create(unsupported).Error)
	require.NoError(t, db.Create(compatible).Error)
	require.NoError(t, db.Create(&model.Ability{Group: "default", Model: "retry-model", ChannelId: unsupported.Id, Enabled: true, Priority: &highPriority, Weight: 1}).Error)
	require.NoError(t, db.Create(&model.Ability{Group: "default", Model: "retry-model", ChannelId: compatible.Id, Enabled: true, Priority: &lowPriority, Weight: 1}).Error)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	retry := 0
	retryParam := &service.RetryParam{
		Ctx:         ctx,
		TokenGroup:  "default",
		ModelName:   "retry-model",
		RequestPath: "/v1/messages",
		Retry:       &retry,
	}
	info := &relaycommon.RelayInfo{
		TokenGroup:      "default",
		UsingGroup:      "default",
		UserGroup:       "default",
		OriginModelName: "retry-model",
		ChannelMeta:     &relaycommon.ChannelMeta{}, // non-nil means this is a retry selection
	}

	selected, apiErr := getChannel(ctx, info, retryParam)
	require.Nil(t, apiErr)
	require.NotNil(t, selected)
	require.Equal(t, compatible.Id, selected.Id)
	_, excluded := retryParam.ExcludedChannelIDs[unsupported.Id]
	require.True(t, excluded, "unsupported retry candidate should be excluded request-locally")
	require.Equal(t, compatible.Id, common.GetContextKeyInt(ctx, constant.ContextKeyChannelId))

	// Preserve the final upstream failure after the last candidate is excluded.
	upstream := types.NewErrorWithStatusCode(errors.New("upstream unavailable"), types.ErrorCodeBadResponse, http.StatusServiceUnavailable)
	info.LastError = upstream
	retryParam.ExcludeChannel(compatible.Id)
	selected, apiErr = getChannel(ctx, info, retryParam)
	require.Nil(t, selected)
	require.Same(t, upstream, apiErr)
	require.Equal(t, http.StatusServiceUnavailable, apiErr.StatusCode)

	info.LastError = nil
	selected, apiErr = getChannel(ctx, info, retryParam)
	require.Nil(t, selected)
	require.NotNil(t, apiErr)
	require.NotSame(t, upstream, apiErr)
}
