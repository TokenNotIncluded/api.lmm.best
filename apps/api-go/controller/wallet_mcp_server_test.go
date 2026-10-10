package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image/png"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/setting/system_setting"
	"github.com/glebarez/sqlite"
	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func setupWalletMCPTest(t *testing.T) (*gorm.DB, model.User, model.User) {
	t.Helper()
	installIdentityCurrencyFixture(t)
	previousDB, previousLogDB, previousRedis := model.DB, model.LOG_DB, common.RedisEnabled
	previousMain, previousLog := common.MainDatabaseType(), common.LogDatabaseType()
	previousAddress := system_setting.ServerAddress
	system_setting.ServerAddress = "https://console.example.test/"
	common.RedisEnabled = false
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	db, err := gorm.Open(sqlite.Open("file:"+strings.ReplaceAll(t.Name(), "/", "_")+"?mode=memory&cache=shared"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	conn, err := db.DB()
	require.NoError(t, err)
	conn.SetMaxOpenConns(1)
	model.DB, model.LOG_DB = db, db
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.WalletTransfer{}, &model.OpenSourceBountyMCPConfirmation{}, &model.OpenSourceBountyMCPOperation{}))
	persistCreditDenominationFixture(t, db)
	user := model.User{Username: "wallet-owner", AffCode: "wallet-owner", Quota: 1000, Status: common.UserStatusEnabled, AuthVersion: 1}
	other := model.User{Username: "wallet-other", AffCode: "wallet-other", Quota: 500, Status: common.UserStatusEnabled, AuthVersion: 1}
	require.NoError(t, db.Create(&user).Error)
	require.NoError(t, db.Create(&other).Error)
	t.Cleanup(func() {
		model.DB, model.LOG_DB, common.RedisEnabled = previousDB, previousLogDB, previousRedis
		common.SetDatabaseTypes(previousMain, previousLog)
		system_setting.ServerAddress = previousAddress
		_ = conn.Close()
	})
	return db, user, other
}

func walletMCPTestExtra(key string) map[string]any {
	return map[string]any{"market_builtin": true, "market_tool_grant": true, "wallet_write": true, "market_request_id": strings.Repeat(key, 64), "market_client_id": "oauth:wallet-test", "market_tool_id": "wallet-test-tool", "market_version_id": "wallet-test-version", "market_grant_id": "wallet-test-grant"}
}

func walletMCPTestSession(t *testing.T, userID int, extra map[string]any) *mcp.ClientSession {
	t.Helper()
	server := mcp.NewServer(&mcp.Implementation{Name: "wallet-test", Version: "1"}, nil)
	registerWalletMCPTools(server)
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
	verifier := func(context.Context, string, *http.Request) (*auth.TokenInfo, error) {
		return &auth.TokenInfo{UserID: strconv.Itoa(userID), Expiration: time.Now().Add(time.Hour), Extra: extra}, nil
	}
	httpServer := httptest.NewServer(auth.RequireBearerToken(verifier, nil)(handler))
	t.Cleanup(httpServer.Close)
	client := mcp.NewClient(&mcp.Implementation{Name: "wallet-client-test", Version: "1"}, &mcp.ClientOptions{MultiRoundTrip: &mcp.MultiRoundTripOptions{Disabled: true}})
	session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: httpServer.URL, HTTPClient: &http.Client{Transport: openSourceBountyBearerTransport{token: "test-only-token"}}, DisableStandaloneSSE: true}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func walletMCPCall(t *testing.T, session *mcp.ClientSession, name string, args any, state string) *mcp.CallToolResult {
	t.Helper()
	params := &mcp.CallToolParams{Name: name, Arguments: args, RequestState: state}
	if state != "" {
		params.InputResponses = mcp.InputResponseMap{"confirmation": &mcp.ElicitResult{Action: "accept", Content: map[string]any{"confirmed": true}}}
	}
	result, err := session.CallTool(context.Background(), params)
	require.NoError(t, err)
	return result
}

func walletMCPData(t *testing.T, result *mcp.CallToolResult) map[string]any {
	t.Helper()
	require.False(t, result.IsError)
	encoded, err := json.Marshal(result.StructuredContent)
	require.NoError(t, err)
	var output walletMCPOutput
	require.NoError(t, json.Unmarshal(encoded, &output))
	data, ok := output.Data.(map[string]any)
	require.True(t, ok)
	return data
}

func requireWalletMCPQR(t *testing.T, result *mcp.CallToolResult, expected string) {
	t.Helper()
	var image *mcp.ImageContent
	for _, content := range result.Content {
		if found, ok := content.(*mcp.ImageContent); ok {
			image = found
		}
	}
	require.NotNil(t, image)
	require.Equal(t, "image/png", image.MIMEType)
	decoded, err := png.Decode(bytes.NewReader(image.Data))
	require.NoError(t, err)
	require.Equal(t, 512, decoded.Bounds().Dx())
	decoder, err := exec.LookPath("zbarimg")
	if err != nil {
		t.Skip("independent QR decoder zbarimg is not installed")
	}
	path := filepath.Join(t.TempDir(), "wallet-link.png")
	require.NoError(t, os.WriteFile(path, image.Data, 0600))
	contents, err := exec.Command(decoder, "--quiet", "--raw", path).Output()
	require.NoError(t, err)
	require.Equal(t, expected, strings.TrimSpace(string(contents)), "the local QR must encode exactly the console link")
}

