package webcompat

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"

	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules"
	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules/store"
)

// Respond preserves the legacy success/message/data envelope. Errors are
// allowlisted domain mappings; database and provider error strings stay private.
func Respond(w http.ResponseWriter, value any, err error) {
	code := http.StatusOK
	body := map[string]any{"success": true, "message": "", "data": value}
	if err != nil {
		var label, message string
		code, label, message = responseError(err)
		if errors.Is(err, store.ErrRateLimited) {
			w.Header().Set("Retry-After", "60")
		}
		body = map[string]any{"success": false, "code": label, "message": message}
		var terms *store.TermsUpdatedError
		if errors.As(err, &terms) {
			body["request_key"] = terms.RequestKey
			body["order_created"] = false
		}
		if errors.Is(err, store.ErrPending) {
			body["data"] = value
		}
	}
	encoded, encodeErr := json.Marshal(body)
	if encodeErr != nil {
		code = http.StatusInternalServerError
		encoded = []byte(`{"success":false,"code":"STORE_UNAVAILABLE","message":"The shop could not complete this request."}`)
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(code)
	_, _ = w.Write(append(encoded, '\n'))
}

func responseError(err error) (int, string, string) {
	var terms *store.TermsUpdatedError
	switch {
	case errors.Is(err, modules.ErrSaturated):
		return http.StatusServiceUnavailable, "EXTENSION_BUSY", "The extension service is busy. Retry shortly."
	case errors.Is(err, modules.ErrNotAccepting):
		return http.StatusServiceUnavailable, "EXTENSION_NOT_ACCEPTING", "The extension service is not accepting new work."
	case errors.As(err, &terms):
		return http.StatusConflict, "STORE_TERMS_UPDATED", "The seller terms changed before this order was created. Read and accept the current terms."
	case errors.Is(err, store.ErrSellerTermsNotConfigured):
		return http.StatusConflict, "STORE_SELLER_TERMS_NOT_CONFIGURED", "Configure seller terms before publishing or relisting this product."
	case errors.Is(err, store.ErrSellerTermsRequired):
		return http.StatusConflict, "STORE_SELLER_TERMS_REQUIRED", "Read and accept the current seller terms before ordering."
	case errors.Is(err, store.ErrDisclaimerRequired):
		return http.StatusConflict, "STORE_DISCLAIMER_REQUIRED", "Read and accept the current merchant disclaimer before ordering."
	case errors.Is(err, store.ErrRateLimited):
		return http.StatusTooManyRequests, "STORE_RATE_LIMITED", "Please wait before sending another request."
	case errors.Is(err, store.ErrUnauthorized):
		return http.StatusUnauthorized, "STORE_LOGIN_REQUIRED", "Sign in before ordering this product."
	case errors.Is(err, store.ErrForbidden):
		return http.StatusForbidden, "STORE_ACCESS_DENIED", "This shop operation is not available to this account."
	case errors.Is(err, store.ErrNotFound):
		return http.StatusNotFound, "STORE_NOT_FOUND", "The shop item or order was not found."
	case errors.Is(err, store.ErrInvalid):
		return http.StatusUnprocessableEntity, "STORE_INVALID_INPUT", "Please check the shop information and amounts."
	case errors.Is(err, store.ErrConflict):
		return http.StatusConflict, "STORE_CONFLICT", "The order or product state has changed. Please refresh."
	case errors.Is(err, store.ErrSoldOut):
		return http.StatusConflict, "STORE_OUT_OF_STOCK", "The product has insufficient available stock."
	case errors.Is(err, store.ErrPaymentUnavailable):
		return http.StatusServiceUnavailable, "STORE_PAYMENT_UNAVAILABLE", "Shop payments are currently unavailable."
	case errors.Is(err, store.ErrPending):
		return http.StatusAccepted, "STORE_PAYMENT_PENDING", "The payment result is pending. Read the existing order before trying again."
	case errors.Is(err, store.ErrUnavailable):
		return http.StatusServiceUnavailable, "STORE_UNAVAILABLE", "The shop could not complete this request."
	default:
		return http.StatusInternalServerError, "STORE_UNAVAILABLE", "The shop could not complete this request."
	}
}

// Page preserves the legacy offset/limit defaults and limits. Duplicate or
// malformed query parameters fail closed rather than selecting an arbitrary one.
func Page(r *http.Request) (offset, limit int, err error) {
	if r == nil || r.URL == nil {
		return 0, 0, store.ErrInvalid
	}
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return 0, 0, store.ErrInvalid
	}
	offset, limit = 0, 30
	for name, target := range map[string]*int{"offset": &offset, "limit": &limit} {
		values, present := query[name]
		if !present {
			continue
		}
		if len(values) != 1 {
			return 0, 0, store.ErrInvalid
		}
		*target, err = strconv.Atoi(values[0])
		if err != nil {
			return 0, 0, store.ErrInvalid
		}
	}
	if !validPage(offset, limit) {
		return 0, 0, store.ErrInvalid
	}
	return offset, limit, nil
}

func validPage(offset, limit int) bool {
	return offset >= 0 && offset <= 100000 && limit >= 1 && limit <= 100
}

// RespondList does not claim a total count. As in the legacy handler, a full page
// means has_more; nil items are serialized as an empty array, never null.
func RespondList[T any](w http.ResponseWriter, items []T, offset, limit int, err error) {
	if err != nil {
		Respond(w, nil, err)
		return
	}
	if !validPage(offset, limit) {
		Respond(w, nil, store.ErrInvalid)
		return
	}
	if items == nil {
		items = []T{}
	}
	Respond(w, map[string]any{"items": items, "offset": offset, "limit": limit, "has_more": len(items) == limit}, nil)
}
