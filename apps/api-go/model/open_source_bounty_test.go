package model

import (
	"fmt"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupOpenSourceBountyTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	previousDB, previousLogDB := DB, LOG_DB
	previousRedisEnabled := common.RedisEnabled
	previousMainDatabaseType, previousLogDatabaseType := common.MainDatabaseType(), common.LogDatabaseType()
	common.RedisEnabled = false
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	common.OptionMapRWMutex.Lock()
	if common.OptionMap == nil {
		common.OptionMap = make(map[string]string)
	}
	previousFeeRate, hadPreviousFeeRate := common.OptionMap[OpenSourceBountyFeeRateOptionKey]
	common.OptionMap[OpenSourceBountyFeeRateOptionKey] = "0"
	common.OptionMapRWMutex.Unlock()

	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	DB, LOG_DB = db, db
	require.NoError(t, db.AutoMigrate(
		&User{}, &Log{}, &OpenSourceBountyProject{}, &OpenSourceBountyChallenge{}, &OpenSourceBountyLedger{}, &OpenSourceBountyDispute{},
		&OpenSourceBountyRESTOperation{},
	))

	t.Cleanup(func() {
		DB, LOG_DB = previousDB, previousLogDB
		common.RedisEnabled = previousRedisEnabled
		common.SetDatabaseTypes(previousMainDatabaseType, previousLogDatabaseType)
		common.OptionMapRWMutex.Lock()
		if hadPreviousFeeRate {
			common.OptionMap[OpenSourceBountyFeeRateOptionKey] = previousFeeRate
		} else {
			delete(common.OptionMap, OpenSourceBountyFeeRateOptionKey)
		}
		common.OptionMapRWMutex.Unlock()
		sqlDB, err := db.DB()
		if err == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}

func setOpenSourceBountyFeeRateForTest(rate string) {
	common.OptionMapRWMutex.Lock()
	common.OptionMap[OpenSourceBountyFeeRateOptionKey] = rate
	common.OptionMapRWMutex.Unlock()
}

func createOpenSourceBountyUser(t *testing.T, db *gorm.DB, username string, quota int, role int) User {
	t.Helper()
	user := User{Username: username, Password: "password", AffCode: username, Quota: quota, Role: role, Status: common.UserStatusEnabled}
	if role < common.RoleAdminUser {
		levelOne := TrustLevelMinUser + 1
		user.TrustLevelOverride = &levelOne
	}
	require.NoError(t, db.Create(&user).Error)
	return user
}

func openSourceBountyInput(repository string, reward int, slots int) OpenSourceBountyDraftInput {
	return OpenSourceBountyDraftInput{
		RepositoryUrl: repository,
		Title:         "Fix reproducible API defects",
		Description:   "Find a reproducible defect and provide a focused fix with verification.",
		Rules:         "The Issue must document reproduction, expected behavior, actual behavior, and impact. The pull request must link the Issue and include verification.",
		RewardQuota:   reward,
		RewardSlots:   slots,
	}
}

func TestOpenSourceBountyEmptyListQueriesReturnNonNilSlices(t *testing.T) {
	db := setupOpenSourceBountyTestDB(t)
	user := createOpenSourceBountyUser(t, db, "empty-list-user", 0, common.RoleCommonUser)

	projects, total, err := ListOpenSourceBounties(user.Id, 1, 20)
	require.NoError(t, err)
	assert.Zero(t, total)
	assert.NotNil(t, projects)
	assert.Empty(t, projects)

	owned, err := ListOwnedOpenSourceBounties(user.Id)
	require.NoError(t, err)
	assert.NotNil(t, owned)
	assert.Empty(t, owned)

	accepted, err := ListAcceptedOpenSourceBounties(user.Id)
	require.NoError(t, err)
	assert.NotNil(t, accepted)
	assert.Empty(t, accepted)

	disputes, err := ListOpenSourceBountyDisputes(user.Id, false)
	require.NoError(t, err)
	assert.NotNil(t, disputes)
	assert.Empty(t, disputes)
}

func TestOpenSourceBountyL0ViewerGetsOnlyPublicBoardAndDetail(t *testing.T) {
	db := setupOpenSourceBountyTestDB(t)
	owner := createOpenSourceBountyUser(t, db, "l0-public-owner", 10_000, common.RoleCommonUser)
	viewer := createOpenSourceBountyUser(t, db, "l0-public-viewer", 0, common.RoleCommonUser)
	project, err := CreateOpenSourceBountyDraft(owner.Id, openSourceBountyInput("https://github.com/example/l0-public", 1_000, 1))
	require.NoError(t, err)
	project, _, err = PublishOpenSourceBounty(owner.Id, project.Id)
	require.NoError(t, err)
	challenge, err := AcceptOpenSourceBounty(viewer.Id, project.Id, "l0-public-viewer")
	require.NoError(t, err)

	privateDraft, err := CreateOpenSourceBountyDraft(viewer.Id, openSourceBountyInput("https://github.com/example/l0-private", 500, 1))
	require.NoError(t, err)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", viewer.Id).Update("trust_level_override", TrustLevelMinUser).Error)

	projects, total, err := ListOpenSourceBounties(viewer.Id, 1, 20)
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	require.Len(t, projects, 1)
	assert.Nil(t, projects[0].ViewerChallenge, "L0 public listing must not expose the viewer's private challenge")

	detail, err := GetOpenSourceBountyDetail(viewer.Id, project.Id)
	require.NoError(t, err)
	assert.Nil(t, detail.Project.ViewerChallenge)
	assert.Empty(t, detail.Challenges)
	assert.Empty(t, detail.Ledger)
	_, err = GetOpenSourceBountyDetail(viewer.Id, privateDraft.Id)
	assert.Equal(t, "OPEN_SOURCE_BOUNTY_NOT_FOUND", OpenSourceBountyErrorCode(err), "L0 must not retain owner-only draft reads")

	require.NoError(t, DB.Model(&User{}).Where("id = ?", viewer.Id).Update("trust_level_override", TrustLevelMinUser+1).Error)
	detail, err = GetOpenSourceBountyDetail(viewer.Id, project.Id)
	require.NoError(t, err)
	require.NotNil(t, detail.Project.ViewerChallenge)
	assert.Equal(t, challenge.Id, detail.Project.ViewerChallenge.Id, "restoring L1 preserves the legitimate personalized public view")
	_, err = GetOpenSourceBountyDetail(viewer.Id, privateDraft.Id)
	require.NoError(t, err, "restoring L1 preserves the legitimate owner-only detail view")
}

func TestOpenSourceBountyBoardRanksByGrossPricePerFix(t *testing.T) {
	db := setupOpenSourceBountyTestDB(t)
	owner := createOpenSourceBountyUser(t, db, "price-ranking-owner", 100_000, common.RoleCommonUser)
	projectsByReward := make(map[int]*OpenSourceBountyProject)
	for index, reward := range []int{500, 5_000, 1_000} {
		input := openSourceBountyInput(fmt.Sprintf("https://github.com/example/ranking-%d", index), reward, 1)
		input.Title = fmt.Sprintf("Ranked bounty %d", reward)
		project, err := CreateOpenSourceBountyDraft(owner.Id, input)
		require.NoError(t, err)
		project, _, err = PublishOpenSourceBounty(owner.Id, project.Id)
		require.NoError(t, err)
		projectsByReward[reward] = project
	}
	require.NoError(t, DB.Model(&OpenSourceBountyProject{}).
		Where("id = ?", projectsByReward[5_000].Id).
		Updates(map[string]any{"status": OpenSourceBountyStatusPaused, "published_at": int64(100)}).Error)

	equalPriceInput := openSourceBountyInput("https://github.com/example/ranking-equal", 5_000, 1)
	equalPriceInput.Title = "Equal-price active bounty"
	equalPriceProject, err := CreateOpenSourceBountyDraft(owner.Id, equalPriceInput)
	require.NoError(t, err)
	equalPriceProject, _, err = PublishOpenSourceBounty(owner.Id, equalPriceProject.Id)
	require.NoError(t, err)
	require.NoError(t, DB.Model(&OpenSourceBountyProject{}).
		Where("id = ?", equalPriceProject.Id).
		Update("published_at", int64(200)).Error)

	projects, total, err := ListOpenSourceBounties(owner.Id, 1, 20)
	require.NoError(t, err)
	assert.Equal(t, int64(4), total)
	require.Len(t, projects, 4)
	assert.Equal(t, []int{5_000, 5_000, 1_000, 500}, []int{
		projects[0].RewardQuota,
		projects[1].RewardQuota,
		projects[2].RewardQuota,
		projects[3].RewardQuota,
	})
	assert.Equal(t, equalPriceProject.Id, projects[0].Id, "an active listing wins only the equal-price tie")
	assert.Equal(t, projectsByReward[5_000].Id, projects[1].Id, "a paused high-price listing still outranks lower prices")

	require.NoError(t, DB.Model(&OpenSourceBountyProject{}).
		Where("id = ?", projectsByReward[1_000].Id).
		Update("status", OpenSourceBountyStatusCompleted).Error)
	projects, total, err = ListOpenSourceBounties(owner.Id, 1, 20)
	require.NoError(t, err)
	assert.Equal(t, int64(3), total, "completed lifecycle records must not occupy the public ranking")
	for _, project := range projects {
		assert.NotEqual(t, OpenSourceBountyStatusCompleted, project.Status)
	}
}

func TestOpenSourceBountyLifecycleChargesOwnerAndTransfersEscrow(t *testing.T) {
	db := setupOpenSourceBountyTestDB(t)
	owner := createOpenSourceBountyUser(t, db, "root-owner", 10_000, common.RoleRootUser)
	participant := createOpenSourceBountyUser(t, db, "contributor", 100, common.RoleCommonUser)

	project, err := CreateOpenSourceBountyDraft(owner.Id, openSourceBountyInput("https://github.com/example/project", 2_000, 1))
	require.NoError(t, err)
	var ownerAfterDraft User
	require.NoError(t, db.First(&ownerAfterDraft, owner.Id).Error)
	assert.Equal(t, 10_000, ownerAfterDraft.Quota, "saving a draft must not charge balance")

	project, charged, err := PublishOpenSourceBounty(owner.Id, project.Id)
	require.NoError(t, err)
	assert.Equal(t, 2_000, charged)
	assert.Equal(t, OpenSourceBountyStatusPublished, project.Status)
	assert.Equal(t, 2_000, project.EscrowQuota)
	require.NoError(t, db.First(&ownerAfterDraft, owner.Id).Error)
	assert.Equal(t, 8_000, ownerAfterDraft.Quota, "root users must pay from their own balance")

	challenge, err := AcceptOpenSourceBounty(participant.Id, project.Id, "@contributor")
	require.NoError(t, err)
	assert.Equal(t, OpenSourceBountyChallengeAccepted, challenge.Status)

	challenge, err = SubmitOpenSourceBountyChallenge(
		participant.Id,
		project.Id,
		"https://github.com/example/project/issues/12",
		"https://github.com/example/project/pull/13",
		"Regression test included.",
	)
	require.NoError(t, err)
	assert.Equal(t, OpenSourceBountyChallengeSubmitted, challenge.Status)

	challenge, transferred, err := ReviewOpenSourceBountyChallenge(owner.Id, challenge.Id, true, "Verified and approved.", 5, "Excellent defect report and focused fix.")
	require.NoError(t, err)
	assert.Equal(t, 2_000, transferred)
	assert.Equal(t, OpenSourceBountyChallengeApproved, challenge.Status)
	assert.Equal(t, 5, challenge.OwnerRatingScore)

	var participantAfter User
	require.NoError(t, db.First(&participantAfter, participant.Id).Error)
	assert.Equal(t, 2_100, participantAfter.Quota)
	require.NoError(t, db.First(project, project.Id).Error)
	assert.Equal(t, 0, project.EscrowQuota)
	assert.Equal(t, OpenSourceBountyStatusCompleted, project.Status)

	var ledger []OpenSourceBountyLedger
	require.NoError(t, db.Order("id asc").Find(&ledger).Error)
	require.Len(t, ledger, 2)
	assert.Equal(t, OpenSourceBountyLedgerEscrowFund, ledger[0].Kind)
	assert.Equal(t, OpenSourceBountyLedgerRewardTransfer, ledger[1].Kind)
	rewardNotifications, err := ListOpenSourceBountyNotifications(participant.Id, 50)
	require.NoError(t, err)
	require.Len(t, rewardNotifications, 1)
	assert.Equal(t, OpenSourceBountyLedgerRewardTransfer, rewardNotifications[0].Kind)
	assert.Equal(t, challenge.Id, rewardNotifications[0].ChallengeId)
	assert.Equal(t, 2_000, rewardNotifications[0].Quota)

	_, _, err = ReviewOpenSourceBountyChallenge(owner.Id, challenge.Id, true, "duplicate approval", 5, "Already reviewed.")
	require.Error(t, err)
	require.NoError(t, db.First(&participantAfter, participant.Id).Error)
	assert.Equal(t, 2_100, participantAfter.Quota, "approval must transfer balance exactly once")
	rewardNotifications, err = ListOpenSourceBountyNotifications(participant.Id, 50)
	require.NoError(t, err)
	require.Len(t, rewardNotifications, 1, "duplicate approval must not duplicate its notification")
}

func TestOpenSourceBountyArchiveIsReversibleAndScopedToFinalStates(t *testing.T) {
	db := setupOpenSourceBountyTestDB(t)
	owner := createOpenSourceBountyUser(t, db, "archive-owner", 10_000, common.RoleCommonUser)
	stranger := createOpenSourceBountyUser(t, db, "archive-stranger", 0, common.RoleCommonUser)
	project, err := CreateOpenSourceBountyDraft(owner.Id, openSourceBountyInput("https://github.com/example/archive", 1_000, 1))
	require.NoError(t, err)

	_, err = ArchiveOpenSourceBounty(owner.Id, project.Id)
	assert.Equal(t, "OPEN_SOURCE_BOUNTY_ARCHIVE_UNAVAILABLE", OpenSourceBountyErrorCode(err))

	require.NoError(t, db.Model(&OpenSourceBountyProject{}).
		Where("id = ?", project.Id).
		Updates(map[string]any{"status": OpenSourceBountyStatusCompleted}).Error)

	active, err := ListOwnedOpenSourceBounties(owner.Id)
	require.NoError(t, err)
	require.Len(t, active, 1)
	assert.Zero(t, active[0].ArchivedAt)

	_, err = ArchiveOpenSourceBounty(stranger.Id, project.Id)
	assert.Equal(t, "OPEN_SOURCE_BOUNTY_NOT_FOUND", OpenSourceBountyErrorCode(err))

	archived, err := ArchiveOpenSourceBounty(owner.Id, project.Id)
	require.NoError(t, err)
	assert.NotZero(t, archived.ArchivedAt)
	active, err = ListOwnedOpenSourceBounties(owner.Id)
	require.NoError(t, err)
	assert.Empty(t, active)
	archivedItems, err := ListOwnedOpenSourceBountiesFiltered(owner.Id, true)
	require.NoError(t, err)
	require.Len(t, archivedItems, 1)
	assert.Equal(t, project.Id, archivedItems[0].Id)

	// Repeating the same action is safe for retrying clients.
	repeated, err := ArchiveOpenSourceBounty(owner.Id, project.Id)
	require.NoError(t, err)
	assert.Equal(t, archived.ArchivedAt, repeated.ArchivedAt)

	unarchived, err := UnarchiveOpenSourceBounty(owner.Id, project.Id)
	require.NoError(t, err)
	assert.Zero(t, unarchived.ArchivedAt)
	active, err = ListOwnedOpenSourceBounties(owner.Id)
	require.NoError(t, err)
	require.Len(t, active, 1)
	assert.Equal(t, project.Id, active[0].Id)
}

func TestOpenSourceBountyTipsAndMutualRatings(t *testing.T) {
	db := setupOpenSourceBountyTestDB(t)
	owner := createOpenSourceBountyUser(t, db, "rating-owner", 10_000, common.RoleCommonUser)
	participant := createOpenSourceBountyUser(t, db, "rating-participant", 0, common.RoleCommonUser)
	project, err := CreateOpenSourceBountyDraft(owner.Id, openSourceBountyInput("https://github.com/example/ratings", 2_000, 1))
	require.NoError(t, err)
	project, _, err = PublishOpenSourceBounty(owner.Id, project.Id)
	require.NoError(t, err)
	challenge, err := AcceptOpenSourceBounty(participant.Id, project.Id, "rating-participant")
	require.NoError(t, err)

	challenge, tipped, err := TipOpenSourceBountyChallenge(owner.Id, challenge.Id, 250, "Thanks for isolating the failing path.")
	require.NoError(t, err)
	assert.Equal(t, 250, tipped)
	assert.Equal(t, 250, challenge.TipQuota)

	notifications, err := ListOpenSourceBountyTipNotifications(participant.Id, 50)
	require.NoError(t, err)
	require.Len(t, notifications, 1)
	assert.Equal(t, owner.Id, notifications[0].SenderUserId)
	assert.Equal(t, owner.Username, notifications[0].SenderUsername)
	assert.Equal(t, project.Title, notifications[0].ProjectTitle)
	assert.Equal(t, 250, notifications[0].Quota)
	assert.Zero(t, notifications[0].RecipientReadAt)
	assert.Zero(t, notifications[0].ThankedAt)

	require.NoError(t, MarkOpenSourceBountyTipNotificationsRead(participant.Id))
	notifications, err = ListOpenSourceBountyTipNotifications(participant.Id, 50)
	require.NoError(t, err)
	assert.NotZero(t, notifications[0].RecipientReadAt)

	thanked, err := ThankOpenSourceBountyTip(participant.Id, notifications[0].Id)
	require.NoError(t, err)
	assert.NotZero(t, thanked.ThankedAt)
	thankedAgain, err := ThankOpenSourceBountyTip(participant.Id, notifications[0].Id)
	require.NoError(t, err)
	assert.Equal(t, thanked.ThankedAt, thankedAgain.ThankedAt, "thanking the same tip must be idempotent")

	stranger := createOpenSourceBountyUser(t, db, "rating-stranger", 0, common.RoleCommonUser)
	_, err = ThankOpenSourceBountyTip(stranger.Id, notifications[0].Id)
	assert.Equal(t, "OPEN_SOURCE_BOUNTY_TIP_NOT_FOUND", OpenSourceBountyErrorCode(err))

	publicProjects, _, err := ListOpenSourceBounties(stranger.Id, 1, 20)
	require.NoError(t, err)
	require.Len(t, publicProjects, 1)
	assert.Equal(t, int64(1), publicProjects[0].OwnerThankHeartCount)

	challenge, err = SubmitOpenSourceBountyChallenge(participant.Id, project.Id,
		"https://github.com/example/ratings/issues/1", "https://github.com/example/ratings/pull/2",
		"Partial fix ready for review.")
	require.NoError(t, err)
	challenge, _, err = ReviewOpenSourceBountyChallenge(owner.Id, challenge.Id, false, "More work is needed.", 4, "Strong diagnosis; the remaining edge case still needs coverage.")
	require.NoError(t, err)
	assert.Equal(t, OpenSourceBountyChallengeRejected, challenge.Status)
	assert.Equal(t, 4, challenge.OwnerRatingScore)

	challenge, err = RateOpenSourceBountyOwner(participant.Id, challenge.Id, 5, "Clear review and a motivating partial-work tip.")
	require.NoError(t, err)
	assert.Equal(t, 5, challenge.ContributorRatingScore)
	_, err = RateOpenSourceBountyOwner(participant.Id, challenge.Id, 1, "Attempted rating revision.")
	assert.Equal(t, "OPEN_SOURCE_BOUNTY_RATING_EXISTS", OpenSourceBountyErrorCode(err))

	var ownerAfter, participantAfter User
	require.NoError(t, db.First(&ownerAfter, owner.Id).Error)
	require.NoError(t, db.First(&participantAfter, participant.Id).Error)
	assert.Equal(t, 7_750, ownerAfter.Quota)
	assert.Equal(t, 250, participantAfter.Quota)

	accepted, err := ListAcceptedOpenSourceBounties(participant.Id)
	require.NoError(t, err)
	require.Len(t, accepted, 1)
	assert.Equal(t, float64(4), accepted[0].ParticipantRatingAverage)
	assert.Equal(t, int64(1), accepted[0].ParticipantRatingCount)
	assert.Equal(t, float64(5), accepted[0].OwnerRatingAverage)
	assert.Equal(t, int64(1), accepted[0].OwnerRatingCount)

	detail, err := GetOpenSourceBountyDetail(owner.Id, project.Id)
	require.NoError(t, err)
	require.Len(t, detail.Challenges, 1)
	assert.Equal(t, float64(4), detail.Challenges[0].ParticipantRatingAverage)
	assert.Equal(t, int64(1), detail.Challenges[0].ParticipantRatingCount)
	assert.Equal(t, float64(5), detail.Challenges[0].OwnerRatingAverage)
	assert.Equal(t, int64(1), detail.Challenges[0].OwnerRatingCount)

	var tipLedger OpenSourceBountyLedger
	require.NoError(t, db.Where("challenge_id = ? AND kind = ?", challenge.Id, OpenSourceBountyLedgerTipTransfer).First(&tipLedger).Error)
	assert.Equal(t, 250, tipLedger.Quota)
	assert.Equal(t, "Thanks for isolating the failing path.", tipLedger.Note)
	assert.NotZero(t, tipLedger.RecipientReadAt)
	assert.Equal(t, thanked.ThankedAt, tipLedger.ThankedAt)
}

func TestOpenSourceBountyNotificationsListReadAndKeepTipCompatibility(t *testing.T) {
	db := setupOpenSourceBountyTestDB(t)
	owner := createOpenSourceBountyUser(t, db, "notification-owner", 10_000, common.RoleCommonUser)
	recipient := createOpenSourceBountyUser(t, db, "notification-recipient", 0, common.RoleCommonUser)
	stranger := createOpenSourceBountyUser(t, db, "notification-stranger", 0, common.RoleCommonUser)
	project, err := CreateOpenSourceBountyDraft(owner.Id, openSourceBountyInput("https://github.com/example/notifications", 2_000, 1))
	require.NoError(t, err)

	rows := []OpenSourceBountyLedger{
		{ProjectId: project.Id, ChallengeId: 11, UserId: owner.Id, CounterpartyUserId: recipient.Id, Kind: OpenSourceBountyLedgerTipTransfer, Quota: 100, Note: "tip", CreatedAt: 1},
		{ProjectId: project.Id, ChallengeId: 12, UserId: owner.Id, CounterpartyUserId: recipient.Id, Kind: OpenSourceBountyLedgerRewardTransfer, Quota: 200, CreatedAt: 2},
		{ProjectId: project.Id, ChallengeId: 13, UserId: owner.Id, CounterpartyUserId: recipient.Id, Kind: OpenSourceBountyLedgerDisputeRewardTransfer, Quota: 300, Note: "settled", CreatedAt: 3},
		{ProjectId: project.Id, ChallengeId: 14, UserId: owner.Id, CounterpartyUserId: stranger.Id, Kind: OpenSourceBountyLedgerRewardTransfer, Quota: 400, CreatedAt: 4},
		{ProjectId: project.Id, UserId: owner.Id, Kind: OpenSourceBountyLedgerEscrowFund, Quota: 2_000, CreatedAt: 5},
	}
	require.NoError(t, db.Create(&rows).Error)

	notifications, err := ListOpenSourceBountyNotifications(recipient.Id, 50)
	require.NoError(t, err)
	require.Len(t, notifications, 3)
	assert.Equal(t, []string{
		OpenSourceBountyLedgerDisputeRewardTransfer,
		OpenSourceBountyLedgerRewardTransfer,
		OpenSourceBountyLedgerTipTransfer,
	}, []string{notifications[0].Kind, notifications[1].Kind, notifications[2].Kind})
	for _, notification := range notifications {
		assert.Equal(t, owner.Id, notification.SenderUserId)
		assert.Equal(t, owner.Username, notification.SenderUsername)
		assert.Equal(t, project.Title, notification.ProjectTitle)
		assert.Zero(t, notification.RecipientReadAt)
	}

	legacyTips, err := ListOpenSourceBountyTipNotifications(recipient.Id, 50)
	require.NoError(t, err)
	require.Len(t, legacyTips, 1)
	assert.Equal(t, OpenSourceBountyLedgerTipTransfer, rows[0].Kind)
	assert.Equal(t, rows[0].Id, legacyTips[0].Id)
	require.NoError(t, MarkOpenSourceBountyTipNotificationsRead(recipient.Id))

	notifications, err = ListOpenSourceBountyNotifications(recipient.Id, 50)
	require.NoError(t, err)
	assert.Zero(t, notifications[0].RecipientReadAt, "legacy read endpoint must not mark dispute rewards")
	assert.Zero(t, notifications[1].RecipientReadAt, "legacy read endpoint must not mark rewards")
	assert.NotZero(t, notifications[2].RecipientReadAt, "legacy read endpoint must still mark tips")

	_, err = ThankOpenSourceBountyTip(recipient.Id, rows[1].Id)
	assert.Equal(t, "OPEN_SOURCE_BOUNTY_TIP_NOT_FOUND", OpenSourceBountyErrorCode(err))
	require.NoError(t, MarkOpenSourceBountyNotificationsRead(recipient.Id))
	notifications, err = ListOpenSourceBountyNotifications(recipient.Id, 50)
	require.NoError(t, err)
	for _, notification := range notifications {
		assert.NotZero(t, notification.RecipientReadAt)
	}

	strangerNotifications, err := ListOpenSourceBountyNotifications(stranger.Id, 50)
	require.NoError(t, err)
	require.Len(t, strangerNotifications, 1)
	assert.Equal(t, 14, strangerNotifications[0].ChallengeId)
}

func TestOpenSourceBountyRESTTipIdempotencyReplayMismatchAndRetry(t *testing.T) {
	db := setupOpenSourceBountyTestDB(t)
	owner := createOpenSourceBountyUser(t, db, "rest-tip-owner", 10_000, common.RoleCommonUser)
	participant := createOpenSourceBountyUser(t, db, "rest-tip-contributor", 0, common.RoleCommonUser)
	project, err := CreateOpenSourceBountyDraft(owner.Id, openSourceBountyInput("https://github.com/example/rest-tip", 1_000, 1))
	require.NoError(t, err)
	project, _, err = PublishOpenSourceBounty(owner.Id, project.Id)
	require.NoError(t, err)
	challenge, err := AcceptOpenSourceBounty(participant.Id, project.Id, "rest-tip-contributor")
	require.NoError(t, err)

	key := "01988f13-4432-7b02-8d5e-9c82794fc001" // gitleaks:allow
	first, err := TipOpenSourceBountyChallengeIdempotent(owner.Id, challenge.Id, 250, "  Thanks for the focused diagnosis.  ", key)
	require.NoError(t, err)
	assert.Equal(t, 250, first.TransferredQuota)
	assert.Equal(t, 250, first.Challenge.TipQuota)
	second, err := TipOpenSourceBountyChallengeIdempotent(owner.Id, challenge.Id, 250, "Thanks for the focused diagnosis.", key)
	require.NoError(t, err)
	assert.Equal(t, first, second, "a replay must return the durably persisted first response")

	_, err = TipOpenSourceBountyChallengeIdempotent(owner.Id, challenge.Id, 251, "Thanks for the focused diagnosis.", key)
	assert.Equal(t, "OPEN_SOURCE_BOUNTY_IDEMPOTENCY_MISMATCH", OpenSourceBountyErrorCode(err))
	var ownerAfter, participantAfter User
	require.NoError(t, db.First(&ownerAfter, owner.Id).Error)
	require.NoError(t, db.First(&participantAfter, participant.Id).Error)
	assert.Equal(t, 8_750, ownerAfter.Quota)
	assert.Equal(t, 250, participantAfter.Quota)
	var tipLedgers int64
	require.NoError(t, db.Model(&OpenSourceBountyLedger{}).Where("challenge_id = ? AND kind = ?", challenge.Id, OpenSourceBountyLedgerTipTransfer).Count(&tipLedgers).Error)
	assert.Equal(t, int64(1), tipLedgers)
	var operations int64
	require.NoError(t, db.Model(&OpenSourceBountyRESTOperation{}).Count(&operations).Error)
	assert.Equal(t, int64(1), operations)

	failedKey := "01988f13-4432-7b02-8d5e-9c82794fc002" // gitleaks:allow
	_, err = TipOpenSourceBountyChallengeIdempotent(owner.Id, challenge.Id, 9_000, "Retry after replenishing balance.", failedKey)
	assert.Equal(t, "OPEN_SOURCE_BOUNTY_INSUFFICIENT_BALANCE", OpenSourceBountyErrorCode(err))
	require.NoError(t, db.Model(&User{}).Where("id = ?", owner.Id).Update("quota", 9_000).Error)
	retried, err := TipOpenSourceBountyChallengeIdempotent(owner.Id, challenge.Id, 9_000, "Retry after replenishing balance.", failedKey)
	require.NoError(t, err)
	assert.Equal(t, 9_000, retried.TransferredQuota, "a rolled-back attempt must not poison a later retry")
}

func TestOpenSourceBountyRESTTipConcurrentDuplicateTransfersOnce(t *testing.T) {
	db := setupOpenSourceBountyTestDB(t)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	owner := createOpenSourceBountyUser(t, db, "rest-race-owner", 10_000, common.RoleCommonUser)
	participant := createOpenSourceBountyUser(t, db, "rest-race-contributor", 0, common.RoleCommonUser)
	project, err := CreateOpenSourceBountyDraft(owner.Id, openSourceBountyInput("https://github.com/example/rest-race", 1_000, 1))
	require.NoError(t, err)
	project, _, err = PublishOpenSourceBounty(owner.Id, project.Id)
	require.NoError(t, err)
	challenge, err := AcceptOpenSourceBounty(participant.Id, project.Id, "rest-race-contributor")
	require.NoError(t, err)

	start := make(chan struct{})
	results := make(chan *OpenSourceBountyTipResult, 2)
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			<-start
			result, err := TipOpenSourceBountyChallengeIdempotent(owner.Id, challenge.Id, 300, "Concurrent response-loss retry.", "01988f13-4432-7b02-8d5e-9c82794fc003")
			results <- result
			errs <- err
		}()
	}
	close(start)
	for i := 0; i < 2; i++ {
		require.NoError(t, <-errs)
		assert.Equal(t, 300, (<-results).TransferredQuota)
	}
	var ownerAfter, participantAfter User
	require.NoError(t, db.First(&ownerAfter, owner.Id).Error)
	require.NoError(t, db.First(&participantAfter, participant.Id).Error)
	assert.Equal(t, 8_700, ownerAfter.Quota)
	assert.Equal(t, 300, participantAfter.Quota)
	var tipLedgers int64
	require.NoError(t, db.Model(&OpenSourceBountyLedger{}).Where("challenge_id = ? AND kind = ?", challenge.Id, OpenSourceBountyLedgerTipTransfer).Count(&tipLedgers).Error)
	assert.Equal(t, int64(1), tipLedgers)
}

