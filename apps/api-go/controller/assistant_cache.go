package controller

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"sync"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/pkg/cachex"
	"github.com/LIghtJUNction/api.lmm.best/pkg/syncx"
	"github.com/LIghtJUNction/api.lmm.best/setting"
)

const (
	assistantResponseCacheNamespace = "new-api:assistant-response:v2"
	assistantCacheMaxEntries        = 256
	assistantCacheMaxBytes          = 8 << 20
	assistantCacheMaxValueBytes     = 256 << 10
	assistantCacheMaxGates          = 256
	assistantCacheGateMaxWait       = 250 * time.Millisecond
)

// Personal skills are deliberately request-scoped.  A cached first-turn
// answer must never stand in for a fresh recall of the signed-in user's
// memories or profile: those skills can change between two otherwise
// identical questions.  Keep this guard narrow so ordinary service questions
// still benefit from the bounded response cache.
var assistantPersonalSkillCacheTerms = []string{
	"记忆", "记住", "记得", "回忆", "回想", "忘记", "画像", "标签",
	"memory", "remember", "recall", "forget", "profile", "preferences",
}

type assistantCachedResponse struct {
	Status            int    `json:"status"`
	Body              []byte `json:"body"`
	ConversationTitle string `json:"conversation_title,omitempty"`
}

var (
	assistantResponseCacheOnce sync.Once
	assistantResponseCache     *cachex.HybridCache[assistantCachedResponse]
	assistantCacheGates        = syncx.NewKeyedGate(assistantCacheMaxGates)
)

// Cache coalescing runs before the agent deadline and before SSE starts.
// Wait only briefly for an identical request; on expiry the caller continues
// uncached under the existing global agent limiter. Never cancel the parent
// request or wait for an entire slow model/tool run merely to check its cache.
func acquireAssistantCacheGate(ctx context.Context, key string) (func(), bool) {
	if ctx.Err() != nil {
		return func() {}, false
	}
	waitCtx, cancel := context.WithTimeout(ctx, assistantCacheGateMaxWait)
	defer cancel()
	return assistantCacheGates.Acquire(waitCtx, key)
}

func getAssistantResponseCache() *cachex.HybridCache[assistantCachedResponse] {
	assistantResponseCacheOnce.Do(func() {
		assistantResponseCache = cachex.NewHybridCache[assistantCachedResponse](cachex.HybridCacheConfig[assistantCachedResponse]{
			Namespace: cachex.Namespace(assistantResponseCacheNamespace),
			// Keep assistant response caching local and bounded.  A Redis-backed
			// response cache would bypass the byte/entry limits below because
			// HybridCache intentionally treats Redis as an unbounded shared store;
			// every unique user question could otherwise accumulate indefinitely.
			// These responses are user-scoped and short-lived, so a per-process
			// cache miss is safer than turning Redis into an unbounded transcript
			// store.
			Redis:        nil,
			RedisEnabled: func() bool { return false },
			RedisCodec:   cachex.JSONCodec[assistantCachedResponse]{},
			MemoryStore: func() cachex.MemoryCache[assistantCachedResponse] {
				return cachex.NewByteCache[assistantCachedResponse](
					assistantCacheMaxEntries,
					assistantCacheMaxBytes,
					func(key string, value assistantCachedResponse) int64 {
						return int64(len(key) + len(value.Body) + len(value.ConversationTitle) + 16)
					},
				)
			},
		})
	})
	return assistantResponseCache
}

type assistantCacheContext struct {
	UserID                   int                      `json:"user_id"`
	Username                 string                   `json:"username,omitempty"`
	Email                    string                   `json:"email,omitempty"`
	EmailDomain              string                   `json:"email_domain,omitempty"`
	EmailCategory            string                   `json:"email_category,omitempty"`
	AuthProviders            []string                 `json:"auth_providers,omitempty"`
	AccessLevel              string                   `json:"access_level"`
	DeveloperAccessGranted   bool                     `json:"developer_access_granted"`
	AccessReviewStatus       string                   `json:"access_review_status,omitempty"`
	PaymentMethodsHidden     bool                     `json:"payment_methods_hidden"`
	PaymentRestrictionCauses []string                 `json:"payment_restriction_causes,omitempty"`
	CustomerProfile          assistantCustomerProfile `json:"customer_profile"`
	Intent                   string                   `json:"current_intent,omitempty"`
	ProfileSignals           []string                 `json:"profile_signals,omitempty"`
}

