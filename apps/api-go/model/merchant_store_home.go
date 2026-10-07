package model

import (
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const MerchantStoreAnnouncementOption = "MerchantStoreAnnouncement"
const merchantStoreHomeSetting = "merchant_store_home"

type MerchantStoreHomeInput struct {
	Biography       string `json:"biography"`
	Announcement    string `json:"announcement"`
	HeaderImage     string `json:"header_image"`
	ExpectedVersion int    `json:"expected_version"`
}

type MerchantStoreHome struct {
	Seller *MerchantStorePublicSeller `json:"seller"`
	dto.MerchantStoreHome
}

func normalizeMerchantStoreHome(in dto.MerchantStoreHome) (dto.MerchantStoreHome, error) {
	in.Biography = strings.TrimSpace(in.Biography)
	in.Announcement = strings.TrimSpace(in.Announcement)
	in.HeaderImage = strings.TrimSpace(in.HeaderImage)
	if !utf8.ValidString(in.Biography) || !utf8.ValidString(in.Announcement) || len(in.Biography) > 4*4096 || utf8.RuneCountInString(in.Biography) > 4096 || len(in.Announcement) > 4*16384 || utf8.RuneCountInString(in.Announcement) > 16384 || in.Version < 0 {
		return in, ErrMerchantStoreInput
	}
	if in.HeaderImage != "" {
		// Merchant headers require HTTPS or the same bounded SVG parser as products.
		if strings.HasPrefix(strings.ToLower(in.HeaderImage), "http:") {
			return in, ErrMerchantStoreInput
		}
		var err error
		in.HeaderImage, err = normalizeMerchantStoreImage(in.HeaderImage)
		if err != nil {
			return in, err
		}
	}
	return in, nil
}

func merchantStoreHomeFromSetting(setting string) (dto.MerchantStoreHome, error) {
	var values map[string]json.RawMessage
	if setting != "" {
		if err := json.Unmarshal([]byte(setting), &values); err != nil {
			return dto.MerchantStoreHome{}, err
		}
	}
	var home dto.MerchantStoreHome
	if raw := values[merchantStoreHomeSetting]; len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &home); err != nil {
			return home, err
		}
	}
	return normalizeMerchantStoreHome(home)
}

func GetMerchantStoreHome(actor, sellerID int) (*MerchantStoreHome, error) {
	if sellerID < 1 || int64(sellerID) > 2147483647 {
		return nil, ErrMerchantStoreInput
	}
	seller, err := storePublicSeller(DB, sellerID, "")
	if err != nil {
		return nil, err
	}
	// Only viewer-visible products contribute a public sales contact. Drafts,
	// private/test products and account email can never become contact copy.
	var product MerchantStoreProduct
	err = MerchantStoreVisibleProductsForViewer(DB, actor).Select("contact").Where("seller_id = ?", sellerID).Order("created_at DESC,id ASC").First(&product).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	seller.ContactEmail = storePublicContactEmail(product.Contact)
	var user User
	if err := DB.Select("id", "setting").First(&user, sellerID).Error; err != nil {
		return nil, err
	}
	home, err := merchantStoreHomeFromSetting(user.Setting)
	if err != nil {
		return nil, err
	}
	return &MerchantStoreHome{Seller: seller, MerchantStoreHome: home}, nil
}

func SaveMerchantStoreHome(actor int, input MerchantStoreHomeInput) (*MerchantStoreHome, error) {
	home, err := normalizeMerchantStoreHome(dto.MerchantStoreHome{Biography: input.Biography, Announcement: input.Announcement, HeaderImage: input.HeaderImage, Version: input.ExpectedVersion})
	if err != nil {
		return nil, err
	}
	err = marketTransaction(DB, func(tx *gorm.DB) error {
		if err := marketLockUsers(tx, actor); err != nil {
			return err
		}
		user, err := storeUser(tx, actor, common.RoleCommonUser)
		if err != nil {
			return err
		}
		if err := storeRequireWriter(tx); err != nil {
			return err
		}
		current, err := merchantStoreHomeFromSetting(user.Setting)
		if err != nil {
			return err
		}
		if current.Version != input.ExpectedVersion {
			return ErrMerchantStoreConflict
		}
		home.Version = current.Version + 1
		values := make(map[string]json.RawMessage)
		if user.Setting != "" {
			if err := json.Unmarshal([]byte(user.Setting), &values); err != nil {
				return err
			}
		}
		if values == nil {
			values = make(map[string]json.RawMessage)
		}
		values[merchantStoreHomeSetting], err = json.Marshal(home)
		if err != nil {
			return err
		}
		encoded, err := json.Marshal(values)
		if err != nil {
			return err
		}
		return tx.Model(&User{}).Where("id = ?", actor).Update("setting", string(encoded)).Error
	})
	if err != nil {
		return nil, err
	}
	if err := invalidateUserCache(actor); err != nil {
		return nil, err
	}
	return GetMerchantStoreHome(actor, actor)
}

func GetMerchantStoreAnnouncement() (string, error) {
	var option Option
	err := DB.Where("key = ?", MerchantStoreAnnouncementOption).First(&option).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return option.Value, nil
}

func SaveMerchantStoreAnnouncement(actor int, content string) error {
	content = strings.TrimSpace(content)
	if !utf8.ValidString(content) || len(content) > 4*16384 || utf8.RuneCountInString(content) > 16384 {
		return ErrMerchantStoreInput
	}
	return marketTransaction(DB, func(tx *gorm.DB) error {
		if err := marketLockUsers(tx, actor); err != nil {
			return err
		}
		if _, err := storeUser(tx, actor, common.RoleAdminUser); err != nil {
			return err
		}
		if err := storeRequireWriter(tx); err != nil {
			return err
		}
		return tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "key"}}, DoUpdates: clause.AssignmentColumns([]string{"value"})}).Create(&Option{Key: MerchantStoreAnnouncementOption, Value: content}).Error
	})
}
