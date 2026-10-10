/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/

package model

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/setting/operation_setting"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

const (
	PublicRelayPending      = "pending"
	PublicRelayApproved     = "approved"
	PublicRelayRejected     = "rejected"
	PublicRelayReportOpen   = "open"
	PublicRelayReportClosed = "closed"
	// Public relay routing is a user preference view, not an unbounded export.
	// Keep the largest list that the preference API accepts in memory even when
	// the approved pool grows to millions of rows.
	publicRelayRoutingMaxItems = 200
)

var (
	ErrPublicRelayInvalidURL      = errors.New("public relay URL must be a public HTTPS or HTTP base URL")
	ErrPublicRelayInvalidInput    = errors.New("invalid public relay contribution")
	ErrPublicRelayNotFound        = errors.New("public relay contribution not found")
	ErrPublicRelayAlreadyReviewed = errors.New("public relay contribution has already been reviewed")
	ErrPublicRelayChannelLinked   = errors.New("channel is already linked to another public relay")
	ErrPublicRelayGroupMismatch   = errors.New("channel is not in the configured public group")
)

// PublicRelayContribution is a user-submitted channel candidate. ChannelConfig
// is retained for server-side processing but must never be serialized because
// it contains upstream credentials.
type PublicRelayContribution struct {
	Id               int     `json:"id" gorm:"primaryKey"`
	UserId           int     `json:"user_id" gorm:"not null;index"`
	ContributorEmail string  `json:"contributor_email" gorm:"type:varchar(255);not null"`
	Name             string  `json:"name" gorm:"type:varchar(120);not null"`
	BaseURL          string  `json:"base_url" gorm:"type:varchar(512);not null"`
	ChannelConfig    string  `json:"-" gorm:"type:text;not null;default:''"`
	Group            string  `json:"group" gorm:"type:varchar(64);not null;index"`
	Models           string  `json:"models" gorm:"type:text"`
	Description      string  `json:"description" gorm:"type:text"`
	Status           string  `json:"status" gorm:"type:varchar(20);not null;index"`
	ReviewNote       string  `json:"review_note,omitempty" gorm:"type:text"`
	ReviewedBy       int     `json:"reviewed_by,omitempty" gorm:"index"`
	CreatedAt        int64   `json:"created_at" gorm:"not null;index"`
	UpdatedAt        int64   `json:"updated_at" gorm:"not null"`
	ReviewedAt       int64   `json:"reviewed_at,omitempty" gorm:"index"`
	ChannelId        int     `json:"channel_id,omitempty" gorm:"index"`
	UsedQuota        int64   `json:"used_quota" gorm:"not null;default:0"`
	TipQuota         int64   `json:"tip_quota" gorm:"not null;default:0"`
	TipCount         int64   `json:"tip_count" gorm:"not null;default:0"`
	WithdrawnQuota   int64   `json:"withdrawn_quota" gorm:"not null;default:0"`
	RatingAverage    float64 `json:"rating_average" gorm:"not null;default:0"`
	RatingCount      int     `json:"rating_count" gorm:"not null;default:0"`
}

func (PublicRelayContribution) TableName() string { return "public_relay_contributions" }

type PublicRelayReport struct {
	Id             int    `json:"id" gorm:"primaryKey"`
	ContributionId int    `json:"contribution_id" gorm:"not null;index;uniqueIndex:idx_public_relay_report_user"`
	ReporterUserId int    `json:"reporter_user_id" gorm:"not null;uniqueIndex:idx_public_relay_report_user"`
	Reason         string `json:"reason" gorm:"type:text;not null"`
	Status         string `json:"status" gorm:"type:varchar(20);not null;index"`
	ReviewedBy     int    `json:"reviewed_by,omitempty" gorm:"index"`
	ReviewNote     string `json:"review_note,omitempty" gorm:"type:text"`
	CreatedAt      int64  `json:"created_at" gorm:"not null;index"`
	ReviewedAt     int64  `json:"reviewed_at,omitempty" gorm:"index"`
}

func (PublicRelayReport) TableName() string { return "public_relay_reports" }

type PublicRelayTip struct {
	Id             int    `json:"id" gorm:"primaryKey"`
	ContributionId int    `json:"contribution_id" gorm:"not null;index"`
	TipperUserId   int    `json:"tipper_user_id" gorm:"not null;index"`
	Quota          int64  `json:"quota" gorm:"not null"`
	Message        string `json:"message" gorm:"type:varchar(500)"`
	CreatedAt      int64  `json:"created_at" gorm:"not null;index"`
}

func (PublicRelayTip) TableName() string { return "public_relay_tips" }

