package controller

import (
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
	waffo "github.com/waffo-com/waffo-go"
	"github.com/waffo-com/waffo-go/config"
	"github.com/waffo-com/waffo-go/core"
	"github.com/waffo-com/waffo-go/types/order"
)

func getWaffoSDK() (*waffo.Waffo, error) {
	env := config.Sandbox
	apiKey := setting.WaffoSandboxApiKey
	privateKey := setting.WaffoSandboxPrivateKey
	publicKey := setting.WaffoSandboxPublicCert
	if !setting.WaffoSandbox {
		env = config.Production
		apiKey = setting.WaffoApiKey
		privateKey = setting.WaffoPrivateKey
		publicKey = setting.WaffoPublicCert
	}
	builder := config.NewConfigBuilder().
		APIKey(apiKey).
		PrivateKey(privateKey).
		WaffoPublicKey(publicKey).
		Environment(env)
	if setting.WaffoMerchantId != "" {
		builder = builder.MerchantID(setting.WaffoMerchantId)
	}
	cfg, err := builder.Build()
	if err != nil {
		return nil, err
	}
	return waffo.New(cfg), nil
}

func getWaffoUserEmail(user *model.User) string {
	return fmt.Sprintf("%d@examples.com", user.Id)
}

func getWaffoCurrency() string {
	// The Waffo integration is quoted in real USD. A configurable label here
	// previously allowed a USD number to be submitted as CNY, the same class of
	// unit-confusion bug as Epay. Keep the legacy option for storage compatibility
	// but never let it change the settlement contract.
	return "USD"
}

func waffoWebhookReceiptLog(path, clientIP string, bodyBytes int) string {
	return fmt.Sprintf("Waffo webhook 收到请求 path=%q client_ip=%s body_bytes=%d", path, clientIP, bodyBytes)
}

func buildWaffoTopUpGoodsInfo(amount decimal.Decimal) *order.GoodsInfo {
	appName := strings.TrimSpace(common.SystemName)
	if appName == "" {
		appName = "LMM API"
	}
	return &order.GoodsInfo{
		GoodsName: fmt.Sprintf("Recharge %s platform units", amount.String()),
		AppName:   appName,
	}
}

// zeroDecimalCurrencies 零小数位币种，金额不能带小数点
var zeroDecimalCurrencies = map[string]bool{
	"IDR": true, "JPY": true, "KRW": true, "VND": true,
}

func formatWaffoAmount(amount float64, currency string) string {
	if zeroDecimalCurrencies[currency] {
		return fmt.Sprintf("%.0f", amount)
	}
	return fmt.Sprintf("%.2f", amount)
}

// getWaffoPayMoney converts platform units to real USD through the global
// recharge contract. WaffoUnitPrice is intentionally not involved: provider
// settings select a gateway, while FX and the base recharge ratio live in one
// authoritative pricing configuration.
func getWaffoPayMoney(amount float64, group string) float64 {
	return getWaffoPayMoneyDecimal(amount, group).InexactFloat64()
}

func getWaffoPayMoneyDecimal(amount float64, group string) decimal.Decimal {
	return getWaffoPayMoneyForAmount(decimal.NewFromFloat(amount), group)
}

func getWaffoPayMoneyForAmount(amount decimal.Decimal, group string) decimal.Decimal {
	pricing, err := standardSettlementPricing("USD")
	if err != nil {
		return decimal.Zero
	}
	quoted, err := quoteTopUpDecimalWithSettlementPricing(
		amount,
		group,
		pricing,
		decimal.NewFromInt(1),
	)
	if err != nil {
		return decimal.Zero
	}
	return quoted
}

type WaffoPayRequest struct {
	Amount         float64 `json:"amount"`
	DiscountCode   string  `json:"discount_code,omitempty"`
	PayMethodIndex *int    `json:"pay_method_index"` // 服务端支付方式列表的索引，nil 表示由 Waffo 自动选择
	PayMethodType  string  `json:"pay_method_type"`  // Deprecated: 兼容旧前端，优先使用 pay_method_index
	PayMethodName  string  `json:"pay_method_name"`  // Deprecated: 兼容旧前端，优先使用 pay_method_index
}