func TestOpenSourceBountyPublicationChargesAdministratorConfiguredFee(t *testing.T) {
	db := setupOpenSourceBountyTestDB(t)
	setOpenSourceBountyFeeRateForTest("2.5")
	root := createOpenSourceBountyUser(t, db, "fee-recipient-root", 100, common.RoleRootUser)
	owner := createOpenSourceBountyUser(t, db, "fee-owner", 5_000, common.RoleCommonUser)
	project, err := CreateOpenSourceBountyDraft(owner.Id, openSourceBountyInput("https://github.com/example/fee", 333, 3))
	require.NoError(t, err)

	charge, err := CalculateOpenSourceBountyPublicationCharge(project)
	require.NoError(t, err)
	assert.Equal(t, 999, charge.GrossQuota)
	assert.Equal(t, 324, charge.NetRewardQuota)
	assert.Equal(t, 972, charge.EscrowQuota)
	assert.Equal(t, 250, charge.PlatformFeeRateBps)
	assert.Equal(t, 27, charge.PlatformFeeQuota, "each contributor slot rounds its fee up independently")
	assert.Equal(t, 999, charge.TotalQuota)

	project, charged, err := PublishOpenSourceBounty(owner.Id, project.Id)
	require.NoError(t, err)
	assert.Equal(t, 999, charged)
	assert.Equal(t, 324, project.NetRewardQuota)
	assert.Equal(t, 250, project.PlatformFeeRateBps)
	assert.Equal(t, 27, project.PlatformFeeQuota)

	var ownerAfter User
	require.NoError(t, db.First(&ownerAfter, owner.Id).Error)
	assert.Equal(t, 4_001, ownerAfter.Quota)
	var feeLedger OpenSourceBountyLedger
	require.NoError(t, db.Where("project_id = ? AND kind = ?", project.Id, OpenSourceBountyLedgerPlatformFee).First(&feeLedger).Error)
	assert.Equal(t, 27, feeLedger.Quota)
	assert.Equal(t, owner.Id, feeLedger.UserId)
	assert.Equal(t, root.Id, feeLedger.CounterpartyUserId)
	var rootAfter User
	require.NoError(t, db.First(&rootAfter, root.Id).Error)
	assert.Equal(t, 127, rootAfter.Quota, "the public platform fee is credited to the super administrator")

	_, refunded, err := CloseOpenSourceBounty(owner.Id, project.Id)
	require.NoError(t, err)
	assert.Equal(t, 972, refunded)
	require.NoError(t, db.First(&ownerAfter, owner.Id).Error)
	assert.Equal(t, 4_973, ownerAfter.Quota, "the public platform fee is retained from the gross listing price")
	require.NoError(t, db.First(&rootAfter, root.Id).Error)
	assert.Equal(t, 127, rootAfter.Quota, "closing a bounty does not refund the platform fee")
}

