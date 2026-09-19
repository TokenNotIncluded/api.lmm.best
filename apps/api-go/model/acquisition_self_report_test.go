package model

import (
	"context"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestAcquisitionSelfReportIndependentAndRetained(t *testing.T) {
	db := acquisitionDB(t)
	ctx := context.Background()
	require.NoError(t, db.Create(&User{Id: 71, Username: "self-report", CreatedAt: time.Now().Unix()}).Error)
	require.NoError(t, db.Create(&AcquisitionAccount{UserID: 71, RegistrationSource: "documentation", CreatedAt: time.Now().Unix()}).Error)
	require.NoError(t, SaveAcquisitionSelfReport(ctx, 71, "friend", "A tutorial"))
	got, err := ReadAcquisitionSelfReport(ctx, 71)
	require.NoError(t, err)
	require.Equal(t, "friend", got.Source)
	missing, err := ReadAcquisitionSelfReport(ctx, 72)
	require.NoError(t, err)
	require.Nil(t, missing)
	var original AcquisitionAccount
	require.NoError(t, db.First(&original, "user_id = ?", 71).Error)
	require.Equal(t, "documentation", original.RegistrationSource)
	require.NoError(t, SaveAcquisitionSelfReport(ctx, 71, "community", "New title"))
	var count int64
	require.NoError(t, db.Model(&AcquisitionSelfReport{}).Count(&count).Error)
	require.EqualValues(t, 1, count)
	require.NoError(t, db.Model(&AcquisitionSelfReport{}).Where("user_id = ?", 71).Update("updated_at", time.Now().Unix()-366*86400).Error)
	require.NoError(t, PurgeAcquisition(ctx))
	require.NoError(t, db.Model(&AcquisitionSelfReport{}).Count(&count).Error)
	require.Zero(t, count)
}
func TestAcquisitionSelfReportRejectsSensitiveLinks(t *testing.T) {
	acquisitionDB(t)
	for _, value := range []string{"https://example.test/?token=secret", "sk-abcdefghijklmno", "password=hello", "line\nbreak"} {
		require.Error(t, SaveAcquisitionSelfReport(context.Background(), 1, "other", value))
	}
	require.Error(t, SaveAcquisitionSelfReport(context.Background(), 1, "unknown", ""))
}
