/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/

package model

import (
	cryptorand "crypto/rand"
	"encoding/base32"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"gorm.io/gorm"
)

const (
	DiscountCodeStatusEnabled  = 1
	DiscountCodeStatusDisabled = 2
)

var discountCodePattern = regexp.MustCompile(`^[A-Z0-9][A-Z0-9_-]{2,63}$`)

var (
	ErrDiscountCodeNotFound  = errors.New("discount code was not found")
	ErrDiscountCodeInactive  = errors.New("discount code is inactive")
	ErrDiscountCodeExpired   = errors.New("discount code has expired")
	ErrDiscountCodeMinimum   = errors.New("discount code minimum amount was not met")
	ErrDiscountCodeExhausted = errors.New("discount code usage limit was reached")
)

// DiscountCode is an administrator-managed percentage discount.  The code is
// intentionally separate from Redemption: redemption codes grant quota while
// discount codes only reduce the settlement amount of a purchase.
type DiscountCode struct {
	Id   int    `json:"id"`
	Code string `json:"code" gorm:"type:varchar(64);uniqueIndex"`
	Name string `json:"name" gorm:"type:varchar(120);index"`
	// OwnerUserID is non-zero only for a private assistant-issued code. It is
	// intentionally hidden from all API responses; the checkout validator uses
	// it to keep a weekly reward from becoming a transferable public coupon.
	OwnerUserID     int   `json:"-" gorm:"index"`
	DiscountPercent int   `json:"discount_percent"`
	MinAmount       int64 `json:"min_amount" gorm:"not null;default:0"`
	Status          int   `json:"status" gorm:"not null;default:1;index"`
	UsedCount       int64 `json:"used_count" gorm:"not null;default:0"`
	// MaxUses is zero for an unlimited administrator code. Private assistant
	// rewards set it to one so a weekly reward cannot be reused for multiple
	// purchases after it has been claimed.
	MaxUses     int64          `json:"max_uses" gorm:"not null;default:0"`
	CreatedBy   int            `json:"created_by" gorm:"index"`
	CreatedTime int64          `json:"created_time" gorm:"not null;index"`
	UpdatedTime int64          `json:"updated_time" gorm:"not null"`
	StartsTime  int64          `json:"starts_time" gorm:"not null;default:0"`
	ExpiredTime int64          `json:"expired_time" gorm:"not null;default:0"`
	DeletedAt   gorm.DeletedAt `json:"-" gorm:"index"`
}

func NormalizeDiscountCode(value string) string {
	return strings.ToUpper(strings.TrimSpace(value))
}

// pi-lens-ignore: go-bare-error
func ValidateDiscountCodeDefinition(code string, percent int, minAmount, startsTime, expiredTime int64) error {
	if !discountCodePattern.MatchString(NormalizeDiscountCode(code)) {
		return errors.New("discount code must be 3-64 characters using A-Z, 0-9, _ or -")
	}
	return ValidateDiscountCodeTerms(percent, minAmount, startsTime, expiredTime)
}

func ValidateDiscountCodeTerms(percent int, minAmount, startsTime, expiredTime int64) error {
	if percent <= 0 || percent >= 100 {
		return errors.New("discount percent must be between 1 and 99")
	}
	if minAmount < 0 {
		return errors.New("minimum amount cannot be negative")
	}
	if startsTime < 0 || expiredTime < 0 || (startsTime > 0 && expiredTime > 0 && expiredTime <= startsTime) {
		return errors.New("invalid discount code validity window")
	}
	return nil
}

func GenerateDiscountCode() (string, error) {
	bytes := make([]byte, 10)
	bytesRead, err := cryptorand.Read(bytes)
	if err != nil {
		return "", fmt.Errorf("generate discount code randomness: %w", err)
	}
	if bytesRead != len(bytes) {
		return "", errors.New("generate discount code randomness: incomplete read")
	}
	code := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(bytes)
	return "LMM-" + code, nil
}

func ValidateDiscountCodeMaxUses(maxUses int64) error {
	if maxUses < 0 {
		return errors.New("maximum discount code uses cannot be negative")
	}
	return nil
}

func GetAllDiscountCodes(startIdx, num int) (codes []*DiscountCode, total int64, err error) {
	query := DB.Model(&DiscountCode{})
	if err = query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	err = query.Order("id desc").Limit(num).Offset(startIdx).Find(&codes).Error
	return codes, total, err
}

func SearchDiscountCodes(keyword, status string, startIdx, num int) (codes []*DiscountCode, total int64, err error) {
	query := DB.Model(&DiscountCode{})
	if keyword = NormalizeDiscountCode(keyword); keyword != "" {
		query = query.Where("code LIKE ? OR name LIKE ?", keyword+"%", "%"+strings.TrimSpace(keyword)+"%")
	}
	if status != "" {
		query = query.Where("status = ?", status)
	}
	if err = query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	err = query.Order("id desc").Limit(num).Offset(startIdx).Find(&codes).Error
	return codes, total, err
}

