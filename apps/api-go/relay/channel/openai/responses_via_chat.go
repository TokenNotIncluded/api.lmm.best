package openai

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/logger"
	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	"github.com/LIghtJUNction/api.lmm.best/relay/helper"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/relayconvert"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/types"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/gin-gonic/gin"
)

func OaiChatToResponsesHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	if resp == nil || resp.Body == nil {
		return nil, types.NewOpenAIError(fmt.Errorf("invalid response"), types.ErrorCodeBadResponse, http.StatusInternalServerError)
	}
	defer service.CloseResponseBodyGracefully(resp)

	body, err := common.ReadResponseBody(resp)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeReadResponseBodyFailed, http.StatusInternalServerError)
	}

	var chatResp dto.OpenAITextResponse
	if err := common.Unmarshal(body, &chatResp); err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
	}
	if oaiError := chatResp.GetOpenAIError(); oaiError != nil && oaiError.Type != "" {
		return nil, types.WithOpenAIError(*oaiError, resp.StatusCode)
	}

	if responseID := helper.GetResponseID(c); responseID != "" {
		chatResp.Id = responseID
	}
	convertResult, err := relayconvert.ConvertResponse(c, info, types.RelayFormatOpenAIResponses, &chatResp)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
	}
	responsesResp, ok := convertResult.Value.(*dto.OpenAIResponsesResponse)
	if !ok {
		return nil, types.NewOpenAIError(fmt.Errorf("expected OpenAI responses response, got %T", convertResult.Value), types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
	}
	usage := convertResult.Usage
	if usage == nil || usage.TotalTokens == 0 {
		text := service.ExtractOutputTextFromResponses(responsesResp)
		usage = service.ResponseText2Usage(c, text, info.UpstreamModelName, info.GetEstimatePromptTokens())
		responsesResp.Usage = relayconvert.UsageFromChatUsage(usage)
	}

	responseBody, err := common.Marshal(responsesResp)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeJsonMarshalFailed, http.StatusInternalServerError)
	}

	service.IOCopyBytesGracefully(c, resp, responseBody)
	return usage, nil
}

func OaiChatToResponsesStreamHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	if resp == nil || resp.Body == nil {
		return nil, types.NewOpenAIError(fmt.Errorf("invalid response"), types.ErrorCodeBadResponse, http.StatusInternalServerError)
	}
	defer service.CloseResponseBodyGracefully(resp)

	responseID := helper.GetResponseID(c)
	state, err := relayconvert.NewResponseStreamState(types.RelayFormatOpenAI, types.RelayFormatOpenAIResponses, relayconvert.ResponseStreamOptions{
		ID:    responseID,
		Model: info.UpstreamModelName,
	})
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponse, http.StatusInternalServerError)
	}
	streamErr := (*types.NewAPIError)(nil)
	upstreamAPIError := false
	writeFailed := false
	endEvidence := chatResponsesEndEvidence{choices: make(map[int]bool)}

	sendEvent := func(event relayconvert.ChatToResponsesStreamEvent) bool {
		data, err := common.Marshal(event.Payload)
		if err != nil {
			streamErr = types.NewOpenAIError(err, types.ErrorCodeJsonMarshalFailed, http.StatusInternalServerError)
			return false
		}
		if err := writeResponsesEvent(c, event.Type, string(data)); err != nil {
			writeFailed = true
			info.StreamStatus.RecordError("downstream stream write failed")
			streamErr = types.NewOpenAIError(fmt.Errorf("downstream stream write failed"), types.ErrorCodeBadResponseBody, http.StatusBadGateway)
			return false
		}
		return true
	}

	helper.StreamScannerHandler(c, resp, info, func(data string, sr *helper.StreamResult) {
		if streamErr != nil {
			sr.Stop(streamErr)
			return
		}

		var errorResp dto.OpenAITextResponse
		if err := common.UnmarshalJsonStr(data, &errorResp); err == nil {
			if oaiError := errorResp.GetOpenAIError(); oaiError != nil && oaiError.Type != "" {
				upstreamAPIError = true
				streamErr = types.WithOpenAIError(*oaiError, resp.StatusCode)
				sr.Stop(streamErr)
				return
			}
		}

		var chunk dto.ChatCompletionsStreamResponse
		if err := common.UnmarshalJsonStr(data, &chunk); err != nil {
			logger.LogError(c, "failed to unmarshal chat stream response: "+err.Error())
			sr.Error(err)
			return
		}
		endEvidence.observe(&chunk)

		results, err := relayconvert.ConvertStreamResponseChunk(c, info, state, &chunk)
		if err != nil {
			streamErr = types.NewOpenAIError(err, types.ErrorCodeBadResponse, http.StatusInternalServerError)
			sr.Stop(streamErr)
			return
		}
		for _, result := range results {
			event, ok := result.Value.(relayconvert.ChatToResponsesStreamEvent)
			if !ok {
				streamErr = types.NewOpenAIError(fmt.Errorf("expected OAI responses stream event, got %T", result.Value), types.ErrorCodeBadResponse, http.StatusInternalServerError)
				sr.Stop(streamErr)
				return
			}
			if !sendEvent(event) {
				sr.Stop(streamErr)
				return
			}
		}
	})

	usage := state.Usage()
	if usage == nil || usage.TotalTokens == 0 {
		usage = service.ResponseText2Usage(c, state.UsageText(), info.UpstreamModelName, info.GetEstimatePromptTokens())
		state.SetUsage(usage)
	}
	// A write failure or canceled downstream must not trigger retries or another
	// terminal write after consumption. Keep the existing usage/settlement path.
	if writeFailed || c.Request.Context().Err() != nil || info.StreamStatus.EndReason == relaycommon.StreamEndReasonClientGone || info.StreamStatus.EndReason == relaycommon.StreamEndReasonPingFail {
		return usage, nil
	}
	if upstreamAPIError && !c.Writer.Written() {
		return usage, streamErr
	}
	complete := streamErr == nil && !info.StreamStatus.HasErrors() && endEvidence.valid && !endEvidence.invalid &&
		(info.StreamStatus.EndReason == relaycommon.StreamEndReasonDone ||
			(info.StreamStatus.EndReason == relaycommon.StreamEndReasonEOF && endEvidence.finished()))
	fail := func() (*dto.Usage, *types.NewAPIError) {
		const message = "Upstream response ended before a terminal event"
		info.StreamStatus.RecordError(message)
		if !c.Writer.Written() {
			return usage, types.NewOpenAIError(fmt.Errorf("%s", message), types.ErrorCodeBadResponseBody, http.StatusBadGateway)
		}
		response, err := relayconvert.FailChatToResponsesStream(state, "upstream_stream_interrupted", message)
		if err != nil {
			info.StreamStatus.RecordError("failed to snapshot interrupted Responses stream")
			return usage, nil
		}
		if response != nil {
			sendEvent(relayconvert.ChatToResponsesStreamEvent{Type: "response.failed", Payload: dto.ResponsesStreamResponse{Type: "response.failed", Response: response}})
		}
		return usage, nil
	}
	if !complete {
		return fail()
	}

	finalResults, err := relayconvert.FinalizeStreamResponse(c, info, state)
	if err != nil {
		return fail()
	}
	for _, result := range finalResults {
		event, ok := result.Value.(relayconvert.ChatToResponsesStreamEvent)
		if !ok {
			return fail()
		}
		if !sendEvent(event) {
			return usage, nil
		}
	}

	return usage, nil
}

// A finish reason closes the current choice, not any future resumed segment.
// Usage-only chunks preserve that evidence while subsequent content revokes it.
type chatResponsesEndEvidence struct {
	valid   bool
	invalid bool
	choices map[int]bool
}

func (e *chatResponsesEndEvidence) observe(chunk *dto.ChatCompletionsStreamResponse) {
	if len(chunk.Choices) > 0 || chunk.Usage != nil {
		e.valid = true
	}
	for _, choice := range chunk.Choices {
		finished := e.choices[choice.Index]
		delta := choice.Delta
		resumed := delta.GetContentString() != "" || delta.GetReasoningContent() != ""
		for _, tool := range delta.ToolCalls {
			resumed = resumed || tool.ID != "" || tool.Function.Name != "" || tool.Function.Arguments != ""
		}
		if resumed {
			finished = false
		}
		if choice.FinishReason != nil && strings.TrimSpace(*choice.FinishReason) != "" {
			switch strings.TrimSpace(*choice.FinishReason) {
			case "stop", "tool_calls", "function_call", "length", "content_filter":
				finished = true
			default:
				finished = false
				e.invalid = true
			}
		}
		e.choices[choice.Index] = finished
	}
}

func (e *chatResponsesEndEvidence) finished() bool {
	if len(e.choices) == 0 {
		return false
	}
	for _, finished := range e.choices {
		if !finished {
			return false
		}
	}
	return true
}