func TestOpenSourceBountyRootPublisherReceivesItsPlatformFee(t *testing.T) {
	db := setupOpenSourceBountyTestDB(t)
	setOpenSourceBountyFeeRateForTest("1")
	root := createOpenSourceBountyUser(t, db, "self-fee-root", 1_000, common.RoleRootUser)
	project, err := CreateOpenSourceBountyDraft(root.Id, openSourceBountyInput("https://github.com/example/self-fee", 1_000, 1))
	require.NoError(t, err)

	project, charged, err := PublishOpenSourceBounty(root.Id, project.Id)
	require.NoError(t, err)
	assert.Equal(t, 1_000, charged)
	assert.Equal(t, 990, project.EscrowQuota)
	var rootAfter User
	require.NoError(t, db.First(&rootAfter, root.Id).Error)
	assert.Equal(t, 10, rootAfter.Quota, "the root publisher pays gross and receives the fee in the same transaction")
	var feeLedger OpenSourceBountyLedger
	require.NoError(t, db.Where("project_id = ? AND kind = ?", project.Id, OpenSourceBountyLedgerPlatformFee).First(&feeLedger).Error)
	assert.Equal(t, root.Id, feeLedger.UserId)
	assert.Equal(t, root.Id, feeLedger.CounterpartyUserId)
}

