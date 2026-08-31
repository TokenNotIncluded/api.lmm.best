package controller

import (
	"errors"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func parseSubscriptionResetIds(value string) ([]int, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	parts := strings.Split(value, ",")
	result := make([]int, 0, len(parts))
	seen := make(map[int]struct{}, len(parts))
	for _, part := range parts {
		id, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil || id <= 0 {
			return nil, errors.New("invalid subscription reset id filter")
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		result = append(result, id)
		if len(result) > 100 {
			return nil, errors.New("too many subscription reset id filters")
		}
	}
	return result, nil
}

func subscriptionAdminPageQuery(c *gin.Context) (int, int) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	return page, pageSize
}

func AdminListSubscriptionRecords(c *gin.Context) {
	page, pageSize := subscriptionAdminPageQuery(c)
	queryFilter := strings.TrimSpace(c.Query("query"))
	if utf8.RuneCountInString(queryFilter) > 200 {
		common.ApiErrorMsg(c, "subscription record search filter is too long")
		return
	}
	planId := 0
	if rawPlanId := strings.TrimSpace(c.Query("plan_id")); rawPlanId != "" {
		parsedPlanId, parseErr := strconv.Atoi(rawPlanId)
		if parseErr != nil || parsedPlanId <= 0 {
			common.ApiErrorMsg(c, "invalid subscription record plan filter")
			return
		}
		planId = parsedPlanId
	}
	status := strings.TrimSpace(c.DefaultQuery("status", "all"))
	if status != "all" && status != "active" && status != "expired" && status != "cancelled" {
		common.ApiErrorMsg(c, "invalid subscription record status filter")
		return
	}
	result, err := model.ListAdminSubscriptionRecords(model.AdminSubscriptionRecordFilter{
		Query: queryFilter, PlanId: planId, Status: status,
		Page: page, PageSize: pageSize,
	})
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, result)
}

func RootListSubscriptionResetEligible(c *gin.Context) {
	page, pageSize := subscriptionAdminPageQuery(c)
	queryFilter := strings.TrimSpace(c.Query("query"))
	if utf8.RuneCountInString(queryFilter) > 200 {
		common.ApiErrorMsg(c, "subscription reset search filter is too long")
		return
	}
	planId := 0
	if rawPlanId := strings.TrimSpace(c.Query("plan_id")); rawPlanId != "" {
		parsedPlanId, parseErr := strconv.Atoi(rawPlanId)
		if parseErr != nil || parsedPlanId <= 0 {
			common.ApiErrorMsg(c, "invalid subscription reset plan filter")
			return
		}
		planId = parsedPlanId
	}
	planIds, err := parseSubscriptionResetIds(c.Query("plan_ids"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	userIds, err := parseSubscriptionResetIds(c.Query("user_ids"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	result, err := model.ListAdminSubscriptionResetEligible(model.AdminSubscriptionResetEligibleFilter{
		Query: queryFilter, PlanId: planId,
		PlanIds: planIds, UserIds: userIds,
		Page: page, PageSize: pageSize,
	})
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, result)
}

type rootSubscriptionResetFilter struct {
	Query   string `json:"query"`
	PlanId  int    `json:"plan_id"`
	PlanIds []int  `json:"plan_ids"`
	UserIds []int  `json:"user_ids"`
}

type rootSubscriptionResetPreviewRequest struct {
	Mode        string                          `json:"mode"`
	Targets     []model.SubscriptionResetTarget `json:"targets"`
	AllMatching bool                            `json:"all_matching"`
	Filter      *rootSubscriptionResetFilter    `json:"filter"`
}

type rootSubscriptionResetExecuteRequest struct {
	OperationId  string `json:"operation_id"`
	PreviewToken string `json:"preview_token"`
}

func RootPreviewSubscriptionsBatch(c *gin.Context) {
	if !requirePaymentCompliance(c) {
		return
	}

	var req rootSubscriptionResetPreviewRequest
	if err := decodeStrictJSONRequest(c, &req); err != nil {
		common.ApiErrorMsg(c, "参数错误")
		return
	}
	if req.AllMatching && req.Filter == nil {
		common.ApiErrorMsg(c, "all_matching subscription resets require an explicit filter object")
		return
	}
	filter := model.AdminSubscriptionResetEligibleFilter{}
	if req.Filter != nil {
		filter = model.AdminSubscriptionResetEligibleFilter{
			Query: req.Filter.Query, PlanId: req.Filter.PlanId,
			PlanIds: req.Filter.PlanIds, UserIds: req.Filter.UserIds,
		}
	}
	result, err := model.AdminPreviewSubscriptionsReset(model.AdminSubscriptionResetBatchInput{
		ActorUserId: c.GetInt("id"), Mode: req.Mode, Targets: req.Targets,
		AllMatching: req.AllMatching, Filter: filter,
	})
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, result)
}

func RootResetSubscriptionsBatch(c *gin.Context) {
	if !requirePaymentCompliance(c) {
		return
	}

	var req rootSubscriptionResetExecuteRequest
	if err := decodeStrictJSONRequest(c, &req); err != nil {
		common.ApiErrorMsg(c, "参数错误")
		return
	}
	result, err := model.AdminResetSubscriptionsBatch(model.AdminSubscriptionResetBatchInput{
		ActorUserId: c.GetInt("id"), OperationId: strings.TrimSpace(req.OperationId),
		PreviewToken: strings.TrimSpace(req.PreviewToken),
		Audit: model.SubscriptionResetAuditContext{
			Username: c.GetString("username"), IP: c.ClientIP(), Role: c.GetInt("role"),
			AuthMethod: auditAuthMethod(c),
		},
	})
	if err != nil {
		common.ApiError(c, err)
		return
	}
	markAuditLogged(c)
	common.ApiSuccess(c, result)
}

func GetSubscriptionResetVouchers(c *gin.Context) {
	vouchers, err := model.ListUserSubscriptionResetVouchers(c.GetInt("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, vouchers)
}

func RedeemSubscriptionResetVoucher(c *gin.Context) {
	voucherId, err := strconv.Atoi(c.Param("id"))
	if err != nil || voucherId <= 0 {
		common.ApiErrorMsg(c, "无效的重置券")
		return
	}
	result, err := model.RedeemUserSubscriptionResetVoucherWithAudit(c.GetInt("id"), voucherId, model.SubscriptionResetAuditContext{
		Username: c.GetString("username"), IP: c.ClientIP(), Role: c.GetInt("role"),
		AuthMethod: auditAuthMethod(c),
	})
	if err != nil {
		switch {
		case errors.Is(err, gorm.ErrRecordNotFound):
			common.ApiErrorMsg(c, "重置券不存在")
		case errors.Is(err, model.ErrSubscriptionResetVoucherExpired):
			common.ApiErrorMsg(c, "重置券已过期")
		case errors.Is(err, model.ErrSubscriptionResetVoucherUnavailable):
			common.ApiErrorMsg(c, "重置券已使用或不可用")
		case errors.Is(err, model.ErrSubscriptionResetRequiresActiveSubscription):
			common.ApiErrorMsg(c, "仅有效订阅用户可以使用重置券")
		default:
			common.ApiError(c, err)
		}
		return
	}
	markAuditLogged(c)
	common.ApiSuccess(c, result)
}
