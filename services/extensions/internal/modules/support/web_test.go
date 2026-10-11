package support

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"unicode/utf8"

	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules/store"
)

// This adds the lookup port to the existing rollback-capable memory image.
// It exercises the actual service, not PostgreSQL SQL or Rust RPC registration.
type webMemoryRepo struct {
	*memoryRepo
	messageReads atomic.Int64
}

func (m *webMemoryRepo) FindConversation(ctx context.Context, id string) (LocatedConversation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return LocatedConversation{}, err
	}
	var found LocatedConversation
	count := 0
	for shop, data := range m.data {
		if raw, ok := data["conversations/"+id]; ok {
			count++
			found.ShopID = shop
			if err := json.Unmarshal(raw, &found.Conversation); err != nil {
				return LocatedConversation{}, err
			}
		}
	}
	if count == 0 {
		return LocatedConversation{}, store.ErrNotFound
	}
	if count != 1 {
		return LocatedConversation{}, store.ErrConflict
	}
	return found, nil
}
func (*webMemoryRepo) ListForActor(context.Context, int64, string, string, bool, int, int) ([]LocatedConversation, error) {
	return nil, store.ErrUnavailable
}
func (m *webMemoryRepo) MessagesBefore(ctx context.Context, shop, id string, before int64, limit int) ([]Message, error) {
	m.messageReads.Add(1)
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	rows := []Message{}
	for key, raw := range m.data[shop] {
		if strings.HasPrefix(key, "messages/") {
			var message Message
			if err := json.Unmarshal(raw, &message); err != nil {
				return nil, err
			}
			if message.ConversationID == id && (before == 0 || message.NumericID < before) {
				rows = append(rows, message)
			}
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].NumericID > rows[j].NumericID })
	if len(rows) > limit {
		rows = rows[:limit]
	}
	return rows, nil
}

type webDirectory struct {
	*directory
	product              store.LocatedProduct
	order                store.LocatedOrder
	productErr, orderErr error
}

func (d *webDirectory) LocateProduct(_ context.Context, id string) (store.LocatedProduct, error) {
	if d.productErr != nil {
		return store.LocatedProduct{}, d.productErr
	}
	if id != d.product.Product.ID {
		return store.LocatedProduct{}, store.ErrNotFound
	}
	return d.product, nil
}
func (d *webDirectory) LocateOrder(_ context.Context, id string) (store.LocatedOrder, error) {
	if d.orderErr != nil {
		return store.LocatedOrder{}, d.orderErr
	}
	if id != d.order.Order.ID {
		return store.LocatedOrder{}, store.ErrNotFound
	}
	return d.order, nil
}

type webFixture struct {
	s    *Service
	repo *webMemoryRepo
	auth *authority
	dir  *webDirectory
}

func newWebFixture(t *testing.T) *webFixture {
	t.Helper()
	repo := &webMemoryRepo{memoryRepo: newRepo()}
	auth := &authority{}
	owner := store.Account{Kind: "personal", ID: 1}
	buyer := store.Account{Kind: "personal", ID: 2}
	dir := &webDirectory{directory: &directory{}, product: store.LocatedProduct{ShopID: "shop", Owner: owner, Product: store.Product{ID: "product", Title: "Persisted product title", Published: true}}, order: store.LocatedOrder{ShopID: "shop", ProductID: "product", Order: store.Order{ID: "order", Buyer: buyer, Seller: owner, ProductTitle: "Immutable order title", State: "paid", Quantity: 2}}}
	service, err := New(repo, auth, dir)
	must(t, err)
	return &webFixture{service, repo, auth, dir}
}
func (f *webFixture) open(t *testing.T) WebConversation {
	t.Helper()
	c, err := f.s.OpenWeb(context.Background(), buyerToken, 2, "product", "")
	must(t, err)
	return c
}

func TestOpenWebProductIsStableAndUsesStoredPartiesAndSubject(t *testing.T) {
	f := newWebFixture(t)
	ctx := context.Background()
	first := f.open(t)
	repeat, err := f.s.OpenWeb(ctx, buyerToken, 2, "product", "")
	must(t, err)
	if first.ID == "" || first.ID != repeat.ID || first.BuyerID != 2 || first.SellerID != 1 || first.ProductID != "product" || first.OrderID != "" || first.Subject != f.dir.product.Product.Title || first.Status != "open" || first.CreatedAt <= 0 {
		t.Fatalf("first=%+v repeat=%+v", first, repeat)
	}
	located, err := f.repo.FindConversation(ctx, first.ID)
	must(t, err)
	if located.ShopID != "shop" || located.Conversation.ProductID != "product" {
		t.Fatalf("persisted=%+v", located)
	}
	other, err := f.s.OpenWeb(ctx, otherToken, 3, "product", "")
	must(t, err)
	if other.ID == first.ID || other.BuyerID != 3 {
		t.Fatalf("inquiries shared private scope: first=%+v other=%+v", first, other)
	}
	_, err = f.s.OpenWeb(ctx, sellerToken, 1, "product", "")
	want(t, err, store.ErrInvalid)
}

