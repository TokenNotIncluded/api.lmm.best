package controller

import (
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/gin-gonic/gin"
)

var profileShareTokenPattern = regexp.MustCompile(`^[0-9a-f]{48}$`)

func profileShareSelfResponse(c *gin.Context, share *model.ProfileShare) {
	if share == nil {
		c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"enabled": false}})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"enabled": true,
			"token":   share.Token,
			"url":     profileShareDestination + "/api/share/profile/" + share.Token + ".svg",
		},
	})
}

func GetSelfProfileShare(c *gin.Context) {
	share, err := model.GetProfileShare(c.GetInt("id"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Unable to load profile sharing"})
		return
	}
	profileShareSelfResponse(c, share)
}

func EnableSelfProfileShare(c *gin.Context) {
	share, err := model.EnableProfileShare(c.GetInt("id"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Unable to enable profile sharing"})
		return
	}
	profileShareSelfResponse(c, share)
}

func DisableSelfProfileShare(c *gin.Context) {
	if err := model.DisableProfileShare(c.GetInt("id")); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Unable to disable profile sharing"})
		return
	}
	profileShareSelfResponse(c, nil)
}

func GetPublicProfileShareSVG(c *gin.Context) {
	pathToken := c.Param("token")
	if !strings.HasSuffix(pathToken, ".svg") {
		c.Status(http.StatusNotFound)
		return
	}
	token := strings.TrimSuffix(pathToken, ".svg")
	if !profileShareTokenPattern.MatchString(token) {
		c.Status(http.StatusNotFound)
		return
	}
	if len(c.Request.URL.RawQuery) > 2048 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "SVG options are too long"})
		return
	}
	options, err := parseProfileShareSVGOptions(c.Request.URL.Query())
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": err.Error()})
		return
	}
	owner, err := model.GetProfileShareOwner(token)
	if err != nil || owner == nil {
		c.Status(http.StatusNotFound)
		return
	}
	var svg string
	if options.Layout == "profile" {
		endDay := time.Now().UTC().Truncate(24 * time.Hour).Unix()
		startDay := endDay - 370*86400
		rows, queryErr := model.GetProfileShareYearDays(owner.Id, startDay, endDay)
		if queryErr != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Unable to load usage"})
			return
		}
		svg = renderProfileShareProfileSVG(options, owner, rows, startDay)
	} else {
		usage, queryErr := model.GetProfileShareUsage(owner.Id, profileSharePeriodStart(options.Period, time.Now()))
		if queryErr != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Unable to load usage"})
			return
		}
		svg = renderProfileShareSVG(options, usage)
	}
	c.Header("Cache-Control", "no-store")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'")
	c.Header("Access-Control-Allow-Origin", "*")
	c.Data(http.StatusOK, "image/svg+xml; charset=utf-8", []byte(svg))
}
