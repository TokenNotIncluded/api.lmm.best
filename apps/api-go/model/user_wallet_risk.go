package model

import (
	"context"

	"gorm.io/gorm"
)

// Version 2 is an explainable review heuristic, not a calibrated probability.
// Risk is recomputed from committed activity; it never blocks or bans accounts.
type UserWalletRisk struct {
	Score                   float64  `json:"score"`
	HighRisk                bool     `json:"high_risk"`
	Version                 int      `json:"version"`
	Reasons                 []string `json:"reasons"`
	CheckinQuota            int64    `json:"checkin_quota"`
	CheckinCount            int64    `json:"checkin_count"`
	TransferredQuota        int64    `json:"transferred_quota"`
	PendingQuota            int64    `json:"pending_quota"`
	ReceivedQuota           int64    `json:"received_quota"`
	HighRiskSenders         int64    `json:"high_risk_senders"`
	ModerationReviewedCount int64    `json:"moderation_reviewed_count"`
	ModerationFlaggedCount  int64    `json:"moderation_flagged_count"`
}

type UserListFilters struct {
	RiskMin   *float64
	RiskMax   *float64
	Transfers string
	Usage     string
	Funding   string
	Checkin   string
}

const userWalletRiskExistingScoreSQL = "CASE WHEN COALESCE(user_wallet_risk_received.score, 0) > user_wallet_risk_base.score THEN COALESCE(user_wallet_risk_received.score, 0) ELSE user_wallet_risk_base.score END"
const userWalletRiskScoreSQL = "CASE WHEN COALESCE(user_moderation_risk.score, 0) > (" + userWalletRiskExistingScoreSQL + ") THEN COALESCE(user_moderation_risk.score, 0) ELSE (" + userWalletRiskExistingScoreSQL + ") END"

// Content-review risk is kept separate from wallet-source risk. In particular,
// receiving a transfer never propagates a sender's moderation classifications.
// Duplicate sources for one real request count once; resets affect this view
// without deleting the historical review and charging receipts.
func userModerationRisk(tx *gorm.DB) *gorm.DB {
	if !tx.Migrator().HasTable(&ModerationJob{}) {
		return tx.Model(&User{}).Select("users.id AS user_id, 0 AS score, 0 AS reviewed_count, 0 AS flagged_count").Where("1 = 0")
	}
	requests := tx.Model(&ModerationJob{}).
		Select("moderation_jobs.user_id, moderation_jobs.request_id, MAX(CASE WHEN moderation_jobs.flagged THEN 1 ELSE 0 END) AS flagged").
		Where("moderation_jobs.status = ? AND moderation_jobs.source IN ?", ModerationJobCompleted, []string{ModerationSourceRelayInput, ModerationSourceAssistantInput})
	if tx.Migrator().HasTable(&AssistantReviewReset{}) {
		requests = requests.Joins("LEFT JOIN assistant_review_resets AS moderation_reset ON moderation_reset.user_id = moderation_jobs.user_id").Where("moderation_jobs.created_at > COALESCE(moderation_reset.reset_at, 0)")
	}
	requests = requests.Group("moderation_jobs.user_id, moderation_jobs.request_id")
	return tx.Table("(?) AS moderation_requests", requests).
		Select("user_id, COUNT(*) AS reviewed_count, SUM(flagged) AS flagged_count, CASE WHEN SUM(flagged) >= 10 THEN 0.95 WHEN SUM(flagged) >= 5 THEN 0.8 WHEN SUM(flagged) >= 2 THEN 0.6 WHEN SUM(flagged) >= 1 THEN 0.4 ELSE 0 END AS score").Group("user_id")
}

