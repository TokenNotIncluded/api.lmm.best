// Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later
package controller

import (
	"strconv"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/setting"
)

func assistantToolDescriptionVariables(cap int, rule setting.AssistantToolRule) map[string]string {
	return map[string]string{
		"max_reward_credits": strconv.Itoa(cap),
		"reward_unit":        common.LedgerQuotaUnit,
		"min_level":          strconv.Itoa(rule.MinLevel),
		"max_level":          strconv.Itoa(rule.MaxLevel),
	}
}

// Run after role filtering and the live reward limits have been attached.
// Keep the shared tool-set cache free of administrator-authored descriptions.
func assistantConfiguredToolDefinitions(catalogue []assistantOpenAIToolDefinition, cap int) []assistantOpenAIToolDefinition {
	_, policy, err := setting.NormalizeAssistantToolPolicy(setting.GetAssistantSettings().ToolPolicy)
	if err != nil {
		return nil
	}
	tools := append([]assistantOpenAIToolDefinition(nil), catalogue...)
	for i := range tools {
		rule := policy.Rule(tools[i].Function.Name)
		tools[i].Function.Description, tools[i].Function.Parameters = setting.ApplyAssistantToolText(
			rule, tools[i].Function.Description, tools[i].Function.Parameters,
			assistantToolDescriptionVariables(cap, rule),
		)
	}
	return tools
}

type assistantToolTextSchema struct {
	Description string                               `json:"description"`
	Parameters  []setting.AssistantToolParameterText `json:"parameters"`
	Variables   map[string]string                    `json:"variables"`
}

type assistantToolTextCatalogueItem struct {
	setting.AssistantToolInfo
	TextSchema assistantToolTextSchema `json:"text_schema"`
}

type assistantToolTextCatalogueGroup struct {
	ID    string                           `json:"id"`
	Label string                           `json:"label"`
	Tools []assistantToolTextCatalogueItem `json:"tools"`
}

func assistantToolCatalogueWithText(policy setting.AssistantToolPolicy) []assistantToolTextCatalogueGroup {
	definitions := make(map[string]assistantOpenAIToolDefinition)
	for _, definition := range assistantTools() {
		definitions[definition.Function.Name] = definition
	}
	cap, err := assistantCurrentGiftMaxCredits()
	if err != nil {
		cap = 0
	}
	groups := make([]assistantToolTextCatalogueGroup, 0)
	for _, group := range setting.AssistantToolCatalogue() {
		item := assistantToolTextCatalogueGroup{ID: group.ID, Label: group.Label, Tools: make([]assistantToolTextCatalogueItem, 0, len(group.Tools))}
		for _, tool := range group.Tools {
			definition := definitions[tool.Name]
			description := definition.Function.Description
			fields := setting.AssistantToolParameterTexts(definition.Function.Parameters)
			if tool.Name == "prepare_new_user_gift" {
				description = assistantNewUserGiftDescriptionTemplate
				for i := range fields {
					if fields[i].Path == "/properties/amount_credits" {
						fields[i].Description = assistantNewUserGiftAmountDescriptionTemplate
					}
				}
			}
			item.Tools = append(item.Tools, assistantToolTextCatalogueItem{
				AssistantToolInfo: tool,
				TextSchema:        assistantToolTextSchema{Description: description, Parameters: fields, Variables: assistantToolDescriptionVariables(cap, policy.Rule(tool.Name))},
			})
		}
		groups = append(groups, item)
	}
	return groups
}
