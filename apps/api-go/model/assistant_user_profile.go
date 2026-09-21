package model

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"gorm.io/gorm"
)

const (
	AssistantProfileUnknown      = "unknown"
	AssistantProfileTechnical    = "technical_cost_sensitive"
	AssistantProfileGuided       = "guided_buyer"
	AssistantProfilePromotion    = "promotion_seeker"
	AssistantProfileSecurityRisk = "security_risk"
	AssistantProfileOperator     = "production_operator"
	AssistantProfilePrivacy      = "privacy_conscious"
	AssistantProfileAccessible   = "mobile_accessibility"
	AssistantProfileNormal       = "normal_user"
	AssistantProfileSupport      = "support_seeking"
	AssistantProfileL0Applicant  = "l0_applicant"
	AssistantProfileCustom       = "custom"
	AssistantProfileSourceAI     = "assistant"
	AssistantProfileSourceAdmin  = "administrator"

	AssistantUserProfileMaxStrategyRunes = 4000
	AssistantUserProfileMaxTags          = 12
	AssistantUserProfileMaxTagRunes      = 48
)

var (
	ErrAssistantProfileKey          = errors.New("assistant profile key is invalid")
	ErrAssistantProfileStrategyLong = errors.New("assistant profile strategy is too long")
	ErrAssistantProfileTagsInvalid  = errors.New("assistant profile tags are invalid")
	ErrAssistantProfileManaged      = errors.New("assistant profile is administrator managed")
	ErrAssistantProfileMissing      = errors.New("assistant profile not found")
	ErrAssistantProfileOwner        = errors.New("assistant profile can only be forgotten by its owner")
	assistantProfileSecretPattern   = regexp.MustCompile(`(?i)(password|passwd|api[ _-]?key|access[ _-]?token|refresh[ _-]?token|client[ _-]?secret|secret|credential|密码|密钥|令牌)\s*[:=：]\s*[^\s,;]+`)
)

// AssistantUserProfile is a per-user response-style skill. The source records
// whether it was learned by the assistant or authored by an administrator;
// administrator-owned rows are authoritative and cannot be overwritten by
// the assistant. It intentionally stores no raw conversations, inferred
// identity traits, or credentials, and is never exposed by a user endpoint.
type AssistantUserProfile struct {
	Id         int    `json:"-" gorm:"primaryKey"`
	UserId     int    `json:"-" gorm:"not null;uniqueIndex"`
	ProfileKey string `json:"-" gorm:"type:varchar(64);not null;default:''"`
	TagsJSON   string `json:"-" gorm:"type:text;not null;default:'[]'"`
	Strategy   string `json:"-" gorm:"type:text;not null;default:''"`
	Source     string `json:"-" gorm:"type:varchar(24);not null;default:administrator"`
	Enabled    bool   `json:"-" gorm:"not null;default:false"`
	UpdatedBy  int    `json:"-" gorm:"not null;default:0;index"`
	CreatedAt  int64  `json:"-" gorm:"not null"`
	UpdatedAt  int64  `json:"-" gorm:"not null;index"`
}

// AssistantUserProfileView is the administrator-only representation. Keep
// this separate from the database model so a future user-facing serializer
// cannot accidentally expose UpdatedBy, the raw JSON column, or internal
// storage details.
type AssistantUserProfileView struct {
	ProfileKey string   `json:"profile_key"`
	Tags       []string `json:"tags"`
	Strategy   string   `json:"strategy"`
	Enabled    bool     `json:"enabled"`
	Source     string   `json:"source"`
	UpdatedAt  int64    `json:"updated_at"`
}

// AssistantUserProfileSummary is the bounded administrator-list view. It is
// deliberately smaller than AssistantUserProfileView: a user table needs the
// AI labels and provenance, never the private response strategy.
type AssistantUserProfileSummary struct {
	ProfileKey string   `json:"profile_key"`
	Tags       []string `json:"tags"`
	Source     string   `json:"source"`
	UpdatedAt  int64    `json:"updated_at"`
}

type ProfileInput struct {
	Key      string
	Tags     []string
	Strategy string
	Source   string
	Enabled  bool
}

// NormalizeProfileInput validates and canonicalizes a profile without
// touching the database.  Confirmation-based callers use this helper to
// preview exactly the value that SaveProfile will persist.
func NormalizeProfileInput(input ProfileInput) (ProfileInput, error) {
	if input.Source != AssistantProfileSourceAI && input.Source != AssistantProfileSourceAdmin {
		return ProfileInput{}, ErrAssistantProfileKey
	}
	profileKey, err := NormalizeAssistantProfileKey(input.Key)
	if err != nil {
		return ProfileInput{}, err
	}
	strategy, err := NormalizeAssistantProfileStrategy(input.Strategy)
	if err != nil {
		return ProfileInput{}, err
	}
	tags, err := NormalizeAssistantProfileTags(input.Tags)
	if err != nil {
		return ProfileInput{}, err
	}
	if profileKey == "" {
		input.Enabled = false
	}
	input.Key, input.Strategy, input.Tags = profileKey, strategy, tags
	return input, nil
}

