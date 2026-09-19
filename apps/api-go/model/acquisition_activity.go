package model

import (
	"context"
	"errors"
	"sort"
	"sync/atomic"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const AcquisitionDaySeconds int64 = 86400

type AcquisitionActivity struct {
	UserID  int   `json:"user_id" gorm:"primaryKey;autoIncrement:false"`
	Day     int64 `json:"day" gorm:"primaryKey;autoIncrement:false"`
	FirstAt int64 `json:"first_at"`
	LastAt  int64 `json:"last_at"`
}
type AcquisitionActivityGap struct {
	FromAt    int64 `json:"from" gorm:"primaryKey;autoIncrement:false"`
	ThroughAt int64 `json:"through" gorm:"index"`
}
type AcquisitionActivityState struct {
	GapFrom        int64  `json:"gap_from"`
	GapThrough     int64  `json:"gap_through"`
	ID             int    `json:"id" gorm:"primaryKey"`
	StartedAt      int64  `json:"started_at"`
	ScannedThrough int64  `json:"scanned_through"`
	UpdatedAt      int64  `json:"updated_at"`
	Status         string `json:"status" gorm:"type:varchar(32)"`
	Incomplete     bool   `json:"incomplete"`
}

var acquisitionPendingGapFrom atomic.Int64
var acquisitionPendingGapThrough atomic.Int64

// Called only after a qualifying response lost its operational receipt. It
// performs no I/O on the relay path; the observer persists a global gap span.
func noteAcquisitionActivityGap(at int64) {
	for {
		old := acquisitionPendingGapFrom.Load()
		if old != 0 && old <= at {
			break
		}
		if acquisitionPendingGapFrom.CompareAndSwap(old, at) {
			break
		}
	}
	for {
		old := acquisitionPendingGapThrough.Load()
		if old >= at {
			break
		}
		if acquisitionPendingGapThrough.CompareAndSwap(old, at) {
			break
		}
	}
}
func saveActivityGap(db *gorm.DB, state *AcquisitionActivityState, from, through int64) error {
	state.Incomplete = true
	if state.GapFrom == 0 || from < state.GapFrom {
		state.GapFrom = from
	}
	if through > state.GapThrough {
		state.GapThrough = through
	}
	return db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "from_at"}}, DoUpdates: clause.Assignments(map[string]any{"through_at": gorm.Expr("CASE WHEN through_at < ? THEN ? ELSE through_at END", through, through)})}).Create(&AcquisitionActivityGap{FromAt: from, ThroughAt: through}).Error; err != nil {
			return err
		}
		return tx.Model(&AcquisitionActivityState{}).Where("id = 1").Updates(map[string]any{
			"incomplete":  true,
			"gap_from":    gorm.Expr("CASE WHEN gap_from = 0 OR gap_from > ? THEN ? ELSE gap_from END", from, from),
			"gap_through": gorm.Expr("CASE WHEN gap_through < ? THEN ? ELSE gap_through END", through, through),
		}).Error
	})
}

func activityState(db *gorm.DB, now int64) (AcquisitionActivityState, error) {
	state := AcquisitionActivityState{ID: 1, StartedAt: now, ScannedThrough: now, Status: "starting", UpdatedAt: now}
	if err := db.Clauses(clause.OnConflict{DoNothing: true}).Create(&state).Error; err != nil {
		return state, err
	}
	err := db.First(&state, 1).Error
	return state, err
}
func acquisitionSuccessPredicate(dialect, alias string) string {
	column := alias + ".other"
	switch dialect {
	case "postgres":
		return "CASE WHEN " + column + " LIKE '%\"acquisition_success_v1\":true%' THEN (" + column + "::jsonb -> 'acquisition_success_v1') = 'true'::jsonb ELSE FALSE END"
	case "mysql":
		return "CASE WHEN JSON_VALID(" + column + ") THEN JSON_TYPE(JSON_EXTRACT(" + column + ", '$.acquisition_success_v1')) = 'BOOLEAN' AND JSON_UNQUOTE(JSON_EXTRACT(" + column + ", '$.acquisition_success_v1')) = 'true' ELSE FALSE END"
	case "clickhouse":
		return "JSONExtractBool(" + column + ", 'acquisition_success_v1') = 1"
	default:
		return "CASE WHEN json_valid(" + column + ") THEN json_type(" + column + ", '$.acquisition_success_v1') = 'true' ELSE 0 END"
	}
}

