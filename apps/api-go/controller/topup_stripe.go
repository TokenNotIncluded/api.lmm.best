package controller

import (
	"context"
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
	"github.com/LIghtJUNction/api.lmm.best/setting"
	"github.com/LIghtJUNction/api.lmm.best/setting/operation_setting"

	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"github.com/stripe/stripe-go/v81"
	"github.com/stripe/stripe-go/v81/checkout/session"
	stripeprice "github.com/stripe/stripe-go/v81/price"
	"github.com/stripe/stripe-go/v81/webhook"
	"github.com/thanhpk/randstr"
)

var stripeAdaptor = &StripeAdaptor{}

// StripePayRequest represents a payment request for Stripe checkout.
type StripePayRequest struct {
	// Amount is the quantity of units to purchase.
	Amount float64 `json:"amount"`
	// PaymentMethod specifies the payment method (e.g., "stripe").
	PaymentMethod string `json:"payment_method"`
	DiscountCode  string `json:"discount_code,omitempty"`
	// SuccessURL is the optional custom URL to redirect after successful payment.
	// If empty, defaults to the server's console log page.
	SuccessURL string `json:"success_url,omitempty"`
	// CancelURL is the optional custom URL to redirect when payment is canceled.
	// If empty, defaults to the server's console topup page.
	CancelURL string `json:"cancel_url,omitempty"`
}

type StripeAdaptor struct {
}

func (*StripeAdaptor) RequestAmount(c *gin.Context, req *StripePayRequest) {
	requestedAmount, err := parseRequestedTopUpAmount(req.Amount)
	if err != nil {
		common.ApiErrorMsg(c, err.Error())
		return
	}
	if !requirePaymentMethodAvailable(c, model.PaymentMethodStripe) {
		return
	}
	if requestedAmount.LessThan(decimal.NewFromInt(getStripeMinTopup())) {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": fmt.Sprintf("充值数量不能小于 %d", getStripeMinTopup())})
		return
	}
	if !requirePaymentMethodTopUpDecimalWithinLimit(c, model.PaymentMethodStripe, requestedAmount) {
		return
	}
	if requestedAmount.GreaterThan(decimal.NewFromInt(10000)) {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "充值数量不能大于 10000"})
		return
	}
	id := c.GetInt("id")
	group, err := model.GetUserGroup(id, true)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "获取用户分组失败"})
		return
	}
	_, _, creditedQuota, err := topUpOrderAmountsDecimal(requestedAmount)
	if err != nil || !requireTopUpCreditCapacity(c, id, creditedQuota) {
		return
	}
	payMoney, _, err := applyDiscountCodeQuoteDecimal(getStripePayMoneyDecimal(requestedAmount, group), requestedAmount, req.DiscountCode, id)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "优惠码无效"})
		return
	}
	expectedAmountMicros, err := monetaryStringToMicros(payMoney.StringFixed(2))
	if err != nil || expectedAmountMicros <= 10_000 {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "充值金额过低"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "success", "data": strconv.FormatFloat(monetaryMicrosToFloat(expectedAmountMicros), 'f', 2, 64)})
}

