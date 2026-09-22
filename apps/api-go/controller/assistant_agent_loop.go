// Copyright (C) 2026 LIghtJUNction
package controller

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/internal/agent"
	"github.com/LIghtJUNction/api.lmm.best/setting"
	"github.com/gin-gonic/gin"
)

const (
	assistantAgentToolCallBudget = 64
	assistantAgentToolTimeout    = 20 * time.Second
)

// Run tools synchronously with a child deadline. Never detach a Gin context
// into a goroutine and return it to the pool while a write can still happen.
func executeAssistantAgentTool(c *gin.Context, call assistantOpenAIToolCall) (result map[string]any) {
	readOnly := assistantToolCallReadOnly(c, call)
	originalRequest := c.Request
	ctx, cancel := context.WithTimeout(originalRequest.Context(), assistantAgentToolTimeout)
	c.Request = originalRequest.WithContext(ctx)
	defer func() {
		c.Request = originalRequest
		cancel()
		if recover() != nil {
			result = map[string]any{"ok": false, "status": "tool_failed", "error": "tool execution failed unexpectedly; inspect live state before retrying", "mutation_attempted": !readOnly, "do_not_retry": true}
		}
	}()
	if err := ctx.Err(); err != nil {
		return map[string]any{"ok": false, "status": "cancelled", "error": "tool cancelled before execution"}
	}
	result = executeAssistantTool(c, call)
	if finishAssistantRegistrationTermination(c) {
		return
	}
	if ctx.Err() != nil {
		// A timed-out write may have committed. Keep that uncertainty fenced.
		return map[string]any{"ok": false, "status": "tool_timeout", "error": "tool exceeded its time limit; narrow the read or verify live state", "mutation_attempted": !readOnly, "do_not_retry": true}
	}
	return result
}

func assistantToolMadeProgress(name string, result map[string]any) bool {
	ok, _ := result["ok"].(bool)
	if !ok || name == "set_conversation_title" {
		return false
	}
	// Empty catalog searches are a common stalled plan: changing the search
	// string makes every call fingerprint unique but supplies no new evidence.
	if name == "list_admin_operations" {
		return assistantOperationInt(result["total"], 0) > 0
	}
	return true
}

// Exhaustion/protocol failures are terminal for this run, not a reason for
// the browser to repeat the entire model/tool plan.
func assistantErrorRetryable(status int, code string, workStarted bool) bool {
	if workStarted {
		return false
	}
	switch code {
	case "ASSISTANT_REQUEST_TIMEOUT", "ASSISTANT_REQUEST_CANCELLED", "ASSISTANT_AGENT_MAX_STEPS",
		"ASSISTANT_REQUIRED_TOOL_MISSING", "ASSISTANT_TOO_MANY_TOOL_CALLS", "ASSISTANT_RUN_FAILED",
		"ASSISTANT_STREAM_INCOMPLETE", "ASSISTANT_INVALID_UPSTREAM_RESPONSE", "ASSISTANT_EMPTY_UPSTREAM_RESPONSE":
		return false
	}
	return assistantRetryableUpstreamStatus(status)
}

