// Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later.
package model

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func supportFixture(t *testing.T) storeFixture {
	t.Helper()
	f := newStoreFixture(t, "balance")
	require.NoError(t, DB.AutoMigrate(MerchantStoreSupportModels()...))
	return f
}

func TestMerchantStoreSupportParticipantsAndEncryptedReplay(t *testing.T) {
	f := supportFixture(t)
	ctx := context.Background()
	thread, err := OpenMerchantStoreSupport(ctx, f.buyer.Id, f.product.ID, "")
	require.NoError(t, err)
	repeat, err := OpenMerchantStoreSupport(ctx, f.buyer.Id, f.product.ID, "")
	require.NoError(t, err)
	require.Equal(t, thread.ID, repeat.ID)
	_, err = OpenMerchantStoreSupport(ctx, f.seller.Id, f.product.ID, "")
	require.ErrorIs(t, err, ErrMerchantStoreInput)
	message, err := SendMerchantStoreSupportMessage(ctx, f.buyer.Id, thread.ID, "buyer-request-1", "  你好，如何使用？  ")
	require.NoError(t, err)
	replayed, err := SendMerchantStoreSupportMessage(ctx, f.buyer.Id, thread.ID, "buyer-request-1", "你好，如何使用？")
	require.NoError(t, err)
	require.Equal(t, message.ID, replayed.ID)
	_, err = SendMerchantStoreSupportMessage(ctx, f.buyer.Id, thread.ID, "buyer-request-1", "different")
	require.ErrorIs(t, err, ErrMerchantStoreConflict)
	var stored MerchantStoreSupportMessage
	require.NoError(t, DB.First(&stored, message.ID).Error)
	require.Empty(t, stored.Body)
	require.NotContains(t, stored.Ciphertext, "如何使用")
	history, err := GetMerchantStoreSupportHistory(ctx, f.seller.Id, thread.ID, 0, 50)
	require.NoError(t, err)
	require.Len(t, history.Items, 1)
	require.Equal(t, "你好，如何使用？", history.Items[0].Body)
	encoded, err := json.Marshal(history)
	require.NoError(t, err)
	for _, hidden := range []string{"ciphertext", "body_digest", "request_key", "scope_key"} {
		require.NotContains(t, string(encoded), hidden)
	}
	// A platform administrator is not a conversation participant.
	_, err = GetMerchantStoreSupportHistory(ctx, f.root.Id, thread.ID, 0, 50)
	require.ErrorIs(t, err, ErrMerchantStoreDenied)
	_, err = SendMerchantStoreSupportMessage(ctx, f.root.Id, thread.ID, "root-request-1", "intrude")
	require.ErrorIs(t, err, ErrMerchantStoreDenied)
	require.ErrorIs(t, MarkMerchantStoreSupportRead(ctx, f.root.Id, thread.ID, message.ID), ErrMerchantStoreDenied)
	require.ErrorIs(t, SetMerchantStoreSupportStatus(ctx, f.root.Id, thread.ID, "resolved"), ErrMerchantStoreDenied)
	_, err = GetMerchantStoreSupportAssistantContext(ctx, f.root.Id, thread.ID)
	require.ErrorIs(t, err, ErrMerchantStoreDenied)
	rows, err := ListMerchantStoreSupport(ctx, f.root.Id, "seller", "", false, 0, 30)
	require.NoError(t, err)
	require.Empty(t, rows)
}

func TestMerchantStoreSupportReadReceiptsAndStatus(t *testing.T) {
	f := supportFixture(t)
	ctx := context.Background()
	thread, err := OpenMerchantStoreSupport(ctx, f.buyer.Id, f.product.ID, "")
	require.NoError(t, err)
	first, err := SendMerchantStoreSupportMessage(ctx, f.buyer.Id, thread.ID, "first-message", "question")
	require.NoError(t, err)
	rows, err := ListMerchantStoreSupport(ctx, f.seller.Id, "seller", "open", true, 0, 30)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.EqualValues(t, 1, rows[0].UnreadCount)
	require.Equal(t, f.buyer.Username, rows[0].BuyerName)
	_, err = GetMerchantStoreSupportHistory(ctx, f.seller.Id, thread.ID, 0, 50)
	require.NoError(t, err)
	// Reading history and sending a response do not silently acknowledge it.
	second, err := SendMerchantStoreSupportMessage(ctx, f.seller.Id, thread.ID, "second-message", "reply")
	require.NoError(t, err)
	rows, err = ListMerchantStoreSupport(ctx, f.seller.Id, "seller", "", true, 0, 30)
	require.NoError(t, err)
	require.EqualValues(t, 1, rows[0].UnreadCount)
	require.NoError(t, MarkMerchantStoreSupportRead(ctx, f.seller.Id, thread.ID, second.ID))
	require.NoError(t, MarkMerchantStoreSupportRead(ctx, f.seller.Id, thread.ID, first.ID))
	history, err := GetMerchantStoreSupportHistory(ctx, f.seller.Id, thread.ID, 0, 50)
	require.NoError(t, err)
	require.Equal(t, second.ID, history.Conversation.SellerReadID)
	rows, err = ListMerchantStoreSupport(ctx, f.seller.Id, "seller", "", true, 0, 30)
	require.NoError(t, err)
	require.Empty(t, rows)
	require.ErrorIs(t, MarkMerchantStoreSupportRead(ctx, f.seller.Id, thread.ID, second.ID+100), ErrMerchantStoreInput)
	require.NoError(t, SetMerchantStoreSupportStatus(ctx, f.seller.Id, thread.ID, "resolved"))
	_, err = SendMerchantStoreSupportMessage(ctx, f.buyer.Id, thread.ID, "third-message", "one more question")
	require.NoError(t, err)
	history, err = GetMerchantStoreSupportHistory(ctx, f.buyer.Id, thread.ID, 0, 2)
	require.NoError(t, err)
	require.Equal(t, "open", history.Conversation.Status)
	require.True(t, history.HasMore)
	require.Len(t, history.Items, 2)
	older, err := GetMerchantStoreSupportHistory(ctx, f.buyer.Id, thread.ID, history.Items[0].ID, 2)
	require.NoError(t, err)
	require.False(t, older.HasMore)
	require.Equal(t, first.ID, older.Items[0].ID)
}

