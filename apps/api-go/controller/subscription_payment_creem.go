package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/logger"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/pkg/paymentpricing"
	"github.com/LIghtJUNction/api.lmm.best/setting"
	"github.com/gin-gonic/gin"
	"github.com/thanhpk/randstr"
)

type SubscriptionCreemPayRequest struct {
	PlanId int `json:"plan_id"`
}

type creemRemoteProduct struct {
	ID            string `json:"id"`
	Price         int64  `json:"price"` // provider minor units
	Currency      string `json:"currency"`
	BillingType   string `json:"billing_type"`
	BillingPeriod string `json:"billing_period"`
	Status        string `json:"status"`
}

func expectedCreemBillingPeriod(plan *model.SubscriptionPlan) (string, error) {
	if plan == nil {
		return "", fmt.Errorf("subscription plan is required")
	}
	switch plan.DurationUnit {
	case model.SubscriptionDurationDay:
		if plan.DurationValue == 1 {
			return "every-day", nil
		}
	case model.SubscriptionDurationMonth:
		switch plan.DurationValue {
		case 1:
			return "every-month", nil
		case 3:
			return "every-three-months", nil
		case 6:
			return "every-six-months", nil
		case 12:
			return "every-year", nil
		}
	case model.SubscriptionDurationYear:
		if plan.DurationValue == 1 {
			return "every-year", nil
		}
	}
	return "", fmt.Errorf("Creem recurring billing supports exactly 1 day, 1/3/6/12 months, or 1 year")
}

func validateCreemRemoteProduct(product *creemRemoteProduct, plan *model.SubscriptionPlan, expectedAmountMicros int64, expectedCurrency string) error {
	if product == nil || plan == nil || strings.TrimSpace(product.ID) != strings.TrimSpace(plan.CreemProductId) {
		return fmt.Errorf("Creem product id mismatch")
	}
	if !strings.EqualFold(strings.TrimSpace(product.Status), "active") || !strings.EqualFold(strings.TrimSpace(product.BillingType), "recurring") {
		return fmt.Errorf("Creem product is not an active recurring product")
	}
	currency := strings.ToUpper(strings.TrimSpace(product.Currency))
	if currency != strings.ToUpper(strings.TrimSpace(expectedCurrency)) {
		return fmt.Errorf("Creem product currency mismatch")
	}
	amountMicros, err := minorCurrencyUnitsToMicros(product.Price, currency)
	if err != nil || amountMicros != expectedAmountMicros {
		return fmt.Errorf("Creem product price mismatch")
	}
	expectedPeriod, err := expectedCreemBillingPeriod(plan)
	if err != nil {
		return err
	}
	if !strings.EqualFold(strings.TrimSpace(product.BillingPeriod), expectedPeriod) {
		return fmt.Errorf("Creem product billing period mismatch")
	}
	return nil
}

