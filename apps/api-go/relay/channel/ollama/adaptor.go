package ollama

import (
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/relay/channel"
	"github.com/LIghtJUNction/api.lmm.best/relay/channel/claude"
	"github.com/LIghtJUNction/api.lmm.best/relay/channel/openai"
	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	relayconstant "github.com/LIghtJUNction/api.lmm.best/relay/constant"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/types"

	"github.com/gin-gonic/gin"
)

type Adaptor struct {
}

func (a *Adaptor) ConvertGeminiRequest(*gin.Context, *relaycommon.RelayInfo, *dto.GeminiChatRequest) (any, error) {
	return nil, errors.New("not implemented")
}

func (a *Adaptor) ConvertClaudeRequest(c *gin.Context, info *relaycommon.RelayInfo, request *dto.ClaudeRequest) (any, error) {
	if useNativeClaudeMessages(info) {
		claudeAdaptor := claude.Adaptor{}
		return claudeAdaptor.ConvertClaudeRequest(c, info, request)
	}

	openaiAdaptor := openai.Adaptor{}
	openaiRequest, err := openaiAdaptor.ConvertClaudeRequest(c, info, request)
	if err != nil {
		return nil, err
	}
	openaiRequest.(*dto.GeneralOpenAIRequest).StreamOptions = &dto.StreamOptions{
		IncludeUsage: true,
	}
	// map to ollama chat request (Claude -> OpenAI -> Ollama chat)
	return openAIChatToOllamaChat(c, openaiRequest.(*dto.GeneralOpenAIRequest))
}

func (a *Adaptor) ConvertAudioRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.AudioRequest) (io.Reader, error) {
	return nil, errors.New("not implemented")
}

func (a *Adaptor) ConvertImageRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.ImageRequest) (any, error) {
	return nil, errors.New("not implemented")
}

func (a *Adaptor) Init(info *relaycommon.RelayInfo) {
}

func (a *Adaptor) GetRequestURL(info *relaycommon.RelayInfo) (string, error) {
	switch info.RelayMode {
	case relayconstant.RelayModeResponses:
		return info.ChannelBaseUrl + "/v1/responses", nil
	case relayconstant.RelayModeResponsesCompact:
		return "", errors.New("Ollama does not support /v1/responses/compact")
	case relayconstant.RelayModeEmbeddings:
		return info.ChannelBaseUrl + "/api/embed", nil
	}
	if useNativeClaudeMessages(info) {
		return info.ChannelBaseUrl + "/v1/messages", nil
	}
	if strings.Contains(info.RequestURLPath, "/v1/completions") || info.RelayMode == relayconstant.RelayModeCompletions {
		return info.ChannelBaseUrl + "/api/generate", nil
	}
	return info.ChannelBaseUrl + "/api/chat", nil
}

func (a *Adaptor) SetupRequestHeader(c *gin.Context, req *http.Header, info *relaycommon.RelayInfo) error {
	channel.SetupApiRequestHeader(info, c, req)
	req.Set("Authorization", "Bearer "+info.ApiKey)
	if useNativeClaudeMessages(info) {
		anthropicVersion := c.Request.Header.Get("anthropic-version")
		if anthropicVersion == "" {
			anthropicVersion = "2023-06-01"
		}
		req.Set("anthropic-version", anthropicVersion)
		claude.CommonClaudeHeadersOperation(c, req, info)
	}
	return nil
}

func (a *Adaptor) ConvertOpenAIRequest(c *gin.Context, info *relaycommon.RelayInfo, request *dto.GeneralOpenAIRequest) (any, error) {
	if request == nil {
		return nil, errors.New("request is nil")
	}
	// decide generate or chat
	if strings.Contains(info.RequestURLPath, "/v1/completions") || info.RelayMode == relayconstant.RelayModeCompletions {
		return openAIToGenerate(c, request)
	}
	return openAIChatToOllamaChat(c, request)
}

func (a *Adaptor) ConvertRerankRequest(c *gin.Context, relayMode int, request dto.RerankRequest) (any, error) {
	return nil, nil
}

func (a *Adaptor) ConvertEmbeddingRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.EmbeddingRequest) (any, error) {
	return requestOpenAI2Embeddings(request), nil
}

func (a *Adaptor) ConvertOpenAIResponsesRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.OpenAIResponsesRequest) (any, error) {
	openaiAdaptor := openai.Adaptor{}
	return openaiAdaptor.ConvertOpenAIResponsesRequest(c, info, request)
}

func (a *Adaptor) DoRequest(c *gin.Context, info *relaycommon.RelayInfo, requestBody io.Reader) (any, error) {
	return channel.DoApiRequest(a, c, info, requestBody)
}

func (a *Adaptor) DoResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (usage any, err *types.NewAPIError) {
	switch info.RelayMode {
	case relayconstant.RelayModeResponses:
		openaiAdaptor := openai.Adaptor{}
		return openaiAdaptor.DoResponse(c, resp, info)
	case relayconstant.RelayModeEmbeddings:
		return ollamaEmbeddingHandler(c, info, resp)
	}
	if useNativeClaudeMessages(info) {
		claudeAdaptor := claude.Adaptor{}
		return claudeAdaptor.DoResponse(c, resp, info)
	}
	if info.IsStream {
		return ollamaStreamHandler(c, info, resp)
	}
	return ollamaChatHandler(c, info, resp)
}

func useNativeClaudeMessages(info *relaycommon.RelayInfo) bool {
	return info != nil &&
		info.RelayFormat == types.RelayFormatClaude &&
		info.ChannelMeta != nil &&
		info.ChannelSetting.PassThroughBodyEnabled
}

func (a *Adaptor) GetModelList() []string {
	return ModelList
}

func (a *Adaptor) GetChannelName() string {
	return ChannelName
}
