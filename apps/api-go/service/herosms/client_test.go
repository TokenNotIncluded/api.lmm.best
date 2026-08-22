package herosms

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestHTTPClientEndpointsMatchHeroSMSEmailsContract(t *testing.T) {
	var deleteCalled atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		require.Equal(t, "test-key", request.Header.Get("ApiKey"))
		writer.Header().Set("Content-Type", "application/json")
		switch request.Method + " " + request.URL.Path {
		case http.MethodGet + " /emails/domains":
			require.Equal(t, "telegram.com", request.URL.Query().Get("site"))
			require.Empty(t, request.URL.Query().Get("page"))
			require.Empty(t, request.URL.Query().Get("size"))
			_ = json.NewEncoder(writer).Encode(map[string]any{"data": []map[string]any{{"name": "mail.test", "cost": 1.23, "count": 3}}})
		case http.MethodGet + " /emails":
			require.Equal(t, "1", request.URL.Query().Get("page"))
			require.Equal(t, "10", request.URL.Query().Get("size"))
			_ = json.NewEncoder(writer).Encode(map[string]any{"data": []map[string]any{{"id": 123, "email": "a@mail.test", "site": "telegram.com", "status": 3, "date": "2026-08-22T00:00:00Z"}}})
		case http.MethodPost + " /emails":
			body, err := io.ReadAll(request.Body)
			require.NoError(t, err)
			require.JSONEq(t, `{"site":"telegram.com","domain":"mail.test"}`, string(body))
			writer.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(writer).Encode(map[string]any{"status": true, "data": map[string]any{"id": 123, "site": "telegram.com", "email": "a@mail.test", "status": 3, "cost": 1.23, "currency": 840, "value": "123456", "message": "ok"}})
		case http.MethodPost + " /emails/batch":
			body, err := io.ReadAll(request.Body)
			require.NoError(t, err)
			require.JSONEq(t, `{"site":"telegram.com","domain":"mail.test","count":2}`, string(body))
			writer.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(writer).Encode(map[string]any{"status": true, "data": []map[string]any{{"site": "telegram.com", "domain": "mail.test", "email": "a@mail.test", "status": 1, "cost": 1.23}, {"site": "telegram.com", "domain": "mail.test", "email": "b@mail.test", "status": 1, "cost": 1.23}}})
		case http.MethodGet + " /emails/123":
			_ = json.NewEncoder(writer).Encode(map[string]any{"status": true, "data": map[string]any{"id": 123, "site": "telegram.com", "email": "a@mail.test", "status": 5, "cost": 1.23, "currency": 840, "value": "123456", "message": "ok"}})
		case http.MethodDelete + " /emails/123":
			deleteCalled.Store(true)
			writer.WriteHeader(http.StatusNoContent)
		case http.MethodPost + " /emails/123/reorder":
			writer.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(writer).Encode(map[string]any{"status": true, "data": map[string]any{"id": 124, "site": "telegram.com", "email": "c@mail.test", "status": 3, "cost": 1.23, "currency": 840}})
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-key")
	domains, err := client.ListDomains(context.Background(), "telegram.com")
	require.NoError(t, err)
	require.Len(t, domains.Data, 1)
	require.Equal(t, "mail.test", domains.Data[0].Name)
	require.Equal(t, 3, domains.Data[0].Count)
	require.Equal(t, "1.23", domains.Data[0].CostUSD.String())

	emails, err := client.ListEmails(context.Background(), 1, 10)
	require.NoError(t, err)
	require.Len(t, emails.Data, 1)
	require.Equal(t, "123", emails.Data[0].ID)
	require.Equal(t, "telegram.com", emails.Data[0].Site)

	single, err := client.CreateEmail(context.Background(), "telegram.com", "mail.test")
	require.NoError(t, err)
	require.Equal(t, "123", single.ID)
	require.Equal(t, 840, single.CurrencyCode)
	require.Equal(t, "123456", single.Code)

	batch, err := client.CreateEmailBatch(context.Background(), "telegram.com", "mail.test", 2)
	require.NoError(t, err)
	require.Len(t, batch.Items, 2)
	require.Equal(t, "a@mail.test", batch.Items[0].Email)
	require.Empty(t, batch.Items[0].ID)

	detail, err := client.GetEmail(context.Background(), "123")
	require.NoError(t, err)
	require.Equal(t, "123456", detail.Code)

	require.NoError(t, client.DeleteEmail(context.Background(), "123"))
	require.True(t, deleteCalled.Load())

	reorder, err := client.ReorderEmail(context.Background(), "123")
	require.NoError(t, err)
	require.Equal(t, "124", reorder.ID)
}

func TestHTTPClientErrorMappingAndSafety(t *testing.T) {
	t.Run("status mapping", func(t *testing.T) {
		cases := []struct {
			status int
			want   error
		}{
			{status: http.StatusUnauthorized, want: ErrUnauthorized},
			{status: http.StatusNotFound, want: ErrNotFound},
			{status: http.StatusUnprocessableEntity, want: ErrInvalidRequest},
			{status: http.StatusTooManyRequests, want: ErrRateLimited},
			{status: http.StatusInternalServerError, want: ErrUpstreamBusy},
		}
		for _, tc := range cases {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.WriteHeader(tc.status)
				_, _ = writer.Write([]byte(`{"ignored":true}`))
			}))
			client := NewClient(server.URL, "test-key")
			_, err := client.ListDomains(context.Background(), "")
			require.ErrorIs(t, err, tc.want)
			server.Close()
		}
	})

	t.Run("malformed json", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"data":[`))
		}))
		defer server.Close()
		client := NewClient(server.URL, "test-key")
		_, err := client.ListDomains(context.Background(), "")
		require.ErrorIs(t, err, ErrBadResponse)
	})

	t.Run("response body capped", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"data":"` + strings.Repeat("x", int(defaultBodyLimit)) + `"}`))
		}))
		defer server.Close()
		client := NewClient(server.URL, "test-key")
		_, err := client.ListDomains(context.Background(), "")
		require.ErrorIs(t, err, ErrBadResponse)
	})

	t.Run("redirect not followed", func(t *testing.T) {
		targetHits := atomic.Int32{}
		target := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			targetHits.Add(1)
			writer.WriteHeader(http.StatusOK)
		}))
		defer target.Close()
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			http.Redirect(writer, request, target.URL, http.StatusFound)
		}))
		defer server.Close()
		client := NewClient(server.URL, "test-key")
		_, err := client.ListDomains(context.Background(), "")
		require.ErrorIs(t, err, ErrBadResponse)
		require.Zero(t, targetHits.Load())
	})

	t.Run("timeout", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			time.Sleep(100 * time.Millisecond)
			_, _ = writer.Write([]byte(`{"data":[]}`))
		}))
		defer server.Close()
		client := NewClient(server.URL, "test-key")
		client.timeout = 10 * time.Millisecond
		_, err := client.ListDomains(context.Background(), "")
		require.ErrorIs(t, err, ErrUpstreamTimeout)
	})

	t.Run("exact email search", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(writer).Encode(map[string]any{"data": []map[string]any{{"id": 123, "email": "b@mail.test", "site": "telegram.com", "status": 3}}})
		}))
		defer server.Close()
		client := NewClient(server.URL, "test-key")
		item, err := FindEmailByExactAddress(context.Background(), client, "b@mail.test")
		require.NoError(t, err)
		require.NotNil(t, item)
		require.Equal(t, "123", item.ID)
	})
}

func TestHTTPClientDelete404(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()
	client := NewClient(server.URL, "test-key")
	err := client.DeleteEmail(context.Background(), "missing")
	require.True(t, errors.Is(err, ErrNotFound))
}
