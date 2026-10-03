package model

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type privateMarketFixture struct {
	db      *gorm.DB
	owner   User
	other   User
	service *ToolMarketService
	version ToolMarketVersion
	tools   []ToolMarketToolVersion
}

func newPrivateMarketFixture(t *testing.T) privateMarketFixture {
	t.Helper()
	db := marketTestDB(t)
	f := privateMarketFixture{db: db, owner: marketTestUser(t, db, "private-owner", 0, common.RoleCommonUser), other: marketTestUser(t, db, "other-root", 0, common.RoleRootUser)}
	input := marketTestDraft(0)
	input.Visibility = "private"
	input.Tools = append(input.Tools, ToolMarketToolInput{Name: "second", InputSchema: json.RawMessage(`{"type":"object"}`), Permissions: []string{"read"}})
	var err error
	f.service, err = SaveToolMarketDraft(f.owner.Id, "", input)
	require.NoError(t, err)
	require.NoError(t, db.First(&f.version, "id = ?", f.service.DraftVersionID).Error)
	require.NoError(t, db.Where("version_id = ?", f.version.ID).Find(&f.tools).Error)
	digests := make(map[string]string, len(f.tools))
	for _, tool := range f.tools {
		digests[tool.ToolID] = strings.Repeat("ab", 32)
	}
	// Simulate the trusted remote validator, never a public input field.
	require.NoError(t, RecordToolMarketValidation(f.owner.Id, f.service.ID, f.version.ID, f.version.Digest, digests))
	return f
}

func TestToolMarketPrivateActivationPublishesOnlyForOwnerAndIsIdempotent(t *testing.T) {
	f := newPrivateMarketFixture(t)
	require.NoError(t, ActivateToolMarketPrivate(f.owner.Id, f.service.ID, f.version.ID))
	require.NoError(t, f.db.First(f.service, "id = ?", f.service.ID).Error)
	require.NoError(t, f.db.First(&f.version, "id = ?", f.version.ID).Error)
	require.Equal(t, "published", f.service.Status)
	require.Equal(t, f.version.ID, f.service.LiveVersionID)
	require.Empty(t, f.service.DraftVersionID)
	require.Equal(t, "published", f.version.Status)
	require.Positive(t, f.version.PublishedAt)
	require.Equal(t, f.version.PublishedAt, f.service.UpdatedAt)
	require.Zero(t, f.version.ReviewedBy)
	for _, tool := range f.tools {
		_, _, err := marketLiveTool(f.db, f.owner.Id, tool.ToolID, f.version.ID)
		require.NoError(t, err)
		_, _, err = marketLiveTool(f.db, f.other.Id, tool.ToolID, f.version.ID)
		require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	}
	// Loading and grants remain explicit; activation creates neither.
	for _, row := range []any{&ToolMarketInstallation{}, &ToolMarketGrant{}, &ToolMarketAccess{}} {
		var count int64
		require.NoError(t, f.db.Model(row).Count(&count).Error)
		require.Zero(t, count)
	}
	publishedAt := f.version.PublishedAt
	require.NoError(t, ActivateToolMarketPrivate(f.owner.Id, f.service.ID, f.version.ID))
	require.NoError(t, f.db.First(&f.version, "id = ?", f.version.ID).Error)
	require.Equal(t, publishedAt, f.version.PublishedAt)
	var events []ToolMarketEvent
	require.NoError(t, f.db.Where("action = ?", "private.activate").Find(&events).Error)
	require.Len(t, events, 1)
	require.Equal(t, f.owner.Id, events[0].ActorID)
	require.Equal(t, f.service.ID, events[0].ObjectID)
	require.JSONEq(t, `{"version_id":"`+f.version.ID+`"}`, events[0].Details)
}

