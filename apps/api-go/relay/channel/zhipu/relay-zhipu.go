package zhipu

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/constant"
	"github.com/LIghtJUNction/api.lmm.best/pkg/cachex"
	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	"github.com/LIghtJUNction/api.lmm.best/relay/helper"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/types"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/samber/lo"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

// https://open.bigmodel.cn/doc/api#chatglm_std
// chatglm_std, chatglm_lite
// https://open.bigmodel.cn/api/paas/v3/model-api/chatglm_std/invoke
// https://open.bigmodel.cn/api/paas/v3/model-api/chatglm_std/sse-invoke

var zhipuTokens = cachex.NewByteCache[zhipuTokenData](256, 1<<20, func(key string, token zhipuTokenData) int64 {
	return int64(len(key) + len(token.Token) + 32)
})
var expSeconds int64 = 24 * 3600

func zhipuTokenKey(apiKey string) string {
	return common.GenerateHMAC(apiKey)
}

func getZhipuToken(apikey string) string {
	key := zhipuTokenKey(apikey)
	if tokenData, ok := zhipuTokens.Load(key); ok {
		if time.Now().Before(tokenData.ExpiryTime) {
			return tokenData.Token
		}
	}

	split := strings.Split(apikey, ".")
	if len(split) != 2 {
		common.SysLog("invalid zhipu key: " + apikey)
		return ""
	}

	id := split[0]
	secret := split[1]

	expMillis := time.Now().Add(time.Duration(expSeconds)*time.Second).UnixNano() / 1e6
	expiryTime := time.Now().Add(time.Duration(expSeconds) * time.Second)

	timestamp := time.Now().UnixNano() / 1e6

	payload := jwt.MapClaims{
		"api_key":   id,
		"exp":       expMillis,
		"timestamp": timestamp,
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, payload)

	token.Header["alg"] = "HS256"
	token.Header["sign_type"] = "SIGN"

	tokenString, err := token.SignedString([]byte(secret))
	if err != nil {
		return ""
	}

	zhipuTokens.SetWithTTL(key, zhipuTokenData{
		Token:      tokenString,
		ExpiryTime: expiryTime,
	}, time.Until(expiryTime))

	return tokenString
}

func requestOpenAI2Zhipu(request dto.GeneralOpenAIRequest) *ZhipuRequest {
	messages := make([]ZhipuMessage, 0, len(request.Messages))
	for _, message := range request.Messages {
		if message.Role == "system" {
			messages = append(messages, ZhipuMessage{
				Role:    "system",
				Content: message.StringContent(),
			})
			messages = append(messages, ZhipuMessage{
				Role:    "user",
				Content: "Okay",
			})
		} else {
			messages = append(messages, ZhipuMessage{
				Role:    message.Role,
				Content: message.StringContent(),
			})
		}
	}
	return &ZhipuRequest{
		Prompt:      messages,
		Temperature: request.Temperature,
		TopP:        lo.FromPtrOr(request.TopP, 0),
		Incremental: false,
	}
}

func responseZhipu2OpenAI(response *ZhipuResponse) *dto.OpenAITextResponse {
	fullTextResponse := dto.OpenAITextResponse{
		Id:      response.Data.TaskId,
		Object:  "chat.completion",
		Created: common.GetTimestamp(),
		Choices: make([]dto.OpenAITextResponseChoice, 0, len(response.Data.Choices)),
		Usage:   response.Data.Usage,
	}
	for i, choice := range response.Data.Choices {
		openaiChoice := dto.OpenAITextResponseChoice{
			Index: i,
			Message: dto.Message{
				Role:    choice.Role,
				Content: strings.Trim(choice.Content, "\""),
			},
			FinishReason: "",
		}
		if i == len(response.Data.Choices)-1 {
			openaiChoice.FinishReason = "stop"
		}
		fullTextResponse.Choices = append(fullTextResponse.Choices, openaiChoice)
	}
	return &fullTextResponse
}

func streamResponseZhipu2OpenAI(zhipuResponse string) *dto.ChatCompletionsStreamResponse {
	var choice dto.ChatCompletionsStreamResponseChoice
	choice.Delta.SetContentString(zhipuResponse)
	response := dto.ChatCompletionsStreamResponse{
		Object:  "chat.completion.chunk",
		Created: common.GetTimestamp(),
		Model:   "chatglm",
		Choices: []dto.ChatCompletionsStreamResponseChoice{choice},
	}
	return &response
}

