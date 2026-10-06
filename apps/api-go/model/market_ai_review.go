package model

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"sort"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/setting"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
)

const (
	ModerationSourceMarketTool    = "market_tool"
	ModerationSourceMarketProduct = "market_product"
)

func IsMarketAIReviewSource(source string) bool {
	return source == ModerationSourceMarketTool || source == ModerationSourceMarketProduct
}

type MarketAIReviewSettings struct {
	ToolMode    string `json:"tool_mode"`
	StoreMode   string `json:"store_mode"`
	ReviewGroup string `json:"review_group"`
	ReviewModel string `json:"review_model"`
}

func (s MarketAIReviewSettings) Mode(source string) string {
	if source == ModerationSourceMarketTool {
		return s.ToolMode
	}
	if source == ModerationSourceMarketProduct {
		return s.StoreMode
	}
	return setting.MarketAIReviewOff
}

func readMarketAIReviewSettings(tx *gorm.DB, values map[string]string) (MarketAIReviewSettings, error) {
	s := MarketAIReviewSettings{ToolMode: setting.MarketAIReviewOff, StoreMode: setting.MarketAIReviewOff, ReviewGroup: setting.DefaultModerationGroup, ReviewModel: setting.DefaultModerationModel}
	if tx == nil {
		return s, errors.New("moderation_settings_unavailable")
	}
	keys := []string{setting.ToolMarketAIReviewModeOptionKey, setting.StoreAIReviewModeOptionKey, setting.ModerationGroupOptionKey, setting.ModerationModelOptionKey}
	var rows []Option
	if err := tx.Where("key IN ?", keys).Find(&rows).Error; err != nil {
		return s, errors.New("moderation_settings_unavailable")
	}
	merged := map[string]string{}
	for _, row := range rows {
		merged[row.Key] = row.Value
	}
	for _, key := range keys {
		if value, exists := values[key]; exists {
			merged[key] = value
		}
	}
	for key, value := range merged {
		switch key {
		case setting.ToolMarketAIReviewModeOptionKey:
			s.ToolMode = value
		case setting.StoreAIReviewModeOptionKey:
			s.StoreMode = value
		case setting.ModerationGroupOptionKey:
			s.ReviewGroup = value
		case setting.ModerationModelOptionKey:
			s.ReviewModel = value
		}
	}
	if setting.ValidateMarketAIReviewMode(s.ToolMode) != nil || setting.ValidateMarketAIReviewMode(s.StoreMode) != nil || !setting.IsModerationModel(s.ReviewModel) || s.ReviewGroup == "" || s.ReviewGroup == "*" || strings.TrimSpace(s.ReviewGroup) != s.ReviewGroup || len(s.ReviewGroup) > 64 {
		return s, errors.New("moderation_settings_unavailable")
	}
	return s, nil
}

func ReadMarketAIReviewSettings(ctx context.Context) (MarketAIReviewSettings, error) {
	if DB == nil {
		return MarketAIReviewSettings{}, errors.New("moderation_settings_unavailable")
	}
	return readMarketAIReviewSettings(DB.WithContext(ctx), nil)
}

// The first row is shared with existing routing writers. Market settings are
// independent of chat enablement, policy, fines and assistant configuration.
func lockMarketAIReviewSettings(tx *gorm.DB) (MarketAIReviewSettings, error) {
	for _, row := range []Option{{Key: setting.ModerationEnabledOptionKey, Value: "false"}, {Key: setting.ToolMarketAIReviewModeOptionKey, Value: setting.MarketAIReviewOff}} {
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error; err != nil {
			return MarketAIReviewSettings{}, err
		}
		if err := lockForUpdate(tx).Where("key = ?", row.Key).First(&row).Error; err != nil {
			return MarketAIReviewSettings{}, err
		}
	}
	return readMarketAIReviewSettings(tx, nil)
}

