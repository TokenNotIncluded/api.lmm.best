package support

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules/store"
)

type WebConversation struct {
	ID            string `json:"id"`
	BuyerID       int64  `json:"buyer_id"`
	SellerID      int64  `json:"seller_id"`
	ProductID     string `json:"product_id"`
	OrderID       string `json:"order_id"`
	Subject       string `json:"subject"`
	Status        string `json:"status"`
	LastMessageID int64  `json:"last_message_id"`
	BuyerReadID   int64  `json:"buyer_read_id"`
	SellerReadID  int64  `json:"seller_read_id"`
	CreatedAt     int64  `json:"created_at"`
	UpdatedAt     int64  `json:"updated_at"`
}
type WebMessage struct {
	ID             int64  `json:"id"`
	ConversationID string `json:"conversation_id"`
	SenderID       int64  `json:"sender_id"`
	Body           string `json:"body"`
	CreatedAt      int64  `json:"created_at"`
}
type WebThread struct {
	WebConversation
	BuyerName   string `json:"buyer_name"`
	SellerName  string `json:"seller_name"`
	UnreadCount int64  `json:"unread_count"`
}
type WebHistory struct {
	Conversation WebConversation `json:"conversation"`
	Items        []WebMessage    `json:"items"`
	HasMore      bool            `json:"has_more"`
}
type WebCustomer struct {
	BuyerID        int64    `json:"buyer_id"`
	DisplayName    string   `json:"display_name"`
	OrderCount     int64    `json:"order_count"`
	PaidQuota      int64    `json:"paid_quota"`
	LastOrderAt    int64    `json:"last_order_at"`
	LastOrderID    string   `json:"last_order_id"`
	ConversationID string   `json:"conversation_id"`
	Note           string   `json:"note"`
	Tags           []string `json:"tags"`
	Revision       int64    `json:"revision"`
}

func webConversation(c Conversation) WebConversation {
	status := "open"
	if c.Closed {
		status = "resolved"
	}
	updated := c.UpdatedAt
	if updated.IsZero() {
		updated = c.CreatedAt
	}
	return WebConversation{c.ID, c.Buyer.ID, c.Seller.ID, c.ProductID, c.OrderID, c.Subject, status, c.LastMessageID, c.BuyerReadID, c.SellerReadID, c.CreatedAt.Unix(), updated.Unix()}
}
func webMessage(m Message) (WebMessage, error) {
	id := m.NumericID
	if id == 0 {
		_, suffix, ok := strings.Cut(m.ID, ":")
		if !ok {
			return WebMessage{}, store.ErrUnavailable
		}
		var err error
		id, err = strconv.ParseInt(suffix, 10, 64)
		if err != nil || id <= 0 {
			return WebMessage{}, store.ErrUnavailable
		}
	}
	return WebMessage{id, m.ConversationID, m.AuthorUserID, m.Body, m.CreatedAt.Unix()}, nil
}
func (s *Service) webActor(ctx context.Context, token string, actor int64) error {
	id, err := s.check(ctx, token, store.Account{Kind: "personal", ID: actor}, store.Read)
	if err != nil {
		return err
	}
	if id != actor {
		return store.ErrForbidden
	}
	return nil
}
func (s *Service) webConversation(ctx context.Context, token string, actor int64, id string) (LocatedConversation, error) {
	if err := s.webActor(ctx, token, actor); err != nil {
		return LocatedConversation{}, err
	}
	index, ok := s.repo.(LegacyIndex)
	if !ok {
		return LocatedConversation{}, store.ErrUnavailable
	}
	row, err := index.FindConversation(ctx, id)
	if err != nil {
		return row, err
	}
	if row.Conversation.Buyer.Kind != "personal" || row.Conversation.Seller.Kind != "personal" || (actor != row.Conversation.Buyer.ID && actor != row.Conversation.Seller.ID) {
		return row, store.ErrForbidden
	}
	_, _, err = s.participant(ctx, token, row.Conversation)
	return row, err
}

type catalogueDirectory interface {
	LocateProduct(context.Context, string) (store.LocatedProduct, error)
	LocateOrder(context.Context, string) (store.LocatedOrder, error)
}

