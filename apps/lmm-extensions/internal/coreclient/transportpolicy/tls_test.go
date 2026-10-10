package transportpolicy

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

func TestRealMutualTLSHTTP2AndExpiredPolicy(t *testing.T) {
	now := time.Now()
	caKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	caTemplate := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "disposable test CA"}, IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour)}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	ca, _ := x509.ParseCertificate(caDER)
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})
	leaf := func(id int64, uri string, usage x509.ExtKeyUsage) ([]byte, []byte, Peer) {
		key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		identity, _ := url.Parse(uri)
		template := &x509.Certificate{SerialNumber: big.NewInt(id), Subject: pkix.Name{CommonName: "test leaf"}, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour), DNSNames: []string{"core.test"}, URIs: []*url.URL{identity}, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{usage}}
		der, e := x509.CreateCertificate(rand.Reader, template, ca, &key.PublicKey, caKey)
		if e != nil {
			t.Fatal(e)
		}
		keyDER, e := x509.MarshalPKCS8PrivateKey(key)
		if e != nil {
			t.Fatal(e)
		}
		d := sha256.Sum256(der)
		return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), Peer{URI: uri, SHA256: hex.EncodeToString(d[:]), NotAfter: now.Add(30 * time.Second)}
	}
	serverCert, serverKey, serverPin := leaf(2, "spiffe://lmm.test/core/a", x509.ExtKeyUsageServerAuth)
	clientCert, clientKey, clientPin := leaf(3, "spiffe://lmm.test/extensions/e1", x509.ExtKeyUsageClientAuth)
	clientPolicy := Policy{Version: 1, IssuedAt: now.Add(-time.Second), ExpiresAt: now.Add(30 * time.Second), Peers: []Peer{serverPin}}
	serverPolicy := Policy{Version: 1, IssuedAt: now.Add(-time.Second), ExpiresAt: now.Add(30 * time.Second), Peers: []Peer{clientPin}}
	var clientStore, serverStore Store
	if clientStore.Replace(clientPolicy, now) != nil || serverStore.Replace(serverPolicy, now) != nil {
		t.Fatal("bad fixtures")
	}
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(caPEM)
	certificate, err := tls.X509KeyPair(serverCert, serverKey)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ProtoMajor != 2 {
			t.Error("HTTP/2 not negotiated")
		}
		if serverStore.Snapshot().CheckPeer(*r.TLS, "spiffe://lmm.test/extensions/e1", time.Now()) != nil {
			http.Error(w, "denied", http.StatusForbidden)
			return
		}
		w.Write([]byte("ok"))
	}))
	server.EnableHTTP2 = true
	server.TLS = &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{certificate}, ClientCAs: roots, ClientAuth: tls.RequireAndVerifyClientCert}
	server.StartTLS()
	defer server.Close()
	config, err := ClientTLS(caPEM, clientCert, clientKey, "core.test", "spiffe://lmm.test/core/a", clientStore.Snapshot)
	if err != nil {
		t.Fatal(err)
	}
	transport := &http.Transport{TLSClientConfig: config, ForceAttemptHTTP2: true, MaxConnsPerHost: 1, MaxIdleConnsPerHost: 1, TLSHandshakeTimeout: time.Second}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: time.Second}
	for i := 0; i < 2; i++ {
		resp, e := client.Get(server.URL)
		if e != nil {
			t.Fatal(e)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatal(resp.StatusCode)
		}
	}
	serverPolicy.Version++
	serverPolicy.Peers[0].NotAfter = time.Now().Add(-time.Second)
	if serverStore.Replace(serverPolicy, time.Now()) != nil {
		t.Fatal("policy update failed")
	}
	resp, err := client.Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatal("revoked certificate accepted on existing HTTP/2 connection")
	}
}
