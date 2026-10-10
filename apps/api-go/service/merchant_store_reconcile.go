package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
)

// A provider's expired checkout and a complete payment ledger are both needed
// before releasing inventory. Local time alone cannot establish non-payment.
func merchantStorePancakeMayClose(order *model.MerchantStoreOrder, payments []waffoPancakePayment, now int64) bool {
	if order == nil || order.ProviderTradeID != "" || order.ProviderCheckoutExpiresAt <= 0 || now < order.ProviderCheckoutExpiresAt+900 {
		return false
	}
	canClose, _ := waffoPancakePaymentDisposition(payments)
	return canClose
}

func merchantStoreCloseVerifiedPancakeLedger(order *model.MerchantStoreOrder, payments []waffoPancakePayment, now int64) (bool, error) {
	if !merchantStorePancakeMayClose(order, payments, now) {
		return false, nil
	}
	proof, _ := json.Marshal(payments)
	hash := sha256.Sum256(append([]byte(order.TradeNo+":"+strconv.FormatInt(order.ProviderCheckoutExpiresAt, 10)+":"), proof...))
	err := model.ConfirmMerchantStoreOrderPaymentClosed(order.ID, "pancake-read:"+hex.EncodeToString(hash[:]))
	return err == nil, err
}

type merchantStoreEpayQueryTransport struct {
	base http.RoundTripper
	host string
	path string
}

func (transport merchantStoreEpayQueryTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request == nil || request.URL == nil || request.Method != http.MethodGet {
		return nil, ErrMerchantStorePaymentNetwork
	}
	u, err := merchantStorePublicHTTPSURL(request.URL.String(), true)
	if err != nil || u.Host != transport.host || u.Path != transport.path {
		return nil, ErrMerchantStorePaymentNetwork
	}
	clone := request.Clone(request.Context())
	clone.Header = request.Header.Clone()
	clone.Header.Del("Cookie")
	clone.Header.Del("Authorization")
	clone.Header.Del("Proxy-Authorization")
	response, err := transport.base.RoundTrip(clone)
	if err != nil {
		return nil, ErrMerchantStorePaymentNetwork
	}
	if err := common.LimitResponseBody(response, 64<<10); err != nil {
		return nil, ErrMerchantStorePaymentVerification
	}
	return response, nil
}

func merchantStoreEpayQueryString(raw json.RawMessage) string {
	var value string
	if json.Unmarshal(raw, &value) == nil {
		return value
	}
	// Legacy ePay implementations serialize merchant/status integers as numbers.
	value = string(raw)
	if value == "" || strings.Trim(value, "0123456789") != "" {
		return ""
	}
	return value
}

func validateMerchantStoreEpayQuery(order *model.MerchantStoreOrder, paymentContext merchantStorePaymentContext, payload []byte) (string, error) {
	var values map[string]json.RawMessage
	if json.Unmarshal(payload, &values) != nil || merchantStoreEpayQueryString(values["code"]) != "1" {
		return "", ErrMerchantStorePaymentVerification
	}
	if merchantStoreEpayQueryString(values["status"]) != "1" {
		// Linux DO explicitly defines status=0 as failed OR processing. Its
		// not-found response is ambiguous too. Neither is proof of closure.
		return "", ErrMerchantStorePaymentIgnored
	}
	minor, err := merchantStoreMoneyToMinor(merchantStoreEpayQueryString(values["money"]))
	tradeID := merchantStoreEpayQueryString(values["trade_no"])
	if err != nil || minor != order.AmountMinor || merchantStoreEpayQueryString(values["pid"]) != paymentContext.Config.PartnerID || merchantStoreEpayQueryString(values["out_trade_no"]) != order.TradeNo || merchantStoreEpayQueryString(values["type"]) != paymentContext.Config.PaymentType || !merchantStoreProviderIDPattern.MatchString(tradeID) {
		return "", ErrMerchantStorePaymentVerification
	}
	if currency := merchantStoreEpayQueryString(values["currency"]); currency != "" && currency != order.Currency {
		return "", ErrMerchantStorePaymentVerification
	}
	return tradeID, nil
}

func merchantStoreQueryEpayPayment(ctx context.Context, order *model.MerchantStoreOrder, paymentContext merchantStorePaymentContext) (string, error) {
	u, err := merchantStorePublicHTTPSURL(paymentContext.Config.GatewayURL, false)
	if err != nil {
		return "", err
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/api.php"
	query := url.Values{"act": {"order"}, "pid": {paymentContext.Config.PartnerID}, "key": {paymentContext.Config.Key}, "out_trade_no": {order.TradeNo}}
	u.RawQuery = query.Encode()
	// Keep the standard ePay key in this bounded, host-pinned request only;
	// upstream URLs/errors are never returned to clients or logged.
	client := newMerchantStorePaymentHTTPClient(order.ID)
	base := client.Transport.(merchantStoreProviderTransport).base
	client.Transport = merchantStoreEpayQueryTransport{base: base, host: u.Host, path: u.Path}
	client.Timeout = 8 * time.Second
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", ErrMerchantStorePaymentConfiguration
	}
	response, err := client.Do(request)
	if err != nil {
		return "", ErrMerchantStorePaymentNetwork
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", ErrMerchantStorePaymentVerification
	}
	payload, err := io.ReadAll(response.Body)
	if err != nil {
		return "", ErrMerchantStorePaymentVerification
	}
	return validateMerchantStoreEpayQuery(order, paymentContext, payload)
}

