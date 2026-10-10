package relay

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResponsesWSCreateStreamIDIsTransportMetadata(t *testing.T) {
	for _, message := range []string{
		`{"type":"response.create","event_id":"evt_create","stream_id":"stream_outer","model":"gpt-5","input":"hello","generate":false}`,
		`{"type":"response.create","event_id":"evt_create","stream_id":"stream_outer","response":{"model":"gpt-5","input":"hello","stream_id":"stream_inner","generate":false}}`,
	} {
		t.Run(message, func(t *testing.T) {
			create, eventID, err := normalizeResponsesWSCreateEvent([]byte(message))
			require.NoError(t, err)
			assert.Equal(t, "evt_create", eventID)
			assert.Equal(t, "stream_outer", create.StreamID)
			assert.Equal(t, "gpt-5", create.Request.Model)
			assert.JSONEq(t, `false`, string(create.Generate))

			httpBody, err := common.Marshal(create.Request)
			require.NoError(t, err)
			assert.NotContains(t, responsesWSCorrelationObject(t, httpBody), "stream_id")
			assert.NotContains(t, responsesWSCorrelationObject(t, httpBody), "event_id")
		})
	}

	var httpRequest dto.OpenAIResponsesRequest
	require.NoError(t, common.Unmarshal([]byte(`{"model":"gpt-5","stream_id":"http_client"}`), &httpRequest))
	body, err := common.Marshal(httpRequest)
	require.NoError(t, err)
	assert.NotContains(t, responsesWSCorrelationObject(t, body), "stream_id")
}

func TestResponsesWSMetadataRetainsValidStreamIDOnMalformedFields(t *testing.T) {
	for _, message := range []string{
		`{"type":42,"event_id":"evt_bad","stream_id":"stream_bad"}`,
		`{"type":"response.create","event_id":42,"stream_id":"stream_bad"}`,
		`{"event_id":"evt_bad","stream_id":"stream_bad"}`,
	} {
		t.Run(message, func(t *testing.T) {
			_, eventID, streamID, err := responsesWSEventMetadata([]byte(message))
			require.Error(t, err)
			assert.Equal(t, "stream_bad", streamID)
			payload, payloadErr := buildResponsesWSErrorPayload(eventID, streamID, newResponsesWSInvalidRequestError(err))
			require.NoError(t, payloadErr)
			assert.Equal(t, "stream_bad", responsesWSCorrelationObject(t, payload)["stream_id"])
		})
	}
}

func TestResponsesWSCreateEventSanitizesOverrideStreamID(t *testing.T) {
	payload := []byte(`{"model":"gpt-5","input":"hello","stream_id":"override","event_id":"override_event","stream":true,"stream_options":{},"background":true}`)
	for _, streamID := range []string{"stream_client", ""} {
		t.Run(streamID, func(t *testing.T) {
			got, err := buildResponsesWSCreateEvent(payload, nil, streamID)
			require.NoError(t, err)
			data := responsesWSCorrelationObject(t, got)
			assert.Equal(t, "response.create", data["type"])
			for _, field := range []string{"event_id", "stream", "stream_options", "background"} {
				assert.NotContains(t, data, field)
			}
			if streamID == "" {
				assert.NotContains(t, data, "stream_id")
			} else {
				assert.Equal(t, streamID, data["stream_id"])
			}
		})
	}
}

func TestResponsesWSErrorPayloadUsesRequestIdentity(t *testing.T) {
	apiErr := types.NewErrorWithStatusCode(errors.New("bad request"), types.ErrorCodeInvalidRequest, http.StatusBadRequest)
	payload, err := buildResponsesWSErrorPayload("evt_rejected", "stream_rejected", apiErr)
	require.NoError(t, err)
	data := responsesWSCorrelationObject(t, payload)
	assert.Equal(t, "error", data["type"])
	assert.Equal(t, "evt_rejected", data["event_id"])
	assert.Equal(t, "stream_rejected", data["stream_id"])
	assert.Equal(t, float64(http.StatusBadRequest), data["status"])

	payload, err = buildResponsesWSErrorPayload("", "", apiErr)
	require.NoError(t, err)
	data = responsesWSCorrelationObject(t, payload)
	assert.NotContains(t, data, "event_id")
	assert.NotContains(t, data, "stream_id")
}

func TestResponsesWSSequentialTurnsDoNotLeakStreamID(t *testing.T) {
	session := &responsesWSSession{}
	for _, streamID := range []string{"stream_first", "stream_second", ""} {
		t.Run(streamID, func(t *testing.T) {
			state := responsesWSCorrelationState(streamID)
			require.True(t, session.tryReserveCurrent(state))
			delta := session.processUpstreamMessage([]byte(`{"type":"response.output_text.delta","delta":""}`))
			terminal := session.processUpstreamMessage([]byte(`{"type":"response.cancelled"}`))
			for _, message := range [][]byte{delta, terminal} {
				data := responsesWSCorrelationObject(t, message)
				if streamID == "" {
					assert.NotContains(t, data, "stream_id")
				} else {
					assert.Equal(t, streamID, data["stream_id"])
				}
			}
			assert.Nil(t, session.getCurrent())
		})
	}
	message := []byte(`{"type":"response.created"}`)
	assert.Equal(t, message, session.processUpstreamMessage(message))
}

