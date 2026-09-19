package model

import (
	"context"
	"fmt"
	"github.com/LIghtJUNction/api.lmm.best/common"
	"gorm.io/gorm"
	"time"
)

const AcquisitionExportLimit = 10000

type AcquisitionExportRow struct {
	UserID             int    `json:"user_id"`
	RegisteredAt       int64  `json:"registered_at"`
	ObservedSource     string `json:"observed_source"`
	Evidence           string `json:"evidence"`
	CorrectedSource    string `json:"corrected_source"`
	CorrectionRevision int64  `json:"correction_revision"`
	FirstSuccessAt     int64  `json:"first_success_at"`
}

func ExportAcquisitionUsers(ctx context.Context, source string, from, to int64) ([]AcquisitionExportRow, error) {
	rows := []AcquisitionExportRow{}
	if len(source) > 253 || from <= 0 || to <= from || to-from > 366*86400 {
		return rows, ErrAcquisitionInvalid
	}
	if DB == nil {
		return rows, gorm.ErrInvalidDB
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	db := DB.WithContext(ctx)
	now := time.Now().Unix()
	config, err := acquisitionConfig(db)
	if err != nil {
		return rows, err
	}
	sourceExpr := fmt.Sprintf("CASE WHEN a.registration_source IS NOT NULL THEN a.registration_source WHEN u.created_at < %d THEN 'historical_unrecorded' ELSE 'unknown' END", config.StartedAt)
	query := db.Table("users AS u").Joins("LEFT JOIN acquisition_accounts AS a ON a.user_id=u.id AND a.created_at >= ?", now-AcquisitionAccountDays*86400).Where("u.role < ? AND u.deleted_at IS NULL AND u.created_at >= ? AND u.created_at < ?", common.RoleAdminUser, from, to)
	if source != "" {
		query = query.Where(sourceExpr+" = ?", source)
	}
	first := db.Model(&AcquisitionActivity{}).Select("user_id, MIN(first_at) AS first_at").Group("user_id")
	err = query.Joins("LEFT JOIN (?) AS first_success ON first_success.user_id=u.id", first).Joins("LEFT JOIN acquisition_correction_heads AS correction ON correction.user_id=u.id AND correction.updated_at >= ?", now-AcquisitionAccountDays*86400).Select("u.id AS user_id,u.created_at AS registered_at," + sourceExpr + " AS observed_source,COALESCE(a.registration_evidence,'unavailable') AS evidence,COALESCE(correction.source,'') AS corrected_source,COALESCE(correction.revision,0) AS correction_revision,COALESCE(first_success.first_at,0) AS first_success_at").Order("u.created_at DESC,u.id DESC").Limit(AcquisitionExportLimit + 1).Scan(&rows).Error
	if len(rows) > AcquisitionExportLimit {
		return nil, ErrAcquisitionInvalid
	}
	return rows, err
}
