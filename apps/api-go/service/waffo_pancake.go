package service

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/setting"
	"github.com/shopspring/decimal"
	pancake "github.com/waffo-com/waffo-pancake-sdk-go"
)

// WaffoPancakePriceSnapshot is the per-session price override sent with checkout.
type WaffoPancakePriceSnapshot struct {
	Amount      string
	TaxCategory string
}

// WaffoPancake checkout regions are deliberately a closed set.  The Waffo
// API has no `region` request field: the China market is selected by sending a
// fixed CN billing detail, while global checkout leaves billingDetail omitted.
type WaffoPancakeCheckoutRegion string

const (
	WaffoPancakeCheckoutRegionGlobal WaffoPancakeCheckoutRegion = "global"
	WaffoPancakeCheckoutRegionChina  WaffoPancakeCheckoutRegion = "china"
)

// waffoPancakeCheckoutLanguages is the allow-list accepted by Waffo's
// checkout endpoint.  Keep this list in sync with the provider's BCP 47
// enum, rather than forwarding an arbitrary browser language tag.
var waffoPancakeCheckoutLanguages = map[string]struct{}{
	"en":         {},
	"pt-BR":      {},
	"es-MX":      {},
	"id-ID":      {},
	"vi-VN":      {},
	"ru-RU":      {},
	"en-KE":      {},
	"es-PE":      {},
	"es-CO":      {},
	"es-CL":      {},
	"zh-Hant-TW": {},
	"zh-Hant-HK": {},
	"th-TH":      {},
	"ja-JP":      {},
	"en-NG":      {},
	"ko-KR":      {},
	"en-HK":      {},
	"zh-Hans-HK": {},
	"pl-PL":      {},
	"tr-TR":      {},
	"zh-Hans":    {},
	"ms-MY":      {},
}

var waffoPancakeChineseCheckoutLanguages = map[string]struct{}{
	"zh-Hans":    {},
	"zh-Hant-TW": {},
	"zh-Hant-HK": {},
	"zh-Hans-HK": {},
}

type WaffoPancakeBillingDetail struct {
	Country      string `json:"country"`
	IsBusiness   bool   `json:"isBusiness"`
	Postcode     string `json:"postcode,omitempty"`
	State        string `json:"state,omitempty"`
	BusinessName string `json:"businessName,omitempty"`
	TaxID        string `json:"taxId,omitempty"`
}

// WaffoPancakeCreateSessionParams is the input to CreateWaffoPancakeCheckoutSession.
// BuyerIdentity must be stable per user (see WaffoPancakeBuyerIdentityFromUserID).
// OrderMerchantExternalID = our trade_no; Pancake echoes it back in webhooks.
type WaffoPancakeCreateSessionParams struct {
	ProductID               string
	BuyerIdentity           string
	PriceSnapshot           *WaffoPancakePriceSnapshot
	BuyerEmail              string
	ExpiresInSeconds        *int
	OrderMerchantExternalID string
	// OrderMetadata is echoed by Waffo in signed webhook events.  Callers use
	// it to bind a callback to the exact product/plan selected at checkout.
	OrderMetadata map[string]string
	// CheckoutRegion is the application-level china/global selector. It is
	// translated to billingDetail by the service; Waffo has no region field.
	CheckoutRegion string
	// CheckoutLanguage is validated against Waffo's supported BCP 47 enum.
	CheckoutLanguage string
	// BillingDetail is populated only from the authenticated user's saved
	// company profile after its invoice toggle has been enabled.
	BillingDetail *WaffoPancakeBillingDetail
}

// WaffoPancakeOrderMetadataProductID and WaffoPancakeOrderMetadataPlanID are
// deliberately namespaced so provider/user-supplied metadata cannot be
// mistaken for our settlement evidence.
const (
	WaffoPancakeOrderMetadataProductID = "lmm_product_id"
	WaffoPancakeOrderMetadataPlanID    = "lmm_plan_id"
)

// WaffoPancakeCheckoutSession is the response of CreateWaffoPancakeCheckoutSession.
// CheckoutURL already carries the `#token=...` fragment; Token / TokenExpiresAt
// are exposed separately for self-service flows driven from new-api's own UI.
type WaffoPancakeCheckoutSession struct {
	SessionID      string
	CheckoutURL    string
	ExpiresAt      string
	OrderID        string
	Token          string
	TokenExpiresAt string
}

// WaffoPancakeWebhookEvent mirrors the SDK's WebhookEvent shape using plain
// strings so controllers don't have to import the SDK package.
type WaffoPancakeWebhookEvent struct {
	ID        string
	Timestamp string
	EventType string
	EventID   string
	StoreID   string
	Mode      string
	Data      WaffoPancakeWebhookData
}

type WaffoPancakeWebhookData struct {
	// OrderID = Pancake ORD_* (logs); OrderMerchantExternalID = our trade_no (lookup).
	OrderID                        string
	OrderStatus                    string
	OrderMerchantExternalID        string
	RefundTicketMerchantExternalID string
	BuyerEmail                     string
	Currency                       string
	Amount                         string
	TaxAmount                      string
	ProductName                    string
	OrderMetadata                  map[string]string
	ProductMetadata                map[string]string
	MerchantProvidedBuyerIdentity  string
	PaymentID                      string
	PaymentStatus                  string
	PaymentMethod                  string
	PaymentLast4                   string
	CurrentPeriodStart             string
	CurrentPeriodEnd               string
	CanceledAt                     string
	RefundStatus                   string
	RefundReason                   string
	RefundCreatedAt                string
	Total                          string
}

// WaffoPancakeWebhookAction is the small, explicit dispatch surface used by
// the HTTP handler. Unknown provider events are acknowledged without mutating
// local payment state so adding a provider event cannot accidentally credit or
// debit a wallet.
type WaffoPancakeWebhookAction string

