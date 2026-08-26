package relay

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/LIghtJUNction/api.lmm.best/common"
	appconstant "github.com/LIghtJUNction/api.lmm.best/constant"
	"github.com/LIghtJUNction/api.lmm.best/logger"
	"github.com/LIghtJUNction/api.lmm.best/middleware"
	appmodel "github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/pkg/wsmanager"
	relaychannel "github.com/LIghtJUNction/api.lmm.best/relay/channel"
	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	"github.com/LIghtJUNction/api.lmm.best/relay/helper"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/types"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/LIghtJUNction/api.lmm.best/setting"
	"github.com/LIghtJUNction/api.lmm.best/setting/ratio_setting"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

const (
	responsesWSEventTypeResponseCreate = "response.create"
	responsesWSEventTypeResponseCancel = "response.cancel"
)

type responsesWSCreateEvent struct {
	Type    string            `json:"type"`
	EventID string            `json:"event_id,omitempty"`
	Request common.RawMessage `json:"response,omitempty"`
}

type responsesWSCreateRequest struct {
	Request  dto.OpenAIResponsesRequest
	Generate common.RawMessage
}

type responsesWSErrorEvent struct {
	Type    string             `json:"type"`
	Status  int                `json:"status"`
	EventID string             `json:"event_id,omitempty"`
	Error   *types.OpenAIError `json:"error"`
}

type responsesWSCallState struct {
	info       *relaycommon.RelayInfo
	usage      *dto.Usage
	outputText responsesWSOutputTextBuffer
	images     relaycommon.ImageGenerationCallCounter
	commitRate middleware.ModelRequestRateLimitCommit
	dataMu     sync.Mutex
	finishing  bool
}

type responsesWSOutputTextBuffer struct {
	builder strings.Builder
	limit   int
}

func newResponsesWSOutputTextBuffer(c *gin.Context) responsesWSOutputTextBuffer {
	limit := 0
	if c != nil {
		limit = common.GetContextKeyInt(c, appconstant.ContextKeyResponseByteLimit)
	}
	if limit <= 0 {
		limit = int(common.ResponseBodyLimit())
	}
	return responsesWSOutputTextBuffer{limit: limit}
}

