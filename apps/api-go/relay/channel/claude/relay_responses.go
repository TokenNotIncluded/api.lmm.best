package claude

import (
	"fmt"

	"github.com/LIghtJUNction/api.lmm.best/common"
	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	"github.com/LIghtJUNction/api.lmm.best/relay/helper"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/relayconvert"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/types"
	"github.com/gin-gonic/gin"
)

const claudeResponsesStreamKey = "claude_responses_stream_state"
const claudeResponsesWriteFailedKey = "claude_responses_write_failed"

func claudeStreamCompleted(c *gin.Context, info *relaycommon.RelayInfo, claudeInfo *ClaudeResponseInfo) bool {
	status := info.StreamStatus
	state := claudeRefusalBillingState(c)
	return claudeInfo.Done && state.finalStopReason != "" && state.messageStopped &&
		(status == nil || (status.IsNormalEnd() && status.EndError == nil && !status.HasErrors()))
}

func claudeResponsesStreamState(c *gin.Context, info *relaycommon.RelayInfo) (*relayconvert.ResponseStreamState, error) {
	if state, ok := c.Get(claudeResponsesStreamKey); ok {
		if state, ok := state.(*relayconvert.ResponseStreamState); ok && state != nil {
			return state, nil
		}
	}
	state, err := relayconvert.NewResponseStreamState(types.RelayFormatOpenAI, types.RelayFormatOpenAIResponses, relayconvert.ResponseStreamOptions{
		ID: helper.GetResponseID(c), Model: info.UpstreamModelName, Created: common.GetTimestamp(), IncludeUsage: true,
	})
	if err == nil {
		c.Set(claudeResponsesStreamKey, state)
	}
	return state, err
}

func sendClaudeResponsesStreamResults(c *gin.Context, results []relayconvert.ResponseResult) *types.NewAPIError {
	for _, result := range results {
		event, ok := result.Value.(relayconvert.ChatToResponsesStreamEvent)
		if !ok {
			return types.NewError(fmt.Errorf("expected Responses stream event, got %T", result.Value), types.ErrorCodeBadResponseBody)
		}
		data, err := common.Marshal(event.Payload)
		if err != nil {
			return types.NewError(err, types.ErrorCodeJsonMarshalFailed)
		}
		if err := helper.ResponseChunkData(c, dto.ResponsesStreamResponse{Type: event.Type}, string(data)); err != nil {
			c.Set(claudeResponsesWriteFailedKey, true)
			return types.NewError(err, types.ErrorCodeBadResponseBody)
		}
	}
	return nil
}

func sendClaudeResponsesStreamChunk(c *gin.Context, info *relaycommon.RelayInfo, response *dto.ChatCompletionsStreamResponse) *types.NewAPIError {
	state, err := claudeResponsesStreamState(c, info)
	if err != nil {
		return types.NewError(err, types.ErrorCodeBadResponseBody)
	}
	results, err := relayconvert.ConvertStreamResponseChunk(c, info, state, response)
	if err != nil {
		return types.NewError(err, types.ErrorCodeBadResponseBody)
	}
	return sendClaudeResponsesStreamResults(c, results)
}

func finalizeClaudeResponsesStream(c *gin.Context, info *relaycommon.RelayInfo, usage *dto.Usage, complete bool) *types.NewAPIError {
	// Preserve measured consumption without replaying upstream work or writing
	// another event to a disconnected client.
	if c.GetBool(claudeResponsesWriteFailedKey) || c.Request.Context().Err() != nil {
		return nil
	}
	state, err := claudeResponsesStreamState(c, info)
	if err != nil {
		return types.NewError(err, types.ErrorCodeBadResponseBody)
	}
	state.SetUsage(relayconvert.UsageFromClaudeUsage(usage))
	if !complete {
		const message = "Upstream response ended before a terminal event"
		info.StreamStatus.RecordError(message)
		if !c.Writer.Written() {
			return types.NewError(fmt.Errorf("%s", message), types.ErrorCodeBadResponseBody)
		}
		response, err := relayconvert.FailChatToResponsesStream(state, "upstream_stream_interrupted", message)
		if err != nil {
			return types.NewError(err, types.ErrorCodeBadResponseBody)
		}
		return sendClaudeResponsesStreamResults(c, []relayconvert.ResponseResult{{Value: relayconvert.ChatToResponsesStreamEvent{
			Type: "response.failed", Payload: dto.ResponsesStreamResponse{Type: "response.failed", Response: response},
		}}})
	}
	results, err := relayconvert.FinalizeStreamResponse(c, info, state)
	if err != nil {
		return types.NewError(err, types.ErrorCodeBadResponseBody)
	}
	return sendClaudeResponsesStreamResults(c, results)
}
