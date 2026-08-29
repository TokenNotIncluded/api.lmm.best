package model

import (
	"fmt"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"gorm.io/gorm"
)

const (
	OpenSourceBountyDisputeOpen           = "open"
	OpenSourceBountyDisputeResolvedPaid   = "resolved_paid"
	OpenSourceBountyDisputeResolvedDenied = "resolved_denied"

	OpenSourceBountyLedgerDisputeRewardTransfer = "dispute_reward_transfer"
)

var openSourceBountyDisputeReasons = map[string]struct{}{
	"merged_but_unpaid":             {},
	"requirements_met_but_rejected": {},
	"misleading_requirements":       {},
	"abusive_conduct":               {},
	"other":                         {},
}

type OpenSourceBountyDispute struct {
	Id                               int     `json:"id"`
	ChallengeId                      int     `json:"challenge_id" gorm:"not null;index"`
	ProjectId                        int     `json:"project_id" gorm:"not null;index"`
	OpenedByUserId                   int     `json:"opened_by_user_id" gorm:"not null;index"`
	AgainstUserId                    int     `json:"against_user_id" gorm:"not null;index"`
	CaseKey                          string  `json:"-" gorm:"type:varchar(96);not null;uniqueIndex"`
	OpenKey                          *string `json:"-" gorm:"type:varchar(64);uniqueIndex"`
	Reason                           string  `json:"reason" gorm:"type:varchar(64);not null"`
	Statement                        string  `json:"statement" gorm:"type:text;not null"`
	ProjectTitleSnapshot             string  `json:"project_title_snapshot" gorm:"type:varchar(120);not null"`
	RepositoryUrlSnapshot            string  `json:"repository_url_snapshot" gorm:"type:varchar(512);not null"`
	ProjectRulesSnapshot             string  `json:"project_rules_snapshot" gorm:"type:text;not null"`
	ProjectEscrowQuotaSnapshot       int     `json:"project_escrow_quota_snapshot" gorm:"not null"`
	ChallengeStatusSnapshot          string  `json:"challenge_status_snapshot" gorm:"type:varchar(20);not null"`
	IssueUrlSnapshot                 string  `json:"issue_url_snapshot" gorm:"type:varchar(512);not null;default:''"`
	PullRequestUrlSnapshot           string  `json:"pull_request_url_snapshot" gorm:"type:varchar(512);not null;default:''"`
	SubmissionNoteSnapshot           string  `json:"submission_note_snapshot" gorm:"type:text;not null;default:''"`
	ReviewNoteSnapshot               string  `json:"review_note_snapshot" gorm:"type:text;not null;default:''"`
	RewardQuotaSnapshot              int     `json:"reward_quota_snapshot" gorm:"not null"`
	TipQuotaSnapshot                 int     `json:"tip_quota_snapshot" gorm:"not null;default:0"`
	OwnerRatingScoreSnapshot         int     `json:"owner_rating_score_snapshot" gorm:"not null;default:0"`
	OwnerRatingCommentSnapshot       string  `json:"owner_rating_comment_snapshot" gorm:"type:varchar(1000);not null;default:''"`
	ContributorRatingScoreSnapshot   int     `json:"contributor_rating_score_snapshot" gorm:"not null;default:0"`
	ContributorRatingCommentSnapshot string  `json:"contributor_rating_comment_snapshot" gorm:"type:varchar(1000);not null;default:''"`
	Status                           string  `json:"status" gorm:"type:varchar(32);not null;index"`
	Resolution                       string  `json:"resolution" gorm:"type:text;not null;default:''"`
	ResolvedByUserId                 int     `json:"resolved_by_user_id" gorm:"not null;default:0;index"`
	CreatedAt                        int64   `json:"created_at" gorm:"bigint;not null;index"`
	UpdatedAt                        int64   `json:"updated_at" gorm:"bigint;not null"`
	ResolvedAt                       int64   `json:"resolved_at" gorm:"bigint;not null;default:0"`
}

func (OpenSourceBountyDispute) TableName() string { return "open_source_bounty_disputes" }