func lockMarketAIReviewOptions(tx *gorm.DB, values map[string]string) error {
	changed := false
	for key := range values {
		if setting.IsMarketAIReviewOption(key) {
			changed = true
		}
	}
	if !changed {
		return nil
	}
	if _, err := lockMarketAIReviewSettings(tx); err != nil {
		return err
	}
	s, err := readMarketAIReviewSettings(tx, values)
	if err != nil {
		return err
	}
	if s.ToolMode != setting.MarketAIReviewOff || s.StoreMode != setting.MarketAIReviewOff {
		return validateModerationRoute(tx, s.ReviewGroup, s.ReviewModel)
	}
	return nil
}

// These exact public fields are the entire provider input. Inventory,
// credentials, schema defaults, order, pickup, gateway and account data never
// participate in that input.
type marketReviewText struct {
	Title       string             `json:"title"`
	Description string             `json:"description"`
	Tools       []marketReviewName `json:"tools,omitempty"`
	Links       []marketReviewName `json:"links,omitempty"`
}
type marketReviewName struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

func marketReviewContent(tx *gorm.DB, source, target, version string) (string, string, error) {
	var text marketReviewText
	if source == ModerationSourceMarketTool {
		var v ToolMarketVersion
		if err := tx.Where("id = ? AND service_id = ?", version, target).First(&v).Error; err != nil {
			return "", "", err
		}
		text.Title, text.Description = v.Name, v.Description
		var tools []ToolMarketToolVersion
		if err := tx.Select("name, description").Where("version_id = ?", version).Order("name ASC").Find(&tools).Error; err != nil {
			return "", "", err
		}
		for _, t := range tools {
			text.Tools = append(text.Tools, marketReviewName{t.Name, t.Description})
		}
	} else if source == ModerationSourceMarketProduct {
		var p MerchantStoreProduct
		if err := tx.Where("id = ?", target).First(&p).Error; err != nil {
			return "", "", err
		}
		text.Title, text.Description = p.Title, p.Description
		for _, link := range p.Links {
			text.Links = append(text.Links, marketReviewName{link.Title, link.Description})
		}
	} else {
		return "", "", ErrModerationJobInvalid
	}
	data, err := json.Marshal(text)
	if err != nil {
		return "", "", err
	}
	digest := sha256.Sum256(data)
	return string(data), hex.EncodeToString(digest[:]), nil
}

func invalidateMarketAIReview(tx *gorm.DB, source, target, token, reason string) error {
	q := tx.Session(&gorm.Session{Logger: logger.Discard}).Model(&ModerationJob{}).Where("source = ? AND target_id = ?", source, target)
	if token != "" {
		q = q.Where("request_id = ?", token)
	}
	now := common.GetTimestamp()
	if err := q.Where("status IN ?", []string{ModerationJobPending, ModerationJobRunning}).Updates(map[string]any{"status": ModerationJobCancelled, "market_outcome": "stale", "error_message": reason, "payload": "", "lease_owner": "", "lease_until": 0, "updated_at": now, "completed_at": now}).Error; err != nil {
		return err
	}
	if token != "" {
		outcome := "stale"
		if reason == "market_review_manual_override" {
			outcome = "overridden"
		}
		return tx.Model(&ModerationJob{}).Where("source = ? AND target_id = ? AND request_id = ? AND status = ?", source, target, token, ModerationJobCompleted).Update("market_outcome", outcome).Error
	}
	return nil
}

func marketAIReviewApplied(tx *gorm.DB, source, target, token string) bool {
	if token == "" {
		return false
	}
	var count int64
	return tx.Model(&ModerationJob{}).Where("source = ? AND target_id = ? AND request_id = ? AND status = ? AND market_outcome IN ?", source, target, token, ModerationJobCompleted, []string{"approved", "rejected"}).Count(&count).Error == nil && count == 1
}