func (*StripeAdaptor) RequestPay(c *gin.Context, req *StripePayRequest) {
	requestedAmount, err := parseRequestedTopUpAmount(req.Amount)
	if err != nil {
		common.ApiErrorMsg(c, err.Error())
		return
	}
	if req.PaymentMethod != model.PaymentMethodStripe {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "不支持的支付渠道"})
		return
	}
	if !requirePaymentMethodAvailable(c, model.PaymentMethodStripe) {
		return
	}
	if requestedAmount.LessThan(decimal.NewFromInt(getStripeMinTopup())) {
		c.JSON(http.StatusOK, gin.H{"message": fmt.Sprintf("充值数量不能小于 %d", getStripeMinTopup()), "data": 10})
		return
	}
	if !requirePaymentMethodTopUpDecimalWithinLimit(c, model.PaymentMethodStripe, requestedAmount) {
		return
	}
	if requestedAmount.GreaterThan(decimal.NewFromInt(10000)) {
		c.JSON(http.StatusOK, gin.H{"message": "充值数量不能大于 10000", "data": 10})
		return
	}

	if req.SuccessURL != "" && common.ValidateRedirectURL(req.SuccessURL) != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": "支付成功重定向URL不在可信任域名列表中", "data": ""})
		return
	}

	if req.CancelURL != "" && common.ValidateRedirectURL(req.CancelURL) != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": "支付取消重定向URL不在可信任域名列表中", "data": ""})
		return
	}

	id := c.GetInt("id")
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
	payMoney, discountCode, err := applyDiscountCodeQuoteDecimal(getStripePayMoneyDecimal(requestedAmount, group), requestedAmount, req.DiscountCode, id)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "优惠码无效"})
		return
	}
	expectedAmountMicros, err := monetaryStringToMicros(payMoney.StringFixed(2))
	if err != nil || expectedAmountMicros <= 10_000 {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "充值金额过低"})
		return
	}
	amount, platformAmountMicros, creditedQuota, err := topUpOrderAmountsDecimal(requestedAmount)
	if err != nil || !requireTopUpCreditCapacity(c, id, creditedQuota) {
		return
	}
	reference := fmt.Sprintf("new-api-ref-%d-%d-%s", user.Id, time.Now().UnixMilli(), randstr.String(4))
	referenceId := "ref_" + common.Sha1([]byte(reference))

	configuredPrice, err := getStripeTopUpPrice()
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Stripe 读取充值价格失败 user_id=%d trade_no=%s amount=%s error=%q", id, referenceId, requestedAmount.String(), err.Error()))
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "拉起支付失败"})
		return
	}
	_, settlementCurrency, err := stripeTopUpLineItem(configuredPrice, expectedAmountMicros)
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Stripe 充值价格配置无效 user_id=%d trade_no=%s amount=%s error=%q", id, referenceId, requestedAmount.String(), err.Error()))
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "支付金额无效"})
		return
	}
	topUp := &model.TopUp{
		UserId:               id,
		Amount:               amount,
		PlatformAmountMicros: platformAmountMicros,
		CreditedQuota:        creditedQuota,
		ExpectedAmountMicros: expectedAmountMicros,
		SettlementCurrency:   settlementCurrency,
		Money:                monetaryMicrosToFloat(expectedAmountMicros),
		TradeNo:              referenceId,
		PaymentMethod:        model.PaymentMethodStripe,
		PaymentProvider:      model.PaymentProviderStripe,
		DiscountCodeId:       discountCodeID(discountCode),
		DiscountPercent:      discountPercent(discountCode),
		CreateTime:           time.Now().Unix(),
		Status:               common.TopUpStatusPending,
	}
	err = topUp.Insert()
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Stripe 创建充值订单失败 user_id=%d trade_no=%s amount=%s error=%q", id, referenceId, requestedAmount.String(), err.Error()))
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "创建订单失败"})
		return
	}
	payLink, _, err := genStripeLinkWithPrice(referenceId, user.StripeCustomer, user.Email, expectedAmountMicros, req.SuccessURL, req.CancelURL, configuredPrice)
	if err != nil {
		// Keep the durable order pending: a transport error can happen after
		// Stripe accepted the session request, in which case the webhook still
		// needs a local order to settle the payment safely.
		logger.LogError(c.Request.Context(), fmt.Sprintf("Stripe 创建 Checkout Session 失败 user_id=%d trade_no=%s amount=%s error=%q", id, referenceId, requestedAmount.String(), err.Error()))
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "拉起支付失败"})
		return
	}
	logger.LogInfo(c.Request.Context(), fmt.Sprintf("Stripe 充值订单创建成功 user_id=%d trade_no=%s amount=%s money=%.2f currency=%s", id, referenceId, requestedAmount.String(), topUp.Money, settlementCurrency))
	c.JSON(http.StatusOK, gin.H{
		"message": "success",
		"data": gin.H{
			"pay_link": payLink,
		},
	})
}

func RequestStripeAmount(c *gin.Context) {
	var req StripePayRequest
	err := c.ShouldBindJSON(&req)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "参数错误"})
		return
	}
	stripeAdaptor.RequestAmount(c, &req)
}

func RequestStripePay(c *gin.Context) {
	var req StripePayRequest
	err := c.ShouldBindJSON(&req)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "参数错误"})
		return
	}
	stripeAdaptor.RequestPay(c, &req)
}

func stripeWebhookReceiptLog(path, clientIP string, bodyBytes int) string {
	return fmt.Sprintf("Stripe webhook 收到请求 path=%q client_ip=%s body_bytes=%d", path, clientIP, bodyBytes)
}