func toAssistantCacheContext(context assistantUserContext) assistantCacheContext {
	return assistantCacheContext{
		UserID:                   context.UserID,
		Username:                 context.Username,
		Email:                    context.Email,
		EmailDomain:              context.EmailDomain,
		EmailCategory:            context.EmailCategory,
		AuthProviders:            append([]string(nil), context.AuthProviders...),
		AccessLevel:              context.AccessLevel,
		DeveloperAccessGranted:   context.DeveloperAccessGranted,
		AccessReviewStatus:       context.AccessReviewStatus,
		PaymentMethodsHidden:     context.PaymentMethodsHidden,
		PaymentRestrictionCauses: append([]string(nil), context.PaymentRestrictionCauses...),
		CustomerProfile:          context.CustomerProfile,
		Intent:                   context.Intent,
		ProfileSignals:           append([]string(nil), context.ProfileSignals...),
	}
}

func assistantCacheConversation(conversation []assistantOpenAIMessage) []assistantOpenAIMessage {
	result := make([]assistantOpenAIMessage, len(conversation))
	copy(result, conversation)
	for index := range result {
		result[index].Content = strings.ToLower(strings.Join(strings.Fields(result[index].Content), " "))
	}
	return result
}

func assistantCacheKey(settings setting.AssistantSettings, conversation []assistantOpenAIMessage, contexts ...assistantUserContext) string {
	if !settings.CacheEnabled || settings.CacheTTLMinutes <= 0 || len(conversation) != 1 || conversation[0].Role != "user" {
		return ""
	}
	if assistantConversationUsesPersonalSkills(conversation) {
		return ""
	}
	var userContext assistantUserContext
	if len(contexts) > 0 {
		userContext = contexts[0]
	}
	// Title generation is metadata work for a new conversation. It must not
	// change the identity of the user's question, otherwise a replay after a
	// lost first response cannot hit the response that was already cached.
	cachePromptContext := userContext
	cachePromptContext.ConversationTitleNeeded = false

	fingerprint := struct {
		Version          string                   `json:"version"`
		Group            string                   `json:"group"`
		Model            string                   `json:"model"`
		SystemPrompt     string                   `json:"system_prompt"`
		AgentLoopEnabled bool                     `json:"agent_loop_enabled"`
		MaxSteps         int                      `json:"max_steps"`
		TimeoutSeconds   int                      `json:"timeout_seconds"`
		TTLMinutes       int                      `json:"ttl_minutes"`
		UserContext      assistantCacheContext    `json:"user_context"`
		Conversation     []assistantOpenAIMessage `json:"conversation"`
	}{
		Version:          "assistant-cache-v2",
		Group:            settings.Group,
		Model:            settings.Model,
		SystemPrompt:     buildAssistantSystemPrompt(settings, cachePromptContext),
		AgentLoopEnabled: settings.AgentLoopEnabled,
		MaxSteps:         settings.MaxSteps,
		TimeoutSeconds:   settings.TimeoutSeconds,
		TTLMinutes:       settings.CacheTTLMinutes,
		UserContext:      toAssistantCacheContext(userContext),
		Conversation:     assistantCacheConversation(conversation),
	}
	raw, err := json.Marshal(fingerprint)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func assistantConversationUsesPersonalSkills(conversation []assistantOpenAIMessage) bool {
	for _, message := range conversation {
		if message.Role != "user" {
			continue
		}
		text := strings.ToLower(strings.TrimSpace(message.Content))
		if assistantTextContainsAny(text, assistantPersonalSkillCacheTerms...) {
			return true
		}
	}
	return false
}

func getAssistantCachedResponse(key string) (assistantCachedResponse, bool) {
	if key == "" {
		return assistantCachedResponse{}, false
	}
	value, found, err := getAssistantResponseCache().Get(key)
	if err != nil {
		common.SysLog("assistant response cache read failed: " + err.Error())
		return assistantCachedResponse{}, false
	}
	if !found || value.Status < 200 || value.Status >= 300 || len(value.Body) == 0 {
		return assistantCachedResponse{}, false
	}

	normalized, err := normalizeAssistantClientResponse(nil, value.Body)
	if err != nil {
		return assistantCachedResponse{}, false
	}
	value.Body = normalized
	return value, true
}

func storeAssistantCachedResponse(settings setting.AssistantSettings, key string, status int, body []byte, conversationTitles ...string) {
	if key == "" || !settings.CacheEnabled || settings.CacheTTLMinutes <= 0 || status < 200 || status >= 300 || len(body) == 0 || len(body) > assistantCacheMaxValueBytes {
		return
	}
	normalized, err := normalizeAssistantClientResponse(nil, body)
	if err != nil {
		return
	}
	conversationTitle := ""
	if len(conversationTitles) > 0 {
		conversationTitle = strings.TrimSpace(conversationTitles[0])
	}
	value := assistantCachedResponse{Status: status, Body: normalized, ConversationTitle: conversationTitle}
	if err := getAssistantResponseCache().SetWithTTL(key, value, time.Duration(settings.CacheTTLMinutes)*time.Minute); err != nil {
		common.SysLog("assistant response cache write failed: " + err.Error())
	}
}
