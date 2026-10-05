package model

import (
	"encoding/json"
	"strings"
	"unicode/utf8"

	"gorm.io/gorm"
)

type ToolMarketDetail struct {
	Service      ToolMarketService       `json:"service"`
	Version      ToolMarketVersion       `json:"version"`
	Tools        []ToolMarketToolVersion `json:"tools"`
	Pricing      string                  `json:"pricing"`
	Validated    bool                    `json:"validated"`
	AllowedUsers []int                   `json:"allowed_users,omitempty"`
}

type ToolMarketListItem struct {
	ID            string `json:"id"`
	OwnerID       int    `json:"owner_id"`
	VersionID     string `json:"version_id"`
	Name          string `json:"name"`
	Description   string `json:"description"`
	ExecutionType string `json:"execution_type"`
	PublishedAt   int64  `json:"published_at"`
	ToolCount     int    `json:"tool_count"`
	MinPriceQuota int    `json:"min_price_quota"`
	MaxPriceQuota int    `json:"max_price_quota"`
	MeteredTools  int    `json:"metered_tools"`
}

const marketListColumns = `s.id, s.owner_id, v.id AS version_id, v.name, v.description, v.execution_type, v.published_at,
	(SELECT COUNT(*) FROM tool_market_tool_versions tv WHERE tv.version_id = v.id) AS tool_count,
	(SELECT COUNT(*) FROM tool_market_tool_versions tv WHERE tv.version_id = v.id AND tv.billing_mode IN ('input_tokens', 'metered')) AS metered_tools,
	COALESCE((SELECT MIN(tv.price_quota) FROM tool_market_tool_versions tv WHERE tv.version_id = v.id), 0) AS min_price_quota,
	COALESCE((SELECT MAX(tv.price_quota) FROM tool_market_tool_versions tv WHERE tv.version_id = v.id), 0) AS max_price_quota`

func marketVisibleQuery(userID int) *gorm.DB {
	return DB.Table("tool_market_services AS s").
		Joins("JOIN tool_market_versions AS v ON v.id = s.live_version_id AND v.service_id = s.id").
		Where("s.status = ? AND v.status = ?", "published", "published").
		Where("v.visibility = 'public' OR s.owner_id = ? OR (v.visibility = 'shared' AND EXISTS (SELECT 1 FROM tool_market_accesses a WHERE a.version_id = v.id AND a.user_id = ?))", userID, userID)
}

func ListToolMarket(userID int, search, executionType string, offset, limit int) ([]ToolMarketListItem, error) {
	search = strings.TrimSpace(search)
	// The UI accepts 120 characters, not 120 UTF-8 bytes. Keep non-ASCII
	// searches usable without relaxing pagination or accepting malformed text.
	if userID < 0 || !utf8.ValidString(search) || utf8.RuneCountInString(search) > 120 || offset < 0 || offset > 10000 || limit < 1 || limit > 100 {
		return nil, ErrToolMarketInput
	}
	q := marketVisibleQuery(userID)
	if executionType != "" {
		if executionType != "remote" && executionType != "serverless" && executionType != "builtin" {
			return nil, ErrToolMarketInput
		}
		q = q.Where("v.execution_type = ?", executionType)
	}
	if search != "" {
		// Treat wildcard characters literally, not as a way to bypass filtering.
		search = strings.NewReplacer("!", "!!", "%", "!%", "_", "!_").Replace(strings.ToLower(search))
		pattern := "%" + search + "%"
		q = q.Where("LOWER(v.name) LIKE ? ESCAPE '!' OR LOWER(v.description) LIKE ? ESCAPE '!' OR EXISTS (SELECT 1 FROM tool_market_tool_versions tv WHERE tv.version_id = v.id AND (LOWER(tv.name) LIKE ? ESCAPE '!' OR LOWER(tv.description) LIKE ? ESCAPE '!'))", pattern, pattern, pattern, pattern)
	}
	rows := []ToolMarketListItem{}
	err := q.Select(marketListColumns).Order("CASE WHEN v.execution_type = 'builtin' THEN 0 ELSE 1 END, v.published_at DESC, s.id ASC").Offset(offset).Limit(limit).Scan(&rows).Error
	return rows, err
}

