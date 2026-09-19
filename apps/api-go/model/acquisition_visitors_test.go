package model

import (
	"context"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestAcquisitionVisitorsAreBrowserEstimatesWithCoverage(t *testing.T) {
	db := acquisitionDB(t)
	now := time.Now().Unix()
	ctx := context.Background()
	require.NoError(t, db.Create(&AcquisitionConfig{ID: 1, StartedAt: now - 10*86400, LookbackDays: 30}).Error)
	require.NoError(t, db.Create(&User{Id: 90, Username: "admin", AffCode: "visitor-admin", Role: 10}).Error)
	for _, v := range []AcquisitionVisitor{{ID: "a"}, {ID: "b"}, {ID: "admin", UserID: 90}} {
		require.NoError(t, db.Create(&v).Error)
	}
	for _, v := range []AcquisitionVisit{
		{VisitorID: "a", Nonce: "a1", Source: "documentation", CreatedAt: now - 30},
		{VisitorID: "a", Nonce: "a2", Source: "documentation", CreatedAt: now - 20},
		{VisitorID: "a", Nonce: "a3", Source: "community", CreatedAt: now - 10},
		{VisitorID: "b", Nonce: "b1", Source: "unknown", CreatedAt: now - 10},
		{VisitorID: "admin", Nonce: "admin1", Source: "documentation", CreatedAt: now - 10},
	} {
		require.NoError(t, db.Create(&v).Error)
	}
	report, err := GetAcquisitionVisitorSummary(ctx, now-100, now)
	require.NoError(t, err)
	require.True(t, report.CoverageComplete)
	require.EqualValues(t, 2, report.ObservedVisitors)
	require.Len(t, report.Channels, 3)
	for _, row := range report.Channels {
		require.EqualValues(t, 1, row.Visitors)
	}
	partial, err := GetAcquisitionVisitorSummary(ctx, now-100*86400, now)
	require.NoError(t, err)
	require.False(t, partial.CoverageComplete)
	require.EqualValues(t, 2, partial.ObservedVisitors)
	require.Equal(t, now-10*86400, partial.AvailableFrom)
}
