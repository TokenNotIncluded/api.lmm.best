package cohere

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	"github.com/LIghtJUNction/api.lmm.best/relay/helper"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/types"
	"github.com/LIghtJUNction/api.lmm.best/service"

	"github.com/gin-gonic/gin"
	"github.com/samber/lo"
)

func requestOpenAI2Cohere(textRequest dto.GeneralOpenAIRequest) *CohereRequest {
	cohereReq := CohereRequest{
		Model:       textRequest.Model,
		ChatHistory: []ChatHistory{},
		Message:     "",
		Stream:      lo.FromPtrOr(textRequest.Stream, false),
		MaxTokens:   textRequest.GetMaxTokens(),
	}
	if common.CohereSafetySetting != "NONE" {
		cohereReq.SafetyMode = common.CohereSafetySetting
	}
	if cohereReq.MaxTokens == 0 {
		cohereReq.MaxTokens = 4000
	}
	for _, msg := range textRequest.Messages {
		if msg.Role == "user" {
			cohereReq.Message = msg.StringContent()
		} else {
			var role string
			if msg.Role == "assistant" {
				role = "CHATBOT"
			} else if msg.Role == "system" {
				role = "SYSTEM"
			} else {
				role = "USER"
			}
			cohereReq.ChatHistory = append(cohereReq.ChatHistory, ChatHistory{
				Role:    role,
				Message: msg.StringContent(),
			})
		}
	}

	return &cohereReq
}

func requestConvertRerank2Cohere(rerankRequest dto.RerankRequest) *CohereRerankRequest {
	topN := lo.FromPtrOr(rerankRequest.TopN, 1)
	if topN <= 0 {
		topN = 1
	}
	cohereReq := CohereRerankRequest{
		Query:           rerankRequest.Query,
		Documents:       rerankRequest.Documents,
		Model:           rerankRequest.Model,
		TopN:            topN,
		ReturnDocuments: true,
	}
	return &cohereReq
}

func stopReasonCohere2OpenAI(reason string) string {
	switch reason {
	case "COMPLETE":
		return "stop"
	case "MAX_TOKENS":
		return "max_tokens"
	default:
		return reason
	}
}

func cohereStreamHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	defer service.CloseResponseBodyGracefully(resp)
	ctx, cancel := context.WithCancel(c.Request.Context())
	defer cancel()
	info.RateLimitStreamStatus = relaycommon.NewStreamStatus()
	responseId := helper.GetResponseID(c)
	createdTime := common.GetTimestamp()
	usage := &dto.Usage{}
	responseText := ""
	if err := helper.CommitEventStreamHeaders(c); err != nil {
		info.RateLimitStreamStatus.SetEndReason(relaycommon.StreamEndReasonClientGone, err)
		info.RateLimitStreamStatus.RecordError("downstream header commit failed")
		return nil, types.NewError(err, types.ErrorCodeBadResponseBody, types.ErrOptionWithSkipRetry())
	}
	scanner := helper.NewStreamScanner(resp.Body)
	scanner.Split(func(data []byte, atEOF bool) (advance int, token []byte, err error) {
		if atEOF && len(data) == 0 {
			return 0, nil, nil
		}
		if i := strings.Index(string(data), "\n"); i >= 0 {
			return i + 1, data[0:i], nil
		}
		if atEOF {
			return len(data), data, nil
		}
		return 0, nil, nil
	})
	dataChan := make(chan string)
	stopChan := make(chan error, 1)
	go func() {
		for scanner.Scan() {
			data := scanner.Text()
			if !helper.SendCtx(ctx, dataChan, data) {
				return
			}
		}
		err := scanner.Err()
		if err != nil {
			common.SysLog("error reading stream: " + err.Error())
		}
		helper.SendCtx(ctx, stopChan, err)
	}()
	isFirst := true
	finished := false
	// Keep reading terminal usage after a failed write; settlement still needs
	// the upstream's billing facts even though this stream cannot count as success.
	writeFailed := false
	writeData := func(data string) {
		if writeFailed {
			return
		}
		err := ctx.Err()
		if err == nil {
			helper.ExtendWriteDeadline(c)
			previousErrors := len(c.Errors)
			err = helper.StringData(c, data)
			if len(c.Errors) > previousErrors {
				err = c.Errors.Last().Err
			}
		}
		if err != nil {
			helper.MarkHTTPStreamDownstreamFailure(c)
			info.RateLimitStreamStatus.SetEndReason(relaycommon.StreamEndReasonClientGone, err)
			info.RateLimitStreamStatus.RecordError("downstream stream write failed")
			writeFailed = true
		}
	}
