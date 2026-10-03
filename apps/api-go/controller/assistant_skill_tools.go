package controller

import (
	"errors"
	"math"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/service"
)

const (
	recallMemoryTool  = "recall_memory"
	saveMemoryTool    = "remember_memory"
	saveProfileTool   = "remember_profile_skill"
	forgetProfileTool = "forget_profile_skill"
)

func assistantSkillTools() []assistantOpenAIToolDefinition {
	return []assistantOpenAIToolDefinition{
		{Type: "function", Function: assistantOpenAIToolFunction{
			Name:        recallMemoryTool,
			Description: "Recall only the signed-in user's own long-term memory skills when prior preferences, projects, environment, or decisions are relevant. The server fixes owner_user_id from authentication; never ask for or supply another user ID. Call this instead of claiming you remember something. An empty result means there is no matching memory.",
			Parameters: objectSchema(map[string]any{
				"query": map[string]any{"type": "string", "maxLength": 240},
				"limit": map[string]any{"type": "integer", "minimum": 1, "maximum": model.AssistantMemoryRecallMax},
			}, nil),
		}},
		{Type: "function", Function: assistantOpenAIToolFunction{
			Name:        saveMemoryTool,
			Description: "Save or update one long-term memory skill for the signed-in user only. Use only for a stable preference, recurring project, durable environment detail, or when the user asks you to remember. Never store secrets, credentials, payment data, transient requests, inferred protected traits, or security labels. Briefly tell the user what was remembered.",
			Parameters: objectSchema(map[string]any{
				"title":   map[string]any{"type": "string", "minLength": 2, "maxLength": model.AssistantMemoryMaxTitleRunes},
				"content": map[string]any{"type": "string", "minLength": 2, "maxLength": model.AssistantMemoryMaxContentRunes},
				"tags": map[string]any{
					"type": "array", "maxItems": model.AssistantMemoryMaxTags,
					"items": map[string]any{"type": "string", "maxLength": model.AssistantUserProfileMaxTagRunes},
				},
			}, []string{"title", "content"}),
		}},
		{Type: "function", Function: assistantOpenAIToolFunction{
			Name:        saveProfileTool,
			Description: "Save a coarse response-style skill for the signed-in user after the conversation provides durable evidence. This is personalization, never identity, access control, risk scoring, or a protected-trait inference. The server chooses a safe strategy for the selected profile; administrators can later review and edit it.",
			Parameters: objectSchema(map[string]any{
				"profile_key": map[string]any{
					"type": "string",
					"enum": []string{
						model.AssistantProfileTechnical, model.AssistantProfileGuided,
						model.AssistantProfileOperator, model.AssistantProfilePrivacy,
						model.AssistantProfileAccessible, model.AssistantProfileNormal,
						model.AssistantProfileSupport, model.AssistantProfileL0Applicant,
					},
				},
				"tags": map[string]any{
					"type": "array", "maxItems": 4,
					"items": map[string]any{
						"type": "string",
						"enum": []string{"advanced", "needs_steps", "cost_sensitive", "production", "privacy", "mobile", "accessibility", "support", "l0"},
					},
				},
			}, []string{"profile_key"}),
		}},
		{Type: "function", Function: assistantOpenAIToolFunction{
			Name:        forgetProfileTool,
			Description: "Forget the signed-in user's AI-created response-style profile only after the user explicitly asks to remove it. Set confirm=true only for that explicit request. Never use this for an administrator-managed profile; the server will refuse it. This changes no memories, account access, or billing data.",
			Parameters: objectSchema(map[string]any{
				"confirm": map[string]any{"type": "boolean"},
			}, []string{"confirm"}),
		}},
	}
}

func isAssistantSkillTool(name string) bool {
	switch name {
	case recallMemoryTool, saveMemoryTool, saveProfileTool, forgetProfileTool:
		return true
	default:
		return false
	}
}

func runSkillTool(name string, userID int, input map[string]any, explicitProfileForget bool) (map[string]any, bool) {
	if !isAssistantSkillTool(name) {
		return nil, false
	}
	if userID <= 0 {
		return map[string]any{"ok": false, "error": "signed-in account is unavailable"}, true
	}
	skills, err := service.OpenSkills(userID, userID)
	if err != nil {
		return map[string]any{"ok": false, "error": "user skills are unavailable"}, true
	}
	switch name {
	case recallMemoryTool:
		return recallMemory(skills, input), true
	case saveMemoryTool:
		return saveMemory(skills, input), true
	case saveProfileTool:
		return saveProfileSkill(skills, input), true
	case forgetProfileTool:
		return forgetProfileSkill(skills, input, explicitProfileForget), true
	default:
		return nil, false
	}
}

