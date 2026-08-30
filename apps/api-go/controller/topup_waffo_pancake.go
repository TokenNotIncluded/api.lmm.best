package controller

import (
	"encoding/json"
	"errors"
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
	"gorm.io/gorm"
)

type WaffoPancakePayRequest struct {
	Amount           float64 `json:"amount"`
	DiscountCode     string  `json:"discount_code,omitempty"`
	CheckoutRegion   string  `json:"checkout_region"`
	CheckoutLanguage string  `json:"checkout_language"`
}

func RequestWaffoPancakeAmount(c *gin.Context) {
	var req WaffoPancakePayRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "参数错误"})
		return
	}
	requestedAmount, err := parseRequestedTopUpAmount(req.Amount)
	if err != nil {
		common.ApiErrorMsg(c, err.Error())
		return
	}
	if !requirePaymentMethodAvailable(c, model.PaymentMethodWaffoPancake) {
		return
	}

	if requestedAmount.LessThan(decimal.NewFromInt(int64(setting.WaffoPancakeMinTopUp))) {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": fmt.Sprintf("充值数量不能小于 %d", setting.WaffoPancakeMinTopUp)})
		return
	}
	if !requirePaymentMethodTopUpDecimalWithinLimit(c, model.PaymentMethodWaffoPancake, requestedAmount) {
		return
	}

	id := c.GetInt("id")
	_, _, creditedQuota, err := topUpOrderAmountsDecimal(requestedAmount)
	if err != nil {
		common.ApiErrorMsg(c, err.Error())
		return
	}
	if !requireTopUpCreditCapacity(c, id, creditedQuota) {
		return
	}
	group, err := model.GetUserGroup(id, true)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "获取用户分组失败"})
		return
	}

	payMoneyDecimal, _, err := applyDiscountCodeQuoteDecimal(getWaffoPancakePayMoneyForAmount(requestedAmount, group), requestedAmount, req.DiscountCode, id)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "优惠码无效"})
		return
	}
	if payMoneyDecimal.LessThanOrEqual(decimal.NewFromFloat(0.01)) {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "充值金额过低"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "success", "data": payMoneyDecimal.StringFixed(2)})
}

func getWaffoPancakePayMoney(amount int64, group string) float64 {
	return getWaffoPancakePayMoneyDecimal(amount, group).InexactFloat64()
}

func getWaffoPancakePayMoneyDecimal(amount int64, group string) decimal.Decimal {
	return getWaffoPancakePayMoneyForAmount(decimal.NewFromInt(amount), group)
}

func getWaffoPancakePayMoneyForAmount(amount decimal.Decimal, group string) decimal.Decimal {
	pricing, err := standardSettlementPricing("USD")
	if err != nil {
		return decimal.Zero
	}
	quoted, err := quoteTopUpDecimalWithSettlementPricing(amount, group, pricing, decimal.NewFromInt(1))
	if err != nil {
		return decimal.Zero
	}
	return quoted
}

func formatWaffoPancakeAmount(payMoney float64) string {
	return decimal.NewFromFloat(payMoney).StringFixed(2)
}

func getWaffoPancakeBuyerEmail(user *model.User) string {
	if user != nil && strings.TrimSpace(user.Email) != "" {
		return user.Email
	}
	return ""
}

// The admin config endpoints below accept typed-but-not-yet-saved creds in
// the body and fall back to persisted creds when the body is blank (see
// resolveWaffoPancakeAdminCreds). Only SaveWaffoPancake writes to OptionMap.

type saveWaffoPancakeRequest struct {
	MerchantID string `json:"merchant_id"`
	PrivateKey string `json:"private_key"`
	ReturnURL  string `json:"return_url"`
	StoreID    string `json:"store_id"`
	ProductID  string `json:"product_id"`
}

type createWaffoPancakePairRequest struct {
	MerchantID string `json:"merchant_id"`
	PrivateKey string `json:"private_key"`
	ReturnURL  string `json:"return_url"`
}

type listWaffoPancakeCatalogRequest struct {
	MerchantID string `json:"merchant_id"`
	PrivateKey string `json:"private_key"`
}

// SaveWaffoPancake atomically persists all five operator-controlled fields.
// Catalog / pair endpoints are transient — only this one writes the OptionMap.
func SaveWaffoPancake(c *gin.Context) {
	var req saveWaffoPancakeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "参数错误"})
		return
	}
	if err := service.SaveWaffoPancakeConfig(
		c.Request.Context(),
		req.MerchantID,
		req.PrivateKey,
		req.ReturnURL,
		req.StoreID,
		req.ProductID,
	); err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf(
			"Waffo Pancake 保存配置失败 store_id=%q product_id=%q error=%q",
			req.StoreID, req.ProductID, err.Error(),
		))
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "保存配置失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"message": "success",
		"data": gin.H{
			"product_id": setting.WaffoPancakeProductID,
			"store_id":   setting.WaffoPancakeStoreID,
		},
	})
}

// resolveWaffoPancakeAdminCreds prefers body creds (typed-but-not-yet-saved
// values, for verification) and falls back to persisted creds when the body
// is blank (so returning admins don't have to re-paste the private key,
// which is stripped from GET /api/option/).
func resolveWaffoPancakeAdminCreds(bodyMerchantID, bodyPrivateKey string) (string, string) {
	m := strings.TrimSpace(bodyMerchantID)
	k := strings.TrimSpace(bodyPrivateKey)
	if m == "" && k == "" {
		return service.WaffoPancakeCredentials()
	}
	if m == "" {
		m, _ = service.WaffoPancakeCredentials()
	}
	if k == "" {
		_, k = service.WaffoPancakeCredentials()
	}
	return m, k
}

// CreateWaffoPancakePair mints a Store + OnetimeProduct pair in one round-
// trip. Surfaces an orphan-store flag when the product half fails so the
// frontend can preselect / retry without losing context.
func CreateWaffoPancakePair(c *gin.Context) {
	var req createWaffoPancakePairRequest
	if c.Request.ContentLength > 0 {
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusOK, gin.H{"message": "error", "data": "参数错误"})
			return
		}
	}
	merchantID, privateKey := resolveWaffoPancakeAdminCreds(req.MerchantID, req.PrivateKey)
	if merchantID == "" || privateKey == "" {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "Waffo Pancake 凭证未配置"})
		return
	}
	result, err := service.CreateWaffoPancakePrimaryPair(
		c.Request.Context(), merchantID, privateKey, req.ReturnURL,
	)
	var resultValue service.WaffoPancakePairResult
	hasResult := result != nil
	if hasResult {
		resultValue = *result
	}
	if err != nil {
		orphan := hasResult && resultValue.OrphanStore
		logger.LogError(c.Request.Context(), fmt.Sprintf(
			"Waffo Pancake 创建店铺与产品失败 orphan_store=%t store_id=%q error=%q",
			orphan, resultValue.StoreID, err.Error(),
		))
		data := gin.H{"error": err.Error()}
		if orphan {
			data["store_id"] = resultValue.StoreID
			data["store_name"] = resultValue.StoreName
			data["orphan_store"] = true
		}
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": data})
		return
	}
	if !hasResult {
		const message = "Waffo Pancake 创建店铺与产品失败：响应为空"
		logger.LogError(c.Request.Context(), message)
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": gin.H{"error": message}})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"message": "success",
		"data": gin.H{
			"store_id":     resultValue.StoreID,
			"store_name":   resultValue.StoreName,
			"product_id":   resultValue.ProductID,
			"product_name": resultValue.ProductName,
		},
	})
}

