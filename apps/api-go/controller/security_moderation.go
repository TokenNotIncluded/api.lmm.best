package controller

import (
	"errors"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/dto"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/setting"
	"github.com/gin-gonic/gin"
)

const moderationSourceKind = "openai_moderation"

// GetAdminModerationModels reports real enabled abilities in an explicit
// routing group. The metadata-only selector never loads channel credentials.
func GetAdminModerationModels(c *gin.Context) {
	group := strings.TrimSpace(c.Query("group"))
	if !validModerationFilterGroup(group) || group == "" {
		common.ApiErrorMsg(c, "group must be an explicit group of at most 64 characters")
		return
	}
	models := make([]string, 0, 2)
	for _, name := range []string{setting.DefaultModerationModel, "omni-moderation-2024-09-26"} {
		channels, err := model.ListOfficialModerationChannels(c.Request.Context(), group, name)
		if err != nil {
			common.ApiErrorMsg(c, "moderation model catalog is unavailable")
			return
		}
		if len(channels) > 0 {
			models = append(models, name)
		}
	}
	common.ApiSuccess(c, gin.H{"group": group, "models": models})
}

func ListAdminModerationReviews(c *gin.Context) {
	filter, page, pageSize, err := parseModerationJobFilter(c)
	if err != nil {
		common.ApiErrorMsg(c, err.Error())
		return
	}
	rows, total, err := model.ListModerationJobs(c.Request.Context(), filter)
	if err != nil {
		common.ApiErrorMsg(c, "moderation review history is unavailable")
		return
	}
	actorRole, actorID := c.GetInt("role"), c.GetInt("id")
	identities := make([]model.AdvancedSecurityEvent, 0, len(rows))
	for _, row := range rows {
		identities = append(identities, model.AdvancedSecurityEvent{UserID: row.UserID})
	}
	roles := securityEventTargetRoles(identities, actorRole, actorID)
	items := make([]dto.SecurityModerationReview, 0, len(rows))
	for _, row := range rows {
		item := dto.SecurityModerationReview{
			ID: row.ID, SourceKind: moderationSourceKind, Source: row.Source,
			UserID: row.UserID, RequestID: row.RequestID, Group: row.Group,
			ReviewModel: row.ReviewModel, Mode: row.CapturedMode, Status: row.Status,
			Attempts: row.Attempts, InputTruncated: row.InputTruncated,
			Flagged: row.Flagged, Categories: row.Categories(), ResponseModel: row.ResponseModel,
			ReviewID: row.ReviewID, FeeRecordID: row.FeeRecordID, FeeCategory: row.FeeCategory,
			FeeStatus: row.FeeStatus, RequestedQuota: row.RequestedQuota, ChargedQuota: row.ChargedQuota,
			CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt, CompletedAt: row.CompletedAt,
		}
		if item.Categories == nil {
			item.Categories = []string{}
		}
		if !canRevealSecurityEvent(row.UserID, actorRole, actorID, roles) {
			// Preserve aggregate classification facts while respecting the same
			// higher-role identity boundary as the existing security history.
			item.UserID, item.RequestID, item.Group = 0, "", ""
			item.ReviewID, item.FeeRecordID = 0, 0
			item.RequestedQuota, item.ChargedQuota = 0, 0
		}
		items = append(items, item)
	}
	common.ApiSuccess(c, gin.H{"rows": items, "total": total, "page": page, "page_size": pageSize})
}

func GetAdminModerationStats(c *gin.Context) {
	stats, err := model.ModerationStats(c.Request.Context())
	if err != nil {
		common.ApiErrorMsg(c, "moderation statistics are unavailable")
		return
	}
	common.ApiSuccess(c, moderationStatsDTO(stats))
}

func moderationStatsDTO(stats model.ModerationQueueStats) dto.ModerationSecurityStats {
	return dto.ModerationSecurityStats{
		Pending: stats.Pending, Running: stats.Running, Completed: stats.Completed,
		Failed: stats.Failed, Cancelled: stats.Cancelled, Flagged: stats.Flagged,
		Fined: stats.Fined, ChargedQuota: stats.ChargedQuota,
	}
}

func publicModerationPolicy(settings setting.ModerationSettings) dto.SecurityModerationPolicy {
	policies := make(map[string]dto.SecurityModerationGroupPolicy, len(settings.GroupPolicies))
	for group, policy := range settings.GroupPolicies {
		fines := make(map[string]float64, len(policy.CategoryFinesUSD))
		for category, amount := range policy.CategoryFinesUSD {
			fines[category] = amount
		}
		policies[group] = dto.SecurityModerationGroupPolicy{Mode: policy.Mode, CategoryFinesUSD: fines}
	}
	return dto.SecurityModerationPolicy{
		Enabled: settings.Enabled, AssistantEnabled: settings.AssistantEnabled,
		Engine: moderationSourceKind, Async: true, GroupPolicies: policies,
		SupportedInputs: []string{"text"}, NoticeOnly: true,
	}
}

func validModerationFilterGroup(group string) bool {
	return group != "*" && utf8.ValidString(group) && utf8.RuneCountInString(group) <= 64 && !strings.ContainsRune(group, '\x00')
}

func parseModerationJobFilter(c *gin.Context) (model.ModerationJobFilter, int, int, error) {
	filter := model.ModerationJobFilter{}
	page, pageSize := 1, 20
	if raw := strings.TrimSpace(c.Query("p")); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 {
			return filter, 0, 0, errors.New("p must be a positive integer")
		}
		page = value
	}
	if raw := strings.TrimSpace(c.Query("page_size")); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 {
			return filter, 0, 0, errors.New("page_size must be a positive integer")
		}
		pageSize = min(value, 100)
	}
	maxInt := int(^uint(0) >> 1)
	if page-1 > maxInt/pageSize {
		return filter, 0, 0, errors.New("p is too large")
	}
	filter.Limit, filter.Offset = pageSize, (page-1)*pageSize
	filter.Group = strings.TrimSpace(c.Query("group"))
	if !validModerationFilterGroup(filter.Group) {
		return filter, 0, 0, errors.New("invalid group")
	}
	filter.Status = strings.TrimSpace(c.Query("status"))
	switch filter.Status {
	case "", model.ModerationJobPending, model.ModerationJobRunning, model.ModerationJobCompleted, model.ModerationJobFailed, model.ModerationJobCancelled:
	default:
		return filter, 0, 0, errors.New("invalid moderation status")
	}
	filter.Source = strings.TrimSpace(c.Query("source"))
	switch filter.Source {
	case "", model.ModerationSourceRelayInput, model.ModerationSourceAssistantInput, model.ModerationSourceAssistantOutput:
	default:
		return filter, 0, 0, errors.New("invalid moderation source")
	}
	for key, target := range map[string]*int64{"start_timestamp": &filter.StartTimestamp, "end_timestamp": &filter.EndTimestamp} {
		if raw := strings.TrimSpace(c.Query(key)); raw != "" {
			value, err := strconv.ParseInt(raw, 10, 64)
			if err != nil || value < 0 {
				return filter, 0, 0, errors.New("invalid " + key)
			}
			*target = value
		}
	}
	if filter.StartTimestamp > 0 && filter.EndTimestamp > 0 && filter.EndTimestamp < filter.StartTimestamp {
		return filter, 0, 0, errors.New("end_timestamp must be greater than or equal to start_timestamp")
	}
	if raw := strings.TrimSpace(c.Query("user_id")); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 {
			return filter, 0, 0, errors.New("user_id must be a positive integer")
		}
		filter.UserID = value
	}
	return filter, page, pageSize, nil
}
