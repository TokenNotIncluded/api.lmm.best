package controller

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/logger"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/LIghtJUNction/api.lmm.best/setting"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"github.com/thanhpk/randstr"
)

type SubscriptionWaffoPancakePayRequest struct {
	PlanId           int    `json:"plan_id"`
	CheckoutRegion   string `json:"checkout_region"`
	CheckoutLanguage string `json:"checkout_language"`
}

func SubscriptionRequestWaffoPancakePay(c *gin.Context) {
	if !requirePaymentCompliance(c) {
		return
	}

	var req SubscriptionWaffoPancakePayRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.PlanId <= 0 {
		common.ApiErrorMsg(c, "参数错误")
		return
	}
	plan, err := model.GetSubscriptionPlanById(req.PlanId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if !plan.Enabled {
		common.ApiErrorMsg(c, "套餐未启用")
		return
	}
	if !requireSubscriptionPaymentMethodAvailable(c, plan, model.PaymentMethodWaffoPancake) {
		return
	}
	if strings.TrimSpace(plan.WaffoPancakeProductId) == "" {
		common.ApiErrorMsg(c, "该套餐未配置 WaffoPancakeProductId")
		return
	}
	// Plan targets its own Pancake product, so we only require credentials
	// here — not the gateway-level WaffoPancakeProductID.
	merchantID, privateKey := service.WaffoPancakeCredentials()
	storeID := strings.TrimSpace(setting.WaffoPancakeStoreID)
	if merchantID == "" || privateKey == "" || storeID == "" {
		common.ApiErrorMsg(c, "Waffo Pancake 未配置或密钥无效")
		return
	}
	catalog, err := service.ListWaffoPancakeCatalog(c.Request.Context(), merchantID, privateKey)
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Waffo Pancake 订阅产品核验失败 plan_id=%d product_id=%s error=%q", plan.Id, plan.WaffoPancakeProductId, err.Error()))
		common.ApiErrorMsg(c, "无法核验 Waffo Pancake 订阅产品")
		return
	}
	if !service.WaffoPancakeCatalogHasActiveSubscriptionProduct(catalog, storeID, plan.WaffoPancakeProductId) {
		common.ApiErrorMsg(c, "套餐绑定的不是有效订阅产品，请重新创建并绑定")
		return
	}

	userId := c.GetInt("id")
	user, err := model.GetUserById(userId, false)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if user == nil {
		common.ApiErrorMsg(c, "用户不存在")
		return
	}

	if plan.MaxPurchasePerUser > 0 {
		count, err := model.CountUserSubscriptionsByPlan(userId, plan.Id)
		if err != nil {
			common.ApiError(c, err)
			return
		}
		if count >= int64(plan.MaxPurchasePerUser) {
			common.ApiErrorMsg(c, "已达到该套餐购买上限")
			return
		}
	}

	expectedAmountMicros, settlementCurrency, err := subscriptionSettlementSnapshot(plan, "USD")
	if err != nil {
		common.ApiErrorMsg(c, "套餐结算金额无效")
		return
	}
	settlementAmount := decimal.NewFromInt(expectedAmountMicros).Shift(-6)
	if !settlementAmount.IsPositive() {
		common.ApiErrorMsg(c, "套餐结算金额无效")
		return
	}

	// WAFFO_PANCAKE_SUB- prefix (vs. wallet's WAFFO_PANCAKE-) drives webhook
	// dispatch in WaffoPancakeWebhook.
	tradeNo := fmt.Sprintf("WAFFO_PANCAKE_SUB-%d-%d-%s", userId, time.Now().UnixMilli(), randstr.String(6))

	order := &model.SubscriptionOrder{
		UserId:               userId,
		PlanId:               plan.Id,
		Money:                plan.PriceAmount,
		TradeNo:              tradeNo,
		PaymentMethod:        model.PaymentMethodWaffoPancake,
		PaymentProvider:      model.PaymentProviderWaffoPancake,
		CreateTime:           time.Now().Unix(),
		Status:               common.TopUpStatusPending,
		PlanSnapshot:         common.GetJsonString(plan),
		ExpectedAmountMicros: expectedAmountMicros,
		SettlementCurrency:   settlementCurrency,
		ProviderProductId:    strings.TrimSpace(plan.WaffoPancakeProductId),
		ProviderStoreId:      storeID,
	}
	if err := order.Insert(); err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Waffo Pancake 订阅订单创建失败 user_id=%d plan_id=%d trade_no=%s error=%q", userId, plan.Id, tradeNo, err.Error()))
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "创建订单失败"})
		return
	}

	expiresInSeconds := 45 * 60
	session, err := service.CreateWaffoPancakeCheckoutSession(c.Request.Context(), &service.WaffoPancakeCreateSessionParams{
		ProductID:     plan.WaffoPancakeProductId,
		BuyerIdentity: service.WaffoPancakeBuyerIdentityFromUserID(user.Id),
		PriceSnapshot: &service.WaffoPancakePriceSnapshot{
			Amount:      settlementAmount.StringFixed(2),
			TaxCategory: "saas",
		},
		OrderMetadata: map[string]string{
			service.WaffoPancakeOrderMetadataProductID: strings.TrimSpace(plan.WaffoPancakeProductId),
			service.WaffoPancakeOrderMetadataPlanID:    strconv.Itoa(plan.Id),
		},
		BuyerEmail:              getWaffoPancakeBuyerEmail(user),
		ExpiresInSeconds:        &expiresInSeconds,
		OrderMerchantExternalID: tradeNo,
		CheckoutRegion:          req.CheckoutRegion,
		CheckoutLanguage:        req.CheckoutLanguage,
	})
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Waffo Pancake 订阅结账会话创建失败 user_id=%d plan_id=%d trade_no=%s error=%q", userId, plan.Id, tradeNo, err.Error()))
		// A transport timeout can happen after Pancake accepted the session.
		// Keep the durable order pending so a later signed webhook can settle it.
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "拉起支付失败"})
		return
	}
	logger.LogInfo(c.Request.Context(), fmt.Sprintf("Waffo Pancake 订阅订单创建成功 user_id=%d plan_id=%d trade_no=%s session_id=%s plan_price=%.2f plan_currency=%s settlement_amount=%s settlement_currency=USD", userId, plan.Id, tradeNo, session.SessionID, plan.PriceAmount, plan.Currency, settlementAmount.StringFixed(2)))

	c.JSON(http.StatusOK, gin.H{
		"message": "success",
		"data": gin.H{
			"checkout_url":     session.CheckoutURL,
			"session_id":       session.SessionID,
			"expires_at":       session.ExpiresAt,
			"order_id":         tradeNo,
			"token":            session.Token,
			"token_expires_at": session.TokenExpiresAt,
		},
	})
}