// ListWaffoPancakeCatalog returns the merchant's Stores + OnetimeProducts.
// Doubles as a credential probe (a successful 200 proves the resolved creds
// authenticate). Credentials are accepted only in the JSON body; never read a
// private key from query parameters because URLs are routinely logged.
func ListWaffoPancakeCatalog(c *gin.Context) {
	var req listWaffoPancakeCatalogRequest
	if c.Request.ContentLength > 0 {
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusOK, gin.H{"message": "error", "data": "参数错误"})
			return
		}
	}
	merchantID, privateKey := resolveWaffoPancakeAdminCreds(req.MerchantID, req.PrivateKey)
	if merchantID == "" || privateKey == "" {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "Waffo Pancake 凭证未配置"})
		return
	}
	catalog, err := service.ListWaffoPancakeCatalog(c.Request.Context(), merchantID, privateKey)
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf(
			"Waffo Pancake 拉取店铺与产品目录失败 error=%q", err.Error(),
		))
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "拉取目录失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "success", "data": catalog})
}

type createWaffoPancakeSubscriptionProductRequest struct {
	Name          string `json:"name"`
	Amount        string `json:"amount"` // real fiat in Currency
	Currency      string `json:"currency"`
	DurationUnit  string `json:"duration_unit"`
	DurationValue int    `json:"duration_value"`
	ProductType   string `json:"product_type"`
}

func parseWaffoPancakePlanProductType(value string) (string, error) {
	productType := strings.ToLower(strings.TrimSpace(value))
	if productType == "" {
		return model.WaffoPancakeProductTypeSubscription, nil
	}
	if productType != model.WaffoPancakeProductTypeOneTime &&
		productType != model.WaffoPancakeProductTypeSubscription {
		return "", fmt.Errorf("Waffo Pancake product type must be one_time or subscription")
	}
	return productType, nil
}

// CreateWaffoPancakeSubscriptionProduct mints a one-time or recurring plan
// product. The legacy route name remains stable for existing admin clients.
func CreateWaffoPancakeSubscriptionProduct(c *gin.Context) {
	var req createWaffoPancakeSubscriptionProductRequest
	if c.Request.ContentLength > 0 {
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusOK, gin.H{"message": "error", "data": "参数错误"})
			return
		}
	}
	if strings.TrimSpace(req.Name) == "" {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "套餐名称不能为空"})
		return
	}
	priceAmount, err := decimal.NewFromString(strings.TrimSpace(req.Amount))
	if err != nil || !priceAmount.IsPositive() {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "套餐法币价格必须大于零"})
		return
	}
	planCurrency, currencyErr := normalizeSubscriptionFiatCurrency(req.Currency)
	if currencyErr != nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "套餐价格币种必须为 CNY 或 USD"})
		return
	}
	expectedAmountMicros, _, err := subscriptionSettlementSnapshot(&model.SubscriptionPlan{
		PriceAmount: priceAmount.InexactFloat64(),
		Currency:    planCurrency,
	}, "USD")
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "套餐结算金额或币种无效"})
		return
	}
	settlementAmount := decimal.NewFromInt(expectedAmountMicros).Shift(-6)
	productType, err := parseWaffoPancakePlanProductType(req.ProductType)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": err.Error()})
		return
	}
	if productType == model.WaffoPancakeProductTypeSubscription {
		if _, err := service.WaffoPancakeBillingPeriodForDuration(req.DurationUnit, req.DurationValue); err != nil {
			c.JSON(http.StatusOK, gin.H{"message": "error", "data": err.Error()})
			return
		}
	}
	merchantID, privateKey := resolveWaffoPancakeAdminCreds("", "")
	storeID := strings.TrimSpace(setting.WaffoPancakeStoreID)
	if merchantID == "" || privateKey == "" || storeID == "" {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "Waffo Pancake 未完成配置，请先在支付设置中完成网关绑定"})
		return
	}
	var productID string
	if productType == model.WaffoPancakeProductTypeOneTime {
		productID, err = service.CreateWaffoPancakeOneTimeProductForPlan(
			c.Request.Context(),
			merchantID,
			privateKey,
			storeID,
			req.Name,
			settlementAmount.StringFixed(2),
			setting.WaffoPancakeReturnURL,
		)
	} else {
		productID, err = service.CreateWaffoPancakeProductForPlan(
			c.Request.Context(),
			merchantID,
			privateKey,
			storeID,
			req.Name,
			settlementAmount.StringFixed(2),
			req.DurationUnit,
			req.DurationValue,
			setting.WaffoPancakeReturnURL,
		)
	}
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf(
			"Waffo Pancake 创建套餐产品失败 store_id=%q name=%q amount=%q product_type=%q error=%q",
			storeID, req.Name, req.Amount, productType, err.Error(),
		))
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "创建套餐产品失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"message": "success",
		"data": gin.H{
			"product_id":          productID,
			"product_name":        req.Name,
			"store_id":            storeID,
			"settlement_currency": "USD",
			"settlement_amount":   settlementAmount.StringFixed(2),
			"product_type":        productType,
		},
	})
}

// ListWaffoPancakeSubscriptionProductOptions returns both plan-compatible
// product families. The legacy route name remains stable for admin clients.
func ListWaffoPancakeSubscriptionProductOptions(c *gin.Context) {
	merchantID, privateKey := resolveWaffoPancakeAdminCreds("", "")
	storeID := strings.TrimSpace(setting.WaffoPancakeStoreID)
	if merchantID == "" || privateKey == "" || storeID == "" {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "Waffo Pancake 未完成配置，请先在支付设置中完成网关绑定"})
		return
	}
	catalog, err := service.ListWaffoPancakeCatalog(c.Request.Context(), merchantID, privateKey)
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf(
			"Waffo Pancake 拉取订阅产品列表失败 store_id=%q error=%q", storeID, err.Error(),
		))
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "拉取产品列表失败"})
		return
	}
	products := []service.WaffoPancakeCatalogProduct{}
	for _, store := range catalog.Stores {
		if store.ID != storeID {
			continue
		}
		products = make([]service.WaffoPancakeCatalogProduct, 0, len(store.OnetimeProducts)+len(store.SubscriptionProducts))
		for _, product := range store.OnetimeProducts {
			product.ProductType = model.WaffoPancakeProductTypeOneTime
			products = append(products, product)
		}
		for _, product := range store.SubscriptionProducts {
			product.ProductType = model.WaffoPancakeProductTypeSubscription
			products = append(products, product)
		}
		break
	}
	c.JSON(http.StatusOK, gin.H{
		"message": "success",
		"data": gin.H{
			"store_id": storeID,
			"products": products,
		},
	})
}