const (
	WaffoPancakeWebhookActionOrderCompleted               WaffoPancakeWebhookAction = "order_completed"
	WaffoPancakeWebhookActionSubscriptionActivated        WaffoPancakeWebhookAction = "subscription_activated"
	WaffoPancakeWebhookActionSubscriptionPaymentSucceeded WaffoPancakeWebhookAction = "subscription_payment_succeeded"
	WaffoPancakeWebhookActionSubscriptionStateChanged     WaffoPancakeWebhookAction = "subscription_state_changed"
	WaffoPancakeWebhookActionRefundSucceeded              WaffoPancakeWebhookAction = "refund_succeeded"
	WaffoPancakeWebhookActionRefundFailed                 WaffoPancakeWebhookAction = "refund_failed"
	WaffoPancakeWebhookActionIgnore                       WaffoPancakeWebhookAction = "ignore"
)

func WaffoPancakeWebhookActionForEvent(eventType string) WaffoPancakeWebhookAction {
	switch strings.TrimSpace(eventType) {
	case "order.completed":
		return WaffoPancakeWebhookActionOrderCompleted
	// State events never prove payment; they only synchronize lifecycle state.
	case "subscription.activated", "subscription.canceling", "subscription.uncanceled", "subscription.updated", "subscription.past_due", "subscription.canceled":
		return WaffoPancakeWebhookActionSubscriptionStateChanged
	case "subscription.payment_succeeded":
		return WaffoPancakeWebhookActionSubscriptionPaymentSucceeded
	case "refund.succeeded":
		return WaffoPancakeWebhookActionRefundSucceeded
	case "refund.failed":
		return WaffoPancakeWebhookActionRefundFailed
	default:
		return WaffoPancakeWebhookActionIgnore
	}
}

// ValidateWaffoPancakeWebhookEvent checks the status fields that Pancake
// includes in signed payment payloads. A signature proves that Pancake sent
// the payload, but it does not make contradictory fields safe to process: a
// refund.succeeded event must not carry refundStatus=failed, for example.
// Older payloads may omit optional status fields, so empty values remain
// accepted for backwards compatibility.
func ValidateWaffoPancakeWebhookEvent(event *WaffoPancakeWebhookEvent) error {
	if event == nil {
		return fmt.Errorf("missing webhook event")
	}
	check := func(field, actual, expected string) error {
		actual = strings.TrimSpace(actual)
		if actual != "" && !strings.EqualFold(actual, expected) {
			return fmt.Errorf("webhook %s mismatch: expected %q actual %q", field, expected, actual)
		}
		return nil
	}

	switch WaffoPancakeWebhookActionForEvent(event.EventType) {
	case WaffoPancakeWebhookActionOrderCompleted:
		if err := check("orderStatus", event.Data.OrderStatus, "completed"); err != nil {
			return err
		}
		return check("paymentStatus", event.Data.PaymentStatus, "succeeded")
	case WaffoPancakeWebhookActionSubscriptionStateChanged:
		return nil
	case WaffoPancakeWebhookActionSubscriptionPaymentSucceeded:
		return check("paymentStatus", event.Data.PaymentStatus, "succeeded")
	case WaffoPancakeWebhookActionRefundSucceeded:
		return check("refundStatus", event.Data.RefundStatus, "succeeded")
	case WaffoPancakeWebhookActionRefundFailed:
		return check("refundStatus", event.Data.RefundStatus, "failed")
	default:
		return nil
	}
}

// NormalizedEventType returns the event type or empty string for a nil event.
func (e *WaffoPancakeWebhookEvent) NormalizedEventType() string {
	if e == nil {
		return ""
	}
	return e.EventType
}

// newWaffoPancakeClient builds an SDK client from persisted settings. The
// runtime checkout / webhook paths use this; configuration endpoints use
// newWaffoPancakeClientFromCreds so the operator can verify typed-but-not-
// yet-saved credentials.
func newWaffoPancakeClient() (*pancake.Client, error) {
	merchantID, privateKey := WaffoPancakeCredentials()
	client, err := pancake.New(pancake.Config{
		MerchantID: merchantID,
		PrivateKey: privateKey,
	})
	if err != nil {
		return nil, fmt.Errorf("initialize Waffo Pancake client: %w", err)
	}
	return client, nil
}

// WaffoPancakeCredentials resolves persisted settings first, then the two
// official environment variables used by the first server-side integration.
// Environment fallback keeps secrets out of the database and is intentionally
// limited to credentials; store/product IDs remain runtime configuration.
func WaffoPancakeCredentials() (string, string) {
	merchantID := strings.TrimSpace(setting.WaffoPancakeMerchantID)
	if merchantID == "" {
		merchantID = strings.TrimSpace(os.Getenv("WAFFO_MERCHANT_ID"))
	}
	privateKey := strings.TrimSpace(setting.WaffoPancakePrivateKey)
	if privateKey == "" {
		privateKey = strings.TrimSpace(os.Getenv("WAFFO_PRIVATE_KEY"))
	}
	return merchantID, privateKey
}

func newWaffoPancakeClientFromCreds(merchantID, privateKey string) (*pancake.Client, error) {
	if strings.TrimSpace(merchantID) == "" || strings.TrimSpace(privateKey) == "" {
		return nil, fmt.Errorf("merchant id and private key are required")
	}
	client, err := pancake.New(pancake.Config{
		MerchantID: merchantID,
		PrivateKey: privateKey,
	})
	if err != nil {
		return nil, fmt.Errorf("initialize Waffo Pancake client: %w", err)
	}
	return client, nil
}

// NormalizeWaffoPancakeCheckoutRegion accepts only the two regions exposed by
// this application. Empty and invalid values intentionally fall back to the
// unrestricted global checkout; callers that need language-based defaulting
// should use ResolveWaffoPancakeCheckoutRegion.
func NormalizeWaffoPancakeCheckoutRegion(region string) WaffoPancakeCheckoutRegion {
	switch strings.TrimSpace(region) {
	case string(WaffoPancakeCheckoutRegionChina):
		return WaffoPancakeCheckoutRegionChina
	case string(WaffoPancakeCheckoutRegionGlobal), "":
		return WaffoPancakeCheckoutRegionGlobal
	default:
		return WaffoPancakeCheckoutRegionGlobal
	}
}