func (b *responsesWSOutputTextBuffer) WriteString(c *gin.Context, value string) {
	if b.limit <= 0 {
		*b = newResponsesWSOutputTextBuffer(c)
	}
	if b.limit <= b.builder.Len() {
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

func (b *responsesWSOutputTextBuffer) Len() int {
	if b == nil {
		return 0
	}
	return b.builder.Len()
}

func (b *responsesWSOutputTextBuffer) String() string {
	if b == nil {
		return ""
	}
	return b.builder.String()
}

type responsesWSSession struct {
	c              *gin.Context
	client         *websocket.Conn
	target         *websocket.Conn
	unregister     func()
	lockedModel    string
	lockedChannel  *appmodel.Channel
	nextEventIndex int
	closeOnce      sync.Once

	clientWriteMu  sync.Mutex
	targetWriteMu  sync.Mutex
	stateMu        sync.Mutex
	current        *responsesWSCallState
	revalidateAuth func(*gin.Context) *types.NewAPIError
	reuseTarget    func(responsesWSCreateRequest, middleware.ModelRequestRateLimitCommit) *types.NewAPIError
}

var loadResponsesWSLockedChannel = appmodel.CacheGetChannel
var isResponsesWSChannelAvailable = appmodel.IsChannelEnabledForGroupModel
var postResponsesWSConsumeQuota = service.PostTextConsumeQuota

func ResponsesWebSocketHelper(c *gin.Context, client *websocket.Conn) *types.NewAPIError {
	common.SetWebSocketReadLimit(client)
	session := &responsesWSSession{c: c, client: client}
	defer session.closeTarget()
	defer session.failCurrent()

	for {
		messageType, message, err := client.ReadMessage()
		if err != nil {
			if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				return nil
			}
			return types.NewError(err, types.ErrorCodeBadRequestBody, types.ErrOptionWithSkipRetry())
		}

		eventType, eventID, eventErr := responsesWSEventMetadata(message)
		if eventErr != nil {
			session.sendError(eventID, newResponsesWSInvalidRequestError(eventErr))
			continue
		}

		switch eventType {
		case responsesWSEventTypeResponseCreate:
			create, normalizedEventID, err := normalizeResponsesWSCreateEvent(message)
			if err != nil {
				session.sendError(eventID, newResponsesWSInvalidRequestError(err))
				continue
			}
			if create.Request.Model == "" {
				session.sendError(normalizedEventID, newResponsesWSInvalidRequestError(errors.New("model is required")))
				continue
			}
			if apiErr := session.handleResponseCreate(create); apiErr != nil {
				session.sendError(normalizedEventID, apiErr)
			}
		case responsesWSEventTypeResponseCancel:
			if apiErr := session.handleResponseCancel(messageType, message); apiErr != nil {
				session.sendError(eventID, apiErr)
			}
		default:
			if !session.hasTarget() {
				session.sendError(eventID, newResponsesWSInvalidRequestError(errors.New("first responses websocket event must be response.create")))
				continue
			}
			if err := session.writeTarget(messageType, message); err != nil {
				return session.handleControlEventWriteFailure(err)
			}
		}
	}
}

func (s *responsesWSSession) handleResponseCancel(messageType int, message []byte) *types.NewAPIError {
	if !s.hasCurrent() || !s.hasTarget() {
		return newResponsesWSInvalidRequestError(errors.New("no response is active to cancel"))
	}
	if err := s.writeTarget(messageType, message); err != nil {
		return s.handleTargetWriteFailure(err)
	}
	return nil
}

func responsesWSEventMetadata(message []byte) (string, string, error) {
	var event struct {
		Type    string `json:"type"`
		EventID string `json:"event_id,omitempty"`
	}
	if err := common.Unmarshal(message, &event); err != nil {
		return "", "", fmt.Errorf("invalid websocket event json: %w", err)
	}
	if strings.TrimSpace(event.Type) == "" {
		return "", event.EventID, errors.New("websocket event type is required")
	}
	return event.Type, event.EventID, nil
}

func newResponsesWSInvalidRequestError(err error) *types.NewAPIError {
	return types.NewErrorWithStatusCode(err, types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
}

func normalizeResponsesWSCreateEvent(message []byte) (responsesWSCreateRequest, string, error) {
	var event responsesWSCreateEvent
	if err := common.Unmarshal(message, &event); err != nil {
		return responsesWSCreateRequest{}, "", err
	}
	if event.Type != responsesWSEventTypeResponseCreate {
		return responsesWSCreateRequest{}, event.EventID, fmt.Errorf("unsupported event type %q", event.Type)
	}

	var raw map[string]common.RawMessage
	if err := common.Unmarshal(message, &raw); err != nil {
		return responsesWSCreateRequest{}, event.EventID, err
	}
	generate := raw["generate"]
	payload := event.Request
	if len(payload) == 0 {
		delete(raw, "type")
		delete(raw, "event_id")
		delete(raw, "background")
		delete(raw, "generate")
		delete(raw, "stream")
		delete(raw, "stream_options")
		var err error
		payload, err = common.Marshal(raw)
		if err != nil {
			return responsesWSCreateRequest{}, event.EventID, err
		}
	} else {
		var responseMap map[string]common.RawMessage
		if err := common.Unmarshal(payload, &responseMap); err == nil {
			if len(generate) == 0 {
				generate = responseMap["generate"]
			}
			delete(responseMap, "generate")
			if merged, err := common.Marshal(responseMap); err == nil {
				payload = merged
			}
		}
	}

	var req dto.OpenAIResponsesRequest
	if err := common.Unmarshal(payload, &req); err != nil {
		return responsesWSCreateRequest{}, event.EventID, err
	}
	req.Stream = nil
	req.StreamOptions = nil
	return responsesWSCreateRequest{Request: req, Generate: generate}, event.EventID, nil
}

func (s *responsesWSSession) handleResponseCreate(create responsesWSCreateRequest) *types.NewAPIError {
	modelName := create.Request.Model
	revalidateAuth := s.revalidateAuth
	if revalidateAuth == nil {
		revalidateAuth = middleware.RevalidateTokenAuth
	}
	if apiErr := revalidateAuth(s.c); apiErr != nil {
		return apiErr
	}
	lockedChannel, apiErr := validateResponsesWSTurnAuthorization(s.c, modelName, s.lockedChannel)
	if apiErr != nil {
		return apiErr
	}
	if lockedChannel != nil {
		s.lockedChannel = lockedChannel
	}
	if s.lockedModel != "" && modelName != s.lockedModel {
		return types.NewErrorWithStatusCode(fmt.Errorf("responses websocket connection is locked to model %q; got %q", s.lockedModel, modelName), types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	}
	if s.hasCurrent() {
		return types.NewErrorWithStatusCode(errors.New("another response.create is already in progress on this websocket connection"), types.ErrorCodeInvalidRequest, http.StatusConflict, types.ErrOptionWithSkipRetry())
	}

	commitRate, apiErr := middleware.CheckModelRequestRateLimit(s.c)
	if apiErr != nil {
		return apiErr
	}
	if !s.hasTarget() {
		return s.connectAndSendFirst(create, commitRate)
	}
	if s.reuseTarget != nil {
		return s.reuseTarget(create, commitRate)
	}
	return s.sendOnExistingTarget(create, commitRate)
}

func (s *responsesWSSession) sendOnExistingTarget(create responsesWSCreateRequest, commitRate middleware.ModelRequestRateLimitCommit) *types.NewAPIError {
	state, payload, apiErr := s.prepareCall(create, commitRate)
	if apiErr != nil {
		commitRate(false)
		return apiErr
	}
	if !s.tryReserveCurrent(state) {
		state.refund(s.c)
		commitRate(false)
		return types.NewErrorWithStatusCode(errors.New("another response.create is already in progress on this websocket connection"), types.ErrorCodeInvalidRequest, http.StatusConflict, types.ErrOptionWithSkipRetry())
	}
	if err := s.writeTarget(websocket.TextMessage, payload); err != nil {
		return s.handleTargetWriteFailureWithState(state, err)
	}
	return nil
}

func (s *responsesWSSession) handleControlEventWriteFailure(err error) *types.NewAPIError {
	apiErr := s.handleTargetWriteFailure(err)
	s.sendError("", apiErr)
	return nil
}

func (s *responsesWSSession) handleTargetWriteFailure(err error) *types.NewAPIError {
	s.failCurrent()
	s.closeTarget()
	apiErr := types.NewError(err, types.ErrorCodeBadResponse)
	apiErr, _ = s.processChannelError(s.lockedChannel, apiErr, nil)
	return apiErr
}

func (s *responsesWSSession) handleTargetWriteFailureWithState(state *responsesWSCallState, err error) *types.NewAPIError {
	s.finishCall(state, false)
	return s.handleTargetWriteFailure(err)
}

func (s *responsesWSSession) connectAndSendFirst(create responsesWSCreateRequest, commitRate middleware.ModelRequestRateLimitCommit) *types.NewAPIError {
	modelName := create.Request.Model
	retryParam := &service.RetryParam{
		Ctx:         s.c,
		TokenGroup:  common.GetContextKeyString(s.c, appconstant.ContextKeyUsingGroup),
		ModelName:   modelName,
		RequestPath: s.c.Request.URL.Path,
		Retry:       common.GetPointer(0),
	}
	if retryParam.TokenGroup == "" {
		retryParam.TokenGroup = common.GetContextKeyString(s.c, appconstant.ContextKeyTokenGroup)
	}

	var lastErr *types.NewAPIError
	for ; retryParam.GetRetry() <= common.RetryTimes; retryParam.IncreaseRetry() {
		channel, apiErr := selectResponsesWSChannelForSession(s.c, modelName, retryParam, s.lockedChannel)
		if apiErr != nil {
			lastErr = apiErr
			break
		}
		addResponsesWSUsedChannel(s.c, channel.Id)
		if channel.Type != appconstant.ChannelTypeOpenAI && channel.Type != appconstant.ChannelTypeOpenHuman && channel.Type != appconstant.ChannelTypeCodex {
			lastErr = types.NewErrorWithStatusCode(fmt.Errorf("responses websocket only supports OpenAI, OpenHuman, and Codex channels, got channel type %d", channel.Type), types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
			continue
		}

		state, payload, apiErr := s.prepareCall(create, commitRate)
		if apiErr != nil {
			commitRate(false)
			return apiErr
		}
		adaptor := GetAdaptor(state.info.ApiType)
		if adaptor == nil {
			state.refund(s.c)
			apiErr = types.NewError(fmt.Errorf("invalid api type: %d", state.info.ApiType), types.ErrorCodeInvalidApiType, types.ErrOptionWithSkipRetry())
			var shouldRetry bool
			lastErr, shouldRetry = s.processChannelError(channel, apiErr, retryParam)
			if !shouldRetry {
				break
			}
			continue
		}
		adaptor.Init(state.info)
		target, apiErr := dialResponsesWebSocketUpstream(s.c, adaptor, state.info)
		if apiErr != nil {
			state.refund(s.c)
			var shouldRetry bool
			lastErr, shouldRetry = s.processChannelError(channel, apiErr, retryParam)
			if !shouldRetry {
				break
			}
			continue
		}

		s.setTarget(target)
		if !s.tryReserveCurrent(state) {
			s.closeTarget()
			state.refund(s.c)
			commitRate(false)
			return types.NewErrorWithStatusCode(errors.New("another response.create is already in progress on this websocket connection"), types.ErrorCodeInvalidRequest, http.StatusConflict, types.ErrOptionWithSkipRetry())
		}
		if err := s.writeTarget(websocket.TextMessage, payload); err != nil {
			s.finishCall(state, false)
			s.closeTarget()
			apiErr = types.NewError(err, types.ErrorCodeBadResponse)
			var shouldRetry bool
			lastErr, shouldRetry = s.processChannelError(channel, apiErr, retryParam)
			if !shouldRetry {
				break
			}
			continue
		}

		s.lockedModel = modelName
		s.lockedChannel = channel
		s.registerChannelClose(channel.Id)
		service.RecordChannelAffinity(s.c, channel.Id)
		s.startTargetReader()
		return nil
	}
	if lastErr == nil {
		lastErr = types.NewError(errors.New("failed to connect responses websocket upstream"), types.ErrorCodeDoRequestFailed, types.ErrOptionWithSkipRetry())
	}
	commitRate(false)
	return lastErr
}

func (s *responsesWSSession) processChannelError(channel *appmodel.Channel, apiErr *types.NewAPIError, retryParam *service.RetryParam) (*types.NewAPIError, bool) {
	if apiErr == nil {
		return nil, false
	}
	apiErr = service.NormalizeViolationFeeError(apiErr)
	statusCodeMapping := ""
	if s.c != nil {
		statusCodeMapping = s.c.GetString("status_code_mapping")
	}
	service.ResetStatusCode(apiErr, statusCodeMapping)
	if channel != nil && s.c != nil {
		service.ProcessChannelError(s.c, *types.NewChannelError(
			channel.Id,
			channel.Type,
			channel.Name,
			channel.ChannelInfo.IsMultiKey,
			common.GetContextKeyString(s.c, appconstant.ContextKeyChannelKey),
			channel.GetAutoBan(),
		), apiErr)
	}
	if retryParam == nil {
		return apiErr, false
	}
	return apiErr, service.ShouldRetryRelayError(s.c, apiErr, common.RetryTimes-retryParam.GetRetry())
}

func (s *responsesWSSession) prepareCall(create responsesWSCreateRequest, commitRate middleware.ModelRequestRateLimitCommit) (*responsesWSCallState, []byte, *types.NewAPIError) {
	req := create.Request
	common.SetContextKey(s.c, appconstant.ContextKeyRequestStartTime, time.Now())
	relayInfo := relaycommon.GenRelayInfoResponses(s.c, &req)
	relayInfo.RequestId = fmt.Sprintf("%s-ws-%d", relayInfo.RequestId, s.nextEventIndex)
	s.nextEventIndex++

	meta := req.GetTokenCountMeta()
	if setting.ShouldCheckPromptSensitive() && meta != nil {
		contains, words := service.CheckSensitiveText(meta.CombineText)
		if contains {
			return nil, nil, types.NewError(fmt.Errorf("user sensitive words detected: %s", strings.Join(words, ", ")), types.ErrorCodeSensitiveWordsDetected, types.ErrOptionWithSkipRetry())
		}
	}
	if setting.ShouldCheckAdvancedSecurityPrompt() {
		securityText := dto.SecurityTextForRequest(&req)
		evaluation := service.EvaluateAdvancedSecurityText(s.c, relayInfo, securityText)
		if len(evaluation.Matches) > 0 {
			matchIDs := make([]string, 0, len(evaluation.Matches))
			for _, match := range evaluation.Matches {
				matchIDs = append(matchIDs, match.RuleID)
			}
			logger.LogWarn(s.c, fmt.Sprintf("advanced security rules matched: %s", strings.Join(matchIDs, ", ")))
			if evaluation.Blocked() {
				apiErr := service.NewAdvancedSecurityAPIError()
				apiErr.SetMessage(common.MessageWithRequestId(apiErr.Error(), relayInfo.RequestId))
				return nil, nil, apiErr
			}
		}
	}
	tokens, err := service.EstimateRequestToken(s.c, meta, relayInfo)
	if err != nil {
		return nil, nil, types.NewError(err, types.ErrorCodeCountTokenFailed)
	}
	relayInfo.SetEstimatePromptTokens(tokens)
	priceData, err := helper.ModelPriceHelper(s.c, relayInfo, tokens, meta)
	if err != nil {
		return nil, nil, types.NewError(err, types.ErrorCodeModelPriceError, types.ErrOptionWithStatusCode(http.StatusBadRequest))
	}
	if !priceData.FreeModel {
		if apiErr := service.PreConsumeBilling(s.c, priceData.QuotaToPreConsume, relayInfo); apiErr != nil {
			return nil, nil, apiErr
		}
	}

	payload, apiErr := buildResponsesWSCreatePayload(s.c, relayInfo, req, create.Generate)
	if apiErr != nil {
		if relayInfo.Billing != nil {
			relayInfo.Billing.Refund(s.c)
		}
		return nil, nil, apiErr
	}
	return &responsesWSCallState{
		info:       relayInfo,
		usage:      &dto.Usage{},
		outputText: newResponsesWSOutputTextBuffer(s.c),
		commitRate: commitRate,
	}, payload, nil
}

func buildResponsesWSCreatePayload(c *gin.Context, relayInfo *relaycommon.RelayInfo, req dto.OpenAIResponsesRequest, generate common.RawMessage) ([]byte, *types.NewAPIError) {
	relayInfo.InitChannelMeta(c)
	request, err := common.DeepCopy(&req)
	if err != nil {
		return nil, types.NewError(fmt.Errorf("failed to copy responses request: %w", err), types.ErrorCodeInvalidRequest, types.ErrOptionWithSkipRetry())
	}
	if err := helper.ModelMappedHelper(c, relayInfo, request); err != nil {
		return nil, types.NewError(err, types.ErrorCodeChannelModelMappedError, types.ErrOptionWithSkipRetry())
	}

	adaptor := GetAdaptor(relayInfo.ApiType)
	if adaptor == nil {
		return nil, types.NewError(fmt.Errorf("invalid api type: %d", relayInfo.ApiType), types.ErrorCodeInvalidApiType, types.ErrOptionWithSkipRetry())
	}
	adaptor.Init(relayInfo)
	convertedRequest, err := adaptor.ConvertOpenAIResponsesRequest(c, relayInfo, *request)
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
	}
	relaycommon.AppendRequestConversionFromRequest(relayInfo, convertedRequest)
	jsonData, err := common.Marshal(convertedRequest)
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
	}
	jsonData, err = relaycommon.RemoveDisabledFields(jsonData, relayInfo.ChannelOtherSettings, relayInfo.ChannelSetting.PassThroughBodyEnabled)
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
	}
	jsonData, err = removeResponsesWSTransportFields(jsonData)
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
	}
	if len(relayInfo.ParamOverride) > 0 {
		jsonData, err = relaycommon.ApplyParamOverrideWithRelayInfo(jsonData, relayInfo)
		if err != nil {
			return nil, newAPIErrorFromParamOverride(err)
		}
	}
	event, err := buildResponsesWSCreateEvent(jsonData, generate)
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
	}
	return event, nil
}