func getWaffoPancakeBuyerIdentity(user *model.User) string {
	if user == nil {
		return ""
	}
	return service.WaffoPancakeBuyerIdentityFromUserID(user.Id)
}

func RequestWaffoPancakePay(c *gin.Context) {
	if !isWaffoPancakeTopUpEnabled() {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "Waffo Pancake 配置不完整"})
		return
	}

	var req WaffoPancakePayRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "参数错误"})
		return
	}
	requestedAmount, err := parseRequestedTopUpAmount(req.Amount)
	if err != nil {
		common.ApiErrorMsg(c, err.Error())
		return
	}
	if !requirePaymentMethodAvailable(c, model.PaymentMethodWaffoPancake) {
		return
	}
	if requestedAmount.LessThan(decimal.NewFromInt(int64(setting.WaffoPancakeMinTopUp))) {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": fmt.Sprintf("充值数量不能小于 %d", setting.WaffoPancakeMinTopUp)})
		return
	}
	if !requirePaymentMethodTopUpDecimalWithinLimit(c, model.PaymentMethodWaffoPancake, requestedAmount) {
		return
	}

	id := c.GetInt("id")
	storedAmount, platformAmountMicros, creditedQuota, err := topUpOrderAmountsDecimal(requestedAmount)
	if err != nil {
		common.ApiErrorMsg(c, err.Error())
		return
	}
	if !requireTopUpCreditCapacity(c, id, creditedQuota) {
		return
	}

	user, err := model.GetUserById(id, false)
	if err != nil || user == nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "用户不存在"})
		return
	}

	group, err := model.GetUserGroup(id, true)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "获取用户分组失败"})
		return
	}

	payMoneyDecimal, discountCode, err := applyDiscountCodeQuoteDecimal(getWaffoPancakePayMoneyForAmount(requestedAmount, group), requestedAmount, req.DiscountCode, id)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "优惠码无效"})
		return
	}
	if payMoneyDecimal.LessThan(decimal.NewFromFloat(0.01)) {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "充值金额过低"})
		return
	}

	tradeNo := fmt.Sprintf("WAFFO_PANCAKE-%d-%d-%s", id, time.Now().UnixMilli(), randstr.String(6))
	paymentAmount := payMoneyDecimal.StringFixed(2)
	expectedAmountMicros, err := monetaryStringToMicros(paymentAmount)
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Waffo Pancake 结算金额无效 user_id=%d trade_no=%s error=%q", id, tradeNo, err.Error()))
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "支付金额无效"})
		return
	}
	topUp := &model.TopUp{
		UserId:               id,
		Amount:               storedAmount,
		PlatformAmountMicros: platformAmountMicros,
		CreditedQuota:        creditedQuota,
		ExpectedAmountMicros: expectedAmountMicros,
		SettlementCurrency:   "USD",
		Money:                monetaryMicrosToFloat(expectedAmountMicros),
		TradeNo:              tradeNo,
		PaymentMethod:        model.PaymentMethodWaffoPancake,
		PaymentProvider:      model.PaymentProviderWaffoPancake,
		ProviderProductId:    strings.TrimSpace(setting.WaffoPancakeProductID),
		ProviderStoreId:      strings.TrimSpace(setting.WaffoPancakeStoreID),
		DiscountCodeId:       discountCodeID(discountCode),
		DiscountPercent:      discountPercent(discountCode),
		CreateTime:           time.Now().Unix(),
		Status:               common.TopUpStatusPending,
	}
	if err := topUp.Insert(); err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Waffo Pancake 创建充值订单失败 user_id=%d trade_no=%s amount=%s error=%q", id, tradeNo, requestedAmount.String(), err.Error()))
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "创建订单失败"})
		return
	}

	expiresInSeconds := 45 * 60
	session, err := service.CreateWaffoPancakeCheckoutSession(c.Request.Context(), &service.WaffoPancakeCreateSessionParams{
		ProductID:     setting.WaffoPancakeProductID,
		BuyerIdentity: getWaffoPancakeBuyerIdentity(user),
		PriceSnapshot: &service.WaffoPancakePriceSnapshot{
			Amount:      paymentAmount,
			TaxCategory: "saas",
		},
		BuyerEmail:              getWaffoPancakeBuyerEmail(user),
		ExpiresInSeconds:        &expiresInSeconds,
		OrderMerchantExternalID: tradeNo,
		OrderMetadata: map[string]string{
			service.WaffoPancakeOrderMetadataProductID: strings.TrimSpace(setting.WaffoPancakeProductID),
		},
		CheckoutRegion:   req.CheckoutRegion,
		CheckoutLanguage: req.CheckoutLanguage,
	})
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Waffo Pancake 创建结账会话失败 user_id=%d trade_no=%s error=%q", id, tradeNo, err.Error()))
		// The provider may have accepted the checkout before an I/O timeout.
		// Keep the order pending so a later signed webhook remains recoverable.
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "拉起支付失败"})
		return
	}
	logger.LogInfo(c.Request.Context(), fmt.Sprintf("Waffo Pancake 充值订单创建成功 user_id=%d trade_no=%s session_id=%s amount=%s money=%s", id, tradeNo, session.SessionID, requestedAmount.String(), paymentAmount))

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

