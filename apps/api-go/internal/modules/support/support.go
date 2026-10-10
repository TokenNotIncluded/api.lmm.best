// Package support owns customer notes, conversations and messages. It never
// queries store tables or core tables; all cross-module reads use Directory.
package support

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules/store"
	"math"
	"strings"
	"time"
)

type Directory interface {
	Owner(context.Context, string) (store.Account, error)
	Parties(context.Context, string, string) (store.Account, store.Account, error)
}
type Repository interface {
	Within(context.Context, string, func(Tx) error) error
}
type Tx interface {
	Get(string, string, any) error
	Put(string, string, any) error
	List(string, string, string, int) ([]json.RawMessage, error)
}
type Service struct {
	repo      Repository
	auth      store.Authority
	directory Directory
}

func New(repo Repository, auth store.Authority, directory Directory) (*Service, error) {
	if store.Missing(repo) || store.Missing(auth) || store.Missing(directory) {
		return nil, store.ErrInvalid
	}
	return &Service{repo, auth, directory}, nil
}

type Customer struct {
	ID        string        `json:"id"`
	Buyer     store.Account `json:"buyer"`
	Notes     string        `json:"notes"`
	Tags      []string      `json:"tags"`
	CreatedAt time.Time     `json:"created_at"`
}
type Conversation struct {
	ID          string        `json:"id"`
	Buyer       store.Account `json:"buyer"`
	Seller      store.Account `json:"seller"`
	CustomerID  string        `json:"customer_id"`
	OrderID     string        `json:"order_id,omitempty"`
	Subject     string        `json:"subject"`
	Closed      bool          `json:"closed"`
	Sequence    int64         `json:"sequence"`
	RequestHash string        `json:"request_hash"`
	CreatedAt   time.Time     `json:"created_at"`
}
type Message struct {
	ID             string        `json:"id"`
	ConversationID string        `json:"conversation_id"`
	AuthorUserID   int64         `json:"author_user_id"`
	AuthorAccount  store.Account `json:"author_account"`
	Body           string        `json:"body"`
	CreatedAt      time.Time     `json:"created_at"`
}
type record struct {
	Hash   string          `json:"hash"`
	Result json.RawMessage `json:"result"`
}

func (s *Service) check(ctx context.Context, token string, a store.Account, p store.Permission) (int64, error) {
	if !a.Valid() {
		return 0, store.ErrInvalid
	}
	id, err := s.auth.Check(ctx, token, a, p)
	if err != nil {
		return 0, err
	}
	if id <= 0 {
		return 0, store.ErrForbidden
	}
	return id, nil
}
func (s *Service) participant(ctx context.Context, token string, c Conversation) (int64, store.Account, error) {
	id, err := s.check(ctx, token, c.Buyer, store.Read)
	if err == nil {
		return id, c.Buyer, nil
	}
	if !errors.Is(err, store.ErrForbidden) {
		return 0, store.Account{}, err
	}
	id, err = s.check(ctx, token, c.Seller, store.Manage)
	return id, c.Seller, err
}

type OpenRequest struct {
	ShopID  string        `json:"shop_id"`
	Buyer   store.Account `json:"buyer"`
	OrderID string        `json:"order_id"`
	Subject string        `json:"subject"`
	Key     string        `json:"key"`
}

