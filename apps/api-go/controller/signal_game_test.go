// Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later.
package controller

import (
	"bytes"
	"encoding/json"
	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/middleware"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/service/signalgames"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSignalGameGuestCompletionThenAuthenticatedClaim(t *testing.T) {
	db := setupManageUserTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.SignalGameAttempt{}, &model.SignalGameRecord{}))
	user := model.User{Username: "game-player", DisplayName: "Game player", AffCode: "signal-owner", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, AuthVersion: 1}
	user.SetAccessToken("synthetic-game-test-pat")
	require.NoError(t, db.Create(&user).Error)
	router := gin.New()
	router.POST("/start", BeginSignalGameAttempt)
	router.POST("/finish", FinishSignalGameAttempt)
	router.POST("/records", middleware.UserAuth(), SaveSignalGameRecord)
	router.GET("/records", middleware.UserAuth(), GetSignalGameRecords)
	router.GET("/ranking", GetSignalGameLeaderboard)
	call := func(method, path string, body any, auth bool) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(body)
		request := httptest.NewRequest(method, path, bytes.NewReader(raw))
		request.Header.Set("Content-Type", "application/json")
		if auth {
			request.Header.Set("Authorization", "Bearer synthetic-game-test-pat")
		}
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		return response
	}
	require.Equal(t, 401, call("POST", "/records", map[string]any{}, false).Code)
	require.Equal(t, 400, call("POST", "/start", map[string]any{"size": 5, "actor": "ai"}, false).Code)
	start := call("POST", "/start", map[string]any{"size": 5, "actor": "ai", "model_id": "test-model", "harness": "codex", "agent_name": "Test agent"}, false)
	require.Equal(t, 200, start.Code)
	var envelope struct {
		Data struct {
			Token string `json:"token"`
			Seed  uint32 `json:"seed"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(start.Body.Bytes(), &envelope))
	token := envelope.Data.Token
	c := signalgames.Generate(envelope.Data.Seed, 5)
	tiles := append([]int(nil), c.Tiles...)
	actions := []int{}
	for _, index := range c.Route {
		for tiles[index] != c.Solution[index] && !signalgames.Won(tiles, 5) {
			tiles[index] = signalgames.Rotate(tiles[index])
			actions = append(actions, index)
		}
	}
	require.Equal(t, 409, call("POST", "/finish", map[string]any{"token": token, "actions": actions}, false).Code)
	require.NoError(t, db.Model(&model.SignalGameAttempt{}).Where("token_hash = ?", signalHash(token)).Update("started_at", time.Now().UnixMilli()-500).Error)
	require.Equal(t, 400, call("POST", "/finish", map[string]any{"token": token, "actions": []int{-1}}, false).Code)
	require.Equal(t, 200, call("POST", "/finish", map[string]any{"token": token, "actions": actions}, false).Code)
	submit := map[string]any{"mode": "challenge", "rules_version": 2, "token": token, "publish": true, "email": "private@example.test", "note": "Testing", "user_id": 999, "moves": 0, "elapsed_ms": 0, "model_id": "forged-model"}
	require.Equal(t, 401, call("POST", "/records", submit, false).Code)
	saved := call("POST", "/records", submit, true)
	require.Equal(t, 200, saved.Code, saved.Body.String())
	require.NotContains(t, saved.Body.String(), "private@example.test")
	var row model.SignalGameRecord
	require.NoError(t, db.First(&row).Error)
	require.Equal(t, user.Id, row.UserId)
	require.Equal(t, len(actions), row.Moves)
	require.GreaterOrEqual(t, row.ElapsedMs, int64(500))
	require.Equal(t, "test-model", row.ModelId)
	require.Equal(t, 200, call("POST", "/records", submit, true).Code)
	var count int64
	require.NoError(t, db.Model(&model.SignalGameRecord{}).Count(&count).Error)
	require.Equal(t, int64(1), count)
	ranking := call("GET", "/ranking?size=5", nil, false)
	require.Equal(t, 200, ranking.Code)
	require.Contains(t, ranking.Body.String(), "Test agent")
	require.False(t, strings.Contains(ranking.Body.String(), token))
	require.NotContains(t, ranking.Body.String(), "private@example.test")
}