func TestOpenSourceBountyPublicationRollsBackWhenFeeRecipientWalletWouldOverflow(t *testing.T) {
	db := setupOpenSourceBountyTestDB(t)
	setOpenSourceBountyFeeRateForTest("1")
	root := createOpenSourceBountyUser(t, db, "overflow-fee-recipient-root", common.MaxWalletQuota, common.RoleRootUser)
	owner := createOpenSourceBountyUser(t, db, "overflow-fee-owner", 2_000, common.RoleCommonUser)
	project, err := CreateOpenSourceBountyDraft(owner.Id, openSourceBountyInput("https://github.com/example/overflow-fee", 1_000, 1))
	require.NoError(t, err)

	_, _, err = PublishOpenSourceBounty(owner.Id, project.Id)
	require.Error(t, err)

	var storedOwner, storedRoot User
	require.NoError(t, db.First(&storedOwner, owner.Id).Error)
	require.NoError(t, db.First(&storedRoot, root.Id).Error)
	assert.Equal(t, 2_000, storedOwner.Quota)
	assert.Equal(t, common.MaxWalletQuota, storedRoot.Quota)
	var storedProject OpenSourceBountyProject
	require.NoError(t, db.First(&storedProject, project.Id).Error)
	assert.Equal(t, OpenSourceBountyStatusDraft, storedProject.Status)
	assert.Zero(t, storedProject.EscrowQuota)
	var ledgerCount int64
	require.NoError(t, db.Model(&OpenSourceBountyLedger{}).Where("project_id = ?", project.Id).Count(&ledgerCount).Error)
	assert.Zero(t, ledgerCount)
}

func TestOpenSourceBountyPublicationRollsBackWithoutFeeRecipient(t *testing.T) {
	db := setupOpenSourceBountyTestDB(t)
	setOpenSourceBountyFeeRateForTest("1")
	owner := createOpenSourceBountyUser(t, db, "missing-fee-recipient-owner", 1_000, common.RoleCommonUser)
	project, err := CreateOpenSourceBountyDraft(owner.Id, openSourceBountyInput("https://github.com/example/missing-fee-recipient", 1_000, 1))
	require.NoError(t, err)

	_, _, err = PublishOpenSourceBounty(owner.Id, project.Id)
	assert.Equal(t, "OPEN_SOURCE_BOUNTY_FEE_RECIPIENT_NOT_FOUND", OpenSourceBountyErrorCode(err))
	var ownerAfter User
	require.NoError(t, db.First(&ownerAfter, owner.Id).Error)
	assert.Equal(t, 1_000, ownerAfter.Quota, "the gross debit rolls back when the fee cannot be credited")
	require.NoError(t, db.First(project, project.Id).Error)
	assert.Equal(t, OpenSourceBountyStatusDraft, project.Status)
}

func TestOpenSourceBountyDailyCheckinRewardCanCoverPublicationFee(t *testing.T) {
	db := setupOpenSourceBountyTestDB(t)
	require.NoError(t, db.AutoMigrate(&Checkin{}))
	setOpenSourceBountyFeeRateForTest("1")
	root := createOpenSourceBountyUser(t, db, "checkin-fee-root", 0, common.RoleRootUser)
	owner := createOpenSourceBountyUser(t, db, "checkin-fee-owner", 990, common.RoleCommonUser)
	project, err := CreateOpenSourceBountyDraft(owner.Id, openSourceBountyInput("https://github.com/example/checkin-fee", 1_000, 1))
	require.NoError(t, err)

	_, _, err = PublishOpenSourceBounty(owner.Id, project.Id)
	assert.Equal(t, "OPEN_SOURCE_BOUNTY_INSUFFICIENT_BALANCE", OpenSourceBountyErrorCode(err))
	_, err = userCheckinWithoutTransaction(&Checkin{
		UserId: owner.Id, CheckinDate: "2026-08-04", QuotaAwarded: 10, CreatedAt: 1,
	}, owner.Id, 10)
	require.NoError(t, err)

	project, charged, err := PublishOpenSourceBounty(owner.Id, project.Id)
	require.NoError(t, err)
	assert.Equal(t, 1_000, charged)
	assert.Equal(t, 990, project.NetRewardQuota)
	assert.Equal(t, 10, project.PlatformFeeQuota)
	var ownerAfter User
	require.NoError(t, db.First(&ownerAfter, owner.Id).Error)
	assert.Zero(t, ownerAfter.Quota)
	var rootAfter User
	require.NoError(t, db.First(&rootAfter, root.Id).Error)
	assert.Equal(t, 10, rootAfter.Quota)
}

func TestOpenSourceBountyFeeRateParsesDecimalBasisPointsExactly(t *testing.T) {
	tests := []struct {
		value string
		want  int
	}{
		{value: "0", want: 0},
		{value: "0.29", want: 29},
		{value: "2.5", want: 250},
		{value: "2.55", want: 255},
		{value: "100.00", want: 10_000},
		{value: " 1.01 ", want: 101},
	}
	for _, test := range tests {
		t.Run(test.value, func(t *testing.T) {
			got, err := parseOpenSourceBountyFeeRateBasisPoints(test.value)
			require.NoError(t, err)
			assert.Equal(t, test.want, got)
			require.NoError(t, validateOptionValue(OpenSourceBountyFeeRateOptionKey, test.value))
		})
	}
	for _, value := range []string{"-1", ".5", "2.555", "100.01", "1e2", "NaN", "Inf"} {
		t.Run("invalid_"+value, func(t *testing.T) {
			_, err := parseOpenSourceBountyFeeRateBasisPoints(value)
			require.Error(t, err)
			require.Error(t, validateOptionValue(OpenSourceBountyFeeRateOptionKey, value))
		})
	}
}

func TestOpenSourceBountyDisputeAllowsThirdPartyEscrowIntervention(t *testing.T) {
	db := setupOpenSourceBountyTestDB(t)
	require.NoError(t, db.AutoMigrate(&OpenSourceBountyMCPConfirmation{}, &OpenSourceBountyMCPOperation{}))
	owner := createOpenSourceBountyUser(t, db, "dishonest-owner", 10_000, common.RoleCommonUser)
	participant := createOpenSourceBountyUser(t, db, "merged-contributor", 0, common.RoleCommonUser)
	admin := createOpenSourceBountyUser(t, db, "dispute-admin", 0, common.RoleAdminUser)
	project, err := CreateOpenSourceBountyDraft(owner.Id, openSourceBountyInput("https://github.com/example/dispute", 2_000, 1))
	require.NoError(t, err)
	project, _, err = PublishOpenSourceBounty(owner.Id, project.Id)
	require.NoError(t, err)
	challenge, err := AcceptOpenSourceBounty(participant.Id, project.Id, "merged-contributor")
	require.NoError(t, err)
	challenge, err = SubmitOpenSourceBountyChallenge(participant.Id, project.Id,
		"https://github.com/example/dispute/issues/7", "https://github.com/example/dispute/pull/8",
		"The fix was merged after passing verification.")
	require.NoError(t, err)
	challenge, _, err = ReviewOpenSourceBountyChallenge(owner.Id, challenge.Id, false, "Refusing payment despite merge.", 1, "Rejected after merge without a valid technical reason.")
	require.NoError(t, err)
	_, err = RateOpenSourceBountyOwner(participant.Id, challenge.Id, 1, "The fix was merged but the promised bounty was refused.")
	require.NoError(t, err)

	dispute, err := OpenOpenSourceBountyDispute(participant.Id, challenge.Id, "merged_but_unpaid", "The linked pull request was merged and satisfies the published acceptance criteria, but the publisher refused the escrowed reward.")
	require.NoError(t, err)
	assert.Equal(t, OpenSourceBountyDisputeOpen, dispute.Status)
	assert.Equal(t, challenge.PullRequestUrl, dispute.PullRequestUrl)
	assert.Equal(t, 1, dispute.OwnerRatingScore)
	assert.Equal(t, 1, dispute.ContributorRatingScore)

	adminCases, err := ListOpenSourceBountyDisputes(admin.Id, true)
	require.NoError(t, err)
	require.Len(t, adminCases, 1)
	assert.Equal(t, "merged-contributor", adminCases[0].ParticipantUsername)

	resolution := "The Issue reproduces a genuine defect, the linked pull request was merged, and the submitted verification satisfies the published rules. Escrow payment is enforced."
	payloadHash, err := OpenSourceBountyMCPPayloadHash(map[string]any{"dispute_id": dispute.Id, "action": "pay", "resolution": resolution, "reward_quota": dispute.RewardQuota})
	require.NoError(t, err)
	state, err := CreateOpenSourceBountyMCPConfirmation(admin.Id, "open_source_bounties.resolve_dispute", payloadHash)
	require.NoError(t, err)
	dispute, transferred, err := ResolveOpenSourceBountyDisputeWithMCPConfirmation(admin.Id, dispute.Id, "pay", resolution, OpenSourceBountyMCPConfirmedOperation{
		State: state, ToolName: "open_source_bounties.resolve_dispute", PayloadHash: payloadHash,
	})
	require.NoError(t, err)
	assert.Equal(t, 2_000, transferred)
	assert.Equal(t, OpenSourceBountyDisputeResolvedPaid, dispute.Status)

	var participantAfter User
	require.NoError(t, db.First(&participantAfter, participant.Id).Error)
	assert.Equal(t, 2_000, participantAfter.Quota)
	require.NoError(t, db.First(project, project.Id).Error)
	assert.Equal(t, 0, project.EscrowQuota)
	var ledger OpenSourceBountyLedger
	require.NoError(t, db.Where("challenge_id = ? AND kind = ?", challenge.Id, OpenSourceBountyLedgerDisputeRewardTransfer).First(&ledger).Error)
	assert.Equal(t, 2_000, ledger.Quota)
	rewardNotifications, err := ListOpenSourceBountyNotifications(participant.Id, 50)
	require.NoError(t, err)
	require.Len(t, rewardNotifications, 1)
	assert.Equal(t, OpenSourceBountyLedgerDisputeRewardTransfer, rewardNotifications[0].Kind)
	assert.Equal(t, challenge.Id, rewardNotifications[0].ChallengeId)
	assert.Equal(t, 2_000, rewardNotifications[0].Quota)
	persisted, found, err := GetOpenSourceBountyMCPOperationResult(admin.Id, "open_source_bounties.resolve_dispute", state)
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, float64(dispute.Id), persisted["dispute_id"])
	assert.Equal(t, float64(2_000), persisted["transferred_quota"], "a response-loss retry can recover the committed payout result")

	duplicate, duplicateTransferred, err := ResolveOpenSourceBountyDispute(admin.Id, dispute.Id, "pay", "Duplicate dispute resolution must not pay twice.")
	require.NoError(t, err)
	assert.Equal(t, OpenSourceBountyDisputeResolvedPaid, duplicate.Status)
	assert.Zero(t, duplicateTransferred)
	rewardNotifications, err = ListOpenSourceBountyNotifications(participant.Id, 50)
	require.NoError(t, err)
	require.Len(t, rewardNotifications, 1, "dispute payout replay must not duplicate its notification")
	require.NoError(t, db.First(&participantAfter, participant.Id).Error)
	assert.Equal(t, 2_000, participantAfter.Quota)
	var challengeAfter OpenSourceBountyChallenge
	require.NoError(t, db.First(&challengeAfter, challenge.Id).Error)
	assert.Equal(t, "Refusing payment despite merge.", challengeAfter.ReviewNote, "administrator resolution must preserve the original review note")
	assert.True(t, challengeAfter.OwnerRatingOverturned)
	accepted, err := ListAcceptedOpenSourceBounties(participant.Id)
	require.NoError(t, err)
	require.Len(t, accepted, 1)
	assert.Zero(t, accepted[0].ParticipantRatingCount, "an overturned rejection score must not reduce ordinary contributor reputation")
}