// PublicRelayReview stores one review per user. The review is deliberately
// separate from the contributor record so a user can update their own rating
// without changing the submitted channel metadata.
type PublicRelayReview struct {
	Id             int    `json:"id" gorm:"primaryKey"`
	ContributionId int    `json:"contribution_id" gorm:"not null;index;uniqueIndex:idx_public_relay_review_user"`
	ReviewerUserId int    `json:"reviewer_user_id" gorm:"not null;uniqueIndex:idx_public_relay_review_user"`
	Rating         int    `json:"rating" gorm:"not null"`
	Comment        string `json:"comment" gorm:"type:text"`
	CreatedAt      int64  `json:"created_at" gorm:"not null;index"`
	UpdatedAt      int64  `json:"updated_at" gorm:"not null"`
}

func (PublicRelayReview) TableName() string { return "public_relay_reviews" }

// PublicRelayPreference is the per-user view of the public pool. Disabled and
// ordered channel IDs are JSON because the list is small and user-scoped; no
// global channel priority is modified by this preference.
type PublicRelayPreference struct {
	UserId           int    `json:"user_id" gorm:"primaryKey"`
	Group            string `json:"group" gorm:"type:varchar(64);not null"`
	DisabledChannels string `json:"disabled_channels" gorm:"type:text"`
	OrderedChannels  string `json:"ordered_channels" gorm:"type:text"`
	UpdatedAt        int64  `json:"updated_at" gorm:"not null"`
}

func (PublicRelayPreference) TableName() string { return "public_relay_preferences" }

type PublicRelayReviewView struct {
	Id             int    `json:"id"`
	ContributionId int    `json:"contribution_id"`
	Rating         int    `json:"rating"`
	Comment        string `json:"comment"`
	CreatedAt      int64  `json:"created_at"`
	UpdatedAt      int64  `json:"updated_at"`
}

type PublicRelayView struct {
	Id                int     `json:"id"`
	ContributorEmail  string  `json:"contributor_email"`
	Name              string  `json:"name"`
	BaseURL           string  `json:"base_url"`
	Group             string  `json:"group"`
	Models            string  `json:"models"`
	Description       string  `json:"description"`
	Status            string  `json:"status"`
	CreatedAt         int64   `json:"created_at"`
	UpdatedAt         int64   `json:"updated_at"`
	UsedQuota         int64   `json:"used_quota"`
	TipQuota          int64   `json:"tip_quota"`
	TipCount          int64   `json:"tip_count"`
	WithdrawnQuota    int64   `json:"withdrawn_quota"`
	AvailableTipQuota int64   `json:"available_tip_quota"`
	UsedQuotaUSD      float64 `json:"used_quota_usd"`
	TipQuotaUSD       float64 `json:"tip_quota_usd"`
	WithdrawnQuotaUSD float64 `json:"withdrawn_quota_usd"`
	RatingAverage     float64 `json:"rating_average"`
	RatingCount       int     `json:"rating_count"`
}

func (item *PublicRelayContribution) PublicView() (PublicRelayView, error) {
	if item.WithdrawnQuota > item.TipQuota || item.TipCount < 0 || item.TipCount > common.MaxWalletQuota {
		return PublicRelayView{}, ErrWalletQuotaOutOfRange
	}
	available := item.TipQuota - item.WithdrawnQuota
	if DB != nil {
		var err error
		available, err = publicRelayAvailableCreditTx(DB, item)
		if err != nil {
			return PublicRelayView{}, err
		}
	}
	amounts := make([]float64, 3)
	for index, quota := range []int64{item.UsedQuota, item.TipQuota, item.WithdrawnQuota} {
		if quota < 0 || quota > common.MaxWalletQuota {
			return PublicRelayView{}, ErrWalletQuotaOutOfRange
		}
		usd, err := common.CreditsToUSD(quota)
		if err != nil {
			return PublicRelayView{}, err
		}
		amounts[index] = usd.InexactFloat64()
	}
	return PublicRelayView{
		Id: item.Id, ContributorEmail: item.ContributorEmail, Name: item.Name,
		BaseURL: item.BaseURL, Group: item.Group, Models: item.Models,
		Description: item.Description, Status: item.Status,
		CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt,
		UsedQuota:      item.UsedQuota,
		WithdrawnQuota: item.WithdrawnQuota, TipQuota: item.TipQuota, TipCount: item.TipCount,
		AvailableTipQuota: available,
		UsedQuotaUSD:      amounts[0],
		TipQuotaUSD:       amounts[1],
		WithdrawnQuotaUSD: amounts[2],
		RatingAverage:     item.RatingAverage, RatingCount: item.RatingCount,
	}, nil
}

