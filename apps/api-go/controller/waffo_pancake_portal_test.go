package controller

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/gin-gonic/gin"
)

func portalTestContext(session bool) (*gin.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/user/self/waffo-pancake/portal?buyerIdentity=other&storeId=other&env=test", strings.NewReader(`{"user_id":999,"buyerIdentity":"other"}`))
	c.Set("id", 42)
	if session {
		c.Set("session_id", "test-session")
		c.Set("auth_version", int64(1))
		c.Set("session_version", int64(1))
	}
	return c, recorder
}

func TestWaffoPancakePortalRequiresSession(t *testing.T) {
	for _, userID := range []int{0, 42} {
		c, recorder := portalTestContext(false)
		c.Set("id", userID)
		requestWaffoPancakeCustomerPortal(c, func(context.Context, int) (*service.WaffoPancakeCustomerPortalLink, error) {
			t.Fatal("anonymous/PAT request reached token issuer")
			return nil, nil
		})
		if recorder.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d", recorder.Code)
		}
		if !strings.Contains(recorder.Header().Get("Cache-Control"), "no-store") {
			t.Fatal("authentication response is cacheable")
		}
	}
}

func TestWaffoPancakePortalUsesSessionIdentity(t *testing.T) {
	c, recorder := portalTestContext(true)
	link := &service.WaffoPancakeCustomerPortalLink{PortalURL: "https://pancake.waffo.ai/consumer/portal/store/STO_test#token=test-only", ExpiresAt: "2026-10-10T20:00:00Z"}
	requestWaffoPancakeCustomerPortal(c, func(ctx context.Context, userID int) (*service.WaffoPancakeCustomerPortalLink, error) {
		if userID != 42 || ctx != c.Request.Context() {
			t.Fatal("caller input replaced the session identity or request context")
		}
		return link, nil
	})
	var result struct {
		Success bool                                   `json:"success"`
		Data    service.WaffoPancakeCustomerPortalLink `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if recorder.Code != http.StatusOK || !result.Success || result.Data != *link {
		t.Fatal("portal response did not preserve the issued link")
	}
	if !strings.Contains(recorder.Header().Get("Cache-Control"), "no-store") || recorder.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatal("portal response lacks secret-handling headers")
	}
}

func TestWaffoPancakePortalDoesNotExposeProviderErrors(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		status int
	}{
		{"not configured", service.ErrWaffoPancakePortalUnavailable, http.StatusServiceUnavailable},
		{"upstream error", errors.New("upstream data token=TEST_SECRET"), http.StatusBadGateway},
		{"empty response", nil, http.StatusBadGateway},
		{"canceled", context.Canceled, http.StatusBadGateway},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, recorder := portalTestContext(true)
			requestWaffoPancakeCustomerPortal(c, func(context.Context, int) (*service.WaffoPancakeCustomerPortalLink, error) { return nil, tc.err })
			if recorder.Code != tc.status || strings.Contains(recorder.Body.String(), "TEST_SECRET") || strings.Contains(recorder.Body.String(), "portal_url") {
				t.Fatal("incorrect or sensitive error response")
			}
		})
	}
}
