package model

import (
	"encoding/json"
	"strconv"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type bountyDisputeCreditFixture struct {
	db          *gorm.DB
	owner       User
	participant User
	admin       User
	project     OpenSourceBountyProject
	challenge   OpenSourceBountyChallenge
	dispute     OpenSourceBountyDispute
	ledgers     []OpenSourceBountyLedger
}

func newBountyDisputeCreditFixture(t *testing.T, reward int, acceptedAtFiling bool) bountyDisputeCreditFixture {
	t.Helper()
	db := setupOpenSourceBountyTestDB(t)
	owner := createOpenSourceBountyUser(t, db, "basis-owner", 20_000_000, common.RoleCommonUser)
	participant := createOpenSourceBountyUser(t, db, "basis-contributor", 0, common.RoleCommonUser)
	admin := createOpenSourceBountyUser(t, db, "basis-admin", 0, common.RoleAdminUser)
	project, err := CreateOpenSourceBountyDraft(owner.Id, openSourceBountyInput("https://github.com/example/basis", reward, 1))
	require.NoError(t, err)
	project, _, err = PublishOpenSourceBounty(owner.Id, project.Id)
	require.NoError(t, err)
	challenge, err := AcceptOpenSourceBounty(participant.Id, project.Id, participant.Username)
	require.NoError(t, err)
	if !acceptedAtFiling {
		challenge, err = SubmitOpenSourceBountyChallenge(participant.Id, project.Id, "https://github.com/example/basis/issues/1", "https://github.com/example/basis/pull/2", "Verified submission before the dispute.")
		require.NoError(t, err)
		_, _, err = TipOpenSourceBountyChallenge(owner.Id, challenge.Id, 123, "A historical already-paid tip.")
		require.NoError(t, err)
		_, _, err = ReviewOpenSourceBountyChallenge(owner.Id, challenge.Id, false, "Payment refused despite verified submission.", 2, "Original rejection must remain visible.")
		require.NoError(t, err)
	}
	view, err := OpenOpenSourceBountyDispute(participant.Id, challenge.Id, "merged_but_unpaid", "The merged submission meets the published criteria, but its escrowed reward has not been paid.")
	require.NoError(t, err)
	f := bountyDisputeCreditFixture{db: db, owner: owner, participant: participant, admin: admin}
	require.NoError(t, db.First(&f.project, project.Id).Error)
	require.NoError(t, db.First(&f.challenge, challenge.Id).Error)
	require.NoError(t, db.First(&f.dispute, view.Id).Error)
	require.NoError(t, db.Order("id").Find(&f.ledgers).Error)
	return f
}

func (f bountyDisputeCreditFixture) basis(t *testing.T, target int) map[string]any {
	t.Helper()
	return map[string]any{"kind": "bounty_dispute_reward", "source_id": strconv.Itoa(f.dispute.Id), "user_id": f.participant.Id,
		"original_quota": f.dispute.RewardQuotaSnapshot, "rebased_quota": target, "source": bountyDisputeCreditSourceOf(&f.dispute)}
}

func (f bountyDisputeCreditFixture) rebaseLive(t *testing.T, target int) {
	t.Helper()
	require.NoError(t, f.db.Model(&f.project).Updates(map[string]any{"escrow_quota": target, "reward_quota": target, "net_reward_quota": target}).Error)
	require.NoError(t, f.db.Model(&f.challenge).UpdateColumn("reward_quota", target).Error)
	// The already issued tip is represented by the user's independently migrated wallet.
	require.NoError(t, f.db.Model(&f.participant).UpdateColumn("quota", 18).Error)
}

func TestBountyDisputeHistoricalRewardUsesBasisAndPreservesEvidence(t *testing.T) {
	f := newBountyDisputeCreditFixture(t, 6_710_363, false)
	putFutureCreditAudit(t, f.db, []int{f.owner.Id, f.participant.Id}, f.dispute.CreatedAt, []map[string]any{f.basis(t, 1_000_000)})
	f.rebaseLive(t, 1_000_000)
	view, transferred, err := ResolveOpenSourceBountyDispute(f.admin.Id, f.dispute.Id, "pay", "Independent review verified the merged change and enforces the promised reward.")
	require.NoError(t, err)
	require.Equal(t, 1_000_000, transferred)
	require.Equal(t, OpenSourceBountyDisputeResolvedPaid, view.Status)
	require.Equal(t, f.dispute.RewardQuotaSnapshot, view.RewardQuotaSnapshot)
	require.Equal(t, f.dispute.ProjectEscrowQuotaSnapshot, view.ProjectEscrowQuotaSnapshot)
	require.Equal(t, f.dispute.TipQuotaSnapshot, view.TipQuotaSnapshot)
	var participant User
	require.NoError(t, f.db.First(&participant, f.participant.Id).Error)
	require.Equal(t, 1_000_018, participant.Quota)
	var project OpenSourceBountyProject
	require.NoError(t, f.db.First(&project, f.project.Id).Error)
	require.Zero(t, project.EscrowQuota)
	var challenge OpenSourceBountyChallenge
	require.NoError(t, f.db.First(&challenge, f.challenge.Id).Error)
	require.Equal(t, 1_000_000, challenge.RewardQuota)
	require.Equal(t, f.challenge.TipQuota, challenge.TipQuota)
	require.Equal(t, f.challenge.ReviewNote, challenge.ReviewNote)
	for _, before := range f.ledgers {
		var after OpenSourceBountyLedger
		require.NoError(t, f.db.First(&after, before.Id).Error)
		require.Equal(t, before, after, "old funding and already-paid tip records stay unchanged")
	}
	var payout OpenSourceBountyLedger
	require.NoError(t, f.db.Where("kind = ?", OpenSourceBountyLedgerDisputeRewardTransfer).First(&payout).Error)
	require.Equal(t, 1_000_000, payout.Quota)
	// A retry after the committed payment must not consult or need the old basis.
	require.NoError(t, f.db.Exec("UPDATE wallet_credit_rebases SET plan=json_set(plan,'$.other_credit_bases',json('[]'))").Error)
	_, transferred, err = ResolveOpenSourceBountyDispute(f.admin.Id, f.dispute.Id, "pay", "Retrying the committed dispute resolution after a lost response.")
	require.NoError(t, err)
	require.Zero(t, transferred)
	var count int64
	require.NoError(t, f.db.Model(&OpenSourceBountyLedger{}).Where("kind = ?", OpenSourceBountyLedgerDisputeRewardTransfer).Count(&count).Error)
	require.EqualValues(t, 1, count)
}

func TestBountyDisputeCreditBasisFailuresRollbackPayment(t *testing.T) {
	for _, kind := range []string{"missing", "owner", "source", "incomplete_source", "reward", "escrow"} {
		t.Run(kind, func(t *testing.T) {
			f := newBountyDisputeCreditFixture(t, 6_710_363, false)
			basis := f.basis(t, 1_000_000)
			bases := []map[string]any{basis}
			switch kind {
			case "missing":
				bases = []map[string]any{}
			case "owner":
				basis["user_id"] = f.owner.Id
			case "source":
				source := bountyDisputeCreditSourceOf(&f.dispute)
				source.ChallengeID++
				basis["source"] = source
			case "incomplete_source":
				encoded, err := json.Marshal(basis["source"])
				require.NoError(t, err)
				var source map[string]any
				require.NoError(t, json.Unmarshal(encoded, &source))
				delete(source, "resolved_at")
				basis["source"] = source
			}
			putFutureCreditAudit(t, f.db, []int{f.owner.Id, f.participant.Id}, f.dispute.CreatedAt, bases)
			f.rebaseLive(t, 1_000_000)
			if kind == "reward" {
				require.NoError(t, f.db.Model(&f.challenge).UpdateColumn("reward_quota", 1_000_001).Error)
			}
			if kind == "escrow" {
				require.NoError(t, f.db.Model(&f.project).UpdateColumn("escrow_quota", 999_999).Error)
			}
			_, transferred, err := ResolveOpenSourceBountyDispute(f.admin.Id, f.dispute.Id, "pay", "The proposed payment must roll back if its immutable credit basis is inconsistent.")
			require.Error(t, err)
			require.Zero(t, transferred)
			if kind == "reward" || kind == "escrow" {
				require.Equal(t, "OPEN_SOURCE_BOUNTY_ESCROW_INSUFFICIENT", OpenSourceBountyErrorCode(err))
			} else {
				require.ErrorIs(t, err, ErrWalletQuotaOutOfRange)
			}
			var participant User
			require.NoError(t, f.db.First(&participant, f.participant.Id).Error)
			require.Equal(t, 18, participant.Quota)
			var dispute OpenSourceBountyDispute
			require.NoError(t, f.db.First(&dispute, f.dispute.Id).Error)
			require.Equal(t, f.dispute, dispute)
			var count int64
			require.NoError(t, f.db.Model(&OpenSourceBountyLedger{}).Where("kind = ?", OpenSourceBountyLedgerDisputeRewardTransfer).Count(&count).Error)
			require.Zero(t, count)
		})
	}
}

func TestBountyDisputeCreatedAfterFreezeUsesCurrentCredits(t *testing.T) {
	f := newBountyDisputeCreditFixture(t, 500_000, false)
	putFutureCreditAudit(t, f.db, []int{f.owner.Id, f.participant.Id}, f.dispute.CreatedAt-1, []map[string]any{})
	_, transferred, err := ResolveOpenSourceBountyDispute(f.admin.Id, f.dispute.Id, "pay", "This claim was created after the migration freeze and already records current credits.")
	require.NoError(t, err)
	require.Equal(t, 500_000, transferred)
	var participant User
	require.NoError(t, f.db.First(&participant, f.participant.Id).Error)
	require.Equal(t, 500_123, participant.Quota)
}

func TestBountyDisputeHistoricalRewardRoundedZeroResolvesOnce(t *testing.T) {
	f := newBountyDisputeCreditFixture(t, 1, false)
	putFutureCreditAudit(t, f.db, []int{f.owner.Id, f.participant.Id}, f.dispute.CreatedAt, []map[string]any{f.basis(t, 0)})
	f.rebaseLive(t, 0)
	view, transferred, err := ResolveOpenSourceBountyDispute(f.admin.Id, f.dispute.Id, "pay", "This historical one-credit reward rounds to zero and its case still resolves exactly once.")
	require.NoError(t, err)
	require.Zero(t, transferred)
	require.Equal(t, OpenSourceBountyDisputeResolvedPaid, view.Status)
	require.Equal(t, 1, view.RewardQuotaSnapshot)
	_, transferred, err = ResolveOpenSourceBountyDispute(f.admin.Id, f.dispute.Id, "pay", "A retry must not produce an additional zero-credit payout ledger.")
	require.NoError(t, err)
	require.Zero(t, transferred)
	var count int64
	require.NoError(t, f.db.Model(&OpenSourceBountyLedger{}).Where("kind = ?", OpenSourceBountyLedgerDisputeRewardTransfer).Count(&count).Error)
	require.EqualValues(t, 1, count)
}

func TestBountyDisputeAcceptedAtFreezeCanSubmitAndPayFromHistoricalBasis(t *testing.T) {
	f := newBountyDisputeCreditFixture(t, 6_710_363, true)
	require.Equal(t, OpenSourceBountyChallengeAccepted, f.dispute.ChallengeStatusSnapshot)
	putFutureCreditAudit(t, f.db, []int{f.owner.Id, f.participant.Id}, f.dispute.CreatedAt, []map[string]any{f.basis(t, 1_000_000)})
	f.rebaseLive(t, 1_000_000)
	_, err := SubmitOpenSourceBountyChallenge(f.participant.Id, f.project.Id, "https://github.com/example/basis/issues/1", "https://github.com/example/basis/pull/2", "Submission completed after the credit migration.")
	require.NoError(t, err)
	view, transferred, err := ResolveOpenSourceBountyDispute(f.admin.Id, f.dispute.Id, "pay", "The previously accepted claim is now submitted and its historical reward can be paid.")
	require.NoError(t, err)
	require.Equal(t, 1_000_000, transferred)
	require.Equal(t, OpenSourceBountyChallengeAccepted, view.ChallengeStatusSnapshot)
	require.Equal(t, 6_710_363, view.RewardQuotaSnapshot)
}
