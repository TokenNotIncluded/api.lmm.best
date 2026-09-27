package controller

import (
	"encoding/json"
	"errors"
	"io"
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
			"enabled":             true,
			"model_usage_enabled": share.ModelUsageEnabled,
			"token":               share.Token,
			"url":                 profileShareDestination + "/api/share/profile/" + share.Token + ".svg",
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
	var request struct {
		ModelUsageEnabled *bool `json:"model_usage_enabled"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(c.Writer, c.Request.Body, 1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil && !errors.Is(err, io.EOF) {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Invalid profile sharing settings"})
		return
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Invalid profile sharing settings"})
		return
	}
	share, err := model.EnableProfileShare(c.GetInt("id"))
	if err == nil && request.ModelUsageEnabled != nil {
		share, err = model.SetProfileShareModelUsage(share, *request.ModelUsageEnabled)
	}
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
	c.Header("Cache-Control", "no-store")
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
	query := c.Request.URL.Query()
	var options profileShareSVGOptions
	var top int
	var err error
	if query.Get("layout") == "models" {
		options, top, err = parseProfileShareModelsSVGOptions(query)
	} else {
		options, err = parseProfileShareSVGOptions(query)
	}
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
	if options.Layout == "models" {
		share, shareErr := model.GetProfileShare(owner.Id)
		if shareErr != nil || share == nil || share.Token != token || !share.ModelUsageEnabled {
			c.Status(http.StatusNotFound)
			return
		}
		now := time.Now()
		start := profileSharePeriodStart(options.Period, now)
		usage, queryErr := model.GetProfileShareModelUsage(owner.Id, start, now.Unix(), top)
		if queryErr != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Unable to load usage"})
			return
		}
		svg = renderProfileShareModelsSVG(options, usage, start, now.Unix())
	} else if options.Layout == "profile" {
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
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'")
	c.Header("Access-Control-Allow-Origin", "*")
	c.Data(http.StatusOK, "image/svg+xml; charset=utf-8", []byte(svg))
}