func TestToolMarketPrivateActivationRejectsUnsafeOrUnvalidatedDrafts(t *testing.T) {
	for _, kind := range []string{"public", "shared", "serverless", "pending", "rejected", "paused", "suspended", "unknown-status", "unvalidated", "stale-validation", "missing-fingerprint", "short-fingerprint", "bad-hex-fingerprint", "paid-second-tool", "negative-price", "missing-tools", "disabled-owner", "other-owner", "missing-id", "wrong-version", "other-service-version"} {
		t.Run(kind, func(t *testing.T) {
			f := newPrivateMarketFixture(t)
			actor, serviceID, versionID := f.owner.Id, f.service.ID, f.version.ID
			want := ErrToolMarketDenied
			switch kind {
			case "public", "shared":
				require.NoError(t, f.db.Model(&f.version).Update("visibility", kind).Error)
			case "serverless":
				require.NoError(t, f.db.Model(&f.version).Update("execution_type", kind).Error)
			case "pending", "rejected":
				require.NoError(t, f.db.Model(&f.version).Update("status", kind).Error)
				want = ErrToolMarketConflict
			case "paused", "suspended", "unknown-status":
				require.NoError(t, f.db.Model(f.service).Update("status", kind).Error)
				if kind == "unknown-status" {
					want = ErrToolMarketConflict
				}
			case "unvalidated":
				require.NoError(t, f.db.Model(&f.version).Update("validation_digest", "").Error)
			case "stale-validation":
				require.NoError(t, f.db.Model(&f.version).Update("digest", strings.Repeat("cd", 32)).Error)
			case "missing-fingerprint", "short-fingerprint", "bad-hex-fingerprint":
				digest := ""
				if kind == "short-fingerprint" {
					digest = strings.Repeat("a", 63)
				} else if kind == "bad-hex-fingerprint" {
					digest = strings.Repeat("x", 64)
				}
				require.NoError(t, f.db.Model(&f.tools[1]).Update("remote_digest", digest).Error)
			case "paid-second-tool", "negative-price":
				price := 1
				if kind == "negative-price" {
					price = -1
				}
				require.NoError(t, f.db.Model(&f.tools[1]).Update("price_quota", price).Error)
			case "missing-tools":
				require.NoError(t, f.db.Where("version_id = ?", f.version.ID).Delete(&ToolMarketToolVersion{}).Error)
			case "disabled-owner":
				require.NoError(t, f.db.Model(&f.owner).Update("status", common.UserStatusDisabled).Error)
			case "other-owner":
				actor = f.other.Id // Even a root user cannot use this owner-only path.
			case "missing-id":
				versionID, want = "", ErrToolMarketInput
			case "wrong-version":
				require.NoError(t, f.db.Model(f.service).Update("draft_version_id", "another-draft").Error)
				want = ErrToolMarketConflict
			case "other-service-version":
				require.NoError(t, f.db.Model(&f.version).Update("service_id", "other-service").Error)
				want = gorm.ErrRecordNotFound
			}
			require.ErrorIs(t, ActivateToolMarketPrivate(actor, serviceID, versionID), want)
			require.NoError(t, f.db.First(f.service, "id = ?", f.service.ID).Error)
			require.Empty(t, f.service.LiveVersionID)
			if kind == "wrong-version" {
				// This case intentionally installed a different current draft.
				require.Equal(t, "another-draft", f.service.DraftVersionID)
			} else {
				require.Equal(t, f.version.ID, f.service.DraftVersionID, "failed activation must preserve the draft")
			}
			var count int64
			require.NoError(t, f.db.Model(&ToolMarketEvent{}).Where("action = ?", "private.activate").Count(&count).Error)
			require.Zero(t, count)
		})
	}
}

