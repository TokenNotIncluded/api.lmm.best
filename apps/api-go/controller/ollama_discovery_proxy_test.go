package controller

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/constant"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/stretchr/testify/require"
)

func TestOllamaDiscoveryHonorsChannelProxy(t *testing.T) {
	var directCalls, proxyCalls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		directCalls.Add(1)
		_, _ = w.Write([]byte(`{"models":[{"name":"wrong-direct-route"}]}`))
	}))
	defer upstream.Close()
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxyCalls.Add(1)
		if r.URL.String() != upstream.URL+"/api/tags" || r.Header.Get("Authorization") != "Bearer test-key" {
			http.Error(w, "unexpected proxy request", http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(`{"models":[{"name":"proxied-model"}]}`))
	}))
	defer proxy.Close()
	channel := &model.Channel{Type: constant.ChannelTypeOllama, Key: "test-key", BaseURL: &upstream.URL}
	channel.SetSetting(dto.ChannelSettings{Proxy: proxy.URL})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	models, err := fetchChannelUpstreamModelIDs(ctx, channel)
	require.NoError(t, err)
	require.Equal(t, []string{"proxied-model"}, models)
	require.EqualValues(t, 1, proxyCalls.Load())
	require.Zero(t, directCalls.Load())
}
