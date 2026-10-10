// Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later
package model

import (
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/pkg/bountycontract"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func generalBountyTestInput() OpenSourceBountyDraftInput {
	return OpenSourceBountyDraftInput{
		Kind: bountycontract.General, PublisherType: bountycontract.Company,
		Title: "Write a product guide", Description: "Write a clear product guide with working examples and source references.",
		Rules:       "Deliver an editable document covering every agreed section and verify the examples.",
		RewardQuota: 500, RewardSlots: 2,
	}
}

func TestGeneralBountyDeadlineDoesNotPreventAcceptedDeliveryOrPayment(t *testing.T) {
	db := setupOpenSourceBountyTestDB(t)
	owner := createOpenSourceBountyUser(t, db, "general-owner", 10000, common.RoleCommonUser)
	participant := createOpenSourceBountyUser(t, db, "general-worker", 0, common.RoleCommonUser)
	late := createOpenSourceBountyUser(t, db, "general-late", 0, common.RoleCommonUser)
	input := generalBountyTestInput()
	input.DeadlineAt = common.GetTimestamp() + 3600
	project, err := CreateOpenSourceBountyDraft(owner.Id, input)
	require.NoError(t, err)
	assert.Equal(t, bountycontract.General, project.Kind)
	assert.Equal(t, bountycontract.Company, project.PublisherType)
	assert.Empty(t, project.RepositoryUrl)
	project, charged, err := PublishOpenSourceBounty(owner.Id, project.Id)
	require.NoError(t, err)
	assert.Equal(t, 1000, charged)
	challenge, err := AcceptOpenSourceBounty(participant.Id, project.Id, "")
	require.NoError(t, err)
	assert.Empty(t, challenge.GithubHandle)
	require.NoError(t, db.Model(project).Update("deadline_at", common.GetTimestamp()-1).Error)
	_, err = AcceptOpenSourceBounty(late.Id, project.Id, "")
	assert.Equal(t, "BOUNTY_DEADLINE_PASSED", OpenSourceBountyErrorCode(err))
	_, err = SetOpenSourceBountyPaused(owner.Id, project.Id, true)
	require.NoError(t, err)
	_, err = SetOpenSourceBountyPaused(owner.Id, project.Id, false)
	require.Error(t, err)
	_, err = SubmitBountyChallenge(participant.Id, project.Id, BountySubmissionInput{DeliveryUrl: "javascript:alert(1)"})
	assert.Equal(t, "BOUNTY_INVALID_DELIVERY", OpenSourceBountyErrorCode(err))
	challenge, err = SubmitBountyChallenge(participant.Id, project.Id, BountySubmissionInput{
		SubmissionNote: "Delivered all agreed sections and verified every included example.",
	})
	require.NoError(t, err)
	assert.Equal(t, OpenSourceBountyChallengeSubmitted, challenge.Status)
	assert.Empty(t, challenge.IssueUrl)
	assert.Empty(t, challenge.PullRequestUrl)
	_, transferred, err := ReviewOpenSourceBountyChallenge(owner.Id, challenge.Id, true, "Checked the document.", 5, "Complete delivery with verified examples.")
	require.NoError(t, err)
	assert.Equal(t, 500, transferred)
	_, transferred, err = ReviewOpenSourceBountyChallenge(owner.Id, challenge.Id, true, "Repeated request.", 5, "Complete delivery with verified examples.")
	require.Error(t, err)
	assert.Zero(t, transferred)
	require.NoError(t, db.First(&participant, participant.Id).Error)
	assert.Equal(t, 500, participant.Quota)
	require.NoError(t, db.First(&owner, owner.Id).Error)
	assert.Equal(t, 9000, owner.Quota)
	var transfers int64
	require.NoError(t, db.Model(&OpenSourceBountyLedger{}).Where("challenge_id = ? AND kind = ?", challenge.Id, OpenSourceBountyLedgerRewardTransfer).Count(&transfers).Error)
	assert.EqualValues(t, 1, transfers)
}

func TestGeneralBountyExpiredPublicationDoesNotDebitAndMetadataStaysFrozen(t *testing.T) {
	db := setupOpenSourceBountyTestDB(t)
	owner := createOpenSourceBountyUser(t, db, "deadline-owner", 10000, common.RoleCommonUser)
	input := generalBountyTestInput()
	project, err := CreateOpenSourceBountyDraft(owner.Id, input)
	require.NoError(t, err)
	require.NoError(t, db.Model(project).Update("deadline_at", common.GetTimestamp()-1).Error)
	_, charged, err := PublishOpenSourceBounty(owner.Id, project.Id)
	assert.Equal(t, "BOUNTY_DEADLINE_PASSED", OpenSourceBountyErrorCode(err))
	assert.Zero(t, charged)
	require.NoError(t, db.First(&owner, owner.Id).Error)
	assert.Equal(t, 10000, owner.Quota)
	project, err = UpdateOpenSourceBountyDraft(owner.Id, project.Id, input)
	require.NoError(t, err)
	project, _, err = PublishOpenSourceBounty(owner.Id, project.Id)
	require.NoError(t, err)
	input.Kind, input.PublisherType, input.DeadlineAt = bountycontract.OpenSource, bountycontract.Individual, common.GetTimestamp()+600
	input.RepositoryUrl = "https://github.com/example/project"
	input.Title = "Clarified product guide"
	updated, err := UpdateOpenSourceBountyContent(owner.Id, project.Id, input)
	require.NoError(t, err)
	assert.Equal(t, bountycontract.General, updated.Kind)
	assert.Equal(t, bountycontract.Company, updated.PublisherType)
	assert.Zero(t, updated.DeadlineAt)
	assert.Empty(t, updated.RepositoryUrl)
	assert.Equal(t, project.EscrowQuota, updated.EscrowQuota)
}

