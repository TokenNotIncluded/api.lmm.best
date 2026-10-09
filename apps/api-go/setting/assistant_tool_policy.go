package setting

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

const (
	AssistantToolPolicyOptionKey = "AssistantToolPolicy"
	DefaultAssistantToolPolicy   = `{"version":1,"groups":{},"tools":{}}`
	AssistantToolPolicyMaxBytes  = 16 << 10
)

// AssistantToolPolicy can only remove capabilities. Existing account, role,
// confirmation, registration and billing checks remain authoritative.
type AssistantToolPolicy struct {
	Version int                          `json:"version"`
	Groups  map[string]bool              `json:"groups"`
	Tools   map[string]bool              `json:"tools"`
	Rules   map[string]AssistantToolRule `json:"rules,omitempty"`
}

type AssistantToolInfo struct {
	Name        string `json:"name"`
	Label       string `json:"label"`
	Description string `json:"description"`
	Effect      string `json:"effect"`
	Access      string `json:"access"`
}

type AssistantToolGroup struct {
	ID    string              `json:"id"`
	Label string              `json:"label"`
	Tools []AssistantToolInfo `json:"tools"`
}

var assistantToolCatalogue = []AssistantToolGroup{
	{"service_help", "Service help", []AssistantToolInfo{
		{"get_service_facts", "Service connection and activities", "Read current connection endpoints, console activities and key setup guidance.", "read_only", "user"},
		{"navigate_to_page", "Open a console page", "Suggest a permitted console page without submitting its forms.", "navigation", "user"},
		{"get_setup_guide", "Client setup guidance", "Read setup steps for the chosen client, device and live model ID.", "read_only", "user"},
		{"search_web", "Search the web", "Search through the configured provider; results cannot authorize account changes.", "read_only", "user"},
		{"calculate_math", "Arithmetic calculator", "Evaluate bounded arithmetic expressions without shell or database access.", "read_only", "user"},
	}},
	{"account_access", "Account and access", []AssistantToolInfo{
		{"get_account_access", "Account access and progress", "Read your current access level, wallet balance and recorded setup progress.", "read_only", "user"},
		{"get_user_overview", "Account overview", "Read your account overview, or an authorized administrator view.", "read_only", "user"},
		{"get_user_usage_summary", "Account usage details", "Read bounded account usage statistics for the permitted account.", "read_only", "user"},
		{"get_usage_summary", "Usage summary", "Read your current model and group usage summary.", "read_only", "l1"},
		{"prepare_user_action", "Prepare an account action", "Prepare a secure form for supported profile or account actions; the user must confirm.", "confirmation", "user"},
		{"grant_l1_access", "Grant verified L1 access", "Activate only the current L0 account after server-checked conversation and registration requirements.", "server_guarded", "l0"},
	}},
	{"models_pricing", "Models and costs", []AssistantToolInfo{
		{"get_available_models", "Available models", "Read live public model IDs for L0, or usable model IDs for an authorized account.", "read_only", "user"},
		{"get_model_pricing", "Model pricing", "Read live pricing for one exact model and permitted routing group.", "read_only", "user"},
		{"calculate_cost", "Cost estimate", "Calculate an estimate from supplied token counts and verified prices.", "read_only", "user"},
		{"get_plan_offers", "Plans and top-up offers", "Read current offers while retaining the L0 payment-intent and checkout restrictions.", "read_only", "user"},
	}},
	{"api_keys", "API keys", []AssistantToolInfo{
		{"request_create_key", "Prepare API key creation", "Prepare a browser confirmation for a key in a permitted group; never reveal it in chat.", "confirmation", "l1"},
		{"list_my_api_keys", "List your API key metadata", "Read your key IDs, names and status without exposing credentials.", "read_only", "user"},
		{"prepare_api_key_action", "Prepare API key revocation", "Prepare deletion or disabling of one exact owned key; confirmation and secure verification remain required.", "confirmation", "user"},
	}},
	{"rewards", "Rewards", []AssistantToolInfo{
		{"send_invitation", "Send an invitation", "Prepare an invitation email using your own referral link; send only after confirmation.", "confirmation", "l1"},
		{"get_invitation_rewards", "Invitation rewards", "Read your invitation reward status and the current reward rules.", "read_only", "l1"},
		{"get_new_user_gift_status", "Welcome gift status", "Read a stored gift decision without evaluating or claiming a gift.", "read_only", "user"},
		{"prepare_new_user_gift", "Evaluate a welcome gift", "Record a one-time eligibility decision and prepare its claim card; credit is granted only after claiming.", "server_guarded", "user"},
		{"get_weekly_discount_status", "Weekly discount status", "Read the current stored weekly decision without changing it.", "read_only", "user"},
		{"prepare_weekly_discount", "Evaluate a weekly discount", "Record a server-checked weekly decision and prepare its claim card.", "server_guarded", "user"},
	}},
	{"bounties", "Open-source bounties", []AssistantToolInfo{
		{"get_bounty_guide", "Bounty guidance", "Read the workflow for publishing, funding, reviewing and settling bounties.", "read_only", "user"},
		{"get_bounty_data", "Bounty data", "Read public bounty data or authorized private and administrator views; never fund or settle a bounty.", "read_only", "mixed"},
	}},
	{"market_catalog", "Products and tool catalogues", []AssistantToolInfo{
		{"get_store_products", "Product catalogue", "Read visible store products without buying or issuing credentials.", "read_only", "user"},
		{"get_store_product", "Product details", "Read one visible product and its purchase requirements.", "read_only", "user"},
		{"get_tool_market_services", "MCP service catalogue", "Read tool-market service listings without loading, authorizing or invoking their tools.", "read_only", "user"},
		{"get_tool_market_service", "MCP service details", "Read one service and its published tool metadata without invoking it.", "read_only", "user"},
	}},
	{"drawing", "Image generation", []AssistantToolInfo{
		{"prepare_image_generation", "Prepare image generation", "Prepare a drawing confirmation using current permitted models; generation may charge the wallet after confirmation.", "confirmation", "l1"},
	}},
	{"human_support", "Human support", []AssistantToolInfo{
		{"get_human_support_status", "Support eligibility and requests", "Read your support eligibility and active in-site request.", "read_only", "user"},
		{"book_technical_support", "Request a support appointment", "Create an in-site appointment only after an explicit booking request and server-checked recharge eligibility.", "server_guarded", "user"},
		{"request_human_support", "Prepare human support or account review", "Prepare an in-site handoff or account-disable review request; the user must confirm.", "confirmation", "user"},
	}},
	{"personalization", "Memory and personalization", []AssistantToolInfo{
		{"get_overview_greeting", "Read overview greeting", "Read your localized overview templates and supported variables.", "read_only", "user"},
		{"set_overview_greeting", "Edit overview greeting", "Prepare a change to your own localized greeting without changing other settings.", "confirmation", "user"},
		{"set_conversation_title", "Conversation title", "Store a short title for the current new conversation without account changes.", "server_guarded", "user"},
		{"recall_memory", "Recall your memories", "Read only your own relevant long-term memories.", "read_only", "user"},
		{"remember_memory", "Remember a preference", "Store a bounded durable preference or project detail for your account; secrets are prohibited.", "server_guarded", "user"},
		{"remember_profile_skill", "Save a response preference", "Store a coarse response-style preference without changing permissions or billing.", "server_guarded", "user"},
		{"forget_profile_skill", "Forget an AI response preference", "Remove only your AI-created response preference after your explicit request.", "server_guarded", "user"},
	}},
	{"registration_safety", "Registration protection", []AssistantToolInfo{
		{"get_registration_risk", "Registration eligibility check", "Read server-verified evidence for the current L0 account.", "read_only", "l0"},
		{"notify_registration_risk", "Record a registration alert", "Record a bounded in-site risk alert only from server-verified registration evidence.", "server_guarded", "l0"},
		{"end_registration_conversation", "Hold an unsafe registration", "Hold the current L0 registration flow only when deterministic server checks require it.", "server_guarded", "l0"},
		{"ban_l0_user", "Suspend an abusive L0 account", "Suspend only the current L0 account when matching server evidence and the global suspension cap permit it.", "server_guarded", "l0"},
	}},
	{"admin_read", "Administrator reads", []AssistantToolInfo{
		{"get_admin_user_skills", "Read user personalization", "Read permitted account profiles and memories with live administrator authority.", "read_only", "admin"},
		{"get_admin_server_config", "Read server settings", "Read allowed server configuration without secrets; root authority is required.", "read_only", "root"},
		{"get_admin_channels", "Read routing channels", "Read bounded channel configuration without provider credentials.", "read_only", "admin"},
		{"get_admin_model_inventory", "Read model inventory", "Read enabled model IDs, routing groups and missing local metadata.", "read_only", "admin"},
		{"list_admin_operations", "Discover console operations", "Read exact permitted console operation metadata; generic execution remains read-only.", "read_only", "admin"},
		{"execute_admin_operation", "Run a console read operation", "Run one discovered read operation with current route permissions; writes and disabled tool aliases are rejected.", "read_only", "admin"},
		{"audit_admin_model_pricing", "Audit model pricing", "Inspect supported model pricing without modifying prices.", "read_only", "admin"},
	}},
	{"admin_changes", "Administrator changes", []AssistantToolInfo{
		{"prepare_admin_user_skill_change", "Prepare user personalization changes", "Prepare an exact administrator preview for supported profile or memory changes.", "confirmation", "admin"},
		{"prepare_admin_config_change", "Prepare server setting changes", "Prepare allowlisted server settings with root authority and explicit browser confirmation.", "confirmation", "root"},
		{"prepare_admin_channel_change", "Prepare channel changes", "Prepare validated channel changes while preserving role and secret restrictions.", "confirmation", "admin"},
		{"prepare_admin_model_sync", "Prepare model metadata import", "Verify selected upstream model IDs and prepare a root-only metadata import.", "confirmation", "root"},
		{"prepare_admin_pricing_change", "Prepare model pricing changes", "Prepare exact pricing changes with root authority and explicit browser confirmation.", "confirmation", "root"},
	}},
	{"site_issues", "Site improvement issues", []AssistantToolInfo{
		{"get_site_issues", "Read issue status", "Read your visible reports and updates; administrators can read all reports.", "read_only", "user"},
		{"create_site_issue", "Create an improvement issue", "Prepare a bug, security, experience or feature report for confirmation.", "confirmation", "user"},
		{"update_site_issue", "Update issue status", "Prepare an administrator update with status, visibility and a note.", "confirmation", "admin"},
	}},
	{"market_connections", "Tool market connections", []AssistantToolInfo{
		{"get_connected_market_tools", "Read connected market tools", "Read allowed services, exact schemas and your own current grants.", "read_only", "l1"},
		{"connect_market_tool", "Authorize a market tool", "Prepare version-bound access and spending limits for browser confirmation.", "confirmation", "l1"},
		{"call_market_tool", "Use a connected market tool", "Call an allowed remote tool using your explicit bounded grant and the existing billing checks.", "server_guarded", "l1"},
	}},
	{"visualizations", "Visualizations", []AssistantToolInfo{
		{"show_chart", "Statistical chart", "Display a line, bar or donut chart from bounded data.", "read_only", "user"},
		{"show_statistics", "Statistic cards", "Display key values with labels and optional icons.", "read_only", "user"},
		{"show_choices", "Choice buttons", "Offer choices that fill the composer without submitting actions.", "read_only", "user"},
		{"show_flowchart", "Flow diagram", "Display a process using labelled steps and connections.", "read_only", "user"},
	}},
}

