package service

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type protectedProxyConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *protectedProxyConn) Read(p []byte) (int, error) { return c.reader.Read(p) }

// Only the trusted proxy address is resolved by the underlying network dialer.
// The target must already be a literal IP checked by protectedFetchDialer. Do not
// fall back to ordinary HTTP forwarding when a proxy rejects CONNECT.
func protectedFetchProxyDialContext(proxyURL *url.URL, dial func(context.Context, string, string) (net.Conn, error)) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, target string) (net.Conn, error) {
		host, _, err := net.SplitHostPort(target)
		if err != nil || net.ParseIP(host) == nil {
			return nil, fmt.Errorf("protected proxy target is not a checked IP address")
		}
		if proxyURL == nil || proxyURL.Hostname() == "" {
			return nil, fmt.Errorf("invalid fetch proxy")
		}
		scheme := strings.ToLower(proxyURL.Scheme)
		// General operator-managed upstreams retain their existing proxy client. This
		// stricter user-URL path must not silently use an unverified proxy protocol.
		if scheme != "http" && scheme != "https" {
			return nil, fmt.Errorf("protected fetch proxy scheme is not supported")
		}
		port := proxyURL.Port()
		if port == "" {
			if scheme == "https" {
				port = "443"
			} else {
				port = "80"
			}
		}
		conn, err := dial(ctx, "tcp", net.JoinHostPort(proxyURL.Hostname(), port))
		if err != nil {
			return nil, err
		}
		ok := false
		defer func() {
			if !ok {
				_ = conn.Close()
			}
		}()
		raw := conn
		stop := context.AfterFunc(ctx, func() { _ = raw.Close() })
		defer stop()
		deadline := time.Now().Add(30 * time.Second)
		if end, exists := ctx.Deadline(); exists && end.Before(deadline) {
			deadline = end
		}
		if err := conn.SetDeadline(deadline); err != nil {
			return nil, err
		}
		if scheme == "https" {
			tlsConn := tls.Client(conn, &tls.Config{ServerName: proxyURL.Hostname(), MinVersion: tls.VersionTLS12})
			if err := tlsConn.HandshakeContext(ctx); err != nil {
				return nil, err
			}
			conn = tlsConn
		}
		req := &http.Request{Method: http.MethodConnect, URL: &url.URL{Opaque: target}, Host: target, Header: make(http.Header)}
		if proxyURL.User != nil {
			password, _ := proxyURL.User.Password()
			token := base64.StdEncoding.EncodeToString([]byte(proxyURL.User.Username() + ":" + password))
			req.Header.Set("Proxy-Authorization", "Basic "+token)
		}
		if err := req.Write(conn); err != nil {
			return nil, err
		}
		limited := &io.LimitedReader{R: conn, N: 64 << 10}
		reader := bufio.NewReader(limited)
		response, err := http.ReadResponse(reader, req)
		if err != nil {
			return nil, err
		}
		if limited.N <= 0 {
			return nil, fmt.Errorf("fetch proxy response headers exceed limit")
		}
		limited.N = math.MaxInt64
		if response.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("fetch proxy CONNECT returned status %d", response.StatusCode)
		}
		if !stop() {
			return nil, ctx.Err()
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := conn.SetDeadline(time.Time{}); err != nil {
			return nil, err
		}
		ok = true
		return &protectedProxyConn{Conn: conn, reader: reader}, nil
	}
}
