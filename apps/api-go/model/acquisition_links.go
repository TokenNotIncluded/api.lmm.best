package model

import (
	"context"
	"encoding/hex"
	"strings"
	"time"
	"unicode/utf8"

	"gorm.io/gorm"
)

type AcquisitionLinkFilter struct {
	Page     int
	PageSize int
	Status   string
	Search   string
}

func ListAcquisitionLinks(ctx context.Context, filter AcquisitionLinkFilter) ([]AcquisitionLink, int64, error) {
	items := []AcquisitionLink{}
	if DB == nil {
		return items, 0, gorm.ErrInvalidDB
	}
	if filter.Page < 1 || filter.PageSize < 1 || filter.PageSize > 100 || utf8.RuneCountInString(filter.Search) > 80 {
		return items, 0, ErrAcquisitionInvalid
	}
	db := DB.WithContext(ctx).Model(&AcquisitionLink{})
	switch filter.Status {
	case "all":
		db = db.Where("deleted_at = 0")
	case "active":
		db = db.Where("deleted_at = 0 AND archived = ?", false)
	case "archived":
		db = db.Where("deleted_at = 0 AND archived = ?", true)
	case "deleted":
		db = db.Where("deleted_at > 0")
	default:
		return items, 0, ErrAcquisitionInvalid
	}
	if search := strings.TrimSpace(filter.Search); search != "" {
		// Escape LIKE operators so a search for a literal campaign name stays literal.
		search = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(strings.ToLower(search))
		pattern := "%" + search + "%"
		db = db.Where(`(LOWER(name) LIKE ? ESCAPE '\' OR LOWER(source) LIKE ? ESCAPE '\' OR LOWER(campaign) LIKE ? ESCAPE '\')`, pattern, pattern, pattern)
	}
	var total int64
	if err := db.Count(&total).Error; err != nil {
		return items, 0, err
	}
	err := db.Order("created_at DESC, id DESC").Offset((filter.Page - 1) * filter.PageSize).Limit(filter.PageSize).Find(&items).Error
	return items, total, err
}

// DeleteAcquisitionLink removes a link from normal management and future link
// recognition while retaining the row for historical spend and attribution.
func DeleteAcquisitionLink(ctx context.Context, id string) (AcquisitionLink, error) {
	var link AcquisitionLink
	if len(id) != 32 {
		return link, ErrAcquisitionInvalid
	}
	if _, err := hex.DecodeString(id); err != nil {
		return link, ErrAcquisitionInvalid
	}
	if DB == nil {
		return link, gorm.ErrInvalidDB
	}
	db := DB.WithContext(ctx)
	if err := db.Where("deleted_at = 0").First(&link, "id = ?", id).Error; err != nil {
		return link, err
	}
	deletedAt := time.Now().Unix()
	result := db.Model(&AcquisitionLink{}).Where("id = ? AND deleted_at = 0", id).Update("deleted_at", deletedAt)
	if result.Error != nil {
		return link, result.Error
	}
	if result.RowsAffected == 0 {
		return link, gorm.ErrRecordNotFound
	}
	link.DeletedAt = deletedAt
	return link, nil
}
