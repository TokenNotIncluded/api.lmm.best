package model

import (
	"encoding/json"
	"fmt"
	"strconv"

	"gorm.io/gorm"
)

// A dispute's filing-time evidence stays in its original unit. Only its future
// reward transfer uses the independently reviewed credit migration basis.
type bountyDisputeCreditSource struct {
	ID                         int    `json:"id"`
	ChallengeID                int    `json:"challenge_id"`
	ProjectID                  int    `json:"project_id"`
	OpenedByUserID             int    `json:"opened_by_user_id"`
	AgainstUserID              int    `json:"against_user_id"`
	ProjectEscrowQuotaSnapshot int    `json:"project_escrow_quota_snapshot"`
	RewardQuotaSnapshot        int    `json:"reward_quota_snapshot"`
	TipQuotaSnapshot           int    `json:"tip_quota_snapshot"`
	ResolvedByUserID           int    `json:"resolved_by_user_id"`
	CreatedAt                  int64  `json:"created_at"`
	UpdatedAt                  int64  `json:"updated_at"`
	ResolvedAt                 int64  `json:"resolved_at"`
	ChallengeStatusSnapshot    string `json:"challenge_status_snapshot"`
	Status                     string `json:"status"`
}

func bountyDisputeCreditSourceOf(dispute *OpenSourceBountyDispute) bountyDisputeCreditSource {
	return bountyDisputeCreditSource{
		ID: dispute.Id, ChallengeID: dispute.ChallengeId, ProjectID: dispute.ProjectId,
		OpenedByUserID: dispute.OpenedByUserId, AgainstUserID: dispute.AgainstUserId,
		ProjectEscrowQuotaSnapshot: dispute.ProjectEscrowQuotaSnapshot,
		RewardQuotaSnapshot:        dispute.RewardQuotaSnapshot, TipQuotaSnapshot: dispute.TipQuotaSnapshot,
		ResolvedByUserID: dispute.ResolvedByUserId, CreatedAt: dispute.CreatedAt,
		UpdatedAt: dispute.UpdatedAt, ResolvedAt: dispute.ResolvedAt,
		ChallengeStatusSnapshot: dispute.ChallengeStatusSnapshot, Status: dispute.Status,
	}
}

func bountyDisputeRewardCreditTx(tx *gorm.DB, participantUserID int, dispute *OpenSourceBountyDispute) (int, error) {
	sourceID := strconv.Itoa(dispute.Id)
	quota, err := WalletFutureCreditQuota(tx, participantUserID, "bounty_dispute_reward", sourceID, dispute.RewardQuotaSnapshot, dispute.CreatedAt)
	if err != nil {
		return 0, err
	}
	basis, _, err := walletFutureCreditBasisTx(tx, participantUserID, "bounty_dispute_reward", sourceID)
	if err != nil || basis == nil {
		return quota, err
	}
	want := bountyDisputeCreditSourceOf(dispute)
	encoded, err := json.Marshal(want)
	if err != nil {
		return 0, err
	}
	var required, fields map[string]json.RawMessage
	var source bountyDisputeCreditSource
	if json.Unmarshal(encoded, &required) != nil || json.Unmarshal(basis.Source, &fields) != nil || len(fields) != len(required) || json.Unmarshal(basis.Source, &source) != nil || source != want {
		return 0, fmt.Errorf("%w: bounty dispute credit source changed", ErrWalletQuotaOutOfRange)
	}
	for key := range required {
		if _, ok := fields[key]; !ok {
			return 0, fmt.Errorf("%w: bounty dispute credit source incomplete", ErrWalletQuotaOutOfRange)
		}
	}
	return quota, nil
}
