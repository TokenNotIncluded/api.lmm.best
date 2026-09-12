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
	"github.com/LIghtJUNction/api.lmm.best/relaykit/types"
	"github.com/LIghtJUNction/api.lmm.best/service"

	"github.com/gin-gonic/gin"
)

func OaiResponsesHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	defer service.CloseResponseBodyGracefully(resp)

	// read response body
	var responsesResponse dto.OpenAIResponsesResponse
	responseBody, err := common.ReadResponseBody(resp)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeReadResponseBodyFailed, http.StatusInternalServerError)
	}
	err = common.Unmarshal(responseBody, &responsesResponse)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
	}
	if oaiError := responsesResponse.GetOpenAIError(); oaiError != nil && oaiError.Type != "" {
		return nil, types.WithOpenAIError(*oaiError, resp.StatusCode)
	}

	// 写入新的 response body
	service.IOCopyBytesGracefully(c, resp, responseBody)

	// compute usage
	usage := dto.Usage{}
	if responsesResponse.Usage != nil {
		service.ApplyResponsesUsage(&usage, responsesResponse.Usage)
	}
	// Count actual tool invocations from Output (not tool declarations).
	for _, output := range responsesResponse.Output {
		switch output.Type {
		case dto.BuildInCallWebSearchCall:
			info.CountBillableToolCall(dto.BuildInCallWebSearchCall, "")
		case dto.BuildInCallFileSearchCall:
			info.CountBillableToolCall(dto.BuildInCallFileSearchCall, "")
		case dto.BuildInCallFunctionCall:
			info.CountBillableToolCall(dto.BuildInCallFunctionCall, output.Name)
		}
	}

	imageCounter := &relaycommon.ImageGenerationCallCounter{}
	if !relaycommon.IsNonBillableResponsesStatus(responsesResponse.Status) {
		for i := range responsesResponse.Output {
			idx := i
			imageCounter.Observe(&responsesResponse.Output[i], &idx)
		}
	}
	imageCounter.Commit(info)

	return &usage, nil
}

func OaiResponsesStreamHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	if resp == nil || resp.Body == nil {
		logger.LogError(c, "invalid response or response body")
		return nil, types.NewError(fmt.Errorf("invalid response"), types.ErrorCodeBadResponse)
	}

	defer service.CloseResponseBodyGracefully(resp)

	var usage = &dto.Usage{}
	var responseTextBuilder strings.Builder
	imageCounter := &relaycommon.ImageGenerationCallCounter{}
	imageCommitted := false
	terminal := false
	hasUsage := false
	downstreamWriteFailed := false
	var lastResponse *dto.OpenAIResponsesResponse
	sequence := -1

	helper.StreamScannerHandler(c, resp, info, func(data string, sr *helper.StreamResult) {

		// 检查当前数据是否包含 completed 状态和 usage 信息
		var event struct {
			dto.ResponsesStreamResponse
			SequenceNumber *int `json:"sequence_number"`
		}
		if err := common.UnmarshalJsonStr(data, &event); err != nil {
			logger.LogError(c, "failed to unmarshal stream response: "+err.Error())
			sr.Error(err)
			return
		}
		streamResponse := event.ResponsesStreamResponse
		if strings.ContainsAny(streamResponse.Type, "\r\n") {
			// JSON escapes are decoded above; validate the value at the SSE
			// boundary before it can introduce another event or data field.
			sr.Error(fmt.Errorf("invalid Responses event name"))
			return
		}
		if event.SequenceNumber != nil && *event.SequenceNumber > sequence {
			sequence = *event.SequenceNumber
		}
		if streamResponse.Response != nil {
			lastResponse = streamResponse.Response
			if lastResponse.Usage != nil {
				*usage = dto.Usage{}
				service.ApplyResponsesUsage(usage, lastResponse.Usage)
				hasUsage = true
			}
		}
		writeErr := writeResponsesEvent(c, streamResponse.Type, data)
		switch streamResponse.Type {
		case "response.completed", "response.done":
			terminal = true
			if streamResponse.Response != nil {
				if !imageCommitted {
					if relaycommon.IsNonBillableResponsesStatus(streamResponse.Response.Status) {
						imageCounter.Reset()
						imageCounter.Commit(info)
						imageCommitted = true
					} else {
						for i := range streamResponse.Response.Output {
							idx := i
							imageCounter.Observe(&streamResponse.Response.Output[i], &idx)
						}
						imageCounter.Commit(info)
						imageCommitted = true
					}
				}
			} else if !imageCommitted {
				imageCounter.Commit(info)
				imageCommitted = true
			}
		case "response.failed", "response.incomplete", "response.cancelled", "response.canceled":
			terminal = true
			if !imageCommitted {
				imageCounter.Reset()
				imageCounter.Commit(info)
				imageCommitted = true
			}
		case "response.output_text.delta", "response.function_call_arguments.delta", "response.reasoning_text.delta", "response.reasoning_summary_text.delta":
			// A tool-only partial response still consumed output. This is only a
			// local lower-bound estimate when the provider supplied no usage.
			responseTextBuilder.WriteString(streamResponse.Delta)
		case dto.ResponsesOutputTypeItemDone:
			if streamResponse.Item != nil {
				switch streamResponse.Item.Type {
				case dto.BuildInCallWebSearchCall:
					info.CountBillableToolCall(dto.BuildInCallWebSearchCall, "")
				case dto.BuildInCallFileSearchCall:
					info.CountBillableToolCall(dto.BuildInCallFileSearchCall, "")
				case dto.BuildInCallFunctionCall:
					info.CountBillableToolCall(dto.BuildInCallFunctionCall, streamResponse.Item.Name)
				case dto.ResponsesOutputTypeImageGenerationCall:
					if !imageCommitted {
						imageCounter.Observe(streamResponse.Item, streamResponse.OutputIndex)
					}
				}
			}
		}
		if writeErr != nil {
			downstreamWriteFailed = true
			sr.Stop(writeErr)
		} else if terminal {
			sr.Done()
		}
	})

	if !hasUsage && usage.CompletionTokens == 0 {
		// 计算输出文本的 token 数量
		tempStr := responseTextBuilder.String()
		if len(tempStr) > 0 {
			// 非正常结束，使用输出文本的 token 数量
			completionTokens := service.CountTextToken(tempStr, info.UpstreamModelName)
			usage.CompletionTokens = completionTokens
		}
	}

	if !hasUsage && usage.PromptTokens == 0 && usage.CompletionTokens != 0 {
		// response.created alone proves acceptance, not consumed input. Only
		// estimate prompt usage after observed output; otherwise leave unknown
		// usage at zero rather than inventing consumption from request size.
		usage.PromptTokens = info.GetEstimatePromptTokens()
	}

	if !hasUsage {
		usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	}
	if !terminal {
		// A consumed HTTP stream must settle through ResponsesHelper's normal
		// path. Returning an API error here would retry/refund the whole request.
		const message = "Upstream response ended before a terminal event"
		info.StreamStatus.RecordError(message)
		if !c.Writer.Written() && !downstreamWriteFailed && c.Request.Context().Err() == nil {
			return usage, types.NewOpenAIError(fmt.Errorf("%s", message), types.ErrorCodeBadResponseBody, http.StatusBadGateway)
		}
		if !downstreamWriteFailed && c.Request.Context().Err() == nil && info.StreamStatus.EndReason != relaycommon.StreamEndReasonHandlerStop && info.StreamStatus.EndReason != relaycommon.StreamEndReasonPingFail {
			response := dto.OpenAIResponsesResponse{Object: "response", Output: []dto.ResponsesOutput{}}
			if lastResponse != nil {
				response = *lastResponse
			}
			response.Status = []byte(`"failed"`)
			response.Error = map[string]string{"code": "upstream_stream_interrupted", "message": message}
			event := struct {
				dto.ResponsesStreamResponse
				SequenceNumber int `json:"sequence_number"`
			}{dto.ResponsesStreamResponse{Type: "response.failed", Response: &response}, sequence + 1}
			data, err := common.Marshal(event)
			if err == nil {
				helper.ExtendWriteDeadline(c)
				if err = writeResponsesEvent(c, event.Type, string(data)); err != nil {
					info.StreamStatus.RecordError("failed to write Responses terminal event")
				}
			}
		}
	}

	return usage, nil
}

// Return write failures directly: Gin's Render records them on the context but
// does not return them to the stream handler, which must stop consuming output.
func writeResponsesEvent(c *gin.Context, eventType, data string) error {
	if strings.ContainsAny(eventType, "\r\n") {
		return fmt.Errorf("invalid Responses event name")
	}
	if err := c.Request.Context().Err(); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(c.Writer, "event: %s\ndata: %s\n\n", eventType, data); err != nil {
		return err
	}
	return helper.FlushWriter(c)
}