func runAssistantAgent(c *gin.Context, settings setting.AssistantSettings, conversation []assistantOpenAIMessage) {
	release, acquired := assistantAgentLimiter.TryAcquire()
	if !acquired {
		writeAssistantError(c, http.StatusServiceUnavailable, "ASSISTANT_BUSY", errors.New("AI assistant is busy; retry shortly"))
		return
	}
	defer release()

	timeout := time.Duration(settings.TimeoutSeconds) * time.Second
	if timeout < 5*time.Second {
		timeout = assistantAgentDefaultTimeout
	}
	timeout = min(timeout, 5*time.Minute)
	started := time.Now()
	ctx, cancel := context.WithTimeout(c.Request.Context(), timeout)
	// Store milliseconds: gin's typed getters assert the concrete type, and a
	// time.Duration would silently read back as zero.
	c.Set(assistantRunTimeoutKey, timeout.Milliseconds())
	c.Set(assistantRunStartedAtKey, started)
	defer cancel()
	originalRequest := c.Request
	c.Request = c.Request.WithContext(ctx)
	defer func() {
		c.Request = originalRequest
		common.CleanupBodyStorage(c)
	}()
	streamSession := assistantStreamSessionFrom(c)
	// Watch only the stream session, never Gin from another goroutine. A
	// terminal deadline event still reaches the browser while a legacy tool
	// unwinds; the handler keeps ownership until the tool actually returns.
	stopWatch := streamSession.watch(ctx, cancel, 10*time.Second, func() map[string]any {
		return assistantRunDiagnostics(c)
	})
	defer stopWatch()
	defer func() {
		if recover() != nil {
			writeAssistantError(c, http.StatusInternalServerError, "ASSISTANT_RUN_FAILED", errors.New("assistant execution failed unexpectedly; inspect completed actions before retrying"))
		}
	}()
	if streamSession != nil && c.GetBool(assistantSupportGuardKey) {
		// Stream deltas can arrive once per token. Poll at most four times a
		// second here; model/tool/final boundaries always check immediately.
		var lastSupportCheck time.Time
		var supportCheckMu sync.Mutex
		streamSession.setSupportCheck(func() error {
			supportCheckMu.Lock()
			if time.Since(lastSupportCheck) < 250*time.Millisecond {
				supportCheckMu.Unlock()
				return nil
			}
			lastSupportCheck = time.Now()
			supportCheckMu.Unlock()
			if err := assistantSupportGuardError(c); err != nil {
				cancel()
				return err
			}
			return nil
		})
		defer streamSession.setSupportCheck(nil)
	}
	if assistantHumanSupportInterrupted(c) || assistantAgentRequestStopped(c) {
		return
	}

	rootRequestID := c.GetString(common.RequestIdKey)
	if rootRequestID == "" {
		rootRequestID = common.NewRequestId()
		c.Set(common.RequestIdKey, rootRequestID)
	}

	userContext := assistantUserContextFromGin(c)
	if userContext.ConversationTitleNeeded {
		// History already derives a redacted fallback title from the question.
		// Optional metadata must not cost a model round trip before the answer.
		userContext = skipAssistantConversationTitle(c)
		c.Set(assistantPromptKey, "") // discard the prepared title-request prompt
	}
	adminAutomationAllowed := false
	if settings.AgentLoopEnabled && userContext.AdministratorMode {
		_, authErr := validateAssistantAdminAutomationSession(c, assistantActorUserID(c))
		adminAutomationAllowed = authErr == nil
	}
	c.Set(assistantAdminAutomationContextKey, adminAutomationAllowed)
	messages := make([]assistantOpenAIMessage, 1, len(conversation)+1)
	messages[0] = assistantOpenAIMessage{Role: "system", Content: assistantPrompt(c, settings, userContext)}
	messages = append(messages, conversation...)
	var compactErr error
	messages, compactErr = compactAssistantAgentContext(messages)
	if compactErr != nil {
		writeAssistantError(c, http.StatusRequestEntityTooLarge, "ASSISTANT_CONTEXT_TOO_LARGE", errors.New("assistant context exceeded its byte budget"))
		return
	}
	maxSteps := settings.MaxSteps
	if maxSteps < 1 {
		maxSteps = 1
	}
	maxSteps = min(maxSteps, assistantAgentMaxSteps)
	forceL0Assessment := assistantL0InterlocutorAssessmentRequired(userContext)
	forceRecommendationWorkflow := assistantRecommendationWorkflowRequired(userContext)
	forceCreateKeyWorkflow := assistantCreateKeyWorkflowRequired(userContext)
	forceImageGenerationWorkflow := assistantImageGenerationWorkflowRequired(userContext)
	forcePublicActivityWorkflow := assistantPublicActivityWorkflowRequired(userContext)
	forceNewUserGiftWorkflow := assistantNewUserGiftWorkflowRequired(userContext)
	forceWeeklyDiscountWorkflow := assistantWeeklyDiscountWorkflowRequired(userContext)
	forceSupportBooking := assistantSupportBookingDecision(userContext.LatestUserRequest) > 0 || (!settings.AgentLoopEnabled && assistantSupportBookingAuthorized(c))
	forceHumanSupportWorkflow := forceSupportBooking || assistantHumanSupportWorkflowRequired(userContext)
	forceTaskWorkflow := assistantNamedToolChoiceName(assistantToolChoiceForContext(userContext)) != ""
	forceReadChain := assistantLiveReadRequired(userContext)
	if forceL0Assessment && maxSteps < 2 {
		maxSteps = 2
	}
	// Removing title generation must not remove the task/answer budget for
	// mandatory tools such as arithmetic when the optional loop is disabled.
	if forceTaskWorkflow && maxSteps < 2 {
		maxSteps = 2
	}
	if minimum := assistantRecommendationWorkflowMinSteps(userContext); maxSteps < minimum {
		maxSteps = minimum
	}
	if minimum := assistantCreateKeyWorkflowMinSteps(userContext); maxSteps < minimum {
		maxSteps = minimum
	}
	if minimum := assistantImageGenerationWorkflowMinSteps(userContext); maxSteps < minimum {
		maxSteps = minimum
	}
	if minimum := assistantLiveActivityWorkflowMinSteps(userContext); maxSteps < minimum {
		maxSteps = minimum
	}
	if forceSupportBooking && maxSteps < 3 {
		maxSteps = 3
	}
	if minimum := assistantHumanSupportWorkflowMinSteps(userContext); maxSteps < minimum {
		maxSteps = minimum
	}
	if minimum := assistantReadChainSteps(userContext); maxSteps < minimum {
		maxSteps = minimum
	}
	if !settings.AgentLoopEnabled {
		if !forceL0Assessment && !forceTaskWorkflow && !forceRecommendationWorkflow && !forceCreateKeyWorkflow && !forceImageGenerationWorkflow && !forcePublicActivityWorkflow && !forceNewUserGiftWorkflow && !forceWeeklyDiscountWorkflow && !forceHumanSupportWorkflow && !forceReadChain {
			maxSteps = 1
		}
	}
	cacheKey := c.GetString("assistant_cache_key")
	// Record the resolved budget before the loop so a deadline inside the first
	// turn still reports the plan the run was actually allowed to use.
	c.Set(assistantRunMaxStepsKey, maxSteps)
	usedCacheSensitiveTool := false
	agentEnabled := maxSteps > 1 && (settings.AgentLoopEnabled || forceL0Assessment || forceTaskWorkflow || forceRecommendationWorkflow || forceCreateKeyWorkflow || forceImageGenerationWorkflow || forcePublicActivityWorkflow || forceNewUserGiftWorkflow || forceWeeklyDiscountWorkflow || forceHumanSupportWorkflow || forceReadChain)
	var tools []assistantOpenAIToolDefinition
	var calledTools, successfulTools map[string]bool
	toolTraces := make([]assistantToolTrace, 0, assistantToolCallsPerTurn)
	if agentEnabled {
		tools = assistantToolDefinitionsForContext(userContext)
		calledTools = make(map[string]bool)
		successfulTools = make(map[string]bool)
	}
	usedCallIDs := make(map[string]bool)
	for _, message := range messages {
		for _, call := range message.ToolCalls {
			usedCallIDs[call.ID] = true
		}
	}
	var loopGuard agent.LoopGuard
	finalAnswerOnly := false
	contextRecoveries := 0
	totalToolCalls := 0
	stalledRounds := 0

	for step := 0; step < maxSteps; step++ {
		c.Set(assistantRunStepKey, step+1)
		if assistantHumanSupportInterrupted(c) || assistantAgentRequestStopped(c) {
			return
		}
		messages, compactErr = compactAssistantAgentContext(messages)
		if compactErr != nil {
			writeAssistantError(c, http.StatusRequestEntityTooLarge, "ASSISTANT_CONTEXT_TOO_LARGE", errors.New("required assistant context exceeded its byte budget"))
			return
		}
		streamTurn := streamSession != nil && settings.StreamEnabled
		request := assistantOpenAIRequest{
			Model:           settings.Model,
			Messages:        messages,
			Stream:          streamTurn,
			Temperature:     settings.Temperature,
			MaxTokens:       settings.MaxTokens,
			ReasoningEffort: assistantReasoningEffort(settings),
		}
		// Reserve the last turn for a final natural-language answer. This
		// makes MaxSteps a hard bound while ensuring a tool call can finish.
		if agentEnabled && step < maxSteps-1 && !finalAnswerOnly {
			request.Tools = tools
			request.ToolChoice = assistantToolChoiceForAgentStep(userContext, calledTools, successfulTools)
		}

		plannedRead, serverRead := assistantPlannedReadCall(request, userContext, calledTools, step)
		phase := "model"
		if serverRead {
			phase = "tool"
		}
		if agentEnabled && (step == maxSteps-1 || finalAnswerOnly) {
			phase = "answer"
			request.Messages = append([]assistantOpenAIMessage(nil), messages...)
			request.Messages[0].Content += "\nThe tool budget for this request is now closed. Answer using only verified results already present. State the scope, failed or incomplete reads, and remaining work. Do not invent user IDs or claim all users were checked. Do not request more tools or imply an uncertain mutation succeeded."
		}
		if err := streamSession.progress(phase, step+1); err != nil {
			cancel()
			return
		}

		var status int
		var body []byte
		var err error
		if serverRead {
			// The server already chose this authorized, argument-free read.
			// Reuse the normal tool path below (budgets, cancellation, live
			// permissions, receipts), without asking a model to echo its name.
			status = http.StatusOK
			body, err = common.MarshalLimit(assistantOpenAIResponse{
				Choices: []assistantOpenAIResponseChoice{{Message: assistantOpenAIResponseMessage{
					ToolCalls: []assistantOpenAIToolCall{plannedRead},
				}}},
			}, assistantUpstreamResponseMaxBytes)
		} else if streamTurn {
			attempts := 0
			status, body, err = relayAssistantTurnWithRetryUsing(c, request, rootRequestID, step, func(c *gin.Context, request assistantOpenAIRequest, rootRequestID string, step int) (int, []byte, error) {
				if attempts > 0 {
					if err := streamSession.resetContent(); err != nil {
						return http.StatusBadGateway, nil, err
					}
				}
				attempts++
				return relayAssistantStreamTurn(c, request, rootRequestID, step, streamSession)
			})
		} else {
			status, body, err = relayAssistantAgentTurn(c, request, rootRequestID, step)
		}
		if assistantHumanSupportInterrupted(c) || assistantAgentRequestStopped(c) {
			return
		}
		if err != nil {
			writeAssistantError(c, http.StatusInternalServerError, "ASSISTANT_REQUEST_BUILD_FAILED", errors.New("failed to build assistant request"))
			return
		}
		if status < http.StatusOK || status >= http.StatusMultipleChoices {
			if contextRecoveries < 2 && assistantUpstreamContextExceeded(status, body) {
				// Provider windows differ. Retry a rejected model request with a
				// smaller history, keeping exact policy, current task and latest
				// tool receipt. No tool has executed for this failed turn.
				if compacted, compactErr := agent.Compact(messages, assistantContextBytes(messages)*2/3); compactErr == nil {
					messages = compacted
					contextRecoveries++
					if resetErr := streamSession.resetContent(); resetErr != nil {
						writeAssistantError(c, http.StatusBadGateway, "ASSISTANT_STREAM_WRITE_FAILED", errors.New("assistant stream output failed"))
						return
					}
					step--
					continue
				}
			}
			writeAssistantUpstreamError(c, "ASSISTANT_UPSTREAM_FAILED", "AI assistant upstream request failed")
			return
		}

		response, err := parseAssistantResponse(body)
		if err != nil || len(response.Choices) == 0 {
			writeAssistantUpstreamError(c, "ASSISTANT_INVALID_UPSTREAM_RESPONSE", "AI assistant upstream returned an invalid response")
			return
		}
		message := response.Choices[0].Message
		// A failed required read closes tool selection. An upstream must not
		// turn that failure into a mutation by ignoring tool_choice=none.
		if choice, ok := request.ToolChoice.(string); ok && choice == "none" && len(message.ToolCalls) > 0 {
			writeAssistantError(c, http.StatusBadGateway, "ASSISTANT_TOOL_CHOICE_VIOLATION", errors.New("assistant requested tools after tool selection was closed"))
			return
		}
		if forceTaskWorkflow || forceRecommendationWorkflow || forceCreateKeyWorkflow || forceImageGenerationWorkflow || forcePublicActivityWorkflow || forceNewUserGiftWorkflow || forceWeeklyDiscountWorkflow || forceHumanSupportWorkflow || forceReadChain {
			requiredTool := assistantNamedToolChoiceName(request.ToolChoice)
			if requiredTool != "" && (len(message.ToolCalls) != 1 || strings.TrimSpace(message.ToolCalls[0].Function.Name) != requiredTool) {
				writeAssistantError(c, http.StatusBadGateway, "ASSISTANT_REQUIRED_TOOL_MISSING", errors.New("assistant did not follow the required tool workflow"))
				return
			}
		}
		if len(message.ToolCalls) == 0 {
			normalizedBody, normalizeErr := normalizeAssistantClientResponse(c, body)
			if normalizeErr != nil {
				writeAssistantUpstreamError(c, "ASSISTANT_EMPTY_UPSTREAM_RESPONSE", "AI assistant upstream returned no usable answer")
				return
			}
			if !usedCacheSensitiveTool && cacheKey != "" {
				storeAssistantCachedResponse(settings, cacheKey, status, normalizedBody, c.GetString(assistantConversationTitleDraftKey))
				c.Header("X-LMM-Assistant-Cache", "STORE")
			}
			if streamSession != nil {
				enrichedBody := assistantHistoryResponseBody(c, status, normalizedBody)
				if c.GetBool("assistant_turn_unavailable") {
					writeAssistantError(c, http.StatusConflict, "ASSISTANT_TURN_UNAVAILABLE", errors.New("saved turn cannot be reused; send a new message"))
					return
				}
				if canonical, exists := c.Get("assistant_history_canonical_content"); exists && c.GetBool("assistant_history_canonical_changed") {
					if text, ok := canonical.(string); ok && text != streamSession.safeContent() {
						if err := streamSession.resetContent(); err != nil {
							return
						}
						if err := streamSession.appendContent(text); err != nil {
							return
						}
						streamTurn = true
					}
				}
				if c.GetBool("assistant_support_response_replaced") {
					writeAssistantSupportCompletion(c, enrichedBody)
					return
				}
				if !streamTurn {
					finalResponse, parseErr := parseAssistantResponse(normalizedBody)
					if parseErr == nil && len(finalResponse.Choices) > 0 {
						_ = streamSession.appendContent(assistantResponseContent(finalResponse.Choices[0].Message.Content))
					}
				}
				streamBody := sanitizeAssistantStreamResponseBody(enrichedBody, streamSession.safeContent())
				c.Set(assistantFinalResponseBodyKey, streamBody)
				if err := streamSession.finish(enrichedBody); err != nil {
					writeAssistantError(c, http.StatusBadGateway, "ASSISTANT_STREAM_WRITE_FAILED", errors.New("assistant stream output failed"))
				}
				return
			}
			c.Data(status, "application/json; charset=utf-8", normalizedBody)
			return
		}
		if (!settings.AgentLoopEnabled && !forceL0Assessment && !forceTaskWorkflow && !forceRecommendationWorkflow && !forceCreateKeyWorkflow && !forceImageGenerationWorkflow && !forcePublicActivityWorkflow && !forceNewUserGiftWorkflow && !forceWeeklyDiscountWorkflow && !forceHumanSupportWorkflow && !forceReadChain) || step >= maxSteps-1 || finalAnswerOnly {
			writeAssistantError(c, http.StatusBadGateway, "ASSISTANT_AGENT_MAX_STEPS", errors.New("assistant agent reached its step limit before producing a final answer"))
			return
		}
		if len(message.ToolCalls) > assistantToolCallsPerResponse {
			writeAssistantError(c, http.StatusBadGateway, "ASSISTANT_TOO_MANY_TOOL_CALLS", errors.New("assistant requested too many tools in one turn"))
			return
		}

		// Canonicalize before retaining the assistant message as well as its
		// results. Repairing only tool_call_id leaves orphaned tool responses
		// when a compatible provider omits IDs or surrounds them with spaces.
		message.ToolCalls = agent.NormalizeCalls(message.ToolCalls, step, usedCallIDs)
		messages = append(messages, assistantOpenAIMessage{
			Role:      "assistant",
			Content:   assistantResponseContent(message.Content),
			ToolCalls: message.ToolCalls,
		})
		executed := 0
		madeProgress := false
		for _, call := range message.ToolCalls {
			if assistantHumanSupportInterrupted(c) || assistantAgentRequestStopped(c) {
				return
			}
			toolName := strings.TrimSpace(call.Function.Name)
			if toolName != "set_conversation_title" {
				usedCacheSensitiveTool = true
			}
			readOnly := assistantToolCallReadOnly(c, call)
			var result map[string]any
			switch {
			case totalToolCalls >= assistantAgentToolCallBudget:
				result = map[string]any{"ok": false, "status": "tool_budget_limit", "error": "not executed: request tool budget exhausted; answer from existing receipts and state any incomplete coverage"}
			case executed >= assistantToolCallsPerTurn:
				result = map[string]any{"ok": false, "status": "tool_batch_limit", "error": "not executed: at most four tools run per round; request this call again in a later round"}
			case assistantAdminRetryMutationBlocked(c, call):
				result = assistantAdminRetryMutationResult()
			case !loopGuard.Allow(call, readOnly):
				result = map[string]any{"ok": false, "status": "tool_repetition_limit", "error": "not executed: this exact call already succeeded or repeatedly made no progress; use the existing result, change the request, or explain the remaining work"}
			default:
				calledTools[toolName] = true
				executed++
				totalToolCalls++
				c.Set("assistant_work_started", true)
				streamSession.markWorkStarted()
				if err := streamSession.progress("tool", step+1); err != nil {
					cancel()
					return
				}
				result = executeAssistantAgentTool(c, call)
				ok, _ := result["ok"].(bool)
				attempted, _ := result["mutation_attempted"].(bool)
				loopGuard.Complete(call, readOnly, ok || attempted)
				if isAssistantAdministratorTool(toolName) && !readOnly && (attempted || (ok && c.GetBool(assistantAdminAutomationContextKey))) {
					c.Set("assistant_admin_mutation_attempted", true)
				}
			}
			if c.IsAborted() || assistantAgentRequestStopped(c) {
				return
			}
			madeProgress = madeProgress || assistantToolMadeProgress(toolName, result)
			resultJSON := assistantAgentToolResultJSON(result)
			if ok, _ := result["ok"].(bool); ok {
				successfulTools[toolName] = true
			}
			if toolName == "set_conversation_title" {
				// Attempt optional metadata once, including malformed drafts, so
				// it cannot consume the turns reserved for the user's task.
				userContext = skipAssistantConversationTitle(c)
			}
			if toolName != "set_conversation_title" {
				toolTraces = append(toolTraces, buildAssistantToolTrace(call, result))
				c.Set(assistantClientToolsKey, toolTraces)
			}
			messages = append(messages, assistantOpenAIMessage{
				Role:       "tool",
				Content:    string(resultJSON),
				ToolCallID: call.ID,
			})
		}
		// An entirely repeated batch gets one final answer turn, so stalled
		// plans cannot spend the remaining budget repeating identical calls.
		if madeProgress {
			stalledRounds = 0
		} else {
			stalledRounds++
		}
		finalAnswerOnly = executed == 0 || stalledRounds >= 2 || totalToolCalls >= assistantAgentToolCallBudget
	}

	writeAssistantError(c, http.StatusBadGateway, "ASSISTANT_AGENT_MAX_STEPS", errors.New("assistant agent reached its step limit"))
}

// Only pre-execute the existing narrow allowlist of argument-free reads,
// and only when the deterministic workflow explicitly selected an exposed
// tool. Parameterized reads and every mutation still go through the model.
func assistantPlannedReadCall(request assistantOpenAIRequest, userContext assistantUserContext, calledTools map[string]bool, step int) (assistantOpenAIToolCall, bool) {
	name := assistantNamedToolChoiceName(request.ToolChoice)
	if calledTools[name] || !assistantServerReadFallbackAllowed(name) || !assistantToolAllowedForContext(name, userContext) {
		return assistantOpenAIToolCall{}, false
	}
	for _, tool := range request.Tools {
		if tool.Type == "function" && tool.Function.Name == name {
			return assistantOpenAIToolCall{
				ID:       fmt.Sprintf("assistant-server-read-%d", step+1),
				Type:     "function",
				Function: assistantOpenAIToolCallFunction{Name: name, Arguments: "{}"},
			}, true
		}
	}
	return assistantOpenAIToolCall{}, false
}
