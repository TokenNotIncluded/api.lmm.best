package service

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/setting/system_setting"
)

// Public positive controls only. The isolated adversarial suite is delivered
// privately for audit #711 and must not be copied into public test output.
func TestRT13OutboundClientSeparation(t *testing.T) {
	var received atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received.Add(1)
		if r.Header.Get("Authorization") != "Bearer RT13_FAKE_OPERATOR_NOT_VALID" {
			t.Error("operator test credential did not reach the configured target")
		}
		_, _ = io.WriteString(w, "rt13-control")
	}))
	defer server.Close()
	endpoint, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	setting := system_setting.GetFetchSetting()
	before := *setting
	defer func() { *setting = before }()
	setting.EnableSSRFProtection = true
	setting.AllowPrivateIp = false
	setting.ApplyIPFilterForDomain = true
	setting.AllowedPorts = []string{endpoint.Port()}
	protected := newProtectedFetchHTTPClientWithProxy(nil, nil, nil,
		func(*http.Request) (*url.URL, error) { return nil, nil })
	defer protected.CloseIdleConnections()
	response, err := protected.Get(server.URL)
	if response != nil {
		_ = response.Body.Close()
	}
	if err == nil || received.Load() != 0 {
		t.Fatal("user-controlled fetch must not reach the private control target")
	}
	operator := newRelayHTTPClient(&http.Transport{Proxy: nil})
	defer operator.CloseIdleConnections()
	request, err := http.NewRequest(http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer RT13_FAKE_OPERATOR_NOT_VALID")
	response, err = operator.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || received.Load() != 1 {
		t.Fatal("explicitly configured operator target must remain usable")
	}
}
