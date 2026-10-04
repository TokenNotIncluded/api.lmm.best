package controller

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/constant"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const typeSafeModelListFixture = `{"models":[{"name":" jev-latest ","description":"Stable release","release_date":"2026-09-01"},{"name":"jev-preview","description":"Preview release","release_date":"2026-09-01"},{"name":"jev-latest"}]}`

func TestParseTypeSafeModelIDsNativeContract(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		want      []string
		wantError string
	}{
		{name: "native names", body: typeSafeModelListFixture, want: []string{"jev-latest", "jev-preview"}},
		{name: "real listed version", body: `{"models":[{"name":"jev-1.13.0"}]}`, want: []string{"jev-1.13.0"}},
		{name: "invalid JSON", body: `{"models":`, wantError: "invalid TypeSafe Models response"},
		{name: "OpenAI shape", body: `{"data":[{"id":"jev-latest"}]}`, wantError: "models is required"},
		{name: "null models", body: `{"models":null}`, wantError: "models is required"},
		{name: "wrong array", body: `{"models":{}}`, wantError: "invalid TypeSafe Models response"},
		{name: "numeric name", body: `{"models":[{"name":1}]}`, wantError: "invalid TypeSafe Models response"},
		{name: "empty models", body: `{"models":[]}`, wantError: "no valid model names"},
		{name: "missing names", body: `{"models":[{"id":"jev-latest"},{"name":" "}]}`, wantError: "no valid model names"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ids, err := parseTypeSafeModelIDs([]byte(test.body))
			if test.wantError != "" {
				require.ErrorContains(t, err, test.wantError)
				require.Nil(t, ids)
				return
			}
			require.NoError(t, err)
			require.Equal(t, test.want, ids)
		})
	}
}

func TestFetchTypeSafeModelsUsesNativeURLAndEnabledSavedKey(t *testing.T) {
	for _, suffix := range []string{"", "/", "/v1", "/v1/", "/gateway/v1/"} {
		t.Run(suffix, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, http.MethodGet, r.Method)
				wantPath := "/v1/models"
				if suffix == "/gateway/v1/" {
					wantPath = "/gateway/v1/models"
				}
				assert.Equal(t, wantPath, r.URL.Path)
				assert.Equal(t, "Bearer enabled-fake-key", r.Header.Get("Authorization"))
				assert.Equal(t, "discovery-enabled-fake-key", r.Header.Get("X-Discovery"))
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(typeSafeModelListFixture))
			}))
			defer server.Close()
			baseURL := server.URL + suffix
			headerOverride := `{"X-Discovery":"discovery-{api_key}"}`
			channel := &model.Channel{
				Type:           constant.ChannelTypeTypeSafe,
				Key:            "disabled-fake-key\nenabled-fake-key",
				BaseURL:        &baseURL,
				HeaderOverride: &headerOverride,
				ChannelInfo: model.ChannelInfo{
					IsMultiKey: true,
					MultiKeyStatusList: map[int]int{
						0: common.ChannelStatusManuallyDisabled,
						1: common.ChannelStatusEnabled,
					},
				},
			}
			ids, err := fetchChannelUpstreamModelIDs(context.Background(), channel)
			require.NoError(t, err)
			// Snapshot IDs are not fabricated as part of the fetched alias list.
			require.Equal(t, []string{"jev-latest", "jev-preview"}, ids)
		})
	}
}

func TestFetchTypeSafeModelsFailureDoesNotReturnStaticCatalog(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusBadGateway} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(status)
				_, _ = w.Write([]byte(`{"error":"secret-fake-key"}`))
			}))
			defer server.Close()
			channel := &model.Channel{Type: constant.ChannelTypeTypeSafe, Key: "secret-fake-key", BaseURL: &server.URL}
			ids, err := fetchChannelUpstreamModelIDs(context.Background(), channel)
			require.ErrorContains(t, err, "status code: "+strconv.Itoa(status))
			require.NotContains(t, err.Error(), channel.Key)
			require.Nil(t, ids)
		})
	}
}

