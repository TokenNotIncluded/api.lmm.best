package model

import (
	"fmt"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/constant"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func resetPricingEndpointTestTables(t *testing.T) {
	t.Helper()
	originalMemoryCacheEnabled := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = true
	require.NoError(t, DB.AutoMigrate(&Channel{}, &Ability{}, &Model{}, &Vendor{}))
	for _, table := range []string{"abilities", "channels", "models", "vendors"} {
		require.NoError(t, DB.Exec("DELETE FROM "+table).Error)
	}
	updatePricingLock.Lock()
	pricingCache.Store(nil)
	updatePricingLock.Unlock()
	InitChannelCache()
	InvalidatePricingCache()
	t.Cleanup(func() {
		updatePricingLock.Lock()
		pricingCache.Store(nil)
		updatePricingLock.Unlock()
		for _, table := range []string{"abilities", "channels", "models", "vendors"} {
			require.NoError(t, DB.Exec("DELETE FROM "+table).Error)
		}
		InitChannelCache()
		InvalidatePricingCache()
		common.MemoryCacheEnabled = originalMemoryCacheEnabled
	})
}

func insertPricingEndpointChannel(t *testing.T, channelID int, channelType int, settings dto.ChannelOtherSettings) {
	t.Helper()
	channel := &Channel{
		Id:     channelID,
		Type:   channelType,
		Key:    fmt.Sprintf("key-%d", channelID),
		Status: common.ChannelStatusEnabled,
		Name:   fmt.Sprintf("channel-%d", channelID),
	}
	if settings.AdvancedCustom != nil {
		channel.SetOtherSettings(settings)
	}
	require.NoError(t, DB.Create(channel).Error)
}

func insertPricingEndpointAbility(t *testing.T, channelID int, modelName string) {
	t.Helper()
	require.NoError(t, DB.Create(&Ability{
		Group:     "default",
		Model:     modelName,
		ChannelId: channelID,
		Enabled:   true,
	}).Error)
}

func pricingEndpointAdvancedCustomConfig(routes ...dto.AdvancedCustomRoute) dto.ChannelOtherSettings {
	return dto.ChannelOtherSettings{
		AdvancedCustom: &dto.AdvancedCustomConfig{
			Routes: routes,
		},
	}
}

func pricingEndpointTypesByModel(t *testing.T) map[string][]constant.EndpointType {
	t.Helper()
	InitChannelCache()
	require.NoError(t, refreshPricingNow())
	return pricingEndpointTypesFromPricing(GetPricing())
}

func pricingEndpointTypesFromPricing(pricings []Pricing) map[string][]constant.EndpointType {
	byModel := make(map[string][]constant.EndpointType)
	for _, pricing := range pricings {
		byModel[pricing.ModelName] = pricing.SupportedEndpointTypes
	}
	return byModel
}

func TestPricingSystemOneUsesNativeEndpointForMappedAliases(t *testing.T) {
	resetPricingEndpointTestTables(t)
	insertPricingEndpointChannel(t, 801, constant.ChannelTypeTypeSafe, dto.ChannelOtherSettings{})
	insertPricingEndpointChannel(t, 802, constant.ChannelTypeNewAPI, dto.ChannelOtherSettings{})
	require.NoError(t, DB.Model(&Channel{}).Where("id = ?", 802).Update("model_mapping", `{"decision-alias":"intermediate","intermediate":"jev-latest"}`).Error)
	insertPricingEndpointAbility(t, 801, "vendor-alias")
	insertPricingEndpointAbility(t, 802, "decision-alias")
	insertPricingEndpointAbility(t, 802, "jev-preview")
	insertPricingEndpointAbility(t, 802, "gpt-4o")

	for _, memoryCache := range []bool{true, false} {
		common.MemoryCacheEnabled = memoryCache
		byModel := pricingEndpointTypesByModel(t)
		for _, name := range []string{"vendor-alias", "decision-alias", "jev-preview"} {
			assert.Equal(t, []constant.EndpointType{constant.EndpointTypeSystemOne}, byModel[name])
		}
		assert.NotContains(t, byModel["gpt-4o"], constant.EndpointTypeSystemOne)
		assert.Equal(t, common.EndpointInfo{Path: "/typesafe/v1/systemone", Method: "POST"}, GetSupportedEndpointMap()[string(constant.EndpointTypeSystemOne)])
	}
}

