package model

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const AcquisitionRawDays = 90
const AcquisitionAccountDays = 365

var ErrAcquisitionInvalid = errors.New("invalid acquisition data")
var opaqueSourceLabelPattern = regexp.MustCompile(`^[A-Za-z0-9]{41,}$`)
var sourceLabelPattern = regexp.MustCompile(`^[\pL\pN _.-]{1,80}$`)

type AcquisitionLink struct {
	ID        string `json:"id" gorm:"type:varchar(32);primaryKey"`
	Name      string `json:"name" gorm:"type:varchar(80);not null"`
	Source    string `json:"source" gorm:"type:varchar(80);index"`
	Medium    string `json:"medium" gorm:"type:varchar(80)"`
	Campaign  string `json:"campaign" gorm:"type:varchar(80);index"`
	Content   string `json:"content" gorm:"type:varchar(80)"`
	Target    string `json:"target" gorm:"type:varchar(80)"`
	Archived  bool   `json:"archived"`
	CreatedAt int64  `json:"created_at" gorm:"index"`
	DeletedAt int64  `json:"deleted_at,omitempty" gorm:"not null;default:0;index"`
}
type AcquisitionVisitor struct {
	ID        string `json:"-" gorm:"type:varchar(64);primaryKey"`
	UserID    int    `json:"-" gorm:"index"`
	CreatedAt int64  `json:"-" gorm:"index"`
}
type AcquisitionVisit struct {
	ConsentVersion int    `json:"consent_version"`
	ID             int64  `json:"id" gorm:"primaryKey"`
	VisitorID      string `json:"-" gorm:"type:varchar(64);not null;uniqueIndex:acquisition_visit_nonce;index"`
	Nonce          string `json:"-" gorm:"type:varchar(64);not null;uniqueIndex:acquisition_visit_nonce"`
	LinkID         string `json:"link_id,omitempty" gorm:"type:varchar(32);index"`
	Source         string `json:"source" gorm:"type:varchar(80);index"`
	Medium         string `json:"medium,omitempty" gorm:"type:varchar(80)"`
	Campaign       string `json:"campaign,omitempty" gorm:"type:varchar(80);index"`
	Content        string `json:"content,omitempty" gorm:"type:varchar(80)"`
	ReferrerHost   string `json:"referrer_host,omitempty" gorm:"type:varchar(253)"`
	Landing        string `json:"landing" gorm:"type:varchar(80)"`
	Evidence       string `json:"evidence" gorm:"type:varchar(32)"`
	CreatedAt      int64  `json:"created_at" gorm:"index"`
}
type AcquisitionAccount struct {
	ConsentVersion       int    `json:"consent_version"`
	FirstSource          string `json:"first_source" gorm:"type:varchar(80)"`
	FirstEvidence        string `json:"first_evidence" gorm:"type:varchar(32)"`
	FirstObservedAt      int64  `json:"first_observed_at"`
	RegistrationInferred bool   `json:"registration_inferred"`
	UserID               int    `json:"user_id" gorm:"primaryKey"`
	FirstVisitID         int64  `json:"first_visit_id"`
	RegistrationVisitID  int64  `json:"registration_visit_id"`
	RegistrationSource   string `json:"registration_source" gorm:"type:varchar(80);index"`
	RegistrationLinkID   string `json:"registration_link_id" gorm:"type:varchar(32);index"`
	RegistrationContent  string `json:"registration_content" gorm:"type:varchar(80)"`
	RegistrationCampaign string `json:"registration_campaign" gorm:"type:varchar(80)"`
	RegistrationEvidence string `json:"registration_evidence" gorm:"type:varchar(32)"`
	RegistrationAt       int64  `json:"registration_at" gorm:"index"`
	AttributionRule      string `json:"attribution_rule" gorm:"type:varchar(40)"`
	LookbackDays         int    `json:"lookback_days"`
	SelfReported         string `json:"self_reported,omitempty" gorm:"type:varchar(80)"`
	SelfReportedAt       int64  `json:"self_reported_at"`
	CreatedAt            int64  `json:"created_at" gorm:"index"`
}
type AcquisitionInput struct {
	ConsentVersion int    `json:"consent_version"`
	Landing        string `json:"landing"`
	Referrer       string `json:"referrer"`
	Source         string `json:"source"`
	Medium         string `json:"medium"`
	Campaign       string `json:"campaign"`
	Content        string `json:"content"`
	LinkID         string `json:"link_id"`
	Nonce          string `json:"nonce"`
	Consent        bool   `json:"consent"`
	Test           bool   `json:"test"`
}