func RequestWaffoAmount(c *gin.Context) {
	var req WaffoPayRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "参数错误"})
		return
	}
	requestedAmount, err := parseRequestedTopUpAmount(req.Amount)
	if err != nil {
		common.ApiErrorMsg(c, err.Error())
		return
	}
	if !requirePaymentMethodAvailable(c, model.PaymentMethodWaffo) {
		return
	}

	waffoMinTopup := int64(setting.WaffoMinTopUp)
	if requestedAmount.LessThan(decimal.NewFromInt(waffoMinTopup)) {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": fmt.Sprintf("充值数量不能小于 %d", waffoMinTopup)})
		return
	}
	if !requirePaymentMethodTopUpDecimalWithinLimit(c, model.PaymentMethodWaffo, requestedAmount) {
		return
	}

	id := c.GetInt("id")
	_, _, creditedQuota, err := topUpOrderAmountsDecimal(requestedAmount)
	if err != nil || !requireTopUpCreditCapacity(c, id, creditedQuota) {
		return
	}
	group, err := model.GetUserGroup(id, true)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "获取用户分组失败"})
		return
	}

	payMoneyDecimal, _, err := applyDiscountCodeQuoteDecimal(getWaffoPayMoneyForAmount(requestedAmount, group), requestedAmount, req.DiscountCode, id)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "优惠码无效"})
		return
	}
	payMoney := payMoneyDecimal.InexactFloat64()
	if payMoney <= 0.01 {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "充值金额过低"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "success", "data": strconv.FormatFloat(payMoney, 'f', 2, 64)})
}

