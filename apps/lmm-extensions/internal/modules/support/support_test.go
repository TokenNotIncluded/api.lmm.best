package support

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules/store"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// Test-only durable image. JSON copies prevent pointer aliasing from hiding
// rollback bugs. Production must inject Postgres, never this implementation.
type memoryRepo struct {
	mu         sync.Mutex
	data       map[string]map[string][]byte
	failCommit atomic.Bool
}
type memoryTx struct{ data map[string][]byte }

func newRepo() *memoryRepo { return &memoryRepo{data: map[string]map[string][]byte{}} }
func (m *memoryRepo) Within(ctx context.Context, scope string, fn func(Tx) error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	image := map[string][]byte{}
	for k, v := range m.data[scope] {
		image[k] = append([]byte(nil), v...)
	}
	if err := fn(&memoryTx{image}); err != nil {
		return err
	}
	if m.failCommit.Swap(false) {
		return errors.New("injected commit failure")
	}
	m.data[scope] = image
	return nil
}
func (t *memoryTx) Get(kind, id string, out any) error {
	b, ok := t.data[kind+"/"+id]
	if !ok {
		return store.ErrNotFound
	}
	return json.Unmarshal(b, out)
}
func (t *memoryTx) Put(kind, id string, v any) error {
	b, err := json.Marshal(v)
	if err == nil {
		t.data[kind+"/"+id] = b
	}
	return err
}
func (t *memoryTx) List(kind, prefix, after string, limit int) ([]json.RawMessage, error) {
	keys := []string{}
	for k := range t.data {
		if strings.HasPrefix(k, kind+"/") && strings.TrimPrefix(k, kind+"/") > after && strings.HasPrefix(strings.TrimPrefix(k, kind+"/"), prefix) {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	out := []json.RawMessage{}
	for _, k := range keys {
		if len(out) == limit {
			break
		}
		out = append(out, json.RawMessage(t.data[k]))
	}
	return out, nil
}

var sellerToken = strings.Repeat("s", 32)
var buyerToken = strings.Repeat("b", 32)
var otherToken = strings.Repeat("x", 32)

type authority struct {
	revoked atomic.Bool
	down    atomic.Bool
}

func (a *authority) Check(_ context.Context, token string, account store.Account, p store.Permission) (int64, error) {
	if a.down.Load() {
		return 0, store.ErrUnavailable
	}
	switch token {
	case sellerToken:
		if a.revoked.Load() {
			return 0, store.ErrForbidden
		}
		if account == (store.Account{Kind: "personal", ID: 1}) || account == (store.Account{Kind: "team", ID: 10}) {
			return 1, nil
		}
	case buyerToken:
		if account == (store.Account{Kind: "personal", ID: 2}) {
			return 2, nil
		}
	case otherToken:
		if account == (store.Account{Kind: "personal", ID: 3}) {
			return 3, nil
		}
		if account == (store.Account{Kind: "team", ID: 10}) && p == store.Read {
			return 3, nil
		}
	default:
		return 0, store.ErrUnauthorized
	}
	return 0, store.ErrForbidden
}

type directory struct {
	down bool
	team bool
}

func (d *directory) Owner(_ context.Context, shop string) (store.Account, error) {
	if d.down {
		return store.Account{}, store.ErrUnavailable
	}
	if shop != "shop" {
		return store.Account{}, store.ErrNotFound
	}
	if d.team {
		return store.Account{Kind: "team", ID: 10}, nil
	}
	return store.Account{Kind: "personal", ID: 1}, nil
}
func (d *directory) Parties(ctx context.Context, shop, order string) (store.Account, store.Account, error) {
	owner, err := d.Owner(ctx, shop)
	if err != nil {
		return store.Account{}, owner, err
	}
	if order != "order" {
		return store.Account{}, owner, store.ErrNotFound
	}
	return store.Account{Kind: "personal", ID: 2}, owner, nil
}
func fixture(t *testing.T, team bool) (*Service, *authority, *directory) {
	t.Helper()
	auth := &authority{}
	dir := &directory{team: team}
	s, err := New(newRepo(), auth, dir)
	must(t, err)
	return s, auth, dir
}
func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
func want(t *testing.T, err, expected error) {
	t.Helper()
	if !errors.Is(err, expected) {
		t.Fatalf("error=%v want=%v", err, expected)
	}
}
func open(t *testing.T, s *Service, buyer store.Account, token, key string) Conversation {
	t.Helper()
	c, err := s.Open(context.Background(), token, OpenRequest{"shop", buyer, "", "Question", key})
	must(t, err)
	return c
}

func TestCustomerPrivacyAndBuyerSellerMessages(t *testing.T) {
	s, _, _ := fixture(t, true)
	ctx := context.Background()
	c := open(t, s, store.Account{Kind: "personal", ID: 2}, buyerToken, "first")
	change := ConversationChange{"shop", c.ID, "buyer-message", "message", "Can you deliver today?"}
	data, err := s.Change(ctx, buyerToken, change)
	must(t, err)
	var m Message
	must(t, json.Unmarshal(data, &m))
	if m.AuthorUserID != 2 || m.AuthorAccount != c.Buyer {
		t.Fatal(m)
	}
	change.Key = "seller-reply"
	change.Body = "Yes."
	data, err = s.Change(ctx, sellerToken, change)
	must(t, err)
	must(t, json.Unmarshal(data, &m))
	if m.AuthorUserID != 1 || m.AuthorAccount != c.Seller {
		t.Fatal(m)
	}
	note := CustomerChange{"shop", c.CustomerID, "note", "PRIVATE_INTERNAL_NOTE", []string{"returning"}}
	_, err = s.UpdateCustomer(ctx, sellerToken, note)
	must(t, err)
	_, err = s.UpdateCustomer(ctx, buyerToken, note)
	want(t, err, store.ErrForbidden)
	thread, err := s.Thread(ctx, buyerToken, "shop", c.ID, "", 100)
	must(t, err)
	encoded, _ := json.Marshal(thread)
	if len(thread.Messages) != 2 || strings.Contains(string(encoded), "PRIVATE_INTERNAL_NOTE") {
		t.Fatal(string(encoded))
	}
	_, err = s.Thread(ctx, otherToken, "shop", c.ID, "", 100)
	want(t, err, store.ErrForbidden)
	customers, err := s.ManageList(ctx, sellerToken, "shop", "customers", "", 100)
	must(t, err)
	if len(customers.Items) != 1 || !strings.Contains(string(customers.Items[0]), "PRIVATE_INTERNAL_NOTE") {
		t.Fatal(customers)
	}
	_, err = s.ManageList(ctx, buyerToken, "shop", "customers", "", 100)
	want(t, err, store.ErrForbidden)
}
func TestConcurrentMessageReplayAndConversationIsolation(t *testing.T) {
	s, _, _ := fixture(t, false)
	ctx := context.Background()
	c := open(t, s, store.Account{Kind: "personal", ID: 2}, buyerToken, "first")
	other := open(t, s, store.Account{Kind: "personal", ID: 3}, otherToken, "other")
	r := ConversationChange{"shop", c.ID, "repeat", "message", "Once only"}
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.Change(ctx, buyerToken, r)
			if err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	thread, err := s.Thread(ctx, buyerToken, "shop", c.ID, "", 100)
	must(t, err)
	if len(thread.Messages) != 1 || thread.Conversation.Sequence != 1 {
		t.Fatal(thread)
	}
	r.Body = "changed payload"
	_, err = s.Change(ctx, buyerToken, r)
	want(t, err, store.ErrConflict)
	for i := 0; i < 12; i++ {
		r.Key = fmt.Sprint(i)
		r.Body = fmt.Sprint("message ", i)
		_, err = s.Change(ctx, buyerToken, r)
		must(t, err)
	}
	_, err = s.Change(ctx, otherToken, ConversationChange{"shop", other.ID, "secret", "message", "OTHER_CUSTOMER_SECRET"})
	must(t, err)
	after := ""
	total := 0
	for i := 0; i < 20; i++ {
		page, err := s.Thread(ctx, buyerToken, "shop", c.ID, after, 2)
		must(t, err)
		for _, m := range page.Messages {
			if m.ConversationID != c.ID || m.Body == "OTHER_CUSTOMER_SECRET" {
				t.Fatal(m)
			}
		}
		total += len(page.Messages)
		after = page.Next
		if after == "" {
			break
		}
	}
	if total != 13 {
		t.Fatalf("messages=%d", total)
	}
	_, err = s.Thread(ctx, buyerToken, "shop", c.ID, other.ID+":00000000000000000001", 10)
	want(t, err, store.ErrInvalid)
}
func TestCloseReopenAndPermissionRevocation(t *testing.T) {
	s, auth, _ := fixture(t, true)
	ctx := context.Background()
	c := open(t, s, store.Account{Kind: "personal", ID: 2}, buyerToken, "first")
	_, err := s.Change(ctx, sellerToken, ConversationChange{"shop", c.ID, "close", "close", ""})
	must(t, err)
	_, err = s.Change(ctx, buyerToken, ConversationChange{"shop", c.ID, "blocked", "message", "hello"})
	want(t, err, store.ErrConflict)
	_, err = s.Change(ctx, buyerToken, ConversationChange{"shop", c.ID, "reopen", "reopen", ""})
	must(t, err)
	auth.revoked.Store(true)
	_, err = s.Change(ctx, sellerToken, ConversationChange{"shop", c.ID, "close", "close", ""})
	want(t, err, store.ErrForbidden)
	auth.down.Store(true)
	_, err = s.Thread(ctx, buyerToken, "shop", c.ID, "", 10)
	want(t, err, store.ErrUnavailable)
}
func TestOrderLinkAndSellerInitiation(t *testing.T) {
	s, _, dir := fixture(t, false)
	ctx := context.Background()
	r := OpenRequest{"shop", store.Account{Kind: "personal", ID: 2}, "", "Question", "new"}
	_, err := s.Open(ctx, sellerToken, r)
	want(t, err, store.ErrForbidden)
	r.OrderID = "missing"
	_, err = s.Open(ctx, sellerToken, r)
	want(t, err, store.ErrNotFound)
	r.OrderID = "order"
	c, err := s.Open(ctx, sellerToken, r)
	must(t, err)
	repeat, err := s.Open(ctx, sellerToken, r)
	must(t, err)
	if c.ID != repeat.ID {
		t.Fatal("duplicate conversation")
	}
	r.Subject = "different"
	_, err = s.Open(ctx, sellerToken, r)
	want(t, err, store.ErrConflict)
	r.Buyer = store.Account{Kind: "personal", ID: 3}
	_, err = s.Open(ctx, sellerToken, r)
	want(t, err, store.ErrForbidden)
	// Existing conversations remain usable if store is down; auth still queries core.
	dir.down = true
	_, err = s.Change(ctx, buyerToken, ConversationChange{"shop", c.ID, "message", "message", "existing thread"})
	must(t, err)
	_, err = s.Open(ctx, buyerToken, OpenRequest{"shop", store.Account{Kind: "personal", ID: 2}, "", "New", "newer"})
	want(t, err, store.ErrUnavailable)
}
func TestSupportHTTPRejectsForgedAuthorAndLeaksNoNotes(t *testing.T) {
	s, _, _ := fixture(t, false)
	handler := s.Handler()
	r := httptest.NewRequest("POST", "/conversations/change", strings.NewReader(`{"author_user_id":1}`))
	r.Header.Set("X-LMM-User-Credential", buyerToken)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 400 {
		t.Fatal(w.Code)
	}
	r = httptest.NewRequest("GET", "/shops/shop/manage/customers?limit=1000", nil)
	r.Header.Set("X-LMM-User-Credential", sellerToken)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 400 {
		t.Fatal(w.Code)
	}
	if _, err := supportTable("lmm_store.orders"); !errors.Is(err, store.ErrInvalid) {
		t.Fatal(err)
	}
	if _, err := NewPostgres(nil); !errors.Is(err, store.ErrInvalid) {
		t.Fatal(err)
	}
	var nilRepo *Postgres
	if _, err := New(nilRepo, &authority{}, &directory{}); !errors.Is(err, store.ErrInvalid) {
		t.Fatal(err)
	}
}

func TestOrderCustomerProjectionPreservesPrivateNotes(t *testing.T) {
	s, _, _ := fixture(t, false)
	ctx := context.Background()
	c, err := s.CustomerFromOrder(ctx, "shop", "order")
	must(t, err)
	_, err = s.UpdateCustomer(ctx, sellerToken, CustomerChange{"shop", c.ID, "notes", "retain this note", []string{"customer"}})
	must(t, err)
	repeated, err := s.CustomerFromOrder(ctx, "shop", "order")
	must(t, err)
	if repeated.ID != c.ID || repeated.Notes != "retain this note" {
		t.Fatal(repeated)
	}
	_, err = s.CustomerFromOrder(ctx, "shop", "fake-order")
	want(t, err, store.ErrNotFound)
}