// Only daily min/max times cross from the operational log store. No text,
// model prompts, IPs, tokens, raw error messages or request IDs are retained.
func ReconcileAcquisitionActivity(ctx context.Context, now int64) error {
	if DB == nil || LOG_DB == nil {
		return gorm.ErrInvalidDB
	}
	db := DB.WithContext(ctx)
	state, err := activityState(db, now)
	if err != nil {
		return err
	}
	if gapFrom := acquisitionPendingGapFrom.Swap(0); gapFrom > 0 {
		gapThrough := acquisitionPendingGapThrough.Load() + 1
		if err := saveActivityGap(db, &state, gapFrom, gapThrough); err != nil {
			noteAcquisitionActivityGap(gapFrom)
			return err
		}
	}
	if !common.LogConsumeEnabled {
		if err := saveActivityGap(db, &state, state.ScannedThrough, now); err != nil {
			return err
		}
		return db.Model(&AcquisitionActivityState{}).Where("id = 1").Updates(map[string]any{"status": "logs_disabled", "incomplete": true, "updated_at": now}).Error
	}
	if state.Status == "logs_disabled" {
		if err := saveActivityGap(db, &state, state.ScannedThrough, now); err != nil {
			return err
		}
	}
	// A delay and overlap cover normal commit lag. Rebuild can replay the full
	// observation window after extended downtime; repeated scans are idempotent.
	from := state.ScannedThrough - 120
	if from < state.StartedAt {
		from = state.StartedAt
	}
	through := state.ScannedThrough + 3600
	if cutoff := now - 60; through > cutoff {
		through = cutoff
	}
	if through <= from {
		return nil
	}
	logDB := LOG_DB.WithContext(ctx)
	dialect := LOG_DB.Dialector.Name()
	marker := acquisitionSuccessPredicate(dialect, "l")
	priorMarker := acquisitionSuccessPredicate(dialect, "earlier")
	receipt := logDB.Table("logs AS l").Select("l.user_id, l.request_id, MIN(l.created_at) AS occurred_at").
		Where("l.type = ? AND l.token_id > 0 AND l.request_id <> '' AND l.created_at >= ? AND l.created_at < ?", LogTypeConsume, from, through).
		Where(marker).
		Group("l.user_id, l.request_id")
	if dialect == "clickhouse" {
		// Tuple membership avoids a correlated subquery on ClickHouse. See
		// https://clickhouse.com/docs/reference/statements/in .
		prior := logDB.Table("logs AS earlier").Select("earlier.user_id, earlier.request_id").Where("earlier.type = ? AND earlier.created_at >= ? AND earlier.created_at < ?", LogTypeConsume, state.StartedAt, from).Where(priorMarker)
		receipt = receipt.Where("(l.user_id, l.request_id) GLOBAL NOT IN (?)", prior)
	} else {
		receipt = receipt.Where("NOT EXISTS (SELECT 1 FROM logs AS earlier WHERE earlier.user_id = l.user_id AND earlier.request_id = l.request_id AND earlier.type = ? AND earlier.created_at >= ? AND earlier.created_at < ? AND "+priorMarker+")", LogTypeConsume, state.StartedAt, from)
	}
	dayExpr := "CAST(FLOOR(occurred_at / 86400.0) * 86400 AS BIGINT)"
	if dialect == "mysql" {
		dayExpr = "CAST(FLOOR(occurred_at / 86400.0) * 86400 AS SIGNED)"
	}
	if dialect == "clickhouse" {
		dayExpr = "toInt64(FLOOR(occurred_at / 86400.0) * 86400)"
	}
	var days []AcquisitionActivity
	err = logDB.Table("(?) AS receipt", receipt).Select("user_id, " + dayExpr + " AS day, MIN(occurred_at) AS first_at, MAX(occurred_at) AS last_at").Group("user_id, " + dayExpr).Limit(10001).Scan(&days).Error
	if err != nil {
		return err
	}
	if len(days) > 10000 {
		return errors.New("acquisition activity window exceeds batch limit")
	}
	return db.Transaction(func(tx *gorm.DB) error {
		// Permission to associate sources comes from an actual opted-in visit.
		// Existing users predate this metric and are not relabelled as newly active.
		ids := []int{}
		for _, day := range days {
			ids = append(ids, day.UserID)
		}
		var allowed []AcquisitionAccount
		if len(ids) > 0 {
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Select("user_id").Where("consent_version >= 2 AND user_id IN (SELECT user_id FROM acquisition_consents WHERE allowed = true AND version >= 2) AND user_id IN ? AND user_id IN (SELECT id FROM users WHERE created_at >= ? AND role < ?)", ids, state.StartedAt, common.RoleAdminUser).Order("user_id ASC").Find(&allowed).Error; err != nil {
				return err
			}
		}
		owners := map[int]bool{}
		for _, account := range allowed {
			owners[account.UserID] = true
		}
		for _, day := range days {
			if !owners[day.UserID] {
				continue
			}
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&day).Error; err != nil {
				return err
			}
			if err := tx.Model(&AcquisitionActivity{}).Where("user_id = ? AND day = ?", day.UserID, day.Day).Updates(map[string]any{
				"first_at": gorm.Expr("CASE WHEN first_at > ? THEN ? ELSE first_at END", day.FirstAt, day.FirstAt),
				"last_at":  gorm.Expr("CASE WHEN last_at < ? THEN ? ELSE last_at END", day.LastAt, day.LastAt),
			}).Error; err != nil {
				return err
			}
		}
		status := "ready"
		if through < now-120 {
			status = "catching_up"
		}
		return tx.Model(&AcquisitionActivityState{}).Where("id = 1 AND scanned_through = ?", state.ScannedThrough).Updates(map[string]any{"scanned_through": through, "updated_at": now, "status": status}).Error
	})
}
func RunAcquisitionActivity(parent context.Context) {
	run := func() {
		now := time.Now().Unix()
		paymentCtx, paymentCancel := context.WithTimeout(parent, 8*time.Second)
		if DB != nil {
			if err := ReconcileAcquisitionFirstPayments(paymentCtx, now); err != nil {
				healthCtx, healthCancel := context.WithTimeout(parent, time.Second)
				_ = DB.WithContext(healthCtx).Model(&AcquisitionConfig{}).Where("id = 1").Update("payment_snapshot_status", "source_unavailable").Error
				healthCancel()
			}
		}
		paymentCancel()
		ctx, cancel := context.WithTimeout(parent, 15*time.Second)
		defer cancel()
		if err := ReconcileAcquisitionActivity(ctx, now); err != nil && DB != nil {
			// Persist a bounded class, never SQL text or database credentials.
			healthCtx, healthCancel := context.WithTimeout(parent, time.Second)
			_ = DB.WithContext(healthCtx).Model(&AcquisitionActivityState{}).Where("id = 1").Updates(map[string]any{"status": "source_unavailable", "updated_at": now}).Error
			healthCancel()
		}
	}
	run()
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-parent.Done():
			return
		case <-ticker.C:
			run()
		}
	}
}
func RebuildAcquisitionActivity(ctx context.Context) error {
	if DB == nil {
		return gorm.ErrInvalidDB
	}
	if _, err := activityState(DB.WithContext(ctx), time.Now().Unix()); err != nil {
		return err
	}
	return DB.WithContext(ctx).Model(&AcquisitionActivityState{}).Where("id = 1").Updates(map[string]any{"scanned_through": gorm.Expr("started_at"), "status": "rebuilding"}).Error
}

