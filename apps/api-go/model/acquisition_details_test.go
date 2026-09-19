package model

import (
	"context"
	"encoding/json"
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
	"time"
)

func TestAcquisitionDetailsStayScopedAndLookbackChangesAreProspective(t *testing.T) {
	db := acquisitionDB(t)
	ctx := context.Background()
	now := time.Now().Unix()
	u := User{Username: "private-name", Email: "private@example.invalid", AffCode: "detail", CreatedAt: now - 10}
	require.NoError(t, db.Create(&u).Error)
	v := AcquisitionVisitor{ID: AcquisitionVisitorHash("detail"), UserID: u.Id, CreatedAt: now}
	require.NoError(t, db.Create(&v).Error)
	require.NoError(t, db.Create(&AcquisitionVisit{VisitorID: v.ID, Nonce: strings.Repeat("a", 32), Source: "github", Landing: "/guide", ReferrerHost: "github.com", CreatedAt: now}).Error)
	require.NoError(t, db.Create(&AcquisitionAccount{UserID: u.Id, RegistrationSource: "github", RegistrationAt: u.CreatedAt, LookbackDays: 30, CreatedAt: now}).Error)
	require.NoError(t, SetAcquisitionLookback(ctx, 7))
	require.Error(t, SetAcquisitionLookback(ctx, 91))
	var account AcquisitionAccount
	require.NoError(t, db.First(&account, u.Id).Error)
	require.Equal(t, 30, account.LookbackDays)
	page, err := ListAcquisitionUsers(ctx, "github", now-100, now+1, 1)
	require.NoError(t, err)
	require.Equal(t, int64(1), page.Total)
	detail, err := GetAcquisitionUserDetail(ctx, u.Id)
	require.NoError(t, err)
	require.Len(t, detail.Recent, 1)
	raw, err := json.Marshal(detail)
	require.NoError(t, err)
	for _, secret := range []string{"private-name", "private@example.invalid", v.ID, strings.Repeat("a", 32)} {
		require.NotContains(t, string(raw), secret)
	}
	other, err := ListAcquisitionUsers(ctx, "unrelated", now-100, now+1, 1)
	require.NoError(t, err)
	require.Zero(t, other.Total)
}
