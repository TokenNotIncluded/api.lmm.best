package palm

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/constant"
	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	"github.com/LIghtJUNction/api.lmm.best/relay/helper"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/types"
	"github.com/LIghtJUNction/api.lmm.best/service"

	"github.com/gin-gonic/gin"
)

// https://developers.generativeai.google/api/rest/generativelanguage/models/generateMessage#request-body
// https://developers.generativeai.google/api/rest/generativelanguage/models/generateMessage#response-body

func responsePaLM2OpenAI(response *PaLMChatResponse) *dto.OpenAITextResponse {
	fullTextResponse := dto.OpenAITextResponse{
		Choices: make([]dto.OpenAITextResponseChoice, 0, len(response.Candidates)),
	}
	for i, candidate := range response.Candidates {
		choice := dto.OpenAITextResponseChoice{
			Index: i,
			Message: dto.Message{
				Role:    "assistant",
				Content: candidate.Content,
			},
			FinishReason: "stop",
		}
		fullTextResponse.Choices = append(fullTextResponse.Choices, choice)
	}
	return &fullTextResponse
}

func streamResponsePaLM2OpenAI(palmResponse *PaLMChatResponse) *dto.ChatCompletionsStreamResponse {
	var choice dto.ChatCompletionsStreamResponseChoice
	if len(palmResponse.Candidates) > 0 {
		choice.Delta.SetContentString(palmResponse.Candidates[0].Content)
	}
	choice.FinishReason = &constant.FinishReasonStop
	var response dto.ChatCompletionsStreamResponse
	response.Object = "chat.completion.chunk"
	response.Model = "palm2"
	response.Choices = []dto.ChatCompletionsStreamResponseChoice{choice}
	return &response
}

func palmStreamHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*types.NewAPIError, string) {
	info.RateLimitStreamStatus = relaycommon.NewStreamStatus()
	status := info.RateLimitStreamStatus
	defer service.CloseResponseBodyGracefully(resp)
	ctx, cancel := context.WithCancel(c.Request.Context())
	defer cancel()
	defer func() {
		if err := c.Request.Context().Err(); err != nil {
			helper.MarkHTTPStreamDownstreamFailure(c)
			status.RecordError("request_canceled")
			status.SetEndReason(relaycommon.StreamEndReasonClientGone, err)
		}
		if writerErr := c.Errors.Last(); writerErr != nil {
			status.RecordError("write_response")
			status.SetEndReason(relaycommon.StreamEndReasonClientGone, writerErr.Err)
		}
		status.SetEndReason(relaycommon.StreamEndReasonDone, nil)
	}()
	responseText := ""
	responseId := helper.GetResponseID(c)
	createdTime := common.GetTimestamp()
	if err := helper.CommitEventStreamHeaders(c); err != nil {
		status.RecordError("downstream header commit failed")
		status.SetEndReason(relaycommon.StreamEndReasonClientGone, err)
		return types.NewError(err, types.ErrorCodeBadResponseBody, types.ErrOptionWithSkipRetry()), ""
	}
	type streamData struct {
		json string
		text string
	}
	dataChan := make(chan streamData)
	stopChan := make(chan bool)
	go func() {
		responseBody, err := common.ReadResponseBody(resp)
		if err != nil {
			status.RecordError("read_response")
			status.SetEndReason(relaycommon.StreamEndReasonScannerErr, err)
			common.SysLog("error reading stream response: " + err.Error())
			helper.SendCtx(ctx, stopChan, true)
			return
		}
		var palmResponse PaLMChatResponse
		err = json.Unmarshal(responseBody, &palmResponse)
		if err != nil {
			status.RecordError("decode_response")
			status.SetEndReason(relaycommon.StreamEndReasonHandlerStop, err)
			common.SysLog("error unmarshalling stream response: " + err.Error())
			helper.SendCtx(ctx, stopChan, true)
			return
		}
		fullTextResponse := streamResponsePaLM2OpenAI(&palmResponse)
		fullTextResponse.Id = responseId
		fullTextResponse.Created = createdTime
		text := ""
		if palmResponse.Error.Code != 0 || len(palmResponse.Candidates) == 0 {
			status.RecordError("upstream_error")
		}
		if len(palmResponse.Candidates) > 0 {
			text = palmResponse.Candidates[0].Content
		}
		jsonResponse, err := json.Marshal(fullTextResponse)
		if err != nil {
			status.RecordError("encode_response")
			status.SetEndReason(relaycommon.StreamEndReasonHandlerStop, err)
			common.SysLog("error marshalling stream response: " + err.Error())
			helper.SendCtx(ctx, stopChan, true)
			return
		}
		if !helper.SendCtx(ctx, dataChan, streamData{json: string(jsonResponse), text: text}) {
			return
		}
		helper.SendCtx(ctx, stopChan, true)
	}()
	for {
		select {
		case data := <-dataChan:
			responseText = data.text
			if err := helper.StringData(c, data.json); err != nil {
				helper.MarkHTTPStreamDownstreamFailure(c)
				status.RecordError("write_response")
				status.SetEndReason(relaycommon.StreamEndReasonClientGone, err)
				return nil, responseText
			}
		case <-stopChan:
			if err := helper.StringData(c, "[DONE]"); err != nil {
				helper.MarkHTTPStreamDownstreamFailure(c)
				status.RecordError("write_response")
				status.SetEndReason(relaycommon.StreamEndReasonClientGone, err)
			}
			return nil, responseText
		case <-ctx.Done():
			helper.MarkHTTPStreamDownstreamFailure(c)
			status.RecordError("request_canceled")
			status.SetEndReason(relaycommon.StreamEndReasonClientGone, ctx.Err())
			return nil, responseText
		}
	}
}

func palmHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	responseBody, err := common.ReadResponseBody(resp)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeReadResponseBodyFailed, http.StatusInternalServerError)
	}
	service.CloseResponseBodyGracefully(resp)
	var palmResponse PaLMChatResponse
	err = json.Unmarshal(responseBody, &palmResponse)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
	}
	if palmResponse.Error.Code != 0 || len(palmResponse.Candidates) == 0 {
		return nil, types.WithOpenAIError(types.OpenAIError{
			Message: palmResponse.Error.Message,
			Type:    palmResponse.Error.Status,
			Param:   "",
			Code:    palmResponse.Error.Code,
		}, resp.StatusCode)
	}
	fullTextResponse := responsePaLM2OpenAI(&palmResponse)
	usage := service.ResponseText2Usage(c, palmResponse.Candidates[0].Content, info.UpstreamModelName, info.GetEstimatePromptTokens())
	fullTextResponse.Usage = *usage
	jsonResponse, err := common.Marshal(fullTextResponse)
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeBadResponseBody)
	}
	c.Writer.Header().Set("Content-Type", "application/json")
	c.Writer.WriteHeader(resp.StatusCode)
	service.IOCopyBytesGracefully(c, resp, jsonResponse)
	return usage, nil
}
