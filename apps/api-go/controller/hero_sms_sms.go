package controller

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/gin-gonic/gin"
)

func ListHeroSMSSMSCountries(c *gin.Context) {
	countries, err := model.GetHeroSMSSMSCountries(
		c.Request.Context(),
		c.Query("service"),
	)
	if err != nil {
		heroSMSError(c, err)
		return
	}
	heroSMSJSON(c, http.StatusOK, countries)
}

func ListHeroSMSSMSServices(c *gin.Context) {
	services, err := model.GetHeroSMSSMSServices(c.Request.Context())
	if err != nil {
		heroSMSError(c, err)
		return
	}
	heroSMSJSON(c, http.StatusOK, services)
}

func GetHeroSMSSMSOffer(c *gin.Context) {
	countryID, err := strconv.Atoi(c.Query("country"))
	if err != nil || countryID < 0 {
		heroSMSError(c, model.NewHeroSMSError(http.StatusBadRequest, "INVALID_REQUEST", "invalid HeroSMS country"))
		return
	}
	offer, err := model.GetHeroSMSSMSOffer(
		c.Request.Context(),
		countryID,
		c.Query("service"),
		c.Query("operator"),
	)
	if err != nil {
		heroSMSError(c, err)
		return
	}
	heroSMSJSON(c, http.StatusOK, offer)
}

func CreateHeroSMSSMSOrder(c *gin.Context) {
	var request model.HeroSMSSMSPurchaseRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		heroSMSError(c, model.NewHeroSMSError(http.StatusBadRequest, "INVALID_REQUEST", "invalid HeroSMS SMS purchase payload"))
		return
	}
	order, quota, status, err := model.CreateHeroSMSSMSOrder(
		c.Request.Context(),
		c.GetInt("id"),
		request,
		c.GetHeader("Idempotency-Key"),
	)
	if err != nil {
		heroSMSError(c, err)
		return
	}
	heroSMSJSON(c, status, gin.H{"order": order, "quota": quota})
}

func GetCurrentHeroSMSSMSOrder(c *gin.Context) {
	order, err := model.GetCurrentHeroSMSSMSOrder(c.Request.Context(), c.GetInt("id"))
	if err != nil {
		heroSMSError(c, err)
		return
	}
	heroSMSJSON(c, http.StatusOK, gin.H{"order": order})
}

func ListCurrentHeroSMSSMSOrders(c *gin.Context) {
	orders, err := model.ListCurrentHeroSMSSMSOrders(c.Request.Context(), c.GetInt("id"))
	if err != nil {
		heroSMSError(c, err)
		return
	}
	heroSMSJSON(c, http.StatusOK, gin.H{"items": orders})
}

func GetHeroSMSSMSOrder(c *gin.Context) {
	orderID := strings.TrimSpace(c.Param("id"))
	order, err := model.RefreshHeroSMSSMSOrder(c.Request.Context(), c.GetInt("id"), orderID)
	if err != nil {
		heroSMSError(c, err)
		return
	}
	heroSMSJSON(c, http.StatusOK, gin.H{"order": order})
}

func CancelHeroSMSSMSOrder(c *gin.Context) {
	order, quota, err := model.CancelHeroSMSSMSOrder(c.Request.Context(), c.GetInt("id"), c.Param("id"))
	if err != nil {
		heroSMSError(c, err)
		return
	}
	heroSMSJSON(c, http.StatusOK, gin.H{"order": order, "quota": quota})
}

func ListHeroSMSSMSOrders(c *gin.Context) {
	page, size := heroSMSPageSize(c)
	var orders *model.HeroSMSSMSOrderPage
	var err error
	if strings.EqualFold(c.Query("summary"), "true") {
		orders, err = model.ListHeroSMSSMSOrderSummaries(c.GetInt("id"), page, size)
	} else {
		orders, err = model.ListHeroSMSSMSOrders(c.GetInt("id"), page, size)
	}
	if err != nil {
		heroSMSError(c, err)
		return
	}
	heroSMSJSON(c, http.StatusOK, orders)
}
