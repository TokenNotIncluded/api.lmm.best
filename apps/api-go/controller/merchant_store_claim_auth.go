package controller

import (
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/gin-gonic/gin"
)

func InspectMerchantStoreSessionClaim(c *gin.Context) {
	c.Header("Referrer-Policy", "no-referrer")
	if err := service.ValidateMerchantStoreClaimPath(c.Request, c.Param("token")); err != nil {
		merchantStoreRespond(c, nil, err)
		return
	}
	metadata, err := model.InspectMerchantStoreClaim(c.Param("token"))
	if err != nil {
		merchantStoreRespond(c, nil, err)
		return
	}
	buyerID := c.GetInt("id")
	if !service.MerchantStoreClaimHasAuthorization(c.Request) {
		// An invalid cookie still permits the safe anonymous metadata response.
		buyerID, _ = service.ValidateMerchantStoreClaimCookie(c.Request, c.Param("token"))
	}
	metadata.PickupLoginSatisfied = service.MerchantStoreClaimBuyerMatches(c.Param("token"), buyerID)
	merchantStoreRespond(c, metadata, nil)
}

func ClaimMerchantStoreSessionOrder(c *gin.Context) {
	c.Header("Referrer-Policy", "no-referrer")
	if err := service.ValidateMerchantStoreClaimPath(c.Request, c.Param("token")); err != nil {
		merchantStoreRespond(c, nil, err)
		return
	}
	var input struct {
		PickupCode string `json:"pickup_code"`
	}
	if c.ShouldBindJSON(&input) != nil {
		merchantStoreRespond(c, nil, model.ErrMerchantStoreInput)
		return
	}
	if service.MerchantStoreClaimHasAuthorization(c.Request) {
		// TryUserAuth has already handled the supplied credential. Even malformed
		// or wrong-account credentials cannot be compensated with a cookie.
		if c.GetInt("id") <= 0 {
			merchantStoreRespond(c, nil, model.ErrMerchantStoreDenied)
			return
		}
		claim, err := model.ClaimMerchantStoreOrder(c.Param("token"), input.PickupCode, c.GetInt("id"))
		merchantStoreRespond(c, claim, err)
		return
	}
	metadata, err := model.InspectMerchantStoreClaim(c.Param("token"))
	if err != nil {
		merchantStoreRespond(c, nil, err)
		return
	}
	if !metadata.PickupLoginRequired {
		claim, err := model.ClaimMerchantStoreOrder(c.Param("token"), input.PickupCode, 0)
		merchantStoreRespond(c, claim, err)
		return
	}
	claim, err := service.ClaimMerchantStoreOrderWithCookie(c.Request, c.Param("token"), input.PickupCode)
	merchantStoreRespond(c, claim, err)
}