// Aggregate in SQL before pagination. Cancelled transfers neither count as
// money sent nor produce a risk association; pending funds are still debited.
func userWalletRiskBase(tx *gorm.DB) *gorm.DB {
	query := tx.Unscoped().Model(&User{})
	if !tx.Migrator().HasTable(&WalletTransfer{}) || !tx.Migrator().HasTable(&Checkin{}) {
		return query.Select("users.id AS user_id, 0 AS score, 0 AS checkin_quota, 0 AS checkin_count, 0 AS transferred_quota, 0 AS pending_quota, 0 AS received_quota")
	}
	checkins := tx.Model(&Checkin{}).Select("user_id, SUM(quota_awarded) AS quota, COUNT(*) AS count").Where("quota_awarded > 0").Group("user_id")
	sent := tx.Model(&WalletTransfer{}).Select("sender_id AS user_id, SUM(quota) AS quota, SUM(CASE WHEN status = 'pending' THEN quota ELSE 0 END) AS pending_quota").Where("status IN ?", []string{"pending", "claimed"}).Group("sender_id")
	received := tx.Model(&WalletTransfer{}).Select("recipient_id AS user_id, SUM(quota) AS quota").Where("status = ?", "claimed").Group("recipient_id")
	query = joinUserTopupTotals(tx, query).
		Joins("LEFT JOIN (?) AS risk_checkins ON risk_checkins.user_id = users.id", checkins).
		Joins("LEFT JOIN (?) AS risk_sent ON risk_sent.user_id = users.id", sent).
		Joins("LEFT JOIN (?) AS risk_incoming ON risk_incoming.user_id = users.id", received)
	score := `CASE
		WHEN COALESCE(risk_checkins.quota, 0) > 0 AND users.used_quota = 0 AND users.request_count = 0 AND COALESCE(risk_sent.quota, 0) > 0 AND COALESCE(user_topup_totals.credited_quota, 0) = 0 AND COALESCE(risk_incoming.quota, 0) = 0 THEN 0.95
		WHEN COALESCE(risk_sent.quota, 0) > 0 AND users.used_quota = 0 AND COALESCE(user_topup_totals.credited_quota, 0) = 0 THEN 0.65
		WHEN COALESCE(risk_sent.quota, 0) > 0 AND users.used_quota = 0 THEN 0.35
		WHEN COALESCE(risk_checkins.quota, 0) > 0 AND COALESCE(risk_sent.quota, 0) > CAST(users.used_quota AS DECIMAL(30, 0)) * 4 AND COALESCE(user_topup_totals.credited_quota, 0) = 0 THEN 0.45
		WHEN COALESCE(risk_checkins.quota, 0) > 0 AND users.used_quota = 0 AND users.request_count = 0 AND COALESCE(user_topup_totals.credited_quota, 0) = 0 THEN 0.25
		ELSE 0 END`
	return query.Select("users.id AS user_id, " + score + " AS score, COALESCE(risk_checkins.quota, 0) AS checkin_quota, COALESCE(risk_checkins.count, 0) AS checkin_count, COALESCE(risk_sent.quota, 0) AS transferred_quota, COALESCE(risk_sent.pending_quota, 0) AS pending_quota, COALESCE(risk_incoming.quota, 0) AS received_quota")
}

func joinUserWalletRisk(tx, query *gorm.DB) *gorm.DB {
	base := userWalletRiskBase(tx)
	var received *gorm.DB
	if tx.Migrator().HasTable(&WalletTransfer{}) {
		received = tx.Table("wallet_transfers AS risk_transfer").
			Joins("JOIN (?) AS risk_sender ON risk_sender.user_id = risk_transfer.sender_id", userWalletRiskBase(tx)).
			Where("risk_transfer.status = ? AND risk_sender.score >= ?", "claimed", 0.8).
			Select("risk_transfer.recipient_id AS user_id, MAX(risk_sender.score * 0.9) AS score, COUNT(DISTINCT risk_transfer.sender_id) AS senders").Group("risk_transfer.recipient_id")
	} else {
		received = tx.Model(&User{}).Select("users.id AS user_id, 0 AS score, 0 AS senders").Where("1 = 0")
	}
	return query.Joins("LEFT JOIN (?) AS user_wallet_risk_base ON user_wallet_risk_base.user_id = users.id", base).
		Joins("LEFT JOIN (?) AS user_wallet_risk_received ON user_wallet_risk_received.user_id = users.id", received).
		Joins("LEFT JOIN (?) AS user_moderation_risk ON user_moderation_risk.user_id = users.id", userModerationRisk(tx))
}

