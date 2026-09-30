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
	"net/http/httptest"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestGetLogsStatHonorsRequestIDFilters(t *testing.T) {
	previousDB, previousLogDB := model.DB, model.LOG_DB
	previousLogType := common.LogDatabaseType()
	db, err := gorm.Open(sqlite.Open("file:log-stats-request-filter?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	model.DB, model.LOG_DB = db, db
	common.SetLogDatabaseType(common.DatabaseTypeSQLite)
	t.Cleanup(func() {
		model.DB, model.LOG_DB = previousDB, previousLogDB
		common.SetLogDatabaseType(previousLogType)
	})
	require.NoError(t, db.AutoMigrate(&model.Log{}))
	require.NoError(t, db.Create(&model.Log{
		Type: model.LogTypeConsume, Quota: 11, PromptTokens: 2,
		RequestId: "request-a", UpstreamRequestId: "upstream-a",
	}).Error)
	require.NoError(t, db.Create(&model.Log{
		Type: model.LogTypeConsume, Quota: 29, PromptTokens: 3,
		RequestId: "request-b", UpstreamRequestId: "upstream-b",
	}).Error)

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest("GET", "/api/log/stat?request_id=request-a", nil)
	GetLogsStat(c)

	var response struct {
		Success bool `json:"success"`
		Data    struct {
			Quota int `json:"quota"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	require.True(t, response.Success)
	require.Equal(t, 11, response.Data.Quota)
}
