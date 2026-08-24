package controller

import (
	"context"
	"errors"
	"net/url"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/stretchr/testify/require"
)

func TestAssistantAdminModelSyncApplyCreatesMissingMetadataAtomically(t *testing.T) {
	db := setupTokenControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.Ability{}, &model.Model{}, &model.Vendor{}))
	require.NoError(t, db.Create(&model.Ability{Group: "default", Model: "gpt-5.6-sol", ChannelId: 1, Enabled: true}).Error)
	require.NoError(t, db.Create(&model.Ability{Group: "default", Model: "claude-opus-5", ChannelId: 1, Enabled: true}).Error)

	change := assistantAdminModelSyncChange{
		Locale: "en",
		Models: []assistantAdminModelSnapshot{
			{ModelName: "claude-opus-5", VendorName: "Anthropic", Status: 1},
			{ModelName: "gpt-5.6-sol", VendorName: "OpenAI", Status: 1},
		},
		Vendors: []assistantAdminVendorSnapshot{
			{Name: "Anthropic", Status: 1},
			{Name: "OpenAI", Status: 1},
		},
		ExpectedMissing: []string{"claude-opus-5", "gpt-5.6-sol"},
	}
	change.SourceDigest = assistantModelSyncDigest(change)
	require.NoError(t, applyAssistantAdminModelSync(change))

	var models []model.Model
	require.NoError(t, db.Order("model_name ASC").Find(&models).Error)
	require.Len(t, models, 2)
	require.Equal(t, "claude-opus-5", models[0].ModelName)
	require.Equal(t, "gpt-5.6-sol", models[1].ModelName)

	var vendors []model.Vendor
	require.NoError(t, db.Order("name ASC").Find(&vendors).Error)
	require.Len(t, vendors, 2)

	// The one-time preview is stale after the first successful apply; a retry
	// must not duplicate metadata or vendors.
	require.Error(t, applyAssistantAdminModelSync(change))
	var count int64
	require.NoError(t, db.Model(&model.Model{}).Count(&count).Error)
	require.EqualValues(t, 2, count)
}

func TestAssistantAdminModelSyncPreviewIncludesStagedMetadata(t *testing.T) {
	change := assistantAdminModelSyncChange{Models: []assistantAdminModelSnapshot{{
		ModelName: "example-model", Description: "Example description", Icon: "Example",
		Tags: "chat,reasoning", VendorName: "Example Vendor", NameRule: 1, Status: 1,
	}}}

	require.Equal(t, []map[string]any{{
		"model_id": "example-model", "description": "Example description", "icon": "Example",
		"tags": "chat,reasoning", "vendor": "Example Vendor", "name_rule": 1, "status": 1,
	}}, assistantAdminModelSyncPreview(change))
}

func TestAssistantModelSyncFetchErrorDetailRedactsRequestURL(t *testing.T) {
	err := &url.Error{
		Op:  "Get",
		URL: "https://example.invalid/catalog?debug=true",
		Err: errors.New("connection refused"),
	}

	require.Equal(t, "connection refused", assistantModelSyncFetchErrorDetail(err))
}

func TestAssistantModelSyncFetchErrorDetailClassifiesDeadline(t *testing.T) {
	require.Equal(t, "request timed out", assistantModelSyncFetchErrorDetail(context.DeadlineExceeded))
}

func TestAssistantModelSyncSourceRedactsCredentialsAndQuery(t *testing.T) {
	rawSource := (&url.URL{
		Scheme: "https", Host: "example.invalid", Path: "/catalog",
		User: url.User("example"), RawQuery: "debug=true", Fragment: "fragment",
	}).String()

	require.Equal(t, "https://example.invalid/catalog", assistantModelSyncSource(rawSource))
}

func TestAssistantAdminModelSyncLocaleAndBounds(t *testing.T) {
	cases := map[string]struct {
		expected string
		ok       bool
	}{
		"":      {expected: "", ok: true},
		"zh-CN": {expected: "zh-CN", ok: true},
		"zh-tw": {expected: "zh-TW", ok: true},
		"ja":    {expected: "ja", ok: true},
		"xx":    {expected: "", ok: false},
	}
	for input, testCase := range cases {
		actual, actualOK := assistantModelSyncLocale(input)
		require.Equal(t, testCase.expected, actual, input)
		require.Equal(t, testCase.ok, actualOK, input)
	}
	require.Equal(t, "abcd", boundedAssistantModelText("  abcdef  ", 4))
}
