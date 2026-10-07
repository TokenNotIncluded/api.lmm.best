package model

import (
	"encoding/json"
	"sync"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func storeSocialFixture(t *testing.T, activate bool) storeFixture {
	t.Helper()
	require.GreaterOrEqual(t, MerchantStoreWriterCapability, 6, "social tests require the reviewed phase-six source")
	f := newStoreFixture(t, "balance")
	// Exercise the real installation and each activation stage. Merely seeding
	// an option to 6 would hide missing schema or an unqualified writer floor.
	require.NoError(t, PrepareMerchantStoreSchema(DB, 1))
	require.NoError(t, ActivateMerchantStoreVariants(DB, 1))
	require.NoError(t, ActivateMerchantStoreProductLifecycle(DB, 2))
	require.NoError(t, ActivateMerchantStoreRefunds(DB, 3))
	require.NoError(t, ActivateMerchantStoreAccess(DB, 4))
	if activate {
		require.NoError(t, ActivateMerchantStorePhaseSix(DB, 5))
	}
	return f
}

func TestMerchantStoreProductLikesPhaseFiveIsUnknownAndReadOnly(t *testing.T) {
	f := storeSocialFixture(t, false)
	require.True(t, DB.Migrator().HasTable(&MerchantStoreProductLike{}), "prepared schema alone cannot activate likes")
	before := storeWriterSnapshot(t)
	view, err := GetMerchantStoreProductLikes(f.buyer.Id, f.product.ID)
	require.NoError(t, err)
	require.False(t, view.Supported)
	require.Nil(t, view.Count)
	require.False(t, view.Liked)
	for _, liked := range []bool{true, false} {
		_, err = SetMerchantStoreProductLike(f.buyer.Id, f.product.ID, liked)
		require.ErrorIs(t, err, ErrMerchantStoreWriterFrozen)
	}
	var count int64
	require.NoError(t, DB.Model(&MerchantStoreProductLike{}).Count(&count).Error)
	require.Zero(t, count)
	require.Equal(t, before, storeWriterSnapshot(t))
}

func TestMerchantStoreProductLikesPersistAbsoluteStateAndLeaveBusinessData(t *testing.T) {
	f := storeSocialFixture(t, true)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", f.buyer.Id).UpdateColumn("quota", 0).Error)
	before := storeWriterSnapshot(t)
	view, err := SetMerchantStoreProductLike(f.buyer.Id, f.product.ID, true)
	require.NoError(t, err)
	require.True(t, view.Supported)
	require.True(t, view.Liked)
	require.EqualValues(t, 1, *view.Count)
	var original MerchantStoreProductLike
	require.NoError(t, DB.First(&original, "user_id = ? AND product_id = ?", f.buyer.Id, f.product.ID).Error)
	view, err = SetMerchantStoreProductLike(f.buyer.Id, f.product.ID, true)
	require.NoError(t, err)
	require.EqualValues(t, 1, *view.Count)
	var persisted MerchantStoreProductLike
	require.NoError(t, DB.First(&persisted, "user_id = ? AND product_id = ?", f.buyer.Id, f.product.ID).Error)
	require.Equal(t, original, persisted, "a retry must retain the first persistent identity and timestamp")
	_, err = SetMerchantStoreProductLike(f.seller.Id, f.product.ID, true)
	require.NoError(t, err)
	for _, actor := range []int{0, f.root.Id, f.buyer.Id} {
		view, err = GetMerchantStoreProductLikes(actor, f.product.ID)
		require.NoError(t, err)
		require.EqualValues(t, 2, *view.Count)
		require.Equal(t, actor == f.buyer.Id, view.Liked)
		encoded, marshalErr := json.Marshal(view)
		require.NoError(t, marshalErr)
		require.NotContains(t, string(encoded), "user_id")
	}
	for i := 0; i < 2; i++ {
		view, err = SetMerchantStoreProductLike(f.buyer.Id, f.product.ID, false)
		require.NoError(t, err)
		require.False(t, view.Liked)
		require.EqualValues(t, 1, *view.Count)
	}
	view, err = GetMerchantStoreProductLikes(f.seller.Id, f.product.ID)
	require.NoError(t, err)
	require.True(t, view.Liked, "another account's unlike cannot remove the seller's like")
	var favorites int64
	require.NoError(t, DB.Model(&MerchantStoreFavorite{}).Count(&favorites).Error)
	require.Zero(t, favorites, "likes must not be stored as favorites")
	require.Equal(t, before, storeWriterSnapshot(t))
}

func TestMerchantStoreProductLikesConcurrentRetriesHaveOneIdentity(t *testing.T) {
	f := storeSocialFixture(t, true)
	errs := make(chan error, 12)
	var workers sync.WaitGroup
	for i := 0; i < cap(errs); i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			_, err := SetMerchantStoreProductLike(f.buyer.Id, f.product.ID, true)
			errs <- err
		}()
	}
	workers.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	view, err := GetMerchantStoreProductLikes(f.buyer.Id, f.product.ID)
	require.NoError(t, err)
	require.True(t, view.Liked)
	require.EqualValues(t, 1, *view.Count)
}