func forgetProfileSkill(skills service.UserSkills, input map[string]any, explicitProfileForget bool) map[string]any {
	confirmed, ok := input["confirm"].(bool)
	if !ok || !confirmed || !explicitProfileForget {
		return map[string]any{
			"ok": false, "status": "explicit_request_required",
			"error": "profile removal requires an explicit user request",
		}
	}
	if err := skills.ForgetProfile(); err != nil {
		if errors.Is(err, model.ErrAssistantProfileManaged) {
			return map[string]any{
				"ok": false, "status": "administrator_managed",
				"error": "profile skill is managed by an administrator and was not removed",
			}
		}
		if errors.Is(err, model.ErrAssistantProfileMissing) {
			return map[string]any{
				"ok": false, "status": "not_found",
				"error": "no AI-created profile skill exists",
			}
		}
		if errors.Is(err, model.ErrAssistantProfileOwner) {
			return map[string]any{
				"ok": false, "status": "owner_scope_required",
				"error": "profile skills can only be removed by their owner",
			}
		}
		return map[string]any{"ok": false, "error": "profile skill could not be removed"}
	}
	return map[string]any{
		"ok": true, "status": "profile_skill_forgotten", "owner_scope": "signed_in_user_only",
	}
}

func stringList(input map[string]any, key string, maximum int) ([]string, bool) {
	raw, exists := input[key]
	if !exists || raw == nil {
		return []string{}, true
	}
	values, ok := raw.([]any)
	if !ok || len(values) > maximum {
		return nil, false
	}
	result := make([]string, 0, len(values))
	for _, value := range values {
		text, ok := value.(string)
		if !ok {
			return nil, false
		}
		if text = strings.TrimSpace(text); text != "" {
			result = append(result, text)
		}
	}
	return result, true
}

func recallMemory(skills service.UserSkills, input map[string]any) map[string]any {
	limit := model.AssistantMemoryRecallMax
	if raw, supplied := inputNumber(input, "limit"); supplied {
		if raw < 1 || raw > model.AssistantMemoryRecallMax || math.Trunc(raw) != raw {
			return map[string]any{"ok": false, "error": "memory recall limit is invalid"}
		}
		limit = int(raw)
	}
	memories, err := skills.Recall(inputString(input, "query"), limit)
	if err != nil {
		return map[string]any{"ok": false, "error": "memory skills could not be recalled"}
	}
	return map[string]any{"ok": true, "owner_scope": "signed_in_user_only", "memories": memories, "match_count": len(memories)}
}

func saveMemory(skills service.UserSkills, input map[string]any) map[string]any {
	tags, ok := stringList(input, "tags", model.AssistantMemoryMaxTags)
	if !ok {
		return map[string]any{"ok": false, "error": "memory tags are invalid"}
	}
	memory, err := skills.Remember(service.MemoryDraft{
		Title: inputString(input, "title"), Content: inputString(input, "content"), Tags: tags, Enabled: true,
	})
	if err != nil {
		return map[string]any{"ok": false, "error": "memory could not be saved"}
	}
	return map[string]any{"ok": true, "status": "remembered", "owner_scope": "signed_in_user_only", "memory": memory.View()}
}

func profileSkillStrategy(profile string) string {
	switch profile {
	case model.AssistantProfileTechnical:
		return "Answer the current technical question directly. Prefer exact model IDs, endpoints, commands, and verifiable details; avoid beginner screening and payment pressure."
	case model.AssistantProfileGuided:
		return "Use short numbered steps, explain one decision at a time, and ask only the minimum question needed for the next action."
	case model.AssistantProfileOperator:
		return "Prioritize reliability, operational checks, concurrency, observability, rollback, and concise production-ready actions."
	case model.AssistantProfilePrivacy:
		return "Prefer data minimization, local options, explicit privacy boundaries, and never request credentials or unnecessary personal data."
	case model.AssistantProfileAccessible:
		return "Prefer short mobile-friendly actions, clear labels, keyboard and screen-reader compatible instructions, and avoid dense layouts."
	case model.AssistantProfileNormal:
		return "Answer the user's current request directly and concisely without repeating generic onboarding questions."
	case model.AssistantProfileSupport:
		return "First resolve the immediate support issue, summarize the evidence, and prepare a human handoff only when needed."
	case model.AssistantProfileL0Applicant:
		return "Answer the current question directly while keeping developer and write actions unavailable until L1. Reuse concrete details already provided; after three server-recorded completed turns, use get_registration_risk and grant_l1_access when the server permits. Recommendation letters and administrator approval are retired."
	default:
		return ""
	}
}

func saveProfileSkill(skills service.UserSkills, input map[string]any) map[string]any {
	profile := inputString(input, "profile_key")
	strategy := profileSkillStrategy(profile)
	if strategy == "" {
		return map[string]any{"ok": false, "error": "profile skill is invalid"}
	}
	tags, ok := stringList(input, "tags", 4)
	if !ok {
		return map[string]any{"ok": false, "error": "profile skill tags are invalid"}
	}
	saved, err := skills.LearnProfile(service.ProfileDraft{Key: profile, Tags: tags, Strategy: strategy, Enabled: true})
	if err != nil {
		if errors.Is(err, model.ErrAssistantProfileManaged) {
			return map[string]any{
				"ok": false, "status": "administrator_managed",
				"error": "profile skill is managed by an administrator and was not changed",
			}
		}
		return map[string]any{"ok": false, "error": "profile skill could not be saved"}
	}
	view := model.AssistantUserProfileViewOf(saved)
	return map[string]any{
		"ok": true, "status": "profile_skill_saved", "owner_scope": "signed_in_user_only",
		"profile_key": view.ProfileKey, "tags": view.Tags,
	}
}
