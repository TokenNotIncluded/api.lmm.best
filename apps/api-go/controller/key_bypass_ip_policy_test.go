/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/

package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

var bypassTestUserSeq atomic.Int64

func nextBypassTestAffCode() string {
	return "bypass-aff-" + strconv.FormatInt(bypassTestUserSeq.Add(1), 10)
}

func TestRelayTokenKeyFromAuthorizationHeader(t *testing.T) {
	cases := []struct {
		name   string
		header string
		want   string
	}{
		{name: "empty header", header: "", want: ""},
		{name: "bearer prefix", header: "Bearer abc123", want: "abc123"},
		{name: "lowercase bearer prefix", header: "bearer abc123", want: "abc123"},
		{name: "sk- prefix stripped", header: "Bearer sk-abc123", want: "abc123"},
		{name: "group suffix dropped", header: "Bearer sk-abc123-mygroup", want: "abc123"},
		{name: "raw key without bearer", header: "abc123", want: "abc123"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, relayTokenKeyFromAuthorizationHeader(testCase.header))
		})
	}
}

func TestKeyBypassesIPPolicyWithoutAuthorizationHeaderNeverTouchesDatabase(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/internal/access-ip-policy", nil)

	// model.DB stays nil: any accidental DB access here would panic, proving
	// the empty-header case short-circuits before touching the database.
	assert.False(t, keyBypassesIPPolicy(c))
}

func setupKeyBypassIPPolicyTest(t *testing.T) {
	t.Helper()
	previousDB := model.DB
	previousRedis := common.RedisEnabled
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.Token{}))
	model.DB = db
	common.RedisEnabled = false
	t.Cleanup(func() {
		model.DB = previousDB
		common.RedisEnabled = previousRedis
	})
}

func createBypassTestUser(t *testing.T, trustLevelOverride int, allowBypass bool) *model.User {
	t.Helper()
	settingJSON, err := json.Marshal(map[string]any{
		"allow_key_bypass_ip_policy": allowBypass,
	})
	require.NoError(t, err)
	user := &model.User{
		Username:           "bypass-test-" + t.Name(),
		Password:           "irrelevant-password",
		Role:               common.RoleCommonUser,
		Status:             common.UserStatusEnabled,
		Setting:            string(settingJSON),
		TrustLevelOverride: &trustLevelOverride,
		AffCode:            nextBypassTestAffCode(),
	}
	require.NoError(t, model.DB.Create(user).Error)
	return user
}

func createBypassTestToken(t *testing.T, userId int, key string) {
	t.Helper()
	token := &model.Token{
		UserId:         userId,
		Key:            key,
		Status:         common.TokenStatusEnabled,
		Name:           "bypass-test-token",
		UnlimitedQuota: true,
		ExpiredTime:    -1,
	}
	require.NoError(t, model.DB.Create(token).Error)
}

// rejectingRoutingRules force a reject decision (fenced on a country that is
// never present in these tests) so a StatusNoContent result can only mean the
// bypass short-circuit fired, not that the underlying policy allowed it.
const rejectingRoutingRules = "dip(geoip:cn) -> reject\nfallback: reject"

func TestCheckIPAccessRoutingPolicyKeyBypass(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupKeyBypassIPPolicyTest(t)

	originalRules := setting.GetIPAccessRoutingRules()
	require.NoError(t, setting.UpdateIPAccessRoutingRules(rejectingRoutingRules))
	t.Cleanup(func() {
		require.NoError(t, setting.UpdateIPAccessRoutingRules(originalRules))
	})

	newRejectedRequest := func(authorization string) *gin.Context {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodGet, "/internal/access-ip-policy", nil)
		c.Request.RemoteAddr = "127.0.0.1:42000"
		c.Request.Header.Set("X-LMM-CN-Source", "1")
		c.Request.Header.Set("X-Original-Client-IP", "203.0.113.8")
		if authorization != "" {
			c.Request.Header.Set("Authorization", authorization)
		}
		return c
	}

	t.Run("no Authorization header falls through to normal reject", func(t *testing.T) {
		c := newRejectedRequest("")
		CheckIPAccessRoutingPolicy(c)
		assert.Equal(t, http.StatusForbidden, c.Writer.Status())
	})

	t.Run("unknown key falls through to normal reject", func(t *testing.T) {
		c := newRejectedRequest("Bearer sk-doesnotexist")
		CheckIPAccessRoutingPolicy(c)
		assert.Equal(t, http.StatusForbidden, c.Writer.Status())
	})

	t.Run("valid key but trust level below L1 falls through to normal reject", func(t *testing.T) {
		user := createBypassTestUser(t, 0, true)
		createBypassTestToken(t, user.Id, "l0bypasskey")

		c := newRejectedRequest("Bearer sk-l0bypasskey")
		CheckIPAccessRoutingPolicy(c)
		assert.Equal(t, http.StatusForbidden, c.Writer.Status())
	})

	t.Run("L1+ user with bypass disabled falls through to normal reject", func(t *testing.T) {
		user := createBypassTestUser(t, 1, false)
		createBypassTestToken(t, user.Id, "l1disabledkey")

		c := newRejectedRequest("Bearer sk-l1disabledkey")
		CheckIPAccessRoutingPolicy(c)
		assert.Equal(t, http.StatusForbidden, c.Writer.Status())
	})

	t.Run("disabled account with bypass enabled falls through to normal reject", func(t *testing.T) {
		settingJSON, err := json.Marshal(map[string]any{"allow_key_bypass_ip_policy": true})
		require.NoError(t, err)
		trustLevel := 1
		user := &model.User{
			Username:           "bypass-test-disabled-" + t.Name(),
			Password:           "irrelevant-password",
			Role:               common.RoleCommonUser,
			Status:             common.UserStatusDisabled,
			Setting:            string(settingJSON),
			TrustLevelOverride: &trustLevel,
			AffCode:            nextBypassTestAffCode(),
		}
		require.NoError(t, model.DB.Create(user).Error)
		createBypassTestToken(t, user.Id, "disabledaccountkey")

		c := newRejectedRequest("Bearer sk-disabledaccountkey")
		CheckIPAccessRoutingPolicy(c)
		assert.Equal(t, http.StatusForbidden, c.Writer.Status())
	})

	t.Run("L1+ user with bypass enabled and valid key skips policy evaluation", func(t *testing.T) {
		user := createBypassTestUser(t, 1, true)
		createBypassTestToken(t, user.Id, "l1enabledkey")

		c := newRejectedRequest("Bearer sk-l1enabledkey")
		CheckIPAccessRoutingPolicy(c)
		assert.Equal(t, http.StatusNoContent, c.Writer.Status())
		assert.Empty(t, c.Writer.Header().Get(accessPolicyResultHeader))
	})
}
