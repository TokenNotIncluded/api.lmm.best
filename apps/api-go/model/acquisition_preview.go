package model

import (
	"context"
	"encoding/hex"
	"errors"
	"strings"

	"gorm.io/gorm"
)

// Shared by real observations and read-only previews; no writes or tracking.
func applyAcquisitionLink(db *gorm.DB, id string, visit *AcquisitionVisit) error {
	if len(id) != 32 {
		return nil
	}
	var link AcquisitionLink
	err := db.Where("deleted_at = 0").First(&link, "id = ?", id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	visit.LinkID, visit.Source, visit.Medium = link.ID, link.Source, link.Medium
	visit.Campaign, visit.Content, visit.Evidence = link.Campaign, link.Content, "promotion_link"
	return nil
}

type AcquisitionLinkPreview struct {
	LinkID       string `json:"link_id"`
	Target       string `json:"target"`
	Source       string `json:"source"`
	Medium       string `json:"medium"`
	Campaign     string `json:"campaign"`
	Content      string `json:"content"`
	Evidence     string `json:"evidence"`
	ReferrerHost string `json:"referrer_host"`
	Excluded     bool   `json:"excluded"`
	Archived     bool   `json:"archived"`
}

func PreviewAcquisitionLink(ctx context.Context, id string) (AcquisitionLinkPreview, error) {
	result := AcquisitionLinkPreview{Excluded: true}
	if len(id) != 32 {
		return result, ErrAcquisitionInvalid
	}
	if _, err := hex.DecodeString(id); err != nil {
		return result, ErrAcquisitionInvalid
	}
	if DB == nil {
		return result, gorm.ErrInvalidDB
	}
	db := DB.WithContext(ctx)
	var link AcquisitionLink
	if err := db.Where("deleted_at = 0").First(&link, "id = ?", id).Error; err != nil {
		return result, err
	}
	if AcquisitionTarget(link.Target) == "" {
		return result, ErrAcquisitionInvalid
	}
	// This normalizes a synthetic input only. It never grants consent, creates
	// visitor cookies, or calls ObserveAcquisition / acquisitionConfig.
	visit, err := NormalizeAcquisition(AcquisitionInput{Consent: true, ConsentVersion: 2, Nonce: strings.Repeat("0", 32), Landing: link.Target, Source: link.Source, Medium: link.Medium, Campaign: link.Campaign, Content: link.Content}, nil, 0)
	if err != nil {
		return result, err
	}
	if err = applyAcquisitionLink(db, id, &visit); err != nil {
		return result, err
	}
	result.LinkID, result.Target, result.Source = visit.LinkID, visit.Landing, visit.Source
	result.Medium, result.Campaign, result.Content = visit.Medium, visit.Campaign, visit.Content
	result.Evidence, result.ReferrerHost, result.Archived = visit.Evidence, visit.ReferrerHost, link.Archived
	return result, nil
}
