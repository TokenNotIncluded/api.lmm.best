// Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later.
package model

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// A customer record belongs to one seller, not to a global customer directory.
// Notes are encrypted and are never included in buyer or assistant responses.
type MerchantStoreCustomer struct {
	SellerID       int      `json:"-" gorm:"primaryKey;autoIncrement:false"`
	BuyerID        int      `json:"buyer_id" gorm:"primaryKey;autoIncrement:false"`
	NoteCiphertext string   `json:"-" gorm:"type:text;not null"`
	Tags           []string `json:"tags" gorm:"serializer:json;type:text"`
	Revision       int64    `json:"revision" gorm:"not null;default:1"`
	UpdatedAt      int64    `json:"updated_at" gorm:"not null"`
}

type MerchantStoreCustomerSummary struct {
	BuyerID        int      `json:"buyer_id"`
	DisplayName    string   `json:"display_name"`
	OrderCount     int64    `json:"order_count"`
	PaidQuota      int64    `json:"paid_quota"`
	LastOrderAt    int64    `json:"last_order_at"`
	LastOrderID    string   `json:"last_order_id"`
	ConversationID string   `json:"conversation_id"`
	Note           string   `json:"note"`
	Tags           []string `json:"tags" gorm:"-"`
	Revision       int64    `json:"revision"`
}

type MerchantStoreCustomerInput struct {
	Note     string   `json:"note"`
	Tags     []string `json:"tags"`
	Revision int64    `json:"revision"`
}

func storeCustomerRelated(tx *gorm.DB, seller, buyer int) (bool, error) {
	if buyer <= 0 || buyer == seller {
		return false, nil
	}
	var count int64
	if err := tx.Model(&MerchantStoreOrder{}).Where("seller_id = ? AND buyer_id = ?", seller, buyer).Limit(1).Count(&count).Error; err != nil {
		return false, err
	}
	if count > 0 {
		return true, nil
	}
	err := tx.Model(&MerchantStoreSupportConversation{}).Where("seller_id = ? AND buyer_id = ?", seller, buyer).Limit(1).Count(&count).Error
	return count > 0, err
}

func ListMerchantStoreCustomers(ctx context.Context, seller int, search string, offset, limit int) ([]MerchantStoreCustomerSummary, error) {
	tx := DB.WithContext(ctx)
	if err := storeSupportActor(tx, seller); err != nil {
		return nil, err
	}
	search = strings.TrimSpace(search)
	if !utf8.ValidString(search) || utf8.RuneCountInString(search) > 100 {
		return nil, ErrMerchantStoreInput
	}
	offset, limit = storePage(offset, limit)
	query := tx.Table("users AS customers").Select(`customers.id AS buyer_id,
		COALESCE(NULLIF(customers.display_name, ''), customers.username) AS display_name,
		(SELECT COUNT(*) FROM merchant_store_orders AS orders WHERE orders.seller_id = ? AND orders.buyer_id = customers.id) AS order_count,
		(SELECT COALESCE(SUM(orders.price_quota), 0) FROM merchant_store_orders AS orders WHERE orders.seller_id = ? AND orders.buyer_id = customers.id AND orders.paid_at > 0) AS paid_quota,
		(SELECT COALESCE(MAX(orders.created_at), 0) FROM merchant_store_orders AS orders WHERE orders.seller_id = ? AND orders.buyer_id = customers.id) AS last_order_at,
		COALESCE((SELECT orders.id FROM merchant_store_orders AS orders WHERE orders.seller_id = ? AND orders.buyer_id = customers.id ORDER BY orders.created_at DESC, orders.id DESC LIMIT 1), '') AS last_order_id,
		COALESCE((SELECT conversations.id FROM merchant_store_support_conversations AS conversations WHERE conversations.seller_id = ? AND conversations.buyer_id = customers.id ORDER BY conversations.updated_at DESC, conversations.id DESC LIMIT 1), '') AS conversation_id`, seller, seller, seller, seller, seller).
		Where("customers.deleted_at IS NULL AND customers.id <> ?", seller).
		Where(`EXISTS (SELECT 1 FROM merchant_store_orders AS orders WHERE orders.seller_id = ? AND orders.buyer_id = customers.id)
			OR EXISTS (SELECT 1 FROM merchant_store_support_conversations AS conversations WHERE conversations.seller_id = ? AND conversations.buyer_id = customers.id)`, seller, seller)
	if search != "" {
		literal := strings.NewReplacer("!", "!!", "%", "!%", "_", "!_").Replace(strings.ToLower(search))
		query = query.Where("LOWER(customers.username) LIKE ? ESCAPE '!' OR LOWER(customers.display_name) LIKE ? ESCAPE '!'", "%"+literal+"%", "%"+literal+"%")
	}
	rows := []MerchantStoreCustomerSummary{}
	if err := query.Order("last_order_at DESC, customers.id DESC").Offset(offset).Limit(limit).Scan(&rows).Error; err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return rows, nil
	}
	ids := make([]int, len(rows))
	for i := range rows {
		ids[i] = rows[i].BuyerID
		rows[i].Tags = []string{}
	}
	var notes []MerchantStoreCustomer
	if err := tx.Where("seller_id = ? AND buyer_id IN ?", seller, ids).Find(&notes).Error; err != nil {
		return nil, err
	}
	byBuyer := make(map[int]MerchantStoreCustomer, len(notes))
	for _, note := range notes {
		byBuyer[note.BuyerID] = note
	}
	for i := range rows {
		note, ok := byBuyer[rows[i].BuyerID]
		if !ok {
			continue
		}
		text, err := storeDecrypt("customer-note", strconv.Itoa(seller)+":"+strconv.Itoa(note.BuyerID), note.NoteCiphertext)
		if err != nil {
			return nil, err
		}
		rows[i].Note, rows[i].Revision = text, note.Revision
		if note.Tags != nil {
			rows[i].Tags = note.Tags
		}
	}
	return rows, nil
}

