package controller

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/logger"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/pkg/paymentpricing"
	"github.com/LIghtJUNction/api.lmm.best/setting"
	"github.com/gin-gonic/gin"
	"github.com/stripe/stripe-go/v81"
	"github.com/stripe/stripe-go/v81/checkout/session"
	stripeprice "github.com/stripe/stripe-go/v81/price"
	"github.com/thanhpk/randstr"
)

const (
	stripeSubscriptionPriceMetadataKey = "subscription_price_id"
	subscriptionTradeNoMetadataKey     = "subscription_trade_no"
	stripeSubscriptionProviderCurrency = paymentpricing.CurrencyUSD
)

var retrieveStripeSubscriptionPrice = stripeprice.Get

type SubscriptionStripePayRequest struct {
	PlanId int `json:"plan_id"`
}

func validateStripeSubscriptionCheckoutEvidence(order *model.SubscriptionOrder, session *stripe.CheckoutSession) error {
	if order == nil || session == nil {
		return fmt.Errorf("stripe subscription evidence is required")
	}
	if order.PaymentProvider != model.PaymentProviderStripe {
		return fmt.Errorf("stripe subscription provider mismatch")
	}
	if session.Mode != stripe.CheckoutSessionModeSubscription {
		return fmt.Errorf("stripe checkout is not subscription mode")
	}
	if session.PaymentStatus != stripe.CheckoutSessionPaymentStatusPaid {
		return fmt.Errorf("stripe subscription payment is not paid")
	}
	currency := strings.ToUpper(string(session.Currency))
	if currency != strings.ToUpper(strings.TrimSpace(order.SettlementCurrency)) {
		return fmt.Errorf("stripe subscription currency mismatch")
	}
	amountMicros, err := minorCurrencyUnitsToMicros(session.AmountTotal, currency)
	if err != nil || amountMicros != order.ExpectedAmountMicros {
		return fmt.Errorf("stripe subscription amount mismatch")
	}
	if session.Metadata[stripeSubscriptionPriceMetadataKey] != order.ProviderProductId {
		return fmt.Errorf("stripe subscription price mismatch")
	}
	if order.TradeNo != "" && session.ClientReferenceID != order.TradeNo {
		return fmt.Errorf("stripe subscription trade number mismatch")
	}
	if session.Subscription == nil || strings.TrimSpace(session.Subscription.ID) == "" {
		return fmt.Errorf("stripe subscription id is missing")
	}
	return nil
}