func StripeWebhook(c *gin.Context) {
	ctx := c.Request.Context()
	if !isStripeWebhookEnabled() {
		logger.LogWarn(ctx, fmt.Sprintf("Stripe webhook 被拒绝 reason=webhook_disabled path=%q client_ip=%s", c.Request.RequestURI, c.ClientIP()))
		c.AbortWithStatus(http.StatusForbidden)
		return
	}

	payload, err := common.ReadAllLimit(c.Request.Body, common.GetAnonymousRequestBodyLimitBytes())
	if err != nil {
		logger.LogError(ctx, fmt.Sprintf("Stripe webhook 读取请求体失败 path=%q client_ip=%s error=%q", c.Request.RequestURI, c.ClientIP(), err.Error()))
		c.AbortWithStatus(http.StatusServiceUnavailable)
		return
	}

	signature := c.GetHeader("Stripe-Signature")
	logger.LogInfo(ctx, stripeWebhookReceiptLog(c.Request.RequestURI, c.ClientIP(), len(payload)))
	event, err := webhook.ConstructEventWithOptions(payload, signature, setting.StripeWebhookSecret, webhook.ConstructEventOptions{
		IgnoreAPIVersionMismatch: true,
	})

	if err != nil {
		logger.LogWarn(ctx, fmt.Sprintf("Stripe webhook 验签失败 path=%q client_ip=%s error=%q", c.Request.RequestURI, c.ClientIP(), err.Error()))
		c.AbortWithStatus(http.StatusBadRequest)
		return
	}

	callerIp := c.ClientIP()
	logger.LogInfo(ctx, fmt.Sprintf("Stripe webhook 验签成功 event_type=%s client_ip=%s path=%q", string(event.Type), callerIp, c.Request.RequestURI))
	var processErr error
	switch event.Type {
	case stripe.EventTypeCheckoutSessionCompleted:
		processErr = sessionCompleted(ctx, event, callerIp)
	case stripe.EventTypeCheckoutSessionExpired:
		processErr = sessionExpired(ctx, event)
	case stripe.EventTypeCheckoutSessionAsyncPaymentSucceeded:
		processErr = sessionAsyncPaymentSucceeded(ctx, event, callerIp)
	case stripe.EventTypeCheckoutSessionAsyncPaymentFailed:
		processErr = sessionAsyncPaymentFailed(ctx, event, callerIp)
	case stripe.EventTypeInvoicePaid, stripe.EventTypeInvoicePaymentSucceeded:
		processErr = stripeSubscriptionInvoicePaid(ctx, event, callerIp)
	case stripe.EventTypeInvoicePaymentFailed:
		processErr = stripeSubscriptionInvoiceFailed(ctx, event, callerIp)
	case stripe.EventTypeCustomerSubscriptionUpdated, stripe.EventTypeCustomerSubscriptionDeleted:
		processErr = stripeSubscriptionStateChanged(ctx, event, callerIp)
	case stripe.EventTypeRefundCreated, stripe.EventTypeRefundUpdated:
		processErr = stripeTopUpRefundSucceeded(ctx, event, callerIp)
	default:
		logger.LogInfo(ctx, fmt.Sprintf("Stripe webhook 忽略事件 event_type=%s client_ip=%s", string(event.Type), callerIp))
	}
	if processErr != nil {
		if stripeWebhookRetryable(processErr) {
			// A signed event that could not be persisted must be retried by Stripe.
			// Returning 200 here used to acknowledge a paid checkout even when a
			// transient database failure left the local order unsettled.
			logger.LogError(ctx, fmt.Sprintf("Stripe webhook 本地处理失败，要求重试 event_type=%s event_id=%s client_ip=%s error=%q", string(event.Type), event.ID, callerIp, processErr.Error()))
			c.AbortWithStatus(http.StatusInternalServerError)
			return
		}
		// Provider evidence conflicts and invalid local states require manual
		// reconciliation. Retrying the same signed payload cannot repair them.
		logger.LogWarn(ctx, fmt.Sprintf("Stripe webhook 本地状态冲突，停止重试 event_type=%s event_id=%s client_ip=%s error=%q", string(event.Type), event.ID, callerIp, processErr.Error()))
	}

	c.Status(http.StatusOK)
}

func stripeWebhookRetryable(err error) bool {
	return !errors.Is(err, model.ErrSubscriptionOrderStatusInvalid) &&
		!errors.Is(err, model.ErrTopUpNotFound) &&
		!errors.Is(err, model.ErrTopUpStatusInvalid) &&
		!errors.Is(err, model.ErrPaymentEvidenceConflict) &&
		!errors.Is(err, model.ErrPaymentMethodMismatch) &&
		!errors.Is(err, model.ErrRefundAmountInvalid)
}

// stripeTopUpRefundSucceeded handles only one-time wallet top-ups. It binds a
// signed Refund object's payment intent to the immutable transaction evidence
// recorded when the original checkout settled. Subscription refunds require a
// separate durable transaction binding and are deliberately not inferred here.
func stripeTopUpRefundSucceeded(ctx context.Context, event stripe.Event, callerIP string) error {
	if event.Data == nil || len(event.Data.Raw) == 0 {
		return fmt.Errorf("%w: Stripe refund event has no object", model.ErrPaymentEvidenceConflict)
	}
	var refund stripe.Refund
	if err := common.Unmarshal(event.Data.Raw, &refund); err != nil {
		return fmt.Errorf("%w: decode Stripe refund: %v", model.ErrPaymentEvidenceConflict, err)
	}
	if refund.Status != stripe.RefundStatusSucceeded {
		logger.LogInfo(ctx, fmt.Sprintf("Stripe 退款未成功，忽略资金回冲 event_type=%s refund_id=%s refund_status=%s client_ip=%s", event.Type, refund.ID, refund.Status, callerIP))
		return nil
	}
	if refund.PaymentIntent == nil || strings.TrimSpace(refund.PaymentIntent.ID) == "" || strings.TrimSpace(refund.ID) == "" {
		return fmt.Errorf("%w: Stripe successful refund lacks payment intent or refund id", model.ErrPaymentEvidenceConflict)
	}
	paymentIntentID := strings.TrimSpace(refund.PaymentIntent.ID)
	topUp, err := model.GetTopUpByProviderTransaction(model.PaymentProviderStripe, paymentIntentID)
	if err != nil {
		return err
	}
	if topUp.Status != common.TopUpStatusSuccess {
		return model.ErrTopUpStatusInvalid
	}
	currency := strings.ToUpper(strings.TrimSpace(topUp.SettlementCurrency))
	refundCurrency := strings.ToUpper(strings.TrimSpace(string(refund.Currency)))
	if currency == "" || refundCurrency == "" || currency != refundCurrency {
		return fmt.Errorf("%w: Stripe refund currency does not match original settlement", model.ErrPaymentEvidenceConflict)
	}
	amountMicros, err := minorCurrencyUnitsToMicros(refund.Amount, currency)
	if err != nil || amountMicros <= 0 {
		return fmt.Errorf("%w: invalid Stripe refund amount", model.ErrPaymentEvidenceConflict)
	}

	refundResult, err := model.ApplyPaymentRefund(
		topUp.TradeNo,
		false,
		amountMicros,
		currency,
		strings.TrimSpace(refund.ID),
		model.PaymentMethodStripe,
		model.PaymentProviderStripe,
		fmt.Sprintf("Stripe refund.succeeded trade_no=%s refund_id=%s", topUp.TradeNo, refund.ID),
		topUp.UserId,
	)
	if err != nil {
		return err
	}
	if refundResult.Created {
		model.RecordLog(topUp.UserId, model.LogTypeRefund, fmt.Sprintf("Stripe refund.succeeded trade_no=%s refund_id=%s amount=%d", topUp.TradeNo, refund.ID, refund.Amount))
	}
	logger.LogInfo(ctx, fmt.Sprintf("Stripe 退款已记账 trade_no=%s user_id=%d refund_id=%s amount_micros=%d quota_debited=%d client_ip=%s", topUp.TradeNo, topUp.UserId, refund.ID, amountMicros, refundResult.QuotaDebited, callerIP))
	return nil
}

