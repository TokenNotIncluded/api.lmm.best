package controller

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/setting/operation_setting"
	"github.com/LIghtJUNction/api.lmm.best/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// ---- Shared types ----

func waffoPancakeProductMustBeRecreated(existing, next *model.SubscriptionPlan) bool {
	if existing == nil || next == nil {
		return false
	}
	existingProductID := strings.TrimSpace(existing.WaffoPancakeProductId)
	nextProductID := strings.TrimSpace(next.WaffoPancakeProductId)
	if existingProductID == "" || existingProductID != nextProductID {
		return false
	}
	existingProductType := model.NormalizeWaffoPancakeProductType(existing.WaffoPancakeProductType)
	nextProductType := model.NormalizeWaffoPancakeProductType(next.WaffoPancakeProductType)
	if existingProductType != nextProductType ||
		existing.PriceAmount != next.PriceAmount ||
		!strings.EqualFold(strings.TrimSpace(existing.Currency), strings.TrimSpace(next.Currency)) {
		return true
	}
	return nextProductType == model.WaffoPancakeProductTypeSubscription &&
		(existing.DurationUnit != next.DurationUnit ||
			existing.DurationValue != next.DurationValue ||
			existing.CustomSeconds != next.CustomSeconds)
}

type SubscriptionPlanDTO struct {
	Plan model.SubscriptionPlan `json:"plan"`
	// PaymentMethods is user-authorized in the public catalog and operator-
	// configured in the admin catalog. Both views require usable gateway
	// credentials rather than trusting a bare provider product ID.
	PaymentMethods    []string `json:"payment_methods"`
	BalancePriceQuota int64    `json:"balance_price_quota"`
}

func normalizeSubscriptionFiatCurrency(value string) (string, error) {
	currency := strings.ToUpper(strings.TrimSpace(value))
	if currency == "" {
		currency = "CNY"
	}
	if currency != "CNY" && currency != "USD" {
		return "", fmt.Errorf("subscription plan currency must be CNY or USD")
	}
	return currency, nil
}

func subscriptionPaymentMethodsWithBalanceQuote(plan *model.SubscriptionPlan, methods []string) ([]string, int64) {
	quota, err := model.SubscriptionBalanceQuota(plan)
	if err == nil && quota > 0 {
		return methods, quota
	}
	filtered := make([]string, 0, len(methods))
	for _, method := range methods {
		if method != model.PaymentMethodBalance {
			filtered = append(filtered, method)
		}
	}
	return filtered, 0
}

type BillingPreferenceRequest struct {
	BillingPreference string `json:"billing_preference"`
}

type SubscriptionBalancePayRequest struct {
	PlanId int `json:"plan_id"`
}

// ---- User APIs ----

func GetSubscriptionPlans(c *gin.Context) {
	if !operation_setting.IsPaymentComplianceConfirmed() {
		common.ApiSuccess(c, []SubscriptionPlanDTO{})
		return
	}

	user, err := model.GetUserById(c.GetInt("id"), false)
	if err != nil || user == nil {
		common.ApiErrorMsg(c, "获取用户信息失败")
		return
	}

	var plans []model.SubscriptionPlan
	if err := model.DB.Where("enabled = ? AND archived_at = 0", true).Order("sort_order desc, id desc").Find(&plans).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	result := make([]SubscriptionPlanDTO, 0, len(plans))
	for _, p := range plans {
		p.NormalizeDefaults()
		methods, balancePriceQuota := subscriptionPaymentMethodsWithBalanceQuote(
			&p,
			subscriptionPaymentMethods(user, &p, time.Now()),
		)
		result = append(result, SubscriptionPlanDTO{
			Plan:              p,
			PaymentMethods:    methods,
			BalancePriceQuota: balancePriceQuota,
		})
	}
	common.ApiSuccess(c, result)
}