func TestPricingFixedNativeCapabilitiesIgnoreLegacyChatMetadata(t *testing.T) {
	resetPricingEndpointTestTables(t)
	insertPricingEndpointChannel(t, 1001, constant.ChannelTypeOpenAI, dto.ChannelOtherSettings{})
	insertPricingEndpointChannel(t, 1002, constant.ChannelTypeNewAPI, dto.ChannelOtherSettings{})
	insertPricingEndpointChannel(t, 1003, constant.ChannelTypeTypeSafe, dto.ChannelOtherSettings{})
	require.NoError(t, DB.Model(&Channel{}).Where("id = ?", 1001).Update("model_mapping", `{"openai-voice-alias":"gpt-live-transcribe"}`).Error)
	require.NoError(t, DB.Model(&Channel{}).Where("id = ?", 1002).Update("model_mapping", `{"voice-alias":"gpt-live-1","decision-alias":"jev-latest","moderation-alias":"omni-moderation-latest"}`).Error)
	models := []struct {
		name      string
		channelID int
		endpoint  constant.EndpointType
	}{
		{"gpt-live-1", 1001, constant.EndpointTypeLive},
		{"gpt-live-transcribe", 1001, constant.EndpointTypeRealtimeTranscription},
		{"gpt-realtime-whisper", 1001, constant.EndpointTypeRealtimeTranscription},
		{"gpt-realtime-translate", 1001, constant.EndpointTypeRealtimeTranslation},
		{"openai-voice-alias", 1001, constant.EndpointTypeRealtimeTranscription},
		{"voice-alias", 1002, constant.EndpointTypeLive},
		{"decision-alias", 1002, constant.EndpointTypeSystemOne},
		{"vendor-decision-alias", 1003, constant.EndpointTypeSystemOne},
		{"moderation-alias", 1002, constant.EndpointTypeModeration},
	}
	for _, item := range models {
		insertPricingEndpointAbility(t, item.channelID, item.name)
		endpoints, err := common.Marshal(map[string]any{
			"openai":              "/v1/chat/completions",
			"legacy-custom-route": "/legacy/chat",
			string(item.endpoint): map[string]any{"path": "/legacy/chat", "method": "POST"},
		})
		require.NoError(t, err)
		require.NoError(t, DB.Create(&Model{ModelName: item.name, Endpoints: string(endpoints), Status: 1, NameRule: NameRuleExact}).Error)
	}
	for _, memoryCache := range []bool{false, true} {
		common.MemoryCacheEnabled = memoryCache
		byModel := pricingEndpointTypesByModel(t)
		endpointMap := GetSupportedEndpointMap()
		for _, item := range models {
			assert.Equal(t, []constant.EndpointType{item.endpoint}, byModel[item.name])
			defaultInfo, found := common.GetDefaultEndpointInfo(item.endpoint)
			require.True(t, found)
			assert.Equal(t, defaultInfo, endpointMap[string(item.endpoint)])
		}
	}
}

func TestPricingUnsupportedNativeVoiceCannotBeProvidedByMetadata(t *testing.T) {
	resetPricingEndpointTestTables(t)
	insertPricingEndpointChannel(t, 1004, constant.ChannelTypeCodex, dto.ChannelOtherSettings{})
	insertPricingEndpointAbility(t, 1004, "gpt-live-1")
	require.NoError(t, DB.Create(&Model{
		ModelName: "gpt-live-1", Endpoints: `{"openai":"/v1/chat/completions","live":"/legacy/chat"}`, Status: 1, NameRule: NameRuleExact,
	}).Error)
	byModel := pricingEndpointTypesByModel(t)
	assert.Empty(t, byModel["gpt-live-1"])
}

func TestResolveChannelModelName(t *testing.T) {
	for _, tt := range []struct {
		name, mapping, want string
		wantError           string
	}{
		{name: "unchanged", want: "alias"},
		{name: "chain", mapping: `{"alias":"intermediate","intermediate":"jev-latest"}`, want: "jev-latest"},
		{name: "self", mapping: `{"alias":"alias"}`, want: "alias"},
		{name: "tail self", mapping: `{"alias":"jev-latest","jev-latest":"jev-latest"}`, want: "jev-latest"},
		{name: "invalid", mapping: `{`, wantError: "unmarshal_model_mapping_failed"},
		{name: "cycle", mapping: `{"alias":"intermediate","intermediate":"alias"}`, wantError: "model_mapping_contains_cycle"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			name, err := ResolveChannelModelName("alias", tt.mapping)
			if tt.wantError != "" {
				require.ErrorContains(t, err, tt.wantError)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, name)
		})
	}
}

func TestPricingAdvancedCustomUsesConfiguredEndpointTypes(t *testing.T) {
	resetPricingEndpointTestTables(t)

	insertPricingEndpointChannel(t, 101, constant.ChannelTypeAdvancedCustom, pricingEndpointAdvancedCustomConfig(
		dto.AdvancedCustomRoute{
			IncomingPath: "/v1/chat/completions",
			UpstreamPath: "/v1/chat/completions",
		},
		dto.AdvancedCustomRoute{
			IncomingPath: "/v1/responses",
			UpstreamPath: "/v1beta/models/{model}:generateContent",
			Converter:    "openai_responses_to_gemini_generate_content",
			Models:       []string{"re:^gemini-"},
		},
	))
	insertPricingEndpointAbility(t, 101, "gemini-2.5-flash")
	insertPricingEndpointAbility(t, 101, "gpt-4o")

	byModel := pricingEndpointTypesByModel(t)

	assert.Equal(t, []constant.EndpointType{
		constant.EndpointTypeOpenAI,
		constant.EndpointTypeOpenAIResponse,
	}, byModel["gemini-2.5-flash"])
	assert.Equal(t, []constant.EndpointType{
		constant.EndpointTypeOpenAI,
	}, byModel["gpt-4o"])
}