func TestResponsesWSLateTerminalsDoNotAcquireANewerTurnIdentity(t *testing.T) {
	session := &responsesWSSession{}
	for i := 0; i < 2; i++ {
		state := responsesWSCorrelationState(fmt.Sprintf("stream_%d", i))
		require.True(t, session.tryReserveCurrent(state))
		session.processUpstreamMessage([]byte(fmt.Sprintf(`{"type":"response.created","response":{"id":"response_%d"}}`, i)))
		session.processUpstreamMessage([]byte(fmt.Sprintf(`{"type":"response.cancelled","response":{"id":"response_%d"}}`, i)))
	}
	current := responsesWSCorrelationState("stream_current")
	require.True(t, session.tryReserveCurrent(current))
	created := session.processUpstreamMessage([]byte(`{"type":"response.created","response_id":"","response":{"id":"response_current"}}`))
	assert.Equal(t, "stream_current", responsesWSCorrelationObject(t, created)["stream_id"])
	assert.Equal(t, "response_current", current.responseID)
	for _, message := range []string{
		`{"type":"response.failed","response":{"id":"response_0"}}`,
		`{"type":"error","response_id":"response_1","error":{"message":"late"}}`,
		`{"type":"response.failed","response_id":"","response":{"id":"response_0"}}`,
		`{"type":"response.failed","response_id":"","response":{"id":"response_foreign"}}`,
	} {
		assert.Equal(t, []byte(message), session.processUpstreamMessage([]byte(message)))
		assert.Same(t, current, session.getCurrent())
	}
	terminal := session.processUpstreamMessage([]byte(`{"type":"response.cancelled","response_id":"","response":{"id":"response_current"}}`))
	assert.Equal(t, "stream_current", responsesWSCorrelationObject(t, terminal)["stream_id"])
	assert.Nil(t, session.getCurrent())
}

func TestResponsesWSCompletedTurnsRetirePendingControls(t *testing.T) {
	target, peer := responsesWSTestPair(t)
	session := &responsesWSSession{target: target}
	for i := 0; i < 40; i++ {
		streamID := fmt.Sprintf("stream_%d", i)
		responsesWSCorrelationForwardControl(t, session, peer, fmt.Sprintf("idle_control_%d", i), streamID)
		state := responsesWSCorrelationState(streamID)
		require.True(t, session.tryReserveCurrent(state))
		responsesWSCorrelationForwardControl(t, session, peer, fmt.Sprintf("control_%d", i), streamID)
		session.processUpstreamMessage([]byte(`{"type":"response.cancelled"}`))
		assert.Empty(t, session.pendingControls)
	}
	assert.Len(t, session.resolvedControls, responsesWSMaxPendingControls)
	current := responsesWSCorrelationState("stream_current")
	require.True(t, session.tryReserveCurrent(current))
	message := session.processUpstreamMessage([]byte(`{"type":"error","event_id":"control_39","error":{"message":"late control"}}`))
	assert.Equal(t, "stream_39", responsesWSCorrelationObject(t, message)["stream_id"])
	assert.Same(t, current, session.getCurrent())
}

func TestResponsesWSCancelTargetAloneNeedsAKnownActiveResponse(t *testing.T) {
	target, peer := responsesWSTestPair(t)
	state := responsesWSCorrelationState("stream_active")
	session := &responsesWSSession{target: target, current: state}
	message := []byte(`{"type":"response.cancel","event_id":"cancel","stream_id":"stream_active","response_id":"response_target"}`)
	require.Nil(t, session.handleResponseCancel(websocket.TextMessage, message))
	require.NoError(t, peer.SetReadDeadline(time.Now().Add(time.Second)))
	_, _, err := peer.ReadMessage()
	require.NoError(t, err)
	upstream := []byte(`{"type":"error","response_id":"response_target","error":{"type":"server_error","message":"active start failed"}}`)
	got := session.processUpstreamMessage(upstream)
	assert.Equal(t, "stream_active", responsesWSCorrelationObject(t, got)["stream_id"])
	assert.Nil(t, session.getCurrent(), "an unseen active response must not be mistaken for a foreign cancel target")
}

func TestResponsesWSOmittedClientIdentityStillObservesProviderIdentity(t *testing.T) {
	state := responsesWSCorrelationState("")
	session := &responsesWSSession{current: state}
	message := []byte(`{"type":"response.cancelled","stream_id":"provider_identity"}`)
	assert.Equal(t, message, session.processUpstreamMessage(message))
	assert.Nil(t, session.getCurrent())
}