func SubscriptionRequestStripePay(c *gin.Context) {
	if !requirePaymentCompliance(c) {
		return
	}

	var req SubscriptionStripePayRequest
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
	if !requireSubscriptionPaymentMethodAvailable(c, plan, model.PaymentMethodStripe) {
		return
	}
	if plan.StripePriceId == "" {
		common.ApiErrorMsg(c, "该套餐未配置 StripePriceId")
		return
	}
	if !strings.HasPrefix(setting.StripeApiSecret, "sk_") && !strings.HasPrefix(setting.StripeApiSecret, "rk_") {
		common.ApiErrorMsg(c, "Stripe 未配置或密钥无效")
		return
	}
	if setting.StripeWebhookSecret == "" {
		common.ApiErrorMsg(c, "Stripe Webhook 未配置")
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

	expectedAmountMicros, settlementCurrency, err := subscriptionSettlementSnapshot(plan, stripeSubscriptionProviderCurrency)
	if err != nil {
		common.ApiErrorMsg(c, "套餐结算金额或币种无效")
		return
	}
	stripe.Key = setting.StripeApiSecret
	providerPrice, err := retrieveStripeSubscriptionPrice(strings.TrimSpace(plan.StripePriceId), nil)
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Stripe 订阅价格读取失败 plan_id=%d price_id=%s error=%q", plan.Id, plan.StripePriceId, err.Error()))
		common.ApiErrorMsg(c, "Stripe 套餐价格读取失败")
		return
	}
	if err := validateStripeSubscriptionPrice(providerPrice, plan.StripePriceId, expectedAmountMicros, settlementCurrency); err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Stripe 订阅价格与本地套餐不一致 plan_id=%d price_id=%s error=%q", plan.Id, plan.StripePriceId, err.Error()))
		common.ApiErrorMsg(c, "Stripe 套餐价格与本地套餐不一致")
		return
	}

	reference := fmt.Sprintf("sub-stripe-ref-%d-%d-%s", user.Id, time.Now().UnixMilli(), randstr.String(4))
	referenceId := "sub_ref_" + common.Sha1([]byte(reference))

	order := &model.SubscriptionOrder{
		UserId:               userId,
		PlanId:               plan.Id,
		Money:                plan.PriceAmount,
		TradeNo:              referenceId,
		PaymentMethod:        model.PaymentMethodStripe,
		PaymentProvider:      model.PaymentProviderStripe,
		CreateTime:           time.Now().Unix(),
		Status:               common.TopUpStatusPending,
		PlanSnapshot:         common.GetJsonString(plan),
		ExpectedAmountMicros: expectedAmountMicros,
		SettlementCurrency:   settlementCurrency,
		ProviderProductId:    strings.TrimSpace(plan.StripePriceId),
	}
	if err := order.Insert(); err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "创建订单失败"})
		return
	}

	// Persist the local pending order before creating the external checkout.
	// If the database write failed after a Stripe session had been created, the
	// customer could still complete payment while the webhook had no local
	// order to settle. The order-first sequence makes every checkout URL
	// traceable to a durable local record.
	payLink, err := genStripeSubscriptionLink(referenceId, user.StripeCustomer, user.Email, plan.StripePriceId)
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Stripe 订阅支付链接创建失败 trade_no=%s plan_id=%d error=%q", referenceId, plan.Id, err.Error()))
		// Keep the order pending: a transport error can happen after Stripe
		// accepted the session request, and the webhook must still be able to
		// settle that payment against this durable local order.
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "拉起支付失败"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "success",
		"data": gin.H{
			"pay_link": payLink,
		},
	})
}

func validateStripeSubscriptionPrice(providerPrice *stripe.Price, expectedPriceID string, expectedAmountMicros int64, expectedCurrency string) error {
	if providerPrice == nil || strings.TrimSpace(providerPrice.ID) != strings.TrimSpace(expectedPriceID) {
		return fmt.Errorf("Stripe price id mismatch")
	}
	if !providerPrice.Active || providerPrice.Recurring == nil {
		return fmt.Errorf("Stripe price is not an active recurring price")
	}
	currency := strings.ToUpper(strings.TrimSpace(string(providerPrice.Currency)))
	if currency != strings.ToUpper(strings.TrimSpace(expectedCurrency)) {
		return fmt.Errorf("Stripe price currency mismatch")
	}
	amountMicros, err := minorCurrencyUnitsToMicros(providerPrice.UnitAmount, currency)
	if err != nil || amountMicros != expectedAmountMicros {
		return fmt.Errorf("Stripe price amount mismatch")
	}
	return nil
}

func genStripeSubscriptionLink(referenceId string, customerId string, email string, priceId string) (string, error) {
	stripe.Key = setting.StripeApiSecret

	metadata := map[string]string{
		subscriptionTradeNoMetadataKey:     referenceId,
		stripeSubscriptionPriceMetadataKey: strings.TrimSpace(priceId),
	}
	params := &stripe.CheckoutSessionParams{
		ClientReferenceID: stripe.String(referenceId),
		SuccessURL:        stripe.String(paymentReturnPath("/wallet")),
		CancelURL:         stripe.String(paymentReturnPath("/wallet")),
		LineItems: []*stripe.CheckoutSessionLineItemParams{
			{
				Price:    stripe.String(priceId),
				Quantity: stripe.Int64(1),
			},
		},
		Mode:             stripe.String(string(stripe.CheckoutSessionModeSubscription)),
		Metadata:         metadata,
		SubscriptionData: &stripe.CheckoutSessionSubscriptionDataParams{Metadata: metadata},
	}

	if "" == customerId {
		if "" != email {
			params.CustomerEmail = stripe.String(email)
		}
		params.CustomerCreation = stripe.String(string(stripe.CheckoutSessionCustomerCreationAlways))
	} else {
		params.Customer = stripe.String(customerId)
	}

	result, err := session.New(params)
	if err != nil {
		return "", err
	}
	return result.URL, nil
}