func GetSubscriptionSelf(c *gin.Context) {
	userId := c.GetInt("id")
	settingMap, _ := model.GetUserSetting(userId, false)
	pref := common.NormalizeBillingPreference(settingMap.BillingPreference)

	// Get all subscriptions (including expired)
	allSubscriptions, err := model.GetAllUserSubscriptions(userId)
	if err != nil {
		allSubscriptions = []model.SubscriptionSummary{}
	}

	// Get active subscriptions for backward compatibility
	activeSubscriptions, err := model.GetAllActiveUserSubscriptions(userId)
	if err != nil {
		activeSubscriptions = []model.SubscriptionSummary{}
	}

	common.ApiSuccess(c, gin.H{
		"billing_preference": pref,
		"subscriptions":      activeSubscriptions, // all active subscriptions
		"all_subscriptions":  allSubscriptions,    // all subscriptions including expired
	})
}

func UpdateSubscriptionPreference(c *gin.Context) {
	userId := c.GetInt("id")
	var req BillingPreferenceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiErrorMsg(c, "参数错误")
		return
	}
	pref := common.NormalizeBillingPreference(req.BillingPreference)

	user, err := model.GetUserById(userId, true)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	current := user.GetSetting()
	current.BillingPreference = pref
	if err := model.UpdateUserSetting(user.Id, current); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"billing_preference": pref})
}

func SubscriptionRequestBalancePay(c *gin.Context) {
	if !requirePaymentCompliance(c) {
		return
	}

	userId := c.GetInt("id")
	var req SubscriptionBalancePayRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.PlanId <= 0 {
		common.ApiErrorMsg(c, "参数错误")
		return
	}

	if err := model.PurchaseSubscriptionWithBalance(userId, req.PlanId); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, nil)
}

// ---- Admin APIs ----

const enabledSubscriptionPlanPaymentMethodRequiredMessage = "套餐启用前至少需要一种可用支付方式"

func enabledSubscriptionPlanHasConfiguredPaymentMethod(plan *model.SubscriptionPlan) bool {
	return plan != nil && (!plan.Enabled || len(subscriptionConfiguredPaymentMethods(plan)) > 0)
}

func subscriptionPlanPaymentMethodRequired(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"success": false,
		"code":    "SUBSCRIPTION_PLAN_PAYMENT_METHOD_REQUIRED",
		"message": enabledSubscriptionPlanPaymentMethodRequiredMessage,
	})
}

func AdminListSubscriptionPlans(c *gin.Context) {
	var plans []model.SubscriptionPlan
	query := model.DB
	includeArchived := c.Query("include_archived")
	if includeArchived != "1" && !strings.EqualFold(includeArchived, "true") {
		query = query.Where("archived_at = 0")
	}
	if err := query.Order("sort_order desc, id desc").Find(&plans).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	result := make([]SubscriptionPlanDTO, 0, len(plans))
	for _, p := range plans {
		p.NormalizeDefaults()
		methods, balancePriceQuota := subscriptionPaymentMethodsWithBalanceQuote(
			&p,
			subscriptionConfiguredPaymentMethods(&p),
		)
		result = append(result, SubscriptionPlanDTO{
			Plan:              p,
			PaymentMethods:    methods,
			BalancePriceQuota: balancePriceQuota,
		})
	}
	common.ApiSuccess(c, result)
}

type AdminUpsertSubscriptionPlanRequest struct {
	Plan model.SubscriptionPlan `json:"plan"`
}

