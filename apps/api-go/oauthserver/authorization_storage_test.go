package oauthserver

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func seedAuthorizationRows(t *testing.T, db *gorm.DB, issuer string, count int, expires time.Time) {
	t.Helper()
	rows := make([]model.OAuthServerAuthorization, count)
	for i := range rows {
		rows[i] = model.OAuthServerAuthorization{Digest: fmt.Sprintf("%s-row-%d", issuer, i), Issuer: issuer, ExpiresAtMs: expires.UnixMilli()}
	}
	require.NoError(t, db.CreateInBatches(rows, 100).Error)
}

func TestAuthorizationStorageCapAcrossIndependentPools(t *testing.T) {
	forDatabases(t, func(t *testing.T, db, other *gorm.DB) {
		first, clock, _ := testServer(t, db)
		second, _, _ := testServer(t, other)
		require.True(t, db.Migrator().HasIndex(&model.OAuthServerAuthorization{}, "idx_oauth_authorization_issuer_expiry"))
		seedAuthorizationRows(t, db, testIssuer, MaxAuthorizationRows-1, clock.now().Add(AuthorizationTTL))
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		start := make(chan struct{})
		results := make(chan error, 8)
		var workers sync.WaitGroup
		for i := range 8 {
			workers.Add(1)
			s := []*Server{first, second}[i%2]
			go func() {
				defer workers.Done()
				<-start
				_, err := s.BeginAuthorization(ctx, authorizationValues().Encode(), testBrowser)
				results <- err
			}()
		}
		close(start)
		workers.Wait()
		close(results)
		accepted := 0
		for err := range results {
			if err == nil {
				accepted++
				continue
			}
			expectProtocol(t, err, "temporarily_unavailable")
		}
		require.Equal(t, 1, accepted)
		var count int64
		require.NoError(t, db.Model(&model.OAuthServerAuthorization{}).Where("issuer = ?", testIssuer).Count(&count).Error)
		require.EqualValues(t, MaxAuthorizationRows, count)
		for range 2 {
			pending, err := second.BeginAuthorization(ctx, authorizationValues().Encode(), testBrowser)
			require.Nil(t, pending)
			expectProtocol(t, err, "temporarily_unavailable")
		}
		require.NoError(t, db.Model(&model.OAuthServerAuthorization{}).Where("issuer = ?", testIssuer).Count(&count).Error)
		require.EqualValues(t, MaxAuthorizationRows, count)
		// Capacity is per issuer, never a process-local or client-controlled key.
		config := testConfig()
		config.Issuer = "https://other.lmm.test"
		otherIssuer, err := New(other, config, &testPolicy{})
		require.NoError(t, err)
		otherIssuer.now = clock.now
		_, err = otherIssuer.BeginAuthorization(ctx, authorizationValues().Encode(), testBrowser)
		require.NoError(t, err)
	})
}