func TestToolMarketPrivateActivationRetryStillEnforcesAuthorityAndSuspension(t *testing.T) {
	for _, kind := range []string{"other-owner", "disabled-owner", "paused", "suspended"} {
		t.Run(kind, func(t *testing.T) {
			f := newPrivateMarketFixture(t)
			require.NoError(t, ActivateToolMarketPrivate(f.owner.Id, f.service.ID, f.version.ID))
			actor := f.owner.Id
			switch kind {
			case "other-owner":
				actor = f.other.Id
			case "disabled-owner":
				require.NoError(t, f.db.Model(&f.owner).Update("status", common.UserStatusDisabled).Error)
			case "paused", "suspended":
				require.NoError(t, SetToolMarketPaused(f.owner.Id, f.service.ID, true))
				if kind == "suspended" {
					require.NoError(t, SetToolMarketPaused(f.other.Id, f.service.ID, true))
				}
			}
			require.ErrorIs(t, ActivateToolMarketPrivate(actor, f.service.ID, f.version.ID), ErrToolMarketDenied)
			require.NoError(t, f.db.First(f.service, "id = ?", f.service.ID).Error)
			if kind == "paused" || kind == "suspended" {
				require.Equal(t, kind, f.service.Status)
			}
		})
	}
}

func TestToolMarketPrivateActivationConcurrentRetriesPublishOnce(t *testing.T) {
	f := newPrivateMarketFixture(t)
	const attempts = 8
	errs := make(chan error, attempts)
	var wg sync.WaitGroup
	for range attempts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- ActivateToolMarketPrivate(f.owner.Id, f.service.ID, f.version.ID)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	var count int64
	require.NoError(t, f.db.Model(&ToolMarketEvent{}).Where("action = ?", "private.activate").Count(&count).Error)
	require.EqualValues(t, 1, count)
}

func TestToolMarketPrivateActivationReplacesLiveVersionAndRetryPreservesNewDraft(t *testing.T) {
	f := newPrivateMarketFixture(t)
	require.NoError(t, ActivateToolMarketPrivate(f.owner.Id, f.service.ID, f.version.ID))
	input := marketTestDraft(0)
	input.Visibility, input.Description = "private", "Updated owner-only snapshot"
	updated, err := SaveToolMarketDraft(f.owner.Id, f.service.ID, input)
	require.NoError(t, err)
	newVersionID := updated.DraftVersionID
	// A retry for the current live version must not discard ongoing edits.
	require.NoError(t, ActivateToolMarketPrivate(f.owner.Id, f.service.ID, f.version.ID))
	require.NoError(t, f.db.First(updated, "id = ?", updated.ID).Error)
	require.Equal(t, newVersionID, updated.DraftVersionID)
	var version ToolMarketVersion
	require.NoError(t, f.db.First(&version, "id = ?", newVersionID).Error)
	var tools []ToolMarketToolVersion
	require.NoError(t, f.db.Where("version_id = ?", newVersionID).Find(&tools).Error)
	digests := make(map[string]string, len(tools))
	for _, tool := range tools {
		digests[tool.ToolID] = strings.Repeat("cd", 32)
	}
	require.NoError(t, RecordToolMarketValidation(f.owner.Id, updated.ID, version.ID, version.Digest, digests))
	require.NoError(t, ActivateToolMarketPrivate(f.owner.Id, updated.ID, version.ID))
	require.NoError(t, f.db.First(updated, "id = ?", updated.ID).Error)
	require.Equal(t, newVersionID, updated.LiveVersionID)
	require.Empty(t, updated.DraftVersionID)
	// Prior version bindings remain immutable but no longer dispatchable.
	require.NoError(t, f.db.First(&f.version, "id = ?", f.version.ID).Error)
	require.Equal(t, "published", f.version.Status)
	_, _, err = marketLiveTool(f.db, f.owner.Id, f.tools[0].ToolID, f.version.ID)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	require.ErrorIs(t, ActivateToolMarketPrivate(f.owner.Id, updated.ID, f.version.ID), ErrToolMarketConflict)
}

func TestToolMarketPrivateActivationCannotTreatPublicLiveVersionAsPrivateRetry(t *testing.T) {
	f := newMarketFixture(t, 0)
	require.ErrorIs(t, ActivateToolMarketPrivate(f.author.Id, f.service.ID, f.service.LiveVersionID), ErrToolMarketDenied)
	var count int64
	require.NoError(t, f.db.Model(&ToolMarketEvent{}).Where("action = ?", "private.activate").Count(&count).Error)
	require.Zero(t, count)
}
