package cloudflare

import (
	"bufio"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/logger"
	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	"github.com/LIghtJUNction/api.lmm.best/relay/helper"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/types"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/samber/lo"

	"github.com/gin-gonic/gin"
)

func convertCf2CompletionsRequest(textRequest dto.GeneralOpenAIRequest) *CfRequest {
	p, _ := textRequest.Prompt.(string)
	return &CfRequest{
		Prompt:      p,
		MaxTokens:   textRequest.GetMaxTokens(),
		Stream:      lo.FromPtrOr(textRequest.Stream, false),
		Temperature: textRequest.Temperature,
	}
}

func cfStreamHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*types.NewAPIError, *dto.Usage) {
	defer service.CloseResponseBodyGracefully(resp)
	if err := helper.ValidateEventStreamResponse(resp); err != nil {
		return types.NewErrorWithStatusCode(err, types.ErrorCodeBadResponse, http.StatusBadGateway), nil
	}
	info.RateLimitStreamStatus = relaycommon.NewStreamStatus()
	status := info.RateLimitStreamStatus
	endedWithDone := false
	defer func() {
		if writerErr := c.Errors.Last(); writerErr != nil {
			status.RecordError("write_response")
			status.SetEndReason(relaycommon.StreamEndReasonClientGone, writerErr.Err)
		}
		if c.Request != nil && c.Request.Context().Err() != nil {
			helper.MarkHTTPStreamDownstreamFailure(c)
			status.RecordError("request_canceled")
			status.SetEndReason(relaycommon.StreamEndReasonClientGone, c.Request.Context().Err())
		}
		if endedWithDone {
			status.SetEndReason(relaycommon.StreamEndReasonDone, nil)
		} else {
			status.SetEndReason(relaycommon.StreamEndReasonEOF, nil)
		}
	}()
	scanner := helper.NewStreamScanner(resp.Body)
	scanner.Split(bufio.ScanLines)

	if err := helper.CommitEventStreamResponseHeaders(c, resp); err != nil {
		status.RecordError("downstream header commit failed")
		status.SetEndReason(relaycommon.StreamEndReasonClientGone, err)
		return types.NewError(err, types.ErrorCodeBadResponseBody, types.ErrOptionWithSkipRetry()), nil
	}
	writeFailed := false
	id := helper.GetResponseID(c)
	var responseText string
	isFirst := true

	for scanner.Scan() {
		data := scanner.Text()
		if len(data) < len("data: ") {
			continue
		}
		data = strings.TrimPrefix(data, "data: ")
		data = strings.TrimSuffix(data, "\r")

		if data == "[DONE]" {
			endedWithDone = true
			break
		}

		var response dto.ChatCompletionsStreamResponse
		err := json.Unmarshal([]byte(data), &response)
		if err != nil {
			status.RecordError("decode_response")
			logger.LogError(c, "error_unmarshalling_stream_response: "+err.Error())
			continue
		}
		// Decode outcome fields separately: embedding the response would promote
		// its custom UnmarshalJSON and leave these sibling fields untouched.
		var outcome struct {
			Error   json.RawMessage `json:"error"`
			Success *bool           `json:"success"`
		}
		if err := json.Unmarshal([]byte(data), &outcome); err != nil {
			status.RecordError("decode_response")
		}
		if (len(outcome.Error) > 0 && string(outcome.Error) != "null") ||
			(outcome.Success != nil && !*outcome.Success) {
			status.RecordError("upstream_error")
		}
		for _, choice := range response.Choices {
			choice.Delta.Role = "assistant"
			responseText += choice.Delta.GetContentString()
		}
		response.Id = id
		response.Model = info.UpstreamModelName
		if writeFailed {
			continue
		}
		err = helper.ObjectData(c, response)
		if isFirst {
			isFirst = false
			info.FirstResponseTime = time.Now()
		}
		if err != nil {
			helper.MarkHTTPStreamDownstreamFailure(c)
			writeFailed = true
			status.SetEndReason(relaycommon.StreamEndReasonClientGone, err)
			status.RecordError("write_response")
			logger.LogError(c, "error_rendering_stream_response: "+err.Error())
		}
	}

	if err := scanner.Err(); err != nil {
		status.RecordError("scan_response")
		status.SetEndReason(relaycommon.StreamEndReasonScannerErr, err)
		logger.LogError(c, "error_scanning_stream_response: "+err.Error())
	}
	usage := service.ResponseText2Usage(c, responseText, info.UpstreamModelName, info.GetEstimatePromptTokens())
	if info.ShouldIncludeUsage && !writeFailed {
		response := helper.GenerateFinalUsageResponse(id, info.StartTime.Unix(), info.UpstreamModelName, *usage)
		err := helper.ObjectData(c, response)
		if err != nil {
			helper.MarkHTTPStreamDownstreamFailure(c)
			writeFailed = true
			status.SetEndReason(relaycommon.StreamEndReasonClientGone, err)
			status.RecordError("write_usage")
			logger.LogError(c, "error_rendering_final_usage_response: "+err.Error())
		}
	}
	if !writeFailed {
		if err := helper.StringData(c, "[DONE]"); err != nil {
			helper.MarkHTTPStreamDownstreamFailure(c)
			status.SetEndReason(relaycommon.StreamEndReasonClientGone, err)
			status.RecordError("write_done")
		}
	}

	return nil, usage
}

func cfHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*types.NewAPIError, *dto.Usage) {
	responseBody, err := common.ReadResponseBody(resp)
	if err != nil {
		return types.NewError(err, types.ErrorCodeBadResponseBody), nil
	}
	service.CloseResponseBodyGracefully(resp)
	var response dto.TextResponse
	err = json.Unmarshal(responseBody, &response)
	if err != nil {
		return types.NewError(err, types.ErrorCodeBadResponseBody), nil
	}
	response.Model = info.UpstreamModelName
	var responseText string
	for _, choice := range response.Choices {
		responseText += choice.Message.StringContent()
	}
	usage := service.ResponseText2Usage(c, responseText, info.UpstreamModelName, info.GetEstimatePromptTokens())
	response.Usage = *usage
	response.Id = helper.GetResponseID(c)
	jsonResponse, err := json.Marshal(response)
	if err != nil {
		return types.NewError(err, types.ErrorCodeBadResponseBody), nil
	}
	c.Writer.Header().Set("Content-Type", "application/json")
	c.Writer.WriteHeader(resp.StatusCode)
	_, _ = service.WriteResponseBytes(c, jsonResponse)
	return nil, usage
}

func cfSTTHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*types.NewAPIError, *dto.Usage) {
	var cfResp CfAudioResponse
	responseBody, err := common.ReadResponseBody(resp)
	if err != nil {
		return types.NewError(err, types.ErrorCodeBadResponseBody), nil
	}
	service.CloseResponseBodyGracefully(resp)
	err = json.Unmarshal(responseBody, &cfResp)
	if err != nil {
		return types.NewError(err, types.ErrorCodeBadResponseBody), nil
	}

	audioResp := &dto.AudioResponse{
		Text: cfResp.Result.Text,
	}

	jsonResponse, err := json.Marshal(audioResp)
	if err != nil {
		return types.NewError(err, types.ErrorCodeBadResponseBody), nil
	}
	c.Writer.Header().Set("Content-Type", "application/json")
	c.Writer.WriteHeader(resp.StatusCode)
	_, _ = service.WriteResponseBytes(c, jsonResponse)

	usage := service.ResponseText2Usage(c, cfResp.Result.Text, info.UpstreamModelName, info.GetEstimatePromptTokens())
	return nil, usage
}