// Called within submission's transaction. Only a bounded durable INSERT is
// added to submission; upstream I/O happens later in the existing worker.
func queueMarketAIReview(tx *gorm.DB, source, target, version, token string, userID int, nonpublic bool) error {
	s, settingsErr := readMarketAIReviewSettings(tx, nil)
	if settingsErr == nil && s.Mode(source) == setting.MarketAIReviewOff {
		return nil
	}
	text, digest, err := marketReviewContent(tx, source, target, version)
	if err != nil {
		return err
	}
	var user User
	if err = tx.Select([]string{"id", "group", "status"}).Where("id = ?", userID).First(&user).Error; err != nil {
		return err
	}
	if user.Status != common.UserStatusEnabled {
		return ErrModerationJobInvalid
	}
	now := common.GetTimestamp()
	key := sha256.Sum256([]byte(source + ":" + target + ":" + token + ":" + digest))
	j := ModerationJob{EventKey: hex.EncodeToString(key[:]), UserID: userID, Source: source, RequestID: token, TargetID: target, TargetVersion: version, Group: user.Group, ReviewGroup: s.ReviewGroup, ReviewModel: s.ReviewModel, InputDigest: digest, Payload: RedactModerationContent(text), CapturedMode: s.Mode(source), CapturedCategoryFinesJSON: "{}", CapturedAmountCurrency: setting.ModerationAmountCurrencyUSD, Status: ModerationJobPending, NextAttemptAt: now, ProviderCallsJSON: "[]", CategoriesJSON: "[]", CategoryScoresJSON: "{}", FeeStatus: "none", CreatedAt: now, UpdatedAt: now}
	reason := ""
	if settingsErr != nil {
		reason = "moderation_settings_unavailable"
	}
	if nonpublic {
		reason = "market_review_nonpublic_listing"
	}
	if len(text) > ModerationMaxPayloadBytes || len(j.Payload) > ModerationMaxPayloadBytes {
		j.InputTruncated = true
		reason = "market_review_input_too_large"
	}
	var pending int64
	if err = tx.Model(&ModerationJob{}).Where("status IN ?", []string{ModerationJobPending, ModerationJobRunning}).Count(&pending).Error; err != nil {
		return err
	}
	if pending >= ModerationPendingMax {
		reason = "market_review_queue_full"
	}
	if reason != "" {
		j.Status, j.MarketOutcome, j.ErrorMessage, j.Payload, j.CompletedAt = ModerationJobFailed, "manual_required", reason, "", now
	}
	return tx.Session(&gorm.Session{Logger: logger.Discard}).Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "event_key"}}, DoNothing: true}).Create(&j).Error
}

// Lock order: settings -> user -> target service/product -> version -> job.
// Manual review/submission already own user and target before invalidating job.
func marketAIReviewTarget(tx *gorm.DB, j *ModerationJob, locked bool) (*ToolMarketService, *ToolMarketVersion, *MerchantStoreProduct, bool, error) {
	q := tx
	if locked {
		q = lockForUpdate(tx)
	}
	if j.Source == ModerationSourceMarketTool {
		var s ToolMarketService
		var v ToolMarketVersion
		if err := q.Where("id = ?", j.TargetID).First(&s).Error; err != nil {
			return nil, nil, nil, false, err
		}
		if err := q.Where("id = ? AND service_id = ?", j.TargetVersion, j.TargetID).First(&v).Error; err != nil {
			return nil, nil, nil, false, err
		}
		ok := s.OwnerID == j.UserID && s.DraftVersionID == v.ID && v.Status == "pending" && v.AIReviewToken == j.RequestID && v.Visibility == "public"
		return &s, &v, nil, ok, nil
	}
	if j.Source == ModerationSourceMarketProduct {
		var p MerchantStoreProduct
		if err := q.Where("id = ?", j.TargetID).First(&p).Error; err != nil {
			return nil, nil, nil, false, err
		}
		return nil, nil, &p, p.SellerID == j.UserID && p.Status == "pending" && p.AIReviewToken == j.RequestID, nil
	}
	return nil, nil, nil, false, ErrModerationJobInvalid
}

