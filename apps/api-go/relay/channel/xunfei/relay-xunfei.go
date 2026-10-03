package xunfei

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/constant"
	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	"github.com/LIghtJUNction/api.lmm.best/relay/helper"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/types"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/samber/lo"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

// https://console.xfyun.cn/services/cbm
// https://www.xfyun.cn/doc/spark/Web.html

func requestOpenAI2Xunfei(request dto.GeneralOpenAIRequest, xunfeiAppId string, domain string) *XunfeiChatRequest {
	messages := make([]XunfeiMessage, 0, len(request.Messages))
	shouldCovertSystemMessage := !strings.HasSuffix(request.Model, "3.5")
	for _, message := range request.Messages {
		if message.Role == "system" && shouldCovertSystemMessage {
			messages = append(messages, XunfeiMessage{
				Role:    "user",
				Content: message.StringContent(),
			})
			messages = append(messages, XunfeiMessage{
				Role:    "assistant",
				Content: "Okay",
			})
		} else {
			messages = append(messages, XunfeiMessage{
				Role:    message.Role,
				Content: message.StringContent(),
			})
		}
	}
	xunfeiRequest := XunfeiChatRequest{}
	xunfeiRequest.Header.AppId = xunfeiAppId
	xunfeiRequest.Parameter.Chat.Domain = domain
	xunfeiRequest.Parameter.Chat.Temperature = request.Temperature
	xunfeiRequest.Parameter.Chat.TopK = lo.FromPtrOr(request.N, 0)
	xunfeiRequest.Parameter.Chat.MaxTokens = request.GetMaxTokens()
	xunfeiRequest.Payload.Message.Text = messages
	return &xunfeiRequest
}

func responseXunfei2OpenAI(response *XunfeiChatResponse) *dto.OpenAITextResponse {
	if len(response.Payload.Choices.Text) == 0 {
		response.Payload.Choices.Text = []XunfeiChatResponseTextItem{
			{
				Content: "",
			},
		}
	}
	choice := dto.OpenAITextResponseChoice{
		Index: 0,
		Message: dto.Message{
			Role:    "assistant",
			Content: response.Payload.Choices.Text[0].Content,
		},
		FinishReason: constant.FinishReasonStop,
	}
	fullTextResponse := dto.OpenAITextResponse{
		Object:  "chat.completion",
		Created: common.GetTimestamp(),
		Choices: []dto.OpenAITextResponseChoice{choice},
		Usage:   response.Payload.Usage.Text,
	}
	return &fullTextResponse
}

func streamResponseXunfei2OpenAI(xunfeiResponse *XunfeiChatResponse) *dto.ChatCompletionsStreamResponse {
	if len(xunfeiResponse.Payload.Choices.Text) == 0 {
		xunfeiResponse.Payload.Choices.Text = []XunfeiChatResponseTextItem{
			{
				Content: "",
			},
		}
	}
	var choice dto.ChatCompletionsStreamResponseChoice
	choice.Delta.SetContentString(xunfeiResponse.Payload.Choices.Text[0].Content)
	if xunfeiResponse.Payload.Choices.Status == 2 {
		choice.FinishReason = &constant.FinishReasonStop
	}
	response := dto.ChatCompletionsStreamResponse{
		Object:  "chat.completion.chunk",
		Created: common.GetTimestamp(),
		Model:   "SparkDesk",
		Choices: []dto.ChatCompletionsStreamResponseChoice{choice},
	}
	return &response
}

func buildXunfeiAuthUrl(hostUrl string, apiKey, apiSecret string) string {
	HmacWithShaToBase64 := func(algorithm, data, key string) string {
		mac := hmac.New(sha256.New, []byte(key))
		mac.Write([]byte(data))
		encodeData := mac.Sum(nil)
		return base64.StdEncoding.EncodeToString(encodeData)
	}
	ul, err := url.Parse(hostUrl)
	if err != nil {
		fmt.Println(err)
	}
	date := time.Now().UTC().Format(time.RFC1123)
	signString := []string{"host: " + ul.Host, "date: " + date, "GET " + ul.Path + " HTTP/1.1"}
	sign := strings.Join(signString, "\n")
	sha := HmacWithShaToBase64("hmac-sha256", sign, apiSecret)
	authUrl := fmt.Sprintf("hmac username=\"%s\", algorithm=\"%s\", headers=\"%s\", signature=\"%s\"", apiKey,
		"hmac-sha256", "host date request-line", sha)
	authorization := base64.StdEncoding.EncodeToString([]byte(authUrl))
	v := url.Values{}
	v.Add("host", ul.Host)
	v.Add("date", date)
	v.Add("authorization", authorization)
	callUrl := hostUrl + "?" + v.Encode()
	return callUrl
}

