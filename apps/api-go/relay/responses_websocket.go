package relay

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/LIghtJUNction/api.lmm.best/common"
	appconstant "github.com/LIghtJUNction/api.lmm.best/constant"
	"github.com/LIghtJUNction/api.lmm.best/logger"
	"github.com/LIghtJUNction/api.lmm.best/middleware"
	appmodel "github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/pkg/wsmanager"
	relaychannel "github.com/LIghtJUNction/api.lmm.best/relay/channel"
	"github.com/LIghtJUNction/api.lmm.best/relay/channel/openai"
	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	"github.com/LIghtJUNction/api.lmm.best/relay/helper"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/relayconvert"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/types"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/LIghtJUNction/api.lmm.best/setting"
	"github.com/LIghtJUNction/api.lmm.best/setting/model_setting"
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
	StreamID string
	EventID  string
}

type responsesWSErrorEvent struct {
	Type     string             `json:"type"`
	Status   int                `json:"status"`
	EventID  string             `json:"event_id,omitempty"`
	StreamID string             `json:"stream_id,omitempty"`
	Error    *types.OpenAIError `json:"error"`
}

type responsesWSCallState struct {
	StreamID            string
	eventID             string
	responseID          string
	info                *relaycommon.RelayInfo
	usage               *dto.Usage
	outputText          responsesWSOutputTextBuffer
	images              relaycommon.ImageGenerationCallCounter
	commitRate          middleware.ModelRequestRateLimitCommit
	rateMu              sync.Mutex
	rateDeliveryPending bool
	rateOutcomeReady    bool
	rateSuccess         bool
	rateFinalized       bool
	dataMu              sync.Mutex
	finishing           bool
}

// Controls can fail asynchronously, including after their response has ended.
// Keep their envelope identity outside the HTTP request and billing state.
type responsesWSControl struct {
	eventID, streamID, responseID string
	isCancel                      bool
	turn                          *responsesWSCallState
}

type responsesWSCompletedCreate struct {
	eventID, streamID string
}

func (create responsesWSCompletedCreate) identityBytes() int {
	return len(create.eventID) + len(create.streamID)
}

const responsesWSMaxPendingControls = 32
const responsesWSMaxControlIdentityBytes = 64 * 1024

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
	c                     *gin.Context
	client                *websocket.Conn
	target                *websocket.Conn
	unregister            func()
	lockedModel           string
	lockedChannel         *appmodel.Channel
	connectionFingerprint string
	connectionKey         string
	connectionKeyIndex    int
	nextEventIndex        int
	closeOnce             sync.Once
	closed                atomic.Bool

	clientWriteMu sync.Mutex
	targetWriteMu sync.Mutex
	// Serializes forwarding with connection replacement, so a retired reader
	// cannot settle or close a newer turn after a configuration change.
	targetReadMu        sync.Mutex
	targetReaders       sync.WaitGroup
	stateMu             sync.Mutex
	current             *responsesWSCallState
	finishedResponseIDs []string
	completedCreates    []responsesWSCompletedCreate
	pendingControls     []responsesWSControl
	resolvedControls    []responsesWSControl
	revalidateAuth      func(*gin.Context) *types.NewAPIError
	reuseTarget         func(responsesWSCreateRequest, middleware.ModelRequestRateLimitCommit) *types.NewAPIError
}

var loadResponsesWSLockedChannel = appmodel.CacheGetChannel
var isResponsesWSChannelAvailable = appmodel.IsChannelEnabledForGroupModel
var postResponsesWSConsumeQuota = service.PostTextConsumeQuota

func ResponsesWebSocketHelper(c *gin.Context, client *websocket.Conn) *types.NewAPIError {
	common.SetWebSocketReadLimit(client)
	session := &responsesWSSession{c: c, client: client}
	unregister, accepted := wsmanager.Register(0, wsmanager.KindResponses, func(code int, reason string) {
		session.closeWithCode(code, reason)
	})
	if !accepted {
		return nil
	}
	session.targetWriteMu.Lock()
	if session.closed.Load() {
		session.targetWriteMu.Unlock()
		unregister()
		return nil
	}
	session.unregister = unregister
	session.targetWriteMu.Unlock()
	defer func() {
		_ = session.client.Close()
		session.closeTarget()
		session.targetReaders.Wait()
	}()
	defer session.failCurrent()

	for {
		messageType, message, err := client.ReadMessage()
		if err != nil {
			if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway, websocket.CloseServiceRestart) {
				return nil
			}
			return types.NewError(err, types.ErrorCodeBadRequestBody, types.ErrOptionWithSkipRetry())
		}

		eventType, eventID, streamID, eventErr := responsesWSEventMetadata(message)
		if eventErr != nil {
			session.sendError(eventID, streamID, newResponsesWSInvalidRequestError(eventErr))
			continue
		}

		switch eventType {
		case responsesWSEventTypeResponseCreate:
			create, normalizedEventID, err := normalizeResponsesWSCreateEvent(message)
			if err != nil {
				session.sendError(eventID, streamID, newResponsesWSInvalidRequestError(err))
				continue
			}
			if create.Request.Model == "" {
				session.sendError(normalizedEventID, create.StreamID, newResponsesWSInvalidRequestError(errors.New("model is required")))
				continue
			}
			if apiErr := session.handleResponseCreate(create); apiErr != nil {
				session.sendError(normalizedEventID, create.StreamID, apiErr)
			}
		case responsesWSEventTypeResponseCancel:
			if apiErr := session.handleResponseCancel(messageType, message); apiErr != nil {
				session.sendError(eventID, streamID, apiErr)
			}
		default:
			if !session.hasTarget() {
				session.sendError(eventID, streamID, newResponsesWSInvalidRequestError(errors.New("first responses websocket event must be response.create")))
				continue
			}
			if apiErr := session.forwardControl(messageType, message, eventID, streamID); apiErr != nil {
				session.sendError(eventID, streamID, apiErr)
			}
		}
	}
}