func AdminCreateSubscriptionPlan(c *gin.Context) {
	if !requirePaymentCompliance(c) {
		return
	}

	var req AdminUpsertSubscriptionPlanRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiErrorMsg(c, "参数错误")
		return
	}
	req.Plan.Id = 0
	req.Plan.ArchivedAt = 0
	if strings.TrimSpace(req.Plan.Title) == "" {
		common.ApiErrorMsg(c, "套餐标题不能为空")
		return
	}
	if req.Plan.PriceAmount < 0 {
		common.ApiErrorMsg(c, "价格不能为负数")
		return
	}
	if req.Plan.PriceAmount > 9999 {
		common.ApiErrorMsg(c, "价格不能超过9999")
		return
	}
	planCurrency, currencyErr := normalizeSubscriptionFiatCurrency(req.Plan.Currency)
	if currencyErr != nil {
		common.ApiErrorMsg(c, "套餐价格币种必须为 CNY 或 USD")
		return
	}
	req.Plan.Currency = planCurrency
	req.Plan.PriceCurrencyVersion = 1
	if req.Plan.AllowBalancePay == nil {
		req.Plan.AllowBalancePay = common.GetPointer(true)
	}
	if req.Plan.AllowWalletOverflow == nil {
		req.Plan.AllowWalletOverflow = common.GetPointer(true)
	}
	if req.Plan.DurationUnit == "" {
		req.Plan.DurationUnit = model.SubscriptionDurationMonth
	}
	if req.Plan.DurationValue <= 0 && req.Plan.DurationUnit != model.SubscriptionDurationCustom {
		req.Plan.DurationValue = 1
	}
	if req.Plan.MaxPurchasePerUser < 0 {
		common.ApiErrorMsg(c, "购买上限不能为负数")
		return
	}
	if req.Plan.TotalAmount < 0 {
		common.ApiErrorMsg(c, "总额度不能为负数")
		return
	}
	req.Plan.UpgradeGroup = strings.TrimSpace(req.Plan.UpgradeGroup)
	if req.Plan.UpgradeGroup != "" {
		if _, ok := ratio_setting.GetGroupRatioCopy()[req.Plan.UpgradeGroup]; !ok {
			common.ApiErrorMsg(c, "升级分组不存在")
			return
		}
	}
	req.Plan.DowngradeGroup = strings.TrimSpace(req.Plan.DowngradeGroup)
	if req.Plan.DowngradeGroup != "" {
		if _, ok := ratio_setting.GetGroupRatioCopy()[req.Plan.DowngradeGroup]; !ok {
			common.ApiErrorMsg(c, "降级分组不存在")
			return
		}
	}
	req.Plan.QuotaResetPeriod = model.NormalizeResetPeriod(req.Plan.QuotaResetPeriod)
	if req.Plan.QuotaResetPeriod == model.SubscriptionResetCustom && req.Plan.QuotaResetCustomSeconds <= 0 {
		common.ApiErrorMsg(c, "自定义重置周期需大于0秒")
		return
	}
	req.Plan.StripePriceId = strings.TrimSpace(req.Plan.StripePriceId)
	req.Plan.CreemProductId = strings.TrimSpace(req.Plan.CreemProductId)
	req.Plan.WaffoPancakeProductId = strings.TrimSpace(req.Plan.WaffoPancakeProductId)
	req.Plan.WaffoPancakeProductType = model.NormalizeWaffoPancakeProductType(req.Plan.WaffoPancakeProductType)
	if !enabledSubscriptionPlanHasConfiguredPaymentMethod(&req.Plan) {
		subscriptionPlanPaymentMethodRequired(c)
		return
	}
	err := model.DB.Create(&req.Plan).Error
	if err != nil {
		common.ApiError(c, err)
		return
	}
	model.InvalidateSubscriptionPlanCache(req.Plan.Id)
	common.ApiSuccess(c, req.Plan)
}

