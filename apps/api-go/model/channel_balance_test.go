package model

import (
	"context"
	"errors"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/stretchr/testify/require"
)

func TestChannelUpdateBalanceContextClassifiesDatabaseFailureAndPreservesContext(t *testing.T) {
	db := setupConsoleActivationTestDB(t)
	require.NoError(t, db.AutoMigrate(&Channel{}))
	channel := Channel{
		Name:   "balance-context-test",
		Key:    "test-key",
		Status: common.ChannelStatusEnabled,
	}
	require.NoError(t, db.Create(&channel).Error)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := channel.UpdateBalanceContext(ctx, 10)

	require.Error(t, err)
	require.ErrorIs(t, err, ErrChannelBalanceUpdate)
	require.True(t, errors.Is(err, context.Canceled), "context cancellation must remain discoverable: %v", err)
}
