package controller

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func financeExportTestContext(query string) *gin.Context {
	gin.SetMode(gin.TestMode)
	req := httptest.NewRequest("GET", "/api/finance/export"+query, nil)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = req
	return c
}

func TestParseFinanceExportWindowDefaultsAndLimits(t *testing.T) {
	start, end, err := parseFinanceExportWindow(financeExportTestContext(""))
	require.NoError(t, err)
	require.InDelta(t, financeExportDefaultWindow, end-start, 2)

	_, _, err = parseFinanceExportWindow(financeExportTestContext("?start_timestamp=20&end_timestamp=10"))
	require.ErrorContains(t, err, "start_timestamp must be before end_timestamp")

	_, _, err = parseFinanceExportWindow(financeExportTestContext("?start_timestamp=1&end_timestamp=31536002"))
	require.ErrorContains(t, err, "cannot exceed")
}

func TestParseFinanceExportWindowAcceptsExactOneYearRange(t *testing.T) {
	start, end, err := parseFinanceExportWindow(financeExportTestContext("?start_timestamp=1700000000&end_timestamp=1731536000"))
	require.NoError(t, err)
	require.EqualValues(t, 1700000000, start)
	require.EqualValues(t, 1731536000, end)
}

func TestFinanceExportFilesAreRedactedAndClipboardFriendly(t *testing.T) {
	bundle := financeExportBundle{
		Manifest: financeExportManifest{
			SchemaVersion: "test",
			Rows:          map[string]int{"users": 1},
			Truncated:     map[string]bool{},
		},
		Options: map[string]string{
			"ModelPrice":                `{"gpt-test": 1.25}`,
			"ModelRatio":                `{"gpt-test": 2}`,
			"GroupRatio":                `{"default": 0.8}`,
			"TopupGroupRatio":           `{"default": 1.1}`,
			"tool_price_setting.prices": `{"web_search": 3}`,
		},
		Users:  []financeUserExport{{ID: 7, Username: "admin", Group: "default", Quota: 100, UsedQuota: 25}},
		TopUps: []financeTopUpExport{{ID: 1, Amount: 10, PaymentProvider: "stripe"}},
		Usage:  []financeUsageExport{{ID: 2, UserID: 7, Quota: 5}},
	}

	documents := financeDocuments(bundle)
	applyFinanceUserRatios(bundle.Users, bundle.Options)
	var users bytes.Buffer
	require.NoError(t, writeFinanceJSON(&users, bundle.Users))
	require.Contains(t, users.String(), `"effective_group_ratio":0.8`)
	require.Contains(t, users.String(), `"effective_topup_group_ratio":1.1`)
	names := make(map[string]bool, len(documents))
	for _, document := range documents {
		names[document.Name] = true
	}
	for _, name := range []string{
		"manifest.json",
		"financial-options.json",
		"model-prices-and-ratios.json",
		"effective-model-pricing.json",
		"users-balances.json",
		"channels-pricing.json",
		"subscription-plans.json",
		"topups.json",
		"subscription-orders.json",
		"subscription-payment-events.json",
		"usage-billing-records.json",
		"bounty-ledger.json",
		"checkins.json",
		"redemptions.json",
		"user-subscriptions.json",
	} {
		require.True(t, names[name])
	}
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	require.NoError(t, writeFinanceText(context, documents))
	text := recorder.Body.String()
	require.Contains(t, text, "users-balances.json")
	require.Contains(t, text, `"gpt-test": 1.25`)
	require.NotContains(t, text, "provider_event_id")
	require.NotContains(t, text, "provider_payload")
}

func TestDecodeFinanceOptionPreservesNonJSONValues(t *testing.T) {
	require.Equal(t, "not-json", decodeFinanceOption("not-json"))
	require.Equal(t, map[string]any{"model": float64(2)}, decodeFinanceOption(`{"model":2}`))
}

func TestApplyFinanceUserRatiosUsesTheUserGroup(t *testing.T) {
	users := []financeUserExport{
		{Group: "default"},
		{Group: "vip"},
		{Group: "missing"},
	}
	applyFinanceUserRatios(users, map[string]string{
		"GroupRatio":      `{"default":1,"vip":0.8}`,
		"TopupGroupRatio": `{"default":1.1,"vip":0.9}`,
	})
	require.NotNil(t, users[0].GroupRatio)
	require.Equal(t, 1.0, *users[0].GroupRatio)
	require.Equal(t, 1.1, *users[0].TopupGroupRatio)
	require.Equal(t, 0.8, *users[1].GroupRatio)
	require.Equal(t, 0.9, *users[1].TopupGroupRatio)
	require.Nil(t, users[2].GroupRatio)
	require.Nil(t, users[2].TopupGroupRatio)
}

func TestSanitizeFinanceBaseURLRemovesCredentialsAndQuery(t *testing.T) {
	raw := "https://user:secret@example.com/v1?token=should-not-export"
	sanitized := sanitizeFinanceBaseURL(&raw)
	require.NotNil(t, sanitized)
	require.Equal(t, "https://example.com", *sanitized)
	require.Nil(t, sanitizeFinanceBaseURL(nil))
}

