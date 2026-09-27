/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
package controller

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/gin-gonic/gin"
)

func aiDirectoryAdError(c *gin.Context, err error) {
	status := http.StatusInternalServerError
	code := "AI_DIRECTORY_AD_FAILED"
	switch {
	case errors.Is(err, model.ErrAIDirectoryAdInvalidInput), errors.Is(err, model.ErrAIDirectoryAdInvalidBid):
		status, code = http.StatusUnprocessableEntity, "AI_DIRECTORY_AD_INVALID_INPUT"
	case errors.Is(err, model.ErrAIDirectoryAdInsufficient):
		status, code = http.StatusPaymentRequired, "AI_DIRECTORY_AD_INSUFFICIENT_BALANCE"
	case errors.Is(err, model.ErrAIDirectoryAdConflict):
		status, code = http.StatusConflict, "AI_DIRECTORY_AD_REQUEST_CONFLICT"
	case errors.Is(err, model.ErrAIDirectoryAdQuoteChanged):
		status, code = http.StatusConflict, "AI_DIRECTORY_AD_QUOTE_CHANGED"
	case errors.Is(err, model.ErrAIDirectoryAdNotFound):
		status, code = http.StatusNotFound, "AI_DIRECTORY_AD_NOT_FOUND"
	}
	message := "Unable to process the advertisement"
	if status != http.StatusInternalServerError {
		message = err.Error()
	}
	c.JSON(status, gin.H{"success": false, "code": code, "message": message})
}

func ListAIDirectoryAds(c *gin.Context) {
	offset, err := strconv.Atoi(c.DefaultQuery("offset", "0"))
	if err != nil {
		aiDirectoryAdError(c, model.ErrAIDirectoryAdInvalidInput)
		return
	}
	ads, more, err := model.ListActiveAIDirectoryAds(time.Now().Unix(), offset, 20)
	if err != nil {
		aiDirectoryAdError(c, err)
		return
	}
	if ads == nil {
		ads = []model.AIDirectoryAd{}
	}
	common.ApiSuccess(c, gin.H{"items": ads, "has_more": more, "next_offset": offset + len(ads)})
}

func QuoteAIDirectoryAd(c *gin.Context) {
	bidCents, err := strconv.ParseInt(c.Query("bid_cents"), 10, 64)
	if err != nil {
		aiDirectoryAdError(c, model.ErrAIDirectoryAdInvalidBid)
		return
	}
	quota, err := model.AIDirectoryAdChargeQuota(bidCents)
	if err != nil {
		aiDirectoryAdError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{
		"bid_cents": bidCents, "quota": quota, "currency": "USD",
		"duration_days": model.AIDirectoryAdDurationDays,
		"min_bid_cents": model.AIDirectoryAdMinBidCents,
		"max_bid_cents": model.AIDirectoryAdMaxBidCents,
	})
}

func CreateAIDirectoryAd(c *gin.Context) {
	var input model.AIDirectoryAdInput
	if err := c.ShouldBindJSON(&input); err != nil {
		aiDirectoryAdError(c, model.ErrAIDirectoryAdInvalidInput)
		return
	}
	ad, created, err := model.CreateAIDirectoryAd(c.GetInt("id"), input, time.Now().Unix())
	if err != nil {
		aiDirectoryAdError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"ad": ad, "created": created, "charged_quota": ad.ChargedQuota})
}

func ListMyAIDirectoryAds(c *gin.Context) {
	ads, err := model.ListMyAIDirectoryAds(c.GetInt("id"))
	if err != nil {
		aiDirectoryAdError(c, err)
		return
	}
	if ads == nil {
		ads = []model.AIDirectoryAd{}
	}
	common.ApiSuccess(c, gin.H{"items": ads})
}

func HideAIDirectoryAd(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		aiDirectoryAdError(c, model.ErrAIDirectoryAdInvalidInput)
		return
	}
	ad, refunded, err := model.HideAIDirectoryAd(id, time.Now().Unix())
	if err != nil {
		aiDirectoryAdError(c, err)
		return
	}
	recordManageAudit(c, "ai_directory_ad.hide", map[string]interface{}{"ad_id": id, "refunded": refunded})
	common.ApiSuccess(c, gin.H{"ad": ad, "refunded": refunded, "refunded_quota": ad.ChargedQuota})
}
