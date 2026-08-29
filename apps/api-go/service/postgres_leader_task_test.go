package service

import (
	"context"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/stretchr/testify/require"
)

func TestScannerLeaderStartsFailClosedWithoutPostgres(t *testing.T) {
	previousMain := common.MainDatabaseType()
	previousLog := common.LogDatabaseType()
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, previousLog)
	t.Cleanup(func() { common.SetDatabaseTypes(previousMain, previousLog) })

	require.ErrorContains(t, StartCodexCredentialAutoRefreshTaskWithContext(context.Background()), "requires PostgreSQL")
	require.ErrorContains(t, StartSubscriptionMaintenanceScanWithContext(context.Background()), "requires PostgreSQL")
}

func TestScannerLeaderStartsRejectNilContext(t *testing.T) {
	require.ErrorContains(t, StartCodexCredentialAutoRefreshTaskWithContext(nil), "context is nil")
	require.ErrorContains(t, StartSubscriptionMaintenanceScanWithContext(nil), "context is nil")
}
