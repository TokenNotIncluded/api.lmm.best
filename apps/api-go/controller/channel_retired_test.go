package controller

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/constant"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestRetiredOpenHumanCannotBeCreatedOrSelectedByTypeChange(t *testing.T) {
	db := setupModelListControllerTestDB(t)
	origin := model.Channel{Type: constant.ChannelTypeTypeSafe, Name: "native Jev", Key: "fixture-key", Group: "human", Models: "jev-latest", Status: common.ChannelStatusEnabled}
	require.NoError(t, db.Create(&origin).Error)
	for _, tc := range []struct {
		method, body string
		handler      gin.HandlerFunc
	}{
		{http.MethodPost, `{"channel":{"type":61,"name":"retired","key":"fixture-key","group":"human","models":"fixture-model"}}`, AddChannel},
		{http.MethodPut, fmt.Sprintf(`{"id":%d,"type":61}`, origin.Id), UpdateChannel},
	} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(tc.method, "/api/channel/", bytes.NewBufferString(tc.body))
		c.Request.Header.Set("Content-Type", "application/json")
		c.Set("id", 1)
		c.Set("role", common.RoleRootUser)
		tc.handler(c)
		require.Contains(t, w.Body.String(), model.ErrRetiredChannelType.Error())
		var count int64
		require.NoError(t, db.Model(&model.Channel{}).Count(&count).Error)
		require.Equal(t, int64(1), count)
		var current model.Channel
		require.NoError(t, db.First(&current, origin.Id).Error)
		require.Equal(t, origin.Type, current.Type)
		require.Equal(t, origin.Key, current.Key)
		require.Equal(t, origin.Group, current.Group)
	}
}

func TestRetiredOpenHumanHistoryReadableAndCannotProbeOrCopy(t *testing.T) {
	db := setupModelListControllerTestDB(t)
	var called atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called.Add(1) }))
	t.Cleanup(server.Close)
	origin := model.Channel{Type: constant.ChannelTypeOpenHuman, Name: "Historical OpenHuman", Key: "fixture-key", Group: "human", Models: "fixture-model", BaseURL: &server.URL, Status: common.ChannelStatusEnabled}
	// Simulate a historical row, without permitting a new insert via model API.
	require.NoError(t, db.Create(&origin).Error)
	loaded, err := model.GetChannelById(origin.Id, false)
	require.NoError(t, err)
	require.Equal(t, 61, loaded.Type)
	require.Equal(t, "human", loaded.Group)
	require.ErrorIs(t, (&model.Channel{Type: 61}).Insert(), model.ErrRetiredChannelType)
	_, err = fetchChannelUpstreamModelIDs(context.Background(), &origin)
	require.ErrorIs(t, err, model.ErrRetiredChannelType)
	_, err = updateStandardChannelBalance(context.Background(), &origin)
	require.ErrorIs(t, err, model.ErrRetiredChannelType)
	result := testChannel(context.Background(), &origin, 1, "fixture-model", "", false)
	require.ErrorContains(t, result.localErr, "channel test is not supported")
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/channel/copy/", nil)
	c.Params = gin.Params{{Key: "id", Value: fmt.Sprint(origin.Id)}}
	CopyChannel(c)
	require.Contains(t, w.Body.String(), model.ErrRetiredChannelType.Error())
	var count int64
	require.NoError(t, db.Model(&model.Channel{}).Count(&count).Error)
	require.Equal(t, int64(1), count)
	require.Zero(t, called.Load(), "retired metadata must not become an outbound OpenAI request")
}
