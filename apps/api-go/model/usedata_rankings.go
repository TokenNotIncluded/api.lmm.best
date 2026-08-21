package model

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"gorm.io/gorm"
)

type RankingQuotaTotal struct {
	ModelName   string `json:"model_name"`
	TotalTokens int64  `json:"total_tokens"`
}

type RankingQuotaBucket struct {
	ModelName string `json:"model_name"`
	Bucket    int64  `json:"bucket"`
	Tokens    int64  `json:"tokens"`
}

type UserRankingTotal struct {
	UserID      int   `json:"user_id"`
	Requests    int64 `json:"requests"`
	TotalTokens int64 `json:"total_tokens"`
}

// compatibilityUserRankingLimit bounds the legacy slice-returning helper.
// Public leaderboard reads use IterateUserRankingRows and retain a top-N
// window themselves; callers of this older compatibility API should not be
// able to materialize every grouped user when the table reaches millions of
// participants.
const compatibilityUserRankingLimit = 100

// UserRankingRow is a single database-aggregated usage row enriched with the
// small public user projection needed by the leaderboard. It is consumed as a
// cursor so callers never materialize every active user in a long window.
type UserRankingRow struct {
	UserRankingTotal
	Username    string
	DisplayName string
	Status      int
	Setting     string
}

func GetRankingQuotaTotals(startTime int64, endTime int64) ([]RankingQuotaTotal, error) {
	var rows []RankingQuotaTotal
	query := DB.Table("quota_data").
		Select("model_name, sum(token_used) as total_tokens").
		Where("model_name <> ''").
		Group("model_name").
		Having("sum(token_used) > 0").
		Order("total_tokens DESC")
	query = applyRankingQuotaTimeRange(query, startTime, endTime)
	err := query.Find(&rows).Error
	return rows, err
}

func GetRankingQuotaBuckets(startTime int64, endTime int64, bucketSize int64) ([]RankingQuotaBucket, error) {
	if bucketSize <= 0 {
		bucketSize = 3600
	}
	bucketExpr := rankingBucketExpr(bucketSize)
	var rows []RankingQuotaBucket
	query := DB.Table("quota_data").
		Select(fmt.Sprintf("model_name, %s as bucket, sum(token_used) as tokens", bucketExpr)).
		Where("model_name <> ''").
		Group(fmt.Sprintf("model_name, %s", bucketExpr)).
		Having("sum(token_used) > 0").
		Order("bucket ASC")
	query = applyRankingQuotaTimeRange(query, startTime, endTime)
	err := query.Find(&rows).Error
	return rows, err
}