type OpenSourceBountyDisputeView struct {
	OpenSourceBountyDispute
	ProjectTitle              string `json:"project_title"`
	RepositoryUrl             string `json:"repository_url"`
	ProjectRules              string `json:"project_rules"`
	ChallengeStatus           string `json:"challenge_status"`
	CurrentProjectEscrowQuota int    `json:"current_project_escrow_quota"`
	IssueUrl                  string `json:"issue_url"`
	PullRequestUrl            string `json:"pull_request_url"`
	SubmissionNote            string `json:"submission_note"`
	ReviewNote                string `json:"review_note"`
	RewardQuota               int    `json:"reward_quota"`
	TipQuota                  int    `json:"tip_quota"`
	OwnerRatingScore          int    `json:"owner_rating_score"`
	OwnerRatingComment        string `json:"owner_rating_comment"`
	ContributorRatingScore    int    `json:"contributor_rating_score"`
	ContributorRatingComment  string `json:"contributor_rating_comment"`
	OwnerUsername             string `json:"owner_username"`
	ParticipantUsername       string `json:"participant_username"`
	OpenedByUsername          string `json:"opened_by_username"`
	AgainstUsername           string `json:"against_username"`
	LiveEvidenceChanged       bool   `json:"live_evidence_changed"`
}

func openSourceBountyDisputeViewQuery() *gorm.DB {
	return DB.Table("open_source_bounty_disputes AS d").
		Select(`d.*, p.title AS project_title, p.repository_url AS repository_url, p.rules AS project_rules, c.status AS challenge_status,
			p.escrow_quota AS current_project_escrow_quota,
			c.issue_url AS issue_url, c.pull_request_url AS pull_request_url,
			c.submission_note AS submission_note, c.review_note AS review_note,
			c.reward_quota AS reward_quota, c.tip_quota AS tip_quota,
			c.owner_rating_score AS owner_rating_score, c.owner_rating_comment AS owner_rating_comment,
			c.contributor_rating_score AS contributor_rating_score, c.contributor_rating_comment AS contributor_rating_comment,
			owner.username AS owner_username, participant.username AS participant_username,
			opener.username AS opened_by_username, against_user.username AS against_username,
			CASE WHEN p.title <> d.project_title_snapshot
				OR p.repository_url <> d.repository_url_snapshot
				OR p.rules <> d.project_rules_snapshot
				OR c.status <> d.challenge_status_snapshot
				OR c.issue_url <> d.issue_url_snapshot
				OR c.pull_request_url <> d.pull_request_url_snapshot
				OR c.submission_note <> d.submission_note_snapshot
				OR c.review_note <> d.review_note_snapshot
				OR c.reward_quota <> d.reward_quota_snapshot
				OR c.tip_quota <> d.tip_quota_snapshot
				OR c.owner_rating_score <> d.owner_rating_score_snapshot
				OR c.owner_rating_comment <> d.owner_rating_comment_snapshot
				OR c.contributor_rating_score <> d.contributor_rating_score_snapshot
				OR c.contributor_rating_comment <> d.contributor_rating_comment_snapshot
			THEN 1 ELSE 0 END AS live_evidence_changed`).
		Joins("JOIN open_source_bounty_challenges c ON c.id = d.challenge_id").
		Joins("JOIN open_source_bounty_projects p ON p.id = d.project_id").
		Joins("JOIN users owner ON owner.id = p.owner_user_id").
		Joins("JOIN users participant ON participant.id = c.participant_user_id").
		Joins("JOIN users opener ON opener.id = d.opened_by_user_id").
		Joins("JOIN users against_user ON against_user.id = d.against_user_id")
}

func OpenOpenSourceBountyDispute(userId int, challengeId int, reason string, statement string) (*OpenSourceBountyDisputeView, error) {
	return openOpenSourceBountyDispute(userId, challengeId, reason, statement, nil)
}

func OpenOpenSourceBountyDisputeWithMCPConfirmation(userId int, challengeId int, reason string, statement string, operation OpenSourceBountyMCPConfirmedOperation) (*OpenSourceBountyDisputeView, error) {
	return openOpenSourceBountyDispute(userId, challengeId, reason, statement, &operation)
}