// ResolveWaffoPancakeCheckoutRegion applies the application defaulting rule:
// an omitted region follows one of the supported Chinese checkout languages,
// while an omitted region with any other (or no) language is global. Explicit
// values always win, and an unrecognised region is never allowed to select CN.
func ResolveWaffoPancakeCheckoutRegion(region, language string) WaffoPancakeCheckoutRegion {
	switch strings.TrimSpace(region) {
	case string(WaffoPancakeCheckoutRegionChina):
		return WaffoPancakeCheckoutRegionChina
	case string(WaffoPancakeCheckoutRegionGlobal):
		return WaffoPancakeCheckoutRegionGlobal
	case "":
		if language = NormalizeWaffoPancakeCheckoutLanguage(language); language != "" {
			if _, ok := waffoPancakeChineseCheckoutLanguages[language]; ok {
				return WaffoPancakeCheckoutRegionChina
			}
		}
		return WaffoPancakeCheckoutRegionGlobal
	default:
		return WaffoPancakeCheckoutRegionGlobal
	}
}

// NormalizeWaffoPancakeCheckoutLanguage returns a supported BCP 47 language
// tag.  Invalid and empty values are omitted so Waffo can infer its default,
// preserving compatibility with clients that predate checkout_language.
func NormalizeWaffoPancakeCheckoutLanguage(language string) string {
	language = strings.TrimSpace(language)
	if _, ok := waffoPancakeCheckoutLanguages[language]; !ok {
		return ""
	}
	return language
}

// buildWaffoPancakeSDKCheckoutParams is kept separate from the network call
// so the region/language security boundary can be tested without credentials.
func buildWaffoPancakeSDKCheckoutParams(params *WaffoPancakeCreateSessionParams) (pancake.AuthenticatedCheckoutParams, error) {
	if params == nil {
		return pancake.AuthenticatedCheckoutParams{}, fmt.Errorf("missing checkout params")
	}

	sdkParams := pancake.AuthenticatedCheckoutParams{
		CreateCheckoutSessionParams: pancake.CreateCheckoutSessionParams{
			ProductID:               params.ProductID,
			Currency:                "USD",
			BuyerEmail:              optionalString(params.BuyerEmail),
			ExpiresInSeconds:        params.ExpiresInSeconds,
			OrderMerchantExternalID: optionalString(params.OrderMerchantExternalID),
			Metadata:                params.OrderMetadata,
		},
		BuyerIdentity: params.BuyerIdentity,
	}
	if params.PriceSnapshot != nil {
		sdkParams.PriceSnapshot = &pancake.PriceInfo{
			Amount:      params.PriceSnapshot.Amount,
			TaxCategory: pancake.TaxCategory(params.PriceSnapshot.TaxCategory),
		}
	}
	if params.BillingDetail != nil {
		sdkParams.BillingDetail = &pancake.BillingDetail{
			Country:      params.BillingDetail.Country,
			IsBusiness:   params.BillingDetail.IsBusiness,
			Postcode:     optionalString(params.BillingDetail.Postcode),
			State:        optionalString(params.BillingDetail.State),
			BusinessName: optionalString(params.BillingDetail.BusinessName),
			TaxID:        optionalString(params.BillingDetail.TaxID),
		}
	} else if ResolveWaffoPancakeCheckoutRegion(params.CheckoutRegion, params.CheckoutLanguage) == WaffoPancakeCheckoutRegionChina {
		// This pre-existing personal-region hint is not company profile data.
		sdkParams.BillingDetail = &pancake.BillingDetail{
			Country:    "CN",
			IsBusiness: false,
		}
	}
	if language := NormalizeWaffoPancakeCheckoutLanguage(params.CheckoutLanguage); language != "" {
		checkoutLanguage := pancake.CashierLanguage(language)
		sdkParams.Language = &checkoutLanguage
	}
	return sdkParams, nil
}

// CreateWaffoPancakeCheckoutSession creates an Authenticated-mode checkout
// session: the order is bound to BuyerIdentity (stable per user) so it stays
// attributable even if the buyer edits the email on Waffo's checkout form.
func CreateWaffoPancakeCheckoutSession(ctx context.Context, params *WaffoPancakeCreateSessionParams) (*WaffoPancakeCheckoutSession, error) {
	if params == nil {
		return nil, fmt.Errorf("missing checkout params")
	}
	if strings.TrimSpace(params.BuyerIdentity) == "" {
		return nil, fmt.Errorf("missing buyer identity")
	}
	if strings.TrimSpace(params.OrderMerchantExternalID) == "" {
		return nil, fmt.Errorf("missing order merchant external id")
	}
	client, err := newWaffoPancakeClient()
	if err != nil {
		return nil, fmt.Errorf("build Waffo Pancake client: %w", err)
	}

	sdkParams, err := buildWaffoPancakeSDKCheckoutParams(params)
	if err != nil {
		return nil, err
	}

	session, err := client.Checkout.Authenticated.Create(ctx, sdkParams)
	if err != nil {
		return nil, err
	}
	if session == nil || strings.TrimSpace(session.CheckoutURL) == "" || strings.TrimSpace(session.SessionID) == "" {
		return nil, fmt.Errorf("Waffo Pancake returned empty checkout session")
	}
	return &WaffoPancakeCheckoutSession{
		SessionID:      session.SessionID,
		CheckoutURL:    session.CheckoutURL,
		ExpiresAt:      session.ExpiresAt,
		Token:          session.Token,
		TokenExpiresAt: session.TokenExpiresAt,
	}, nil
}

func optionalString(s string) *string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	v := s
	return &v
}

// WaffoPancakeBuyerIdentityFromUserID renders the canonical buyer identity
// for checkout. Webhook handlers compare against the value rendered here to
// reject identity mismatches, so both call sites must use this function.
func WaffoPancakeBuyerIdentityFromUserID(userID int) string {
	return fmt.Sprintf("new-api-user-%d", userID)
}

