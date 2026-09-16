package model

import (
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/stretchr/testify/require"
)

func TestUserTopUpHistoryIncludesOrdersOlderThanThirtyDays(t *testing.T) {
	db := setupTopUpSortModelTestDB(t)
	now := time.Now().Unix()

	require.NoError(t, db.Create(&[]TopUp{
		{
			Id:            1,
			UserId:        7,
			TradeNo:       "old-order",
			Amount:        10,
			Money:         10,
			Status:        common.TopUpStatusSuccess,
			PaymentMethod: PaymentMethodStripe,
			CreateTime:    now - 90*24*60*60,
		},
		{
			Id:            2,
			UserId:        7,
			TradeNo:       "recent-order",
			Amount:        20,
			Money:         20,
			Status:        common.TopUpStatusSuccess,
			PaymentMethod: PaymentMethodStripe,
			CreateTime:    now - 24*60*60,
		},
	}).Error)

	history, total, err := GetUserTopUps(
		7,
		topUpSortPage(1, 10),
		NewTopUpSortSpec("", "", false),
	)
	require.NoError(t, err)
	require.Equal(t, int64(2), total)
	require.Equal(t, []int{2, 1}, topUpSortIDs(history))

	searched, total, err := SearchUserTopUps(
		7,
		"%old-order%",
		topUpSortPage(1, 10),
		NewTopUpSortSpec("", "", false),
	)
	require.NoError(t, err)
	require.Equal(t, int64(1), total)
	require.Equal(t, []int{1}, topUpSortIDs(searched))
}
