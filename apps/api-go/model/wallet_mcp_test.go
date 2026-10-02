package model

import (
	"errors"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestWalletTransferMCPConfirmationAtomicLifecycle(t *testing.T) {
	sender, other := transferUsers(t)
	require.NoError(t, DB.AutoMigrate(&OpenSourceBountyMCPConfirmation{}, &OpenSourceBountyMCPOperation{}))
	requestKey := "wallet-mcp-test-request-000001"
	hash, err := OpenSourceBountyMCPPayloadHash(map[string]any{"quota": 300, "request_key": requestKey, "auth_version": sender.AuthVersion})
	require.NoError(t, err)
	state, err := CreateOpenSourceBountyMCPConfirmation(sender.Id, "wallet.transfer.create", hash)
	require.NoError(t, err)
	op := OpenSourceBountyMCPConfirmedOperation{State: state, ToolName: "wallet.transfer.create", PayloadHash: hash}
	_, err = CreateWalletTransferWithMCPConfirmation(sender.Id, 300, requestKey, sender.AuthVersion+1, op)
	require.ErrorIs(t, err, ErrWalletTransferUnavailable)
	_, err = CreateWalletTransferWithMCPConfirmation(other.Id, 300, requestKey, other.AuthVersion, op)
	require.Error(t, err)
	// A fault between the wallet write and operation receipt must roll back
	// the debit, transfer row and confirmation consumption as one transaction.
	injected := errors.New("test-only confirmation receipt failure")
	require.NoError(t, DB.Callback().Create().Before("gorm:create").Register("wallet_mcp_receipt_fault", func(tx *gorm.DB) {
		if tx.Statement.Schema != nil && tx.Statement.Schema.Table == "open_source_bounty_mcp_operations" {
			tx.AddError(injected)
		}
	}))
	t.Cleanup(func() { _ = DB.Callback().Create().Remove("wallet_mcp_receipt_fault") })
	_, err = CreateWalletTransferWithMCPConfirmation(sender.Id, 300, requestKey, sender.AuthVersion, op)
	require.ErrorIs(t, err, injected)
	require.NoError(t, DB.Callback().Create().Remove("wallet_mcp_receipt_fault"))
	var confirmation OpenSourceBountyMCPConfirmation
	require.NoError(t, DB.First(&confirmation, "id = ?", state).Error)
	require.Zero(t, confirmation.ConsumedAt)
	var before User
	require.NoError(t, DB.First(&before, sender.Id).Error)
	require.Equal(t, 1000, before.Quota)
	var count int64
	require.NoError(t, DB.Model(&WalletTransfer{}).Where("sender_id = ?", sender.Id).Count(&count).Error)
	require.Zero(t, count)
	var wg sync.WaitGroup
	results := make(chan *WalletTransfer, 10)
	errs := make(chan error, 10)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			transfer, err := CreateWalletTransferWithMCPConfirmation(sender.Id, 300, requestKey, sender.AuthVersion, op)
			results <- transfer
			errs <- err
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	var created *WalletTransfer
	for transfer := range results {
		require.NotNil(t, transfer)
		if created == nil {
			created = transfer
		} else {
			require.Equal(t, created.Id, transfer.Id)
		}
	}
	_, err = CreateWalletTransferWithMCPConfirmation(sender.Id, 301, requestKey, sender.AuthVersion, op)
	require.Error(t, err)
	var after User
	require.NoError(t, DB.First(&after, sender.Id).Error)
	require.Equal(t, 700, after.Quota)
	require.NoError(t, DB.First(&confirmation, "id = ?", state).Error)
	require.Positive(t, confirmation.ConsumedAt)
	cancelHash, err := OpenSourceBountyMCPPayloadHash(map[string]any{"transfer_id": created.Id, "quota": created.Quota})
	require.NoError(t, err)
	cancelState, err := CreateOpenSourceBountyMCPConfirmation(sender.Id, "wallet.transfer.cancel", cancelHash)
	require.NoError(t, err)
	cancel := OpenSourceBountyMCPConfirmedOperation{State: cancelState, ToolName: "wallet.transfer.cancel", PayloadHash: cancelHash}
	require.ErrorIs(t, CancelWalletTransferWithMCPConfirmation(created.Id, other.Id, other.AuthVersion, cancel), ErrWalletTransferUnavailable)
	require.ErrorIs(t, CancelWalletTransferWithMCPConfirmation(created.Id, sender.Id, sender.AuthVersion+1, cancel), ErrWalletTransferUnavailable)
	for i := 0; i < 3; i++ {
		require.NoError(t, CancelWalletTransferWithMCPConfirmation(created.Id, sender.Id, sender.AuthVersion, cancel))
	}
	require.NoError(t, DB.First(&after, sender.Id).Error)
	require.Equal(t, 1000, after.Quota)
}

func TestWalletTransferMCPConfirmationPostgres(t *testing.T) {
	db := openIsolatedPostgresCacheTestDB(t, &User{}, &WalletTransfer{}, &OpenSourceBountyMCPConfirmation{}, &OpenSourceBountyMCPOperation{})
	previousDB, previousLog := DB, LOG_DB
	DB, LOG_DB = db, db
	t.Cleanup(func() { DB, LOG_DB = previousDB, previousLog })
	usePostgresDatabaseType(t)
	TestWalletTransferMCPConfirmationAtomicLifecycle(t)
}