func reconcileMerchantStorePayment(ctx context.Context, order *model.MerchantStoreOrder, now int64) error {
	if order == nil || (order.Status != "pending" && order.Status != "reconciliation_pending") || order.GatewaySnapshot == "" {
		return nil
	}
	if order.ProviderTradeID != "" && order.VerifiedPaymentIssueAt > 0 {
		// This receipt was already fully verified. Retry its frozen obligation,
		// never replace positive paid evidence with a later negative ledger.
		err := completeMerchantStoreVerifiedPayment(order.ID, order.ProviderTradeID)
		code := ""
		if err != nil {
			code = "settlement_retry_pending"
		}
		if markErr := model.MarkMerchantStorePaymentChecked(order.ID, now, code); markErr != nil && err == nil {
			return markErr
		}
		return err
	}
	paymentContext, err := loadMerchantStorePaymentContext(order)
	if err != nil {
		_ = model.MarkMerchantStorePaymentChecked(order.ID, now, "configuration_unavailable")
		return err
	}
	checkContext, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	code := ""
	switch order.PaymentMethod {
	case MerchantStoreExternalPancake, MerchantStorePlatformPancake:
		client, clientErr := newMerchantStorePancakeClient(paymentContext.Config, order.ID)
		if clientErr != nil {
			err = ErrMerchantStorePaymentConfiguration
			break
		}
		payments, queryErr := waffoPancakePaymentsByTradeNo(checkContext, client, order.TradeNo)
		if queryErr != nil {
			err = ErrMerchantStorePaymentVerification
			break
		}
		closed, closeErr := merchantStoreCloseVerifiedPancakeLedger(order, payments, now)
		err = closeErr
		if !closed && err == nil {
			// A succeeded ledger status alone does not prove the frozen amount,
			// currency, buyer and product. Wait for the fully signed order event.
			code = "awaiting_verified_callback"
		}
	case MerchantStoreExternalEpay, MerchantStorePlatformLinuxDO:
		tradeID, queryErr := merchantStoreQueryEpayPayment(checkContext, order, paymentContext)
		if queryErr == ErrMerchantStorePaymentIgnored {
			code = "provider_closure_unconfirmed"
		} else if queryErr != nil {
			err = queryErr
		} else {
			err = completeMerchantStoreVerifiedPayment(order.ID, tradeID)
			if err == nil && order.PaymentMethod == MerchantStorePlatformLinuxDO {
				err = merchantStoreRecordLinuxDORefundBasis(order.ID, tradeID, order.AmountMinor)
			}
		}
	default:
		return ErrMerchantStorePaymentConfiguration
	}
	if err != nil {
		code = "provider_lookup_failed"
	}
	if markErr := model.MarkMerchantStorePaymentChecked(order.ID, now, code); markErr != nil && err == nil {
		err = markErr
	}
	return err
}

// ReconcileMerchantStoreOrderPayment reads the trusted provider API using the
// credentials and money quote frozen before checkout. It cannot accept a client
// supplied paid/closed assertion and never refunds from an unsigned return URL.
func ReconcileMerchantStoreOrderPayment(ctx context.Context, actorID int, orderID string) (*model.MerchantStoreOrder, error) {
	order, err := model.GetMerchantStorePaymentOrder(orderID)
	actor, actorErr := model.GetUserById(actorID, false)
	if err != nil || actorErr != nil || order == nil || actor == nil || actor.Status != common.UserStatusEnabled || (actorID != order.BuyerID && actorID != order.SellerID && actor.Role < common.RoleAdminUser) {
		return nil, ErrMerchantStorePaymentAccess
	}
	if err := reconcileMerchantStorePayment(ctx, order, time.Now().Unix()); err != nil {
		return nil, err
	}
	if actorID == order.BuyerID || actorID == order.SellerID {
		return model.GetMerchantStoreOrder(actorID, orderID)
	}
	order, err = model.GetMerchantStorePaymentOrder(orderID)
	if err == nil {
		order.CheckoutURL = ""
		order.PaymentIssued = order.GatewaySnapshot != "" || order.ProviderSessionID != ""
	}
	return order, err
}

func reconcileMerchantStorePaymentBatch(ctx context.Context, limit int) error {
	now := time.Now().Unix()
	orders, err := model.DueMerchantStorePaymentOrders(ctx, now, limit)
	if err != nil {
		return err
	}
	for index := range orders {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// Failed lookups keep all reservations and are retried after the
		// model's fifteen-minute check interval; never log secret error text.
		_ = reconcileMerchantStorePayment(ctx, &orders[index], now)
	}
	return nil
}