// PublicRelayTipBounds preserves the original integer-Credit withdrawal and
// tip policies. These legacy thresholds are not USD prices: clients display
// their real USD equivalent using the immutable currency anchor.
func PublicRelayTipBounds() (minimumWithdrawal, maximumTip int64, err error) {
	if _, err = common.LegacyPricingUnitsPerUSD(); err != nil {
		return 0, 0, err
	}
	legacy, err := common.LegacyPricingQuotaPerUnit()
	if err != nil {
		return 0, 0, err
	}
	minimum := legacy.Mul(decimal.NewFromInt(10)).Truncate(0)
	maximum := legacy.Mul(decimal.NewFromInt(100)).Truncate(0)
	if !minimum.IsPositive() || maximum.GreaterThan(decimal.NewFromInt(common.MaxWalletQuota)) {
		return 0, 0, ErrWalletQuotaOutOfRange
	}
	return minimum.IntPart(), maximum.IntPart(), nil
}

func normalizePublicRelayURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" && parsed.Scheme != "http" || parsed.Hostname() == "" || parsed.User != nil || parsed.Fragment != "" || parsed.RawQuery != "" || parsed.ForceQuery || len(raw) > 512 {
		return "", ErrPublicRelayInvalidURL
	}
	host := strings.TrimSuffix(strings.ToLower(parsed.Hostname()), ".")
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.Contains(host, "%") {
		return "", ErrPublicRelayInvalidURL
	}
	if ip := net.ParseIP(host); ip != nil && (!(&common.SSRFProtection{}).IsIPAccessAllowed(ip)) {
		return "", ErrPublicRelayInvalidURL
	}
	if port := parsed.Port(); port != "" {
		number, err := strconv.Atoi(port)
		if err != nil || number < 1 || number > 65535 {
			return "", ErrPublicRelayInvalidURL
		}
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	return parsed.String(), nil
}

func normalizePublicRelayInput(name, baseURL, models, description string) (string, string, string, string, error) {
	name = strings.TrimSpace(name)
	models = strings.TrimSpace(models)
	description = strings.TrimSpace(description)
	if name == "" || models == "" || len([]rune(name)) > 120 || len([]rune(models)) > 4000 || len([]rune(description)) > 4000 {
		return "", "", "", "", ErrPublicRelayInvalidInput
	}
	baseURL, err := normalizePublicRelayURL(baseURL)
	if err != nil {
		return "", "", "", "", err
	}
	return name, baseURL, models, description, nil
}

