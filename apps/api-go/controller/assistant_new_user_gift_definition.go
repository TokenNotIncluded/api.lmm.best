package controller

import (
	"strconv"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/setting"
)

const assistantNewUserGiftDescriptionTemplate = "Grant the signed-in user's welcome gift when the conversation shows they want it. Use the purpose already supplied; ordinary coding, learning or chatting is enough. Decide the amount yourself. Do not require a fixed application phrase, a minimum message length, another approval, or a claim click. Choose an integer amount_credits with 0 <= amount_credits < {{max_reward_credits}}, in {{reward_unit}}. The upper bound is exclusive; 0 disables issuance when it is the configured upper bound. Choosing an amount of 0 makes no new decision and grants no credit. A positive approved gift is credited directly. The server checks account eligibility, current limits and duplicate issuance; retries cannot credit twice. Read-only status questions use get_new_user_gift_status. Do not reward multiple-account or automated reward farming. Do not reset old decisions. Report success only from a claimed receipt, using public_credit_amount or amount_usd, not legacy cents."

const assistantNewUserGiftAmountDescriptionTemplate = "Integer wallet ledger credits. Choose 0 <= amount_credits < {{max_reward_credits}}. This is an exclusive upper bound in {{reward_unit}}, not USD cents or public display credits. Zero makes no new decision."

func assistantNewUserGiftToolDefinition() assistantOpenAIToolDefinition {
	cap, err := assistantCurrentGiftMaxCredits()
	if err != nil {
		cap = 0
	}
	return assistantNewUserGiftToolDefinitionWithCap(cap)
}

func assistantNewUserGiftToolDefinitionWithCap(cap int) assistantOpenAIToolDefinition {
	variables := map[string]string{
		"max_reward_credits": strconv.Itoa(cap),
		"reward_unit":        common.LedgerQuotaUnit,
	}
	return assistantOpenAIToolDefinition{Type: "function", Function: assistantOpenAIToolFunction{
		Name:        "prepare_new_user_gift",
		Description: setting.RenderAssistantToolDescription(assistantNewUserGiftDescriptionTemplate, variables),
		Parameters: objectSchema(map[string]any{
			"amount_credits": map[string]any{
				"type": "integer", "minimum": 0, "exclusiveMaximum": cap,
				"description": setting.RenderAssistantToolDescription(assistantNewUserGiftAmountDescriptionTemplate, variables),
			},
			"amount_unit": map[string]any{
				"type": "string", "enum": []string{common.LedgerQuotaUnit},
				"description": "Wallet ledger credit unit; do not convert from a displayed currency amount without verified unit data.",
			},
			"reason": map[string]any{
				"type": "string", "maxLength": 240,
				"description": "Optional short explanation based on the conversation. Do not ask the user to write an application or provide a longer explanation.",
			},
		}, []string{"amount_credits"}),
	}}
}