func GetToolMarketDetail(userID int, serviceID string, draft bool) (*ToolMarketDetail, error) {
	var detail ToolMarketDetail
	if draft {
		if userID <= 0 {
			return nil, ErrToolMarketDenied
		}
		if err := DB.First(&detail.Service, "id = ? AND owner_id = ?", serviceID, userID).Error; err != nil {
			return nil, err
		}
		if err := DB.First(&detail.Version, "id = ? AND service_id = ?", detail.Service.DraftVersionID, serviceID).Error; err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(detail.Version.AllowedUsers), &detail.AllowedUsers)
	} else {
		var visible ToolMarketListItem
		if err := marketVisibleQuery(userID).Where("s.id = ?", serviceID).Select("s.id, v.id AS version_id").Take(&visible).Error; err != nil {
			return nil, err
		}
		if err := DB.First(&detail.Service, "id = ?", visible.ID).Error; err != nil {
			return nil, err
		}
		if err := DB.First(&detail.Version, "id = ?", visible.VersionID).Error; err != nil {
			return nil, err
		}
		// Draft identifiers are private author metadata, including on public services.
		detail.Service.DraftVersionID = ""
	}
	detail.Validated = marketBuiltinVersion(detail.Service, detail.Version) || (detail.Version.ValidationDigest != "" && detail.Version.ValidationDigest == detail.Version.Digest)
	if detail.Service.OwnerID == userID {
		_ = json.Unmarshal([]byte(detail.Version.AllowedUsers), &detail.AllowedUsers)
	}
	detail.Tools = []ToolMarketToolVersion{}
	if err := DB.Where("version_id = ?", detail.Version.ID).Order("name, tool_id").Find(&detail.Tools).Error; err != nil {
		return nil, err
	}
	free := 0
	for i, tool := range detail.Tools {
		detail.Tools[i].AvailableMeteringMetrics = ToolMarketMeteringMetrics(detail.Service.ID, detail.Version.Endpoint, tool.Name)
		if tool.PriceQuota == 0 {
			free++
		}
	}
	detail.Pricing = "paid"
	if len(detail.Tools) > 0 && free == len(detail.Tools) {
		detail.Pricing = "free"
	} else if free > 0 {
		detail.Pricing = "partially_free"
	}
	return &detail, nil
}

// Author analytics never reuse the caller's private call view. The transfer
// view contains only accounting facts, never arguments or results.
func ListToolMarketCalls(userID, offset, limit int) ([]ToolMarketCall, error) {
	if userID <= 0 || offset < 0 || offset > 10000 || limit < 1 || limit > 100 {
		return nil, ErrToolMarketInput
	}
	rows := []ToolMarketCall{}
	err := DB.Where("user_id = ?", userID).Order("created_at DESC, id").Offset(offset).Limit(limit).Find(&rows).Error
	return rows, err
}

type ToolMarketIncome struct {
	ID        string `json:"id"`
	CallID    string `json:"call_id"`
	Kind      string `json:"kind"`
	Quota     int    `json:"quota"`
	FeeBPS    int    `json:"fee_bps"`
	CreatedAt int64  `json:"created_at"`
}

func ListToolMarketIncome(userID, offset, limit int) ([]ToolMarketIncome, error) {
	if userID <= 0 || offset < 0 || offset > 10000 || limit < 1 || limit > 100 {
		return nil, ErrToolMarketInput
	}
	rows := []ToolMarketIncome{}
	err := DB.Model(&ToolMarketTransfer{}).Select("id,call_id,kind,quota,fee_bps,created_at").Where("to_user_id = ?", userID).Order("created_at DESC, id").Offset(offset).Limit(limit).Scan(&rows).Error
	return rows, err
}

func ListToolMarketAccountResources(userID int, kind string, offset, limit int) (any, error) {
	if userID <= 0 || offset < 0 || offset > 10000 || limit < 1 || limit > 100 {
		return nil, ErrToolMarketInput
	}
	q := DB.Offset(offset).Limit(limit)
	switch kind {
	case "tokens":
		rows := []ToolMarketToken{}
		err := q.Where("user_id = ?", userID).Order("created_at DESC, id").Find(&rows).Error
		return rows, err
	case "services":
		rows := []struct {
			ToolMarketService `gorm:"embedded"`
			Name              string `json:"name"`
		}{}
		err := q.Table("tool_market_services AS s").Joins("LEFT JOIN tool_market_versions d ON d.id = s.draft_version_id").Joins("LEFT JOIN tool_market_versions v ON v.id = s.live_version_id").Select("s.*, COALESCE(d.name, v.name, '') AS name").Where("s.owner_id = ?", userID).Order("s.created_at DESC, s.id").Scan(&rows).Error
		return rows, err
	case "grants":
		rows := []ToolMarketGrant{}
		err := q.Where("user_id = ?", userID).Order("created_at DESC, id").Find(&rows).Error
		return rows, err
	case "installations":
		rows := []ToolMarketInstallation{}
		err := q.Where("user_id = ?", userID).Order("client_id, tool_id").Find(&rows).Error
		return rows, err
	case "budgets":
		rows := []ToolMarketBudget{}
		err := q.Where("user_id = ?", userID).Order("scope, scope_id").Find(&rows).Error
		return rows, err
	case "favorites":
		// Reapply current visibility, including revoked shared access.
		rows := []ToolMarketListItem{}
		err := marketVisibleQuery(userID).Joins("JOIN tool_market_favorites f ON f.service_id = s.id AND f.user_id = ?", userID).
			Select(marketListColumns).
			Order("f.created_at DESC, s.id").Offset(offset).Limit(limit).Scan(&rows).Error
		return rows, err
	default:
		return nil, ErrToolMarketInput
	}
}