func TestResponsesWSGatewayErrorsCarryEnvelopeIdentityOnTheWire(t *testing.T) {
	done := make(chan struct{})
	engine := gin.New()
	engine.GET("/v1/responses", func(c *gin.Context) {
		defer close(done)
		client, err := (&websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}).Upgrade(c.Writer, c.Request, nil)
		if err != nil {
			return
		}
		defer client.Close()
		_ = ResponsesWebSocketHelper(c, client)
	})
	server := httptest.NewServer(engine)
	t.Cleanup(server.Close)
	client, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/v1/responses", nil)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = client.Close()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("gateway did not exit")
		}
	})
	for _, envelope := range []struct{ message, streamID string }{
		{`{"type":"response.create","stream_id":"planner"}`, "planner"},
		{`{"type":42,"stream_id":"malformed"}`, "malformed"},
		{`{"type":"response.create"}`, ""},
	} {
		require.NoError(t, client.WriteMessage(websocket.TextMessage, []byte(envelope.message)))
		require.NoError(t, client.SetReadDeadline(time.Now().Add(time.Second)))
		_, message, readErr := client.ReadMessage()
		require.NoError(t, readErr)
		event := responsesWSCorrelationObject(t, message)
		assert.Equal(t, "error", event["type"])
		if envelope.streamID == "" {
			assert.NotContains(t, event, "stream_id")
		} else {
			assert.Equal(t, envelope.streamID, event["stream_id"])
		}
	}
}

func TestResponsesWSProviderStreamIdentityIsPreservedAndIsolatesObservation(t *testing.T) {
	state := responsesWSCorrelationState("stream_active")
	session := &responsesWSSession{current: state}
	foreignMessages := []string{
		`{"type":"response.output_text.delta","stream_id":"stream_foreign","delta":"must not be billed"}`,
		`{"type":"response.failed","stream_id":"stream_foreign","response":{"usage":{"input_tokens":9,"output_tokens":8,"total_tokens":17}}}`,
		`{"type":"error","stream_id":"stream_foreign","error":{"message":"foreign error"}}`,
	}
	for _, message := range foreignMessages {
		assert.Equal(t, []byte(message), session.processUpstreamMessage([]byte(message)))
		assert.Same(t, state, session.getCurrent())
	}
	assert.Empty(t, state.outputText.String())
	assert.Equal(t, 0, state.usage.TotalTokens)

	for _, explicit := range []string{`""`, `null`} {
		message := []byte(`{"type":"response.created","stream_id":` + explicit + `}`)
		assert.Equal(t, message, session.processUpstreamMessage(message), "an explicit provider field must not be replaced")
	}
	message := []byte(`{"type":"session.updated","stream_id":"provider_session"}`)
	assert.Equal(t, message, session.processUpstreamMessage(message))
	assert.Same(t, state, session.getCurrent())

	terminal := []byte(`{"type":"response.cancelled","stream_id":"stream_active"}`)
	assert.Equal(t, terminal, session.processUpstreamMessage(terminal))
	assert.Nil(t, session.getCurrent())
}

func TestResponsesWSRejectedOverlapKeepsActiveIdentity(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	state := responsesWSCorrelationState("stream_active")
	session := &responsesWSSession{
		c:              c,
		current:        state,
		revalidateAuth: func(*gin.Context) *types.NewAPIError { return nil },
	}
	create, eventID, err := normalizeResponsesWSCreateEvent([]byte(`{"type":"response.create","event_id":"evt_overlap","stream_id":"stream_overlap","model":"gpt-5"}`))
	require.NoError(t, err)
	apiErr := session.handleResponseCreate(create)
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusConflict, apiErr.StatusCode)
	payload, err := buildResponsesWSErrorPayload(eventID, create.StreamID, apiErr)
	require.NoError(t, err)
	assert.Equal(t, "stream_overlap", responsesWSCorrelationObject(t, payload)["stream_id"])
	assert.Same(t, state, session.getCurrent())
	assert.Equal(t, "stream_active", state.StreamID)
}

func TestResponsesWSMismatchedCancelDoesNotForwardOrSettle(t *testing.T) {
	target, peer := responsesWSTestPair(t)
	state := responsesWSCorrelationState("stream_active")
	session := &responsesWSSession{target: target, current: state}
	apiErr := session.handleResponseCancel(websocket.TextMessage, []byte(`{"type":"response.cancel","event_id":"evt_cancel","stream_id":"stream_other"}`))
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusBadRequest, apiErr.StatusCode)
	assert.Same(t, state, session.getCurrent())
	require.NoError(t, peer.SetReadDeadline(time.Now().Add(50*time.Millisecond)))
	_, _, err := peer.ReadMessage()
	require.Error(t, err, "mismatched cancel must not be sent to the provider")
}

