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
	applyCrossProtocolStreamUsage(info, result)
	return result, nil
}

func ConvertRequestByID(c *gin.Context, info *relaycommon.RelayInfo, converter string, request any) (*relayconvert.RequestResult, error) {
	result, err := relayconvert.ConvertRequestByID(c, info, converter, request)
	if err != nil {
		return nil, err
	}
	applyCrossProtocolStreamUsage(info, result)
	return result, nil
}

func ConvertRequestVia(c *gin.Context, info *relaycommon.RelayInfo, request any, path ...types.RelayFormat) (*relayconvert.RequestResult, error) {
	result, err := relayconvert.ConvertRequestVia(c, info, request, path...)
	if err != nil {
		return nil, err
	}
	applyCrossProtocolStreamUsage(info, result)
	return result, nil
}

// applyCrossProtocolStreamUsage enforces the host-side OpenAI Chat streaming
// contract after format conversion. Downstream Claude, Gemini, and Responses
// requests cannot carry OpenAI Chat's stream_options contract themselves, so
// a converted streaming request must explicitly ask a compatible upstream to
// include usage for billing and usage accounting.
func applyCrossProtocolStreamUsage(info *relaycommon.RelayInfo, result *relayconvert.RequestResult) {
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