func (s *Service) OpenWeb(ctx context.Context, token string, actor int64, productID, orderID string) (WebConversation, error) {
	if (productID == "") == (orderID == "") {
		return WebConversation{}, store.ErrInvalid
	}
	if err := s.webActor(ctx, token, actor); err != nil {
		return WebConversation{}, err
	}
	dir, ok := s.directory.(catalogueDirectory)
	if !ok {
		return WebConversation{}, store.ErrUnavailable
	}
	var request OpenRequest
	request.Buyer = store.Account{Kind: "personal", ID: actor}
	if orderID != "" {
		order, err := dir.LocateOrder(ctx, orderID)
		if err != nil {
			return WebConversation{}, err
		}
		if order.Order.Buyer.Kind != "personal" || order.Order.Seller.Kind != "personal" || (actor != order.Order.Buyer.ID && actor != order.Order.Seller.ID) {
			return WebConversation{}, store.ErrForbidden
		}
		request.ShopID, request.Buyer, request.OrderID, request.Subject = order.ShopID, order.Order.Buyer, order.Order.ID, order.Order.ProductTitle
		productID = order.ProductID
		request.Key = store.ID("web-order-support", orderID)
		request.StableActor = order.Order.Buyer.ID
	} else {
		product, err := dir.LocateProduct(ctx, productID)
		if err != nil {
			return WebConversation{}, err
		}
		if !product.Product.Published {
			return WebConversation{}, store.ErrNotFound
		}
		if len(product.Product.LegacyPublic) > 0 {
			var snapshot map[string]json.RawMessage
			if json.Unmarshal(product.Product.LegacyPublic, &snapshot) != nil || snapshot == nil {
				return WebConversation{}, store.ErrUnavailable
			}
			if raw, exists := snapshot["visibility"]; exists {
				var visibility string
				if string(raw) == "null" || json.Unmarshal(raw, &visibility) != nil {
					return WebConversation{}, store.ErrUnavailable
				}
				if visibility != "" && visibility != "public" {
					return WebConversation{}, store.ErrNotFound
				}
			}
		}
		if product.Owner.Kind != "personal" || product.Owner.ID == actor {
			return WebConversation{}, store.ErrInvalid
		}
		request.ShopID, request.Subject = product.ShopID, product.Product.Title
		request.Key = store.ID("web-product-support", productID, actor)
	}
	c, err := s.Open(ctx, token, request)
	if err != nil {
		return WebConversation{}, err
	}
	err = s.repo.Within(ctx, request.ShopID, func(tx Tx) error {
		if err := tx.Get("conversations", c.ID, &c); err != nil {
			return err
		}
		c.ProductID = productID
		return tx.Put("conversations", c.ID, c)
	})
	return webConversation(c), err
}
func (s *Service) ListWeb(ctx context.Context, token string, actor int64, role, status string, unread bool, offset, limit int) ([]WebThread, error) {
	if err := s.webActor(ctx, token, actor); err != nil {
		return nil, err
	}
	if offset < 0 || offset > 100000 || !store.Page(limit) {
		return nil, store.ErrInvalid
	}
	index, ok := s.repo.(LegacyIndex)
	if !ok {
		return nil, store.ErrUnavailable
	}
	rows, err := index.ListForActor(ctx, actor, role, status, unread, offset, limit)
	if err != nil {
		return nil, err
	}
	out := make([]WebThread, 0, len(rows))
	for _, row := range rows {
		account := row.Conversation.Buyer
		if role == "seller" {
			account = row.Conversation.Seller
		}
		if account.Kind != "personal" || account.ID != actor {
			return nil, store.ErrForbidden
		}
		out = append(out, WebThread{WebConversation: webConversation(row.Conversation), UnreadCount: row.UnreadCount})
	}
	return out, nil
}
func (s *Service) HistoryWeb(ctx context.Context, token string, actor int64, id string, before int64, limit int) (WebHistory, error) {
	if before < 0 || !store.Page(limit) {
		return WebHistory{}, store.ErrInvalid
	}
	row, err := s.webConversation(ctx, token, actor, id)
	if err != nil {
		return WebHistory{}, err
	}
	index := s.repo.(LegacyIndex)
	messages, err := index.MessagesBefore(ctx, row.ShopID, id, before, limit+1)
	if err != nil {
		return WebHistory{}, err
	}
	out := WebHistory{Conversation: webConversation(row.Conversation), Items: []WebMessage{}, HasMore: len(messages) > limit}
	if out.HasMore {
		messages = messages[:limit]
	}
	for i := len(messages) - 1; i >= 0; i-- {
		m, err := webMessage(messages[i])
		if err != nil {
			return WebHistory{}, err
		}
		out.Items = append(out.Items, m)
	}
	return out, nil
}
func (s *Service) SendWeb(ctx context.Context, token string, actor int64, id, key, body string) (WebMessage, error) {
	body = strings.TrimSpace(body)
	if len(key) < 8 || len(key) > 64 || strings.ContainsAny(key, "\x00\r\n:") || !utf8.ValidString(body) || body == "" || utf8.RuneCountInString(body) > 4000 || strings.ContainsRune(body, 0) {
		return WebMessage{}, store.ErrInvalid
	}
	row, err := s.webConversation(ctx, token, actor, id)
	if err != nil {
		return WebMessage{}, err
	}
	data, err := s.Change(ctx, token, ConversationChange{ShopID: row.ShopID, ConversationID: id, Key: key, Action: "message", Body: body})
	if err != nil {
		return WebMessage{}, err
	}
	var message Message
	if json.Unmarshal(data, &message) != nil {
		return WebMessage{}, store.ErrUnavailable
	}
	return webMessage(message)
}
func (s *Service) MarkReadWeb(ctx context.Context, token string, actor int64, id string, through int64) error {
	if through <= 0 {
		return store.ErrInvalid
	}
	row, err := s.webConversation(ctx, token, actor, id)
	if err != nil {
		return err
	}
	return s.repo.Within(ctx, row.ShopID, func(tx Tx) error {
		checker, ok := tx.(interface {
			MessageExists(string, int64) (bool, error)
		})
		if !ok {
			return store.ErrUnavailable
		}
		exists, err := checker.MessageExists(id, through)
		if err != nil {
			return err
		}
		if !exists {
			return store.ErrInvalid
		}
		var c Conversation
		if err = tx.Get("conversations", id, &c); err != nil {
			return err
		}
		if actor == c.Buyer.ID {
			c.BuyerReadID = max(c.BuyerReadID, through)
		} else if actor == c.Seller.ID {
			c.SellerReadID = max(c.SellerReadID, through)
		} else {
			return store.ErrForbidden
		}
		return tx.Put("conversations", id, c)
	})
}
func (s *Service) StatusWeb(ctx context.Context, token string, actor int64, id, status string) error {
	if status != "open" && status != "resolved" {
		return store.ErrInvalid
	}
	row, err := s.webConversation(ctx, token, actor, id)
	if err != nil {
		return err
	}
	return s.repo.Within(ctx, row.ShopID, func(tx Tx) error {
		var c Conversation
		if err := tx.Get("conversations", id, &c); err != nil {
			return err
		}
		if actor != c.Buyer.ID && actor != c.Seller.ID {
			return store.ErrForbidden
		}
		c.Closed = status == "resolved"
		c.UpdatedAt = time.Now().UTC()
		return tx.Put("conversations", id, c)
	})
}
func (s *Service) AssistantContextWeb(ctx context.Context, token string, actor int64, id string) (map[string]any, error) {
	history, err := s.HistoryWeb(ctx, token, actor, id, 0, 10)
	if err != nil {
		return nil, err
	}
	role := "buyer"
	if actor == history.Conversation.SellerID {
		role = "seller"
	}
	messages := []map[string]string{}
	for _, m := range history.Items {
		r := "buyer"
		if m.SenderID == history.Conversation.SellerID {
			r = "seller"
		}
		text := []rune(m.Body)
		if len(text) > 500 {
			text = append(text[:500], '…')
		}
		messages = append(messages, map[string]string{"role": r, "text": string(text)})
	}
	out := map[string]any{"role": role, "conversation_id": id, "subject": history.Conversation.Subject, "messages": messages}
	if history.Conversation.OrderID != "" {
		dir, ok := s.directory.(catalogueDirectory)
		if !ok {
			return nil, store.ErrUnavailable
		}
		order, err := dir.LocateOrder(ctx, history.Conversation.OrderID)
		if err != nil {
			return nil, err
		}
		if order.Order.Buyer.ID != history.Conversation.BuyerID || order.Order.Seller.ID != history.Conversation.SellerID {
			return nil, store.ErrForbidden
		}
		out["order"] = map[string]any{"id": order.Order.ID, "status": order.Order.State, "quantity": order.Order.Quantity}
	}
	return out, nil
}

// Customer summaries need core-owned public profile names and credit-denominated
// order aggregates. Do not substitute external minor amounts or fabricated
// balances/names merely to make the old route return a successful empty list.
func (s *Service) ListCustomersWeb(ctx context.Context, token string, actor int64, q string, offset, limit int) ([]WebCustomer, error) {
	if err := s.webActor(ctx, token, actor); err != nil {
		return nil, err
	}
	return nil, store.ErrUnavailable
}
func (s *Service) SaveCustomerWeb(ctx context.Context, token string, actor, buyer int64, note string, tags []string, revision int64) error {
	if err := s.webActor(ctx, token, actor); err != nil {
		return err
	}
	return store.ErrUnavailable
}
