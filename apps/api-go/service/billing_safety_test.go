package service

import (
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/model"
	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type billingSafetyFunding struct {
	calls []int
	fail  bool
}

func (*billingSafetyFunding) Source() string       { return BillingSourceWallet }
func (*billingSafetyFunding) PreConsume(int) error { return nil }
func (*billingSafetyFunding) Refund() error        { return nil }
func (f *billingSafetyFunding) Settle(delta int) error {
	f.calls = append(f.calls, delta)
	if f.fail {
		return errors.New("funding unavailable")
	}
	return nil
}

// Any call through this sentinel is a failure: validation must run first.
type billingSafetyForbiddenSettler struct{ relaycommon.BillingSettler }

func (*billingSafetyForbiddenSettler) GetPreConsumedQuota() int {
	panic("negative total reached the billing getter")
}

func (*billingSafetyForbiddenSettler) Settle(int) error {
	panic("negative total reached settlement")
}

func TestBillingSafetyPublicNegativeTotal(t *testing.T) {
	for _, info := range []*relaycommon.RelayInfo{
		nil,
		{FinalPreConsumedQuota: 100},
		{Billing: &billingSafetyForbiddenSettler{}},
	} {
		require.ErrorContains(t, SettleBilling(nil, info, -1), "cannot be negative")
	}
}

func TestBillingSafetyInvalidSessionState(t *testing.T) {
	for _, state := range []string{"active", "settled", "refunded"} {
		for _, actual := range []int{-101, -1, 0, 50, 100, 150} {
			if actual >= 0 && state != "refunded" {
				continue
			}
			t.Run(fmt.Sprintf("%s/%d", state, actual), func(t *testing.T) {
				funding := &billingSafetyFunding{}
				s := &BillingSession{
					funding: funding, relayInfo: &relaycommon.RelayInfo{IsPlayground: true, SubscriptionPostDelta: 23},
					preConsumedQuota: 100, tokenConsumed: 100,
					settled: state == "settled", refunded: state == "refunded",
				}
				require.Error(t, s.Settle(actual))
				require.Empty(t, funding.calls)
				require.Equal(t, state == "settled", s.settled)
				require.Equal(t, state == "refunded", s.refunded)
				require.False(t, s.fundingSettled)
				require.False(t, s.settlementAttempted)
				require.Nil(t, s.subscriptionResult)
				require.Equal(t, 100, s.GetPreConsumedQuota())
				require.Equal(t, 100, s.tokenConsumed)
				require.EqualValues(t, 23, s.relayInfo.SubscriptionPostDelta)
				require.Equal(t, state == "active", s.NeedsRefund())
			})
		}
	}
}

func TestBillingSafetyManagedSubscriptionGuards(t *testing.T) {
	for _, refunded := range []bool{false, true} {
		s := &BillingSession{funding: &SubscriptionFunding{managed: true}, refunded: refunded}
		require.ErrorContains(t, s.Settle(-1), "invalid subscription settlement")
		if refunded {
			require.ErrorContains(t, s.Settle(0), "invalid subscription settlement")
		}
		require.False(t, s.settlementAttempted)
		require.Nil(t, s.subscriptionResult)
	}
}

func TestBillingSafetyFundingFailureRemainsRetryable(t *testing.T) {
	funding := &billingSafetyFunding{fail: true}
	s := &BillingSession{funding: funding, relayInfo: &relaycommon.RelayInfo{IsPlayground: true}, preConsumedQuota: 100, tokenConsumed: 100}
	require.EqualError(t, s.Settle(150), "funding unavailable")
	require.False(t, s.fundingSettled)
	require.False(t, s.settled)
	require.True(t, s.NeedsRefund())
	funding.fail = false
	require.NoError(t, s.Settle(150))
	require.NoError(t, s.Settle(150))
	require.Equal(t, []int{50, 50}, funding.calls)
	require.False(t, s.NeedsRefund())
}

func billingSafetyWallet(t *testing.T) (*gorm.DB, *relaycommon.RelayInfo, *gin.Context, *BillingSession) {
	t.Helper()
	db, info, c := subscriptionBillingFixture(t, 100000, true, "wallet_only")
	c.Request = httptest.NewRequest("POST", "/", nil)
	s, apiErr := NewBillingSession(c, info, 100)
	require.Nil(t, apiErr)
	return db, info, c, s
}

func assertBillingSafetyWallet(t *testing.T, db *gorm.DB, info *relaycommon.RelayInfo, actual int) {
	t.Helper()
	var user model.User
	var token model.Token
	require.NoError(t, db.First(&user, info.UserId).Error)
	require.NoError(t, db.First(&token, info.TokenId).Error)
	require.Equal(t, 1000000-actual, user.Quota)
	require.Equal(t, 1000000-actual, token.RemainQuota)
	require.Equal(t, actual, token.UsedQuota)
}

func TestBillingSafetyWalletSettlementBalances(t *testing.T) {
	for _, actual := range []int{0, 50, 100, 150} {
		t.Run(fmt.Sprint(actual), func(t *testing.T) {
			db, info, c, s := billingSafetyWallet(t)
			// The legacy entry point must reject before any balance adjustment.
			require.Error(t, SettleBilling(c, info, -1))
			info.Billing = s
			require.Error(t, SettleBilling(c, info, -1))
			require.Error(t, s.Settle(-1))
			assertBillingSafetyWallet(t, db, info, 100)

			errs := make(chan error, 16)
			var wg sync.WaitGroup
			for i := 0; i < cap(errs); i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					errs <- s.Settle(actual)
				}()
			}
			wg.Wait()
			close(errs)
			for err := range errs {
				require.NoError(t, err)
			}
			require.True(t, s.settled)
			require.Error(t, s.Settle(-1), "even a settled session must reject negative totals")
			s.Refund(c)
			assertBillingSafetyWallet(t, db, info, actual)
		})
	}
}

func TestBillingSafetyRefundedWalletCannotSettle(t *testing.T) {
	db, info, c, s := billingSafetyWallet(t)
	previous := billingRefundTasks
	billingRefundTasks = &refundTaskTracker{}
	t.Cleanup(func() { billingRefundTasks = previous })
	s.Refund(c)
	require.True(t, s.refunded)
	for _, actual := range []int{0, 50, 100, 150} {
		require.Error(t, s.Settle(actual))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	report, err := DrainBillingRefundTasks(ctx)
	require.NoError(t, err)
	require.EqualValues(t, 1, report.Accepted)
	require.EqualValues(t, 1, report.Finished)
	require.Zero(t, report.Failed)
	require.False(t, s.settled)
	require.False(t, s.fundingSettled)
	assertBillingSafetyWallet(t, db, info, 0)
}

func TestBillingSafetyReserveAndGetterConcurrent(t *testing.T) {
	db, info, _, s := billingSafetyWallet(t)
	stop := make(chan struct{})
	var wg, ready sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		ready.Add(1)
		go func() {
			defer wg.Done()
			ready.Done()
			for {
				select {
				case <-stop:
					return
				default:
					if quota := s.GetPreConsumedQuota(); quota < 100 || quota > 150 {
						t.Errorf("invalid reserved quota: %d", quota)
					}
				}
			}
		}()
	}
	defer func() { close(stop); wg.Wait() }()
	ready.Wait()
	for target := 101; target <= 150; target++ {
		require.NoError(t, s.Reserve(target))
	}
	require.Equal(t, 150, s.GetPreConsumedQuota())
	assertBillingSafetyWallet(t, db, info, 150)
}