func WaffoPancakeWebhook(c *gin.Context) {
	if !isWaffoPancakeWebhookEnabled() {
		logger.LogWarn(c.Request.Context(), fmt.Sprintf("Waffo Pancake webhook 被拒绝 reason=webhook_disabled path=%q client_ip=%s", c.Request.RequestURI, c.ClientIP()))
		c.String(http.StatusForbidden, "webhook disabled")
		return
	}

	// :env splits test vs prod traffic at the routing layer — operator
	// registers each URL in the matching webhook slot in Pancake's dashboard.
	// We then enforce event.mode == expectedEnv to catch mis-registrations.
	expectedEnv := strings.TrimSpace(c.Param("env"))
	if expectedEnv != "test" && expectedEnv != "prod" {
		logger.LogWarn(c.Request.Context(), fmt.Sprintf(
			"Waffo Pancake webhook 路径环境段无效 env=%q path=%q client_ip=%s",
			expectedEnv, c.Request.RequestURI, c.ClientIP(),
		))
		c.String(http.StatusNotFound, "unknown env")
		return
	}

	bodyBytes, err := common.ReadAllLimit(c.Request.Body, common.GetAnonymousRequestBodyLimitBytes())
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Waffo Pancake webhook 读取请求体失败 path=%q client_ip=%s error=%q", c.Request.RequestURI, c.ClientIP(), err.Error()))
		c.String(http.StatusBadRequest, "bad request")
		return
	}

	signature := c.GetHeader("X-Waffo-Signature")
	logger.LogInfo(c.Request.Context(), fmt.Sprintf("Waffo Pancake webhook 收到请求 path=%q client_ip=%s body_bytes=%d", c.Request.RequestURI, c.ClientIP(), len(bodyBytes)))

	event, err := service.VerifyConfiguredWaffoPancakeWebhook(string(bodyBytes), signature)
	if err != nil {
		logger.LogWarn(c.Request.Context(), fmt.Sprintf("Waffo Pancake webhook 验签失败 path=%q client_ip=%s error=%q", c.Request.RequestURI, c.ClientIP(), err.Error()))
		c.String(http.StatusUnauthorized, "invalid signature")
		return
	}

	if !strings.EqualFold(strings.TrimSpace(event.Mode), expectedEnv) {
		logger.LogError(c.Request.Context(), fmt.Sprintf(
			"Waffo Pancake webhook 环境不匹配 expected=%q actual_mode=%q event_id=%s order_id=%s client_ip=%s",
			expectedEnv, event.Mode, event.ID, event.Data.OrderID, c.ClientIP(),
		))
		c.String(http.StatusOK, "OK")
		return
	}
	if err := service.ValidateWaffoPancakeWebhookEvent(event); err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf(
			"Waffo Pancake webhook 状态字段不一致 event_type=%s event_id=%s order_id=%s client_ip=%s error=%q",
			event.EventType, event.ID, event.Data.OrderID, c.ClientIP(), err.Error(),
		))
		// The signature is valid, but the provider payload is permanently
		// contradictory. A retry would deliver the same event, so acknowledge it
		// without mutating local payment state.
		c.String(http.StatusOK, "OK")
		return
	}

	eventType := event.NormalizedEventType()
	logger.LogInfo(c.Request.Context(), fmt.Sprintf("Waffo Pancake webhook 验签成功 event_type=%s event_id=%s order_id=%s client_ip=%s", eventType, event.ID, event.Data.OrderID, c.ClientIP()))
	action := service.WaffoPancakeWebhookActionForEvent(eventType)
	switch action {
	case service.WaffoPancakeWebhookActionRefundSucceeded, service.WaffoPancakeWebhookActionRefundFailed:
		if err := handleWaffoPancakeRefundEvent(c, event); err != nil {
			logger.LogError(c.Request.Context(), fmt.Sprintf("Waffo Pancake 退款事件处理失败 event_type=%s event_id=%s order_id=%s client_ip=%s error=%q", eventType, event.ID, event.Data.OrderID, c.ClientIP(), err.Error()))
			c.String(http.StatusInternalServerError, "retry")
			return
		}
		c.String(http.StatusOK, "OK")
		return
	case service.WaffoPancakeWebhookActionIgnore:
		c.String(http.StatusOK, "OK")
		return
	}

	// Dispatch by trade_no prefix. OrderMerchantExternalID = our trade_no;
	// OrderID is Pancake's internal ORD_* (logs only).
	rawTradeNo := strings.TrimSpace(event.Data.OrderMerchantExternalID)
	isSubscription := strings.HasPrefix(rawTradeNo, "WAFFO_PANCAKE_SUB-")
	if (action == service.WaffoPancakeWebhookActionSubscriptionStateChanged ||
		action == service.WaffoPancakeWebhookActionSubscriptionPaymentSucceeded) && !isSubscription {
		// A recurring-provider event must never be allowed to settle a wallet
		// top-up. The local subscription order prefix is the explicit type
		// boundary; acknowledge a misrouted signed event without mutating state.
		logger.LogWarn(c.Request.Context(), fmt.Sprintf("Waffo Pancake 订阅事件与本地订单类型不匹配 event_type=%s event_id=%s trade_no=%s client_ip=%s", eventType, event.ID, rawTradeNo, c.ClientIP()))
		c.String(http.StatusOK, "OK")
		return
	}

	if isSubscription {
		tradeNo, err := service.ResolveWaffoPancakeSubscriptionTradeNo(event)
		if err != nil {
			logger.LogError(c.Request.Context(), fmt.Sprintf(
				"Waffo Pancake webhook 订阅订单解析失败 event_id=%s order_id=%s buyer_identity=%q client_ip=%s error=%q",
				event.ID, event.Data.OrderID, event.Data.MerchantProvidedBuyerIdentity, c.ClientIP(), err.Error(),
			))
			c.String(http.StatusOK, "OK")
			return
		}
		order := model.GetSubscriptionOrderByTradeNo(tradeNo)
		if order == nil {
			logger.LogError(c.Request.Context(), fmt.Sprintf(
				"Waffo Pancake webhook 订阅订单消失 trade_no=%s event_id=%s order_id=%s client_ip=%s",
				tradeNo, event.ID, event.Data.OrderID, c.ClientIP(),
			))
			c.String(http.StatusOK, "OK")
			return
		}

		productType := waffoPancakeSubscriptionOrderProductType(order)
		if action == service.WaffoPancakeWebhookActionSubscriptionStateChanged &&
			productType != model.WaffoPancakeProductTypeSubscription {
			logger.LogWarn(c.Request.Context(), fmt.Sprintf("Waffo Pancake recurring state event does not match one-time plan product trade_no=%s event_type=%s event_id=%s", tradeNo, eventType, event.ID))
			c.String(http.StatusOK, "OK")
			return
		}

		if action == service.WaffoPancakeWebhookActionSubscriptionStateChanged {
			periodStart, periodEnd, periodErr := waffoPancakeSubscriptionPeriod(event, false)
			if periodErr != nil {
				logger.LogError(c.Request.Context(), fmt.Sprintf("Waffo Pancake 订阅周期无效 trade_no=%s event_id=%s error=%q", tradeNo, event.ID, periodErr.Error()))
				c.String(http.StatusOK, "OK")
				return
			}
			canceledAt, canceledErr := parseWaffoPancakeTimestamp(event.Data.CanceledAt, "canceledAt", false)
			if canceledErr != nil {
				logger.LogError(c.Request.Context(), fmt.Sprintf("Waffo Pancake 订阅取消时间无效 trade_no=%s event_id=%s error=%q", tradeNo, event.ID, canceledErr.Error()))
				c.String(http.StatusOK, "OK")
				return
			}
			providerState := strings.TrimPrefix(eventType, "subscription.")
			LockOrder(tradeNo)
			stateErr := model.UpdateSubscriptionProviderState(
				tradeNo,
				model.PaymentProviderWaffoPancake,
				strings.TrimSpace(event.Data.OrderID),
				providerState,
				periodStart,
				periodEnd,
				canceledAt,
			)
			UnlockOrder(tradeNo)
			if stateErr != nil {
				logger.LogError(c.Request.Context(), fmt.Sprintf("Waffo Pancake 订阅状态同步失败 trade_no=%s event_type=%s event_id=%s error=%q", tradeNo, eventType, event.ID, stateErr.Error()))
				c.String(http.StatusInternalServerError, "retry")
				return
			}
			c.String(http.StatusOK, "OK")
			return
		}

		if action != service.WaffoPancakeWebhookActionSubscriptionPaymentSucceeded &&
			action != service.WaffoPancakeWebhookActionOrderCompleted {
			c.String(http.StatusOK, "OK")
			return
		}
		if !waffoPancakeSubscriptionOrderAcceptsSettlementAction(order, action) {
			logger.LogWarn(c.Request.Context(), fmt.Sprintf("Waffo Pancake settlement event does not match plan product type trade_no=%s event_type=%s event_id=%s product_type=%s", tradeNo, eventType, event.ID, productType))
			c.String(http.StatusOK, "OK")
			return
		}
		if action == service.WaffoPancakeWebhookActionSubscriptionPaymentSucceeded &&
			(order.ExpectedAmountMicros <= 0 || strings.TrimSpace(order.SettlementCurrency) == "") {
			logger.LogError(c.Request.Context(), fmt.Sprintf("Waffo Pancake recurring order has no immutable settlement snapshot trade_no=%s event_id=%s", tradeNo, event.ID))
			c.String(http.StatusOK, "OK")
			return
		}

		plan, planErr := model.GetSubscriptionPlanById(order.PlanId)
		if planErr != nil {
			logger.LogError(c.Request.Context(), fmt.Sprintf(
				"Waffo Pancake webhook 订阅套餐读取失败 trade_no=%s plan_id=%d event_id=%s client_ip=%s error=%q",
				tradeNo, order.PlanId, event.ID, c.ClientIP(), planErr.Error(),
			))
			c.String(http.StatusInternalServerError, "retry")
			return
		}
		if validationErr := validateWaffoPancakeSubscriptionEvent(event, order, plan); validationErr != nil {
			logger.LogError(c.Request.Context(), fmt.Sprintf(
				"Waffo Pancake webhook 订阅证据不匹配 trade_no=%s plan_id=%d event_id=%s client_ip=%s error=%q",
				tradeNo, order.PlanId, event.ID, c.ClientIP(), validationErr.Error(),
			))
			// The event is signed but permanently belongs to a different local
			// order/product. Acknowledge it without granting the subscription;
			// retrying cannot repair a provider/local evidence mismatch.
			c.String(http.StatusOK, "OK")
			return
		}

		var paymentEvent *model.SubscriptionPaymentEvent
		if action == service.WaffoPancakeWebhookActionSubscriptionPaymentSucceeded {
			periodStart, periodEnd, periodErr := waffoPancakeSubscriptionPeriod(event, true)
			if periodErr != nil {
				logger.LogError(c.Request.Context(), fmt.Sprintf("Waffo Pancake 订阅支付周期无效 trade_no=%s event_id=%s error=%q", tradeNo, event.ID, periodErr.Error()))
				c.String(http.StatusOK, "OK")
				return
			}
			providerEventID := strings.TrimSpace(event.EventID)
			if providerEventID == "" {
				providerEventID = strings.TrimSpace(event.ID)
			}
			providerTransactionID := strings.TrimSpace(event.Data.PaymentID)
			settlementAmountMicros, amountErr := monetaryStringToMicros(event.Data.Amount)
			if providerEventID == "" || providerTransactionID == "" || amountErr != nil {
				logger.LogError(c.Request.Context(), fmt.Sprintf("Waffo Pancake 订阅支付缺少幂等结算证据 trade_no=%s event_id=%s payment_id=%s error=%v", tradeNo, providerEventID, providerTransactionID, amountErr))
				c.String(http.StatusOK, "OK")
				return
			}
			paymentEvent = &model.SubscriptionPaymentEvent{
				PaymentProvider:        model.PaymentProviderWaffoPancake,
				ProviderEventId:        providerEventID,
				ProviderTransactionId:  providerTransactionID,
				SettlementCurrency:     strings.ToUpper(strings.TrimSpace(event.Data.Currency)),
				SettlementAmountMicros: settlementAmountMicros,
				PeriodStart:            periodStart,
				PeriodEnd:              periodEnd,
			}
		}
		LockOrder(tradeNo)
		defer UnlockOrder(tradeNo)
		if err := model.CompleteSubscriptionOrder(tradeNo, string(bodyBytes), model.PaymentProviderWaffoPancake, ""); err != nil {
			logger.LogError(c.Request.Context(), fmt.Sprintf("Waffo Pancake 订阅完成失败 trade_no=%s event_id=%s order_id=%s client_ip=%s error=%q", tradeNo, event.ID, event.Data.OrderID, c.ClientIP(), err.Error()))
			c.String(http.StatusInternalServerError, "retry")
			return
		}
		if paymentEvent != nil {
			if err := model.ApplySubscriptionPaymentEvent(
				tradeNo,
				paymentEvent,
				strings.TrimSpace(event.Data.OrderID),
				"active",
			); err != nil {
				logger.LogError(c.Request.Context(), fmt.Sprintf("Waffo Pancake 订阅支付周期应用失败 trade_no=%s event_id=%s order_id=%s client_ip=%s error=%q", tradeNo, event.ID, event.Data.OrderID, c.ClientIP(), err.Error()))
				c.String(http.StatusInternalServerError, "retry")
				return
			}
		}
		logger.LogInfo(c.Request.Context(), fmt.Sprintf("Waffo Pancake 订阅支付已应用 trade_no=%s event_id=%s order_id=%s client_ip=%s", tradeNo, event.ID, event.Data.OrderID, c.ClientIP()))
		c.String(http.StatusOK, "OK")
		return
	}

	tradeNo, err := service.ResolveWaffoPancakeTradeNo(event)
	if err != nil {
		// LogError (not LogWarn): covers order-not-found and buyer-identity
		// mismatch — both warrant human attention. 200 OK so Waffo doesn't
		// retry a permanently-unresolvable webhook.
		logger.LogError(c.Request.Context(), fmt.Sprintf(
			"Waffo Pancake webhook 订单解析失败 event_id=%s order_id=%s buyer_identity=%q client_ip=%s error=%q",
			event.ID, event.Data.OrderID, event.Data.MerchantProvidedBuyerIdentity, c.ClientIP(), err.Error(),
		))
		c.String(http.StatusOK, "OK")
		return
	}
	topUp := model.GetTopUpByTradeNo(tradeNo)
	if err := validateWaffoPancakeTopUpEvent(event, topUp); err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf(
			"Waffo Pancake webhook 充值订单证据不匹配 trade_no=%s event_id=%s order_id=%s client_ip=%s error=%q",
			tradeNo, event.ID, event.Data.OrderID, c.ClientIP(), err.Error(),
		))
		// The event is signed but permanently belongs to a different product or
		// store. A retry cannot repair that mismatch; acknowledge without credit.
		c.String(http.StatusOK, "OK")
		return
	}
	LockOrder(tradeNo)
	defer UnlockOrder(tradeNo)
	// Re-read after taking the order lock. Two identical provider callbacks can
	// otherwise both observe the old pending snapshot and create duplicate logs.
	topUp = model.GetTopUpByTradeNo(tradeNo)
	if topUp == nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Waffo Pancake 充值订单在加锁后消失 trade_no=%s event_id=%s", tradeNo, event.ID))
		c.String(http.StatusInternalServerError, "retry")
		return
	}
	wasPending := topUp.Status == common.TopUpStatusPending

	settledAmountMicros, err := monetaryStringToMicros(event.Data.Amount)
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Waffo Pancake 回调金额无效 trade_no=%s event_id=%s order_id=%s client_ip=%s error=%q", tradeNo, event.ID, event.Data.OrderID, c.ClientIP(), err.Error()))
		c.String(http.StatusInternalServerError, "retry")
		return
	}
	providerEventId := event.ID
	if providerEventId == "" {
		providerEventId = event.EventID
	}
	completed, err := model.CompleteExternalTopUp(model.ExternalTopUpSettlement{
		TradeNo:               tradeNo,
		PaymentProvider:       model.PaymentProviderWaffoPancake,
		PaymentMethod:         model.PaymentMethodWaffoPancake,
		SettlementCurrency:    event.Data.Currency,
		SettledAmountMicros:   settledAmountMicros,
		ProviderEventId:       providerEventId,
		ProviderTransactionId: event.Data.OrderID,
		ProviderStoreId:       event.StoreID,
	})
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Waffo Pancake 充值处理失败 trade_no=%s event_id=%s order_id=%s client_ip=%s error=%q", tradeNo, event.ID, event.Data.OrderID, c.ClientIP(), err.Error()))
		c.String(http.StatusInternalServerError, "retry")
		return
	}

	if wasPending {
		model.RecordTopupLog(
			completed.UserId,
			fmt.Sprintf("Waffo Pancake充值成功，充值额度: %v，支付金额: %.2f", logger.FormatQuota(int(completed.CreditedQuota)), completed.Money),
			c.ClientIP(),
			completed.PaymentMethod,
			model.PaymentMethodWaffoPancake,
		)
	}
	logger.LogInfo(c.Request.Context(), fmt.Sprintf("Waffo Pancake 充值成功 trade_no=%s user_id=%d quota=%d event_id=%s order_id=%s client_ip=%s", tradeNo, completed.UserId, completed.CreditedQuota, event.ID, event.Data.OrderID, c.ClientIP()))
	c.String(http.StatusOK, "OK")
}