func sessionCompleted(ctx context.Context, event stripe.Event, callerIp string) error {
	customerId := event.GetObjectValue("customer")
	referenceId := event.GetObjectValue("client_reference_id")
	status := event.GetObjectValue("status")
	if "complete" != status {
		logger.LogWarn(ctx, fmt.Sprintf("Stripe checkout.completed 状态异常，忽略处理 trade_no=%s status=%s client_ip=%s", referenceId, status, callerIp))
		return nil
	}

	paymentStatus := event.GetObjectValue("payment_status")
	if paymentStatus != "paid" {
		logger.LogInfo(ctx, fmt.Sprintf("Stripe Checkout 支付未完成，等待异步结果 trade_no=%s payment_status=%s client_ip=%s", referenceId, paymentStatus, callerIp))
		return nil
	}

	return fulfillOrder(ctx, event, referenceId, customerId, callerIp)
}

// sessionAsyncPaymentSucceeded handles delayed payment methods (bank transfer, SEPA, etc.)
// that confirm payment after the checkout session completes.
func sessionAsyncPaymentSucceeded(ctx context.Context, event stripe.Event, callerIp string) error {
	customerId := event.GetObjectValue("customer")
	referenceId := event.GetObjectValue("client_reference_id")
	logger.LogInfo(ctx, fmt.Sprintf("Stripe 异步支付成功 trade_no=%s client_ip=%s", referenceId, callerIp))

	return fulfillOrder(ctx, event, referenceId, customerId, callerIp)
}

// sessionAsyncPaymentFailed marks orders as failed when delayed payment methods
// ultimately fail (e.g. bank transfer not received, SEPA rejected).
func sessionAsyncPaymentFailed(ctx context.Context, event stripe.Event, callerIp string) error {
	referenceId := event.GetObjectValue("client_reference_id")
	logger.LogWarn(ctx, fmt.Sprintf("Stripe 异步支付失败 trade_no=%s client_ip=%s", referenceId, callerIp))

	if len(referenceId) == 0 {
		logger.LogWarn(ctx, fmt.Sprintf("Stripe 异步支付失败事件缺少订单号 client_ip=%s", callerIp))
		return nil
	}

	LockOrder(referenceId)
	defer UnlockOrder(referenceId)

	topUp := model.GetTopUpByTradeNo(referenceId)
	if topUp == nil {
		logger.LogWarn(ctx, fmt.Sprintf("Stripe 异步支付失败但本地订单不存在 trade_no=%s client_ip=%s", referenceId, callerIp))
		return nil
	}

	if topUp.PaymentProvider != model.PaymentProviderStripe {
		logger.LogWarn(ctx, fmt.Sprintf("Stripe 异步支付失败但订单支付网关不匹配 trade_no=%s payment_provider=%s client_ip=%s", referenceId, topUp.PaymentProvider, callerIp))
		return nil
	}

	if topUp.Status != common.TopUpStatusPending {
		logger.LogInfo(ctx, fmt.Sprintf("Stripe 异步支付失败但订单状态非 pending，忽略处理 trade_no=%s status=%s client_ip=%s", referenceId, topUp.Status, callerIp))
		return nil
	}

	topUp.Status = common.TopUpStatusFailed
	if err := topUp.Update(); err != nil {
		logger.LogError(ctx, fmt.Sprintf("Stripe 标记充值订单失败状态失败 trade_no=%s client_ip=%s error=%q", referenceId, callerIp, err.Error()))
		return fmt.Errorf("mark async payment failed: %w", err)
	}
	logger.LogInfo(ctx, fmt.Sprintf("Stripe 充值订单已标记为失败 trade_no=%s client_ip=%s", referenceId, callerIp))
	return nil
}