func AdminUpdateSubscriptionPlan(c *gin.Context) {
	if !requirePaymentCompliance(c) {
		return
	}

	id, _ := strconv.Atoi(c.Param("id"))
	if id <= 0 {
		common.ApiErrorMsg(c, "无效的ID")
		return
	}
	existingPlan, lookupErr := model.GetSubscriptionPlanById(id)
	if lookupErr != nil {
		common.ApiError(c, lookupErr)
		return
	}
	if existingPlan.ArchivedAt > 0 {
		common.ApiErrorMsg(c, "Archived subscription plans must be restored before they can be edited")
		return
	}
	var req AdminUpsertSubscriptionPlanRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiErrorMsg(c, "参数错误")
		return
	}
	if req.Plan.AllowBalancePay == nil {
		req.Plan.AllowBalancePay = existingPlan.AllowBalancePay
		if req.Plan.AllowBalancePay == nil {
			req.Plan.AllowBalancePay = common.GetPointer(true)
		}
	}
	if req.Plan.AllowWalletOverflow == nil {
		req.Plan.AllowWalletOverflow = existingPlan.AllowWalletOverflow
		if req.Plan.AllowWalletOverflow == nil {
			req.Plan.AllowWalletOverflow = common.GetPointer(true)
		}
	}
	if strings.TrimSpace(req.Plan.Title) == "" {
		common.ApiErrorMsg(c, "套餐标题不能为空")
		return
	}
	if req.Plan.PriceAmount < 0 {
		common.ApiErrorMsg(c, "价格不能为负数")
		return
	}
	if req.Plan.PriceAmount > 9999 {
		common.ApiErrorMsg(c, "价格不能超过9999")
		return
	}
	req.Plan.Id = id
	planCurrency, currencyErr := normalizeSubscriptionFiatCurrency(req.Plan.Currency)
	if currencyErr != nil {
		common.ApiErrorMsg(c, "套餐价格币种必须为 CNY 或 USD")
		return
	}
	req.Plan.Currency = planCurrency
	req.Plan.PriceCurrencyVersion = 1
	if req.Plan.DurationUnit == "" {
		req.Plan.DurationUnit = model.SubscriptionDurationMonth
	}
	if req.Plan.DurationValue <= 0 && req.Plan.DurationUnit != model.SubscriptionDurationCustom {
		req.Plan.DurationValue = 1
	}
	if req.Plan.MaxPurchasePerUser < 0 {
		common.ApiErrorMsg(c, "购买上限不能为负数")
		return
	}
	if req.Plan.TotalAmount < 0 {
		common.ApiErrorMsg(c, "总额度不能为负数")
		return
	}
	req.Plan.UpgradeGroup = strings.TrimSpace(req.Plan.UpgradeGroup)
	if req.Plan.UpgradeGroup != "" {
		if _, ok := ratio_setting.GetGroupRatioCopy()[req.Plan.UpgradeGroup]; !ok {
			common.ApiErrorMsg(c, "升级分组不存在")
			return
		}
	}
	req.Plan.DowngradeGroup = strings.TrimSpace(req.Plan.DowngradeGroup)
	if req.Plan.DowngradeGroup != "" {
		if _, ok := ratio_setting.GetGroupRatioCopy()[req.Plan.DowngradeGroup]; !ok {
			common.ApiErrorMsg(c, "降级分组不存在")
			return
		}
	}
	req.Plan.QuotaResetPeriod = model.NormalizeResetPeriod(req.Plan.QuotaResetPeriod)
	if req.Plan.QuotaResetPeriod == model.SubscriptionResetCustom && req.Plan.QuotaResetCustomSeconds <= 0 {
		common.ApiErrorMsg(c, "自定义重置周期需大于0秒")
		return
	}
	req.Plan.StripePriceId = strings.TrimSpace(req.Plan.StripePriceId)
	req.Plan.CreemProductId = strings.TrimSpace(req.Plan.CreemProductId)
	req.Plan.WaffoPancakeProductId = strings.TrimSpace(req.Plan.WaffoPancakeProductId)
	if strings.TrimSpace(req.Plan.WaffoPancakeProductType) == "" {
		req.Plan.WaffoPancakeProductType = model.NormalizeWaffoPancakeProductType(existingPlan.WaffoPancakeProductType)
	} else {
		req.Plan.WaffoPancakeProductType = model.NormalizeWaffoPancakeProductType(req.Plan.WaffoPancakeProductType)
	}
	if waffoPancakeProductMustBeRecreated(existingPlan, &req.Plan) {
		common.ApiErrorMsg(c, "套餐价格、币种、商品类型或订阅周期已变化，请重新创建并绑定 Waffo Pancake 商品")
		return
	}
	if !enabledSubscriptionPlanHasConfiguredPaymentMethod(&req.Plan) {
		subscriptionPlanPaymentMethodRequired(c)
		return
	}

	err := model.DB.Transaction(func(tx *gorm.DB) error {
		// update plan (allow zero values updates with map)
		updateMap := map[string]interface{}{
			"title":                      req.Plan.Title,
			"subtitle":                   req.Plan.Subtitle,
			"price_amount":               req.Plan.PriceAmount,
			"currency":                   req.Plan.Currency,
			"duration_unit":              req.Plan.DurationUnit,
			"duration_value":             req.Plan.DurationValue,
			"custom_seconds":             req.Plan.CustomSeconds,
			"enabled":                    req.Plan.Enabled,
			"sort_order":                 req.Plan.SortOrder,
			"stripe_price_id":            req.Plan.StripePriceId,
			"creem_product_id":           req.Plan.CreemProductId,
			"waffo_pancake_product_id":   req.Plan.WaffoPancakeProductId,
			"waffo_pancake_product_type": req.Plan.WaffoPancakeProductType,
			"max_purchase_per_user":      req.Plan.MaxPurchasePerUser,
			"total_amount":               req.Plan.TotalAmount,
			"upgrade_group":              req.Plan.UpgradeGroup,
			"downgrade_group":            req.Plan.DowngradeGroup,
			"quota_reset_period":         req.Plan.QuotaResetPeriod,
			"quota_reset_custom_seconds": req.Plan.QuotaResetCustomSeconds,
			"updated_at":                 common.GetTimestamp(),
		}
		if req.Plan.AllowBalancePay != nil {
			updateMap["allow_balance_pay"] = *req.Plan.AllowBalancePay
		}
		if req.Plan.AllowWalletOverflow != nil {
			updateMap["allow_wallet_overflow"] = *req.Plan.AllowWalletOverflow
		}
		updated := tx.Model(&model.SubscriptionPlan{}).Where("id = ? AND archived_at = 0", id).Updates(updateMap)
		if updated.Error != nil {
			return updated.Error
		}
		if updated.RowsAffected == 0 {
			var plan model.SubscriptionPlan
			if err := tx.Select("id", "archived_at").Where("id = ?", id).First(&plan).Error; err != nil {
				return err
			}
			if plan.ArchivedAt > 0 {
				return errors.New("archived subscription plans must be restored before they can be edited")
			}
		}
		return nil
	})
	if err != nil {
		common.ApiError(c, err)
		return
	}
	model.InvalidateSubscriptionPlanCache(id)
	common.ApiSuccess(c, nil)
}