// validateWaffoPancakeTopUpEvent binds newly-created wallet checkouts to the
// configured store and product. Waffo echoes order metadata in signed webhook
// payloads, so a valid event for another product must not settle this order.
// Older orders may not have metadata; those remain compatible, while any
// metadata that is present is always checked.
func validateWaffoPancakeTopUpEvent(event *service.WaffoPancakeWebhookEvent, topUp *model.TopUp) error {
	if event == nil || topUp == nil {
		return fmt.Errorf("missing top-up settlement evidence")
	}
	if expectedStore := strings.TrimSpace(topUp.ProviderStoreId); expectedStore != "" &&
		strings.TrimSpace(event.StoreID) != expectedStore {
		return fmt.Errorf("top-up store mismatch: expected=%q actual=%q", expectedStore, strings.TrimSpace(event.StoreID))
	}
	actualProduct, present := event.Data.OrderMetadata[service.WaffoPancakeOrderMetadataProductID]
	if present {
		expectedProduct := strings.TrimSpace(topUp.ProviderProductId)
		actualProduct = strings.TrimSpace(actualProduct)
		// A metadata key is signed evidence from a newly-created checkout. An
		// empty value is not equivalent to a legacy payload with no key: reject
		// it when the local order has a product binding, rather than silently
		// accepting an unbound event.
		if expectedProduct != "" && actualProduct != expectedProduct {
			return fmt.Errorf("top-up product metadata mismatch: expected=%q actual=%q", expectedProduct, actualProduct)
		}
	}
	return nil
}