// fulfillOrder is the shared logic for crediting quota after payment is confirmed.
func fulfillOrder(ctx context.Context, event stripe.Event, referenceId string, customerId string, callerIp string) error {
	if len(referenceId) == 0 {
		logger.LogWarn(ctx, fmt.Sprintf("Stripe 完成订单时缺少订单号 client_ip=%s", callerIp))
		return nil
	}

	LockOrder(referenceId)
	defer UnlockOrder(referenceId)
	if subscriptionOrder := model.GetSubscriptionOrderByTradeNo(referenceId); subscriptionOrder != nil {
		var checkout stripe.CheckoutSession
		if err := json.Unmarshal(event.Data.Raw, &checkout); err != nil {
			return fmt.Errorf("decode Stripe subscription checkout: %w", err)
		}
		if err := validateStripeSubscriptionCheckoutEvidence(subscriptionOrder, &checkout); err != nil {
			// A signed but mismatched checkout cannot become valid on retry. Do
			// not grant access; keep the order pending for manual reconciliation.
			logger.LogError(ctx, fmt.Sprintf("Stripe 订阅结算证据不匹配 trade_no=%s event_id=%s error=%q", referenceId, event.ID, err.Error()))
			return nil
		}
		if err := model.CompleteSubscriptionOrder(referenceId, string(event.Data.Raw), model.PaymentProviderStripe, ""); err != nil {
			logger.LogError(ctx, fmt.Sprintf("Stripe 订阅订单处理失败 trade_no=%s event_type=%s client_ip=%s error=%q", referenceId, string(event.Type), callerIp, err.Error()))
			return fmt.Errorf("complete subscription order: %w", err)
		}
		if err := model.UpdateSubscriptionProviderState(
			referenceId,
			model.PaymentProviderStripe,
			checkout.Subscription.ID,
			"active",
			0,
			0,
			0,
		); err != nil {
			return fmt.Errorf("store Stripe subscription identity: %w", err)
		}
		logger.LogInfo(ctx, fmt.Sprintf("Stripe 订阅订单处理成功 trade_no=%s event_type=%s client_ip=%s", referenceId, string(event.Type), callerIp))
		return nil
	}

	amountTotal, parseErr := strconv.ParseInt(event.GetObjectValue("amount_total"), 10, 64)
	if parseErr != nil {
		logger.LogError(ctx, fmt.Sprintf("Stripe 充值回调金额无效 trade_no=%s event_type=%s client_ip=%s error=%q", referenceId, string(event.Type), callerIp, parseErr.Error()))
		return nil
	}
	currency := strings.ToUpper(event.GetObjectValue("currency"))
	settledAmountMicros, parseErr := minorCurrencyUnitsToMicros(amountTotal, currency)
	if parseErr != nil {
		logger.LogError(ctx, fmt.Sprintf("Stripe 充值回调结算金额无效 trade_no=%s event_type=%s client_ip=%s error=%q", referenceId, string(event.Type), callerIp, parseErr.Error()))
		return nil
	}
	amountSubtotal, parseErr := strconv.ParseInt(event.GetObjectValue("amount_subtotal"), 10, 64)
	if parseErr != nil {
		logger.LogError(ctx, fmt.Sprintf("Stripe 充值回调小计无效 trade_no=%s event_type=%s client_ip=%s error=%q", referenceId, string(event.Type), callerIp, parseErr.Error()))
		return nil
	}
	providerQuotedAmountMicros, parseErr := minorCurrencyUnitsToMicros(amountSubtotal, currency)
	if parseErr != nil {
		logger.LogError(ctx, fmt.Sprintf("Stripe 充值回调小计金额无效 trade_no=%s event_type=%s client_ip=%s error=%q", referenceId, string(event.Type), callerIp, parseErr.Error()))
		return nil
	}
	providerTransactionId := event.GetObjectValue("payment_intent")
	if providerTransactionId == "" {
		providerTransactionId = event.GetObjectValue("id")
	}
	completed, err := model.CompleteExternalTopUp(model.ExternalTopUpSettlement{
		TradeNo:                    referenceId,
		PaymentProvider:            model.PaymentProviderStripe,
		PaymentMethod:              model.PaymentMethodStripe,
		SettlementCurrency:         currency,
		SettledAmountMicros:        settledAmountMicros,
		ProviderQuotedAmountMicros: providerQuotedAmountMicros,
		ProviderEventId:            event.ID,
		ProviderTransactionId:      providerTransactionId,
		StripeCustomer:             customerId,
	})
	if err != nil {
		logger.LogError(ctx, fmt.Sprintf("Stripe 充值处理失败 trade_no=%s event_type=%s client_ip=%s error=%q", referenceId, string(event.Type), callerIp, err.Error()))
		return fmt.Errorf("complete external topup: %w", err)
	}

	model.RecordTopupLog(completed.UserId, fmt.Sprintf("使用在线充值成功，充值金额: %v，支付金额：%.2f", logger.FormatQuota(int(completed.CreditedQuota)), completed.Money), callerIp, completed.PaymentMethod, model.PaymentMethodStripe)
	logger.LogInfo(ctx, fmt.Sprintf("Stripe 充值成功 trade_no=%s amount_total=%d currency=%s event_type=%s client_ip=%s", referenceId, amountTotal, currency, string(event.Type), callerIp))
	return nil
}