// RequestWaffoPay 创建 Waffo 支付订单
func RequestWaffoPay(c *gin.Context) {
	if !setting.WaffoEnabled {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "Waffo 支付未启用"})
		return
	}

	var req WaffoPayRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "参数错误"})
		return
	}
	requestedAmount, err := parseRequestedTopUpAmount(req.Amount)
	if err != nil {
		common.ApiErrorMsg(c, err.Error())
		return
	}
	if !requirePaymentMethodAvailable(c, model.PaymentMethodWaffo) {
		return
	}
	waffoMinTopup := int64(setting.WaffoMinTopUp)
	if requestedAmount.LessThan(decimal.NewFromInt(waffoMinTopup)) {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": fmt.Sprintf("充值数量不能小于 %d", waffoMinTopup)})
		return
	}
	if !requirePaymentMethodTopUpDecimalWithinLimit(c, model.PaymentMethodWaffo, requestedAmount) {
		return
	}

	id := c.GetInt("id")
	amount, platformAmountMicros, creditedQuota, err := topUpOrderAmountsDecimal(requestedAmount)
	if err != nil || !requireTopUpCreditCapacity(c, id, creditedQuota) {
		return
	}

	user, err := model.GetUserById(id, false)
	if err != nil || user == nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "用户不存在"})
		return
	}

	// 从服务端配置查找支付方式，客户端只传索引或旧字段
	var resolvedPayMethodType, resolvedPayMethodName string
	methods := setting.GetWaffoPayMethods()
	if req.PayMethodIndex != nil {
		// 新协议：按索引查找
		idx := *req.PayMethodIndex
		if idx < 0 || idx >= len(methods) {
			logger.LogWarn(c.Request.Context(), fmt.Sprintf("Waffo 支付方式索引无效 user_id=%d pay_method_index=%d method_count=%d", id, idx, len(methods)))
			c.JSON(http.StatusOK, gin.H{"message": "error", "data": "不支持的支付方式"})
			return
		}
		resolvedPayMethodType = methods[idx].PayMethodType
		resolvedPayMethodName = methods[idx].PayMethodName
	} else if req.PayMethodType != "" {
		// 兼容旧前端：验证客户端传的值在服务端列表中
		valid := false
		for _, m := range methods {
			if m.PayMethodType == req.PayMethodType && m.PayMethodName == req.PayMethodName {
				valid = true
				resolvedPayMethodType = m.PayMethodType
				resolvedPayMethodName = m.PayMethodName
				break
			}
		}
		if !valid {
			logger.LogWarn(c.Request.Context(), fmt.Sprintf("Waffo 支付方式无效 user_id=%d pay_method_type=%s pay_method_name=%q", id, req.PayMethodType, req.PayMethodName))
			c.JSON(http.StatusOK, gin.H{"message": "error", "data": "不支持的支付方式"})
			return
		}
	}
	// resolvedPayMethodType/Name 为空时，Waffo 自动选择支付方式

	group, err := model.GetUserGroup(id, true)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "获取用户分组失败"})
		return
	}
	payMoneyDecimal, discountCode, err := applyDiscountCodeQuoteDecimal(getWaffoPayMoneyForAmount(requestedAmount, group), requestedAmount, req.DiscountCode, id)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "优惠码无效"})
		return
	}
	payMoney := payMoneyDecimal.InexactFloat64()
	if payMoney < 0.01 {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "充值金额过低"})
		return
	}

	// 生成唯一订单号，paymentRequestId 与 merchantOrderId 保持一致，简化追踪
	merchantOrderId := fmt.Sprintf("WAFFO-%d-%d-%s", id, time.Now().UnixMilli(), randstr.String(6))
	paymentRequestId := merchantOrderId

	currency := getWaffoCurrency()
	paymentAmount := formatWaffoAmount(payMoney, currency)
	expectedAmountMicros, err := monetaryStringToMicros(paymentAmount)
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Waffo 结算金额无效 user_id=%d trade_no=%s error=%q", id, merchantOrderId, err.Error()))
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "支付金额无效"})
		return
	}

	// 创建本地订单
	topUp := &model.TopUp{
		UserId:               id,
		Amount:               amount,
		PlatformAmountMicros: platformAmountMicros,
		CreditedQuota:        creditedQuota,
		ExpectedAmountMicros: expectedAmountMicros,
		SettlementCurrency:   strings.ToUpper(currency),
		Money:                monetaryMicrosToFloat(expectedAmountMicros),
		TradeNo:              merchantOrderId,
		PaymentMethod:        model.PaymentMethodWaffo,
		PaymentProvider:      model.PaymentProviderWaffo,
		DiscountCodeId:       discountCodeID(discountCode),
		DiscountPercent:      discountPercent(discountCode),
		CreateTime:           time.Now().Unix(),
		Status:               common.TopUpStatusPending,
	}
	if err := topUp.Insert(); err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Waffo 创建充值订单失败 user_id=%d trade_no=%s amount=%s error=%q", id, merchantOrderId, requestedAmount.String(), err.Error()))
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "创建订单失败"})
		return
	}

	sdk, err := getWaffoSDK()
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Waffo SDK 初始化失败 user_id=%d trade_no=%s error=%q", id, merchantOrderId, err.Error()))
		topUp.Status = common.TopUpStatusFailed
		_ = topUp.Update()
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "支付配置错误"})
		return
	}

	callbackAddr := service.GetCallbackAddress()
	notifyUrl := callbackAddr + "/api/waffo/webhook"
	if setting.WaffoNotifyUrl != "" {
		notifyUrl = setting.WaffoNotifyUrl
	}
	returnUrl := paymentReturnPath("/wallet?show_history=true")
	if setting.WaffoReturnUrl != "" {
		returnUrl = setting.WaffoReturnUrl
	}

	goodsInfo := buildWaffoTopUpGoodsInfo(requestedAmount)
	createParams := &order.CreateOrderParams{
		PaymentRequestID: paymentRequestId,
		MerchantOrderID:  merchantOrderId,
		OrderAmount:      paymentAmount,
		OrderCurrency:    currency,
		OrderDescription: goodsInfo.GoodsName,
		OrderRequestedAt: time.Now().UTC().Format("2006-01-02T15:04:05.000Z"),
		NotifyURL:        notifyUrl,
		MerchantInfo: &order.MerchantInfo{
			MerchantID: setting.WaffoMerchantId,
		},
		UserInfo: &order.UserInfo{
			UserID:       strconv.Itoa(user.Id),
			UserEmail:    getWaffoUserEmail(user),
			UserTerminal: "WEB",
		},
		PaymentInfo: &order.PaymentInfo{
			ProductName:   "ONE_TIME_PAYMENT",
			PayMethodType: resolvedPayMethodType,
			PayMethodName: resolvedPayMethodName,
		},
		GoodsInfo:          goodsInfo,
		SuccessRedirectURL: returnUrl,
		FailedRedirectURL:  returnUrl,
	}
	resp, err := sdk.Order().Create(c.Request.Context(), createParams, nil)
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Waffo 创建订单失败 user_id=%d trade_no=%s error=%q", id, merchantOrderId, err.Error()))
		// A transport failure is ambiguous: Waffo may have created the order.
		// Preserve pending so a later signed callback can still settle it.
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "拉起支付失败"})
		return
	}
	if !resp.IsSuccess() {
		logger.LogWarn(c.Request.Context(), fmt.Sprintf("Waffo 创建订单业务失败 user_id=%d trade_no=%s code=%s message=%q response=%q", id, merchantOrderId, resp.Code, resp.Message, common.GetJsonString(resp)))
		topUp.Status = common.TopUpStatusFailed
		_ = topUp.Update()
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "拉起支付失败"})
		return
	}

	orderData := resp.GetData()
	logger.LogInfo(c.Request.Context(), fmt.Sprintf("Waffo 充值订单创建成功 user_id=%d trade_no=%s amount=%s money=%.2f pay_method_type=%s pay_method_name=%q", id, merchantOrderId, requestedAmount.String(), payMoney, resolvedPayMethodType, resolvedPayMethodName))

	paymentUrl := orderData.FetchRedirectURL()
	if paymentUrl == "" {
		paymentUrl = orderData.OrderAction
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "success",
		"data": gin.H{
			"payment_url": paymentUrl,
			"order_id":    merchantOrderId,
		},
	})
}

