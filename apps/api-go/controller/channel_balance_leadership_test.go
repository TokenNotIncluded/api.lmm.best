package controller

import (
	"context"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/stretchr/testify/require"
)

func TestAutomaticChannelBalanceLeaderStartFailsClosedWithoutPostgres(t *testing.T) {
	previousMain := common.MainDatabaseType()
	previousLog := common.LogDatabaseType()
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, previousLog)
	t.Cleanup(func() { common.SetDatabaseTypes(previousMain, previousLog) })

	require.ErrorContains(t, StartAutomaticChannelBalanceUpdateWithContext(context.Background(), 1), "requires PostgreSQL")
	require.ErrorContains(t, StartAutomaticChannelBalanceUpdateWithContext(nil, 1), "context is nil")
	require.ErrorContains(t, StartAutomaticChannelBalanceUpdateWithContext(context.Background(), 0), "frequency must be positive")
}

func TestAutomaticChannelBalanceLoopStopsBeforeScanningWhenCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan struct{})
	go func() {
		runAutomaticChannelBalanceUpdates(ctx, time.Hour)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("automatic channel balance loop ignored cancellation")
	}
}
