// Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later.
package controller

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/service/signalgames"
	"github.com/gin-gonic/gin"
	"net/mail"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

func signalHash(s string) string {
	digest := sha256.Sum256([]byte(s))
	return hex.EncodeToString(digest[:])
}
func signalError(c *gin.Context, status int, message string) {
	c.JSON(status, gin.H{"success": false, "message": message})
}
func validSignalActor(actor, modelId, harness, agent string) bool {
	return (actor == "human" && modelId == "" && harness == "" && agent == "") || (actor == "ai" && strings.TrimSpace(modelId) != "" && len(modelId) <= 100 && strings.TrimSpace(harness) != "" && len(harness) <= 60 && strings.TrimSpace(agent) != "" && len(agent) <= 80)
}
func GetSignalGameDaily(c *gin.Context) {
	size, err := strconv.Atoi(c.DefaultQuery("size", "5"))
	if err != nil || !signalgames.ValidSize(size) {
		signalError(c, 400, "Invalid board size")
		return
	}
	day := time.Now().UTC().Format("2006-01-02")
	seed, _ := signalgames.DailySeed(day, size)
	c.JSON(200, gin.H{"success": true, "data": gin.H{"day": day, "seed": seed, "size": size, "rules_version": signalgames.RulesVersion, "tiles": signalgames.Generate(seed, size).Tiles, "max_moves": signalgames.MaxMoves(size)}})
}
func BeginSignalGameAttempt(c *gin.Context) {
	var input struct {
		Size      int    `json:"size"`
		Actor     string `json:"actor"`
		ModelId   string `json:"model_id"`
		Harness   string `json:"harness"`
		AgentName string `json:"agent_name"`
	}
	if err := c.ShouldBindJSON(&input); err != nil || !signalgames.ValidSize(input.Size) || !validSignalActor(input.Actor, input.ModelId, input.Harness, input.AgentName) {
		signalError(c, 400, "Invalid challenge participant")
		return
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		signalError(c, 503, "Challenge unavailable")
		return
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	now := time.Now()
	day := now.UTC().Format("2006-01-02")
	seed, _ := signalgames.DailySeed(day, input.Size)
	row := model.SignalGameAttempt{TokenHash: signalHash(token), Seed: seed, Size: input.Size, Day: day, Actor: input.Actor, ModelId: input.ModelId, Harness: input.Harness, AgentName: input.AgentName, StartedAt: now.UnixMilli() + 3000, CreatedAt: now.Unix()}
	if err := model.CreateSignalGameAttempt(&row); err != nil {
		signalError(c, 503, "Challenge unavailable")
		return
	}
	c.JSON(200, gin.H{"success": true, "data": gin.H{"token": token, "seed": seed, "size": row.Size, "day": day, "rules_version": signalgames.RulesVersion, "countdown_seconds": 3, "max_moves": signalgames.MaxMoves(row.Size)}})
}
func FinishSignalGameAttempt(c *gin.Context) {
	var input struct {
		Token   string `json:"token"`
		Actions []int  `json:"actions"`
	}
	if err := c.ShouldBindJSON(&input); err != nil || len(input.Token) != 43 {
		signalError(c, 400, "Invalid challenge result")
		return
	}
	row, err := model.FindSignalGameAttempt(signalHash(input.Token))
	if err != nil {
		signalError(c, 404, "Challenge not found")
		return
	}
	if _, err = signalgames.Replay(row.Seed, row.Size, input.Actions, false); err != nil {
		signalError(c, 400, "Challenge replay rejected")
		return
	}
	actions, _ := json.Marshal(input.Actions)
	row, err = model.FinishSignalGameAttempt(row.TokenHash, string(actions), len(input.Actions), time.Now().UnixMilli())
	if err != nil {
		signalError(c, 409, "Challenge is expired, changed, or still counting down")
		return
	}
	c.JSON(200, gin.H{"success": true, "data": gin.H{"moves": row.Moves, "elapsed_ms": row.ElapsedMs, "finished_at": row.FinishedAt}})
}
func GetSignalGameLeaderboard(c *gin.Context) {
	day := c.DefaultQuery("day", time.Now().UTC().Format("2006-01-02"))
	size, err := strconv.Atoi(c.DefaultQuery("size", "5"))
	if err != nil {
		signalError(c, 400, "Invalid board size")
		return
	}
	if _, err = signalgames.DailySeed(day, size); err != nil {
		signalError(c, 400, "Invalid challenge")
		return
	}
	rows, err := model.ListSignalGameLeaderboard(day, size)
	if err != nil {
		signalError(c, 503, "Leaderboard unavailable")
		return
	}
	c.JSON(200, gin.H{"success": true, "data": gin.H{"day": day, "size": size, "entries": rows}})
}
func GetSignalGameRecords(c *gin.Context) {
	rows, err := model.ListSignalGameRecords(c.GetInt("id"))
	if err != nil {
		signalError(c, 503, "Records unavailable")
		return
	}
	c.JSON(200, gin.H{"success": true, "data": rows})
}
func SaveSignalGameRecord(c *gin.Context) {
	var input struct {
		ExpectedUserId int    `json:"expected_user_id"`
		Mode           string `json:"mode"`
		Size           int    `json:"size"`
		Seed           uint32 `json:"seed"`
		RulesVersion   int    `json:"rules_version"`
		Actions        []int  `json:"actions"`
		Token          string `json:"token"`
		Publish        bool   `json:"publish"`
		Actor          string `json:"actor"`
		ModelId        string `json:"model_id"`
		Harness        string `json:"harness"`
		AgentName      string `json:"agent_name"`
		Email          string `json:"email"`
		Note           string `json:"note"`
	}
	if err := c.ShouldBindJSON(&input); err != nil || input.RulesVersion != signalgames.RulesVersion || !utf8.ValidString(input.Note) || utf8.RuneCountInString(input.Note) > 160 || len(input.Email) > 254 {
		signalError(c, 400, "Invalid game record")
		return
	}
	if input.ExpectedUserId != 0 && input.ExpectedUserId != c.GetInt("id") {
		signalError(c, 409, "Account changed before submission")
		return
	}
	if input.Email != "" {
		address, err := mail.ParseAddress(input.Email)
		if err != nil || address.Address != input.Email {
			signalError(c, 400, "Invalid email")
			return
		}
	}
	row := model.SignalGameRecord{UserId: c.GetInt("id"), Mode: input.Mode, RulesVersion: signalgames.RulesVersion, Email: input.Email, Note: input.Note, Public: input.Publish, CreatedAt: time.Now().Unix()}
	if input.Mode == "challenge" {
		if len(input.Token) != 43 {
			signalError(c, 400, "Completed challenge required")
			return
		}
		attempt, err := model.FindSignalGameAttempt(signalHash(input.Token))
		if err != nil || attempt.FinishedAt == 0 {
			signalError(c, 400, "Completed challenge required")
			return
		}
		row.AttemptId = &attempt.Id
		row.Day = attempt.Day
		row.Seed = attempt.Seed
		row.Size = attempt.Size
		row.Moves = attempt.Moves
		row.ElapsedMs = attempt.ElapsedMs
		row.Actions = attempt.Actions
		row.Actor = attempt.Actor
		row.ModelId = attempt.ModelId
		row.Harness = attempt.Harness
		row.AgentName = attempt.AgentName
		row.Digest = signalHash(fmt.Sprintf("challenge:%d", attempt.Id))
	} else if input.Mode == "practice" {
		if input.Publish || !validSignalActor(input.Actor, input.ModelId, input.Harness, input.AgentName) {
			signalError(c, 400, "Practice results are private")
			return
		}
		hints, err := signalgames.Replay(input.Seed, input.Size, input.Actions, true)
		if err != nil {
			signalError(c, 400, "Game replay rejected")
			return
		}
		actions, _ := json.Marshal(input.Actions)
		row.Size = input.Size
		row.Seed = input.Seed
		row.Moves = len(input.Actions)
		row.Hints = hints
		row.Actions = string(actions)
		row.Actor = input.Actor
		row.ModelId = input.ModelId
		row.Harness = input.Harness
		row.AgentName = input.AgentName
		row.Digest = signalHash(fmt.Sprintf("practice:%d:%d:%d:%s:%s:%s:%s:%s", row.UserId, row.Size, row.Seed, actions, row.Actor, row.ModelId, row.Harness, row.AgentName))
	} else {
		signalError(c, 400, "Invalid game mode")
		return
	}
	identity, _ := json.Marshal([]string{row.Actor, row.ModelId, row.Harness, row.AgentName})
	row.ParticipantKey = signalHash(string(identity))
	if err := model.SaveSignalGameRecord(&row); err != nil {
		if errors.Is(err, model.ErrSignalGameConflict) {
			signalError(c, 409, "This result belongs to another account")
			return
		}
		signalError(c, 503, "Could not save game record")
		return
	}
	c.JSON(200, gin.H{"success": true, "data": row})
}