func buildResponsesWSCreateEvent(jsonData []byte, generate common.RawMessage) ([]byte, error) {
	var event map[string]common.RawMessage
	if err := common.Unmarshal(jsonData, &event); err != nil {
		return nil, err
	}
	typeData, err := common.Marshal(responsesWSEventTypeResponseCreate)
	if err != nil {
		return nil, err
	}
	event["type"] = typeData
	delete(event, "event_id")
	delete(event, "background")
	delete(event, "stream")
	delete(event, "stream_options")
	if len(generate) > 0 {
		event["generate"] = generate
	}
	return common.Marshal(event)
}

func removeResponsesWSTransportFields(jsonData []byte) ([]byte, error) {
	var data map[string]any
	if err := common.Unmarshal(jsonData, &data); err != nil {
		return jsonData, err
	}
	delete(data, "stream")
	delete(data, "stream_options")
	delete(data, "background")
	return common.Marshal(data)
}

func dialResponsesWebSocketUpstream(c *gin.Context, adaptor relaychannel.Adaptor, info *relaycommon.RelayInfo) (*websocket.Conn, *types.NewAPIError) {
	fullRequestURL, err := adaptor.GetRequestURL(info)
	if err != nil {
		return nil, types.NewError(fmt.Errorf("get request url failed: %w", err), types.ErrorCodeDoRequestFailed)
	}
	fullRequestURL = toWebSocketURL(fullRequestURL)
	targetHeader := http.Header{}
	if err := adaptor.SetupRequestHeader(c, &targetHeader, info); err != nil {
		return nil, types.NewError(fmt.Errorf("setup request header failed: %w", err), types.ErrorCodeDoRequestFailed)
	}
	sanitizedContext := sanitizedResponsesWSHeaderContext(c)
	headerOverride, err := relaychannel.ResolveHeaderOverride(info, sanitizedContext)
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeChannelHeaderOverrideInvalid)
	}
	for key, value := range headerOverride {
		targetHeader.Set(key, value)
	}
	mergeHeaderTokens(targetHeader, "OpenAI-Beta", responsesWSRequiredBetaTokens(info)...)
	targetConn, resp, err := websocket.DefaultDialer.Dial(fullRequestURL, targetHeader)
	if err != nil {
		statusCode := http.StatusInternalServerError
		if resp != nil {
			statusCode = resp.StatusCode
			_ = resp.Body.Close()
		}
		return nil, types.NewErrorWithStatusCode(fmt.Errorf("dial failed to %s: %w", relaycommon.SanitizeURLForLog(fullRequestURL), err), types.ErrorCodeDoRequestFailed, statusCode)
	}
	common.SetWebSocketReadLimit(targetConn)
	return targetConn, nil
}