func stripeInvoiceTradeNo(invoice *stripe.Invoice) string {
	if invoice == nil {
		return ""
	}
	if invoice.SubscriptionDetails != nil {
		if tradeNo := strings.TrimSpace(invoice.SubscriptionDetails.Metadata[subscriptionTradeNoMetadataKey]); tradeNo != "" {
			return tradeNo
		}
	}
	if invoice.Subscription != nil {
		return strings.TrimSpace(invoice.Subscription.Metadata[subscriptionTradeNoMetadataKey])
	}
	return ""
}

func stripeInvoicePeriod(invoice *stripe.Invoice, expectedPriceID string) (int64, int64, error) {
	if invoice == nil || invoice.Lines == nil {
		return 0, 0, fmt.Errorf("stripe invoice lines are missing")
	}
	for _, line := range invoice.Lines.Data {
		if line == nil || line.Period == nil || line.Price == nil {
			continue
		}
		if expectedPriceID != "" && strings.TrimSpace(line.Price.ID) != strings.TrimSpace(expectedPriceID) {
			continue
		}
		if line.Period.End <= line.Period.Start {
			return 0, 0, fmt.Errorf("stripe invoice period is invalid")
		}
		return line.Period.Start, line.Period.End, nil
	}
	return 0, 0, fmt.Errorf("stripe invoice does not contain the expected recurring price")
}

func validateStripeSubscriptionInvoiceEvidence(order *model.SubscriptionOrder, invoice *stripe.Invoice) (int64, int64, error) {
	if order == nil || invoice == nil {
		return 0, 0, fmt.Errorf("stripe subscription invoice evidence is required")
	}
	if order.PaymentProvider != model.PaymentProviderStripe {
		return 0, 0, fmt.Errorf("stripe subscription provider mismatch")
	}
	if invoice.Status != stripe.InvoiceStatusPaid {
		return 0, 0, fmt.Errorf("stripe subscription invoice is not paid")
	}
	currency := strings.ToUpper(string(invoice.Currency))
	if currency != strings.ToUpper(strings.TrimSpace(order.SettlementCurrency)) {
		return 0, 0, fmt.Errorf("stripe subscription invoice currency mismatch")
	}
	amountMicros, err := minorCurrencyUnitsToMicros(invoice.Total, currency)
	if err != nil || amountMicros != order.ExpectedAmountMicros {
		return 0, 0, fmt.Errorf("stripe subscription invoice amount mismatch")
	}
	return stripeInvoicePeriod(invoice, order.ProviderProductId)
}

func stripeSubscriptionInvoicePaid(ctx context.Context, event stripe.Event, callerIP string) error {
	var invoice stripe.Invoice
	if err := json.Unmarshal(event.Data.Raw, &invoice); err != nil {
		return fmt.Errorf("decode stripe subscription invoice: %w", err)
	}
	tradeNo := stripeInvoiceTradeNo(&invoice)
	if tradeNo == "" {
		return nil
	}
	order := model.GetSubscriptionOrderByTradeNo(tradeNo)
	if order == nil {
		logger.LogWarn(ctx, fmt.Sprintf("Stripe 订阅账单找不到本地订单 trade_no=%s invoice_id=%s client_ip=%s", tradeNo, invoice.ID, callerIP))
		return nil
	}
	periodStart, periodEnd, err := validateStripeSubscriptionInvoiceEvidence(order, &invoice)
	if err != nil {
		logger.LogWarn(ctx, fmt.Sprintf("Stripe 订阅账单证据不匹配 trade_no=%s invoice_id=%s client_ip=%s error=%q", tradeNo, invoice.ID, callerIP, err.Error()))
		return fmt.Errorf("%w: %v", model.ErrPaymentEvidenceConflict, err)
	}
	if invoice.Subscription == nil || strings.TrimSpace(invoice.Subscription.ID) == "" || strings.TrimSpace(invoice.ID) == "" {
		return fmt.Errorf("%w: stripe subscription invoice identity is missing", model.ErrPaymentEvidenceConflict)
	}
	LockOrder(tradeNo)
	defer UnlockOrder(tradeNo)
	if err := model.CompleteSubscriptionOrder(tradeNo, string(event.Data.Raw), model.PaymentProviderStripe, ""); err != nil {
		return fmt.Errorf("complete stripe subscription order from invoice: %w", err)
	}
	amountMicros, err := minorCurrencyUnitsToMicros(invoice.Total, strings.ToUpper(string(invoice.Currency)))
	if err != nil {
		return err
	}
	return model.ApplySubscriptionPaymentEvent(tradeNo, &model.SubscriptionPaymentEvent{
		PaymentProvider:        model.PaymentProviderStripe,
		ProviderEventId:        event.ID,
		ProviderTransactionId:  invoice.ID,
		SettlementCurrency:     strings.ToUpper(string(invoice.Currency)),
		SettlementAmountMicros: amountMicros,
		PeriodStart:            periodStart,
		PeriodEnd:              periodEnd,
	}, invoice.Subscription.ID, "active")
}

