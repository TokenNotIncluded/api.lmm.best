package model

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"gorm.io/gorm"
)

type AcquisitionUserRow struct {
	UserID         int    `json:"user_id"`
	RegisteredAt   int64  `json:"registered_at"`
	Source         string `json:"source"`
	Evidence       string `json:"evidence"`
	FirstSuccessAt int64  `json:"first_success_at"`
}
type AcquisitionUserPage struct {
	Items []AcquisitionUserRow `json:"items"`
	Total int64                `json:"total"`
	Page  int                  `json:"page"`
}

func ListAcquisitionUsers(ctx context.Context, source string, from, to int64, page int) (AcquisitionUserPage, error) {
	if DB == nil {
		return AcquisitionUserPage{}, gorm.ErrInvalidDB
	}
	result := AcquisitionUserPage{Items: []AcquisitionUserRow{}, Page: page}
	if len(source) > 253 || from <= 0 || to <= from || to-from > 366*86400 || page < 1 || page > 100000 {
		return result, ErrAcquisitionInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	db := DB.WithContext(ctx)
	config, err := acquisitionConfig(db)
	if err != nil {
		return result, err
	}
	sourceExpr := fmt.Sprintf("CASE WHEN a.registration_source IS NOT NULL THEN a.registration_source WHEN u.created_at < %d THEN 'historical_unrecorded' ELSE 'unknown' END", config.StartedAt)
	query := db.Table("users AS u").Joins("LEFT JOIN acquisition_accounts AS a ON a.user_id = u.id AND a.created_at >= ?", time.Now().Unix()-AcquisitionAccountDays*86400).Where("u.role < ? AND u.created_at >= ? AND u.created_at < ?", common.RoleAdminUser, from, to)
	if source != "" {
		query = query.Where(sourceExpr+" = ?", source)
	}
	if err := query.Count(&result.Total).Error; err != nil {
		return result, err
	}
	first := db.Model(&AcquisitionActivity{}).Select("user_id, MIN(first_at) AS first_at").Group("user_id")
	err = query.Joins("LEFT JOIN (?) AS first_success ON first_success.user_id = u.id", first).Select("u.id AS user_id, u.created_at AS registered_at, " + sourceExpr + " AS source, COALESCE(a.registration_evidence,'unavailable') AS evidence, COALESCE(first_success.first_at,0) AS first_success_at").Order("u.created_at DESC, u.id DESC").Offset((page - 1) * 50).Limit(50).Scan(&result.Items).Error
	return result, err
}

type AcquisitionUserDetail struct {
	SelfReported   *AcquisitionSelfReport `json:"self_reported"`
	UserID         int                    `json:"user_id"`
	RegisteredAt   int64                  `json:"registered_at"`
	Attribution    *AcquisitionAccount    `json:"attribution"`
	FirstSuccessAt int64                  `json:"first_success_at"`
	Recent         []AcquisitionVisit     `json:"recent"`
	RecentLimit    int                    `json:"recent_limit"`
	Historical     bool                   `json:"historical"`
}

func GetAcquisitionUserDetail(ctx context.Context, id int) (AcquisitionUserDetail, error) {
	if DB == nil {
		return AcquisitionUserDetail{}, gorm.ErrInvalidDB
	}
	result := AcquisitionUserDetail{UserID: id, Recent: []AcquisitionVisit{}, RecentLimit: 20}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	db := DB.WithContext(ctx)
	var user User
	if err := db.Select("id", "created_at").First(&user, id).Error; err != nil {
		return result, err
	}
	var err error
	result.SelfReported, err = ReadAcquisitionSelfReport(ctx, id)
	if err != nil {
		return result, err
	}
	result.RegisteredAt = user.CreatedAt
	config, err := acquisitionConfig(db)
	if err != nil {
		return result, err
	}
	result.Historical = user.CreatedAt < config.StartedAt
	var account AcquisitionAccount
	err = db.Where("user_id = ? AND created_at >= ?", id, time.Now().Unix()-AcquisitionAccountDays*86400).First(&account).Error
	if err == nil {
		result.Attribution = &account
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return result, err
	}
	if err := db.Model(&AcquisitionActivity{}).Select("COALESCE(MIN(first_at),0)").Where("user_id = ?", id).Scan(&result.FirstSuccessAt).Error; err != nil {
		return result, err
	}
	err = db.Table("acquisition_visits AS v").Joins("JOIN acquisition_visitors AS owner ON owner.id = v.visitor_id").Where("owner.user_id = ? AND v.created_at >= ?", id, time.Now().Unix()-AcquisitionRawDays*86400).Select("v.id,v.link_id,v.source,v.medium,v.campaign,v.content,v.referrer_host,v.landing,v.evidence,v.created_at").Order("v.created_at DESC, v.id DESC").Limit(20).Scan(&result.Recent).Error
	return result, err
}
