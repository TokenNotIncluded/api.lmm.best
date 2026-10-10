package controller

import (
	"sort"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/dto"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/setting"

	"github.com/gin-gonic/gin"
)

func GetPublicSecurityPolicy(c *gin.Context) {
	moderationSettings, err := model.ReadModerationSettingsContext(c.Request.Context())
	if err != nil {
		common.ApiErrorMsg(c, "security policy is unavailable")
		return
	}
	common.ApiSuccess(c, buildPublicSecurityPolicy(moderationSettings))
}

func GetAdminSecurityPolicy(c *gin.Context) {
	moderationSettings, err := model.ReadModerationSettingsContext(c.Request.Context())
	if err != nil {
		common.ApiErrorMsg(c, "security policy is unavailable")
		return
	}
	common.ApiSuccess(c, dto.AdminSecurityPolicy{
		Public:       buildPublicSecurityPolicy(moderationSettings),
		Settings:     dto.SecuritySettings{Action: "retired", Retired: true},
		Rules:        []dto.SecurityAdminRule{},
		ViolationFee: dto.SecurityViolationFeeSettings{},
	})
}

func GetPublicSecurityStats(c *gin.Context) {
	stats, err := model.ModerationStats(c.Request.Context())
	if err != nil {
		common.ApiErrorMsg(c, "moderation statistics are unavailable")
		return
	}
	moderation := moderationStatsDTO(stats)
	common.ApiSuccess(c, dto.SecurityStats{Moderation: &moderation})
}

func buildPublicSecurityPolicy(moderationSettings setting.ModerationSettings) dto.PublicSecurityPolicy {
	return dto.PublicSecurityPolicy{
		PolicyVersion:          "openai-moderation-v1",
		ReferenceEffectiveDate: "",
		ReferenceURL:           "https://developers.openai.com/api/docs/guides/moderation",
		Alignment:              "OpenAI Moderation content classification",
		Enforcement:            dto.SecuritySettings{Action: "retired", Retired: true},
		ProtectedGroups:        moderationProtectedGroups(moderationSettings),
		RiskCategories:         []dto.SecurityRiskCategory{},
		Rules:                  []dto.SecurityRuleSummary{},
		ViolationFees:          []dto.SecurityViolationFeeRule{},
		Moderation:             publicModerationPolicy(moderationSettings),
	}
}

func moderationProtectedGroups(settings setting.ModerationSettings) []string {
	if !settings.Enabled && !settings.AssistantEnabled {
		return []string{}
	}
	groups := make([]string, 0, len(settings.GroupPolicies))
	for group, policy := range settings.GroupPolicies {
		if policy.Mode != setting.ModerationModeOff {
			groups = append(groups, group)
		}
	}
	sort.Strings(groups)
	return groups
}
