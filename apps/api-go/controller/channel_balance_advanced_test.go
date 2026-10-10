package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/constant"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func balanceTestPointer[T any](value T) *T { return &value }

func advancedBalanceFixture(t *testing.T, endpoint string, config *dto.AdvancedCustomBalanceConfig) (*model.Channel, *gorm.DB) {
	t.Helper()
	previousDB, previousLogDB := model.DB, model.LOG_DB
	previousMain, previousLog := common.MainDatabaseType(), common.LogDatabaseType()
	t.Cleanup(func() {
		model.DB, model.LOG_DB = previousDB, previousLogDB
		common.SetDatabaseTypes(previousMain, previousLog)
	})
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "balance.sqlite")), &gorm.Config{})
	require.NoError(t, err)
	model.DB = db
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	require.NoError(t, db.AutoMigrate(&model.Channel{}))
	settings, err := json.Marshal(dto.ChannelOtherSettings{AdvancedCustom: &dto.AdvancedCustomConfig{Routes: []dto.AdvancedCustomRoute{
		{IncomingPath: dto.AdvancedCustomBalancePath, UpstreamPath: endpoint, Balance: config},
	}}})
	require.NoError(t, err)
	channel := &model.Channel{Id: 7, Type: constant.ChannelTypeAdvancedCustom, Key: "private-balance-key", BaseURL: balanceTestPointer("http://unused.invalid"), OtherSettings: string(settings), Balance: 42.5, BalanceUpdatedTime: 123}
	require.NoError(t, db.Create(channel).Error)

	return channel, db
}

func requireBalanceUnchanged(t *testing.T, db *gorm.DB) {
	t.Helper()
	var persisted model.Channel
	require.NoError(t, db.First(&persisted, 7).Error)
	require.Equal(t, 42.5, persisted.Balance)
	require.EqualValues(t, 123, persisted.BalanceUpdatedTime)
}

