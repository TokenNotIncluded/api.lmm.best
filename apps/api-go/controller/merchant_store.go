package controller

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/LIghtJUNction/api.lmm.best/setting/operation_setting"
	"github.com/LIghtJUNction/api.lmm.best/setting/system_setting"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

const merchantStoreDisclaimerText = `Third-party products are sold and delivered by the listed merchant. Please read the description, delivery conditions, price and merchant contact details before ordering. The official badge identifies products sold by a current platform administrator; other products are independent merchant products.

The platform provides listing review, payment records and delivery links. A listing review is not a guarantee of product quality, suitability, legality or continued availability. Contact the merchant first about product issues, and keep your order number and payment record when requesting platform assistance. The platform may pause products or investigate reports.

Digital text and activation codes may be revealed immediately after confirmed payment. Do not share your private delivery link or pickup code. Check the merchant's stated terms before buying; any refund request must be handled according to the applicable order and payment terms.

Payments credited to a merchant's platform balance cannot be withdrawn and may only be used for consumption on the platform. External merchant gateways receive the payment directly, while the platform charges the merchant a service fee in credits.

By accepting, you confirm that you have read these terms and understand that you are purchasing from the named third-party merchant. You can reopen this notice at any time from the shop.`

func merchantStoreRespond(c *gin.Context, value any, err error) {
	c.Header("Cache-Control", "no-store")
	if err == nil {
		common.ApiSuccess(c, value)
		return
	}
	status, code, message := http.StatusInternalServerError, "STORE_UNAVAILABLE", "The shop could not complete this request."
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		status, code, message = http.StatusNotFound, "STORE_NOT_FOUND", "The shop item or order was not found."
	case errors.Is(err, model.ErrMerchantStoreInput):
		status, code, message = http.StatusUnprocessableEntity, "STORE_INVALID_INPUT", "Please check the shop information and amounts."
	case errors.Is(err, model.ErrMerchantStoreRefundUnsupported):
		status, code, message = http.StatusConflict, "STORE_REFUND_EVIDENCE_REQUIRED", "This payment needs verified provider evidence before a partial refund."
	case errors.Is(err, model.ErrMerchantStoreWriterFrozen):
		status, code, message = http.StatusServiceUnavailable, "STORE_UPGRADE_IN_PROGRESS", "Shop updates are in progress. Existing orders remain accessible."
	case errors.Is(err, model.ErrMerchantStoreVariantRequired):
		status, code, message = http.StatusUnprocessableEntity, "STORE_VARIANT_REQUIRED", "Select a product variant before ordering."
	case errors.Is(err, model.ErrMerchantStoreMinimumPrice):
		status, code, message = http.StatusUnprocessableEntity, "STORE_MINIMUM_PRICE", "The product unit price is below the current minimum."
	case errors.Is(err, model.ErrMerchantStoreTestMode):
		status, code, message = http.StatusUnprocessableEntity, "STORE_TEST_MODE", "Exit product test mode before submitting or publishing."
	case errors.Is(err, model.ErrMerchantStorePaymentSelection):
		status, code, message = http.StatusUnprocessableEntity, "STORE_PAYMENT_SELECTION_UNAVAILABLE", "Select only currently enabled merchant payment methods."
	case errors.Is(err, model.ErrMerchantStorePaymentCategoryDisabled):
		status, code, message = http.StatusConflict, "STORE_PAYMENT_CATEGORY_DISABLED", "The merchant has disabled this payment category."
	case errors.Is(err, model.ErrMerchantStoreDenied), errors.Is(err, service.ErrMerchantStorePaymentAccess):
		status, code, message = http.StatusForbidden, "STORE_ACCESS_DENIED", "This shop operation is not available to this account."
	case errors.Is(err, model.ErrMerchantStoreConflict), errors.Is(err, model.ErrMerchantStorePendingLimit):
		status, code, message = http.StatusConflict, "STORE_CONFLICT", "The order or product state has changed. Please refresh."
	case errors.Is(err, model.ErrMerchantStoreBalance):
		status, code, message = http.StatusConflict, "STORE_INSUFFICIENT_BALANCE", "There are not enough credits to complete this operation."
	case errors.Is(err, model.ErrMerchantStoreStock):
		status, code, message = http.StatusConflict, "STORE_OUT_OF_STOCK", "The product has insufficient available stock."
	case errors.Is(err, model.ErrMerchantStorePurchaseLimit):
		status, code, message = http.StatusConflict, "STORE_PURCHASE_LIMIT", "This quantity exceeds the product purchase limit."
	case errors.Is(err, model.ErrMerchantStoreDisclaimer):
		status, code, message = http.StatusConflict, "STORE_DISCLAIMER_REQUIRED", "Read and accept the current merchant disclaimer before ordering."
	case errors.Is(err, model.ErrMerchantStoreSellerTerms):
		status, code, message = http.StatusConflict, "STORE_SELLER_TERMS_REQUIRED", "Read and accept the current seller terms before ordering."
	case errors.Is(err, model.ErrMerchantStoreLoginRequired):
		status, code, message = http.StatusUnauthorized, "STORE_LOGIN_REQUIRED", "Sign in before ordering this product."
	case errors.Is(err, model.ErrMerchantStoreEmailUnverified):
		status, code, message = http.StatusConflict, "STORE_EMAIL_VERIFICATION_REQUIRED", "Verify your current email address before receiving private delivery links."
	case errors.Is(err, model.ErrMerchantStoreEmailVerificationInvalid):
		status, code, message = http.StatusUnprocessableEntity, "STORE_EMAIL_VERIFICATION_INVALID", "The email verification code is invalid or expired."
	case errors.Is(err, model.ErrMerchantStoreEmailVerificationCooldown):
		status, code, message = http.StatusTooManyRequests, "STORE_EMAIL_VERIFICATION_COOLDOWN", "Wait one minute before requesting another email verification code."
	case errors.Is(err, model.ErrMerchantStoreUnavailable):
		status, code, message = http.StatusConflict, "STORE_TRADING_PAUSED", "This product or payment method is currently unavailable."
	case errors.Is(err, model.ErrMerchantStoreDiscountUnavailable):
		status, code, message = http.StatusConflict, "STORE_PROMOTION_UNAVAILABLE", "Store promotion unavailable"
	case errors.Is(err, model.ErrMerchantStoreDiscountLimit):
		status, code, message = http.StatusConflict, "STORE_PROMOTION_LIMIT", "Store promotion limit reached"
	case errors.Is(err, service.ErrMerchantStorePaymentMinimum):
		status, code, message = http.StatusUnprocessableEntity, "STORE_PAYMENT_MINIMUM", "Payment amount is below the gateway minimum; choose balance"
	case errors.Is(err, service.ErrMerchantStorePaymentConfiguration):
		status, code, message = http.StatusConflict, "STORE_PAYMENT_CONFIGURATION", "This payment method needs a valid configuration."
	case errors.Is(err, service.ErrMerchantStorePaymentVerification):
		status, code, message = http.StatusBadRequest, "STORE_PAYMENT_VERIFICATION", "The payment could not be verified."
	case errors.Is(err, service.ErrMerchantStorePaymentNetwork):
		status, code, message = http.StatusUnprocessableEntity, "STORE_PAYMENT_DESTINATION", "The payment gateway must use a public HTTPS destination."
	case errors.Is(err, common.ErrPersistentKeyUnavailable):
		status, code, message = http.StatusServiceUnavailable, "STORE_SECURE_STORAGE_UNAVAILABLE", "Secure shop storage is currently unavailable."
	}
	// Database, gateway, crypto and provider errors can contain credentials or
	// private delivery data. Never serialize their raw error strings.
	response := gin.H{"success": false, "code": code, "message": message}
	var minimum *service.MerchantStorePaymentMinimumError
	if errors.As(err, &minimum) {
		response["order_id"], response["order_status"], response["order_cancelled"] = minimum.OrderID, minimum.OrderStatus, minimum.OrderCancelled
		if !minimum.OrderCancelled {
			response["message"] = "The order or product state has changed. Please refresh."
		}
	}
	c.AbortWithStatusJSON(status, response)
}

