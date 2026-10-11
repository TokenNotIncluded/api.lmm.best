// Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later.
package composition

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	pb "github.com/TokenNotIncluded/api.lmm.best/extensions/internal/corepb"
	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules"
	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules/store"
	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/webcompat"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// This is an actual PostgreSQL + protobuf/UDS wire test. Its authority server
// is a synthetic fixture, not a substitute for lmm-core identity acceptance.
type fixtureAuthority struct {
	pb.UnimplementedCoreControlServer
	calls        atomic.Int64
	serviceToken string
	users        []int64
}

func (f *fixtureAuthority) Authorize(ctx context.Context, _ *pb.AuthorizeRequest) (*pb.AuthorizeResponse, error) {
	f.calls.Add(1)
	m, _ := metadata.FromIncomingContext(ctx)
	if len(m.Get("authorization")) != 1 || m.Get("authorization")[0] != "Bearer "+f.serviceToken {
		return nil, status.Error(codes.Unauthenticated, "service")
	}
	credential := m.Get("x-lmm-user-credential")
	if len(credential) != 1 {
		return nil, status.Error(codes.Unauthenticated, "user")
	}
	id := int64(0)
	for _, n := range f.users {
		if credential[0] == fixtureCredential(n) {
			id = n
		}
	}
	if id == 0 {
		return nil, status.Error(codes.Unauthenticated, "user")
	}
	return &pb.AuthorizeResponse{UserId: id, CredentialKind: pb.CredentialKind_CREDENTIAL_KIND_SESSION, Owner: &pb.Account{Kind: pb.AccountKind_ACCOUNT_KIND_PERSONAL, Id: id}}, nil
}
func fixtureCredential(id int64) string {
	return fmt.Sprintf("lmms_synthetic_credential_for_user_%d", id)
}