func waffoPancakeSubscriptionOrderProductType(order *model.SubscriptionOrder) string {
	if order == nil {
		return model.WaffoPancakeProductTypeSubscription
	}
	var snapshot struct {
		ProductType string `json:"waffo_pancake_product_type"`
	}
	if err := json.Unmarshal([]byte(order.PlanSnapshot), &snapshot); err != nil {
		return model.WaffoPancakeProductTypeSubscription
	}
	return model.NormalizeWaffoPancakeProductType(snapshot.ProductType)
}

func waffoPancakeSubscriptionOrderAcceptsSettlementAction(order *model.SubscriptionOrder, action service.WaffoPancakeWebhookAction) bool {
	productType := waffoPancakeSubscriptionOrderProductType(order)
	switch action {
	case service.WaffoPancakeWebhookActionOrderCompleted:
		// Historical one-time orders predate immutable amount snapshots.
		return order != nil && (order.ExpectedAmountMicros <= 0 || productType == model.WaffoPancakeProductTypeOneTime)
	case service.WaffoPancakeWebhookActionSubscriptionPaymentSucceeded:
		return productType == model.WaffoPancakeProductTypeSubscription
	default:
		return false
	}
}

// validateWaffoPancakeSubscriptionEvent keeps a signed provider callback
// bound to the local subscription order. Signature verification authenticates
// Waffo, but it does not prove that the event belongs to this plan or amount.
// Product metadata is written into new checkout sessions and echoed by Waffo;
// requiring it prevents a valid event for another product in the same merchant
// from activating this local order.
func parseWaffoPancakeTimestamp(value, field string, required bool) (int64, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		if required {
			return 0, fmt.Errorf("missing subscription %s", field)
		}
		return 0, nil
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return 0, fmt.Errorf("invalid subscription %s: %w", field, err)
	}
	return parsed.Unix(), nil
}