// webhookPayloadWithSubInfo 扩展 PAYMENT_NOTIFICATION，包含 SDK 未定义的 subscriptionInfo 字段
type webhookPayloadWithSubInfo struct {
	EventType string `json:"eventType"`
	Result    struct {
		core.PaymentNotificationResult
		SubscriptionInfo *webhookSubscriptionInfo `json:"subscriptionInfo,omitempty"`
	} `json:"result"`
}

type webhookSubscriptionInfo struct {
	Period              string `json:"period,omitempty"`
	MerchantRequest     string `json:"merchantRequest,omitempty"`
	SubscriptionID      string `json:"subscriptionId,omitempty"`
	SubscriptionRequest string `json:"subscriptionRequest,omitempty"`
}

// WaffoWebhook 处理 Waffo 回调通知（支付/退款/订阅）
func WaffoWebhook(c *gin.Context) {
	if !isWaffoWebhookEnabled() {
		logger.LogWarn(c.Request.Context(), fmt.Sprintf("Waffo webhook 被拒绝 reason=webhook_disabled path=%q client_ip=%s", c.Request.RequestURI, c.ClientIP()))
		c.AbortWithStatus(http.StatusForbidden)
		return
	}

	bodyBytes, err := common.ReadAllLimit(c.Request.Body, common.GetAnonymousRequestBodyLimitBytes())
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Waffo webhook 读取请求体失败 path=%q client_ip=%s error=%q", c.Request.RequestURI, c.ClientIP(), err.Error()))
		c.AbortWithStatus(http.StatusBadRequest)
		return
	}

	sdk, err := getWaffoSDK()
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Waffo webhook SDK 初始化失败 path=%q client_ip=%s error=%q", c.Request.RequestURI, c.ClientIP(), err.Error()))
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}

	wh := sdk.Webhook()
	signature := c.GetHeader("X-SIGNATURE")
	// Never write the signed payload or signature to logs. The callback can
	// contain buyer/payment data, and retaining the signature creates an
	// unnecessary replay capability for anyone who can read application logs.
	logger.LogInfo(c.Request.Context(), waffoWebhookReceiptLog(c.Request.RequestURI, c.ClientIP(), len(bodyBytes)))

	// 验证请求签名
	if !wh.VerifySignature(string(bodyBytes), signature) {
		logger.LogWarn(c.Request.Context(), fmt.Sprintf("Waffo webhook 验签失败 path=%q client_ip=%s", c.Request.RequestURI, c.ClientIP()))
		c.AbortWithStatus(http.StatusBadRequest)
		return
	}

	var event core.WebhookEvent
	if err := common.Unmarshal(bodyBytes, &event); err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Waffo webhook 解析失败 path=%q client_ip=%s error=%q", c.Request.RequestURI, c.ClientIP(), err.Error()))
		sendWaffoWebhookResponse(c, wh, false, "invalid payload")
		return
	}

	switch event.EventType {
	case core.EventPayment:
		// 解析为扩展类型，区分普通支付和订阅支付
		var payload webhookPayloadWithSubInfo
		if err := common.Unmarshal(bodyBytes, &payload); err != nil {
			logger.LogError(c.Request.Context(), fmt.Sprintf("Waffo 支付回调载荷解析失败 event_type=%s client_ip=%s error=%q", event.EventType, c.ClientIP(), err.Error()))
			sendWaffoWebhookResponse(c, wh, false, "invalid payment payload")
			return
		}
		logger.LogInfo(c.Request.Context(), fmt.Sprintf("Waffo webhook 验签并解析成功 event_type=%s merchant_order_id=%s order_status=%s client_ip=%s", event.EventType, payload.Result.MerchantOrderID, payload.Result.OrderStatus, c.ClientIP()))
		handleWaffoPayment(c, wh, &payload.Result.PaymentNotificationResult)
	case core.EventRefund:
		var payload core.RefundNotification
		if err := common.Unmarshal(bodyBytes, &payload); err != nil {
			logger.LogError(c.Request.Context(), fmt.Sprintf("Waffo 退款回调载荷解析失败 event_type=%s client_ip=%s error=%q", event.EventType, c.ClientIP(), err.Error()))
			sendWaffoWebhookResponse(c, wh, false, "invalid refund payload")
			return
		}
		handleWaffoRefund(c, wh, payload.Result)
	default:
		logger.LogInfo(c.Request.Context(), fmt.Sprintf("Waffo webhook 忽略事件 event_type=%s client_ip=%s", event.EventType, c.ClientIP()))
		sendWaffoWebhookResponse(c, wh, true, "")
	}
}