func (s *responsesWSSession) handleResponseCancel(messageType int, message []byte) *types.NewAPIError {
	state := s.getCurrent()
	if state == nil || !s.hasTarget() {
		return newResponsesWSInvalidRequestError(errors.New("no response is active to cancel"))
	}
	_, eventID, streamID, err := responsesWSEventMetadata(message)
	if err != nil {
		return newResponsesWSInvalidRequestError(err)
	}
	if streamID != "" && streamID != state.StreamID {
		return newResponsesWSInvalidRequestError(errors.New("stream_id does not match the active response"))
	}
	return s.forwardControl(messageType, message, eventID, streamID)
}

func (s *responsesWSSession) forwardControl(messageType int, message []byte, eventID, streamID string) *types.NewAPIError {
	var envelope struct {
		Type       string            `json:"type"`
		ResponseID common.RawMessage `json:"response_id"`
	}
	if err := common.Unmarshal(message, &envelope); err != nil {
		return newResponsesWSInvalidRequestError(err)
	}
	var responseID string
	_ = common.Unmarshal(envelope.ResponseID, &responseID)
	control := responsesWSControl{eventID: eventID, streamID: streamID, responseID: responseID, isCancel: envelope.Type == responsesWSEventTypeResponseCancel}
	// Unidentified data controls have no distinguishable asynchronous response.
	// Retain legacy forwarding without filling the registry with audio chunks.
	if control.eventID != "" || control.responseID != "" || control.isCancel {
		s.stateMu.Lock()
		control.turn = s.current
		identityBytes := control.identityBytes()
		for _, pending := range s.pendingControls {
			identityBytes += pending.identityBytes()
		}
		duplicate := false
		if eventID != "" {
			duplicate = s.current != nil && s.current.eventID == eventID
			for _, previous := range s.pendingControls {
				duplicate = duplicate || previous.eventID == eventID
			}
			for _, previous := range s.resolvedControls {
				duplicate = duplicate || previous.eventID == eventID
			}
			for _, previous := range s.completedCreates {
				duplicate = duplicate || previous.eventID == eventID
			}
		}
		if duplicate || len(s.pendingControls) >= responsesWSMaxPendingControls || identityBytes > responsesWSMaxControlIdentityBytes {
			s.stateMu.Unlock()
			return newResponsesWSInvalidRequestError(errors.New("response control identity is already pending or the pending control limit was reached"))
		}
		// Register before the write so an immediate upstream rejection cannot
		// race ahead of its correlation entry. Write failure closes the target.
		s.pendingControls = append(s.pendingControls, control)
		s.stateMu.Unlock()
	}
	if err := s.writeTarget(messageType, message); err != nil {
		return s.handleTargetWriteFailure(err)
	}
	return nil
}

func (control responsesWSControl) identityBytes() int {
	return len(control.eventID) + len(control.streamID) + len(control.responseID)
}

func responsesWSEventMetadata(message []byte) (string, string, string, error) {
	var raw map[string]common.RawMessage
	if err := common.Unmarshal(message, &raw); err != nil || raw == nil {
		return "", "", "", errors.New("invalid websocket event json")
	}
	// Read identities independently so valid strings remain available when a
	// different metadata field is malformed. Never infer one from response content.
	var eventType, eventID, streamID string
	typeErr := common.Unmarshal(raw["type"], &eventType)
	var eventErr, streamErr error
	if field, ok := raw["event_id"]; ok {
		eventErr = common.Unmarshal(field, &eventID)
	}
	if field, ok := raw["stream_id"]; ok {
		streamErr = common.Unmarshal(field, &streamID)
	}
	if streamErr != nil {
		return eventType, eventID, "", errors.New("stream_id must be a string or null")
	}
	if eventErr != nil {
		return eventType, "", streamID, errors.New("event_id must be a string or null")
	}
	if typeErr != nil {
		return "", eventID, streamID, errors.New("websocket event type must be a string")
	}
	if strings.TrimSpace(eventType) == "" {
		return "", eventID, streamID, errors.New("websocket event type is required")
	}
	return eventType, eventID, streamID, nil
}