// VerifyConfiguredWaffoPancakeWebhook verifies the signature header. The SDK
// picks the matching test / prod public key from the payload's `mode` field.
func VerifyConfiguredWaffoPancakeWebhook(payload string, signatureHeader string) (*WaffoPancakeWebhookEvent, error) {
	evt, err := pancake.VerifyWebhookTyped[pancake.WebhookEventData](payload, signatureHeader, nil)
	if err != nil {
		return nil, err
	}
	identity := ""
	if evt.Data.MerchantProvidedBuyerIdentity != nil {
		identity = *evt.Data.MerchantProvidedBuyerIdentity
	}
	externalID := ""
	if evt.Data.OrderMerchantExternalID != nil {
		externalID = *evt.Data.OrderMerchantExternalID
	}
	refundExternalID := ""
	if evt.Data.RefundTicketMerchantExternalID != nil {
		refundExternalID = *evt.Data.RefundTicketMerchantExternalID
	}
	paymentID := ""
	if evt.Data.PaymentID != nil {
		paymentID = *evt.Data.PaymentID
	}
	paymentStatus := ""
	if evt.Data.PaymentStatus != nil {
		paymentStatus = *evt.Data.PaymentStatus
	}
	paymentMethod := ""
	if evt.Data.PaymentMethod != nil {
		paymentMethod = *evt.Data.PaymentMethod
	}
	paymentLast4 := ""
	if evt.Data.PaymentLast4 != nil {
		paymentLast4 = *evt.Data.PaymentLast4
	}
	currentPeriodStart := ""
	if evt.Data.CurrentPeriodStart != nil {
		currentPeriodStart = *evt.Data.CurrentPeriodStart
	}
	currentPeriodEnd := ""
	if evt.Data.CurrentPeriodEnd != nil {
		currentPeriodEnd = *evt.Data.CurrentPeriodEnd
	}
	canceledAt := ""
	if evt.Data.CanceledAt != nil {
		canceledAt = *evt.Data.CanceledAt
	}
	refundStatus := ""
	if evt.Data.RefundStatus != nil {
		refundStatus = *evt.Data.RefundStatus
	}
	refundReason := ""
	if evt.Data.RefundReason != nil {
		refundReason = *evt.Data.RefundReason
	}
	refundCreatedAt := ""
	if evt.Data.RefundCreatedAt != nil {
		refundCreatedAt = *evt.Data.RefundCreatedAt
	}
	total := ""
	if evt.Data.Total != nil {
		total = *evt.Data.Total
	}
	orderStatus := ""
	if evt.Data.OrderStatus != nil {
		orderStatus = *evt.Data.OrderStatus
	}
	return &WaffoPancakeWebhookEvent{
		ID:        evt.ID,
		Timestamp: evt.Timestamp,
		EventType: evt.EventType,
		EventID:   evt.EventID,
		StoreID:   evt.StoreID,
		Mode:      string(evt.Mode),
		Data: WaffoPancakeWebhookData{
			OrderID:                        evt.Data.OrderID,
			OrderStatus:                    orderStatus,
			OrderMerchantExternalID:        externalID,
			RefundTicketMerchantExternalID: refundExternalID,
			BuyerEmail:                     evt.Data.BuyerEmail,
			Currency:                       evt.Data.Currency,
			Amount:                         evt.Data.Amount,
			TaxAmount:                      evt.Data.TaxAmount,
			ProductName:                    evt.Data.ProductName,
			OrderMetadata:                  evt.Data.OrderMetadata,
			ProductMetadata:                evt.Data.ProductMetadata,
			MerchantProvidedBuyerIdentity:  identity,
			PaymentID:                      paymentID,
			PaymentStatus:                  paymentStatus,
			PaymentMethod:                  paymentMethod,
			PaymentLast4:                   paymentLast4,
			CurrentPeriodStart:             currentPeriodStart,
			CurrentPeriodEnd:               currentPeriodEnd,
			CanceledAt:                     canceledAt,
			RefundStatus:                   refundStatus,
			RefundReason:                   refundReason,
			RefundCreatedAt:                refundCreatedAt,
			Total:                          total,
		},
	}, nil
}

// ResolveWaffoPancakeTradeNo maps a verified webhook event to a local TopUp
// trade_no via OrderMerchantExternalID, and rejects buyer-identity mismatches.
func ResolveWaffoPancakeTradeNo(event *WaffoPancakeWebhookEvent) (string, error) {
	if event == nil {
		return "", fmt.Errorf("missing webhook event")
	}
	tradeNo := strings.TrimSpace(event.Data.OrderMerchantExternalID)
	if tradeNo == "" {
		return "", fmt.Errorf("missing webhook orderMerchantExternalId")
	}
	topUp := model.GetTopUpByTradeNo(tradeNo)
	if topUp == nil || topUp.PaymentProvider != model.PaymentProviderWaffoPancake {
		return "", fmt.Errorf("waffo pancake order not found for tradeNo=%s", tradeNo)
	}
	expectedIdentity := WaffoPancakeBuyerIdentityFromUserID(topUp.UserId)
	actualIdentity := strings.TrimSpace(event.Data.MerchantProvidedBuyerIdentity)
	if actualIdentity != expectedIdentity {
		return "", fmt.Errorf(
			"waffo pancake buyer identity mismatch for tradeNo=%s: expected=%q actual=%q",
			tradeNo,
			expectedIdentity,
			actualIdentity,
		)
	}
	return tradeNo, nil
}