func responsesWSRequiredBetaTokens(info *relaycommon.RelayInfo) []string {
	tokens := []string{"responses_websockets=2026-02-06"}
	if info != nil && info.ChannelType == appconstant.ChannelTypeCodex {
		tokens = append([]string{"responses=experimental"}, tokens...)
	}
	return tokens
}

func sanitizedResponsesWSHeaderContext(c *gin.Context) *gin.Context {
	if c == nil {
		return nil
	}
	sanitized := c.Copy()
	if c.Request == nil {
		return sanitized
	}
	sanitized.Request = c.Request.Clone(c.Request.Context())
	sanitized.Request.Header = c.Request.Header.Clone()
	for name := range sanitized.Request.Header {
		if isSensitiveResponsesWSClientHeader(name) {
			sanitized.Request.Header.Del(name)
		}
	}
	return sanitized
}

func isSensitiveResponsesWSClientHeader(name string) bool {
	lower := strings.ToLower(strings.TrimSpace(name))
	if strings.HasPrefix(lower, "sec-websocket-") {
		return true
	}
	switch lower {
	case "authorization", "cookie", "proxy-authorization", "x-api-key", "x-goog-api-key",
		"connection", "keep-alive", "proxy-authenticate", "proxy-connection", "te", "trailer",
		"transfer-encoding", "upgrade", "openai-beta":
		return true
	default:
		return false
	}
}

