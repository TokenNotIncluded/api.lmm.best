package service

import (
	"net"
	"net/http"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/internal/extore"
)

// The shared transport resolves at dial time and pins only public addresses.
// Merchant input cannot enable environment proxies, private IPs or insecure TLS.
var MerchantStoreExtoreClient = extore.Client{HTTP: newMerchantStoreExtoreHTTPClient()}

func newMerchantStoreExtoreHTTPClient() *http.Client {
	protection := &common.SSRFProtection{AllowPrivateIp: false, AllowedPorts: []int{443}, ApplyIPFilterForDomain: true}
	dialer := protectedFetchDialer{
		resolver:      net.DefaultResolver,
		dialContext:   (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		getProtection: func() (*common.SSRFProtection, bool, error) { return protection, true, nil },
	}
	return &http.Client{
		Timeout:       20 * time.Second,
		Transport:     &http.Transport{Proxy: nil, DialContext: dialer.DialContext, ForceAttemptHTTP2: true, TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 10 * time.Second, MaxResponseHeaderBytes: 32 << 10, MaxIdleConns: 16, MaxConnsPerHost: 4, IdleConnTimeout: 30 * time.Second},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

func MerchantStoreExtoreRedirectURI() string {
	origin, err := merchantStorePublicOrigin()
	if err != nil {
		return ""
	}
	origin, err = extore.NormalizeOrigin(origin)
	if err != nil {
		return ""
	}
	return origin + "/store/manage"
}
