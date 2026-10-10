package model

import (
	"strconv"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestMidjourneyLogFilters(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	previousDB := DB
	DB = db
	t.Cleanup(func() {
		DB = previousDB
		_ = sqlDB.Close()
	})
	require.NoError(t, db.AutoMigrate(&Midjourney{}))
	const submitted int64 = 1791590400000
	rows := []Midjourney{
		{Id: 1, UserId: 10, ChannelId: 8, MjId: "own-video", Status: "SUCCESS", Action: "VIDEO", SubmitTime: submitted},
		{Id: 2, UserId: 20, ChannelId: 9, MjId: "other-video", Status: "SUCCESS", Action: "VIDEO", SubmitTime: submitted},
		{Id: 3, UserId: 10, ChannelId: 8, MjId: "own-failed", Status: "FAILURE", Action: "VIDEO", SubmitTime: submitted},
		{Id: 4, UserId: 10, ChannelId: 8, MjId: "own-image", Status: "SUCCESS", Action: "IMAGINE", SubmitTime: submitted},
		{Id: 5, UserId: 10, ChannelId: 8, MjId: "old-video", Status: "SUCCESS", Action: "VIDEO", SubmitTime: submitted - 86400000},
	}
	require.NoError(t, db.Create(&rows).Error)

	t.Run("status action time and count agree", func(t *testing.T) {
		params := TaskQueryParams{Status: "SUCCESS", Action: "VIDEO", StartTimestamp: strconv.FormatInt(submitted, 10)}
		require.Len(t, GetAllTasks(0, 20, params), 2)
		require.EqualValues(t, 2, CountAllTasks(params))
		owned := GetAllUserTask(10, 0, 20, params)
		require.Len(t, owned, 1)
		require.Equal(t, "own-video", owned[0].MjId)
		require.EqualValues(t, 1, CountAllUserTask(10, params))
	})

	t.Run("channel filter is administrator only", func(t *testing.T) {
		params := TaskQueryParams{Status: "SUCCESS", Action: "VIDEO", ChannelID: "9", StartTimestamp: strconv.FormatInt(submitted, 10)}
		all := GetAllTasks(0, 20, params)
		require.Len(t, all, 1)
		require.Equal(t, 20, all[0].UserId)
		require.EqualValues(t, 1, CountAllTasks(params))
		owned := GetAllUserTask(10, 0, 20, params)
		require.Len(t, owned, 1)
		require.Equal(t, 10, owned[0].UserId)
		require.EqualValues(t, 1, CountAllUserTask(10, params))
	})

	t.Run("old IDs remain searchable without an implicit date boundary", func(t *testing.T) {
		params := TaskQueryParams{MjID: "old-video"}
		require.Len(t, GetAllUserTask(10, 0, 20, params), 1)
		require.EqualValues(t, 1, CountAllUserTask(10, params))
		require.Empty(t, GetAllUserTask(20, 0, 20, params))
		require.Zero(t, CountAllUserTask(20, params))
	})

	t.Run("pagination does not change the matching count", func(t *testing.T) {
		params := TaskQueryParams{Status: "SUCCESS", Action: "VIDEO"}
		first := GetAllTasks(0, 1, params)
		second := GetAllTasks(1, 1, params)
		require.Len(t, first, 1)
		require.Len(t, second, 1)
		require.NotEqual(t, first[0].Id, second[0].Id)
		require.EqualValues(t, 3, CountAllTasks(params))
	})

	t.Run("filter values are bound parameters", func(t *testing.T) {
		params := TaskQueryParams{Status: "SUCCESS' OR 1=1 --"}
		require.Empty(t, GetAllTasks(0, 20, params))
		require.Zero(t, CountAllTasks(params))
		require.Empty(t, GetAllUserTask(10, 0, 20, params))
		require.Zero(t, CountAllUserTask(10, params))
	})
}
