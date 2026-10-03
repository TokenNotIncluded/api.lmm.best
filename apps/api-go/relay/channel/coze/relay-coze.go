package coze

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/LIghtJUNction/api.lmm.best/common"
	appconstant "github.com/LIghtJUNction/api.lmm.best/constant"
	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	"github.com/LIghtJUNction/api.lmm.best/relay/helper"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/types"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/samber/lo"

	"github.com/gin-gonic/gin"
)

func convertCozeChatRequest(c *gin.Context, request dto.GeneralOpenAIRequest) *CozeChatRequest {
	var messages []CozeEnterMessage
	// 将 request的messages的role为user的content转换为CozeMessage
	for _, message := range request.Messages {
		if message.Role == "user" {
			messages = append(messages, CozeEnterMessage{
				Role:    "user",
				Content: message.Content,
				// TODO: support more content type
				ContentType: "text",
			})
		}
	}
	user := request.User
	if len(user) == 0 {
		user = json.RawMessage(helper.GetResponseID(c))
	}
	cozeRequest := &CozeChatRequest{
		BotId:              c.GetString("bot_id"),
		UserId:             user,
		AdditionalMessages: messages,
		Stream:             lo.FromPtrOr(request.Stream, false),
	}
	return cozeRequest
}

func cozeChatHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	responseBody, err := common.ReadResponseBody(resp)
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeBadResponseBody)
	}
	service.CloseResponseBodyGracefully(resp)
	// convert coze response to openai response
	var response dto.TextResponse
	var cozeResponse CozeChatDetailResponse
	response.Model = info.UpstreamModelName
	err = json.Unmarshal(responseBody, &cozeResponse)
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeBadResponseBody)
	}
	if cozeResponse.Code != 0 {
		return nil, types.NewError(errors.New(cozeResponse.Msg), types.ErrorCodeBadResponseBody)
	}
	// 从上下文获取 usage
	var usage dto.Usage
	usage.PromptTokens = c.GetInt("coze_input_count")
	usage.CompletionTokens = c.GetInt("coze_output_count")
	usage.TotalTokens = c.GetInt("coze_token_count")
	response.Usage = usage
	response.Id = helper.GetResponseID(c)

	var responseContent json.RawMessage
	for _, data := range cozeResponse.Data {
		if data.Type == "answer" {
			responseContent = data.Content
			response.Created = data.CreatedAt
		}
	}
	// 添加 response.Choices
	response.Choices = []dto.OpenAITextResponseChoice{
		{
			Index:        0,
			Message:      dto.Message{Role: "assistant", Content: responseContent},
			FinishReason: "stop",
		},
	}
	jsonResponse, err := json.Marshal(response)
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeBadResponseBody)
	}
	c.Writer.Header().Set("Content-Type", "application/json")
	c.Writer.WriteHeader(resp.StatusCode)
	_, _ = service.WriteResponseBytes(c, jsonResponse)

	return &usage, nil
}

type cozeResponseTextBuffer struct {
	builder strings.Builder
	limit   int
}

func newCozeResponseTextBuffer(c *gin.Context) *cozeResponseTextBuffer {
	limit := 0
	if c != nil {
		limit = common.GetContextKeyInt(c, appconstant.ContextKeyResponseByteLimit)
	}
	if limit <= 0 {
		limit = int(common.ResponseBodyLimit())
	}
	return &cozeResponseTextBuffer{limit: limit}
}

func (b *cozeResponseTextBuffer) WriteString(value string) {
	if b == nil || b.limit <= b.builder.Len() {
		return
	}
	remaining := b.limit - b.builder.Len()
	if len(value) > remaining {
		for remaining > 0 && !utf8.RuneStart(value[remaining]) {
			remaining--
		}
		value = value[:remaining]
	}
	b.builder.WriteString(value)
}

func (b *cozeResponseTextBuffer) String() string {
	if b == nil {
		return ""
	}
	return b.builder.String()
}

func cozeChatStreamHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	defer service.CloseResponseBodyGracefully(resp)
	if err := helper.ValidateEventStreamResponse(resp); err != nil {
		return nil, types.NewErrorWithStatusCode(err, types.ErrorCodeBadResponse, http.StatusBadGateway)
	}
	info.RateLimitStreamStatus = relaycommon.NewStreamStatus()
	status := info.RateLimitStreamStatus
	sawCompleted := false
	defer func() {
		if writerErr := c.Errors.Last(); writerErr != nil {
			status.RecordError("write_response")
			status.SetEndReason(relaycommon.StreamEndReasonClientGone, writerErr.Err)
		}
		if err := cozeRequestContext(c).Err(); err != nil {
			helper.MarkHTTPStreamDownstreamFailure(c)
			status.RecordError("request_canceled")
			status.SetEndReason(relaycommon.StreamEndReasonClientGone, err)
		}
		if sawCompleted {
			status.SetEndReason(relaycommon.StreamEndReasonDone, nil)
		} else {
			status.RecordError("upstream stream ended without a completion event")
			status.SetEndReason(relaycommon.StreamEndReasonEOF, nil)
		}
	}()
	scanner := helper.NewStreamScanner(resp.Body)
	scanner.Split(bufio.ScanLines)
	if err := helper.CommitEventStreamResponseHeaders(c, resp); err != nil {
		status.RecordError("downstream header commit failed")
		status.SetEndReason(relaycommon.StreamEndReasonClientGone, err)
		return nil, types.NewError(err, types.ErrorCodeBadResponseBody, types.ErrOptionWithSkipRetry())
	}
	id := helper.GetResponseID(c)
	responseText := newCozeResponseTextBuffer(c)

	var currentEvent string
	var currentData string
	var usage = &dto.Usage{}

	for scanner.Scan() {
		if err := cozeRequestContext(c).Err(); err != nil {
			helper.MarkHTTPStreamDownstreamFailure(c)
			status.RecordError("request_canceled")
			status.SetEndReason(relaycommon.StreamEndReasonClientGone, err)
			return nil, types.NewError(err, types.ErrorCodeBadResponseBody)
		}
		line := scanner.Text()

		if line == "" {
			if currentEvent != "" && currentData != "" {
				// handle last event
				if err := handleCozeEvent(c, currentEvent, currentData, responseText, usage, id, info); err != nil {
					status.RecordError("handle_event")
					if helper.HTTPStreamDownstreamFailed(c) {
						status.SetEndReason(relaycommon.StreamEndReasonClientGone, err)
					} else {
						status.SetEndReason(relaycommon.StreamEndReasonHandlerStop, err)
					}
					return nil, types.NewError(err, types.ErrorCodeBadResponseBody)
				}
				if currentEvent == "conversation.chat.completed" {
					sawCompleted = true
				}
				currentEvent = ""
				currentData = ""
			}
			continue
		}

		if strings.HasPrefix(line, "event:") {
			currentEvent = strings.TrimSpace(line[6:])
			continue
		}

		if strings.HasPrefix(line, "data:") {
			currentData = strings.TrimSpace(line[5:])
			continue
		}
	}

	// Last event
	if currentEvent != "" && currentData != "" {
		if err := handleCozeEvent(c, currentEvent, currentData, responseText, usage, id, info); err != nil {
			status.RecordError("handle_event")
			if helper.HTTPStreamDownstreamFailed(c) {
				status.SetEndReason(relaycommon.StreamEndReasonClientGone, err)
			} else {
				status.SetEndReason(relaycommon.StreamEndReasonHandlerStop, err)
			}
			return nil, types.NewError(err, types.ErrorCodeBadResponseBody)
		}
		if currentEvent == "conversation.chat.completed" {
			sawCompleted = true
		}
	}

	if err := scanner.Err(); err != nil {
		status.RecordError("scan_response")
		status.SetEndReason(relaycommon.StreamEndReasonScannerErr, err)
		return nil, types.NewError(err, types.ErrorCodeBadResponseBody)
	}
	if err := helper.StringData(c, "[DONE]"); err != nil {
		helper.MarkHTTPStreamDownstreamFailure(c)
		status.SetEndReason(relaycommon.StreamEndReasonClientGone, err)
		status.RecordError("write_done")
	}

	if usage.TotalTokens == 0 {
		usage = service.ResponseText2Usage(c, responseText.String(), info.UpstreamModelName, c.GetInt("coze_input_count"))
	}

	return usage, nil
}

func handleCozeEvent(c *gin.Context, event string, data string, responseText *cozeResponseTextBuffer, usage *dto.Usage, id string, info *relaycommon.RelayInfo) error {
	switch event {
	case "conversation.chat.completed":
		// 将 data 解析为 CozeChatResponseData
		var chatData CozeChatResponseData
		err := json.Unmarshal([]byte(data), &chatData)
		if err != nil {
			return fmt.Errorf("unmarshal completed event: %w", err)
		}

		usage.PromptTokens = chatData.Usage.InputCount
		usage.CompletionTokens = chatData.Usage.OutputCount
		usage.TotalTokens = chatData.Usage.TokenCount

		finishReason := "stop"
		stopResponse := helper.GenerateStopResponse(id, common.GetTimestamp(), info.UpstreamModelName, finishReason)
		return helper.ObjectData(c, stopResponse)

	case "conversation.message.delta":
		// 将 data 解析为 CozeChatV3MessageDetail
		var messageData CozeChatV3MessageDetail
		err := json.Unmarshal([]byte(data), &messageData)
		if err != nil {
			return fmt.Errorf("unmarshal message delta: %w", err)
		}

		var content string
		err = json.Unmarshal(messageData.Content, &content)
		if err != nil {
			return fmt.Errorf("unmarshal message content: %w", err)
		}

		responseText.WriteString(content)

		openaiResponse := dto.ChatCompletionsStreamResponse{
			Id:      id,
			Object:  "chat.completion.chunk",
			Created: common.GetTimestamp(),
			Model:   info.UpstreamModelName,
		}

		choice := dto.ChatCompletionsStreamResponseChoice{
			Index: 0,
		}
		choice.Delta.SetContentString(content)
		openaiResponse.Choices = append(openaiResponse.Choices, choice)

		return helper.ObjectData(c, openaiResponse)

	case "error":
		var errorData CozeError
		err := json.Unmarshal([]byte(data), &errorData)
		if err != nil {
			return fmt.Errorf("unmarshal error event: %w", err)
		}

		common.SysLog(fmt.Sprintf("stream event error: %v %v", errorData.Code, errorData.Message))
		info.RateLimitStreamStatus.RecordError("upstream_error")
		info.RateLimitStreamStatus.SetEndReason(relaycommon.StreamEndReasonHandlerStop, errors.New("coze stream error"))
	case "conversation.chat.failed", "conversation.chat.canceled":
		info.RateLimitStreamStatus.RecordError("upstream_chat_failed")
		info.RateLimitStreamStatus.SetEndReason(relaycommon.StreamEndReasonHandlerStop, errors.New("coze chat failed"))
	}
	return nil
}