// ResolveWaffoPancakeRefundTradeNo applies the same provider/order binding as
// ResolveWaffoPancakeTradeNo, but accepts a missing buyer identity. Pancake's
// refund payload inherits the order external ID, while identity is optional on
// the SDK type; a signed provider event must not be discarded merely because
// that optional field was omitted. If present, the identity is still checked.
func ResolveWaffoPancakeRefundTradeNo(event *WaffoPancakeWebhookEvent) (string, error) {
	if event == nil {
		return "", fmt.Errorf("missing webhook event")
	}
	tradeNo := strings.TrimSpace(event.Data.OrderMerchantExternalID)
	if tradeNo == "" {
		return "", fmt.Errorf("missing webhook orderMerchantExternalId")
	}
	topUp := model.GetTopUpByTradeNo(tradeNo)
	if topUp == nil || topUp.PaymentProvider != model.PaymentProviderWaffoPancake {
		return "", fmt.Errorf("waffo pancake refund order not found for tradeNo=%s", tradeNo)
	}
	// Refunds bypass CompleteExternalTopUp, so repeat its store binding here.
	// A merchant may have multiple Waffo stores; a signed event for another
	// store must never be allowed to mutate this order's finance history. Keep
	// the empty-order exception for legacy rows that predate store evidence.
	if expectedStore := strings.TrimSpace(topUp.ProviderStoreId); expectedStore != "" &&
		strings.TrimSpace(event.StoreID) != expectedStore {
		return "", fmt.Errorf(
			"waffo pancake refund store mismatch for tradeNo=%s: expected=%q actual=%q",
			tradeNo,
			expectedStore,
			strings.TrimSpace(event.StoreID),
		)
	}
	if topUp.ProviderTransactionId != nil {
		expectedTransaction := strings.TrimSpace(*topUp.ProviderTransactionId)
		if expectedTransaction != "" && strings.TrimSpace(event.Data.OrderID) != expectedTransaction {
			return "", fmt.Errorf(
				"waffo pancake refund transaction mismatch for tradeNo=%s: expected=%q actual=%q",
				tradeNo,
				expectedTransaction,
				strings.TrimSpace(event.Data.OrderID),
			)
		}
	}
	if expectedCurrency := strings.ToUpper(strings.TrimSpace(topUp.SettlementCurrency)); expectedCurrency != "" &&
		strings.ToUpper(strings.TrimSpace(event.Data.Currency)) != expectedCurrency {
		return "", fmt.Errorf(
			"waffo pancake refund currency mismatch for tradeNo=%s: expected=%q actual=%q",
			tradeNo,
			expectedCurrency,
			strings.ToUpper(strings.TrimSpace(event.Data.Currency)),
		)
	}
	actualIdentity := strings.TrimSpace(event.Data.MerchantProvidedBuyerIdentity)
	if actualIdentity != "" {
		expectedIdentity := WaffoPancakeBuyerIdentityFromUserID(topUp.UserId)
		if actualIdentity != expectedIdentity {
			return "", fmt.Errorf(
				"waffo pancake refund buyer identity mismatch for tradeNo=%s: expected=%q actual=%q",
				tradeNo,
				expectedIdentity,
				actualIdentity,
			)
		}
	}
	return tradeNo, nil
}

// ResolveWaffoPancakeSubscriptionTradeNo is the SubscriptionOrder counterpart
// of ResolveWaffoPancakeTradeNo.
func ResolveWaffoPancakeSubscriptionTradeNo(event *WaffoPancakeWebhookEvent) (string, error) {
	if event == nil {
		return "", fmt.Errorf("missing webhook event")
	}
	tradeNo := strings.TrimSpace(event.Data.OrderMerchantExternalID)
	if tradeNo == "" {
		return "", fmt.Errorf("missing webhook orderMerchantExternalId")
	}
	order := model.GetSubscriptionOrderByTradeNo(tradeNo)
	if order == nil || order.PaymentProvider != model.PaymentProviderWaffoPancake {
		return "", fmt.Errorf("waffo pancake subscription order not found for tradeNo=%s", tradeNo)
	}
	expectedIdentity := WaffoPancakeBuyerIdentityFromUserID(order.UserId)
	actualIdentity := strings.TrimSpace(event.Data.MerchantProvidedBuyerIdentity)
	if actualIdentity != expectedIdentity {
		return "", fmt.Errorf(
			"waffo pancake buyer identity mismatch for subscription tradeNo=%s: expected=%q actual=%q",
			tradeNo,
			expectedIdentity,
			actualIdentity,
		)
	}
	return tradeNo, nil
}

func validateWaffoPancakeSubscriptionSettlement(event *WaffoPancakeWebhookEvent, expectedAmount float64) error {
	expectedStore := strings.TrimSpace(setting.WaffoPancakeStoreID)
	actualStore := strings.TrimSpace(event.StoreID)
	if expectedStore == "" || actualStore != expectedStore {
		return fmt.Errorf("store mismatch: expected=%q actual=%q", expectedStore, actualStore)
	}
	if currency := strings.ToUpper(strings.TrimSpace(event.Data.Currency)); currency != "USD" {
		return fmt.Errorf("currency mismatch: expected=%q actual=%q", "USD", currency)
	}
	actualAmount, err := decimal.NewFromString(strings.TrimSpace(event.Data.Amount))
	if err != nil {
		return fmt.Errorf("invalid amount: %w", err)
	}
	expected := decimal.NewFromFloat(expectedAmount).Round(2)
	if !actualAmount.Equal(expected) {
		return fmt.Errorf("amount mismatch: expected=%s actual=%s", expected.StringFixed(2), actualAmount.String())
	}
	return nil
}

// ResolveWaffoPancakeRefundSubscriptionTradeNo is the refund counterpart of
// ResolveWaffoPancakeSubscriptionTradeNo. Refund payloads may omit the
// optional buyer identity, so an empty value is accepted while a supplied one
// is still verified against the original subscription order.
func ResolveWaffoPancakeRefundSubscriptionTradeNo(event *WaffoPancakeWebhookEvent) (string, error) {
	if event == nil {
		return "", fmt.Errorf("missing webhook event")
	}
	tradeNo := strings.TrimSpace(event.Data.OrderMerchantExternalID)
	if tradeNo == "" {
		return "", fmt.Errorf("missing webhook orderMerchantExternalId")
	}
	order := model.GetSubscriptionOrderByTradeNo(tradeNo)
	if order == nil || order.PaymentProvider != model.PaymentProviderWaffoPancake {
		return "", fmt.Errorf("waffo pancake refund subscription order not found for tradeNo=%s", tradeNo)
	}
	actualIdentity := strings.TrimSpace(event.Data.MerchantProvidedBuyerIdentity)
	if actualIdentity != "" {
		expectedIdentity := WaffoPancakeBuyerIdentityFromUserID(order.UserId)
		if actualIdentity != expectedIdentity {
			return "", fmt.Errorf(
				"waffo pancake refund subscription buyer identity mismatch for tradeNo=%s: expected=%q actual=%q",
				tradeNo,
				expectedIdentity,
				actualIdentity,
			)
		}
	}
	return tradeNo, nil
}

// Deterministic default names for "+ Create": stable bodies mean stable
// X-Idempotency-Key, which lets Pancake dedupe retries server-side.
const (
	defaultWaffoPancakeStoreName   = "lmm-forge-store"
	defaultWaffoPancakeProductName = "lmm-forge-wallet-topup"
)