func mergeHeaderTokens(header http.Header, name string, required ...string) {
	seen := make(map[string]struct{})
	tokens := make([]string, 0, len(required)+2)
	appendToken := func(token string) {
		token = strings.TrimSpace(token)
		if token == "" {
			return
		}
		key := strings.ToLower(token)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		tokens = append(tokens, token)
	}
	for _, value := range header.Values(name) {
		for _, token := range strings.Split(value, ",") {
			appendToken(token)
		}
	}
	for _, token := range required {
		appendToken(token)
	}
	header.Set(name, strings.Join(tokens, ", "))
}

func toWebSocketURL(raw string) string {
	switch {
	case strings.HasPrefix(raw, "https://"):
		return "wss://" + strings.TrimPrefix(raw, "https://")
	case strings.HasPrefix(raw, "http://"):
		return "ws://" + strings.TrimPrefix(raw, "http://")
	default:
		return raw
	}
}

func (s *responsesWSSession) startTargetReader() {
	target := s.getTarget()
	if target == nil {
		return
	}
	go func() {
		for {
			messageType, message, err := target.ReadMessage()
			if err != nil {
				if !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
					logger.LogError(s.c, "responses websocket upstream read failed: "+err.Error())
				}
				s.failCurrent()
				_ = s.client.Close()
				return
			}
			s.observeUpstreamMessage(message)
			if err := s.writeClient(messageType, message); err != nil {
				logger.LogError(s.c, "responses websocket client write failed: "+err.Error())
				s.failCurrent()
				s.closeTarget()
				return
			}
		}
	}()
}