func TestOpenWebOrderParticipantsShareOneConversation(t *testing.T) {
	for _, sellerFirst := range []bool{false, true} {
		t.Run(fmt.Sprint(sellerFirst), func(t *testing.T) {
			f := newWebFixture(t)
			ctx := context.Background()
			firstToken, secondToken := buyerToken, sellerToken
			firstActor, secondActor := int64(2), int64(1)
			if sellerFirst {
				firstToken, secondToken = secondToken, firstToken
				firstActor, secondActor = secondActor, firstActor
			}
			first, err := f.s.OpenWeb(ctx, firstToken, firstActor, "", "order")
			must(t, err)
			second, err := f.s.OpenWeb(ctx, secondToken, secondActor, "", "order")
			must(t, err)
			if first.ID != second.ID || first.BuyerID != 2 || first.SellerID != 1 || first.OrderID != "order" || first.ProductID != "product" || first.Subject != "Immutable order title" {
				t.Fatalf("first=%+v second=%+v", first, second)
			}
			_, err = f.s.OpenWeb(ctx, otherToken, 3, "", "order")
			want(t, err, store.ErrForbidden)
		})
	}
}

func TestOpenWebRejectsPrivateProductsForgedActorsAndUnavailableDirectory(t *testing.T) {
	for _, tc := range []struct {
		name, token, product, order string
		actor                       int64
		edit                        func(*webFixture)
		want                        error
	}{
		{"no-scope", buyerToken, "", "", 2, nil, store.ErrInvalid},
		{"two-scopes", buyerToken, "product", "order", 2, nil, store.ErrInvalid},
		{"forged-actor", sellerToken, "product", "", 2, nil, store.ErrForbidden},
		{"unknown-session", "unknown", "product", "", 2, nil, store.ErrUnauthorized},
		{"missing-product", buyerToken, "missing", "", 2, nil, store.ErrNotFound},
		{"unpublished", buyerToken, "product", "", 2, func(f *webFixture) { f.dir.product.Product.Published = false }, store.ErrNotFound},
		{"private", buyerToken, "product", "", 2, func(f *webFixture) { f.dir.product.Product.LegacyPublic = json.RawMessage(`{"visibility":"private"}`) }, store.ErrNotFound},
		{"null-visibility", buyerToken, "product", "", 2, func(f *webFixture) { f.dir.product.Product.LegacyPublic = json.RawMessage(`{"visibility":null}`) }, store.ErrUnavailable},
		{"invalid-snapshot", buyerToken, "product", "", 2, func(f *webFixture) { f.dir.product.Product.LegacyPublic = json.RawMessage(`{"visibility":`) }, store.ErrUnavailable},
		{"team-product", buyerToken, "product", "", 2, func(f *webFixture) { f.dir.product.Owner = store.Account{Kind: "team", ID: 10} }, store.ErrInvalid},
		{"team-order-buyer", sellerToken, "", "order", 1, func(f *webFixture) { f.dir.order.Order.Buyer = store.Account{Kind: "team", ID: 10} }, store.ErrForbidden},
		{"team-order-seller", buyerToken, "", "order", 2, func(f *webFixture) { f.dir.order.Order.Seller = store.Account{Kind: "team", ID: 10} }, store.ErrForbidden},
		{"directory-down", buyerToken, "product", "", 2, func(f *webFixture) { f.dir.productErr = store.ErrUnavailable }, store.ErrUnavailable},
		{"core-down", buyerToken, "product", "", 2, func(f *webFixture) { f.auth.down.Store(true) }, store.ErrUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newWebFixture(t)
			if tc.edit != nil {
				tc.edit(f)
			}
			_, err := f.s.OpenWeb(context.Background(), tc.token, tc.actor, tc.product, tc.order)
			want(t, err, tc.want)
			f.repo.mu.Lock()
			defer f.repo.mu.Unlock()
			if len(f.repo.data) != 0 {
				t.Fatalf("rejected opening wrote records: %v", f.repo.data)
			}
		})
	}
	f := newWebFixture(t)
	service, err := New(newRepo(), f.auth, &directory{})
	must(t, err)
	_, err = service.OpenWeb(context.Background(), buyerToken, 2, "product", "")
	want(t, err, store.ErrUnavailable)
}