func merchantStorePage(c *gin.Context) (int, int, bool) {
	offset, e1 := strconv.Atoi(c.DefaultQuery("offset", "0"))
	limit, e2 := strconv.Atoi(c.DefaultQuery("limit", "30"))
	if e1 != nil || e2 != nil || offset < 0 || offset > 100000 || limit < 1 || limit > 100 {
		merchantStoreRespond(c, nil, model.ErrMerchantStoreInput)
		return 0, 0, false
	}
	return offset, limit, true
}

func merchantStoreList[T any](c *gin.Context, items []T, offset, limit int, err error) {
	if items == nil {
		items = []T{}
	}
	merchantStoreRespond(c, gin.H{"items": items, "offset": offset, "limit": limit, "has_more": len(items) == limit}, err)
}

func publicStoreProduct(p model.MerchantStoreProduct) model.MerchantStoreProduct {
	p.ReviewNote, p.ReviewedBy = "", 0
	service.FilterMerchantStorePublicPaymentMethods(&p)
	return p
}

func ListMerchantStore(c *gin.Context) {
	offset, limit, ok := merchantStorePage(c)
	if !ok {
		return
	}
	sellerID := 0
	if raw, present := c.GetQuery("seller_id"); present || len(c.Request.URL.Query()["seller_id"]) != 0 {
		var parseErr error
		sellerID, parseErr = strconv.Atoi(raw)
		if parseErr != nil || len(c.Request.URL.Query()["seller_id"]) != 1 || sellerID < 1 || int64(sellerID) > 2147483647 || raw != strconv.Itoa(sellerID) {
			merchantStoreRespond(c, nil, model.ErrMerchantStoreInput)
			return
		}
	}
	catalogueQuery, ok := merchantStoreCatalogueQuery(c)
	if !ok {
		return
	}
	var items []model.MerchantStoreProduct
	var err error
	if model.MerchantStoreCatalogueSupported() {
		items, err = model.ListMerchantStoreCatalogue(merchantStoreViewer(c), c.Query("q"), sellerID, offset, limit, catalogueQuery)
	} else {
		// Older floors retain their original read contract; supplied new filters
		// cannot silently turn into client-side filtering over one page.
		if catalogueQuery.Sort != "" || catalogueQuery.Tag != "" || catalogueQuery.Stock != "" || catalogueQuery.AutoDelivery != nil || catalogueQuery.AIProcessing != nil || catalogueQuery.GuestPurchase != nil {
			merchantStoreRespond(c, nil, model.ErrMerchantStoreUnavailable)
			return
		}
		items, err = model.ListMerchantStoreProductsForSellerViewer(merchantStoreViewer(c), c.Query("q"), sellerID, offset, limit)
	}
	for i := range items {
		items[i] = publicStoreProduct(items[i])
	}
	if err != nil {
		merchantStoreRespond(c, nil, err)
		return
	}
	if items == nil {
		items = []model.MerchantStoreProduct{}
	}
	var seller *model.MerchantStorePublicSeller
	if sellerID != 0 {
		seller, err = model.GetMerchantStoreSellerProfileForViewer(merchantStoreViewer(c), sellerID)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			err = nil
		}
	}
	merchantStoreRespond(c, gin.H{"items": items, "offset": offset, "limit": limit, "has_more": len(items) == limit, "seller": seller}, err)
}