func openOpenSourceBountyDispute(userId int, challengeId int, reason string, statement string, operation *OpenSourceBountyMCPConfirmedOperation) (*OpenSourceBountyDisputeView, error) {
	reason = strings.TrimSpace(reason)
	statement = strings.TrimSpace(statement)
	if _, ok := openSourceBountyDisputeReasons[reason]; !ok {
		return nil, bountyError("OPEN_SOURCE_BOUNTY_INVALID_DISPUTE", "invalid bounty dispute reason")
	}
	if len(statement) < 20 || len(statement) > 5000 {
		return nil, bountyError("OPEN_SOURCE_BOUNTY_INVALID_DISPUTE", "dispute statement must contain 20 to 5000 characters")
	}
	var dispute OpenSourceBountyDispute
	err := DB.Transaction(func(tx *gorm.DB) error {
		if operation != nil {
			if err := validateOpenSourceBountyMCPConfirmationTx(tx, userId, operation.ToolName, operation.PayloadHash, operation.State); err != nil {
				return err
			}
		}
		var challengeReference OpenSourceBountyChallenge
		if err := tx.Select("id", "project_id").Where("id = ?", challengeId).First(&challengeReference).Error; err != nil {
			return bountyError("OPEN_SOURCE_BOUNTY_CHALLENGE_NOT_FOUND", "challenge was not found")
		}
		var project OpenSourceBountyProject
		if err := lockForUpdate(tx).Where("id = ?", challengeReference.ProjectId).First(&project).Error; err != nil {
			return bountyError("OPEN_SOURCE_BOUNTY_NOT_FOUND", "bounty project was not found")
		}
		var challenge OpenSourceBountyChallenge
		if err := lockForUpdate(tx).Where("id = ?", challengeId).First(&challenge).Error; err != nil {
			return bountyError("OPEN_SOURCE_BOUNTY_CHALLENGE_NOT_FOUND", "challenge was not found")
		}
		if challenge.ProjectId != project.Id {
			return bountyError("OPEN_SOURCE_BOUNTY_DISPUTE_IDENTITY_MISMATCH", "challenge project changed while the dispute was opened")
		}
		if project.Status == OpenSourceBountyStatusDraft || project.Status == OpenSourceBountyStatusClosed {
			return bountyError("OPEN_SOURCE_BOUNTY_INVALID_STATE", "closed or unpublished bounty escrow cannot be disputed")
		}
		if userId != challenge.ParticipantUserId && userId != project.OwnerUserId {
			return bountyError("OPEN_SOURCE_BOUNTY_FORBIDDEN", "only a bounty party can open a dispute")
		}
		if challenge.Status == OpenSourceBountyChallengeWithdrawn || challenge.Status == OpenSourceBountyChallengeCancelled {
			return bountyError("OPEN_SOURCE_BOUNTY_INVALID_CHALLENGE_STATE", "inactive challenges cannot be disputed")
		}
		if challenge.Status == OpenSourceBountyChallengeRejected && challenge.RejectedAt <= common.GetTimestamp()-OpenSourceBountyAppealWindowSeconds {
			return bountyError("OPEN_SOURCE_BOUNTY_DISPUTE_WINDOW_EXPIRED", "the seven-day dispute window for this rejected challenge has expired")
		}
		// Bounty entity locks always follow project -> challenge -> dispute. This
		// order is shared with review and resolution to avoid PostgreSQL deadlocks.
		var existing OpenSourceBountyDispute
		if err := lockForUpdate(tx).Where("challenge_id = ? AND status = ?", challengeId, OpenSourceBountyDisputeOpen).First(&existing).Error; err == nil {
			return bountyError("OPEN_SOURCE_BOUNTY_DISPUTE_EXISTS", "an open dispute already exists for this challenge")
		} else if err != gorm.ErrRecordNotFound {
			return err
		}
		var previousCase OpenSourceBountyDispute
		if err := lockForUpdate(tx).Where("challenge_id = ? AND opened_by_user_id = ?", challengeId, userId).First(&previousCase).Error; err == nil {
			return bountyError("OPEN_SOURCE_BOUNTY_DISPUTE_EXISTS", "this party has already opened the final dispute case for this challenge")
		} else if err != gorm.ErrRecordNotFound {
			return err
		}
		againstUserId := project.OwnerUserId
		if userId == project.OwnerUserId {
			againstUserId = challenge.ParticipantUserId
		}
		now := common.GetTimestamp()
		caseKey := fmt.Sprintf("challenge:%d:user:%d", challengeId, userId)
		openKey := fmt.Sprintf("challenge:%d", challengeId)
		dispute = OpenSourceBountyDispute{
			ChallengeId: challengeId, ProjectId: project.Id, OpenedByUserId: userId,
			AgainstUserId: againstUserId, CaseKey: caseKey, OpenKey: &openKey, Reason: reason, Statement: statement,
			ProjectTitleSnapshot: project.Title, RepositoryUrlSnapshot: project.RepositoryUrl,
			ProjectRulesSnapshot: project.Rules, ProjectEscrowQuotaSnapshot: project.EscrowQuota,
			ChallengeStatusSnapshot: challenge.Status, IssueUrlSnapshot: challenge.IssueUrl,
			PullRequestUrlSnapshot: challenge.PullRequestUrl,
			SubmissionNoteSnapshot: challenge.SubmissionNote, ReviewNoteSnapshot: challenge.ReviewNote,
			RewardQuotaSnapshot: challenge.RewardQuota, TipQuotaSnapshot: challenge.TipQuota,
			OwnerRatingScoreSnapshot: challenge.OwnerRatingScore, OwnerRatingCommentSnapshot: challenge.OwnerRatingComment,
			ContributorRatingScoreSnapshot: challenge.ContributorRatingScore, ContributorRatingCommentSnapshot: challenge.ContributorRatingComment,
			Status: OpenSourceBountyDisputeOpen, CreatedAt: now, UpdatedAt: now,
		}
		if err := tx.Create(&dispute).Error; err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "unique") {
				return bountyError("OPEN_SOURCE_BOUNTY_DISPUTE_EXISTS", "an open dispute already exists for this challenge")
			}
			return err
		}
		if operation != nil {
			return completeOpenSourceBountyMCPOperationTx(tx, userId, operation.ToolName, operation.PayloadHash, operation.State, map[string]any{"dispute_id": dispute.Id})
		}
		return nil
	})
	if err != nil {
		if operation != nil && OpenSourceBountyErrorCode(err) == "OPEN_SOURCE_BOUNTY_MCP_CONFIRMATION_INVALID" {
			_ = ConsumeOpenSourceBountyMCPConfirmation(userId, operation.ToolName, operation.PayloadHash, operation.State)
		}
		return nil, err
	}
	RecordLog(userId, LogTypeSystem, fmt.Sprintf("Opened dispute %d for open-source bounty challenge %d", dispute.Id, challengeId))
	return GetOpenSourceBountyDispute(userId, dispute.Id, false)
}