func TestHistoryWebReturnsNewestPageInAscendingOrderAndHidesOtherCustomers(t *testing.T) {
	f := newWebFixture(t)
	ctx := context.Background()
	c := f.open(t)
	for i := 1; i <= 5; i++ {
		token, actor := buyerToken, int64(2)
		if i%2 == 0 {
			token, actor = sellerToken, 1
		}
		message, err := f.s.SendWeb(ctx, token, actor, c.ID, fmt.Sprintf("message-%d", i), fmt.Sprintf("body %d", i))
		must(t, err)
		if message.ID != int64(i) || message.SenderID != actor {
			t.Fatal(message)
		}
	}
	_, err := f.s.UpdateCustomer(ctx, sellerToken, CustomerChange{ShopID: "shop", CustomerID: store.ID("customer", store.Account{Kind: "personal", ID: 2}), Key: "private-note", Notes: "PRIVATE_CUSTOMER_NOTE"})
	must(t, err)
	other, err := f.s.OpenWeb(ctx, otherToken, 3, "product", "")
	must(t, err)
	_, err = f.s.SendWeb(ctx, otherToken, 3, other.ID, "other-msg-01", "OTHER_CUSTOMER_SECRET")
	must(t, err)
	for _, tc := range []struct {
		before int64
		limit  int
		ids    []int64
		more   bool
	}{{0, 2, []int64{4, 5}, true}, {4, 2, []int64{2, 3}, true}, {2, 2, []int64{1}, false}, {1, 2, []int64{}, false}} {
		page, err := f.s.HistoryWeb(ctx, buyerToken, 2, c.ID, tc.before, tc.limit)
		must(t, err)
		ids := []int64{}
		for _, message := range page.Items {
			ids = append(ids, message.ID)
			if message.ConversationID != c.ID {
				t.Fatal(message)
			}
		}
		if !reflect.DeepEqual(ids, tc.ids) || page.HasMore != tc.more || page.Conversation.ID != c.ID || page.Items == nil {
			t.Fatalf("page=%+v ids=%v expected=%v", page, ids, tc.ids)
		}
		encoded, err := json.Marshal(page)
		must(t, err)
		for _, private := range []string{"PRIVATE_CUSTOMER_NOTE", "OTHER_CUSTOMER_SECRET", "request_hash", "author_account", "customer_id"} {
			if strings.Contains(string(encoded), private) {
				t.Fatalf("private value leaked: %s", encoded)
			}
		}
	}
}

func TestWebConversationRequiresTheActualPersonalParticipantOnEveryRead(t *testing.T) {
	f := newWebFixture(t)
	ctx := context.Background()
	c := f.open(t)
	for _, tc := range []struct {
		token string
		actor int64
		want  error
	}{{otherToken, 3, store.ErrForbidden}, {buyerToken, 1, store.ErrForbidden}, {sellerToken, 2, store.ErrForbidden}, {"unknown", 2, store.ErrUnauthorized}} {
		reads := f.repo.messageReads.Load()
		_, err := f.s.HistoryWeb(ctx, tc.token, tc.actor, c.ID, 0, 50)
		want(t, err, tc.want)
		if f.repo.messageReads.Load() != reads {
			t.Fatal("unauthorized message lookup")
		}
	}
	_, err := f.s.HistoryWeb(ctx, sellerToken, 1, c.ID, 0, 50)
	must(t, err)
	f.auth.revoked.Store(true)
	_, err = f.s.HistoryWeb(ctx, sellerToken, 1, c.ID, 0, 50)
	want(t, err, store.ErrForbidden)
	f.auth.revoked.Store(false)
	f.auth.down.Store(true)
	_, err = f.s.HistoryWeb(ctx, buyerToken, 2, c.ID, 0, 50)
	want(t, err, store.ErrUnavailable)
	f.auth.down.Store(false)
	for _, tc := range []struct {
		before int64
		limit  int
	}{{-1, 50}, {0, 0}, {0, 101}} {
		_, err = f.s.HistoryWeb(ctx, buyerToken, 2, c.ID, tc.before, tc.limit)
		want(t, err, store.ErrInvalid)
	}
	must(t, f.repo.Within(ctx, "shop", func(tx Tx) error {
		var stored Conversation
		if err := tx.Get("conversations", c.ID, &stored); err != nil {
			return err
		}
		stored.Seller = store.Account{Kind: "team", ID: 10}
		return tx.Put("conversations", c.ID, stored)
	}))
	_, err = f.s.HistoryWeb(ctx, buyerToken, 2, c.ID, 0, 50)
	want(t, err, store.ErrForbidden)
}