func GetPublicMerchantStoreProduct(c *gin.Context) {
	p, err := model.GetMerchantStoreProductForViewer(merchantStoreViewer(c), c.Param("id"))
	if err == nil {
		*p = publicStoreProduct(*p)
		if len(c.Request.Header.Values("X-Store-Guest")) != 0 {
			err = model.PopulateMerchantStoreGuestPurchaseRemaining(merchantStoreGuestHeader(c), p)
		} else {
			err = model.PopulateMerchantStoreBuyerPurchaseRemaining(merchantStoreViewer(c), p)
		}
	}
	merchantStoreRespond(c, p, err)
}

func GetMerchantStoreConfig(c *gin.Context) {
	config, err := model.GetMerchantStoreConfig()
	var presets []model.MerchantStoreLinkPreset
	if err == nil {
		presets, err = model.GetMerchantStoreLinkPresets()
	}
	catalog := service.MerchantStorePlatformPaymentCatalog(availablePaymentMethods(operation_setting.IsPaymentComplianceConfirmed()))
	merchantStoreRespond(c, gin.H{
		"fee_bps": config.FeeBPS, "promotion_quota": config.PromotionQuota, "minimum_unit_price_quota": config.MinimumUnitPriceQuota,
		"product_test_mode_supported":       true,
		"store_catalogue_supported":         model.MerchantStoreCatalogueSupported(),
		"store_collections_supported":       model.MerchantStoreCollectionsSupported(),
		"store_access_supported":            model.MerchantStoreAccessSupported(),
		"product_purchase_limits_supported": model.MerchantStorePurchaseLimitsSupported(),
		"product_link_presets":              presets,
		"linuxdo_units_per_usd":             config.LinuxDOUnitsPerUSD,
		"credits_per_usd":                   common.FixedCreditsPerUSD, "external_minimum_quota": model.MerchantStoreExternalMinimumQuota,
		"disclaimer_version": model.MerchantStoreDisclaimerVersion, "disclaimer_text": merchantStoreDisclaimerText,
		"platform_payment_methods": service.AvailableMerchantStorePlatformMethods(catalog),
		"platform_payment_catalog": catalog,
	}, err)
}