func checkIfChatComplete(a *Adaptor, c *gin.Context, info *relaycommon.RelayInfo) (error, bool) {
	requestURL := fmt.Sprintf("%s/v3/chat/retrieve", info.ChannelBaseUrl)

	requestURL = requestURL + "?conversation_id=" + c.GetString("coze_conversation_id") + "&chat_id=" + c.GetString("coze_chat_id")
	// 将 conversationId和chatId作为参数发送get请求
	req, err := http.NewRequestWithContext(cozeRequestContext(c), "GET", requestURL, nil)
	if err != nil {
		return err, false
	}
	err = a.SetupRequestHeader(c, &req.Header, info)
	if err != nil {
		return err, false
	}

	resp, err := doRequest(c, req, info) // 调用 doRequest
	if err != nil {
		return err, false
	}
	if resp == nil { // 确保在 doRequest 失败时 resp 不为 nil 导致 panic
		return fmt.Errorf("resp is nil"), false
	}
	defer service.CloseResponseBodyGracefully(resp)

	// 解析 resp 到 CozeChatResponse
	var cozeResponse CozeChatResponse
	responseBody, err := common.ReadResponseBody(resp)
	if err != nil {
		return fmt.Errorf("read response body failed: %w", err), false
	}
	err = json.Unmarshal(responseBody, &cozeResponse)
	if err != nil {
		return fmt.Errorf("unmarshal response body failed: %w", err), false
	}
	if cozeResponse.Data.Status == "completed" {
		// 在上下文设置 usage
		c.Set("coze_token_count", cozeResponse.Data.Usage.TokenCount)
		c.Set("coze_output_count", cozeResponse.Data.Usage.OutputCount)
		c.Set("coze_input_count", cozeResponse.Data.Usage.InputCount)
		return nil, true
	} else if cozeResponse.Data.Status == "failed" || cozeResponse.Data.Status == "canceled" || cozeResponse.Data.Status == "requires_action" {
		return fmt.Errorf("chat status: %s", cozeResponse.Data.Status), false
	} else {
		return nil, false
	}
}

func getChatDetail(a *Adaptor, c *gin.Context, info *relaycommon.RelayInfo) (*http.Response, error) {
	requestURL := fmt.Sprintf("%s/v3/chat/message/list", info.ChannelBaseUrl)

	requestURL = requestURL + "?conversation_id=" + c.GetString("coze_conversation_id") + "&chat_id=" + c.GetString("coze_chat_id")
	req, err := http.NewRequestWithContext(cozeRequestContext(c), "GET", requestURL, nil)
	if err != nil {
		return nil, fmt.Errorf("new request failed: %w", err)
	}
	err = a.SetupRequestHeader(c, &req.Header, info)
	if err != nil {
		return nil, fmt.Errorf("setup request header failed: %w", err)
	}
	resp, err := doRequest(c, req, info)
	if err != nil {
		return nil, fmt.Errorf("do request failed: %w", err)
	}
	return resp, nil
}

func doRequest(c *gin.Context, req *http.Request, info *relaycommon.RelayInfo) (*http.Response, error) {
	client, err := service.GetHttpClientWithProxySettings(info.ChannelSetting.Proxy, info.ChannelSetting)
	if err != nil {
		return nil, fmt.Errorf("new proxy http client failed: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil { // 增加对 client.Do(req) 返回错误的检查
		return nil, fmt.Errorf("client.Do failed: %w", err)
	}
	if limit := common.GetContextKeyInt(c, appconstant.ContextKeyResponseByteLimit); limit > 0 {
		if err := common.LimitResponseBody(resp, int64(limit)); err != nil {
			return nil, err
		}
	}
	// _ = resp.Body.Close()
	return resp, nil
}
