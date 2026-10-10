package channel

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	common2 "github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/constant"
	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	"github.com/LIghtJUNction/api.lmm.best/relay/helper"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/types"

	"github.com/gin-gonic/gin"
)

const (
	compatibilityErrorBodyLimit        = 1 << 20
	compatibilityRetryRequestBodyLimit = 8 << 20
)

// openAIOptionalParameters is deliberately limited to optional top-level
// request fields. We never remove messages, tools, images, or other semantic
// input merely because an upstream rejects a request.
var openAIOptionalParameters = []string{
	"max_completion_tokens",
	"max_tokens",
	"reasoning_effort",
	"verbosity",
	"temperature",
	"top_logprobs",
	"top_p",
	"top_k",
	"stop",
	"n",
	"frequency_penalty",
	"presence_penalty",
	"response_format",
	"seed",
	"parallel_tool_calls",
	"tool_choice",
	"logprobs",
	"modalities",
	"audio",
	"service_tier",
}

func resetUpstreamCompatibilityMarkers(c *gin.Context) {
	if c == nil {
		return
	}
	common2.SetContextKey(c, constant.ContextKeyUpstreamChannelFailure, false)
	common2.SetContextKey(c, constant.ContextKeyUpstreamCapabilityMismatch, false)
	common2.SetContextKey(c, constant.ContextKeyUpstreamUnsupportedParameter, false)
}

func markUpstreamResponse(c *gin.Context, statusCode int, body []byte) {
	if c == nil {
		return
	}
	if statusCode >= http.StatusInternalServerError && statusCode <= http.StatusServiceUnavailable {
		common2.SetContextKey(c, constant.ContextKeyUpstreamChannelFailure, true)
	}
	if statusCode != http.StatusBadRequest {
		return
	}
	if isVisionCapabilityError(body) {
		common2.SetContextKey(c, constant.ContextKeyUpstreamCapabilityMismatch, true)
		common2.SetContextKey(c, constant.ContextKeyUpstreamChannelFailure, true)
	}
	if _, unsupported := unsupportedOpenAIParameter(body); unsupported {
		common2.SetContextKey(c, constant.ContextKeyUpstreamUnsupportedParameter, true)
		common2.SetContextKey(c, constant.ContextKeyUpstreamChannelFailure, true)
	}
}

func isOpenAICompatibilityRequest(req *http.Request, info *relaycommon.RelayInfo) bool {
	return req != nil && req.Method == http.MethodPost && req.GetBody != nil &&
		info != nil && info.RelayFormat == types.RelayFormatOpenAI
}

// readAndRestoreResponseBody makes a body inspectable without changing what
// RelayErrorHandler or a response adaptor receives later.
func readAndRestoreResponseBody(resp *http.Response) ([]byte, bool) {
	if resp == nil || resp.Body == nil {
		return nil, true
	}
	if resp.ContentLength > compatibilityErrorBodyLimit {
		return nil, false
	}
	// ContentLength is commonly unknown for chunked upstream errors. Keep the
	// same ceiling in that case too; a malformed 400 response must not turn the
	// compatibility probe into an unbounded heap allocation.
	body, err := common2.ReadAllLimit(resp.Body, compatibilityErrorBodyLimit)
	_ = resp.Body.Close()
	resp.Body = io.NopCloser(bytes.NewReader(body))
	if err != nil {
		return nil, false
	}
	return body, true
}

func containsAny(text string, terms ...string) bool {
	for _, term := range terms {
		if strings.Contains(text, term) {
			return true
		}
	}
	return false
}

func isVisionCapabilityError(body []byte) bool {
	text := strings.ToLower(string(body))
	if !containsAny(text, "image", "vision", "multimodal", "picture", "image_url") {
		return false
	}
	return containsAny(text,
		"not support",
		"does not support",
		"unsupported",
		"not available",
		"cannot process",
		"can't process",
		"capability",
	)
}

func unsupportedOpenAIParameter(body []byte) (string, bool) {
	text := strings.ToLower(string(body))
	if !containsAny(text,
		"unsupported",
		"not support",
		"does not support",
		"unknown parameter",
		"unrecognized parameter",
		"unexpected parameter",
		"extra inputs",
		"additional properties",
		"invalid parameter",
	) {
		return "", false
	}
	for _, parameter := range openAIOptionalParameters {
		if strings.Contains(text, parameter) {
			return parameter, true
		}
	}
	return "", true
}

