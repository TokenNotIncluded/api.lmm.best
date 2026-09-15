package service

import (
	"context"
	"fmt"

	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/relayconvert"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/types"
	"github.com/gin-gonic/gin"
)

func init() {
	relayconvert.SetMediaResolver(relayconvert.MediaResolver{
		// relayconvert is gin-free; recover the gin context when the caller
		// passed one so file caching/cleanup keeps working.
		GetBase64Data: func(ctx context.Context, source types.FileSource, reason ...string) (string, string, error) {
			ginCtx, _ := ctx.(*gin.Context)
			return GetBase64Data(ginCtx, source, reason...)
		},
		DecodeBase64FileData: DecodeBase64FileData,
	})
}

func ConvertRequest(c *gin.Context, info *relaycommon.RelayInfo, target types.RelayFormat, request any) (*relayconvert.RequestResult, error) {
	result, err := relayconvert.ConvertRequest(c, info, target, request)
	if err != nil {
		return nil, err
	}
	// The direct OpenAI-compatible adaptor converts Gemini requests through this
	// generic facade. Claude has its own adaptor-side handling, and keeping that
	// distinction avoids changing internal Claude -> Chat -> Responses staging.
	if result.From == types.RelayFormatGemini {
		applyOpenAIChatStreamUsage(info, result)
	}
	return result, nil
}

func ConvertRequestByID(c *gin.Context, info *relaycommon.RelayInfo, converter string, request any) (*relayconvert.RequestResult, error) {
	result, err := relayconvert.ConvertRequestByID(c, info, converter, request)
	if err != nil {
		return nil, err
	}
	// Explicit converter IDs are currently consumed by the advanced-custom
	// adaptor. Its Claude, Gemini, and Responses -> Chat routes all need the
	// same upstream usage contract.
	applyOpenAIChatStreamUsage(info, result)
	return result, nil
}

func ConvertRequestVia(c *gin.Context, info *relaycommon.RelayInfo, request any, path ...types.RelayFormat) (*relayconvert.RequestResult, error) {
	return relayconvert.ConvertRequestVia(c, info, request, path...)
}

// applyOpenAIChatStreamUsage asks compatible OpenAI Chat upstreams to report
// usage on streaming cross-protocol requests. The downstream protocol cannot
// carry OpenAI Chat's stream_options field itself, while billing still needs
// the upstream usage frame.
func applyOpenAIChatStreamUsage(info *relaycommon.RelayInfo, result *relayconvert.RequestResult) {
	if info == nil || info.ChannelMeta == nil || result == nil || !info.SupportStreamOptions || !info.IsStream {
		return
	}
	if result.From == result.To || result.To != types.RelayFormatOpenAI {
		return
	}
	request, ok := result.Value.(*dto.GeneralOpenAIRequest)
	if !ok || request == nil {
		return
	}
	request.StreamOptions = &dto.StreamOptions{IncludeUsage: true}
}

func ClaudeToOpenAIRequest(claudeRequest dto.ClaudeRequest, info *relaycommon.RelayInfo) (*dto.GeneralOpenAIRequest, error) {
	result, err := ConvertRequest(nil, info, types.RelayFormatOpenAI, &claudeRequest)
	if err != nil {
		return nil, err
	}
	openAIRequest, ok := result.Value.(*dto.GeneralOpenAIRequest)
	if !ok {
		return nil, fmt.Errorf("expected OpenAI chat completions request, got %T", result.Value)
	}
	return openAIRequest, nil
}

func GeminiToOpenAIRequest(geminiRequest *dto.GeminiChatRequest, info *relaycommon.RelayInfo) (*dto.GeneralOpenAIRequest, error) {
	result, err := ConvertRequest(nil, info, types.RelayFormatOpenAI, geminiRequest)
	if err != nil {
		return nil, err
	}
	openAIRequest, ok := result.Value.(*dto.GeneralOpenAIRequest)
	if !ok {
		return nil, fmt.Errorf("expected OpenAI chat completions request, got %T", result.Value)
	}
	return openAIRequest, nil
}
