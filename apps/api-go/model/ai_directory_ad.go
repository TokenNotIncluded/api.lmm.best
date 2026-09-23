/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
package model

import (
	"errors"
	"fmt"
	"math"
	"net"
	"net/url"
	"regexp"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

const (
	AIDirectoryAdMinBidCents  int64 = 100
	AIDirectoryAdMaxBidCents  int64 = 1_000_000
	AIDirectoryAdDurationDays       = 30
	AIDirectoryAdStatusActive       = "active"
	AIDirectoryAdStatusHidden       = "hidden"
)

var adRequestIDPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]{16,80}$`)

var (
	ErrAIDirectoryAdInvalidInput = errors.New("invalid advertisement details")
	ErrAIDirectoryAdInvalidBid   = errors.New("advertisement bid must be between $1 and $10,000")
	ErrAIDirectoryAdInsufficient = errors.New("insufficient wallet balance")
	ErrAIDirectoryAdConflict     = errors.New("advertisement request ID was already used for different details")
	ErrAIDirectoryAdQuoteChanged = errors.New("advertisement price changed; review a fresh quote")
	ErrAIDirectoryAdNotFound     = errors.New("advertisement not found")
)

type AIDirectoryAd struct {
	ID           int    `json:"id" gorm:"primaryKey"`
	OwnerUserID  int    `json:"-" gorm:"not null;index"`
	Name         string `json:"name" gorm:"type:varchar(80);not null"`
	URL          string `json:"url" gorm:"type:varchar(2048);not null"`
	Summary      string `json:"summary" gorm:"type:varchar(180);not null;default:''"`
	Description  string `json:"description" gorm:"type:text;not null;default:''"`
	BidCents     int64  `json:"bid_cents" gorm:"not null;index"`
	ChargedQuota int    `json:"charged_quota" gorm:"not null"`
	RequestID    string `json:"-" gorm:"type:varchar(80);not null;uniqueIndex"`
	Status       string `json:"status" gorm:"type:varchar(16);not null;index"`
	PaidAt       int64  `json:"paid_at" gorm:"not null;index"`
	ExpiresAt    int64  `json:"expires_at" gorm:"not null;index"`
	HiddenAt     int64  `json:"hidden_at" gorm:"not null;default:0"`
	RefundedAt   int64  `json:"refunded_at" gorm:"not null;default:0"`
}

func (AIDirectoryAd) TableName() string { return "ai_directory_ads" }

type AIDirectoryAdInput struct {
	Name          string `json:"name"`
	URL           string `json:"url"`
	Summary       string `json:"summary"`
	Description   string `json:"description"`
	BidCents      int64  `json:"bid_cents"`
	ExpectedQuota int    `json:"expected_quota"`
	RequestID     string `json:"request_id"`
}

func normalizeAIDirectoryAdURL(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" || len(value) > 2048 || strings.ContainsAny(value, "\r\n\t ") {
		return "", ErrAIDirectoryAdInvalidInput
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil {
		return "", ErrAIDirectoryAdInvalidInput
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "localhost" || strings.HasSuffix(host, ".local") || !strings.Contains(host, ".") {
		return "", ErrAIDirectoryAdInvalidInput
	}
	if ip := net.ParseIP(host); ip != nil && (ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified()) {
		return "", ErrAIDirectoryAdInvalidInput
	}
	return parsed.String(), nil
}

func normalizeAIDirectoryAdInput(input AIDirectoryAdInput) (AIDirectoryAdInput, error) {
	input.Name = strings.TrimSpace(input.Name)
	input.Summary = strings.TrimSpace(input.Summary)
	input.Description = strings.TrimSpace(input.Description)
	if input.Name == "" || len([]rune(input.Name)) > 80 || len([]rune(input.Summary)) > 180 || len([]rune(input.Description)) > 1200 || !adRequestIDPattern.MatchString(input.RequestID) {
		return input, ErrAIDirectoryAdInvalidInput
	}
	var err error
	input.URL, err = normalizeAIDirectoryAdURL(input.URL)
	if err != nil {
		return input, err
	}
	if input.BidCents < AIDirectoryAdMinBidCents || input.BidCents > AIDirectoryAdMaxBidCents {
		return input, ErrAIDirectoryAdInvalidBid
	}
	if input.ExpectedQuota <= 0 {
		return input, ErrAIDirectoryAdInvalidInput
	}
	return input, nil
}

func AIDirectoryAdChargeQuota(bidCents int64) (int, error) {
	if bidCents < AIDirectoryAdMinBidCents || bidCents > AIDirectoryAdMaxBidCents {
		return 0, ErrAIDirectoryAdInvalidBid
	}
	if common.QuotaPerUnit <= 0 || math.IsNaN(common.QuotaPerUnit) || math.IsInf(common.QuotaPerUnit, 0) {
		return 0, ErrWalletQuotaOutOfRange
	}
	charge := decimal.NewFromInt(bidCents).
		Mul(decimal.NewFromFloat(common.QuotaPerUnit)).
		Div(decimal.NewFromInt(100)).Ceil()
	if !charge.IsPositive() || !charge.IsInteger() || !charge.BigInt().IsInt64() || charge.GreaterThan(decimal.NewFromInt(int64(common.MaxWalletQuota))) {
		return 0, ErrWalletQuotaOutOfRange
	}
	return int(charge.IntPart()), nil
}

func sameAIDirectoryAdRequest(ad AIDirectoryAd, ownerID int, input AIDirectoryAdInput) bool {
	return ad.OwnerUserID == ownerID && ad.Name == input.Name && ad.URL == input.URL &&
		ad.Summary == input.Summary && ad.Description == input.Description && ad.BidCents == input.BidCents
}

// CreateAIDirectoryAd charges the wallet and publishes one immutable 30-day
// listing in a single transaction. RequestID makes response-loss retries safe.
func CreateAIDirectoryAd(ownerID int, input AIDirectoryAdInput, now int64) (AIDirectoryAd, bool, error) {
	var ad AIDirectoryAd
	if ownerID <= 0 || now <= 0 {
		return ad, false, ErrAIDirectoryAdInvalidInput
	}
	input, err := normalizeAIDirectoryAdInput(input)
	if err != nil {
		return ad, false, err
	}
	if err := DB.Where("request_id = ?", input.RequestID).First(&ad).Error; err == nil {
		if !sameAIDirectoryAdRequest(ad, ownerID, input) || ad.ChargedQuota != input.ExpectedQuota {
			return AIDirectoryAd{}, false, ErrAIDirectoryAdConflict
		}
		return ad, false, nil
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return AIDirectoryAd{}, false, err
	}
	charge, err := AIDirectoryAdChargeQuota(input.BidCents)
	if err != nil {
		return ad, false, err
	}
	if charge != input.ExpectedQuota {
		return ad, false, ErrAIDirectoryAdQuoteChanged
	}
	ad = AIDirectoryAd{
		OwnerUserID: ownerID, Name: input.Name, URL: input.URL, Summary: input.Summary,
		Description: input.Description, BidCents: input.BidCents, ChargedQuota: charge,
		RequestID: input.RequestID, Status: AIDirectoryAdStatusActive,
		PaidAt: now, ExpiresAt: now + int64(AIDirectoryAdDurationDays)*24*60*60,
	}
	err = DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&ad).Error; err != nil {
			return err
		}
		result := UpdateWalletQuotaByDelta(tx.Model(&User{}).Where("id = ? AND quota >= ?", ownerID, charge), -charge)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrAIDirectoryAdInsufficient
		}
		return nil
	})
	if err != nil {
		// A concurrent retry can lose the unique-index race after the first
		// transaction has committed. Resolve only the same owner's same request.
		var existing AIDirectoryAd
		if lookupErr := DB.Where("request_id = ?", input.RequestID).First(&existing).Error; lookupErr == nil {
			if sameAIDirectoryAdRequest(existing, ownerID, input) && existing.ChargedQuota == input.ExpectedQuota {
				return existing, false, nil
			}
			return AIDirectoryAd{}, false, ErrAIDirectoryAdConflict
		}
		return AIDirectoryAd{}, false, err
	}
	if cacheErr := cacheIncrUserQuota(ownerID, -int64(charge)); cacheErr != nil {
		common.SysLog(fmt.Sprintf("AI directory ad %d wallet cache refresh failed: %v", ad.ID, cacheErr))
	}
	RecordLog(ownerID, LogTypeManage, fmt.Sprintf("Paid %d quota for AI directory advertisement %d", charge, ad.ID))
	return ad, true, nil
}

func ListActiveAIDirectoryAds(now int64, offset, limit int) ([]AIDirectoryAd, bool, error) {
	if offset < 0 || offset > 100_000 || limit < 1 || limit > 50 {
		return nil, false, ErrAIDirectoryAdInvalidInput
	}
	var ads []AIDirectoryAd
	err := DB.Where("status = ? AND expires_at > ?", AIDirectoryAdStatusActive, now).
		Order("bid_cents DESC, paid_at ASC, id ASC").Offset(offset).Limit(limit + 1).Find(&ads).Error
	if err != nil {
		return nil, false, err
	}
	more := len(ads) > limit
	if more {
		ads = ads[:limit]
	}
	return ads, more, nil
}

func ListMyAIDirectoryAds(ownerID int) ([]AIDirectoryAd, error) {
	if ownerID <= 0 {
		return nil, ErrAIDirectoryAdInvalidInput
	}
	var ads []AIDirectoryAd
	err := DB.Where("owner_user_id = ?", ownerID).Order("paid_at DESC, id DESC").Limit(100).Find(&ads).Error
	return ads, err
}

// HideAIDirectoryAd withdraws a still-running placement and refunds its full
// wallet charge exactly once. A second request returns the existing result.
func HideAIDirectoryAd(adID int, now int64) (AIDirectoryAd, bool, error) {
	var ad AIDirectoryAd
	if adID <= 0 || now <= 0 {
		return ad, false, ErrAIDirectoryAdInvalidInput
	}
	refunded := false
	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := lockForUpdate(tx).Where("id = ?", adID).First(&ad).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrAIDirectoryAdNotFound
			}
			return err
		}
		if ad.Status == AIDirectoryAdStatusHidden {
			return nil
		}
		if ad.Status != AIDirectoryAdStatusActive || ad.ExpiresAt <= now {
			return ErrAIDirectoryAdNotFound
		}
		updated := tx.Model(&AIDirectoryAd{}).
			Where("id = ? AND status = ? AND expires_at > ?", ad.ID, AIDirectoryAdStatusActive, now).
			Updates(map[string]any{"status": AIDirectoryAdStatusHidden, "hidden_at": now, "refunded_at": now})
		if updated.Error != nil {
			return updated.Error
		}
		if updated.RowsAffected != 1 {
			return ErrAIDirectoryAdNotFound
		}
		credit := UpdateWalletQuotaByDelta(tx.Model(&User{}).Where("id = ?", ad.OwnerUserID), ad.ChargedQuota)
		if credit.Error != nil {
			return credit.Error
		}
		if credit.RowsAffected != 1 {
			return ErrWalletQuotaOutOfRange
		}
		ad.Status = AIDirectoryAdStatusHidden
		ad.HiddenAt = now
		ad.RefundedAt = now
		refunded = true
		return nil
	})
	if err != nil {
		return AIDirectoryAd{}, false, err
	}
	if refunded {
		if cacheErr := cacheIncrUserQuota(ad.OwnerUserID, int64(ad.ChargedQuota)); cacheErr != nil {
			common.SysLog(fmt.Sprintf("AI directory ad %d refund cache refresh failed: %v", ad.ID, cacheErr))
		}
		RecordLog(ad.OwnerUserID, LogTypeRefund, fmt.Sprintf("Refunded %d quota for hidden AI directory advertisement %d", ad.ChargedQuota, ad.ID))
	}
	return ad, refunded, nil
}
