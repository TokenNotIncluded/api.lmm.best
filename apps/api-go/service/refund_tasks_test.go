package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"net/http/httptest"
)

type blockingRefundFunding struct{ started, release chan struct{} }

func (*blockingRefundFunding) Source() string       { return BillingSourceWallet }
func (*blockingRefundFunding) PreConsume(int) error { return nil }
func (*blockingRefundFunding) Settle(int) error     { return nil }
func (f *blockingRefundFunding) Refund() error      { close(f.started); <-f.release; return nil }

func TestBillingSessionRefundTrackedBeforeAsyncExecution(t *testing.T) {
	previous := billingRefundTasks
	tracker := &refundTaskTracker{}
	billingRefundTasks = tracker
	t.Cleanup(func() { billingRefundTasks = previous })
	funding := &blockingRefundFunding{make(chan struct{}), make(chan struct{})}
	s := &BillingSession{funding: funding, tokenConsumed: 1, relayInfo: &relaycommon.RelayInfo{IsPlayground: true}}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/", nil)
	s.Refund(c)
	<-funding.started
	s.Refund(c) // duplicate must not create another task
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r, err := DrainBillingRefundTasks(ctx)
	require.Error(t, err)
	require.EqualValues(t, 1, r.Accepted)
	require.EqualValues(t, 1, r.Active)
	require.Zero(t, r.Finished)
	close(funding.release)
	r, err = DrainBillingRefundTasks(context.Background())
	require.NoError(t, err)
	require.EqualValues(t, 1, r.Finished)
	late := &BillingSession{funding: funding, tokenConsumed: 1, relayInfo: &relaycommon.RelayInfo{IsPlayground: true}}
	late.Refund(c)
	require.False(t, late.refunded, "rejected work must not be marked refunded")
	require.True(t, late.NeedsRefund())
}

func TestRefundTasksDrainIncludesLateSubmission(t *testing.T) {
	var tracker refundTaskTracker
	first, err := tracker.begin()
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	finished := make(chan RefundTaskReport, 1)
	go func() {
		r, e := tracker.drain(ctx)
		if e != nil {
			t.Error(e)
		}
		finished <- r
	}()
	late, err := tracker.begin()
	require.NoError(t, err)
	first(false)
	select {
	case <-finished:
		t.Fatal("drained before late task ended")
	default:
	}
	late(false)
	r := <-finished
	require.EqualValues(t, 2, r.Finished)
	require.Zero(t, r.Active)
	_, err = tracker.begin()
	require.Error(t, err)
}

func TestRefundTasksTimeoutDoesNotSealOrClaimCompletion(t *testing.T) {
	var tracker refundTaskTracker
	done, err := tracker.begin()
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r, err := tracker.drain(ctx)
	require.ErrorIs(t, err, context.Canceled)
	require.EqualValues(t, 1, r.Active)
	require.Zero(t, r.Finished)
	late, err := tracker.begin()
	require.NoError(t, err)
	done(false)
	late(false)
	_, err = tracker.drain(context.Background())
	require.NoError(t, err)
}

func TestRefundTasksErrorsAndPanicsAreNotFinancialSuccess(t *testing.T) {
	for _, panics := range []bool{false, true} {
		var tracker refundTaskTracker
		reported := make(chan error, 1)
		require.NoError(t, tracker.goRun(func() error {
			if panics {
				panic("private data")
			}
			return errors.New("funding failure")
		}, func(err error) { reported <- err }))
		r, err := tracker.drain(context.Background())
		require.Error(t, err)
		require.EqualValues(t, 1, r.Finished)
		require.EqualValues(t, 1, r.Failed)
		require.Zero(t, r.Active)
		require.Error(t, <-reported)
	}
}

func TestRefundTasksConcurrentSubmissions(t *testing.T) {
	var tracker refundTaskTracker
	anchor, err := tracker.begin()
	require.NoError(t, err)
	completed := make(chan RefundTaskReport, 1)
	go func() {
		r, err := tracker.drain(context.Background())
		if err != nil {
			t.Error(err)
		}
		completed <- r
	}()
	var submit sync.WaitGroup
	for i := 0; i < 100; i++ {
		submit.Add(1)
		go func() {
			defer submit.Done()
			if e := tracker.goRun(func() error { return nil }, func(error) {}); e != nil {
				t.Error(e)
			}
		}()
	}
	submit.Wait()
	anchor(false)
	r := <-completed
	require.EqualValues(t, 101, r.Accepted)
	require.Equal(t, r.Accepted, r.Finished)
}