func normalizePublicRelayChannelConfig(rawConfig, name, baseURL, models string) (string, error) {
	rawConfig = strings.TrimSpace(rawConfig)
	if rawConfig == "" || len(rawConfig) > 128<<10 {
		return "", ErrPublicRelayInvalidInput
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal([]byte(rawConfig), &envelope); err != nil {
		return "", ErrPublicRelayInvalidInput
	}
	var channel struct {
		Name    string `json:"name"`
		BaseURL string `json:"base_url"`
		Key     string `json:"key"`
		Models  string `json:"models"`
	}
	if err := json.Unmarshal(envelope["channel"], &channel); err != nil {
		return "", ErrPublicRelayInvalidInput
	}
	if strings.TrimSpace(channel.Name) != name || strings.TrimSpace(channel.Key) == "" || strings.TrimSpace(channel.Models) != models {
		return "", ErrPublicRelayInvalidInput
	}
	configBaseURL, err := normalizePublicRelayURL(channel.BaseURL)
	if err != nil || configBaseURL != baseURL {
		return "", ErrPublicRelayInvalidInput
	}
	if _, err := preparePublicRelayChannel(&PublicRelayContribution{
		Name: name, BaseURL: baseURL, Models: models, ChannelConfig: rawConfig,
	}); err != nil {
		return "", err
	}
	canonical, err := json.Marshal(envelope)
	if err != nil {
		return "", ErrPublicRelayInvalidInput
	}
	return string(canonical), nil
}

func CreatePublicRelayContribution(userID int, email, name, baseURL, models, description, channelConfig string) (*PublicRelayContribution, error) {
	if userID <= 0 || strings.TrimSpace(email) == "" {
		return nil, ErrPublicRelayInvalidInput
	}
	name, baseURL, models, description, err := normalizePublicRelayInput(name, baseURL, models, description)
	if err != nil {
		return nil, err
	}
	channelConfig, err = normalizePublicRelayChannelConfig(channelConfig, name, baseURL, models)
	if err != nil {
		return nil, err
	}
	now := common.GetTimestamp()
	item := &PublicRelayContribution{
		UserId: userID, ContributorEmail: strings.TrimSpace(email), Name: name,
		BaseURL: baseURL, Group: operation_setting.GetPublicRelayGroup(), Models: models,
		Description: description, ChannelConfig: channelConfig, Status: PublicRelayPending, CreatedAt: now, UpdatedAt: now,
	}
	if err := DB.Create(item).Error; err != nil {
		return nil, err
	}
	return item, nil
}

func ListApprovedPublicRelays(limit int) ([]PublicRelayView, error) {
	if _, err := common.CreditsPerUSD(); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	items := make([]PublicRelayContribution, 0)
	err := publicRelayVisibleQuery(DB, operation_setting.GetPublicRelayGroup()).
		Order("rating_average DESC, rating_count DESC, updated_at DESC, id DESC").Limit(limit).Find(&items).Error
	if err != nil {
		return nil, err
	}
	views := make([]PublicRelayView, 0, len(items))
	for i := range items {
		view, err := items[i].PublicView()
		if err != nil {
			return nil, err
		}
		views = append(views, view)
	}
	return views, nil
}

func UpdatePublicRelayRating(contributionID, userID, rating int, comment string) error {
	comment = strings.TrimSpace(comment)
	if contributionID <= 0 || userID <= 0 || rating < 1 || rating > 5 || len([]rune(comment)) > 2000 {
		return ErrPublicRelayInvalidInput
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		var item PublicRelayContribution
		if err := lockForUpdate(tx).Where("id = ? AND status = ? AND "+commonGroupCol+" = ? AND channel_id > 0", contributionID, PublicRelayApproved, operation_setting.GetPublicRelayGroup()).First(&item).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrPublicRelayNotFound
			}
			return err
		}
		now := common.GetTimestamp()
		var review PublicRelayReview
		err := tx.Where("contribution_id = ? AND reviewer_user_id = ?", contributionID, userID).First(&review).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			review = PublicRelayReview{ContributionId: contributionID, ReviewerUserId: userID, Rating: rating, Comment: comment, CreatedAt: now, UpdatedAt: now}
			if err := tx.Create(&review).Error; err != nil {
				return err
			}
		} else if err != nil {
			return err
		} else if err := tx.Model(&review).Updates(map[string]interface{}{"rating": rating, "comment": comment, "updated_at": now}).Error; err != nil {
			return err
		}
		var aggregate struct {
			Average float64
			Count   int64
		}
		if err := tx.Model(&PublicRelayReview{}).Where("contribution_id = ?", contributionID).Select("COALESCE(AVG(rating), 0) AS average, COUNT(*) AS count").Scan(&aggregate).Error; err != nil {
			return err
		}
		return tx.Model(&item).Updates(map[string]interface{}{"rating_average": aggregate.Average, "rating_count": aggregate.Count, "updated_at": now}).Error
	})
}

func ListPublicRelayReviews(contributionID, limit int) ([]PublicRelayReviewView, error) {
	if contributionID <= 0 {
		return nil, ErrPublicRelayInvalidInput
	}
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	var item PublicRelayContribution
	if err := DB.Where("id = ? AND status = ? AND "+commonGroupCol+" = ? AND channel_id > 0", contributionID, PublicRelayApproved, operation_setting.GetPublicRelayGroup()).First(&item).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrPublicRelayNotFound
		}
		return nil, err
	}
	var reviews []PublicRelayReview
	err := DB.Where("contribution_id = ?", contributionID).Order("updated_at DESC, id DESC").Limit(limit).Find(&reviews).Error
	views := make([]PublicRelayReviewView, 0, len(reviews))
	for _, review := range reviews {
		views = append(views, PublicRelayReviewView{Id: review.Id, ContributionId: review.ContributionId, Rating: review.Rating, Comment: review.Comment, CreatedAt: review.CreatedAt, UpdatedAt: review.UpdatedAt})
	}
	return views, err
}

type PublicRelayRoutingItem struct {
	PublicRelayView
	ChannelId int  `json:"channel_id"`
	Disabled  bool `json:"disabled"`
	Position  int  `json:"position"`
}

func decodePublicRelayIDs(raw string) []int {
	var ids []int
	if json.Unmarshal([]byte(raw), &ids) != nil {
		return nil
	}
	seen := make(map[int]struct{}, len(ids))
	result := make([]int, 0, len(ids))
	for _, id := range ids {
		if id > 0 {
			if _, ok := seen[id]; !ok {
				seen[id] = struct{}{}
				result = append(result, id)
			}
		}
	}
	return result
}