func streamMetaResponseZhipu2OpenAI(zhipuResponse *ZhipuStreamMetaResponse) (*dto.ChatCompletionsStreamResponse, *dto.Usage) {
	var choice dto.ChatCompletionsStreamResponseChoice
	choice.Delta.SetContentString("")
	choice.FinishReason = &constant.FinishReasonStop
	response := dto.ChatCompletionsStreamResponse{
		Id:      zhipuResponse.RequestId,
		Object:  "chat.completion.chunk",
		Created: common.GetTimestamp(),
		Model:   "chatglm",
		Choices: []dto.ChatCompletionsStreamResponseChoice{choice},
	}
	return &response, &zhipuResponse.Usage
}

func zhipuStreamHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	defer service.CloseResponseBodyGracefully(resp)
	ctx, cancel := context.WithCancel(c.Request.Context())
	defer cancel()
	info.RateLimitStreamStatus = relaycommon.NewStreamStatus()
	streamStatus := info.RateLimitStreamStatus
	var usage *dto.Usage
	sawMeta := false
	scanner := helper.NewStreamScanner(resp.Body)
	scanner.Split(bufio.ScanLines)
	dataChan := make(chan string)
	metaChan := make(chan string)
	stopChan := make(chan error, 1)
	go func() {
		for scanner.Scan() {
			data := scanner.Text()
			lines := strings.Split(data, "\n")
			for i, line := range lines {
				if len(line) < 5 {
					continue
				}
				if line[:5] == "data:" {
					if !helper.SendCtx(ctx, dataChan, line[5:]) {
						return
					}
					if i != len(lines)-1 {
						if !helper.SendCtx(ctx, dataChan, "\n") {
							return
						}
					}
				} else if line[:5] == "meta:" {
					if !helper.SendCtx(ctx, metaChan, line[5:]) {
						return
					}
				}
			}
		}
		err := scanner.Err()
		if err != nil {
			common.SysLog("error reading stream: " + err.Error())
		}
		stopChan <- err
	}()
	helper.SetEventStreamHeaders(c)
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
		case data := <-dataChan:
			response := streamResponseZhipu2OpenAI(data)
			jsonResponse, err := json.Marshal(response)
			if err != nil {
				common.SysLog("error marshalling stream response: " + err.Error())
				streamStatus.RecordError("stream_encode_error")
				return true
			}
			return writeEvent(string(jsonResponse))
		case data := <-metaChan:
			var zhipuResponse ZhipuStreamMetaResponse
			err := json.Unmarshal([]byte(data), &zhipuResponse)
			if err != nil {
				common.SysLog("error unmarshalling stream response: " + err.Error())
				streamStatus.RecordError("stream_decode_error")
				return true
			}
			response, zhipuUsage := streamMetaResponseZhipu2OpenAI(&zhipuResponse)
			jsonResponse, err := json.Marshal(response)
			if err != nil {
				common.SysLog("error marshalling stream response: " + err.Error())
				streamStatus.RecordError("stream_encode_error")
				return true
			}
			usage = zhipuUsage
			sawMeta = true
			return writeEvent(string(jsonResponse))
		case err := <-stopChan:
			if err != nil {
				streamStatus.RecordError("stream_scanner_error")
				streamStatus.SetEndReason(relaycommon.StreamEndReasonScannerErr, err)
			}
			writeEvent("[DONE]")
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
	} else if sawMeta {
		streamStatus.SetEndReason(relaycommon.StreamEndReasonDone, nil)
	} else {
		streamStatus.SetEndReason(relaycommon.StreamEndReasonEOF, nil)
	}
	return usage, nil
}

func zhipuHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	var zhipuResponse ZhipuResponse
	responseBody, err := common.ReadResponseBody(resp)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeReadResponseBodyFailed, http.StatusInternalServerError)
	}
	service.CloseResponseBodyGracefully(resp)
	err = json.Unmarshal(responseBody, &zhipuResponse)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
	}
	if !zhipuResponse.Success {
		return nil, types.WithOpenAIError(types.OpenAIError{
			Message: zhipuResponse.Msg,
			Code:    zhipuResponse.Code,
		}, resp.StatusCode)
	}
	fullTextResponse := responseZhipu2OpenAI(&zhipuResponse)
	jsonResponse, err := json.Marshal(fullTextResponse)
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeBadResponseBody)
	}
	c.Writer.Header().Set("Content-Type", "application/json")
	c.Writer.WriteHeader(resp.StatusCode)
	_, _ = c.Writer.Write(jsonResponse)
	return &fullTextResponse.Usage, nil
}