func xunfeiStreamHandler(c *gin.Context, info *relaycommon.RelayInfo, textRequest dto.GeneralOpenAIRequest, appId string, apiSecret string, apiKey string) (*dto.Usage, *types.NewAPIError) {
	ctx, cancel := context.WithCancel(c.Request.Context())
	defer cancel()
	info.RateLimitStreamStatus = relaycommon.NewStreamStatus()
	streamStatus := info.RateLimitStreamStatus
	domain, authUrl := getXunfeiAuthUrl(c, apiKey, apiSecret, textRequest.Model)
	dataChan, doneChan, err := xunfeiMakeRequestWithContext(c, ctx, textRequest, domain, authUrl, appId)
	if err != nil {
		streamStatus.RecordError("stream_request_error")
		if ctx.Err() != nil {
			streamStatus.SetEndReason(relaycommon.StreamEndReasonClientGone, ctx.Err())
		} else {
			streamStatus.SetEndReason(relaycommon.StreamEndReasonScannerErr, err)
		}
		return nil, types.NewError(err, types.ErrorCodeDoRequestFailed)
	}
	helper.SetEventStreamHeaders(c)
	var usage dto.Usage
	var responseErr error
	writeFailed := false
	writeEvent := func(data string) bool {
		// Keep reading after a write failure so upstream usage is still collected.
		if writeFailed {
			return true
		}
		c.Render(-1, common.CustomEvent{Data: "data: " + data})
		if err := c.Errors.Last(); err != nil {
			writeFailed = true
			streamStatus.RecordError("downstream_write_error")
			streamStatus.SetEndReason(relaycommon.StreamEndReasonClientGone, err)
		}
		return true
	}
	disconnected := c.Stream(func(_ io.Writer) bool {
		select {
		case <-ctx.Done():
			streamStatus.RecordError("client_cancelled")
			streamStatus.SetEndReason(relaycommon.StreamEndReasonClientGone, ctx.Err())
			return false
		case xunfeiResponse := <-dataChan:
			if xunfeiResponse.Header.Code != 0 {
				streamStatus.RecordError("upstream_error")
			}
			usage.PromptTokens += xunfeiResponse.Payload.Usage.Text.PromptTokens
			usage.CompletionTokens += xunfeiResponse.Payload.Usage.Text.CompletionTokens
			usage.TotalTokens += xunfeiResponse.Payload.Usage.Text.TotalTokens
			response := streamResponseXunfei2OpenAI(&xunfeiResponse)
			jsonResponse, err := json.Marshal(response)
			if err != nil {
				common.SysLog("error marshalling stream response: " + err.Error())
				streamStatus.RecordError("stream_encode_error")
				return true
			}
			return writeEvent(string(jsonResponse))
		case responseErr = <-doneChan:
			if responseErr == nil {
				writeEvent("[DONE]")
			} else {
				streamStatus.RecordError("stream_response_error")
				if ctx.Err() != nil {
					streamStatus.SetEndReason(relaycommon.StreamEndReasonClientGone, ctx.Err())
				} else {
					streamStatus.SetEndReason(relaycommon.StreamEndReasonScannerErr, responseErr)
				}
			}
			return false
		}
	})
	if disconnected || ctx.Err() != nil {
		if streamStatus.EndReason != relaycommon.StreamEndReasonClientGone {
			streamStatus.RecordError("client_cancelled")
		}
		err := ctx.Err()
		if err == nil {
			err = context.Canceled
		}
		streamStatus.SetEndReason(relaycommon.StreamEndReasonClientGone, err)
	} else {
		streamStatus.SetEndReason(relaycommon.StreamEndReasonDone, nil)
	}
	if responseErr != nil {
		return nil, types.NewError(responseErr, types.ErrorCodeBadResponseBody)
	}
	return &usage, nil
}

func xunfeiHandler(c *gin.Context, textRequest dto.GeneralOpenAIRequest, appId string, apiSecret string, apiKey string) (*dto.Usage, *types.NewAPIError) {
	domain, authUrl := getXunfeiAuthUrl(c, apiKey, apiSecret, textRequest.Model)
	dataChan, doneChan, err := xunfeiMakeRequest(c, textRequest, domain, authUrl, appId)
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeDoRequestFailed)
	}
	var usage dto.Usage
	var content strings.Builder
	var xunfeiResponse XunfeiChatResponse
	stop := false
	for !stop {
		select {
		case xunfeiResponse = <-dataChan:
			if len(xunfeiResponse.Payload.Choices.Text) == 0 {
				continue
			}
			content.WriteString(xunfeiResponse.Payload.Choices.Text[0].Content)
			usage.PromptTokens += xunfeiResponse.Payload.Usage.Text.PromptTokens
			usage.CompletionTokens += xunfeiResponse.Payload.Usage.Text.CompletionTokens
			usage.TotalTokens += xunfeiResponse.Payload.Usage.Text.TotalTokens
		case responseErr := <-doneChan:
			if responseErr != nil {
				return nil, types.NewError(responseErr, types.ErrorCodeBadResponseBody)
			}
			stop = true
		}
	}
	if len(xunfeiResponse.Payload.Choices.Text) == 0 {
		xunfeiResponse.Payload.Choices.Text = []XunfeiChatResponseTextItem{
			{
				Content: "",
			},
		}
	}
	xunfeiResponse.Payload.Choices.Text[0].Content = content.String()

	response := responseXunfei2OpenAI(&xunfeiResponse)
	jsonResponse, err := json.Marshal(response)
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeBadResponseBody)
	}
	c.Writer.Header().Set("Content-Type", "application/json")
	_, _ = service.WriteResponseBytes(c, jsonResponse)
	return &usage, nil
}