func TestMerchantStoreSupportOrderOwnershipAndInput(t *testing.T) {
	f := supportFixture(t)
	ctx := context.Background()
	order, _, err := CreateMerchantStoreOrder(f.checkout("support-order", "balance"))
	require.NoError(t, err)
	thread, err := OpenMerchantStoreSupport(ctx, f.seller.Id, "", order.ID)
	require.NoError(t, err)
	fromBuyer, err := OpenMerchantStoreSupport(ctx, f.buyer.Id, "", order.ID)
	require.NoError(t, err)
	require.Equal(t, thread.ID, fromBuyer.ID)
	_, err = OpenMerchantStoreSupport(ctx, f.root.Id, "", order.ID)
	require.ErrorIs(t, err, ErrMerchantStoreDenied)
	require.NoError(t, UnlistMerchantStoreProduct(f.seller.Id, f.product.ID))
	_, err = OpenMerchantStoreSupport(ctx, f.buyer.Id, "", order.ID)
	require.NoError(t, err)
	_, err = OpenMerchantStoreSupport(ctx, f.buyer.Id, f.product.ID, order.ID)
	require.ErrorIs(t, err, ErrMerchantStoreInput)
	for _, body := range []string{"", "  ", strings.Repeat("字", 4001), "bad\x00text", "\xff"} {
		_, err = SendMerchantStoreSupportMessage(ctx, f.buyer.Id, thread.ID, "invalid-message", body)
		require.ErrorIs(t, err, ErrMerchantStoreInput)
	}
	_, err = SendMerchantStoreSupportMessage(ctx, f.buyer.Id, thread.ID, "short", "body")
	require.ErrorIs(t, err, ErrMerchantStoreInput)
	_, err = GetMerchantStoreSupportHistory(ctx, f.buyer.Id, thread.ID, -1, 10)
	require.ErrorIs(t, err, ErrMerchantStoreInput)
	_, err = ListMerchantStoreSupport(ctx, f.buyer.Id, "admin", "", false, 0, 30)
	require.ErrorIs(t, err, ErrMerchantStoreInput)
	// Do not turn a guest order into an authenticated buyer's order.
	require.NoError(t, DB.Model(order).UpdateColumn("buyer_id", 0).Error)
	_, err = OpenMerchantStoreSupport(ctx, f.seller.Id, "", order.ID)
	require.ErrorIs(t, err, ErrMerchantStoreLoginRequired)
}