func (s *responsesWSSession) observeUpstreamMessage(message []byte) {
	state := s.getCurrent()
	if state == nil {
		return
	}
	var streamResponse dto.ResponsesStreamResponse
	if err := common.Unmarshal(message, &streamResponse); err != nil {
		return
	}

	terminal := false
	success := false
	state.dataMu.Lock()
	state.info.SetFirstResponseTime()
	switch streamResponse.Type {
	case "response.completed", "response.done":
		if streamResponse.Response != nil && relaycommon.IsNonBillableResponsesStatus(streamResponse.Response.Status) {
			state.images.Reset()
			terminal = true
			break
		}
		s.applyTerminalResponseUsage(state, streamResponse.Response)
		terminal = true
		success = true
	case "response.incomplete", "response.failed", "response.cancelled", "response.canceled", "response.error", "error":
		s.applyTerminalResponseUsage(state, streamResponse.Response)
		terminal = true
		success = responsesWSHasBillablePartialLocked(state)
	case "response.output_text.delta":
		state.outputText.WriteString(s.c, streamResponse.Delta)
	case dto.ResponsesOutputTypeItemDone:
		if streamResponse.Item != nil {
			switch streamResponse.Item.Type {
			case dto.BuildInCallWebSearchCall:
				state.info.CountBillableToolCall(dto.BuildInCallWebSearchCall, "")
			case dto.BuildInCallFileSearchCall:
				state.info.CountBillableToolCall(dto.BuildInCallFileSearchCall, "")
			case dto.BuildInCallFunctionCall, dto.BuildInCallToolUse:
				state.info.CountBillableToolCall(streamResponse.Item.Type, streamResponse.Item.Name)
			case dto.ResponsesOutputTypeImageGenerationCall:
				state.images.Observe(streamResponse.Item, streamResponse.OutputIndex)
			}
		}
	}
	state.dataMu.Unlock()
	if terminal {
		s.finishCall(state, success)
	}
}

func (s *responsesWSSession) applyTerminalResponseUsage(state *responsesWSCallState, response *dto.OpenAIResponsesResponse) {
	if state == nil || response == nil {
		return
	}
	if response.Usage != nil {
		service.ApplyResponsesUsage(state.usage, response.Usage)
	}
	for i := range response.Output {
		output := &response.Output[i]
		if output.Type == dto.ResponsesOutputTypeImageGenerationCall {
			idx := i
			state.images.Observe(output, &idx)
		}
	}
}

func (s *responsesWSSession) finishCall(state *responsesWSCallState, success bool) {
	if state == nil || !s.beginFinish(state) {
		return
	}
	defer s.completeFinish(state)
	if !success {
		state.dataMu.Lock()
		if responsesWSHasBillablePartialLocked(state) {
			success = true
		} else {
			state.images.Reset()
		}
		state.dataMu.Unlock()
	}
	if !success {
		state.refund(s.c)
		if state.commitRate != nil {
			state.commitRate(false)
		}
		return
	}
	state.dataMu.Lock()
	state.images.Commit(state.info)
	finalizeResponsesWSUsage(state)
	state.dataMu.Unlock()
	postResponsesWSConsumeQuota(s.c, state.info, state.usage, nil)
	if state.commitRate != nil {
		state.commitRate(true)
	}
}

func responsesWSHasBillablePartialLocked(state *responsesWSCallState) bool {
	if state == nil {
		return false
	}
	if state.usage != nil && (state.usage.PromptTokens != 0 || state.usage.CompletionTokens != 0 || state.usage.TotalTokens != 0 || state.usage.InputTokens != 0 || state.usage.OutputTokens != 0) {
		return true
	}
	if state.outputText.Len() != 0 || state.images.Count() != 0 {
		return true
	}
	if state.info == nil || state.info.ResponsesUsageInfo == nil {
		return false
	}
	for _, tool := range state.info.ResponsesUsageInfo.BuiltInTools {
		if tool != nil && tool.CallCount > 0 {
			return true
		}
	}
	return false
}

func finalizeResponsesWSUsage(state *responsesWSCallState) {
	if state == nil || state.usage == nil || state.info == nil {
		return
	}
	if state.usage.CompletionTokens == 0 {
		if output := state.outputText.String(); output != "" {
			state.usage.CompletionTokens = service.CountTextToken(output, state.info.UpstreamModelName)
		}
	}
	if state.usage.PromptTokens == 0 && state.usage.CompletionTokens != 0 {
		state.usage.PromptTokens = state.info.GetEstimatePromptTokens()
	}
	if state.usage.TotalTokens == 0 {
		state.usage.TotalTokens = state.usage.PromptTokens + state.usage.CompletionTokens
	}
}

func (state *responsesWSCallState) refund(c *gin.Context) {
	if state != nil && state.info != nil && state.info.Billing != nil {
		state.info.Billing.Refund(c)
	}
}

func (s *responsesWSSession) tryReserveCurrent(state *responsesWSCallState) bool {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	if s.current != nil {
		return false
	}
	s.current = state
	return true
}

func (s *responsesWSSession) hasCurrent() bool {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	return s.current != nil
}

func (s *responsesWSSession) beginFinish(state *responsesWSCallState) bool {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	if state == nil || s.current != state || state.finishing {
		return false
	}
	state.finishing = true
	return true
}

func (s *responsesWSSession) completeFinish(state *responsesWSCallState) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	if s.current == state {
		s.current = nil
	}
}

func (s *responsesWSSession) getCurrent() *responsesWSCallState {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	return s.current
}

func (s *responsesWSSession) failCurrent() {
	if state := s.getCurrent(); state != nil {
		s.finishCall(state, false)
	}
}

func (s *responsesWSSession) writeClient(messageType int, message []byte) error {
	s.clientWriteMu.Lock()
	defer s.clientWriteMu.Unlock()
	return s.client.WriteMessage(messageType, message)
}

func (s *responsesWSSession) hasTarget() bool {
	s.targetWriteMu.Lock()
	defer s.targetWriteMu.Unlock()
	return s.target != nil
}

func (s *responsesWSSession) getTarget() *websocket.Conn {
	s.targetWriteMu.Lock()
	defer s.targetWriteMu.Unlock()
	return s.target
}

func (s *responsesWSSession) setTarget(target *websocket.Conn) {
	s.targetWriteMu.Lock()
	defer s.targetWriteMu.Unlock()
	s.target = target
}

