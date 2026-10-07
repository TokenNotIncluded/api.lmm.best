package controller

import (
	"errors"
	"fmt"
	"math"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/logger"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/gin-gonic/gin"
)

// amount_cents is a retained legacy input, never a true USD amount. The stored
// Credit grant is authoritative, including for gifts offered before migration.
type assistantNewUserGiftResponse struct {
	*model.AssistantNewUserGift
	AmountUnit    string   `json:"amount_unit"`
	CreditAmount  int      `json:"credit_amount"`
	AmountUSD     *float64 `json:"amount_usd"`
	Currency      string   `json:"currency"`
	CreditsPerUSD *float64 `json:"credits_per_usd"`
	common.CreditDenomination
	CreditAmountUnit   string `json:"credit_amount_unit"`
	PublicCreditAmount string `json:"public_credit_amount"`
}

func assistantGiftMoneyFields(gift *model.AssistantNewUserGift) (map[string]any, error) {
	amountCents, credits := 0, 0
	if gift != nil {
		amountCents, credits = gift.AmountCents, gift.Quota
		var err error
		credits, err = model.AssistantGiftCreditQuota(gift)
		if err != nil {
			return nil, err
		}
	}
	fields := map[string]any{
		"amount_cents": amountCents, "amount_unit": "LEGACY_CENTS",
		"credit_amount": credits, "credit_amount_unit": common.LedgerQuotaUnit, "amount_usd": nil,
		"currency": "USD", "credits_per_usd": nil,
	}
	usd, anchor, err := assistantFiatProjection(int64(credits))
	if errors.Is(err, errAssistantCurrencyProjectionUnavailable) {
		return fields, nil
	}
	if err != nil {
		return nil, err
	}
	fields["amount_usd"], fields["credits_per_usd"] = usd, anchor
	units, err := model.CreditDenominationSnapshot()
	if err != nil {
		return nil, err
	}
	for key, value := range creditUnitMetadataFieldsFor(units) {
		fields[key] = value
	}
	public, err := units.ProjectLedgerQuota(int64(credits))
	if err != nil {
		return nil, err
	}
	fields["public_credit_amount"] = public.String()
	return fields, nil
}

func assistantGiftResponse(gift *model.AssistantNewUserGift) (*assistantNewUserGiftResponse, error) {
	fields, err := assistantGiftMoneyFields(gift)
	if err != nil || gift == nil {
		return nil, err
	}
	response := &assistantNewUserGiftResponse{
		AssistantNewUserGift: gift, AmountUnit: "LEGACY_CENTS", CreditAmount: fields["credit_amount"].(int),
		Currency: "USD",
	}
	response.CreditAmountUnit = common.LedgerQuotaUnit
	// Reuse the same captured basis that produced public_credit_amount.
	if version, ok := fields["credit_unit_schema_version"].(int); ok {
		response.CreditDenomination = common.CreditDenomination{CreditUnitSchemaVersion: version,
			QuotaUnit: fields["quota_unit"].(string), PublicCreditUnit: fields["public_credit_unit"].(string), LegacyCreditUnit: fields["legacy_credit_unit"].(string),
			LedgerQuotaPerUSD: fields["ledger_quota_per_usd"].(float64), LedgerQuotaPerUSDExact: fields["ledger_quota_per_usd_exact"].(string),
			PublicCreditsPerUSD: fields["public_credits_per_usd"].(float64), PublicCreditsPerUSDExact: fields["public_credits_per_usd_exact"].(string)}
	}
	response.PublicCreditAmount, _ = fields["public_credit_amount"].(string)
	if value, ok := fields["amount_usd"].(float64); ok {
		response.AmountUSD = &value
	}
	if value, ok := fields["credits_per_usd"].(float64); ok {
		response.CreditsPerUSD = &value
	}
	return response, nil
}

func GetAssistantNewUserGift(c *gin.Context) {
	gift, err := model.GetAssistantNewUserGift(c.GetInt("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	response, err := assistantGiftResponse(gift)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, response)
}

func ClaimAssistantNewUserGift(c *gin.Context) {
	// Reject an unavailable denomination before registration or claim writes.
	if _, err := common.CreditsPerUSD(); err != nil {
		common.ApiError(c, err)
		return
	}
	if err := model.ObserveAssistantRegistration(c.GetInt("id"), c.ClientIP(), ""); err != nil {
		common.ApiError(c, model.ErrAssistantRegistrationCheck)
		return
	}
	gift, alreadyClaimed, err := model.ClaimAssistantNewUserGift(c.GetInt("id"))
	if err != nil {
		status := http.StatusConflict
		code := "ASSISTANT_NEW_USER_GIFT_UNAVAILABLE"
		if !errors.Is(err, model.ErrAssistantGiftUnavailable) {
			common.ApiError(c, err)
			return
		}
		c.AbortWithStatusJSON(status, gin.H{"success": false, "code": code, "message": err.Error()})
		return
	}
	if !alreadyClaimed {
		quota, err := model.AssistantGiftCreditQuota(gift)
		if err != nil {
			common.ApiError(c, err)
			return
		}
		model.RecordLog(c.GetInt("id"), model.LogTypeTopup, fmt.Sprintf("领取 AI 新用户礼包，获得额度 %s", logger.LogQuota(quota)))
	}
	response, err := assistantGiftResponse(gift)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"gift": response, "already_claimed": alreadyClaimed})
}