func TestPricingModelMetadataEndpointsMergeWithAdvancedCustomInference(t *testing.T) {
	resetPricingEndpointTestTables(t)

	insertPricingEndpointChannel(t, 103, constant.ChannelTypeAdvancedCustom, pricingEndpointAdvancedCustomConfig(
		dto.AdvancedCustomRoute{
			IncomingPath: "/v1/responses",
			UpstreamPath: "/v1beta/models/{model}:generateContent",
			Converter:    "openai_responses_to_gemini_generate_content",
			Models:       []string{"re:^gemini-"},
		},
	))
	insertPricingEndpointAbility(t, 103, "gemini-2.5-flash")
	require.NoError(t, DB.Create(&Model{
		ModelName: "gemini-2.5-flash",
		Endpoints: `{
			"openai": "/v1/chat/completions"
		}`,
		Status:   1,
		NameRule: NameRuleExact,
	}).Error)

	byModel := pricingEndpointTypesByModel(t)

	assert.Equal(t, []constant.EndpointType{
		constant.EndpointTypeOpenAIResponse,
		constant.EndpointTypeOpenAI,
	}, byModel["gemini-2.5-flash"])
}

func TestPricingModelMetadataEndpointsCanProvideEndpointWithoutChannelInference(t *testing.T) {
	resetPricingEndpointTestTables(t)

	insertPricingEndpointChannel(t, 104, constant.ChannelTypeAdvancedCustom, pricingEndpointAdvancedCustomConfig(
		dto.AdvancedCustomRoute{
			IncomingPath: "/v1/responses",
			UpstreamPath: "/v1beta/models/{model}:generateContent",
			Converter:    "openai_responses_to_gemini_generate_content",
			Models:       []string{"re:^gemini-"},
		},
	))
	insertPricingEndpointAbility(t, 104, "metadata-only-model")
	require.NoError(t, DB.Create(&Model{
		ModelName: "metadata-only-model",
		Endpoints: `{
			"openai": "/v1/chat/completions"
		}`,
		Status:   1,
		NameRule: NameRuleExact,
	}).Error)

	byModel := pricingEndpointTypesByModel(t)

	assert.Equal(t, []constant.EndpointType{constant.EndpointTypeOpenAI}, byModel["metadata-only-model"])
}

func TestPricingAdvancedCustomMissingConfigFallsBackToChannelType(t *testing.T) {
	resetPricingEndpointTestTables(t)

	insertPricingEndpointChannel(t, 102, constant.ChannelTypeAdvancedCustom, dto.ChannelOtherSettings{})
	insertPricingEndpointAbility(t, 102, "gpt-4o")

	byModel := pricingEndpointTypesByModel(t)

	assert.Equal(t, []constant.EndpointType{constant.EndpointTypeOpenAI}, byModel["gpt-4o"])
}

func TestPricingNativeChannelEndpointTypesUnchanged(t *testing.T) {
	resetPricingEndpointTestTables(t)

	insertPricingEndpointChannel(t, 201, constant.ChannelTypeOpenAI, dto.ChannelOtherSettings{})
	insertPricingEndpointChannel(t, 202, constant.ChannelTypeGemini, dto.ChannelOtherSettings{})
	insertPricingEndpointChannel(t, 203, constant.ChannelTypeAnthropic, dto.ChannelOtherSettings{})
	insertPricingEndpointAbility(t, 201, "gpt-4o")
	insertPricingEndpointAbility(t, 202, "gemini-2.5-flash")
	insertPricingEndpointAbility(t, 203, "claude-3-5-sonnet")

	byModel := pricingEndpointTypesByModel(t)

	assert.Equal(t, []constant.EndpointType{constant.EndpointTypeOpenAI}, byModel["gpt-4o"])
	assert.Equal(t, []constant.EndpointType{constant.EndpointTypeGemini, constant.EndpointTypeOpenAI}, byModel["gemini-2.5-flash"])
	assert.Equal(t, []constant.EndpointType{constant.EndpointTypeAnthropic, constant.EndpointTypeOpenAI}, byModel["claude-3-5-sonnet"])
}