func TestResponsesWSAsyncControlErrorsUseCallerIdentityWithoutSettling(t *testing.T) {
	for _, nestedEventID := range []bool{false, true} {
		t.Run(map[bool]string{false: "top event_id", true: "nested event_id"}[nestedEventID], func(t *testing.T) {
			target, peer := responsesWSTestPair(t)
			state := responsesWSCorrelationState("stream_active")
			session := &responsesWSSession{target: target, current: state}
			message := []byte(`{"type":"session.update","event_id":"evt_control","stream_id":"stream_control"}`)
			require.Nil(t, session.forwardControl(websocket.TextMessage, message, "evt_control", "stream_control"))
			require.NoError(t, peer.SetReadDeadline(time.Now().Add(time.Second)))
			_, forwarded, err := peer.ReadMessage()
			require.NoError(t, err)
			assert.Equal(t, message, forwarded)

			upstream := `{"type":"error","event_id":"evt_control","error":{"message":"rejected control"}}`
			if nestedEventID {
				upstream = `{"type":"error","error":{"event_id":"evt_control","message":"rejected control"}}`
			}
			got := responsesWSCorrelationObject(t, session.processUpstreamMessage([]byte(upstream)))
			assert.Equal(t, "stream_control", got["stream_id"])
			assert.Same(t, state, session.getCurrent())
			assert.Empty(t, state.outputText.String())
		})
	}
}

func TestResponsesWSAsyncCancelErrorDoesNotSettleActiveResponse(t *testing.T) {
	target, peer := responsesWSTestPair(t)
	state := responsesWSCorrelationState("stream_active")
	session := &responsesWSSession{target: target, current: state}
	message := []byte(`{"type":"response.cancel","event_id":"evt_cancel","stream_id":"stream_active"}`)
	require.Nil(t, session.handleResponseCancel(websocket.TextMessage, message))
	require.NoError(t, peer.SetReadDeadline(time.Now().Add(time.Second)))
	_, forwarded, err := peer.ReadMessage()
	require.NoError(t, err)
	assert.Equal(t, message, forwarded)

	got := responsesWSCorrelationObject(t, session.processUpstreamMessage([]byte(`{"type":"error","error":{"event_id":"evt_cancel","message":"cancel rejected"}}`)))
	assert.Equal(t, "stream_active", got["stream_id"])
	assert.Same(t, state, session.getCurrent())
	session.processUpstreamMessage([]byte(`{"type":"response.cancelled"}`))
	assert.Nil(t, session.getCurrent())
}

func TestResponsesWSPendingControlsHaveBoundedIdentities(t *testing.T) {
	t.Run("pending count", func(t *testing.T) {
		target, peer := responsesWSTestPair(t)
		state := responsesWSCorrelationState("stream_active")
		session := &responsesWSSession{target: target, current: state}
		for i := 0; i < responsesWSMaxPendingControls; i++ {
			eventID := fmt.Sprintf("evt_control_%d", i)
			responsesWSCorrelationForwardControl(t, session, peer, eventID, "stream_control")
		}
		message := []byte(`{"type":"session.update","event_id":"evt_overflow","stream_id":"stream_overflow"}`)
		apiErr := session.forwardControl(websocket.TextMessage, message, "evt_overflow", "stream_overflow")
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusBadRequest, apiErr.StatusCode)
		assert.Same(t, state, session.getCurrent())
		require.NoError(t, peer.SetReadDeadline(time.Now().Add(50*time.Millisecond)))
		_, _, err := peer.ReadMessage()
		require.Error(t, err, "a rejected over-capacity control must not reach the provider")
	})
	t.Run("identity bytes", func(t *testing.T) {
		target, peer := responsesWSTestPair(t)
		state := responsesWSCorrelationState("stream_active")
		session := &responsesWSSession{target: target, current: state}
		eventID := strings.Repeat("x", responsesWSMaxControlIdentityBytes+1)
		message, err := common.Marshal(map[string]string{"type": "session.update", "event_id": eventID})
		require.NoError(t, err)
		apiErr := session.forwardControl(websocket.TextMessage, message, eventID, "")
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusBadRequest, apiErr.StatusCode)
		assert.Same(t, state, session.getCurrent())
		require.NoError(t, peer.SetReadDeadline(time.Now().Add(50*time.Millisecond)))
		_, _, err = peer.ReadMessage()
		require.Error(t, err, "an oversized control identity must not reach the provider")
	})
}

func TestResponsesWSResolvedControlErrorsStayWithOriginalTurn(t *testing.T) {
	target, peer := responsesWSTestPair(t)
	first := responsesWSCorrelationState("stream_first")
	session := &responsesWSSession{target: target, current: first}
	responsesWSCorrelationForwardControl(t, session, peer, "evt_control", "stream_first")
	controlError := []byte(`{"type":"error","event_id":"evt_control","error":{"message":"rejected control"}}`)
	got := responsesWSCorrelationObject(t, session.processUpstreamMessage(controlError))
	assert.Equal(t, "stream_first", got["stream_id"])
	assert.Same(t, first, session.getCurrent())
	session.processUpstreamMessage([]byte(`{"type":"response.cancelled"}`))
	require.Nil(t, session.getCurrent())
	second := responsesWSCorrelationState("stream_second")
	require.True(t, session.tryReserveCurrent(second))
	got = responsesWSCorrelationObject(t, session.processUpstreamMessage(controlError))
	assert.Equal(t, "stream_first", got["stream_id"])
	assert.Same(t, second, session.getCurrent(), "a duplicate old control error must not settle a later turn")

	message := []byte(`{"type":"session.update","event_id":"evt_control","stream_id":"stream_second"}`)
	apiErr := session.forwardControl(websocket.TextMessage, message, "evt_control", "stream_second")
	require.NotNil(t, apiErr, "a resolved client identity cannot be reused while its late-error tombstone is retained")
	assert.Same(t, second, session.getCurrent())
}