func xunfeiMakeRequest(c *gin.Context, textRequest dto.GeneralOpenAIRequest, domain, authUrl, appId string) (chan XunfeiChatResponse, chan error, error) {
	requestContext := context.Background()
	if c != nil && c.Request != nil {
		requestContext = c.Request.Context()
	}
	return xunfeiMakeRequestWithContext(c, requestContext, textRequest, domain, authUrl, appId)
}

func xunfeiMakeRequestWithContext(c *gin.Context, requestContext context.Context, textRequest dto.GeneralOpenAIRequest, domain, authUrl, appId string) (chan XunfeiChatResponse, chan error, error) {
	d := websocket.Dialer{
		HandshakeTimeout: 5 * time.Second,
	}
	conn, resp, err := d.DialContext(requestContext, authUrl, nil)
	if err != nil {
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		return nil, nil, err
	}
	if resp == nil || resp.StatusCode != 101 {
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		return nil, nil, fmt.Errorf("unexpected Xunfei websocket status")
	}
	stopOnCancel := context.AfterFunc(requestContext, func() {
		_ = conn.Close()
	})
	responseByteLimit := int64(common.GetContextKeyInt(c, constant.ContextKeyResponseByteLimit))
	if responseByteLimit <= 0 {
		responseByteLimit = common.ResponseBodyLimit()
	}
	conn.SetReadLimit(responseByteLimit)

	data := requestOpenAI2Xunfei(textRequest, appId, domain)
	err = conn.WriteJSON(data)
	if err != nil {
		stopOnCancel()
		_ = conn.Close()
		return nil, nil, err
	}

	dataChan := make(chan XunfeiChatResponse)
	doneChan := make(chan error, 1)
	go func() {
		var responseErr error
		defer func() {
			stopOnCancel()
			_ = conn.Close()
			doneChan <- responseErr
		}()
		remaining := responseByteLimit
		for {
			if remaining <= 0 {
				responseErr = common.ErrLimitExceeded
				break
			}
			conn.SetReadLimit(remaining)
			_, msg, err := conn.ReadMessage()
			if err != nil {
				if requestContext.Err() != nil {
					responseErr = requestContext.Err()
				} else if errors.Is(err, websocket.ErrReadLimit) {
					responseErr = common.ErrLimitExceeded
				} else {
					responseErr = fmt.Errorf("read websocket response: %w", err)
				}
				common.SysLog("error reading stream response: " + responseErr.Error())
				break
			}
			remaining -= int64(len(msg))
			var response XunfeiChatResponse
			err = json.Unmarshal(msg, &response)
			if err != nil {
				responseErr = fmt.Errorf("unmarshal websocket response: %w", err)
				common.SysLog("error unmarshalling stream response: " + responseErr.Error())
				break
			}
			if !helper.SendCtx(requestContext, dataChan, response) {
				responseErr = requestContext.Err()
				if responseErr == nil {
					responseErr = context.Canceled
				}
				return
			}
			if response.Payload.Choices.Status == 2 {
				if err != nil {
					common.SysLog("error closing websocket connection: " + err.Error())
				}
				break
			}
		}
	}()

	return dataChan, doneChan, nil
}

func apiVersion2domain(apiVersion string) string {
	switch apiVersion {
	case "v1.1":
		return "lite"
	case "v2.1":
		return "generalv2"
	case "v3.1":
		return "generalv3"
	case "v3.5":
		return "generalv3.5"
	case "v4.0":
		return "4.0Ultra"
	}
	return "general" + apiVersion
}

func getXunfeiAuthUrl(c *gin.Context, apiKey string, apiSecret string, modelName string) (string, string) {
	apiVersion := getAPIVersion(c, modelName)
	domain := apiVersion2domain(apiVersion)
	authUrl := buildXunfeiAuthUrl(fmt.Sprintf("wss://spark-api.xf-yun.com/%s/chat", apiVersion), apiKey, apiSecret)
	return domain, authUrl
}

func getAPIVersion(c *gin.Context, modelName string) string {
	query := c.Request.URL.Query()
	apiVersion := query.Get("api-version")
	if apiVersion != "" {
		return apiVersion
	}
	parts := strings.Split(modelName, "-")
	if len(parts) == 2 {
		apiVersion = parts[1]
		return apiVersion

	}
	apiVersion = c.GetString("api_version")
	if apiVersion != "" {
		return apiVersion
	}
	apiVersion = "v1.1"
	common.SysLog("api_version not found, using default: " + apiVersion)
	return apiVersion
}