func (s *responsesWSSession) writeTarget(messageType int, message []byte) error {
	s.targetWriteMu.Lock()
	defer s.targetWriteMu.Unlock()
	if s.target == nil {
		return errors.New("responses websocket upstream is not connected")
	}
	return s.target.WriteMessage(messageType, message)
}

func (s *responsesWSSession) sendError(eventID string, apiErr *types.NewAPIError) {
	if apiErr == nil {
		return
	}
	payload, err := buildResponsesWSErrorPayload(eventID, apiErr)
	if err == nil {
		_ = s.writeClient(websocket.TextMessage, payload)
	}
}

func buildResponsesWSErrorPayload(eventID string, apiErr *types.NewAPIError) ([]byte, error) {
	if apiErr == nil {
		return nil, errors.New("api error is nil")
	}
	status := apiErr.StatusCode
	if status == 0 {
		status = http.StatusInternalServerError
	}
	openaiErr := apiErr.ToOpenAIError()
	return common.Marshal(&responsesWSErrorEvent{Type: "error", Status: status, EventID: eventID, Error: &openaiErr})
}

func (s *responsesWSSession) closeTarget() {
	var target *websocket.Conn
	var unregister func()
	s.targetWriteMu.Lock()
	target = s.target
	s.target = nil
	unregister = s.unregister
	s.unregister = nil
	s.targetWriteMu.Unlock()
	if unregister != nil {
		unregister()
	}
	if target != nil {
		_ = target.Close()
	}
}

func (s *responsesWSSession) registerChannelClose(channelID int) {
	unregister := wsmanager.Register(channelID, wsmanager.KindResponses, func(reason string) {
		s.closeForPolicy(reason)
	})
	s.targetWriteMu.Lock()
	if s.unregister != nil {
		s.unregister()
	}
	s.unregister = unregister
	s.targetWriteMu.Unlock()
}

func (s *responsesWSSession) closeForPolicy(reason string) {
	s.closeOnce.Do(func() {
		s.failCurrent()
		deadline := time.Now().Add(time.Second)
		closeMessage := websocket.FormatCloseMessage(websocket.ClosePolicyViolation, reason)
		_ = s.client.WriteControl(websocket.CloseMessage, closeMessage, deadline)
		if target := s.getTarget(); target != nil {
			_ = target.WriteControl(websocket.CloseMessage, closeMessage, deadline)
		}
		s.closeTarget()
		_ = s.client.Close()
	})
}

func checkResponsesWSModelAccess(c *gin.Context, modelName string) *types.NewAPIError {
	if !common.GetContextKeyBool(c, appconstant.ContextKeyTokenModelLimitEnabled) {
		return nil
	}
	raw, ok := common.GetContextKey(c, appconstant.ContextKeyTokenModelLimit)
	if !ok {
		return types.NewErrorWithStatusCode(errors.New("token has no model access"), types.ErrorCodeAccessDenied, http.StatusForbidden, types.ErrOptionWithSkipRetry())
	}
	tokenModelLimit, ok := raw.(map[string]bool)
	if !ok {
		tokenModelLimit = map[string]bool{}
	}
	matchName := ratio_setting.FormatMatchingModelName(modelName)
	if _, ok := tokenModelLimit[matchName]; !ok {
		return types.NewErrorWithStatusCode(fmt.Errorf("token is not allowed to use model %s", modelName), types.ErrorCodeAccessDenied, http.StatusForbidden, types.ErrOptionWithSkipRetry())
	}
	return nil
}