func TestFetchModelsTypeSafeCreateAndSavedChannel(t *testing.T) {
	db := setupModelListControllerTestDB(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1/models", r.URL.Path)
		assert.Equal(t, "Bearer preview-fake-key", r.Header.Get("Authorization"))
		_, _ = w.Write([]byte(typeSafeModelListFixture))
	}))
	defer server.Close()

	body, err := common.Marshal(fetchModelsRequest{Type: constant.ChannelTypeTypeSafe, Key: "preview-fake-key\nsecond-fake-key", BaseURL: &server.URL})
	require.NoError(t, err)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/channel/fetch_models", bytes.NewReader(body))
	ctx.Request.Header.Set("Content-Type", "application/json")
	FetchModels(ctx)
	require.JSONEq(t, `{"success":true,"message":"","data":["jev-latest","jev-preview"]}`, recorder.Body.String())

	channel := &model.Channel{Name: "native TypeSafe", Type: constant.ChannelTypeTypeSafe, Key: "preview-fake-key", BaseURL: &server.URL}
	require.NoError(t, db.Create(channel).Error)
	recorder = httptest.NewRecorder()
	ctx, _ = gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/channel/fetch_models/"+strconv.Itoa(channel.Id), nil)
	ctx.Params = gin.Params{{Key: "id", Value: strconv.Itoa(channel.Id)}}
	FetchUpstreamModels(ctx)
	require.JSONEq(t, `{"success":true,"message":"","data":["jev-latest","jev-preview"]}`, recorder.Body.String())
	require.NotContains(t, recorder.Body.String(), channel.Key)
}

func TestTypeSafeModelUpdateKeepsUnlistedDocumentedSnapshot(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(typeSafeModelListFixture))
	}))
	defer server.Close()
	channel := &model.Channel{
		Type:    constant.ChannelTypeTypeSafe,
		Key:     "fake-key",
		BaseURL: &server.URL,
		Models:  "jev-1.13.0,jev-latest,obsolete-alias",
	}
	add, removed, err := collectPendingUpstreamModelChanges(context.Background(), channel, channel.GetOtherSettings())
	require.NoError(t, err)
	require.Equal(t, []string{"jev-preview"}, add)
	require.Equal(t, []string{"obsolete-alias"}, removed)

	// The same exception must not alter OpenAI-compatible discovery behavior.
	add, removed = collectPendingUpstreamModelChangesFromModels(channel.GetModels(), []string{"jev-latest", "jev-preview"}, nil, nil)
	require.Equal(t, []string{"jev-preview"}, add)
	require.Equal(t, []string{"jev-1.13.0", "obsolete-alias"}, removed)
}

func TestFailedTypeSafeModelDiscoveryDoesNotStageRemoval(t *testing.T) {
	db := setupModelListControllerTestDB(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"id":"jev-latest"}]}`))
	}))
	defer server.Close()
	channel := &model.Channel{Name: "invalid native response", Type: constant.ChannelTypeTypeSafe, Key: "fake-key", BaseURL: &server.URL, Models: "jev-1.13.0,jev-latest"}
	settings := channel.GetOtherSettings()
	settings.UpstreamModelUpdateCheckEnabled = true
	settings.UpstreamModelUpdateAutoSyncEnabled = true
	channel.SetOtherSettings(settings)
	require.NoError(t, db.Create(channel).Error)
	changed, added, err := checkAndPersistChannelUpstreamModelUpdates(context.Background(), channel, &settings, true, true)
	require.ErrorContains(t, err, "models is required")
	require.False(t, changed)
	require.Zero(t, added)
	require.Empty(t, settings.UpstreamModelUpdateLastDetectedModels)
	require.Empty(t, settings.UpstreamModelUpdateLastRemovedModels)
	reloaded, err := model.GetChannelById(channel.Id, true)
	require.NoError(t, err)
	require.Equal(t, "jev-1.13.0,jev-latest", reloaded.Models)
}
