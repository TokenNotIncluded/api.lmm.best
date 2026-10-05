package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/gin-gonic/gin"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

// Independently exported from 5956acfe, not produced by the compatibility
// helper. The wallet ID also matches the actual installed 76 backup metadata.
const walletLegacyGoldenVersion = "0f274805-9613-5871-be9d-7007d0515822"

type walletLegacyCatalogGolden struct {
	SourceCommit string `json:"source_commit"`
	Services     []struct {
		Key, Name, Description string
		ServiceID              string `json:"service_id"`
		VersionID              string `json:"version_id"`
		Tools                  []struct {
			model.ToolMarketToolInput
			ToolID string `json:"tool_id"`
		}
	} `json:"services"`
}

func readWalletLegacyGolden(t *testing.T) walletLegacyCatalogGolden {
	t.Helper()
	raw, err := os.ReadFile("testdata/wallet-legacy-catalog-5956.json")
	require.NoError(t, err)
	var golden walletLegacyCatalogGolden
	require.NoError(t, json.Unmarshal(raw, &golden))
	require.Equal(t, "5956acfe469b06cb2c6562c14bc1d191b7ecbc68", golden.SourceCommit)
	require.Len(t, golden.Services, 3)
	return golden
}

func walletCatalogCanonicalJSON(t *testing.T, raw json.RawMessage) json.RawMessage {
	t.Helper()
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	require.NoError(t, decoder.Decode(&value))
	encoded, err := json.Marshal(value)
	require.NoError(t, err)
	return encoded
}

func walletLegacyGoldenInputs(t *testing.T) []model.ToolMarketBuiltinServiceInput {
	t.Helper()
	var inputs []model.ToolMarketBuiltinServiceInput
	for _, service := range readWalletLegacyGolden(t).Services {
		input := model.ToolMarketBuiltinServiceInput{Key: service.Key, Name: service.Name, Description: service.Description}
		for _, tool := range service.Tools {
			tool.InputSchema = walletCatalogCanonicalJSON(t, tool.InputSchema)
			tool.OutputSchema = walletCatalogCanonicalJSON(t, tool.OutputSchema)
			input.Tools = append(input.Tools, tool.ToolMarketToolInput)
		}
		inputs = append(inputs, input)
	}
	return inputs
}

func TestWalletLegacyCatalogRetainsActual76GoldenAndAllToolContracts(t *testing.T) {
	inputs, err := toolMarketBuiltinCatalogInputs(context.Background())
	require.NoError(t, err)
	require.Equal(t, walletLegacyGoldenInputs(t), inputs, "all three services and all 29 full tool contracts must match the independent baseline")
	totalTools := 0
	for i, service := range readWalletLegacyGolden(t).Services {
		require.Equal(t, service.ServiceID, model.ToolMarketBuiltinServiceID(inputs[i].Key))
		id, err := model.ToolMarketBuiltinVersionID(inputs[i])
		require.NoError(t, err)
		require.Equal(t, service.VersionID, id)
		if service.Key == "wallet" {
			require.Equal(t, "72c055a5-be44-59cc-9c63-36e03e671bf8", service.ServiceID)
			require.Equal(t, walletLegacyGoldenVersion, id)
			require.Len(t, inputs[i].Tools, 5)
		}
		totalTools += len(inputs[i].Tools)
	}
	require.Equal(t, 29, totalTools)
}

