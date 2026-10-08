package controller

import (
	"encoding/json"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/pkg/servicetier"
	"github.com/LIghtJUNction/api.lmm.best/setting"
	"github.com/LIghtJUNction/api.lmm.best/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"net/http"
	"sync"
	"time"
)

var serviceTierSyncLock sync.Mutex

func GetServiceTierPricing(c *gin.Context) {
	policy, catalog, err := setting.ServiceTierPricing()
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"policy": policy, "catalog": catalog, "fresh": catalog.Fresh(time.Now()), "max_age_hours": int(servicetier.MaxCatalogAge.Hours()), "groups": ratio_setting.GetGroupRatioCopy()}})
}
func UpdateServiceTierPricing(c *gin.Context) {
	var policy servicetier.Policy
	if err := c.ShouldBindJSON(&policy); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Invalid service-tier policy"})
		return
	}
	if err := policy.Validate(); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": err.Error()})
		return
	}
	groups := ratio_setting.GetGroupRatioCopy()
	for _, list := range [][]string{policy.FastGroups, policy.UltrafastGroups} {
		for _, g := range list {
			if _, ok := groups[g]; !ok {
				c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Unknown group: " + g})
				return
			}
		}
	}
	if policy.Enabled {
		_, catalog, err := setting.ServiceTierPricing()
		if err != nil || !catalog.Fresh(time.Now()) {
			c.JSON(http.StatusConflict, gin.H{"success": false, "message": "Synchronize official prices before enabling acceleration"})
			return
		}
	}
	value, _ := json.Marshal(policy)
	if err := model.UpdateOption(servicetier.PolicyOption, string(value)); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
		return
	}
	GetServiceTierPricing(c)
}
func SyncServiceTierPricing(c *gin.Context) {
	if !serviceTierSyncLock.TryLock() {
		c.JSON(http.StatusConflict, gin.H{"success": false, "message": "Service-tier price sync is already running"})
		return
	}
	defer serviceTierSyncLock.Unlock()
	catalog, err := servicetier.FetchCatalog(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"success": false, "message": "Official price sync failed; existing prices were not changed: " + err.Error()})
		return
	}
	data, err := json.Marshal(catalog)
	if err == nil {
		err = model.UpdateOption(servicetier.CatalogOption, string(data))
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
		return
	}
	// A price refresh never changes permissions, markups or locked model prices.
	GetServiceTierPricing(c)
}
