/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/

package controller

import (
	"errors"
	"strconv"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/gin-gonic/gin"
)

func GetAllDiscountCodes(c *gin.Context) {
	pageInfo := common.GetPageQuery(c)
	codes, total, err := model.GetAllDiscountCodes(pageInfo.GetStartIdx(), pageInfo.GetPageSize())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(codes)
	common.ApiSuccess(c, pageInfo)
}

func SearchDiscountCodes(c *gin.Context) {
	pageInfo := common.GetPageQuery(c)
	codes, total, err := model.SearchDiscountCodes(c.Query("keyword"), c.Query("status"), pageInfo.GetStartIdx(), pageInfo.GetPageSize())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(codes)
	common.ApiSuccess(c, pageInfo)
}

func GetDiscountCode(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	code, err := model.GetDiscountCodeById(id)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, code)
}

func AddDiscountCode(c *gin.Context) {
	var input model.DiscountCode
	if err := c.ShouldBindJSON(&input); err != nil {
		common.ApiError(c, err)
		return
	}
	input.Code = model.NormalizeDiscountCode(input.Code)
	if err := validateDiscountCodeInput(&input); err != nil {
		common.ApiErrorMsg(c, err.Error())
		return
	}
	input.CreatedBy = c.GetInt("id")
	input.Status = model.DiscountCodeStatusEnabled
	if err := input.Insert(); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			common.ApiErrorMsg(c, "优惠码已存在")
			return
		}
		common.ApiError(c, err)
		return
	}
	recordManageAudit(c, "discount_code.create", map[string]interface{}{
		"id": input.Id, "code": input.Code, "discount_percent": input.DiscountPercent,
	})
	common.ApiSuccess(c, input)
}

const discountCodeBatchMaxCount = 100

type discountCodeBatchRequest struct {
	Name            string `json:"name"`
	Count           int    `json:"count"`
	DiscountPercent int    `json:"discount_percent"`
	MinAmount       int64  `json:"min_amount"`
	MaxUses         int64  `json:"max_uses"`
	StartsTime      int64  `json:"starts_time"`
	ExpiredTime     int64  `json:"expired_time"`
}

func AddDiscountCodes(c *gin.Context) {
	var request discountCodeBatchRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		common.ApiError(c, err)
		return
	}
	if request.Count < 1 || request.Count > discountCodeBatchMaxCount {
		common.ApiErrorMsg(c, "优惠码数量必须在 1 到 100 之间")
		return
	}
	startsTime := request.StartsTime
	if startsTime <= 0 {
		startsTime = common.GetTimestamp()
	}
	template := model.DiscountCode{
		Name:            strings.TrimSpace(request.Name),
		DiscountPercent: request.DiscountPercent,
		MinAmount:       request.MinAmount,
		MaxUses:         request.MaxUses,
		StartsTime:      startsTime,
		ExpiredTime:     request.ExpiredTime,
		CreatedBy:       c.GetInt("id"),
		Status:          model.DiscountCodeStatusEnabled,
	}
	if err := validateDiscountCodeBatchInput(&template); err != nil {
		common.ApiErrorMsg(c, err.Error())
		return
	}
	codes, err := model.CreateDiscountCodes(template, request.Count)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAudit(c, "discount_code.batch_create", map[string]interface{}{
		"name":             template.Name,
		"count":            len(codes),
		"discount_percent": template.DiscountPercent,
		"max_uses":         template.MaxUses,
	})
	common.ApiSuccess(c, codes)
}

func UpdateDiscountCode(c *gin.Context) {
	var input model.DiscountCode
	if err := c.ShouldBindJSON(&input); err != nil {
		common.ApiError(c, err)
		return
	}
	current, err := model.GetDiscountCodeById(input.Id)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if c.Query("status_only") != "" {
		if input.Status != model.DiscountCodeStatusEnabled && input.Status != model.DiscountCodeStatusDisabled {
			common.ApiErrorMsg(c, "无效的优惠码状态")
			return
		}
		current.Status = input.Status
	} else {
		current.Code = model.NormalizeDiscountCode(input.Code)
		current.Name = strings.TrimSpace(input.Name)
		current.DiscountPercent = input.DiscountPercent
		current.MinAmount = input.MinAmount
		current.MaxUses = input.MaxUses
		current.StartsTime = input.StartsTime
		current.ExpiredTime = input.ExpiredTime
		if err := validateDiscountCodeInput(current); err != nil {
			common.ApiErrorMsg(c, err.Error())
			return
		}
	}
	if err := current.Update(); err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAudit(c, "discount_code.update", map[string]interface{}{"id": current.Id, "status": current.Status})
	common.ApiSuccess(c, current)
}

func DeleteDiscountCode(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if err := model.DeleteDiscountCodeById(id); err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAudit(c, "discount_code.delete", map[string]interface{}{"id": id})
	common.ApiSuccess(c, nil)
}

func DeleteExhaustedDiscountCodes(c *gin.Context) {
	// pi-lens-ignore: compiler:UndeclaredImportedName
	rows, err := model.DeleteExhaustedDiscountCodes()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAudit(c, "discount_code.delete_exhausted", map[string]interface{}{"count": rows})
	common.ApiSuccess(c, gin.H{"count": rows})
}

type discountCodeValidationRequest struct {
	Code          string `json:"code"`
	Amount        int64  `json:"amount"`
	PaymentMethod string `json:"payment_method"`
}

// ValidateDiscountCode is intentionally a user-scoped endpoint. It returns a
// quote preview, never the administrator's code inventory or internal fields.
func ValidateDiscountCode(c *gin.Context) {
	var req discountCodeValidationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiError(c, err)
		return
	}
	row, err := model.ValidateDiscountCodeForUser(req.Code, req.Amount, common.GetTimestamp(), c.GetInt("id"))
	if err != nil {
		common.ApiErrorMsg(c, discountCodeErrorMessage(err))
		return
	}
	common.ApiSuccess(c, gin.H{
		"code":             row.Code,
		"discount_percent": row.DiscountPercent,
		"min_amount":       row.MinAmount,
	})
}

// pi-lens-ignore: go-bare-error
func validateDiscountCodeInput(code *model.DiscountCode) error {
	if err := validateDiscountCodeBatchInput(code); err != nil {
		return err
	}
	return model.ValidateDiscountCodeDefinition(code.Code, code.DiscountPercent, code.MinAmount, code.StartsTime, code.ExpiredTime)
}

// pi-lens-ignore: go-bare-error
func validateDiscountCodeBatchInput(code *model.DiscountCode) error {
	if strings.TrimSpace(code.Name) == "" || len([]rune(code.Name)) > 120 {
		return errors.New("优惠码名称不能为空且不能超过 120 个字符")
	}
	if err := model.ValidateDiscountCodeMaxUses(code.MaxUses); err != nil {
		return err
	}
	return model.ValidateDiscountCodeTerms(code.DiscountPercent, code.MinAmount, code.StartsTime, code.ExpiredTime)
}

func discountCodeErrorMessage(err error) string {
	switch {
	case errors.Is(err, model.ErrDiscountCodeNotFound):
		return "优惠码不存在"
	case errors.Is(err, model.ErrDiscountCodeInactive):
		return "优惠码未启用或尚未生效"
	case errors.Is(err, model.ErrDiscountCodeExpired):
		return "优惠码已过期"
	case errors.Is(err, model.ErrDiscountCodeMinimum):
		return "当前充值金额未达到优惠码最低金额"
	case errors.Is(err, model.ErrDiscountCodeExhausted):
		return "优惠码使用次数已达上限"
	default:
		return "优惠码无效"
	}
}
