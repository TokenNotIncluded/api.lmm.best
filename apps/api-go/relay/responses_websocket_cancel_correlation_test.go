package relay

import (
	"fmt"
	"testing"
	"time"

	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
)

func TestResponsesWSForeignErrorsCannotRetireCurrentCancel(t *testing.T) {
	previousPost := postResponsesWSConsumeQuota
	t.Cleanup(func() { postResponsesWSConsumeQuota = previousPost })
	foreignIdentities := []struct{ name, fields string }{
		{"top_old", `"response_id":"response_old"`},
		{"nested_old", `"response":{"id":"response_old"}`},
		{"empty_top_nested_old", `"response_id":"","response":{"id":"response_old"}`},
		{"top_foreign", `"response_id":"response_foreign"`},
		{"nested_foreign", `"response":{"id":"response_foreign"}`},
	}
	for _, foreign := range foreignIdentities {
		for _, knownActive := range []bool{false, true} {
			if !knownActive && (foreign.name == "top_foreign" || foreign.name == "nested_foreign") {
				continue
			}
			for _, explicitTarget := range []bool{false, true} {
				for _, identified := range []bool{false, true} {
					references := []string{"none"}
					if identified {
						references = append(references, "top", "nested")
					}
					for _, reference := range references {
						for _, partial := range []bool{false, true} {
							name := fmt.Sprintf("%s/known_active_%t/explicit_target_%t/event_id_%t/late_ref_%s/partial_%t", foreign.name, knownActive, explicitTarget, identified, reference, partial)
							t.Run(name, func(t *testing.T) {
								posted := 0
								postResponsesWSConsumeQuota = func(_ *gin.Context, _ *relaycommon.RelayInfo, _ *dto.Usage, _ []string) { posted++ }
								target, peer := responsesWSTestPair(t)
								session := &responsesWSSession{target: target}
								previous := responsesWSCorrelationState("stream_old")
								previous.eventID = "evt_old"
								previous.responseID = "response_old"
								require.True(t, session.tryReserveCurrent(previous))
								session.processUpstreamMessage([]byte(`{"type":"response.cancelled","response":{"id":"response_old"}}`))
								require.Nil(t, session.getCurrent())
								require.Equal(t, []string{"response_old"}, session.finishedResponseIDs)

								current := responsesWSCorrelationState("stream_current")
								current.eventID = "evt_current"
								if knownActive {
									current.responseID = "response_current"
								}
								completions := []bool{}
								current.commitRate = func(success bool) { completions = append(completions, success) }
								refund := &responsesWSRetryRefund{}
								current.info.Billing = refund
								if partial {
									current.usage.PromptTokens = 7
									current.usage.CompletionTokens = 3
									current.usage.TotalTokens = 10
								}
								require.True(t, session.tryReserveCurrent(current))
								eventID := ""
								if identified {
									eventID = "evt_cancel"
								}
								targetField := ""
								if explicitTarget {
									targetField = `,"response_id":"response_current"`
								}
								cancel := []byte(fmt.Sprintf(`{"type":"response.cancel","event_id":%q,"stream_id":"stream_current"%s}`, eventID, targetField))
								require.Nil(t, session.handleResponseCancel(websocket.TextMessage, cancel))
								require.NoError(t, peer.SetReadDeadline(time.Now().Add(time.Second)))
								_, forwarded, err := peer.ReadMessage()
								require.NoError(t, err)
								require.Equal(t, cancel, forwarded)
								require.Len(t, session.pendingControls, 1)

								foreignError := []byte(`{"type":"error",` + foreign.fields + `,"error":{"type":"invalid_request_error","code":"response_not_found","message":"foreign rejected"}}`)
								foreignOutput := session.processUpstreamMessage(foreignError)
								t.Logf("FOREIGN_OBSERVATION case=%s pending=%d resolved=%d current=%t rate=%v refunds=%d posted=%d output=%s", name, len(session.pendingControls), len(session.resolvedControls), session.getCurrent() == current, completions, refund.refunds, posted, foreignOutput)
								if len(session.pendingControls) != 1 || len(session.resolvedControls) != 0 {
									t.Errorf("FOREIGN_ERROR_RETIRED_CURRENT_CANCEL: pending=%d resolved=%d", len(session.pendingControls), len(session.resolvedControls))
								}
								require.Same(t, current, session.getCurrent(), "the foreign error must not directly settle current")
								require.Empty(t, completions)
								require.Zero(t, refund.refunds)
								require.Zero(t, posted)

								lateError := `{"type":"error","error":{"type":"invalid_request_error","code":"response_not_active","message":"legitimate delayed cancel rejection"}}`
								if reference == "top" {
									lateError = `{"type":"error","event_id":"evt_cancel","error":{"type":"invalid_request_error","code":"response_not_active","message":"legitimate delayed cancel rejection"}}`
								} else if reference == "nested" {
									lateError = `{"type":"error","event_id":"provider_error","error":{"event_id":"evt_cancel","type":"invalid_request_error","code":"response_not_active","message":"legitimate delayed cancel rejection"}}`
								}
								lateOutput := session.processUpstreamMessage([]byte(lateError))
								t.Logf("LATE_OBSERVATION case=%s pending=%d resolved=%d current=%t rate=%v refunds=%d posted=%d output=%s", name, len(session.pendingControls), len(session.resolvedControls), session.getCurrent() == current, completions, refund.refunds, posted, lateOutput)
								if session.getCurrent() != current || len(completions) != 0 || refund.refunds != 0 || posted != 0 {
									t.Errorf("LATE_CANCEL_ERROR_SETTLED_CURRENT: current=%t rate=%v refunds=%d posted=%d", session.getCurrent() == current, completions, refund.refunds, posted)
								}
							})
						}
					}
				}
			}
		}
	}
}