func TestWalletMCPReadTopupAndPrivateQR(t *testing.T) {
	_, user, _ := setupWalletMCPTest(t)
	session := walletMCPTestSession(t, user.Id, walletMCPTestExtra("a"))
	tools, err := session.ListTools(context.Background(), nil)
	require.NoError(t, err)
	require.Len(t, tools.Tools, 5)
	balance := walletMCPData(t, walletMCPCall(t, session, "wallet.balance", map[string]any{}, ""))
	require.EqualValues(t, 1000, balance["available_quota"])
	require.EqualValues(t, 0, balance["tool_price_quota"])
	result := walletMCPCall(t, session, "wallet.topup_link", map[string]any{"amount": 25}, "")
	data := walletMCPData(t, result)
	require.Equal(t, "https://console.example.test/wallet?topup_amount=25", data["url"])
	require.Equal(t, true, data["payment_confirmation_required"])
	requireWalletMCPQR(t, result, data["url"].(string))
	for _, amount := range []any{0, -1, 1.5, 1_000_001, "25", "1e3"} {
		require.True(t, walletMCPCall(t, session, "wallet.topup_link", map[string]any{"amount": amount}, "").IsError, fmt.Sprint(amount))
	}
}

func TestWalletMCPRequiresExactGrantAndBoundConfirmation(t *testing.T) {
	db, user, other := setupWalletMCPTest(t)
	extra := walletMCPTestExtra("a")
	session := walletMCPTestSession(t, user.Id, extra)
	args := map[string]any{"quota": 300}
	pending := walletMCPCall(t, session, "wallet.transfer.create", args, "")
	require.True(t, pending.NeedsInput())
	form := pending.InputRequests["confirmation"].(*mcp.ElicitParams)
	require.Contains(t, form.Message, "Hold USD")
	require.NotContains(t, form.Message, "internal")
	require.Contains(t, form.Message, "fee is 0")
	var current model.User
	require.NoError(t, db.First(&current, user.Id).Error)
	require.Equal(t, 1000, current.Quota)
	for _, test := range []struct {
		key   string
		value any
	}{
		{"market_builtin", false}, {"market_tool_grant", false}, {"wallet_write", false},
		{"market_request_id", strings.Repeat("b", 64)}, {"market_client_id", "oauth:another-client"},
		{"market_tool_id", "another-tool"}, {"market_version_id", "another-version"}, {"market_grant_id", "another-grant"},
	} {
		t.Run(test.key, func(t *testing.T) {
			changed := make(map[string]any, len(extra))
			for k, v := range extra {
				changed[k] = v
			}
			changed[test.key] = test.value
			badSession := walletMCPTestSession(t, user.Id, changed)
			require.True(t, walletMCPCall(t, badSession, "wallet.transfer.create", args, pending.RequestState).IsError)
		})
	}
	require.True(t, walletMCPCall(t, session, "wallet.transfer.create", map[string]any{"quota": 301}, pending.RequestState).IsError)
	require.True(t, walletMCPCall(t, session, "wallet.transfer.create", args, "mcp_confirm_forged").IsError)
	otherSession := walletMCPTestSession(t, other.Id, extra)
	require.True(t, walletMCPCall(t, otherSession, "wallet.transfer.create", args, pending.RequestState).IsError)
	declined, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "wallet.transfer.create", Arguments: args, RequestState: pending.RequestState, InputResponses: mcp.InputResponseMap{"confirmation": &mcp.ElicitResult{Action: "decline"}}})
	require.NoError(t, err)
	require.True(t, declined.IsError)
	missing, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "wallet.transfer.create", Arguments: args, RequestState: pending.RequestState})
	require.NoError(t, err)
	require.True(t, missing.IsError)
	require.NoError(t, db.First(&current, user.Id).Error)
	require.Equal(t, 1000, current.Quota)
	result := walletMCPCall(t, session, "wallet.transfer.create", args, pending.RequestState)
	data := walletMCPData(t, result)
	link := data["share_url"].(string)
	parsed, err := url.Parse(link)
	require.NoError(t, err)
	require.Equal(t, "console.example.test", parsed.Host)
	require.Equal(t, "/transfer", parsed.Path)
	require.Empty(t, parsed.RawQuery)
	require.Len(t, parsed.Fragment, 64)
	requireWalletMCPQR(t, result, link)
	var wg sync.WaitGroup
	results := make(chan *mcp.CallToolResult, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "wallet.transfer.create", Arguments: args, RequestState: pending.RequestState, InputResponses: mcp.InputResponseMap{"confirmation": &mcp.ElicitResult{Action: "accept", Content: map[string]any{"confirmed": true}}}})
			if err == nil {
				results <- result
			} else {
				results <- &mcp.CallToolResult{IsError: true}
			}
		}()
	}
	wg.Wait()
	close(results)
	for result := range results {
		require.Equal(t, link, walletMCPData(t, result)["share_url"])
	}
	require.True(t, walletMCPCall(t, session, "wallet.transfer.create", map[string]any{"quota": 301}, pending.RequestState).IsError, "successful confirmation replay must still reject amount changes")
	require.NoError(t, db.First(&current, user.Id).Error)
	require.Equal(t, 700, current.Quota)
	var count int64
	require.NoError(t, db.Model(&model.WalletTransfer{}).Count(&count).Error)
	require.EqualValues(t, 1, count)
}

