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

func TestHTTPClientEndpoints(t *testing.T) {
	var deleteCalled atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		require.Equal(t, "test-key", request.Header.Get("ApiKey"))
		writer.Header().Set("Content-Type", "application/json")
		switch request.Method + " " + request.URL.Path {
		case http.MethodGet + " /emails/domains":
			require.Equal(t, "2", request.URL.Query().Get("page"))
			require.Equal(t, "5", request.URL.Query().Get("size"))
			require.Equal(t, "demo", request.URL.Query().Get("site"))
			_ = json.NewEncoder(writer).Encode(map[string]any{"data": []map[string]any{{"id": "d1", "domain": "mail.test", "site": "demo", "stock": 3, "cost": "1.23", "currency": "USD", "currency_code": 840}}, "page": 2, "size": 5, "total": 1})
		case http.MethodGet + " /emails":
			require.Equal(t, "1", request.URL.Query().Get("page"))
			require.Equal(t, "10", request.URL.Query().Get("size"))
			_ = json.NewEncoder(writer).Encode(map[string]any{"data": []map[string]any{{"id": "e1", "email": "a@mail.test"}}, "page": 1, "size": 10, "total": 1})
		case http.MethodPost + " /emails":
			body, err := io.ReadAll(request.Body)
			require.NoError(t, err)
			require.JSONEq(t, `{"id":"d1"}`, string(body))
			_ = json.NewEncoder(writer).Encode(map[string]any{"id": "e1", "email": "a@mail.test", "cost": "1.23", "currency": "USD", "currency_code": 840, "domain_id": "d1", "status": "active"})
		case http.MethodPost + " /emails/batch":
			body, err := io.ReadAll(request.Body)
			require.NoError(t, err)
			require.JSONEq(t, `{"id":"d1","amount":2}`, string(body))
			_ = json.NewEncoder(writer).Encode(map[string]any{"data": []map[string]any{{"email": "a@mail.test"}, {"id": "e2", "email": "b@mail.test", "cost": "1.23", "currency": "USD", "currency_code": 840, "status": "active"}}})
		case http.MethodGet + " /emails/e1":
			_ = json.NewEncoder(writer).Encode(map[string]any{"id": "e1", "email": "a@mail.test", "cost": "1.23", "currency": "USD", "currency_code": 840, "code": "123456", "message": "ok"})
		case http.MethodDelete + " /emails/e1":
			deleteCalled.Store(true)
			writer.WriteHeader(http.StatusNoContent)
		case http.MethodPost + " /emails/e1/reorder":
			_ = json.NewEncoder(writer).Encode(map[string]any{"id": "e3", "email": "c@mail.test", "cost": "1.23", "currency": "USD", "currency_code": 840})
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-key")
	domains, err := client.ListDomains(context.Background(), 2, 5, "demo")
	require.NoError(t, err)
	require.Len(t, domains.Data, 1)
	require.Equal(t, "d1", domains.Data[0].ID)

	emails, err := client.ListEmails(context.Background(), 1, 10)
	require.NoError(t, err)
	require.Len(t, emails.Data, 1)

	single, err := client.CreateEmail(context.Background(), "d1")
	require.NoError(t, err)
	require.Equal(t, "e1", single.ID)

	batch, err := client.CreateEmailBatch(context.Background(), "d1", 2)
	require.NoError(t, err)
	require.Len(t, batch.Items, 2)
	require.Equal(t, "a@mail.test", batch.Items[0].Email)
	require.Empty(t, batch.Items[0].ID)

	detail, err := client.GetEmail(context.Background(), "e1")
	require.NoError(t, err)
	require.Equal(t, "123456", detail.Code)

	require.NoError(t, client.DeleteEmail(context.Background(), "e1"))
	require.True(t, deleteCalled.Load())

	reorder, err := client.ReorderEmail(context.Background(), "e1")
	require.NoError(t, err)
	require.Equal(t, "e3", reorder.ID)
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
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				writer.WriteHeader(tc.status)
				_, _ = writer.Write([]byte(`{"ignored":true}`))
			}))
			client := NewClient(server.URL, "test-key")
			_, err := client.ListDomains(context.Background(), 1, 1, "")
			require.ErrorIs(t, err, tc.want)
			server.Close()
		}
	})

	t.Run("malformed json", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"data":[`))
		}))
		defer server.Close()
		client := NewClient(server.URL, "test-key")
		_, err := client.ListDomains(context.Background(), 1, 1, "")
		require.ErrorIs(t, err, ErrBadResponse)
	})

	t.Run("response body capped", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"data":"` + strings.Repeat("x", int(defaultBodyLimit)) + `"}`))
		}))
		defer server.Close()
		client := NewClient(server.URL, "test-key")
		_, err := client.ListDomains(context.Background(), 1, 1, "")
		require.ErrorIs(t, err, ErrBadResponse)
	})

	t.Run("redirect not followed", func(t *testing.T) {
		targetHits := atomic.Int32{}
		target := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			targetHits.Add(1)
			writer.WriteHeader(http.StatusOK)
		}))
		defer target.Close()
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			http.Redirect(writer, request, target.URL, http.StatusFound)
		}))
		defer server.Close()
		client := NewClient(server.URL, "test-key")
		_, err := client.ListDomains(context.Background(), 1, 1, "")
		require.ErrorIs(t, err, ErrBadResponse)
		require.Zero(t, targetHits.Load())
	})

	t.Run("timeout", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			time.Sleep(100 * time.Millisecond)
			_, _ = writer.Write([]byte(`{"data":[]}`))
		}))
		defer server.Close()
		client := NewClient(server.URL, "test-key")
		client.timeout = 10 * time.Millisecond
		_, err := client.ListDomains(context.Background(), 1, 1, "")
		require.ErrorIs(t, err, ErrUpstreamTimeout)
	})

	t.Run("exact email search", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			_ = json.NewEncoder(writer).Encode(map[string]any{"data": []map[string]any{{"id": "e2", "email": "b@mail.test"}}, "page": 2, "size": 100, "total": 101})
		}))
		defer server.Close()
		client := NewClient(server.URL, "test-key")
		item, err := FindEmailByExactAddress(context.Background(), client, "b@mail.test")
		require.NoError(t, err)
		require.NotNil(t, item)
		require.Equal(t, "e2", item.ID)
	})
}

func TestHTTPClientDelete404(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()
	client := NewClient(server.URL, "test-key")
	err := client.DeleteEmail(context.Background(), "missing")
	require.True(t, errors.Is(err, ErrNotFound))
}