func TestFinanceExportTextHasStableSectionOrder(t *testing.T) {
	documents := []financeDocument{
		{Name: "manifest.json", Value: "manifest"},
		{Name: "financial-options.json", Value: "options"},
		{Name: "model-prices-and-ratios.json", Value: "prices"},
		{Name: "effective-model-pricing.json", Value: "effective-prices"},
		{Name: "users-balances.json", Value: "users"},
		{Name: "channels-pricing.json", Value: "channels"},
		{Name: "subscription-plans.json", Value: "plans"},
		{Name: "topups.json", Value: "topups"},
		{Name: "subscription-orders.json", Value: "orders"},
		{Name: "usage-billing-records.json", Value: "usage"},
		{Name: "bounty-ledger.json", Value: "ledger"},
		{Name: "checkins.json", Value: "checkins"},
		{Name: "redemptions.json", Value: "redemptions"},
		{Name: "user-subscriptions.json", Value: "subscriptions"},
	}
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	require.NoError(t, writeFinanceText(context, documents))
	text := recorder.Body.String()
	require.True(t, strings.Index(text, "manifest.json") < strings.Index(text, "users-balances.json"))
	require.True(t, strings.Index(text, "users-balances.json") < strings.Index(text, "usage-billing-records.json"))
}

func TestFinanceDocumentsCanStreamUsersWithoutMaterializingRows(t *testing.T) {
	bundle := financeExportBundle{
		UserStream: func(writer io.Writer) error {
			_, err := io.WriteString(writer, "[\n{\"user_id\":1}\n]\n")
			return err
		},
	}
	var output bytes.Buffer
	document := financeDocuments(bundle)[4]
	require.Equal(t, "users-balances.json", document.Name)
	require.NoError(t, document.write(&output))
	require.Equal(t, "[\n{\"user_id\":1}\n]\n", output.String())
}

type financeExportStreamTestRow struct {
	ID    int    `json:"id" gorm:"column:id"`
	Value string `json:"value" gorm:"column:value"`
}

func TestStreamFinanceQueryJSONReadsRowsIncrementally(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.Exec("CREATE TABLE finance_export_stream_rows (id INTEGER PRIMARY KEY, value TEXT NOT NULL)").Error)
	require.NoError(t, db.Exec("INSERT INTO finance_export_stream_rows (id, value) VALUES (1, 'first'), (2, 'second')").Error)

	var output bytes.Buffer
	query := db.Table("finance_export_stream_rows").Select("id", "value").Order("id ASC")
	require.NoError(t, streamFinanceQueryJSON[financeExportStreamTestRow](&output, query))

	var rows []financeExportStreamTestRow
	require.NoError(t, json.Unmarshal(output.Bytes(), &rows))
	require.Equal(t, []financeExportStreamTestRow{{ID: 1, Value: "first"}, {ID: 2, Value: "second"}}, rows)
}

func TestStreamFinanceChannelsJSONRedactsURLs(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.Exec("CREATE TABLE finance_export_channels (id INTEGER PRIMARY KEY, name TEXT, base_url TEXT)").Error)
	require.NoError(t, db.Exec("INSERT INTO finance_export_channels (id, name, base_url) VALUES (1, 'primary', 'https://user:secret@example.com/v1?token=hidden')").Error)

	var output bytes.Buffer
	query := db.Table("finance_export_channels").Select("id", "name", "base_url").Order("id ASC")
	require.NoError(t, streamFinanceChannelsJSON(&output, query))
	require.Contains(t, output.String(), `"base_url":"https://example.com"`)
	require.NotContains(t, output.String(), "secret")
	require.NotContains(t, output.String(), "token=hidden")
}

func TestCountFinanceExportRowsReportsTheExportCapSeparately(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.Exec("CREATE TABLE finance_export_count_rows (id INTEGER PRIMARY KEY)").Error)
	require.NoError(t, db.Exec("INSERT INTO finance_export_count_rows (id) VALUES (1), (2)").Error)

	count, truncated, err := countFinanceExportRows(db.Table("finance_export_count_rows"))
	require.NoError(t, err)
	require.Equal(t, 2, count)
	require.False(t, truncated)
}

func TestWriteFinanceZipContainsStableFiles(t *testing.T) {
	documents := []financeDocument{
		{Name: "manifest.json", Value: "manifest"},
		{Name: "financial-options.json", Value: "options"},
		{Name: "model-prices-and-ratios.json", Value: "prices"},
		{Name: "effective-model-pricing.json", Value: "effective-prices"},
		{Name: "users-balances.json", Value: "users"},
		{Name: "channels-pricing.json", Value: "channels"},
		{Name: "subscription-plans.json", Value: "plans"},
		{Name: "topups.json", Value: "topups"},
		{Name: "subscription-orders.json", Value: "orders"},
		{Name: "usage-billing-records.json", Value: "usage"},
		{Name: "bounty-ledger.json", Value: "ledger"},
		{Name: "checkins.json", Value: "checkins"},
		{Name: "redemptions.json", Value: "redemptions"},
		{Name: "user-subscriptions.json", Value: "subscriptions"},
	}
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	require.NoError(t, writeFinanceZip(context, documents))
	require.Equal(t, "application/zip", recorder.Header().Get("Content-Type"))
	archive, err := zip.NewReader(bytes.NewReader(recorder.Body.Bytes()), int64(recorder.Body.Len()))
	require.NoError(t, err)
	require.Len(t, archive.File, len(documents))
	require.Equal(t, "manifest.json", archive.File[0].Name)
	require.Equal(t, "user-subscriptions.json", archive.File[len(archive.File)-1].Name)
}

func BenchmarkFinanceJSONStream(b *testing.B) {
	usage := make([]financeUsageExport, 20_000)
	for index := range usage {
		usage[index] = financeUsageExport{
			ID: index + 1, UserID: index%200 + 1, ModelName: "gpt-5.6-sol",
			PromptTokens: 2048, CompletionTokens: 512, Quota: 2560,
		}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if err := writeFinanceJSON(io.Discard, usage); err != nil {
			b.Fatal(err)
		}
	}
}
