package model

import (
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrMerchantStoreSellerTerms = errors.New("current seller terms must be configured and accepted")

// A seller configures terms; a buyer accepts them. Preserve errors.Is for
// existing moderation callers without presenting a buyer action to a seller.
var ErrMerchantStoreSellerTermsNotConfigured error = &merchantStoreSellerTermsNotConfigured{}

type merchantStoreSellerTermsNotConfigured struct{}

func (*merchantStoreSellerTermsNotConfigured) Error() string {
	return "configure seller terms before publishing"
}
func (*merchantStoreSellerTermsNotConfigured) Unwrap() error { return ErrMerchantStoreSellerTerms }

type merchantStoreTermsVersionChanged struct{}

func (*merchantStoreTermsVersionChanged) Error() string { return "seller terms version changed" }
func (*merchantStoreTermsVersionChanged) Unwrap() error { return ErrMerchantStoreSellerTerms }

// Constructed only by checkout after authenticated subject locks and its exact
// request-key replay lookup. No order or wallet mutation has occurred.
type MerchantStoreTermsUpdatedError struct{ requestKey string }

func (*MerchantStoreTermsUpdatedError) Error() string {
	return "seller terms updated before order creation"
}
func (*MerchantStoreTermsUpdatedError) Unwrap() error        { return ErrMerchantStoreSellerTerms }
func (e *MerchantStoreTermsUpdatedError) RequestKey() string { return e.requestKey }

type MerchantStoreSellerTerms struct {
	SellerID  int    `json:"-" gorm:"primaryKey"`
	Version   string `json:"version" gorm:"size:36;not null"`
	Content   string `json:"content" gorm:"type:text;not null"`
	UpdatedAt int64  `json:"updated_at"`
}

type MerchantStoreTermsAcceptance struct {
	ID         string `json:"-" gorm:"primaryKey;size:64"`
	Kind       string `json:"-" gorm:"size:16;not null;default:seller"`
	Subject    string `json:"-" gorm:"size:48;not null;index"`
	SellerID   int    `json:"-" gorm:"not null;index"`
	Version    string `json:"-" gorm:"size:36;not null"`
	AcceptedAt int64  `json:"-" gorm:"type:bigint;not null"`
}

type MerchantStoreTermsView struct {
	Version    string `json:"version"`
	Content    string `json:"content"`
	Required   bool   `json:"required"`
	Configured bool   `json:"configured"`
	Accepted   bool   `json:"accepted"`
	UpdatedAt  int64  `json:"updated_at"`
}

type MerchantStoreTermsInput struct {
	Content         string `json:"content"`
	ExpectedVersion string `json:"expected_version"`
}

func storeTermsContentValid(content string) bool {
	return utf8.ValidString(content) && len(content) <= 64<<10 && strings.TrimSpace(content) != "" && !strings.ContainsRune(content, 0)
}

func storeSellerTerms(tx *gorm.DB, sellerID int) (*MerchantStoreSellerTerms, error) {
	var row MerchantStoreSellerTerms
	if e := tx.First(&row, "seller_id = ?", sellerID).Error; errors.Is(e, gorm.ErrRecordNotFound) {
		return nil, nil
	} else if e != nil {
		return nil, e
	}
	if !storeTermsContentValid(row.Content) || len(row.Version) != 36 {
		return nil, ErrMerchantStoreSellerTerms
	}
	return &row, nil
}

func storeTermsView(tx *gorm.DB, sellerID int, subject string) (*MerchantStoreTermsView, error) {
	if !storeAccessActive(tx) {
		return &MerchantStoreTermsView{}, nil
	}
	v := &MerchantStoreTermsView{Required: true}
	row, e := storeSellerTerms(tx, sellerID)
	if e != nil || row == nil {
		return v, e
	}
	v.Version, v.Content, v.UpdatedAt, v.Configured = row.Version, row.Content, row.UpdatedAt, true
	if subject != "" {
		var n int64
		e = tx.Model(&MerchantStoreTermsAcceptance{}).Where("id = ?", storeAgreementAcceptanceID("seller", subject, sellerID, row.Version)).Count(&n).Error
		v.Accepted = n == 1
	}
	return v, e
}

func GetMerchantStoreSellerTerms(actor int) (*MerchantStoreTermsView, error) {
	if _, e := storeUser(DB, actor, common.RoleCommonUser); e != nil {
		return nil, e
	}
	return storeTermsView(DB, actor, "")
}

func SaveMerchantStoreSellerTerms(actor int, in MerchantStoreTermsInput) (*MerchantStoreTermsView, error) {
	in.Content = strings.TrimSpace(in.Content)
	if !storeTermsContentValid(in.Content) || len(in.ExpectedVersion) > 36 {
		return nil, ErrMerchantStoreInput
	}
	var view *MerchantStoreTermsView
	e := marketTransaction(DB, func(tx *gorm.DB) error {
		// Checkout also locks the seller before reading terms. This serializes
		// initial creation and revisions with the frozen checkout agreement.
		if e := marketLockUsers(tx, actor); e != nil {
			return e
		}
		if _, e := storeUser(tx, actor, common.RoleCommonUser); e != nil {
			return e
		}
		if e := MerchantStoreAccessRequiresWriter(tx); e != nil {
			return e
		}
		row, e := storeSellerTerms(tx, actor)
		if e != nil {
			return e
		}
		version := ""
		if row != nil {
			version = row.Version
		}
		if in.ExpectedVersion != version {
			return ErrMerchantStoreConflict
		}
		if row == nil || row.Content != in.Content {
			row = &MerchantStoreSellerTerms{SellerID: actor, Version: uuid.NewString(), Content: in.Content, UpdatedAt: common.GetTimestamp()}
			if e := tx.Save(row).Error; e != nil {
				return e
			}
			if e := storeEvent(tx, actor, fmtStoreActor(actor), "seller_terms_revision"); e != nil {
				return e
			}
		}
		view, e = storeTermsView(tx, actor, "")
		return e
	})
	return view, e
}

func storeAgreementAcceptanceID(kind, subject string, sellerID int, version string) string {
	return storeHash("store-agreement:" + kind + ":" + subject + ":" + fmtStoreActor(sellerID) + ":" + version)
}

func storeRequireConfiguredSellerTerms(tx *gorm.DB, sellerID int) error {
	required, e := storeWriterGateRow(tx, "SHARE")
	if e != nil {
		return e
	}
	if required < 5 {
		return nil
	}
	row, e := storeSellerTerms(tx, sellerID)
	if errors.Is(e, ErrMerchantStoreSellerTerms) {
		return ErrMerchantStoreSellerTermsNotConfigured
	}
	if e != nil {
		return e
	}
	if row == nil {
		return ErrMerchantStoreSellerTermsNotConfigured
	}
	return nil
}

// Called after locking seller and before stock/fees; all sellers, including
// official accounts, must have their own current terms. Replays return before
// this check and retain the agreement actually accepted for that order.
func storeCheckoutSellerTerms(tx *gorm.DB, sellerID int, subject string, in MerchantStoreCheckoutInput) (*MerchantStoreSellerTerms, int64, error) {
	required, e := storeWriterGateRow(tx, "SHARE")
	if e != nil {
		return nil, 0, e
	}
	if required < 5 {
		return nil, 0, nil
	}
	row, e := storeSellerTerms(tx, sellerID)
	if e != nil {
		return nil, 0, e
	}
	if row == nil || subject == "" {
		return nil, 0, ErrMerchantStoreSellerTerms
	}
	if in.SellerTermsVersion != row.Version {
		return nil, 0, &merchantStoreTermsVersionChanged{}
	}
	id := storeAgreementAcceptanceID("seller", subject, sellerID, row.Version)
	var accepted MerchantStoreTermsAcceptance
	e = tx.First(&accepted, "id = ?", id).Error
	if e == nil {
		return row, accepted.AcceptedAt, nil
	}
	if !errors.Is(e, gorm.ErrRecordNotFound) {
		return nil, 0, e
	}
	if !in.AcceptSellerTerms {
		return nil, 0, ErrMerchantStoreSellerTerms
	}
	accepted = MerchantStoreTermsAcceptance{ID: id, Kind: "seller", Subject: subject, SellerID: sellerID, Version: row.Version, AcceptedAt: common.GetTimestamp()}
	e = tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&accepted).Error
	return row, accepted.AcceptedAt, e
}

func GetMerchantStoreProductTerms(actor int, guestToken, productID string) (*MerchantStoreTermsView, error) {
	p, e := GetMerchantStoreProductForViewer(actor, productID)
	if e != nil {
		return nil, e
	}
	subject := ""
	if actor > 0 {
		if _, e := storeUser(DB, actor, common.RoleCommonUser); e != nil {
			return nil, e
		}
		subject = "user:" + fmtStoreActor(actor)
	} else if guestToken != "" {
		guest, e := ResolveMerchantStoreGuest(DB, guestToken)
		if e != nil {
			return nil, e
		}
		subject = "guest:" + guest.ID
	}
	return storeTermsView(DB, p.SellerID, subject)
}
