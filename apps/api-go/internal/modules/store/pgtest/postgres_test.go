// This isolated test module keeps the database driver out of the extension
// host's public dependency files. Run only against a new disposable *_test DB.
package pgtest

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules/store"
	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules/support"
	_ "github.com/lib/pq"
)

type authority struct{}

func (authority) Check(_ context.Context, token string, a store.Account, _ store.Permission) (int64, error) {
	if token == "seller" && a == (store.Account{Kind: "personal", ID: 1}) {
		return 1, nil
	}
	if token == "buyer" && a == (store.Account{Kind: "personal", ID: 2}) {
		return 2, nil
	}
	return 0, store.ErrForbidden
}

type funds struct {
	mu      sync.Mutex
	seen    map[string]store.Receipt
	effects int
}

func (*funds) Available() bool { return true }
func (f *funds) Execute(_ context.Context, _ string, c store.MoneyCommand) (store.Receipt, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r, ok := f.seen[c.Key]; ok {
		if r.CommandHash != store.ID(c) {
			return store.Receipt{}, store.ErrConflict
		}
		return r, nil
	}
	r := store.Receipt{Key: c.Key, CommandHash: store.ID(c), Reference: "test:" + c.Key, Outcome: "succeeded"}
	f.seen[c.Key] = r
	f.effects++
	return r, nil
}
func TestPostgresAcceptance(t *testing.T) {
	dsn := os.Getenv("LMM_STORE_TEST_DSN")
	if dsn == "" {
		t.Skip("set LMM_STORE_TEST_DSN to a new disposable database ending in _test")
	}
	db, err := sql.Open("postgres", dsn)
	must(t, err)
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(8)
	ctx := context.Background()
	var name string
	must(t, db.QueryRowContext(ctx, "SELECT current_database()").Scan(&name))
	if !strings.HasSuffix(name, "_test") {
		t.Fatal("refusing a database whose name does not end in _test")
	}
	var exists bool
	must(t, db.QueryRowContext(ctx, "SELECT EXISTS(SELECT FROM pg_namespace WHERE nspname IN ('lmm_store','lmm_support'))").Scan(&exists))
	if exists {
		t.Fatal("use a fresh database without commerce schemas")
	}
	_, err = db.ExecContext(ctx, store.Schema)
	must(t, err)
	t.Cleanup(func() {
		_, _ = db.ExecContext(context.Background(), "DROP SCHEMA lmm_support CASCADE; DROP SCHEMA lmm_store CASCADE")
	})
	_, err = db.ExecContext(ctx, support.Schema)
	must(t, err)
	db2, err := sql.Open("postgres", dsn)
	must(t, err)
	defer db2.Close()
	db2.SetMaxOpenConns(8)
	repo1, _ := store.NewPostgres(db)
	repo2, _ := store.NewPostgres(db2)
	ledger := &funds{seen: map[string]store.Receipt{}}
	a, _ := store.New(repo1, authority{}, ledger)
	b, _ := store.New(repo2, authority{}, ledger)
	change := func(c store.CatalogChange, out any) {
		t.Helper()
		data, err := a.Catalog(ctx, "seller", c)
		must(t, err)
		must(t, json.Unmarshal(data, out))
	}
	var shop store.Shop
	change(store.CatalogChange{Action: "create_shop", Owner: store.Account{Kind: "personal", ID: 1}, Title: "Database test", Key: "shop"}, &shop)
	var product store.Product
	change(store.CatalogChange{Action: "create_product", ShopID: shop.ID, Title: "Product", Key: "product"}, &product)
	var v store.Variant
	change(store.CatalogChange{Action: "create_variant", ShopID: shop.ID, ProductID: product.ID, Title: "Variant", Price: store.Price{Amount: 100, Currency: "CNY"}, TrackStock: true, Quota: 5, Key: "variant"}, &v)
	change(store.CatalogChange{Action: "stock_adjust", ShopID: shop.ID, ID: v.ID, Delta: 8, Reason: "received", Key: "stock"}, &v)
	change(store.CatalogChange{Action: "product_status", ShopID: shop.ID, ID: product.ID, Published: true, Key: "publish"}, &product)
	var wg sync.WaitGroup
	var accepted atomic.Int64
	var orderMu sync.Mutex
	orders := []store.Order{}
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s := a
			if i%2 == 0 {
				s = b
			}
			o, err := s.Purchase(ctx, "buyer", store.Purchase{ShopID: shop.ID, VariantID: v.ID, Buyer: store.Account{Kind: "personal", ID: 2}, Quantity: 1, Key: fmt.Sprint(i)})
			if err == nil {
				accepted.Add(1)
				orderMu.Lock()
				orders = append(orders, o)
				orderMu.Unlock()
			} else if !errors.Is(err, store.ErrSoldOut) {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	if accepted.Load() != 5 {
		t.Fatalf("concurrent allocations=%d", accepted.Load())
	}
	order := orders[0]
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s := a
			if i%2 == 0 {
				s = b
			}
			_, err := s.ChangeOrder(ctx, "buyer", store.OrderChange{ShopID: shop.ID, OrderID: order.ID, Action: "pay"})
			if err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	if ledger.effects != 1 {
		t.Fatalf("duplicate money effects=%d", ledger.effects)
	}
	// New service instance, two pools and the same durable order/operation records.
	restarted, _ := store.New(repo2, authority{}, ledger)
	_, err = restarted.ChangeOrder(ctx, "buyer", store.OrderChange{ShopID: shop.ID, OrderID: order.ID, Action: "pay"})
	must(t, err)
	_, err = db.ExecContext(ctx, "UPDATE lmm_store.variants SET body=jsonb_set(body,'{stock}','-1') WHERE shop_id=$1 AND id=$2", shop.ID, v.ID)
	if err == nil {
		t.Fatal("negative inventory passed database constraint")
	}
	helpRepo, _ := support.NewPostgres(db2)
	help, _ := support.New(helpRepo, authority{}, restarted)
	customer, err := help.CustomerFromOrder(ctx, shop.ID, order.ID)
	must(t, err)
	room, err := help.Open(ctx, "buyer", support.OpenRequest{ShopID: shop.ID, Buyer: order.Buyer, OrderID: order.ID, Subject: "Question", Key: "conversation"})
	must(t, err)
	_, err = help.Change(ctx, "buyer", support.ConversationChange{ShopID: shop.ID, ConversationID: room.ID, Key: "message", Action: "message", Body: "Hello"})
	must(t, err)
	_, err = help.UpdateCustomer(ctx, "seller", support.CustomerChange{ShopID: shop.ID, CustomerID: customer.ID, Key: "note", Notes: "private"})
	must(t, err)
	thread, err := help.Thread(ctx, "buyer", shop.ID, room.ID, "", 10)
	must(t, err)
	if len(thread.Messages) != 1 {
		t.Fatal(thread)
	}
	raw, _ := json.Marshal(thread)
	if strings.Contains(string(raw), "private") {
		t.Fatal("private notes leaked")
	}
}
func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
