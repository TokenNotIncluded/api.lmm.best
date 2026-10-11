package transportpolicy

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"net/url"
	"testing"
	"time"
)

func fixture() (Policy, tls.ConnectionState, time.Time) {
	now := time.Unix(1800000000, 0)
	uri, _ := url.Parse("spiffe://lmm.test/core/a")
	leaf := &x509.Certificate{Raw: []byte("test-only-leaf"), URIs: []*url.URL{uri}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour)}
	digest := sha256.Sum256(leaf.Raw)
	p := Policy{Version: 1, IssuedAt: now.Add(-time.Second), ExpiresAt: now.Add(5 * time.Second), Peers: []Peer{{URI: uri.String(), SHA256: hex.EncodeToString(digest[:]), NotAfter: now.Add(4 * time.Second)}}}
	state := tls.ConnectionState{HandshakeComplete: true, NegotiatedProtocol: "h2", Version: tls.VersionTLS13, PeerCertificates: []*x509.Certificate{leaf}, VerifiedChains: [][]*x509.Certificate{{leaf}}}
	return p, state, now
}
func TestPolicyAndEstablishedConnectionRevocation(t *testing.T) {
	p, state, now := fixture()
	if p.CheckPeer(state, "spiffe://lmm.test/core/a", now) != nil {
		t.Fatal("valid peer denied")
	}
	p.Peers[0].NotAfter = now
	if p.CheckPeer(state, "spiffe://lmm.test/core/a", now) == nil {
		t.Fatal("revoked peer reused connection")
	}
}
func TestRejectUnsafeStates(t *testing.T) {
	cases := []struct {
		name   string
		change func(*Policy, *tls.ConnectionState, *time.Time)
	}{
		{"wrong identity", func(p *Policy, s *tls.ConnectionState, n *time.Time) { p.Peers[0].URI = "spiffe://lmm.test/core/b" }},
		{"expired cert", func(p *Policy, s *tls.ConnectionState, n *time.Time) { s.PeerCertificates[0].NotAfter = *n }},
		{"nil leaf", func(p *Policy, s *tls.ConnectionState, n *time.Time) { s.PeerCertificates[0] = nil }},
		{"nil verified leaf", func(p *Policy, s *tls.ConnectionState, n *time.Time) { s.VerifiedChains = [][]*x509.Certificate{{nil}} }},
		{"unverified", func(p *Policy, s *tls.ConnectionState, n *time.Time) { s.VerifiedChains = nil }},
		{"wrong verified leaf", func(p *Policy, s *tls.ConnectionState, n *time.Time) {
			s.VerifiedChains = [][]*x509.Certificate{{{Raw: []byte("different")}}}
		}},
		{"TLS 1.2", func(p *Policy, s *tls.ConnectionState, n *time.Time) { s.Version = tls.VersionTLS12 }},
		{"incomplete handshake", func(p *Policy, s *tls.ConnectionState, n *time.Time) { s.HandshakeComplete = false }},
		{"expired lease", func(p *Policy, s *tls.ConnectionState, n *time.Time) { p.ExpiresAt = *n }},
		{"unbounded lease", func(p *Policy, s *tls.ConnectionState, n *time.Time) { p.ExpiresAt = n.Add(time.Hour) }},
		{"future policy", func(p *Policy, s *tls.ConnectionState, n *time.Time) { p.IssuedAt = n.Add(time.Second) }},
		{"duplicate pin", func(p *Policy, s *tls.ConnectionState, n *time.Time) { p.Peers = append(p.Peers, p.Peers[0]) }},
		{"no peer", func(p *Policy, s *tls.ConnectionState, n *time.Time) { p.Peers = nil }},
		{"zero epoch", func(p *Policy, s *tls.ConnectionState, n *time.Time) { p.Version = 0 }},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			p, s, n := fixture()
			tt.change(&p, &s, &n)
			if p.CheckPeer(s, "spiffe://lmm.test/core/a", n) == nil {
				t.Fatal("unsafe peer accepted")
			}
		})
	}
}
func TestClientTLSRejectsMissingOrOversizedTrust(t *testing.T) {
	for _, ca := range [][]byte{nil, []byte("not PEM"), make([]byte, 65537)} {
		if _, err := ClientTLS(ca, nil, nil, "core.test", "spiffe://lmm.test/core/a", func() Policy { return Policy{} }); err == nil {
			t.Fatal("invalid trust accepted")
		}
	}
}

func TestStoreRejectsRollbackAndDoesNotKeepBrokenConfig(t *testing.T) {
	p, state, now := fixture()
	var store Store
	if store.Replace(p, now) != nil {
		t.Fatal("valid replacement denied")
	}
	if store.Snapshot().CheckPeer(state, "spiffe://lmm.test/core/a", now) != nil {
		t.Fatal("valid snapshot denied")
	}
	p.Version++
	if store.Replace(p, now) != nil {
		t.Fatal("rotation denied")
	}
	p.Version--
	if store.Replace(p, now) == nil {
		t.Fatal("rollback accepted")
	}
	if store.Snapshot().CheckPeer(state, "spiffe://lmm.test/core/a", now) == nil {
		t.Fatal("broken replacement retained old authorization")
	}
	p.Version += 2
	if store.Replace(p, now) != nil {
		t.Fatal("forward recovery failed")
	}
	p.Peers[0].URI = "changed-by-caller"
	if store.Snapshot().Peers[0].URI == p.Peers[0].URI {
		t.Fatal("input alias mutates live policy")
	}
	snapshot := store.Snapshot()
	snapshot.Peers[0].URI = "changed-by-reader"
	if store.Snapshot().Peers[0].URI == snapshot.Peers[0].URI {
		t.Fatal("output alias mutates live policy")
	}
}