func SaveMerchantStoreLinkPresets(c *gin.Context) {
	var input struct {
		Presets []model.MerchantStoreLinkPreset `json:"presets"`
	}
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil || decoder.Decode(new(any)) != io.EOF {
		merchantStoreRespond(c, nil, model.ErrMerchantStoreInput)
		return
	}
	merchantStoreRespond(c, nil, model.SaveMerchantStoreLinkPresets(c.GetInt("id"), input.Presets))
}

func SetMerchantStoreConfig(c *gin.Context) {
	var input model.MerchantStoreConfigPatch
	if c.ShouldBindJSON(&input) != nil {
		merchantStoreRespond(c, nil, model.ErrMerchantStoreInput)
		return
	}
	merchantStoreRespond(c, nil, model.PatchMerchantStoreConfig(c.GetInt("id"), input))
}

func SetMerchantStorePromotionPrice(c *gin.Context) {
	var input struct {
		PromotionQuota int `json:"promotion_quota"`
	}
	if c.ShouldBindJSON(&input) != nil {
		merchantStoreRespond(c, nil, model.ErrMerchantStoreInput)
		return
	}
	merchantStoreRespond(c, nil, model.SetMerchantStorePromotionPrice(c.GetInt("id"), input.PromotionQuota))
}

func GetMerchantStoreDisclaimer(c *gin.Context) {
	accepted := false
	var err error
	if merchantStoreViewer(c) > 0 {
		accepted, err = model.HasMerchantStoreDisclaimerAcceptance(c.GetInt("id"))
	} else {
		accepted, err = model.HasMerchantStoreGuestDisclaimerAcceptance(merchantStoreGuestHeader(c))
	}
	merchantStoreRespond(c, gin.H{"version": model.MerchantStoreDisclaimerVersion, "text": merchantStoreDisclaimerText, "accepted": accepted}, err)
}

func AcceptMerchantStoreDisclaimer(c *gin.Context) {
	var input struct {
		Version  string `json:"version"`
		Accepted bool   `json:"accepted"`
	}
	if c.ShouldBindJSON(&input) != nil || !input.Accepted {
		merchantStoreRespond(c, nil, model.ErrMerchantStoreInput)
		return
	}
	merchantStoreRespond(c, nil, model.AcceptMerchantStoreDisclaimer(c.GetInt("id"), input.Version))
}

func ListMyMerchantStoreProducts(c *gin.Context) {
	offset, limit, ok := merchantStorePage(c)
	if !ok {
		return
	}
	items, err := model.ListMerchantStoreProducts(c.GetInt("id"), false, offset, limit)
	merchantStoreList(c, items, offset, limit, err)
}

func GetMerchantStoreProductDraft(c *gin.Context) {
	p, err := model.GetMerchantStoreProduct(c.GetInt("id"), c.Param("id"))
	merchantStoreRespond(c, p, err)
}

func GetMerchantStoreProductPreview(c *gin.Context) {
	p, err := model.GetMerchantStoreProductPreview(c.GetInt("id"), c.Param("id"))
	if err == nil {
		err = model.PopulateMerchantStoreBuyerPurchaseRemaining(c.GetInt("id"), p)
	}
	if err == nil {
		*p = publicStoreProduct(*p)
	}
	merchantStoreRespond(c, p, err)
}

func SaveMerchantStoreProduct(c *gin.Context) {
	var input model.MerchantStoreProductInput
	if c.ShouldBindJSON(&input) != nil {
		merchantStoreRespond(c, nil, model.ErrMerchantStoreInput)
		return
	}
	p, err := model.SaveMerchantStoreProduct(c.GetInt("id"), c.Param("id"), input)
	merchantStoreRespond(c, p, err)
}

func SubmitMerchantStoreProduct(c *gin.Context) {
	merchantStoreRespond(c, nil, model.SubmitMerchantStoreProduct(c.GetInt("id"), c.Param("id")))
}