var assistantToolGroupsByName = func() map[string]string {
	groups := make(map[string]string)
	for _, group := range assistantToolCatalogue {
		for _, tool := range group.Tools {
			groups[tool.Name] = group.ID
		}
	}
	return groups
}()

// Return detached slices: API callers cannot mutate the registered catalogue.
func AssistantToolCatalogue() []AssistantToolGroup {
	groups := append([]AssistantToolGroup(nil), assistantToolCatalogue...)
	for index := range groups {
		groups[index].Tools = append([]AssistantToolInfo(nil), groups[index].Tools...)
	}
	return groups
}

func (policy AssistantToolPolicy) Enabled(name string) bool {
	group, known := assistantToolGroupsByName[name]
	if !known {
		return false
	}
	if enabled, present := policy.Groups[group]; present && !enabled {
		return false
	}
	enabled, present := policy.Tools[name]
	return !present || enabled
}

// Reject ambiguous duplicate fields, null booleans and unknown identifiers.
// In particular, a typo must never silently turn a disabled capability on.
func NormalizeAssistantToolPolicy(raw string) (string, AssistantToolPolicy, error) {
	if len(raw) > AssistantToolPolicyMaxBytes {
		return "", AssistantToolPolicy{}, errors.New("assistant tool policy exceeds 16384 bytes")
	}
	if strings.TrimSpace(raw) == "" {
		raw = DefaultAssistantToolPolicy
	}
	policy := AssistantToolPolicy{Groups: map[string]bool{}, Tools: map[string]bool{}}
	invalid := func() (string, AssistantToolPolicy, error) {
		return "", AssistantToolPolicy{}, errors.New("assistant tool policy must contain version 1 and known groups/tools with boolean values")
	}
	decoder := json.NewDecoder(bytes.NewBufferString(raw))
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return invalid()
	}
	seen := map[string]bool{}
	for decoder.More() {
		token, err := decoder.Token()
		key, ok := token.(string)
		if err != nil || !ok || seen[key] {
			return invalid()
		}
		seen[key] = true
		switch key {
		case "version":
			if err := decoder.Decode(&policy.Version); err != nil || policy.Version != 1 {
				return invalid()
			}
		case "rules":
			rules, err := decodeAssistantToolRules(decoder)
			if err != nil {
				return invalid()
			}
			policy.Rules = rules
		case "groups", "tools":
			if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
				return invalid()
			}
			values := policy.Tools
			if key == "groups" {
				values = policy.Groups
			}
			for decoder.More() {
				token, err := decoder.Token()
				name, ok := token.(string)
				if _, duplicate := values[name]; err != nil || !ok || duplicate || !assistantToolPolicyIDKnown(key, name) {
					return invalid()
				}
				var enabled *bool
				if err := decoder.Decode(&enabled); err != nil || enabled == nil {
					return invalid()
				}
				values[name] = *enabled
			}
			if token, err := decoder.Token(); err != nil || token != json.Delim('}') {
				return invalid()
			}
		default:
			return invalid()
		}
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim('}') || !seen["version"] {
		return invalid()
	}
	if _, err := decoder.Token(); err != io.EOF {
		return invalid()
	}
	canonical, err := json.Marshal(policy)
	return string(canonical), policy, err
}

func assistantToolPolicyIDKnown(kind, id string) bool {
	if kind == "tools" {
		_, known := assistantToolGroupsByName[id]
		return known
	}
	for _, group := range assistantToolCatalogue {
		if group.ID == id {
			return true
		}
	}
	return false
}

func UpdateAssistantToolPolicy(raw string) error {
	canonical, policy, err := NormalizeAssistantToolPolicy(raw)
	if err != nil {
		return err
	}
	assistantSettingsMutex.Lock()
	defer assistantSettingsMutex.Unlock()
	assistantSettings.ToolPolicy = canonical
	assistantToolPolicy = policy
	return nil
}

var assistantToolPolicy = AssistantToolPolicy{Version: 1}

func AssistantToolEnabled(name string) bool {
	assistantSettingsMutex.RLock()
	defer assistantSettingsMutex.RUnlock()
	return assistantToolPolicy.Enabled(name)
}

func AssistantToolKnown(name string) bool {
	_, known := assistantToolGroupsByName[name]
	return known
}

func AssistantToolDisabledError(name string) error {
	return fmt.Errorf("assistant tool %s is disabled by the administrator", name)
}