func (AssistantUserProfile) TableName() string { return "assistant_user_profiles" }

func validAssistantProfileKeys() map[string]struct{} {
	return map[string]struct{}{
		AssistantProfileUnknown:      {},
		AssistantProfileTechnical:    {},
		AssistantProfileGuided:       {},
		AssistantProfilePromotion:    {},
		AssistantProfileSecurityRisk: {},
		AssistantProfileOperator:     {},
		AssistantProfilePrivacy:      {},
		AssistantProfileAccessible:   {},
		AssistantProfileNormal:       {},
		AssistantProfileSupport:      {},
		AssistantProfileL0Applicant:  {},
		AssistantProfileCustom:       {},
	}
}

// NormalizeAssistantProfileKey is shared by the admin API and the assistant
// runtime so a manually edited value cannot become an arbitrary prompt field.
func NormalizeAssistantProfileKey(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return "", nil
	}
	if _, ok := validAssistantProfileKeys()[value]; !ok {
		return "", ErrAssistantProfileKey
	}
	return value, nil
}

// NormalizeAssistantProfileStrategy removes controls and credential-shaped
// values before a trusted administrator note can reach an assistant model.
// This is a safety boundary, not a substitute for the normal secret filter.
func NormalizeAssistantProfileStrategy(value string) (string, error) {
	value = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.In(r, unicode.Cf) {
			return -1
		}
		return r
	}, strings.TrimSpace(value))
	value = assistantProfileSecretPattern.ReplaceAllString(value, "$1: [REDACTED]")
	value = strings.Join(strings.Fields(value), " ")
	if utf8.RuneCountInString(value) > AssistantUserProfileMaxStrategyRunes {
		return "", ErrAssistantProfileStrategyLong
	}
	return value, nil
}

func NormalizeAssistantProfileTags(tags []string) ([]string, error) {
	if len(tags) > AssistantUserProfileMaxTags {
		return nil, ErrAssistantProfileTagsInvalid
	}
	seen := make(map[string]struct{}, len(tags))
	result := make([]string, 0, len(tags))
	for _, tag := range tags {
		tag = strings.Map(func(r rune) rune {
			if unicode.IsControl(r) || unicode.In(r, unicode.Cf) {
				return -1
			}
			return r
		}, strings.TrimSpace(tag))
		tag = strings.Join(strings.Fields(tag), " ")
		// Tags are shown in administrator tooling, so apply the same secret
		// redaction boundary as assistant memory before persisting them.
		tag = RedactAssistantHistoryContent(tag)
		if tag == "" {
			continue
		}
		if utf8.RuneCountInString(tag) > AssistantUserProfileMaxTagRunes {
			return nil, ErrAssistantProfileTagsInvalid
		}
		if _, ok := seen[tag]; ok {
			continue
		}
		seen[tag] = struct{}{}
		result = append(result, tag)
	}
	return result, nil
}

func AssistantUserProfileTags(profile *AssistantUserProfile) []string {
	if profile == nil || strings.TrimSpace(profile.TagsJSON) == "" {
		return nil
	}
	var tags []string
	if json.Unmarshal([]byte(profile.TagsJSON), &tags) != nil {
		return nil
	}
	validated, err := NormalizeAssistantProfileTags(tags)
	if err != nil {
		return nil
	}
	return validated
}

func AssistantUserProfileViewOf(profile *AssistantUserProfile) AssistantUserProfileView {
	if profile == nil {
		return AssistantUserProfileView{Tags: []string{}}
	}
	strategy, err := NormalizeAssistantProfileStrategy(profile.Strategy)
	if err != nil {
		strategy = assistantProfileSecretPattern.ReplaceAllString(profile.Strategy, "$1: [REDACTED]")
		strategyRunes := []rune(strategy)
		if len(strategyRunes) > AssistantUserProfileMaxStrategyRunes {
			strategy = string(strategyRunes[:AssistantUserProfileMaxStrategyRunes])
		}
	}
	return AssistantUserProfileView{
		ProfileKey: profile.ProfileKey,
		Tags:       AssistantUserProfileTags(profile),
		Strategy:   strategy,
		Enabled:    profile.Enabled,
		Source:     profile.Source,
		UpdatedAt:  profile.UpdatedAt,
	}
}

func AssistantUserProfileSummaryOf(profile *AssistantUserProfile) AssistantUserProfileSummary {
	if profile == nil {
		return AssistantUserProfileSummary{Tags: []string{}}
	}
	return AssistantUserProfileSummary{
		ProfileKey: profile.ProfileKey,
		Tags:       AssistantUserProfileTags(profile),
		Source:     profile.Source,
		UpdatedAt:  profile.UpdatedAt,
	}
}

func GetAssistantUserProfile(userID int) (*AssistantUserProfile, error) {
	if userID <= 0 {
		return nil, gorm.ErrInvalidData
	}
	var profile AssistantUserProfile
	result := DB.Where("user_id = ?", userID).Limit(1).Find(&profile)
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected == 0 {
		return nil, nil
	}
	return &profile, nil
}

