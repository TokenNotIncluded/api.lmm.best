package controller

import (
	"net/http"
	"strconv"
	"strings"

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
	c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"success": false, "code": "INTERNAL_ERROR", "message": err.Error()})
}

func heroSMSPageSize(c *gin.Context) (int, int) {
	page, _ := strconv.Atoi(strings.TrimSpace(c.Query("page")))
	size, _ := strconv.Atoi(strings.TrimSpace(c.Query("size")))
	if page < 1 {
		page = 1
	}
	if size < 1 {
		size = 20
	}
	if size > 100 {
		size = 100
	}
	return page, size
}

func GetHeroSMSOptions(c *gin.Context) {
	heroSMSJSON(c, http.StatusOK, model.GetHeroSMSSettingsView())
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
	heroSMSJSON(c, http.StatusOK, model.GetHeroSMSSettingsView())
}

func TestHeroSMSOptions(c *gin.Context) {
	if err := model.TestHeroSMSConfiguration(c.Request.Context()); err != nil {
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
	heroSMSJSON(c, http.StatusOK, model.GetHeroSMSSettingsView())
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
	heroSMSJSON(c, status, order)
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
	result, status, err := model.ReorderHeroSMSEmailActivation(c.Request.Context(), c.GetInt("id"), c.Param("id"), c.GetHeader("Idempotency-Key"))
	if err != nil {
		heroSMSError(c, err)
		return
	}
	heroSMSJSON(c, status, result)
}
