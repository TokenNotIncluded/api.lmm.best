package model

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestAcquisitionCorrectionAppendOnlyAndBusinessIsolation(t *testing.T) {
	db := acquisitionDB(t)
	ctx := context.Background()
	now := time.Now().Unix()
	user := User{Username: "correct-source", AffCode: "correct-source", Email: "private@example.invalid", Quota: 123, InviterId: 71, CreatedAt: now - 10}
	require.NoError(t, db.Create(&user).Error)
	account := AcquisitionAccount{UserID: user.Id, RegistrationSource: "github", RegistrationEvidence: "promotion_link", FirstSource: "github", CreatedAt: now}
	require.NoError(t, db.Create(&account).Error)
	first, err := SaveAcquisitionCorrection(ctx, user.Id, 9, 0, "community", "Confirmed with campaign records")
	require.NoError(t, err)
	require.Equal(t, "github", first.PreviousSource)
	second, err := SaveAcquisitionCorrection(ctx, user.Id, 10, first.ID, "documentation", "Updated after checking the campaign")
	require.NoError(t, err)
	require.Equal(t, "community", second.PreviousSource)
	require.Equal(t, first.ID, second.PreviousRevision)
	_, err = SaveAcquisitionCorrection(ctx, user.Id, 9, first.ID, "social", "Outdated revision")
	require.ErrorIs(t, err, ErrAcquisitionCorrectionConflict)
	require.Error(t, db.Model(&first).Update("reason", "rewrite").Error)
	history, err := ReadAcquisitionCorrections(ctx, user.Id)
	require.NoError(t, err)
	require.Len(t, history.Items, 2)
	require.Equal(t, second.ID, history.Head.Revision)
	require.Equal(t, "Confirmed with campaign records", history.Items[1].Reason)
	require.NoError(t, db.First(&account, user.Id).Error)
	require.Equal(t, "github", account.RegistrationSource)
	require.Equal(t, "github", account.FirstSource)
	require.NoError(t, db.First(&user, user.Id).Error)
	require.Equal(t, 123, user.Quota)
	require.Equal(t, 71, user.InviterId)
	other, err := ReadAcquisitionCorrections(ctx, user.Id+999)
	require.NoError(t, err)
	require.Empty(t, other.Items)
	rows, err := ExportAcquisitionUsers(ctx, "github", now-100, now+1)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "github", rows[0].ObservedSource)
	require.Equal(t, "documentation", rows[0].CorrectedSource)
	payload, _ := json.Marshal(rows)
	require.NotContains(t, string(payload), user.Email)
	require.NotContains(t, string(payload), user.Username)
	rows, err = ExportAcquisitionUsers(ctx, "documentation", now-100, now+1)
	require.NoError(t, err)
	require.Empty(t, rows)
	require.NoError(t, RevokeAcquisitionAccount(ctx, user.Id))
	history, err = ReadAcquisitionCorrections(ctx, user.Id)
	require.NoError(t, err)
	require.Empty(t, history.Items)
	require.Nil(t, history.Head)
	_, err = SaveAcquisitionCorrection(ctx, user.Id, 9, 0, "github", "Do not undo consent")
	require.ErrorIs(t, err, ErrAcquisitionInvalid)
}