// PopulateAssistantUserProfiles attaches bounded AI-label summaries to user
// management rows. Visibility mirrors transcript history: an administrator
// may see only a strictly lower-role account, and no ordinary user receives
// another account's internal profile. The query is batched for the page, so a
// user table never incurs one profile query per row.
func PopulateAssistantUserProfiles(users []*User, viewerUserID, viewerRole int) error {
	return PopulateAssistantUserProfilesContext(context.Background(), users, viewerUserID, viewerRole)
}

func PopulateAssistantUserProfilesContext(ctx context.Context, users []*User, viewerUserID, viewerRole int) error {
	authorizedIDs := make([]int, 0, len(users))
	usersByID := make(map[int]*User, len(users))
	for _, user := range users {
		if user == nil {
			continue
		}
		user.AssistantProfile = nil
		if viewerRole < common.RoleAdminUser || viewerRole <= user.Role || user.Id == viewerUserID {
			continue
		}
		authorizedIDs = append(authorizedIDs, user.Id)
		usersByID[user.Id] = user
	}
	if len(authorizedIDs) == 0 {
		return nil
	}

	var profiles []AssistantUserProfile
	if err := DB.WithContext(ctx).Where("user_id IN ? AND enabled = ?", authorizedIDs, true).
		Select("user_id, profile_key, tags_json, source, updated_at").
		Find(&profiles).Error; err != nil {
		return err
	}
	for index := range profiles {
		profile := &profiles[index]
		if user := usersByID[profile.UserId]; user != nil {
			summary := AssistantUserProfileSummaryOf(profile)
			user.AssistantProfile = &summary
		}
	}
	return nil
}

func UpsertAssistantUserProfile(userID, updatedBy int, profileKey string, tags []string, strategy string, enabled bool) (*AssistantUserProfile, error) {
	return SaveProfile(userID, updatedBy, ProfileInput{
		Key: profileKey, Tags: tags, Strategy: strategy,
		Source: AssistantProfileSourceAdmin, Enabled: enabled,
	})
}

func SaveProfile(userID, updatedBy int, input ProfileInput) (*AssistantUserProfile, error) {
	if userID <= 0 || updatedBy <= 0 {
		return nil, gorm.ErrInvalidData
	}
	var err error
	input, err = NormalizeProfileInput(input)
	if err != nil {
		return nil, err
	}
	profileKey, strategy, tags := input.Key, input.Strategy, input.Tags
	tagsJSON, err := json.Marshal(tags)
	if err != nil {
		return nil, err
	}
	now := common.GetTimestamp()
	var saved AssistantUserProfile
	err = DB.Transaction(func(tx *gorm.DB) error {
		if err := lockAssistantOwner(tx, userID); err != nil {
			return err
		}
		findResult := lockForUpdate(tx).Where("user_id = ?", userID).Limit(1).Find(&saved)
		if findResult.Error != nil {
			return findResult.Error
		}
		if findResult.RowsAffected == 0 {
			saved = AssistantUserProfile{
				UserId: userID, ProfileKey: profileKey, TagsJSON: string(tagsJSON), Strategy: strategy,
				Source: input.Source, Enabled: input.Enabled, UpdatedBy: updatedBy, CreatedAt: now, UpdatedAt: now,
			}
			return tx.Create(&saved).Error
		}
		if input.Source == AssistantProfileSourceAI && saved.Source != AssistantProfileSourceAI {
			return ErrAssistantProfileManaged
		}
		saved.ProfileKey, saved.TagsJSON, saved.Strategy = profileKey, string(tagsJSON), strategy
		saved.Source, saved.Enabled, saved.UpdatedBy, saved.UpdatedAt = input.Source, input.Enabled, updatedBy, now
		return tx.Save(&saved).Error
	})
	if err != nil {
		return nil, err
	}
	return &saved, nil
}

// DeleteAssistantUserProfile removes only an assistant-owned profile after an
// explicit owner request. Administrator-authored profiles are immutable from
// this path, and actor/user equality is checked again at the model boundary.
func DeleteAssistantUserProfile(userID, actorID int) error {
	if userID <= 0 || actorID <= 0 {
		return gorm.ErrInvalidData
	}
	if userID != actorID {
		return ErrAssistantProfileOwner
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		if err := lockAssistantOwner(tx, userID); err != nil {
			return err
		}
		var profile AssistantUserProfile
		result := lockForUpdate(tx).Where("user_id = ?", userID).Limit(1).Find(&profile)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return ErrAssistantProfileMissing
		}
		if profile.Source != AssistantProfileSourceAI {
			return ErrAssistantProfileManaged
		}
		return tx.Delete(&profile).Error
	})
}

// AssistantUserProfileDefault returns an API-safe empty state for users who
// have never received a manual override.
func AssistantUserProfileDefault(userID int) *AssistantUserProfile {
	return &AssistantUserProfile{UserId: userID, Enabled: false}
}

func assistantUserProfileNow() int64 { return time.Now().Unix() }
