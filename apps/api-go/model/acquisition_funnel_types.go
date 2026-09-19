package model

// Counts refer to the same registration cohort and each account's own fixed
// observation window. Missing historical evidence is never a failed conversion.
type AcquisitionFunnelStage struct {
	ID                          string   `json:"id"`
	Observed                    int64    `json:"observed"`
	NotObserved                 int64    `json:"not_observed"`
	Unknown                     int64    `json:"unknown"`
	MatureObserved              int64    `json:"mature_observed"`
	MatureKnown                 int64    `json:"mature_known"`
	Observing                   int64    `json:"observing"`
	ConversionRate              *float64 `json:"conversion_rate"`
	TimingAccounts              int64    `json:"timing_accounts"`
	MeanSecondsFromRegistration *float64 `json:"mean_seconds_from_registration"`
}
type AcquisitionCohortFunnel struct {
	Source            string                   `json:"source"`
	Registrations     int64                    `json:"registrations"`
	MatureAccounts    int64                    `json:"mature_accounts"`
	ObservingAccounts int64                    `json:"observing_accounts"`
	Stages            []AcquisitionFunnelStage `json:"stages"`
}
type AcquisitionFirstPaymentSource struct {
	Source       string `json:"source"`
	Evidence     string `json:"evidence"`
	Rule         string `json:"rule"`
	LookbackDays int    `json:"lookback_days"`
	Inferred     bool   `json:"inferred"`
	Accounts     int64  `json:"accounts"`
}
type AcquisitionFunnelReport struct {
	Filters                  AcquisitionFunnelFilter         `json:"filters"`
	From                     int64                           `json:"from"`
	To                       int64                           `json:"to"`
	ObservedUntil            int64                           `json:"observed_until"`
	ObservationDays          int                             `json:"observation_days"`
	Attribution              string                          `json:"attribution"`
	PaymentSnapshotUpdatedAt int64                           `json:"payment_snapshot_updated_at"`
	PaymentSnapshotStatus    string                          `json:"payment_snapshot_status"`
	Channels                 []AcquisitionCohortFunnel       `json:"channels"`
	FirstPaymentSources      []AcquisitionFirstPaymentSource `json:"first_payment_sources"`
	Unavailable              []string                        `json:"unavailable"`
}
