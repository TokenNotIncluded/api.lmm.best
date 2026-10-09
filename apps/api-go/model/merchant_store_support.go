// Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later.
package model

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Support has its own additive migration registry. It does not change the shop
// writer floor, payment state, stock, account roles, or wallet balances.
type MerchantStoreSupportConversation struct {
	ID            string `json:"id" gorm:"primaryKey;size:36"`
	ScopeKey      string `json:"-" gorm:"size:160;not null;uniqueIndex"`
	BuyerID       int    `json:"buyer_id" gorm:"not null;index:store_support_buyer,priority:1"`
	SellerID      int    `json:"seller_id" gorm:"not null;index:store_support_seller,priority:1"`
	ProductID     string `json:"product_id" gorm:"size:36;not null"`
	OrderID       string `json:"order_id" gorm:"size:64;not null;default:''"`
	Subject       string `json:"subject" gorm:"size:200;not null"`
	Status        string `json:"status" gorm:"size:16;not null;default:'open'"`
	LastMessageID int64  `json:"last_message_id" gorm:"not null;default:0"`
	BuyerReadID   int64  `json:"buyer_read_id" gorm:"not null;default:0"`
	SellerReadID  int64  `json:"seller_read_id" gorm:"not null;default:0"`
	CreatedAt     int64  `json:"created_at" gorm:"not null"`
	UpdatedAt     int64  `json:"updated_at" gorm:"not null;index:store_support_buyer,priority:2;index:store_support_seller,priority:2"`
}

type MerchantStoreSupportMessage struct {
	ID             int64  `json:"id" gorm:"primaryKey;autoIncrement;index:store_support_history,priority:2"`
	ConversationID string `json:"conversation_id" gorm:"size:36;not null;index:store_support_history,priority:1;uniqueIndex:store_support_replay,priority:1"`
	SenderID       int    `json:"sender_id" gorm:"not null;uniqueIndex:store_support_replay,priority:2"`
	RequestKey     string `json:"-" gorm:"size:64;not null;uniqueIndex:store_support_replay,priority:3"`
	BodyDigest     string `json:"-" gorm:"size:64;not null"`
	Ciphertext     string `json:"-" gorm:"type:text;not null"`
	Body           string `json:"body" gorm:"-:all"`
	CreatedAt      int64  `json:"created_at" gorm:"not null"`
}

type MerchantStoreSupportThread struct {
	MerchantStoreSupportConversation
	BuyerName   string `json:"buyer_name"`
	SellerName  string `json:"seller_name"`
	UnreadCount int64  `json:"unread_count"`
}

type MerchantStoreSupportHistory struct {
	Conversation MerchantStoreSupportConversation `json:"conversation"`
	Items        []MerchantStoreSupportMessage     `json:"items"`
	HasMore      bool                              `json:"has_more"`
}

func MerchantStoreSupportModels() []interface{} {
	return []interface{}{&MerchantStoreSupportConversation{}, &MerchantStoreSupportMessage{}, &MerchantStoreCustomer{}}
}

func storeSupportActor(tx *gorm.DB, actor int) error {
	if actor <= 0 {
		return ErrMerchantStoreDenied
	}
	_, err := storeUser(tx, actor, common.RoleCommonUser)
	return err
}

func storeSupportConversation(tx *gorm.DB, actor int, id string, lock bool) (*MerchantStoreSupportConversation, error) {
	if err := storeSupportActor(tx, actor); err != nil {
		return nil, err
	}
	query := tx.Where("id = ? AND (buyer_id = ? OR seller_id = ?)", id, actor, actor)
	if lock {
		query = query.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	var row MerchantStoreSupportConversation
	if err := query.First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrMerchantStoreDenied
		}
		return nil, err
	}
	return &row, nil
}

