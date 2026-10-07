package controller

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"

	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/gin-gonic/gin"
)

func ListMyMerchantStoreAnalytics(c *gin.Context)    { listMerchantStoreAnalytics(c, false) }
func ListAdminMerchantStoreAnalytics(c *gin.Context) { listMerchantStoreAnalytics(c, true) }

func listMerchantStoreAnalytics(c *gin.Context, all bool) {
	offset, limit, ok := merchantStorePage(c)
	if !ok {
		return
	}
	values := c.Request.URL.Query()["days"]
	if len(values) > 1 {
		merchantStoreRespond(c, nil, model.ErrMerchantStoreInput)
		return
	}
	days, raw := 30, c.DefaultQuery("days", "30")
	if raw == "all" {
		days = 0
	} else {
		var err error
		days, err = strconv.Atoi(raw)
		if err != nil || days < 1 || days > 3650 {
			merchantStoreRespond(c, nil, model.ErrMerchantStoreInput)
			return
		}
	}
	result, err := model.GetMerchantStoreAnalytics(c.GetInt("id"), all, days, offset, limit)
	merchantStoreRespond(c, result, err)
}

func RecordMerchantStoreTraffic(c *gin.Context) {
	var input model.MerchantStoreTrafficInput
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil || decoder.Decode(new(any)) != io.EOF {
		merchantStoreRespond(c, nil, model.ErrMerchantStoreInput)
		return
	}
	if err := model.RecordMerchantStoreTraffic(merchantStoreViewer(c), c.Param("id"), input); err != nil {
		merchantStoreRespond(c, nil, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusAccepted, gin.H{"success": true})
}

func GetMerchantStoreAnalyticsConfig(c *gin.Context) {
	config, err := model.GetMerchantStoreAnalyticsConfig(c.GetInt("id"))
	merchantStoreRespond(c, config, err)
}

func SaveMerchantStoreAnalyticsConfig(c *gin.Context) {
	var input model.MerchantStoreAnalyticsConfig
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil || decoder.Decode(new(any)) != io.EOF {
		merchantStoreRespond(c, nil, model.ErrMerchantStoreInput)
		return
	}
	merchantStoreRespond(c, input, model.SaveMerchantStoreAnalyticsConfig(c.GetInt("id"), input))
}
