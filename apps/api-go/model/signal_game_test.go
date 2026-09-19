// Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later.
package model

import (
	"encoding/json"
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
)

func TestSignalGameClaimAndLeaderboardPrivacy(t *testing.T) {
	db := setupRedPacketTestDB(t, &User{}, &SignalGameAttempt{}, &SignalGameRecord{})
	require.NoError(t, db.Create(&User{Id: 1, Username: "player", AffCode: "signal-a", DisplayName: "Player"}).Error)
	require.NoError(t, db.Create(&User{Id: 2, Username: "other", AffCode: "signal-b", DisplayName: "Other"}).Error)
	attempt := SignalGameAttempt{TokenHash: "test-nonce", Seed: 42, Size: 5, Day: "2026-09-19", Actor: "ai", ModelId: "test-model", Harness: "codex", AgentName: "Test agent", StartedAt: 4000, CreatedAt: 1}
	require.NoError(t, db.Create(&attempt).Error)
	_, err := FinishSignalGameAttempt(attempt.TokenHash, "[1]", 1, 3999)
	require.ErrorIs(t, err, ErrSignalGameCountdown)
	done, err := FinishSignalGameAttempt(attempt.TokenHash, "[1]", 1, 5000)
	require.NoError(t, err)
	require.Equal(t, int64(1000), done.ElapsedMs)
	retry, err := FinishSignalGameAttempt(attempt.TokenHash, "[1]", 1, 8000)
	require.NoError(t, err)
	require.Equal(t, done.ElapsedMs, retry.ElapsedMs)
	_, err = FinishSignalGameAttempt(attempt.TokenHash, "[2]", 1, 9000)
	require.ErrorIs(t, err, ErrSignalGameConflict)
	row := SignalGameRecord{UserId: 1, AttemptId: &attempt.Id, Mode: "challenge", Day: attempt.Day, Size: 5, Moves: 10, ElapsedMs: 1000, Actor: "ai", ModelId: attempt.ModelId, Harness: attempt.Harness, AgentName: attempt.AgentName, ParticipantKey: "agent-key", Email: "private@example.test", Note: "public note", Actions: "[1]", Digest: "result-one", CreatedAt: 5000}
	require.NoError(t, SaveSignalGameRecord(&row))
	ranks, err := ListSignalGameLeaderboard(attempt.Day, 5)
	require.NoError(t, err)
	require.Empty(t, ranks)
	publish := row
	publish.Public = true
	require.NoError(t, SaveSignalGameRecord(&publish))
	ranks, err = ListSignalGameLeaderboard(attempt.Day, 5)
	require.NoError(t, err)
	require.Len(t, ranks, 1)
	require.Equal(t, "Player", ranks[0].Name)
	raw, _ := json.Marshal(ranks)
	require.False(t, strings.Contains(string(raw), "private@example.test"))
	require.False(t, strings.Contains(string(raw), "test-nonce"))
	theft := row
	theft.Id = 0
	theft.UserId = 2
	require.ErrorIs(t, SaveSignalGameRecord(&theft), ErrSignalGameConflict)
	own, err := ListSignalGameRecords(2)
	require.NoError(t, err)
	require.Empty(t, own)
	otherSize, err := ListSignalGameLeaderboard(attempt.Day, 64)
	require.NoError(t, err)
	require.Empty(t, otherSize)
	practice := SignalGameRecord{UserId: 1, Mode: "practice", Day: attempt.Day, Size: 5, Moves: 1, Public: true, ParticipantKey: "agent-key", Digest: "practice", CreatedAt: 1000}
	require.NoError(t, db.Create(&practice).Error)
	better := SignalGameRecord{UserId: 1, Mode: "challenge", Day: attempt.Day, Size: 5, Moves: 9, ElapsedMs: 5000, Public: true, ParticipantKey: "agent-key", Digest: "better", CreatedAt: 6000}
	require.NoError(t, db.Create(&better).Error)
	ranks, err = ListSignalGameLeaderboard(attempt.Day, 5)
	require.NoError(t, err)
	require.Len(t, ranks, 1)
	require.Equal(t, 9, ranks[0].Moves)
}