func (filters UserListFilters) Apply(query *gorm.DB) *gorm.DB {
	if filters.RiskMin != nil {
		query = query.Where("("+userWalletRiskScoreSQL+") >= ?", *filters.RiskMin)
	}
	if filters.RiskMax != nil {
		query = query.Where("("+userWalletRiskScoreSQL+") <= ?", *filters.RiskMax)
	}
	switch filters.Transfers {
	case "sent":
		query = query.Where("user_wallet_risk_base.transferred_quota > 0")
	case "received":
		query = query.Where("user_wallet_risk_base.received_quota > 0")
	case "none":
		query = query.Where("user_wallet_risk_base.transferred_quota = 0 AND user_wallet_risk_base.received_quota = 0")
	}
	switch filters.Usage {
	case "zero":
		query = query.Where("users.used_quota = 0")
	case "consumed":
		query = query.Where("users.used_quota > 0")
	}
	switch filters.Funding {
	case "paid":
		query = query.Where("COALESCE(user_topup_totals.credited_quota, 0) > 0")
	case "unpaid":
		query = query.Where("COALESCE(user_topup_totals.credited_quota, 0) = 0")
	}
	switch filters.Checkin {
	case "yes":
		query = query.Where("user_wallet_risk_base.checkin_count > 0")
	case "no":
		query = query.Where("user_wallet_risk_base.checkin_count = 0")
	}
	return query
}

func PopulateUserWalletRiskContext(ctx context.Context, users []*User) error {
	ids := make([]int, 0, len(users))
	byID := make(map[int]*User, len(users))
	for _, user := range users {
		if user != nil {
			ids = append(ids, user.Id)
			byID[user.Id] = user
		}
	}
	if len(ids) == 0 {
		return nil
	}
	type aggregate struct {
		UserID                  int
		Score                   float64
		BaseScore               float64
		CheckinQuota            int64
		CheckinCount            int64
		TransferredQuota        int64
		PendingQuota            int64
		ReceivedQuota           int64
		HighRiskSenders         int64
		ModerationReviewedCount int64
		ModerationFlaggedCount  int64
	}
	var rows []aggregate
	tx := DB.WithContext(ctx)
	query := joinUserWalletRisk(tx, tx.Unscoped().Model(&User{})).Where("users.id IN ?", ids)
	if err := query.Select("users.id AS user_id, (" + userWalletRiskScoreSQL + ") AS score, user_wallet_risk_base.score AS base_score, user_wallet_risk_base.checkin_quota, user_wallet_risk_base.checkin_count, user_wallet_risk_base.transferred_quota, user_wallet_risk_base.pending_quota, user_wallet_risk_base.received_quota, COALESCE(user_wallet_risk_received.senders, 0) AS high_risk_senders, COALESCE(user_moderation_risk.reviewed_count, 0) AS moderation_reviewed_count, COALESCE(user_moderation_risk.flagged_count, 0) AS moderation_flagged_count").Scan(&rows).Error; err != nil {
		return err
	}
	for _, row := range rows {
		reasons := []string{}
		switch row.BaseScore {
		case 0.95:
			reasons = append(reasons, "checkin_transfer_only")
		case 0.65, 0.35:
			reasons = append(reasons, "outbound_without_usage")
		case 0.45:
			reasons = append(reasons, "transfer_dominates_usage")
		case 0.25:
			reasons = append(reasons, "checkin_without_usage")
		}
		if row.HighRiskSenders > 0 {
			reasons = append(reasons, "received_from_high_risk")
		}
		if row.ModerationFlaggedCount > 0 {
			reasons = append(reasons, "moderation_violations")
		}
		byID[row.UserID].WalletRisk = &UserWalletRisk{Score: row.Score, HighRisk: row.Score >= 0.8, Version: 2, Reasons: reasons, CheckinQuota: row.CheckinQuota, CheckinCount: row.CheckinCount, TransferredQuota: row.TransferredQuota, PendingQuota: row.PendingQuota, ReceivedQuota: row.ReceivedQuota, HighRiskSenders: row.HighRiskSenders, ModerationReviewedCount: row.ModerationReviewedCount, ModerationFlaggedCount: row.ModerationFlaggedCount}
	}
	return nil
}

func (options UserSortOptions) needsWalletRisk() bool {
	switch options.SortBy {
	case "risk_score", "transferred_quota", "received_quota", "checkin_quota":
		return true
	}
	return options.Filters.RiskMin != nil || options.Filters.RiskMax != nil || options.Filters.Transfers != "" || options.Filters.Checkin != ""
}