func TestOpenSourceBountyOpenDisputeFreezesEscrowAndRewardSlot(t *testing.T) {
	db := setupOpenSourceBountyTestDB(t)
	owner := createOpenSourceBountyUser(t, db, "freeze-owner", 10_000, common.RoleCommonUser)
	participant := createOpenSourceBountyUser(t, db, "freeze-contributor", 0, common.RoleCommonUser)
	other := createOpenSourceBountyUser(t, db, "waiting-contributor", 0, common.RoleCommonUser)
	admin := createOpenSourceBountyUser(t, db, "freeze-admin", 0, common.RoleAdminUser)
	project, err := CreateOpenSourceBountyDraft(owner.Id, openSourceBountyInput("https://github.com/example/freeze", 1_000, 1))
	require.NoError(t, err)
	project, _, err = PublishOpenSourceBounty(owner.Id, project.Id)
	require.NoError(t, err)
	challenge, err := AcceptOpenSourceBounty(participant.Id, project.Id, "freeze-contributor")
	require.NoError(t, err)
	challenge, err = SubmitOpenSourceBountyChallenge(participant.Id, project.Id, "https://github.com/example/freeze/issues/1", "https://github.com/example/freeze/pull/2", "Verified fix.")
	require.NoError(t, err)
	challenge, _, err = ReviewOpenSourceBountyChallenge(owner.Id, challenge.Id, false, "Payment rejected.", 2, "Rejected despite useful work.")
	require.NoError(t, err)

	_, _, err = CloseOpenSourceBounty(owner.Id, project.Id)
	assert.Equal(t, "OPEN_SOURCE_BOUNTY_APPEAL_WINDOW", OpenSourceBountyErrorCode(err))
	_, err = AcceptOpenSourceBounty(other.Id, project.Id, "waiting-contributor")
	assert.Equal(t, "OPEN_SOURCE_BOUNTY_FULL", OpenSourceBountyErrorCode(err))

	dispute, err := OpenOpenSourceBountyDispute(participant.Id, challenge.Id, "requirements_met_but_rejected", "The submitted fix meets the published requirements and the escrow must remain available during review.")
	require.NoError(t, err)

	_, _, err = CloseOpenSourceBounty(owner.Id, project.Id)
	assert.Equal(t, "OPEN_SOURCE_BOUNTY_OPEN_DISPUTES", OpenSourceBountyErrorCode(err))
	_, err = AcceptOpenSourceBounty(other.Id, project.Id, "waiting-contributor")
	assert.Equal(t, "OPEN_SOURCE_BOUNTY_FULL", OpenSourceBountyErrorCode(err))

	_, _, err = ResolveOpenSourceBountyDispute(admin.Id, dispute.Id, "deny", "The submitted evidence did not establish that all acceptance requirements were met.")
	require.NoError(t, err)
	_, refunded, err := CloseOpenSourceBounty(owner.Id, project.Id)
	require.NoError(t, err)
	assert.Equal(t, 1_000, refunded)
}

func TestOpenSourceBountyRejectedChallengeAppealWindowExpires(t *testing.T) {
	db := setupOpenSourceBountyTestDB(t)
	owner := createOpenSourceBountyUser(t, db, "appeal-owner", 10_000, common.RoleCommonUser)
	participant := createOpenSourceBountyUser(t, db, "appeal-contributor", 0, common.RoleCommonUser)
	project, err := CreateOpenSourceBountyDraft(owner.Id, openSourceBountyInput("https://github.com/example/appeal", 1_000, 1))
	require.NoError(t, err)
	project, _, err = PublishOpenSourceBounty(owner.Id, project.Id)
	require.NoError(t, err)
	challenge, err := AcceptOpenSourceBounty(participant.Id, project.Id, "appeal-contributor")
	require.NoError(t, err)
	challenge, err = SubmitOpenSourceBountyChallenge(participant.Id, project.Id, "https://github.com/example/appeal/issues/1", "https://github.com/example/appeal/pull/2", "Appeal evidence.")
	require.NoError(t, err)
	challenge, _, err = ReviewOpenSourceBountyChallenge(owner.Id, challenge.Id, false, "Submission rejected.", 2, "Requirements were not met.")
	require.NoError(t, err)

	expiredAt := common.GetTimestamp() - OpenSourceBountyAppealWindowSeconds - 1
	require.NoError(t, db.Model(&OpenSourceBountyChallenge{}).Where("id = ?", challenge.Id).Update("rejected_at", expiredAt).Error)
	_, err = OpenOpenSourceBountyDispute(participant.Id, challenge.Id, "requirements_met_but_rejected", "This filing occurs only after the published dispute window has already expired.")
	assert.Equal(t, "OPEN_SOURCE_BOUNTY_DISPUTE_WINDOW_EXPIRED", OpenSourceBountyErrorCode(err))

	_, refunded, err := CloseOpenSourceBounty(owner.Id, project.Id)
	require.NoError(t, err)
	assert.Equal(t, 1_000, refunded)
}

func TestOpenSourceBountyRejectedContributorCanRetryAfterAppealWindow(t *testing.T) {
	db := setupOpenSourceBountyTestDB(t)
	owner := createOpenSourceBountyUser(t, db, "retry-owner", 10_000, common.RoleCommonUser)
	participant := createOpenSourceBountyUser(t, db, "retry-contributor", 0, common.RoleCommonUser)
	project, err := CreateOpenSourceBountyDraft(owner.Id, openSourceBountyInput("https://github.com/example/retry", 1_000, 1))
	require.NoError(t, err)
	project, _, err = PublishOpenSourceBounty(owner.Id, project.Id)
	require.NoError(t, err)

	first, err := AcceptOpenSourceBounty(participant.Id, project.Id, participant.Username)
	require.NoError(t, err)
	first, err = SubmitOpenSourceBountyChallenge(participant.Id, project.Id,
		"https://github.com/example/retry/issues/1", "https://github.com/example/retry/pull/2", "First attempt for review.")
	require.NoError(t, err)
	first, _, err = ReviewOpenSourceBountyChallenge(owner.Id, first.Id, false, "Please address the remaining failure.", 2, "The first attempt is incomplete.")
	require.NoError(t, err)

	expiredAt := common.GetTimestamp() - OpenSourceBountyAppealWindowSeconds - 1
	require.NoError(t, db.Model(&OpenSourceBountyChallenge{}).Where("id = ?", first.Id).Update("rejected_at", expiredAt).Error)

	retry, err := AcceptOpenSourceBounty(participant.Id, project.Id, participant.Username)
	require.NoError(t, err)
	assert.NotEqual(t, first.Id, retry.Id)
	assert.Equal(t, OpenSourceBountyChallengeAccepted, retry.Status)

	var attempts []OpenSourceBountyChallenge
	require.NoError(t, db.Where("project_id = ? AND participant_user_id = ?", project.Id, participant.Id).Order("id ASC").Find(&attempts).Error)
	require.Len(t, attempts, 2)
	assert.Equal(t, OpenSourceBountyChallengeRejected, attempts[0].Status)
	assert.Equal(t, OpenSourceBountyChallengeAccepted, attempts[1].Status)
	require.NoError(t, db.Model(&attempts[0]).Update("updated_at", retry.UpdatedAt+100).Error)
	projects, _, err := ListOpenSourceBounties(participant.Id, 1, 20)
	require.NoError(t, err)
	require.Len(t, projects, 1)
	require.NotNil(t, projects[0].ViewerChallenge)
	assert.Equal(t, retry.Id, projects[0].ViewerChallenge.Id, "the active retry must remain the viewer's current challenge")

	_, err = AcceptOpenSourceBounty(participant.Id, project.Id, participant.Username)
	assert.Equal(t, "OPEN_SOURCE_BOUNTY_ALREADY_ACCEPTED", OpenSourceBountyErrorCode(err))
}

func TestOpenSourceBountyRetryMigrationDropsLegacyUniqueIndex(t *testing.T) {
	db := setupOpenSourceBountyTestDB(t)
	require.NoError(t, db.Exec("CREATE UNIQUE INDEX idx_open_source_bounty_participant ON open_source_bounty_challenges(project_id, participant_user_id)").Error)
	require.True(t, db.Migrator().HasIndex(&OpenSourceBountyChallenge{}, legacyOpenSourceBountyParticipantIndex))

	require.NoError(t, migrateOpenSourceBountyChallengeRetryIndex())
	assert.False(t, db.Migrator().HasIndex(&OpenSourceBountyChallenge{}, legacyOpenSourceBountyParticipantIndex))
	assert.True(t, db.Migrator().HasIndex(&OpenSourceBountyChallenge{}, "idx_open_source_bounty_project_participant"))
}