// IterateRankingQuotaBuckets keeps the database aggregation semantics of
// GetRankingQuotaBuckets while avoiding a Go slice containing every
// model/time-bucket pair. Public ranking history only needs a small projection
// of those rows, so callers can consume the ordered result incrementally.
func IterateRankingQuotaBuckets(startTime int64, endTime int64, bucketSize int64, visit func(RankingQuotaBucket) error) error {
	if DB == nil || visit == nil {
		return fmt.Errorf("iterate ranking quota buckets: %w", gorm.ErrInvalidData)
	}
	if bucketSize <= 0 {
		bucketSize = 3600
	}
	bucketExpr := rankingBucketExpr(bucketSize)
	query := DB.Table("quota_data").
		Select(fmt.Sprintf("model_name, %s as bucket, sum(token_used) as tokens", bucketExpr)).
		Where("model_name <> ''").
		Group(fmt.Sprintf("model_name, %s", bucketExpr)).
		Having("sum(token_used) > 0").
		Order("bucket ASC")
	query = applyRankingQuotaTimeRange(query, startTime, endTime)
	rows, err := query.Rows()
	if err != nil {
		return fmt.Errorf("query ranking quota buckets: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var row RankingQuotaBucket
		if err := rows.Scan(&row.ModelName, &row.Bucket, &row.Tokens); err != nil {
			return fmt.Errorf("scan ranking quota bucket: %w", err)
		}
		if err := visit(row); err != nil {
			return fmt.Errorf("visit ranking quota bucket: %w", err)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate ranking quota buckets: %w", err)
	}
	return nil
}

func GetUserRankingTotals(startTime int64, endTime int64) ([]UserRankingTotal, error) {
	var rows []UserRankingTotal
	query := DB.Table("quota_data").
		Select("user_id, COALESCE(SUM(count), 0) AS requests, COALESCE(SUM(token_used), 0) AS total_tokens").
		Where("user_id > 0").
		Group("user_id").
		Having("COALESCE(SUM(count), 0) > 0 OR COALESCE(SUM(token_used), 0) > 0").
		Order("total_tokens DESC, requests DESC").
		Limit(compatibilityUserRankingLimit)
	query = applyRankingQuotaTimeRange(query, startTime, endTime)
	return rows, query.Find(&rows).Error
}

// IterateUserRankingRows streams grouped usage rows directly from the
// database. The slice-returning helper above remains for compatibility, while
// the public leaderboard uses this bounded path for large installations.
func IterateUserRankingRows(ctx context.Context, startTime int64, endTime int64, visit func(UserRankingRow) error) error {
	if DB == nil || visit == nil {
		return fmt.Errorf("iterate user ranking rows: %w", gorm.ErrInvalidData)
	}
	if ctx == nil {
		return fmt.Errorf("iterate user ranking rows: nil context")
	}
	query := DB.WithContext(ctx).Table("quota_data").
		Select("quota_data.user_id, COALESCE(SUM(quota_data.count), 0) AS requests, COALESCE(SUM(quota_data.token_used), 0) AS total_tokens, users.username, users.display_name, users.status, users.setting").
		Joins("JOIN users ON users.id = quota_data.user_id AND users.deleted_at IS NULL").
		Where("quota_data.user_id > ?", 0).
		Group("quota_data.user_id, users.username, users.display_name, users.status, users.setting").
		Having("COALESCE(SUM(quota_data.count), 0) > 0 OR COALESCE(SUM(quota_data.token_used), 0) > 0").
		Order("total_tokens DESC, requests DESC, quota_data.user_id ASC")
	query = applyRankingQuotaTimeRange(query, startTime, endTime)
	rows, err := query.Rows()
	if err != nil {
		return fmt.Errorf("query user ranking rows: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var row UserRankingRow
		var username, displayName, setting sql.NullString
		if err := rows.Scan(
			&row.UserID,
			&row.Requests,
			&row.TotalTokens,
			&username,
			&displayName,
			&row.Status,
			&setting,
		); err != nil {
			return fmt.Errorf("scan user ranking row: %w", err)
		}
		row.Username = username.String
		row.DisplayName = displayName.String
		row.Setting = setting.String
		if err := visit(row); err != nil {
			return fmt.Errorf("visit user ranking row: %w", err)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate user ranking rows: %w", err)
	}
	return nil
}

func GetUsersForUsageRanking(userIDs []int) ([]*User, error) {
	if len(userIDs) == 0 {
		return []*User{}, nil
	}

	var users []*User
	err := DB.Select("id, username, display_name, status, setting").
		Where("id IN ?", userIDs).
		Find(&users).Error
	return users, err
}

func rankingBucketExpr(bucketSize int64) string {
	if common.UsingMainDatabase(common.DatabaseTypeMySQL) {
		return fmt.Sprintf("FLOOR(created_at / %d) * %d", bucketSize, bucketSize)
	}
	return fmt.Sprintf("(created_at / %d) * %d", bucketSize, bucketSize)
}

func applyRankingQuotaTimeRange(query *gorm.DB, startTime int64, endTime int64) *gorm.DB {
	if startTime > 0 {
		query = query.Where("quota_data.created_at >= ?", startTime)
	}
	if endTime > 0 {
		query = query.Where("quota_data.created_at <= ?", endTime)
	}
	return query
}