func TestAdvancedCustomBalanceGETCompatibilityAndUnknownJSON(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		updated    bool
	}{
		{"OpenAI credit summary", `{"object":"credit_summary","total_available":12.5}`, true},
		{"unknown valid JSON", `{"account":{"remaining":12.5}}`, false},
		{"legacy invalid credit summary remains raw", `{"object":"credit_summary","total_available":-1}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requests := make(chan *http.Request, 2)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests <- r; _, _ = io.WriteString(w, tc.body) }))
			defer server.Close()
			channel, db := advancedBalanceFixture(t, server.URL+"/balance", nil)
			result, err := fetchAdvancedCustomBalance(context.Background(), channel)
			require.NoError(t, err)
			request := <-requests
			require.Equal(t, "GET", request.Method)
			require.Equal(t, "Bearer private-balance-key", request.Header.Get("Authorization"))
			if tc.updated {
				require.Equal(t, 12.5, result.Balance)
				var persisted model.Channel
				require.NoError(t, db.First(&persisted, 7).Error)
				require.Equal(t, 12.5, persisted.Balance)
				require.Greater(t, persisted.BalanceUpdatedTime, int64(123))
			} else {
				require.JSONEq(t, tc.body, result.RawResponse)
				requireBalanceUnchanged(t, db)
			}
		})
	}
}

func TestAdvancedCustomBalancePOSTExtractsConfiguredJSONWithoutChangingInferenceType(t *testing.T) {
	type captured struct{ method, path, body, contentType string }
	requests := make(chan captured, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		requests <- captured{r.Method, r.URL.Path, string(body), r.Header.Get("Content-Type")}
		_, _ = io.WriteString(w, `{"a/b":{"m~n":[1250]}}`)
	}))
	defer server.Close()
	template := `{"key":"{api_key}"}`
	channel, db := advancedBalanceFixture(t, server.URL+"/account/balance", &dto.AdvancedCustomBalanceConfig{Method: "POST", BodyTemplate: &template, JSONPointer: "/a~1b/m~0n/0", Scale: balanceTestPointer(0.01)})
	result, err := fetchAdvancedCustomBalance(context.Background(), channel)
	require.NoError(t, err)
	require.Equal(t, 12.5, result.Balance)
	request := <-requests
	require.Equal(t, "POST", request.method)
	require.Equal(t, "/account/balance", request.path)
	require.Equal(t, "application/json", request.contentType)
	require.JSONEq(t, `{"key":"private-balance-key"}`, request.body)
	var persisted model.Channel
	require.NoError(t, db.First(&persisted, 7).Error)
	require.Equal(t, constant.ChannelTypeAdvancedCustom, persisted.Type)
	require.Equal(t, 12.5, persisted.Balance)
}

func TestAdvancedCustomBalancePOSTWithoutBodyAndInvalidTemplateDoNotInventPayloads(t *testing.T) {
	var calls atomic.Int64
	requests := make(chan string, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		body, _ := io.ReadAll(r.Body)
		requests <- r.Method + ":" + string(body)
		_, _ = io.WriteString(w, `{"balance":0}`)
	}))
	defer server.Close()
	channel, db := advancedBalanceFixture(t, server.URL, &dto.AdvancedCustomBalanceConfig{Method: "POST", JSONPointer: "/balance"})
	result, err := fetchAdvancedCustomBalance(context.Background(), channel)
	require.NoError(t, err)
	require.Equal(t, "POST:", <-requests)
	require.Zero(t, result.Balance, "zero is a valid measured balance")
	var persisted model.Channel
	require.NoError(t, db.First(&persisted, 7).Error)
	require.Zero(t, persisted.Balance)
	for _, template := range []string{"", `{"key":{api_key}}`, `"` + strings.Repeat("x", dto.MaxAdvancedCustomBalanceBodyBytes) + `"`} {
		settings := channel.GetOtherSettings()
		settings.AdvancedCustom.Routes[0].Balance.BodyTemplate = &template
		encoded, err := json.Marshal(settings)
		require.NoError(t, err)
		channel.OtherSettings = string(encoded)
		_, err = fetchAdvancedCustomBalance(context.Background(), channel)
		require.Error(t, err)
		require.NotContains(t, err.Error(), channel.Key)
		require.EqualValues(t, 1, calls.Load(), "invalid templates must fail before egress")
		require.NoError(t, db.First(&persisted, 7).Error)
		require.Zero(t, persisted.Balance)
	}
}

func TestAdvancedCustomBalanceMalformedSettingsNeverRepairOrOverwritePersistedChannel(t *testing.T) {
	for _, field := range []string{"settings", "setting", "header_override"} {
		t.Run(field, func(t *testing.T) {
			var calls atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				_, _ = io.WriteString(w, `{"balance":999}`)
			}))
			defer server.Close()
			channel, db := advancedBalanceFixture(t, server.URL, &dto.AdvancedCustomBalanceConfig{JSONPointer: "/balance"})
			malformed := `{"private-template-secret":`
			require.NoError(t, db.Model(&model.Channel{}).Where("id = ?", 7).Update(field, malformed).Error)
			switch field {
			case "settings":
				channel.OtherSettings = malformed
			case "setting":
				channel.Setting = &malformed
			case "header_override":
				channel.HeaderOverride = &malformed
			}
			channel.Balance, channel.BalanceUpdatedTime = 999, 999 // stale in-memory snapshot must never be saved
			_, err := fetchAdvancedCustomBalance(context.Background(), channel)
			require.Error(t, err)
			require.NotContains(t, err.Error(), "private-template-secret")
			require.Zero(t, calls.Load())
			requireBalanceUnchanged(t, db)
			var persisted string
			require.NoError(t, db.Model(&model.Channel{}).Where("id = ?", 7).Select(field).Scan(&persisted).Error)
			require.Equal(t, malformed, persisted)
		})
	}
}

func TestAdvancedCustomBalanceInvalidExtractionNeverOverwritesAndNeverLeaksResponse(t *testing.T) {
	for _, body := range []string{
		`{"balance":"private-response-secret"}`, `{"balance":null}`, `{"balance":true}`, `{"other":1}`, `{"balance":-1}`, `{"balance":1e400}`, `{"balance":1,"balance":999}`, `{"balance":{}}`, `private-response-secret`,
	} {
		t.Run(body, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, body) }))
			defer server.Close()
			channel, db := advancedBalanceFixture(t, server.URL, &dto.AdvancedCustomBalanceConfig{JSONPointer: "/balance"})
			_, err := fetchAdvancedCustomBalance(context.Background(), channel)
			require.Error(t, err)
			require.NotContains(t, err.Error(), "private-response-secret")
			require.NotContains(t, err.Error(), channel.Key)
			requireBalanceUnchanged(t, db)
		})
	}
	for _, tc := range []struct {
		body, pointer string
		scale         float64
	}{
		{`{"a":{"balance":1},"a":{"balance":2}}`, "/a/balance", 1},
		{`{"a":[12]}`, "/a/01", 1}, {`{"a":[12]}`, "/a/-", 1}, {`{"a":[12]}`, "/a/18446744073709551616", 1},
		{`{"balance":1e308}`, "/balance", 100},
	} {
		_, err := extractAdvancedCustomBalance(json.RawMessage(tc.body), &dto.AdvancedCustomBalanceConfig{JSONPointer: tc.pointer, Scale: &tc.scale})
		require.Error(t, err)
	}
}

func TestAdvancedCustomBalanceRedirectAndHostRestrictionsDoNotForwardCredentials(t *testing.T) {
	var originCalls, destinationCalls atomic.Int64
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		destinationCalls.Add(1)
		_, _ = io.WriteString(w, `{"balance":999}`)
	}))
	defer destination.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		originCalls.Add(1)
		http.Redirect(w, r, destination.URL, http.StatusTemporaryRedirect)
	}))
	defer origin.Close()
	channel, db := advancedBalanceFixture(t, origin.URL, &dto.AdvancedCustomBalanceConfig{Method: "POST", BodyTemplate: balanceTestPointer(`{"key":"{api_key}"}`), JSONPointer: "/balance"})
	_, err := fetchAdvancedCustomBalance(context.Background(), channel)
	require.Error(t, err)
	require.EqualValues(t, 1, originCalls.Load())
	require.Zero(t, destinationCalls.Load())
	requireBalanceUnchanged(t, db)
	channel.HeaderOverride = balanceTestPointer(`{"Host":"other.invalid"}`)
	_, err = fetchAdvancedCustomBalance(context.Background(), channel)
	require.Error(t, err)
	require.EqualValues(t, 1, originCalls.Load(), "Host mismatch must fail before any request")
	requireBalanceUnchanged(t, db)
	for _, endpoint := range []string{"http://user:secret@127.0.0.1/balance", origin.URL + "/balance#fragment"} {
		require.Error(t, validateAdvancedCustomBalanceDestination(endpoint, http.Header{}))
	}
}

func TestAdvancedCustomBalanceResponseLimitAndDeadlinePreserveOldBalance(t *testing.T) {
	for _, slow := range []bool{false, true} {
		t.Run(fmt.Sprint(slow), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if slow {
					_, _ = io.WriteString(w, `{"balance":`)
					w.(http.Flusher).Flush()
					<-r.Context().Done()
				} else {
					_, _ = io.WriteString(w, `{"balance":1,"padding":"`+strings.Repeat("x", maxAdvancedCustomBalanceResponseBytes)+`"}`)
				}
			}))
			defer server.Close()
			channel, db := advancedBalanceFixture(t, server.URL, &dto.AdvancedCustomBalanceConfig{JSONPointer: "/balance"})
			started := time.Now()
			_, err := fetchAdvancedCustomBalance(context.Background(), channel)
			require.Error(t, err)
			require.Less(t, time.Since(started), 8*time.Second)
			requireBalanceUnchanged(t, db)
		})
	}
}