func TestOpenSourceBountyDisputeRejectsPartyAdministrators(t *testing.T) {
	db := setupOpenSourceBountyTestDB(t)
	thirdPartyAdmin := createOpenSourceBountyUser(t, db, "independent-admin", 0, common.RoleAdminUser)

	createRejectedDispute := func(repository string, ownerRole int, participantRole int) (User, User, *OpenSourceBountyDisputeView) {
		owner := createOpenSourceBountyUser(t, db, repository+"-owner", 10_000, ownerRole)
		participant := createOpenSourceBountyUser(t, db, repository+"-participant", 0, participantRole)
		project, err := CreateOpenSourceBountyDraft(owner.Id, openSourceBountyInput("https://github.com/example/"+repository, 1_000, 1))
		require.NoError(t, err)
		project, _, err = PublishOpenSourceBounty(owner.Id, project.Id)
		require.NoError(t, err)
		challenge, err := AcceptOpenSourceBounty(participant.Id, project.Id, repository+"-participant")
		require.NoError(t, err)
		challenge, err = SubmitOpenSourceBountyChallenge(participant.Id, project.Id, "https://github.com/example/"+repository+"/issues/1", "https://github.com/example/"+repository+"/pull/2", "Conflict evidence.")
		require.NoError(t, err)
		challenge, _, err = ReviewOpenSourceBountyChallenge(owner.Id, challenge.Id, false, "Submission rejected.", 1, "Disputed review outcome.")
		require.NoError(t, err)
		dispute, err := OpenOpenSourceBountyDispute(participant.Id, challenge.Id, "merged_but_unpaid", "The administrator conflict check must prevent either bounty party from deciding this claim.")
		require.NoError(t, err)
		return owner, participant, dispute
	}

	adminOwner, _, ownerDispute := createRejectedDispute("admin-owner-conflict", common.RoleAdminUser, common.RoleCommonUser)
	_, _, err := ResolveOpenSourceBountyDispute(adminOwner.Id, ownerDispute.Id, "deny", "The bounty owner must not deny a dispute against themselves.")
	assert.Equal(t, "OPEN_SOURCE_BOUNTY_DISPUTE_CONFLICT", OpenSourceBountyErrorCode(err))
	_, _, err = ResolveOpenSourceBountyDispute(thirdPartyAdmin.Id, ownerDispute.Id, "deny", "An independent administrator reviewed and denied this claim.")
	require.NoError(t, err)

	_, adminParticipant, participantDispute := createRejectedDispute("admin-participant-conflict", common.RoleCommonUser, common.RoleAdminUser)
	_, _, err = ResolveOpenSourceBountyDispute(adminParticipant.Id, participantDispute.Id, "pay", "The contributor must not force payment to themselves as administrator.")
	assert.Equal(t, "OPEN_SOURCE_BOUNTY_DISPUTE_CONFLICT", OpenSourceBountyErrorCode(err))
	_, _, err = ResolveOpenSourceBountyDispute(thirdPartyAdmin.Id, participantDispute.Id, "deny", "An independent administrator reviewed and denied this claim.")
	require.NoError(t, err)
}

func TestOpenSourceBountyConcurrentOpenCreatesOneDispute(t *testing.T) {
	db := setupOpenSourceBountyTestDB(t)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	owner := createOpenSourceBountyUser(t, db, "concurrent-owner", 10_000, common.RoleCommonUser)
	participant := createOpenSourceBountyUser(t, db, "concurrent-contributor", 0, common.RoleCommonUser)
	project, err := CreateOpenSourceBountyDraft(owner.Id, openSourceBountyInput("https://github.com/example/concurrent", 1_000, 1))
	require.NoError(t, err)
	project, _, err = PublishOpenSourceBounty(owner.Id, project.Id)
	require.NoError(t, err)
	challenge, err := AcceptOpenSourceBounty(participant.Id, project.Id, "concurrent-contributor")
	require.NoError(t, err)
	challenge, err = SubmitOpenSourceBountyChallenge(participant.Id, project.Id, "https://github.com/example/concurrent/issues/1", "https://github.com/example/concurrent/pull/2", "Concurrent filing evidence.")
	require.NoError(t, err)

	start := make(chan struct{})
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			<-start
			_, err := OpenOpenSourceBountyDispute(participant.Id, challenge.Id, "merged_but_unpaid", "Two concurrent filing attempts must still create exactly one open dispute for this challenge.")
			results <- err
		}()
	}
	close(start)
	successes := 0
	for i := 0; i < 2; i++ {
		if err := <-results; err == nil {
			successes++
		} else {
			assert.Equal(t, "OPEN_SOURCE_BOUNTY_DISPUTE_EXISTS", OpenSourceBountyErrorCode(err))
		}
	}
	assert.Equal(t, 1, successes)
	var count int64
	require.NoError(t, db.Model(&OpenSourceBountyDispute{}).Where("challenge_id = ? AND status = ?", challenge.Id, OpenSourceBountyDisputeOpen).Count(&count).Error)
	assert.Equal(t, int64(1), count)
}

func TestOpenSourceBountyConcurrentReviewAndResolveTransferOnce(t *testing.T) {
	db := setupOpenSourceBountyTestDB(t)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	owner := createOpenSourceBountyUser(t, db, "race-owner", 10_000, common.RoleCommonUser)
	participant := createOpenSourceBountyUser(t, db, "race-contributor", 0, common.RoleCommonUser)
	admin := createOpenSourceBountyUser(t, db, "race-admin", 0, common.RoleAdminUser)
	project, err := CreateOpenSourceBountyDraft(owner.Id, openSourceBountyInput("https://github.com/example/race", 2_000, 1))
	require.NoError(t, err)
	project, _, err = PublishOpenSourceBounty(owner.Id, project.Id)
	require.NoError(t, err)
	challenge, err := AcceptOpenSourceBounty(participant.Id, project.Id, "race-contributor")
	require.NoError(t, err)
	challenge, err = SubmitOpenSourceBountyChallenge(participant.Id, project.Id, "https://github.com/example/race/issues/1", "https://github.com/example/race/pull/2", "Fix ready.")
	require.NoError(t, err)
	dispute, err := OpenOpenSourceBountyDispute(participant.Id, challenge.Id, "merged_but_unpaid", "The contribution is complete; owner approval and administrator enforcement must not both transfer the reward.")
	require.NoError(t, err)

	start := make(chan struct{})
	done := make(chan error, 2)
	go func() {
		<-start
		_, _, err := ReviewOpenSourceBountyChallenge(owner.Id, challenge.Id, true, "Approved by owner.", 5, "Complete focused fix.")
		done <- err
	}()
	go func() {
		<-start
		_, _, err := ResolveOpenSourceBountyDispute(admin.Id, dispute.Id, "pay", "Administrator verified the linked Issue, pull request, and acceptance evidence.")
		done <- err
	}()
	close(start)
	for i := 0; i < 2; i++ {
		err := <-done
		if err != nil {
			assert.Contains(t, []string{"OPEN_SOURCE_BOUNTY_INVALID_CHALLENGE_STATE", "OPEN_SOURCE_BOUNTY_DISPUTE_RESOLVED"}, OpenSourceBountyErrorCode(err))
		}
	}
	var participantAfter User
	require.NoError(t, db.First(&participantAfter, participant.Id).Error)
	assert.Equal(t, 2_000, participantAfter.Quota)
	var payouts int64
	require.NoError(t, db.Model(&OpenSourceBountyLedger{}).Where("challenge_id = ? AND reward_payout_key IS NOT NULL", challenge.Id).Count(&payouts).Error)
	assert.Equal(t, int64(1), payouts)
}

func TestOpenSourceBountyDisputeKeepsSnapshotAndShowsLiveTipAndRatings(t *testing.T) {
	db := setupOpenSourceBountyTestDB(t)
	owner := createOpenSourceBountyUser(t, db, "evidence-owner", 10_000, common.RoleCommonUser)
	participant := createOpenSourceBountyUser(t, db, "evidence-contributor", 0, common.RoleCommonUser)
	admin := createOpenSourceBountyUser(t, db, "evidence-admin", 0, common.RoleAdminUser)
	project, err := CreateOpenSourceBountyDraft(owner.Id, openSourceBountyInput("https://github.com/example/evidence", 2_000, 1))
	require.NoError(t, err)
	project, _, err = PublishOpenSourceBounty(owner.Id, project.Id)
	require.NoError(t, err)
	challenge, err := AcceptOpenSourceBounty(participant.Id, project.Id, "evidence-contributor")
	require.NoError(t, err)
	challenge, err = SubmitOpenSourceBountyChallenge(participant.Id, project.Id, "https://github.com/example/evidence/issues/1", "https://github.com/example/evidence/pull/2", "Evidence submitted before the dispute.")
	require.NoError(t, err)

	dispute, err := OpenOpenSourceBountyDispute(participant.Id, challenge.Id, "merged_but_unpaid", "The submitted contribution is complete, and later tips and ratings must remain visible to the independent reviewer.")
	require.NoError(t, err)
	assert.Zero(t, dispute.TipQuotaSnapshot)
	assert.Zero(t, dispute.OwnerRatingScoreSnapshot)
	assert.Zero(t, dispute.ContributorRatingScoreSnapshot)
	assert.False(t, dispute.LiveEvidenceChanged)

	_, tipped, err := TipOpenSourceBountyChallenge(owner.Id, challenge.Id, 250, "Useful work completed before final adjudication.")
	require.NoError(t, err)
	assert.Equal(t, 250, tipped)
	challenge, _, err = ReviewOpenSourceBountyChallenge(owner.Id, challenge.Id, false, "The owner rejected the submitted result.", 2, "Useful diagnosis, but payment was refused.")
	require.NoError(t, err)
	_, err = RateOpenSourceBountyOwner(participant.Id, challenge.Id, 1, "The merged work was not paid as promised.")
	require.NoError(t, err)

	current, err := GetOpenSourceBountyDispute(admin.Id, dispute.Id, true)
	require.NoError(t, err)
	assert.Zero(t, current.TipQuotaSnapshot, "filing-time tip evidence must remain immutable")
	assert.Zero(t, current.OwnerRatingScoreSnapshot, "filing-time owner rating must remain immutable")
	assert.Zero(t, current.ContributorRatingScoreSnapshot, "filing-time contributor rating must remain immutable")
	assert.Equal(t, 250, current.TipQuota)
	assert.Equal(t, 2, current.OwnerRatingScore)
	assert.Equal(t, "Useful diagnosis, but payment was refused.", current.OwnerRatingComment)
	assert.Equal(t, 1, current.ContributorRatingScore)
	assert.Equal(t, "The merged work was not paid as promised.", current.ContributorRatingComment)
	assert.Equal(t, OpenSourceBountyChallengeRejected, current.ChallengeStatus)
	assert.True(t, current.LiveEvidenceChanged)
}