func TestMerchantStoreProductLikesUseLiveViewerVisibility(t *testing.T) {
	f := storeSocialFixture(t, true)
	_, err := SetMerchantStoreProductLike(f.buyer.Id, f.product.ID, true)
	require.NoError(t, err)
	require.NoError(t, DB.Model(&MerchantStoreProduct{}).Where("id = ?", f.product.ID).Updates(map[string]any{"visibility": "registered", "test_mode": false}).Error)
	_, err = GetMerchantStoreProductLikes(0, f.product.ID)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	view, err := GetMerchantStoreProductLikes(f.buyer.Id, f.product.ID)
	require.NoError(t, err)
	require.True(t, view.Liked)
	require.NoError(t, DB.Model(&MerchantStoreProduct{}).Where("id = ?", f.product.ID).Updates(map[string]any{"visibility": "private", "test_mode": true}).Error)
	for _, actor := range []int{0, f.buyer.Id, f.root.Id} {
		_, err = GetMerchantStoreProductLikes(actor, f.product.ID)
		require.ErrorIs(t, err, gorm.ErrRecordNotFound)
		if actor > 0 {
			_, err = SetMerchantStoreProductLike(actor, f.product.ID, true)
			require.ErrorIs(t, err, gorm.ErrRecordNotFound)
		}
	}
	view, err = GetMerchantStoreProductLikes(f.seller.Id, f.product.ID)
	require.NoError(t, err)
	require.EqualValues(t, 1, *view.Count)
	require.False(t, view.Liked)
	for _, status := range []string{"unlisted", "deleted", "paused"} {
		require.NoError(t, DB.Model(&MerchantStoreProduct{}).Where("id = ?", f.product.ID).Updates(map[string]any{"visibility": "public", "test_mode": false, "status": status}).Error)
		_, err = SetMerchantStoreProductLike(f.seller.Id, f.product.ID, true)
		require.ErrorIs(t, err, gorm.ErrRecordNotFound, status)
	}
}

func TestMerchantStoreProductLikesRejectDisabledActorAndSeller(t *testing.T) {
	f := storeSocialFixture(t, true)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", f.buyer.Id).UpdateColumn("status", common.UserStatusDisabled).Error)
	_, err := GetMerchantStoreProductLikes(f.buyer.Id, f.product.ID)
	require.ErrorIs(t, err, ErrMerchantStoreDenied)
	_, err = SetMerchantStoreProductLike(f.buyer.Id, f.product.ID, true)
	require.ErrorIs(t, err, ErrMerchantStoreDenied)
	_, err = SetMerchantStoreProductLike(0, f.product.ID, true)
	require.ErrorIs(t, err, ErrMerchantStoreDenied)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", f.seller.Id).UpdateColumn("status", common.UserStatusDisabled).Error)
	_, err = GetMerchantStoreProductLikes(0, f.product.ID)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	_, err = SetMerchantStoreProductLike(f.root.Id, f.product.ID, true)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
}

func TestMerchantStoreProductLikesDamagedSchemaIsUnknownAndFrozen(t *testing.T) {
	f := storeSocialFixture(t, true)
	require.NoError(t, DB.Migrator().DropTable(&MerchantStoreProductLike{}))
	view, err := GetMerchantStoreProductLikes(0, f.product.ID)
	require.NoError(t, err)
	require.False(t, view.Supported)
	require.Nil(t, view.Count)
	_, err = SetMerchantStoreProductLike(f.buyer.Id, f.product.ID, true)
	require.ErrorIs(t, err, ErrMerchantStoreWriterFrozen)
}
