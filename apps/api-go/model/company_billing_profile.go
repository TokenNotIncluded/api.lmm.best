package model

import (
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	CompanyBillingPostcodeMaxRunes     = 32
	CompanyBillingStateMaxRunes        = 128
	CompanyBillingBusinessNameMaxRunes = 255
	CompanyBillingTaxIDMaxRunes        = 64
)

var ErrCompanyBillingProfileNotFound = errors.New("company billing profile not found")

// CompanyBillingProfile contains invoice identity data owned by exactly one user.
// BusinessName and TaxID must never be included in logs, audit details, or errors.
type CompanyBillingProfile struct {
	UserID         int    `json:"-" gorm:"column:user_id;primaryKey;autoIncrement:false;not null"`
	Country        string `json:"country" gorm:"type:char(2);not null"`
	IsBusiness     bool   `json:"isBusiness" gorm:"not null"`
	Postcode       string `json:"postcode" gorm:"type:varchar(32);not null;default:''"`
	State          string `json:"state" gorm:"type:varchar(128);not null;default:''"`
	BusinessName   string `json:"businessName" gorm:"type:varchar(255);not null;default:''"`
	TaxID          string `json:"taxId" gorm:"column:tax_id;type:varchar(64);not null;default:''"`
	UseForInvoices bool   `json:"useForInvoices" gorm:"not null;default:false"`
	CreatedAt      int64  `json:"createdAt" gorm:"not null"`
	UpdatedAt      int64  `json:"updatedAt" gorm:"not null"`
}

func (CompanyBillingProfile) TableName() string { return "company_billing_profiles" }

type CompanyBillingProfileInput struct {
	Country        string
	IsBusiness     bool
	Postcode       string
	State          string
	BusinessName   string
	TaxID          string
	UseForInvoices bool
}

type CompanyBillingProfileFieldError struct {
	Field string
	Code  string
}

func (e *CompanyBillingProfileFieldError) Error() string {
	return "invalid company billing profile field: " + e.Field
}

func companyBillingFieldError(field, code string) error {
	return &CompanyBillingProfileFieldError{Field: field, Code: code}
}

func normalizeCompanyBillingText(value string) string {
	return strings.TrimSpace(value)
}

func validateCompanyBillingText(field, value string, maxRunes int) error {
	if !utf8.ValidString(value) {
		return companyBillingFieldError(field, "invalid_text")
	}
	if utf8.RuneCountInString(value) > maxRunes {
		return companyBillingFieldError(field, "too_long")
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return companyBillingFieldError(field, "invalid_text")
		}
	}
	return nil
}

var iso3166Alpha2Countries = func() map[string]struct{} {
	const codes = "AD AE AF AG AI AL AM AO AQ AR AS AT AU AW AX AZ BA BB BD BE BF BG BH BI BJ BL BM BN BO BQ BR BS BT BV BW BY BZ CA CC CD CF CG CH CI CK CL CM CN CO CR CU CV CW CX CY CZ DE DJ DK DM DO DZ EC EE EG EH ER ES ET FI FJ FK FM FO FR GA GB GD GE GF GG GH GI GL GM GN GP GQ GR GS GT GU GW GY HK HM HN HR HT HU ID IE IL IM IN IO IQ IR IS IT JE JM JO JP KE KG KH KI KM KN KP KR KW KY KZ LA LB LC LI LK LR LS LT LU LV LY MA MC MD ME MF MG MH MK ML MM MN MO MP MQ MR MS MT MU MV MW MX MY MZ NA NC NE NF NG NI NL NO NP NR NU NZ OM PA PE PF PG PH PK PL PM PN PR PS PT PW PY QA RE RO RS RU RW SA SB SC SD SE SG SH SI SJ SK SL SM SN SO SR SS ST SV SX SY SZ TC TD TF TG TH TJ TK TL TM TN TO TR TT TV TW TZ UA UG UM US UY UZ VA VC VE VG VI VN VU WF WS YE YT ZA ZM ZW"
	result := make(map[string]struct{}, 249)
	for _, code := range strings.Fields(codes) {
		result[code] = struct{}{}
	}
	return result
}()

