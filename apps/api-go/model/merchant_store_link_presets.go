package model

import (
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const MerchantStoreProductLinkPresetsOption = "MerchantStoreProductLinkPresets"
const storeLinkPresetsMaxBytes = 64 << 10

var storeLinkPresetID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// Presets are public copy sources. Products retain independent MerchantStoreLink
// values and never refer to the preset ID after the seller adds a link.
type MerchantStoreLinkPreset struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	URL         string `json:"url"`
	Description string `json:"description"`
}

func validateStoreLinkPresets(presets []MerchantStoreLinkPreset) error {
	if presets == nil || len(presets) > 50 {
		return ErrMerchantStoreInput
	}
	seen := make(map[string]bool, len(presets))
	for _, preset := range presets {
		if !storeLinkPresetID.MatchString(preset.ID) || seen[preset.ID] ||
			strings.TrimSpace(preset.Title) == "" || len(preset.Title) > 200 ||
			strings.ContainsFunc(preset.Title, unicode.IsControl) ||
			len(preset.Description) > 4096 || !storeURL(preset.URL) ||
			!utf8.ValidString(preset.Title) || !utf8.ValidString(preset.URL) || !utf8.ValidString(preset.Description) {
			return ErrMerchantStoreInput
		}
		seen[preset.ID] = true
	}
	return nil
}

func parseStoreLinkPresets(value string) ([]MerchantStoreLinkPreset, error) {
	if len(value) > storeLinkPresetsMaxBytes || !utf8.ValidString(value) {
		return nil, ErrMerchantStoreInput
	}
	var presets []MerchantStoreLinkPreset
	decoder := json.NewDecoder(strings.NewReader(value))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&presets); err != nil {
		return nil, ErrMerchantStoreInput
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, ErrMerchantStoreInput
	}
	if err := validateStoreLinkPresets(presets); err != nil {
		return nil, err
	}
	return presets, nil
}

func GetMerchantStoreLinkPresets() ([]MerchantStoreLinkPreset, error) {
	var option Option
	err := DB.Where("key = ?", MerchantStoreProductLinkPresetsOption).First(&option).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return []MerchantStoreLinkPreset{}, nil
	}
	if err != nil {
		return nil, err
	}
	return parseStoreLinkPresets(option.Value)
}

func SaveMerchantStoreLinkPresets(actor int, presets []MerchantStoreLinkPreset) error {
	if err := validateStoreLinkPresets(presets); err != nil {
		return err
	}
	value, err := json.Marshal(presets)
	if err != nil || len(value) > storeLinkPresetsMaxBytes {
		return ErrMerchantStoreInput
	}
	return marketTransaction(DB, func(tx *gorm.DB) error {
		if err := marketLockUsers(tx, actor); err != nil {
			return err
		}
		if _, err := storeUser(tx, actor, common.RoleRootUser); err != nil {
			return err
		}
		if err := storeRequireWriter(tx); err != nil {
			return err
		}
		return tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "key"}}, DoUpdates: clause.AssignmentColumns([]string{"value"})}).Create(&Option{Key: MerchantStoreProductLinkPresetsOption, Value: string(value)}).Error
	})
}
