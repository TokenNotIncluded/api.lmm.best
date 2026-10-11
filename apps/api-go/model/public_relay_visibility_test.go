/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/

package model

import (
	"path/filepath"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/constant"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// Use a private connection, not model.DB or an environment DSN. In particular,
// do not call initCol: the controller's standalone SQLite fixture does not.
func openPublicRelayVisibilityDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "visibility.db")), &gorm.Config{})
	require.NoError(t, err)
	connection, err := db.DB()
	require.NoError(t, err)
	connection.SetMaxOpenConns(1)
	t.Cleanup(func() { require.NoError(t, connection.Close()) })
	require.NoError(t, db.AutoMigrate(&Channel{}, &Ability{}, &PublicRelayContribution{}))
	return db
}

func seedPublicRelayVisibility(t *testing.T, db *gorm.DB, group string) (*Channel, *PublicRelayContribution) {
	t.Helper()
	channel := &Channel{
		Type: 1, Name: "visibility fixture", Group: group,
		Models: "model-a", Status: common.ChannelStatusEnabled,
	}
	require.NoError(t, db.Create(channel).Error)
	require.NoError(t, db.Create(&Ability{
		Group: group, Model: "model-a", ChannelId: channel.Id, Enabled: true,
	}).Error)
	item := &PublicRelayContribution{
		UserId: 1, ContributorEmail: "owner@example.invalid", Name: "visibility fixture",
		BaseURL: "https://upstream.example.invalid", Models: "model-a",
		Group: group, Status: PublicRelayApproved, ChannelId: channel.Id,
	}
	require.NoError(t, db.Create(item).Error)
	return channel, item
}

func TestPublicRelayVisibleQueryWithoutInitializedColumns(t *testing.T) {
	db := openPublicRelayVisibilityDB(t)
	_, first := seedPublicRelayVisibility(t, db, "community")
	_, quoted := seedPublicRelayVisibility(t, db, "community' OR 1=1 --")

	previous := commonGroupCol
	t.Cleanup(func() { commonGroupCol = previous })
	// Both an uninitialized field and stale metadata from another connection
	// must be irrelevant. Do not make this pass by initializing the fixture.
	for _, legacy := range []string{"", "invalid_legacy_column"} {
		commonGroupCol = legacy
		for _, tc := range []struct {
			group string
			id    int
		}{
			{"community", first.Id},
			{"community' OR 1=1 --", quoted.Id},
			{"unavailable-group", 0},
		} {
			var items []PublicRelayContribution
			err := publicRelayVisibleQuery(db, tc.group).Find(&items).Error
			require.NoError(t, err, "legacy column %q, group %q", legacy, tc.group)
			if tc.id == 0 {
				require.Empty(t, items)
				continue
			}
			require.Len(t, items, 1, "group values must remain bound parameters")
			require.Equal(t, tc.id, items[0].Id)
		}
	}
}

func TestPublicRelayVisibleQueryPreservesAvailabilityFilters(t *testing.T) {
	for _, tc := range []struct {
		name    string
		visible bool
		mutate  func(*gorm.DB, *Channel, *PublicRelayContribution) error
	}{
		{name: "enabled", visible: true},
		{name: "disabled channel", mutate: func(db *gorm.DB, channel *Channel, _ *PublicRelayContribution) error {
			return db.Model(channel).Update("status", 0).Error
		}},
		{name: "disabled ability", mutate: func(db *gorm.DB, channel *Channel, _ *PublicRelayContribution) error {
			return db.Model(&Ability{}).Where("channel_id = ?", channel.Id).Update("enabled", false).Error
		}},
		{name: "pending submission", mutate: func(db *gorm.DB, _ *Channel, item *PublicRelayContribution) error {
			return db.Model(item).Update("status", PublicRelayPending).Error
		}},
		{name: "rejected submission", mutate: func(db *gorm.DB, _ *Channel, item *PublicRelayContribution) error {
			return db.Model(item).Update("status", PublicRelayRejected).Error
		}},
		{name: "different contribution group", mutate: func(db *gorm.DB, _ *Channel, item *PublicRelayContribution) error {
			return db.Model(item).Update("group", "other-community").Error
		}},
		{name: "different ability group", mutate: func(db *gorm.DB, channel *Channel, _ *PublicRelayContribution) error {
			return db.Model(&Ability{}).Where("channel_id = ?", channel.Id).Update("group", "other-community").Error
		}},
		{name: "unlinked contribution", mutate: func(db *gorm.DB, _ *Channel, item *PublicRelayContribution) error {
			return db.Model(item).Update("channel_id", 0).Error
		}},
		{name: "non API provider", mutate: func(db *gorm.DB, channel *Channel, _ *PublicRelayContribution) error {
			return db.Model(channel).Update("type", constant.ChannelTypeOpenHuman).Error
		}},
		{name: "missing channel", mutate: func(db *gorm.DB, channel *Channel, _ *PublicRelayContribution) error {
			return db.Delete(channel).Error
		}},
		{name: "missing ability", mutate: func(db *gorm.DB, channel *Channel, _ *PublicRelayContribution) error {
			return db.Where("channel_id = ?", channel.Id).Delete(&Ability{}).Error
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := openPublicRelayVisibilityDB(t)
			channel, item := seedPublicRelayVisibility(t, db, "community")
			if tc.mutate != nil {
				require.NoError(t, tc.mutate(db, channel, item))
			}
			var items []PublicRelayContribution
			require.NoError(t, publicRelayVisibleQuery(db, "community").Find(&items).Error)
			if tc.visible {
				require.Len(t, items, 1)
				require.Equal(t, item.Id, items[0].Id)
			} else {
				require.Empty(t, items)
			}
		})
	}
}