func TestResponsesWSAmbiguousControlErrorsDoNotClaimActiveStream(t *testing.T) {
	target, peer := responsesWSTestPair(t)
	state := responsesWSCorrelationState("stream_active")
	session := &responsesWSSession{target: target, current: state}
	responsesWSCorrelationForwardControl(t, session, peer, "evt_control_a", "stream_control_a")
	responsesWSCorrelationForwardControl(t, session, peer, "evt_control_b", "stream_control_b")
	for _, message := range []string{
		`{"type":"error","event_id":"evt_control_a","error":{"event_id":"evt_control_b","message":"conflicting references"}}`,
		`{"type":"error","error":{"event_id":"evt_unknown_control","message":"unknown reference"}}`,
	} {
		assert.Equal(t, []byte(message), session.processUpstreamMessage([]byte(message)))
		assert.Same(t, state, session.getCurrent())
	}
}

func TestResponsesWSCreateErrorReferenceFinishesTheActiveTurn(t *testing.T) {
	for _, nested := range []bool{false, true} {
		t.Run(fmt.Sprint(nested), func(t *testing.T) {
			state := responsesWSCorrelationState("stream_create")
			state.eventID = "evt_create"
			commits := []bool{}
			state.commitRate = func(success bool) { commits = append(commits, success) }
			session := &responsesWSSession{current: state}
			// A pending cancel must not steal a rejection naming response.create.
			session.pendingControls = []responsesWSControl{{eventID: "evt_cancel", isCancel: true, turn: state}}
			upstream := `{"type":"error","event_id":"evt_create","error":{"type":"invalid_request_error","code":"response_not_active"}}`
			if nested {
				upstream = `{"type":"error","event_id":"provider_error","error":{"event_id":"evt_create","type":"invalid_request_error","code":"response_not_active"}}`
			}
			got := responsesWSCorrelationObject(t, session.processUpstreamMessage([]byte(upstream)))
			assert.Equal(t, "stream_create", got["stream_id"])
			assert.Nil(t, session.getCurrent(), "a rejected create must release the active turn")
			assert.Equal(t, []bool{false}, commits, "the rejected create releases its success reservation once")
			next := responsesWSCorrelationState("stream_next")
			next.eventID = "evt_next"
			require.True(t, session.tryReserveCurrent(next), "the next create must not be blocked by a rejected turn")
			late := []byte(`{"type":"error","error":{"event_id":"evt_create","message":"late create error"}}`)
			assert.Equal(t, late, session.processUpstreamMessage(late))
			assert.Same(t, next, session.getCurrent())
			assert.Equal(t, []bool{false}, commits)
		})
	}
}

func TestResponsesWSNestedErrorReferenceCannotConsumeAnotherControl(t *testing.T) {
	for _, reference := range []string{"evt_unknown", "evt_create"} {
		t.Run(reference, func(t *testing.T) {
			state := responsesWSCorrelationState("stream_create")
			state.eventID = "evt_create"
			control := responsesWSControl{eventID: "evt_control", streamID: "stream_control", turn: state}
			session := &responsesWSSession{current: state, pendingControls: []responsesWSControl{control}}
			message := []byte(fmt.Sprintf(`{"type":"error","event_id":"evt_control","error":{"event_id":%q,"message":"conflicting reference"}}`, reference))
			assert.Equal(t, message, session.processUpstreamMessage(message))
			assert.Same(t, state, session.getCurrent())
			assert.Equal(t, []responsesWSControl{control}, session.pendingControls, "conflicting references cannot retire a different control")
			assert.Empty(t, session.resolvedControls)
		})
	}

	state := responsesWSCorrelationState("stream_create")
	state.eventID = "evt_create"
	control := responsesWSControl{eventID: "evt_control", streamID: "stream_control", turn: state}
	session := &responsesWSSession{current: state, pendingControls: []responsesWSControl{control}}
	message := []byte(`{"type":"error","event_id":"evt_create","error":{"event_id":"evt_control","message":"conflicting reference"}}`)
	assert.Equal(t, message, session.processUpstreamMessage(message))
	assert.Same(t, state, session.getCurrent())
	assert.Equal(t, []responsesWSControl{control}, session.pendingControls)
}