func stripeSubscriptionInvoiceFailed(ctx context.Context, event stripe.Event, callerIP string) error {
	var invoice stripe.Invoice
	if err := json.Unmarshal(event.Data.Raw, &invoice); err != nil {
		return fmt.Errorf("decode failed stripe subscription invoice: %w", err)
	}
	tradeNo := stripeInvoiceTradeNo(&invoice)
	if tradeNo == "" {
		return nil
	}
	order := model.GetSubscriptionOrderByTradeNo(tradeNo)
	if order == nil || invoice.Subscription == nil {
		return nil
	}
	periodStart, periodEnd, _ := stripeInvoicePeriod(&invoice, order.ProviderProductId)
	logger.LogWarn(ctx, fmt.Sprintf("Stripe 订阅续费失败 trade_no=%s invoice_id=%s client_ip=%s", tradeNo, invoice.ID, callerIP))
	return model.UpdateSubscriptionProviderState(tradeNo, model.PaymentProviderStripe, invoice.Subscription.ID, "past_due", periodStart, periodEnd, 0)
}

func stripeSubscriptionStateChanged(ctx context.Context, event stripe.Event, callerIP string) error {
	var subscription stripe.Subscription
	if err := json.Unmarshal(event.Data.Raw, &subscription); err != nil {
		return fmt.Errorf("decode stripe subscription state: %w", err)
	}
	tradeNo := strings.TrimSpace(subscription.Metadata[subscriptionTradeNoMetadataKey])
	if tradeNo == "" {
		return nil
	}
	state := strings.ToLower(strings.TrimSpace(string(subscription.Status)))
	if event.Type == stripe.EventTypeCustomerSubscriptionDeleted {
		state = "canceled"
	} else if subscription.CancelAtPeriodEnd && state == "active" {
		state = "canceling"
	}
	logger.LogInfo(ctx, fmt.Sprintf("Stripe 订阅状态同步 trade_no=%s subscription_id=%s state=%s client_ip=%s", tradeNo, subscription.ID, state, callerIP))
	return model.UpdateSubscriptionProviderState(tradeNo, model.PaymentProviderStripe, subscription.ID, state, subscription.CurrentPeriodStart, subscription.CurrentPeriodEnd, subscription.CanceledAt)
}

func sessionExpired(ctx context.Context, event stripe.Event) error {
	referenceId := event.GetObjectValue("client_reference_id")
	status := event.GetObjectValue("status")
	if "expired" != status {
		logger.LogWarn(ctx, fmt.Sprintf("Stripe checkout.expired 状态异常，忽略处理 trade_no=%s status=%s", referenceId, status))
		return nil
	}

	if len(referenceId) == 0 {
		logger.LogWarn(ctx, "Stripe checkout.expired 缺少订单号")
		return nil
	}

	// Subscription order expiration
	LockOrder(referenceId)
	defer UnlockOrder(referenceId)
	if err := model.ExpireSubscriptionOrder(referenceId, model.PaymentProviderStripe); err == nil {
		logger.LogInfo(ctx, fmt.Sprintf("Stripe 订阅订单已过期 trade_no=%s", referenceId))
		return nil
	} else if err != nil && !errors.Is(err, model.ErrSubscriptionOrderNotFound) {
		logger.LogError(ctx, fmt.Sprintf("Stripe 订阅订单过期处理失败 trade_no=%s error=%q", referenceId, err.Error()))
		return fmt.Errorf("expire subscription order: %w", err)
	}

	err := model.UpdatePendingTopUpStatus(referenceId, model.PaymentProviderStripe, common.TopUpStatusExpired)
	if errors.Is(err, model.ErrTopUpNotFound) {
		logger.LogWarn(ctx, fmt.Sprintf("Stripe 充值订单不存在，无法标记过期 trade_no=%s", referenceId))
		return nil
	}
	if err != nil {
		logger.LogError(ctx, fmt.Sprintf("Stripe 充值订单过期处理失败 trade_no=%s error=%q", referenceId, err.Error()))
		return fmt.Errorf("expire topup order: %w", err)
	}

	logger.LogInfo(ctx, fmt.Sprintf("Stripe 充值订单已过期 trade_no=%s", referenceId))
	return nil
}

// genStripeLink generates a Stripe Checkout session URL for payment.
// It creates a new checkout session with the specified parameters and returns the payment URL.
//
// Parameters:
//   - referenceId: unique reference identifier for the transaction
//   - customerId: existing Stripe customer ID (empty string if new customer)
//   - email: customer email address for new customer creation
//   - amount: quantity of units to purchase
//   - successURL: custom URL to redirect after successful payment (empty for default)
//   - cancelURL: custom URL to redirect when payment is canceled (empty for default)
//
// Returns the checkout session URL or an error if the session creation fails.
func genStripeLink(referenceId string, customerId string, email string, expectedAmountMicros int64, successURL string, cancelURL string) (string, string, error) {
	configuredPrice, err := getStripeTopUpPrice()
	if err != nil {
		return "", "", err
	}
	return genStripeLinkWithPrice(referenceId, customerId, email, expectedAmountMicros, successURL, cancelURL, configuredPrice)
}