func (s *Service) Open(ctx context.Context, token string, r OpenRequest) (Conversation, error) {
	if !r.Buyer.Valid() || !store.Text(r.Subject, 200) || !store.Key(r.Key) {
		return Conversation{}, store.ErrInvalid
	}
	owner, err := s.directory.Owner(ctx, r.ShopID)
	if err != nil {
		return Conversation{}, err
	}
	if owner == r.Buyer {
		return Conversation{}, store.ErrForbidden
	}
	c := Conversation{Buyer: r.Buyer, Seller: owner}
	actor, party, err := s.participant(ctx, token, c)
	if err != nil {
		return Conversation{}, err
	}
	// Seller-initiated conversations require an actual order relationship.
	if party == owner && r.OrderID == "" {
		return Conversation{}, store.ErrForbidden
	}
	if r.OrderID != "" {
		buyer, seller, err := s.directory.Parties(ctx, r.ShopID, r.OrderID)
		if err != nil {
			return Conversation{}, err
		}
		if buyer != r.Buyer || seller != owner {
			return Conversation{}, store.ErrForbidden
		}
	}
	id := store.ID("conversation", actor, r.Buyer, r.Key)
	hash := store.ID(r)
	err = s.repo.Within(ctx, r.ShopID, func(tx Tx) error {
		if err := tx.Get("conversations", id, &c); err == nil {
			if c.RequestHash != hash {
				return store.ErrConflict
			}
			return nil
		} else if !errors.Is(err, store.ErrNotFound) {
			return err
		}
		customerID := store.ID("customer", r.Buyer)
		var customer Customer
		if err := tx.Get("customers", customerID, &customer); errors.Is(err, store.ErrNotFound) {
			customer = Customer{ID: customerID, Buyer: r.Buyer, Tags: []string{}, CreatedAt: time.Now().UTC()}
			if err = tx.Put("customers", customerID, customer); err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
		c = Conversation{ID: id, Buyer: r.Buyer, Seller: owner, CustomerID: customerID, OrderID: r.OrderID, Subject: r.Subject, RequestHash: hash, CreatedAt: time.Now().UTC()}
		return tx.Put("conversations", id, c)
	})
	return c, err
}

type ConversationChange struct {
	ShopID         string `json:"shop_id"`
	ConversationID string `json:"conversation_id"`
	Key            string `json:"key"`
	Action         string `json:"action"`
	Body           string `json:"body"`
}

func (s *Service) Change(ctx context.Context, token string, r ConversationChange) (json.RawMessage, error) {
	if !store.Key(r.Key) {
		return nil, store.ErrInvalid
	}
	var c Conversation
	if err := s.repo.Within(ctx, r.ShopID, func(tx Tx) error { return tx.Get("conversations", r.ConversationID, &c) }); err != nil {
		return nil, err
	}
	actor, party, err := s.participant(ctx, token, c)
	if err != nil {
		return nil, err
	}
	key := store.ID("conversation-change", actor, r.ConversationID, r.Key)
	hash := store.ID(r)
	var out json.RawMessage
	err = s.repo.Within(ctx, r.ShopID, func(tx Tx) error {
		var old record
		if err := tx.Get("commands", key, &old); err == nil {
			if old.Hash != hash {
				return store.ErrConflict
			}
			out = old.Result
			return nil
		} else if !errors.Is(err, store.ErrNotFound) {
			return err
		}
		if err := tx.Get("conversations", r.ConversationID, &c); err != nil {
			return err
		}
		var value any
		switch r.Action {
		case "message":
			if !store.Text(r.Body, 8192) {
				return store.ErrInvalid
			}
			if c.Closed {
				return store.ErrConflict
			}
			if c.Sequence == math.MaxInt64 {
				return store.ErrConflict
			}
			c.Sequence++
			message := Message{ID: fmt.Sprintf("%s:%020d", c.ID, c.Sequence), ConversationID: c.ID, AuthorUserID: actor, AuthorAccount: party, Body: r.Body, CreatedAt: time.Now().UTC()}
			if err := tx.Put("messages", message.ID, message); err != nil {
				return err
			}
			value = message
		case "close":
			c.Closed = true
			value = c
		case "reopen":
			c.Closed = false
			value = c
		default:
			return store.ErrInvalid
		}
		if err := tx.Put("conversations", c.ID, c); err != nil {
			return err
		}
		data, err := json.Marshal(value)
		if err != nil {
			return err
		}
		out = data
		return tx.Put("commands", key, record{hash, out})
	})
	return out, err
}

type Thread struct {
	Conversation Conversation `json:"conversation"`
	Messages     []Message    `json:"messages"`
	Next         string       `json:"next"`
}

func (s *Service) Thread(ctx context.Context, token, shop, conversation, after string, limit int) (Thread, error) {
	out := Thread{Messages: []Message{}}
	prefix := conversation + ":"
	if !store.Page(limit) || (after != "" && !strings.HasPrefix(after, prefix)) {
		return out, store.ErrInvalid
	}
	if err := s.repo.Within(ctx, shop, func(tx Tx) error { return tx.Get("conversations", conversation, &out.Conversation) }); err != nil {
		return out, err
	}
	if _, _, err := s.participant(ctx, token, out.Conversation); err != nil {
		return Thread{}, err
	}
	err := s.repo.Within(ctx, shop, func(tx Tx) error {
		if err := tx.Get("conversations", conversation, &out.Conversation); err != nil {
			return err
		}
		rows, err := tx.List("messages", prefix, after, limit)
		if err != nil {
			return err
		}
		for _, b := range rows {
			var m Message
			if err = json.Unmarshal(b, &m); err != nil {
				return err
			}
			out.Messages = append(out.Messages, m)
			out.Next = m.ID
		}
		if len(rows) < limit {
			out.Next = ""
		}
		return nil
	})
	return out, err
}

type CustomerChange struct {
	ShopID     string   `json:"shop_id"`
	CustomerID string   `json:"customer_id"`
	Key        string   `json:"key"`
	Notes      string   `json:"notes"`
	Tags       []string `json:"tags"`
}

func (s *Service) UpdateCustomer(ctx context.Context, token string, r CustomerChange) (Customer, error) {
	if !store.Key(r.Key) || len(r.Notes) > 4000 || strings.ContainsRune(r.Notes, 0) || len(r.Tags) > 10 {
		return Customer{}, store.ErrInvalid
	}
	for _, tag := range r.Tags {
		if !store.Text(tag, 64) {
			return Customer{}, store.ErrInvalid
		}
	}
	owner, err := s.directory.Owner(ctx, r.ShopID)
	if err != nil {
		return Customer{}, err
	}
	actor, err := s.check(ctx, token, owner, store.Manage)
	if err != nil {
		return Customer{}, err
	}
	var out Customer
	key := store.ID("customer-change", actor, r.Key)
	hash := store.ID(r)
	err = s.repo.Within(ctx, r.ShopID, func(tx Tx) error {
		var old record
		if err := tx.Get("commands", key, &old); err == nil {
			if old.Hash != hash {
				return store.ErrConflict
			}
			return json.Unmarshal(old.Result, &out)
		} else if !errors.Is(err, store.ErrNotFound) {
			return err
		}
		if err := tx.Get("customers", r.CustomerID, &out); err != nil {
			return err
		}
		out.Notes = r.Notes
		out.Tags = append([]string{}, r.Tags...)
		if err := tx.Put("customers", out.ID, out); err != nil {
			return err
		}
		data, err := json.Marshal(out)
		if err != nil {
			return err
		}
		return tx.Put("commands", key, record{hash, data})
	})
	return out, err
}
func (s *Service) ManageList(ctx context.Context, token, shop, kind, after string, limit int) (store.Listing[json.RawMessage], error) {
	out := store.Listing[json.RawMessage]{Items: []json.RawMessage{}}
	if !store.Page(limit) || (kind != "customers" && kind != "conversations") {
		return out, store.ErrInvalid
	}
	owner, err := s.directory.Owner(ctx, shop)
	if err != nil {
		return out, err
	}
	if _, err = s.check(ctx, token, owner, store.Manage); err != nil {
		return out, err
	}
	err = s.repo.Within(ctx, shop, func(tx Tx) error {
		var err error
		out.Items, err = tx.List(kind, "", after, limit)
		if err != nil {
			return err
		}
		if len(out.Items) == limit {
			var last struct {
				ID string `json:"id"`
			}
			if err = json.Unmarshal(out.Items[len(out.Items)-1], &last); err != nil {
				return err
			}
			out.Next = last.ID
		}
		return nil
	})
	return out, err
}

// CustomerFromOrder is an idempotent in-process event-consumer port, not an HTTP
// endpoint. Resolve parties from the store; never trust buyer IDs in an event.
// Replays do not overwrite private notes or tags. No user session is stored.
func (s *Service) CustomerFromOrder(ctx context.Context, shop, orderID string) (Customer, error) {
	buyer, seller, err := s.directory.Parties(ctx, shop, orderID)
	if err != nil {
		return Customer{}, err
	}
	owner, err := s.directory.Owner(ctx, shop)
	if err != nil {
		return Customer{}, err
	}
	if !buyer.Valid() || seller != owner || buyer == seller {
		return Customer{}, store.ErrConflict
	}
	id := store.ID("customer", buyer)
	var out Customer
	err = s.repo.Within(ctx, shop, func(tx Tx) error {
		if err := tx.Get("customers", id, &out); err == nil {
			return nil
		} else if !errors.Is(err, store.ErrNotFound) {
			return err
		}
		out = Customer{ID: id, Buyer: buyer, Tags: []string{}, CreatedAt: time.Now().UTC()}
		return tx.Put("customers", id, out)
	})
	return out, err
}
