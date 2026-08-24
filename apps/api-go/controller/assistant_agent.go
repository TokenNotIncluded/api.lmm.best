package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/constant"
	"github.com/LIghtJUNction/api.lmm.best/internal/agent"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/pkg/syncx"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/types"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/LIghtJUNction/api.lmm.best/setting"
	"github.com/LIghtJUNction/api.lmm.best/setting/operation_setting"
	"github.com/LIghtJUNction/api.lmm.best/setting/ratio_setting"
	"github.com/LIghtJUNction/api.lmm.best/setting/system_setting"
	"github.com/expr-lang/expr"
	"github.com/gin-gonic/gin"
)

const (
	assistantToolArgumentsMaxBytes        = 16 * 1024
	assistantToolCallsPerTurn             = 4
	assistantAgentDefaultTimeout          = 45 * time.Second
	assistantUpstreamMaxAttempts          = 3
	assistantUpstreamRetryBaseDelay       = 200 * time.Millisecond
	assistantRecommendationTTL            = 30 * time.Minute
	minDeveloperAccessReasonRunes         = 5
	minDeveloperAccessRecommendationRunes = 20
	maxDeveloperAccessDraftRunes          = 2000
	assistantInterlocutorAssessmentTool   = "assess_l0_interlocutor"
	assistantMathExpressionMaxBytes       = 512
	assistantMathVariablesMax             = 32
	assistantConversationTitleMaxRunes    = 60
	assistantUpstreamRequestMaxBytes      = 768 << 10
	assistantUpstreamResponseMaxBytes     = 256 << 10
	assistantToolResultMaxBytes           = 64 << 10
	assistantAgentContextMaxBytes         = 512 << 10
	assistantAgentMaxConcurrent           = 16
)

