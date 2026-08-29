package model

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"gorm.io/gorm"
)

const (
	OpenSourceBountyStatusDraft     = "draft"
	OpenSourceBountyStatusPublished = "published"
	OpenSourceBountyStatusPaused    = "paused"
	OpenSourceBountyStatusCompleted = "completed"
	OpenSourceBountyStatusClosed    = "closed"

	OpenSourceBountyChallengeAccepted  = "accepted"
	OpenSourceBountyChallengeSubmitted = "submitted"
	OpenSourceBountyChallengeApproved  = "approved"
	OpenSourceBountyChallengeRejected  = "rejected"
	OpenSourceBountyChallengeWithdrawn = "withdrawn"
	OpenSourceBountyChallengeCancelled = "cancelled"

	OpenSourceBountyLedgerEscrowFund     = "escrow_fund"
	OpenSourceBountyLedgerRewardTransfer = "reward_transfer"
	OpenSourceBountyLedgerEscrowRefund   = "escrow_refund"
	OpenSourceBountyLedgerTipTransfer    = "tip_transfer"
	OpenSourceBountyLedgerPlatformFee    = "platform_fee"

	OpenSourceBountyFeeRateOptionKey  = "OpenSourceBountyFeeRate"
	defaultOpenSourceBountyFeeRateBps = 100
)

const (
	maxOpenSourceBountyPageSize         = 50
	maxOpenSourceBountyTipQuota         = 1_000_000_000
	OpenSourceBountyAppealWindowSeconds = 7 * 24 * 60 * 60
)