func getStripeTopUpPrice() (*stripe.Price, error) {
	if !strings.HasPrefix(setting.StripeApiSecret, "sk_") && !strings.HasPrefix(setting.StripeApiSecret, "rk_") {
		return nil, fmt.Errorf("无效的Stripe API密钥")
	}

	stripe.Key = setting.StripeApiSecret
	configuredPrice, err := stripeprice.Get(setting.StripePriceId, nil)
	if err != nil {
		return nil, err
	}
	return configuredPrice, nil
}

func genStripeLinkWithPrice(referenceId string, customerId string, email string, expectedAmountMicros int64, successURL string, cancelURL string, configuredPrice *stripe.Price) (string, string, error) {
	lineItem, currency, err := stripeTopUpLineItem(configuredPrice, expectedAmountMicros)
	if err != nil {
		return "", "", err
	}

	// Use custom URLs if provided, otherwise use defaults
	if successURL == "" {
		successURL = paymentReturnPath("/usage-logs")
	}
	if cancelURL == "" {
		cancelURL = paymentReturnPath("/wallet")
	}

	params := &stripe.CheckoutSessionParams{
		ClientReferenceID: stripe.String(referenceId),
		SuccessURL:        stripe.String(successURL),
		CancelURL:         stripe.String(cancelURL),
		LineItems: []*stripe.CheckoutSessionLineItemParams{
			lineItem,
		},
		Mode:                stripe.String(string(stripe.CheckoutSessionModePayment)),
		AllowPromotionCodes: stripe.Bool(setting.StripePromotionCodesEnabled),
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
		return "", "", err
	}
	actualSubtotalMicros, err := minorCurrencyUnitsToMicros(result.AmountSubtotal, currency)
	if err != nil {
		return "", "", fmt.Errorf("invalid Stripe checkout settlement: %w", err)
	}
	if actualSubtotalMicros != expectedAmountMicros {
		return "", "", fmt.Errorf("Stripe checkout subtotal does not match canonical quote")
	}
	return result.URL, currency, nil
}

func stripeTopUpLineItem(configuredPrice *stripe.Price, expectedAmountMicros int64) (*stripe.CheckoutSessionLineItemParams, string, error) {
	if configuredPrice == nil || configuredPrice.Product == nil || strings.TrimSpace(configuredPrice.Product.ID) == "" {
		return nil, "", fmt.Errorf("Stripe price has no product")
	}
	currency := strings.ToUpper(strings.TrimSpace(string(configuredPrice.Currency)))
	minorAmount, err := monetaryMicrosToMinorCurrencyUnits(expectedAmountMicros, currency)
	if err != nil {
		return nil, "", err
	}
	return &stripe.CheckoutSessionLineItemParams{
		PriceData: &stripe.CheckoutSessionLineItemPriceDataParams{
			Currency:   stripe.String(strings.ToLower(currency)),
			Product:    stripe.String(configuredPrice.Product.ID),
			UnitAmount: stripe.Int64(minorAmount),
		},
		Quantity: stripe.Int64(1),
	}, currency, nil
}

func stripeTopUpQuoteMicros(amount int64, group string) (int64, error) {
	return monetaryStringToMicros(strconv.FormatFloat(getStripePayMoney(float64(amount), group), 'f', 2, 64))
}

func GetChargedAmount(count float64, user model.User) float64 {
	topUpGroupRatio := common.GetTopupGroupRatio(user.Group)
	if topUpGroupRatio == 0 {
		topUpGroupRatio = 1
	}

	return count * topUpGroupRatio
}

func getStripeCreditedQuota(amount int64, group string) decimal.Decimal {
	topUpGroupRatio := common.GetTopupGroupRatio(group)
	if topUpGroupRatio == 0 {
		topUpGroupRatio = 1
	}
	return decimal.NewFromInt(amount).
		Mul(decimal.NewFromFloat(topUpGroupRatio)).
		Mul(decimal.NewFromFloat(common.QuotaPerUnit))
}

func getStripePayMoney(amount float64, group string) float64 {
	return getStripePayMoneyDecimal(decimal.NewFromFloat(amount), group).InexactFloat64()
}

func getStripePayMoneyDecimal(amount decimal.Decimal, group string) decimal.Decimal {
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

func getStripeMinTopup() int64 {
	minTopup := int64(setting.StripeMinTopUp)
	if operation_setting.GetQuotaDisplayType() == operation_setting.QuotaDisplayTypeTokens {
		if !validQuotaPerUnit() {
			return int64(common.MaxWalletQuota)
		}
		quotaPerUnit, ok := decimalInt64Truncated(decimal.NewFromFloat(common.QuotaPerUnit))
		if !ok || quotaPerUnit < 0 {
			return int64(common.MaxWalletQuota)
		}
		converted, ok := decimalInt64Truncated(decimal.NewFromInt(minTopup).Mul(decimal.NewFromInt(quotaPerUnit)))
		if !ok || converted < 0 || converted > int64(common.MaxWalletQuota) {
			return int64(common.MaxWalletQuota)
		}
		minTopup = converted
	}
	return minTopup
}