func requestBodyWithoutParameter(req *http.Request, parameter string) ([]byte, bool, error) {
	if req == nil || req.GetBody == nil || strings.TrimSpace(parameter) == "" {
		return nil, false, nil
	}
	// Compatibility retries are optional. Do not duplicate a large request
	// body into the raw JSON, decoded map, and re-encoded retry payload: the
	// normal relay path can still return the original upstream response.
	if req.ContentLength > compatibilityRetryRequestBodyLimit {
		return nil, false, nil
	}
	body, err := req.GetBody()
	if err != nil {
		return nil, false, err
	}
	original, readErr := common2.ReadAllLimit(body, compatibilityRetryRequestBodyLimit)
	_ = body.Close()
	if readErr != nil {
		return nil, false, readErr
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(original, &payload); err != nil || payload == nil {
		return nil, false, err
	}
	if _, exists := payload[parameter]; !exists {
		return nil, false, nil
	}
	delete(payload, parameter)
	updated, err := common2.MarshalLimit(payload, compatibilityRetryRequestBodyLimit)
	if err != nil {
		return nil, false, err
	}
	return updated, true, nil
}

func cloneRequestWithBody(req *http.Request, body []byte) (*http.Request, error) {
	if req == nil {
		return nil, fmt.Errorf("cannot clone a nil request")
	}
	retryReq := req.Clone(req.Context())
	retryReq.Body = io.NopCloser(bytes.NewReader(body))
	retryReq.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(body)), nil
	}
	retryReq.ContentLength = int64(len(body))
	retryReq.TransferEncoding = nil
	if retryReq.Header != nil {
		retryReq.Header.Del("Content-Length")
	}
	return retryReq, nil
}

// maybeRetryOpenAICompatibilityError retries a narrowly identified optional
// parameter failure once with that field omitted. It also marks explicit
// vision mismatches so the outer relay loop can select another channel. Image
// input is never stripped: doing so would turn a valid request into a silent,
// semantically different request.
func maybeRetryOpenAICompatibilityError(c *gin.Context, client *http.Client, req *http.Request, info *relaycommon.RelayInfo, resp *http.Response) (*http.Response, error) {
	if resp == nil {
		return nil, nil
	}
	markUpstreamResponse(c, resp.StatusCode, nil)
	if resp.StatusCode != http.StatusBadRequest {
		return resp, nil
	}

	body, readable := readAndRestoreResponseBody(resp)
	if !readable {
		return resp, nil
	}
	if isVisionCapabilityError(body) {
		markUpstreamResponse(c, resp.StatusCode, body)
		return resp, nil
	}
	if !isOpenAICompatibilityRequest(req, info) {
		return resp, nil
	}
	parameter, unsupported := unsupportedOpenAIParameter(body)
	if !unsupported {
		return resp, nil
	}

	if parameter == "" {
		markUpstreamResponse(c, resp.StatusCode, body)
		return resp, nil
	}
	retryBody, changed, err := requestBodyWithoutParameter(req, parameter)
	if err != nil {
		return resp, nil
	}
	if !changed {
		markUpstreamResponse(c, resp.StatusCode, body)
		return resp, nil
	}

	retryReq, err := cloneRequestWithBody(req, retryBody)
	if err != nil {
		return resp, nil
	}
	// Compatibility retries must not remove the reserved output cap or paid tier.
	if err := helper.ApplyServiceTierToRequest(c, retryReq, info); err != nil {
		return resp, nil
	}
	_ = resp.Body.Close()
	retryResp, err := client.Do(retryReq)
	if err != nil {
		common2.SetContextKey(c, constant.ContextKeyUpstreamChannelFailure, true)
		return nil, types.NewError(err, types.ErrorCodeDoRequestFailed, types.ErrOptionWithHideErrMsg("upstream error: compatibility retry failed"))
	}
	if retryResp == nil {
		common2.SetContextKey(c, constant.ContextKeyUpstreamChannelFailure, true)
		return nil, fmt.Errorf("compatibility retry returned an empty response")
	}

	if retryResp.StatusCode == http.StatusBadRequest {
		if retryBody, ok := readAndRestoreResponseBody(retryResp); ok {
			markUpstreamResponse(c, retryResp.StatusCode, retryBody)
		}
	} else {
		markUpstreamResponse(c, retryResp.StatusCode, nil)
	}
	return retryResp, nil
}