// A product starts a buyer inquiry. An order lets either actual participant
// start after-sales support, including for a product that is no longer listed.
// Account roles never grant access to another buyer/seller's private messages.
func OpenMerchantStoreSupport(ctx context.Context, actor int, productID, orderID string) (*MerchantStoreSupportConversation, error) {
	if (productID == "") == (orderID == "") || len(productID) > 36 || len(orderID) > 64 {
		return nil, ErrMerchantStoreInput
	}
	var result MerchantStoreSupportConversation
	err := marketTransaction(DB.WithContext(ctx), func(tx *gorm.DB) error {
		if err := storeSupportActor(tx, actor); err != nil {
			return err
		}
		now := common.GetTimestamp()
		row := MerchantStoreSupportConversation{ID: uuid.NewString(), Status: "open", CreatedAt: now, UpdatedAt: now}
		if orderID != "" {
			var order MerchantStoreOrder
			err := tx.Where("id = ? AND (buyer_id = ? OR seller_id = ?)", orderID, actor, actor).First(&order).Error
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrMerchantStoreDenied
			}
			if err != nil {
				return err
			}
			// A guest order cannot be claimed by supplying its number or email.
			if order.BuyerID <= 0 {
				return ErrMerchantStoreLoginRequired
			}
			row.BuyerID, row.SellerID = order.BuyerID, order.SellerID
			row.ProductID, row.OrderID, row.Subject = order.ProductID, order.ID, order.ProductTitle
			row.ScopeKey = "order:" + order.ID
		} else {
			var product MerchantStoreProduct
			if err := MerchantStoreVisibleProductsForViewer(tx, actor).Where("merchant_store_products.id = ?", productID).First(&product).Error; err != nil {
				return err
			}
			row.BuyerID, row.SellerID = actor, product.SellerID
			row.ProductID, row.Subject = product.ID, product.Title
			row.ScopeKey = "product:" + product.ID + ":buyer:" + strconv.Itoa(actor)
		}
		if row.BuyerID == row.SellerID {
			return ErrMerchantStoreInput
		}
		if err := storeSupportActor(tx, row.SellerID); err != nil {
			return err
		}
		if err := storeSupportActor(tx, row.BuyerID); err != nil {
			return err
		}
		if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "scope_key"}}, DoNothing: true}).Create(&row).Error; err != nil {
			return err
		}
		result = MerchantStoreSupportConversation{}
		return tx.Where("scope_key = ? AND buyer_id = ? AND seller_id = ?", row.ScopeKey, row.BuyerID, row.SellerID).First(&result).Error
	})
	return &result, err
}

func ListMerchantStoreSupport(ctx context.Context, actor int, role, status string, unread bool, offset, limit int) ([]MerchantStoreSupportThread, error) {
	tx := DB.WithContext(ctx)
	if err := storeSupportActor(tx, actor); err != nil {
		return nil, err
	}
	if (role != "buyer" && role != "seller") || (status != "" && status != "open" && status != "resolved") {
		return nil, ErrMerchantStoreInput
	}
	offset, limit = storePage(offset, limit)
	owner, read := "buyer_id", "buyer_read_id"
	if role == "seller" {
		owner, read = "seller_id", "seller_read_id"
	}
	// Both column names are chosen from literals above, never request text.
	query := tx.Table("merchant_store_support_conversations AS conversations").
		Select("conversations.*, COALESCE(NULLIF(buyer.display_name, ''), buyer.username, '') AS buyer_name, COALESCE(NULLIF(seller.display_name, ''), seller.username, '') AS seller_name, (SELECT COUNT(*) FROM merchant_store_support_messages AS messages WHERE messages.conversation_id = conversations.id AND messages.sender_id <> ? AND messages.id > conversations."+read+") AS unread_count", actor).
		Joins("LEFT JOIN users AS buyer ON buyer.id = conversations.buyer_id").
		Joins("LEFT JOIN users AS seller ON seller.id = conversations.seller_id").
		Where("conversations."+owner+" = ?", actor)
	if status != "" {
		query = query.Where("conversations.status = ?", status)
	}
	if unread {
		query = query.Where("EXISTS (SELECT 1 FROM merchant_store_support_messages AS unread WHERE unread.conversation_id = conversations.id AND unread.sender_id <> ? AND unread.id > conversations."+read+")", actor)
	}
	rows := []MerchantStoreSupportThread{}
	err := query.Order("conversations.updated_at DESC, conversations.id DESC").Offset(offset).Limit(limit).Scan(&rows).Error
	return rows, err
}