func TestMerchantStoreCustomersHistoricalOrdersPrivateNotesAndRevision(t *testing.T) {
	f := supportFixture(t)
	ctx := context.Background()
	order, _, err := CreateMerchantStoreOrder(f.checkout("historic-buyer", "balance"))
	require.NoError(t, err)
	// A synthetic order for another seller must not contaminate this summary.
	other := MerchantStoreOrder{ID: "other-seller-order", TradeNo: "other-seller-trade", SellerID: f.root.Id, BuyerID: f.buyer.Id, ProductID: f.product.ID, Status: "paid", PriceQuota: 9999999, PaidAt: 1, CreatedAt: 1, PickupTokenHash: "other-pickup-hash"}
	require.NoError(t, DB.Create(&other).Error)
	rows, err := ListMerchantStoreCustomers(ctx, f.seller.Id, "", 0, 30)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.EqualValues(t, 1, rows[0].OrderCount)
	require.EqualValues(t, order.PriceQuota, rows[0].PaidQuota)
	require.Equal(t, order.ID, rows[0].LastOrderID)
	require.Empty(t, rows[0].ConversationID)
	note := MerchantStoreCustomerInput{Note: "PRIVATE-SELLER-NOTE", Tags: []string{"regular", "regular"}}
	require.NoError(t, SaveMerchantStoreCustomer(ctx, f.seller.Id, f.buyer.Id, note))
	require.ErrorIs(t, SaveMerchantStoreCustomer(ctx, f.seller.Id, f.buyer.Id, note), ErrMerchantStoreConflict)
	require.ErrorIs(t, SaveMerchantStoreCustomer(ctx, f.buyer.Id, f.seller.Id, note), ErrMerchantStoreDenied)
	rows, err = ListMerchantStoreCustomers(ctx, f.seller.Id, "store-buyer", 0, 30)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, note.Note, rows[0].Note)
	require.EqualValues(t, 1, rows[0].Revision)
	require.Equal(t, []string{"regular"}, rows[0].Tags)
	otherRows, err := ListMerchantStoreCustomers(ctx, f.root.Id, "", 0, 30)
	require.NoError(t, err)
	require.Len(t, otherRows, 1)
	require.Empty(t, otherRows[0].Note)
	require.EqualValues(t, 9999999, otherRows[0].PaidQuota)
	var stored MerchantStoreCustomer
	require.NoError(t, DB.Where("seller_id = ? AND buyer_id = ?", f.seller.Id, f.buyer.Id).First(&stored).Error)
	require.NotContains(t, stored.NoteCiphertext, note.Note)
	note.Revision, note.Note, note.Tags = 1, "", []string{}
	require.NoError(t, SaveMerchantStoreCustomer(ctx, f.seller.Id, f.buyer.Id, note))
	require.ErrorIs(t, SaveMerchantStoreCustomer(ctx, f.seller.Id, f.buyer.Id, note), ErrMerchantStoreConflict)
	rows, err = ListMerchantStoreCustomers(ctx, f.seller.Id, "%", 0, 30)
	require.NoError(t, err)
	require.Empty(t, rows)
}

func TestMerchantStoreSupportAssistantAllowlistAndBounds(t *testing.T) {
	f := supportFixture(t)
	ctx := context.Background()
	order, _, err := CreateMerchantStoreOrder(f.checkout("assistant-order", "balance"))
	require.NoError(t, err)
	thread, err := OpenMerchantStoreSupport(ctx, f.buyer.Id, "", order.ID)
	require.NoError(t, err)
	require.NoError(t, SaveMerchantStoreCustomer(ctx, f.seller.Id, f.buyer.Id, MerchantStoreCustomerInput{Note: "NEVER-SHARE-NOTE"}))
	for i := 0; i < 12; i++ {
		_, err := SendMerchantStoreSupportMessage(ctx, f.buyer.Id, thread.ID, fmt.Sprintf("context-message-%d", i), strings.Repeat("字", 600))
		require.NoError(t, err)
	}
	for _, actor := range []int{f.buyer.Id, f.seller.Id} {
		context, err := GetMerchantStoreSupportAssistantContext(ctx, actor, thread.ID)
		require.NoError(t, err)
		require.Len(t, context.Messages, 10)
		require.LessOrEqual(t, len([]rune(context.Messages[0].Text)), 501)
		require.Equal(t, order.ID, context.Order.ID)
		if actor == f.seller.Id { require.Equal(t, "seller", context.Role) } else { require.Equal(t, "buyer", context.Role) }
		encoded, err := json.Marshal(context)
		require.NoError(t, err)
		for _, hidden := range []string{"NEVER-SHARE-NOTE", "pickup", "checkout_url", "gateway", "buyer_id", "seller_id", "email", "CARD-SECRET"} {
			require.NotContains(t, string(encoded), hidden)
		}
	}
}

func TestMerchantStoreSupportConcurrentReplay(t *testing.T) {
	f := supportFixture(t)
	ctx := context.Background()
	thread, err := OpenMerchantStoreSupport(ctx, f.buyer.Id, f.product.ID, "")
	require.NoError(t, err)
	var wg sync.WaitGroup
	ids := make(chan int64, 2)
	errors := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			message, err := SendMerchantStoreSupportMessage(ctx, f.buyer.Id, thread.ID, "same-request-key", "same body")
			errors <- err
			if err == nil { ids <- message.ID }
		}()
	}
	wg.Wait()
	close(errors)
	close(ids)
	for err := range errors { require.NoError(t, err) }
	var first int64
	for id := range ids { if first == 0 { first = id }; require.Equal(t, first, id) }
	history, err := GetMerchantStoreSupportHistory(ctx, f.buyer.Id, thread.ID, 0, 50)
	require.NoError(t, err)
	require.Len(t, history.Items, 1)
}

func TestMerchantStoreSupportIndependentMigrationRegistry(t *testing.T) {
	f := supportFixture(t)
	_ = f
	require.NoError(t, DB.AutoMigrate(MerchantStoreSupportModels()...))
	for _, support := range MerchantStoreSupportModels() {
		found := false
		for _, main := range mainMigrationModels() { if reflect.TypeOf(main) == reflect.TypeOf(support) { found = true } }
		require.True(t, found, "support must be in normal migrations")
		for _, historic := range MerchantStoreModels() { require.NotEqual(t, reflect.TypeOf(support), reflect.TypeOf(historic), "do not change historical store activation requirements") }
	}
}