func selectResponsesWSChannel(c *gin.Context, modelName string, retryParam *service.RetryParam) (*appmodel.Channel, *types.NewAPIError) {
	if channelID := common.GetContextKeyString(c, appconstant.ContextKeyTokenSpecificChannelId); channelID != "" {
		id, err := strconv.Atoi(channelID)
		if err != nil {
			return nil, types.NewErrorWithStatusCode(err, types.ErrorCodeGetChannelFailed, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
		}
		channel, err := appmodel.GetChannelById(id, true)
		if err != nil {
			return nil, types.NewErrorWithStatusCode(err, types.ErrorCodeGetChannelFailed, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
		}
		if channel.Status != common.ChannelStatusEnabled {
			return nil, types.NewErrorWithStatusCode(errors.New("specified channel is disabled"), types.ErrorCodeGetChannelFailed, http.StatusForbidden, types.ErrOptionWithSkipRetry())
		}
		if apiErr := middleware.SetupContextForSelectedChannel(c, channel, modelName); apiErr != nil {
			return nil, apiErr
		}
		return channel, nil
	}

	usingGroup := common.GetContextKeyString(c, appconstant.ContextKeyUsingGroup)
	if usingGroup == "" {
		usingGroup = common.GetContextKeyString(c, appconstant.ContextKeyTokenGroup)
	}
	if retryParam.GetRetry() == 0 {
		if preferredChannelID, found := service.GetPreferredChannelByAffinity(c, modelName, usingGroup); found {
			preferred, err := appmodel.CacheGetChannel(preferredChannelID)
			if err == nil && preferred != nil && preferred.Status == common.ChannelStatusEnabled {
				if usingGroup == "auto" {
					userGroup := common.GetContextKeyString(c, appconstant.ContextKeyUserGroup)
					for _, group := range service.GetUserAutoGroup(userGroup) {
						if appmodel.IsChannelEnabledForGroupModel(group, modelName, preferred.Id) {
							common.SetContextKey(c, appconstant.ContextKeyAutoGroup, group)
							service.MarkChannelAffinityUsed(c, group, preferred.Id)
							if apiErr := middleware.SetupContextForSelectedChannel(c, preferred, modelName); apiErr != nil {
								return nil, apiErr
							}
							return preferred, nil
						}
					}
				} else if appmodel.IsChannelEnabledForGroupModel(usingGroup, modelName, preferred.Id) {
					service.MarkChannelAffinityUsed(c, usingGroup, preferred.Id)
					if apiErr := middleware.SetupContextForSelectedChannel(c, preferred, modelName); apiErr != nil {
						return nil, apiErr
					}
					return preferred, nil
				}
			}
		}
	}

	channel, selectGroup, err := service.CacheGetRandomSatisfiedChannel(retryParam)
	if err != nil {
		return nil, types.NewError(fmt.Errorf("获取分组 %s 下模型 %s 的可用渠道失败（retry）: %s", selectGroup, modelName, err.Error()), types.ErrorCodeGetChannelFailed, types.ErrOptionWithSkipRetry())
	}
	if channel == nil {
		return nil, types.NewError(fmt.Errorf("分组 %s 下模型 %s 的可用渠道不存在（retry）", selectGroup, modelName), types.ErrorCodeGetChannelFailed, types.ErrOptionWithSkipRetry())
	}
	if apiErr := middleware.SetupContextForSelectedChannel(c, channel, modelName); apiErr != nil {
		return nil, apiErr
	}
	return channel, nil
}

func selectResponsesWSChannelForSession(c *gin.Context, modelName string, retryParam *service.RetryParam, locked *appmodel.Channel) (*appmodel.Channel, *types.NewAPIError) {
	if locked == nil {
		return selectResponsesWSChannel(c, modelName, retryParam)
	}
	return validateResponsesWSTurnAuthorization(c, modelName, locked)
}

func validateResponsesWSTurnAuthorization(c *gin.Context, modelName string, locked *appmodel.Channel) (*appmodel.Channel, *types.NewAPIError) {
	if apiErr := checkResponsesWSModelAccess(c, modelName); apiErr != nil {
		return nil, apiErr
	}
	if locked == nil {
		return nil, nil
	}
	channel, err := loadResponsesWSLockedChannel(locked.Id)
	if err != nil || channel == nil {
		return nil, types.NewErrorWithStatusCode(fmt.Errorf("locked channel %d is unavailable", locked.Id), types.ErrorCodeGetChannelFailed, http.StatusServiceUnavailable, types.ErrOptionWithSkipRetry())
	}
	if channel.Status != common.ChannelStatusEnabled {
		return nil, types.NewErrorWithStatusCode(fmt.Errorf("locked channel %d is disabled", channel.Id), types.ErrorCodeGetChannelFailed, http.StatusForbidden, types.ErrOptionWithSkipRetry())
	}
	if channel.Type != appconstant.ChannelTypeOpenAI && channel.Type != appconstant.ChannelTypeOpenHuman && channel.Type != appconstant.ChannelTypeCodex {
		return nil, types.NewErrorWithStatusCode(fmt.Errorf("locked channel %d no longer supports responses websocket", channel.Id), types.ErrorCodeGetChannelFailed, http.StatusForbidden, types.ErrOptionWithSkipRetry())
	}
	if channelIDRaw := common.GetContextKeyString(c, appconstant.ContextKeyTokenSpecificChannelId); channelIDRaw != "" {
		channelID, parseErr := strconv.Atoi(channelIDRaw)
		if parseErr != nil || channelID != channel.Id {
			return nil, types.NewErrorWithStatusCode(fmt.Errorf("locked channel %d is not allowed by the token channel constraint", channel.Id), types.ErrorCodeGetChannelFailed, http.StatusForbidden, types.ErrOptionWithSkipRetry())
		}
	}
	usingGroup := common.GetContextKeyString(c, appconstant.ContextKeyUsingGroup)
	if usingGroup == "" {
		usingGroup = common.GetContextKeyString(c, appconstant.ContextKeyTokenGroup)
	}
	allowed := false
	selectedAutoGroup := ""
	if usingGroup == "auto" {
		userGroup := common.GetContextKeyString(c, appconstant.ContextKeyUserGroup)
		for _, group := range service.GetUserAutoGroup(userGroup) {
			if isResponsesWSChannelAvailable(group, modelName, channel.Id) {
				selectedAutoGroup = group
				allowed = true
				break
			}
		}
	} else {
		allowed = isResponsesWSChannelAvailable(usingGroup, modelName, channel.Id)
	}
	if !allowed {
		return nil, types.NewErrorWithStatusCode(fmt.Errorf("locked channel %d is no longer available for group %s and model %s", channel.Id, usingGroup, modelName), types.ErrorCodeGetChannelFailed, http.StatusForbidden, types.ErrOptionWithSkipRetry())
	}
	if selectedAutoGroup != "" {
		common.SetContextKey(c, appconstant.ContextKeyAutoGroup, selectedAutoGroup)
	}
	if apiErr := middleware.SetupContextForSelectedChannel(c, channel, modelName); apiErr != nil {
		return nil, apiErr
	}
	return channel, nil
}

func addResponsesWSUsedChannel(c *gin.Context, channelID int) {
	usedChannels := c.GetStringSlice("use_channel")
	usedChannels = append(usedChannels, strconv.Itoa(channelID))
	c.Set("use_channel", usedChannels)
}