func newResponsesWSInvalidRequestError(err error) *types.NewAPIError {
	return types.NewErrorWithStatusCode(err, types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
}

func normalizeResponsesWSCreateEvent(message []byte) (responsesWSCreateRequest, string, error) {
	_, eventID, streamID, metadataErr := responsesWSEventMetadata(message)
	create := responsesWSCreateRequest{StreamID: streamID, EventID: eventID}
	if metadataErr != nil {
		return create, eventID, metadataErr
	}
	var event responsesWSCreateEvent
	if err := common.Unmarshal(message, &event); err != nil {
		return create, eventID, err
	}
	if event.Type != responsesWSEventTypeResponseCreate {
		return create, event.EventID, fmt.Errorf("unsupported event type %q", event.Type)
	}

	var raw map[string]common.RawMessage
	if err := common.Unmarshal(message, &raw); err != nil {
		return create, event.EventID, err
	}
	generate := raw["generate"]
	payload := event.Request
	if len(payload) == 0 {
		delete(raw, "type")
		delete(raw, "event_id")
		delete(raw, "stream_id")
		delete(raw, "background")
		delete(raw, "generate")
		delete(raw, "stream")
		delete(raw, "stream_options")
		var err error
		payload, err = common.Marshal(raw)
		if err != nil {
			return create, event.EventID, err
		}
	} else {
		var responseMap map[string]common.RawMessage
		if err := common.Unmarshal(payload, &responseMap); err == nil {
			if len(generate) == 0 {
				generate = responseMap["generate"]
			}
			delete(responseMap, "generate")
			delete(responseMap, "stream_id")
			if merged, err := common.Marshal(responseMap); err == nil {
				payload = merged
			}
		}
	}

	var req dto.OpenAIResponsesRequest
	if err := common.Unmarshal(payload, &req); err != nil {
		return create, event.EventID, err
	}
	req.Stream = nil
	req.StreamOptions = nil
	create.Request, create.Generate = req, generate
	return create, event.EventID, nil
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
		if !s.hasCurrent() && (apiErr.GetErrorCode() == "responses_websocket_disabled" || apiErr.GetErrorCode() == "responses_websocket_unsupported") {
			// Rejecting a second create must not orphan an already admitted turn.
			// Its terminal event still releases the rate reservation and settles
			// any output; an idle connection can be retired immediately.
			s.closeTarget()
		}
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
	if create.EventID != "" {
		if len(create.EventID)+len(create.StreamID) > responsesWSMaxControlIdentityBytes {
			return newResponsesWSInvalidRequestError(errors.New("response.create identity exceeds the retained identity limit"))
		}
		s.stateMu.Lock()
		duplicate := false
		for _, control := range s.pendingControls {
			duplicate = duplicate || control.eventID == create.EventID
		}
		for _, control := range s.resolvedControls {
			duplicate = duplicate || control.eventID == create.EventID
		}
		for _, previous := range s.completedCreates {
			duplicate = duplicate || previous.eventID == create.EventID
		}
		s.stateMu.Unlock()
		if duplicate {
			return newResponsesWSInvalidRequestError(errors.New("event_id belongs to a retained control or create"))
		}
	}
	if lockedChannel != nil && s.hasTarget() && s.connectionFingerprint != "" {
		s.retainConnectionCredential(lockedChannel)
		if responsesWSConnectionFingerprint(s.c, lockedChannel, modelName) != s.connectionFingerprint {
			s.closeTarget()
		}
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

func (s *responsesWSSession) handleTargetWriteFailure(err error) *types.NewAPIError {
	s.failCurrent()
	s.closeTarget()
	apiErr := types.NewError(err, types.ErrorCodeBadResponse)
	apiErr, _ = s.processChannelError(s.lockedChannel, apiErr, nil)
	return apiErr
}

func (s *responsesWSSession) handleTargetWriteFailureWithState(state *responsesWSCallState, err error) *types.NewAPIError {
	s.finishCall(state, false, false)
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
		if apiErr := responsesWSChannelEligibility(channel, s.c.Request.URL.Path, modelName); apiErr != nil {
			lastErr = apiErr
			break
		}
		addResponsesWSUsedChannel(s.c, channel.Id)

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
		if err := s.writeFirstTargetEvent(state, payload); err != nil {
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
		s.connectionFingerprint = responsesWSConnectionFingerprint(s.c, channel, modelName)
		s.connectionKey = state.info.ApiKey
		s.connectionKeyIndex = state.info.ChannelMultiKeyIndex
		if !s.registerChannelClose(channel.Id) {
			return nil
		}
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

func (s *responsesWSSession) writeFirstTargetEvent(state *responsesWSCallState, payload []byte) error {
	if err := s.writeTarget(websocket.TextMessage, payload); err != nil {
		// A failed, invisible connection attempt refunds its billing reservation,
		// while the logical create keeps its rate reservation through retries.
		s.finishCallWithRate(state, false, false, false)
		s.closeTarget()
		return err
	}
	return nil
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

	payload, apiErr := buildResponsesWSCreatePayload(s.c, relayInfo, req, create.Generate, create.StreamID)
	if apiErr != nil {
		if relayInfo.Billing != nil {
			relayInfo.Billing.Refund(s.c)
		}
		return nil, nil, apiErr
	}
	return &responsesWSCallState{
		StreamID:   create.StreamID,
		eventID:    create.EventID,
		info:       relayInfo,
		usage:      &dto.Usage{},
		outputText: newResponsesWSOutputTextBuffer(s.c),
		commitRate: commitRate,
	}, payload, nil
}

func buildResponsesWSCreatePayload(c *gin.Context, relayInfo *relaycommon.RelayInfo, req dto.OpenAIResponsesRequest, generate common.RawMessage, streamID string) ([]byte, *types.NewAPIError) {
	relayInfo.InitChannelMeta(c)
	request, err := common.DeepCopy(&req)
	if err != nil {
		return nil, types.NewError(fmt.Errorf("failed to copy responses request: %w", err), types.ErrorCodeInvalidRequest, types.ErrOptionWithSkipRetry())
	}
	if !model_setting.GetGlobalSettings().PassThroughRequestEnabled && !relayInfo.ChannelSetting.PassThroughBodyEnabled {
		if err := relayconvert.SanitizeToolSchemas(request); err != nil {
			return nil, types.NewError(err, types.ErrorCodeInvalidRequest, types.ErrOptionWithSkipRetry())
		}
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
	event, err := buildResponsesWSCreateEvent(jsonData, generate, streamID)
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
	}
	return event, nil
}

func buildResponsesWSCreateEvent(jsonData []byte, generate common.RawMessage, streamID string) ([]byte, error) {
	var event map[string]common.RawMessage
	if err := common.Unmarshal(jsonData, &event); err != nil {
		return nil, err
	}
	typeData, err := common.Marshal(responsesWSEventTypeResponseCreate)
	if err != nil {
		return nil, err
	}
	event["type"] = typeData
	delete(event, "stream_id")
	if streamID != "" {
		event["stream_id"], err = common.Marshal(streamID)
		if err != nil {
			return nil, err
		}
	}
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
	var data map[string]common.RawMessage
	if err := common.Unmarshal(jsonData, &data); err != nil {
		return jsonData, err
	}
	delete(data, "stream")
	delete(data, "stream_options")
	delete(data, "background")
	delete(data, "stream_id")
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
	statusCode := http.StatusInternalServerError
	if resp != nil {
		statusCode = resp.StatusCode
		if closeErr := resp.Body.Close(); closeErr != nil {
			logger.LogError(c, "close responses websocket handshake body failed: "+closeErr.Error())
		}
	}
	if err != nil {
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
		// pi-lens-ignore: opengrep:javascript.lang.security.detect-insecure-websocket.detect-insecure-websocket
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
	s.targetReaders.Add(1)
	go func() {
		defer s.targetReaders.Done()
		for {
			messageType, message, err := target.ReadMessage()
			if err != nil {
				s.targetReadMu.Lock()
				if s.getTarget() != target {
					s.targetReadMu.Unlock()
					return
				}
				if !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
					logger.LogError(s.c, "responses websocket upstream read failed: "+err.Error())
				}
				s.failCurrent()
				_ = s.client.Close()
				s.targetReadMu.Unlock()
				return
			}
			current, err := s.forwardTargetMessage(target, messageType, message)
			if !current {
				return
			}
			if err != nil {
				return
			}
		}
	}()
}

func (s *responsesWSSession) forwardTargetMessage(target *websocket.Conn, messageType int, message []byte) (bool, error) {
	s.targetReadMu.Lock()
	defer s.targetReadMu.Unlock()
	if s.getTarget() != target {
		return false, nil
	}
	err := s.forwardUpstreamMessage(messageType, message)
	if err != nil {
		logger.LogError(s.c, "responses websocket client write failed: "+err.Error())
		s.failCurrent()
		s.closeTargetLocked()
	}
	return true, err
}

// processUpstreamMessage captures identity before terminal observation clears the
// turn. Only protocol response/error events are annotated; provider fields win.
func (s *responsesWSSession) processUpstreamMessage(message []byte) []byte {
	return s.processUpstreamMessageForState(s.getCurrent(), message)
}

func (s *responsesWSSession) forwardUpstreamMessage(messageType int, message []byte) error {
	state := s.getCurrent()
	if state != nil {
		state.rateMu.Lock()
		state.rateDeliveryPending = true
		state.rateMu.Unlock()
	}
	message = s.processUpstreamMessageForState(state, message)
	err := s.writeClient(messageType, message)
	if state != nil {
		state.completeRateDelivery(err == nil)
	}
	return err
}

func (s *responsesWSSession) processUpstreamMessageForState(state *responsesWSCallState, message []byte) []byte {
	var event map[string]common.RawMessage
	if err := common.Unmarshal(message, &event); err != nil || event == nil {
		return message
	}
	var eventType, explicitStreamID string
	if err := common.Unmarshal(event["type"], &eventType); err != nil || (eventType != "error" && !strings.HasPrefix(eventType, "response.")) {
		return message
	}
	if field, present := event["stream_id"]; present {
		if err := common.Unmarshal(field, &explicitStreamID); err != nil {
			return message
		}
	}
	streamID := ""
	if state != nil {
		streamID = state.StreamID
	}
	observe := true
	if eventType == "error" {
		if controlStreamID, matched, ambiguous := s.correlateControlError(event, state); matched || ambiguous {
			streamID = controlStreamID
			observe = false
		}
	}
	if explicitStreamID != "" && streamID != "" && explicitStreamID != streamID {
		observe = false
	}
	if observe {
		var responseID string
		_ = common.Unmarshal(event["response_id"], &responseID)
		if responseID == "" {
			var response struct {
				ID string `json:"id"`
			}
			_ = common.Unmarshal(event["response"], &response)
			responseID = response.ID
		}
		activeResponseID := ""
		if state != nil {
			state.dataMu.Lock()
			activeResponseID = state.responseID
			state.dataMu.Unlock()
		}
		s.stateMu.Lock()
		previousResponse := false
		for _, finishedResponseID := range s.finishedResponseIDs {
			previousResponse = previousResponse || responseID == finishedResponseID
		}
		s.stateMu.Unlock()
		if responseID != "" && (previousResponse || activeResponseID != "" && responseID != activeResponseID) {
			// A late terminal for an earlier response cannot borrow the current
			// turn's identity, usage or successful-request rate slot.
			observe, streamID = false, ""
		}
		if observe && state != nil && strings.HasPrefix(eventType, "response.") && responseID != "" {
			state.dataMu.Lock()
			state.responseID = responseID
			state.dataMu.Unlock()
		}
	}
	if observe {
		s.observeUpstreamMessageForState(state, message)
	}
	if _, present := event["stream_id"]; !present && streamID != "" {
		event["stream_id"], _ = common.Marshal(streamID)
		if correlated, err := common.Marshal(event); err == nil {
			return correlated
		}
	}
	return message
}

// Exact references take priority over inference. An ambiguous or unknown nested
// client reference must not turn a control failure into an active-turn failure.
func (s *responsesWSSession) correlateControlError(event map[string]common.RawMessage, state *responsesWSCallState) (string, bool, bool) {
	var eventID, streamID, responseID string
	_ = common.Unmarshal(event["event_id"], &eventID)
	_ = common.Unmarshal(event["stream_id"], &streamID)
	_ = common.Unmarshal(event["response_id"], &responseID)
	if responseID == "" {
		var response struct {
			ID string `json:"id"`
		}
		_ = common.Unmarshal(event["response"], &response)
		responseID = response.ID
	}
	var failure struct {
		EventID string `json:"event_id"`
		Type    string `json:"type"`
		Code    string `json:"code"`
	}
	_ = common.Unmarshal(event["error"], &failure)
	activeResponseID := ""
	if state != nil {
		state.dataMu.Lock()
		activeResponseID = state.responseID
		state.dataMu.Unlock()
	}
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	if failure.EventID == "" {
		for _, previous := range s.completedCreates {
			if eventID != previous.eventID {
				continue
			}
			currentResponse := activeResponseID != "" && responseID == activeResponseID
			currentStream := state != nil && streamID != "" && streamID == state.StreamID && streamID != previous.streamID
			if !currentResponse && !currentStream {
				return "", false, true
			}
			// An explicit current response or a distinct current stream can
			// identify a provider output ID that collides with an old create.
			return "", false, false
		}
	}
	all := make([]responsesWSControl, 0, len(s.pendingControls)+len(s.resolvedControls))
	all = append(all, s.pendingControls...)
	all = append(all, s.resolvedControls...)
	topMatch, referenceMatch := -1, -1
	for i, control := range all {
		if control.eventID == "" {
			continue
		}
		if control.eventID == eventID {
			if topMatch >= 0 {
				return "", false, true
			}
			topMatch = i
		}
		if control.eventID == failure.EventID {
			if referenceMatch >= 0 {
				return "", false, true
			}
			referenceMatch = i
		}
	}
	topIsCreate := state != nil && state.eventID != "" && eventID == state.eventID
	referenceIsCreate := state != nil && state.eventID != "" && failure.EventID == state.eventID
	match := topMatch
	if failure.EventID != "" {
		// The nested reference names the rejected client event. An active
		// create rejection must settle that turn, never a pending cancel.
		if referenceIsCreate {
			if topMatch >= 0 {
				return "", false, true
			}
			return "", false, false
		}
		if referenceMatch < 0 || topIsCreate || topMatch >= 0 && topMatch != referenceMatch {
			return "", false, true
		}
		match = referenceMatch
	} else if topIsCreate {
		return "", false, false
	}
	if match < 0 {
		for i, control := range all {
			targetMatch := (state == nil || activeResponseID != "") && responseID != "" && responseID == control.responseID && responseID != activeResponseID && (streamID == "" || streamID == control.streamID)
			if !targetMatch {
				continue
			}
			if match >= 0 {
				return "", false, true
			}
			match = i
		}
	}
	if match < 0 {
		knownForeignResponse := responseID != "" && activeResponseID != "" && responseID != activeResponseID
		for _, finishedResponseID := range s.finishedResponseIDs {
			if responseID != "" && responseID == finishedResponseID {
				knownForeignResponse = true
				break
			}
		}
		for i, control := range s.pendingControls {
			// An implicit cancel cannot claim a known foreign or completed response,
			// including before response.created identifies the current response.
			targetMatch := responseID == "" || responseID == control.responseID || control.responseID == "" && !knownForeignResponse
			cancelRejection := control.isCancel && failure.Type == "invalid_request_error" && (failure.Code == "response_not_found" || failure.Code == "response_not_active" || failure.Code == "response_already_completed") && targetMatch && (streamID == "" || streamID == control.streamID)
			if !cancelRejection {
				continue
			}
			if match >= 0 {
				return "", false, true
			}
			match = i
		}
	}
	if match < 0 {
		return "", false, false
	}
	control := all[match]
	if match < len(s.pendingControls) {
		copy(s.pendingControls[match:], s.pendingControls[match+1:])
		last := len(s.pendingControls) - 1
		s.pendingControls[last] = responsesWSControl{}
		s.pendingControls = s.pendingControls[:last]
		s.rememberResolvedControlLocked(control)
	}
	return control.streamID, true, false
}

func (s *responsesWSSession) rememberResolvedControlLocked(control responsesWSControl) {
	control.turn = nil
	s.resolvedControls = append(s.resolvedControls, control)
	identityBytes := 0
	for _, resolved := range s.resolvedControls {
		identityBytes += resolved.identityBytes()
	}
	for len(s.resolvedControls) > responsesWSMaxPendingControls || identityBytes > responsesWSMaxControlIdentityBytes {
		identityBytes -= s.resolvedControls[0].identityBytes()
		s.resolvedControls[0] = responsesWSControl{}
		s.resolvedControls = s.resolvedControls[1:]
	}
}

func (s *responsesWSSession) observeUpstreamMessage(message []byte) {
	s.observeUpstreamMessageForState(s.getCurrent(), message)
}

func (s *responsesWSSession) observeUpstreamMessageForState(state *responsesWSCallState, message []byte) {
	if state == nil {
		return
	}
	var streamResponse dto.ResponsesStreamResponse
	if err := common.Unmarshal(message, &streamResponse); err != nil {
		return
	}

	terminal := false
	success := false
	requestSucceeded := false
	state.dataMu.Lock()
	state.info.SetFirstResponseTime()
	if streamResponse.Response != nil {
		state.info.ObserveResponseModel(streamResponse.Response.Model)
		if streamResponse.Response.ID != "" {
			state.responseID = streamResponse.Response.ID
		}
		if streamResponse.Response.Usage != nil {
			candidate := &dto.Usage{}
			service.ApplyResponsesUsage(candidate, streamResponse.Response.Usage)
			if service.ValidUsage(candidate) {
				*state.usage = *candidate
				state.info.ResponsesUsageReported = true
			}
		}
	}
	switch streamResponse.Type {
	case "response.completed", "response.done":
		s.applyTerminalResponseUsage(state, streamResponse.Response)
		terminal = true
		if streamResponse.Response != nil && (relaycommon.IsNonBillableResponsesStatus(streamResponse.Response.Status) || streamResponse.Response.Error != nil) {
			if relaycommon.IsNonBillableResponsesStatus(streamResponse.Response.Status) {
				state.images.Reset()
			}
			success = responsesWSHasBillablePartialLocked(state)
			break
		}
		if !state.info.ResponsesUsageReported && state.outputText.Len() == 0 {
			state.outputText.WriteString(s.c, openai.ResponsesTerminalOutputText(string(message)))
		}
		success = true
		requestSucceeded = true
	case "response.incomplete", "response.failed", "response.cancelled", "response.canceled", "response.error", "error":
		s.applyTerminalResponseUsage(state, streamResponse.Response)
		terminal = true
		success = responsesWSHasBillablePartialLocked(state)
	case "response.output_text.delta", "response.refusal.delta", "response.function_call_arguments.delta", "response.reasoning_text.delta", "response.reasoning_summary_text.delta":
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
		s.finishCall(state, success, requestSucceeded)
	}
}

func (s *responsesWSSession) applyTerminalResponseUsage(state *responsesWSCallState, response *dto.OpenAIResponsesResponse) {
	if state == nil || response == nil {
		return
	}
	if response.Usage != nil {
		// Only terminal events call this helper. Reports, including explicit
		// zero, remain authoritative on successful and failed turns alike.
		if state.info != nil {
			state.info.ResponsesUsageReported = true
		}
		candidate := &dto.Usage{}
		service.ApplyResponsesUsage(candidate, response.Usage)
		*state.usage = *candidate
	}
	for i := range response.Output {
		output := &response.Output[i]
		if output.Type == dto.ResponsesOutputTypeImageGenerationCall {
			idx := i
			state.images.Observe(output, &idx)
		}
	}
}

func (s *responsesWSSession) finishCall(state *responsesWSCallState, success, requestSucceeded bool) {
	s.finishCallWithRate(state, success, requestSucceeded, true)
}

func (s *responsesWSSession) finishCallWithRate(state *responsesWSCallState, success, requestSucceeded, completeRate bool) {
	if state == nil || !s.beginFinish(state) {
		return
	}
	// An invisible failed first write may be retried with the same logical
	// create identity; it must not enter completed-create history yet.
	defer s.completeFinish(state, completeRate)
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
		if completeRate {
			state.finishRate(false)
		}
		return
	}
	state.dataMu.Lock()
	state.images.Commit(state.info)
	finalizeResponsesWSUsage(state)
	state.dataMu.Unlock()
	postResponsesWSConsumeQuota(s.c, state.info, state.usage, nil)
	if completeRate {
		// Billable partial output does not make a failed/cancelled request a
		// success for the shared HTTP/WebSocket success-only rate limit.
		state.finishRate(requestSucceeded)
	}
}

func (state *responsesWSCallState) finishRate(success bool) {
	state.rateMu.Lock()
	if state.rateFinalized {
		state.rateMu.Unlock()
		return
	}
	if state.rateDeliveryPending {
		state.rateOutcomeReady, state.rateSuccess = true, success
		state.rateMu.Unlock()
		return
	}
	state.rateFinalized = true
	state.rateMu.Unlock()
	if state.commitRate != nil {
		state.commitRate(success)
	}
}

func (state *responsesWSCallState) completeRateDelivery(delivered bool) {
	state.rateMu.Lock()
	state.rateDeliveryPending = false
	if state.rateFinalized || !state.rateOutcomeReady && delivered {
		state.rateMu.Unlock()
		return
	}
	success := state.rateOutcomeReady && state.rateSuccess && delivered
	state.rateFinalized = true
	state.rateMu.Unlock()
	if state.commitRate != nil {
		state.commitRate(success)
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
	if state.info.ResponsesUsageReported {
		return
	}
	if state.usage.CompletionTokens == 0 {
		if output := state.outputText.String(); output != "" {
			state.usage.CompletionTokens = service.CountTextToken(output, state.info.UpstreamModelName)
		}
	}
	if state.usage.PromptTokens == 0 && state.usage.CompletionTokens != 0 {
		estimated := state.info.GetEstimatePromptTokens()
		if estimated < 0 {
			estimated = 0
		} else if estimated > openai.MaxUnverifiedResponsesInputTokens {
			estimated = openai.MaxUnverifiedResponsesInputTokens
		}
		state.usage.PromptTokens = estimated
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
	for i := range s.pendingControls {
		if s.pendingControls[i].turn == nil {
			s.pendingControls[i].turn = state
		}
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

func (s *responsesWSSession) completeFinish(state *responsesWSCallState, rememberCreate bool) {
	state.dataMu.Lock()
	responseID := state.responseID
	state.dataMu.Unlock()
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	if s.current == state {
		s.current = nil
		if rememberCreate && state.eventID != "" {
			s.completedCreates = append(s.completedCreates, responsesWSCompletedCreate{eventID: state.eventID, streamID: state.StreamID})
			identityBytes := 0
			for _, previous := range s.completedCreates {
				identityBytes += previous.identityBytes()
			}
			for len(s.completedCreates) > responsesWSMaxPendingControls || identityBytes > responsesWSMaxControlIdentityBytes {
				identityBytes -= s.completedCreates[0].identityBytes()
				s.completedCreates[0] = responsesWSCompletedCreate{}
				s.completedCreates = s.completedCreates[1:]
			}
		}
		if responseID != "" {
			s.finishedResponseIDs = append(s.finishedResponseIDs, responseID)
			identityBytes := 0
			for _, finishedResponseID := range s.finishedResponseIDs {
				identityBytes += len(finishedResponseID)
			}
			for len(s.finishedResponseIDs) > responsesWSMaxPendingControls || identityBytes > responsesWSMaxControlIdentityBytes {
				identityBytes -= len(s.finishedResponseIDs[0])
				s.finishedResponseIDs[0] = ""
				s.finishedResponseIDs = s.finishedResponseIDs[1:]
			}
		}
		retained := s.pendingControls[:0]
		for _, control := range s.pendingControls {
			if control.turn == state {
				s.rememberResolvedControlLocked(control)
			} else {
				retained = append(retained, control)
			}
		}
		for i := len(retained); i < len(s.pendingControls); i++ {
			s.pendingControls[i] = responsesWSControl{}
		}
		s.pendingControls = retained
	}
}

func (s *responsesWSSession) getCurrent() *responsesWSCallState {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	return s.current
}

func (s *responsesWSSession) failCurrent() {
	if state := s.getCurrent(); state != nil {
		s.finishCall(state, false, false)
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
	s.targetReadMu.Lock()
	defer s.targetReadMu.Unlock()
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

func (s *responsesWSSession) sendError(eventID, streamID string, apiErr *types.NewAPIError) {
	if apiErr == nil {
		return
	}
	payload, err := buildResponsesWSErrorPayload(eventID, streamID, apiErr)
	if err == nil {
		_ = s.writeClient(websocket.TextMessage, payload)
	}
}

func buildResponsesWSErrorPayload(eventID, streamID string, apiErr *types.NewAPIError) ([]byte, error) {
	if apiErr == nil {
		return nil, errors.New("api error is nil")
	}
	status := apiErr.StatusCode
	if status == 0 {
		status = http.StatusInternalServerError
	}
	openaiErr := apiErr.ToOpenAIError()
	return common.Marshal(&responsesWSErrorEvent{Type: "error", Status: status, EventID: eventID, StreamID: streamID, Error: &openaiErr})
}

func (s *responsesWSSession) closeTarget() {
	s.targetReadMu.Lock()
	defer s.targetReadMu.Unlock()
	s.closeTargetLocked()
}

// The caller holds targetReadMu, excluding forwarding by the retired reader.
func (s *responsesWSSession) closeTargetLocked() {
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

func (s *responsesWSSession) registerChannelClose(channelID int) bool {
	unregister, accepted := wsmanager.Register(channelID, wsmanager.KindResponses, func(code int, reason string) {
		s.closeWithCode(code, reason)
	})
	if !accepted {
		return false
	}

	s.targetWriteMu.Lock()
	if s.closed.Load() {
		s.targetWriteMu.Unlock()
		unregister()
		return false
	}
	previousUnregister := s.unregister
	s.unregister = unregister
	s.targetWriteMu.Unlock()
	if previousUnregister != nil {
		previousUnregister()
	}
	return true
}

func (s *responsesWSSession) closeWithCode(code int, reason string) {
	s.closeOnce.Do(func() {
		s.closed.Store(true)
		s.failCurrent()
		deadline := time.Now().Add(time.Second)
		closeMessage := websocket.FormatCloseMessage(code, reason)
		_ = s.client.WriteControl(websocket.CloseMessage, closeMessage, deadline)
		if target := s.getTarget(); target != nil {
			_ = target.WriteControl(websocket.CloseMessage, closeMessage, deadline)
		}
		_ = s.client.Close()
		s.closeTarget()
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
		if apiErr := responsesWSChannelEligibility(channel, c.Request.URL.Path, modelName); apiErr != nil {
			return nil, apiErr
		}
		if apiErr := setupResponsesWSChannelContext(c, channel, modelName); apiErr != nil {
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
			if err == nil && preferred != nil && preferred.Status == common.ChannelStatusEnabled && responsesWSChannelEligibility(preferred, c.Request.URL.Path, modelName) == nil {
				if usingGroup == "auto" {
					userGroup := common.GetContextKeyString(c, appconstant.ContextKeyUserGroup)
					for _, group := range service.GetUserAutoGroup(userGroup) {
						if appmodel.IsChannelEnabledForGroupModel(group, modelName, preferred.Id) {
							common.SetContextKey(c, appconstant.ContextKeyAutoGroup, group)
							service.MarkChannelAffinityUsed(c, group, preferred.Id)
							if apiErr := setupResponsesWSChannelContext(c, preferred, modelName); apiErr != nil {
								return nil, apiErr
							}
							return preferred, nil
						}
					}
				} else if appmodel.IsChannelEnabledForGroupModel(usingGroup, modelName, preferred.Id) {
					service.MarkChannelAffinityUsed(c, usingGroup, preferred.Id)
					if apiErr := setupResponsesWSChannelContext(c, preferred, modelName); apiErr != nil {
						return nil, apiErr
					}
					return preferred, nil
				}
			}
		}
	}

	var eligibilityErr *types.NewAPIError
	for {
		// Capability filtering is local selection, not an upstream retry. Keep
		// existing priority, auto-group and public-relay ordering semantics.
		restoreRetry := retryParam.PreserveRetryState()
		groupIndex := common.GetContextKeyInt(c, appconstant.ContextKeyAutoGroupIndex)
		groupRetryIndex := common.GetContextKeyInt(c, appconstant.ContextKeyAutoGroupRetryIndex)
		channel, selectGroup, err := service.CacheGetRandomSatisfiedChannel(retryParam)
		if err != nil {
			return nil, types.NewError(fmt.Errorf("获取分组 %s 下模型 %s 的可用渠道失败（retry）: %s", selectGroup, modelName, err.Error()), types.ErrorCodeGetChannelFailed, types.ErrOptionWithSkipRetry())
		}
		if channel == nil {
			if eligibilityErr != nil {
				return nil, eligibilityErr
			}
			return nil, types.NewErrorWithStatusCode(errors.New("no eligible responses websocket channel is available"), types.ErrorCode("responses_websocket_unsupported"), http.StatusBadRequest, types.ErrOptionWithSkipRetry())
		}
		if apiErr := responsesWSChannelEligibility(channel, c.Request.URL.Path, modelName); apiErr != nil {
			eligibilityErr = apiErr
			if channel.Id <= 0 {
				return nil, apiErr
			}
			retryParam.ExcludeChannel(channel.Id)
			restoreRetry()
			common.SetContextKey(c, appconstant.ContextKeyAutoGroupIndex, groupIndex)
			common.SetContextKey(c, appconstant.ContextKeyAutoGroupRetryIndex, groupRetryIndex)
			continue
		}
		if apiErr := setupResponsesWSChannelContext(c, channel, modelName); apiErr != nil {
			return nil, apiErr
		}
		return channel, nil
	}
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
	requestPath := "/v1/responses"
	if c.Request != nil && c.Request.URL != nil {
		requestPath = c.Request.URL.Path
	}
	if apiErr := responsesWSChannelEligibility(channel, requestPath, modelName); apiErr != nil {
		return nil, apiErr
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
	if apiErr := setupResponsesWSChannelContext(c, channel, modelName); apiErr != nil {
		return nil, apiErr
	}
	return channel, nil
}

func addResponsesWSUsedChannel(c *gin.Context, channelID int) {
	usedChannels := c.GetStringSlice("use_channel")
	usedChannels = append(usedChannels, strconv.Itoa(channelID))
	c.Set("use_channel", usedChannels)
}