type AdminUpdateSubscriptionPlanStatusRequest struct {
	Enabled *bool `json:"enabled"`
}

func AdminUpdateSubscriptionPlanStatus(c *gin.Context) {
	if !requirePaymentCompliance(c) {
		return
	}

	id, _ := strconv.Atoi(c.Param("id"))
	if id <= 0 {
		common.ApiErrorMsg(c, "无效的ID")
		return
	}
	var req AdminUpdateSubscriptionPlanStatusRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.Enabled == nil {
		common.ApiErrorMsg(c, "参数错误")
		return
	}
	if *req.Enabled {
		plan, err := model.GetSubscriptionPlanById(id)
		if err != nil {
			common.ApiError(c, err)
			return
		}
		if plan.ArchivedAt > 0 {
			common.ApiErrorMsg(c, "Archived subscription plans must be restored before they can be enabled")
			return
		}
		plan.Enabled = true
		if !enabledSubscriptionPlanHasConfiguredPaymentMethod(plan) {
			subscriptionPlanPaymentMethodRequired(c)
			return
		}
	}
	updateQuery := model.DB.Model(&model.SubscriptionPlan{}).Where("id = ?", id)
	if *req.Enabled {
		updateQuery = updateQuery.Where("archived_at = 0")
	}
	updated := updateQuery.Update("enabled", *req.Enabled)
	if updated.Error != nil {
		common.ApiError(c, updated.Error)
		return
	}
	if *req.Enabled && updated.RowsAffected == 0 {
		var plan model.SubscriptionPlan
		if err := model.DB.Select("id", "archived_at").Where("id = ?", id).First(&plan).Error; err != nil {
			common.ApiError(c, err)
			return
		}
		if plan.ArchivedAt > 0 {
			common.ApiErrorMsg(c, "Archived subscription plans must be restored before they can be enabled")
			return
		}
	}
	model.InvalidateSubscriptionPlanCache(id)
	common.ApiSuccess(c, nil)
}