func GetOpenSourceBountyDispute(userId int, disputeId int, admin bool) (*OpenSourceBountyDisputeView, error) {
	var view OpenSourceBountyDisputeView
	query := openSourceBountyDisputeViewQuery().Where("d.id = ?", disputeId)
	if !admin {
		query = query.Where("d.opened_by_user_id = ? OR d.against_user_id = ?", userId, userId)
	}
	if err := query.Scan(&view).Error; err != nil {
		return nil, err
	}
	if view.Id == 0 {
		return nil, bountyError("OPEN_SOURCE_BOUNTY_DISPUTE_NOT_FOUND", "bounty dispute was not found")
	}
	return &view, nil
}

func ListOpenSourceBountyDisputes(userId int, admin bool) ([]OpenSourceBountyDisputeView, error) {
	return ListOpenSourceBountyDisputesFiltered(userId, admin, "", 50)
}

func ListOpenSourceBountyDisputesFiltered(userId int, admin bool, status string, limit int) ([]OpenSourceBountyDisputeView, error) {
	status = strings.TrimSpace(status)
	if status != "" && status != OpenSourceBountyDisputeOpen && status != OpenSourceBountyDisputeResolvedPaid && status != OpenSourceBountyDisputeResolvedDenied {
		return nil, bountyError("OPEN_SOURCE_BOUNTY_INVALID_DISPUTE_FILTER", "invalid bounty dispute status filter")
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > 100 {
		limit = 100
	}
	views := make([]OpenSourceBountyDisputeView, 0)
	query := openSourceBountyDisputeViewQuery()
	if admin {
		var user User
		if err := DB.Select("id", "role", "status").Where("id = ? AND deleted_at IS NULL", userId).First(&user).Error; err != nil || user.Status != common.UserStatusEnabled || user.Role < common.RoleAdminUser {
			return nil, bountyError("OPEN_SOURCE_BOUNTY_FORBIDDEN", "administrator access is required")
		}
	} else {
		query = query.Where("d.opened_by_user_id = ? OR d.against_user_id = ?", userId, userId)
	}
	if status != "" {
		query = query.Where("d.status = ?", status)
	}
	if err := query.Order("CASE WHEN d.status = 'open' THEN 0 ELSE 1 END, d.updated_at DESC, d.id DESC").Limit(limit).Scan(&views).Error; err != nil {
		return nil, err
	}
	return views, nil
}

func ResolveOpenSourceBountyDispute(adminUserId int, disputeId int, action string, resolution string) (*OpenSourceBountyDisputeView, int, error) {
	return resolveOpenSourceBountyDispute(adminUserId, disputeId, action, resolution, nil)
}

func ResolveOpenSourceBountyDisputeWithMCPConfirmation(adminUserId int, disputeId int, action string, resolution string, operation OpenSourceBountyMCPConfirmedOperation) (*OpenSourceBountyDisputeView, int, error) {
	return resolveOpenSourceBountyDispute(adminUserId, disputeId, action, resolution, &operation)
}

func resolveOpenSourceBountyDispute(adminUserId int, disputeId int, action string, resolution string, operation *OpenSourceBountyMCPConfirmedOperation) (*OpenSourceBountyDisputeView, int, error) {
	action = strings.TrimSpace(action)
	resolution = strings.TrimSpace(resolution)
	if action != "pay" && action != "deny" {
		return nil, 0, bountyError("OPEN_SOURCE_BOUNTY_INVALID_DISPUTE_RESOLUTION", "dispute resolution action must be pay or deny")
	}
	if len(resolution) < 10 || len(resolution) > 5000 {
		return nil, 0, bountyError("OPEN_SOURCE_BOUNTY_INVALID_DISPUTE_RESOLUTION", "resolution must contain 10 to 5000 characters")
	}
	transferredQuota := 0
	participantUserId := 0
	err := DB.Transaction(func(tx *gorm.DB) error {
		if operation != nil {
			if err := validateOpenSourceBountyMCPConfirmationTx(tx, adminUserId, operation.ToolName, operation.PayloadHash, operation.State); err != nil {
				return err
			}
		}
		var admin User
		if err := tx.Select("id", "role", "status").Where("id = ? AND deleted_at IS NULL", adminUserId).First(&admin).Error; err != nil || admin.Status != common.UserStatusEnabled || admin.Role < common.RoleAdminUser {
			return bountyError("OPEN_SOURCE_BOUNTY_FORBIDDEN", "administrator access is required")
		}
		var disputeReference OpenSourceBountyDispute
		if err := tx.Select("id", "project_id", "challenge_id").Where("id = ?", disputeId).First(&disputeReference).Error; err != nil {
			return bountyError("OPEN_SOURCE_BOUNTY_DISPUTE_NOT_FOUND", "bounty dispute was not found")
		}
		var project OpenSourceBountyProject
		if err := lockForUpdate(tx).Where("id = ?", disputeReference.ProjectId).First(&project).Error; err != nil {
			return err
		}
		var challenge OpenSourceBountyChallenge
		if err := lockForUpdate(tx).Where("id = ?", disputeReference.ChallengeId).First(&challenge).Error; err != nil {
			return err
		}
		var dispute OpenSourceBountyDispute
		if err := lockForUpdate(tx).Where("id = ?", disputeId).First(&dispute).Error; err != nil {
			return bountyError("OPEN_SOURCE_BOUNTY_DISPUTE_NOT_FOUND", "bounty dispute was not found")
		}
		if dispute.Status != OpenSourceBountyDisputeOpen {
			if (action == "pay" && dispute.Status == OpenSourceBountyDisputeResolvedPaid) || (action == "deny" && dispute.Status == OpenSourceBountyDisputeResolvedDenied) {
				return nil
			}
			return bountyError("OPEN_SOURCE_BOUNTY_DISPUTE_RESOLVED", "bounty dispute is already resolved with a different action")
		}
		if challenge.ProjectId != project.Id || dispute.ProjectId != project.Id || dispute.ChallengeId != challenge.Id || challenge.ParticipantUserId <= 0 || project.OwnerUserId <= 0 || challenge.ParticipantUserId == project.OwnerUserId {
			return bountyError("OPEN_SOURCE_BOUNTY_DISPUTE_IDENTITY_MISMATCH", "dispute parties do not match the bounty challenge and project")
		}
		if adminUserId == project.OwnerUserId || adminUserId == challenge.ParticipantUserId {
			return bountyError("OPEN_SOURCE_BOUNTY_DISPUTE_CONFLICT", "a bounty party cannot adjudicate their own dispute")
		}
		now := common.GetTimestamp()
		status := OpenSourceBountyDisputeResolvedDenied
		if action == "pay" {
			if dispute.OpenedByUserId != challenge.ParticipantUserId || dispute.AgainstUserId != project.OwnerUserId {
				return bountyError("OPEN_SOURCE_BOUNTY_DISPUTE_NOT_PAYABLE", "only a contributor claim against the bounty owner can receive an enforced escrow payment")
			}
			if challenge.Status != OpenSourceBountyChallengeSubmitted && challenge.Status != OpenSourceBountyChallengeRejected {
				return bountyError("OPEN_SOURCE_BOUNTY_INVALID_CHALLENGE_STATE", "an enforced payout requires a submitted or rejected challenge")
			}
			if challenge.IssueUrl == "" || challenge.PullRequestUrl == "" {
				return bountyError("OPEN_SOURCE_BOUNTY_INVALID_CHALLENGE_STATE", "a dispute payout requires submitted Issue and pull request evidence")
			}
			if dispute.RewardQuotaSnapshot <= 0 || challenge.RewardQuota != dispute.RewardQuotaSnapshot || project.EscrowQuota < dispute.RewardQuotaSnapshot {
				return bountyError("OPEN_SOURCE_BOUNTY_ESCROW_INSUFFICIENT", "bounty escrow is insufficient")
			}
			participantUserId = challenge.ParticipantUserId
			credit := UpdateWalletQuotaByDelta(
				tx.Model(&User{}).Where("id = ? AND deleted_at IS NULL", participantUserId),
				dispute.RewardQuotaSnapshot,
			)
			if credit.Error != nil {
				return credit.Error
			}
			if credit.RowsAffected != 1 {
				return bountyError("OPEN_SOURCE_BOUNTY_PARTICIPANT_NOT_FOUND", "challenge participant was not found")
			}
			transferredQuota = dispute.RewardQuotaSnapshot
			remainingEscrow := project.EscrowQuota - transferredQuota
			projectUpdates := map[string]any{"escrow_quota": remainingEscrow, "updated_at": now}
			if remainingEscrow == 0 {
				projectUpdates["status"] = OpenSourceBountyStatusCompleted
				projectUpdates["closed_at"] = now
			}
			if err := tx.Model(&project).Updates(projectUpdates).Error; err != nil {
				return err
			}
			if err := tx.Model(&challenge).Updates(map[string]any{
				"status": OpenSourceBountyChallengeApproved, "owner_rating_overturned": challenge.Status == OpenSourceBountyChallengeRejected && challenge.OwnerRatingScore > 0,
				"paid_at": now, "updated_at": now,
			}).Error; err != nil {
				return err
			}
			payoutKey := fmt.Sprintf("challenge:%d", challenge.Id)
			if err := tx.Create(&OpenSourceBountyLedger{
				ProjectId: project.Id, ChallengeId: challenge.Id, UserId: project.OwnerUserId,
				CounterpartyUserId: participantUserId, Kind: OpenSourceBountyLedgerDisputeRewardTransfer,
				Quota: transferredQuota, Note: resolution, RewardPayoutKey: &payoutKey, CreatedAt: now,
			}).Error; err != nil {
				return err
			}
			status = OpenSourceBountyDisputeResolvedPaid
		}
		if err := tx.Model(&dispute).Updates(map[string]any{
			"status": status, "resolution": resolution, "resolved_by_user_id": adminUserId,
			"resolved_at": now, "updated_at": now, "open_key": nil,
		}).Error; err != nil {
			return err
		}
		if operation != nil {
			return completeOpenSourceBountyMCPOperationTx(tx, adminUserId, operation.ToolName, operation.PayloadHash, operation.State, map[string]any{
				"dispute_id": dispute.Id, "transferred_quota": transferredQuota,
			})
		}
		return nil
	})
	if err != nil {
		if operation != nil && OpenSourceBountyErrorCode(err) == "OPEN_SOURCE_BOUNTY_MCP_CONFIRMATION_INVALID" {
			_ = ConsumeOpenSourceBountyMCPConfirmation(adminUserId, operation.ToolName, operation.PayloadHash, operation.State)
		}
		return nil, 0, err
	}
	if transferredQuota > 0 {
		if err := cacheIncrUserQuota(participantUserId, int64(transferredQuota)); err != nil {
			common.SysLog("failed to increase participant quota cache after bounty dispute resolution: " + err.Error())
		}
		RecordLog(participantUserId, LogTypeTopup, fmt.Sprintf("Received %d quota through open-source bounty dispute %d", transferredQuota, disputeId))
	}
	RecordLog(adminUserId, LogTypeSystem, fmt.Sprintf("Resolved open-source bounty dispute %d with action %s", disputeId, action))
	view, err := GetOpenSourceBountyDispute(adminUserId, disputeId, true)
	return view, transferredQuota, err
}
