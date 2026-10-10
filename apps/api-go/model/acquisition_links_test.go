package model

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestAcquisitionLinkDeletionPreservesHistoryAndStopsRecognition(t *testing.T) {
	db := acquisitionDB(t)
	require.NoError(t, db.AutoMigrate(&AcquisitionCost{}))
	ctx := context.Background()
	link, err := SaveAcquisitionLink(ctx, AcquisitionLink{Name: "Launch", Source: "community", Campaign: "fall", Target: "/guide"})
	require.NoError(t, err)
	visitorID := AcquisitionVisitorHash("link-delete-history")
	before, err := ObserveAcquisition(ctx, visitorID, 0, AcquisitionInput{Consent: true, Nonce: strings.Repeat("a", 32), Landing: "/guide", LinkID: link.ID, Source: "community"}, nil)
	require.NoError(t, err)
	require.Equal(t, "promotion_link", before.Evidence)
	now := time.Now().Unix()
	scope := AcquisitionCostScope{LinkID: link.ID, FromAt: now - 30*86400, ToAt: now - 20*86400, ObservationDays: 7, Currency: "USD"}
	_, err = SaveAcquisitionCost(ctx, scope, 2_000_000, 1)
	require.NoError(t, err)

	deleted, err := DeleteAcquisitionLink(ctx, link.ID)
	require.NoError(t, err)
	require.Positive(t, deleted.DeletedAt)
	_, err = DeleteAcquisitionLink(ctx, link.ID)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	_, err = SaveAcquisitionLink(ctx, AcquisitionLink{ID: link.ID, Name: "Renamed"})
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	_, err = PreviewAcquisitionLink(ctx, link.ID)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)

	var historical AcquisitionVisit
	require.NoError(t, db.First(&historical, before.ID).Error)
	require.Equal(t, link.ID, historical.LinkID)
	report, err := GetAcquisitionCostReport(ctx, scope)
	require.NoError(t, err)
	require.NotNil(t, report.Spend)
	require.Equal(t, int64(2_000_000), report.Spend.AmountMicros)
	after, err := ObserveAcquisition(ctx, visitorID, 0, AcquisitionInput{Consent: true, Nonce: strings.Repeat("b", 32), Landing: "/guide", LinkID: link.ID, Source: "community"}, nil)
	require.NoError(t, err)
	require.Empty(t, after.LinkID)
	require.Equal(t, "campaign_parameters", after.Evidence)
}

func TestAcquisitionLinkListFiltersSearchAndPaginates(t *testing.T) {
	acquisitionDB(t)
	ctx := context.Background()
	first, err := SaveAcquisitionLink(ctx, AcquisitionLink{Name: "Launch 100", Source: "community", Target: "/"})
	require.NoError(t, err)
	archived, err := SaveAcquisitionLink(ctx, AcquisitionLink{Name: "Older", Source: "docs", Target: "/guide"})
	require.NoError(t, err)
	archived.Archived = true
	_, err = SaveAcquisitionLink(ctx, archived)
	require.NoError(t, err)
	deleted, err := SaveAcquisitionLink(ctx, AcquisitionLink{Name: "Removed", Source: "social", Target: "/"})
	require.NoError(t, err)
	_, err = DeleteAcquisitionLink(ctx, deleted.ID)
	require.NoError(t, err)

	filter := AcquisitionLinkFilter{Page: 1, PageSize: 1, Status: "all"}
	items, total, err := ListAcquisitionLinks(ctx, filter)
	require.NoError(t, err)
	require.Equal(t, int64(2), total)
	require.Len(t, items, 1)
	filter.Page = 2
	items, total, err = ListAcquisitionLinks(ctx, filter)
	require.NoError(t, err)
	require.Equal(t, int64(2), total)
	require.Len(t, items, 1)
	filter.Page, filter.PageSize, filter.Status, filter.Search = 1, 20, "active", "100"
	items, total, err = ListAcquisitionLinks(ctx, filter)
	require.NoError(t, err)
	require.Equal(t, int64(1), total)
	require.Equal(t, first.ID, items[0].ID)
	filter.Search = "%"
	items, total, err = ListAcquisitionLinks(ctx, filter)
	require.NoError(t, err)
	require.Zero(t, total)
	require.Empty(t, items)
	filter.Status, filter.Search = "archived", "docs"
	items, total, err = ListAcquisitionLinks(ctx, filter)
	require.NoError(t, err)
	require.Equal(t, int64(1), total)
	require.Equal(t, archived.ID, items[0].ID)
	filter.Status, filter.Search = "deleted", ""
	items, total, err = ListAcquisitionLinks(ctx, filter)
	require.NoError(t, err)
	require.Equal(t, int64(1), total)
	require.Equal(t, deleted.ID, items[0].ID)
	filter.Status = "unknown"
	_, _, err = ListAcquisitionLinks(ctx, filter)
	require.ErrorIs(t, err, ErrAcquisitionInvalid)
}

func TestAcquisitionLinkMigrationKeepsExistingRowsVisible(t *testing.T) {
	db := acquisitionDB(t)
	require.NoError(t, db.Migrator().DropColumn(&AcquisitionLink{}, "deleted_at"))
	id := strings.Repeat("c", 32)
	require.NoError(t, db.Exec(
		"INSERT INTO acquisition_links (id, name, source, target, archived, created_at) VALUES (?, ?, ?, ?, ?, ?)",
		id, "Existing campaign", "docs", "/guide", false, time.Now().Unix(),
	).Error)
	require.NoError(t, db.AutoMigrate(&AcquisitionLink{}))
	items, total, err := ListAcquisitionLinks(context.Background(), AcquisitionLinkFilter{Page: 1, PageSize: 20, Status: "all"})
	require.NoError(t, err)
	require.Equal(t, int64(1), total)
	require.Equal(t, id, items[0].ID)
	require.Zero(t, items[0].DeletedAt)
}
