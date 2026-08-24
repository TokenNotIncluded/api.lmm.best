package controller

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

const (
	assistantAdminMaxModelSyncItems = 64
	assistantAdminMaxModelNameRunes = 200
)

// assistantAdminModelSnapshot is the bounded, non-secret subset of upstream
// metadata that the assistant may stage. It intentionally excludes endpoints
// and any provider configuration.
type assistantAdminModelSnapshot struct {
	ModelName   string `json:"model_id"`
	Description string `json:"description,omitempty"`
	Icon        string `json:"icon,omitempty"`
	Tags        string `json:"tags,omitempty"`
	VendorName  string `json:"vendor"`
	NameRule    int    `json:"name_rule"`
	Status      int    `json:"status"`
}

type assistantAdminVendorSnapshot struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Icon        string `json:"icon,omitempty"`
	Status      int    `json:"status"`
}

// assistantAdminModelSyncChange is persisted in a short-lived, session-bound
// auth flow. Apply rechecks that every model is still missing and writes all
// rows in one transaction.
type assistantAdminModelSyncChange struct {
	Locale          string                         `json:"locale"`
	Models          []assistantAdminModelSnapshot  `json:"models"`
	Vendors         []assistantAdminVendorSnapshot `json:"vendors,omitempty"`
	ExpectedMissing []string                       `json:"expected_missing"`
	SourceDigest    string                         `json:"source_digest"`
}

func boundedAssistantModelText(value string, maxRunes int) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	runes := []rune(value)
	if len(runes) > maxRunes {
		runes = runes[:maxRunes]
	}
	return strings.TrimSpace(string(runes))
}

func assistantModelSyncLocale(value string) (string, bool) {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case "", "default":
		return "", true
	case "en":
		return "en", true
	case "zh", "zh-cn":
		return "zh-CN", true
	case "zh-tw":
		return "zh-TW", true
	case "ja":
		return "ja", true
	default:
		return "", false
	}
}

func assistantModelSyncContext(c *gin.Context) (context.Context, context.CancelFunc) {
	parent := context.Background()
	if c != nil && c.Request != nil {
		parent = c.Request.Context()
	}
	seconds := common.GetEnvOrDefault("SYNC_HTTP_TIMEOUT_SECONDS", 15)
	if seconds < 1 {
		seconds = 15
	}
	return context.WithTimeout(parent, time.Duration(seconds)*time.Second)
}

func assistantModelSyncSource(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Host == "" {
		return "configured upstream"
	}
	parsed.User = nil
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String()
}

func assistantModelSyncFetchErrorDetail(err error) string {
	if err == nil {
		return "unknown error"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "request timed out"
	}
	if errors.Is(err, context.Canceled) {
		return "request canceled"
	}
	var requestErr *url.Error
	if errors.As(err, &requestErr) && requestErr.Err != nil {
		err = requestErr.Err
	}
	detail := boundedAssistantModelText(err.Error(), 256)
	if detail == "" {
		return "unknown error"
	}
	return detail
}