func GetAssistantJourney(c *gin.Context) {
	journey, err := model.GetAssistantJourney(c.GetInt("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, journey)
}

func assistantConversationEvidence(c *gin.Context) (turns int, runes int) {
	if c == nil {
		return 0, 0
	}
	// Compression markers and omitted excerpts are model context only. Count
	// substantive evidence from the original owned text so summaries cannot
	// manufacture turns or inflate a short user's request into reward merit.
	raw, exists := c.Get(assistantPolicyConversationKey)
	if !exists {
		raw, exists = c.Get("assistant_conversation")
	}
	if !exists {
		return 0, 0
	}
	messages, ok := raw.([]assistantOpenAIMessage)
	if !ok {
		return 0, 0
	}
	for _, message := range messages {
		if message.Role != "user" {
			continue
		}
		content := strings.TrimSpace(message.Content)
		length := utf8.RuneCountInString(content)
		if length < 4 {
			continue
		}
		turns++
		runes += length
	}
	return turns, runes
}

func executeAssistantNewUserGiftTool(c *gin.Context, userID int, input map[string]any) map[string]any {
	if assistantRewardReadOnlyRequest(c) {
		return assistantGiftReadOnlyRequestResult()
	}
	if unit := inputString(input, "amount_unit"); unit != "" && unit != "LEGACY_CENTS" {
		return map[string]any{"ok": false, "status": "invalid_decision", "error": "amount_cents uses LEGACY_CENTS, not fiat cents"}
	}
	amount, ok := inputNumber(input, "amount_cents")
	if !ok || math.IsNaN(amount) || math.IsInf(amount, 0) || math.Trunc(amount) != amount {
		return map[string]any{"ok": false, "status": "invalid_decision", "error": "amount_cents must be an integer from 0 to 1000"}
	}
	turns, runes := assistantConversationEvidence(c)
	gift, created, err := model.DecideAssistantNewUserGift(
		userID,
		assistantHistoryConversationID(c),
		int(amount),
		inputString(input, "reason"),
		turns,
		runes,
		c.ClientIP(),
	)
	if err != nil {
		reasonCode := model.AssistantGiftErrorCode(err)
		switch {
		case errors.Is(err, model.ErrAssistantGiftIneligible), errors.Is(err, model.ErrAssistantGiftAbuse):
			if reasonCode == "" {
				reasonCode = "ineligible"
			}
			return map[string]any{"ok": false, "status": "ineligible", "reason_code": reasonCode, "error": "this account is not eligible for a new-user gift"}
		case errors.Is(err, model.ErrAssistantGiftInvalid):
			if reasonCode == "" {
				reasonCode = "invalid_decision"
			}
			if reasonCode == "insufficient_conversation" {
				return map[string]any{"ok": false, "status": "more_conversation_needed", "reason_code": reasonCode, "error": "ask for the missing concrete purpose or planned work before evaluating the one-time gift; one sufficiently detailed user message is enough"}
			}
			return map[string]any{"ok": false, "status": "invalid_decision", "reason_code": reasonCode, "error": "the one-time gift decision was invalid"}
		default:
			return map[string]any{"ok": false, "status": "unavailable", "error": "the gift decision could not be saved"}
		}
	}
	money, err := assistantGiftMoneyFields(gift)
	if err != nil {
		return map[string]any{"ok": false, "status": "unavailable", "error": "gift currency units are unavailable"}
	}
	if gift.Status == model.AssistantGiftOffered && c != nil {
		action := make(map[string]any, len(money)+3)
		for key, value := range money {
			action[key] = value
		}
		action["type"], action["reason"], action["status"] = "new_user_gift", gift.Reason, gift.Status
		c.Set(assistantClientActionKey, action)
	}
	money["ok"], money["created"], money["status"], money["reason"] = true, created, gift.Status, gift.Reason
	money["next_step"] = "The user claims an offered gift from the gift shown in the chat. Never claim it for them. amount_cents is LEGACY_CENTS; explain the gift using public_credit_amount or amount_usd."
	return money
}
