package controller

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/gin-gonic/gin"
)

const heroSMSMutationRequestMaxBytes = 16 << 10

func heroSMSJSON(c *gin.Context, status int, payload any) {
	c.JSON(status, gin.H{"success": true, "data": payload})
}

func heroSMSError(c *gin.Context, err error) {
	if apiErr, ok := err.(*model.HeroSMSError); ok {
		c.AbortWithStatusJSON(apiErr.Status, gin.H{"success": false, "code": apiErr.Code, "message": apiErr.Message})
		return
	}
	common.SysLog(fmt.Sprintf("HeroSMS request failed: %T", err))
	c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"success": false, "code": "INTERNAL_ERROR", "message": "HeroSMS operation failed"})
}

func heroSMSPageSize(c *gin.Context) (int, int) {
	page := 1
	if parsed, err := strconv.Atoi(strings.TrimSpace(c.Query("page"))); err == nil && parsed > 0 {
		page = parsed
	}
	size := 20
	if parsed, err := strconv.Atoi(strings.TrimSpace(c.Query("size"))); err == nil && parsed > 0 {
		size = parsed
	}
	if size > 100 {
		size = 100
	}
	return page, size
}

func respondHeroSMSSettings(c *gin.Context) {
	// pi-lens-ignore: compiler:WrongAssignCount
	settings, err := model.GetHeroSMSSettingsView()
	if err != nil {
		heroSMSError(c, err)
		return
	}
	heroSMSJSON(c, http.StatusOK, settings)
}

func GetHeroSMSOptions(c *gin.Context) {
	respondHeroSMSSettings(c)
}

func PutHeroSMSOptions(c *gin.Context) {
	var request model.HeroSMSSettingsUpdate
	if err := c.ShouldBindJSON(&request); err != nil {
		heroSMSError(c, model.NewHeroSMSError(http.StatusBadRequest, "INVALID_REQUEST", "invalid HeroSMS option payload"))
		return
	}
	if err := model.UpdateHeroSMSSettings(request); err != nil {
		heroSMSError(c, err)
		return
	}
	respondHeroSMSSettings(c)
}

func CheckHeroSMSOptions(c *gin.Context) {
	var request struct {
		APIKey string `json:"api_key"`
	}
	if err := c.ShouldBindJSON(&request); err != nil && !errors.Is(err, io.EOF) {
		heroSMSError(c, model.NewHeroSMSError(http.StatusBadRequest, "INVALID_REQUEST", "invalid HeroSMS test payload"))
		return
	}
	// pi-lens-ignore: compiler:UndeclaredImportedName
	if err := model.CheckHeroSMSConfiguration(c.Request.Context(), request.APIKey); err != nil {
		heroSMSError(c, err)
		return
	}
	heroSMSJSON(c, http.StatusOK, gin.H{"ok": true})
}

func DeleteHeroSMSOptionKey(c *gin.Context) {
	if err := model.ClearHeroSMSAPIKey(); err != nil {
		heroSMSError(c, err)
		return
	}
	respondHeroSMSSettings(c)
}

func ListHeroSMSEmailProducts(c *gin.Context) {
	page, size := heroSMSPageSize(c)
	products, err := model.ListHeroSMSEmailProducts(c.Request.Context(), page, size, c.Query("site"))
	if err != nil {
		heroSMSError(c, err)
		return
	}
	heroSMSJSON(c, http.StatusOK, products)
}

func CreateHeroSMSEmailActivations(c *gin.Context) {
	var request model.HeroSMSEmailPurchaseRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		heroSMSError(c, model.NewHeroSMSError(http.StatusBadRequest, "INVALID_REQUEST", "invalid HeroSMS activation payload"))
		return
	}
	order, status, err := model.CreateHeroSMSEmailActivations(c.Request.Context(), c.GetInt("id"), c.GetHeader("Idempotency-Key"), request)
	if err != nil {
		heroSMSError(c, err)
		return
	}
	heroSMSJSON(c, status, gin.H{"order": order, "activations": order.Activations})
}

func ListHeroSMSEmailActivations(c *gin.Context) {
	page, size := heroSMSPageSize(c)
	result, err := model.ListHeroSMSEmailActivations(c.GetInt("id"), page, size, c.Query("status"))
	if err != nil {
		heroSMSError(c, err)
		return
	}
	heroSMSJSON(c, http.StatusOK, result)
}

func GetCurrentHeroSMSEmailActivation(c *gin.Context) {
	// pi-lens-ignore: compiler:UndeclaredImportedName
	activation, err := model.GetCurrentHeroSMSEmailActivation(c.GetInt("id"))
	if err != nil {
		heroSMSError(c, err)
		return
	}
	heroSMSJSON(c, http.StatusOK, activation)
}

func GetHeroSMSEmailActivation(c *gin.Context) {
	result, err := model.GetHeroSMSEmailActivation(c.GetInt("id"), c.Param("id"))
	if err != nil {
		heroSMSError(c, err)
		return
	}
	heroSMSJSON(c, http.StatusOK, result)
}

func RefreshHeroSMSEmailActivation(c *gin.Context) {
	result, err := model.RefreshHeroSMSEmailActivation(c.Request.Context(), c.GetInt("id"), c.Param("id"))
	if err != nil {
		heroSMSError(c, err)
		return
	}
	heroSMSJSON(c, http.StatusOK, result)
}

func CancelHeroSMSEmailActivation(c *gin.Context) {
	result, err := model.CancelHeroSMSEmailActivation(c.Request.Context(), c.GetInt("id"), c.Param("id"))
	if err != nil {
		heroSMSError(c, err)
		return
	}
	heroSMSJSON(c, http.StatusOK, result)
}

func ReorderHeroSMSEmailActivation(c *gin.Context) {
	var request struct {
		DomainID string `json:"domain_id"`
	}
	if err := c.ShouldBindJSON(&request); err != nil || strings.TrimSpace(request.DomainID) == "" {
		heroSMSError(c, model.NewHeroSMSError(http.StatusBadRequest, "INVALID_REQUEST", "a fresh HeroSMS reorder quote is required"))
		return
	}
	// pi-lens-ignore: compiler:WrongArgCount
	result, status, err := model.ReorderHeroSMSEmailActivation(c.Request.Context(), c.GetInt("id"), c.Param("id"), c.GetHeader("Idempotency-Key"), request.DomainID)
	if err != nil {
		heroSMSError(c, err)
		return
	}
	heroSMSJSON(c, status, gin.H{"order": result, "activations": result.Activations})
}
