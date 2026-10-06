package controller

import (
	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/gin-gonic/gin"
)

// Tests inject this sender; isolated tests never send real verification mail.
var merchantStoreOrderSearchSender = service.SendMerchantStoreOrderSearchEmail

func SendMerchantStoreOrderSearchVerification(c *gin.Context) {
	var input struct {
		Email string `json:"email"`
	}
	if c.ShouldBindJSON(&input) != nil {
		merchantStoreRespond(c, nil, model.ErrMerchantStoreInput)
		return
	}
	challengeID, code, email, err := model.BeginMerchantStoreOrderSearch(input.Email)
	var expiresAt int64
	if err == nil {
		expiresAt, err = model.GetMerchantStoreOrderSearchChallengeExpires(challengeID)
	}
	if err == nil {
		err = merchantStoreOrderSearchSender(c.Request.Context(), email, code)
	}
	// Sending does not look up orders. Mailboxes with and without orders have
	// the same response, verification flow and rate limits.
	merchantStoreRespond(c, gin.H{"challenge_id": challengeID, "expires_in": max(int64(0), expiresAt-common.GetTimestamp()), "resend_after": 60}, err)
}

func ConfirmMerchantStoreOrderSearchVerification(c *gin.Context) {
	var input struct {
		ChallengeID string `json:"challenge_id"`
		Code        string `json:"code"`
	}
	if c.ShouldBindJSON(&input) != nil {
		merchantStoreRespond(c, nil, model.ErrMerchantStoreInput)
		return
	}
	token, err := model.ConfirmMerchantStoreOrderSearch(input.ChallengeID, input.Code)
	merchantStoreRespond(c, gin.H{"search_token": token, "expires_in": 900}, err)
}

func ListMerchantStoreOrdersByEmail(c *gin.Context) {
	c.Header("Referrer-Policy", "no-referrer")
	var input struct {
		SearchToken string `json:"search_token"`
		Offset      int    `json:"offset"`
		Limit       int    `json:"limit"`
	}
	if c.ShouldBindJSON(&input) != nil {
		merchantStoreRespond(c, nil, model.ErrMerchantStoreInput)
		return
	}
	if input.Limit == 0 {
		input.Limit = 30
	}
	if input.Offset < 0 || input.Offset > 100000 || input.Limit < 1 || input.Limit > 100 {
		merchantStoreRespond(c, nil, model.ErrMerchantStoreInput)
		return
	}
	items, err := model.ListMerchantStoreOrdersByVerifiedEmail(input.SearchToken, input.Offset, input.Limit)
	if err == nil {
		for i := range items {
			if items[i].PickupToken != "" {
				items[i].PickupURL, err = merchantStorePickupURL(items[i].PickupToken)
				if err != nil {
					break
				}
			}
		}
	}
	merchantStoreList(c, items, input.Offset, input.Limit, err)
}

func GetMerchantStoreOrderByNumber(c *gin.Context) {
	c.Header("Referrer-Policy", "no-referrer")
	actor := merchantStoreViewer(c)
	item, err := model.GetMerchantStoreOrderSearchSummaryWithGuest(c.Param("trade_no"), actor, merchantStoreGuestHeader(c))
	if err == nil && (item.Status == "paid" || item.Status == "refund_pending") && actor > 0 {
		// The public order number grants status access only. Existing buyer
		// authorization or verified mailbox proof is required for a pickup link.
		if token, tokenErr := model.GetMerchantStoreOrderPickupToken(actor, item.RawOrderID); tokenErr == nil {
			item.PickupURL, err = merchantStorePickupURL(token)
		}
	}
	merchantStoreRespond(c, item, err)
}