func TestResponsesWSCompletedCreateReferencesCannotSettleANewerTurn(t *testing.T) {
	for _, streamID := range []string{"stream_current", "stream_previous"} {
		t.Run(streamID, func(t *testing.T) {
			session := &responsesWSSession{}
			previous := responsesWSCorrelationState("stream_previous")
			previous.eventID = "evt_previous"
			require.True(t, session.tryReserveCurrent(previous))
			session.processUpstreamMessage([]byte(`{"type":"response.cancelled"}`))
			current := responsesWSCorrelationState(streamID)
			current.eventID = "evt_current"
			require.True(t, session.tryReserveCurrent(current))
			for _, message := range []string{
				`{"type":"error","event_id":"evt_previous","error":{"message":"late create error"}}`,
				`{"type":"error","event_id":"evt_previous","stream_id":"stream_previous","error":{"message":"late create error"}}`,
				`{"type":"error","stream_id":"` + streamID + `","error":{"event_id":"evt_previous","message":"late create error"}}`,
			} {
				assert.Equal(t, []byte(message), session.processUpstreamMessage([]byte(message)))
				assert.Same(t, current, session.getCurrent())
			}
			message := session.processUpstreamMessage([]byte(`{"type":"error","event_id":"evt_current","error":{"message":"current create failed"}}`))
			assert.Equal(t, streamID, responsesWSCorrelationObject(t, message)["stream_id"])
			assert.Nil(t, session.getCurrent())
		})
	}
}

func TestResponsesWSCurrentIdentityOverridesACompletedCreateOutputID(t *testing.T) {
	for _, reference := range []string{"stream", "response", "nested response"} {
		t.Run(reference, func(t *testing.T) {
			current := responsesWSCorrelationState("stream_current")
			current.eventID = "evt_current"
			current.responseID = "response_current"
			previousStreamID := "stream_previous"
			if reference != "stream" {
				previousStreamID = current.StreamID
			}
			session := &responsesWSSession{
				current:          current,
				completedCreates: []responsesWSCompletedCreate{{eventID: "evt_previous", streamID: previousStreamID}},
				pendingControls:  []responsesWSControl{{eventID: "evt_cancel", isCancel: true, turn: current}},
			}
			fields := `"stream_id":"stream_current"`
			if reference == "response" {
				fields = `"response_id":"response_current"`
			} else if reference == "nested response" {
				fields = `"response_id":"","response":{"id":"response_current"}`
			}
			message := []byte(`{"type":"error","event_id":"evt_previous",` + fields + `,"error":{"type":"invalid_request_error","code":"response_not_found"}}`)
			got := session.processUpstreamMessage(message)
			assert.Equal(t, "stream_current", responsesWSCorrelationObject(t, got)["stream_id"])
			assert.Nil(t, session.getCurrent(), "a known current identity must override a coinciding provider output ID")
		})
	}
}

func TestResponsesWSCompletedCreateHistoryBoundsAndIdentityReuse(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	target, peer := responsesWSTestPair(t)
	session := &responsesWSSession{c: c, target: target, revalidateAuth: func(*gin.Context) *types.NewAPIError { return nil }}
	for i := 0; i <= responsesWSMaxPendingControls; i++ {
		state := responsesWSCorrelationState("stream_history")
		state.eventID = fmt.Sprintf("evt_%d", i)
		require.True(t, session.tryReserveCurrent(state))
		session.processUpstreamMessage([]byte(`{"type":"response.cancelled"}`))
	}
	require.Len(t, session.completedCreates, responsesWSMaxPendingControls)
	assert.Equal(t, "evt_1", session.completedCreates[0].eventID)
	create := responsesWSCreateRequest{EventID: "evt_1", StreamID: "stream_next", Request: dto.OpenAIResponsesRequest{Model: "gpt-5"}}
	apiErr := session.handleResponseCreate(create)
	require.NotNil(t, apiErr)
	assert.Contains(t, apiErr.Error(), "retained")
	apiErr = session.forwardControl(websocket.TextMessage, []byte(`{"type":"session.update","event_id":"evt_1"}`), "evt_1", "")
	require.NotNil(t, apiErr)
	assert.Empty(t, session.pendingControls)
	// Evicted IDs can be reused, while callers must avoid delayed references
	// beyond the bounded correlation window.
	responsesWSCorrelationForwardControl(t, session, peer, "evt_0", "stream_next")

	for i := 0; i < 3; i++ {
		state := responsesWSCorrelationState("stream_history")
		state.eventID = strings.Repeat("x", responsesWSMaxControlIdentityBytes/2) + fmt.Sprint(i)
		require.True(t, session.tryReserveCurrent(state))
		session.processUpstreamMessage([]byte(`{"type":"response.cancelled"}`))
	}
	require.Len(t, session.completedCreates, 1, "the history also evicts identities exceeding the aggregate byte limit")
	create.EventID = strings.Repeat("x", responsesWSMaxControlIdentityBytes)
	create.StreamID = "x"
	apiErr = session.handleResponseCreate(create)
	require.NotNil(t, apiErr)
	assert.Contains(t, apiErr.Error(), "identity limit")
	assert.Nil(t, session.getCurrent())
}