func TestResponsesWSCancelRejectionInferencePreservesExplicitTargetsAndLegacy(t *testing.T) {
	previousPost := postResponsesWSConsumeQuota
	t.Cleanup(func() { postResponsesWSConsumeQuota = previousPost })
	cases := []struct {
		name, activeResponseID, targetResponseID, errorIdentity string
		finishedResponseIDs                                     []string
	}{
		{"current implicit top", "response_current", "", `"response_id":"response_current",`, nil},
		{"current implicit nested", "response_current", "", `"response":{"id":"response_current"},`, nil},
		{"foreign explicit top", "response_current", "response_foreign", `"response_id":"response_foreign",`, nil},
		{"foreign explicit nested", "response_current", "response_foreign", `"response":{"id":"response_foreign"},`, nil},
		{"finished explicit before created", "", "response_old", `"response":{"id":"response_old"},`, []string{"response_old"}},
		{"unknown active unseen top", "", "", `"response_id":"response_unseen",`, nil},
		{"unknown active unseen nested", "", "", `"response_id":"","response":{"id":"response_unseen"},`, nil},
		{"unreferenced legacy", "response_current", "", "", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			posted := 0
			postResponsesWSConsumeQuota = func(_ *gin.Context, _ *relaycommon.RelayInfo, _ *dto.Usage, _ []string) { posted++ }
			target, peer := responsesWSTestPair(t)
			current := responsesWSCorrelationState("stream_current")
			current.responseID = tc.activeResponseID
			completions := []bool{}
			current.commitRate = func(success bool) { completions = append(completions, success) }
			refund := &responsesWSRetryRefund{}
			current.info.Billing = refund
			session := &responsesWSSession{target: target, current: current, finishedResponseIDs: tc.finishedResponseIDs}
			cancel := []byte(fmt.Sprintf(`{"type":"response.cancel","stream_id":"stream_current","response_id":%q}`, tc.targetResponseID))
			require.Nil(t, session.handleResponseCancel(websocket.TextMessage, cancel))
			require.NoError(t, peer.SetReadDeadline(time.Now().Add(time.Second)))
			_, forwarded, err := peer.ReadMessage()
			require.NoError(t, err)
			require.Equal(t, cancel, forwarded)
			require.Len(t, session.pendingControls, 1)
			rejection := []byte(`{"type":"error",` + tc.errorIdentity + `"error":{"type":"invalid_request_error","code":"response_not_found"}}`)
			session.processUpstreamMessage(rejection)
			require.Empty(t, session.pendingControls)
			require.Len(t, session.resolvedControls, 1)
			require.Same(t, current, session.getCurrent())
			require.Empty(t, completions)
			require.Zero(t, refund.refunds)
			require.Zero(t, posted)
		})
	}
}