func UnlistMerchantStoreProduct(c *gin.Context) {
	merchantStoreRespond(c, nil, model.UnlistMerchantStoreProduct(c.GetInt("id"), c.Param("id")))
}

func DeleteMerchantStoreProduct(c *gin.Context) {
	merchantStoreRespond(c, nil, model.DeleteMerchantStoreProduct(c.GetInt("id"), c.Param("id")))
}

func SetMerchantStoreProductPaused(c *gin.Context) {
	var input struct {
		Paused bool `json:"paused"`
	}
	if c.ShouldBindJSON(&input) != nil {
		merchantStoreRespond(c, nil, model.ErrMerchantStoreInput)
		return
	}
	merchantStoreRespond(c, nil, model.SetMerchantStoreProductPaused(c.GetInt("id"), c.Param("id"), input.Paused))
}

func AddMerchantStoreInventory(c *gin.Context) {
	var input struct {
		Items []string `json:"items"`
	}
	if c.ShouldBindJSON(&input) != nil {
		merchantStoreRespond(c, nil, model.ErrMerchantStoreInput)
		return
	}
	count, err := model.AddMerchantStoreStock(c.GetInt("id"), c.Param("id"), input.Items)
	merchantStoreRespond(c, gin.H{"added": count}, err)
}

func ListMerchantStoreInventory(c *gin.Context) {
	offset, limit, ok := merchantStorePage(c)
	if !ok {
		return
	}
	items, err := model.ListMerchantStoreStock(c.GetInt("id"), c.Param("id"), offset, limit)
	merchantStoreList(c, items, offset, limit, err)
}

func DeleteMerchantStoreInventory(c *gin.Context) {
	merchantStoreRespond(c, nil, model.RemoveMerchantStoreStock(c.GetInt("id"), c.Param("id"), c.Param("stock_id")))
}

func PurchaseMerchantStorePromotion(c *gin.Context) {
	var input struct {
		Months     int    `json:"months"`
		RequestKey string `json:"request_key"`
	}
	if c.ShouldBindJSON(&input) != nil {
		merchantStoreRespond(c, nil, model.ErrMerchantStoreInput)
		return
	}
	p, err := model.PurchaseMerchantStorePromotion(c.GetInt("id"), c.Param("id"), input.Months, input.RequestKey)
	merchantStoreRespond(c, p, err)
}

func ListMerchantStoreReviews(c *gin.Context) {
	offset, limit, ok := merchantStorePage(c)
	if !ok {
		return
	}
	items, err := model.ListMerchantStoreProducts(c.GetInt("id"), true, offset, limit)
	merchantStoreList(c, items, offset, limit, err)
}

func ReviewMerchantStoreProduct(c *gin.Context) {
	var input struct {
		Approve bool   `json:"approve"`
		Note    string `json:"note"`
	}
	if c.ShouldBindJSON(&input) != nil {
		merchantStoreRespond(c, nil, model.ErrMerchantStoreInput)
		return
	}
	merchantStoreRespond(c, nil, model.ReviewMerchantStoreProduct(c.GetInt("id"), c.Param("id"), input.Approve, input.Note))
}

func CreateMerchantStoreOrder(c *gin.Context) {
	if len(c.Request.Header.Values("X-Store-Guest")) != 0 {
		merchantStoreRespond(c, nil, model.ErrMerchantStoreDenied)
		return
	}
	var input model.MerchantStoreCheckoutInput
	if c.ShouldBindJSON(&input) != nil {
		merchantStoreRespond(c, nil, model.ErrMerchantStoreInput)
		return
	}
	input.BuyerID = c.GetInt("id")
	order, created, err := model.CreateMerchantStoreOrder(input)
	merchantStoreRespond(c, gin.H{"order": order, "created": created}, err)
}

func ListMyMerchantStoreOrders(c *gin.Context) {
	offset, limit, ok := merchantStorePage(c)
	if !ok {
		return
	}
	role := c.DefaultQuery("role", "buyer")
	if role != "buyer" && role != "seller" {
		merchantStoreRespond(c, nil, model.ErrMerchantStoreInput)
		return
	}
	items, err := model.ListMerchantStoreOrders(c.GetInt("id"), role == "seller", offset, limit)
	merchantStoreList(c, items, offset, limit, err)
}

func GetMerchantStoreOrder(c *gin.Context) {
	order, err := model.GetMerchantStoreOrder(c.GetInt("id"), c.Param("id"))
	merchantStoreRespond(c, order, err)
}