var githubNamePattern = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9_.-]{0,98}[A-Za-z0-9])?$`)
var openSourceBountyFeeRatePattern = regexp.MustCompile(`^(?:100(?:\.0{1,2})?|[0-9]{1,2}(?:\.[0-9]{1,2})?)$`)

type OpenSourceBountyError struct {
	Code    string
	Message string
}

func (e *OpenSourceBountyError) Error() string { return e.Message }

func bountyError(code string, message string) error {
	return &OpenSourceBountyError{Code: code, Message: message}
}

func OpenSourceBountyErrorCode(err error) string {
	var target *OpenSourceBountyError
	if errors.As(err, &target) {
		return target.Code
	}
	return "OPEN_SOURCE_BOUNTY_INTERNAL_ERROR"
}

type OpenSourceBountyProject struct {
	Id                 int    `json:"id"`
	OwnerUserId        int    `json:"owner_user_id" gorm:"not null;index"`
	RepositoryUrl      string `json:"repository_url" gorm:"type:varchar(512);not null;index"`
	Title              string `json:"title" gorm:"type:varchar(120);not null"`
	Description        string `json:"description" gorm:"type:text;not null"`
	Rules              string `json:"rules" gorm:"type:text;not null"`
	RewardQuota        int    `json:"reward_quota" gorm:"not null;default:0"`
	NetRewardQuota     int    `json:"net_reward_quota" gorm:"not null;default:0"`
	RewardSlots        int    `json:"reward_slots" gorm:"not null;default:0"`
	EscrowQuota        int    `json:"escrow_quota" gorm:"not null;default:0"`
	PlatformFeeRateBps int    `json:"platform_fee_rate_bps" gorm:"not null;default:0"`
	PlatformFeeQuota   int    `json:"platform_fee_quota" gorm:"not null;default:0"`
	Status             string `json:"status" gorm:"type:varchar(20);not null;default:'draft';index"`
	CreatedAt          int64  `json:"created_at" gorm:"bigint;not null"`
	UpdatedAt          int64  `json:"updated_at" gorm:"bigint;not null"`
	PublishedAt        int64  `json:"published_at" gorm:"bigint;not null;default:0;index"`
	ClosedAt           int64  `json:"closed_at" gorm:"bigint;not null;default:0"`
	ArchivedAt         int64  `json:"archived_at" gorm:"bigint;not null;default:0;index"`
}

func (OpenSourceBountyProject) TableName() string { return "open_source_bounty_projects" }

type OpenSourceBountyChallenge struct {
	Id                       int    `json:"id"`
	ProjectId                int    `json:"project_id" gorm:"not null;index;index:idx_open_source_bounty_project_participant,priority:1"`
	ParticipantUserId        int    `json:"participant_user_id" gorm:"not null;index;index:idx_open_source_bounty_project_participant,priority:2"`
	GithubHandle             string `json:"github_handle" gorm:"type:varchar(100);not null"`
	Status                   string `json:"status" gorm:"type:varchar(20);not null;index"`
	IssueUrl                 string `json:"issue_url" gorm:"type:varchar(512);not null;default:''"`
	PullRequestUrl           string `json:"pull_request_url" gorm:"type:varchar(512);not null;default:'';index"`
	SubmissionNote           string `json:"submission_note" gorm:"type:text;not null;default:''"`
	ReviewNote               string `json:"review_note" gorm:"type:text;not null;default:''"`
	RewardQuota              int    `json:"reward_quota" gorm:"not null;default:0"`
	TipQuota                 int    `json:"tip_quota" gorm:"not null;default:0"`
	OwnerRatingScore         int    `json:"owner_rating_score" gorm:"not null;default:0"`
	OwnerRatingComment       string `json:"owner_rating_comment" gorm:"type:varchar(1000);not null;default:''"`
	OwnerRatedAt             int64  `json:"owner_rated_at" gorm:"bigint;not null;default:0"`
	ContributorRatingScore   int    `json:"contributor_rating_score" gorm:"not null;default:0"`
	ContributorRatingComment string `json:"contributor_rating_comment" gorm:"type:varchar(1000);not null;default:''"`
	ContributorRatedAt       int64  `json:"contributor_rated_at" gorm:"bigint;not null;default:0"`
	OwnerRatingOverturned    bool   `json:"owner_rating_overturned" gorm:"not null;default:false;index"`
	AcceptedAt               int64  `json:"accepted_at" gorm:"bigint;not null"`
	SubmittedAt              int64  `json:"submitted_at" gorm:"bigint;not null;default:0"`
	ReviewedAt               int64  `json:"reviewed_at" gorm:"bigint;not null;default:0"`
	RejectedAt               int64  `json:"rejected_at" gorm:"bigint;not null;default:0;index"`
	PaidAt                   int64  `json:"paid_at" gorm:"bigint;not null;default:0"`
	CreatedAt                int64  `json:"created_at" gorm:"bigint;not null"`
	UpdatedAt                int64  `json:"updated_at" gorm:"bigint;not null"`
}

func (OpenSourceBountyChallenge) TableName() string { return "open_source_bounty_challenges" }

type OpenSourceBountyLedger struct {
	Id                 int     `json:"id"`
	ProjectId          int     `json:"project_id" gorm:"not null;index"`
	ChallengeId        int     `json:"challenge_id" gorm:"not null;default:0;index"`
	UserId             int     `json:"user_id" gorm:"not null;index"`
	CounterpartyUserId int     `json:"counterparty_user_id" gorm:"not null;default:0;index"`
	Kind               string  `json:"kind" gorm:"type:varchar(32);not null;index"`
	Quota              int     `json:"quota" gorm:"not null"`
	Note               string  `json:"note" gorm:"type:varchar(500);not null;default:''"`
	RewardPayoutKey    *string `json:"-" gorm:"type:varchar(64);uniqueIndex"`
	RecipientReadAt    int64   `json:"recipient_read_at" gorm:"bigint;not null;default:0;index"`
	ThankedAt          int64   `json:"thanked_at" gorm:"bigint;not null;default:0;index"`
	CreatedAt          int64   `json:"created_at" gorm:"bigint;not null;index"`
}

func (OpenSourceBountyLedger) TableName() string { return "open_source_bounty_ledgers" }

type OpenSourceBountyDraftInput struct {
	RepositoryUrl string `json:"repository_url"`
	Title         string `json:"title"`
	Description   string `json:"description"`
	Rules         string `json:"rules"`
	RewardQuota   int    `json:"reward_quota"`
	RewardSlots   int    `json:"reward_slots"`
}

type OpenSourceBountyProjectView struct {
	OpenSourceBountyProject
	OwnerUsername          string                     `json:"owner_username"`
	ActiveChallengeCount   int64                      `json:"active_challenge_count"`
	ApprovedChallengeCount int64                      `json:"approved_challenge_count"`
	OwnerRatingAverage     float64                    `json:"owner_rating_average"`
	OwnerRatingCount       int64                      `json:"owner_rating_count"`
	OwnerThankHeartCount   int64                      `json:"owner_thank_heart_count"`
	ViewerChallenge        *OpenSourceBountyChallenge `json:"viewer_challenge,omitempty" gorm:"-"`
}

type OpenSourceBountyTipNotification struct {
	Id              int    `json:"id"`
	ProjectId       int    `json:"project_id"`
	ChallengeId     int    `json:"challenge_id"`
	SenderUserId    int    `json:"sender_user_id"`
	SenderUsername  string `json:"sender_username"`
	ProjectTitle    string `json:"project_title"`
	Quota           int    `json:"quota"`
	Note            string `json:"note"`
	RecipientReadAt int64  `json:"recipient_read_at"`
	ThankedAt       int64  `json:"thanked_at"`
	CreatedAt       int64  `json:"created_at"`
}

type OpenSourceBountyNotification struct {
	Id              int    `json:"id"`
	ProjectId       int    `json:"project_id"`
	ChallengeId     int    `json:"challenge_id"`
	SenderUserId    int    `json:"sender_user_id"`
	SenderUsername  string `json:"sender_username"`
	Kind            string `json:"kind"`
	ProjectTitle    string `json:"project_title"`
	Quota           int    `json:"quota"`
	Note            string `json:"note"`
	RecipientReadAt int64  `json:"recipient_read_at"`
	ThankedAt       int64  `json:"thanked_at"`
	CreatedAt       int64  `json:"created_at"`
}

type OpenSourceBountyChallengeView struct {
	OpenSourceBountyChallenge
	ParticipantUsername      string                       `json:"participant_username"`
	ProjectTitle             string                       `json:"project_title"`
	RepositoryUrl            string                       `json:"repository_url"`
	OwnerUsername            string                       `json:"owner_username"`
	ParticipantRatingAverage float64                      `json:"participant_rating_average"`
	ParticipantRatingCount   int64                        `json:"participant_rating_count"`
	OwnerRatingAverage       float64                      `json:"owner_rating_average"`
	OwnerRatingCount         int64                        `json:"owner_rating_count"`
	Dispute                  *OpenSourceBountyDisputeView `json:"dispute,omitempty" gorm:"-"`
}

type OpenSourceBountyProjectDetail struct {
	Project    OpenSourceBountyProjectView     `json:"project"`
	Challenges []OpenSourceBountyChallengeView `json:"challenges"`
	Ledger     []OpenSourceBountyLedger        `json:"ledger"`
}

type OpenSourceBountyFeeConfig struct {
	RatePercent     float64 `json:"rate_percent"`
	RateBasisPoints int     `json:"rate_basis_points"`
}

type OpenSourceBountyPublicationCharge struct {
	GrossQuota         int `json:"gross_quota"`
	NetRewardQuota     int `json:"net_reward_quota"`
	EscrowQuota        int `json:"escrow_quota"`
	PlatformFeeQuota   int `json:"platform_fee_quota"`
	PlatformFeeRateBps int `json:"platform_fee_rate_bps"`
	TotalQuota         int `json:"total_quota"`
}

func GetOpenSourceBountyFeeConfig() OpenSourceBountyFeeConfig {
	common.OptionMapRWMutex.RLock()
	raw := common.OptionMap[OpenSourceBountyFeeRateOptionKey]
	common.OptionMapRWMutex.RUnlock()
	basisPoints, err := parseOpenSourceBountyFeeRateBasisPoints(raw)
	if err != nil {
		basisPoints = defaultOpenSourceBountyFeeRateBps
	}
	return OpenSourceBountyFeeConfig{RatePercent: float64(basisPoints) / 100, RateBasisPoints: basisPoints}
}

func parseOpenSourceBountyFeeRateBasisPoints(raw string) (int, error) {
	value := strings.TrimSpace(raw)
	if !openSourceBountyFeeRatePattern.MatchString(value) {
		return 0, fmt.Errorf("open-source bounty fee rate must be between 0 and 100 with at most two decimal places")
	}
	parts := strings.SplitN(value, ".", 2)
	whole, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, err
	}
	fraction := ""
	if len(parts) == 2 {
		fraction = parts[1]
	}
	for len(fraction) < 2 {
		fraction += "0"
	}
	fractionalBasisPoints := 0
	if fraction != "" {
		fractionalBasisPoints, err = strconv.Atoi(fraction)
		if err != nil {
			return 0, err
		}
	}
	return whole*100 + fractionalBasisPoints, nil
}

func CalculateOpenSourceBountyPublicationCharge(project *OpenSourceBountyProject) (OpenSourceBountyPublicationCharge, error) {
	grossTotal, err := bountyCharge(project.RewardQuota, project.RewardSlots)
	if err != nil {
		return OpenSourceBountyPublicationCharge{}, err
	}
	config := GetOpenSourceBountyFeeConfig()
	reward64, bps64 := int64(project.RewardQuota), int64(config.RateBasisPoints)
	feePerSlot64 := (reward64/10_000)*bps64 + ((reward64%10_000)*bps64+9_999)/10_000
	maxInt := int64(int(^uint(0) >> 1))
	if feePerSlot64 < 0 || feePerSlot64 >= reward64 {
		return OpenSourceBountyPublicationCharge{}, bountyError("OPEN_SOURCE_BOUNTY_INVALID_FEE", "platform fee leaves no contributor reward")
	}
	netReward64 := reward64 - feePerSlot64
	escrow64 := netReward64 * int64(project.RewardSlots)
	fee64 := feePerSlot64 * int64(project.RewardSlots)
	if escrow64 < 0 || fee64 < 0 || escrow64 > maxInt || fee64 > maxInt || escrow64+fee64 != int64(grossTotal) {
		return OpenSourceBountyPublicationCharge{}, bountyError("OPEN_SOURCE_BOUNTY_INVALID_QUOTA", "bounty quota is too large")
	}
	return OpenSourceBountyPublicationCharge{
		GrossQuota:       grossTotal,
		NetRewardQuota:   int(netReward64),
		EscrowQuota:      int(escrow64),
		PlatformFeeQuota: int(fee64), PlatformFeeRateBps: config.RateBasisPoints,
		TotalQuota: grossTotal,
	}, nil
}

func openSourceBountyPlatformFeeRecipient(tx *gorm.DB) (*User, error) {
	var recipient User
	err := tx.Select("id", "username").
		Where("role = ? AND status = ? AND deleted_at IS NULL", common.RoleRootUser, common.UserStatusEnabled).
		Order("id ASC").First(&recipient).Error
	if err != nil {
		return nil, bountyError("OPEN_SOURCE_BOUNTY_FEE_RECIPIENT_NOT_FOUND", "an enabled super administrator is required to receive the platform fee")
	}
	return &recipient, nil
}

func GetOpenSourceBountyPlatformFeeRecipient() (*User, error) {
	return openSourceBountyPlatformFeeRecipient(DB)
}

func normalizeBountyDraft(input OpenSourceBountyDraftInput) (OpenSourceBountyDraftInput, error) {
	repositoryUrl, err := NormalizeGithubRepositoryUrl(input.RepositoryUrl)
	if err != nil {
		return input, err
	}
	input.RepositoryUrl = repositoryUrl
	input.Title = strings.TrimSpace(input.Title)
	input.Description = strings.TrimSpace(input.Description)
	input.Rules = strings.TrimSpace(input.Rules)
	if len(input.Title) < 4 || len(input.Title) > 120 {
		return input, bountyError("OPEN_SOURCE_BOUNTY_INVALID_TITLE", "title must contain 4 to 120 characters")
	}
	if len(input.Description) < 20 || len(input.Description) > 2000 {
		return input, bountyError("OPEN_SOURCE_BOUNTY_INVALID_DESCRIPTION", "description must contain 20 to 2000 characters")
	}
	if len(input.Rules) < 20 || len(input.Rules) > 5000 {
		return input, bountyError("OPEN_SOURCE_BOUNTY_INVALID_RULES", "rules must contain 20 to 5000 characters")
	}
	if input.RewardQuota <= 0 {
		return input, bountyError("OPEN_SOURCE_BOUNTY_INVALID_QUOTA", "reward quota must be positive")
	}
	if input.RewardSlots < 1 || input.RewardSlots > 100 {
		return input, bountyError("OPEN_SOURCE_BOUNTY_INVALID_SLOTS", "reward slots must be between 1 and 100")
	}
	if _, err := bountyCharge(input.RewardQuota, input.RewardSlots); err != nil {
		return input, err
	}
	return input, nil
}

func bountyCharge(rewardQuota int, rewardSlots int) (int, error) {
	maxInt := int(^uint(0) >> 1)
	if rewardQuota <= 0 || rewardSlots <= 0 || rewardQuota > maxInt/rewardSlots {
		return 0, bountyError("OPEN_SOURCE_BOUNTY_INVALID_QUOTA", "bounty quota is too large")
	}
	return rewardQuota * rewardSlots, nil
}

func NormalizeGithubRepositoryUrl(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || !strings.EqualFold(u.Scheme, "https") || !strings.EqualFold(u.Hostname(), "github.com") {
		return "", bountyError("OPEN_SOURCE_BOUNTY_INVALID_REPOSITORY", "repository must be a public GitHub HTTPS URL")
	}
	parts := strings.Split(strings.Trim(strings.TrimSpace(u.Path), "/"), "/")
	if len(parts) != 2 {
		return "", bountyError("OPEN_SOURCE_BOUNTY_INVALID_REPOSITORY", "repository must point to a GitHub owner and repository")
	}
	owner := parts[0]
	repository := strings.TrimSuffix(parts[1], ".git")
	if !githubNamePattern.MatchString(owner) || !githubNamePattern.MatchString(repository) {
		return "", bountyError("OPEN_SOURCE_BOUNTY_INVALID_REPOSITORY", "repository contains an invalid GitHub owner or repository name")
	}
	return "https://github.com/" + owner + "/" + repository, nil
}

func normalizeGithubHandle(raw string) (string, error) {
	handle := strings.TrimPrefix(strings.TrimSpace(raw), "@")
	if !githubNamePattern.MatchString(handle) {
		return "", bountyError("OPEN_SOURCE_BOUNTY_INVALID_HANDLE", "GitHub handle is invalid")
	}
	return handle, nil
}

func normalizeGithubEvidence(raw string, repositoryUrl string, kind string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	u, err := url.Parse(raw)
	if err != nil || !strings.EqualFold(u.Scheme, "https") || !strings.EqualFold(u.Hostname(), "github.com") {
		return "", bountyError("OPEN_SOURCE_BOUNTY_INVALID_EVIDENCE", "submitted Issue and pull request links must be GitHub HTTPS URLs")
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) != 4 || parts[2] != kind {
		return "", bountyError("OPEN_SOURCE_BOUNTY_INVALID_EVIDENCE", "Issue or pull request URL has an invalid path")
	}
	if _, err := strconv.ParseInt(parts[3], 10, 64); err != nil {
		return "", bountyError("OPEN_SOURCE_BOUNTY_INVALID_EVIDENCE", "Issue or pull request number is invalid")
	}
	repository, err := NormalizeGithubRepositoryUrl("https://github.com/" + parts[0] + "/" + parts[1])
	if err != nil || !strings.EqualFold(repository, repositoryUrl) {
		return "", bountyError("OPEN_SOURCE_BOUNTY_EVIDENCE_REPOSITORY_MISMATCH", "every submitted Issue or pull request must belong to the bounty repository")
	}
	return repository + "/" + kind + "/" + parts[3], nil
}

func CreateOpenSourceBountyDraft(ownerUserId int, input OpenSourceBountyDraftInput) (*OpenSourceBountyProject, error) {
	if ownerUserId <= 0 {
		return nil, bountyError("OPEN_SOURCE_BOUNTY_UNAUTHORIZED", "invalid bounty owner")
	}
	normalized, err := normalizeBountyDraft(input)
	if err != nil {
		return nil, err
	}
	now := common.GetTimestamp()
	project := &OpenSourceBountyProject{
		OwnerUserId: ownerUserId, RepositoryUrl: normalized.RepositoryUrl, Title: normalized.Title,
		Description: normalized.Description, Rules: normalized.Rules,
		RewardQuota: normalized.RewardQuota, RewardSlots: normalized.RewardSlots, Status: OpenSourceBountyStatusDraft,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := DB.Create(project).Error; err != nil {
		return nil, err
	}
	return project, nil
}

func UpdateOpenSourceBountyDraft(ownerUserId int, projectId int, input OpenSourceBountyDraftInput) (*OpenSourceBountyProject, error) {
	normalized, err := normalizeBountyDraft(input)
	if err != nil {
		return nil, err
	}
	result := DB.Model(&OpenSourceBountyProject{}).
		Where("id = ? AND owner_user_id = ? AND status = ?", projectId, ownerUserId, OpenSourceBountyStatusDraft).
		Updates(map[string]interface{}{
			"repository_url": normalized.RepositoryUrl, "title": normalized.Title, "description": normalized.Description,
			"rules": normalized.Rules, "reward_quota": normalized.RewardQuota,
			"reward_slots": normalized.RewardSlots, "updated_at": common.GetTimestamp(),
		})
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected != 1 {
		return nil, bountyError("OPEN_SOURCE_BOUNTY_DRAFT_NOT_FOUND", "editable bounty draft was not found")
	}
	return GetOpenSourceBountyProject(projectId)
}

func DeleteOpenSourceBountyDraft(ownerUserId int, projectId int) error {
	return deleteOpenSourceBountyDraft(ownerUserId, projectId, nil)
}

func DeleteOpenSourceBountyDraftWithMCPConfirmation(ownerUserId int, projectId int, operation OpenSourceBountyMCPConfirmedOperation) error {
	return deleteOpenSourceBountyDraft(ownerUserId, projectId, &operation)
}

func deleteOpenSourceBountyDraft(ownerUserId int, projectId int, operation *OpenSourceBountyMCPConfirmedOperation) error {
	err := DB.Transaction(func(tx *gorm.DB) error {
		if operation != nil {
			if err := validateOpenSourceBountyMCPConfirmationTx(tx, ownerUserId, operation.ToolName, operation.PayloadHash, operation.State); err != nil {
				return err
			}
		}
		result := tx.Where("id = ? AND owner_user_id = ? AND status = ?", projectId, ownerUserId, OpenSourceBountyStatusDraft).
			Delete(&OpenSourceBountyProject{})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return bountyError("OPEN_SOURCE_BOUNTY_DRAFT_NOT_FOUND", "deletable bounty draft was not found")
		}
		if operation != nil {
			return completeOpenSourceBountyMCPOperationTx(tx, ownerUserId, operation.ToolName, operation.PayloadHash, operation.State, map[string]any{"project_id": projectId})
		}
		return nil
	})
	if err != nil && operation != nil && OpenSourceBountyErrorCode(err) == "OPEN_SOURCE_BOUNTY_MCP_CONFIRMATION_INVALID" {
		_ = ConsumeOpenSourceBountyMCPConfirmation(ownerUserId, operation.ToolName, operation.PayloadHash, operation.State)
	}
	return err
}

func PublishOpenSourceBounty(ownerUserId int, projectId int) (*OpenSourceBountyProject, int, error) {
	return publishOpenSourceBounty(ownerUserId, projectId, nil)
}

func PublishOpenSourceBountyWithMCPConfirmation(ownerUserId int, projectId int, operation OpenSourceBountyMCPConfirmedOperation) (*OpenSourceBountyProject, int, error) {
	return publishOpenSourceBounty(ownerUserId, projectId, &operation)
}

func publishOpenSourceBounty(ownerUserId int, projectId int, operation *OpenSourceBountyMCPConfirmedOperation) (*OpenSourceBountyProject, int, error) {
	chargedQuota := 0
	platformFeeQuota := 0
	platformFeeRecipientUserId := 0
	err := DB.Transaction(func(tx *gorm.DB) error {
		if operation != nil {
			if err := validateOpenSourceBountyMCPConfirmationTx(tx, ownerUserId, operation.ToolName, operation.PayloadHash, operation.State); err != nil {
				return err
			}
		}
		var project OpenSourceBountyProject
		if err := lockForUpdate(tx).Where("id = ? AND owner_user_id = ?", projectId, ownerUserId).First(&project).Error; err != nil {
			return bountyError("OPEN_SOURCE_BOUNTY_NOT_FOUND", "bounty project was not found")
		}
		if project.Status != OpenSourceBountyStatusDraft {
			return bountyError("OPEN_SOURCE_BOUNTY_INVALID_STATE", "only a draft bounty can be published")
		}
		charge, err := CalculateOpenSourceBountyPublicationCharge(&project)
		if err != nil {
			return err
		}
		chargedQuota = charge.TotalQuota
		result := UpdateWalletQuotaByDelta(
			tx.Model(&User{}).Where("id = ? AND deleted_at IS NULL AND quota >= ?", ownerUserId, chargedQuota),
			-chargedQuota,
		)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return bountyError("OPEN_SOURCE_BOUNTY_INSUFFICIENT_BALANCE", "insufficient balance to publish this bounty")
		}
		if charge.PlatformFeeQuota > 0 {
			recipient, err := openSourceBountyPlatformFeeRecipient(tx)
			if err != nil {
				return err
			}
			if operation != nil && operation.PlatformFeeRecipientUserId != recipient.Id {
				return bountyError("OPEN_SOURCE_BOUNTY_MCP_CONFIRMATION_INVALID", "the platform fee recipient changed; request a new confirmation")
			}
			result := UpdateWalletQuotaByDelta(
				tx.Model(&User{}).Where("id = ? AND role = ? AND status = ? AND deleted_at IS NULL", recipient.Id, common.RoleRootUser, common.UserStatusEnabled),
				charge.PlatformFeeQuota,
			)
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return bountyError("OPEN_SOURCE_BOUNTY_FEE_RECIPIENT_NOT_FOUND", "the super administrator fee account is unavailable")
			}
			platformFeeQuota = charge.PlatformFeeQuota
			platformFeeRecipientUserId = recipient.Id
		}
		now := common.GetTimestamp()
		if err := tx.Model(&project).Updates(map[string]interface{}{
			"status": OpenSourceBountyStatusPublished, "escrow_quota": charge.EscrowQuota,
			"net_reward_quota":      charge.NetRewardQuota,
			"platform_fee_rate_bps": charge.PlatformFeeRateBps, "platform_fee_quota": charge.PlatformFeeQuota,
			"published_at": now, "updated_at": now,
		}).Error; err != nil {
			return err
		}
		entries := []OpenSourceBountyLedger{
			{ProjectId: project.Id, UserId: ownerUserId, Kind: OpenSourceBountyLedgerEscrowFund, Quota: charge.EscrowQuota, CreatedAt: now},
		}
		if charge.PlatformFeeQuota > 0 {
			entries = append(entries, OpenSourceBountyLedger{
				ProjectId: project.Id, UserId: ownerUserId, CounterpartyUserId: platformFeeRecipientUserId,
				Kind: OpenSourceBountyLedgerPlatformFee, Quota: charge.PlatformFeeQuota, CreatedAt: now,
			})
		}
		if err := tx.Create(&entries).Error; err != nil {
			return err
		}
		if operation != nil {
			return completeOpenSourceBountyMCPOperationTx(tx, ownerUserId, operation.ToolName, operation.PayloadHash, operation.State, map[string]any{
				"project_id": project.Id, "charged_quota": chargedQuota,
			})
		}
		return nil
	})
	if err != nil {
		if operation != nil && OpenSourceBountyErrorCode(err) == "OPEN_SOURCE_BOUNTY_MCP_CONFIRMATION_INVALID" {
			_ = ConsumeOpenSourceBountyMCPConfirmation(ownerUserId, operation.ToolName, operation.PayloadHash, operation.State)
		}
		return nil, 0, err
	}
	if ownerUserId == platformFeeRecipientUserId {
		if err := cacheDecrUserQuota(ownerUserId, int64(chargedQuota-platformFeeQuota)); err != nil {
			common.SysLog("failed to update publisher quota cache after self-receiving the bounty platform fee: " + err.Error())
		}
	} else {
		if err := cacheDecrUserQuota(ownerUserId, int64(chargedQuota)); err != nil {
			common.SysLog("failed to decrease user quota cache after publishing open-source bounty: " + err.Error())
		}
		if platformFeeQuota > 0 {
			if err := cacheIncrUserQuota(platformFeeRecipientUserId, int64(platformFeeQuota)); err != nil {
				common.SysLog("failed to increase super administrator quota cache after bounty publication: " + err.Error())
			}
		}
	}
	RecordLog(ownerUserId, LogTypeSystem, fmt.Sprintf("Published open-source bounty %d with %d gross listing quota", projectId, chargedQuota))
	if platformFeeQuota > 0 {
		RecordLog(platformFeeRecipientUserId, LogTypeTopup, fmt.Sprintf("Received %d platform fee quota from open-source bounty %d", platformFeeQuota, projectId))
	}
	project, err := GetOpenSourceBountyProject(projectId)
	return project, chargedQuota, err
}

func SetOpenSourceBountyPaused(ownerUserId int, projectId int, paused bool) (*OpenSourceBountyProject, error) {
	from, to := OpenSourceBountyStatusPublished, OpenSourceBountyStatusPaused
	if !paused {
		from, to = OpenSourceBountyStatusPaused, OpenSourceBountyStatusPublished
	}
	result := DB.Model(&OpenSourceBountyProject{}).
		Where("id = ? AND owner_user_id = ? AND status = ?", projectId, ownerUserId, from).
		Updates(map[string]interface{}{"status": to, "updated_at": common.GetTimestamp()})
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected != 1 {
		return nil, bountyError("OPEN_SOURCE_BOUNTY_INVALID_STATE", "bounty cannot change to the requested state")
	}
	return GetOpenSourceBountyProject(projectId)
}

func CloseOpenSourceBounty(ownerUserId int, projectId int) (*OpenSourceBountyProject, int, error) {
	return closeOpenSourceBounty(ownerUserId, projectId, nil)
}

func CloseOpenSourceBountyWithMCPConfirmation(ownerUserId int, projectId int, operation OpenSourceBountyMCPConfirmedOperation) (*OpenSourceBountyProject, int, error) {
	return closeOpenSourceBounty(ownerUserId, projectId, &operation)
}

func closeOpenSourceBounty(ownerUserId int, projectId int, operation *OpenSourceBountyMCPConfirmedOperation) (*OpenSourceBountyProject, int, error) {
	refundedQuota := 0
	err := DB.Transaction(func(tx *gorm.DB) error {
		if operation != nil {
			if err := validateOpenSourceBountyMCPConfirmationTx(tx, ownerUserId, operation.ToolName, operation.PayloadHash, operation.State); err != nil {
				return err
			}
		}
		var project OpenSourceBountyProject
		if err := lockForUpdate(tx).Where("id = ? AND owner_user_id = ?", projectId, ownerUserId).First(&project).Error; err != nil {
			return bountyError("OPEN_SOURCE_BOUNTY_NOT_FOUND", "bounty project was not found")
		}
		if project.Status != OpenSourceBountyStatusPublished && project.Status != OpenSourceBountyStatusPaused {
			return bountyError("OPEN_SOURCE_BOUNTY_INVALID_STATE", "only a published or paused bounty can be closed")
		}
		var active int64
		if err := tx.Model(&OpenSourceBountyChallenge{}).
			Where("project_id = ? AND status IN ?", projectId, []string{OpenSourceBountyChallengeAccepted, OpenSourceBountyChallengeSubmitted}).
			Count(&active).Error; err != nil {
			return err
		}
		if active > 0 {
			return bountyError("OPEN_SOURCE_BOUNTY_ACTIVE_CHALLENGES", "cancel unsubmitted challenges or review submitted work before closing the bounty")
		}
		var openDisputes int64
		if err := tx.Model(&OpenSourceBountyDispute{}).
			Where("project_id = ? AND status = ?", projectId, OpenSourceBountyDisputeOpen).
			Count(&openDisputes).Error; err != nil {
			return err
		}
		if openDisputes > 0 {
			return bountyError("OPEN_SOURCE_BOUNTY_OPEN_DISPUTES", "resolve open bounty disputes before closing or refunding escrow")
		}
		var appealableRejections int64
		appealCutoff := common.GetTimestamp() - OpenSourceBountyAppealWindowSeconds
		if err := tx.Model(&OpenSourceBountyChallenge{}).
			Where(`project_id = ? AND status = ? AND rejected_at > ? AND NOT EXISTS (
				SELECT 1 FROM open_source_bounty_disputes dispute
				WHERE dispute.challenge_id = open_source_bounty_challenges.id AND dispute.status IN ?
			)`, projectId, OpenSourceBountyChallengeRejected, appealCutoff, []string{OpenSourceBountyDisputeResolvedPaid, OpenSourceBountyDisputeResolvedDenied}).
			Count(&appealableRejections).Error; err != nil {
			return err
		}
		if appealableRejections > 0 {
			return bountyError("OPEN_SOURCE_BOUNTY_APPEAL_WINDOW", "rejected challenges remain appealable for seven days before escrow can be refunded")
		}
		refundedQuota = project.EscrowQuota
		if refundedQuota > 0 {
			result := UpdateWalletQuotaByDelta(
				tx.Model(&User{}).Where("id = ? AND deleted_at IS NULL", ownerUserId),
				refundedQuota,
			)
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return bountyError("OPEN_SOURCE_BOUNTY_OWNER_NOT_FOUND", "bounty owner was not found")
			}
		}
		now := common.GetTimestamp()
		if err := tx.Model(&project).Updates(map[string]interface{}{
			"status": OpenSourceBountyStatusClosed, "escrow_quota": 0, "closed_at": now, "updated_at": now,
		}).Error; err != nil {
			return err
		}
		if refundedQuota > 0 {
			if err := tx.Create(&OpenSourceBountyLedger{
				ProjectId: projectId, UserId: ownerUserId, Kind: OpenSourceBountyLedgerEscrowRefund,
				Quota: refundedQuota, CreatedAt: now,
			}).Error; err != nil {
				return err
			}
		}
		if operation != nil {
			return completeOpenSourceBountyMCPOperationTx(tx, ownerUserId, operation.ToolName, operation.PayloadHash, operation.State, map[string]any{
				"project_id": projectId, "refunded_quota": refundedQuota,
			})
		}
		return nil
	})
	if err != nil {
		if operation != nil && OpenSourceBountyErrorCode(err) == "OPEN_SOURCE_BOUNTY_MCP_CONFIRMATION_INVALID" {
			_ = ConsumeOpenSourceBountyMCPConfirmation(ownerUserId, operation.ToolName, operation.PayloadHash, operation.State)
		}
		return nil, 0, err
	}
	if refundedQuota > 0 {
		if err := cacheIncrUserQuota(ownerUserId, int64(refundedQuota)); err != nil {
			common.SysLog("failed to increase user quota cache after closing open-source bounty: " + err.Error())
		}
	}
	RecordLog(ownerUserId, LogTypeSystem, fmt.Sprintf("Closed open-source bounty %d and refunded %d quota", projectId, refundedQuota))
	project, err := GetOpenSourceBountyProject(projectId)
	return project, refundedQuota, err
}

// ArchiveOpenSourceBounty hides a completed or closed bounty from the owner's
// default project list without deleting its lifecycle, evidence, or ledger.
// Archiving is intentionally reversible so it cannot be used as a destructive
// substitute for deleting a draft.
func ArchiveOpenSourceBounty(ownerUserId int, projectId int) (*OpenSourceBountyProject, error) {
	return setOpenSourceBountyArchived(ownerUserId, projectId, true)
}

// UnarchiveOpenSourceBounty restores an archived completed or closed bounty to
// the owner's active project list.
func UnarchiveOpenSourceBounty(ownerUserId int, projectId int) (*OpenSourceBountyProject, error) {
	return setOpenSourceBountyArchived(ownerUserId, projectId, false)
}

func setOpenSourceBountyArchived(ownerUserId int, projectId int, archived bool) (*OpenSourceBountyProject, error) {
	if ownerUserId <= 0 || projectId <= 0 {
		return nil, bountyError("OPEN_SOURCE_BOUNTY_NOT_FOUND", "bounty project was not found")
	}
	var project OpenSourceBountyProject
	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := lockForUpdate(tx).
			Where("id = ? AND owner_user_id = ?", projectId, ownerUserId).
			First(&project).Error; err != nil {
			return bountyError("OPEN_SOURCE_BOUNTY_NOT_FOUND", "bounty project was not found")
		}
		if project.Status != OpenSourceBountyStatusCompleted && project.Status != OpenSourceBountyStatusClosed {
			return bountyError("OPEN_SOURCE_BOUNTY_ARCHIVE_UNAVAILABLE", "only completed or closed bounties can be archived")
		}
		alreadyArchived := project.ArchivedAt > 0
		if alreadyArchived == archived {
			return nil
		}
		updates := map[string]interface{}{"archived_at": int64(0), "updated_at": common.GetTimestamp()}
		if archived {
			updates["archived_at"] = common.GetTimestamp()
		}
		if err := tx.Model(&project).Updates(updates).Error; err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	action := "Unarchived"
	if archived {
		action = "Archived"
	}
	RecordLog(ownerUserId, LogTypeSystem, fmt.Sprintf("%s open-source bounty %d", action, projectId))
	return GetOpenSourceBountyProject(projectId)
}

func GetOpenSourceBountyProject(projectId int) (*OpenSourceBountyProject, error) {
	var project OpenSourceBountyProject
	if err := DB.First(&project, "id = ?", projectId).Error; err != nil {
		return nil, bountyError("OPEN_SOURCE_BOUNTY_NOT_FOUND", "bounty project was not found")
	}
	return &project, nil
}

func openSourceBountyProjectQuery() *gorm.DB {
	appealCutoff := common.GetTimestamp() - OpenSourceBountyAppealWindowSeconds
	return DB.Table("open_source_bounty_projects AS p").
		Select(`p.*, u.username AS owner_username,
			(SELECT COUNT(*) FROM open_source_bounty_challenges c WHERE c.project_id = p.id AND (
				c.status IN ('accepted','submitted') OR
				(c.status = 'rejected' AND c.rejected_at > ? AND NOT EXISTS (
					SELECT 1 FROM open_source_bounty_disputes resolved_dispute WHERE resolved_dispute.challenge_id = c.id AND resolved_dispute.status IN ('resolved_paid','resolved_denied')
				)) OR EXISTS (
					SELECT 1 FROM open_source_bounty_disputes dispute WHERE dispute.challenge_id = c.id AND dispute.status = 'open'
				)
			)) AS active_challenge_count,
			(SELECT COUNT(*) FROM open_source_bounty_challenges c WHERE c.project_id = p.id AND c.status = 'approved') AS approved_challenge_count,
			COALESCE((SELECT AVG(c.contributor_rating_score) FROM open_source_bounty_challenges c JOIN open_source_bounty_projects rated_project ON rated_project.id = c.project_id WHERE rated_project.owner_user_id = p.owner_user_id AND c.contributor_rating_score > 0), 0) AS owner_rating_average,
			(SELECT COUNT(*) FROM open_source_bounty_challenges c JOIN open_source_bounty_projects rated_project ON rated_project.id = c.project_id WHERE rated_project.owner_user_id = p.owner_user_id AND c.contributor_rating_score > 0) AS owner_rating_count,
			(SELECT COUNT(*) FROM open_source_bounty_ledgers heart WHERE heart.user_id = p.owner_user_id AND heart.kind = 'tip_transfer' AND heart.thanked_at > 0) AS owner_thank_heart_count`, appealCutoff).
		Joins("JOIN users u ON u.id = p.owner_user_id AND u.deleted_at IS NULL")
}

func openSourceBountyTipNotificationQuery() *gorm.DB {
	return DB.Table("open_source_bounty_ledgers AS tip").
		Select(`tip.id, tip.project_id, tip.challenge_id, tip.user_id AS sender_user_id,
			sender.username AS sender_username, project.title AS project_title, tip.quota, tip.note,
			tip.recipient_read_at, tip.thanked_at, tip.created_at`).
		Joins("JOIN users sender ON sender.id = tip.user_id AND sender.deleted_at IS NULL").
		Joins("JOIN open_source_bounty_projects project ON project.id = tip.project_id")
}

func openSourceBountyNotificationQuery(db *gorm.DB) *gorm.DB {
	return db.Table("open_source_bounty_ledgers AS notification").
		Select(`notification.id, notification.project_id, notification.challenge_id,
			notification.user_id AS sender_user_id, sender.username AS sender_username,
			notification.kind, project.title AS project_title, notification.quota, notification.note,
			notification.recipient_read_at, notification.thanked_at, notification.created_at`).
		Joins("JOIN users sender ON sender.id = notification.user_id AND sender.deleted_at IS NULL").
		Joins("JOIN open_source_bounty_projects project ON project.id = notification.project_id")
}

func openSourceBountyNotificationKinds() []string {
	return []string{
		OpenSourceBountyLedgerTipTransfer,
		OpenSourceBountyLedgerRewardTransfer,
		OpenSourceBountyLedgerDisputeRewardTransfer,
	}
}

func ListOpenSourceBountyNotifications(recipientUserId int, limit int) ([]OpenSourceBountyNotification, error) {
	if limit < 1 || limit > 100 {
		limit = 50
	}
	items := make([]OpenSourceBountyNotification, 0)
	err := openSourceBountyNotificationQuery(DB).
		Where("notification.kind IN ? AND notification.counterparty_user_id = ?", openSourceBountyNotificationKinds(), recipientUserId).
		Order("notification.created_at DESC, notification.id DESC").Limit(limit).Scan(&items).Error
	return items, err
}

func MarkOpenSourceBountyNotificationsRead(recipientUserId int) error {
	return DB.Model(&OpenSourceBountyLedger{}).
		Where("kind IN ? AND counterparty_user_id = ? AND recipient_read_at = 0", openSourceBountyNotificationKinds(), recipientUserId).
		Update("recipient_read_at", common.GetTimestamp()).Error
}

func ListOpenSourceBountyTipNotifications(recipientUserId int, limit int) ([]OpenSourceBountyTipNotification, error) {
	if limit < 1 || limit > 100 {
		limit = 50
	}
	items := make([]OpenSourceBountyTipNotification, 0)
	err := openSourceBountyTipNotificationQuery().
		Where("tip.kind = ? AND tip.counterparty_user_id = ?", OpenSourceBountyLedgerTipTransfer, recipientUserId).
		Order("tip.created_at DESC, tip.id DESC").Limit(limit).Scan(&items).Error
	return items, err
}

func MarkOpenSourceBountyTipNotificationsRead(recipientUserId int) error {
	return DB.Model(&OpenSourceBountyLedger{}).
		Where("kind = ? AND counterparty_user_id = ? AND recipient_read_at = 0", OpenSourceBountyLedgerTipTransfer, recipientUserId).
		Update("recipient_read_at", common.GetTimestamp()).Error
}

func ThankOpenSourceBountyTip(recipientUserId int, tipId int) (*OpenSourceBountyTipNotification, error) {
	if tipId <= 0 {
		return nil, bountyError("OPEN_SOURCE_BOUNTY_INVALID_ID", "invalid open-source bounty tip identifier")
	}
	now := common.GetTimestamp()
	result := DB.Model(&OpenSourceBountyLedger{}).
		Where("id = ? AND kind = ? AND counterparty_user_id = ? AND thanked_at = 0", tipId, OpenSourceBountyLedgerTipTransfer, recipientUserId).
		Updates(map[string]any{"thanked_at": now, "recipient_read_at": now})
	if result.Error != nil {
		return nil, result.Error
	}
	var notification OpenSourceBountyTipNotification
	if err := openSourceBountyTipNotificationQuery().
		Where("tip.id = ? AND tip.kind = ? AND tip.counterparty_user_id = ?", tipId, OpenSourceBountyLedgerTipTransfer, recipientUserId).
		Scan(&notification).Error; err != nil {
		return nil, err
	}
	if notification.Id == 0 {
		return nil, bountyError("OPEN_SOURCE_BOUNTY_TIP_NOT_FOUND", "tip notification was not found")
	}
	if result.RowsAffected > 0 {
		RecordLog(recipientUserId, LogTypeSystem, fmt.Sprintf("Thanked open-source bounty tip %d", tipId))
		RecordLog(notification.SenderUserId, LogTypeSystem, fmt.Sprintf("Received a thank heart for open-source bounty tip %d", tipId))
	}
	return &notification, nil
}

func openSourceBountyViewerChallengePriority(status string) int {
	switch status {
	case OpenSourceBountyChallengeApproved:
		return 4
	case OpenSourceBountyChallengeAccepted, OpenSourceBountyChallengeSubmitted:
		return 3
	case OpenSourceBountyChallengeRejected:
		return 2
	case OpenSourceBountyChallengeWithdrawn, OpenSourceBountyChallengeCancelled:
		return 1
	default:
		return 0
	}
}

func attachViewerChallenges(views []OpenSourceBountyProjectView, viewerUserId int) error {
	if viewerUserId <= 0 || len(views) == 0 {
		return nil
	}
	ids := make([]int, 0, len(views))
	for _, view := range views {
		ids = append(ids, view.Id)
	}
	var challenges []OpenSourceBountyChallenge
	if err := DB.Where("participant_user_id = ? AND project_id IN ?", viewerUserId, ids).Find(&challenges).Error; err != nil {
		return err
	}
	byProject := make(map[int]*OpenSourceBountyChallenge, len(challenges))
	for i := range challenges {
		challenge := challenges[i]
		current, exists := byProject[challenge.ProjectId]
		candidatePriority := openSourceBountyViewerChallengePriority(challenge.Status)
		currentPriority := -1
		if exists {
			currentPriority = openSourceBountyViewerChallengePriority(current.Status)
		}
		if !exists || candidatePriority > currentPriority ||
			(candidatePriority == currentPriority && challenge.Id > current.Id) {
			byProject[challenge.ProjectId] = &challenge
		}
	}
	for i := range views {
		views[i].ViewerChallenge = byProject[views[i].Id]
	}
	return nil
}

func ListOpenSourceBounties(viewerUserId int, page int, pageSize int) ([]OpenSourceBountyProjectView, int64, error) {
	viewerUserId = openSourceBountyPrivateViewerId(viewerUserId)
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > maxOpenSourceBountyPageSize {
		pageSize = 20
	}
	statuses := []string{OpenSourceBountyStatusPublished, OpenSourceBountyStatusPaused}
	var total int64
	if err := DB.Model(&OpenSourceBountyProject{}).Where("status IN ?", statuses).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	views := make([]OpenSourceBountyProjectView, 0)
	err := openSourceBountyProjectQuery().Where("p.status IN ?", statuses).
		Order("p.reward_quota DESC").
		Order("CASE WHEN p.status = 'published' THEN 0 ELSE 1 END ASC").
		Order("p.published_at ASC, p.id ASC").
		Offset((page - 1) * pageSize).Limit(pageSize).Scan(&views).Error
	if err != nil {
		return nil, 0, err
	}
	if err := attachViewerChallenges(views, viewerUserId); err != nil {
		return nil, 0, err
	}
	return views, total, nil
}

func ListOwnedOpenSourceBounties(ownerUserId int) ([]OpenSourceBountyProjectView, error) {
	return ListOwnedOpenSourceBountiesFiltered(ownerUserId, false)
}

// ListOwnedOpenSourceBountiesLimited is the bounded read used by the
// assistant. The extra row lets the caller report truncation without
// materializing an unbounded owner history.
func ListOwnedOpenSourceBountiesLimited(ownerUserId int, limit int) ([]OpenSourceBountyProjectView, error) {
	views := make([]OpenSourceBountyProjectView, 0)
	query := openSourceBountyProjectQuery().Where("p.owner_user_id = ? AND p.archived_at = 0", ownerUserId).
		Order("p.created_at DESC, p.id DESC")
	if limit > 0 {
		query = query.Limit(limit)
	}
	if err := query.Scan(&views).Error; err != nil {
		return nil, err
	}
	return views, nil
}

// ListOwnedOpenSourceBountiesFiltered returns either active (default) or
// archived owner projects. Keeping the filter in SQL prevents archived rows
// from consuming pagination/list rendering capacity.
func ListOwnedOpenSourceBountiesFiltered(ownerUserId int, archived bool) ([]OpenSourceBountyProjectView, error) {
	views := make([]OpenSourceBountyProjectView, 0)
	archiveFilter := "p.archived_at = 0"
	if archived {
		archiveFilter = "p.archived_at > 0"
	}
	if err := openSourceBountyProjectQuery().Where("p.owner_user_id = ? AND "+archiveFilter, ownerUserId).
		Order("p.created_at DESC, p.id DESC").Scan(&views).Error; err != nil {
		return nil, err
	}
	return views, nil
}

func GetOpenSourceBountyDetail(viewerUserId int, projectId int) (*OpenSourceBountyProjectDetail, error) {
	viewerUserId = openSourceBountyPrivateViewerId(viewerUserId)
	var view OpenSourceBountyProjectView
	if err := openSourceBountyProjectQuery().Where("p.id = ?", projectId).Scan(&view).Error; err != nil {
		return nil, err
	}
	if view.Id == 0 {
		return nil, bountyError("OPEN_SOURCE_BOUNTY_NOT_FOUND", "bounty project was not found")
	}
	if (view.Status == OpenSourceBountyStatusDraft || view.Status == OpenSourceBountyStatusClosed || view.ArchivedAt > 0) && view.OwnerUserId != viewerUserId {
		return nil, bountyError("OPEN_SOURCE_BOUNTY_NOT_FOUND", "bounty project was not found")
	}
	views := []OpenSourceBountyProjectView{view}
	if err := attachViewerChallenges(views, viewerUserId); err != nil {
		return nil, err
	}
	detail := &OpenSourceBountyProjectDetail{Project: views[0], Challenges: []OpenSourceBountyChallengeView{}, Ledger: []OpenSourceBountyLedger{}}
	if view.OwnerUserId == viewerUserId {
		if err := openSourceBountyChallengeViewQuery(DB).Where("c.project_id = ?", projectId).
			Order("c.created_at DESC, c.id DESC").Scan(&detail.Challenges).Error; err != nil {
			return nil, err
		}
		if err := attachOpenSourceBountyDisputes(detail.Challenges); err != nil {
			return nil, err
		}
		if err := DB.Where("project_id = ?", projectId).Order("created_at DESC, id DESC").Find(&detail.Ledger).Error; err != nil {
			return nil, err
		}
	}
	return detail, nil
}

func attachOpenSourceBountyDisputes(views []OpenSourceBountyChallengeView) error {
	if len(views) == 0 {
		return nil
	}
	challengeIds := make([]int, 0, len(views))
	for _, view := range views {
		challengeIds = append(challengeIds, view.Id)
	}
	var disputes []OpenSourceBountyDisputeView
	if err := openSourceBountyDisputeViewQuery().Where("d.challenge_id IN ?", challengeIds).
		Order("d.created_at DESC, d.id DESC").Scan(&disputes).Error; err != nil {
		return err
	}
	byChallenge := make(map[int]*OpenSourceBountyDisputeView, len(disputes))
	for i := range disputes {
		dispute := disputes[i]
		if _, exists := byChallenge[dispute.ChallengeId]; !exists {
			byChallenge[dispute.ChallengeId] = &dispute
		}
	}
	for i := range views {
		views[i].Dispute = byChallenge[views[i].Id]
	}
	return nil
}

func openSourceBountyChallengeViewQuery(db *gorm.DB) *gorm.DB {
	return db.Table("open_source_bounty_challenges AS c").
		Select(`c.*, participant.username AS participant_username, p.title AS project_title, p.repository_url, owner.username AS owner_username,
			COALESCE((SELECT AVG(history.owner_rating_score) FROM open_source_bounty_challenges history WHERE history.participant_user_id = c.participant_user_id AND history.owner_rating_score > 0 AND history.owner_rating_overturned = false), 0) AS participant_rating_average,
			(SELECT COUNT(*) FROM open_source_bounty_challenges history WHERE history.participant_user_id = c.participant_user_id AND history.owner_rating_score > 0 AND history.owner_rating_overturned = false) AS participant_rating_count,
			COALESCE((SELECT AVG(history.contributor_rating_score) FROM open_source_bounty_challenges history JOIN open_source_bounty_projects history_project ON history_project.id = history.project_id WHERE history_project.owner_user_id = p.owner_user_id AND history.contributor_rating_score > 0), 0) AS owner_rating_average,
			(SELECT COUNT(*) FROM open_source_bounty_challenges history JOIN open_source_bounty_projects history_project ON history_project.id = history.project_id WHERE history_project.owner_user_id = p.owner_user_id AND history.contributor_rating_score > 0) AS owner_rating_count`).
		Joins("JOIN users participant ON participant.id = c.participant_user_id").
		Joins("JOIN open_source_bounty_projects p ON p.id = c.project_id").
		Joins("JOIN users owner ON owner.id = p.owner_user_id")
}

func ListAcceptedOpenSourceBounties(participantUserId int) ([]OpenSourceBountyChallengeView, error) {
	views := make([]OpenSourceBountyChallengeView, 0)
	if err := openSourceBountyChallengeViewQuery(DB).Where("c.participant_user_id = ?", participantUserId).
		Order("c.updated_at DESC, c.id DESC").Scan(&views).Error; err != nil {
		return nil, err
	}
	if err := attachOpenSourceBountyDisputes(views); err != nil {
		return nil, err
	}
	return views, nil
}

// ListAcceptedOpenSourceBountiesLimited is the bounded assistant read. The
// extra row is retained so the presentation layer can mark the response as
// truncated while keeping dispute enrichment bounded as well.
func ListAcceptedOpenSourceBountiesLimited(participantUserId int, limit int) ([]OpenSourceBountyChallengeView, error) {
	views := make([]OpenSourceBountyChallengeView, 0)
	query := openSourceBountyChallengeViewQuery(DB).
		Where("c.participant_user_id = ?", participantUserId).
		Order("c.updated_at DESC, c.id DESC")
	if limit > 0 {
		query = query.Limit(limit)
	}
	if err := query.Scan(&views).Error; err != nil {
		return nil, err
	}
	if err := attachOpenSourceBountyDisputes(views); err != nil {
		return nil, err
	}
	return views, nil
}

func AcceptOpenSourceBounty(participantUserId int, projectId int, rawGithubHandle string) (*OpenSourceBountyChallenge, error) {
	handle, err := normalizeGithubHandle(rawGithubHandle)
	if err != nil {
		return nil, err
	}
	var challenge OpenSourceBountyChallenge
	err = DB.Transaction(func(tx *gorm.DB) error {
		var project OpenSourceBountyProject
		if err := lockForUpdate(tx).Where("id = ?", projectId).First(&project).Error; err != nil {
			return bountyError("OPEN_SOURCE_BOUNTY_NOT_FOUND", "bounty project was not found")
		}
		if project.Status != OpenSourceBountyStatusPublished {
			return bountyError("OPEN_SOURCE_BOUNTY_NOT_ACCEPTING", "bounty is not accepting new challenges")
		}
		if project.OwnerUserId == participantUserId {
			return bountyError("OPEN_SOURCE_BOUNTY_OWNER_CANNOT_ACCEPT", "bounty owner cannot accept their own challenge")
		}
		appealCutoff := common.GetTimestamp() - OpenSourceBountyAppealWindowSeconds
		// Do not materialize every historical attempt for this user/project. A
		// contributor can accumulate an arbitrary number of rejected attempts,
		// and the old slice made acceptance memory grow with that history while
		// issuing one dispute count query per rejected row. The state checks are
		// equivalent when expressed as bounded existence queries.
		var activeAttempts int64
		if err := tx.Model(&OpenSourceBountyChallenge{}).
			Where("project_id = ? AND participant_user_id = ? AND status IN ?", projectId, participantUserId,
				[]string{OpenSourceBountyChallengeAccepted, OpenSourceBountyChallengeSubmitted, OpenSourceBountyChallengeApproved}).
			Count(&activeAttempts).Error; err != nil {
			return err
		}
		if activeAttempts > 0 {
			return bountyError("OPEN_SOURCE_BOUNTY_ALREADY_ACCEPTED", "this bounty already has an active or completed attempt")
		}

		var retryPending int64
		if err := tx.Model(&OpenSourceBountyChallenge{}).
			Where(`project_id = ? AND participant_user_id = ? AND status = ? AND (
				(
					rejected_at > ? AND NOT EXISTS (
					SELECT 1 FROM open_source_bounty_disputes resolved_dispute
					WHERE resolved_dispute.challenge_id = open_source_bounty_challenges.id AND resolved_dispute.status = ?
				)
				) OR
				EXISTS (
					SELECT 1 FROM open_source_bounty_disputes dispute
					WHERE dispute.challenge_id = open_source_bounty_challenges.id AND dispute.status = ?
				)
			)`, projectId, participantUserId, OpenSourceBountyChallengeRejected, appealCutoff, OpenSourceBountyDisputeResolvedDenied, OpenSourceBountyDisputeOpen).
			Count(&retryPending).Error; err != nil {
			return err
		}
		if retryPending > 0 {
			return bountyError("OPEN_SOURCE_BOUNTY_RETRY_PENDING", "wait until the rejected attempt's dispute is resolved or its seven-day appeal window ends")
		}
		var occupied int64
		if err := tx.Model(&OpenSourceBountyChallenge{}).Where(`project_id = ? AND (
			status IN ? OR
			(status = ? AND rejected_at > ? AND NOT EXISTS (
				SELECT 1 FROM open_source_bounty_disputes resolved_dispute
				WHERE resolved_dispute.challenge_id = open_source_bounty_challenges.id AND resolved_dispute.status IN ?
			)) OR EXISTS (
				SELECT 1 FROM open_source_bounty_disputes dispute
				WHERE dispute.challenge_id = open_source_bounty_challenges.id AND dispute.status = ?
			)
		)`, projectId, []string{OpenSourceBountyChallengeAccepted, OpenSourceBountyChallengeSubmitted, OpenSourceBountyChallengeApproved},
			OpenSourceBountyChallengeRejected, appealCutoff, []string{OpenSourceBountyDisputeResolvedPaid, OpenSourceBountyDisputeResolvedDenied}, OpenSourceBountyDisputeOpen).Count(&occupied).Error; err != nil {
			return err
		}
		if occupied >= int64(project.RewardSlots) {
			return bountyError("OPEN_SOURCE_BOUNTY_FULL", "all reward slots are currently occupied")
		}
		now := common.GetTimestamp()
		netRewardQuota := project.NetRewardQuota
		if netRewardQuota <= 0 {
			netRewardQuota = project.RewardQuota
		}
		challenge = OpenSourceBountyChallenge{
			ProjectId: projectId, ParticipantUserId: participantUserId, GithubHandle: handle,
			Status: OpenSourceBountyChallengeAccepted, RewardQuota: netRewardQuota,
			AcceptedAt: now, CreatedAt: now, UpdatedAt: now,
		}
		return tx.Create(&challenge).Error
	})
	if err != nil {
		return nil, err
	}
	return &challenge, DB.First(&challenge, challenge.Id).Error
}

func SubmitOpenSourceBountyChallenge(participantUserId int, projectId int, issueUrl string, pullRequestUrl string, submissionNote string) (*OpenSourceBountyChallenge, error) {
	submissionNote = strings.TrimSpace(submissionNote)
	if len(submissionNote) > 2000 {
		return nil, bountyError("OPEN_SOURCE_BOUNTY_INVALID_SUBMISSION", "completion note must contain at most 2000 characters")
	}
	var challenge OpenSourceBountyChallenge
	err := DB.Transaction(func(tx *gorm.DB) error {
		var project OpenSourceBountyProject
		if err := tx.Where("id = ?", projectId).First(&project).Error; err != nil {
			return bountyError("OPEN_SOURCE_BOUNTY_NOT_FOUND", "bounty project was not found")
		}
		if err := lockForUpdate(tx).Where("project_id = ? AND participant_user_id = ? AND status = ?", projectId, participantUserId, OpenSourceBountyChallengeAccepted).
			Order("id DESC").First(&challenge).Error; err != nil {
			return bountyError("OPEN_SOURCE_BOUNTY_CHALLENGE_NOT_FOUND", "accepted challenge was not found")
		}
		if challenge.Status != OpenSourceBountyChallengeAccepted {
			return bountyError("OPEN_SOURCE_BOUNTY_INVALID_CHALLENGE_STATE", "only an accepted challenge can be submitted")
		}
		normalizedIssue, err := normalizeGithubEvidence(issueUrl, project.RepositoryUrl, "issues")
		if err != nil {
			return err
		}
		normalizedPullRequest, err := normalizeGithubEvidence(pullRequestUrl, project.RepositoryUrl, "pull")
		if err != nil {
			return err
		}
		if normalizedIssue == "" && normalizedPullRequest == "" {
			return bountyError("OPEN_SOURCE_BOUNTY_EVIDENCE_REQUIRED", "provide at least one GitHub Issue or pull request URL")
		}
		var duplicate int64
		if normalizedPullRequest != "" {
			if err := tx.Model(&OpenSourceBountyChallenge{}).
				Where("project_id = ? AND id <> ? AND pull_request_url = ? AND status IN ?", projectId, challenge.Id, normalizedPullRequest,
					[]string{OpenSourceBountyChallengeSubmitted, OpenSourceBountyChallengeApproved}).Count(&duplicate).Error; err != nil {
				return err
			}
		}
		if duplicate > 0 {
			return bountyError("OPEN_SOURCE_BOUNTY_DUPLICATE_PULL_REQUEST", "this pull request has already been submitted")
		}
		now := common.GetTimestamp()
		return tx.Model(&challenge).Updates(map[string]interface{}{
			"issue_url": normalizedIssue, "pull_request_url": normalizedPullRequest,
			"submission_note": submissionNote,
			"status":          OpenSourceBountyChallengeSubmitted, "submitted_at": now, "updated_at": now,
		}).Error
	})
	if err != nil {
		return nil, err
	}
	return &challenge, DB.First(&challenge, challenge.Id).Error
}

func WithdrawOpenSourceBountyChallenge(participantUserId int, challengeId int) (*OpenSourceBountyChallenge, error) {
	return withdrawOpenSourceBountyChallenge(participantUserId, challengeId, nil)
}

// CancelOpenSourceBountyChallenge lets the publisher release a reward slot
// that was reserved but has no submitted work. Submitted work must still go
// through review so cancellation cannot bypass payout or dispute protection.
func CancelOpenSourceBountyChallenge(ownerUserId int, challengeId int) (*OpenSourceBountyChallenge, error) {
	return cancelOpenSourceBountyChallenge(ownerUserId, challengeId, nil)
}

func CancelOpenSourceBountyChallengeWithMCPConfirmation(ownerUserId int, challengeId int, operation OpenSourceBountyMCPConfirmedOperation) (*OpenSourceBountyChallenge, error) {
	return cancelOpenSourceBountyChallenge(ownerUserId, challengeId, &operation)
}

func cancelOpenSourceBountyChallenge(ownerUserId int, challengeId int, operation *OpenSourceBountyMCPConfirmedOperation) (*OpenSourceBountyChallenge, error) {
	var challenge OpenSourceBountyChallenge
	err := DB.Transaction(func(tx *gorm.DB) error {
		if operation != nil {
			if err := validateOpenSourceBountyMCPConfirmationTx(tx, ownerUserId, operation.ToolName, operation.PayloadHash, operation.State); err != nil {
				return err
			}
		}
		var challengeReference OpenSourceBountyChallenge
		if err := tx.Select("id", "project_id").Where("id = ?", challengeId).First(&challengeReference).Error; err != nil {
			return bountyError("OPEN_SOURCE_BOUNTY_CHALLENGE_NOT_FOUND", "challenge was not found")
		}
		var project OpenSourceBountyProject
		if err := lockForUpdate(tx).Where("id = ? AND owner_user_id = ?", challengeReference.ProjectId, ownerUserId).First(&project).Error; err != nil {
			return bountyError("OPEN_SOURCE_BOUNTY_FORBIDDEN", "only the bounty owner can cancel this challenge")
		}
		if project.Status != OpenSourceBountyStatusPublished && project.Status != OpenSourceBountyStatusPaused {
			return bountyError("OPEN_SOURCE_BOUNTY_INVALID_STATE", "only a published or paused bounty can cancel a challenge")
		}
		if err := lockForUpdate(tx).Where("id = ?", challengeId).First(&challenge).Error; err != nil {
			return bountyError("OPEN_SOURCE_BOUNTY_CHALLENGE_NOT_FOUND", "challenge was not found")
		}
		if challenge.ProjectId != project.Id {
			return bountyError("OPEN_SOURCE_BOUNTY_INVALID_CHALLENGE_STATE", "challenge project changed while it was cancelled")
		}
		if challenge.Status != OpenSourceBountyChallengeAccepted {
			return bountyError("OPEN_SOURCE_BOUNTY_INVALID_CHALLENGE_STATE", "only an unsubmitted challenge can be cancelled")
		}
		var openDisputes int64
		if err := tx.Model(&OpenSourceBountyDispute{}).
			Where("challenge_id = ? AND status = ?", challengeId, OpenSourceBountyDisputeOpen).
			Count(&openDisputes).Error; err != nil {
			return err
		}
		if openDisputes > 0 {
			return bountyError("OPEN_SOURCE_BOUNTY_OPEN_DISPUTES", "a challenge with an open dispute cannot be cancelled")
		}
		if err := tx.Model(&challenge).Updates(map[string]interface{}{
			"status": OpenSourceBountyChallengeCancelled, "updated_at": common.GetTimestamp(),
		}).Error; err != nil {
			return err
		}
		if operation != nil {
			return completeOpenSourceBountyMCPOperationTx(tx, ownerUserId, operation.ToolName, operation.PayloadHash, operation.State, map[string]any{
				"challenge_id": challengeId,
			})
		}
		return nil
	})
	if err != nil {
		if operation != nil && OpenSourceBountyErrorCode(err) == "OPEN_SOURCE_BOUNTY_MCP_CONFIRMATION_INVALID" {
			_ = ConsumeOpenSourceBountyMCPConfirmation(ownerUserId, operation.ToolName, operation.PayloadHash, operation.State)
		}
		return nil, err
	}
	RecordLog(ownerUserId, LogTypeSystem, fmt.Sprintf("Cancelled unsubmitted open-source bounty challenge %d", challengeId))
	return &challenge, DB.First(&challenge, challengeId).Error
}

func WithdrawOpenSourceBountyChallengeWithMCPConfirmation(participantUserId int, challengeId int, operation OpenSourceBountyMCPConfirmedOperation) (*OpenSourceBountyChallenge, error) {
	return withdrawOpenSourceBountyChallenge(participantUserId, challengeId, &operation)
}

func withdrawOpenSourceBountyChallenge(participantUserId int, challengeId int, operation *OpenSourceBountyMCPConfirmedOperation) (*OpenSourceBountyChallenge, error) {
	var challenge OpenSourceBountyChallenge
	err := DB.Transaction(func(tx *gorm.DB) error {
		if operation != nil {
			if err := validateOpenSourceBountyMCPConfirmationTx(tx, participantUserId, operation.ToolName, operation.PayloadHash, operation.State); err != nil {
				return err
			}
		}
		if err := lockForUpdate(tx).Where("id = ? AND participant_user_id = ?", challengeId, participantUserId).First(&challenge).Error; err != nil {
			return bountyError("OPEN_SOURCE_BOUNTY_CHALLENGE_NOT_FOUND", "challenge was not found")
		}
		if challenge.Status != OpenSourceBountyChallengeAccepted && challenge.Status != OpenSourceBountyChallengeSubmitted {
			return bountyError("OPEN_SOURCE_BOUNTY_INVALID_CHALLENGE_STATE", "challenge cannot be withdrawn")
		}
		var openDisputes int64
		if err := tx.Model(&OpenSourceBountyDispute{}).Where("challenge_id = ? AND status = ?", challengeId, OpenSourceBountyDisputeOpen).Count(&openDisputes).Error; err != nil {
			return err
		}
		if openDisputes > 0 {
			return bountyError("OPEN_SOURCE_BOUNTY_OPEN_DISPUTES", "a challenge with an open dispute cannot be withdrawn")
		}
		if err := tx.Model(&challenge).Updates(map[string]interface{}{"status": OpenSourceBountyChallengeWithdrawn, "updated_at": common.GetTimestamp()}).Error; err != nil {
			return err
		}
		if operation != nil {
			return completeOpenSourceBountyMCPOperationTx(tx, participantUserId, operation.ToolName, operation.PayloadHash, operation.State, map[string]any{"challenge_id": challengeId})
		}
		return nil
	})
	if err != nil {
		if operation != nil && OpenSourceBountyErrorCode(err) == "OPEN_SOURCE_BOUNTY_MCP_CONFIRMATION_INVALID" {
			_ = ConsumeOpenSourceBountyMCPConfirmation(participantUserId, operation.ToolName, operation.PayloadHash, operation.State)
		}
		return nil, err
	}
	return &challenge, DB.First(&challenge, challengeId).Error
}

func validateOpenSourceBountyRating(score int, comment string) (string, error) {
	comment = strings.TrimSpace(comment)
	if score < 1 || score > 5 {
		return "", bountyError("OPEN_SOURCE_BOUNTY_INVALID_RATING", "rating score must be between 1 and 5")
	}
	if len(comment) < 2 || len(comment) > 1000 {
		return "", bountyError("OPEN_SOURCE_BOUNTY_INVALID_RATING", "rating comment must contain 2 to 1000 characters")
	}
	return comment, nil
}

func ReviewOpenSourceBountyChallenge(ownerUserId int, challengeId int, approve bool, reviewNote string, ratingScore int, ratingComment string) (*OpenSourceBountyChallenge, int, error) {
	return reviewOpenSourceBountyChallenge(ownerUserId, challengeId, approve, reviewNote, ratingScore, ratingComment, nil)
}

func ReviewOpenSourceBountyChallengeWithMCPConfirmation(ownerUserId int, challengeId int, approve bool, reviewNote string, ratingScore int, ratingComment string, operation OpenSourceBountyMCPConfirmedOperation) (*OpenSourceBountyChallenge, int, error) {
	return reviewOpenSourceBountyChallenge(ownerUserId, challengeId, approve, reviewNote, ratingScore, ratingComment, &operation)
}

func reviewOpenSourceBountyChallenge(ownerUserId int, challengeId int, approve bool, reviewNote string, ratingScore int, ratingComment string, operation *OpenSourceBountyMCPConfirmedOperation) (*OpenSourceBountyChallenge, int, error) {
	reviewNote = strings.TrimSpace(reviewNote)
	if len(reviewNote) > 2000 {
		return nil, 0, bountyError("OPEN_SOURCE_BOUNTY_INVALID_REVIEW", "review note is too long")
	}
	var err error
	ratingComment, err = validateOpenSourceBountyRating(ratingScore, ratingComment)
	if err != nil {
		return nil, 0, err
	}
	transferredQuota := 0
	participantUserId := 0
	var challenge OpenSourceBountyChallenge
	err = DB.Transaction(func(tx *gorm.DB) error {
		if operation != nil {
			if err := validateOpenSourceBountyMCPConfirmationTx(tx, ownerUserId, operation.ToolName, operation.PayloadHash, operation.State); err != nil {
				return err
			}
		}
		var challengeReference OpenSourceBountyChallenge
		if err := tx.Select("id", "project_id").Where("id = ?", challengeId).First(&challengeReference).Error; err != nil {
			return bountyError("OPEN_SOURCE_BOUNTY_CHALLENGE_NOT_FOUND", "challenge submission was not found")
		}
		var project OpenSourceBountyProject
		if err := lockForUpdate(tx).Where("id = ? AND owner_user_id = ?", challengeReference.ProjectId, ownerUserId).First(&project).Error; err != nil {
			return bountyError("OPEN_SOURCE_BOUNTY_FORBIDDEN", "only the bounty owner can review this submission")
		}
		if err := lockForUpdate(tx).Where("id = ?", challengeId).First(&challenge).Error; err != nil {
			return bountyError("OPEN_SOURCE_BOUNTY_CHALLENGE_NOT_FOUND", "challenge submission was not found")
		}
		if challenge.ProjectId != project.Id {
			return bountyError("OPEN_SOURCE_BOUNTY_DISPUTE_IDENTITY_MISMATCH", "challenge project changed while the submission was reviewed")
		}
		var openDisputes []OpenSourceBountyDispute
		if err := lockForUpdate(tx).Where("challenge_id = ? AND status = ?", challenge.Id, OpenSourceBountyDisputeOpen).Find(&openDisputes).Error; err != nil {
			return err
		}
		if challenge.Status != OpenSourceBountyChallengeSubmitted {
			return bountyError("OPEN_SOURCE_BOUNTY_INVALID_CHALLENGE_STATE", "only a submitted challenge can be reviewed")
		}
		now := common.GetTimestamp()
		if !approve {
			if err := tx.Model(&challenge).Updates(map[string]interface{}{
				"status": OpenSourceBountyChallengeRejected, "review_note": reviewNote,
				"owner_rating_score": ratingScore, "owner_rating_comment": ratingComment,
				"owner_rated_at": now, "reviewed_at": now, "rejected_at": now, "updated_at": now,
			}).Error; err != nil {
				return err
			}
			if operation != nil {
				return completeOpenSourceBountyMCPOperationTx(tx, ownerUserId, operation.ToolName, operation.PayloadHash, operation.State, map[string]any{
					"challenge_id": challenge.Id, "transferred_quota": 0,
				})
			}
			return nil
		}
		payoutKey := fmt.Sprintf("challenge:%d", challenge.Id)
		if project.Status != OpenSourceBountyStatusPublished && project.Status != OpenSourceBountyStatusPaused {
			return bountyError("OPEN_SOURCE_BOUNTY_INVALID_STATE", "bounty is not in a payable state")
		}
		if challenge.RewardQuota <= 0 || project.EscrowQuota < challenge.RewardQuota {
			return bountyError("OPEN_SOURCE_BOUNTY_ESCROW_INSUFFICIENT", "bounty escrow is insufficient")
		}
		participantUserId = challenge.ParticipantUserId
		result := UpdateWalletQuotaByDelta(
			tx.Model(&User{}).Where("id = ? AND deleted_at IS NULL", participantUserId),
			challenge.RewardQuota,
		)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return bountyError("OPEN_SOURCE_BOUNTY_PARTICIPANT_NOT_FOUND", "challenge participant was not found")
		}
		transferredQuota = challenge.RewardQuota
		remainingEscrow := project.EscrowQuota - transferredQuota
		projectUpdates := map[string]interface{}{"escrow_quota": remainingEscrow, "updated_at": now}
		if remainingEscrow == 0 {
			projectUpdates["status"] = OpenSourceBountyStatusCompleted
			projectUpdates["closed_at"] = now
		}
		if err := tx.Model(&project).Updates(projectUpdates).Error; err != nil {
			return err
		}
		if err := tx.Model(&challenge).Updates(map[string]interface{}{
			"status": OpenSourceBountyChallengeApproved, "review_note": reviewNote,
			"owner_rating_score": ratingScore, "owner_rating_comment": ratingComment,
			"owner_rated_at": now, "reviewed_at": now, "paid_at": now, "updated_at": now,
		}).Error; err != nil {
			return err
		}
		if err := tx.Model(&OpenSourceBountyDispute{}).
			Where("challenge_id = ? AND status = ?", challenge.Id, OpenSourceBountyDisputeOpen).
			Updates(map[string]any{
				"status": OpenSourceBountyDisputeResolvedPaid, "resolution": "The publisher approved and paid the reward after the dispute was opened.",
				"resolved_by_user_id": ownerUserId, "resolved_at": now, "updated_at": now, "open_key": nil,
			}).Error; err != nil {
			return err
		}
		if err := tx.Create(&OpenSourceBountyLedger{
			ProjectId: project.Id, ChallengeId: challenge.Id, UserId: ownerUserId,
			CounterpartyUserId: participantUserId, Kind: OpenSourceBountyLedgerRewardTransfer,
			Quota: transferredQuota, RewardPayoutKey: &payoutKey, CreatedAt: now,
		}).Error; err != nil {
			return err
		}
		if operation != nil {
			return completeOpenSourceBountyMCPOperationTx(tx, ownerUserId, operation.ToolName, operation.PayloadHash, operation.State, map[string]any{
				"challenge_id": challenge.Id, "transferred_quota": transferredQuota,
			})
		}
		return nil
	})
	if err != nil {
		if operation != nil && OpenSourceBountyErrorCode(err) == "OPEN_SOURCE_BOUNTY_MCP_CONFIRMATION_INVALID" {
			_ = ConsumeOpenSourceBountyMCPConfirmation(ownerUserId, operation.ToolName, operation.PayloadHash, operation.State)
		}
		return nil, 0, err
	}
	if approve && transferredQuota > 0 {
		if err := cacheIncrUserQuota(participantUserId, int64(transferredQuota)); err != nil {
			common.SysLog("failed to increase participant quota cache after open-source bounty approval: " + err.Error())
		}
		RecordLog(participantUserId, LogTypeTopup, fmt.Sprintf("Received %d quota from open-source bounty %d", transferredQuota, challenge.ProjectId))
		RecordLog(ownerUserId, LogTypeSystem, fmt.Sprintf("Approved open-source bounty challenge %d and transferred %d quota", challengeId, transferredQuota))
	}
	return &challenge, transferredQuota, DB.First(&challenge, challengeId).Error
}

func RateOpenSourceBountyOwner(participantUserId int, challengeId int, score int, comment string) (*OpenSourceBountyChallenge, error) {
	return rateOpenSourceBountyOwner(participantUserId, challengeId, score, comment, nil)
}

func RateOpenSourceBountyOwnerWithMCPConfirmation(participantUserId int, challengeId int, score int, comment string, operation OpenSourceBountyMCPConfirmedOperation) (*OpenSourceBountyChallenge, error) {
	return rateOpenSourceBountyOwner(participantUserId, challengeId, score, comment, &operation)
}

func rateOpenSourceBountyOwner(participantUserId int, challengeId int, score int, comment string, operation *OpenSourceBountyMCPConfirmedOperation) (*OpenSourceBountyChallenge, error) {
	comment, err := validateOpenSourceBountyRating(score, comment)
	if err != nil {
		return nil, err
	}
	var challenge OpenSourceBountyChallenge
	err = DB.Transaction(func(tx *gorm.DB) error {
		if operation != nil {
			if err := validateOpenSourceBountyMCPConfirmationTx(tx, participantUserId, operation.ToolName, operation.PayloadHash, operation.State); err != nil {
				return err
			}
		}
		if err := lockForUpdate(tx).Where("id = ? AND participant_user_id = ?", challengeId, participantUserId).First(&challenge).Error; err != nil {
			return bountyError("OPEN_SOURCE_BOUNTY_CHALLENGE_NOT_FOUND", "challenge was not found")
		}
		if challenge.Status != OpenSourceBountyChallengeApproved && challenge.Status != OpenSourceBountyChallengeRejected {
			return bountyError("OPEN_SOURCE_BOUNTY_INVALID_CHALLENGE_STATE", "the bounty owner can only be rated after review")
		}
		if challenge.ContributorRatedAt > 0 {
			return bountyError("OPEN_SOURCE_BOUNTY_RATING_EXISTS", "the publisher rating for this challenge has already been submitted")
		}
		now := common.GetTimestamp()
		if err := tx.Model(&challenge).Updates(map[string]any{
			"contributor_rating_score": score, "contributor_rating_comment": comment,
			"contributor_rated_at": now, "updated_at": now,
		}).Error; err != nil {
			return err
		}
		if operation != nil {
			return completeOpenSourceBountyMCPOperationTx(tx, participantUserId, operation.ToolName, operation.PayloadHash, operation.State, map[string]any{"challenge_id": challengeId})
		}
		return nil
	})
	if err != nil {
		if operation != nil && OpenSourceBountyErrorCode(err) == "OPEN_SOURCE_BOUNTY_MCP_CONFIRMATION_INVALID" {
			_ = ConsumeOpenSourceBountyMCPConfirmation(participantUserId, operation.ToolName, operation.PayloadHash, operation.State)
		}
		return nil, err
	}
	return &challenge, DB.First(&challenge, challengeId).Error
}

func TipOpenSourceBountyChallenge(ownerUserId int, challengeId int, quota int, note string) (*OpenSourceBountyChallenge, int, error) {
	result, err := tipOpenSourceBountyChallenge(ownerUserId, challengeId, quota, note, nil, nil)
	if err != nil {
		return nil, 0, err
	}
	return &result.Challenge, result.TransferredQuota, nil
}

func TipOpenSourceBountyChallengeWithMCPConfirmation(ownerUserId int, challengeId int, quota int, note string, operation OpenSourceBountyMCPConfirmedOperation) (*OpenSourceBountyChallenge, int, error) {
	result, err := tipOpenSourceBountyChallenge(ownerUserId, challengeId, quota, note, &operation, nil)
	if err != nil {
		return nil, 0, err
	}
	return &result.Challenge, result.TransferredQuota, nil
}

func TipOpenSourceBountyChallengeIdempotent(ownerUserId int, challengeId int, quota int, note string, idempotencyKey string) (*OpenSourceBountyTipResult, error) {
	spec, err := newOpenSourceBountyRESTTipOperationSpec(ownerUserId, challengeId, quota, note, idempotencyKey)
	if err != nil {
		return nil, err
	}
	return tipOpenSourceBountyChallenge(ownerUserId, challengeId, quota, note, nil, spec)
}

func tipOpenSourceBountyChallenge(ownerUserId int, challengeId int, quota int, note string, operation *OpenSourceBountyMCPConfirmedOperation, restOperation *openSourceBountyRESTOperationSpec) (*OpenSourceBountyTipResult, error) {
	note = strings.TrimSpace(note)
	if quota <= 0 || quota > maxOpenSourceBountyTipQuota {
		return nil, bountyError("OPEN_SOURCE_BOUNTY_INVALID_TIP", "tip quota must be a positive supported amount")
	}
	if len(note) > 500 {
		return nil, bountyError("OPEN_SOURCE_BOUNTY_INVALID_TIP", "tip note is too long")
	}
	participantUserId := 0
	var challenge OpenSourceBountyChallenge
	var result OpenSourceBountyTipResult
	replayed := false
	err := DB.Transaction(func(tx *gorm.DB) error {
		if operation != nil {
			if err := validateOpenSourceBountyMCPConfirmationTx(tx, ownerUserId, operation.ToolName, operation.PayloadHash, operation.State); err != nil {
				return err
			}
		}
		var restRecord *OpenSourceBountyRESTOperation
		if restOperation != nil {
			var replay *OpenSourceBountyTipResult
			var err error
			restRecord, replay, err = reserveOpenSourceBountyRESTOperationTx(tx, restOperation)
			if err != nil {
				return err
			}
			if replay != nil {
				result = *replay
				replayed = true
				return nil
			}
		}
		var challengeReference OpenSourceBountyChallenge
		if err := tx.Select("id", "project_id").Where("id = ?", challengeId).First(&challengeReference).Error; err != nil {
			return bountyError("OPEN_SOURCE_BOUNTY_CHALLENGE_NOT_FOUND", "challenge was not found")
		}
		var project OpenSourceBountyProject
		if err := lockForUpdate(tx).Where("id = ? AND owner_user_id = ?", challengeReference.ProjectId, ownerUserId).First(&project).Error; err != nil {
			return bountyError("OPEN_SOURCE_BOUNTY_FORBIDDEN", "only the bounty owner can tip this contributor")
		}
		if err := lockForUpdate(tx).Where("id = ?", challengeId).First(&challenge).Error; err != nil {
			return bountyError("OPEN_SOURCE_BOUNTY_CHALLENGE_NOT_FOUND", "challenge was not found")
		}
		if challenge.ProjectId != project.Id {
			return bountyError("OPEN_SOURCE_BOUNTY_DISPUTE_IDENTITY_MISMATCH", "challenge project changed while the tip was sent")
		}
		if challenge.Status == OpenSourceBountyChallengeWithdrawn || challenge.Status == OpenSourceBountyChallengeCancelled {
			return bountyError("OPEN_SOURCE_BOUNTY_INVALID_CHALLENGE_STATE", "inactive challenges cannot receive tips")
		}
		participantUserId = challenge.ParticipantUserId
		if participantUserId == ownerUserId {
			return bountyError("OPEN_SOURCE_BOUNTY_SELF_TIP", "bounty owners cannot tip themselves")
		}
		debit := UpdateWalletQuotaByDelta(
			tx.Model(&User{}).Where("id = ? AND deleted_at IS NULL AND quota >= ?", ownerUserId, quota),
			-quota,
		)
		if debit.Error != nil {
			return debit.Error
		}
		if debit.RowsAffected != 1 {
			return bountyError("OPEN_SOURCE_BOUNTY_INSUFFICIENT_BALANCE", "insufficient balance to send this tip")
		}
		credit := UpdateWalletQuotaByDelta(
			tx.Model(&User{}).Where("id = ? AND deleted_at IS NULL", participantUserId),
			quota,
		)
		if credit.Error != nil {
			return credit.Error
		}
		if credit.RowsAffected != 1 {
			return bountyError("OPEN_SOURCE_BOUNTY_PARTICIPANT_NOT_FOUND", "challenge participant was not found")
		}
		now := common.GetTimestamp()
		if err := tx.Model(&challenge).Updates(map[string]any{
			"tip_quota": gorm.Expr("tip_quota + ?", quota), "updated_at": now,
		}).Error; err != nil {
			return err
		}
		if err := tx.Create(&OpenSourceBountyLedger{
			ProjectId: project.Id, ChallengeId: challenge.Id, UserId: ownerUserId,
			CounterpartyUserId: participantUserId, Kind: OpenSourceBountyLedgerTipTransfer,
			Quota: quota, Note: note, CreatedAt: now,
		}).Error; err != nil {
			return err
		}
		if err := tx.First(&challenge, challenge.Id).Error; err != nil {
			return err
		}
		remainingQuota := 0
		if err := tx.Model(&User{}).Where("id = ?", ownerUserId).Select("quota").Scan(&remainingQuota).Error; err != nil {
			return err
		}
		result = OpenSourceBountyTipResult{Challenge: challenge, TransferredQuota: quota, RemainingQuota: remainingQuota}
		if restRecord != nil {
			if err := completeOpenSourceBountyRESTOperationTx(tx, restRecord, &result); err != nil {
				return err
			}
		}
		if operation != nil {
			return completeOpenSourceBountyMCPOperationTx(tx, ownerUserId, operation.ToolName, operation.PayloadHash, operation.State, map[string]any{
				"challenge_id": challenge.Id, "transferred_quota": quota,
			})
		}
		return nil
	})
	if err != nil {
		if restOperation != nil && errors.Is(err, errOpenSourceBountyRESTOperationRace) {
			return getOpenSourceBountyRESTTipResult(restOperation)
		}
		if operation != nil && OpenSourceBountyErrorCode(err) == "OPEN_SOURCE_BOUNTY_MCP_CONFIRMATION_INVALID" {
			_ = ConsumeOpenSourceBountyMCPConfirmation(ownerUserId, operation.ToolName, operation.PayloadHash, operation.State)
		}
		return nil, err
	}
	if replayed {
		return &result, nil
	}
	if err := cacheDecrUserQuota(ownerUserId, int64(quota)); err != nil {
		common.SysLog("failed to decrease owner quota cache after open-source bounty tip: " + err.Error())
	}
	if err := cacheIncrUserQuota(participantUserId, int64(quota)); err != nil {
		common.SysLog("failed to increase participant quota cache after open-source bounty tip: " + err.Error())
	}
	RecordLog(participantUserId, LogTypeTopup, fmt.Sprintf("Received %d quota tip from open-source bounty %d", quota, challenge.ProjectId))
	RecordLog(ownerUserId, LogTypeSystem, fmt.Sprintf("Tipped open-source bounty challenge %d with %d quota", challengeId, quota))
	return &result, nil
}