func TestPostgresWebCompatibility(t *testing.T) {
	root := os.Getenv("LMM_EXTENSION_ASSEMBLY_TEST_DIR")
	if root == "" {
		t.Skip("requires owned synthetic PostgreSQL databases; see test receipt")
	}
	ctx := context.Background()
	suffix := fmt.Sprint(time.Now().UnixNano())
	shopID, productID, variantID, orderID := "shop-"+suffix, "product-"+suffix, "variant-"+suffix, "order-"+suffix
	storeCfg := StorageConfig{"store", filepath.Join(root, "store.url"), "lmm_store_assembly_test"}
	supportCfg := StorageConfig{"support", filepath.Join(root, "support.url"), "lmm_support_assembly_test"}
	// Reject a privileged installation credential even in the right database.
	if db, err := openStorage(ctx, StorageConfig{"store", filepath.Join(root, "store-admin.url"), storeCfg.Database}); err == nil {
		db.Close()
		t.Fatal("installation role accepted at runtime")
	}
	seed, err := openStorage(ctx, storeCfg)
	if err != nil {
		t.Fatal(err)
	}
	repo, _ := store.NewPostgres(seed)
	buyer, seller := store.Account{Kind: "personal", ID: 101}, store.Account{Kind: "personal", ID: 202}
	err = repo.Within(ctx, shopID, func(tx store.Tx) error {
		items := []struct {
			kind, id string
			value    any
		}{
			{"shops", shopID, store.Shop{ID: shopID, Owner: seller, Title: "Synthetic shop"}},
			{"products", productID, store.Product{ID: productID, Title: "Synthetic product", Published: true, LegacyPublicVersion: 1, LegacyPublic: json.RawMessage(fmt.Sprintf(`{"id":%q,"seller_id":202,"title":"Synthetic product","price_quota":500000,"visibility":"public","variants":[]}`, productID))}},
			{"variants", variantID, store.Variant{ID: variantID, ProductID: productID, Price: store.Price{Amount: 100, Currency: "USD"}, Quota: -1}},
			{"orders", orderID, store.Order{ID: orderID, Buyer: buyer, Seller: seller, VariantID: variantID, ProductTitle: "Synthetic product", Quantity: 1, UnitPrice: store.Price{Amount: 100, Currency: "USD"}, Total: store.Price{Amount: 100, Currency: "USD"}, State: store.AwaitingPayment}},
		}
		for _, item := range items {
			if err := tx.Put(item.kind, item.id, item.value); err != nil {
				return err
			}
		}
		return nil
	})
	seed.Close()
	if err != nil {
		t.Fatal(err)
	}
	rpcToken, err := os.ReadFile(filepath.Join(root, "rpc-token"))
	if err != nil {
		t.Fatal(err)
	}
	hostToken, err := os.ReadFile(filepath.Join(root, "host-token"))
	if err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(t.TempDir(), "rpc.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	rateActor := time.Now().UnixNano()%1000000000 + 1000
	authority := &fixtureAuthority{serviceToken: strings.TrimSpace(string(rpcToken)), users: []int64{101, 202, 303, rateActor, rateActor + 1}}
	rpc := grpc.NewServer()
	pb.RegisterCoreControlServer(rpc, authority)
	go rpc.Serve(listener)
	defer rpc.Stop()
	cfg := Config{Selected: []string{"identity", "store", "support"}, Socket: socket, TokenFile: filepath.Join(root, "rpc-token"), Store: storeCfg, Support: supportCfg}
	runtime, err := New(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { runtime.Close() }()
	newWeb := func() *httptest.Server {
		host, err := modules.NewHost(runtime.Modules, hostToken)
		if err != nil {
			t.Fatal(err)
		}
		if err = host.MarkReady(); err != nil {
			t.Fatal(err)
		}
		auth, err := webcompat.NewAuthenticator(runtime.Client, hostToken)
		if err != nil {
			t.Fatal(err)
		}
		server := httptest.NewServer(webcompat.Mount(host, auth, runtime.Store, runtime.Support))
		t.Cleanup(server.Close)
		return server
	}
	web := newWeb()
	request := func(method, path, token string, in any, want int) map[string]json.RawMessage {
		t.Helper()
		var body []byte
		if in != nil {
			body, _ = json.Marshal(in)
		}
		req, err := http.NewRequest(method, web.URL+path, bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		if in != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := web.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var envelope map[string]json.RawMessage
		if err = json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != want {
			t.Fatalf("%s %s status %d want %d", method, path, resp.StatusCode, want)
		}
		return envelope
	}
	request("GET", "/api/store/products", "", nil, 200)
	request("GET", "/api/store/products/"+productID, string(hostToken), nil, 401)
	request("GET", "/api/store/products/"+productID, fixtureCredential(999), nil, 401)
	request("POST", "/api/store/orders/"+orderID+"/pay", fixtureCredential(303), map[string]any{}, 403)
	request("POST", "/api/store/orders/"+orderID+"/pay", fixtureCredential(101), map[string]any{}, 503)
	opened := request("POST", "/api/store/support/conversations", fixtureCredential(101), map[string]any{"order_id": orderID}, 200)
	var conversation struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(opened["data"], &conversation) != nil || conversation.ID == "" {
		t.Fatal("missing conversation")
	}
	prefix := "/api/store/support/conversations/" + conversation.ID
	request("GET", prefix+"/messages", fixtureCredential(303), nil, 403)
	for i := 1; i <= 3; i++ {
		request("POST", prefix+"/messages", fixtureCredential(101), map[string]any{"request_key": fmt.Sprintf("synthetic-message-%d", i), "body": fmt.Sprintf("Message %d", i)}, 200)
	}
	history := request("GET", prefix+"/messages?limit=2", fixtureCredential(202), nil, 200)
	var page struct {
		Items []struct {
			ID   int64  `json:"id"`
			Body string `json:"body"`
		} `json:"items"`
		HasMore bool `json:"has_more"`
	}
	if json.Unmarshal(history["data"], &page) != nil || len(page.Items) != 2 || !page.HasMore || page.Items[0].Body != "Message 2" || page.Items[1].Body != "Message 3" {
		t.Fatal("history pagination or order")
	}
	before := request("GET", prefix+"/messages?before="+fmt.Sprint(page.Items[0].ID), fixtureCredential(202), nil, 200)
	var older struct {
		Items []struct {
			Body string `json:"body"`
		} `json:"items"`
	}
	_ = json.Unmarshal(before["data"], &older)
	if len(older.Items) != 1 || older.Items[0].Body != "Message 1" {
		t.Fatal("reverse cursor")
	}
	last := page.Items[1].ID
	request("GET", "/api/store/support/conversations?role=seller&unread=true", fixtureCredential(202), nil, 200)
	request("PUT", prefix+"/read", fixtureCredential(202), map[string]any{"through_id": last + 99}, 422)
	request("PUT", prefix+"/read", fixtureCredential(202), map[string]any{"through_id": last}, 200)
	read := request("GET", "/api/store/support/conversations?role=seller&unread=true", fixtureCredential(202), nil, 200)
	var threads struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	_ = json.Unmarshal(read["data"], &threads)
	for _, thread := range threads.Items {
		if thread.ID == conversation.ID {
			t.Fatal("read cursor did not clear unread")
		}
	}
	request("PUT", prefix+"/status", fixtureCredential(202), map[string]any{"status": "resolved"}, 200)
	request("PUT", prefix+"/status", fixtureCredential(202), map[string]any{"status": "open"}, 200)
	request("POST", prefix+"/messages", fixtureCredential(202), map[string]any{"request_key": "unicode-valid-4000", "body": strings.Repeat("😀", 4000)}, 200)
	request("POST", prefix+"/messages", fixtureCredential(202), map[string]any{"request_key": "unicode-invalid-4001", "body": strings.Repeat("😀", 4001)}, 422)
	request("GET", prefix+"/assistant-context", fixtureCredential(101), nil, 200)
	request("GET", "/api/store/my/customers", fixtureCredential(202), nil, 503)
	// The process resources are recreated; history and IDs come from PostgreSQL.
	web.Close()
	if err = runtime.Close(); err != nil {
		t.Fatal(err)
	}
	runtime, err = New(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	web = newWeb()
	persisted := request("GET", prefix+"/messages", fixtureCredential(101), nil, 200)
	var all struct {
		Items []any `json:"items"`
	}
	_ = json.Unmarshal(persisted["data"], &all)
	if len(all.Items) != 4 {
		t.Fatalf("persisted %d messages want 4", len(all.Items))
	}
	if authority.calls.Load() < 15 {
		t.Fatal("authority not consulted on protected requests")
	}
	// Two independently composed workers share one bounded user/scope window.
	peer, err := New(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	slots := make(chan struct{}, 8)
	results := make(chan error, 70)
	for i := 0; i < 70; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			slots <- struct{}{}
			defer func() { <-slots }()
			service := runtime.Support
			if i%2 == 1 {
				service = peer.Support
			}
			results <- service.RateLimitWeb(ctx, fixtureCredential(rateActor), rateActor, "message")
		}(i)
	}
	wg.Wait()
	close(results)
	accepted, limited := 0, 0
	for err := range results {
		if err == nil {
			accepted++
		} else if errors.Is(err, store.ErrRateLimited) {
			limited++
		} else {
			t.Fatal(err)
		}
	}
	if accepted != 60 || limited != 10 {
		t.Fatalf("cross-worker rate accepted=%d limited=%d", accepted, limited)
	}
	if err = peer.Close(); err != nil {
		t.Fatal(err)
	}
	peer, err = New(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	if err = peer.Support.RateLimitWeb(ctx, fixtureCredential(rateActor), rateActor, "message"); !errors.Is(err, store.ErrRateLimited) {
		t.Fatal("restart reset rate window")
	}
	if err = peer.Support.RateLimitWeb(ctx, fixtureCredential(rateActor), rateActor, "receipt"); err != nil {
		t.Fatal("scope isolation", err)
	}
	if err = peer.Support.RateLimitWeb(ctx, fixtureCredential(rateActor+1), rateActor+1, "message"); err != nil {
		t.Fatal("user isolation", err)
	}
	// Actual SQL constraint rejects overlong native byte bodies independently
	// of the HTTP Unicode validation, and leaves message data unchanged.
	db, err := openStorage(ctx, supportCfg)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.ExecContext(ctx, `INSERT INTO lmm_support.messages(shop_id,id,body) VALUES ('synthetic-shop','invalid-long',jsonb_build_object('author_user_id',101,'body',$1::text))`, strings.Repeat("x", 16001))
	if err == nil {
		t.Fatal("database body constraint missing")
	}
}