func TestWalletLegacyCatalogProfileIsTextOnlyDetachedAndFailsClosed(t *testing.T) {
	definitions, err := BuiltinToolMarketDefinitions(context.Background())
	require.NoError(t, err)
	var current model.ToolMarketBuiltinServiceInput
	for _, definition := range definitions {
		if definition.Key != "wallet" {
			continue
		}
		current = model.ToolMarketBuiltinServiceInput{Key: definition.Key, Name: definition.Name, Description: definition.Description}
		for _, tool := range definition.Tools {
			input, err := json.Marshal(tool.InputSchema)
			require.NoError(t, err)
			output, err := json.Marshal(tool.OutputSchema)
			require.NoError(t, err)
			permission := "write"
			if tool.Annotations.ReadOnlyHint {
				permission = "read"
			}
			current.Tools = append(current.Tools, model.ToolMarketToolInput{Name: tool.Name, Description: tool.Description, InputSchema: input, OutputSchema: output, Permissions: []string{permission}})
		}
	}
	require.Len(t, current.Tools, 5)
	legacy, err := walletLegacyCatalogCompatibility(current)
	require.NoError(t, err)
	changes := 0
	for i, original := range current.Tools {
		reversed, err := walletCatalogToolDescriptions(legacy.Tools[i], false)
		require.NoError(t, err)
		require.Equal(t, original, reversed, "only the exact three description leaves may differ")
		if original.Description != legacy.Tools[i].Description {
			changes++
		}
		if !bytes.Equal(original.InputSchema, legacy.Tools[i].InputSchema) {
			changes++
		}
	}
	require.Equal(t, 3, changes)
	legacy.Tools[0].InputSchema[0] = 'x'
	legacy.Tools[0].OutputSchema[0] = 'x'
	legacy.Tools[0].Permissions[0] = "write"
	require.True(t, json.Valid(current.Tools[0].InputSchema))
	require.True(t, json.Valid(current.Tools[0].OutputSchema))
	require.Equal(t, []string{"read"}, current.Tools[0].Permissions)
	for _, mutation := range []string{"future_contract", "missing_property", "new_tool", "duplicate_tool"} {
		t.Run(mutation, func(t *testing.T) {
			changed := current
			changed.Tools = append([]model.ToolMarketToolInput(nil), current.Tools...)
			switch mutation {
			case "future_contract":
				changed.Tools[1].Description = "Amount is USD, using a new contract"
			case "missing_property":
				changed.Tools[1].InputSchema = json.RawMessage(`{"type":"object","properties":{}}`)
			case "new_tool":
				changed.Tools = append(changed.Tools, model.ToolMarketToolInput{Name: "wallet.new_tool"})
			case "duplicate_tool":
				changed.Tools[4] = changed.Tools[0]
			}
			_, err := walletLegacyCatalogCompatibility(changed)
			require.ErrorIs(t, err, model.ErrToolMarketConflict)
		})
	}
	current.Key = "other_service"
	unchanged, err := walletLegacyCatalogCompatibility(current)
	require.NoError(t, err)
	require.Equal(t, current, unchanged, "the profile must not apply to other builtins")
	// The catalog conversion must not contaminate the cached direct MCP docs.
	direct, err := BuiltinToolMarketDefinitions(context.Background())
	require.NoError(t, err)
	require.Equal(t, definitions, direct)
}

func assertWalletCurrentDocs(t *testing.T, tools []model.ToolMarketToolVersion) {
	t.Helper()
	for _, tool := range tools {
		if tool.Name == "wallet.topup_link" {
			require.Equal(t, walletMCPTopupDescription, tool.Description)
			require.Contains(t, tool.InputSchema, "This is not raw wallet credits or USD")
		}
		if tool.Name == "wallet.transfer.create" {
			require.Contains(t, tool.InputSchema, "One credit is one raw quota unit")
		}
	}
}