func waffoRefundEventID(result *core.RefundNotificationResult) string {
	if result == nil {
		return ""
	}
	for _, value := range []string{
		result.AcquiringRefundOrderID,
		result.RefundRequestID,
		result.MerchantRefundOrderID,
	} {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

// handleWaffoRefund applies only final, signed refund notifications. The
// original payment request ID is the local trade number because checkout
// creation sets paymentRequestId and merchantOrderId to the same value.
func handleWaffoRefund(c *gin.Context, wh *core.WebhookHandler, result *core.RefundNotificationResult) {
	if result == nil {
		logger.LogWarn(c.Request.Context(), fmt.Sprintf("Waffo 退款回调缺少结果 client_ip=%s", c.ClientIP()))
		sendWaffoWebhookResponse(c, wh, false, "missing refund result")
		return
	}

	status := strings.TrimSpace(result.RefundStatus)
	tradeNo := strings.TrimSpace(result.OrigPaymentRequestID)
	if status != core.RefundStatusPartiallyRefunded && status != core.RefundStatusFullyRefunded {
		logger.LogInfo(c.Request.Context(), fmt.Sprintf("Waffo 退款未成功，忽略资金回冲 trade_no=%s refund_status=%s client_ip=%s", tradeNo, status, c.ClientIP()))
		sendWaffoWebhookResponse(c, wh, true, "")
		return
	}
	if tradeNo == "" {
		logger.LogWarn(c.Request.Context(), fmt.Sprintf("Waffo 成功退款缺少原支付单号 refund_id=%s client_ip=%s", waffoRefundEventID(result), c.ClientIP()))
		sendWaffoWebhookResponse(c, wh, false, "missing original payment id")
		return
	}
	refundEventID := waffoRefundEventID(result)
	if refundEventID == "" {
		logger.LogWarn(c.Request.Context(), fmt.Sprintf("Waffo 成功退款缺少稳定退款标识 trade_no=%s client_ip=%s", tradeNo, c.ClientIP()))
		sendWaffoWebhookResponse(c, wh, false, "missing refund id")
		return
	}
	amountMicros, err := monetaryStringToMicros(result.RefundAmount)
	if err != nil || amountMicros <= 0 {
		logger.LogWarn(c.Request.Context(), fmt.Sprintf("Waffo 退款金额无效 trade_no=%s refund_id=%s client_ip=%s", tradeNo, refundEventID, c.ClientIP()))
		sendWaffoWebhookResponse(c, wh, false, "invalid refund amount")
		return
	}

	// Bind the event to an already-settled local Waffo top-up before using any
	// refund fields. Currency is read from the original local settlement, not
	// from the callback's user-facing display field.
	topUp := model.GetTopUpByTradeNo(tradeNo)
	if topUp == nil || topUp.PaymentProvider != model.PaymentProviderWaffo || topUp.Status != common.TopUpStatusSuccess {
		logger.LogWarn(c.Request.Context(), fmt.Sprintf("Waffo 退款原订单不匹配 trade_no=%s refund_id=%s client_ip=%s", tradeNo, refundEventID, c.ClientIP()))
		sendWaffoWebhookResponse(c, wh, false, "refund order mismatch")
		return
	}
	currency := strings.TrimSpace(topUp.SettlementCurrency)
	if currency == "" {
		logger.LogWarn(c.Request.Context(), fmt.Sprintf("Waffo 退款原订单缺少结算币种 trade_no=%s refund_id=%s client_ip=%s", tradeNo, refundEventID, c.ClientIP()))
		sendWaffoWebhookResponse(c, wh, false, "missing settlement currency")
		return
	}
	refundResult, err := model.ApplyPaymentRefund(
		tradeNo,
		false,
		amountMicros,
		currency,
		refundEventID,
		model.PaymentMethodWaffo,
		model.PaymentProviderWaffo,
		fmt.Sprintf("Waffo refund.succeeded trade_no=%s refund_id=%s", tradeNo, refundEventID),
		topUp.UserId,
	)
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Waffo 退款处理失败 trade_no=%s refund_id=%s client_ip=%s error=%q", tradeNo, refundEventID, c.ClientIP(), err.Error()))
		sendWaffoWebhookResponse(c, wh, false, "refund processing failed")
		return
	}
	if refundResult.Created {
		model.RecordLog(topUp.UserId, model.LogTypeRefund, fmt.Sprintf("Waffo refund.succeeded trade_no=%s refund_id=%s amount=%s", tradeNo, refundEventID, result.RefundAmount))
	}
	logger.LogInfo(c.Request.Context(), fmt.Sprintf("Waffo 退款已记账 trade_no=%s user_id=%d refund_id=%s amount_micros=%d quota_debited=%d", tradeNo, topUp.UserId, refundEventID, amountMicros, refundResult.QuotaDebited))
	sendWaffoWebhookResponse(c, wh, true, "")
}

// handleWaffoPayment 处理支付完成通知
func handleWaffoPayment(c *gin.Context, wh *core.WebhookHandler, result *core.PaymentNotificationResult) {
	if result.OrderStatus != "PAY_SUCCESS" {
		logger.LogInfo(c.Request.Context(), fmt.Sprintf("Waffo 订单状态非成功，忽略充值 trade_no=%s order_status=%s client_ip=%s", result.MerchantOrderID, result.OrderStatus, c.ClientIP()))
		terminalFailure := map[string]bool{
			"PAY_FAIL":  true,
			"CLOSED":    true,
			"CANCELLED": true,
			"EXPIRED":   true,
		}
		if result.MerchantOrderID != "" && terminalFailure[strings.ToUpper(strings.TrimSpace(result.OrderStatus))] {
			if err := model.UpdatePendingTopUpStatus(result.MerchantOrderID, model.PaymentProviderWaffo, common.TopUpStatusFailed); err != nil &&
				!errors.Is(err, model.ErrTopUpNotFound) &&
				!errors.Is(err, model.ErrTopUpStatusInvalid) {
				logger.LogError(c.Request.Context(), fmt.Sprintf("Waffo 标记失败订单状态失败 trade_no=%s error=%q", result.MerchantOrderID, err.Error()))
			}
		}
		sendWaffoWebhookResponse(c, wh, true, "")
		return
	}

	merchantOrderId := result.MerchantOrderID
	if merchantOrderId == "" || (result.PaymentRequestID != "" && result.PaymentRequestID != merchantOrderId) {
		logger.LogWarn(c.Request.Context(), fmt.Sprintf("Waffo 回调订单标识不匹配 trade_no=%s payment_request_id=%s client_ip=%s", merchantOrderId, result.PaymentRequestID, c.ClientIP()))
		sendWaffoWebhookResponse(c, wh, false, "invalid order identity")
		return
	}

	LockOrder(merchantOrderId)
	defer UnlockOrder(merchantOrderId)

	settledAmountMicros, err := monetaryStringToMicros(result.OrderAmount)
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Waffo 回调金额无效 trade_no=%s client_ip=%s error=%q", merchantOrderId, c.ClientIP(), err.Error()))
		sendWaffoWebhookResponse(c, wh, false, "invalid settlement amount")
		return
	}
	providerTransactionId := result.AcquiringOrderID
	if providerTransactionId == "" {
		providerTransactionId = result.PaymentRequestID
	}
	completed, err := model.CompleteExternalTopUp(model.ExternalTopUpSettlement{
		TradeNo:               merchantOrderId,
		PaymentProvider:       model.PaymentProviderWaffo,
		PaymentMethod:         model.PaymentMethodWaffo,
		SettlementCurrency:    result.OrderCurrency,
		SettledAmountMicros:   settledAmountMicros,
		ProviderTransactionId: providerTransactionId,
	})
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Waffo 充值处理失败 trade_no=%s client_ip=%s error=%q", merchantOrderId, c.ClientIP(), err.Error()))
		sendWaffoWebhookResponse(c, wh, false, err.Error())
		return
	}

	logger.LogInfo(c.Request.Context(), fmt.Sprintf("Waffo 充值成功 trade_no=%s user_id=%d quota=%d client_ip=%s", merchantOrderId, completed.UserId, completed.CreditedQuota, c.ClientIP()))
	sendWaffoWebhookResponse(c, wh, true, "")
}

// sendWaffoWebhookResponse 发送签名响应
func sendWaffoWebhookResponse(c *gin.Context, wh *core.WebhookHandler, success bool, msg string) {
	var body, sig string
	if success {
		body, sig = wh.BuildSuccessResponse()
	} else {
		body, sig = wh.BuildFailedResponse(msg)
	}
	c.Header("X-SIGNATURE", sig)
	c.Data(http.StatusOK, "application/json", []byte(body))
}