func ValidateCompanyBillingAddress(country, state, postcode string) error {
	country = strings.ToUpper(normalizeCompanyBillingText(country))
	if _, ok := iso3166Alpha2Countries[country]; !ok {
		return companyBillingFieldError("country", "invalid_country")
	}
	if err := validateCompanyBillingText("state", normalizeCompanyBillingText(state), CompanyBillingStateMaxRunes); err != nil {
		return err
	}
	return validateCompanyBillingText("postcode", normalizeCompanyBillingText(postcode), CompanyBillingPostcodeMaxRunes)
}

func NormalizeAndValidateCompanyBillingProfile(input CompanyBillingProfileInput) (CompanyBillingProfileInput, error) {
	input.Country = strings.ToUpper(normalizeCompanyBillingText(input.Country))
	input.Postcode = normalizeCompanyBillingText(input.Postcode)
	input.State = normalizeCompanyBillingText(input.State)
	input.BusinessName = normalizeCompanyBillingText(input.BusinessName)
	input.TaxID = normalizeCompanyBillingText(input.TaxID)

	if err := ValidateCompanyBillingAddress(input.Country, input.State, input.Postcode); err != nil {
		return CompanyBillingProfileInput{}, err
	}
	for _, field := range []struct {
		name  string
		value string
		max   int
	}{
		{name: "businessName", value: input.BusinessName, max: CompanyBillingBusinessNameMaxRunes},
		{name: "taxId", value: input.TaxID, max: CompanyBillingTaxIDMaxRunes},
	} {
		if err := validateCompanyBillingText(field.name, field.value, field.max); err != nil {
			return CompanyBillingProfileInput{}, err
		}
	}

	return input, nil
}

// ValidateCompanyBillingProfileRequiredFields accepts only an authoritative
// provider response. A nil slice means that preview failed or omitted rules
// and is rejected; a non-nil empty slice is an explicit provider decision.
func ValidateCompanyBillingProfileRequiredFields(profile *CompanyBillingProfile, requiredFields []string) error {
	if profile == nil || requiredFields == nil {
		return companyBillingFieldError("requiredFields", "preview_unavailable")
	}
	values := map[string]string{
		"postcode":     profile.Postcode,
		"state":        profile.State,
		"businessName": profile.BusinessName,
		"taxId":        profile.TaxID,
	}
	seen := make(map[string]struct{}, len(requiredFields))
	for _, rawField := range requiredFields {
		field := strings.TrimSpace(rawField)
		if _, duplicate := seen[field]; duplicate {
			continue
		}
		seen[field] = struct{}{}
		value, supported := values[field]
		if !supported {
			return companyBillingFieldError("requiredFields", "unsupported_field")
		}
		if value == "" {
			return companyBillingFieldError(field, "required")
		}
	}
	return nil
}

func GetCompanyBillingProfile(userID int) (*CompanyBillingProfile, error) {
	if userID <= 0 {
		return nil, ErrCompanyBillingProfileNotFound
	}
	var profile CompanyBillingProfile
	if err := DB.Where("user_id = ?", userID).First(&profile).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrCompanyBillingProfileNotFound
		}
		return nil, err
	}
	return &profile, nil
}

func GetCompanyBillingProfileForAutomaticBilling(userID int) (*CompanyBillingProfile, error) {
	if userID <= 0 {
		return nil, nil
	}
	var profile CompanyBillingProfile
	if err := DB.Where("user_id = ? AND use_for_invoices = ?", userID, true).First(&profile).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &profile, nil
}

func SaveCompanyBillingProfile(userID int, input CompanyBillingProfileInput) (*CompanyBillingProfile, error) {
	if userID <= 0 {
		return nil, ErrCompanyBillingProfileNotFound
	}
	normalized, err := NormalizeAndValidateCompanyBillingProfile(input)
	if err != nil {
		return nil, err
	}
	now := common.GetTimestamp()
	profile := CompanyBillingProfile{
		UserID:         userID,
		Country:        normalized.Country,
		IsBusiness:     normalized.IsBusiness,
		Postcode:       normalized.Postcode,
		State:          normalized.State,
		BusinessName:   normalized.BusinessName,
		TaxID:          normalized.TaxID,
		UseForInvoices: normalized.UseForInvoices,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := DB.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "user_id"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"country", "is_business", "postcode", "state", "business_name", "tax_id",
			"use_for_invoices", "updated_at",
		}),
	}).Create(&profile).Error; err != nil {
		return nil, err
	}
	return GetCompanyBillingProfile(userID)
}