func TestResponsesWSMalformedProviderStreamIDDoesNotSettle(t *testing.T) {
	for _, explicit := range []string{`42`, `{}`, `[]`} {
		t.Run(explicit, func(t *testing.T) {
			state := responsesWSCorrelationState("stream_active")
			session := &responsesWSSession{current: state}
			message := []byte(`{"type":"response.failed","stream_id":` + explicit + `}`)
			assert.Equal(t, message, session.processUpstreamMessage(message))
			assert.Same(t, state, session.getCurrent())
		})
	}
}

func TestResponsesWSTerminalAccountingSeparatesBillingAndSuccessRateSlots(t *testing.T) {
	previousPost := postResponsesWSConsumeQuota
	t.Cleanup(func() { postResponsesWSConsumeQuota = previousPost })
	for _, terminalType := range []string{"response.completed", "response.done", "response.incomplete", "response.failed", "response.cancelled", "response.canceled", "response.error", "error"} {
		t.Run(terminalType, func(t *testing.T) {
			posted := 0
			postResponsesWSConsumeQuota = func(_ *gin.Context, _ *relaycommon.RelayInfo, usage *dto.Usage, _ []string) {
				posted++
				assert.Equal(t, 7, usage.PromptTokens)
				assert.Equal(t, 3, usage.CompletionTokens)
			}
			commits := []bool{}
			state := responsesWSCorrelationState("stream_billable")
			state.commitRate = func(success bool) { commits = append(commits, success) }
			session := &responsesWSSession{current: state}
			message := []byte(`{"type":"` + terminalType + `","response":{"usage":{"input_tokens":7,"output_tokens":3,"total_tokens":10}}}`)
			session.processUpstreamMessage(message)
			session.processUpstreamMessage(message)
			assert.Nil(t, session.getCurrent())
			assert.Equal(t, 1, posted, "usage must be billed exactly once")
			requestSucceeded := terminalType == "response.completed" || terminalType == "response.done"
			assert.Equal(t, []bool{requestSucceeded}, commits, "only a successful turn consumes a successful-request rate slot")
		})
	}
}

func TestResponsesWSCreateRejectsRetainedControlEventID(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	for _, resolved := range []bool{false, true} {
		t.Run(fmt.Sprint(resolved), func(t *testing.T) {
			session := &responsesWSSession{c: c, revalidateAuth: func(*gin.Context) *types.NewAPIError { return nil }}
			control := responsesWSControl{eventID: "shared_event", streamID: "old_control"}
			if resolved {
				target, peer := responsesWSTestPair(t)
				session.target = target
				oldTurn := responsesWSCorrelationState("old_turn")
				require.True(t, session.tryReserveCurrent(oldTurn))
				responsesWSCorrelationForwardControl(t, session, peer, control.eventID, control.streamID)
				session.processUpstreamMessage([]byte(`{"type":"response.cancelled"}`))
				require.Nil(t, session.getCurrent())
				require.Len(t, session.resolvedControls, 1)
			} else {
				session.pendingControls = []responsesWSControl{control}
			}
			create := responsesWSCreateRequest{EventID: "shared_event", StreamID: "new_turn", Request: dto.OpenAIResponsesRequest{Model: "gpt-5"}}
			apiErr := session.handleResponseCreate(create)
			require.NotNil(t, apiErr)
			assert.Equal(t, http.StatusBadRequest, apiErr.StatusCode)
			assert.Contains(t, apiErr.Error(), "retained control")
			assert.Nil(t, session.getCurrent())
		})
	}
}