func TestWalletMCPCancelOwnershipRefundAndReplay(t *testing.T) {
	db, user, other := setupWalletMCPTest(t)
	transfer, err := model.CreateWalletTransfer(user.Id, 300, "test-rest-create-0000001")
	require.NoError(t, err)
	session := walletMCPTestSession(t, user.Id, walletMCPTestExtra("b"))
	otherSession := walletMCPTestSession(t, other.Id, walletMCPTestExtra("c"))
	args := map[string]any{"transfer_id": transfer.Id}
	require.True(t, walletMCPCall(t, otherSession, "wallet.transfer.cancel", args, "").IsError)
	pending := walletMCPCall(t, session, "wallet.transfer.cancel", args, "")
	require.True(t, pending.NeedsInput())
	require.Contains(t, pending.InputRequests["confirmation"].(*mcp.ElicitParams).Message, "exactly 300")
	for i := 0; i < 3; i++ {
		walletMCPData(t, walletMCPCall(t, session, "wallet.transfer.cancel", args, pending.RequestState))
	}
	var current model.User
	require.NoError(t, db.First(&current, user.Id).Error)
	require.Equal(t, 1000, current.Quota)
	_, err = model.ClaimWalletTransfer(transfer.Token, other.Id)
	require.ErrorIs(t, err, model.ErrWalletTransferUnavailable)
	list := walletMCPCall(t, otherSession, "wallet.transfers.list", map[string]any{}, "")
	require.False(t, list.IsError)
	encoded, err := json.Marshal(list.StructuredContent)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), transfer.Token)
	require.NotContains(t, string(encoded), "wallet-owner")
}

func TestWalletMCPExpiredAuthChangedAndFailedDebitLeaveBalanceSafe(t *testing.T) {
	db, user, _ := setupWalletMCPTest(t)
	session := walletMCPTestSession(t, user.Id, walletMCPTestExtra("a"))
	args := map[string]any{"quota": 300}
	pending := walletMCPCall(t, session, "wallet.transfer.create", args, "")
	require.NoError(t, db.Model(&model.OpenSourceBountyMCPConfirmation{}).Where("id = ?", pending.RequestState).Update("expires_at", time.Now().Unix()-1).Error)
	require.True(t, walletMCPCall(t, session, "wallet.transfer.create", args, pending.RequestState).IsError)
	pending = walletMCPCall(t, session, "wallet.transfer.create", args, "")
	require.NoError(t, db.Model(&model.User{}).Where("id = ?", user.Id).Update("auth_version", 2).Error)
	require.True(t, walletMCPCall(t, session, "wallet.transfer.create", args, pending.RequestState).IsError)
	poor := walletMCPCall(t, session, "wallet.transfer.create", map[string]any{"quota": 1001}, "")
	require.True(t, poor.NeedsInput())
	require.True(t, walletMCPCall(t, session, "wallet.transfer.create", map[string]any{"quota": 1001}, poor.RequestState).IsError)
	var confirmation model.OpenSourceBountyMCPConfirmation
	require.NoError(t, db.First(&confirmation, "id = ?", poor.RequestState).Error)
	require.Zero(t, confirmation.ConsumedAt, "a failed debit must not commit confirmation consumption")
	var current model.User
	require.NoError(t, db.First(&current, user.Id).Error)
	require.Equal(t, 1000, current.Quota)
	require.NoError(t, db.Model(&model.User{}).Where("id = ?", user.Id).Update("status", common.UserStatusDisabled).Error)
	require.True(t, walletMCPCall(t, session, "wallet.balance", map[string]any{}, "").IsError)
}

func TestWalletMCPRejectsUntrustedConsoleOriginsBeforeHoldingMoney(t *testing.T) {
	db, user, _ := setupWalletMCPTest(t)
	session := walletMCPTestSession(t, user.Id, walletMCPTestExtra("a"))
	for _, address := range []string{"", "http://console.example.test", "javascript:alert(1)", "https://secret@console.example.test", "https://console.example.test/path", "https://console.example.test?redirect=https://elsewhere.example", "https://console.example.test/#secret"} {
		system_setting.ServerAddress = address
		require.True(t, walletMCPCall(t, session, "wallet.transfer.create", map[string]any{"quota": 300}, "").IsError, address)
	}
	var current model.User
	require.NoError(t, db.First(&current, user.Id).Error)
	require.Equal(t, 1000, current.Quota)
}