func AcquisitionLabel(value string) string {
	value = strings.TrimSpace(value)
	lower := strings.ToLower(value)
	if !sourceLabelPattern.MatchString(value) || strings.Contains(lower, "sk-") || strings.Contains(lower, "bearer") || strings.Count(value, ".") >= 2 {
		return ""
	}
	// Avoid retaining long opaque URL parameters, which may contain credentials.
	if opaqueSourceLabelPattern.MatchString(value) {
		return ""
	}
	return value
}
func AcquisitionTarget(value string) string {
	switch value {
	case "/", "/guide", "/pricing", "/challenges", "/sign-up", "/sign-in":
		return value
	}
	return ""
}
func acquisitionHost(value string) string {
	if len(value) > 2048 {
		return ""
	}
	u, err := url.Parse(value)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil {
		return ""
	}
	host := strings.ToLower(u.Hostname())
	if len(host) > 253 || strings.ContainsAny(host, " @:%") || net.ParseIP(host) != nil || !strings.Contains(host, ".") || strings.HasSuffix(host, ".local") {
		return ""
	}
	return host
}
func AcquisitionRandomID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return ""
	}
	return hex.EncodeToString(b[:])
}
func AcquisitionVisitorHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

// No raw URL, query string, IP, user-agent or credential enters these models.
func NormalizeAcquisition(input AcquisitionInput, ownHosts []string, now int64) (AcquisitionVisit, error) {
	if !input.Consent || input.Test || len(input.Nonce) != 32 {
		return AcquisitionVisit{}, ErrAcquisitionInvalid
	}
	if _, err := hex.DecodeString(input.Nonce); err != nil {
		return AcquisitionVisit{}, ErrAcquisitionInvalid
	}
	landing, err := url.Parse(input.Landing)
	if err != nil || landing.IsAbs() || landing.Host != "" || strings.HasPrefix(landing.Path, "/oauth") || strings.Contains(landing.Path, "callback") || strings.Contains(landing.Path, "/return") {
		return AcquisitionVisit{}, ErrAcquisitionInvalid
	}
	page := AcquisitionTarget(landing.Path)
	if page == "" {
		return AcquisitionVisit{}, ErrAcquisitionInvalid
	}
	for _, key := range []string{"code", "state", "session_id", "payment_intent", "trade_no", "redirect_status"} {
		if landing.Query().Has(key) {
			return AcquisitionVisit{}, ErrAcquisitionInvalid
		}
	}
	host := acquisitionHost(input.Referrer)
	for _, own := range ownHosts {
		if strings.EqualFold(host, own) || strings.HasSuffix(host, "."+strings.ToLower(own)) {
			host = ""
		}
	}
	version := input.ConsentVersion
	if version == 0 {
		version = 1
	}
	if version < 1 || version > 2 {
		return AcquisitionVisit{}, ErrAcquisitionInvalid
	}
	result := AcquisitionVisit{ConsentVersion: version, Nonce: input.Nonce, Landing: page, ReferrerHost: host, CreatedAt: now, Source: "unknown", Evidence: "unavailable"}
	if source := AcquisitionLabel(input.Source); source != "" {
		result.Source = source
		result.Evidence = "campaign_parameters"
		result.Medium = AcquisitionLabel(input.Medium)
		result.Campaign = AcquisitionLabel(input.Campaign)
		result.Content = AcquisitionLabel(input.Content)
	} else if host != "" {
		result.Source = host
		result.Evidence = "browser_referrer"
	}
	return result, nil
}
func SaveAcquisitionLink(ctx context.Context, input AcquisitionLink) (AcquisitionLink, error) {
	if DB == nil {
		return input, gorm.ErrInvalidDB
	}
	db := DB.WithContext(ctx)
	input.Name = AcquisitionLabel(input.Name)
	if input.Name == "" {
		return input, ErrAcquisitionInvalid
	}
	if input.ID != "" {
		// Attribution dimensions and target are immutable. Rename/archive only.
		var old AcquisitionLink
		if err := db.Where("deleted_at = 0").First(&old, "id = ?", input.ID).Error; err != nil {
			return input, err
		}
		if err := db.Model(&old).Updates(map[string]any{"name": input.Name, "archived": input.Archived}).Error; err != nil {
			return input, err
		}
		old.Name = input.Name
		old.Archived = input.Archived
		return old, nil
	}
	input.Source = AcquisitionLabel(input.Source)
	input.Medium = AcquisitionLabel(input.Medium)
	input.Campaign = AcquisitionLabel(input.Campaign)
	input.Content = AcquisitionLabel(input.Content)
	input.Target = AcquisitionTarget(input.Target)
	if input.Source == "" || input.Target == "" {
		return input, ErrAcquisitionInvalid
	}
	input.ID = AcquisitionRandomID()
	input.CreatedAt = time.Now().Unix()
	if input.ID == "" {
		return input, ErrAcquisitionInvalid
	}
	return input, db.Create(&input).Error
}
func ObserveAcquisition(ctx context.Context, visitorID string, userID int, input AcquisitionInput, ownHosts []string) (AcquisitionVisit, error) {
	now := time.Now().Unix()
	visit, err := NormalizeAcquisition(input, ownHosts, now)
	if err != nil {
		return visit, err
	}
	if DB == nil {
		return visit, gorm.ErrInvalidDB
	}
	if len(visitorID) != 64 {
		return visit, ErrAcquisitionInvalid
	}
	db := DB.WithContext(ctx)
	if _, err := acquisitionConfig(db); err != nil {
		return visit, err
	}
	err = db.Transaction(func(tx *gorm.DB) error {
		if userID > 0 {
			var consent AcquisitionConsent
			err := tx.First(&consent, "user_id = ?", userID).Error
			if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			if err == nil && !consent.Allowed || visit.ConsentVersion >= 2 && (err != nil || consent.Version < 2) {
				return ErrAcquisitionInvalid
			}
		}
		visitor := AcquisitionVisitor{ID: visitorID, CreatedAt: now}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&visitor).Error; err != nil {
			return err
		}
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&visitor, "id = ?", visitorID).Error; err != nil {
			return err
		}
		// A shared browser must not transfer another account's history.
		if visitor.UserID != 0 && visitor.UserID != userID {
			return ErrAcquisitionInvalid
		}
		if userID > 0 {
			if err := tx.Model(&visitor).Where("user_id = 0 OR user_id = ?", userID).Update("user_id", userID).Error; err != nil {
				return err
			}
		}
		if err := applyAcquisitionLink(tx, input.LinkID, &visit); err != nil {
			return err
		}
		if userID > 0 && visit.ConsentVersion >= 2 {
			upgraded := tx.Model(&AcquisitionAccount{}).Where("user_id = ? AND consent_version < 2", userID).Update("consent_version", 2)
			if upgraded.Error != nil {
				return upgraded.Error
			}

		}
		visit.VisitorID = visitorID
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&visit).Error; err != nil {
			return err
		}
		return tx.Where("visitor_id = ? AND nonce = ?", visitorID, input.Nonce).First(&visit).Error
	})
	return visit, err
}