var (
	assistantMathExpressionPattern = regexp.MustCompile(`^[0-9A-Za-z_+\-*/%^().,\s]+$`)
	assistantMathVariablePattern   = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,31}$`)
	// Require a model-like suffix after the provider family. Without this
	// boundary, ordinary phrases such as “Claude Code” look like a model ID and
	// unnecessarily force a catalog read on every client-setup question.
	assistantModelReferencePattern = regexp.MustCompile(`(?i)\b(?:gpt|claude|gemini|deepseek|qwen|llama|mistral|kimi|glm|codex)(?:(?:[-._:/][a-z0-9]+|[0-9])[a-z0-9._:/-]*|(?:\s+[a-z][a-z0-9._:/-]*){0,2}\s+[a-z0-9._:/-]*[0-9][a-z0-9._:/-]*(?:\s+[a-z0-9._:/-]+){0,3})\b`)
	// Keep discovery open to newly configured providers. The live pricing
	// catalog is the source of truth, so model-reference detection must not
	// require adding every future provider to the allowlist above. This
	// fallback recognizes compact IDs such as minimax-m3, grok-4.6,
	// mimo-v2.5-pro, and seed-2.1-pro while still rejecting ordinary words
	// such as “Claude Code”.
	assistantGenericModelReferencePattern = regexp.MustCompile(`(?i)\b[a-z][a-z0-9]*(?:[-._][a-z0-9]*[0-9][a-z0-9]*(?:[-._][a-z0-9]+)*)+\b`)
	// A live catalog may also contain stable, non-versioned IDs such as
	// "codex-auto-review". Keep those discoverable without cloning the full
	// pricing snapshot for ordinary prose that merely contains a number or a
	// hyphenated phrase.
	assistantCatalogModelCandidatePattern = regexp.MustCompile(`(?i)\b(?:[a-z][a-z0-9]*[0-9][a-z0-9]*|[a-z][a-z0-9]*(?:[-._:/][a-z0-9]+)+)\b`)
	assistantAgentLimiter                 = syncx.NewLimiter(assistantAgentMaxConcurrent)
	assistantTools                        = sync.OnceValue(buildAssistantTools)
)

type assistantL1RecommendationDraft struct {
	UserStatement    string `json:"user_statement"`
	Recommendation   string `json:"recommendation"`
	PresetId         string `json:"preset_id,omitempty"`
	PresetGeneration int64  `json:"preset_generation,omitempty"`
	PresetVersion    string `json:"preset_version,omitempty"`
}

type assistantOpenAIToolDefinition = agent.Tool
type assistantOpenAIToolFunction = agent.Function
type assistantOpenAIToolCall = agent.Call
type assistantOpenAIToolCallFunction = agent.CallFunction
type assistantOpenAIResponse = agent.Response
type assistantOpenAIResponseChoice = agent.Choice
type assistantOpenAIResponseMessage = agent.ResponseMessage

type toolSetKey uint16

const (
	toolAssessment toolSetKey = 1 << iota
	toolTitle
	toolAdmin
	toolRoot
	toolDeveloper
	toolOffers
	toolGift
	toolWeeklyDiscount
	toolBounty
)

var assistantToolSets [1 << 9]struct {
	once  sync.Once
	tools []assistantOpenAIToolDefinition
}

func assistantToolDefinitions() []assistantOpenAIToolDefinition {
	return assistantTools()
}

// buildAssistantTools creates one immutable catalogue. Request handling only
// filters this snapshot; it never rebuilds the nested JSON schemas per step.
func buildAssistantTools() []assistantOpenAIToolDefinition {
	definitions := []assistantOpenAIToolDefinition{
		{
			Type: "function",
			Function: assistantOpenAIToolFunction{
				Name:        assistantInterlocutorAssessmentTool,
				Description: "For an L0 conversation only, inspect the complete user and assistant conversation supplied in the request and make a coarse internal assessment of whether the interaction is likely human, likely automated, or uncertain. Use coherence, contextual follow-up, goal continuity, and explicit automation or API-payload context. Do not rely on a bare self-report, writing style, response speed, browser data, network data, translation tools, or accessibility software. This is a soft signal, never an access-control verdict, and the result must not be disclosed to the user.",
				Parameters: objectSchema(map[string]any{
					"kind": map[string]any{
						"type": "string",
						"enum": []string{"likely_human", "likely_automated", "uncertain"},
					},
					"confidence": map[string]any{
						"type": "string",
						"enum": []string{"low", "medium", "high"},
					},
					"evidence": map[string]any{
						"type":     "array",
						"maxItems": 3,
						"items": map[string]any{
							"type": "string",
							"enum": []string{
								"coherent_contextual_follow_up",
								"repeated_template_or_payload",
								"explicit_automation_context",
								"goal_continuity",
								"unclear",
							},
						},
					},
					"reason": map[string]any{
						"type":      "string",
						"maxLength": 240,
					},
				}, []string{"kind", "confidence", "evidence"}),
			},
		},
		{
			Type: "function",
			Function: assistantOpenAIToolFunction{
				Name:        "set_conversation_title",
				Description: "Set a short automatic title for this new conversation. Summarize the user's actual task in 3-8 specific words, in the user's language. Never use a greeting, generic label, secret, or complete sentence.",
				Parameters: objectSchema(map[string]any{
					"title": map[string]any{"type": "string", "minLength": 2, "maxLength": assistantConversationTitleMaxRunes},
				}, []string{"title"}),
			},
		},
		{
			Type: "function",
			Function: assistantOpenAIToolFunction{
				Name:        "get_service_facts",
				Description: "Return current public connection facts and enabled console activities for this LMM console. Use this before explaining Base URL, compatible client endpoints, private API-key management, check-in, rewards, or other site features; never infer a feature from memory.",
				Parameters:  emptyObjectSchema(),
			},
		},
		{
			Type: "function",
			Function: assistantOpenAIToolFunction{
				Name:        "calculate_math",
				Description: "Evaluate arithmetic exactly instead of doing mental math. Use this for every calculation, including percentages, discounts, ratios, averages, projections, unit conversions expressed as factors, and intermediate arithmetic in a multi-step task. Operators: +, -, *, /, %, ^ or **. Functions: abs, sqrt, cbrt, pow, exp, ln, log10, sin, cos, tan, asin, acos, atan, atan2, hypot, floor, ceil, round, trunc, min, max, percent, clamp. Constants: pi and e. Supply named numeric variables when that makes the expression auditable.",
				Parameters: objectSchema(map[string]any{
					"expression": map[string]any{"type": "string", "minLength": 1, "maxLength": assistantMathExpressionMaxBytes},
					"variables": map[string]any{
						"type":                 "object",
						"additionalProperties": map[string]any{"type": "number"},
					},
				}, []string{"expression"}),
			},
		},
		{
			Type: "function",
			Function: assistantOpenAIToolFunction{
				Name:        "calculate_cost",
				Description: "Calculate an estimated USD cost from token counts and supplied per-million-token prices. Never invent prices; ask for missing prices when needed.",
				Parameters: objectSchema(map[string]any{
					"input_tokens":           map[string]any{"type": "number", "minimum": 0},
					"output_tokens":          map[string]any{"type": "number", "minimum": 0},
					"input_usd_per_million":  map[string]any{"type": "number", "minimum": 0},
					"output_usd_per_million": map[string]any{"type": "number", "minimum": 0},
					"group_ratio":            map[string]any{"type": "number", "minimum": 0},
				}, []string{"input_tokens", "output_tokens", "input_usd_per_million", "output_usd_per_million"}),
			},
		},
		{
			Type: "function",
			Function: assistantOpenAIToolFunction{
				Name:        "get_account_access",
				Description: "Read the signed-in user's non-secret access state, such as trust level and whether developer features are unlocked.",
				Parameters:  emptyObjectSchema(),
			},
		},
		{
			Type: "function",
			Function: assistantOpenAIToolFunction{
				Name:        "get_l1_recommendation",
				Description: "Read the signed-in user's one current L1 access recommendation letter and review status. Call this before discussing, drafting, polishing, replacing, or removing the in-console recommendation. This is the authoritative shared letter visible to the user and administrators.",
				Parameters:  emptyObjectSchema(),
			},
		},
		{
			Type: "function",
			Function: assistantOpenAIToolFunction{
				Name:        "get_available_models",
				Description: "Return the model IDs and usable routing groups available to the signed-in user. Never invent a model ID.",
				Parameters:  emptyObjectSchema(),
			},
		},
		{
			Type: "function",
			Function: assistantOpenAIToolFunction{
				Name:        "get_model_pricing",
				Description: "Return the signed-in user's live per-group prices for one exact model ID. Call this before calculating cost; if the user has not chosen a model, ask them or call get_available_models first.",
				Parameters: objectSchema(map[string]any{
					"model_id": map[string]any{"type": "string", "minLength": 1, "maxLength": 200},
					"group":    map[string]any{"type": "string", "maxLength": 64},
				}, []string{"model_id"}),
			},
		},
		{
			Type: "function",
			Function: assistantOpenAIToolFunction{
				Name:        "get_plan_offers",
				Description: "Return current enabled subscription plans and configured top-up discounts for comparison. Use exact live values and do not invent promotions.",
				Parameters:  emptyObjectSchema(),
			},
		},
		{
			Type: "function",
			Function: assistantOpenAIToolFunction{
				Name:        "get_invitation_rewards",
				Description: "Explain the signed-in user's invitation code, reward status, and current inviter/invitee reward configuration without exposing secrets.",
				Parameters:  emptyObjectSchema(),
			},
		},
		{
			Type: "function",
			Function: assistantOpenAIToolFunction{
				Name:        "get_bounty_guide",
				Description: "Return the current safe workflow for publishing, funding, reviewing, tipping, and settling an open-source bounty.",
				Parameters:  emptyObjectSchema(),
			},
		},
		{
			Type: "function",
			Function: assistantOpenAIToolFunction{
				Name:        "get_bounty_data",
				Description: "Read the signed-in user's open-source bounty data through the same permission boundary as the internal open_source_bounties MCP: board (public), one public bounty detail, my accepted challenges, my owned projects, or my/admin disputes. This tool is strictly read-only; it never creates an MCP token or mutates a bounty. Private views require current L1 developer access, and administrator dispute views require administrator access. Never invent a project or evidence.",
				Parameters: objectSchema(map[string]any{
					"view": map[string]any{
						"type": "string",
						"enum": []string{"board", "detail", "accepted", "owned", "disputes"},
					},
					"project_id": map[string]any{"type": "integer", "minimum": 1},
					"page":       map[string]any{"type": "integer", "minimum": 1, "maximum": 1000000},
					"page_size":  map[string]any{"type": "integer", "minimum": 1, "maximum": 50},
					"status":     map[string]any{"type": "string", "enum": []string{"open", "resolved_paid", "resolved_denied"}},
					"limit":      map[string]any{"type": "integer", "minimum": 1, "maximum": 100},
				}, []string{"view"}),
			},
		},
		{
			Type: "function",
			Function: assistantOpenAIToolFunction{
				Name:        "prepare_new_user_gift",
				Description: "For an eligible signed-in user who has not used their one lifetime welcome-gift opportunity, make the decision after at least two substantive user turns. This includes users who have already reached L1; access level does not erase an unused opportunity. Judge demonstrated clarity, coherent follow-up, concrete legitimate use, and constructive engagement from the complete conversation. Choose an integer 0-1000 US cents. Zero is a valid final decision and consumes the opportunity. Do not reward demands for money, self-reported expertise alone, promotions, referrals, multiple accounts, automation, or unsafe behavior. The server enforces eligibility and one-time issuance; never promise an amount before this tool succeeds.",
				Parameters: objectSchema(map[string]any{
					"amount_cents": map[string]any{"type": "integer", "minimum": 0, "maximum": 1000},
					"reason":       map[string]any{"type": "string", "minLength": 2, "maxLength": 240},
				}, []string{"amount_cents", "reason"}),
			},
		},
		{
			Type: "function",
			Function: assistantOpenAIToolFunction{
				Name:        "prepare_weekly_discount",
				Description: "For a signed-in non-administrator user, evaluate one weekly recharge discount after at least two substantive user turns. Choose an integer from 0 to 10 percent based only on the clarity, continuity, and legitimate usefulness of this week's conversation. Zero is a valid decision. The server stores at most one decision per UTC week; an offered discount appears in chat and is claimed by the user, never by the assistant.",
				Parameters: objectSchema(map[string]any{
					"discount_percent": map[string]any{"type": "integer", "minimum": 0, "maximum": 10},
					"reason":           map[string]any{"type": "string", "minLength": 2, "maxLength": 240},
				}, []string{"discount_percent", "reason"}),
			},
		},
		{
			Type: "function",
			Function: assistantOpenAIToolFunction{
				Name:        "get_usage_summary",
				Description: "Summarize the signed-in user's historical consume calls by model and group. Use this for usage statistics instead of exposing raw logs.",
				Parameters: objectSchema(map[string]any{
					"days": map[string]any{"type": "integer", "minimum": 1, "maximum": 90},
				}, nil),
			},
		},
		{
			Type: "function",
			Function: assistantOpenAIToolFunction{
				Name:        "navigate_to_page",
				Description: "Navigate the signed-in user to one allowlisted page inside this LMM console. Use this when the user asks to open, jump to, or locate something. For the users or usage-log page, identifier may be a username, email, or numeric user ID; regular users may only target themselves and administrators may only target users in their permitted scope.",
				Parameters: objectSchema(map[string]any{
					"page": map[string]any{
						"type": "string",
						"enum": []string{"home", "getting-started", "pricing", "wallet", "usage-logs", "keys", "drawing", "models", "profile", "support", "open-source-bounties", "users"},
					},
					"identifier": map[string]any{"type": "string", "maxLength": 200},
					"query":      map[string]any{"type": "string", "maxLength": 200},
					"section":    map[string]any{"type": "string", "enum": []string{"common", "drawing", "task"}},
				}, []string{"page"}),
			},
		},
		{
			Type: "function",
			Function: assistantOpenAIToolFunction{
				Name:        "prepare_image_generation",
				Description: "For an L1 user, prepare one image-generation request through the drawing workbench. Use the live catalog and an exact image-capable model; prefer the live image-2 group/model only when the catalog exposes them, otherwise ask the user to choose. This only creates a short-lived confirmation card; it never spends quota until the user confirms in the UI.",
				Parameters: objectSchema(map[string]any{
					"prompt":  map[string]any{"type": "string", "minLength": 1, "maxLength": assistantDrawingPromptMaxRunes},
					"model":   map[string]any{"type": "string", "maxLength": 200},
					"group":   map[string]any{"type": "string", "maxLength": 64},
					"size":    map[string]any{"type": "string", "maxLength": 32},
					"quality": map[string]any{"type": "string", "maxLength": 32},
					"n":       map[string]any{"type": "integer", "minimum": 1, "maximum": assistantDrawingMaxImages},
				}, []string{"prompt"}),
			},
		},
		{
			Type: "function",
			Function: assistantOpenAIToolFunction{
				Name:        "get_user_overview",
				Description: "Read a sanitized account overview. With no target, read the signed-in user's own account. An administrator may provide a username, email, or numeric ID for a permitted lower-role user. Never returns passwords, access tokens, OAuth subject IDs, or raw request content.",
				Parameters: objectSchema(map[string]any{
					"user_id":    map[string]any{"type": "integer", "minimum": 1},
					"identifier": map[string]any{"type": "string", "maxLength": 200},
				}, nil),
			},
		},
		{
			Type: "function",
			Function: assistantOpenAIToolFunction{
				Name:        "get_user_usage_summary",
				Description: "Analyze aggregate usage for the signed-in user, or for a permitted target when the caller is an administrator. Returns totals and model/group aggregates only, never raw logs or request content.",
				Parameters: objectSchema(map[string]any{
					"user_id":    map[string]any{"type": "integer", "minimum": 1},
					"identifier": map[string]any{"type": "string", "maxLength": 200},
					"days":       map[string]any{"type": "integer", "minimum": 1, "maximum": 90},
				}, nil),
			},
		},
		{
			Type: "function",
			Function: assistantOpenAIToolFunction{
				Name:        "prepare_user_action",
				Description: "Prepare a confirmation card for a safe user-account action. Supported actions are change_password, bind_oauth, unbind_oauth, disable, and delete. Never pass a password or secret to this tool. Regular users can act only on themselves; administrators can act only on permitted lower-role targets. OAuth binding is interactive and must be completed by the target user in their own session.",
				Parameters: objectSchema(map[string]any{
					"action":     map[string]any{"type": "string", "enum": []string{"change_password", "bind_oauth", "unbind_oauth", "disable", "delete"}},
					"user_id":    map[string]any{"type": "integer", "minimum": 1},
					"identifier": map[string]any{"type": "string", "maxLength": 200},
					"provider":   map[string]any{"type": "string", "maxLength": 120},
				}, []string{"action"}),
			},
		},
		{
			Type: "function",
			Function: assistantOpenAIToolFunction{
				Name:        "search_web",
				Description: "Search the administrator-configured web search API for current software installation or platform information. If no search API is configured, report that limitation.",
				Parameters: objectSchema(map[string]any{
					"query": map[string]any{"type": "string", "minLength": 2, "maxLength": 500},
				}, []string{"query"}),
			},
		},
		{
			Type: "function",
			Function: assistantOpenAIToolFunction{
				Name:        "get_setup_guide",
				Description: "Return verified platform-specific install commands and gateway configuration for Claude Code, CC Switch, Claude Desktop, Codex, and compatible clients. model_id must be an exact value returned by get_available_models for this account; use this tool instead of guessing client capabilities, models, or endpoint formats.",
				Parameters: objectSchema(map[string]any{
					"platform": map[string]any{"type": "string", "enum": []string{"windows", "linux", "macos"}},
					"topic":    map[string]any{"type": "string", "enum": []string{"claude-code", "cc-switch", "claude-desktop", "chatgpt-client", "codex", "cursor", "open-webui", "other-openai-compatible"}},
					"model_id": map[string]any{"type": "string", "minLength": 1, "maxLength": 200},
				}, []string{"platform", "topic", "model_id"}),
			},
		},
		{
			Type: "function",
			Function: assistantOpenAIToolFunction{
				Name:        "prepare_l1_recommendation",
				Description: "Prepare a new or revised draft of the signed-in L0 user's one shared administrator recommendation after a substantive conversation. For an edit, use the current letter returned by get_l1_recommendation and the full conversation. Never use this tool to remove a letter. This does not submit, update, delete, or approve anything; the user must explicitly confirm the draft in the UI.",
				Parameters: objectSchema(map[string]any{
					"user_statement": map[string]any{"type": "string", "minLength": 5, "maxLength": 2000},
					"recommendation": map[string]any{"type": "string", "minLength": 20, "maxLength": 2000},
				}, []string{"user_statement", "recommendation"}),
			},
		},
		{
			Type: "function",
			Function: assistantOpenAIToolFunction{
				Name:        "request_create_key",
				Description: "Prepare creation of an API key. First call without a group to load the signed-in user's live group choices, then ask the user to choose one exact group. If that group has a configured warning, show it and set accept_group_warning=true only after the user explicitly accepts it. Only after that choice may you request explicit confirmation; never claim a key was created from this tool.",
				Parameters: objectSchema(map[string]any{
					"name":                 map[string]any{"type": "string", "maxLength": 50},
					"group":                map[string]any{"type": "string", "maxLength": 64},
					"accept_group_warning": map[string]any{"type": "boolean"},
				}, nil),
			},
		},
		{
			Type: "function",
			Function: assistantOpenAIToolFunction{
				Name:        "request_human_support",
				Description: "Prepare a handoff to an administrator. The message is required and must contain at least 5 characters. Use action=disable_account only when the conversation has established a concrete account-safety reason; this creates an administrator review request and never disables an account directly. Any write action requires explicit confirmation in the UI.",
				Parameters: objectSchema(map[string]any{
					"message":        map[string]any{"type": "string", "minLength": 5, "maxLength": 2000},
					"action":         map[string]any{"type": "string", "enum": []string{"support", "disable_account"}},
					"target_user_id": map[string]any{"type": "integer", "minimum": 1},
				}, []string{"message"}),
			},
		},
		{
			Type: "function",
			Function: assistantOpenAIToolFunction{
				Name:        "get_admin_assistant_review",
				Description: "For an administrator only, read the latest privacy-minimized automatic assistant review. It contains bounded aggregate intent, profile, preset-conversion, chat-to-purchase conversion, order, and refund signals plus support-queue and security follow-ups; it never contains transcripts, user identities, or per-user memory. Use it before proposing changes to AssistantSkills.",
				Parameters:  emptyObjectSchema(),
			},
		},
		{
			Type: "function",
			Function: assistantOpenAIToolFunction{
				Name:        "get_admin_user_skills",
				Description: "For an administrator only, read one lower-role user's bounded assistant profile and long-term memory skills. target_user_id is required. The server enforces the same strict higher-role visibility lattice as conversation history; peer or higher-role administrators are denied. Never reveal secrets or use these skills as access-control or risk labels.",
				Parameters: objectSchema(map[string]any{
					"target_user_id": map[string]any{"type": "integer", "minimum": 1},
				}, []string{"target_user_id"}),
			},
		},
		{
			Type: "function",
			Function: assistantOpenAIToolFunction{
				Name:        "prepare_admin_user_skill_change",
				Description: "For an administrator only, prepare a confirmation-gated edit to one permitted lower-role user's assistant memory or profile skill. Use get_admin_user_skills first. This never writes immediately; the administrator must confirm the exact preview in the UI. Memory deletes require memory_id. Never store credentials, payment data, protected traits, or security labels.",
				Parameters: objectSchema(map[string]any{
					"target_user_id": map[string]any{"type": "integer", "minimum": 1},
					"kind":           map[string]any{"type": "string", "enum": []string{"memory", "profile"}},
					"operation":      map[string]any{"type": "string", "enum": []string{"upsert", "delete"}},
					"memory_id":      map[string]any{"type": "integer", "minimum": 1},
					"title":          map[string]any{"type": "string", "maxLength": model.AssistantMemoryMaxTitleRunes},
					"content":        map[string]any{"type": "string", "maxLength": model.AssistantMemoryMaxContentRunes},
					"tags":           map[string]any{"type": "array", "maxItems": model.AssistantUserProfileMaxTags, "items": map[string]any{"type": "string", "maxLength": model.AssistantUserProfileMaxTagRunes}},
					"profile_key":    map[string]any{"type": "string", "maxLength": 64},
					"strategy":       map[string]any{"type": "string", "maxLength": model.AssistantUserProfileMaxStrategyRunes},
					"enabled":        map[string]any{"type": "boolean"},
				}, []string{"target_user_id", "kind"}),
			},
		},
		{
			Type: "function",
			Function: assistantOpenAIToolFunction{
				Name:        "get_admin_server_config",
				Description: "For an administrator only, read the current non-secret server configuration that the assistant can safely manage. Credentials, provider keys, payment secrets, session secrets, and arbitrary shell or database access are always omitted.",
				Parameters:  emptyObjectSchema(),
			},
		},
		{
			Type: "function",
			Function: assistantOpenAIToolFunction{
				Name:        "prepare_admin_config_change",
				Description: "For an administrator only, prepare an exact preview of one or more allowlisted non-secret server settings. This never applies a change; the administrator must confirm the preview in the UI.",
				Parameters: objectSchema(map[string]any{
					"changes": map[string]any{
						"type":                 "object",
						"additionalProperties": true,
					},
				}, []string{"changes"}),
			},
		},
		{
			Type: "function",
			Function: assistantOpenAIToolFunction{
				Name:        "get_admin_channels",
				Description: "For an administrator only, list channel routing metadata and manual status. Channel keys, provider credentials, headers, proxies, upstream URLs, balances, and private settings are always omitted.",
				Parameters:  emptyObjectSchema(),
			},
		},
		{
			Type: "function",
			Function: assistantOpenAIToolFunction{
				Name:        "prepare_admin_channel_change",
				Description: "For an administrator only, prepare an exact preview for safe channel routing metadata or enable/disable status. This never applies a change; the administrator must confirm the preview in the UI. Never request keys, provider settings, headers, proxies, or upstream URLs through this tool.",
				Parameters: objectSchema(map[string]any{
					"channel_id": map[string]any{"type": "integer", "minimum": 1},
					"changes": map[string]any{
						"type":                 "object",
						"additionalProperties": true,
					},
				}, []string{"channel_id", "changes"}),
			},
		},
		{
			Type: "function",
			Function: assistantOpenAIToolFunction{
				Name:        "get_admin_model_inventory",
				Description: "For an administrator only, read the live enabled model IDs, configured routing groups, and the bounded list of model IDs referenced by channels but missing local metadata. This is read-only, omits provider secrets, and does not verify whether those IDs exist in the upstream catalog.",
				Parameters:  emptyObjectSchema(),
			},
		},
		{
			Type: "function",
			Function: assistantOpenAIToolFunction{
				Name:        "prepare_admin_model_sync",
				Description: "For a root administrator only, verify selected locally-missing model IDs against the live upstream catalog and prepare a confirmation-gated import for the IDs found there. Call get_admin_model_inventory first; do not claim an ID is available upstream until this tool returns its preview. This never writes immediately; the UI must show the exact metadata and skipped IDs, and the administrator must confirm.",
				Parameters: objectSchema(map[string]any{
					"model_ids": map[string]any{"type": "array", "maxItems": assistantAdminMaxModelSyncItems, "items": map[string]any{"type": "string", "maxLength": assistantAdminMaxModelNameRunes}},
					"locale":    map[string]any{"type": "string", "enum": []string{"en", "zh-CN", "zh-TW", "ja"}},
				}, nil),
			},
		},
		{
			Type: "function",
			Function: assistantOpenAIToolFunction{
				Name:        "prepare_admin_pricing_change",
				Description: "For an administrator only, prepare an exact preview for one enabled model's pricing. Use ratio for token pricing or fixed_request for a per-request price; optional completion, cache, image, and audio ratios update the same exact model. This never applies a change; the administrator must confirm the preview in the UI.",
				Parameters: objectSchema(map[string]any{
					"model_id":               map[string]any{"type": "string", "minLength": 1, "maxLength": 200},
					"mode":                   map[string]any{"type": "string", "enum": []string{"ratio", "fixed_request"}},
					"value":                  map[string]any{"type": "number", "minimum": 0, "maximum": 1000000},
					"completion_ratio":       map[string]any{"type": "number", "minimum": 0, "maximum": 1000000},
					"cache_ratio":            map[string]any{"type": "number", "minimum": 0, "maximum": 1000000},
					"create_cache_ratio":     map[string]any{"type": "number", "minimum": 0, "maximum": 1000000},
					"image_ratio":            map[string]any{"type": "number", "minimum": 0, "maximum": 1000000},
					"audio_ratio":            map[string]any{"type": "number", "minimum": 0, "maximum": 1000000},
					"audio_completion_ratio": map[string]any{"type": "number", "minimum": 0, "maximum": 1000000},
				}, []string{"model_id", "mode", "value"}),
			},
		},
	}
	return append(definitions, assistantSkillTools()...)
}

// assistantToolDefinitionsForContext keeps the model's tool catalogue aligned
// with the signed-in account.  Execution still performs the same checks as a
// second line of defence, but an L0 model should not be invited to speculate
// about administrator tools or account-specific pricing in the first place.
func assistantToolDefinitionsForContext(userContext assistantUserContext) []assistantOpenAIToolDefinition {
	set := &assistantToolSets[keyForTools(userContext)]
	set.once.Do(func() {
		all := assistantToolDefinitions()
		set.tools = make([]assistantOpenAIToolDefinition, 0, len(all))
		for _, definition := range all {
			if assistantToolAllowedForContext(definition.Function.Name, userContext) {
				set.tools = append(set.tools, definition)
			}
		}
	})
	return set.tools
}

func keyForTools(context assistantUserContext) toolSetKey {
	var key toolSetKey
	if assistantL0InterlocutorAssessmentRequired(context) {
		key |= toolAssessment
	}
	if context.ConversationTitleNeeded {
		key |= toolTitle
	}
	if context.AdministratorMode {
		key |= toolAdmin
	}
	if context.AccessLevel == "ROOT" {
		key |= toolRoot
	}
	if context.DeveloperAccessGranted {
		key |= toolDeveloper
	}
	if assistantPaymentOfferStateForContext(context) == assistantPaymentOfferReady {
		key |= toolOffers
	}
	if assistantNewUserGiftToolAllowed(context) {
		key |= toolGift
	}
	if assistantWeeklyDiscountToolAllowed(context) {
		key |= toolWeeklyDiscount
	}
	if assistantBountyReadToolAllowed(context) {
		key |= toolBounty
	}
	return key
}

func assistantNewUserGiftToolAllowed(context assistantUserContext) bool {
	// An unused opportunity survives L0 -> L1 upgrades. Deterministic server
	// checks still reject disabled/disposable/abusive accounts and the unique
	// gift row makes the decision one-time. Keep the existing high-risk and
	// promotion guard at the tool boundary so the assistant does not invite a
	// known-abusive conversation into a reward flow.
	if context.AdministratorMode || context.GiftRewardBlocked {
		return false
	}
	return context.CustomerProfile != assistantProfilePromotion && context.CustomerProfile != assistantProfileSecurityRisk
}

func assistantWeeklyDiscountToolAllowed(context assistantUserContext) bool {
	// The weekly reward is deliberately separate from the one-time welcome
	// gift: a normal promotion question is not itself abuse. Keep the hard
	// security boundary and administrator separation, while letting the model
	// judge conversation quality on the server-backed weekly record.
	return !context.AdministratorMode && context.CustomerProfile != assistantProfileSecurityRisk
}

func assistantToolAllowedForContext(name string, userContext assistantUserContext) bool {
	if assistantL0InterlocutorAssessmentRequired(userContext) {
		return name == assistantInterlocutorAssessmentTool
	}
	if name == assistantInterlocutorAssessmentTool {
		return false
	}
	if name == "set_conversation_title" {
		return userContext.ConversationTitleNeeded
	}
	if name == "prepare_new_user_gift" {
		return assistantNewUserGiftToolAllowed(userContext)
	}
	if name == "prepare_weekly_discount" {
		return assistantWeeklyDiscountToolAllowed(userContext)
	}
	if name == "prepare_image_generation" {
		return common.DrawingEnabled && userContext.DeveloperAccessGranted
	}
	if name == "get_bounty_data" {
		return assistantBountyReadToolAllowed(userContext)
	}
	if userContext.AdministratorMode {
		if userContext.AccessLevel != "ROOT" {
			switch name {
			case "get_admin_server_config", "prepare_admin_config_change", "prepare_admin_pricing_change", "prepare_admin_model_sync":
				return false
			}
		}
		return true
	}
	if userContext.DeveloperAccessGranted {
		return name != "get_admin_assistant_review" &&
			name != "get_admin_server_config" &&
			name != "prepare_admin_config_change" &&
			name != "get_admin_model_inventory" &&
			name != "prepare_admin_model_sync" &&
			name != "get_admin_channels" &&
			name != "prepare_admin_channel_change" &&
			name != "prepare_admin_pricing_change" &&
			name != "get_admin_user_skills" &&
			name != "prepare_admin_user_skill_change"
	}
	if isAssistantSkillTool(name) {
		return true
	}
	switch name {
	case "get_service_facts",
		"calculate_math",
		"calculate_cost",
		"get_account_access",
		"get_l1_recommendation",
		"get_available_models",
		"get_model_pricing",
		"navigate_to_page",
		"get_user_overview",
		"get_user_usage_summary",
		"prepare_user_action",
		"get_bounty_guide",
		"get_bounty_data",
		"search_web",
		"get_setup_guide",
		"prepare_l1_recommendation",
		"request_human_support":
		return true
	case "get_plan_offers":
		// A regular L0 user may see current offers only after the assistant's
		// deterministic payment-intent gate reaches ready. Restriction flags
		// always win and are never overridden by user wording.
		return assistantPaymentOfferStateForContext(userContext) == assistantPaymentOfferReady
	default:
		return false
	}
}

func assistantL0InterlocutorAssessmentRequired(_ assistantUserContext) bool {
	// The model-generated assessment was not persisted or consumed by any
	// authorization or abuse-control decision. Keeping it in the synchronous
	// path doubled model calls for L0 users without adding a security boundary.
	// Any future anti-abuse signal must be collected asynchronously and consumed
	// by a deterministic policy before this gate can be enabled again.
	return false
}

func assistantToolExecutionAllowedForContext(name string, userContext assistantUserContext) bool {
	if name == "get_plan_offers" && !userContext.AdministratorMode && !userContext.DeveloperAccessGranted {
		// Let the read-only plan tool return the deterministic payment-intent or
		// restriction result. It does not expose offers or checkout unless its own
		// policy gate reaches ready.
		return true
	}
	return assistantToolAllowedForContext(name, userContext)
}

func assistantToolChoiceForContext(userContext assistantUserContext) any {
	name := ""
	if userContext.ConversationTitleNeeded {
		name = "set_conversation_title"
	} else if assistantPlanOfferWorkflowRequired(userContext) {
		// A ready, explicit purchase request must read the live offers before
		// the model can answer from stale plan context or invent a price.
		name = "get_plan_offers"
	} else if assistantHumanSupportRequest(userContext.LatestUserRequest) {
		name = "request_human_support"
	} else if assistantPublicActivityQuestion(userContext.LatestUserRequest) {
		name = "get_service_facts"
	} else if assistantNewUserGiftRequest(userContext.LatestUserRequest) {
		name = "prepare_new_user_gift"
	} else if assistantWeeklyDiscountRequest(userContext.LatestUserRequest) {
		name = "prepare_weekly_discount"
	} else {
		switch userContext.Intent {
		case model.AssistantIntentCost, model.AssistantIntentModels:
			name = "get_available_models"
		case model.AssistantIntentMath:
			name = "calculate_math"
		case model.AssistantIntentAPIKey, model.AssistantIntentClientSetup:
			name = "get_service_facts"
		case model.AssistantIntentOnboarding:
			name = "get_account_access"
		case model.AssistantIntentRecommendation:
			name = "get_l1_recommendation"
		case model.AssistantIntentUsage:
			name = "get_usage_summary"
		case model.AssistantIntentInvitation:
			name = "get_invitation_rewards"
		case model.AssistantIntentBounty:
			if assistantBountyReadRequest(userContext.LatestUserRequest) {
				name = "get_bounty_data"
			} else {
				name = "get_bounty_guide"
			}
		}
	}
	// A bare exact model ID (for example “gpt-5.6-sol 能用吗？”) may not
	// contain the words “model” or “模型”, so deterministic intent
	// classification can leave it as `other`. It still must be checked against
	// the live catalog before the model makes an availability claim.
	if name == "" && assistantHasModelReference(userContext.LatestUserRequest) {
		name = "get_available_models"
	}
	if name == "" && assistantExplicitImageRequest(userContext.LatestUserRequest) {
		name = "prepare_image_generation"
	}
	if name != "" && assistantToolAllowedForContext(name, userContext) {
		return map[string]any{
			"type": "function",
			"function": map[string]any{
				"name": name,
			},
		}
	}
	return "auto"
}

// assistantHasModelReference keeps model discovery data-driven. The legacy
// family pattern covers natural-language forms such as “GPT 5.6 SOL”; the
// generic pattern and the live pricing snapshot cover any newly configured
// provider without requiring a code change or an invented model ID.
func assistantHasModelReference(text string) bool {
	if assistantModelReferencePattern.MatchString(text) || assistantGenericModelReferencePattern.MatchString(text) {
		return true
	}
	normalized := strings.ToLower(strings.TrimSpace(text))
	if normalized == "" {
		return false
	}
	// Most assistant turns are ordinary prose. Avoid cloning the complete live
	// pricing snapshot unless the text contains a model-like token. Keep this
	// gate provider-agnostic so stable IDs without digits remain discoverable.
	if !assistantCatalogModelCandidatePattern.MatchString(normalized) {
		return false
	}
	for _, pricing := range getPricingCache() {
		modelID := strings.ToLower(strings.TrimSpace(pricing.ModelName))
		// Ignore labels that cannot be distinguished from ordinary prose. Exact
		// catalog IDs with a version or separator are safe to match here.
		if len(modelID) < 3 || !strings.ContainsAny(modelID, "0123456789-._:/") {
			continue
		}
		if strings.Contains(normalized, modelID) {
			return true
		}
	}
	return false
}

// assistantBountyReadToolAllowed keeps the larger read tool out of unrelated
// conversations. The intent is part of the tool-set cache key, so a bounty
// request cannot accidentally reuse a catalogue built for another topic.
func assistantBountyReadToolAllowed(userContext assistantUserContext) bool {
	return userContext.Intent == model.AssistantIntentBounty || assistantBountyReadRequest(userContext.LatestUserRequest)
}

func assistantBountyReadRequest(text string) bool {
	normalized := strings.ToLower(strings.TrimSpace(text))
	if normalized == "" {
		return false
	}
	return assistantTextContainsAny(normalized,
		"有哪些悬赏", "悬赏列表", "浏览悬赏", "查看悬赏", "查悬赏", "悬赏详情", "悬赏状态",
		"我的悬赏", "我接受的", "已接受", "争议", "纠纷", "dispute", "bounty list", "list bounties",
		"browse bounties", "show my bounties", "accepted challenges", "bounty details", "bounty status",
	)
}

// assistantHumanSupportRequest distinguishes an explicit handoff request from
// a general question about where support lives. The former must prepare the
// confirmation-gated handoff tool so the assistant cannot merely draft prose;
// the latter can still receive ordinary navigation guidance.
func assistantHumanSupportRequest(text string) bool {
	normalized := strings.ToLower(strings.TrimSpace(text))
	return assistantTextContainsAny(normalized,
		"提交人工客服", "提交工单", "人工核查", "转人工", "联系管理员处理", "请管理员处理",
		"submit a support ticket", "submit to support", "request human support", "human review",
		"contact an administrator", "send this to support",
	)
}

func assistantPublicActivityQuestion(text string) bool {
	normalized := strings.ToLower(strings.TrimSpace(text))
	return assistantTextContainsAny(normalized,
		"签到", "打卡", "每日奖励", "奖励活动", "网站活动", "check in", "check-in", "daily check", "daily reward", "site activity", "site feature",
	)
}

// assistantExplicitWelcomeGiftRequest distinguishes the site's one-time
// welcome-gift workflow from generic requests for free credits. "免费额度"
// is also a common, legitimate way to describe the welcome gift, so it must
// not by itself turn an otherwise eligible user into the promotion-abuse
// profile. Stronger farming signals (multiple accounts, disposable mail,
// coupons, referrals, etc.) still remain promotion signals below.
func assistantExplicitWelcomeGiftRequest(text string) bool {
	normalized := strings.ToLower(strings.TrimSpace(text))
	return assistantTextContainsAny(normalized,
		"新用户礼包", "新用户福利", "新手礼包", "新手奖励", "新用户奖励", "新人礼包", "新人福利", "新手福利",
		"welcome gift", "welcome bonus", "new-user gift", "new user gift", "new user bonus",
	)
}

func assistantGiftPromotionConflict(text string) bool {
	// Keep the overlapping "free credit" terms out of this list. They are
	// allowed when attached to an explicit welcome-gift request; every other
	// promotion signal continues to block the reward workflow.
	return assistantTextContainsAny(text,
		"薅羊毛", "羊毛", "白嫖", "只想", "只要", "优惠码", "免费试用", "批量注册", "多个账号", "多账号", "临时邮箱", "一次性邮箱",
		"coupon", "discount", "free trial", "referral", "multiple accounts", "temporary email", "disposable email", "only want", "just want",
	)
}

func assistantNewUserGiftRequest(text string) bool {
	normalized := strings.ToLower(strings.TrimSpace(text))
	if assistantTextContainsAny(normalized,
		"新用户礼包", "新用户福利", "新手礼包", "新手奖励", "新用户奖励", "新人礼包", "新人福利", "新手福利",
		"welcome gift", "welcome bonus", "new-user gift", "new user gift", "new user bonus",
		"免费额度", "赠送额度", "送我额度", "刀额度", "美元额度", "美金额度", "free credit", "welcome credit",
	) {
		return true
	}
	// Users often ask about the amount without naming the gift, for example
	// “作为一个新用户，你希望给我多少额度”. Keep this narrow: a new-user
	// marker plus a credit/amount term and an explicit request must be present,
	// so ordinary quota, pricing, or account-balance questions do not enter the
	// one-time reward workflow by accident.
	hasNewUserMarker := assistantTextContainsAny(normalized, "新用户", "新手", "新人", "new user", "new-user", "welcome")
	hasCreditTerm := assistantTextContainsAny(normalized, "额度", "credit", "credits")
	hasRequestTerm := assistantTextContainsAny(normalized, "多少", "给我", "送我", "发我", "领", "how much", "can i get", "what amount")
	return hasNewUserMarker && hasCreditTerm && hasRequestTerm
}

func assistantWeeklyDiscountRequest(text string) bool {
	normalized := strings.ToLower(strings.TrimSpace(text))
	return assistantTextContainsAny(normalized,
		"优惠码", "折扣码", "充值折扣", "每周优惠", "每周折扣", "本周优惠",
		"weekly discount", "weekly coupon", "recharge discount", "discount code",
	)
}

// assistantReadChain returns the smallest deterministic read chain
// needed to answer compound fact requests. A model remains responsible for
// the tool arguments, but it cannot skip live service/model/price reads and
// replace them with an advertisement or a guessed value.
func assistantReadChain(userContext assistantUserContext) []string {
	text := strings.ToLower(strings.TrimSpace(userContext.LatestUserRequest))
	if text == "" {
		return nil
	}
	tools := make([]string, 0, 3)
	hasModelReference := assistantHasModelReference(text)
	if assistantPlanOfferWorkflowRequired(userContext) {
		// Keep this first: plan offers are a live, read-only fact source and
		// must be loaded before the final answer for an explicit ready purchase.
		tools = append(tools, "get_plan_offers")
	}
	if userContext.Intent == model.AssistantIntentRecommendation {
		// Recommendation is a single shared, user-visible document. Read the
		// authoritative current row before answering even for a plain “show my
		// recommendation” request; otherwise a disabled agent loop could make
		// the model answer from stale context and miss the existing letter.
		tools = append(tools, "get_l1_recommendation")
	}
	if assistantPublicActivityQuestion(text) {
		tools = append(tools, "get_service_facts")
	}
	if (assistantNewUserGiftRequest(text) || userContext.NewUserGiftRequested) && assistantNewUserGiftToolAllowed(userContext) {
		tools = append(tools, "prepare_new_user_gift")
	}
	if (assistantWeeklyDiscountRequest(text) || userContext.WeeklyDiscountRequested) && assistantWeeklyDiscountToolAllowed(userContext) {
		tools = append(tools, "prepare_weekly_discount")
	}
	if assistantTextContainsAny(text,
		"base url", "base_url", "服务地址", "接口地址", "endpoint", "端点",
	) {
		if !slices.Contains(tools, "get_service_facts") {
			tools = append(tools, "get_service_facts")
		}
	}
	if userContext.Intent == model.AssistantIntentCost ||
		userContext.Intent == model.AssistantIntentModels ||
		assistantTextContainsAny(text, "模型", "model id", "model_id", "available model") ||
		hasModelReference {
		if !slices.Contains(tools, "get_available_models") {
			tools = append(tools, "get_available_models")
		}
	}
	if userContext.Intent == model.AssistantIntentCost && hasModelReference {
		tools = append(tools, "get_model_pricing")
	}
	if userContext.Intent == model.AssistantIntentBounty && assistantBountyReadRequest(text) {
		tools = append(tools, "get_bounty_data")
	}
	return tools
}

func assistantNextRead(userContext assistantUserContext, calledTools, successfulTools map[string]bool) (string, bool) {
	for _, name := range assistantReadChain(userContext) {
		if !calledTools[name] {
			return name, false
		}
		if !successfulTools[name] {
			// A failed authoritative read must not be bypassed by a later tool.
			// Stop forcing the chain and let the final answer report the failure.
			return "", true
		}
	}
	return "", false
}

func assistantRecommendationWorkflowRequired(userContext assistantUserContext) bool {
	return userContext.Intent == model.AssistantIntentRecommendation &&
		userContext.RecommendationAction != assistantRecommendationActionNone
}

func assistantCreateKeyWorkflowRequired(userContext assistantUserContext) bool {
	return userContext.DeveloperAccessGranted && userContext.CreateKeyAction != assistantCreateKeyActionNone
}

func assistantPlanOfferWorkflowRequired(userContext assistantUserContext) bool {
	return userContext.Intent == model.AssistantIntentPlanPurchase &&
		assistantPaymentOfferStateForContext(userContext) == assistantPaymentOfferReady &&
		assistantToolAllowedForContext("get_plan_offers", userContext)
}

func assistantPublicActivityWorkflowRequired(userContext assistantUserContext) bool {
	return assistantPublicActivityQuestion(userContext.LatestUserRequest) &&
		assistantToolAllowedForContext("get_service_facts", userContext)
}

func assistantNewUserGiftWorkflowRequired(userContext assistantUserContext) bool {
	return (assistantNewUserGiftRequest(userContext.LatestUserRequest) || userContext.NewUserGiftRequested) &&
		assistantNewUserGiftToolAllowed(userContext)
}

func assistantWeeklyDiscountWorkflowRequired(userContext assistantUserContext) bool {
	return (assistantWeeklyDiscountRequest(userContext.LatestUserRequest) || userContext.WeeklyDiscountRequested) &&
		assistantWeeklyDiscountToolAllowed(userContext)
}

func assistantHumanSupportWorkflowRequired(userContext assistantUserContext) bool {
	return assistantHumanSupportRequest(userContext.LatestUserRequest) &&
		assistantToolAllowedForContext("request_human_support", userContext)
}

func assistantHumanSupportWorkflowMinSteps(userContext assistantUserContext) int {
	if !assistantHumanSupportWorkflowRequired(userContext) {
		return 0
	}
	steps := 2 // prepare a confirmation card, then answer
	if userContext.ConversationTitleNeeded {
		steps++
	}
	return steps
}

func assistantLiveActivityWorkflowMinSteps(userContext assistantUserContext) int {
	steps := 1 // final answer
	if assistantPublicActivityWorkflowRequired(userContext) {
		steps++
	}
	if assistantNewUserGiftWorkflowRequired(userContext) {
		steps++
	}
	if assistantWeeklyDiscountWorkflowRequired(userContext) {
		steps++
	}
	if steps == 1 {
		return 0
	}
	if userContext.ConversationTitleNeeded {
		steps++
	}
	return steps
}

func assistantNeedsReadChain(userContext assistantUserContext) bool {
	return len(assistantReadChain(userContext)) > 1
}

// assistantLiveReadRequired keeps authoritative recommendation,
// catalog/activity reads on the agent path even when the optional multi-step
// loop is disabled. A single model-ID or recommendation question still needs
// one live tool call; otherwise the configured model can answer from stale
// training data.
func assistantLiveReadRequired(userContext assistantUserContext) bool {
	return len(assistantReadChain(userContext)) > 0
}

func assistantReadChainSteps(userContext assistantUserContext) int {
	if !assistantLiveReadRequired(userContext) {
		return 0
	}
	steps := len(assistantReadChain(userContext)) + 1 // reads, then final answer
	if userContext.ConversationTitleNeeded {
		steps++
	}
	return steps
}

func assistantRecommendationWorkflowMinSteps(userContext assistantUserContext) int {
	if !assistantRecommendationWorkflowRequired(userContext) {
		return 0
	}
	steps := 2 // read the current letter, then produce a final answer
	if userContext.RecommendationAction == assistantRecommendationActionRevise &&
		!userContext.DeveloperAccessGranted &&
		strings.EqualFold(strings.TrimSpace(userContext.AccessLevel), "L0") {
		steps++ // prepare the confirmation-gated revision draft
	}
	if userContext.ConversationTitleNeeded {
		steps++
	}
	return steps
}

func assistantCreateKeyWorkflowMinSteps(userContext assistantUserContext) int {
	if !assistantCreateKeyWorkflowRequired(userContext) {
		return 0
	}
	steps := 2 // prepare the confirmation or group-choice result, then answer
	if userContext.CreateKeyAction == assistantCreateKeyActionRequest {
		steps++ // read live service facts before preparing key creation
	}
	if userContext.ConversationTitleNeeded {
		steps++
	}
	return steps
}

func assistantToolChoiceForAgentStep(userContext assistantUserContext, calledTools map[string]bool, successfulTools map[string]bool) any {
	choice := assistantToolChoiceForContext(userContext)
	if userContext.ConversationTitleNeeded {
		return choice
	}
	if assistantCreateKeyWorkflowRequired(userContext) {
		if userContext.CreateKeyAction == assistantCreateKeyActionRequest && !calledTools["get_service_facts"] {
			return assistantNamedToolChoice("get_service_facts")
		}
		if userContext.CreateKeyAction == assistantCreateKeyActionRequest && !successfulTools["get_service_facts"] {
			return "none"
		}
		if !calledTools["request_create_key"] {
			return assistantNamedToolChoice("request_create_key")
		}
		return "none"
	}
	if assistantHumanSupportWorkflowRequired(userContext) {
		if !calledTools["request_human_support"] {
			return assistantNamedToolChoice("request_human_support")
		}
		return "none"
	}
	if assistantImageGenerationWorkflowRequired(userContext) {
		if !calledTools["prepare_image_generation"] {
			return assistantNamedToolChoice("prepare_image_generation")
		}
		return "none"
	}
	if assistantPublicActivityWorkflowRequired(userContext) {
		if !calledTools["get_service_facts"] {
			return assistantNamedToolChoice("get_service_facts")
		}
		if !successfulTools["get_service_facts"] {
			return "none"
		}
	}
	if assistantWeeklyDiscountWorkflowRequired(userContext) {
		if !calledTools["prepare_weekly_discount"] {
			return assistantNamedToolChoice("prepare_weekly_discount")
		}
		if !successfulTools["prepare_weekly_discount"] {
			return "none"
		}
	}
	if assistantNewUserGiftWorkflowRequired(userContext) {
		if !calledTools["prepare_new_user_gift"] {
			return assistantNamedToolChoice("prepare_new_user_gift")
		}
		if !successfulTools["prepare_new_user_gift"] {
			return "none"
		}
	}
	if !assistantRecommendationWorkflowRequired(userContext) {
		if name, failed := assistantNextRead(userContext, calledTools, successfulTools); name != "" && assistantToolAllowedForContext(name, userContext) {
			return assistantNamedToolChoice(name)
		} else if failed {
			return "none"
		}
		if forcedName := assistantNamedToolChoiceName(choice); forcedName != "" && calledTools[forcedName] {
			return "auto"
		}
		return choice
	}

	if !calledTools["get_l1_recommendation"] {
		return assistantNamedToolChoice("get_l1_recommendation")
	}
	if !successfulTools["get_l1_recommendation"] {
		return "none"
	}
	if userContext.RecommendationAction == assistantRecommendationActionRemove {
		return "none"
	}
	if userContext.DeveloperAccessGranted || !strings.EqualFold(strings.TrimSpace(userContext.AccessLevel), "L0") {
		return "none"
	}
	if !successfulTools["prepare_l1_recommendation"] {
		return assistantNamedToolChoice("prepare_l1_recommendation")
	}
	return "none"
}

func assistantNamedToolChoice(name string) any {
	name = strings.TrimSpace(name)
	if name == "" {
		// Never serialize an empty function name. Some OpenAI-compatible
		// gateways treat an empty named choice as `tool_choice.name` missing
		// and reject the entire request. Let the provider choose instead.
		return "auto"
	}
	return map[string]any{
		"type": "function",
		"function": map[string]any{
			"name": name,
		},
	}
}

func assistantNamedToolChoiceName(choice any) string {
	object, ok := choice.(map[string]any)
	if !ok {
		return ""
	}
	if name, ok := object["name"].(string); ok {
		return strings.TrimSpace(name)
	}
	function, ok := object["function"].(map[string]any)
	if !ok {
		return ""
	}
	name, _ := function["name"].(string)
	return strings.TrimSpace(name)
}

func assistantResponsesToolChoice(choice any) (any, bool) {
	object, ok := choice.(map[string]any)
	if !ok {
		return nil, false
	}
	if choiceType, _ := object["type"].(string); strings.TrimSpace(choiceType) != "function" {
		return nil, false
	}
	name, _ := object["name"].(string)
	if strings.TrimSpace(name) == "" {
		if function, ok := object["function"].(map[string]any); ok {
			name, _ = function["name"].(string)
		}
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, false
	}
	return map[string]any{"type": "function", "name": name}, true
}

// assistantOmitToolChoiceForMissingName handles gateways that reject the
// otherwise-valid OpenAI string choices (`auto`/`none`) with a misleading
// `tool_choice.name` error. Omitting the optional field lets those gateways
// apply their native default. Named choices are adapted to the Responses
// shape first, so required-tool workflows keep their explicit contract.
func assistantOmitToolChoiceForMissingName(choice any) bool {
	value, ok := choice.(string)
	if !ok {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "auto", "none", "required":
		return true
	default:
		return false
	}
}

func assistantToolChoiceNameRequired(body []byte) bool {
	text := strings.ToLower(string(body))
	if !strings.Contains(text, "tool_choice.name") {
		return false
	}
	// Gateways use both the OpenAI-style error code and a human-readable
	// variant.  Treat only an explicit missing-parameter message as the
	// compatibility signal; a generic tool-choice error must not silently
	// drop a required choice.
	return strings.Contains(text, "missing_required_parameter") ||
		strings.Contains(text, "missing required parameter")
}

// assistantNamedToolChoiceUnsupported identifies providers that implement
// function tools but reject selecting one exact function. The fallback is
// deliberately narrower than a generic retry: only bounded, read-only tools
// may execute server-side, while every write and confirmation remains model-
// initiated and confirmation-gated.
func assistantNamedToolChoiceUnsupported(body []byte) bool {
	message := strings.ToLower(string(body))
	if !strings.Contains(message, "tool_choice") && !strings.Contains(message, "tool choice") && !strings.Contains(message, "工具") {
		return false
	}
	return strings.Contains(message, "does not support") ||
		strings.Contains(message, "not support") ||
		strings.Contains(message, "不支持指定工具") ||
		strings.Contains(message, "不支持指定工具的强制选择")
}

func assistantServerReadFallbackAllowed(name string) bool {
	switch strings.TrimSpace(name) {
	case "get_l1_recommendation", "get_account_access", "get_service_facts", "get_available_models":
		return true
	default:
		return false
	}
}

func emptyObjectSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"properties":           map[string]any{},
		"additionalProperties": false,
	}
}

func objectSchema(properties map[string]any, required []string) map[string]any {
	schema := map[string]any{
		"type":                 "object",
		"properties":           properties,
		"additionalProperties": false,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func setAssistantRelayRequest(c *gin.Context, request assistantOpenAIRequest) error {
	payload, err := common.MarshalLimit(request, assistantUpstreamRequestMaxBytes)
	if err != nil {
		return err
	}

	common.CleanupBodyStorage(c)
	storage, err := common.CreateBodyStorage(payload)
	if err != nil {
		return err
	}
	c.Set(common.KeyBodyStorage, storage)
	common.SetContextKey(c, constant.ContextKeyResponseByteLimit, assistantUpstreamResponseMaxBytes)
	c.Set("assistant_request", true)
	c.Request.Body = io.NopCloser(storage)
	c.Request.ContentLength = int64(len(payload))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Request.URL.Path = "/v1/chat/completions"
	c.Request.URL.RawPath = ""
	c.Request.RequestURI = "/v1/chat/completions"
	return nil
}

type assistantRelayRecorder struct {
	gin.ResponseWriter
	header      http.Header
	body        *common.LimitBuffer
	writeErr    error
	status      int
	wroteHeader bool
}

func newAssistantRelayRecorder(writer gin.ResponseWriter) *assistantRelayRecorder {
	return &assistantRelayRecorder{
		ResponseWriter: writer,
		header:         make(http.Header),
		body:           common.NewLimitBuffer(assistantUpstreamResponseMaxBytes),
		status:         http.StatusOK,
	}
}

func (r *assistantRelayRecorder) Header() http.Header {
	return r.header
}

func (r *assistantRelayRecorder) WriteHeader(statusCode int) {
	if statusCode <= 0 {
		// gin.Context.Render uses -1 to write content without changing status.
		return
	}
	if r.wroteHeader {
		return
	}
	r.status = statusCode
}

func (r *assistantRelayRecorder) WriteHeaderNow() {
	if !r.wroteHeader {
		r.wroteHeader = true
	}
}

func (r *assistantRelayRecorder) Write(data []byte) (int, error) {
	r.WriteHeaderNow()
	if r.writeErr != nil {
		return len(data), nil
	}
	written, err := r.body.Write(data)
	if err != nil {
		r.writeErr = err
		return len(data), nil
	}
	return written, nil
}

func (r *assistantRelayRecorder) WriteString(data string) (int, error) {
	r.WriteHeaderNow()
	if r.writeErr != nil {
		return len(data), nil
	}
	written, err := r.body.WriteString(data)
	if err != nil {
		r.writeErr = err
		return len(data), nil
	}
	return written, nil
}

func (r *assistantRelayRecorder) Flush() {
	r.WriteHeaderNow()
}

func (r *assistantRelayRecorder) Status() int {
	if r.status <= 0 {
		return http.StatusOK
	}
	return r.status
}

func (r *assistantRelayRecorder) Size() int {
	if !r.wroteHeader {
		return -1
	}
	return r.body.Len()
}

func (r *assistantRelayRecorder) Written() bool {
	return r.wroteHeader
}

func (r *assistantRelayRecorder) ResetForRelayRetry() error {
	clear(r.header)
	r.body = common.NewLimitBuffer(assistantUpstreamResponseMaxBytes)
	r.writeErr = nil
	r.status = http.StatusOK
	r.wroteHeader = false
	return nil
}

func relayAssistantTurn(c *gin.Context, request assistantOpenAIRequest, rootRequestID string, step int) (int, []byte, error) {
	if err := setAssistantRelayRequest(c, request); err != nil {
		return http.StatusInternalServerError, nil, err
	}

	originalWriter := c.Writer
	recorder := newAssistantRelayRecorder(originalWriter)
	c.Writer = recorder
	c.Set(common.RequestIdKey, fmt.Sprintf("%s-assistant-%d", rootRequestID, step+1))
	defer func() {
		c.Writer = originalWriter
		c.Set(common.RequestIdKey, rootRequestID)
	}()

	Relay(c, types.RelayFormatOpenAI)
	if recorder.writeErr != nil {
		return recorder.Status(), nil, recorder.writeErr
	}
	return recorder.Status(), recorder.body.Bytes(), nil
}

var relayAssistantStreamTurn = relayAssistantStreamTurnWithSession

func relayAssistantStreamTurnWithSession(c *gin.Context, request assistantOpenAIRequest, rootRequestID string, step int, session *assistantStreamSession) (int, []byte, error) {
	if session == nil {
		return http.StatusInternalServerError, nil, errors.New("assistant stream session is unavailable")
	}
	if err := setAssistantRelayRequest(c, request); err != nil {
		return http.StatusInternalServerError, nil, err
	}

	originalWriter := c.Writer
	relayWriter := newAssistantStreamingRelayWriter(originalWriter, session)
	c.Writer = relayWriter
	c.Set(common.RequestIdKey, fmt.Sprintf("%s-assistant-%d", rootRequestID, step+1))
	defer func() {
		c.Writer = originalWriter
		c.Set(common.RequestIdKey, rootRequestID)
	}()

	Relay(c, types.RelayFormatOpenAI)
	if relayWriter.writeErr != nil {
		return relayWriter.Status(), nil, relayWriter.writeErr
	}
	body, err := relayWriter.responseBody()
	if err != nil {
		return relayWriter.Status(), nil, err
	}
	return relayWriter.Status(), body, nil
}

func assistantRetryableUpstreamStatus(status int) bool {
	switch status {
	case http.StatusRequestTimeout, http.StatusTooEarly, http.StatusTooManyRequests:
		return true
	default:
		return status >= http.StatusInternalServerError && status <= 599
	}
}

func assistantUpstreamRetryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	// Keep the retry budget short enough for an interactive chat while still
	// spreading a burst across the provider's recovery window.
	delay := assistantUpstreamRetryBaseDelay * time.Duration(1<<(attempt-1))
	if delay > 1500*time.Millisecond {
		return 1500 * time.Millisecond
	}
	return delay
}

// relayAssistantTurnWithRetry retries only the model call. Tool calls are
// executed after a successful response, so a provider timeout cannot repeat a
// key/configuration preparation action. The outer browser retry has the same
// property because all assistant writes are confirmation-gated or idempotent.
func relayAssistantTurnWithRetry(c *gin.Context, request assistantOpenAIRequest, rootRequestID string, step int) (int, []byte, error) {
	return relayAssistantTurnWithRetryUsing(c, request, rootRequestID, step, relayAssistantTurn)
}

func relayAssistantTurnWithRetryUsing(c *gin.Context, request assistantOpenAIRequest, rootRequestID string, step int, turn func(*gin.Context, assistantOpenAIRequest, string, int) (int, []byte, error)) (int, []byte, error) {
	var status int
	var body []byte
	responsesToolChoiceFallbackUsed := false
	omitToolChoiceFallbackUsed := false
	for attempt := 1; attempt <= assistantUpstreamMaxAttempts; attempt++ {
		status, body, err := turn(c, request, rootRequestID, step)
		if err != nil {
			return status, body, err
		}
		if status >= http.StatusOK && status < http.StatusMultipleChoices {
			response, parseErr := parseAssistantResponse(body)
			if parseErr == nil && len(response.Choices) > 0 {
				return status, body, nil
			}
			// A malformed/empty successful provider response is treated as a
			// transient upstream failure and receives the same bounded retry.
			if attempt == assistantUpstreamMaxAttempts {
				return status, body, nil
			}
		}
		if assistantToolChoiceNameRequired(body) {
			if !responsesToolChoiceFallbackUsed {
				if fallback, ok := assistantResponsesToolChoice(request.ToolChoice); ok {
					// A few OpenAI-compatible Responses gateways expose the chat
					// endpoint but validate tool_choice using the Responses shape:
					// {"type":"function","name":"..."}. Retry once with that
					// shape instead of burning the normal retry budget on the same
					// invalid request.
					request.ToolChoice = fallback
					responsesToolChoiceFallbackUsed = true
					continue
				}
			}
			if !omitToolChoiceFallbackUsed && assistantOmitToolChoiceForMissingName(request.ToolChoice) {
				// Some gateways reject `auto` or `none` as if a named function
				// were required. Omitting the field is the provider-neutral
				// fallback for a turn that does not have a required tool.
				request.ToolChoice = nil
				omitToolChoiceFallbackUsed = true
				continue
			}
		}
		if !assistantRetryableUpstreamStatus(status) || attempt == assistantUpstreamMaxAttempts {
			return status, body, nil
		}

		timer := time.NewTimer(assistantUpstreamRetryDelay(attempt))
		select {
		case <-c.Request.Context().Done():
			if !timer.Stop() {
				<-timer.C
			}
			return http.StatusRequestTimeout, nil, c.Request.Context().Err()
		case <-timer.C:
		}
	}
	return status, body, nil
}

var relayAssistantAgentTurn = relayAssistantTurnWithRetry

func assistantContextBytes(messages []assistantOpenAIMessage) int {
	return agent.Bytes(messages)
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
	ctx, cancel := context.WithTimeout(c.Request.Context(), timeout)
	defer cancel()
	originalRequest := c.Request
	c.Request = c.Request.WithContext(ctx)
	defer func() {
		c.Request = originalRequest
		common.CleanupBodyStorage(c)
	}()
	streamSession := assistantStreamSessionFrom(c)

	rootRequestID := c.GetString(common.RequestIdKey)
	if rootRequestID == "" {
		rootRequestID = common.NewRequestId()
		c.Set(common.RequestIdKey, rootRequestID)
	}

	userContext := assistantUserContextFromGin(c)
	messages := make([]assistantOpenAIMessage, 1, len(conversation)+1)
	messages[0] = assistantOpenAIMessage{Role: "system", Content: assistantPrompt(c, settings, userContext)}
	messages = append(messages, conversation...)
	if assistantContextBytes(messages) > assistantAgentContextMaxBytes {
		writeAssistantError(c, http.StatusRequestEntityTooLarge, "ASSISTANT_CONTEXT_TOO_LARGE", errors.New("assistant context exceeded its byte budget"))
		return
	}
	maxSteps := settings.MaxSteps
	if maxSteps < 1 {
		maxSteps = 1
	}
	forceL0Assessment := assistantL0InterlocutorAssessmentRequired(userContext)
	forceRecommendationWorkflow := assistantRecommendationWorkflowRequired(userContext)
	forceCreateKeyWorkflow := assistantCreateKeyWorkflowRequired(userContext)
	forceImageGenerationWorkflow := assistantImageGenerationWorkflowRequired(userContext)
	forcePublicActivityWorkflow := assistantPublicActivityWorkflowRequired(userContext)
	forceNewUserGiftWorkflow := assistantNewUserGiftWorkflowRequired(userContext)
	forceWeeklyDiscountWorkflow := assistantWeeklyDiscountWorkflowRequired(userContext)
	forceHumanSupportWorkflow := assistantHumanSupportWorkflowRequired(userContext)
	forceConversationTitle := userContext.ConversationTitleNeeded
	forceReadChain := assistantLiveReadRequired(userContext)
	if forceL0Assessment && maxSteps < 2 {
		maxSteps = 2
	}
	if forceConversationTitle && maxSteps < 2 {
		// A title is a real agent action, not optional model prose. Keep it
		// available even when an administrator disables the general-purpose
		// multi-step loop.
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
	if minimum := assistantHumanSupportWorkflowMinSteps(userContext); maxSteps < minimum {
		maxSteps = minimum
	}
	if minimum := assistantReadChainSteps(userContext); maxSteps < minimum {
		maxSteps = minimum
	}
	if !settings.AgentLoopEnabled {
		if !forceL0Assessment && !forceConversationTitle && !forceRecommendationWorkflow && !forceCreateKeyWorkflow && !forceImageGenerationWorkflow && !forcePublicActivityWorkflow && !forceNewUserGiftWorkflow && !forceWeeklyDiscountWorkflow && !forceHumanSupportWorkflow && !forceReadChain {
			maxSteps = 1
		}
	}
	cacheKey := c.GetString("assistant_cache_key")
	usedCacheSensitiveTool := false
	agentEnabled := maxSteps > 1 && (settings.AgentLoopEnabled || forceL0Assessment || forceConversationTitle || forceRecommendationWorkflow || forceCreateKeyWorkflow || forceImageGenerationWorkflow || forcePublicActivityWorkflow || forceNewUserGiftWorkflow || forceWeeklyDiscountWorkflow || forceHumanSupportWorkflow || forceReadChain)
	var tools []assistantOpenAIToolDefinition
	var calledTools, successfulTools map[string]bool
	toolTraces := make([]assistantToolTrace, 0, assistantToolCallsPerTurn)
	if agentEnabled {
		tools = assistantToolDefinitionsForContext(userContext)
		calledTools = make(map[string]bool)
		successfulTools = make(map[string]bool)
	}

	for step := 0; step < maxSteps; step++ {
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
		if agentEnabled && step < maxSteps-1 {
			request.Tools = tools
			request.ToolChoice = assistantToolChoiceForAgentStep(userContext, calledTools, successfulTools)
		}

		var status int
		var body []byte
		var err error
		if streamTurn {
			status, body, err = relayAssistantStreamTurn(c, request, rootRequestID, step, streamSession)
		} else {
			status, body, err = relayAssistantAgentTurn(c, request, rootRequestID, step)
		}
		if err != nil {
			writeAssistantError(c, http.StatusInternalServerError, "ASSISTANT_REQUEST_BUILD_FAILED", errors.New("failed to build assistant request"))
			return
		}
		if status < http.StatusOK || status >= http.StatusMultipleChoices {
			forcedTool := assistantNamedToolChoiceName(request.ToolChoice)
			if assistantNamedToolChoiceUnsupported(body) && assistantServerReadFallbackAllowed(forcedTool) {
				// The provider cannot select the read explicitly. Execute the
				// bounded server-owned read, append its verified result, then let
				// the next streamed model turn draft from that context.
				call := assistantOpenAIToolCall{
					ID:       fmt.Sprintf("assistant-server-read-%d", step+1),
					Type:     "function",
					Function: assistantOpenAIToolCallFunction{Name: forcedTool},
				}
				result := executeAssistantTool(c, call)
				if c.IsAborted() {
					return
				}
				resultJSON, marshalErr := common.MarshalLimit(result, assistantToolResultMaxBytes)
				if marshalErr != nil {
					resultJSON = []byte(`{"ok":false,"error":"tool result exceeded its byte budget"}`)
				}
				calledTools[forcedTool] = true
				if ok, _ := result["ok"].(bool); ok {
					successfulTools[forcedTool] = true
				}
				usedCacheSensitiveTool = true
				toolTraces = append(toolTraces, buildAssistantToolTrace(call, result))
				c.Set(assistantClientToolsKey, toolTraces)
				messages = append(messages, assistantOpenAIMessage{
					Role:      "assistant",
					ToolCalls: []assistantOpenAIToolCall{call},
				})
				messages = append(messages, assistantOpenAIMessage{
					Role:       "tool",
					Content:    string(resultJSON),
					ToolCallID: call.ID,
				})
				continue
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
		if forceConversationTitle || forceRecommendationWorkflow || forceCreateKeyWorkflow || forceImageGenerationWorkflow || forcePublicActivityWorkflow || forceNewUserGiftWorkflow || forceWeeklyDiscountWorkflow || forceHumanSupportWorkflow || forceReadChain {
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
		if (!settings.AgentLoopEnabled && !forceL0Assessment && !forceConversationTitle && !forceRecommendationWorkflow && !forceCreateKeyWorkflow && !forceImageGenerationWorkflow && !forcePublicActivityWorkflow && !forceNewUserGiftWorkflow && !forceWeeklyDiscountWorkflow && !forceHumanSupportWorkflow && !forceReadChain) || step >= maxSteps-1 {
			writeAssistantError(c, http.StatusBadGateway, "ASSISTANT_AGENT_MAX_STEPS", errors.New("assistant agent reached its step limit before producing a final answer"))
			return
		}
		if len(message.ToolCalls) > assistantToolCallsPerTurn {
			writeAssistantError(c, http.StatusBadGateway, "ASSISTANT_TOO_MANY_TOOL_CALLS", errors.New("assistant requested too many tools in one turn"))
			return
		}

		messages = append(messages, assistantOpenAIMessage{
			Role:      "assistant",
			Content:   assistantResponseContent(message.Content),
			ToolCalls: message.ToolCalls,
		})
		for index, call := range message.ToolCalls {
			toolName := strings.TrimSpace(call.Function.Name)
			calledTools[toolName] = true
			if toolName != "set_conversation_title" {
				usedCacheSensitiveTool = true
			}
			result := executeAssistantTool(c, call)
			if c.IsAborted() {
				return
			}
			resultJSON, marshalErr := common.MarshalLimit(result, assistantToolResultMaxBytes)
			if ok, _ := result["ok"].(bool); ok {
				successfulTools[toolName] = true
			}
			if toolName == "set_conversation_title" {
				// The title tool updates the Gin context. Keep this loop's local
				// policy snapshot in sync so the next step advances to the task
				// tool instead of forcing the title again.
				userContext = assistantUserContextFromGin(c)
			}
			if marshalErr != nil {
				resultJSON = []byte(`{"ok":false,"error":"tool result exceeded its byte budget"}`)
			}
			toolTraces = append(toolTraces, buildAssistantToolTrace(call, result))
			c.Set(assistantClientToolsKey, toolTraces)
			callID := strings.TrimSpace(call.ID)
			if callID == "" {
				callID = fmt.Sprintf("assistant-call-%d-%d", step+1, index+1)
			}
			messages = append(messages, assistantOpenAIMessage{
				Role:       "tool",
				Content:    string(resultJSON),
				ToolCallID: callID,
			})
		}
		if assistantContextBytes(messages) > assistantAgentContextMaxBytes {
			writeAssistantError(c, http.StatusBadGateway, "ASSISTANT_CONTEXT_TOO_LARGE", errors.New("assistant tool context exceeded its byte budget"))
			return
		}
	}

	writeAssistantError(c, http.StatusBadGateway, "ASSISTANT_AGENT_MAX_STEPS", errors.New("assistant agent reached its step limit"))
}

func parseAssistantResponse(body []byte) (assistantOpenAIResponse, error) {
	return agent.Parse(body)
}

func assistantResponseContent(raw json.RawMessage) string {
	return agent.Text(raw)
}

// normalizeAssistantClientResponse is the only provider-to-browser response
// boundary. It deliberately discards provider IDs, model names, usage,
// reasoning, errors, and unknown fields, retaining only normalized text and
// server-issued metadata.
func normalizeAssistantClientResponse(c *gin.Context, body []byte) ([]byte, error) {
	response, err := parseAssistantResponse(body)
	if err != nil || len(response.Choices) == 0 {
		return nil, errors.New("assistant upstream returned an invalid response")
	}
	content := strings.TrimSpace(assistantResponseContent(response.Choices[0].Message.Content))
	if content == "" {
		return nil, errors.New("assistant upstream returned no usable text")
	}
	payload := map[string]any{
		"choices": []any{map[string]any{
			"message": map[string]any{"role": "assistant", "content": content},
		}},
	}
	if c != nil {
		if requestID := strings.TrimSpace(c.GetString(common.RequestIdKey)); requestID != "" {
			payload["lmm_request_id"] = requestID
		}
		if action, exists := c.Get(assistantClientActionKey); exists {
			payload["lmm_assistant_action"] = action
		}
		if tools, exists := c.Get(assistantClientToolsKey); exists {
			if traces, ok := tools.([]assistantToolTrace); ok && len(traces) > 0 {
				payload["lmm_assistant_tools"] = traces
			}
		}
	}
	// The upstream body is bounded before parsing, but JSON normalization can
	// expand content again (for example, encoding/json escapes '<' as
	// "\\u003c"). Keep the provider-to-browser boundary within the same
	// retained-byte budget instead of allowing an expanded response to escape
	// the relay limit.
	return common.MarshalLimit(payload, assistantUpstreamResponseMaxBytes)
}

func writeAssistantUpstreamError(c *gin.Context, code, message string) {
	payload := gin.H{"success": false, "code": code, "message": message, "retryable": true}
	if requestID := strings.TrimSpace(c.GetString(common.RequestIdKey)); requestID != "" {
		payload["request_id"] = requestID
	}
	if session := assistantStreamSessionFrom(c); session != nil {
		_ = session.fail(http.StatusBadGateway, code, message)
		c.Abort()
		return
	}
	c.AbortWithStatusJSON(http.StatusBadGateway, payload)
}

func writeAssistantRawResponse(c *gin.Context, status int, body []byte, fallbackCode string) {
	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		writeAssistantUpstreamError(c, fallbackCode, "AI assistant upstream request failed")
		return
	}
	normalizedBody, err := normalizeAssistantClientResponse(c, body)
	if err != nil {
		writeAssistantUpstreamError(c, fallbackCode, "AI assistant upstream returned no usable answer")
		return
	}
	c.Data(status, "application/json; charset=utf-8", normalizedBody)
}

func assistantActorUserID(c *gin.Context) int {
	if c == nil {
		return 0
	}
	if userID := c.GetInt(assistantActorUserIDKey); userID > 0 {
		return userID
	}
	return c.GetInt("id")
}

func assistantDeveloperCapabilityRequired(userID int, capability string) (map[string]any, bool) {
	if userID <= 0 {
		return map[string]any{"ok": false, "status": "l1_required", "error": "L1 access is required for " + capability}, true
	}
	_, granted, err := getAssistantDeveloperAccess(userID)
	if err != nil {
		return map[string]any{"ok": false, "error": "account access could not be loaded"}, true
	}
	if !granted {
		return map[string]any{
			"ok":        false,
			"status":    "l1_required",
			"error":     "L1 access is required for " + capability,
			"next_step": "Ask the user to continue the L1 onboarding conversation and submit an administrator recommendation.",
		}, true
	}
	return nil, false
}

func executeAssistantTool(c *gin.Context, call assistantOpenAIToolCall) map[string]any {
	actorUserID := assistantActorUserID(c)
	name := strings.TrimSpace(call.Function.Name)
	if c != nil {
		if rawContext, exists := c.Get(assistantUserContextKey); exists {
			if userContext, ok := rawContext.(assistantUserContext); ok && !assistantToolExecutionAllowedForContext(name, userContext) {
				return map[string]any{
					"ok":     false,
					"status": "tool_not_allowed",
					"error":  "this assistant action is not available for the current account state",
				}
			}
		}
	}
	arguments := strings.TrimSpace(call.Function.Arguments)
	if arguments == "" {
		arguments = "{}"
	}
	if len(arguments) > assistantToolArgumentsMaxBytes {
		return map[string]any{"ok": false, "error": "tool arguments are too large"}
	}
	var input map[string]any
	if err := json.Unmarshal([]byte(arguments), &input); err != nil {
		return map[string]any{"ok": false, "error": "tool arguments must be valid JSON"}
	}
	if name == forgetProfileTool && (c == nil || !assistantExplicitProfileForgetRequest(c.GetString("assistant_history_latest_message"))) {
		return map[string]any{
			"ok": false, "status": "explicit_request_required",
			"error": "profile removal requires an explicit user request",
		}
	}
	explicitProfileForget := false
	if name == forgetProfileTool && c != nil {
		if rawContext, exists := c.Get(assistantUserContextKey); exists {
			if userContext, ok := rawContext.(assistantUserContext); ok {
				explicitProfileForget = assistantExplicitProfileForgetRequest(userContext.LatestUserRequest)
			}
		}
	}
	if result, handled := runSkillTool(name, actorUserID, input, explicitProfileForget); handled {
		return result
	}

	switch name {
	case assistantInterlocutorAssessmentTool:
		return executeAssistantInterlocutorAssessmentTool(c, input)
	case "set_conversation_title":
		return executeAssistantConversationTitleTool(c, input)
	case "get_service_facts":
		rootURL := strings.TrimRight(system_setting.ServerAddress, "/")
		baseURL := rootURL
		if rootURL == "" {
			rootURL = "the service root shown in the current console"
			baseURL = "the /v1 endpoint shown in the current console"
		} else {
			baseURL += "/v1"
		}
		checkinSetting := operation_setting.GetCheckinSetting()
		checkinFacts := map[string]any{
			"enabled":         checkinSetting.Enabled,
			"page_path":       "/profile",
			"status_endpoint": "/api/user/checkin",
			"frequency":       "once_per_day",
			"reward_type":     "random_quota",
		}
		if checkinSetting.Enabled {
			checkinFacts["base_min_quota"] = checkinSetting.MinQuota
			checkinFacts["base_max_quota"] = checkinSetting.MaxQuota
			if actorUserID > 0 {
				if rewardRange, err := model.GetUserCheckinRewardRange(actorUserID); err == nil {
					checkinFacts["trust_level"] = rewardRange.TrustLevel
					checkinFacts["reward_multiplier"] = rewardRange.Multiplier
					checkinFacts["min_quota"] = rewardRange.MinQuota
					checkinFacts["max_quota"] = rewardRange.MaxQuota
				}
				if checkedInToday, err := model.HasCheckedInToday(actorUserID); err == nil {
					checkinFacts["checked_in_today"] = checkedInToday
				}
			}
		}
		keyGroupOptions := []assistantKeyGroupOption(nil)
		if actorUserID > 0 {
			if user, userErr := model.GetUserCache(actorUserID); userErr == nil && user != nil {
				keyGroupOptions = getAssistantKeyGroupOptions(user.Group)
			}
		}
		return map[string]any{
			"ok":                       true,
			"service_root":             rootURL,
			"openai_base_url":          baseURL,
			"client_model_instruction": "Call get_available_models and use an exact model_ids value; the assistant's own model is not a client default.",
			"api_keys_are_private":     true,
			"key_management_path":      "/keys",
			"key_group_options":        keyGroupOptions,
			"group_catalog_source":     "live_user_usable_groups",
			"activities": map[string]any{
				"daily_checkin": checkinFacts,
			},
			"cc_switch_import": map[string]any{
				"supported":                true,
				"protocol":                 "ccswitch://v1/import",
				"application":              "claude",
				"requires_private_api_key": true,
				"ui_action":                "Import to CC Switch",
			},
			"write_actions": "require explicit confirmation in the UI",
		}
	case "calculate_math":
		return executeAssistantMathTool(input)
	case "calculate_cost":
		return executeAssistantCostTool(input)
	case "get_account_access":
		return executeAssistantAccountTool(actorUserID)
	case "get_l1_recommendation":
		return executeAssistantL1RecommendationStateTool(c, actorUserID)
	case "navigate_to_page":
		return executeAssistantNavigateTool(c, actorUserID, input)
	case "get_user_overview":
		return executeAssistantUserOverviewTool(c, actorUserID, input)
	case "get_user_usage_summary":
		return executeAssistantUserUsageTool(c, actorUserID, input)
	case "prepare_user_action":
		return executeAssistantPrepareUserActionTool(c, actorUserID, input)
	case "get_available_models":
		return executeAssistantModelsTool(actorUserID)
	case "get_model_pricing":
		return executeAssistantModelPricingTool(actorUserID, input)
	case "get_plan_offers":
		userContext := assistantUserContextFromGin(c)
		if c == nil {
			if result, blocked := assistantDeveloperCapabilityRequired(actorUserID, "plan offers"); blocked {
				return result
			}
			return executeAssistantPlanOffersTool(actorUserID)
		}
		if _, hasContext := c.Get(assistantUserContextKey); !hasContext {
			if result, blocked := assistantDeveloperCapabilityRequired(actorUserID, "plan offers"); blocked {
				return result
			}
			return executeAssistantPlanOffersTool(actorUserID)
		}
		paymentOfferState := assistantPaymentOfferStateForContext(userContext)
		if !userContext.DeveloperAccessGranted && paymentOfferState != assistantPaymentOfferReady && paymentOfferState != assistantPaymentOfferBlocked {
			return map[string]any{
				"ok":        false,
				"status":    "payment_intent_required",
				"error":     "one more payment detail is needed before showing payment options",
				"next_step": "Ask for the intended use, approximate amount, or preferred payment method.",
			}
		}
		if !userContext.DeveloperAccessGranted {
			return executeAssistantPlanOffersTool(actorUserID)
		}
		if result, blocked := assistantDeveloperCapabilityRequired(actorUserID, "plan offers"); blocked {
			return result
		}
		return executeAssistantPlanOffersTool(actorUserID)
	case "get_invitation_rewards":
		if result, blocked := assistantDeveloperCapabilityRequired(actorUserID, "invitation rewards"); blocked {
			return result
		}
		return executeAssistantInvitationTool(actorUserID)
	case "get_bounty_guide":
		return executeAssistantBountyTool()
	case "get_bounty_data":
		return executeAssistantBountyDataTool(actorUserID, input)
	case "prepare_new_user_gift":
		return executeAssistantNewUserGiftTool(c, actorUserID, input)
	case "prepare_weekly_discount":
		return executeAssistantWeeklyDiscountTool(c, actorUserID, input)
	case "prepare_image_generation":
		return executeAssistantImageGenerationTool(c, actorUserID, input)
	case "get_usage_summary":
		if result, blocked := assistantDeveloperCapabilityRequired(actorUserID, "usage statistics"); blocked {
			return result
		}
		return executeAssistantUsageTool(actorUserID, input)
	case "search_web":
		return executeAssistantSearchTool(c, input)
	case "get_setup_guide":
		return executeAssistantSetupTool(actorUserID, input)
	case "prepare_l1_recommendation":
		if assistantUserContextFromGin(c).RecommendationAction == assistantRecommendationActionRemove {
			return map[string]any{
				"ok":      false,
				"status":  "removal_requires_user_ui",
				"error":   "AI cannot remove or replace a recommendation for a removal request",
				"message": "Tell the user to clear the visible Recommendation letter field and choose Save changes in the existing UI.",
			}
		}
		return executeAssistantL1RecommendationTool(c, actorUserID, input)
	case "request_create_key":
		if c == nil {
			return map[string]any{"ok": false, "error": "signed-in account is unavailable"}
		}
		return executeAssistantCreateKeyRequestTool(c, actorUserID, input)
	case "request_human_support":
		message := strings.TrimSpace(inputString(input, "message"))
		messageLength := len([]rune(message))
		if messageLength < 5 || messageLength > 2000 {
			return map[string]any{
				"ok":     false,
				"status": "message_invalid",
				"error":  "support message must contain 5 to 2000 characters",
			}
		}
		if inputString(input, "action") == "disable_account" {
			return executeAssistantAccountDisableRequestTool(c, actorUserID, input, message)
		}
		if c == nil || c.GetBool("use_access_token") {
			return map[string]any{"ok": false, "status": "session_required", "error": "a browser login session is required to prepare support confirmation"}
		}
		sessionID := strings.TrimSpace(c.GetString("session_id"))
		if sessionID == "" {
			return map[string]any{"ok": false, "status": "session_required", "error": "a browser login session is required to prepare support confirmation"}
		}
		payload, err := json.Marshal(map[string]string{"message": message})
		if err != nil {
			return map[string]any{"ok": false, "error": "support confirmation could not be prepared"}
		}
		confirmationToken, _, err := model.CreateAuthFlow(model.AuthFlowCreate{
			Purpose:   model.AuthFlowPurposeAssistantHandoff,
			UserId:    actorUserID,
			SessionId: sessionID,
			Payload:   string(payload),
			ExpiresAt: time.Now().Add(assistantHandoffConfirmationTTL),
		})
		if err != nil {
			return map[string]any{"ok": false, "error": "support confirmation could not be prepared"}
		}
		action := map[string]any{
			"type":                  "human_support",
			"confirmation_token":    confirmationToken,
			"requires_confirmation": true,
			"expires_in_seconds":    int(assistantHandoffConfirmationTTL / time.Second),
			"message":               message,
			"ui_path":               "/support",
		}
		c.Set(assistantClientActionKey, action)
		return map[string]any{
			"ok":            true,
			"status":        "confirmation_required",
			"action":        "human_support",
			"ui_path":       "/support",
			"message":       "Ask the user to confirm sending this message to an administrator.",
			"draft_message": message,
		}
	case "get_admin_server_config":
		return executeAssistantAdminConfigTool(c, actorUserID)
	case "get_admin_model_inventory":
		return executeAssistantAdminModelInventoryTool(actorUserID)
	case "prepare_admin_model_sync":
		return executeAssistantAdminModelSyncTool(c, actorUserID, input)
	case "get_admin_assistant_review":
		return executeAssistantReviewTool(actorUserID)
	case "get_admin_user_skills":
		return executeAssistantAdminUserSkillsTool(actorUserID, input)
	case "prepare_admin_config_change":
		return executeAssistantAdminConfigChangeTool(c, actorUserID, input)
	case "get_admin_channels":
		return executeAssistantAdminChannelsTool(actorUserID)
	case "prepare_admin_channel_change":
		return executeAssistantAdminChannelChangeTool(c, actorUserID, input)
	case "prepare_admin_pricing_change":
		return executeAssistantAdminPricingChangeTool(c, actorUserID, input)
	case "prepare_admin_user_skill_change":
		return executeAssistantAdminUserSkillChangeTool(c, actorUserID, input)
	default:
		return map[string]any{"ok": false, "error": "unknown assistant tool"}
	}
}

func executeAssistantConversationTitleTool(c *gin.Context, input map[string]any) map[string]any {
	if c == nil || !assistantUserContextFromGin(c).ConversationTitleNeeded {
		return map[string]any{"ok": false, "error": "this conversation does not need an automatic title"}
	}
	title := strings.TrimSpace(inputString(input, "title"))
	if title == "" {
		return map[string]any{"ok": false, "error": "a conversation title is required"}
	}
	runes := []rune(model.RedactAssistantHistoryContent(title))
	if len(runes) > assistantConversationTitleMaxRunes {
		runes = runes[:assistantConversationTitleMaxRunes]
	}
	title = strings.TrimSpace(string(runes))
	if title == "" {
		return map[string]any{"ok": false, "error": "the conversation title became empty after safety filtering"}
	}
	c.Set(assistantConversationTitleDraftKey, title)
	userContext := assistantUserContextFromGin(c)
	userContext.ConversationTitleNeeded = false
	c.Set(assistantUserContextKey, userContext)
	return map[string]any{"ok": true, "title": title}
}

func executeAssistantL1RecommendationStateTool(c *gin.Context, userID int) map[string]any {
	if userID <= 0 {
		return map[string]any{"ok": false, "error": "signed-in account is unavailable"}
	}
	request, err := model.GetDeveloperAccessRequest(userID)
	if err != nil {
		return map[string]any{"ok": false, "error": "the current recommendation could not be loaded"}
	}
	if request == nil {
		result := map[string]any{
			"ok":             true,
			"status":         "none",
			"recommendation": "",
			"next_step":      "Use the conversation context to prepare the user's one L1 recommendation when requested.",
		}
		if assistantUserContextFromGin(c).RecommendationAction == assistantRecommendationActionRemove {
			result["next_step"] = "Tell the user there is no recommendation letter to remove. Do not call prepare_l1_recommendation."
		}
		return result
	}
	result := map[string]any{
		"ok":                      true,
		"status":                  request.Status,
		"source":                  request.Source,
		"user_statement":          request.Reason,
		"recommendation":          request.AIRecommendation,
		"administrator_note":      request.AdminNote,
		"is_single_shared_letter": true,
		"next_step":               "For an AI edit, prepare a revised draft of this same letter and require UI confirmation before replacing it.",
	}
	if assistantUserContextFromGin(c).RecommendationAction == assistantRecommendationActionRemove {
		result["next_step"] = "Do not call prepare_l1_recommendation and do not change the administrator queue. Tell the user to clear the visible Recommendation letter field in the existing UI and choose Save changes; that direct user action remains explicitly confirmed."
		result["removal_requires_user_ui"] = true
	}
	return result
}

func executeAssistantInterlocutorAssessmentTool(c *gin.Context, input map[string]any) map[string]any {
	if c == nil {
		return map[string]any{"ok": false, "status": "context_unavailable", "error": "conversation context is unavailable"}
	}
	userContext := assistantUserContextFromGin(c)
	if !assistantL0InterlocutorAssessmentRequired(userContext) {
		return map[string]any{"ok": false, "status": "not_required", "error": "the L0 interlocutor assessment is not required"}
	}

	kind := strings.TrimSpace(inputString(input, "kind"))
	confidence := strings.TrimSpace(inputString(input, "confidence"))
	if kind != "likely_human" && kind != "likely_automated" && kind != "uncertain" {
		return map[string]any{"ok": false, "status": "assessment_invalid", "error": "assessment kind is invalid"}
	}
	if confidence != "low" && confidence != "medium" && confidence != "high" {
		return map[string]any{"ok": false, "status": "assessment_invalid", "error": "assessment confidence is invalid"}
	}
	evidence, ok := assistantAssessmentEvidence(input["evidence"])
	if !ok {
		return map[string]any{"ok": false, "status": "assessment_invalid", "error": "assessment evidence is invalid"}
	}

	userContext.InterlocutorAssessed = true
	c.Set(assistantUserContextKey, userContext)
	result := map[string]any{
		"ok":         true,
		"status":     "recorded",
		"assessment": map[string]any{"kind": kind, "confidence": confidence},
		"next_step":  "Continue the conversation using the normal L0 policy. Treat this as a soft signal, not an access decision.",
	}
	if len(evidence) == 0 {
		result["assessment"].(map[string]any)["evidence"] = []string{"unclear"}
	}
	return result
}

func assistantAssessmentEvidence(value any) ([]string, bool) {
	if value == nil {
		return nil, false
	}
	values, ok := value.([]any)
	if !ok || len(values) > 3 {
		return nil, false
	}
	allowed := map[string]struct{}{
		"coherent_contextual_follow_up": {},
		"repeated_template_or_payload":  {},
		"explicit_automation_context":   {},
		"goal_continuity":               {},
		"unclear":                       {},
	}
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		item, ok := value.(string)
		item = strings.TrimSpace(item)
		if !ok || item == "" {
			return nil, false
		}
		if _, exists := allowed[item]; !exists {
			return nil, false
		}
		if _, exists := seen[item]; exists {
			continue
		}
		seen[item] = struct{}{}
		result = append(result, item)
	}
	return result, true
}

// executeAssistantAccountDisableRequestTool follows the existing assistant
// action pattern: prepare a short-lived, session-bound confirmation draft and
// expose it as lmm_assistant_action. The follow-up endpoint consumes the flow
// and creates a pending admin request; this function never changes User.Status.
func executeAssistantAccountDisableRequestTool(c *gin.Context, userID int, input map[string]any, reason string) map[string]any {
	if c == nil || userID <= 0 || c.GetBool("use_access_token") {
		return map[string]any{"ok": false, "error": "账号安全操作需要有效的浏览器登录会话"}
	}
	sessionID := strings.TrimSpace(c.GetString("session_id"))
	if sessionID == "" {
		return map[string]any{"ok": false, "error": "账号安全操作需要有效的浏览器登录会话"}
	}
	actor, err := model.GetUserById(userID, false)
	if err != nil {
		return map[string]any{"ok": false, "error": "无法读取当前账号"}
	}
	targetUserID := userID
	if rawTarget, exists := input["target_user_id"]; exists {
		number, ok := inputNumber(input, "target_user_id")
		if !ok || number < 1 || math.Trunc(number) != number {
			return map[string]any{"ok": false, "status": "target_invalid", "error": "目标用户编号无效"}
		}
		targetUserID = int(number)
		if rawTarget == nil {
			return map[string]any{"ok": false, "status": "target_invalid", "error": "目标用户编号无效"}
		}
	}
	if actor.Role < common.RoleAdminUser && targetUserID != actor.Id {
		return map[string]any{"ok": false, "status": "target_forbidden", "error": "普通用户只能为自己的账号提交安全申请"}
	}
	if actor.Role >= common.RoleAdminUser && targetUserID == actor.Id {
		return map[string]any{"ok": false, "status": "target_forbidden", "error": "管理员只能指定低权限目标账号"}
	}
	target, err := model.GetUserById(targetUserID, false)
	if err != nil {
		return map[string]any{"ok": false, "status": "target_not_found", "error": "目标账号不存在"}
	}
	if target.Role == common.RoleRootUser || (actor.Role < common.RoleRootUser && target.Role >= common.RoleAdminUser) {
		return map[string]any{"ok": false, "status": "target_forbidden", "error": "该目标账号不在当前管理员可操作范围内"}
	}
	payload, err := json.Marshal(assistantAccountDisableDraft{
		TargetUserID: targetUserID,
		Reason:       reason,
	})
	if err != nil {
		return map[string]any{"ok": false, "error": "账号安全申请无法准备"}
	}
	confirmationToken, _, err := model.CreateAuthFlow(model.AuthFlowCreate{
		Purpose:   assistantAccountDisableAuthFlowPurpose,
		UserId:    actor.Id,
		SessionId: sessionID,
		Payload:   string(payload),
		ExpiresAt: time.Now().Add(assistantRecommendationTTL),
	})
	if err != nil {
		return map[string]any{"ok": false, "error": "账号安全申请确认无法创建"}
	}
	action := map[string]any{
		"type":               "account_disable_request",
		"target_user_id":     targetUserID,
		"target_username":    target.Username,
		"reason":             reason,
		"confirmation_token": confirmationToken,
	}
	c.Set(assistantClientActionKey, action)
	return map[string]any{
		"ok":                 true,
		"status":             "confirmation_required",
		"action":             "account_disable_request",
		"target_user_id":     targetUserID,
		"target_username":    target.Username,
		"admin_confirmation": true,
		"message":            "这只是提交给管理员的禁用建议。请向用户展示目标、原因和管理员审核说明，并在用户明确确认后调用账号操作申请接口；在管理员批准前账号不会被禁用。",
	}
}

func executeAssistantL1RecommendationTool(c *gin.Context, userID int, input map[string]any) map[string]any {
	if c == nil || userID <= 0 {
		return map[string]any{"ok": false, "error": "signed-in account is unavailable"}
	}
	user, err := model.GetUserCache(userID)
	if err != nil {
		return map[string]any{"ok": false, "error": "account access could not be loaded"}
	}
	access, err := model.GetDeveloperAccessStateForUserBase(user)
	if err != nil {
		return map[string]any{"ok": false, "error": "developer access could not be loaded"}
	}
	if access.Granted {
		return map[string]any{"ok": false, "status": "already_active", "error": "L1 access is already active"}
	}
	sessionID := strings.TrimSpace(c.GetString("session_id"))
	if sessionID == "" {
		return map[string]any{"ok": false, "error": "a browser login session is required to prepare an L1 recommendation"}
	}
	statement := strings.TrimSpace(inputString(input, "user_statement"))
	recommendation := strings.TrimSpace(inputString(input, "recommendation"))
	if len([]rune(statement)) < minDeveloperAccessReasonRunes || len([]rune(statement)) > maxDeveloperAccessDraftRunes {
		return map[string]any{"ok": false, "status": "statement_invalid", "error": "user statement must contain 5 to 2000 characters"}
	}
	if len([]rune(recommendation)) < minDeveloperAccessRecommendationRunes || len([]rune(recommendation)) > maxDeveloperAccessDraftRunes {
		return map[string]any{"ok": false, "status": "recommendation_invalid", "error": "AI recommendation must contain 20 to 2000 characters"}
	}
	draft := assistantL1RecommendationDraft{
		UserStatement:  statement,
		Recommendation: recommendation,
	}
	if attribution, ok := promptPresetRef(c); ok {
		draft.PresetId = attribution.PresetId
		draft.PresetGeneration = attribution.Generation
		draft.PresetVersion = attribution.Version
	}
	payload, err := json.Marshal(draft)
	if err != nil {
		return map[string]any{"ok": false, "error": "AI recommendation could not be prepared"}
	}
	confirmationToken, _, err := model.CreateAuthFlow(model.AuthFlowCreate{
		Purpose:   model.AuthFlowPurposeAssistantL1,
		UserId:    userID,
		SessionId: sessionID,
		Payload:   string(payload),
		ExpiresAt: time.Now().Add(assistantRecommendationTTL),
	})
	if err != nil {
		return map[string]any{"ok": false, "error": "AI recommendation confirmation could not be created"}
	}
	action := map[string]any{
		"type":               "l1_recommendation",
		"user_statement":     statement,
		"recommendation":     recommendation,
		"confirmation_token": confirmationToken,
	}
	c.Set(assistantClientActionKey, action)
	return map[string]any{
		"ok":      true,
		"status":  "confirmation_required",
		"action":  "l1_recommendation",
		"message": "Explain that this recommendation is only a draft. Ask the user to review and explicitly confirm it in the UI; administrator approval is still required.",
	}
}

func executeAssistantCostTool(input map[string]any) map[string]any {
	inputTokens, okInput := inputNumber(input, "input_tokens")
	outputTokens, okOutput := inputNumber(input, "output_tokens")
	inputPrice, okInputPrice := inputNumber(input, "input_usd_per_million")
	outputPrice, okOutputPrice := inputNumber(input, "output_usd_per_million")
	if !okInput || !okOutput || !okInputPrice || !okOutputPrice || inputTokens < 0 || outputTokens < 0 || inputPrice < 0 || outputPrice < 0 {
		return map[string]any{"ok": false, "error": "token counts and prices must be non-negative numbers"}
	}
	ratio := 1.0
	if suppliedRatio, exists := inputNumber(input, "group_ratio"); exists {
		ratio = suppliedRatio
	}
	if ratio < 0 || math.IsNaN(ratio) || math.IsInf(ratio, 0) {
		return map[string]any{"ok": false, "error": "group ratio must be a non-negative finite number"}
	}
	inputCost := inputTokens / 1_000_000 * inputPrice
	outputCost := outputTokens / 1_000_000 * outputPrice
	return map[string]any{
		"ok":              true,
		"input_cost_usd":  inputCost * ratio,
		"output_cost_usd": outputCost * ratio,
		"total_cost_usd":  (inputCost + outputCost) * ratio,
		"group_ratio":     ratio,
		"formula":         "(input_tokens / 1,000,000 × input price + output_tokens / 1,000,000 × output price) × group ratio",
	}
}

func executeAssistantMathTool(input map[string]any) map[string]any {
	expression := strings.TrimSpace(inputString(input, "expression"))
	if expression == "" {
		return map[string]any{"ok": false, "error": "a math expression is required"}
	}
	if len(expression) > assistantMathExpressionMaxBytes || !assistantMathExpressionPattern.MatchString(expression) {
		return map[string]any{"ok": false, "error": "expression contains unsupported characters or is too long"}
	}

	environment := map[string]any{
		"pi":  math.Pi,
		"e":   math.E,
		"abs": math.Abs, "sqrt": math.Sqrt, "cbrt": math.Cbrt,
		"pow": math.Pow, "exp": math.Exp, "ln": math.Log, "log10": math.Log10,
		"sin": math.Sin, "cos": math.Cos, "tan": math.Tan,
		"asin": math.Asin, "acos": math.Acos, "atan": math.Atan, "atan2": math.Atan2,
		"hypot": math.Hypot, "floor": math.Floor, "ceil": math.Ceil,
		"round": math.Round, "trunc": math.Trunc, "min": math.Min, "max": math.Max,
		"percent": func(value float64) float64 { return value / 100 },
		"clamp": func(value, minimum, maximum float64) float64 {
			return math.Min(math.Max(value, minimum), maximum)
		},
	}
	variables := map[string]float64{}
	if rawVariables, exists := input["variables"]; exists {
		variableMap, ok := rawVariables.(map[string]any)
		if !ok || len(variableMap) > assistantMathVariablesMax {
			return map[string]any{"ok": false, "error": "variables must be an object with at most 32 numeric entries"}
		}
		for name := range variableMap {
			if !assistantMathVariablePattern.MatchString(name) {
				return map[string]any{"ok": false, "error": "variable names must be simple ASCII identifiers"}
			}
			if _, reserved := environment[name]; reserved {
				return map[string]any{"ok": false, "error": "variable name conflicts with a math function or constant"}
			}
			value, ok := inputNumber(variableMap, name)
			if !ok {
				return map[string]any{"ok": false, "error": "all variables must be finite numbers"}
			}
			variables[name] = value
			environment[name] = value
		}
	}

	program, err := expr.Compile(expression, expr.Env(environment), expr.AsFloat64())
	if err != nil {
		return map[string]any{"ok": false, "error": "invalid math expression"}
	}
	output, err := expr.Run(program, environment)
	if err != nil {
		return map[string]any{"ok": false, "error": "math expression could not be evaluated"}
	}
	result, ok := output.(float64)
	if !ok || math.IsNaN(result) || math.IsInf(result, 0) {
		return map[string]any{"ok": false, "error": "math result is not a finite number"}
	}
	return map[string]any{
		"ok":         true,
		"expression": expression,
		"variables":  variables,
		"result":     result,
	}
}

func executeAssistantModelsTool(userID int) map[string]any {
	if userID <= 0 {
		return map[string]any{"ok": false, "error": "signed-in account is unavailable"}
	}
	user, err := model.GetUserCache(userID)
	if err != nil {
		return map[string]any{"ok": false, "error": "available models could not be loaded"}
	}
	access, err := model.GetDeveloperAccessStateForUserBase(user)
	if err != nil {
		return map[string]any{"ok": false, "error": "developer access could not be loaded"}
	}
	acceptUnsetRatioModel := modelListAcceptsUnsetRatioModel(userID)
	if !access.Granted {
		models := getPublicCatalogModelIDsForUser(userID)
		if len(models) == 0 {
			return map[string]any{
				"ok":             false,
				"status":         "catalog_unavailable",
				"error":          "the live public model catalog is temporarily unavailable",
				"catalog_source": "live_pricing_catalog",
				"next_step":      "Tell the user the live catalog is warming and do not guess or substitute model IDs.",
			}
		}
		return map[string]any{
			"ok":                           true,
			"status":                       "public_preview",
			"model_ids":                    models,
			"model_list_path":              "/pricing",
			"availability_scope":           "public_preview_not_account_entitlement",
			"developer_access_granted":     false,
			"account_model_access_locked":  true,
			"preview_matches_live_catalog": true,
			"catalog_source":               "live_pricing_catalog",
			"next_step":                    "Answer with these exact live catalog IDs. Explain that L1 is required to use them, but do not claim that the models are unknown.",
		}
	}
	if user.Role >= common.RoleAdminUser {
		pricing := getPricingCache()
		if pricing == nil {
			return map[string]any{
				"ok":                  false,
				"status":              "pricing_cache_unready",
				"error":               "available models are temporarily unavailable while the pricing cache warms",
				"pricing_cache_ready": false,
				"next_step":           "Retry the live model inventory after the pricing cache is ready; do not infer that no models are enabled.",
			}
		}
		modelSet := make(map[string]struct{}, len(pricing))
		for _, candidate := range pricing {
			if strings.TrimSpace(candidate.ModelName) != "" && modelListIncludesModel(candidate.ModelName, acceptUnsetRatioModel) {
				modelSet[candidate.ModelName] = struct{}{}
			}
		}
		models := make([]string, 0, len(modelSet))
		for modelID := range modelSet {
			models = append(models, modelID)
		}
		sort.Strings(models)
		if len(models) == 0 {
			return map[string]any{
				"ok":                  false,
				"status":              "pricing_cache_empty",
				"error":               "available models are temporarily unavailable because the pricing cache contains no usable model IDs",
				"pricing_cache_ready": false,
				"next_step":           "Check the live pricing/model configuration and retry; do not infer that no models are enabled.",
			}
		}
		groups := assistantAdminConfiguredGroups()
		groupNames := make([]string, 0, len(groups))
		for group := range groups {
			groupNames = append(groupNames, group)
		}
		sort.Strings(groupNames)
		return map[string]any{
			"ok":                         true,
			"groups":                     groupNames,
			"model_ids":                  models,
			"model_list_path":            "/models",
			"selection_required":         true,
			"assistant_model_is_client":  false,
			"administrator_scope":        "all_enabled_models_and_configured_groups",
			"sensitive_settings_omitted": true,
		}
	}
	groups := service.GetUserUsableGroups(user.Group)
	groupNames := make([]string, 0, len(groups))
	for group := range groups {
		groupNames = append(groupNames, group)
	}
	sort.Strings(groupNames)
	models, err := service.GetGroupsEnabledModelsWithError(groupNames)
	if err != nil {
		return map[string]any{
			"ok":        false,
			"status":    "catalog_unavailable",
			"error":     "available models are temporarily unavailable while the model catalog is loading",
			"next_step": "Retry the live model inventory; do not infer that no models are enabled.",
		}
	}
	filteredModels := make([]string, 0, len(models))
	for _, modelID := range models {
		if modelListIncludesModel(modelID, acceptUnsetRatioModel) {
			filteredModels = append(filteredModels, modelID)
		}
	}
	models = filteredModels
	sort.Strings(models)
	return map[string]any{
		"ok":                        true,
		"groups":                    groupNames,
		"model_ids":                 models,
		"model_list_path":           "/models",
		"selection_required":        true,
		"assistant_model_is_client": false,
	}
}

func executeAssistantModelPricingTool(userID int, input map[string]any) map[string]any {
	if userID <= 0 {
		return map[string]any{"ok": false, "error": "signed-in account is unavailable"}
	}
	modelID := inputString(input, "model_id")
	if modelID == "" {
		return map[string]any{
			"ok":        false,
			"status":    "model_required",
			"error":     "an exact model ID is required",
			"next_step": "Ask the user to choose a model or call get_available_models first.",
		}
	}
	user, err := model.GetUserCache(userID)
	if err != nil {
		return map[string]any{"ok": false, "error": "account pricing access could not be loaded"}
	}
	isAdministrator := user.Role >= common.RoleAdminUser
	access, err := model.GetDeveloperAccessStateForUserBase(user)
	if err != nil {
		return map[string]any{"ok": false, "error": "developer access could not be loaded"}
	}
	previewOnly := !access.Granted
	usableGroups := service.GetUserUsableGroups(user.Group)
	if isAdministrator {
		usableGroups = assistantAdminConfiguredGroups()
	} else if previewOnly {
		usableGroups = map[string]string{
			"default": setting.GetUsableGroupDescription("default"),
		}
	}
	requestedGroup := inputString(input, "group")
	if requestedGroup != "" {
		if _, ok := usableGroups[requestedGroup]; !ok {
			return map[string]any{"ok": false, "status": "invalid_group", "error": "the requested group is not available for this account"}
		}
	}
	// Public preview pricing must use the same billable-model predicate as the
	// public catalog.  A pricing-cache row without a configured model ratio is
	// not a free model and must never be exposed as a zero-dollar reference.
	if previewOnly && !modelListIncludesModel(modelID, false) {
		return map[string]any{
			"ok":        false,
			"status":    "model_not_in_public_preview",
			"error":     "the exact model ID is not in the current public preview",
			"next_step": "Call get_available_models and answer with the exact public preview IDs instead of guessing.",
		}
	}

	pricing := getPricingCache()
	if pricing == nil {
		return map[string]any{"ok": false, "error": "live pricing is temporarily unavailable"}
	}
	var selected *model.Pricing
	for index := range pricing {
		candidate := &pricing[index]
		if candidate.ModelName != modelID {
			continue
		}
		if len(filterPricingByUsableGroups([]model.Pricing{*candidate}, usableGroups)) == 0 {
			continue
		}
		selected = candidate
		break
	}
	if selected == nil {
		if previewOnly {
			return map[string]any{
				"ok":        false,
				"status":    "model_not_in_public_preview",
				"error":     "the exact model ID is not in the current public preview",
				"next_step": "Call get_available_models and answer with the exact public preview IDs instead of guessing.",
			}
		}
		return map[string]any{
			"ok":        false,
			"status":    "model_unavailable",
			"error":     "the exact model ID is not available to this account",
			"next_step": "Call get_available_models and ask the user to choose one of the returned IDs.",
		}
	}

	groupIDs := make([]string, 0, len(usableGroups))
	for groupID := range usableGroups {
		if requestedGroup != "" && groupID != requestedGroup {
			continue
		}
		if !common.StringsContains(selected.EnableGroup, "all") && !common.StringsContains(selected.EnableGroup, groupID) {
			continue
		}
		groupIDs = append(groupIDs, groupID)
	}
	sort.Strings(groupIDs)
	trustLevel := 0
	trustDiscountRatio := 1.0
	if !previewOnly {
		trust, trustErr := model.GetTrustLevelInfoForUserBase(user)
		if trustErr != nil {
			return map[string]any{"ok": false, "error": "trust-level pricing could not be loaded"}
		}
		trustLevel = trust.Level
		trustDiscountRatio = trust.DiscountRatio
	}
	configuredRatios := ratio_setting.GetGroupRatioCopy()
	prices := make([]map[string]any, 0, len(groupIDs))
	for _, groupID := range groupIDs {
		baseGroupRatio, configured := configuredRatios[groupID]
		if !configured {
			baseGroupRatio = 1
		}
		if override, ok := ratio_setting.GetGroupGroupRatio(user.Group, groupID); ok && !previewOnly {
			baseGroupRatio = override
		}
		groupRatio := baseGroupRatio * trustDiscountRatio
		entry := map[string]any{
			"group":                groupID,
			"group_description":    usableGroups[groupID],
			"base_group_ratio":     baseGroupRatio,
			"trust_discount_ratio": trustDiscountRatio,
			"group_ratio":          groupRatio,
		}
		if selected.QuotaType == 0 && selected.BillingMode != "tiered_expr" {
			inputRate := selected.ModelRatio * 2 * groupRatio
			entry["input_usd_per_million"] = inputRate
			entry["output_usd_per_million"] = inputRate * selected.CompletionRatio
			if selected.CacheRatio != nil {
				entry["cache_read_usd_per_million"] = inputRate * *selected.CacheRatio
			}
			if selected.CreateCacheRatio != nil {
				entry["cache_write_usd_per_million"] = inputRate * *selected.CreateCacheRatio
			}
		} else if selected.QuotaType == 1 {
			entry["request_usd"] = selected.ModelPrice * groupRatio
		}
		prices = append(prices, entry)
	}
	if len(prices) == 0 {
		return map[string]any{"ok": false, "error": "no usable pricing group was found for this model"}
	}
	pricingScope := "assistant_account"
	calculationInstruction := "The returned USD prices already include the routing-group ratio and the live trust-level discount. Pass group_ratio=1 to calculate_cost so neither multiplier is applied twice."
	if previewOnly {
		pricingScope = "public_preview_reference"
		calculationInstruction = "The returned USD reference prices include the public default-group ratio and no account-specific discount. Pass group_ratio=1 to calculate_cost and explain that L1 access is still required to use the model."
	}

	return map[string]any{
		"ok":                          true,
		"model_id":                    selected.ModelName,
		"trust_level":                 trustLevel,
		"trust_discount_ratio":        trustDiscountRatio,
		"quota_type":                  selected.QuotaType,
		"billing_mode":                selected.BillingMode,
		"billing_expression":          selected.BillingExpr,
		"prices":                      prices,
		"supported_endpoint_types":    selected.SupportedEndpointTypes,
		"administrator_scope":         isAdministrator,
		"pricing_scope":               pricingScope,
		"account_model_access_locked": previewOnly,
		"calculation_instruction":     calculationInstruction,
	}
}

func executeAssistantPlanOffersTool(userID int) map[string]any {
	if userID <= 0 {
		return map[string]any{"ok": false, "error": "signed-in account is unavailable"}
	}
	user, err := model.GetUserById(userID, false)
	if err != nil {
		return map[string]any{"ok": false, "error": "account access could not be loaded"}
	}
	access, err := model.GetDeveloperAccessStateForUser(user)
	if err != nil {
		return map[string]any{"ok": false, "error": "developer access could not be loaded"}
	}
	complianceConfirmed := operation_setting.IsPaymentComplianceConfirmed()
	checkoutAvailable := paymentGatewayAvailabilityForUser(user, complianceConfirmed, time.Now()).hasPayment()
	paymentHidden := model.IsPaymentRestricted(user) && !checkoutAvailable
	result := map[string]any{
		"ok":                           true,
		"developer_access_granted":     access.Granted,
		"read_only":                    !checkoutAvailable,
		"checkout_available":           checkoutAvailable,
		"payment_hidden":               paymentHidden,
		"plans":                        []SubscriptionPlanDTO{},
		"topup_discounts":              map[int]float64{},
		"payment_compliance_confirmed": complianceConfirmed,
	}
	if paymentHidden {
		result["message"] = "Payment channels are unavailable for this account; do not direct the user to checkout."
		result["status"] = "payment_restricted"
	} else if !complianceConfirmed {
		result["message"] = "Current plan offers are view-only until payment compliance is confirmed."
	} else if !checkoutAvailable {
		result["message"] = "Current plan offers are view-only because no eligible checkout method is available."
	}
	if model.DB == nil {
		return map[string]any{"ok": false, "error": "subscription plans are temporarily unavailable"}
	}
	var plans []model.SubscriptionPlan
	if err := model.DB.Where("enabled = ?", true).Order("sort_order desc, id desc").Find(&plans).Error; err != nil {
		return map[string]any{"ok": false, "error": "subscription plans could not be loaded"}
	}
	planValues := make([]SubscriptionPlanDTO, 0, len(plans))
	for _, plan := range plans {
		plan.NormalizeDefaults()
		planValues = append(planValues, SubscriptionPlanDTO{Plan: plan})
	}
	discountValues := make(map[int]float64, len(operation_setting.GetPaymentSetting().AmountDiscount))
	if !paymentHidden {
		for amount, multiplier := range operation_setting.GetPaymentSetting().AmountDiscount {
			discountValues[amount] = multiplier
		}
	}
	result["plans"] = planValues
	result["topup_discounts"] = discountValues
	return result
}

func executeAssistantInvitationTool(userID int) map[string]any {
	if result, blocked := assistantDeveloperCapabilityRequired(userID, "invitation rewards"); blocked {
		return result
	}
	if userID <= 0 {
		return map[string]any{"ok": false, "error": "signed-in account is unavailable"}
	}
	user, err := model.GetUserById(userID, false)
	if err != nil {
		return map[string]any{"ok": false, "error": "invitation information could not be loaded"}
	}
	result := map[string]any{
		"ok":                           true,
		"affiliate_code_available":     strings.TrimSpace(user.AffCode) != "",
		"affiliate_code_path":          "/aff",
		"invited_count":                user.AffCount,
		"pending_reward_usd":           float64(user.AffQuota) / common.QuotaPerUnit,
		"total_reward_usd":             float64(user.AffHistoryQuota) / common.QuotaPerUnit,
		"reward_per_inviter_usd":       float64(common.QuotaForInviter) / common.QuotaPerUnit,
		"reward_per_invitee_usd":       float64(common.QuotaForInvitee) / common.QuotaPerUnit,
		"promotional_rewards_eligible": !model.IsDisposableEmail(user.Email),
		"payment_compliance_confirmed": operation_setting.IsPaymentComplianceConfirmed(),
		"next_step":                    "Open the invitation page to generate or copy the current invitation code.",
	}
	if model.IsDisposableEmail(user.Email) {
		result["message"] = "Known disposable email domains are not eligible for new-account or invitation promotional credits. Use a durable email for legitimate referrals; ordinary account access and administrator review remain available."
	}
	if !operation_setting.IsPaymentComplianceConfirmed() {
		result["message"] = "Reward configuration is shown for explanation only; payment-related rewards remain subject to the platform compliance setting."
	}
	return result
}

func executeAssistantBountyTool() map[string]any {
	fee := model.GetOpenSourceBountyFeeConfig()
	return map[string]any{
		"ok": true,
		"steps": []string{
			"Open the open-source bounties page and choose create project.",
			"Provide the repository, issue or pull request, acceptance criteria, gross reward, and number of fixes.",
			"Review the platform fee, net escrow, and total balance debit before publishing.",
			"Publish only after explicitly confirming the funding action.",
			"Review submitted evidence; when work is accepted, settle the fix and optionally add a separate non-refundable tip.",
			"Use the dispute flow when publisher and contributor cannot agree; do not fabricate evidence.",
		},
		"platform_fee_percent": fee.RatePercent,
		"page":                 "/open-source-bounties",
		"message":              "The public platform fee helps fund AI customer-service token costs. A bounty publisher may also give a contributor a separate tip; exact charges and escrow are shown before confirmation.",
	}
}

const assistantBountyReadMaxRows = 50

// executeAssistantBountyDataTool is the in-process equivalent of the
// read-only open_source_bounties MCP tools. It deliberately uses the actor's
// session identity rather than minting or reading a personal MCP token. The
// model functions are the same permission-aware functions used by the MCP
// handlers, and no write operation is exposed here.
func executeAssistantBountyDataTool(userID int, input map[string]any) map[string]any {
	if userID <= 0 {
		return map[string]any{"ok": false, "status": "signed_in_required", "error": "a signed-in account is required to read bounty data"}
	}
	view := strings.ToLower(inputString(input, "view"))
	if view == "" {
		view = "board"
	}
	mcpToolName := map[string]string{
		"board": "open_source_bounties.list", "detail": "open_source_bounties.get",
		"accepted": "open_source_bounties.list_accepted", "owned": "open_source_bounties.list_owned",
		"disputes": "open_source_bounties.list_disputes",
	}[view]
	result := map[string]any{
		"ok":                                    true,
		"view":                                  view,
		"read_only":                             true,
		"mcp_equivalent":                        mcpToolName,
		"write_actions_require_ui_confirmation": true,
	}
	fail := func(err error) map[string]any {
		result["ok"] = false
		result["error"] = "bounty data could not be loaded"
		if code := model.OpenSourceBountyErrorCode(err); code != "OPEN_SOURCE_BOUNTY_INTERNAL_ERROR" {
			result["status"] = code
		}
		return result
	}
	privateRead := func() bool {
		if err := model.RequireOpenSourceBountyDeveloperAccess(userID); err != nil {
			result["ok"] = false
			result["status"] = "l1_required"
			result["error"] = "L1 developer access is required for private bounty data"
			result["next_step"] = "Continue the L1 onboarding conversation if you need your accepted, owned, or dispute records."
			return false
		}
		return true
	}

	switch view {
	case "board":
		page, ok := assistantBountyReadInt(input, "page", 1, 1, 1000000)
		if !ok {
			return map[string]any{"ok": false, "status": "invalid_input", "error": "page must be an integer from 1 to 1000000"}
		}
		pageSize, ok := assistantBountyReadInt(input, "page_size", 20, 1, assistantBountyReadMaxRows)
		if !ok {
			return map[string]any{"ok": false, "status": "invalid_input", "error": "page_size must be an integer from 1 to 50"}
		}
		items, total, err := model.ListOpenSourceBounties(userID, page, pageSize)
		if err != nil {
			return fail(err)
		}
		result["data"] = map[string]any{"items": items, "total": total, "page": page, "page_size": pageSize}
		return result
	case "detail":
		projectID, ok := assistantBountyReadInt(input, "project_id", 0, 1, int(^uint(0)>>1))
		if !ok {
			return map[string]any{"ok": false, "status": "invalid_input", "error": "project_id must be a positive integer"}
		}
		detail, err := model.GetOpenSourceBountyDetail(userID, projectID)
		if err != nil {
			return fail(err)
		}
		if detail == nil {
			return map[string]any{"ok": false, "status": "not_found", "error": "bounty project was not found"}
		}
		bounded := *detail
		bounded.Challenges, bounded.Ledger = boundAssistantBountyDetail(detail.Challenges, detail.Ledger)
		result["data"] = &bounded
		result["truncated"] = len(detail.Challenges) > len(bounded.Challenges) || len(detail.Ledger) > len(bounded.Ledger)
		return result
	case "accepted":
		if !privateRead() {
			return result
		}
		items, err := model.ListAcceptedOpenSourceBountiesLimited(userID, assistantBountyReadMaxRows+1)
		if err != nil {
			return fail(err)
		}
		result["data"], result["truncated"] = boundAssistantBountyRows(items)
		return result
	case "owned":
		if !privateRead() {
			return result
		}
		items, err := model.ListOwnedOpenSourceBountiesLimited(userID, assistantBountyReadMaxRows+1)
		if err != nil {
			return fail(err)
		}
		result["data"], result["truncated"] = boundAssistantBountyRows(items)
		return result
	case "disputes":
		if !privateRead() {
			return result
		}
		limit, ok := assistantBountyReadInt(input, "limit", assistantBountyReadMaxRows, 1, 100)
		if !ok {
			return map[string]any{"ok": false, "status": "invalid_input", "error": "limit must be an integer from 1 to 100"}
		}
		status := inputString(input, "status")
		items, err := model.ListOpenSourceBountyDisputesFiltered(userID, bountyMCPIsAdmin(userID), status, limit)
		if err != nil {
			return fail(err)
		}
		result["data"] = items
		return result
	default:
		return map[string]any{"ok": false, "status": "invalid_input", "error": "view must be board, detail, accepted, owned, or disputes"}
	}
}

func assistantBountyReadInt(input map[string]any, key string, fallback int, minimum int, maximum int) (int, bool) {
	value, exists := inputNumber(input, key)
	if !exists {
		return fallback, true
	}
	if value != math.Trunc(value) || value < float64(minimum) || value > float64(maximum) {
		return 0, false
	}
	return int(value), true
}

func boundAssistantBountyDetail(challenges []model.OpenSourceBountyChallengeView, ledger []model.OpenSourceBountyLedger) ([]model.OpenSourceBountyChallengeView, []model.OpenSourceBountyLedger) {
	if len(challenges) > assistantBountyReadMaxRows {
		challenges = challenges[:assistantBountyReadMaxRows]
	}
	if len(ledger) > assistantBountyReadMaxRows {
		ledger = ledger[:assistantBountyReadMaxRows]
	}
	return challenges, ledger
}

func boundAssistantBountyRows[T any](rows []T) ([]T, bool) {
	if len(rows) <= assistantBountyReadMaxRows {
		return rows, false
	}
	return rows[:assistantBountyReadMaxRows], true
}

func executeAssistantUsageTool(userID int, input map[string]any) map[string]any {
	if result, blocked := assistantDeveloperCapabilityRequired(userID, "usage statistics"); blocked {
		return result
	}
	days := 30
	if value, exists := inputNumber(input, "days"); exists {
		days = int(value)
	}
	if days < 1 || days > 90 {
		return map[string]any{"ok": false, "error": "days must be between 1 and 90"}
	}
	end := time.Now().Unix()
	start := time.Now().Add(-time.Duration(days) * 24 * time.Hour).Unix()
	summary, err := model.GetAssistantUsageSummary(userID, start, end, 20)
	if err != nil {
		return map[string]any{"ok": false, "error": "historical usage could not be loaded"}
	}
	return map[string]any{
		"ok":       true,
		"days":     days,
		"source":   "consume logs",
		"summary":  summary,
		"raw_logs": false,
	}
}

func executeAssistantSearchTool(c *gin.Context, input map[string]any) map[string]any {
	query := inputString(input, "query")
	ctx := context.Background()
	if c != nil && c.Request != nil {
		ctx = c.Request.Context()
	}
	response, err := ExecuteAssistantSearch(ctx, query)
	if err != nil {
		return map[string]any{
			"ok":         false,
			"configured": response.Configured,
			"query":      response.Query,
			"status":     response.Status,
			"error":      err.Error(),
		}
	}
	return map[string]any{
		"ok":         true,
		"configured": response.Configured,
		"query":      response.Query,
		"status":     response.Status,
		"results":    response.Results,
	}
}

func executeAssistantAccountTool(userID int) map[string]any {
	if userID <= 0 {
		return map[string]any{"ok": false, "error": "signed-in account is unavailable"}
	}
	user, err := model.GetUserCache(userID)
	if err != nil {
		return map[string]any{"ok": false, "error": "account access could not be loaded"}
	}
	access, err := model.GetDeveloperAccessStateForUserBase(user)
	if err != nil {
		return map[string]any{"ok": false, "error": "developer access could not be loaded"}
	}
	trust, err := model.GetTrustLevelInfoForUserBase(user)
	if err != nil {
		return map[string]any{"ok": false, "error": "trust level could not be loaded"}
	}
	result := map[string]any{
		"ok":                       true,
		"trust_level":              trust.Level,
		"developer_access_granted": access.Granted,
		"paid_activation_complete": access.PaidActivationComplete,
		"console_activated":        user.ConsoleActivatedAt > 0,
	}
	request, requestErr := model.GetDeveloperAccessRequest(userID)
	if requestErr != nil {
		return map[string]any{"ok": false, "error": "L1 recommendation status could not be loaded"}
	}
	if request != nil {
		result["l1_request"] = map[string]any{
			"status":            request.Status,
			"source":            request.Source,
			"user_statement":    request.Reason,
			"ai_recommendation": request.AIRecommendation,
			"admin_note":        request.AdminNote,
			"created_at":        request.CreatedAt,
			"reviewed_at":       request.ReviewedAt,
		}
	}
	if access.Granted {
		result["next_step"] = "Continue setup through the assistant; API-key creation still requires explicit UI confirmation."
	} else if request != nil && request.Status == model.DeveloperAccessRequestPending {
		result["next_step"] = "Tell the user the recommendation is pending administrator review."
	} else {
		result["next_step"] = "Continue the onboarding conversation and prepare an L1 recommendation only after collecting a concrete use case."
	}
	return result
}

// quotePOSIXShellLiteral returns a single shell word without leaving any part of
// value open to expansion or command substitution.
func quotePOSIXShellLiteral(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

// quotePowerShellLiteral returns a single-quoted PowerShell string. PowerShell
// represents a literal apostrophe inside such a string as two apostrophes.
func quotePowerShellLiteral(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func executeAssistantSetupTool(userID int, input map[string]any) map[string]any {
	platform := strings.ToLower(strings.TrimSpace(inputString(input, "platform")))
	topic := strings.ToLower(strings.TrimSpace(inputString(input, "topic")))
	if platform != "windows" && platform != "linux" && platform != "macos" {
		return map[string]any{"ok": false, "error": "platform must be windows, linux, or macos"}
	}
	if topic != "claude-code" && topic != "cc-switch" && topic != "claude-desktop" && topic != "chatgpt-client" && topic != "codex" && topic != "cursor" && topic != "open-webui" && topic != "other-openai-compatible" {
		return map[string]any{"ok": false, "error": "topic is not supported"}
	}
	rootURL := strings.TrimRight(system_setting.ServerAddress, "/")
	if rootURL == "" {
		rootURL = "<SERVICE_ROOT_URL>"
	}
	openAIBaseURL := rootURL + "/v1"
	if rootURL == "<SERVICE_ROOT_URL>" {
		openAIBaseURL = "<OPENAI_BASE_URL>"
	}
	clientModel := strings.TrimSpace(inputString(input, "model_id"))
	if clientModel == "" {
		return map[string]any{
			"ok":        false,
			"status":    "model_required",
			"error":     "an exact model ID is required",
			"next_step": "Call get_available_models and use one exact model_ids value.",
		}
	}
	modelsResult := executeAssistantModelsTool(userID)
	if ok, _ := modelsResult["ok"].(bool); !ok {
		return modelsResult
	}
	modelIDs, ok := modelsResult["model_ids"].([]string)
	if !ok || !slices.Contains(modelIDs, clientModel) {
		status := "model_unavailable"
		if modelsResult["status"] == "public_preview" {
			status = "model_not_in_public_preview"
		}
		return map[string]any{
			"ok":                  false,
			"status":              status,
			"error":               "the requested model ID is not available in the signed-in account catalog",
			"available_model_ids": modelIDs,
			"next_step":           "Use one exact available_model_ids value; do not guess or rewrite the model ID.",
		}
	}
	accountModelAccessLocked, _ := modelsResult["account_model_access_locked"].(bool)
	developerAccessGranted := !accountModelAccessLocked
	if reportedAccess, reported := modelsResult["developer_access_granted"].(bool); reported {
		developerAccessGranted = reportedAccess
	}
	securityNote := "Create the key in this console, never paste an existing secret into chat, and test with a newly opened terminal or client session."
	credentialStep := "Create an API key in this console and replace only the <YOUR_API_KEY> placeholder."
	credentialPhrase := "a newly created API key"
	testStep := "Send a short test request after configuring the client."
	if accountModelAccessLocked {
		securityNote = "API key creation and authenticated requests remain locked until L1 approval. You can install the client and review the placeholder configuration now without creating or sharing a key."
		credentialStep = "Keep the <YOUR_API_KEY> placeholder while access is locked; after L1 approval, create a key in this console and replace only that placeholder."
		credentialPhrase = "the <YOUR_API_KEY> placeholder (replace it with a new key only after L1 approval)"
		testStep = "After L1 approval and key creation, open a new client session and send a short test request."
	}
	lockedAwareStep := func(unlocked, locked string) string {
		if accountModelAccessLocked {
			return locked
		}
		return unlocked
	}

	result := map[string]any{
		"ok":                          true,
		"platform":                    platform,
		"topic":                       topic,
		"service_root":                rootURL,
		"openai_base_url":             openAIBaseURL,
		"client_model_id":             clientModel,
		"api_key":                     "<YOUR_API_KEY>",
		"developer_access_granted":    developerAccessGranted,
		"account_model_access_locked": accountModelAccessLocked,
		"security_note":               securityNote,
	}

	switch topic {
	case "claude-code":
		installCommand := "curl -fsSL https://claude.ai/install.sh | bash"
		configuration := fmt.Sprintf("export ANTHROPIC_BASE_URL=%s\nexport ANTHROPIC_AUTH_TOKEN='<YOUR_API_KEY>'\nexport ANTHROPIC_MODEL=%s\nclaude", quotePOSIXShellLiteral(rootURL), quotePOSIXShellLiteral(clientModel))
		if platform == "windows" {
			installCommand = "winget install Anthropic.ClaudeCode"
			configuration = fmt.Sprintf("$env:ANTHROPIC_BASE_URL=%s\n$env:ANTHROPIC_AUTH_TOKEN='<YOUR_API_KEY>'\n$env:ANTHROPIC_MODEL=%s\nclaude", quotePowerShellLiteral(rootURL), quotePowerShellLiteral(clientModel))
		} else if platform == "macos" {
			installCommand = "brew install --cask claude-code"
		}
		result["install_command"] = installCommand
		result["configuration"] = configuration
		result["endpoint_format"] = "Anthropic Messages; use the service root without /v1"
		result["steps"] = []string{
			"Install Claude Code with the command returned by this tool, then run claude --version.",
			credentialStep,
			lockedAwareStep("Apply the returned environment variables in a terminal opened for the project, then run claude.", testStep),
		}
		result["official_docs"] = "https://code.claude.com/docs/en/setup"
	case "cc-switch":
		installGuide := "Download CC-Switch-v{version}-Windows.msi from the official GitHub Releases page."
		if platform == "macos" {
			installGuide = "brew install --cask cc-switch"
		} else if platform == "linux" {
			installGuide = "Download the official AppImage or distribution package; on Arch Linux use paru -S cc-switch-bin."
		}
		result["install_guide"] = installGuide
		result["provider"] = map[string]any{
			"application": "Claude",
			"env": map[string]string{
				"ANTHROPIC_BASE_URL":   rootURL,
				"ANTHROPIC_AUTH_TOKEN": "<YOUR_API_KEY>",
				"ANTHROPIC_MODEL":      clientModel,
			},
		}
		result["endpoint_format"] = "Anthropic Messages; use the service root without /v1"
		result["steps"] = []string{
			"Install CC Switch only from its official site or GitHub Releases.",
			"Create or select an API key in this console; the key stays in a shielded private card.",
			"Use Import to CC Switch from that private card (or the key's CC Switch action on /keys). The UI constructs the ccswitch:// link and CC Switch shows an import confirmation.",
			"Select Claude, add a Custom provider, and enter the returned service root, model ID, and " + credentialPhrase + ".",
			lockedAwareStep("Confirm the import or save and enable the provider, then open a new terminal and send a short test with Claude Code.", testStep),
		}
		result["cc_switch_import"] = map[string]any{
			"supported":   true,
			"protocol":    "ccswitch://v1/import",
			"resource":    "provider",
			"application": "claude",
			"endpoint":    rootURL,
			"model":       clientModel,
			"api_key":     "<PRIVATE_API_KEY>",
			"link_parameters": map[string]any{
				"resource": "provider",
				"app":      "claude",
				"name":     "LMM",
				"endpoint": rootURL,
				"apiKey":   "<PRIVATE_API_KEY>",
				"model":    clientModel,
				"homepage": rootURL,
				"enabled":  true,
			},
			"build_instructions": "After the user confirms and creates a key, the assistant UI replaces <PRIVATE_API_KEY> client-side and opens the CC Switch import confirmation. Never print the completed URL or ask the user to paste the key into chat.",
		}
		result["official_releases"] = "https://github.com/farion1231/cc-switch/releases"
		result["official_docs"] = "https://github.com/farion1231/cc-switch"
	case "claude-desktop":
		result["direct_custom_gateway_supported"] = false
		result["endpoint_format"] = "Anthropic Messages through CC Switch local routing"
		if platform == "linux" {
			result["supported"] = false
			result["limitation"] = "CC Switch currently manages third-party Claude Desktop profiles on Windows and macOS; use Claude Code on Linux for this service."
		} else {
			result["supported"] = true
			result["steps"] = []string{
				"Install and launch the official Claude Desktop app once.",
				"In CC Switch, enable Claude Desktop and import the Claude Code provider or add a custom provider.",
				"Map the Sonnet role to the returned model ID, enable local routing, then fully restart Claude Desktop.",
			}
		}
		result["official_docs"] = "https://code.claude.com/docs/en/desktop-quickstart"
		result["cc_switch_docs"] = "https://github.com/farion1231/cc-switch/blob/main/docs/user-manual/en/2-providers/2.6-claude-desktop.md"
	case "chatgpt-client":
		result["supported"] = false
		result["direct_custom_gateway_supported"] = false
		result["limitation"] = "The official ChatGPT app uses OpenAI sign-in and does not accept this service's Base URL or API key as a custom provider."
		result["recommended_alternatives"] = []string{"CC Switch", "Codex CLI", "Open WebUI", "another client that explicitly supports custom OpenAI-compatible providers"}
		result["official_download"] = "https://chatgpt.com/download/"
	case "codex":
		apiKeyCommand := "export LMM_API_KEY='<YOUR_API_KEY>'"
		if platform == "windows" {
			apiKeyCommand = "$env:LMM_API_KEY='<YOUR_API_KEY>'"
		}
		result["install_command"] = "npm install -g @openai/codex"
		result["api_key_command"] = apiKeyCommand
		result["config_path"] = "~/.codex/config.toml"
		result["config_toml"] = fmt.Sprintf("model = %q\nmodel_provider = \"lmm\"\n\n[model_providers.lmm]\nname = \"LMM\"\nbase_url = %q\nenv_key = \"LMM_API_KEY\"\nwire_api = \"responses\"", clientModel, openAIBaseURL)
		result["endpoint_format"] = "OpenAI Responses API; use the /v1 Base URL"
		result["steps"] = []string{
			"Install Codex, then create the user-level ~/.codex/config.toml with the returned provider configuration.",
			lockedAwareStep("Set LMM_API_KEY in the current shell without writing the key into config.toml.", credentialStep),
			lockedAwareStep("Run codex in a project directory and verify the provider and model shown by /status.", testStep),
		}
		result["official_docs"] = "https://developers.openai.com/codex/cli"
		result["config_reference"] = "https://developers.openai.com/codex/config-reference"
	case "cursor":
		result["endpoint_format"] = "OpenAI-compatible; use the /v1 Base URL only if the installed Cursor version exposes a custom Base URL"
		result["steps"] = []string{
			"Open Cursor Settings and check whether the installed version exposes a custom OpenAI-compatible Base URL.",
			"If supported, enter the returned /v1 Base URL, exact model ID, and " + credentialPhrase + ".",
			"If the setting is absent, do not assume the official client can use this gateway; choose CC Switch or another compatible client.",
		}
	case "open-webui":
		result["endpoint_format"] = "OpenAI-compatible; use the /v1 Base URL"
		result["steps"] = []string{
			"Open Open WebUI administrator settings and add an OpenAI-compatible connection.",
			"Enter the returned /v1 Base URL and " + credentialPhrase + ", then refresh the model list.",
			lockedAwareStep("Select the exact returned model ID and send a short test request.", testStep),
		}
		result["official_docs"] = "https://docs.openwebui.com/getting-started/quick-start/connect-a-provider/starting-with-openai-compatible/"
	case "other-openai-compatible":
		result["endpoint_format"] = "OpenAI-compatible; use the /v1 Base URL"
		result["steps"] = []string{
			"Confirm that the client explicitly supports a custom OpenAI-compatible Base URL.",
			"Enter the returned /v1 Base URL, exact model ID, and " + credentialPhrase + ".",
			lockedAwareStep("Send a short test and verify that the client uses a route supported by this service.", testStep),
		}
	}
	return result
}

func inputString(input map[string]any, key string) string {
	value, _ := input[key].(string)
	return strings.TrimSpace(value)
}

func inputNumber(input map[string]any, key string) (float64, bool) {
	value, exists := input[key]
	if !exists {
		return 0, false
	}
	var number float64
	switch typed := value.(type) {
	case float64:
		number = typed
	case json.Number:
		parsed, err := typed.Float64()
		if err != nil {
			return 0, false
		}
		number = parsed
	default:
		return 0, false
	}
	if math.IsNaN(number) || math.IsInf(number, 0) {
		return 0, false
	}
	return number, true
}