func TestOpenSourceBountyConcurrentOpenAndReviewPreserveDispute(t *testing.T) {
	db := setupOpenSourceBountyTestDB(t)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	owner := createOpenSourceBountyUser(t, db, "open-review-owner", 10_000, common.RoleCommonUser)
	participant := createOpenSourceBountyUser(t, db, "open-review-contributor", 0, common.RoleCommonUser)
	project, err := CreateOpenSourceBountyDraft(owner.Id, openSourceBountyInput("https://github.com/example/open-review", 1_000, 1))
	require.NoError(t, err)
	project, _, err = PublishOpenSourceBounty(owner.Id, project.Id)
	require.NoError(t, err)
	challenge, err := AcceptOpenSourceBounty(participant.Id, project.Id, "open-review-contributor")
	require.NoError(t, err)
	challenge, err = SubmitOpenSourceBountyChallenge(participant.Id, project.Id, "https://github.com/example/open-review/issues/1", "https://github.com/example/open-review/pull/2", "Concurrent dispute evidence.")
	require.NoError(t, err)

	start := make(chan struct{})
	done := make(chan error, 2)
	go func() {
		<-start
		_, err := OpenOpenSourceBountyDispute(participant.Id, challenge.Id, "requirements_met_but_rejected", "Opening a dispute while the publisher reviews must preserve one valid case without a lock inversion.")
		done <- err
	}()
	go func() {
		<-start
		_, _, err := ReviewOpenSourceBountyChallenge(owner.Id, challenge.Id, false, "Rejected while the dispute is being opened.", 2, "The work remains useful evidence for review.")
		done <- err
	}()
	close(start)
	for i := 0; i < 2; i++ {
		require.NoError(t, <-done)
	}
	var disputes []OpenSourceBountyDispute
	require.NoError(t, db.Where("challenge_id = ?", challenge.Id).Find(&disputes).Error)
	require.Len(t, disputes, 1)
	assert.Equal(t, OpenSourceBountyDisputeOpen, disputes[0].Status)
	require.NoError(t, db.First(&challenge, challenge.Id).Error)
	assert.Equal(t, OpenSourceBountyChallengeRejected, challenge.Status)
}

func TestOpenSourceBountyConcurrentTipAndResolvePreserveBothTransfers(t *testing.T) {
	db := setupOpenSourceBountyTestDB(t)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	owner := createOpenSourceBountyUser(t, db, "tip-resolve-owner", 10_000, common.RoleCommonUser)
	participant := createOpenSourceBountyUser(t, db, "tip-resolve-contributor", 0, common.RoleCommonUser)
	admin := createOpenSourceBountyUser(t, db, "tip-resolve-admin", 0, common.RoleAdminUser)
	project, err := CreateOpenSourceBountyDraft(owner.Id, openSourceBountyInput("https://github.com/example/tip-resolve", 2_000, 1))
	require.NoError(t, err)
	project, _, err = PublishOpenSourceBounty(owner.Id, project.Id)
	require.NoError(t, err)
	challenge, err := AcceptOpenSourceBounty(participant.Id, project.Id, "tip-resolve-contributor")
	require.NoError(t, err)
	challenge, err = SubmitOpenSourceBountyChallenge(participant.Id, project.Id, "https://github.com/example/tip-resolve/issues/1", "https://github.com/example/tip-resolve/pull/2", "Tip and payout evidence.")
	require.NoError(t, err)
	dispute, err := OpenOpenSourceBountyDispute(participant.Id, challenge.Id, "merged_but_unpaid", "A tip and an administrator payout may race, but both valid transfers must commit without deadlock or duplication.")
	require.NoError(t, err)

	start := make(chan struct{})
	done := make(chan error, 2)
	go func() {
		<-start
		_, _, err := TipOpenSourceBountyChallenge(owner.Id, challenge.Id, 100, "Partial-work tip during adjudication.")
		done <- err
	}()
	go func() {
		<-start
		_, _, err := ResolveOpenSourceBountyDispute(admin.Id, dispute.Id, "pay", "The administrator verified the submitted defect, merged fix, and published acceptance criteria.")
		done <- err
	}()
	close(start)
	for i := 0; i < 2; i++ {
		require.NoError(t, <-done)
	}
	var participantAfter User
	require.NoError(t, db.First(&participantAfter, participant.Id).Error)
	assert.Equal(t, 2_100, participantAfter.Quota)
	require.NoError(t, db.First(&challenge, challenge.Id).Error)
	assert.Equal(t, 100, challenge.TipQuota)
	var payouts int64
	require.NoError(t, db.Model(&OpenSourceBountyLedger{}).Where("challenge_id = ? AND reward_payout_key IS NOT NULL", challenge.Id).Count(&payouts).Error)
	assert.Equal(t, int64(1), payouts)
}

func TestOpenSourceBountyDisputeRejectsRevokedAdminAndInvalidClaimants(t *testing.T) {
	db := setupOpenSourceBountyTestDB(t)
	owner := createOpenSourceBountyUser(t, db, "claim-owner", 10_000, common.RoleCommonUser)
	participant := createOpenSourceBountyUser(t, db, "claim-contributor", 0, common.RoleCommonUser)
	admin := createOpenSourceBountyUser(t, db, "claim-admin", 0, common.RoleAdminUser)
	project, err := CreateOpenSourceBountyDraft(owner.Id, openSourceBountyInput("https://github.com/example/claims", 1_000, 2))
	require.NoError(t, err)
	project, _, err = PublishOpenSourceBounty(owner.Id, project.Id)
	require.NoError(t, err)
	challenge, err := AcceptOpenSourceBounty(participant.Id, project.Id, "claim-contributor")
	require.NoError(t, err)
	challenge, err = SubmitOpenSourceBountyChallenge(participant.Id, project.Id, "https://github.com/example/claims/issues/1", "https://github.com/example/claims/pull/2", "Claim evidence.")
	require.NoError(t, err)
	dispute, err := OpenOpenSourceBountyDispute(participant.Id, challenge.Id, "merged_but_unpaid", "The administrator role must be revalidated in the same transaction that would transfer escrow.")
	require.NoError(t, err)
	require.NoError(t, db.Model(&User{}).Where("id = ?", admin.Id).Update("role", common.RoleCommonUser).Error)
	_, _, err = ResolveOpenSourceBountyDispute(admin.Id, dispute.Id, "pay", "This revoked administrator must not be allowed to transfer escrowed funds.")
	assert.Equal(t, "OPEN_SOURCE_BOUNTY_FORBIDDEN", OpenSourceBountyErrorCode(err))

	require.NoError(t, db.Model(&User{}).Where("id = ?", admin.Id).Update("role", common.RoleAdminUser).Error)
	_, _, err = ResolveOpenSourceBountyDispute(admin.Id, dispute.Id, "deny", "The case remains open only until a currently authorized administrator resolves it.")
	require.NoError(t, err)
	_, err = OpenOpenSourceBountyDispute(participant.Id, challenge.Id, "merged_but_unpaid", "A resolved case is final for the same party and cannot repeatedly freeze the same reward slot.")
	assert.Equal(t, "OPEN_SOURCE_BOUNTY_DISPUTE_EXISTS", OpenSourceBountyErrorCode(err))

	ownerClaim, err := OpenOpenSourceBountyDispute(owner.Id, challenge.Id, "other", "The publisher may request review, but cannot turn an owner-filed claim into a payment to the contributor.")
	require.NoError(t, err)
	_, _, err = ResolveOpenSourceBountyDispute(admin.Id, ownerClaim.Id, "pay", "Owner-filed disputes cannot authorize an enforced contributor payment from escrow.")
	assert.Equal(t, "OPEN_SOURCE_BOUNTY_DISPUTE_NOT_PAYABLE", OpenSourceBountyErrorCode(err))

	other := createOpenSourceBountyUser(t, db, "withdrawn-contributor", 0, common.RoleCommonUser)
	withdrawn, err := AcceptOpenSourceBounty(other.Id, project.Id, "withdrawn-contributor")
	require.NoError(t, err)
	withdrawn, err = WithdrawOpenSourceBountyChallenge(other.Id, withdrawn.Id)
	require.NoError(t, err)
	_, err = OpenOpenSourceBountyDispute(other.Id, withdrawn.Id, "other", "A withdrawn challenge cannot be converted into a payable dispute after releasing its reward slot.")
	assert.Equal(t, "OPEN_SOURCE_BOUNTY_INVALID_CHALLENGE_STATE", OpenSourceBountyErrorCode(err))
}

func TestCloseOpenSourceBountyRefundsOnlyUnusedEscrow(t *testing.T) {
	db := setupOpenSourceBountyTestDB(t)
	owner := createOpenSourceBountyUser(t, db, "owner", 10_000, common.RoleCommonUser)
	participant := createOpenSourceBountyUser(t, db, "participant", 0, common.RoleCommonUser)
	project, err := CreateOpenSourceBountyDraft(owner.Id, openSourceBountyInput("https://github.com/example/refund", 2_000, 2))
	require.NoError(t, err)
	project, _, err = PublishOpenSourceBounty(owner.Id, project.Id)
	require.NoError(t, err)
	challenge, err := AcceptOpenSourceBounty(participant.Id, project.Id, "participant")
	require.NoError(t, err)

	_, _, err = CloseOpenSourceBounty(owner.Id, project.Id)
	assert.Equal(t, "OPEN_SOURCE_BOUNTY_ACTIVE_CHALLENGES", OpenSourceBountyErrorCode(err))
	_, err = WithdrawOpenSourceBountyChallenge(participant.Id, challenge.Id)
	require.NoError(t, err)

	project, refunded, err := CloseOpenSourceBounty(owner.Id, project.Id)
	require.NoError(t, err)
	assert.Equal(t, 4_000, refunded)
	assert.Equal(t, OpenSourceBountyStatusClosed, project.Status)

	var ownerAfter User
	require.NoError(t, db.First(&ownerAfter, owner.Id).Error)
	assert.Equal(t, 10_000, ownerAfter.Quota, "closing refunds all unused escrow when the fee rate is zero")
}

