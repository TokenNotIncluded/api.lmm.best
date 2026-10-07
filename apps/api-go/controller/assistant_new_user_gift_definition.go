package controller

import (
	"fmt"
	"github.com/LIghtJUNction/api.lmm.best/common"
)

func assistantNewUserGiftToolDefinition() assistantOpenAIToolDefinition {
	cap, err := assistantCurrentGiftMaxCredits()
	if err != nil {
		cap = 0
	}
	return assistantOpenAIToolDefinition{Type: "function", Function: assistantOpenAIToolFunction{
		Name:        "prepare_new_user_gift",
		Description: fmt.Sprintf("For an eligible signed-in user with an unused lifetime welcome-gift decision, use the complete conversation to judge a concrete legitimate workflow and planned work. One sufficiently detailed user message is enough; reuse supplied details and ask at most one essential clarification. Choose amount_credits as integer wallet credits between 0 and %d, the current administrator-configured maximum. A maximum of 0 disables issuance; do not consume the opportunity while disabled. Zero is a valid declined decision only while gifts are enabled. L1 does not erase an unused opportunity. A legitimate application or business automation is not abuse; do not reward money demands, self-reported expertise alone, promotion/referral farming, multiple accounts, automated reward farming, or unsafe behavior. The server enforces current caps, eligibility and one-time issuance. Never promise an amount before tool success or claim the gift for the user. Explain the actual value using public_credit_amount or amount_usd.", cap),
		Parameters: objectSchema(map[string]any{
			"amount_credits": map[string]any{"type": "integer", "minimum": 0, "maximum": cap, "description": "Exact integer wallet credits, bounded by the current administrator-configured maximum."},
			"amount_unit":    map[string]any{"type": "string", "enum": []string{common.LedgerQuotaUnit}},
			"reason":         map[string]any{"type": "string", "minLength": 2, "maxLength": 240},
		}, []string{"amount_credits", "reason"}),
	}}
}
