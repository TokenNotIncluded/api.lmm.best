package controller

import (
	"strings"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/LIghtJUNction/api.lmm.best/setting"
	"github.com/LIghtJUNction/api.lmm.best/setting/operation_setting"
	"github.com/gin-gonic/gin"
)

// subscriptionConfiguredPaymentMethods reports the operator-configured
// checkout catalog for one plan. It deliberately ignores per-user audience and
// unlock rules so the admin list can distinguish a usable channel from a bare
// product ID whose gateway credentials are incomplete.
func subscriptionConfiguredPaymentMethods(plan *model.SubscriptionPlan) []string {
	if plan == nil || !operation_setting.IsPaymentComplianceConfirmed() {
		return []string{}
	}

	methods := make([]string, 0, len(operation_setting.PayMethods)+4)
	seen := make(map[string]struct{}, cap(methods))
	appendConfigured := func(method string, configured bool) {
		if !configured {
			return
		}
		if _, exists := seen[method]; exists {
			return
		}
		methods = append(methods, method)
		seen[method] = struct{}{}
	}

	appendConfigured(model.PaymentMethodBalance, plan.AllowBalancePay == nil || *plan.AllowBalancePay)
	appendConfigured(model.PaymentMethodStripe,
		strings.TrimSpace(plan.StripePriceId) != "" &&
			(strings.HasPrefix(strings.TrimSpace(setting.StripeApiSecret), "sk_") ||
				strings.HasPrefix(strings.TrimSpace(setting.StripeApiSecret), "rk_")) &&
			strings.TrimSpace(setting.StripeWebhookSecret) != "")
	appendConfigured(model.PaymentMethodCreem,
		strings.TrimSpace(plan.CreemProductId) != "" &&
			strings.TrimSpace(setting.CreemApiKey) != "" &&
			(strings.TrimSpace(setting.CreemWebhookSecret) != "" || setting.CreemTestMode))

	merchantID, privateKey := service.WaffoPancakeCredentials()
	appendConfigured(model.PaymentMethodWaffoPancake,
		strings.TrimSpace(plan.WaffoPancakeProductId) != "" &&
			strings.TrimSpace(merchantID) != "" && strings.TrimSpace(privateKey) != "")

	// Generic ePay methods are global and need no product ID on the plan because
	// checkout sends the plan amount directly.
	if isEpayTopUpEnabled() {
		for _, configured := range operation_setting.PayMethods {
			method := strings.TrimSpace(configured["type"])
			if method == "" || method == model.PaymentMethodStripe ||
				method == model.PaymentMethodCreem || method == model.PaymentMethodWaffo ||
				method == model.PaymentMethodWaffoPancake || method == model.PaymentMethodBalance {
				continue
			}
			appendConfigured(method, true)
		}
	}

	return methods
}

// subscriptionPaymentMethods filters the configured plan catalog through the
// signed-in user's payment restrictions, audience and unlock rules.
func subscriptionPaymentMethods(user *model.User, plan *model.SubscriptionPlan, now time.Time) []string {
	if user == nil || plan == nil {
		return []string{}
	}

	configured := subscriptionConfiguredPaymentMethods(plan)
	methods := make([]string, 0, len(configured))
	for _, method := range configured {
		if method == model.PaymentMethodBalance {
			// Balance checkout is subject to the same payment restriction gate as
			// its endpoint. Explicitly-audienced external methods remain eligible.
			if !model.IsPaymentRestricted(user) {
				methods = append(methods, method)
			}
			continue
		}
		if isPaymentMethodAvailableForUser(user, method, now) {
			methods = append(methods, method)
		}
	}
	return methods
}

// requireSubscriptionPaymentMethodAvailable keeps the checkout endpoint and
// the public plan catalog on the same server-side source of truth. A provider
// may be globally configured for wallet top-ups but still be unavailable for
// this plan (missing its product ID) or for this user (audience/unlock rules).
func requireSubscriptionPaymentMethodAvailable(c *gin.Context, plan *model.SubscriptionPlan, method string) bool {
	if plan == nil {
		common.ApiErrorMsg(c, "套餐不存在")
		return false
	}

	var user *model.User
	if cached, ok := c.Get("payment_user"); ok {
		user, _ = cached.(*model.User)
	}
	if user == nil {
		var err error
		user, err = model.GetUserById(c.GetInt("id"), false)
		if err != nil || user == nil {
			common.ApiErrorMsg(c, "获取用户信息失败")
			return false
		}
	}

	for _, available := range subscriptionPaymentMethods(user, plan, time.Now()) {
		if available == method {
			return true
		}
	}
	common.ApiErrorMsg(c, "该支付方式不可用于此套餐")
	return false
}
