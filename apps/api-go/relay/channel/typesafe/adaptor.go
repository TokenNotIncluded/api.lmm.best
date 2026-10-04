package typesafe

import (
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/logger"
	"github.com/LIghtJUNction/api.lmm.best/relay/channel"
	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	relayconstant "github.com/LIghtJUNction/api.lmm.best/relay/constant"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/types"
	"github.com/gin-gonic/gin"
)

const ChannelName = "TypeSafe"

var ModelList = []string{"jev-1.13.0", "jev-latest", "jev-preview"}

type Adaptor struct{}

func (a *Adaptor) Init(_ *relaycommon.RelayInfo) {}

// SystemOneURL accepts the vendor and compatible gateway base URL conventions
// supported by the official TypeSafe 1.0.0 plugin.
func SystemOneURL(baseURL string, chained bool) string {
	baseURL = strings.TrimRight(baseURL, "/")
	if strings.HasSuffix(baseURL, "/v1") {
		baseURL = strings.TrimSuffix(baseURL, "/v1")
	}
	if baseURL == "" {
		baseURL = "https://api.typesafe.ai"
	}
	if chained {
		return baseURL + "/typesafe/v1/systemone"
	}
	return baseURL + "/v1/systemone"
}

func (a *Adaptor) GetRequestURL(info *relaycommon.RelayInfo) (string, error) {
	if info.RelayMode != relayconstant.RelayModeSystemOne {
		return "", channel.NewUnsupportedEndpointError(ChannelName, channel.Endpoint(info.RelayFormat))
	}
	return SystemOneURL(info.ChannelBaseUrl, false), nil
}

func (a *Adaptor) SetupRequestHeader(_ *gin.Context, headers *http.Header, info *relaycommon.RelayInfo) error {
	headers.Set("Authorization", "Bearer "+info.ApiKey)
	headers.Set("Content-Type", "application/json")
	headers.Set("Accept", "application/json")
	return nil
}

func ConvertSystemOneRequest(request *dto.SystemOneRequest) (any, error) {
	if err := request.Validate(); err != nil {
		return nil, types.NewErrorWithStatusCode(err, types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	}
	if !dto.IsSystemOneModel(request.Model) {
		return nil, types.NewErrorWithStatusCode(errors.New("unsupported TypeSafe model; use a declared Jev model or map an alias to it"), types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	}
	return request.UpstreamRequest(), nil
}

func (a *Adaptor) ConvertSystemOneRequest(_ *gin.Context, _ *relaycommon.RelayInfo, request *dto.SystemOneRequest) (any, error) {
	return ConvertSystemOneRequest(request)
}

func (a *Adaptor) SupportsEndpoint(endpoint channel.Endpoint) bool {
	return endpoint == channel.EndpointSystemOne
}

func (a *Adaptor) DoRequest(c *gin.Context, info *relaycommon.RelayInfo, body io.Reader) (any, error) {
	return channel.DoApiRequest(a, c, info, body)
}

// DoSystemOneResponse is shared by the vendor adaptor and NewAPI chaining.
// It validates the complete response before delivery, preserving native answers.
func DoSystemOneResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (any, *types.NewAPIError) {
	request, ok := info.Request.(*dto.SystemOneRequest)
	if !ok {
		return nil, types.NewErrorWithStatusCode(errors.New("invalid System One request type"), types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	}
	body, err := common.ReadResponseBody(resp)
	if err != nil {
		return nil, types.NewErrorWithStatusCode(err, types.ErrorCodeBadResponse, http.StatusBadGateway, types.ErrOptionWithSkipRetry())
	}
	response, err := dto.ValidateSystemOneResponse(body, request.Questions)
	if err != nil {
		return nil, types.NewErrorWithStatusCode(err, types.ErrorCodeBadResponse, http.StatusBadGateway, types.ErrOptionWithSkipRetry())
	}
	tokens, valid := response.InputTokenUsage()
	info.SystemOneUsageStatus = "reported"
	usageSource := "typesafe"
	if !valid {
		tokens = 0
		info.SystemOneUsageStatus = "invalid"
		usageSource = "typesafe_context_reservation"
		logger.LogWarn(c, "TypeSafe input token usage is missing or invalid; retaining the input context reservation")
	}
	info.SetFirstResponseTime()
	info.ObserveResponseModel(response.Model)
	info.ResponseCompleted = true
	usage := &dto.Usage{PromptTokens: tokens, InputTokens: tokens, TotalTokens: tokens, UsageSource: usageSource}
	c.Data(resp.StatusCode, "application/json", body)
	return usage, nil
}

func (a *Adaptor) DoResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (any, *types.NewAPIError) {
	return DoSystemOneResponse(c, resp, info)
}

func (a *Adaptor) GetModelList() []string { return append([]string(nil), ModelList...) }
func (a *Adaptor) GetChannelName() string { return ChannelName }

func (a *Adaptor) unsupported(endpoint string) (any, error) {
	return nil, channel.NewUnsupportedEndpointError(ChannelName, channel.Endpoint(endpoint))
}

func (a *Adaptor) ConvertOpenAIRequest(_ *gin.Context, _ *relaycommon.RelayInfo, _ *dto.GeneralOpenAIRequest) (any, error) {
	return a.unsupported("chat_completions")
}
func (a *Adaptor) ConvertOpenAIResponsesRequest(_ *gin.Context, _ *relaycommon.RelayInfo, _ dto.OpenAIResponsesRequest) (any, error) {
	return a.unsupported("responses")
}
func (a *Adaptor) ConvertRerankRequest(_ *gin.Context, _ int, _ dto.RerankRequest) (any, error) {
	return a.unsupported("rerank")
}
func (a *Adaptor) ConvertEmbeddingRequest(_ *gin.Context, _ *relaycommon.RelayInfo, _ dto.EmbeddingRequest) (any, error) {
	return a.unsupported("embeddings")
}
func (a *Adaptor) ConvertAudioRequest(_ *gin.Context, _ *relaycommon.RelayInfo, _ dto.AudioRequest) (io.Reader, error) {
	return nil, channel.NewUnsupportedEndpointError(ChannelName, channel.Endpoint("audio"))
}
func (a *Adaptor) ConvertImageRequest(_ *gin.Context, _ *relaycommon.RelayInfo, _ dto.ImageRequest) (any, error) {
	return a.unsupported("images")
}
func (a *Adaptor) ConvertClaudeRequest(_ *gin.Context, _ *relaycommon.RelayInfo, _ *dto.ClaudeRequest) (any, error) {
	return a.unsupported("claude_messages")
}
func (a *Adaptor) ConvertGeminiRequest(_ *gin.Context, _ *relaycommon.RelayInfo, _ *dto.GeminiChatRequest) (any, error) {
	return a.unsupported("gemini")
}
