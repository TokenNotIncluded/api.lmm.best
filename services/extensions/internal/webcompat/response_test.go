package webcompat

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules/store"
)

func responseBody(t *testing.T, w *httptest.ResponseRecorder) map[string]json.RawMessage {
	t.Helper()
	var body map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON: %v: %s", err, w.Body.String())
	}
	return body
}

func TestRespondPreservesSuccessEnvelopeIncludingNullData(t *testing.T) {
	for _, value := range []any{nil, map[string]string{"id": "order-one"}} {
		w := httptest.NewRecorder()
		Respond(w, value, nil)
		body := responseBody(t, w)
		if w.Code != 200 || string(body["success"]) != "true" || string(body["message"]) != `""` || body["data"] == nil || body["code"] != nil {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
		if w.Header().Get("Cache-Control") != "no-store" || !strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") {
			t.Fatal(w.Header())
		}
	}
}

func TestRespondMapsDomainErrorsWithoutLeakingRawErrorsOrData(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{
		{store.ErrUnauthorized, 401, "STORE_LOGIN_REQUIRED"},
		{store.ErrForbidden, 403, "STORE_ACCESS_DENIED"},
		{store.ErrNotFound, 404, "STORE_NOT_FOUND"},
		{store.ErrInvalid, 422, "STORE_INVALID_INPUT"},
		{store.ErrConflict, 409, "STORE_CONFLICT"},
		{store.ErrSoldOut, 409, "STORE_OUT_OF_STOCK"},
		{store.ErrPaymentUnavailable, 503, "STORE_PAYMENT_UNAVAILABLE"},
		{store.ErrUnavailable, 503, "STORE_UNAVAILABLE"},
		{errors.New("sql synthetic-provider-secret"), 500, "STORE_UNAVAILABLE"},
	} {
		t.Run(tc.code+tc.err.Error(), func(t *testing.T) {
			w := httptest.NewRecorder()
			Respond(w, "synthetic-provider-secret", fmt.Errorf("synthetic-provider-secret: %w", tc.err))
			body := responseBody(t, w)
			if w.Code != tc.status || string(body["success"]) != "false" || string(body["code"]) != `"`+tc.code+`"` || body["data"] != nil {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			if strings.Contains(w.Body.String(), "synthetic-provider-secret") || body["message"] == nil {
				t.Fatal(w.Body.String())
			}
		})
	}
}

func TestRespondPendingRemainsAnUnsuccessfulRecoverableResult(t *testing.T) {
	w := httptest.NewRecorder()
	Respond(w, map[string]string{"id": "existing-order"}, store.ErrPending)
	body := responseBody(t, w)
	if w.Code != 202 || string(body["success"]) != "false" || string(body["code"]) != `"STORE_PAYMENT_PENDING"` || !strings.Contains(string(body["data"]), "existing-order") {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestRespondEncodingFailureDoesNotStartAPartialSuccess(t *testing.T) {
	w := httptest.NewRecorder()
	Respond(w, make(chan int), nil)
	body := responseBody(t, w)
	if w.Code != 500 || string(body["success"]) != "false" || string(body["code"]) != `"STORE_UNAVAILABLE"` {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestPagePreservesLegacyBoundsAndRejectsAmbiguity(t *testing.T) {
	for _, tc := range []struct {
		query         string
		offset, limit int
		invalid       bool
	}{
		{"", 0, 30, false}, {"offset=100000&limit=100", 100000, 100, false},
		{"offset=10&limit=1", 10, 1, false}, {"q=search", 0, 30, false},
		{"offset=-1", 0, 0, true}, {"offset=100001", 0, 0, true},
		{"limit=0", 0, 0, true}, {"limit=101", 0, 0, true},
		{"offset=", 0, 0, true}, {"limit=", 0, 0, true},
		{"offset=1&offset=2", 0, 0, true}, {"limit=1&limit=1", 0, 0, true},
		{"offset=999999999999999999999999", 0, 0, true},
		{"offset=1.5", 0, 0, true}, {"limit=%ZZ", 0, 0, true},
		{"offset=1;limit=2", 0, 0, true},
	} {
		t.Run(tc.query, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			r.URL.RawQuery = tc.query
			offset, limit, err := Page(r)
			if tc.invalid {
				if !errors.Is(err, store.ErrInvalid) {
					t.Fatalf("offset=%d limit=%d err=%v", offset, limit, err)
				}
			} else if err != nil || offset != tc.offset || limit != tc.limit {
				t.Fatalf("offset=%d limit=%d err=%v", offset, limit, err)
			}
		})
	}
}

func TestRespondListHasNoInventedTotalAndPreservesFullPageRule(t *testing.T) {
	for _, tc := range []struct {
		items []int
		limit int
		more  bool
	}{
		{nil, 30, false}, {[]int{1}, 2, false}, {[]int{1, 2}, 2, true}, {[]int{1, 2, 3}, 2, false},
	} {
		w := httptest.NewRecorder()
		RespondList(w, tc.items, 10, tc.limit, nil)
		body := responseBody(t, w)
		var data map[string]json.RawMessage
		if err := json.Unmarshal(body["data"], &data); err != nil {
			t.Fatal(err)
		}
		if string(data["offset"]) != "10" || string(data["has_more"]) != fmt.Sprint(tc.more) || data["total"] != nil || string(data["items"]) == "null" {
			t.Fatal(string(body["data"]))
		}
	}
}

func TestRespondListDoesNotExposeItemsOnFailure(t *testing.T) {
	w := httptest.NewRecorder()
	RespondList(w, []string{"private-order"}, 0, 30, store.ErrForbidden)
	if w.Code != 403 || strings.Contains(w.Body.String(), "private-order") {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}