func MarketAIReviewPreflight(ctx context.Context, j *ModerationJob) error {
	if j == nil || !IsMarketAIReviewSource(j.Source) {
		return ErrModerationJobInvalid
	}
	db := moderationDB(ctx)
	s, err := readMarketAIReviewSettings(db, nil)
	if err != nil {
		return err
	}
	if s.Mode(j.Source) == setting.MarketAIReviewOff || s.ReviewGroup != j.ReviewGroup || s.ReviewModel != j.ReviewModel {
		return errors.New("market_review_disabled")
	}
	var count int64
	if err = db.Model(&ModerationJob{}).Where("id = ? AND status = ? AND lease_owner = ? AND lease_until > ?", j.ID, ModerationJobRunning, j.LeaseOwner, common.GetTimestamp()).Count(&count).Error; err != nil {
		return err
	}
	if count != 1 {
		return ErrModerationLeaseLost
	}
	var u User
	if err = db.Select("id, status").Where("id = ?", j.UserID).First(&u).Error; err != nil {
		return err
	}
	if u.Status != common.UserStatusEnabled {
		return errors.New("market_review_stale")
	}
	_, _, _, current, err := marketAIReviewTarget(db, j, false)
	if err != nil {
		return err
	}
	if !current {
		return errors.New("market_review_stale")
	}
	_, digest, err := marketReviewContent(db, j.Source, j.TargetID, j.TargetVersion)
	if err != nil {
		return err
	}
	if digest != j.InputDigest {
		return errors.New("market_review_stale")
	}
	return nil
}

type MarketAIReviewCompletion struct {
	Flagged                   bool
	Categories                []string
	Scores                    map[string]float64
	ResponseModel             string
	TechnicalValidationPassed bool
	Now                       int64
}