func TestAuthorizationStorageExpiryRecoveryPreservesLiveCSRFAndTombstones(t *testing.T) {
	forDatabases(t, func(t *testing.T, db, _ *gorm.DB) {
		s, clock, _ := testServer(t, db)
		seedAuthorizationRows(t, db, testIssuer, MaxAuthorizationRows+model.OAuthAuthorizationCleanupBatchSize+5, clock.now())
		_, err := s.BeginAuthorization(context.Background(), authorizationValues().Encode(), testBrowser)
		expectProtocol(t, err, "temporarily_unavailable")
		var count int64
		require.NoError(t, db.Model(&model.OAuthServerAuthorization{}).Count(&count).Error)
		require.EqualValues(t, MaxAuthorizationRows+5, count, "bounded expiry cleanup must commit even when admission is refused")
		pending, err := s.BeginAuthorization(context.Background(), authorizationValues().Encode(), testBrowser)
		require.NoError(t, err)
		csrf := "a-single-use-server-generated-csrf-32-bytes"
		require.NoError(t, s.SetBrowserCSRF(context.Background(), pending.Transaction, testBrowser, csrf))
		seedAuthorizationRows(t, db, "https://other.lmm.test", 1, clock.now())
		require.NoError(t, db.Create(&model.OAuthServerGrant{ID: "retained-family", Issuer: testIssuer, AbsoluteExpiresAtMs: clock.now().Add(time.Hour).UnixMilli()}).Error)
		require.NoError(t, db.Create(&model.OAuthServerCode{Digest: "retained-code", Issuer: testIssuer, FamilyID: "retained-family", UsedAtMs: 1, ExpiresAtMs: 1}).Error)
		require.NoError(t, db.Create(&model.OAuthServerToken{Digest: "retained-refresh", Issuer: testIssuer, FamilyID: "retained-family", Kind: "refresh", UsedAtMs: 1, ExpiresAtMs: 1}).Error)
		for range 20 {
			deleted, err := model.CleanupExpiredOAuthAuthorizations(db, "", clock.now())
			require.NoError(t, err)
			require.LessOrEqual(t, deleted, int64(model.OAuthAuthorizationCleanupBatchSize))
			if deleted < model.OAuthAuthorizationCleanupBatchSize {
				break
			}
		}
		require.NoError(t, db.Model(&model.OAuthServerAuthorization{}).Count(&count).Error)
		require.EqualValues(t, 1, count)
		for _, row := range []any{&model.OAuthServerGrant{}, &model.OAuthServerCode{}, &model.OAuthServerToken{}} {
			require.NoError(t, db.Model(row).Count(&count).Error)
			require.EqualValues(t, 1, count)
		}
		flow, err := s.ConsumeBrowserFlow(context.Background(), testBrowser, csrf)
		require.NoError(t, err)
		require.NotNil(t, flow)
		_, err = s.ConsumeBrowserFlow(context.Background(), testBrowser, csrf)
		expectProtocol(t, err, "invalid_request")
		// Expiry is inclusive at the exact boundary, never before it.
		clock.advance(AuthorizationTTL)
		deleted, err := model.CleanupExpiredOAuthAuthorizations(db, testIssuer, clock.now())
		require.NoError(t, err)
		require.EqualValues(t, 1, deleted)
	})
}

func TestAuthorizationStorageRejectsInvalidAndCancelledRequestsWithoutWrites(t *testing.T) {
	forDatabases(t, func(t *testing.T, db, _ *gorm.DB) {
		s, clock, _ := testServer(t, db)
		seedAuthorizationRows(t, db, testIssuer, 1, clock.now())
		_, err := s.BeginAuthorization(context.Background(), authorizationValues().Encode()+"&client_id=duplicate", testBrowser)
		expectProtocol(t, err, "invalid_request")
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err = s.BeginAuthorization(ctx, authorizationValues().Encode(), testBrowser)
		require.Error(t, err)
		var count int64
		require.NoError(t, db.Model(&model.OAuthServerAuthorization{}).Count(&count).Error)
		require.EqualValues(t, 1, count)
	})
}

func TestAuthorizationStorageSamplesTimeAfterAdmissionLock(t *testing.T) {
	forDatabases(t, func(t *testing.T, db, other *gorm.DB) {
		first, clock, _ := testServer(t, db)
		second, _, _ := testServer(t, other)
		holder := db.Begin()
		require.NoError(t, holder.Error)
		t.Cleanup(func() { _ = holder.Rollback().Error })
		acquired, err := first.lockAuthorizationAdmission(holder)
		require.NoError(t, err)
		require.True(t, acquired)
		reads := make(chan struct{}, 1)
		second.now = func() time.Time { reads <- struct{}{}; return clock.now() }
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		type result struct {
			pending *PendingAuthorization
			err     error
		}
		results := make(chan result, 1)
		go func() {
			pending, err := second.BeginAuthorization(ctx, authorizationValues().Encode(), testBrowser)
			results <- result{pending, err}
		}()
		if db.Dialector.Name() == "postgres" {
			got := <-results
			require.Nil(t, got.pending)
			expectProtocol(t, got.err, "temporarily_unavailable")
		} else {
			select {
			case <-reads:
				t.Fatal("authorization time was sampled before the waiting writer acquired its lock")
			case <-time.After(25 * time.Millisecond):
			}
		}
		clock.advance(AuthorizationTTL)
		require.NoError(t, holder.Rollback().Error)
		if db.Dialector.Name() == "postgres" {
			pending, err := second.BeginAuthorization(ctx, authorizationValues().Encode(), testBrowser)
			results <- result{pending, err}
		}
		got := <-results
		require.NoError(t, got.err)
		require.Equal(t, clock.now().Add(AuthorizationTTL), got.pending.ExpiresAt)
	})
}
