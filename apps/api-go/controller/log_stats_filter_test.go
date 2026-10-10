/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
package controller

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type logStatsResponse struct {
	Success bool `json:"success"`
	Data    struct {
		Quota int `json:"quota"`
		Rpm   int `json:"rpm"`
		Tpm   int `json:"tpm"`
	} `json:"data"`
}

func setupLogStatsFilterFixture(t *testing.T) {
	t.Helper()
	previousDB, previousLogDB := model.DB, model.LOG_DB
	previousLogType := common.LogDatabaseType()
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", uuid.NewString())), &gorm.Config{})
	require.NoError(t, err)
	model.DB, model.LOG_DB = db, db
	common.SetLogDatabaseType(common.DatabaseTypeSQLite)
	t.Cleanup(func() {
		model.DB, model.LOG_DB = previousDB, previousLogDB
		common.SetLogDatabaseType(previousLogType)
	})
	require.NoError(t, db.AutoMigrate(&model.Log{}))
	now := time.Now().Unix()
	rows := []model.Log{
		{UserId: 42, Username: "old-name", CreatedAt: now, Type: model.LogTypeConsume, Quota: 11, PromptTokens: 2, CompletionTokens: 1, RequestId: "request-a", UpstreamRequestId: "upstream-a"},
		{UserId: 42, Username: "old-name", CreatedAt: now, Type: model.LogTypeConsume, Quota: 29, PromptTokens: 3, CompletionTokens: 4, RequestId: "request-b", UpstreamRequestId: "upstream-b"},
		{UserId: 43, Username: "other-name", CreatedAt: now, Type: model.LogTypeConsume, Quota: 100, PromptTokens: 5, CompletionTokens: 6, RequestId: "request-a", UpstreamRequestId: "upstream-other"},
	}
	require.NoError(t, db.Create(&rows).Error)
}

func callLogStats(t *testing.T, handler gin.HandlerFunc, query string, userID int, username string) logStatsResponse {
	t.Helper()
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest("GET", "/api/log/stat?"+query, nil)
	if userID != 0 {
		c.Set("id", userID)
	}
	if username != "" {
		c.Set("username", username)
	}
	handler(c)
	var response logStatsResponse
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	require.True(t, response.Success)
	return response
}

func TestGetLogsStatHonorsRequestIDFilters(t *testing.T) {
	setupLogStatsFilterFixture(t)
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name      string
		query     string
		wantQuota int
		wantRpm   int
		wantTpm   int
	}{
		{name: "request id", query: "request_id=request-a", wantQuota: 111, wantRpm: 2, wantTpm: 14},
		{name: "upstream request id", query: "upstream_request_id=upstream-b", wantQuota: 29, wantRpm: 1, wantTpm: 7},
		{name: "both request ids", query: "request_id=request-a&upstream_request_id=upstream-a", wantQuota: 11, wantRpm: 1, wantTpm: 3},
		{name: "no match", query: "request_id=missing", wantQuota: 0, wantRpm: 0, wantTpm: 0},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := callLogStats(t, GetLogsStat, test.query, 0, "")
			require.Equal(t, test.wantQuota, response.Data.Quota)
			require.Equal(t, test.wantRpm, response.Data.Rpm)
			require.Equal(t, test.wantTpm, response.Data.Tpm)
		})
	}
}

func TestGetLogsSelfStatHonorsRequestIDAndAccountScope(t *testing.T) {
	setupLogStatsFilterFixture(t)
	gin.SetMode(gin.TestMode)
	response := callLogStats(t, GetLogsSelfStat, "request_id=request-a", 42, "old-name")
	require.Equal(t, 11, response.Data.Quota)
	require.Equal(t, 1, response.Data.Rpm)
	require.Equal(t, 3, response.Data.Tpm)
}