type AcquisitionActivitySummary struct {
	IncompleteAccounts int64    `json:"incomplete_accounts"`
	Source             string   `json:"source"`
	EligibleAccounts   int64    `json:"eligible_accounts"`
	SuccessfulAccounts int64    `json:"successful_accounts"`
	MatureAccounts     int64    `json:"mature_accounts"`
	RetainedAccounts   int64    `json:"retained_accounts"`
	ObservingAccounts  int64    `json:"observing_accounts"`
	RetentionRate      *float64 `json:"retention_rate"`
}

func AcquisitionActivityReport(ctx context.Context, from, to int64) (*AcquisitionActivityState, []AcquisitionActivitySummary, error) {
	var state AcquisitionActivityState
	err := DB.WithContext(ctx).First(&state, 1).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	first := DB.WithContext(ctx).Model(&AcquisitionActivity{}).Select("user_id, MIN(first_at) AS first_at, MIN(day) AS first_day").Group("user_id")
	var rows []struct {
		RegisteredAt int64
		Source       string
		UserID       int
		FirstAt      int64
		FirstDay     int64
		Retained     int64
	}
	err = DB.WithContext(ctx).Table("acquisition_accounts AS a").Joins("JOIN users AS u ON u.id = a.user_id").
		Joins("LEFT JOIN (?) AS first_success ON first_success.user_id = u.id", first).
		Joins("LEFT JOIN acquisition_activities AS seventh ON seventh.user_id = u.id AND seventh.day = first_success.first_day + ?", 7*AcquisitionDaySeconds).
		Where("a.consent_version >= 2 AND a.user_id IN (SELECT user_id FROM acquisition_consents WHERE allowed = true AND version >= 2) AND u.role < ? AND u.created_at >= ? AND u.created_at < ? AND u.created_at >= ?", common.RoleAdminUser, from, to, state.StartedAt).
		Select("a.registration_source AS source, u.id AS user_id, u.created_at AS registered_at, COALESCE(first_success.first_at,0) AS first_at, COALESCE(first_success.first_day,0) AS first_day, CASE WHEN seventh.user_id IS NULL THEN 0 ELSE 1 END AS retained").Scan(&rows).Error
	if err != nil {
		return &state, nil, err
	}
	var gaps []AcquisitionActivityGap
	if state.Incomplete {
		if err := DB.WithContext(ctx).Where("through_at > ?", from).Order("from_at").Find(&gaps).Error; err != nil {
			return &state, nil, err
		}
		if len(gaps) == 0 && state.GapThrough > from {
			gaps = append(gaps, AcquisitionActivityGap{FromAt: state.GapFrom, ThroughAt: state.GapThrough})
		}
	}
	// Merge overlapping intervals so membership is a bounded binary search,
	// rather than one full gap scan for every account.
	merged := []AcquisitionActivityGap{}
	for _, gap := range gaps {
		n := len(merged)
		if n > 0 && gap.FromAt <= merged[n-1].ThroughAt {
			if gap.ThroughAt > merged[n-1].ThroughAt {
				merged[n-1].ThroughAt = gap.ThroughAt
			}
		} else {
			merged = append(merged, gap)
		}
	}
	groups := map[string]*AcquisitionActivitySummary{}
	for _, row := range rows {
		if groups[row.Source] == nil {
			groups[row.Source] = &AcquisitionActivitySummary{Source: row.Source}
		}
		group := groups[row.Source]
		group.EligibleAccounts++
		if row.FirstAt == 0 {
			continue
		}
		group.SuccessfulAccounts++
		if row.FirstDay+8*AcquisitionDaySeconds <= state.ScannedThrough {
			gapIndex := sort.Search(len(merged), func(i int) bool { return merged[i].ThroughAt > row.RegisteredAt })
			affected := state.Incomplete && (state.GapFrom == 0 || state.GapThrough == 0 || gapIndex < len(merged) && merged[gapIndex].FromAt < row.FirstDay+8*AcquisitionDaySeconds)
			if affected {
				group.IncompleteAccounts++
				continue
			}
			group.MatureAccounts++
			group.RetainedAccounts += row.Retained
		} else {
			group.ObservingAccounts++
		}
	}
	result := []AcquisitionActivitySummary{}
	for _, group := range groups {
		if group.MatureAccounts > 0 && state.Status == "ready" {
			rate := float64(group.RetainedAccounts) / float64(group.MatureAccounts)
			group.RetentionRate = &rate
		}
		result = append(result, *group)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Source < result[j].Source })
	return &state, result, nil
}