// CreateWaffoPancakePrimaryStore creates a Pancake Store using in-flight
// (not-yet-persisted) credentials and returns the new store ID.
func CreateWaffoPancakePrimaryStore(ctx context.Context, merchantID, privateKey string) (string, error) {
	client, err := newWaffoPancakeClientFromCreds(merchantID, privateKey)
	if err != nil {
		return "", err
	}
	storeRes, err := client.Stores.Create(ctx, pancake.CreateStoreParams{
		Name: defaultWaffoPancakeStoreName,
	})
	if err != nil {
		return "", fmt.Errorf("create Waffo Pancake store: %w", err)
	}
	return storeRes.Store.ID, nil
}

type waffoPancakeProductPublication struct {
	ID             string                       `json:"id"`
	Status         pancake.ProductVersionStatus `json:"status"`
	HasProdVersion bool                         `json:"hasProdVersion"`
}

type waffoPancakeProductPublicationQuery struct {
	OnetimeProduct *waffoPancakeProductPublication `json:"onetimeProduct"`
}

// waffoPancakeProductHasLiveVersion distinguishes the two valid API-key
// workflows. A test key creates a test-only version which still needs Publish;
// a production key creates the production version directly, so calling the
// test→production Publish endpoint would incorrectly fail with
// "No test version found".
func waffoPancakeProductHasLiveVersion(ctx context.Context, client *pancake.Client, productID string) (bool, error) {
	response, err := pancake.GraphQLQuery[waffoPancakeProductPublicationQuery](ctx, client, pancake.GraphQLParams{
		// Pancake's deployed schema currently declares onetimeProduct.id as
		// String!, despite older SDK documentation showing ID!.
		Query: `query ($id: String!) {
			onetimeProduct(id: $id) {
				id
				status
				hasProdVersion
			}
		}`,
		Variables: map[string]any{"id": productID},
	})
	if err != nil {
		return false, fmt.Errorf("query Waffo Pancake product publication: %w", err)
	}
	if len(response.Errors) > 0 {
		return false, fmt.Errorf("waffo pancake product publication query returned %d errors: %s",
			len(response.Errors), response.Errors[0].Message)
	}
	product := response.Data.OnetimeProduct
	if product == nil || strings.TrimSpace(product.ID) != productID {
		return false, nil
	}
	return product.HasProdVersion && product.Status == pancake.ProductVersionStatusActive, nil
}

func ensureWaffoPancakeProductPublished(ctx context.Context, client *pancake.Client, productID string) error {
	if ready, err := waffoPancakeProductHasLiveVersion(ctx, client, productID); err == nil && ready {
		return nil
	}

	published, err := client.OnetimeProducts.Publish(ctx, pancake.PublishOnetimeProductParams{ID: productID})
	if err != nil {
		// A production-bound API key creates an already-live product. Recheck the
		// authoritative read model before surfacing Publish's expected
		// "No test version found" response. The same reconciliation also covers
		// a lost Publish response after the provider committed it.
		if ready, verifyErr := waffoPancakeProductHasLiveVersion(ctx, client, productID); verifyErr == nil && ready {
			return nil
		} else if verifyErr != nil {
			return fmt.Errorf("%w (verify published product: %v)", err, verifyErr)
		}
		return err
	}
	if published == nil || strings.TrimSpace(published.Product.ID) != productID {
		return fmt.Errorf("published Waffo Pancake product id mismatch")
	}
	if published.Product.Status != pancake.ProductVersionStatusActive {
		return fmt.Errorf("published Waffo Pancake product is not active")
	}
	return nil
}

// WaffoPancakeBillingPeriodForDuration maps local entitlement duration to the
// recurring cadences Pancake actually supports. Refuse an approximate mapping:
// charging monthly for a 30-day/custom local plan would drift over time.
func WaffoPancakeBillingPeriodForDuration(durationUnit string, durationValue int) (pancake.BillingPeriod, error) {
	switch strings.ToLower(strings.TrimSpace(durationUnit)) {
	case "day":
		if durationValue == 7 {
			return pancake.BillingPeriodWeekly, nil
		}
	case "month":
		switch durationValue {
		case 1:
			return pancake.BillingPeriodMonthly, nil
		case 3:
			return pancake.BillingPeriodQuarterly, nil
		case 12:
			return pancake.BillingPeriodYearly, nil
		}
	case "year":
		if durationValue == 1 {
			return pancake.BillingPeriodYearly, nil
		}
	}
	return "", fmt.Errorf("Waffo Pancake recurring billing supports exactly 7 days, 1 month, 3 months, 12 months, or 1 year")
}

func ensureWaffoPancakeSubscriptionProductPublished(ctx context.Context, client *pancake.Client, storeID, productID string) error {
	published, err := client.SubscriptionProducts.Publish(ctx, pancake.PublishSubscriptionProductParams{ID: productID})
	if err == nil {
		if published == nil || strings.TrimSpace(published.Product.ID) != productID {
			return fmt.Errorf("published Waffo Pancake subscription product id mismatch")
		}
		if published.Product.Status != pancake.ProductVersionStatusActive {
			return fmt.Errorf("published Waffo Pancake subscription product is not active")
		}
		return nil
	}

	// Production-bound keys can create an already-live product and reject a
	// second Publish call. Reconcile against the provider read model before
	// treating that expected response as a failure.
	catalog, verifyErr := listWaffoPancakeCatalogWithClient(ctx, client)
	if verifyErr == nil {
		for _, store := range catalog.Stores {
			if strings.TrimSpace(store.ID) != strings.TrimSpace(storeID) {
				continue
			}
			for _, product := range store.SubscriptionProducts {
				if strings.TrimSpace(product.ID) == productID && strings.EqualFold(strings.TrimSpace(product.Status), "active") {
					return nil
				}
			}
		}
	}
	if verifyErr != nil {
		return fmt.Errorf("%w (verify published subscription product: %v)", err, verifyErr)
	}
	return err
}

