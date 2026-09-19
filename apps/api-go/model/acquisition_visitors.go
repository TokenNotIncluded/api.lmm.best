package model

import (
	"context"
	"github.com/LIghtJUNction/api.lmm.best/common"
	"gorm.io/gorm"
	"time"
)

type AcquisitionVisitorCount struct {
	Source   string `json:"source"`
	Visitors int64  `json:"visitors"`
}
type AcquisitionVisitorSummary struct {
	From             int64                     `json:"from"`
	To               int64                     `json:"to"`
	AvailableFrom    int64                     `json:"available_from"`
	CoverageComplete bool                      `json:"coverage_complete"`
	ObservedVisitors int64                     `json:"observed_visitors"`
	Channels         []AcquisitionVisitorCount `json:"channels"`
}

// Visitors are consenting browser identifiers, not natural people. A browser
// seen in two channels contributes to both rows but once to the overall count.
func GetAcquisitionVisitorSummary(ctx context.Context, from, to int64) (AcquisitionVisitorSummary, error) {
	result := AcquisitionVisitorSummary{From: from, To: to, Channels: []AcquisitionVisitorCount{}}
	now := time.Now().Unix()
	if from <= 0 || to <= from || to-from > 366*86400 || to > now+60 {
		return result, ErrAcquisitionInvalid
	}
	if DB == nil {
		return result, gorm.ErrInvalidDB
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	db := DB.WithContext(ctx)
	config, err := acquisitionConfig(db)
	if err != nil {
		return result, err
	}
	result.AvailableFrom = now - AcquisitionRawDays*86400
	if config.StartedAt > result.AvailableFrom {
		result.AvailableFrom = config.StartedAt
	}
	result.CoverageComplete = from >= result.AvailableFrom
	actualFrom := from
	if actualFrom < result.AvailableFrom {
		actualFrom = result.AvailableFrom
	}
	query := func() *gorm.DB {
		return db.Table("acquisition_visits AS v").Joins("JOIN acquisition_visitors AS visitor ON visitor.id = v.visitor_id").Joins("LEFT JOIN users AS u ON u.id = visitor.user_id").Where("v.created_at >= ? AND v.created_at < ? AND (visitor.user_id = 0 OR (u.id IS NOT NULL AND u.role < ? AND u.deleted_at IS NULL))", actualFrom, to, common.RoleAdminUser)
	}
	if err = query().Distinct("v.visitor_id").Count(&result.ObservedVisitors).Error; err != nil {
		return result, err
	}
	err = query().Select("v.source AS source, COUNT(DISTINCT v.visitor_id) AS visitors").Group("v.source").Order("visitors DESC, v.source ASC").Scan(&result.Channels).Error
	return result, err
}