func waffoPancakeSubscriptionPeriod(event *service.WaffoPancakeWebhookEvent, required bool) (int64, int64, error) {
	if event == nil {
		return 0, 0, fmt.Errorf("missing subscription event")
	}
	start, err := parseWaffoPancakeTimestamp(event.Data.CurrentPeriodStart, "currentPeriodStart", required)
	if err != nil {
		return 0, 0, err
	}
	end, err := parseWaffoPancakeTimestamp(event.Data.CurrentPeriodEnd, "currentPeriodEnd", required)
	if err != nil {
		return 0, 0, err
	}
	if required && end <= start {
		return 0, 0, fmt.Errorf("invalid subscription billing period")
	}
	return start, end, nil
}

func validateWaffoPancakeSubscriptionEvent(event *service.WaffoPancakeWebhookEvent, order *model.SubscriptionOrder, plan *model.SubscriptionPlan) error {
	if event == nil || order == nil || plan == nil {
		return fmt.Errorf("missing subscription settlement evidence")
	}
	// The plan price is real ISO fiat; Pancake's provider settlement is USD.
	expectedCurrency := strings.ToUpper(strings.TrimSpace(order.SettlementCurrency))
	if expectedCurrency == "" {
		expectedCurrency = "USD"
	}
	actualCurrency := strings.ToUpper(strings.TrimSpace(event.Data.Currency))
	if actualCurrency == "" || actualCurrency != expectedCurrency {
		return fmt.Errorf("subscription currency mismatch: expected=%q actual=%q", expectedCurrency, actualCurrency)
	}

	expectedAmount := order.ExpectedAmountMicros
	if expectedAmount <= 0 {
		var err error
		expectedAmount, _, err = subscriptionSettlementSnapshot(plan, expectedCurrency)
		if err != nil {
			return fmt.Errorf("invalid local subscription settlement: %w", err)
		}
	}
	actualAmount, err := monetaryStringToMicros(event.Data.Amount)
	if err != nil {
		return fmt.Errorf("invalid provider subscription amount: %w", err)
	}
	if actualAmount != expectedAmount {
		return fmt.Errorf("subscription amount mismatch: expected_micros=%d actual_micros=%d", expectedAmount, actualAmount)
	}

	// The order snapshot is authoritative. Global/plan fields are legacy
	// fallback only for pending orders created before immutable bindings.
	expectedStore := strings.TrimSpace(order.ProviderStoreId)
	if expectedStore == "" {
		expectedStore = strings.TrimSpace(setting.WaffoPancakeStoreID)
	}
	if expectedStore != "" && strings.TrimSpace(event.StoreID) != expectedStore {
		return fmt.Errorf("subscription store mismatch: expected=%q actual=%q", expectedStore, strings.TrimSpace(event.StoreID))
	}
	expectedProduct := strings.TrimSpace(order.ProviderProductId)
	if expectedProduct == "" {
		expectedProduct = strings.TrimSpace(plan.WaffoPancakeProductId)
	}
	actualProduct := strings.TrimSpace(event.Data.OrderMetadata[service.WaffoPancakeOrderMetadataProductID])
	if expectedProduct == "" || actualProduct == "" || actualProduct != expectedProduct {
		return fmt.Errorf("subscription product metadata mismatch: expected=%q actual=%q", expectedProduct, actualProduct)
	}
	if expectedPlan := strconv.Itoa(plan.Id); strings.TrimSpace(event.Data.OrderMetadata[service.WaffoPancakeOrderMetadataPlanID]) != expectedPlan {
		return fmt.Errorf("subscription plan metadata mismatch: expected=%q actual=%q", expectedPlan, strings.TrimSpace(event.Data.OrderMetadata[service.WaffoPancakeOrderMetadataPlanID]))
	}
	return nil
}

// handleWaffoPancakeRefundEvent records a signed refund notification and
// reverses the value granted by the original order. The model transaction
// makes the ledger row, cumulative refund amount, and wallet/subscription
// adjustment one idempotent operation.
func handleWaffoPancakeRefundEvent(c *gin.Context, event *service.WaffoPancakeWebhookEvent) error {
	if event == nil {
		return fmt.Errorf("missing refund event")
	}
	if err := service.ValidateWaffoPancakeWebhookEvent(event); err != nil {
		return err
	}
	rawTradeNo := strings.TrimSpace(event.Data.OrderMerchantExternalID)
	isSubscription := strings.HasPrefix(rawTradeNo, "WAFFO_PANCAKE_SUB-")
	var err error
	var tradeNo string
	var userID int
	var status string
	var subscriptionOrder *model.SubscriptionOrder
	if isSubscription {
		tradeNo, err = service.ResolveWaffoPancakeRefundSubscriptionTradeNo(event)
		if err != nil {
			return fmt.Errorf("resolve refund subscription: %w", err)
		}
		order := model.GetSubscriptionOrderByTradeNo(tradeNo)
		if order == nil {
			return fmt.Errorf("refund subscription order disappeared trade_no=%s", tradeNo)
		}
		subscriptionOrder = order
		if err := validateWaffoPancakeSubscriptionRefundEvent(event, order); err != nil {
			return err
		}
		subscriptionOrder = order
		userID, status = order.UserId, order.Status
	} else {
		tradeNo, err = service.ResolveWaffoPancakeRefundTradeNo(event)
		if err != nil {
			return fmt.Errorf("resolve refund order: %w", err)
		}
		topUp := model.GetTopUpByTradeNo(tradeNo)
		if topUp == nil {
			return fmt.Errorf("refund order disappeared trade_no=%s", tradeNo)
		}
		userID, status = topUp.UserId, topUp.Status
	}
	if status != common.TopUpStatusSuccess {
		return fmt.Errorf("refund order is not settled trade_no=%s status=%s", tradeNo, status)
	}

	action := service.WaffoPancakeWebhookActionForEvent(event.NormalizedEventType())
	if action == service.WaffoPancakeWebhookActionRefundFailed {
		providerEventID := waffoPancakeRefundEventID(event)
		if providerEventID == "" {
			return fmt.Errorf("refund event has no stable id")
		}
		claimed, err := model.ClaimWaffoPancakeWebhookEvent(
			model.PaymentProviderWaffoPancake,
			providerEventID,
			event.NormalizedEventType(),
		)
		if err != nil {
			return fmt.Errorf("claim refund event: %w", err)
		}
		if !claimed {
			return nil
		}
		reason := strings.TrimSpace(event.Data.RefundReason)
		if len([]rune(reason)) > 200 {
			reason = string([]rune(reason)[:200])
		}
		logger.LogWarn(c.Request.Context(), fmt.Sprintf("Waffo Pancake 退款失败 trade_no=%s user_id=%d refund_id=%s reason=%q", tradeNo, userID, event.Data.RefundTicketMerchantExternalID, reason))
		model.RecordLog(userID, model.LogTypeRefund, fmt.Sprintf("Waffo Pancake refund.failed trade_no=%s refund_id=%s reason=%s", tradeNo, event.Data.RefundTicketMerchantExternalID, reason))
		return nil
	}

	amountMicros, err := monetaryStringToMicros(event.Data.Amount)
	if err != nil || amountMicros <= 0 {
		if err == nil {
			err = fmt.Errorf("refund amount must be positive")
		}
		return fmt.Errorf("invalid refund amount: %w", err)
	}
	providerEventID := waffoPancakeRefundEventID(event)
	if providerEventID == "" {
		return fmt.Errorf("refund event has no stable id")
	}
	if subscriptionOrder != nil {
		latestPayment, latestErr := model.GetLatestSubscriptionPaymentEvent(subscriptionOrder.Id)
		switch {
		case latestErr == nil:
			// Pancake's refund schema does not guarantee paymentId. When it is
			// supplied, enforce that it targets the current paid period; otherwise
			// preserve the provider's order-level refund compatibility.
			refundPaymentID := strings.TrimSpace(event.Data.PaymentID)
			if refundPaymentID != "" && refundPaymentID != strings.TrimSpace(latestPayment.ProviderTransactionId) {
				return fmt.Errorf("subscription refund does not bind to the current paid period")
			}
		case !errors.Is(latestErr, gorm.ErrRecordNotFound):
			return fmt.Errorf("load subscription payment event: %w", latestErr)
		}
	}
	refundResult, err := model.ApplyWaffoPancakeRefund(
		tradeNo,
		isSubscription,
		amountMicros,
		strings.ToUpper(strings.TrimSpace(event.Data.Currency)),
		providerEventID,
		model.PaymentMethodWaffoPancake,
		model.PaymentProviderWaffoPancake,
		fmt.Sprintf("Waffo Pancake refund.succeeded trade_no=%s order_id=%s refund_id=%s", tradeNo, event.Data.OrderID, event.Data.RefundTicketMerchantExternalID),
		userID,
	)
	if err != nil {
		return err
	}
	if !refundResult.Created {
		return nil
	}
	model.RecordLog(userID, model.LogTypeRefund, fmt.Sprintf("Waffo Pancake refund.succeeded trade_no=%s refund_id=%s amount=%s %s", tradeNo, event.Data.RefundTicketMerchantExternalID, event.Data.Amount, strings.ToUpper(strings.TrimSpace(event.Data.Currency))))
	logger.LogInfo(c.Request.Context(), fmt.Sprintf("Waffo Pancake 退款已记账 trade_no=%s user_id=%d amount_micros=%d quota_debited=%d refund_id=%s", tradeNo, userID, amountMicros, refundResult.QuotaDebited, event.Data.RefundTicketMerchantExternalID))
	return nil
}