func getConfiguredCreemProduct(ctx context.Context, productID string) (*creemRemoteProduct, error) {
	if strings.TrimSpace(setting.CreemApiKey) == "" {
		return nil, fmt.Errorf("Creem API key is not configured")
	}
	apiURL := "https://api.creem.io/v1/products"
	if setting.CreemTestMode {
		apiURL = "https://test-api.creem.io/v1/products"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL+"?product_id="+url.QueryEscape(strings.TrimSpace(productID)), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("x-api-key", setting.CreemApiKey)
	client := &http.Client{
		Timeout:       10 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := common.ReadResponseBody(resp)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("Creem product lookup returned HTTP %d", resp.StatusCode)
	}
	var product creemRemoteProduct
	if err := json.Unmarshal(body, &product); err != nil {
		return nil, err
	}
	if strings.TrimSpace(product.ID) == "" {
		var wrapped struct {
			Data creemRemoteProduct `json:"data"`
		}
		if err := json.Unmarshal(body, &wrapped); err != nil {
			return nil, err
		}
		product = wrapped.Data
	}
	if strings.TrimSpace(product.ID) == "" {
		return nil, fmt.Errorf("Creem product lookup returned no product")
	}
	return &product, nil
}

func validateCreemSubscriptionCheckoutEvidence(order *model.SubscriptionOrder, event *CreemWebhookEvent) error {
	if order == nil || event == nil {
		return fmt.Errorf("missing Creem subscription settlement evidence")
	}
	if order.PaymentProvider != model.PaymentProviderCreem || order.ExpectedAmountMicros <= 0 || strings.TrimSpace(order.SettlementCurrency) == "" || strings.TrimSpace(order.ProviderProductId) == "" {
		return fmt.Errorf("Creem subscription order has no immutable settlement snapshot")
	}
	if event.EventType != "checkout.completed" || !strings.EqualFold(event.Object.Order.Status, "paid") || !strings.EqualFold(event.Object.Order.Type, "recurring") {
		return fmt.Errorf("Creem checkout is not a paid recurring order")
	}
	if order.TradeNo != "" && strings.TrimSpace(event.Object.RequestId) != strings.TrimSpace(order.TradeNo) {
		return fmt.Errorf("Creem subscription trade number mismatch")
	}
	productID := strings.TrimSpace(event.Object.Order.Product)
	if productID == "" {
		productID = strings.TrimSpace(event.Object.Product.Id)
	}
	if productID != strings.TrimSpace(order.ProviderProductId) {
		return fmt.Errorf("Creem subscription product mismatch")
	}
	currency := strings.ToUpper(strings.TrimSpace(event.Object.Order.Currency))
	if currency != strings.ToUpper(strings.TrimSpace(order.SettlementCurrency)) {
		return fmt.Errorf("Creem subscription currency mismatch")
	}
	amountMicros, err := minorCurrencyUnitsToMicros(int64(event.Object.Order.AmountPaid), currency)
	if err != nil || amountMicros != order.ExpectedAmountMicros {
		return fmt.Errorf("Creem subscription amount mismatch")
	}
	if strings.TrimSpace(event.Id) == "" || strings.TrimSpace(event.Object.Order.Transaction) == "" {
		return fmt.Errorf("Creem subscription idempotency evidence is missing")
	}
	return nil
}

func SubscriptionRequestCreemPay(c *gin.Context) {
	if !requirePaymentCompliance(c) {
		return
	}

	var req SubscriptionCreemPayRequest

	// Keep body for debugging consistency (like RequestCreemPay)
	bodyBytes, err := common.ReadAllLimit(c.Request.Body, common.GetAnonymousRequestBodyLimitBytes())
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Creem 订阅支付请求读取失败 error=%q", err.Error()))
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "read query error"})
		return
	}
	c.Request.Body = io.NopCloser(bytes.NewReader(bodyBytes))

	if err := c.ShouldBindJSON(&req); err != nil || req.PlanId <= 0 {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "参数错误"})
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
	if !requireSubscriptionPaymentMethodAvailable(c, plan, model.PaymentMethodCreem) {
		return
	}
	if plan.CreemProductId == "" {
		common.ApiErrorMsg(c, "该套餐未配置 CreemProductId")
		return
	}
	if setting.CreemWebhookSecret == "" && !setting.CreemTestMode {
		common.ApiErrorMsg(c, "Creem Webhook 未配置")
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

	expectedAmountMicros, settlementCurrency, err := subscriptionSettlementSnapshot(plan, paymentpricing.CurrencyUSD)
	if err != nil {
		common.ApiErrorMsg(c, "套餐结算金额或币种无效")
		return
	}
	remoteProduct, err := getConfiguredCreemProduct(c.Request.Context(), plan.CreemProductId)
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Creem 订阅产品读取失败 plan_id=%d product_id=%s error=%q", plan.Id, plan.CreemProductId, err.Error()))
		common.ApiErrorMsg(c, "无法核验 Creem 订阅产品")
		return
	}
	if err := validateCreemRemoteProduct(remoteProduct, plan, expectedAmountMicros, settlementCurrency); err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Creem 订阅产品不匹配 plan_id=%d product_id=%s error=%q", plan.Id, plan.CreemProductId, err.Error()))
		common.ApiErrorMsg(c, "Creem 产品价格、币种或周期与套餐不一致")
		return
	}

	reference := "sub-creem-ref-" + randstr.String(6)
	referenceId := "sub_ref_" + common.Sha1([]byte(reference+time.Now().String()+user.Username))

	// Create the immutable order before redirecting to Creem. Subscription
	// prices are real fiat and use only ConvertFiat; wallet purchase ratio B is
	// deliberately absent from this path.
	order := &model.SubscriptionOrder{
		UserId:               userId,
		PlanId:               plan.Id,
		Money:                plan.PriceAmount,
		TradeNo:              referenceId,
		PaymentMethod:        model.PaymentMethodCreem,
		PaymentProvider:      model.PaymentProviderCreem,
		CreateTime:           time.Now().Unix(),
		Status:               common.TopUpStatusPending,
		PlanSnapshot:         common.GetJsonString(plan),
		ExpectedAmountMicros: expectedAmountMicros,
		SettlementCurrency:   settlementCurrency,
		ProviderProductId:    strings.TrimSpace(plan.CreemProductId),
	}
	if err := order.Insert(); err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "创建订单失败"})
		return
	}

	// Creem subscriptions settle in USD independently of quota display mode.
	product := &CreemProduct{
		ProductId: plan.CreemProductId,
		Name:      plan.Title,
		Price:     float64(expectedAmountMicros) / 1_000_000,
		Currency:  settlementCurrency,
		Quota:     0,
	}

	checkoutUrl, err := genCreemLink(c.Request.Context(), referenceId, product, user.Email, user.Username)
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Creem 订阅支付链接创建失败 trade_no=%s product_id=%s error=%q", referenceId, product.ProductId, err.Error()))
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "拉起支付失败"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "success",
		"data": gin.H{
			"checkout_url": checkoutUrl,
			"order_id":     referenceId,
		},
	})
}