streamLoop:
	for {
		if err := ctx.Err(); err != nil {
			helper.MarkHTTPStreamDownstreamFailure(c)
			info.RateLimitStreamStatus.SetEndReason(relaycommon.StreamEndReasonClientGone, err)
			break
		}
		select {
		case <-ctx.Done():
			helper.MarkHTTPStreamDownstreamFailure(c)
			info.RateLimitStreamStatus.SetEndReason(relaycommon.StreamEndReasonClientGone, ctx.Err())
			break streamLoop
		case data := <-dataChan:
			if isFirst {
				isFirst = false
				info.FirstResponseTime = time.Now()
			}
			data = strings.TrimSuffix(data, "\r")
			var cohereResp CohereResponse
			err := json.Unmarshal([]byte(data), &cohereResp)
			if err != nil {
				common.SysLog("error unmarshalling stream response: " + err.Error())
				info.RateLimitStreamStatus.RecordError("invalid upstream stream response")
				continue
			}
			var openaiResp dto.ChatCompletionsStreamResponse
			openaiResp.Id = responseId
			openaiResp.Created = createdTime
			openaiResp.Object = "chat.completion.chunk"
			openaiResp.Model = info.UpstreamModelName
			if cohereResp.IsFinished {
				finished = true
				if cohereResp.FinishReason == "ERROR" || strings.HasPrefix(cohereResp.FinishReason, "ERROR_") {
					info.RateLimitStreamStatus.RecordError("upstream stream terminal failure")
				}
				finishReason := stopReasonCohere2OpenAI(cohereResp.FinishReason)
				openaiResp.Choices = []dto.ChatCompletionsStreamResponseChoice{
					{
						Delta:        dto.ChatCompletionsStreamResponseChoiceDelta{},
						Index:        0,
						FinishReason: &finishReason,
					},
				}
				if cohereResp.Response != nil {
					usage.PromptTokens = cohereResp.Response.Meta.BilledUnits.InputTokens
					usage.CompletionTokens = cohereResp.Response.Meta.BilledUnits.OutputTokens
				}
			} else {
				openaiResp.Choices = []dto.ChatCompletionsStreamResponseChoice{
					{
						Delta: dto.ChatCompletionsStreamResponseChoiceDelta{
							Role:    "assistant",
							Content: &cohereResp.Text,
						},
						Index: 0,
					},
				}
				responseText += cohereResp.Text
			}
			jsonStr, err := json.Marshal(openaiResp)
			if err != nil {
				common.SysLog("error marshalling stream response: " + err.Error())
				info.RateLimitStreamStatus.RecordError("failed to encode stream response")
				continue
			}
			writeData(string(jsonStr))
		case err := <-stopChan:
			if err != nil {
				info.RateLimitStreamStatus.SetEndReason(relaycommon.StreamEndReasonScannerErr, err)
			} else if !finished {
				info.RateLimitStreamStatus.RecordError("upstream stream ended without a terminal response")
			}
			writeData("[DONE]")
			info.RateLimitStreamStatus.SetEndReason(relaycommon.StreamEndReasonEOF, nil)
			break streamLoop
		}
	}
	if usage.PromptTokens == 0 {
		usage = service.ResponseText2Usage(c, responseText, info.UpstreamModelName, info.GetEstimatePromptTokens())
	}
	return usage, nil
}

func cohereHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	createdTime := common.GetTimestamp()
	responseBody, err := common.ReadResponseBody(resp)
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeBadResponseBody)
	}
	service.CloseResponseBodyGracefully(resp)
	var cohereResp CohereResponseResult
	err = json.Unmarshal(responseBody, &cohereResp)
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeBadResponseBody)
	}
	usage := dto.Usage{}
	usage.PromptTokens = cohereResp.Meta.BilledUnits.InputTokens
	usage.CompletionTokens = cohereResp.Meta.BilledUnits.OutputTokens
	usage.TotalTokens = cohereResp.Meta.BilledUnits.InputTokens + cohereResp.Meta.BilledUnits.OutputTokens

	var openaiResp dto.TextResponse
	openaiResp.Id = cohereResp.ResponseId
	openaiResp.Created = createdTime
	openaiResp.Object = "chat.completion"
	openaiResp.Model = info.UpstreamModelName
	openaiResp.Usage = usage

	openaiResp.Choices = []dto.OpenAITextResponseChoice{
		{
			Index:        0,
			Message:      dto.Message{Content: cohereResp.Text, Role: "assistant"},
			FinishReason: stopReasonCohere2OpenAI(cohereResp.FinishReason),
		},
	}

	jsonResponse, err := json.Marshal(openaiResp)
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeBadResponseBody)
	}
	c.Writer.Header().Set("Content-Type", "application/json")
	c.Writer.WriteHeader(resp.StatusCode)
	_, _ = service.WriteResponseBytes(c, jsonResponse)
	return &usage, nil
}

func cohereRerankHandler(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (*dto.Usage, *types.NewAPIError) {
	responseBody, err := common.ReadResponseBody(resp)
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeBadResponseBody)
	}
	service.CloseResponseBodyGracefully(resp)
	var cohereResp CohereRerankResponseResult
	err = json.Unmarshal(responseBody, &cohereResp)
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeBadResponseBody)
	}
	usage := dto.Usage{}
	if cohereResp.Meta.BilledUnits.InputTokens == 0 {
		usage.PromptTokens = info.GetEstimatePromptTokens()
		usage.CompletionTokens = 0
		usage.TotalTokens = info.GetEstimatePromptTokens()
	} else {
		usage.PromptTokens = cohereResp.Meta.BilledUnits.InputTokens
		usage.CompletionTokens = cohereResp.Meta.BilledUnits.OutputTokens
		usage.TotalTokens = cohereResp.Meta.BilledUnits.InputTokens + cohereResp.Meta.BilledUnits.OutputTokens
	}

	var rerankResp dto.RerankResponse
	rerankResp.Results = cohereResp.Results
	rerankResp.Usage = usage

	jsonResponse, err := json.Marshal(rerankResp)
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeBadResponseBody)
	}
	c.Writer.Header().Set("Content-Type", "application/json")
	c.Writer.WriteHeader(resp.StatusCode)
	_, _ = service.WriteResponseBytes(c, jsonResponse)
	return &usage, nil
}