func TestWalletLegacyCatalogOldGrantAndPublicPresentationRemainSeparate(t *testing.T) {
	installIdentityCurrencyFixture(t)
	require.NoError(t, common.SetCreditCurrencyBasis(decimal.NewFromInt(500000), decimal.NewFromInt(500000)))
	db, user := setupToolMarketBuiltinControllerTest(t)
	baseline := walletLegacyGoldenInputs(t)
	require.NoError(t, model.EnsureToolMarketBuiltinServices(baseline))
	tool := builtinControllerTool(t, user.Id, "wallet", "wallet.balance")
	require.Equal(t, walletLegacyGoldenVersion, tool.VersionID)
	grant := builtinControllerGrant(t, user.Id, "legacy-wallet-client", tool)
	before, err := model.GetToolMarketDetail(user.Id, model.ToolMarketBuiltinServiceID("wallet"), false)
	require.NoError(t, err)
	require.NoError(t, EnsureToolMarketBuiltinCatalog(context.Background()))
	require.NoError(t, model.VerifyToolMarketBuiltinServices(context.Background(), baseline), "the old binary's exact descriptor verifier must still pass")
	stored, err := model.GetToolMarketDetail(user.Id, before.Service.ID, false)
	require.NoError(t, err)
	require.Equal(t, before, stored)
	var retained model.ToolMarketGrant
	require.NoError(t, db.First(&retained, "id = ?", grant.ID).Error)
	require.Equal(t, *grant, retained)
	_, err = model.GetToolMarketExecution(user.Id, "legacy-wallet-client", tool.ToolID, walletLegacyGoldenVersion, grant.ID)
	require.NoError(t, err, "an existing exact old-version grant remains valid")
	presented, err := walletCurrentCatalogPresentation(context.Background(), stored)
	require.NoError(t, err)
	assertWalletCurrentDocs(t, presented.Tools)
	require.Equal(t, stored.Version, presented.Version)
	for i, row := range stored.Tools {
		copy := presented.Tools[i]
		copy.Description, copy.InputSchema = row.Description, row.InputSchema
		require.Equal(t, row, copy, "presentation must retain all authority and snapshot fields")
	}
	// Explicitly exercise mutable leaves and private struct fields without a
	// JSON round-trip, which would silently discard json:"-" fields.
	stored.AllowedUsers = []int{user.Id}
	stored.Version.ValidationDigest = "private-version-value"
	stored.Tools[0].RemoteDigest = "private-tool-value"
	stored.Tools[0].BillingRules = []model.ToolMarketBillingRule{{Metric: "images", RateQuota: 10, MaxQuantity: 2}}
	stored.Tools[0].AvailableMeteringMetrics = []string{"images"}
	detached, err := walletCurrentCatalogPresentation(context.Background(), stored)
	require.NoError(t, err)
	require.Equal(t, stored.Version.ValidationDigest, detached.Version.ValidationDigest)
	require.Equal(t, stored.Tools[0].RemoteDigest, detached.Tools[0].RemoteDigest)
	detached.AllowedUsers[0] = 999
	detached.Tools[0].BillingRules[0].RateQuota = 999
	detached.Tools[0].AvailableMeteringMetrics[0] = "changed"
	require.Equal(t, user.Id, stored.AllowedUsers[0])
	require.Equal(t, 10, stored.Tools[0].BillingRules[0].RateQuota)
	require.Equal(t, "images", stored.Tools[0].AvailableMeteringMetrics[0])
	wrong := *before
	wrong.Version.ID = "61b3fae7-7f75-5d32-9e68-dce0cdbc1b30"
	_, err = walletCurrentCatalogPresentation(context.Background(), &wrong)
	require.ErrorIs(t, err, model.ErrToolMarketConflict)

	engine := gin.New()
	engine.Use(func(c *gin.Context) { c.Set("id", user.Id); c.Next() })
	engine.GET("/api/tool-market/services/:id", GetToolMarket)
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/tool-market/services/"+before.Service.ID, nil))
	require.Equal(t, http.StatusOK, w.Code)
	var public struct {
		Success bool
		Data    model.ToolMarketDetail
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &public))
	require.True(t, public.Success)
	assertWalletCurrentDocs(t, public.Data.Tools)
	require.Equal(t, walletLegacyGoldenVersion, public.Data.Version.ID)

	topup := builtinControllerTool(t, user.Id, "wallet", "wallet.topup_link")
	transfer := builtinControllerTool(t, user.Id, "wallet", "wallet.transfer.create")
	builtinControllerGrant(t, user.Id, "legacy-wallet-client", topup)
	builtinControllerGrant(t, user.Id, "legacy-wallet-client", transfer)
	token, _, err := model.CreateToolMarketToken(user.Id, "legacy-wallet-client", true, false, common.GetTimestamp()+3600)
	require.NoError(t, err)
	server := httptest.NewServer(NewToolMarketMCPHandler())
	t.Cleanup(server.Close)
	client := mcp.NewClient(&mcp.Implementation{Name: "wallet-compat-test", Version: "1"}, &mcp.ClientOptions{MultiRoundTrip: &mcp.MultiRoundTripOptions{Disabled: true}})
	session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: server.URL, HTTPClient: &http.Client{Transport: openSourceBountyBearerTransport{token: token}}, DisableStandaloneSSE: true, MaxRetries: -1}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })
	list, err := session.ListTools(context.Background(), nil)
	require.NoError(t, err)
	require.Len(t, list.Tools, 6)
	for _, descriptor := range list.Tools {
		raw, err := json.Marshal(descriptor.InputSchema)
		require.NoError(t, err)
		switch descriptor.Name {
		case "market_tool_" + strings.ReplaceAll(topup.ToolID, "-", ""):
			require.Contains(t, descriptor.Description, "whole legacy batch amount")
			require.Contains(t, string(raw), "This is not raw wallet credits or USD")
		case "market_tool_" + strings.ReplaceAll(transfer.ToolID, "-", ""):
			require.Contains(t, string(raw), "One credit is one raw quota unit")
		}
	}
	details, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "lmm_market_details", Arguments: map[string]any{"service_id": before.Service.ID}})
	require.NoError(t, err)
	require.False(t, details.IsError)
	encoded, err := json.Marshal(details.StructuredContent)
	require.NoError(t, err)
	var mcpDetail model.ToolMarketDetail
	require.NoError(t, json.Unmarshal(encoded, &mcpDetail))
	assertWalletCurrentDocs(t, mcpDetail.Tools)
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "market_tool_" + strings.ReplaceAll(tool.ToolID, "-", ""), Arguments: map[string]any{"request_id": "old-grant-balance", "arguments": map[string]any{}}})
	require.NoError(t, err)
	require.False(t, result.IsError, "the old granted tool dispatches successfully under the unchanged version")
	balance := walletMCPData(t, result)
	require.EqualValues(t, 1, balance["credit_unit"])
	require.EqualValues(t, 500000, balance["quota_per_platform_credit"])
	require.Equal(t, "LEGACY", balance["quota_per_platform_credit_unit"])
	require.Equal(t, "500000", balance["credits_per_usd"])
	require.InDelta(t, 10000.0/500000, balance["available_usd"], 1e-16)
	after, err := model.GetToolMarketDetail(user.Id, before.Service.ID, false)
	require.NoError(t, err)
	require.Equal(t, before, after, "public presentation and read-only invocation never rewrite immutable catalog rows")
	var account model.User
	require.NoError(t, db.First(&account, user.Id).Error)
	require.Equal(t, 10000, account.Quota)
}

