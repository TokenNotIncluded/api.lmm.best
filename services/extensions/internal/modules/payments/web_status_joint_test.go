// Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later.
package payments_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/app"
	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/coreclient"
	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules"
	p "github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules/payments"
	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules/payments/rustbridge"
	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/webcompat"
	_ "github.com/lib/pq"
)

type webJointFixture struct {
	Socket          string   `json:"socket"`
	IdentityToken   string   `json:"identity_token"`
	PaymentToken    string   `json:"payment_token"`
	HostToken       string   `json:"host_token"`
	Owner           string   `json:"owner"`
	Old             string   `json:"old"`
	Other           string   `json:"other"`
	APIKey          string   `json:"api_key"`
	UserID          int64    `json:"user_id"`
	Intent          p.Intent `json:"intent"`
	TeamIntent      p.Intent `json:"team_intent"`
	DatabaseURLFile string   `json:"database_url_file"`
	DatabaseName    string   `json:"database_name"`
}

// Run only under the Rust-owned SQLx fixture. Every identity and payment
// authority call crosses the real Unix RPC into Rust backed by fresh PG.
// No provider adapter is constructed. The actual app starts its normal worker
// against empty channels and a fixture with no pending money work.
func TestPaymentWebStatusRealRust(t *testing.T) {
	path := os.Getenv("PAYMENT_WEB_STATUS_FIXTURE")
	if path == "" {
		t.Skip("real PG/Rust payment web fixture NOT RUN")
	}
	phase := os.Getenv("PAYMENT_WEB_STATUS_PHASE")
	if phase != "live" && phase != "revoked" {
		t.Fatal("invalid web joint phase")
	}
	private := func(path string) []byte {
		t.Helper()
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
			t.Fatal("unsafe private fixture input")
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal("cannot read private fixture input")
		}
		return raw
	}
	var f webJointFixture
	if json.Unmarshal(private(path), &f) != nil || f.Intent.ID <= 0 || f.UserID <= 0 || !strings.HasPrefix(f.DatabaseName, "lmm_payments_") {
		t.Fatal("invalid isolated web fixture")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	db, err := sql.Open("postgres", strings.TrimSpace(string(private(f.DatabaseURLFile))))
	if err != nil {
		t.Fatal("cannot open isolated module PG")
	}
	defer db.Close()
	db.SetMaxOpenConns(2)
	var privileged, ownerMembership bool
	if db.QueryRowContext(ctx, `SELECT rolsuper OR rolcreatedb OR rolcreaterole OR rolreplication OR rolbypassrls, EXISTS(SELECT 1 FROM pg_roles r WHERE r.rolname<>current_user AND pg_has_role(current_user,r.oid,'MEMBER')) FROM pg_roles WHERE rolname=current_user`).Scan(&privileged, &ownerMembership) != nil || privileged || ownerMembership {
		t.Fatal("payment module credential is privileged")
	}
	store, err := p.NewPostgres(ctx, db, f.DatabaseName)
	if err != nil {
		t.Fatal("real PG guard rejected module fixture")
	}
	identity, err := coreclient.New(f.Socket, []byte(f.IdentityToken))
	if err != nil {
		t.Fatal("cannot construct real identity client")
	}
	defer identity.Close()
	payment, err := coreclient.New(f.Socket, []byte(f.PaymentToken))
	if err != nil {
		t.Fatal("cannot construct real payment client")
	}
	defer payment.Close()
	bridge, err := rustbridge.New(payment)
	if err != nil {
		t.Fatal("cannot construct real bridge")
	}
	service, err := p.New(store, bridge)
	if err != nil {
		t.Fatal("cannot construct read service")
	}
	auth, err := webcompat.NewAuthenticator(identity, []byte(f.HostToken))
	if err != nil {
		t.Fatal("cannot construct real authenticator")
	}
	reader := webcompat.NewPaymentStatusReader(service)
	host, err := modules.NewHostWithLimit(nil, []byte(f.HostToken), 1)
	if err != nil {
		t.Fatal("cannot construct host")
	}
	handler := webcompat.Mount(host, auth, nil, nil, webcompat.AdditionalServices{Payments: reader})
	orderID := "top_" + strconv.FormatInt(f.Intent.ID, 10)
	orderPath := "/api/user/payments/orders/" + orderID
	teamPath := "/api/user/payments/orders/top_" + strconv.FormatInt(f.TeamIntent.ID, 10)
	snapshot := func() string {
		t.Helper()
		var snapshot string
		if db.QueryRowContext(ctx, `SELECT COALESCE(jsonb_agg(to_jsonb(o) ORDER BY id)::text,'[]') FROM payment_orders o`).Scan(&snapshot) != nil {
			t.Fatal("cannot snapshot real module rows")
		}
		return snapshot
	}
	call := func(h http.Handler, method, path, credential string, want int, label string) *httptest.ResponseRecorder {
		t.Helper()
		before := snapshot()
		req := httptest.NewRequest(method, path, nil)
		if credential != "" {
			req.Header.Set("Authorization", "Bearer "+credential)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != want || label != "" && !strings.Contains(rec.Body.String(), label) {
			t.Fatalf("web joint %s: HTTP %d, want %d (%s)", method, rec.Code, want, label)
		}
		if snapshot() != before {
			t.Fatal("reading/rejected web request mutated the durable order")
		}
		body := rec.Body.String()
		for _, secret := range []string{f.Owner, f.Old, f.Other, f.APIKey, f.IdentityToken, f.PaymentToken, f.HostToken, "fixture_sensitive_callback", "fixture_sensitive_signature", "fixture_sensitive_request_hash", "fixture_sensitive_checkout"} {
			if secret != "" && strings.Contains(body, secret) {
				t.Fatal("public payment response leaked private credential/evidence")
			}
		}
		for _, key := range []string{`"request_hash"`, `"request_sha256"`, `"jobs"`, `"evidence"`, `"signature"`, `"journal_id"`, `"account_id"`, `"created_by"`, `"checkout"`, `"intent"`, `"credit_receipt"`} {
			if strings.Contains(body, key) {
				t.Fatal("public payment response exposed internal persistence fields")
			}
		}
		if want != http.StatusServiceUnavailable || label == "PAYMENT_DISABLED" {
			if rec.Header().Get("Cache-Control") != "no-store" && want != http.StatusNotFound {
				t.Fatal("private payment response lacks no-store")
			}
		}
		return rec
	}
	if phase == "revoked" {
		if host.MarkReady() != nil {
			t.Fatal("host readiness")
		}
		call(handler, "GET", orderPath, f.Owner, 401, "PAYMENT_LOGIN_REQUIRED")
		call(handler, "GET", teamPath, f.Other, 403, "PAYMENT_ACCESS_DENIED")
		t.Log("same owner and URL denied after actual Rust logout; no local cached authority")
		return
	}
	base := p.Order{ID: orderID, ChannelID: 1, Namespace: "stripe/acct_web_status_fixture/test", Intent: f.Intent, CreditState: "pending", Jobs: map[string]p.Job{}, Refunds: map[string]p.Refund{}, Checkout: p.Checkout{ID: "cs_fixture", URL: "fixture_sensitive_checkout"}}
	if store.Insert(ctx, base) != nil {
		t.Fatal("cannot seed new synthetic module order")
	}
	teamOrder := base
	teamOrder.ID = "top_" + strconv.FormatInt(f.TeamIntent.ID, 10)
	teamOrder.Intent = f.TeamIntent
	if teamOrder.Intent.CreatedBy == 7102 || store.Insert(ctx, teamOrder) != nil {
		t.Fatal("cannot seed noncreator team fixture")
	}

	call(handler, "GET", orderPath, f.Owner, 503, "") // pre-ready cannot bypass host lifecycle
	if host.MarkReady() != nil {
		t.Fatal("host readiness")
	}
	rec := call(handler, "GET", orderPath, f.Owner, 200, `"state":"awaiting_payment"`)
	var body struct {
		Success bool                          `json:"success"`
		Data    webcompat.PaymentPublicStatus `json:"data"`
	}
	if json.Unmarshal(rec.Body.Bytes(), &body) != nil || !body.Success || body.Data.OrderID != orderID || body.Data.Currency != "USD" || body.Data.AmountMinor != 100 || body.Data.CoreCreditConfirmed || body.Data.ProviderPaid {
		t.Fatal("prepared Rust intent was misreported as money")
	}
	call(handler, "HEAD", orderPath, f.Owner, 200, "")
	call(handler, "GET", teamPath, f.Other, 200, `"state":"awaiting_payment"`)
	// Corrupt only this new fixture's module snapshot; the runtime role cannot
	// alter Core. Restore exact bytes before continuing with authorization cases.
	var original string
	if db.QueryRowContext(ctx, `SELECT snapshot::text FROM payment_orders WHERE id=$1`, orderID).Scan(&original) != nil {
		t.Fatal("cannot freeze module fixture row")
	}
	for _, malform := range []func(*p.Order){func(o *p.Order) { o.Intent.AmountMinor = 0 }, func(o *p.Order) { o.Intent.AmountMinor = -1 }, func(o *p.Order) { o.Intent.Currency = "usd" }, func(o *p.Order) { o.Intent.Currency = "US" }, func(o *p.Order) { o.Intent = f.TeamIntent }} {
		var corrupted p.Order
		if json.Unmarshal([]byte(original), &corrupted) != nil {
			t.Fatal("cannot decode private fixture row")
		}
		malform(&corrupted)
		raw, err := json.Marshal(corrupted)
		if err != nil {
			t.Fatal("cannot encode synthetic bad row")
		}
		if _, err = db.ExecContext(ctx, `UPDATE payment_orders SET snapshot=$1 WHERE id=$2`, string(raw), orderID); err != nil {
			t.Fatal("cannot construct synthetic bad module row")
		}
		call(handler, "GET", orderPath, f.Owner, 503, "PAYMENT_UNAVAILABLE")
		call(handler, "HEAD", orderPath, f.Owner, 503, "PAYMENT_UNAVAILABLE")
		if _, err = db.ExecContext(ctx, `UPDATE payment_orders SET snapshot=$1 WHERE id=$2`, original, orderID); err != nil {
			t.Fatal("cannot restore private fixture row")
		}
	}
	for _, c := range []struct {
		credential string
		code       int
		label      string
	}{{f.Other, 403, "PAYMENT_ACCESS_DENIED"}, {f.Old, 401, "PAYMENT_LOGIN_REQUIRED"}, {f.APIKey, 401, "PAYMENT_LOGIN_REQUIRED"}, {f.HostToken, 401, "PAYMENT_LOGIN_REQUIRED"}, {"", 401, "PAYMENT_LOGIN_REQUIRED"}, {"lmms_unknown_web_fixture_0000000000000000000", 401, "PAYMENT_LOGIN_REQUIRED"}} {
		call(handler, "GET", orderPath, c.credential, c.code, c.label)
	}
	for _, method := range []string{"POST", "PUT", "PATCH", "DELETE"} {
		call(handler, method, orderPath, f.Owner, 403, "PAYMENT_HOST_CANNOT_MINT")
	}
	call(handler, "GET", "/api/user/payments/orders/top_missing", f.Owner, 404, "PAYMENT_NOT_FOUND")
	disabled := webcompat.Mount(host, auth, nil, nil)
	call(disabled, "GET", orderPath, f.Owner, 503, "PAYMENT_DISABLED")
	held, err := host.TryAcquire()
	if err != nil {
		t.Fatal("cannot reserve host capacity")
	}
	call(handler, "GET", orderPath, f.Owner, 503, "")
	held()
	call(handler, "GET", orderPath, f.Owner, 200, `"state":"awaiting_payment"`)
	set := func(fn func(*p.Order)) {
		t.Helper()
		if store.Mutate(ctx, orderID, func(o *p.Order) error { fn(o); return nil }) != nil {
			t.Fatal("cannot seed synthetic read state")
		}
	}
	for _, kind := range []string{"checkout", "credit", "hold", "refund_submit", "commit", "release", "external_refund"} {
		set(func(o *p.Order) {
			o.Jobs = map[string]p.Job{"private_job": {Key: "private_job", Kind: kind, State: "review", Evidence: p.Evidence{Payload: []byte("fixture_sensitive_callback"), Signature: []byte("fixture_sensitive_signature")}}}
		})
		call(handler, "GET", orderPath, f.Owner, 200, `"state":"review"`)
	}
	set(func(o *p.Order) { o.Jobs = map[string]p.Job{}; o.CreditState = "rejected" })
	call(handler, "GET", orderPath, f.Owner, 200, `"state":"review"`)
	set(func(o *p.Order) {
		o.CreditState = "pending"
		o.Refunds = map[string]p.Refund{"unknown": {ID: "unknown", State: "held", Dispatch: p.DispatchUnknown}, "responded": {ID: "responded", State: "released", Dispatch: p.DispatchResponded}}
	})
	for i := 0; i < 20; i++ {
		call(handler, "GET", orderPath, f.Owner, 200, `"refund_dispatch":"unknown"`)
	}
	// Restore no-work fixture state before booting the actual app. Empty provider
	// channels and no pending jobs prevent external checkout or refund requests.
	set(func(o *p.Order) {
		o.CreditState = "pending"
		o.Refunds = map[string]p.Refund{}
		o.Jobs = map[string]p.Job{}
	})
	beforeApp := snapshot()
	runPaymentWebApp(t, ctx, f, orderPath)
	if snapshot() != beforeApp {
		t.Fatal("actual app lifecycle or status request mutated durable module rows")
	}
	host.BeginDrain()
	call(handler, "GET", orderPath, f.Owner, 503, "")
	if host.Wait(ctx) != nil {
		t.Fatal("host retained a read permit after drain")
	}
	t.Log("real PG + Rust auth/ReadPaymentReceipt + Go Service + Mount: prepared read, cross-user/old/API-key/host/anonymous denial, default disabled, write refusal, pre-ready/saturation/recovery/drain, review/rejected/UNKNOWN projection, no module-row write and no sensitive DTO leakage")
}

// Only this subprocess enters the real production assembly entry point.
func TestPaymentWebAppProcess(t *testing.T) {
	config := os.Getenv("PAYMENT_WEB_STATUS_APP_CONFIG")
	if config == "" {
		t.Skip("owned app subprocess only")
	}
	os.Args = []string{os.Args[0], "--config", config}
	if err := app.Run(); err != nil {
		t.Fatal("real app startup failed:", err)
	}
}

func runPaymentWebApp(t *testing.T, ctx context.Context, f webJointFixture, orderPath string) {
	t.Helper()
	output := filepath.Dir(os.Getenv("PAYMENT_WEB_STATUS_FIXTURE"))
	private := func(name, value string) string {
		t.Helper()
		path := filepath.Join(output, name)
		if os.WriteFile(path, []byte(value), 0600) != nil {
			t.Fatal("cannot write private app fixture")
		}
		return path
	}
	host := private("app-host-token.private", f.HostToken)
	identity := private("app-identity-token.private", f.IdentityToken)
	payment := private("app-payment-token.private", f.PaymentToken)
	provider := private("app-provider.private.toml", "version=1\nprovider='stripe-test'\nchannels=[]\n")
	config := private("app.private.toml", fmt.Sprintf("version=1\n[host]\nlisten='127.0.0.1:0'\ntoken_file=%q\nmodules=['payments']\nweb_compat=true\nmax_in_flight=2\n[core]\nsocket=%q\nidentity_token_file=%q\npayment_token_file=%q\n[payments]\ndatabase_name=%q\ndatabase_url_file=%q\nconfig_file=%q\ncallbacks_enabled=false\n", host, f.Socket, identity, payment, f.DatabaseName, f.DatabaseURLFile, provider))
	log, err := os.OpenFile(filepath.Join(output, "go-app.private.log"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal("cannot open private app log")
	}
	defer log.Close()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestPaymentWebAppProcess$", "-test.count=1", "-test.v", "-test.timeout=30s")
	for _, value := range os.Environ() {
		key, _, _ := strings.Cut(value, "=")
		if key == "DATABASE_URL" || key == "SQL_DSN" || key == "LOG_SQL_DSN" || strings.HasPrefix(key, "PG") || strings.HasPrefix(key, "LMM_CORE") || strings.HasPrefix(key, "LMM_EXTENSION") || strings.HasPrefix(key, "LMM_STORE") || strings.HasPrefix(key, "LMM_SUPPORT") || strings.HasPrefix(key, "LMM_LEGAL") || key == "LMM_DB_MIGRATION_MODE" {
			continue
		}
		cmd.Env = append(cmd.Env, value)
	}
	cmd.Env = append(cmd.Env, "PAYMENT_WEB_STATUS_APP_CONFIG="+config)
	cmd.Stdout = log
	cmd.Stderr = log
	if cmd.Start() != nil {
		t.Fatal("cannot start owned app process")
	}
	stopped := false
	defer func() {
		if !stopped {
			_ = cmd.Process.Signal(os.Interrupt)
			_ = cmd.Wait()
		}
	}()
	address := ""
	end := time.Now().Add(10 * time.Second)
	for address == "" && time.Now().Before(end) {
		entries, _ := os.ReadDir(fmt.Sprintf("/proc/%d/fd", cmd.Process.Pid))
		inodes := map[string]bool{}
		for _, entry := range entries {
			target, _ := os.Readlink(fmt.Sprintf("/proc/%d/fd/%s", cmd.Process.Pid, entry.Name()))
			if strings.HasPrefix(target, "socket:[") {
				inodes[strings.TrimSuffix(strings.TrimPrefix(target, "socket:["), "]")] = true
			}
		}
		table, _ := os.ReadFile(fmt.Sprintf("/proc/%d/net/tcp", cmd.Process.Pid))
		for _, line := range strings.Split(string(table), "\n") {
			cols := strings.Fields(line)
			if len(cols) < 10 || cols[3] != "0A" || !inodes[cols[9]] {
				continue
			}
			pair := strings.Split(cols[1], ":")
			if len(pair) != 2 || pair[0] != "0100007F" {
				continue
			}
			port, _ := strconv.ParseUint(pair[1], 16, 16)
			if port == 0 || port == 18080 || port == 18081 || port == 50837 || port == 47399 {
				continue
			}
			if address != "" {
				t.Fatal("owned app listener is ambiguous")
			}
			address = net.JoinHostPort("127.0.0.1", strconv.FormatUint(port, 10))
		}
		if address == "" {
			time.Sleep(20 * time.Millisecond)
		}
	}
	if address == "" {
		t.Fatal("actual app never bound its own ephemeral loopback listener; inspect private app log")
	}
	client := &http.Client{Timeout: 3 * time.Second}
	for _, c := range []struct {
		method, credential string
		code               int
	}{{"GET", f.Owner, 200}, {"HEAD", f.Owner, 200}, {"GET", f.Other, 403}, {"GET", f.Old, 401}, {"GET", f.HostToken, 401}, {"GET", "", 401}, {"POST", f.Owner, 403}} {
		req, _ := http.NewRequestWithContext(ctx, c.method, "http://"+address+orderPath, nil)
		if c.credential != "" {
			req.Header.Set("Authorization", "Bearer "+c.credential)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal("actual app HTTP request failed")
		}
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		_ = resp.Body.Close()
		if resp.StatusCode != c.code {
			t.Fatalf("actual app status route HTTP %d, want %d", resp.StatusCode, c.code)
		}
		if c.code == 200 && c.method == "GET" && (!bytes.Contains(raw, []byte(`"state":"awaiting_payment"`)) || bytes.Contains(raw, []byte(`"core_credit_confirmed":true`))) {
			t.Fatal("actual app did not return the uncredited read model")
		}
		if resp.Header.Get("Cache-Control") != "no-store" {
			t.Fatal("actual app status was cacheable")
		}
	}
	teamRequest, _ := http.NewRequestWithContext(ctx, "GET", "http://"+address+"/api/user/payments/orders/top_"+strconv.FormatInt(f.TeamIntent.ID, 10), nil)
	teamRequest.Header.Set("Authorization", "Bearer "+f.Other)
	teamResponse, teamError := client.Do(teamRequest)
	if teamError != nil {
		t.Fatal("actual app team status transport failed")
	}
	_ = teamResponse.Body.Close()
	if teamResponse.StatusCode != 200 {
		t.Fatal("actual app rejected Rust-authorized team noncreator")
	}
	if cmd.Process.Signal(os.Interrupt) != nil {
		t.Fatal("cannot drain owned app process")
	}
	err = cmd.Wait()
	stopped = true
	if err != nil {
		t.Fatal("actual app did not exit cleanly after SIGINT")
	}
	appLog, readErr := os.ReadFile(filepath.Join(output, "go-app.private.log"))
	if readErr != nil || !bytes.Contains(appLog, []byte("--- PASS: TestPaymentWebAppProcess")) || bytes.Contains(appLog, []byte("--- SKIP:")) {
		t.Fatal("actual app helper was skipped or did not run")
	}

	t.Log("actual app.Run payments-only assembly served owner status over its own loopback port; other/old/host/anonymous denied, writes refused, graceful SIGINT joined")
}