func GetMerchantStoreSupportHistory(ctx context.Context, actor int, id string, before int64, limit int) (*MerchantStoreSupportHistory, error) {
	tx := DB.WithContext(ctx)
	row, err := storeSupportConversation(tx, actor, id, false)
	if err != nil {
		return nil, err
	}
	if before < 0 || limit < 1 || limit > 100 {
		return nil, ErrMerchantStoreInput
	}
	query := tx.Where("conversation_id = ?", id)
	if before > 0 {
		query = query.Where("id < ?", before)
	}
	items := []MerchantStoreSupportMessage{}
	if err := query.Order("id DESC").Limit(limit + 1).Find(&items).Error; err != nil {
		return nil, err
	}
	hasMore := len(items) > limit
	if hasMore {
		items = items[:limit]
	}
	for i := range items {
		items[i].Body, err = storeDecrypt("support-message", items[i].ConversationID+":"+items[i].RequestKey+":"+strconv.Itoa(items[i].SenderID), items[i].Ciphertext)
		if err != nil {
			return nil, err
		}
	}
	for left, right := 0, len(items)-1; left < right; left, right = left+1, right-1 {
		items[left], items[right] = items[right], items[left]
	}
	return &MerchantStoreSupportHistory{Conversation: *row, Items: items, HasMore: hasMore}, nil
}

func SendMerchantStoreSupportMessage(ctx context.Context, actor int, id, requestKey, body string) (*MerchantStoreSupportMessage, error) {
	body = strings.TrimSpace(body)
	if len(requestKey) < 8 || len(requestKey) > 64 || strings.ContainsAny(requestKey, "\x00\r\n:") || !utf8.ValidString(body) || body == "" || utf8.RuneCountInString(body) > 4000 || strings.ContainsRune(body, 0) {
		return nil, ErrMerchantStoreInput
	}
	var result MerchantStoreSupportMessage
	err := marketTransaction(DB.WithContext(ctx), func(tx *gorm.DB) error {
		row, err := storeSupportConversation(tx, actor, id, true)
		if err != nil {
			return err
		}
		result = MerchantStoreSupportMessage{}
		err = tx.Where("conversation_id = ? AND sender_id = ? AND request_key = ?", id, actor, requestKey).First(&result).Error
		if err == nil {
			if result.BodyDigest != storeHash(body) {
				return ErrMerchantStoreConflict
			}
			result.Body = body
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		ciphertext, err := storeEncrypt("support-message", id+":"+requestKey+":"+strconv.Itoa(actor), body)
		if err != nil {
			return err
		}
		now := common.GetTimestamp()
		result = MerchantStoreSupportMessage{ConversationID: id, SenderID: actor, RequestKey: requestKey, BodyDigest: storeHash(body), Ciphertext: ciphertext, Body: body, CreatedAt: now}
		if err := tx.Create(&result).Error; err != nil {
			return err
		}
		// Do not mark earlier incoming messages read merely because a user sends.
		return tx.Model(row).Updates(map[string]any{"last_message_id": result.ID, "status": "open", "updated_at": now}).Error
	})
	return &result, err
}

func MarkMerchantStoreSupportRead(ctx context.Context, actor int, id string, through int64) error {
	if through <= 0 {
		return ErrMerchantStoreInput
	}
	return marketTransaction(DB.WithContext(ctx), func(tx *gorm.DB) error {
		row, err := storeSupportConversation(tx, actor, id, true)
		if err != nil {
			return err
		}
		var message MerchantStoreSupportMessage
		if err := tx.Select("id").Where("id = ? AND conversation_id = ?", through, id).First(&message).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrMerchantStoreInput
			}
			return err
		}
		column, current := "buyer_read_id", row.BuyerReadID
		if actor == row.SellerID {
			column, current = "seller_read_id", row.SellerReadID
		}
		if through <= current {
			return nil
		}
		return tx.Model(row).UpdateColumn(column, through).Error
	})
}

func SetMerchantStoreSupportStatus(ctx context.Context, actor int, id, status string) error {
	if status != "open" && status != "resolved" {
		return ErrMerchantStoreInput
	}
	return marketTransaction(DB.WithContext(ctx), func(tx *gorm.DB) error {
		row, err := storeSupportConversation(tx, actor, id, true)
		if err != nil {
			return err
		}
		return tx.Model(row).Updates(map[string]any{"status": status, "updated_at": common.GetTimestamp()}).Error
	})
}
