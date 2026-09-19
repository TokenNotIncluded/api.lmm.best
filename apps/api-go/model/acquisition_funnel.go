package model

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"gorm.io/gorm"
)

type AcquisitionFunnelFilter struct {
	Source           string `json:"source"`
	Campaign         string `json:"campaign"`
	Content          string `json:"content"`
	ConnectionMethod string `json:"connection_method"`
}
type acquisitionFunnelUser struct {
	ID             int
	CreatedAt      int64
	Source         string
	Campaign       string
	Content        string
	ConsentVersion int
	Allowed        bool
}
type acquisitionMilestone struct {
	UserID int
	At     int64
}

var acquisitionFunnelStageIDs = []string{"application_submitted", "access_approved", "oauth_authorized", "api_key_created", "credential_ready", "client_configured", "first_successful_request", "first_payment", "repeat_payment"}

func GetAcquisitionFunnel(ctx context.Context, from, to int64, observationDays int, filter AcquisitionFunnelFilter) (AcquisitionFunnelReport, error) {
	return getAcquisitionFunnelAt(ctx, from, to, observationDays, filter, time.Now().Unix())
}

func getAcquisitionFunnelAt(ctx context.Context, from, to int64, observationDays int, filter AcquisitionFunnelFilter, now int64) (AcquisitionFunnelReport, error) {
	result := AcquisitionFunnelReport{From: from, To: to, ObservedUntil: now, ObservationDays: observationDays, Attribution: "registration_cohort_fixed_window", Filters: filter, Channels: []AcquisitionCohortFunnel{}, FirstPaymentSources: []AcquisitionFirstPaymentSource{}, Unavailable: []string{"historical_permission_events", "historical_deleted_credentials", "configuration_outside_verified_cli", "announcement_queue_completion_history"}}
	if from <= 0 || to <= from || to-from > 366*86400 || observationDays < 1 || observationDays > 90 {
		return result, ErrAcquisitionInvalid
	}
	for _, value := range []string{filter.Source, filter.Campaign, filter.Content} {
		if value != "" && AcquisitionLabel(value) != value {
			return result, ErrAcquisitionInvalid
		}
	}
	switch filter.ConnectionMethod {
	case "", "oauth", "api_key", "both", "unknown":
	default:
		return result, ErrAcquisitionInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if DB == nil {
		return result, gorm.ErrInvalidDB
	}
	db := DB.WithContext(ctx)
	config, err := acquisitionConfig(db)
	if err != nil {
		return result, err
	}
	result.PaymentSnapshotUpdatedAt, result.PaymentSnapshotStatus = config.PaymentSnapshotUpdatedAt, config.PaymentSnapshotStatus
	source := fmt.Sprintf("CASE WHEN a.registration_source IS NOT NULL AND a.registration_source <> '' THEN a.registration_source WHEN u.created_at < %d THEN 'historical_unrecorded' ELSE 'unknown' END", config.StartedAt)
	cohort := db.Table("users u").Joins("LEFT JOIN acquisition_accounts a ON a.user_id = u.id AND a.created_at >= ?", now-AcquisitionAccountDays*86400).Joins("LEFT JOIN acquisition_consents c ON c.user_id = u.id AND c.allowed = ? AND c.version >= 2", true).Where("u.created_at >= ? AND u.created_at < ? AND u.deleted_at IS NULL AND u.role < ?", from, to, common.RoleAdminUser)
	if filter.Source != "" {
		cohort = cohort.Where(source+" = ?", filter.Source)
	}
	if filter.Campaign != "" {
		cohort = cohort.Where("a.registration_campaign = ?", filter.Campaign)
	}
	if filter.Content != "" {
		cohort = cohort.Where("a.registration_content = ?", filter.Content)
	}
	var users []acquisitionFunnelUser
	if err := cohort.Select("u.id, u.created_at, " + source + " AS source, a.registration_campaign AS campaign, a.registration_content AS content, COALESCE(a.consent_version,0) AS consent_version, CASE WHEN c.user_id IS NULL THEN false ELSE true END AS allowed").Limit(10001).Scan(&users).Error; err != nil {
		return result, err
	}
	if len(users) > 10000 {
		return result, fmt.Errorf("acquisition cohort exceeds 10000 accounts; narrow the date range")
	}
	if len(users) == 0 {
		return result, nil
	}
	ids := make([]int, 0, len(users))
	for _, user := range users {
		ids = append(ids, user.ID)
	}
	milestones := map[string]map[int]int64{}
	load := func(stage, table, expression, condition string, args ...any) error {
		var rows []acquisitionMilestone
		query := db.Table(table).Where("user_id IN ?", ids)
		if condition != "" {
			query = query.Where(condition, args...)
		}
		if err := query.Select("user_id, MIN(" + expression + ") AS at").Group("user_id").Scan(&rows).Error; err != nil {
			return err
		}
		values := map[int]int64{}
		for _, row := range rows {
			if stage == "oauth_authorized" {
				row.At /= 1000
			}
			values[row.UserID] = row.At
		}
		milestones[stage] = values
		return nil
	}
	// Surviving credential rows prove creation, including subsequently revoked
	// credentials. Absence cannot prove a historical negative after hard deletion.
	for _, spec := range []struct{ stage, table, expression, condition string }{
		{"application_submitted", "developer_access_requests", "created_at", "created_at > 0"},
		{"access_approved", "developer_access_requests", "reviewed_at", "status = 'approved' AND reviewed_at > 0"},
		{"api_key_created", "tokens", "created_time", "oauth_managed = false AND created_time > 0"},
		{"oauth_authorized", "oauth_server_grants", "created_at_ms", "created_at_ms > 0"},
		{"client_configured", "l1_onboarding_todos", "client_configured_at", "client_configured_at > 0"},
		{"first_successful_request", "acquisition_activities", "first_at", "first_at > 0"},
	} {
		if err := load(spec.stage, spec.table, spec.expression, spec.condition); err != nil {
			return result, err
		}
	}
	// OAuth-managed API credentials remain valid evidence for clients using the
	// existing OAuth token backend rather than OAuthServerGrant.
	if err := load("oauth_token", "tokens", "created_time", "oauth_managed = true AND created_time > 0"); err != nil {
		return result, err
	}
	for id, at := range milestones["oauth_token"] {
		if old := milestones["oauth_authorized"][id]; old == 0 || at < old {
			milestones["oauth_authorized"][id] = at
		}
	}
	var state AcquisitionActivityState
	err = db.First(&state, 1).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return result, err
	}
	var gaps []AcquisitionActivityGap
	if err := db.Where("through_at >= ? AND from_at <= ?", from, now).Find(&gaps).Error; err != nil {
		return result, err
	}
	payments, err := acquisitionSuccessfulPayments(db, from, now, ids)
	if err != nil {
		return result, err
	}
	byUser := map[int][]acquisitionCashEvent{}
	for _, event := range payments {
		byUser[event.UserID] = append(byUser[event.UserID], event)
	}
	var snapshots []AcquisitionFirstPayment
	if err := db.Where("user_id IN ? AND snapshotted_at >= ?", ids, now-AcquisitionAccountDays*86400).Find(&snapshots).Error; err != nil {
		return result, err
	}
	bySnapshot := map[int]AcquisitionFirstPayment{}
	for _, snapshot := range snapshots {
		bySnapshot[snapshot.UserID] = snapshot
	}
	channels := map[string]*AcquisitionCohortFunnel{}
	timing := map[string]map[string]int64{}
	firstSources := map[string]*AcquisitionFirstPaymentSource{}
	for _, user := range users {
		end := user.CreatedAt + int64(observationDays)*86400
		mature := end <= now
		if end > now {
			end = now
		}
		within := func(at int64) bool { return at >= user.CreatedAt && at <= end && at > 0 }
		oauth, key := milestones["oauth_authorized"][user.ID], milestones["api_key_created"][user.ID]
		connection := "unknown"
		if within(oauth) {
			connection = "oauth"
		}
		if within(key) {
			if connection == "oauth" {
				connection = "both"
			} else {
				connection = "api_key"
			}
		}
		if filter.ConnectionMethod != "" && filter.ConnectionMethod != connection {
			continue
		}
		channel := channels[user.Source]
		if channel == nil {
			channel = &AcquisitionCohortFunnel{Source: user.Source}
			for _, stage := range acquisitionFunnelStageIDs {
				channel.Stages = append(channel.Stages, AcquisitionFunnelStage{ID: stage})
			}
			channels[user.Source] = channel
			timing[user.Source] = map[string]int64{}
		}
		channel.Registrations++
		if mature {
			channel.MatureAccounts++
		} else {
			channel.ObservingAccounts++
		}
		times := map[string]int64{}
		for stage, values := range milestones {
			if within(values[user.ID]) {
				times[stage] = values[user.ID]
			}
		}
		if within(oauth) {
			times["credential_ready"] = oauth
		}
		if within(key) && (times["credential_ready"] == 0 || key < times["credential_ready"]) {
			times["credential_ready"] = key
		}
		firstPaymentUnknown, repeatPaymentUnknown := false, false
		cashTimes := []int64{}
		uncertain := []int64{}
		for _, event := range byUser[user.ID] {
			cash, unknown := acquisitionPaymentClass(event)
			if event.OccurredAt > 0 && !within(event.OccurredAt) {
				continue
			}
			if event.OccurredAt <= 0 && event.CreatedAt > end {
				continue
			}
			if cash {
				cashTimes = append(cashTimes, event.OccurredAt)
			}
			if unknown {
				lower := event.OccurredAt
				if lower <= 0 {
					lower = event.CreatedAt
				}
				uncertain = append(uncertain, lower)
			}
		}
		for _, lower := range uncertain {
			if len(cashTimes) == 0 || lower <= cashTimes[0] {
				firstPaymentUnknown = true
			}
			if len(cashTimes) < 2 || lower <= cashTimes[1] {
				repeatPaymentUnknown = true
			}
		}
		if len(cashTimes) > 0 && !firstPaymentUnknown {
			times["first_payment"] = cashTimes[0]
		}
		if len(cashTimes) > 1 && !repeatPaymentUnknown {
			times["repeat_payment"] = cashTimes[1]
		}
		activityKnown := user.Allowed && user.ConsentVersion >= 2 && state.StartedAt > 0 && user.CreatedAt >= state.StartedAt && state.ScannedThrough >= end && user.CreatedAt >= now-AcquisitionAccountDays*86400
		for _, gap := range gaps {
			if gap.FromAt <= end && gap.ThroughAt >= user.CreatedAt {
				activityKnown = false
			}
		}
		// Consent is required even when an old derived activity row remains during
		// a concurrent withdrawal. No source/account association is synthesized.
		if !user.Allowed || user.ConsentVersion < 2 {
			delete(times, "first_successful_request")
		}
		for i := range channel.Stages {
			stage := &channel.Stages[i]
			at := times[stage.ID]
			known := at > 0
			if stage.ID == "first_payment" {
				known = !firstPaymentUnknown
			}
			if stage.ID == "repeat_payment" {
				known = !repeatPaymentUnknown
			}
			if stage.ID == "first_successful_request" {
				known = known || activityKnown
			}
			if at > 0 {
				stage.Observed++
				if mature {
					stage.MatureObserved++
					stage.TimingAccounts++
					timing[user.Source][stage.ID] += at - user.CreatedAt
				}
			} else if known {
				stage.NotObserved++
			} else {
				stage.Unknown++
			}
			if mature && known {
				stage.MatureKnown++
			}
			if !mature {
				stage.Observing++
			}
		}
		if times["first_payment"] > 0 {
			value := AcquisitionFirstPaymentSource{Source: "unknown", Evidence: "snapshot_pending", Rule: "last_external_before_payment_v1", Accounts: 1}
			if snapshot, ok := bySnapshot[user.ID]; ok && user.Allowed && user.ConsentVersion >= 2 {
				value.Rule, value.LookbackDays, value.Inferred = snapshot.Rule, snapshot.LookbackDays, snapshot.Inferred
				switch {
				case snapshot.HistoryConflictAt > 0 || (snapshot.FirstPaidAt > 0 && snapshot.FirstPaidAt != times["first_payment"]):
					value.Evidence = "history_conflict"
					result.PaymentSnapshotStatus = "history_conflict"
				case snapshot.FirstPaidAt == 0:
					value.Evidence = snapshot.Evidence
				default:
					value.Source, value.Evidence = snapshot.Source, snapshot.Evidence
				}
			} else if !user.Allowed || user.ConsentVersion < 2 {
				value.Evidence = "consent_unavailable"
			}
			bucket := fmt.Sprintf("%s\x00%s\x00%s\x00%d\x00%t", value.Source, value.Evidence, value.Rule, value.LookbackDays, value.Inferred)
			if old := firstSources[bucket]; old != nil {
				old.Accounts++
			} else {
				firstSources[bucket] = &value
			}
		}
	}
	if result.PaymentSnapshotStatus == "history_conflict" {
		result.Unavailable = append(result.Unavailable, "first_payment_history_conflict")
	}
	for _, channel := range channels {
		for i := range channel.Stages {
			stage := &channel.Stages[i]
			if channel.MatureAccounts > 0 && stage.MatureKnown == channel.MatureAccounts {
				rate := float64(stage.MatureObserved) / float64(channel.MatureAccounts)
				stage.ConversionRate = &rate
			}
			if stage.TimingAccounts > 0 {
				seconds := float64(timing[channel.Source][stage.ID]) / float64(stage.TimingAccounts)
				stage.MeanSecondsFromRegistration = &seconds
			}
		}
		result.Channels = append(result.Channels, *channel)
	}
	for _, value := range firstSources {
		result.FirstPaymentSources = append(result.FirstPaymentSources, *value)
	}
	sort.Slice(result.Channels, func(i, j int) bool { return result.Channels[i].Source < result.Channels[j].Source })
	sort.Slice(result.FirstPaymentSources, func(i, j int) bool {
		a, b := result.FirstPaymentSources[i], result.FirstPaymentSources[j]
		return fmt.Sprintf("%s:%s:%d:%t", a.Source, a.Evidence, a.LookbackDays, a.Inferred) < fmt.Sprintf("%s:%s:%d:%t", b.Source, b.Evidence, b.LookbackDays, b.Inferred)
	})
	return result, nil
}
