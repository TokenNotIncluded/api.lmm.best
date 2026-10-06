package model

import (
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestToolMarketAuthorEditPublishedSnapshotWithoutDraft(t *testing.T) {
	f := newMarketFixture(t, 100)
	require.Empty(t, f.service.DraftVersionID)
	var beforeVersions int64
	require.NoError(t, f.db.Model(&ToolMarketVersion{}).Count(&beforeVersions).Error)
	detail, err := GetToolMarketDetail(f.author.Id, f.service.ID, true)
	require.NoError(t, err)
	require.Equal(t, f.service.LiveVersionID, detail.Version.ID)
	require.Empty(t, detail.Service.DraftVersionID)
	var afterVersions int64
	require.NoError(t, f.db.Model(&ToolMarketVersion{}).Count(&afterVersions).Error)
	require.Equal(t, beforeVersions, afterVersions, "opening the editor must not create a draft")
	_, err = GetToolMarketDetail(f.buyer.Id, f.service.ID, true)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	_, err = GetToolMarketDetail(0, f.service.ID, true)
	require.ErrorIs(t, err, ErrToolMarketDenied)

	draft, err := SaveToolMarketDraft(f.author.Id, f.service.ID, marketTestDraft(200))
	require.NoError(t, err)
	detail, err = GetToolMarketDetail(f.author.Id, f.service.ID, true)
	require.NoError(t, err)
	require.Equal(t, draft.DraftVersionID, detail.Version.ID)
	require.Equal(t, 200, detail.Tools[0].PriceQuota)
	require.NoError(t, f.db.Model(&ToolMarketService{}).Where("id = ?", f.service.ID).Update("draft_version_id", "missing-draft").Error)
	_, err = GetToolMarketDetail(f.author.Id, f.service.ID, true)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound, "a broken nonempty pointer must not fall back")
}

func TestToolMarketAuthorEditActivatedPrivateSnapshot(t *testing.T) {
	f := newPrivateMarketFixture(t)
	require.NoError(t, ActivateToolMarketPrivate(f.owner.Id, f.service.ID, f.version.ID))
	detail, err := GetToolMarketDetail(f.owner.Id, f.service.ID, true)
	require.NoError(t, err)
	require.Equal(t, f.version.ID, detail.Version.ID)
	_, err = GetToolMarketDetail(f.other.Id, f.service.ID, true)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
}