func CompleteMarketAIReview(ctx context.Context, id int64, owner string, c MarketAIReviewCompletion) error {
	if DB == nil || id <= 0 || owner == "" || !setting.IsModerationModel(c.ResponseModel) || len(c.Categories) > 32 || len(c.Scores) > 32 {
		return ErrModerationJobInvalid
	}
	if c.Now <= 0 {
		c.Now = common.GetTimestamp()
	}
	cats := []string{}
	seen := map[string]bool{}
	for _, cat := range c.Categories {
		if !setting.IsModerationCategory(cat) {
			return ErrModerationJobInvalid
		}
		if !seen[cat] {
			cats = append(cats, cat)
			seen[cat] = true
		}
	}
	if c.Flagged && len(cats) == 0 {
		return ErrModerationJobInvalid
	}
	if !c.Flagged {
		cats = []string{}
	}
	for cat, value := range c.Scores {
		if !setting.IsModerationCategory(cat) || math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > 1 {
			return ErrModerationJobInvalid
		}
	}
	sort.Strings(cats)
	encodedCats, _ := json.Marshal(cats)
	encodedScores, err := json.Marshal(c.Scores)
	if err != nil {
		return err
	}
	return moderationDB(ctx).Transaction(func(tx *gorm.DB) error {
		s, err := lockMarketAIReviewSettings(tx)
		if err != nil {
			return err
		}
		var identity ModerationJob
		if err = tx.Where("id = ?", id).First(&identity).Error; err != nil {
			return err
		}
		if !IsMarketAIReviewSource(identity.Source) {
			return ErrModerationJobInvalid
		}
		if identity.Status == ModerationJobCompleted {
			return nil
		}
		var user User
		if err = lockForUpdate(tx).Select("id, status").Where("id = ?", identity.UserID).First(&user).Error; err != nil {
			return err
		}
		service, version, product, current, err := marketAIReviewTarget(tx, &identity, true)
		if err != nil {
			return err
		}
		var j ModerationJob
		if err = lockForUpdate(tx).Where("id = ?", id).First(&j).Error; err != nil {
			return err
		}
		if j.Status == ModerationJobCompleted {
			return nil
		}
		now := common.GetTimestamp()
		if c.Now > now {
			now = c.Now
		}
		if j.Status != ModerationJobRunning || j.LeaseOwner != owner || j.LeaseUntil <= now {
			return ErrModerationLeaseLost
		}
		values := map[string]any{"status": ModerationJobCompleted, "market_outcome": "reference", "flagged": c.Flagged, "categories_json": string(encodedCats), "category_scores_json": string(encodedScores), "response_model": c.ResponseModel, "payload": "", "lease_owner": "", "lease_until": 0, "updated_at": now, "completed_at": now, "error_message": "", "fee_status": "none", "requested_quota": 0, "charged_quota": 0, "fee_record_id": 0, "review_id": 0}
		_, digest, err := marketReviewContent(tx, j.Source, j.TargetID, j.TargetVersion)
		if err != nil {
			return err
		}
		if !current || user.Status != common.UserStatusEnabled || digest != j.InputDigest || j.InputTruncated {
			values["status"], values["market_outcome"], values["error_message"] = ModerationJobCancelled, "stale", "market_review_stale"
		} else if s.Mode(j.Source) == setting.MarketAIReviewOff || s.ReviewGroup != j.ReviewGroup || s.ReviewModel != j.ReviewModel {
			values["status"], values["market_outcome"], values["error_message"] = ModerationJobCancelled, "stale", "market_review_disabled"
		} else if j.CapturedMode == setting.MarketAIReviewAuto && s.Mode(j.Source) == setting.MarketAIReviewAuto {
			if version != nil && !c.Flagged {
				valid := c.TechnicalValidationPassed && version.ValidationDigest != "" && version.ValidationDigest == version.Digest
				var tools []ToolMarketToolVersion
				if err = tx.Where("version_id = ?", version.ID).Find(&tools).Error; err != nil {
					return err
				}
				for _, tool := range tools {
					if ValidateToolMarketMetering(service.ID, *version, tool) != nil {
						valid = false
					}
				}
				if !valid {
					values["market_outcome"], values["error_message"] = "manual_required", "market_review_validation_required"
					return tx.Model(&j).Updates(values).Error
				}
			}
			outcome := "approved"
			status := "published"
			if c.Flagged {
				outcome, status = "rejected", "rejected"
			}
			values["market_outcome"] = outcome
			if product != nil {
				product.Status, product.ReviewedBy, product.ReviewedAt, product.UpdatedAt = status, 0, now, now
				product.ReviewNote = "AI listing text review: " + outcome
				if err = tx.Save(product).Error; err != nil {
					return err
				}
				if err = storeEvent(tx, 0, product.ID, "ai_review_"+outcome); err != nil {
					return err
				}
			} else {
				version.Status, version.ReviewedBy, version.ReviewNote = status, 0, "AI listing text review: "+outcome
				if !c.Flagged {
					version.PublishedAt = now
					service.LiveVersionID, service.DraftVersionID = version.ID, ""
					if service.Status != "paused" && service.Status != "suspended" {
						service.Status = "published"
					}
					service.UpdatedAt = now
					if err = tx.Save(service).Error; err != nil {
						return err
					}
				}
				if err = tx.Save(version).Error; err != nil {
					return err
				}
				if err = marketEvent(tx, 0, service.ID, "version.ai_review", map[string]any{"version_id": version.ID, "job_id": j.ID, "outcome": outcome}); err != nil {
					return err
				}
			}
		}
		return tx.Model(&j).Updates(values).Error
	})
}

type MarketAIReviewResult struct {
	ID             int64              `json:"id"`
	Source         string             `json:"source"`
	TargetID       string             `json:"target_id"`
	ContentVersion string             `json:"content_version"`
	ContentHash    string             `json:"content_hash"`
	Mode           string             `json:"mode"`
	Status         string             `json:"status"`
	Attempts       int                `json:"attempts"`
	ReviewModel    string             `json:"review_model"`
	ResponseModel  string             `json:"response_model"`
	Flagged        *bool              `json:"flagged"`
	Categories     []string           `json:"categories"`
	CategoryScores map[string]float64 `json:"category_scores"`
	Recommendation *string            `json:"recommendation"`
	Outcome        *string            `json:"outcome"`
	Applied        bool               `json:"applied"`
	ErrorCode      *string            `json:"error_code"`
	CreatedAt      int64              `json:"created_at"`
	UpdatedAt      int64              `json:"updated_at"`
	CompletedAt    int64              `json:"completed_at"`
	CheckedAt      int64              `json:"checked_at"`
	Coverage       string             `json:"coverage"`
}

