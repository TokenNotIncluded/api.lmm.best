package controller

import (
	"net/http"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/LIghtJUNction/api.lmm.best/setting"
	"github.com/LIghtJUNction/api.lmm.best/setting/ratio_setting"

	"github.com/gin-gonic/gin"
)

func filterPricingByUsableGroups(pricing []model.Pricing, usableGroup map[string]string) []model.Pricing {
	if len(pricing) == 0 {
		return pricing
	}
	if len(usableGroup) == 0 {
		return []model.Pricing{}
	}

	filtered := make([]model.Pricing, 0, len(pricing))
	for _, item := range pricing {
		if common.StringsContains(item.EnableGroup, "all") {
			filtered = append(filtered, item)
			continue
		}
		for _, group := range item.EnableGroup {
			if _, ok := usableGroup[group]; ok {
				filtered = append(filtered, item)
				break
			}
		}
	}
	return filtered
}

type pricingView struct {
	pricing     []model.Pricing
	groupRatio  map[string]float64
	usableGroup map[string]string
}

func publicPricingGroupDescriptions(
	pricing []model.Pricing,
	groupRatio map[string]float64,
) map[string]string {
	descriptions := make(map[string]string)
	for group := range groupRatio {
		if group != "all" {
			descriptions[group] = setting.GetUsableGroupDescription(group)
		}
	}
	for _, item := range pricing {
		for _, group := range item.EnableGroup {
			if group == "all" {
				continue
			}
			if _, exists := descriptions[group]; !exists {
				descriptions[group] = setting.GetUsableGroupDescription(group)
			}
		}
	}
	return descriptions
}

func buildPricingView(
	pricing []model.Pricing,
	groupRatio map[string]float64,
	groupDescriptions map[string]string,
	authenticated bool,
) pricingView {
	if authenticated {
		filteredRatio := make(map[string]float64)
		for group, ratio := range groupRatio {
			if _, ok := groupDescriptions[group]; ok {
				filteredRatio[group] = ratio
			}
		}
		return pricingView{
			pricing:     filterPricingByUsableGroups(pricing, groupDescriptions),
			groupRatio:  filteredRatio,
			usableGroup: groupDescriptions,
		}
	}

	representedGroups := make(map[string]struct{})
	allGroupsEnabled := false
	for _, item := range pricing {
		for _, group := range item.EnableGroup {
			if group == "all" {
				allGroupsEnabled = true
				continue
			}
			representedGroups[group] = struct{}{}
		}
	}

	disclosedRatios := make(map[string]float64)
	disclosedGroups := make(map[string]string)
	for group, ratio := range groupRatio {
		if group == "all" {
			continue
		}
		if _, represented := representedGroups[group]; !allGroupsEnabled && !represented {
			continue
		}
		disclosedRatios[group] = ratio
		description := group
		if configuredDescription, ok := groupDescriptions[group]; ok {
			description = configuredDescription
		}
		disclosedGroups[group] = description
	}
	if !allGroupsEnabled {
		for group := range representedGroups {
			if _, disclosed := disclosedRatios[group]; disclosed {
				continue
			}
			disclosedRatios[group] = 1
			description := group
			if configuredDescription, ok := groupDescriptions[group]; ok {
				description = configuredDescription
			}
			disclosedGroups[group] = description
		}
	}

	return pricingView{
		pricing:     pricing,
		groupRatio:  disclosedRatios,
		usableGroup: disclosedGroups,
	}
}

func buildPricingResponse(c *gin.Context, applyTrustDiscount bool) (int, gin.H) {
	pricing := getPricingCache()
	if pricing == nil {
		return http.StatusServiceUnavailable, gin.H{
			"success": false,
			"message": "pricing cache is not ready",
		}
	}
	userId, exists := c.Get("id")
	groupRatio := map[string]float64{}
	var user *model.UserBase
	for s, f := range ratio_setting.GetGroupRatioCopy() {
		groupRatio[s] = f
	}
	var group string
	if exists {
		id, ok := userId.(int)
		if !ok {
			return http.StatusInternalServerError, gin.H{
				"success": false,
				"message": "pricing account context is invalid",
			}
		}
		loadedUser, err := model.GetUserCache(id)
		if err == nil {
			user = loadedUser
			group = loadedUser.Group
			for g := range groupRatio {
				ratio, ok := ratio_setting.GetGroupGroupRatio(group, g)
				if ok {
					groupRatio[g] = ratio
				}
			}
		}
	}

	groupDescriptions := make(map[string]string)
	if exists {
		groupDescriptions = service.GetUserUsableGroups(group)
	} else {
		groupDescriptions = publicPricingGroupDescriptions(pricing, groupRatio)
	}
	view := buildPricingView(pricing, groupRatio, groupDescriptions, exists)

	response := gin.H{
		"success":            true,
		"data":               view.pricing,
		"vendors":            model.GetVendors(),
		"group_ratio":        view.groupRatio,
		"usable_group":       view.usableGroup,
		"supported_endpoint": model.GetSupportedEndpointMap(),
		"auto_groups":        service.GetUserAutoGroup(group),
		"pricing_version":    "a42d372ccf0b5dd13ecf71203521f9d2",
	}
	if applyTrustDiscount && user != nil {
		trust, err := model.GetTrustLevelInfoForUserBase(user)
		if err != nil {
			return http.StatusServiceUnavailable, gin.H{
				"success": false,
				"message": "trust-level pricing is temporarily unavailable",
			}
		}
		adjustedRatios := make(map[string]float64, len(view.groupRatio))
		for groupID, ratio := range view.groupRatio {
			adjustedRatios[groupID] = ratio * trust.DiscountRatio
		}
		response["group_ratio"] = adjustedRatios
		response["trust_level"] = trust.Level
		response["trust_discount_ratio"] = trust.DiscountRatio
		response["pricing_scope"] = "assistant_account"
	}
	return http.StatusOK, response
}

func GetPricing(c *gin.Context) {
	status, response := buildPricingResponse(c, false)
	c.JSON(status, response)
}

func ResetModelRatio(c *gin.Context) {
	defaultStr := ratio_setting.DefaultModelRatio2JSONString()
	result, err := model.UpdateOptionWithWarnings("ModelRatio", defaultStr)
	if err != nil {
		c.JSON(200, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	model.InvalidatePricingCache()
	if err := refreshPricingCache(); err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(200, gin.H{
		"success":       true,
		"message":       "重置模型倍率成功",
		"warnings":      result.Warnings,
		"locked_models": result.LockedModels,
	})
}