func TestOwnerCanCancelUnsubmittedChallengeBeforeClosingBounty(t *testing.T) {
	db := setupOpenSourceBountyTestDB(t)
	owner := createOpenSourceBountyUser(t, db, "cancel-owner", 10_000, common.RoleCommonUser)
	participant := createOpenSourceBountyUser(t, db, "cancel-participant", 0, common.RoleCommonUser)
	other := createOpenSourceBountyUser(t, db, "cancel-other", 0, common.RoleCommonUser)
	project, err := CreateOpenSourceBountyDraft(owner.Id, openSourceBountyInput("https://github.com/example/cancel", 1_000, 1))
	require.NoError(t, err)
	project, _, err = PublishOpenSourceBounty(owner.Id, project.Id)
	require.NoError(t, err)
	challenge, err := AcceptOpenSourceBounty(participant.Id, project.Id, "cancel-participant")
	require.NoError(t, err)

	_, err = CancelOpenSourceBountyChallenge(other.Id, challenge.Id)
	assert.Equal(t, "OPEN_SOURCE_BOUNTY_FORBIDDEN", OpenSourceBountyErrorCode(err))
	_, _, err = CloseOpenSourceBounty(owner.Id, project.Id)
	assert.Equal(t, "OPEN_SOURCE_BOUNTY_ACTIVE_CHALLENGES", OpenSourceBountyErrorCode(err))

	challenge, err = CancelOpenSourceBountyChallenge(owner.Id, challenge.Id)
	require.NoError(t, err)
	assert.Equal(t, OpenSourceBountyChallengeCancelled, challenge.Status)
	_, _, err = TipOpenSourceBountyChallenge(owner.Id, challenge.Id, 100, "Cancelled challenges must remain inactive.")
	assert.Equal(t, "OPEN_SOURCE_BOUNTY_INVALID_CHALLENGE_STATE", OpenSourceBountyErrorCode(err))
	_, err = OpenOpenSourceBountyDispute(participant.Id, challenge.Id, "other", "A cancelled challenge cannot be reopened through the dispute workflow.")
	assert.Equal(t, "OPEN_SOURCE_BOUNTY_INVALID_CHALLENGE_STATE", OpenSourceBountyErrorCode(err))

	project, refunded, err := CloseOpenSourceBounty(owner.Id, project.Id)
	require.NoError(t, err)
	assert.Equal(t, 1_000, refunded)
	assert.Equal(t, OpenSourceBountyStatusClosed, project.Status)
}

func TestOwnerCannotCancelSubmittedChallenge(t *testing.T) {
	db := setupOpenSourceBountyTestDB(t)
	owner := createOpenSourceBountyUser(t, db, "cancel-submitted-owner", 10_000, common.RoleCommonUser)
	participant := createOpenSourceBountyUser(t, db, "cancel-submitted-participant", 0, common.RoleCommonUser)
	project, err := CreateOpenSourceBountyDraft(owner.Id, openSourceBountyInput("https://github.com/example/cancel-submitted", 1_000, 1))
	require.NoError(t, err)
	project, _, err = PublishOpenSourceBounty(owner.Id, project.Id)
	require.NoError(t, err)
	challenge, err := AcceptOpenSourceBounty(participant.Id, project.Id, "cancel-submitted-participant")
	require.NoError(t, err)
	challenge, err = SubmitOpenSourceBountyChallenge(
		participant.Id,
		project.Id,
		"https://github.com/example/cancel-submitted/issues/1",
		"",
		"Submitted work must be reviewed instead of cancelled.",
	)
	require.NoError(t, err)

	_, err = CancelOpenSourceBountyChallenge(owner.Id, challenge.Id)
	assert.Equal(t, "OPEN_SOURCE_BOUNTY_INVALID_CHALLENGE_STATE", OpenSourceBountyErrorCode(err))
	_, _, err = CloseOpenSourceBounty(owner.Id, project.Id)
	assert.Equal(t, "OPEN_SOURCE_BOUNTY_ACTIVE_CHALLENGES", OpenSourceBountyErrorCode(err))
}

func TestPublishOpenSourceBountyInsufficientBalanceIsAtomic(t *testing.T) {
	db := setupOpenSourceBountyTestDB(t)
	owner := createOpenSourceBountyUser(t, db, "poor-owner", 999, common.RoleCommonUser)
	project, err := CreateOpenSourceBountyDraft(owner.Id, openSourceBountyInput("https://github.com/example/atomic", 1_000, 1))
	require.NoError(t, err)

	_, _, err = PublishOpenSourceBounty(owner.Id, project.Id)
	assert.Equal(t, "OPEN_SOURCE_BOUNTY_INSUFFICIENT_BALANCE", OpenSourceBountyErrorCode(err))
	require.NoError(t, db.First(project, project.Id).Error)
	assert.Equal(t, OpenSourceBountyStatusDraft, project.Status)
	assert.Zero(t, project.EscrowQuota)
	var ownerAfter User
	require.NoError(t, db.First(&ownerAfter, owner.Id).Error)
	assert.Equal(t, 999, ownerAfter.Quota)
	var ledgerCount int64
	require.NoError(t, db.Model(&OpenSourceBountyLedger{}).Count(&ledgerCount).Error)
	assert.Zero(t, ledgerCount)
}

func TestSubmitOpenSourceBountyRejectsEvidenceFromAnotherRepository(t *testing.T) {
	db := setupOpenSourceBountyTestDB(t)
	owner := createOpenSourceBountyUser(t, db, "owner", 10_000, common.RoleCommonUser)
	participant := createOpenSourceBountyUser(t, db, "participant", 0, common.RoleCommonUser)
	project, err := CreateOpenSourceBountyDraft(owner.Id, openSourceBountyInput("https://github.com/example/source", 1_000, 1))
	require.NoError(t, err)
	project, _, err = PublishOpenSourceBounty(owner.Id, project.Id)
	require.NoError(t, err)
	_, err = AcceptOpenSourceBounty(participant.Id, project.Id, "participant")
	require.NoError(t, err)

	_, err = SubmitOpenSourceBountyChallenge(
		participant.Id,
		project.Id,
		"https://github.com/other/project/issues/1",
		"https://github.com/example/source/pull/2",
		"",
	)
	assert.Equal(t, "OPEN_SOURCE_BOUNTY_EVIDENCE_REPOSITORY_MISMATCH", OpenSourceBountyErrorCode(err))
}

func TestSubmitOpenSourceBountyAcceptsEitherIssueOrPullRequestEvidence(t *testing.T) {
	db := setupOpenSourceBountyTestDB(t)
	owner := createOpenSourceBountyUser(t, db, "flexible-evidence-owner", 10_000, common.RoleCommonUser)
	issueOnlyContributor := createOpenSourceBountyUser(t, db, "issue-only-contributor", 0, common.RoleCommonUser)
	secondIssueContributor := createOpenSourceBountyUser(t, db, "second-issue-contributor", 0, common.RoleCommonUser)
	pullRequestOnlyContributor := createOpenSourceBountyUser(t, db, "pull-request-only-contributor", 0, common.RoleCommonUser)
	emptyEvidenceContributor := createOpenSourceBountyUser(t, db, "empty-evidence-contributor", 0, common.RoleCommonUser)
	project, err := CreateOpenSourceBountyDraft(owner.Id, openSourceBountyInput("https://github.com/example/flexible-evidence", 1_000, 4))
	require.NoError(t, err)
	project, _, err = PublishOpenSourceBounty(owner.Id, project.Id)
	require.NoError(t, err)

	for _, contributor := range []User{issueOnlyContributor, secondIssueContributor, pullRequestOnlyContributor, emptyEvidenceContributor} {
		_, err = AcceptOpenSourceBounty(contributor.Id, project.Id, contributor.Username)
		require.NoError(t, err)
	}

	issueOnly, err := SubmitOpenSourceBountyChallenge(
		issueOnlyContributor.Id,
		project.Id,
		"https://github.com/example/flexible-evidence/issues/1",
		"",
		"Issue documents the completed work.",
	)
	require.NoError(t, err)
	assert.Equal(t, "https://github.com/example/flexible-evidence/issues/1", issueOnly.IssueUrl)
	assert.Empty(t, issueOnly.PullRequestUrl)

	secondIssueOnly, err := SubmitOpenSourceBountyChallenge(
		secondIssueContributor.Id,
		project.Id,
		"https://github.com/example/flexible-evidence/issues/2",
		"",
		"A second Issue-only completion must not collide on an empty PR URL.",
	)
	require.NoError(t, err)
	assert.Equal(t, "https://github.com/example/flexible-evidence/issues/2", secondIssueOnly.IssueUrl)
	assert.Empty(t, secondIssueOnly.PullRequestUrl)

	pullRequestOnly, err := SubmitOpenSourceBountyChallenge(
		pullRequestOnlyContributor.Id,
		project.Id,
		"",
		"https://github.com/example/flexible-evidence/pull/3",
		"Pull request contains the completed work.",
	)
	require.NoError(t, err)
	assert.Empty(t, pullRequestOnly.IssueUrl)
	assert.Equal(t, "https://github.com/example/flexible-evidence/pull/3", pullRequestOnly.PullRequestUrl)

	_, err = SubmitOpenSourceBountyChallenge(emptyEvidenceContributor.Id, project.Id, "", "", "No link supplied.")
	assert.Equal(t, "OPEN_SOURCE_BOUNTY_EVIDENCE_REQUIRED", OpenSourceBountyErrorCode(err))
}

func TestGetOpenSourceBountyDetailListsEveryChallengeParticipant(t *testing.T) {
	db := setupOpenSourceBountyTestDB(t)
	owner := createOpenSourceBountyUser(t, db, "participant-list-owner", 10_000, common.RoleCommonUser)
	acceptedContributor := createOpenSourceBountyUser(t, db, "accepted-participant", 0, common.RoleCommonUser)
	submittedContributor := createOpenSourceBountyUser(t, db, "submitted-participant", 0, common.RoleCommonUser)
	withdrawnContributor := createOpenSourceBountyUser(t, db, "withdrawn-participant", 0, common.RoleCommonUser)
	project, err := CreateOpenSourceBountyDraft(owner.Id, openSourceBountyInput("https://github.com/example/participant-list", 1_000, 3))
	require.NoError(t, err)
	project, _, err = PublishOpenSourceBounty(owner.Id, project.Id)
	require.NoError(t, err)

	var withdrawnChallenge *OpenSourceBountyChallenge
	for _, contributor := range []User{acceptedContributor, submittedContributor, withdrawnContributor} {
		challenge, acceptErr := AcceptOpenSourceBounty(contributor.Id, project.Id, contributor.Username)
		err = acceptErr
		require.NoError(t, err)
		if contributor.Id == withdrawnContributor.Id {
			withdrawnChallenge = challenge
		}
	}
	require.NotNil(t, withdrawnChallenge)
	_, err = SubmitOpenSourceBountyChallenge(
		submittedContributor.Id,
		project.Id,
		"https://github.com/example/participant-list/issues/1",
		"",
		"Issue-only completion.",
	)
	require.NoError(t, err)
	_, err = WithdrawOpenSourceBountyChallenge(withdrawnContributor.Id, withdrawnChallenge.Id)
	require.NoError(t, err)

	detail, err := GetOpenSourceBountyDetail(owner.Id, project.Id)
	require.NoError(t, err)
	require.Len(t, detail.Challenges, 3)
	byUsername := make(map[string]OpenSourceBountyChallengeView, len(detail.Challenges))
	for _, challenge := range detail.Challenges {
		byUsername[challenge.ParticipantUsername] = challenge
	}
	assert.Equal(t, OpenSourceBountyChallengeAccepted, byUsername[acceptedContributor.Username].Status)
	assert.Equal(t, OpenSourceBountyChallengeSubmitted, byUsername[submittedContributor.Username].Status)
	assert.Equal(t, OpenSourceBountyChallengeWithdrawn, byUsername[withdrawnContributor.Username].Status)
	assert.Equal(t, acceptedContributor.Id, byUsername[acceptedContributor.Username].ParticipantUserId)
	assert.Equal(t, acceptedContributor.Username, byUsername[acceptedContributor.Username].GithubHandle)
}