func encodePublicRelayIDs(ids []int) string {
	if ids == nil {
		ids = []int{}
	}
	data, _ := json.Marshal(ids)
	return string(data)
}

func GetPublicRelayRoutingPreference(userID int, group string) (disabled, ordered []int, err error) {
	if userID <= 0 {
		return nil, nil, gorm.ErrInvalidData
	}
	var preference PublicRelayPreference
	err = DB.Where(map[string]interface{}{"user_id": userID, "group": strings.TrimSpace(group)}).First(&preference).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return []int{}, []int{}, nil
	}
	return decodePublicRelayIDs(preference.DisabledChannels), decodePublicRelayIDs(preference.OrderedChannels), err
}

func ListPublicRelayRouting(userID int) ([]PublicRelayRoutingItem, string, error) {
	group := operation_setting.GetPublicRelayGroup()
	if _, err := common.CreditsPerUSD(); err != nil {
		return nil, group, err
	}
	disabled, ordered, err := GetPublicRelayRoutingPreference(userID, group)
	if err != nil {
		return nil, group, err
	}
	orderPos := make(map[int]int, len(ordered))
	for index, id := range ordered {
		orderPos[id] = index
	}
	pinned := decodePublicRelayIDs(encodePublicRelayIDs(append(append([]int{}, ordered...), disabled...)))
	items := make([]PublicRelayContribution, 0, publicRelayRoutingMaxItems)
	query := publicRelayVisibleQuery(DB, group)
	if len(pinned) > 0 {
		if err := query.Where("channel_id IN ?", pinned).Order("rating_average DESC, rating_count DESC, updated_at DESC, id DESC").Limit(publicRelayRoutingMaxItems).Find(&items).Error; err != nil {
			return nil, group, err
		}
	}
	if len(items) < publicRelayRoutingMaxItems {
		rest := publicRelayVisibleQuery(DB, group)
		if len(pinned) > 0 {
			rest = rest.Where("channel_id NOT IN ?", pinned)
		}
		var tail []PublicRelayContribution
		if err := rest.Order("rating_average DESC, rating_count DESC, updated_at DESC, id DESC").Limit(publicRelayRoutingMaxItems - len(items)).Find(&tail).Error; err != nil {
			return nil, group, err
		}
		items = append(items, tail...)
	}
	sort.SliceStable(items, func(i, j int) bool {
		pi, iok := orderPos[items[i].ChannelId]
		pj, jok := orderPos[items[j].ChannelId]
		if iok != jok {
			return iok
		}
		if iok && pi != pj {
			return pi < pj
		}
		return items[i].RatingAverage > items[j].RatingAverage
	})
	disabledSet := make(map[int]struct{}, len(disabled))
	for _, id := range disabled {
		disabledSet[id] = struct{}{}
	}
	result := make([]PublicRelayRoutingItem, 0, len(items))
	for index, item := range items {
		_, isDisabled := disabledSet[item.ChannelId]
		view, err := item.PublicView()
		if err != nil {
			return nil, group, err
		}
		result = append(result, PublicRelayRoutingItem{PublicRelayView: view, ChannelId: item.ChannelId, Disabled: isDisabled, Position: index})
	}
	return result, group, nil
}

func UpdatePublicRelayRouting(userID int, group string, disabled, ordered []int) error {
	if userID <= 0 || strings.TrimSpace(group) == "" || len(disabled) > 200 || len(ordered) > 200 {
		return ErrPublicRelayInvalidInput
	}
	group = strings.TrimSpace(group)
	// Validate only the IDs submitted by this request. Loading every approved
	// contribution just to build a membership set made a preference update
	// scale with the entire public pool.
	candidateIDs := make([]int, 0, len(disabled)+len(ordered))
	seenCandidates := make(map[int]struct{}, len(disabled)+len(ordered))
	for _, id := range append(append([]int{}, disabled...), ordered...) {
		if id > 0 {
			if _, exists := seenCandidates[id]; !exists {
				seenCandidates[id] = struct{}{}
				candidateIDs = append(candidateIDs, id)
			}
		}
	}
	if len(candidateIDs) > publicRelayRoutingMaxItems {
		return ErrPublicRelayInvalidInput
	}
	valid := make(map[int]struct{}, len(candidateIDs))
	if len(candidateIDs) > 0 {
		var validIDs []int
		if err := DB.Model(&PublicRelayContribution{}).
			Where("status = ? AND "+commonGroupCol+" = ? AND channel_id > 0 AND channel_id IN ?", PublicRelayApproved, group, candidateIDs).
			Pluck("channel_id", &validIDs).Error; err != nil {
			return err
		}
		for _, id := range validIDs {
			valid[id] = struct{}{}
		}
	}
	sanitize := func(ids []int) []int {
		seen := make(map[int]struct{}, len(ids))
		result := make([]int, 0, len(ids))
		for _, id := range ids {
			if _, ok := valid[id]; ok {
				if _, duplicate := seen[id]; !duplicate {
					seen[id] = struct{}{}
					result = append(result, id)
				}
			}
		}
		return result
	}
	now := common.GetTimestamp()
	return DB.Save(&PublicRelayPreference{UserId: userID, Group: group, DisabledChannels: encodePublicRelayIDs(sanitize(disabled)), OrderedChannels: encodePublicRelayIDs(sanitize(ordered)), UpdatedAt: now}).Error
}