// CreateWaffoPancakeProductForPlan mints a live recurring Pancake
// SubscriptionProduct. amount is a real USD settlement price, never a platform
// balance amount.
func CreateWaffoPancakeProductForPlan(ctx context.Context, merchantID, privateKey, storeID, name, amount, durationUnit string, durationValue int, returnURL string) (string, error) {
	storeID = strings.TrimSpace(storeID)
	if storeID == "" {
		return "", fmt.Errorf("store id is required to create a product")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("plan name is required")
	}
	amount = strings.TrimSpace(amount)
	if amount == "" {
		return "", fmt.Errorf("plan price is required")
	}
	billingPeriod, err := WaffoPancakeBillingPeriodForDuration(durationUnit, durationValue)
	if err != nil {
		return "", err
	}
	client, err := newWaffoPancakeClientFromCreds(merchantID, privateKey)
	if err != nil {
		return "", err
	}
	prodRes, err := client.SubscriptionProducts.Create(ctx, pancake.CreateSubscriptionProductParams{
		StoreID:       storeID,
		Name:          name,
		BillingPeriod: billingPeriod,
		Prices: pancake.Prices{
			"USD": {
				Amount:      amount,
				TaxCategory: pancake.TaxCategory("saas"),
			},
		},
		SuccessURL: optionalString(strings.TrimSpace(returnURL)),
	})
	if err != nil {
		return "", fmt.Errorf("create Waffo Pancake subscription product: %w", err)
	}
	productID := strings.TrimSpace(prodRes.Product.ID)
	if productID == "" {
		return "", fmt.Errorf("created Waffo Pancake subscription product has no id")
	}
	if err := ensureWaffoPancakeSubscriptionProductPublished(ctx, client, storeID, productID); err != nil {
		return "", fmt.Errorf("publish Waffo Pancake subscription product: %w", err)
	}
	return productID, nil
}

// CreateWaffoPancakeOneTimeProductForPlan mints a live one-time product for
// a plan. Checkout freezes the same settlement amount in its price snapshot.
func CreateWaffoPancakeOneTimeProductForPlan(ctx context.Context, merchantID, privateKey, storeID, name, amount, returnURL string) (string, error) {
	storeID = strings.TrimSpace(storeID)
	if storeID == "" {
		return "", fmt.Errorf("store id is required to create a product")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("plan name is required")
	}
	parsedAmount, err := decimal.NewFromString(strings.TrimSpace(amount))
	if err != nil || !parsedAmount.IsPositive() {
		return "", fmt.Errorf("plan price must be positive")
	}
	client, err := newWaffoPancakeClientFromCreds(merchantID, privateKey)
	if err != nil {
		return "", err
	}
	prodRes, err := client.OnetimeProducts.Create(ctx, pancake.CreateOnetimeProductParams{
		StoreID: storeID,
		Name:    name,
		Prices: pancake.Prices{
			"USD": {
				Amount:      parsedAmount.StringFixed(2),
				TaxCategory: pancake.TaxCategory("saas"),
			},
		},
		SuccessURL: optionalString(strings.TrimSpace(returnURL)),
	})
	if err != nil {
		return "", fmt.Errorf("create Waffo Pancake one-time product: %w", err)
	}
	productID := strings.TrimSpace(prodRes.Product.ID)
	if productID == "" {
		return "", fmt.Errorf("created Waffo Pancake one-time product has no id")
	}
	if err := ensureWaffoPancakeProductPublished(ctx, client, productID); err != nil {
		return "", fmt.Errorf("publish Waffo Pancake one-time product: %w", err)
	}
	return productID, nil
}

// CreateWaffoPancakePrimaryProduct mints a live wallet-top-up
// OnetimeProduct under storeID. Per-checkout price overrides via PriceSnapshot
// are what make the "1.00" seed price irrelevant at runtime.
func CreateWaffoPancakePrimaryProduct(ctx context.Context, merchantID, privateKey, storeID, returnURL string) (string, error) {
	return CreateWaffoPancakeOneTimeProductForPlan(
		ctx,
		merchantID,
		privateKey,
		storeID,
		defaultWaffoPancakeProductName,
		"1.00",
		returnURL,
	)
}

// WaffoPancakePairResult is the response of CreateWaffoPancakePrimaryPair.
// When OrphanStore is true the store was created but the product wasn't,
// so the caller can surface a partial-failure message with StoreID.
type WaffoPancakePairResult struct {
	StoreID     string
	StoreName   string
	ProductID   string
	ProductName string
	OrphanStore bool
}

// CreateWaffoPancakePrimaryPair mints a Store + OnetimeProduct in one
// round-trip — the canonical "+ Create" entry point. Nothing is persisted
// to settings; the operator's final Save commits the chosen IDs.
func CreateWaffoPancakePrimaryPair(ctx context.Context, merchantID, privateKey, returnURL string) (*WaffoPancakePairResult, error) {
	storeID, err := CreateWaffoPancakePrimaryStore(ctx, merchantID, privateKey)
	if err != nil {
		return nil, err
	}
	productID, err := CreateWaffoPancakePrimaryProduct(ctx, merchantID, privateKey, storeID, returnURL)
	if err != nil {
		return &WaffoPancakePairResult{
			StoreID:     storeID,
			StoreName:   defaultWaffoPancakeStoreName,
			OrphanStore: true,
		}, fmt.Errorf("store created at %s but product creation failed: %w", storeID, err)
	}
	return &WaffoPancakePairResult{
		StoreID:     storeID,
		StoreName:   defaultWaffoPancakeStoreName,
		ProductID:   productID,
		ProductName: defaultWaffoPancakeProductName,
	}, nil
}

// SaveWaffoPancakeConfig persists the operator-controlled fields atomically
// at the end of the configuration flow via model.UpdateOptionsBulk (single
// DB transaction). A blank privateKey is treated as "keep current"
// (Stripe-style API-secret UX) and is omitted from the bulk payload.
func SaveWaffoPancakeConfig(ctx context.Context, merchantID, privateKey, returnURL, storeID, productID string) error {
	merchantID = strings.TrimSpace(merchantID)
	if merchantID == "" {
		merchantID, _ = WaffoPancakeCredentials()
	}
	storeID = strings.TrimSpace(storeID)
	productID = strings.TrimSpace(productID)
	if merchantID == "" || storeID == "" || productID == "" {
		return fmt.Errorf("merchant id, store id, and product id are required to save")
	}
	values := map[string]string{
		"WaffoPancakeMerchantID": merchantID,
		"WaffoPancakeReturnURL":  strings.TrimSpace(returnURL),
		"WaffoPancakeStoreID":    storeID,
		"WaffoPancakeProductID":  productID,
	}
	if pk := strings.TrimSpace(privateKey); pk != "" {
		values["WaffoPancakePrivateKey"] = pk
	}
	if err := model.UpdateOptionsBulk(values); err != nil {
		return fmt.Errorf("persist Waffo Pancake config: %w", err)
	}
	return nil
}

