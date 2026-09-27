package controller

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/gin-gonic/gin"
)

const acquisitionCookie = "lmm_acquisition"

func acquisitionContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 500*time.Millisecond)
}
func acquisitionCookieHash(c *gin.Context) string {
	if c == nil || c.Request == nil {
		return ""
	}
	value, err := c.Cookie(acquisitionCookie)
	if err != nil || len(value) != 32 {
		return ""
	}
	return model.AcquisitionVisitorHash(value)
}
func recordAcquisitionRegistration(c *gin.Context, userID int) {
	visitor := acquisitionCookieHash(c)
	if visitor == "" {
		return
	}
	// Completion of registration never waits for or depends on analytics.
	go func() {
		ctx, cancel := acquisitionContext()
		defer cancel()
		_ = model.AttributeAcquisitionRegistration(ctx, userID, visitor)
	}()
}
func ObserveAcquisition(c *gin.Context) {
	var input model.AcquisitionInput
	if c.ShouldBindJSON(&input) != nil || !input.Consent || input.Test || c.GetInt("role") >= common.RoleAdminUser {
		c.Status(http.StatusNoContent)
		return
	}
	cookie, _ := c.Cookie(acquisitionCookie)
	if len(cookie) != 32 {
		cookie = model.AcquisitionRandomID()
	}
	if cookie == "" {
		c.Status(http.StatusNoContent)
		return
	}
	ctx, cancel := acquisitionContext()
	defer cancel()
	own := strings.Split(c.Request.Host, ":")[0]
	_, err := model.ObserveAcquisition(ctx, model.AcquisitionVisitorHash(cookie), c.GetInt("id"), input, []string{own, "lmm.best"})
	if err != nil {
		c.Status(http.StatusNoContent)
		return
	}
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(acquisitionCookie, cookie, 30*86400, "/", "", common.SessionCookieSecure, true)

	c.Status(http.StatusNoContent)
}
func RevokeAcquisition(c *gin.Context) {
	if c.GetInt("id") == 0 && strings.TrimSpace(c.GetHeader("Authorization")) != "" {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"success": false, "message": "Authentication required"})
		return
	}
	visitor := acquisitionCookieHash(c)
	ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
	defer cancel()
	if visitor != "" {
		if err := model.RevokeAcquisitionVisitor(ctx, visitor); err != nil {
			common.ApiError(c, err)
			return
		}
	}
	if err := model.RevokeAcquisitionAccount(ctx, c.GetInt("id")); err != nil {
		common.ApiError(c, err)
		return
	}
	// Keep the cookie on failure so an anonymous user can retry the withdrawal.
	c.SetCookie(acquisitionCookie, "", -1, "/", "", common.SessionCookieSecure, true)
	c.Status(http.StatusNoContent)
}

func ListAcquisitionLinks(c *gin.Context) {
	page, _ := strconv.Atoi(c.Query("page"))
	if page < 1 {
		page = 1
	}
	if page > 100000 {
		common.ApiError(c, model.ErrAcquisitionInvalid)
		return
	}
	pageSize := 100
	if c.Query("page_size") != "" {
		pageSize, _ = strconv.Atoi(c.Query("page_size"))
		if pageSize < 1 || pageSize > 100 {
			common.ApiError(c, model.ErrAcquisitionInvalid)
			return
		}
	}
	items, total, err := model.ListAcquisitionLinks(c.Request.Context(), model.AcquisitionLinkFilter{
		Page: page, PageSize: pageSize, Status: c.DefaultQuery("status", "all"), Search: c.Query("q"),
	})
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"items": items, "total": total, "page": page, "page_size": pageSize})
}
func SaveAcquisitionLink(c *gin.Context) {
	var input model.AcquisitionLink
	if c.ShouldBindJSON(&input) != nil {
		common.ApiError(c, model.ErrAcquisitionInvalid)
		return
	}
	result, err := model.SaveAcquisitionLink(c.Request.Context(), input)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, result)
}
func DeleteAcquisitionLink(c *gin.Context) {
	link, err := model.DeleteAcquisitionLink(c.Request.Context(), c.Param("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAuditFor(c, 0, "acquisition.link.delete", map[string]interface{}{"link_id": link.ID})
	common.ApiSuccess(c, gin.H{"id": link.ID, "deleted": true})
}
func AcquisitionReport(c *gin.Context) {
	from, _ := strconv.ParseInt(c.Query("from"), 10, 64)
	to, _ := strconv.ParseInt(c.Query("to"), 10, 64)
	result, err := model.GetAcquisitionReport(c.Request.Context(), from, to)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, result)
}

func RebuildAcquisitionActivity(c *gin.Context) {
	if err := model.RebuildAcquisitionActivity(c.Request.Context()); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"scheduled": true})
}

func ListAcquisitionUsers(c *gin.Context) {
	from, _ := strconv.ParseInt(c.Query("from"), 10, 64)
	to, _ := strconv.ParseInt(c.Query("to"), 10, 64)
	page, _ := strconv.Atoi(c.Query("page"))
	if page < 1 {
		page = 1
	}
	result, err := model.ListAcquisitionUsers(c.Request.Context(), c.Query("source"), from, to, page)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, result)
}
func GetAcquisitionUserDetail(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		common.ApiError(c, model.ErrAcquisitionInvalid)
		return
	}
	var target model.User
	err = model.DB.WithContext(c.Request.Context()).Select("id", "role").First(&target, id).Error
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if !canManageTargetRole(c.GetInt("role"), target.Role) {
		c.AbortWithStatus(http.StatusForbidden)
		return
	}
	result, err := model.GetAcquisitionUserDetail(c.Request.Context(), id)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, result)
}

func GrantAcquisitionConsent(c *gin.Context) {
	if err := model.GrantAcquisitionConsent(c.Request.Context(), c.GetInt("id")); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"allowed": true, "version": 2})
}

func SetAcquisitionLookback(c *gin.Context) {
	var input struct {
		Days int `json:"days"`
	}
	if c.ShouldBindJSON(&input) != nil {
		common.ApiError(c, model.ErrAcquisitionInvalid)
		return
	}
	if err := model.SetAcquisitionLookback(c.Request.Context(), input.Days); err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAuditFor(c, 0, "acquisition.lookback", map[string]interface{}{"days": input.Days})
	common.ApiSuccess(c, gin.H{"days": input.Days})
}

func PreviewAcquisitionLink(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
	defer cancel()
	result, err := model.PreviewAcquisitionLink(ctx, c.Param("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, result)
}