func TestWalletLegacyAliasRejectsUnrepresentablePositiveQWithoutChangingCredits(t *testing.T) {
	db, user, _ := setupWalletMCPTest(t)
	session := walletMCPTestSession(t, user.Id, walletMCPTestExtra("a"))
	require.NoError(t, common.SetCreditCurrencyBasis(decimal.NewFromInt(500000), decimal.RequireFromString("1e-500")))
	require.True(t, walletMCPCall(t, session, "wallet.balance", map[string]any{}, "").IsError, "a positive frozen legacy Q must not become a fabricated zero alias")
	var stored model.User
	require.NoError(t, db.First(&stored, user.Id).Error)
	require.Equal(t, 1000, stored.Quota)
	require.NoError(t, common.SetCreditCurrencyBasis(decimal.NewFromInt(500000), decimal.RequireFromString("0.5")))
	persistCreditDenominationFixture(t, db)
	data := walletMCPData(t, walletMCPCall(t, session, "wallet.balance", map[string]any{}, ""))
	require.Equal(t, 0.5, data["quota_per_platform_credit"], "representable positive fractional legacy Q remains valid")
	require.EqualValues(t, 1, data["credit_unit"])
	require.Equal(t, "500000", data["credits_per_usd"])
	require.InDelta(t, 1000.0/500000, data["available_usd"], 1e-16)
}