func TestResponsesWSFirstWriteRetryKeepsTheLogicalRateReservation(t *testing.T) {
	previousPost := postResponsesWSConsumeQuota
	t.Cleanup(func() { postResponsesWSConsumeQuota = previousPost })
	posted := 0
	postResponsesWSConsumeQuota = func(_ *gin.Context, _ *relaycommon.RelayInfo, _ *dto.Usage, _ []string) { posted++ }

	// The limiter's completion is once-only: releasing the first invisible
	// attempt would admit competing requests and make later success a no-op.
	limiter := common.InMemoryRateLimiter{}
	reserved, allowed := limiter.Reserve("ws-logical-retry", 1, 60)
	require.True(t, allowed)
	completions := []bool{}
	commit := func(success bool) {
		completions = append(completions, success)
		reserved(success)
	}
	assertSlotPinned := func() {
		competing, admitted := limiter.Reserve("ws-logical-retry", 1, 60)
		if admitted {
			competing(false)
		}
		assert.False(t, admitted, "another request must not take the reserved success slot during retry")
	}
	failedTarget, _ := responsesWSTestPair(t)
	require.NoError(t, failedTarget.Close())
	first := responsesWSCorrelationState("logical_stream")
	first.eventID = "evt_retry"
	first.commitRate = commit
	refund := &responsesWSRetryRefund{}
	first.info.Billing = refund
	session := &responsesWSSession{target: failedTarget, current: first}
	payload := []byte(`{"type":"response.create","event_id":"evt_retry","stream_id":"logical_stream","model":"gpt-5"}`)
	require.Error(t, session.writeFirstTargetEvent(first, payload))
	assert.Equal(t, 1, refund.refunds, "failed channel attempt still refunds its billing reservation")
	assertSlotPinned()
	assert.Empty(t, completions)
	assert.Nil(t, session.getCurrent())
	assert.Empty(t, session.completedCreates, "an invisible failed write is an attempt, not a completed logical create")

	successfulTarget, targetPeer := responsesWSTestPair(t)
	session.setTarget(successfulTarget)
	second := responsesWSCorrelationState("logical_stream")
	second.eventID = "evt_retry"
	second.commitRate = commit
	require.True(t, session.tryReserveCurrent(second))
	require.NoError(t, session.writeFirstTargetEvent(second, payload))
	require.NoError(t, targetPeer.SetReadDeadline(time.Now().Add(time.Second)))
	_, forwarded, err := targetPeer.ReadMessage()
	require.NoError(t, err)
	assert.Equal(t, payload, forwarded)
	assertSlotPinned()
	client, clientPeer := responsesWSTestPair(t)
	session.client = client
	terminal := []byte(`{"type":"response.completed","response":{"usage":{"input_tokens":7,"output_tokens":3,"total_tokens":10}}}`)
	require.NoError(t, session.forwardUpstreamMessage(websocket.TextMessage, terminal))
	require.NoError(t, clientPeer.SetReadDeadline(time.Now().Add(time.Second)))
	_, _, err = clientPeer.ReadMessage()
	require.NoError(t, err)
	competing, admitted := limiter.Reserve("ws-logical-retry", 1, 60)
	if admitted {
		competing(false)
	}
	assert.False(t, admitted, "successful retry consumes the reserved success slot")
	assert.Equal(t, []bool{true}, completions)
	assert.Equal(t, 1, posted)
	assert.Nil(t, session.getCurrent())
	session.failCurrent()
	assert.Equal(t, []bool{true}, completions)
}

func TestResponsesWSTerminalClientWriteFailureReleasesSuccessSlotAfterBilling(t *testing.T) {
	previousPost := postResponsesWSConsumeQuota
	t.Cleanup(func() { postResponsesWSConsumeQuota = previousPost })
	posted := 0
	postResponsesWSConsumeQuota = func(_ *gin.Context, _ *relaycommon.RelayInfo, usage *dto.Usage, _ []string) {
		posted++
		assert.Equal(t, 10, usage.TotalTokens)
	}
	client, _ := responsesWSTestPair(t)
	require.NoError(t, client.Close())
	state := responsesWSCorrelationState("stream_completed")
	limiter := common.InMemoryRateLimiter{}
	reserved, admitted := limiter.Reserve("ws-terminal-delivery", 1, 60)
	require.True(t, admitted)
	completions := []bool{}
	state.commitRate = func(success bool) { completions = append(completions, success); reserved(success) }
	session := &responsesWSSession{client: client, current: state}
	message := []byte(`{"type":"response.completed","response":{"usage":{"input_tokens":7,"output_tokens":3,"total_tokens":10}}}`)
	require.Error(t, session.forwardUpstreamMessage(websocket.TextMessage, message))
	assert.Equal(t, 1, posted, "provider usage remains billable when downstream delivery fails")
	assert.Nil(t, session.getCurrent(), "billing completion may clear the active turn before the write result")
	assert.Equal(t, []bool{false}, completions, "the captured rate reservation must survive until the failed client write")
	next, admitted := limiter.Reserve("ws-terminal-delivery", 1, 60)
	require.True(t, admitted, "downstream terminal write failure must release the reserved success slot")
	next(false)
	session.failCurrent()
	state.finishRate(true)
	assert.Equal(t, []bool{false}, completions, "later cleanup cannot change or duplicate the failed outcome")
}

type responsesWSRetryRefund struct {
	relaycommon.BillingSettler
	refunds int
}

func (refund *responsesWSRetryRefund) Refund(*gin.Context) { refund.refunds++ }

func responsesWSCorrelationState(streamID string) *responsesWSCallState {
	return &responsesWSCallState{
		StreamID: streamID,
		info:     &relaycommon.RelayInfo{},
		usage:    &dto.Usage{},
	}
}

func responsesWSCorrelationObject(t *testing.T, payload []byte) map[string]any {
	t.Helper()
	var data map[string]any
	require.NoError(t, common.Unmarshal(payload, &data))
	return data
}

func responsesWSCorrelationForwardControl(t *testing.T, session *responsesWSSession, peer *websocket.Conn, eventID, streamID string) {
	t.Helper()
	message, err := common.Marshal(map[string]string{"type": "session.update", "event_id": eventID, "stream_id": streamID})
	require.NoError(t, err)
	require.Nil(t, session.forwardControl(websocket.TextMessage, message, eventID, streamID))
	require.NoError(t, peer.SetReadDeadline(time.Now().Add(time.Second)))
	_, forwarded, err := peer.ReadMessage()
	require.NoError(t, err)
	assert.Equal(t, message, forwarded)
}