func AdminDeleteSubscriptionPlan(c *gin.Context) {
	if !requirePaymentCompliance(c) {
		return
	}

	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		common.ApiErrorMsg(c, "无效的ID")
		return
	}
	result, err := model.AdminDeleteSubscriptionPlan(id)
	if err != nil {
		switch {
		case errors.Is(err, gorm.ErrRecordNotFound):
			common.ApiErrorMsg(c, "Subscription plan not found")
		default:
			common.ApiError(c, err)
		}
		return
	}
	recordManageAudit(c, "subscription.plan_remove", map[string]interface{}{
		"plan_id":          id,
		"action":           result.Action,
		"cancelled_orders": result.CancelledOrders,
	})
	common.ApiSuccess(c, result)
}

func AdminRestoreSubscriptionPlan(c *gin.Context) {
	if !requirePaymentCompliance(c) {
		return
	}
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		common.ApiErrorMsg(c, "无效的ID")
		return
	}
	plan, err := model.AdminRestoreSubscriptionPlan(id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			common.ApiErrorMsg(c, "Subscription plan not found")
			return
		}
		common.ApiError(c, err)
		return
	}
	recordManageAudit(c, "subscription.plan_restore", map[string]interface{}{"plan_id": id})
	common.ApiSuccess(c, plan)
}

type AdminBindSubscriptionRequest struct {
	UserId int `json:"user_id"`
	PlanId int `json:"plan_id"`
}

func AdminBindSubscription(c *gin.Context) {
	if !requirePaymentCompliance(c) {
		return
	}

	var req AdminBindSubscriptionRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.UserId <= 0 || req.PlanId <= 0 {
		common.ApiErrorMsg(c, "参数错误")
		return
	}
	msg, err := model.AdminBindSubscription(req.UserId, req.PlanId, "")
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if msg != "" {
		common.ApiSuccess(c, gin.H{"message": msg})
		return
	}
	common.ApiSuccess(c, nil)
}

// ---- Admin: user subscription management ----

func AdminListUserSubscriptions(c *gin.Context) {
	userId, _ := strconv.Atoi(c.Param("id"))
	if userId <= 0 {
		common.ApiErrorMsg(c, "无效的用户ID")
		return
	}
	subs, err := model.GetAllUserSubscriptions(userId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, subs)
}

type AdminCreateUserSubscriptionRequest struct {
	PlanId int `json:"plan_id"`
}

// AdminCreateUserSubscription creates a new user subscription from a plan (no payment).
func AdminCreateUserSubscription(c *gin.Context) {
	if !requirePaymentCompliance(c) {
		return
	}

	userId, _ := strconv.Atoi(c.Param("id"))
	if userId <= 0 {
		common.ApiErrorMsg(c, "无效的用户ID")
		return
	}
	var req AdminCreateUserSubscriptionRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.PlanId <= 0 {
		common.ApiErrorMsg(c, "参数错误")
		return
	}
	msg, err := model.AdminBindSubscription(userId, req.PlanId, "")
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if msg != "" {
		common.ApiSuccess(c, gin.H{"message": msg})
		return
	}
	common.ApiSuccess(c, nil)
}

// AdminInvalidateUserSubscription cancels a user subscription immediately.
func AdminInvalidateUserSubscription(c *gin.Context) {
	subId, _ := strconv.Atoi(c.Param("id"))
	if subId <= 0 {
		common.ApiErrorMsg(c, "无效的订阅ID")
		return
	}
	msg, err := model.AdminInvalidateUserSubscription(subId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if msg != "" {
		common.ApiSuccess(c, gin.H{"message": msg})
		return
	}
	common.ApiSuccess(c, nil)
}

// AdminDeleteUserSubscription hard-deletes a user subscription.
func AdminDeleteUserSubscription(c *gin.Context) {
	subId, _ := strconv.Atoi(c.Param("id"))
	if subId <= 0 {
		common.ApiErrorMsg(c, "无效的订阅ID")
		return
	}
	msg, err := model.AdminDeleteUserSubscription(subId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if msg != "" {
		common.ApiSuccess(c, gin.H{"message": msg})
		return
	}
	common.ApiSuccess(c, nil)
}