func assistantModelSyncDigest(change assistantAdminModelSyncChange) string {
	payload := struct {
		Locale  string                         `json:"locale"`
		Models  []assistantAdminModelSnapshot  `json:"models"`
		Vendors []assistantAdminVendorSnapshot `json:"vendors"`
	}{change.Locale, change.Models, change.Vendors}
	encoded, _ := json.Marshal(payload)
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

func assistantAdminModelSyncPlan(c *gin.Context, requested []string, locale string) (assistantAdminModelSyncChange, []string, error) {
	locale, ok := assistantModelSyncLocale(locale)
	if !ok {
		return assistantAdminModelSyncChange{}, nil, errors.New("locale must be empty, en, zh-CN, zh-TW, or ja")
	}

	missing, err := model.GetMissingModels()
	if err != nil {
		return assistantAdminModelSyncChange{}, nil, errors.New("missing model inventory is unavailable")
	}
	sort.Strings(missing)
	missingSet := make(map[string]struct{}, len(missing))
	for _, name := range missing {
		missingSet[name] = struct{}{}
	}

	target := append([]string(nil), requested...)
	if len(target) == 0 {
		if len(missing) > assistantAdminMaxModelSyncItems {
			return assistantAdminModelSyncChange{}, nil, fmt.Errorf("%d models are missing; provide at most %d exact model IDs per confirmation", len(missing), assistantAdminMaxModelSyncItems)
		}
		target = missing
	}
	if len(target) > assistantAdminMaxModelSyncItems {
		return assistantAdminModelSyncChange{}, nil, fmt.Errorf("at most %d exact model IDs may be synchronized at once", assistantAdminMaxModelSyncItems)
	}

	seen := make(map[string]struct{}, len(target))
	normalizedTarget := make([]string, 0, len(target))
	for _, raw := range target {
		name := boundedAssistantModelText(raw, assistantAdminMaxModelNameRunes)
		if name == "" {
			return assistantAdminModelSyncChange{}, nil, errors.New("model IDs must not be empty")
		}
		if _, duplicate := seen[name]; duplicate {
			continue
		}
		seen[name] = struct{}{}
		if _, isMissing := missingSet[name]; !isMissing {
			return assistantAdminModelSyncChange{}, nil, fmt.Errorf("model %q is no longer missing; refresh the inventory first", name)
		}
		normalizedTarget = append(normalizedTarget, name)
	}
	sort.Strings(normalizedTarget)
	if len(normalizedTarget) == 0 {
		return assistantAdminModelSyncChange{}, nil, errors.New("no missing models were selected")
	}

	ctx, cancel := assistantModelSyncContext(c)
	defer cancel()
	modelsURL, vendorsURL := getUpstreamURLs(locale)
	var modelsEnvelope upstreamEnvelope[upstreamModel]
	var vendorsEnvelope upstreamEnvelope[upstreamVendor]
	if err := fetchJSON(ctx, modelsURL, &modelsEnvelope); err != nil {
		return assistantAdminModelSyncChange{}, nil, fmt.Errorf(
			"upstream model catalog is unavailable (source %s): %s",
			assistantModelSyncSource(modelsURL),
			assistantModelSyncFetchErrorDetail(err),
		)
	}
	// Vendor metadata is optional for the existing sync endpoint, but fetching
	// it here lets the confirmation show exactly which vendor rows will be new.
	_ = fetchJSON(ctx, vendorsURL, &vendorsEnvelope)

	upstreamByName := make(map[string]upstreamModel, len(modelsEnvelope.Data))
	for _, item := range modelsEnvelope.Data {
		name := boundedAssistantModelText(item.ModelName, assistantAdminMaxModelNameRunes)
		if name != "" {
			upstreamByName[name] = item
		}
	}
	vendorByName := make(map[string]upstreamVendor, len(vendorsEnvelope.Data))
	for _, item := range vendorsEnvelope.Data {
		name := boundedAssistantModelText(item.Name, 128)
		if name != "" {
			vendorByName[name] = item
		}
	}

	change := assistantAdminModelSyncChange{Locale: locale, ExpectedMissing: normalizedTarget}
	skipped := make([]string, 0)
	vendorNames := make(map[string]struct{})
	for _, name := range normalizedTarget {
		item, found := upstreamByName[name]
		if !found {
			skipped = append(skipped, name)
			continue
		}
		snapshot := assistantAdminModelSnapshot{
			ModelName:   name,
			Description: boundedAssistantModelText(item.Description, 1024),
			Icon:        boundedAssistantModelText(item.Icon, 128),
			Tags:        boundedAssistantModelText(item.Tags, 255),
			VendorName:  boundedAssistantModelText(item.VendorName, 128),
			NameRule:    item.NameRule,
			Status:      chooseStatus(item.Status, 1),
		}
		change.Models = append(change.Models, snapshot)
		if snapshot.VendorName != "" {
			vendorNames[snapshot.VendorName] = struct{}{}
		}
	}
	if len(change.Models) == 0 {
		return assistantAdminModelSyncChange{}, skipped, errors.New("none of the selected missing models exists in the upstream catalog")
	}
	change.ExpectedMissing = make([]string, 0, len(change.Models))
	for _, item := range change.Models {
		change.ExpectedMissing = append(change.ExpectedMissing, item.ModelName)
	}
	for vendorName := range vendorNames {
		item := vendorByName[vendorName]
		change.Vendors = append(change.Vendors, assistantAdminVendorSnapshot{
			Name:        vendorName,
			Description: boundedAssistantModelText(item.Description, 1024),
			Icon:        boundedAssistantModelText(item.Icon, 128),
			Status:      chooseStatus(item.Status, 1),
		})
	}
	sort.Slice(change.Models, func(i, j int) bool { return change.Models[i].ModelName < change.Models[j].ModelName })
	sort.Slice(change.Vendors, func(i, j int) bool { return change.Vendors[i].Name < change.Vendors[j].Name })
	change.SourceDigest = assistantModelSyncDigest(change)
	return change, skipped, nil
}

func assistantAdminModelSyncPreview(change assistantAdminModelSyncChange) []map[string]any {
	preview := make([]map[string]any, 0, len(change.Models))
	for _, item := range change.Models {
		preview = append(preview, map[string]any{
			"model_id":   item.ModelName,
			"description": item.Description,
			"icon":        item.Icon,
			"tags":        item.Tags,
			"vendor":      item.VendorName,
			"name_rule":   item.NameRule,
			"status":      item.Status,
		})
	}
	return preview
}

func executeAssistantAdminModelInventoryTool(userID int) map[string]any {
	if _, err := assistantAdminUser(userID); err != nil {
		return map[string]any{"ok": false, "error": err.Error()}
	}
	missing, err := model.GetMissingModels()
	if err != nil {
		return map[string]any{"ok": false, "error": "missing model inventory is unavailable"}
	}
	pricing := getPricingCache()
	if pricing == nil {
		return map[string]any{
			"ok":                  false,
			"status":              "pricing_cache_unready",
			"error":               "model inventory is temporarily unavailable while the pricing cache warms",
			"pricing_cache_ready": false,
			"next_step":           "Retry the model inventory after the live pricing cache is ready; do not infer that no models are enabled.",
		}
	}
	modelIDs := make([]string, 0, len(pricing))
	seen := make(map[string]struct{}, len(pricing))
	for _, item := range pricing {
		name := strings.TrimSpace(item.ModelName)
		if name == "" {
			continue
		}
		if _, exists := seen[name]; exists {
			continue
		}
		seen[name] = struct{}{}
		modelIDs = append(modelIDs, name)
	}
	sort.Strings(modelIDs)
	if len(modelIDs) == 0 {
		return map[string]any{
			"ok":                  false,
			"status":              "pricing_cache_empty",
			"error":               "model inventory is temporarily unavailable because the pricing cache contains no usable model IDs",
			"pricing_cache_ready": false,
			"next_step":           "Check the live pricing/model configuration and retry; do not infer that no models are enabled.",
		}
	}
	truncated := len(missing) > assistantAdminMaxModelSyncItems
	if truncated {
		missing = missing[:assistantAdminMaxModelSyncItems]
	}
	return map[string]any{
		"ok": true, "model_ids": modelIDs, "missing_model_ids": missing,
		"missing_truncated": truncated, "groups": assistantAdminConfiguredGroups(),
		"upstream_availability_checked": false,
		"next_step": "These IDs are missing from local metadata only; do not claim that the upstream catalog contains them until prepare_admin_model_sync returns a preview. Then wait for the administrator to confirm it.",
	}
}

func executeAssistantAdminModelSyncTool(c *gin.Context, userID int, input map[string]any) map[string]any {
	if _, err := assistantRootUser(userID); err != nil {
		return map[string]any{"ok": false, "error": err.Error()}
	}
	requested, ok := stringList(input, "model_ids", assistantAdminMaxModelSyncItems)
	if !ok {
		return map[string]any{"ok": false, "error": "model_ids must be an array of exact model IDs"}
	}
	change, skipped, err := assistantAdminModelSyncPlan(c, requested, inputString(input, "locale"))
	if err != nil {
		if skipped == nil {
			skipped = []string{}
		}
		return map[string]any{"ok": false, "status": "model_sync_unavailable", "error": err.Error(), "skipped_model_ids": skipped}
	}
	token, err := createAssistantAdminFlow(c, userID, assistantAdminChangePayload{Kind: assistantAdminModelSyncChangeKind, ModelSync: &change})
	if err != nil {
		return map[string]any{"ok": false, "error": "administrator browser session is required to prepare a model sync"}
	}
	action := map[string]any{
		"type":                  "admin_model_sync",
		"confirmation_token":    token,
		"requires_confirmation": true,
		"expires_in_seconds":    int(assistantAdminChangeLifetime / time.Second),
		"models":                assistantAdminModelSyncPreview(change),
		"vendors":               change.Vendors,
		"locale":                change.Locale,
		"source_digest":         change.SourceDigest,
	}
	c.Set(assistantClientActionKey, action)
	return map[string]any{
		"ok": true, "status": "confirmation_required", "action": "admin_model_sync",
		"models": assistantAdminModelSyncPreview(change), "vendors": change.Vendors,
		"skipped_model_ids": skipped,
		"next_step":         "Show the exact models and vendors, then ask the administrator to confirm in the UI.",
	}
}

func applyAssistantAdminModelSync(change assistantAdminModelSyncChange) error {
	if len(change.Models) == 0 || len(change.Models) > assistantAdminMaxModelSyncItems {
		return errors.New("administrator model sync preview is empty or too large")
	}
	if len(change.ExpectedMissing) != len(change.Models) {
		return errors.New("administrator model sync preview is inconsistent; prepare it again")
	}
	currentMissing, err := model.GetMissingModels()
	if err != nil {
		return errors.New("missing model inventory is unavailable")
	}
	missingSet := make(map[string]struct{}, len(currentMissing))
	for _, name := range currentMissing {
		missingSet[name] = struct{}{}
	}
	for _, expected := range change.ExpectedMissing {
		if _, ok := missingSet[expected]; !ok {
			return errors.New("a selected model is no longer missing; prepare the sync again")
		}
	}

	return model.DB.Transaction(func(tx *gorm.DB) error {
		vendorIDs := make(map[string]int, len(change.Vendors))
		for _, vendor := range change.Vendors {
			name := boundedAssistantModelText(vendor.Name, 128)
			if name == "" {
				continue
			}
			var existing model.Vendor
			err := tx.Where("name = ?", name).First(&existing).Error
			if err == nil {
				vendorIDs[name] = existing.Id
				continue
			}
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			now := common.GetTimestamp()
			created := model.Vendor{
				Name: name, Description: boundedAssistantModelText(vendor.Description, 1024),
				Icon: boundedAssistantModelText(vendor.Icon, 128), Status: chooseStatus(vendor.Status, 1),
				CreatedTime: now, UpdatedTime: now,
			}
			if err := tx.Create(&created).Error; err != nil {
				return err
			}
			vendorIDs[name] = created.Id
		}
		for _, item := range change.Models {
			name := boundedAssistantModelText(item.ModelName, assistantAdminMaxModelNameRunes)
			if name == "" {
				return errors.New("administrator model sync contains an empty model ID")
			}
			if _, ok := missingSet[name]; !ok {
				return errors.New("a selected model is no longer missing; prepare the sync again")
			}
			var existing model.Model
			if err := tx.Where("model_name = ?", name).First(&existing).Error; err == nil {
				return errors.New("a selected model already exists; prepare the sync again")
			} else if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			now := common.GetTimestamp()
			record := model.Model{
				ModelName: name, Description: boundedAssistantModelText(item.Description, 1024),
				Icon: boundedAssistantModelText(item.Icon, 128), Tags: boundedAssistantModelText(item.Tags, 255),
				VendorID: vendorIDs[boundedAssistantModelText(item.VendorName, 128)],
				Status:   chooseStatus(item.Status, 1), SyncOfficial: 1,
				CreatedTime: now, UpdatedTime: now, NameRule: item.NameRule,
			}
			if err := tx.Create(&record).Error; err != nil {
				return err
			}
		}
		return nil
	})
}