func PublicRelayDisabledChannels(userID int, group string) (map[int]struct{}, []int, error) {
	disabled, ordered, err := GetPublicRelayRoutingPreference(userID, group)
	set := make(map[int]struct{}, len(disabled))
	for _, id := range disabled {
		set[id] = struct{}{}
	}
	return set, ordered, err
}

func ListUserPublicRelayContributions(userID, limit int) ([]PublicRelayManagementView, error) {
	if _, err := common.CreditsPerUSD(); err != nil {
		return nil, err
	}
	if userID <= 0 {
		return nil, gorm.ErrInvalidData
	}
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	items := make([]PublicRelayContribution, 0)
	if err := DB.Where("user_id = ?", userID).Order("created_at DESC, id DESC").Limit(limit).Find(&items).Error; err != nil {
		return nil, err
	}
	return publicRelayManagementViews(items)
}

func ListAdminPublicRelayContributions(status string, limit int) ([]PublicRelayManagementView, error) {
	if _, err := common.CreditsPerUSD(); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	query := DB.Order("created_at DESC, id DESC").Limit(limit)
	status = strings.TrimSpace(strings.ToLower(status))
	if status == "reviewable" {
		query = query.Where("status = ? OR (status = ? AND channel_id = 0)", PublicRelayPending, PublicRelayApproved)
	} else if status != "" {
		query = query.Where("status = ?", status)
	}
	items := make([]PublicRelayContribution, 0)
	if err := query.Find(&items).Error; err != nil {
		return nil, err
	}
	return publicRelayManagementViews(items)
}

func ReviewPublicRelayContribution(id, adminID int, approve bool, note string) (*PublicRelayContribution, error) {
	if id <= 0 || adminID <= 0 {
		return nil, gorm.ErrInvalidData
	}
	note = strings.TrimSpace(note)
	if len([]rune(note)) > 2000 || !approve && len([]rune(note)) < 2 {
		return nil, ErrPublicRelayInvalidInput
	}
	var item PublicRelayContribution
	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := lockForUpdate(tx).First(&item, id).Error; err != nil {
			return err
		}
		// Repair approvals created by older versions which never published a channel.
		if item.Status != PublicRelayPending && !(item.Status == PublicRelayApproved && item.ChannelId == 0) {
			return ErrPublicRelayAlreadyReviewed
		}
		item.Status = PublicRelayRejected
		if approve {
			channel, err := preparePublicRelayChannel(&item)
			if err != nil {
				return err
			}
			if err := tx.Create(channel).Error; err != nil {
				return err
			}
			if err := channel.AddAbilities(tx); err != nil {
				return err
			}
			item.ChannelId, item.Group = channel.Id, channel.Group
			item.Status = PublicRelayApproved
		}
		item.ReviewNote, item.ReviewedBy, item.ReviewedAt, item.UpdatedAt = note, adminID, common.GetTimestamp(), common.GetTimestamp()
		return tx.Save(&item).Error
	})
	if errors.Is(err, gorm.ErrRecordNotFound) {
		err = ErrPublicRelayNotFound
	}
	if err == nil && approve && common.MemoryCacheEnabled {
		// A committed approval must not be reported as failed and retried.
		_ = InitChannelCache()
	}
	return &item, err
}