func TestInitChannelCacheInvalidatesPricingCache(t *testing.T) {
	resetPricingEndpointTestTables(t)

	insertPricingEndpointChannel(t, 301, constant.ChannelTypeAdvancedCustom, pricingEndpointAdvancedCustomConfig(
		dto.AdvancedCustomRoute{
			IncomingPath: "/v1/chat/completions",
			UpstreamPath: "/v1/chat/completions",
		},
	))
	insertPricingEndpointAbility(t, 301, "gemini-3.5-flash")
	InitChannelCache()

	initial := pricingEndpointTypesByModel(t)
	require.Equal(t, []constant.EndpointType{constant.EndpointTypeOpenAI}, initial["gemini-3.5-flash"])

	var channel Channel
	require.NoError(t, DB.First(&channel, "id = ?", 301).Error)
	channel.SetOtherSettings(pricingEndpointAdvancedCustomConfig(
		dto.AdvancedCustomRoute{
			IncomingPath: "/v1/chat/completions",
			UpstreamPath: "/v1/chat/completions",
		},
		dto.AdvancedCustomRoute{
			IncomingPath: "/v1/responses",
			UpstreamPath: "/v1beta/models/{model}:generateContent",
			Converter:    "openai_responses_to_gemini_generate_content",
			Models:       []string{"re:^gemini-"},
		},
	))
	require.NoError(t, DB.Model(&Channel{}).Where("id = ?", 301).Update("settings", channel.OtherSettings).Error)
	InitChannelCache()

	updated := pricingEndpointTypesByModel(t)
	assert.Equal(t, []constant.EndpointType{
		constant.EndpointTypeOpenAI,
		constant.EndpointTypeOpenAIResponse,
	}, updated["gemini-3.5-flash"])
}

func TestInitChannelCacheInvalidatesStartupPricingBuiltBeforeChannelCache(t *testing.T) {
	resetPricingEndpointTestTables(t)

	insertPricingEndpointChannel(t, 302, constant.ChannelTypeAdvancedCustom, pricingEndpointAdvancedCustomConfig(
		dto.AdvancedCustomRoute{
			IncomingPath: "/v1/chat/completions",
			UpstreamPath: "/v1/chat/completions",
		},
		dto.AdvancedCustomRoute{
			IncomingPath: "/v1/responses",
			UpstreamPath: "/v1beta/models/{model}:generateContent",
			Converter:    "openai_responses_to_gemini_generate_content",
			Models:       []string{"re:^gemini-"},
		},
	))
	insertPricingEndpointAbility(t, 302, "gemini-3.5-flash")

	require.NoError(t, refreshPricingNow())
	staleByModel := pricingEndpointTypesFromPricing(GetPricing())
	require.Equal(t, []constant.EndpointType{constant.EndpointTypeOpenAI}, staleByModel["gemini-3.5-flash"])

	InitChannelCache()

	require.NoError(t, refreshPricingNow())
	rebuiltByModel := pricingEndpointTypesFromPricing(GetPricing())
	assert.Equal(t, []constant.EndpointType{
		constant.EndpointTypeOpenAI,
		constant.EndpointTypeOpenAIResponse,
	}, rebuiltByModel["gemini-3.5-flash"])
}

func TestCacheUpdateChannelSyncsAdvancedCustomConfig(t *testing.T) {
	resetPricingEndpointTestTables(t)

	channel := &Channel{
		Id:     401,
		Type:   constant.ChannelTypeAdvancedCustom,
		Key:    "key-401",
		Status: common.ChannelStatusEnabled,
		Name:   "channel-401",
	}
	channel.SetOtherSettings(pricingEndpointAdvancedCustomConfig(dto.AdvancedCustomRoute{
		IncomingPath: "/v1/responses",
		UpstreamPath: "/v1beta/models/{model}:generateContent",
		Converter:    "openai_responses_to_gemini_generate_content",
	}))
	CacheUpdateChannel(channel)

	require.NotNil(t, channel2advancedCustomConfig[401])
	assert.Equal(t, []constant.EndpointType{constant.EndpointTypeOpenAIResponse}, channel2advancedCustomConfig[401].SupportedEndpointTypesForModel("gemini-3.5-flash"))

	channel.SetOtherSettings(pricingEndpointAdvancedCustomConfig(dto.AdvancedCustomRoute{
		IncomingPath: "/v1/chat/completions",
		UpstreamPath: "/v1/chat/completions",
	}))
	CacheUpdateChannel(channel)

	require.NotNil(t, channel2advancedCustomConfig[401])
	assert.Equal(t, []constant.EndpointType{constant.EndpointTypeOpenAI}, channel2advancedCustomConfig[401].SupportedEndpointTypesForModel("gemini-3.5-flash"))

	channel.Type = constant.ChannelTypeOpenAI
	CacheUpdateChannel(channel)

	assert.Nil(t, channel2advancedCustomConfig[401])
}