func SaveMerchantStoreCustomer(ctx context.Context, seller, buyer int, in MerchantStoreCustomerInput) error {
	in.Note = strings.TrimSpace(in.Note)
	if in.Revision < 0 || in.Revision >= 9007199254740991 || !utf8.ValidString(in.Note) || utf8.RuneCountInString(in.Note) > 4000 || strings.ContainsRune(in.Note, 0) || len(in.Tags) > 10 {
		return ErrMerchantStoreInput
	}
	tags := make([]string, 0, len(in.Tags))
	seen := map[string]bool{}
	for _, tag := range in.Tags {
		tag = strings.TrimSpace(tag)
		if tag == "" || !utf8.ValidString(tag) || utf8.RuneCountInString(tag) > 32 || strings.ContainsAny(tag, "\x00\r\n") {
			return ErrMerchantStoreInput
		}
		if !seen[tag] {
			tags, seen[tag] = append(tags, tag), true
		}
	}
	return marketTransaction(DB.WithContext(ctx), func(tx *gorm.DB) error {
		if err := storeSupportActor(tx, seller); err != nil {
			return err
		}
		related, err := storeCustomerRelated(tx, seller, buyer)
		if err != nil {
			return err
		}
		if !related {
			return ErrMerchantStoreDenied
		}
		ciphertext, err := storeEncrypt("customer-note", strconv.Itoa(seller)+":"+strconv.Itoa(buyer), in.Note)
		if err != nil {
			return err
		}
		row := MerchantStoreCustomer{SellerID: seller, BuyerID: buyer, NoteCiphertext: ciphertext, Tags: tags, Revision: in.Revision + 1, UpdatedAt: common.GetTimestamp()}
		if in.Revision == 0 {
			result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&row)
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return ErrMerchantStoreConflict
			}
			return nil
		}
		result := tx.Model(&MerchantStoreCustomer{}).Where("seller_id = ? AND buyer_id = ? AND revision = ?", seller, buyer, in.Revision).
			Select("note_ciphertext", "tags", "revision", "updated_at").Updates(&row)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrMerchantStoreConflict
		}
		return nil
	})
}

// This deliberately is not a serialized User or Order. Payment links, pickup
// codes, inventory, customer notes and account credentials have no fields here.
type MerchantStoreSupportAssistantContext struct {
	Role           string `json:"role"`
	ConversationID string `json:"conversation_id"`
	Subject        string `json:"subject"`
	Order          *struct {
		ID       string `json:"id"`
		Status   string `json:"status"`
		Quantity int    `json:"quantity"`
	} `json:"order,omitempty"`
	Messages []MerchantStoreSupportAssistantMessage `json:"messages"`
}

type MerchantStoreSupportAssistantMessage struct {
	Role string `json:"role"`
	Text string `json:"text"`
}

func GetMerchantStoreSupportAssistantContext(ctx context.Context, actor int, id string) (*MerchantStoreSupportAssistantContext, error) {
	history, err := GetMerchantStoreSupportHistory(ctx, actor, id, 0, 10)
	if err != nil {
		return nil, err
	}
	row := history.Conversation
	result := &MerchantStoreSupportAssistantContext{Role: "buyer", ConversationID: row.ID, Subject: row.Subject, Messages: []MerchantStoreSupportAssistantMessage{}}
	if actor == row.SellerID {
		result.Role = "seller"
	}
	if row.OrderID != "" {
		var order MerchantStoreOrder
		err := DB.WithContext(ctx).Select("id", "status", "quantity").Where("id = ? AND buyer_id = ? AND seller_id = ?", row.OrderID, row.BuyerID, row.SellerID).First(&order).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
		if err == nil {
			result.Order = &struct {
				ID       string `json:"id"`
				Status   string `json:"status"`
				Quantity int    `json:"quantity"`
			}{order.ID, order.Status, order.Quantity}
		}
	}
	for _, message := range history.Items {
		role := "buyer"
		if message.SenderID == row.SellerID {
			role = "seller"
		}
		text := []rune(message.Body)
		if len(text) > 500 {
			text = append(text[:500], '…')
		}
		result.Messages = append(result.Messages, MerchantStoreSupportAssistantMessage{Role: role, Text: string(text)})
	}
	return result, nil
}
