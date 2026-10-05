package model

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/common"

	"gorm.io/gorm"
)

const (
	ModerationProviderCallMax         = 32
	moderationProviderJournalMaxBytes = 16 << 10
	moderationProviderMaxBatches      = 9
)

// ModerationProviderCall contains only bounded provider-issued identifiers,
// paired with the lease attempt and batch that actually received them.
type ModerationProviderCall struct {
	Attempt    int    `json:"attempt"`
	BatchIndex int    `json:"batch_index"`
	ResponseID string `json:"response_id"`
	RequestID  string `json:"request_id"`
}

func ModerationSubjectIdentifier(value string) string {
	decoded, err := hex.DecodeString(value)
	if err != nil || len(value) != 64 || len(decoded) != 32 || value != strings.ToLower(value) {
		return ""
	}
	return value
}

// ModerationProviderIdentifier never truncates an upstream identifier or
// retains a credential-shaped value as if it were a genuine provider ID.
func ModerationProviderIdentifier(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 128 || RedactModerationContent(value) != value {
		return ""
	}
	for _, prefix := range []string{"sk-", "sk_", "rk-", "rk_", "sess-", "sess_", "bearer-", "bearer_", "AIza"} {
		if strings.HasPrefix(strings.ToLower(value), strings.ToLower(prefix)) {
			return ""
		}
	}
	for _, char := range value {
		if !(char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '-' || char == '_') {
			return ""
		}
	}
	return value
}

func validModerationProviderCall(call ModerationProviderCall) bool {
	return call.Attempt >= 1 && call.Attempt <= ModerationJobMaxAttempts && call.BatchIndex >= 1 && call.BatchIndex <= moderationProviderMaxBatches &&
		(call.ResponseID != "" || call.RequestID != "") &&
		(call.ResponseID == "" || ModerationProviderIdentifier(call.ResponseID) == call.ResponseID) &&
		(call.RequestID == "" || ModerationProviderIdentifier(call.RequestID) == call.RequestID)
}

func decodeModerationProviderCalls(encoded string) ([]ModerationProviderCall, bool) {
	if encoded == "" {
		return []ModerationProviderCall{}, true
	}
	var calls []ModerationProviderCall
	if len(encoded) > moderationProviderJournalMaxBytes || json.Unmarshal([]byte(encoded), &calls) != nil || len(calls) > ModerationProviderCallMax {
		return nil, false
	}
	for _, call := range calls {
		if !validModerationProviderCall(call) {
			return nil, false
		}
	}
	if calls == nil {
		calls = []ModerationProviderCall{}
	}
	return calls, true
}

func (job ModerationJob) ProviderCalls() []ModerationProviderCall {
	calls, ok := decodeModerationProviderCalls(job.ProviderCallsJSON)
	if !ok {
		return []ModerationProviderCall{}
	}
	return calls
}

// AppendModerationProviderCall persists each successful batch before the next
// provider request. A later error or retry keeps earlier calls. The active
// attempt, owner and live lease are all fenced at the locked write boundary.
func AppendModerationProviderCall(ctx context.Context, id int64, owner string, call ModerationProviderCall, now int64) error {
	if DB == nil || id <= 0 || owner == "" || now <= 0 || !validModerationProviderCall(call) {
		return ErrModerationJobInvalid
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return moderationDB(ctx).Transaction(func(tx *gorm.DB) error {
		var job ModerationJob
		if err := lockForUpdate(tx).Select("id", "provider_calls_json", "lease_until").Where("id = ? AND status = ? AND lease_owner = ? AND attempts = ? AND lease_until > ?", id, ModerationJobRunning, owner, call.Attempt, now).First(&job).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrModerationLeaseLost
			}
			return err
		}
		// Waiting for the row lock can cross the lease deadline. The caller's
		// earlier timestamp cannot authorize a write or an idempotent success.
		if job.LeaseUntil <= max(now, common.GetTimestamp()) {
			return ErrModerationLeaseLost
		}
		calls, valid := decodeModerationProviderCalls(job.ProviderCallsJSON)
		if !valid {
			return ErrModerationJobInvalid
		}
		for _, previous := range calls {
			if previous.Attempt == call.Attempt && previous.BatchIndex == call.BatchIndex {
				if previous == call {
					if job.LeaseUntil <= max(now, common.GetTimestamp()) {
						return ErrModerationLeaseLost
					}
					return nil
				}
				return ErrModerationJobInvalid
			}
		}
		if len(calls) >= ModerationProviderCallMax {
			return ErrModerationJobInvalid
		}
		encoded, err := json.Marshal(append(calls, call))
		if err != nil || len(encoded) > moderationProviderJournalMaxBytes {
			return ErrModerationJobInvalid
		}
		leaseCheckTime := max(now, common.GetTimestamp())
		result := tx.Model(&ModerationJob{}).Where("id = ? AND status = ? AND lease_owner = ? AND attempts = ? AND lease_until > ?", id, ModerationJobRunning, owner, call.Attempt, leaseCheckTime).Updates(map[string]any{"provider_calls_json": string(encoded), "updated_at": leaseCheckTime})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrModerationLeaseLost
		}
		return nil
	})
}
