package model

import (
	"sync"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/stretchr/testify/require"
)

func transferUsers(t *testing.T) (User, User) {
	t.Helper()
	if DB.Dialector.Name() == "sqlite" {
		truncateTables(t)
	} else {
		require.NoError(t, DB.Exec("DELETE FROM users").Error)
	}
	require.NoError(t, DB.AutoMigrate(&WalletTransfer{}))
	require.NoError(t, DB.Exec("DELETE FROM wallet_transfers").Error)
	sender := User{Username: "transfer-sender", AffCode: "transfer-sender", Status: common.UserStatusEnabled, Quota: 1000}
	recipient := User{Username: "transfer-recipient", AffCode: "transfer-recipient", DisplayName: "Receiver", Email: "receiver@example.test", Status: common.UserStatusEnabled, Quota: 100}
	require.NoError(t, DB.Create(&sender).Error)
	require.NoError(t, DB.Create(&recipient).Error)
	return sender, recipient
}

func TestWalletTransferLifecycleAndRetries(t *testing.T) {
	sender, recipient := transferUsers(t)
	transfer, err := CreateWalletTransfer(sender.Id, 300, "request-0000000001")
	require.NoError(t, err)
	retry, err := CreateWalletTransfer(sender.Id, 300, "request-0000000001")
	require.NoError(t, err)
	require.Equal(t, transfer.Id, retry.Id)
	require.NoError(t, DB.First(&sender, sender.Id).Error)
	require.Equal(t, 700, sender.Quota)
	_, err = CreateWalletTransfer(sender.Id, 301, "request-0000000001")
	require.ErrorIs(t, err, ErrWalletTransferInvalid)
	_, err = ClaimWalletTransfer(transfer.Token, sender.Id)
	require.ErrorIs(t, err, ErrWalletTransferUnavailable)
	claimed, err := ClaimWalletTransfer(transfer.Token, recipient.Id)
	require.NoError(t, err)
	require.Equal(t, "claimed", claimed.Status)
	require.Positive(t, claimed.ClaimedAt)
	require.Equal(t, recipient.Email, claimed.RecipientEmail)
	require.Equal(t, recipient.DisplayName, claimed.RecipientName)
	_, err = ClaimWalletTransfer(transfer.Token, recipient.Id)
	require.NoError(t, err)
	require.NoError(t, DB.First(&recipient, recipient.Id).Error)
	require.Equal(t, 400, recipient.Quota)
	require.ErrorIs(t, CancelWalletTransfer(transfer.Id, sender.Id), ErrWalletTransferUnavailable)
	own, err := ListWalletTransfers(sender.Id, 0)
	require.NoError(t, err)
	require.Len(t, own, 1)
	other, err := ListWalletTransfers(recipient.Id, 0)
	require.NoError(t, err)
	require.Empty(t, other)
	require.Equal(t, 1100, sender.Quota+recipient.Quota)
}

func TestWalletTransferRefundAndAuthorization(t *testing.T) {
	sender, recipient := transferUsers(t)
	transfer, err := CreateWalletTransfer(sender.Id, 1000, "request-0000000002")
	require.NoError(t, err)
	require.ErrorIs(t, CancelWalletTransfer(transfer.Id, recipient.Id), ErrWalletTransferUnavailable)
	require.NoError(t, CancelWalletTransfer(transfer.Id, sender.Id))
	require.NoError(t, CancelWalletTransfer(transfer.Id, sender.Id))
	_, err = ClaimWalletTransfer(transfer.Token, recipient.Id)
	require.ErrorIs(t, err, ErrWalletTransferUnavailable)
	require.NoError(t, DB.First(&sender, sender.Id).Error)
	require.Equal(t, 1000, sender.Quota)
}

func TestWalletTransferValidationAndRollback(t *testing.T) {
	sender, recipient := transferUsers(t)
	for _, quota := range []int{0, -1, 1001, common.MaxWalletQuota + 1} {
		_, err := CreateWalletTransfer(sender.Id, quota, "request-0000000003")
		require.Error(t, err)
	}
	require.NoError(t, DB.First(&sender, sender.Id).Error)
	require.Equal(t, 1000, sender.Quota)
	transfer, err := CreateWalletTransfer(sender.Id, 100, "request-0000000004")
	require.NoError(t, err)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", recipient.Id).Update("quota", common.MaxWalletQuota).Error)
	_, err = ClaimWalletTransfer(transfer.Token, recipient.Id)
	require.ErrorIs(t, err, ErrWalletQuotaOutOfRange)
	pending, err := InspectWalletTransfer(transfer.Token)
	require.NoError(t, err)
	require.Equal(t, "pending", pending.Status)
	require.Zero(t, pending.RecipientID)
	_, err = InspectWalletTransfer("bad")
	require.ErrorIs(t, err, ErrWalletTransferUnavailable)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", recipient.Id).Updates(map[string]interface{}{"quota": 100, "status": common.UserStatusDisabled}).Error)
	_, err = ClaimWalletTransfer(transfer.Token, recipient.Id)
	require.Error(t, err)
}

func TestWalletTransferConcurrentClaimAndCancel(t *testing.T) {
	sender, recipient := transferUsers(t)
	transfer, err := CreateWalletTransfer(sender.Id, 300, "request-0000000005")
	require.NoError(t, err)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if i%2 == 0 {
				_, _ = ClaimWalletTransfer(transfer.Token, recipient.Id)
			} else {
				_ = CancelWalletTransfer(transfer.Id, sender.Id)
			}
		}(i)
	}
	wg.Wait()
	require.NoError(t, DB.First(&sender, sender.Id).Error)
	require.NoError(t, DB.First(&recipient, recipient.Id).Error)
	require.Equal(t, 1100, sender.Quota+recipient.Quota)
	state, err := InspectWalletTransfer(transfer.Token)
	require.NoError(t, err)
	if state.Status == "claimed" {
		require.Equal(t, 700, sender.Quota)
		require.Equal(t, 400, recipient.Quota)
	} else {
		require.Equal(t, "cancelled", state.Status)
		require.Equal(t, 1000, sender.Quota)
		require.Equal(t, 100, recipient.Quota)
	}
}

func TestWalletTransferConcurrentCreationRetry(t *testing.T) {
	sender, _ := transferUsers(t)
	var wg sync.WaitGroup
	errs := make(chan error, 10)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := CreateWalletTransfer(sender.Id, 300, "request-0000000006")
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	require.NoError(t, DB.First(&sender, sender.Id).Error)
	require.Equal(t, 700, sender.Quota)
	records, err := ListWalletTransfers(sender.Id, 0)
	require.NoError(t, err)
	require.Len(t, records, 1)
}

func TestWalletTransferPostgresConcurrency(t *testing.T) {
	db := openIsolatedPostgresCacheTestDB(t, &User{}, &WalletTransfer{})
	previousDB, previousLog := DB, LOG_DB
	DB, LOG_DB = db, db
	t.Cleanup(func() { DB, LOG_DB = previousDB, previousLog })
	usePostgresDatabaseType(t)
	t.Run("creation-retry", TestWalletTransferConcurrentCreationRetry)
	t.Run("claim-cancel", TestWalletTransferConcurrentClaimAndCancel)
	t.Run("lifecycle", TestWalletTransferLifecycleAndRetries)
	t.Run("rollback", TestWalletTransferValidationAndRollback)
}