// validateWaffoPancakeSubscriptionRefundEvent keeps a signed refund bound to
// the same merchant store, currency, and product as the local subscription
// order. Refund events carry the original order metadata, but they are not
// themselves sufficient evidence that the refund belongs to this plan: the
// external trade number is the lookup key and must be supplemented with the
// immutable checkout facts before recording finance data.
func validateWaffoPancakeSubscriptionRefundEvent(event *service.WaffoPancakeWebhookEvent, order *model.SubscriptionOrder) error {
	if event == nil || order == nil {
		return fmt.Errorf("missing subscription refund settlement evidence")
	}
	actualStore := strings.TrimSpace(event.StoreID)
	actualProduct, productMetadataPresent := event.Data.OrderMetadata[service.WaffoPancakeOrderMetadataProductID]
	actualPlan, planMetadataPresent := event.Data.OrderMetadata[service.WaffoPancakeOrderMetadataPlanID]
	metadataPresent := productMetadataPresent || planMetadataPresent
	// Older refund payloads (and orders created before checkout metadata was
	// introduced) do not carry StoreID or OrderMetadata. Keep those payloads
	// processable, but never ignore a contradictory value when the provider
	// does send one. New payloads with either binding field are validated below.
	if expectedStore := strings.TrimSpace(setting.WaffoPancakeStoreID); expectedStore != "" &&
		(actualStore != "" || metadataPresent) && actualStore != expectedStore {
		return fmt.Errorf("subscription refund store mismatch: expected=%q actual=%q", expectedStore, strings.TrimSpace(event.StoreID))
	}
	plan, err := model.GetSubscriptionPlanById(order.PlanId)
	if err != nil || plan == nil {
		if metadataPresent && err != nil {
			return fmt.Errorf("subscription refund plan could not be loaded: %w", err)
		}
		if metadataPresent {
			return fmt.Errorf("subscription refund plan could not be loaded")
		}
		// The plan may have been removed after a legacy order was settled. The
		// trade number and (when supplied) buyer identity were already bound by
		// ResolveWaffoPancakeRefundSubscriptionTradeNo, so retain compatibility.
		return nil
	}
	expectedCurrency := strings.ToUpper(strings.TrimSpace(plan.Currency))
	if expectedCurrency == "" {
		expectedCurrency = "USD"
	}
	actualCurrency := strings.ToUpper(strings.TrimSpace(event.Data.Currency))
	if actualCurrency != "" && actualCurrency != expectedCurrency {
		return fmt.Errorf("subscription refund currency mismatch: expected=%q actual=%q", expectedCurrency, actualCurrency)
	}
	if !metadataPresent {
		return nil
	}
	if actualCurrency == "" {
		return fmt.Errorf("subscription refund currency mismatch: expected=%q actual=%q", expectedCurrency, actualCurrency)
	}
	expectedProduct := strings.TrimSpace(plan.WaffoPancakeProductId)
	if expectedProduct == "" {
		return fmt.Errorf("subscription refund product is not configured")
	}
	actualProduct = strings.TrimSpace(actualProduct)
	if !productMetadataPresent || actualProduct != expectedProduct {
		return fmt.Errorf("subscription refund product metadata mismatch: expected=%q actual=%q", expectedProduct, actualProduct)
	}
	actualPlan = strings.TrimSpace(actualPlan)
	if expectedPlan := strconv.Itoa(plan.Id); !planMetadataPresent || actualPlan != expectedPlan {
		return fmt.Errorf("subscription refund plan metadata mismatch: expected=%q actual=%q", expectedPlan, actualPlan)
	}
	return nil
}

func waffoPancakeRefundEventID(event *service.WaffoPancakeWebhookEvent) string {
	if event == nil {
		return ""
	}
	if id := strings.TrimSpace(event.ID); id != "" {
		return id
	}
	if id := strings.TrimSpace(event.EventID); id != "" {
		return id
	}
	return strings.TrimSpace(event.Data.RefundTicketMerchantExternalID)
}