func LinkPublicRelayChannel(contributionID, channelID int) error {
	if contributionID <= 0 || channelID <= 0 {
		return gorm.ErrInvalidData
	}
	group := operation_setting.GetPublicRelayGroup()
	return DB.Transaction(func(tx *gorm.DB) error {
		var item PublicRelayContribution
		if err := lockForUpdate(tx).Where("id = ? AND status = ?", contributionID, PublicRelayApproved).First(&item).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrPublicRelayNotFound
			}
			return err
		}
		var channel Channel
		if err := lockForUpdate(tx).First(&channel, channelID).Error; err != nil {
			return err
		}
		inGroup := false
		for _, channelGroup := range channel.GetGroups() {
			if channelGroup == group {
				inGroup = true
				break
			}
		}
		if !inGroup {
			return ErrPublicRelayGroupMismatch
		}
		if channel.PublicRelayContributionId != 0 && channel.PublicRelayContributionId != contributionID {
			return ErrPublicRelayChannelLinked
		}
		if item.ChannelId != 0 && item.ChannelId != channelID {
			return ErrPublicRelayChannelLinked
		}
		now := common.GetTimestamp()
		if err := tx.Model(&channel).Updates(map[string]interface{}{"public_relay_contribution_id": contributionID}).Error; err != nil {
			return err
		}
		return tx.Model(&item).Updates(map[string]interface{}{"channel_id": channelID, "group": group, "updated_at": now}).Error
	})
}

// RecordPublicRelayUsage records settled usage for read-only contributor
// analytics. It does not grant automatic rewards; users and administrators
// can choose to tip a contributor explicitly.
func RecordPublicRelayUsage(channelID, quota int) error {
	if channelID <= 0 || quota <= 0 {
		return nil
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		var item PublicRelayContribution
		if err := lockForUpdate(tx).Where("channel_id = ? AND status = ?", channelID, PublicRelayApproved).First(&item).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			return err
		}
		if item.UsedQuota < 0 || int64(quota) > common.MaxWalletQuota-item.UsedQuota {
			return ErrWalletQuotaOutOfRange
		}
		return tx.Model(&item).Updates(map[string]interface{}{
			"used_quota": gorm.Expr("used_quota + ?", quota), "updated_at": common.GetTimestamp(),
		}).Error
	})
}

func TipPublicRelayContribution(contributionID, tipperID int, quota int64, message string) error {
	_, maximumTip, err := PublicRelayTipBounds()
	if err != nil {
		return err
	}
	message = strings.TrimSpace(message)
	if contributionID <= 0 || tipperID <= 0 || quota <= 0 || quota > maximumTip || len([]rune(message)) > 500 {
		return ErrPublicRelayInvalidInput
	}
	recipientID := 0
	err = DB.Transaction(func(tx *gorm.DB) error {
		var item PublicRelayContribution
		if err := lockForUpdate(tx).Where(map[string]interface{}{"id": contributionID, "status": PublicRelayApproved, "group": operation_setting.GetPublicRelayGroup()}).Where("channel_id > 0").First(&item).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrPublicRelayNotFound
			}
			return err
		}
		if item.UserId == tipperID {
			return ErrPublicRelayInvalidInput
		}
		if item.TipQuota < 0 || item.WithdrawnQuota < 0 || item.WithdrawnQuota > item.TipQuota || item.TipQuota > common.MaxWalletQuota-quota || item.TipCount < 0 || item.TipCount >= common.MaxWalletQuota {
			return ErrWalletQuotaOutOfRange
		}
		recipientID = item.UserId
		var tipper User
		if err := lockForUpdate(tx).First(&tipper, tipperID).Error; err != nil {
			return err
		}
		if int64(tipper.Quota) < quota {
			return ErrPublicRelayInvalidInput
		}
		debit := UpdateWalletQuotaByDelta(
			tx.Model(&tipper).Where("quota >= ?", int(quota)),
			-int(quota),
		)
		if debit.Error != nil {
			return debit.Error
		}
		if debit.RowsAffected != 1 {
			return ErrPublicRelayInvalidInput
		}
		// Tips remain in the contribution ledger until the owner explicitly
		// withdraws them into a selectable group. Crediting the owner's quota
		// here would make the later withdrawal double-spend the same tip.
		now := common.GetTimestamp()
		if err := tx.Create(&PublicRelayTip{ContributionId: contributionID, TipperUserId: tipperID, Quota: quota, Message: message, CreatedAt: now}).Error; err != nil {
			return err
		}
		return tx.Model(&item).Updates(map[string]interface{}{"tip_quota": gorm.Expr("tip_quota + ?", quota), "tip_count": gorm.Expr("tip_count + 1"), "updated_at": now}).Error
	})
	if err != nil {
		return err
	}
	if err := cacheDecrUserQuota(tipperID, quota); err != nil {
		common.SysLog("failed to decrease tipper quota cache: " + err.Error())
	}
	RecordLog(recipientID, LogTypeTopup, fmt.Sprintf("Received %d pending quota tip for public relay %d", quota, contributionID))
	RecordLog(tipperID, LogTypeSystem, fmt.Sprintf("Tipped public relay %d with %d quota", contributionID, quota))
	return nil
}