func TestParticipantUsesBuyerReadOrSellerManageWithoutWeakFallback(t *testing.T) {
	service, auth, _ := fixture(t, true)
	ctx := context.Background()
	c := open(t, service, store.Account{Kind: "personal", ID: 2}, buyerToken, "participant")
	actor, party, err := service.participant(ctx, buyerToken, c)
	must(t, err)
	if actor != 2 || party != c.Buyer {
		t.Fatal(actor, party)
	}
	actor, party, err = service.participant(ctx, sellerToken, c)
	must(t, err)
	if actor != 1 || party != c.Seller {
		t.Fatal(actor, party)
	}
	_, _, err = service.participant(ctx, otherToken, c)
	want(t, err, store.ErrForbidden)
	auth.down.Store(true)
	_, _, err = service.participant(ctx, buyerToken, c)
	want(t, err, store.ErrUnavailable)
}

func TestSendWebAcceptsExactly4000UnicodeCharactersAndKeepsReplayStable(t *testing.T) {
	for _, character := range []string{"界", "😀"} {
		t.Run(character, func(t *testing.T) {
			f := newWebFixture(t)
			ctx := context.Background()
			c := f.open(t)
			body := strings.Repeat(character, 4000)
			if utf8.RuneCountInString(body) != 4000 {
				t.Fatal("invalid Unicode fixture")
			}
			first, err := f.s.SendWeb(ctx, buyerToken, 2, c.ID, "unicode-0001", "  "+body+"  ")
			must(t, err)
			repeat, err := f.s.SendWeb(ctx, buyerToken, 2, c.ID, "unicode-0001", body)
			must(t, err)
			if first.ID <= 0 || first.ID != repeat.ID || first.Body != body || repeat.Body != body || first.SenderID != 2 {
				t.Fatalf("message identity=%d/%d bytes=%d", first.ID, repeat.ID, len(first.Body))
			}
			_, err = f.s.SendWeb(ctx, buyerToken, 2, c.ID, "unicode-0001", "different payload")
			want(t, err, store.ErrConflict)
			_, err = f.s.SendWeb(ctx, buyerToken, 2, c.ID, "unicode-0002", body+character)
			want(t, err, store.ErrInvalid)
			for _, invalid := range []string{" ", "a\x00b", string([]byte{0xff})} {
				_, err = f.s.SendWeb(ctx, buyerToken, 2, c.ID, "invalid-0001", invalid)
				want(t, err, store.ErrInvalid)
			}
			for _, key := range []string{"short", strings.Repeat("k", 65), "colon:key", "newline\nkey"} {
				_, err = f.s.SendWeb(ctx, buyerToken, 2, c.ID, key, "hello")
				want(t, err, store.ErrInvalid)
			}
			_, err = f.s.SendWeb(ctx, otherToken, 3, c.ID, "forged-0001", "forged author")
			want(t, err, store.ErrForbidden)
			history, err := f.s.HistoryWeb(ctx, buyerToken, 2, c.ID, 0, 100)
			must(t, err)
			if len(history.Items) != 1 || history.Items[0].Body != body || history.Items[0].ID != first.ID || history.Conversation.LastMessageID != first.ID {
				t.Fatalf("history items=%d last=%d", len(history.Items), history.Conversation.LastMessageID)
			}
		})
	}
}

func TestWebMessageConversionRejectsMissingOrMalformedNumericIdentity(t *testing.T) {
	for _, id := range []string{"no-sequence", "c:not-number", "c:0", "c:-1", "c:9223372036854775808"} {
		_, err := webMessage(Message{ID: id})
		want(t, err, store.ErrUnavailable)
	}
	converted, err := webMessage(Message{ID: "c:00000000000000000042", ConversationID: "c", AuthorUserID: 2, Body: "hello"})
	must(t, err)
	if converted.ID != 42 || converted.ConversationID != "c" || converted.SenderID != 2 || converted.Body != "hello" {
		t.Fatal(converted)
	}
}

var _ LegacyIndex = (*webMemoryRepo)(nil)
var _ catalogueDirectory = (*webDirectory)(nil)