func GetDiscountCodeById(id int) (*DiscountCode, error) {
	if id <= 0 {
		return nil, errors.New("invalid discount code id")
	}
	var code DiscountCode
	if err := DB.First(&code, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &code, nil
}

func GetDiscountCodeByValue(value string) (*DiscountCode, error) {
	code := NormalizeDiscountCode(value)
	if code == "" {
		return nil, ErrDiscountCodeNotFound
	}
	var row DiscountCode
	if err := DB.Where("code = ?", code).First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrDiscountCodeNotFound
		}
		return nil, err
	}
	return &row, nil
}

// ValidateDiscountCode checks only the current purchase eligibility.  It does
// not reserve usage; usage is counted atomically when the payment settles.
// pi-lens-ignore: go-bare-error
func ValidateDiscountCode(value string, amount int64, now int64) (*DiscountCode, error) {
	return ValidateDiscountCodeForUser(value, amount, now, 0)
}

// ValidateDiscountCodeForUser applies the same public discount rules and, for
// private assistant-issued codes, additionally requires the authenticated
// owner. userID=0 is reserved for administrator/public validation and cannot
// use a private code.
func ValidateDiscountCodeForUser(value string, amount int64, now int64, userID int) (*DiscountCode, error) {
	row, err := GetDiscountCodeByValue(value)
	if err != nil {
		return nil, err
	}
	if row.Status != DiscountCodeStatusEnabled {
		return nil, ErrDiscountCodeInactive
	}
	if row.StartsTime > 0 && row.StartsTime > now {
		return nil, ErrDiscountCodeInactive
	}
	if row.ExpiredTime > 0 && row.ExpiredTime < now {
		return nil, ErrDiscountCodeExpired
	}
	if amount < row.MinAmount {
		return nil, ErrDiscountCodeMinimum
	}
	if row.OwnerUserID != 0 && row.OwnerUserID != userID {
		return nil, ErrDiscountCodeNotFound
	}
	if row.MaxUses > 0 && row.UsedCount >= row.MaxUses {
		return nil, ErrDiscountCodeExhausted
	}
	return row, nil
}

func (code *DiscountCode) Insert() error {
	code.Code = NormalizeDiscountCode(code.Code)
	code.CreatedTime = common.GetTimestamp()
	code.UpdatedTime = code.CreatedTime
	return DB.Create(code).Error
}

func CreateDiscountCodes(template DiscountCode, count int) ([]DiscountCode, error) {
	if count <= 0 {
		return nil, errors.New("discount code count must be positive")
	}

	tx := DB.Begin()
	if tx.Error != nil {
		return nil, tx.Error
	}
	created := make([]DiscountCode, 0, count)
	now := common.GetTimestamp()

	for index := 0; index < count; index++ {
		var item DiscountCode
		var lastErr error
		for attempt := 0; attempt < 5; attempt++ {
			code, err := GenerateDiscountCode()
			if err != nil {
				tx.Rollback()
				return nil, err
			}
			candidate := template
			candidate.Id = 0
			candidate.Code = code
			candidate.UsedCount = 0
			candidate.CreatedTime = now
			candidate.UpdatedTime = now
			// pi-lens-ignore: ast-grep:gorm-n-plus-one
			if err := tx.Create(&candidate).Error; err != nil {
				lastErr = err
				if !strings.Contains(strings.ToLower(err.Error()), "unique") {
					tx.Rollback()
					return nil, err
				}
				continue
			}
			item = candidate
			lastErr = nil
			break
		}
		if lastErr != nil {
			tx.Rollback()
			return nil, lastErr
		}
		created = append(created, item)
	}

	if err := tx.Commit().Error; err != nil {
		return nil, err
	}
	return created, nil
}

func (code *DiscountCode) Update() error {
	code.Code = NormalizeDiscountCode(code.Code)
	code.UpdatedTime = common.GetTimestamp()
	return DB.Model(&DiscountCode{}).Where("id = ?", code.Id).Updates(map[string]interface{}{
		"code":             code.Code,
		"name":             code.Name,
		"discount_percent": code.DiscountPercent,
		"min_amount":       code.MinAmount,
		"status":           code.Status,
		"max_uses":         code.MaxUses,
		"starts_time":      code.StartsTime,
		"expired_time":     code.ExpiredTime,
		"updated_time":     code.UpdatedTime,
	}).Error
}

func DeleteDiscountCodeById(id int) error {
	if id <= 0 {
		return errors.New("invalid discount code id")
	}
	return DB.Delete(&DiscountCode{}, id).Error
}

// DeleteExhaustedDiscountCodes removes only finite-use codes whose usage limit
// has been reached. Partially used and unlimited codes remain available.
func DeleteExhaustedDiscountCodes() (int64, error) {
	result := DB.Where("max_uses > 0 AND used_count >= max_uses").Delete(&DiscountCode{})
	return result.RowsAffected, result.Error
}