func marketAIReviewResult(j ModerationJob) MarketAIReviewResult {
	r := MarketAIReviewResult{ID: j.ID, Source: j.Source, TargetID: j.TargetID, ContentVersion: j.TargetVersion, ContentHash: j.InputDigest, Mode: j.CapturedMode, Status: j.Status, Attempts: j.Attempts, ReviewModel: j.ReviewModel, ResponseModel: j.ResponseModel, Applied: j.MarketOutcome == "approved" || j.MarketOutcome == "rejected", CreatedAt: j.CreatedAt, UpdatedAt: j.UpdatedAt, CompletedAt: j.CompletedAt, CheckedAt: j.CompletedAt, Coverage: "public_listing_text"}
	if r.ContentVersion == "" {
		r.ContentVersion = j.RequestID
	}
	if j.MarketOutcome != "" {
		outcome := j.MarketOutcome
		r.Outcome = &outcome
	}
	if j.ResponseModel != "" {
		flagged := j.Flagged
		r.Flagged = &flagged
		r.Categories = j.Categories()
		_ = json.Unmarshal([]byte(j.CategoryScoresJSON), &r.CategoryScores)
		recommendation := "approve"
		if flagged {
			recommendation = "reject"
		}
		r.Recommendation = &recommendation
	}
	if j.ErrorMessage != "" {
		code := marketAIReviewErrorCode(j.ErrorMessage)
		r.ErrorCode = &code
	}
	return r
}

func marketAIReviewErrorCode(code string) string {
	switch code {
	case "market_review_disabled", "market_review_stale", "market_review_resubmitted", "market_review_manual_override", "market_review_input_too_large", "market_review_nonpublic_listing", "market_review_validation_required", "market_review_queue_full", "moderation_settings_unavailable", "moderation_route_unavailable", "moderation_provider_unavailable", "moderation_provider_rejected", "moderation_response_invalid", "moderation_trace_unavailable":
		return code
	case "review worker lease expired":
		return "market_review_lease_expired"
	default:
		return "market_review_unavailable"
	}
}

func ListMarketAIReviews(ctx context.Context, actor int, source, target, version string) ([]MarketAIReviewResult, error) {
	if DB == nil || !IsMarketAIReviewSource(source) {
		return nil, ErrModerationJobInvalid
	}
	db := moderationDB(ctx)
	var user User
	if err := db.Select("id, role, status").Where("id = ?", actor).First(&user).Error; err != nil {
		return nil, err
	}
	if user.Status != common.UserStatusEnabled {
		return nil, ErrToolMarketDenied
	}
	owner := 0
	if source == ModerationSourceMarketTool {
		var s ToolMarketService
		if err := db.Where("id = ?", target).First(&s).Error; err != nil {
			return nil, err
		}
		owner = s.OwnerID
		if version == "" {
			version = s.DraftVersionID
			if version == "" {
				version = s.LiveVersionID
			}
		}
		if version != "" {
			var count int64
			if err := db.Model(&ToolMarketVersion{}).Where("id = ? AND service_id = ?", version, target).Count(&count).Error; err != nil {
				return nil, err
			}
			if count != 1 {
				return nil, ErrToolMarketInput
			}
		}
	} else {
		var p MerchantStoreProduct
		if err := db.Where("id = ?", target).First(&p).Error; err != nil {
			return nil, err
		}
		owner = p.SellerID
	}
	if actor != owner && user.Role < common.RoleAdminUser {
		return nil, ErrToolMarketDenied
	}
	q := db.Where("source = ? AND target_id = ?", source, target)
	if source == ModerationSourceMarketTool && version != "" {
		q = q.Where("target_version = ?", version)
	}
	var jobs []ModerationJob
	if err := q.Order("id DESC").Limit(20).Find(&jobs).Error; err != nil {
		return nil, err
	}
	rows := make([]MarketAIReviewResult, 0, len(jobs))
	for _, j := range jobs {
		rows = append(rows, marketAIReviewResult(j))
	}
	return rows, nil
}