func WithdrawPublicRelayTips(contributionID, userID int, targetGroup string) (int64, error) {
	minimumWithdrawal, _, err := PublicRelayTipBounds()
	if err != nil {
		return 0, err
	}
	if contributionID <= 0 || userID <= 0 || strings.TrimSpace(targetGroup) == "" {
		return 0, gorm.ErrInvalidData
	}
	var amount int64
	err = DB.Transaction(func(tx *gorm.DB) error {
		var item PublicRelayContribution
		if err := lockForUpdate(tx).Where("id = ? AND user_id = ? AND status = ?", contributionID, userID, PublicRelayApproved).First(&item).Error; err != nil {
			return err
		}
		if item.TipQuota < 0 || item.TipQuota > common.MaxWalletQuota || item.WithdrawnQuota < 0 || item.WithdrawnQuota > item.TipQuota {
			return ErrWalletQuotaOutOfRange
		}
		rawAvailable := item.TipQuota - item.WithdrawnQuota
		available, err := publicRelayAvailableCreditTx(tx, &item)
		if err != nil {
			return err
		}
		if available < minimumWithdrawal {
			return ErrPublicRelayInvalidInput
		}
		amount = available
		if err := ApplyWalletQuotaDelta(tx, userID, int(amount)); err != nil {
			return err
		}
		// Consume the full source pool once; only corrected credits enter the wallet.
		return tx.Model(&item).UpdateColumns(map[string]interface{}{"withdrawn_quota": gorm.Expr("withdrawn_quota + ?", rawAvailable), "updated_at": common.GetTimestamp()}).Error
	})
	if err != nil {
		return 0, err
	}
	if cacheErr := cacheIncrUserQuota(userID, amount); cacheErr != nil {
		common.SysLog("failed to increase contributor quota cache after public relay withdrawal: " + cacheErr.Error())
	}
	RecordLog(userID, LogTypeTopup, fmt.Sprintf("Withdrew %d quota from public relay tips %d into group %s", amount, contributionID, targetGroup))
	return amount, nil
}

func CreatePublicRelayReport(contributionID, reporterID int, reason string) (*PublicRelayReport, error) {
	reason = strings.TrimSpace(reason)
	if contributionID <= 0 || reporterID <= 0 || len([]rune(reason)) < 2 || len([]rune(reason)) > 2000 {
		return nil, ErrPublicRelayInvalidInput
	}
	var report PublicRelayReport
	err := DB.Transaction(func(tx *gorm.DB) error {
		var item PublicRelayContribution
		if err := lockForUpdate(tx).Where("id = ? AND status = ? AND "+commonGroupCol+" = ? AND channel_id > 0", contributionID, PublicRelayApproved, operation_setting.GetPublicRelayGroup()).First(&item).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrPublicRelayNotFound
			}
			return err
		}
		err := tx.Where("contribution_id = ? AND reporter_user_id = ?", contributionID, reporterID).First(&report).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		report.ContributionId, report.ReporterUserId = contributionID, reporterID
		report.Reason, report.Status = reason, PublicRelayReportOpen
		report.ReviewedBy, report.ReviewNote, report.ReviewedAt = 0, "", 0
		report.CreatedAt = common.GetTimestamp()
		return tx.Save(&report).Error
	})
	return &report, err
}

func ListAdminPublicRelayReports(status string, limit int) ([]PublicRelayReport, error) {
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	query := DB.Order("created_at DESC, id DESC").Limit(limit)
	if status = strings.TrimSpace(strings.ToLower(status)); status != "" {
		query = query.Where("status = ?", status)
	}
	items := make([]PublicRelayReport, 0)
	return items, query.Find(&items).Error
}

func ReviewPublicRelayReport(id, adminID int, closeReport bool, note string) error {
	if id <= 0 || adminID <= 0 {
		return gorm.ErrInvalidData
	}
	note = strings.TrimSpace(note)
	if len([]rune(note)) > 2000 {
		return ErrPublicRelayInvalidInput
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		var report PublicRelayReport
		if err := lockForUpdate(tx).First(&report, id).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrPublicRelayNotFound
			}
			return err
		}
		status := PublicRelayReportOpen
		if closeReport {
			status = PublicRelayReportClosed
		}
		return tx.Model(&report).Updates(map[string]interface{}{
			"status": status, "reviewed_by": adminID, "review_note": note, "reviewed_at": common.GetTimestamp(),
		}).Error
	})
}