type WaffoPancakeCatalogProduct struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Status        string `json:"status"`
	BillingPeriod string `json:"billingPeriod,omitempty"`
	ProductType   string `json:"product_type,omitempty"`
}

// WaffoPancakeCatalogStore keeps one-time wallet products and recurring plan
// products in separate collections so an admin cannot bind the wrong type.
type WaffoPancakeCatalogStore struct {
	ID                   string                       `json:"id"`
	Name                 string                       `json:"name"`
	Status               string                       `json:"status"`
	ProdEnabled          bool                         `json:"prodEnabled"`
	OnetimeProducts      []WaffoPancakeCatalogProduct `json:"onetimeProducts"`
	SubscriptionProducts []WaffoPancakeCatalogProduct `json:"subscriptionProducts"`
}

type WaffoPancakeCatalog struct {
	Stores []WaffoPancakeCatalogStore `json:"stores"`
}

type waffoPancakeStoresQuery struct {
	Stores []WaffoPancakeCatalogStore `json:"stores"`
}

type waffoPancakeProductsQuery struct {
	OnetimeProducts      []WaffoPancakeCatalogProduct `json:"onetimeProducts"`
	SubscriptionProducts []WaffoPancakeCatalogProduct `json:"subscriptionProducts"`
}

func listWaffoPancakeCatalogWithClient(ctx context.Context, client *pancake.Client) (*WaffoPancakeCatalog, error) {
	storesResponse, err := pancake.GraphQLQuery[waffoPancakeStoresQuery](ctx, client, pancake.GraphQLParams{
		Query: `query {
			stores {
				id
				name
				status
				prodEnabled
			}
		}`,
	})
	if err != nil {
		return nil, fmt.Errorf("query Waffo Pancake stores: %w", err)
	}
	if len(storesResponse.Errors) > 0 {
		return nil, fmt.Errorf("waffo pancake stores query returned %d errors: %s",
			len(storesResponse.Errors), storesResponse.Errors[0].Message)
	}

	stores := storesResponse.Data.Stores
	for i := range stores {
		storeID := strings.TrimSpace(stores[i].ID)
		if storeID == "" {
			continue
		}
		productsResponse, err := pancake.GraphQLQuery[waffoPancakeProductsQuery](ctx, client, pancake.GraphQLParams{
			Query: `query ($storeId: String!) {
				onetimeProducts(storeId: $storeId, filter: { status: { eq: "active" } }) {
					id
					name
					status
				}
				subscriptionProducts(storeId: $storeId, filter: { status: { eq: "active" } }) {
					id
					name
					status
					billingPeriod
				}
			}`,
			Variables: map[string]any{"storeId": storeID},
		})
		if err != nil {
			return nil, fmt.Errorf("query Waffo Pancake products for store %s: %w", storeID, err)
		}
		if len(productsResponse.Errors) > 0 {
			return nil, fmt.Errorf("waffo pancake products query for store %s returned %d errors: %s",
				storeID, len(productsResponse.Errors), productsResponse.Errors[0].Message)
		}
		stores[i].OnetimeProducts = productsResponse.Data.OnetimeProducts
		stores[i].SubscriptionProducts = productsResponse.Data.SubscriptionProducts
	}

	// Drop non-active products defensively as well as at the GraphQL filter,
	// because providers may return legacy rows or ignore an unsupported filter.
	for i := range stores {
		activeOneTime := stores[i].OnetimeProducts[:0]
		for _, product := range stores[i].OnetimeProducts {
			if strings.EqualFold(strings.TrimSpace(product.Status), "active") {
				activeOneTime = append(activeOneTime, product)
			}
		}
		stores[i].OnetimeProducts = activeOneTime

		activeSubscriptions := stores[i].SubscriptionProducts[:0]
		for _, product := range stores[i].SubscriptionProducts {
			if strings.EqualFold(strings.TrimSpace(product.Status), "active") {
				activeSubscriptions = append(activeSubscriptions, product)
			}
		}
		stores[i].SubscriptionProducts = activeSubscriptions
	}
	return &WaffoPancakeCatalog{Stores: stores}, nil
}

// ListWaffoPancakeCatalog queries Pancake's GraphQL `stores` for the
// merchant's stores + onetime products. A successful call also proves
// the supplied credentials authenticate (doubles as a credential probe).
func ListWaffoPancakeCatalog(ctx context.Context, merchantID, privateKey string) (*WaffoPancakeCatalog, error) {
	client, err := newWaffoPancakeClientFromCreds(merchantID, privateKey)
	if err != nil {
		return nil, fmt.Errorf("prepare Waffo Pancake catalog client: %w", err)
	}
	catalog, err := listWaffoPancakeCatalogWithClient(ctx, client)
	if err != nil {
		return nil, fmt.Errorf("list Waffo Pancake catalog: %w", err)
	}
	return catalog, nil
}

func WaffoPancakeCatalogHasActiveSubscriptionProduct(catalog *WaffoPancakeCatalog, storeID, productID string) bool {
	return waffoPancakeCatalogHasActiveProduct(catalog, storeID, productID, true)
}

func WaffoPancakeCatalogHasActiveOneTimeProduct(catalog *WaffoPancakeCatalog, storeID, productID string) bool {
	return waffoPancakeCatalogHasActiveProduct(catalog, storeID, productID, false)
}

func waffoPancakeCatalogHasActiveProduct(catalog *WaffoPancakeCatalog, storeID, productID string, subscription bool) bool {
	if catalog == nil {
		return false
	}
	storeID = strings.TrimSpace(storeID)
	productID = strings.TrimSpace(productID)
	if storeID == "" || productID == "" {
		return false
	}
	for _, store := range catalog.Stores {
		if strings.TrimSpace(store.ID) != storeID {
			continue
		}
		products := store.OnetimeProducts
		if subscription {
			products = store.SubscriptionProducts
		}
		for _, product := range products {
			if strings.TrimSpace(product.ID) == productID && strings.EqualFold(strings.TrimSpace(product.Status), "active") {
				return true
			}
		}
	}
	return false
}