// Called only by a successful registration path, never by a later login.
func AttributeAcquisitionRegistration(ctx context.Context, userID int, visitorID string) error {
	if DB == nil || userID <= 0 {
		return gorm.ErrInvalidData
	}
	return DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var user User
		if err := tx.Select("id", "created_at").First(&user, userID).Error; err != nil {
			return err
		}
		now := time.Now().Unix()
		config, err := acquisitionConfig(tx)
		if err != nil {
			return err
		}
		account := AcquisitionAccount{UserID: userID, RegistrationAt: user.CreatedAt, CreatedAt: now, RegistrationSource: "unknown", RegistrationEvidence: "unavailable", LookbackDays: config.LookbackDays, AttributionRule: fmt.Sprintf("current_or_last_external_%dd", config.LookbackDays)}
		if len(visitorID) == 64 {
			claim := tx.Model(&AcquisitionVisitor{}).Where("id = ? AND (user_id = 0 OR user_id = ?)", visitorID, userID).Update("user_id", userID)
			if claim.Error != nil {
				return claim.Error
			}
			// Some databases report zero affected rows for an unchanged owner.
			// Read ownership instead of treating that as an unassociated visit.
			var owner AcquisitionVisitor
			ownerErr := tx.First(&owner, "id = ?", visitorID).Error
			if ownerErr != nil && !errors.Is(ownerErr, gorm.ErrRecordNotFound) {
				return ownerErr
			}
			if ownerErr == nil && owner.UserID == userID {
				var consentVisit AcquisitionVisit
				consentErr := tx.Where("visitor_id = ? AND created_at <= ?", visitorID, user.CreatedAt).Order("created_at DESC, id DESC").First(&consentVisit).Error
				if consentErr == nil {
					account.ConsentVersion = consentVisit.ConsentVersion
				} else if !errors.Is(consentErr, gorm.ErrRecordNotFound) {
					return consentErr
				}
				var first, selected AcquisitionVisit
				err := tx.Where("visitor_id = ? AND created_at >= ? AND created_at <= ?", visitorID, user.CreatedAt-AcquisitionRawDays*86400, user.CreatedAt).Order("created_at ASC, id ASC").First(&first).Error
				if err == nil {
					account.FirstVisitID = first.ID
					account.FirstSource = first.Source
					account.FirstEvidence = first.Evidence
					account.FirstObservedAt = first.CreatedAt
				} else if !errors.Is(err, gorm.ErrRecordNotFound) {
					return err
				}
				err = tx.Where("visitor_id = ? AND created_at >= ? AND created_at <= ? AND source <> ?", visitorID, user.CreatedAt-int64(config.LookbackDays)*86400, user.CreatedAt, "unknown").Order("created_at DESC, id DESC").First(&selected).Error
				if err == nil {
					var latest AcquisitionVisit
					if err := tx.Where("visitor_id = ? AND created_at <= ?", visitorID, user.CreatedAt).Order("created_at DESC, id DESC").First(&latest).Error; err != nil {
						return err
					}
					account.RegistrationInferred = selected.ID != latest.ID
					account.RegistrationVisitID = selected.ID
					account.RegistrationSource = selected.Source
					account.RegistrationLinkID = selected.LinkID
					account.RegistrationCampaign = selected.Campaign
					account.RegistrationContent = selected.Content
					account.RegistrationEvidence = selected.Evidence
				} else if !errors.Is(err, gorm.ErrRecordNotFound) {
					return err
				}
			}
		}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&account).Error; err != nil {
			return err
		}
		var locked AcquisitionAccount
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Select("user_id").First(&locked, "user_id = ?", userID).Error; err != nil {
			return err
		}
		var consent AcquisitionConsent
		err = tx.First(&consent, "user_id = ?", userID).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err == nil && !consent.Allowed {
			return tx.Where("user_id = ?", userID).Delete(&AcquisitionAccount{}).Error
		}
		if account.ConsentVersion >= 2 {
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&AcquisitionConsent{UserID: userID, Allowed: true, Version: 2, UpdatedAt: now}).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func RevokeAcquisitionVisitor(ctx context.Context, visitorID string) error {
	if DB == nil {
		return gorm.ErrInvalidDB
	}
	return DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var owner AcquisitionVisitor
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&owner, "id = ?", visitorID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			return err
		}
		if owner.UserID > 0 {
			if err := revokeAcquisitionAccountTx(tx, owner.UserID); err != nil {
				return err
			}
		}

		if err := tx.Where("visitor_id = ?", visitorID).Delete(&AcquisitionVisit{}).Error; err != nil {
			return err
		}
		return tx.Where("id = ?", visitorID).Delete(&AcquisitionVisitor{}).Error
	})
}

// Authenticated privacy withdrawal also works after the visitor cookie expires.
func RevokeAcquisitionAccount(ctx context.Context, userID int) error {
	if userID <= 0 {
		return nil
	}
	if DB == nil {
		return gorm.ErrInvalidDB
	}
	return DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error { return revokeAcquisitionAccountTx(tx, userID) })
}
