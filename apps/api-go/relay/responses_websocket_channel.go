package relay

import (
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/common"
	appconstant "github.com/LIghtJUNction/api.lmm.best/constant"
	"github.com/LIghtJUNction/api.lmm.best/middleware"
	appmodel "github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/types"
	"github.com/gin-gonic/gin"
	"golang.org/x/net/http/httpguts"
)

// Keep initial selection and locked-channel reuse on the same capability rule.
func responsesWSChannelSupportsType(channelType int) bool {
	switch channelType {
	case appconstant.ChannelTypeOpenAI, appconstant.ChannelTypeOpenHuman, appconstant.ChannelTypeCodex,
		appconstant.ChannelTypeAdvancedCustom, appconstant.ChannelTypeSub2API, appconstant.ChannelTypeNewAPI:
		return true
	default:
		return false
	}
}

func responsesWSChannelDefaultEnabled(channelType int) bool {
	return channelType == appconstant.ChannelTypeOpenAI || channelType == appconstant.ChannelTypeOpenHuman || channelType == appconstant.ChannelTypeCodex
}

func responsesWSChannelEligibility(channel *appmodel.Channel, requestPath, modelName string) *types.NewAPIError {
	unsupported := func(reason string) *types.NewAPIError {
		return types.NewErrorWithStatusCode(fmt.Errorf("channel cannot use responses websocket: %s", reason), types.ErrorCode("responses_websocket_unsupported"), http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	}
	if channel == nil || !responsesWSChannelSupportsType(channel.Type) {
		return unsupported("unsupported channel type")
	}
	if requestPath != "/v1/responses" {
		return unsupported("a /v1/responses route is required")
	}
	settings := channel.GetSetting()
	enabled := responsesWSChannelDefaultEnabled(channel.Type)
	if settings.ResponsesWebSocketEnabled != nil {
		enabled = *settings.ResponsesWebSocketEnabled
	}
	if !enabled {
		return types.NewErrorWithStatusCode(fmt.Errorf("responses websocket is disabled for channel %d", channel.Id), types.ErrorCode("responses_websocket_disabled"), http.StatusForbidden, types.ErrOptionWithSkipRetry())
	}
	// The existing WebSocket transport does not implement per-channel proxies.
	// Do not silently ignore one when opting a new channel into the transport.
	if !responsesWSChannelDefaultEnabled(channel.Type) && strings.TrimSpace(settings.Proxy) != "" {
		return unsupported("per-channel proxies are not supported by this websocket transport")
	}
	if !responsesWSChannelDefaultEnabled(channel.Type) {
		base, err := url.Parse(channel.GetBaseURL())
		if err != nil || base.User != nil || base.Fragment != "" || base.Host == "" || (base.Scheme != "http" && base.Scheme != "https") {
			// A full Advanced Custom route can supply its own target URL.
			if channel.Type != appconstant.ChannelTypeAdvancedCustom {
				return unsupported("an HTTP(S) base URL without URL credentials or a fragment is required")
			}
		}
	}
	if channel.Type != appconstant.ChannelTypeAdvancedCustom {
		return nil
	}
	config := channel.GetOtherSettings().AdvancedCustom
	if config == nil || config.Validate() != nil {
		return unsupported("invalid advanced custom routes")
	}
	route, ok := config.MatchPathForModel(requestPath, modelName)
	if !ok || route.IncomingPath != "/v1/responses" || (strings.TrimSpace(route.Converter) != "" && strings.TrimSpace(route.Converter) != "none") {
		return unsupported("advanced custom requires a converter-free /v1/responses route")
	}
	target, err := url.Parse(strings.TrimSpace(route.UpstreamPath))
	if err != nil || target.Fragment != "" || target.User != nil || !strings.HasSuffix(strings.TrimRight(target.Path, "/"), "/responses") {
		return unsupported("advanced custom upstream must be a Responses path without URL credentials or a fragment")
	}
	if target.Scheme == "" {
		base, err := url.Parse(channel.GetBaseURL())
		if err != nil || base.Host == "" || base.User != nil || base.Fragment != "" || (base.Scheme != "http" && base.Scheme != "https") {
			return unsupported("a relative advanced custom route requires an HTTP(S) base URL without URL credentials or a fragment")
		}
	}
	if route.Auth != nil && strings.TrimSpace(route.Auth.Type) == dto.AdvancedCustomAuthTypeHeader {
		name := strings.ToLower(strings.TrimSpace(route.Auth.Name))
		if !httpguts.ValidHeaderFieldName(name) || !httpguts.ValidHeaderFieldValue(route.Auth.Value) || name == "connection" || name == "upgrade" || name == "host" || name == "content-length" || name == "transfer-encoding" || strings.HasPrefix(name, "sec-websocket-") {
			return unsupported("advanced custom credentials cannot override websocket handshake headers")
		}
	}
	return nil
}

func setupResponsesWSChannelContext(c *gin.Context, channel *appmodel.Channel, modelName string) *types.NewAPIError {
	if apiErr := middleware.SetupContextForSelectedChannel(c, channel, modelName); apiErr != nil {
		return apiErr
	}
	// The HTTP distributor only writes nonempty organization values. A persistent
	// session also needs to revoke the previous value when the setting is cleared.
	organization := ""
	if channel.OpenAIOrganization != nil {
		organization = *channel.OpenAIOrganization
	}
	common.SetContextKey(c, appconstant.ContextKeyChannelOrganization, organization)
	return nil
}

// Hash fields shaping the physical connection. Parameter overrides may also
// rewrite handshake headers, so include them conservatively. Never log keys.
func responsesWSConnectionFingerprint(c *gin.Context, channel *appmodel.Channel, modelName string) string {
	var route dto.AdvancedCustomRoute
	if channel.Type == appconstant.ChannelTypeAdvancedCustom {
		route, _ = channel.GetOtherSettings().AdvancedCustom.MatchPathForModel("/v1/responses", modelName)
	}
	data, _ := common.Marshal(struct {
		Type         int
		BaseURL      string
		Key          string
		Proxy        string
		Organization string
		Header       map[string]interface{}
		Param        map[string]interface{}
		UpstreamPath string
		Auth         *dto.AdvancedCustomRouteAuth
		ModelMapping string
	}{channel.Type, channel.GetBaseURL(), common.GetContextKeyString(c, appconstant.ContextKeyChannelKey), channel.GetSetting().Proxy,
		common.GetContextKeyString(c, appconstant.ContextKeyChannelOrganization), common.GetContextKeyStringMap(c, appconstant.ContextKeyChannelHeaderOverride),
		common.GetContextKeyStringMap(c, appconstant.ContextKeyChannelParamOverride),
		strings.TrimSpace(route.UpstreamPath), route.Auth, channel.GetModelMapping()})
	return fmt.Sprintf("%x", sha256.Sum256(data))
}

func (s *responsesWSSession) retainConnectionCredential(channel *appmodel.Channel) {
	if s.connectionFingerprint == "" {
		return
	}
	if !channel.ChannelInfo.IsMultiKey {
		if channel.Key == s.connectionKey {
			common.SetContextKey(s.c, appconstant.ContextKeyChannelKey, s.connectionKey)
		}
		return
	}
	keys := channel.GetKeys()
	index := s.connectionKeyIndex
	if index >= 0 && index < len(keys) && keys[index] == s.connectionKey {
		status := channel.ChannelInfo.MultiKeyStatusList[index]
		if status == 0 || status == common.ChannelStatusEnabled {
			common.SetContextKey(s.c, appconstant.ContextKeyChannelKey, s.connectionKey)
			common.SetContextKey(s.c, appconstant.ContextKeyChannelMultiKeyIndex, index)
		}
	}
}