func TestGeneralBountyDeliveryIsPreservedInDisputeSnapshot(t *testing.T) {
	db := setupOpenSourceBountyTestDB(t)
	owner := createOpenSourceBountyUser(t, db, "evidence-owner", 10000, common.RoleCommonUser)
	participant := createOpenSourceBountyUser(t, db, "evidence-worker", 0, common.RoleCommonUser)
	project, err := CreateOpenSourceBountyDraft(owner.Id, generalBountyTestInput())
	require.NoError(t, err)
	project, _, err = PublishOpenSourceBounty(owner.Id, project.Id)
	require.NoError(t, err)
	_, err = AcceptOpenSourceBounty(participant.Id, project.Id, "")
	require.NoError(t, err)
	challenge, err := SubmitBountyChallenge(participant.Id, project.Id, BountySubmissionInput{DeliveryUrl: "https://example.com/original.pdf"})
	require.NoError(t, err)
	dispute, err := OpenOpenSourceBountyDispute(participant.Id, challenge.Id, "other", "The completed guide is available at the agreed delivery URL but remains unpaid.")
	require.NoError(t, err)
	assert.Equal(t, challenge.DeliveryUrl, dispute.DeliveryUrlSnapshot)
	assert.Equal(t, challenge.DeliveryUrl, dispute.DeliveryUrl)
	require.NoError(t, db.Model(challenge).Update("delivery_url", "https://example.com/changed.pdf").Error)
	var snapshot OpenSourceBountyDispute
	require.NoError(t, db.First(&snapshot, dispute.Id).Error)
	assert.Equal(t, "https://example.com/original.pdf", snapshot.DeliveryUrlSnapshot)
}

func TestGeneralBountyDisputeCanPayWithoutGithubEvidence(t *testing.T) {
	db := setupOpenSourceBountyTestDB(t)
	owner := createOpenSourceBountyUser(t, db, "dispute-owner", 10000, common.RoleCommonUser)
	participant := createOpenSourceBountyUser(t, db, "dispute-worker", 0, common.RoleCommonUser)
	admin := createOpenSourceBountyUser(t, db, "dispute-admin", 0, common.RoleAdminUser)
	project, err := CreateOpenSourceBountyDraft(owner.Id, generalBountyTestInput())
	require.NoError(t, err)
	project, _, err = PublishOpenSourceBounty(owner.Id, project.Id)
	require.NoError(t, err)
	_, err = AcceptOpenSourceBounty(participant.Id, project.Id, "")
	require.NoError(t, err)
	challenge, err := SubmitBountyChallenge(participant.Id, project.Id, BountySubmissionInput{
		SubmissionNote: "Delivered every agreed document section and verified the included examples.",
	})
	require.NoError(t, err)
	dispute, err := OpenOpenSourceBountyDispute(participant.Id, challenge.Id, "requirements_met_but_rejected", "Every agreed section is delivered; request an independent review of the completion evidence.")
	require.NoError(t, err)
	_, amount, err := ResolveOpenSourceBountyDispute(owner.Id, dispute.Id, "pay", "The owner must not adjudicate their own dispute.")
	require.Error(t, err)
	assert.Zero(t, amount)
	_, amount, err = ResolveOpenSourceBountyDispute(admin.Id, dispute.Id, "pay", "The independent review confirms every acceptance requirement is met.")
	require.NoError(t, err)
	assert.Equal(t, 500, amount)
	_, amount, err = ResolveOpenSourceBountyDispute(admin.Id, dispute.Id, "pay", "Repeated resolution must not transfer a second reward.")
	require.NoError(t, err)
	assert.Zero(t, amount)
	require.NoError(t, db.First(&participant, participant.Id).Error)
	assert.Equal(t, 500, participant.Quota)
}

func TestGeneralBountyLegacyRepositoryInputStillRequiresGithubEvidence(t *testing.T) {
	db := setupOpenSourceBountyTestDB(t)
	owner := createOpenSourceBountyUser(t, db, "legacy-owner", 10000, common.RoleCommonUser)
	participant := createOpenSourceBountyUser(t, db, "legacy-worker", 0, common.RoleCommonUser)
	project, err := CreateOpenSourceBountyDraft(owner.Id, openSourceBountyInput("https://github.com/example/project", 500, 1))
	require.NoError(t, err)
	assert.Equal(t, bountycontract.OpenSource, project.Kind)
	project, _, err = PublishOpenSourceBounty(owner.Id, project.Id)
	require.NoError(t, err)
	_, err = AcceptOpenSourceBounty(participant.Id, project.Id, "")
	require.Error(t, err)
	_, err = AcceptOpenSourceBounty(participant.Id, project.Id, "worker")
	require.NoError(t, err)
	_, err = SubmitBountyChallenge(participant.Id, project.Id, BountySubmissionInput{DeliveryUrl: "https://example.com/file"})
	require.Error(t, err)
	_, err = SubmitBountyChallenge(participant.Id, project.Id, BountySubmissionInput{IssueUrl: "https://github.com/other/project/issues/1"})
	require.Error(t, err)
	_, err = SubmitBountyChallenge(participant.Id, project.Id, BountySubmissionInput{IssueUrl: "https://github.com/example/project/issues/1"})
	require.NoError(t, err)
}