func CancelMerchantStoreOrder(c *gin.Context) {
	merchantStoreRespond(c, nil, model.CancelMerchantStoreOrder(c.GetInt("id"), c.Param("id")))
}

func RequestMerchantStorePayment(c *gin.Context) {
	var input struct {
		Currency string `json:"currency"`
	}
	if c.ShouldBindJSON(&input) != nil {
		merchantStoreRespond(c, nil, model.ErrMerchantStoreInput)
		return
	}
	session, err := service.CreateMerchantStorePaymentSession(c.Request.Context(), c.GetInt("id"), c.Param("id"), input.Currency)
	merchantStoreRespond(c, session, err)
}

func ReconcileMerchantStorePayment(c *gin.Context) {
	order, err := service.ReconcileMerchantStoreOrderPayment(c.Request.Context(), c.GetInt("id"), c.Param("id"))
	merchantStoreRespond(c, order, err)
}

func merchantStorePickupURL(token string) (string, error) {
	origin := strings.TrimSuffix(strings.TrimSpace(system_setting.ServerAddress), "/")
	u, err := url.Parse(origin)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" {
		return "", model.ErrMerchantStoreUnavailable
	}
	return origin + "/store/claim/" + url.PathEscape(token), nil
}

func GetMerchantStorePickupLink(c *gin.Context) {
	c.Header("Referrer-Policy", "no-referrer")
	token, err := model.GetMerchantStoreOrderPickupToken(c.GetInt("id"), c.Param("id"))
	link := ""
	if err == nil {
		link, err = merchantStorePickupURL(token)
	}
	merchantStoreRespond(c, gin.H{"pickup_url": link}, err)
}

func InspectMerchantStoreClaim(c *gin.Context) {
	c.Header("Referrer-Policy", "no-referrer")
	metadata, err := model.InspectMerchantStoreClaim(c.Param("token"))
	merchantStoreRespond(c, metadata, err)
}

func ClaimMerchantStoreOrder(c *gin.Context) {
	c.Header("Referrer-Policy", "no-referrer")
	var input struct {
		PickupCode string `json:"pickup_code"`
	}
	if c.ShouldBindJSON(&input) != nil {
		merchantStoreRespond(c, nil, model.ErrMerchantStoreInput)
		return
	}
	claim, err := model.ClaimMerchantStoreOrder(c.Param("token"), input.PickupCode, c.GetInt("id"))
	merchantStoreRespond(c, claim, err)
}

func GetMerchantStorePaymentSettings(c *gin.Context) {
	items, err := service.ListMerchantStorePaymentGateways(c.GetInt("id"))
	if err != nil {
		merchantStoreRespond(c, nil, err)
		return
	}
	user, err := model.GetUserById(c.GetInt("id"), false)
	if err != nil {
		merchantStoreRespond(c, nil, err)
		return
	}
	config, err := model.GetMerchantStoreConfig()
	if err != nil {
		merchantStoreRespond(c, nil, err)
		return
	}
	categories, err := model.GetMerchantStorePaymentCategories(c.GetInt("id"))
	merchantStoreRespond(c, gin.H{"items": items, "categories": categories, "balance_quota": user.Quota, "external_eligible": user.Quota > model.MerchantStoreExternalMinimumQuota, "fee_bps": config.FeeBPS}, err)
}

func SetMerchantStorePaymentCategories(c *gin.Context) {
	var input struct {
		PlatformEnabled *bool `json:"platform_enabled"`
		ExternalEnabled *bool `json:"external_enabled"`
	}
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil || input.PlatformEnabled == nil || input.ExternalEnabled == nil || decoder.Decode(new(any)) != io.EOF {
		merchantStoreRespond(c, nil, model.ErrMerchantStoreInput)
		return
	}
	value := model.MerchantStorePaymentCategories{PlatformEnabled: *input.PlatformEnabled, ExternalEnabled: *input.ExternalEnabled}
	merchantStoreRespond(c, value, model.SetMerchantStorePaymentCategories(c.GetInt("id"), value))
}

func SaveMerchantStorePaymentSettings(c *gin.Context) {
	var input service.MerchantStoreGatewayConfigInput
	if c.ShouldBindJSON(&input) != nil {
		merchantStoreRespond(c, nil, model.ErrMerchantStoreInput)
		return
	}
	view, err := service.SaveMerchantStorePaymentGateway(c.GetInt("id"), input)
	merchantStoreRespond(c, view, err)
}
