// Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later.
package model

import (
	"errors"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"time"
)

var ErrSignalGameConflict = errors.New("game result belongs to another account or has changed")
var ErrSignalGameExpired = errors.New("challenge expired")
var ErrSignalGameCountdown = errors.New("countdown has not ended")

type SignalGameAttempt struct {
	Id         int    `gorm:"primaryKey"`
	TokenHash  string `gorm:"size:64;uniqueIndex;not null"`
	Seed       uint32 `gorm:"not null"`
	Size       int    `gorm:"not null"`
	Day        string `gorm:"size:10;not null"`
	Actor      string `gorm:"size:8;not null"`
	ModelId    string `gorm:"size:100"`
	Harness    string `gorm:"size:60"`
	AgentName  string `gorm:"size:80"`
	StartedAt  int64  `gorm:"not null"`
	FinishedAt int64
	Moves      int
	ElapsedMs  int64
	Actions    string `gorm:"type:text"`
	CreatedAt  int64  `gorm:"index;not null"`
}
type SignalGameRecord struct {
	Id             int    `json:"id" gorm:"primaryKey"`
	UserId         int    `json:"-" gorm:"index:idx_signal_user_created,priority:1"`
	AttemptId      *int   `json:"-" gorm:"uniqueIndex"`
	Mode           string `json:"mode" gorm:"size:16;not null"`
	Day            string `json:"day" gorm:"size:10;index:idx_signal_daily,priority:1;not null"`
	Size           int    `json:"size" gorm:"index:idx_signal_daily,priority:2;not null"`
	Seed           uint32 `json:"seed" gorm:"not null"`
	RulesVersion   int    `json:"rules_version" gorm:"not null"`
	Moves          int    `json:"moves" gorm:"not null"`
	ElapsedMs      int64  `json:"elapsed_ms" gorm:"not null"`
	Hints          int    `json:"hints" gorm:"not null"`
	Actor          string `json:"actor" gorm:"size:8;not null"`
	ModelId        string `json:"model_id" gorm:"size:100"`
	Harness        string `json:"harness" gorm:"size:60"`
	AgentName      string `json:"agent_name" gorm:"size:80"`
	ParticipantKey string `json:"-" gorm:"size:64;not null"`
	Email          string `json:"-" gorm:"size:254"`
	Note           string `json:"note" gorm:"size:160"`
	Actions        string `json:"-" gorm:"type:text;not null"`
	Digest         string `json:"-" gorm:"size:64;uniqueIndex;not null"`
	Public         bool   `json:"public" gorm:"index:idx_signal_daily,priority:3;not null"`
	CreatedAt      int64  `json:"created_at" gorm:"index:idx_signal_user_created,priority:2;not null"`
}
type SignalGameRank struct {
	Player     int    `json:"player"`
	Name       string `json:"name"`
	Actor      string `json:"actor"`
	ModelId    string `json:"model_id"`
	Harness    string `json:"harness"`
	AgentName  string `json:"agent_name"`
	Note       string `json:"note"`
	Moves      int    `json:"moves"`
	ElapsedMs  int64  `json:"elapsed_ms"`
	FinishedAt int64  `json:"finished_at"`
}

func CreateSignalGameAttempt(row *SignalGameAttempt) error {
	if err := DB.Where("finished_at = 0 AND created_at < ?", time.Now().Add(-24*time.Hour).Unix()).Delete(&SignalGameAttempt{}).Error; err != nil {
		return err
	}
	return DB.Create(row).Error
}
func FindSignalGameAttempt(hash string) (SignalGameAttempt, error) {
	var row SignalGameAttempt
	err := DB.Where("token_hash = ?", hash).Take(&row).Error
	return row, err
}
func FinishSignalGameAttempt(hash, actions string, moves int, now int64) (SignalGameAttempt, error) {
	var result SignalGameAttempt
	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("token_hash = ?", hash).Take(&result).Error; err != nil {
			return err
		}
		if result.FinishedAt != 0 {
			if result.Actions != actions {
				return ErrSignalGameConflict
			}
			return nil
		}
		if now < result.StartedAt {
			return ErrSignalGameCountdown
		}
		if now-result.StartedAt > 24*60*60*1000 {
			return ErrSignalGameExpired
		}
		result.FinishedAt = now
		result.ElapsedMs = now - result.StartedAt
		result.Actions = actions
		result.Moves = moves
		return tx.Save(&result).Error
	})
	return result, err
}
func SaveSignalGameRecord(record *SignalGameRecord) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		if record.AttemptId != nil {
			var attempt SignalGameAttempt
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Select("id").Where("id = ?", *record.AttemptId).Take(&attempt).Error; err != nil {
				return err
			}
		}
		var user User
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Select("id").Where("id = ?", record.UserId).Take(&user).Error; err != nil {
			return err
		}
		var existing SignalGameRecord
		query := tx.Where("digest = ?", record.Digest)
		if record.AttemptId != nil {
			query = tx.Where("attempt_id = ?", *record.AttemptId)
		}
		err := query.Take(&existing).Error
		if err == nil {
			if existing.UserId != record.UserId {
				return ErrSignalGameConflict
			}
			if record.Public && !existing.Public {
				if err = tx.Model(&existing).Update("public", true).Error; err != nil {
					return err
				}
				existing.Public = true
			}
			*record = existing
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		var count int64
		if err = tx.Model(&SignalGameRecord{}).Where("user_id = ? AND created_at >= ?", record.UserId, time.Now().UTC().Truncate(24*time.Hour).Unix()).Count(&count).Error; err != nil {
			return err
		}
		if count >= 100 {
			return errors.New("daily record limit reached")
		}
		return tx.Create(record).Error
	})
}
func ListSignalGameRecords(userId int) ([]SignalGameRecord, error) {
	records := make([]SignalGameRecord, 0)
	err := DB.Where("user_id = ?", userId).Order("id DESC").Limit(30).Find(&records).Error
	return records, err
}
func ListSignalGameLeaderboard(day string, size int) ([]SignalGameRank, error) {
	ranks := make([]SignalGameRank, 0)
	err := DB.Raw(`SELECT r.user_id AS player, COALESCE(NULLIF(u.display_name,''),u.username) AS name, r.actor,r.model_id,r.harness,r.agent_name,r.note,r.moves,r.elapsed_ms,r.created_at AS finished_at
 FROM signal_game_records r JOIN users u ON u.id=r.user_id
 WHERE r.day=? AND r.size=? AND r.mode='challenge' AND r.public=? AND u.deleted_at IS NULL
 AND NOT EXISTS (SELECT 1 FROM signal_game_records b WHERE b.day=r.day AND b.size=r.size AND b.mode='challenge' AND b.public=? AND b.user_id=r.user_id AND b.participant_key=r.participant_key AND
 (b.moves<r.moves OR (b.moves=r.moves AND b.elapsed_ms<r.elapsed_ms) OR (b.moves=r.moves AND b.elapsed_ms=r.elapsed_ms AND b.id<r.id)))
 ORDER BY r.moves ASC,r.elapsed_ms ASC,r.id ASC LIMIT 50`, day, size, true, true).Scan(&ranks).Error
	return ranks, err
}
