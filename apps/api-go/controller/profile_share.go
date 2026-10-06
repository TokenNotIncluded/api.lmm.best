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
	profiles := share.LinkedProfiles
	if profiles == nil {
		profiles = []model.ProfileLinkedProfile{}
	}
	if err := validateProfileLinkedProfiles(profiles, time.Now()); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Unable to load profile sharing settings"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"enabled":                 true,
			"model_usage_enabled":     share.ModelUsageEnabled,
			"aggregate_usage_enabled": share.AggregateUsageEnabled,
			"linked_profiles":         profiles,
			"aggregate_sources":       resolveProfileAggregateSources(c.Request.Context(), share, "30d", time.Now()),
			"token":                   share.Token,
			"url":                     profileShareDestination + "/api/share/profile/" + share.Token + ".svg",
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
		ModelUsageEnabled     *bool                         `json:"model_usage_enabled"`
		AggregateUsageEnabled *bool                         `json:"aggregate_usage_enabled"`
		LinkedProfiles        *[]model.ProfileLinkedProfile `json:"linked_profiles"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(c.Writer, c.Request.Body, ProfileShareSettingsMaxBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil && !errors.Is(err, io.EOF) {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Invalid profile sharing settings"})
		return
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Invalid profile sharing settings"})
		return
	}
	if request.LinkedProfiles != nil {
		if err := validateProfileLinkedProfiles(*request.LinkedProfiles, time.Now()); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": err.Error()})
			return
		}
	}
	share, err := model.EnableProfileShare(c.GetInt("id"))
	if err == nil {
		share, err = model.SetProfileShareSettings(share, request.ModelUsageEnabled, request.AggregateUsageEnabled, request.LinkedProfiles)
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
	} else if query.Get("layout") == "aggregate" {
		options, err = parseProfileShareAggregateSVGOptions(query)
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
	if options.Layout == "aggregate" {
		share, shareErr := model.GetProfileShare(owner.Id)
		if shareErr != nil || share == nil || share.Token != token || !share.AggregateUsageEnabled || validateProfileLinkedProfiles(share.LinkedProfiles, time.Now()) != nil {
			c.Status(http.StatusNotFound)
			return
		}
		sources := resolveProfileAggregateSources(c.Request.Context(), share, options.Period, time.Now())
		// External fetches can take seconds. Recheck permission and configuration
		// after they finish; no cache can keep a revoked token/configuration alive.
		current, readErr := model.GetProfileShare(owner.Id)
		if readErr != nil || current == nil || current.Token != token || !current.AggregateUsageEnabled {
			c.Status(http.StatusNotFound)
			return
		}
		oldConfig, _ := json.Marshal(share.LinkedProfiles)
		newConfig, _ := json.Marshal(current.LinkedProfiles)
		currentOwner, ownerErr := model.GetProfileShareOwner(token)
		if string(oldConfig) != string(newConfig) || ownerErr != nil || currentOwner == nil {
			c.Status(http.StatusNotFound)
			return
		}
		svg = renderProfileShareAggregateSVG(options, sources)
	} else if options.Layout == "models" {
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
		svg, err = renderProfileShareModelsSVG(options, usage, start, now.Unix())
		if err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"success": false, "code": "CREDIT_UNITS_UNAVAILABLE", "message": "Unable to display currency amounts"})
			return
		}
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
