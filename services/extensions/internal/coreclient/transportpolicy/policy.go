// Package transportpolicy validates RPC peer identities without user authority.
// Call CheckPeer before each new RPC, including on reused connections. This
// package never opens a database and does not retry or approve money commands.
package transportpolicy

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"sync"
	"time"
)

const MaxPeers = 8
const MaxLease = 60 * time.Second

var ErrDenied = errors.New("RPC peer is not authorized")

type Peer struct {
	URI      string
	SHA256   string // SHA-256 of the complete DER leaf certificate, not its key.
	NotAfter time.Time
}

type Policy struct {
	Version   uint64
	IssuedAt  time.Time
	ExpiresAt time.Time
	Peers     []Peer
}

// Validate rejects malformed or expired snapshots. A broken replacement must
// stop new RPCs; callers must not silently keep an old snapshot indefinitely.
func (p Policy) Validate(now time.Time) error {
	if p.Version == 0 || p.IssuedAt.After(now) || !now.Before(p.ExpiresAt) ||
		!p.ExpiresAt.After(p.IssuedAt) || p.ExpiresAt.Sub(p.IssuedAt) > MaxLease ||
		len(p.Peers) == 0 || len(p.Peers) > MaxPeers {
		return ErrDenied
	}
	seen := make(map[string]bool)
	for _, peer := range p.Peers {
		decoded, err := hex.DecodeString(peer.SHA256)
		if err != nil || len(decoded) != sha256.Size || hex.EncodeToString(decoded) != peer.SHA256 ||
			peer.URI == "" || len(peer.URI) > 256 || peer.NotAfter.IsZero() || seen[peer.SHA256] {
			return ErrDenied
		}
		seen[peer.SHA256] = true
	}
	return nil
}

// CheckPeer requires normal TLS verification AND an exact service URI and
// certificate pin. The certificate and policy are checked again on each call.
// An already-open TLS connection is not a permanent authorization grant.
func (p Policy) CheckPeer(state tls.ConnectionState, expectedURI string, now time.Time) error {
	if !state.HandshakeComplete {
		return ErrDenied
	}
	return p.checkVerifiedPeer(state, expectedURI, now)
}

// VerifyConnection runs before HandshakeComplete becomes true.
func (p Policy) checkVerifiedPeer(state tls.ConnectionState, expectedURI string, now time.Time) error {
	if p.Validate(now) != nil || state.Version < tls.VersionTLS13 || state.NegotiatedProtocol != "h2" ||
		len(state.VerifiedChains) == 0 || len(state.PeerCertificates) == 0 || state.PeerCertificates[0] == nil {
		return ErrDenied
	}
	leaf := state.PeerCertificates[0]
	// Require the verified chain to describe the same leaf presented by TLS.
	verified := false
	for _, chain := range state.VerifiedChains {
		if len(chain) > 0 && chain[0] != nil && leaf.Equal(chain[0]) {
			verified = true
			break
		}
	}
	if !verified || now.Before(leaf.NotBefore) || !now.Before(leaf.NotAfter) {
		return ErrDenied
	}
	if len(leaf.URIs) != 1 || leaf.URIs[0].String() != expectedURI {
		return ErrDenied
	}
	digest := sha256.Sum256(leaf.Raw)
	pin := hex.EncodeToString(digest[:])
	for _, peer := range p.Peers {
		if peer.URI == expectedURI && peer.SHA256 == pin && now.Before(peer.NotAfter) {
			return nil
		}
	}
	return ErrDenied
}

// ClientTLS never uses system roots, skips verification, or falls back to
// plaintext. Callers must also CheckPeer for each RPC on an existing connection.
func ClientTLS(caPEM, certPEM, keyPEM []byte, serverName, expectedURI string, policy func() Policy) (*tls.Config, error) {
	if len(caPEM) == 0 || len(caPEM) > 64*1024 || len(certPEM) > 64*1024 || len(keyPEM) > 64*1024 ||
		serverName == "" || expectedURI == "" || policy == nil {
		return nil, ErrDenied
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caPEM) {
		return nil, ErrDenied
	}
	identity, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, ErrDenied
	}
	return &tls.Config{
		MinVersion:   tls.VersionTLS13,
		RootCAs:      roots,
		Certificates: []tls.Certificate{identity},
		ServerName:   serverName,
		NextProtos:   []string{"h2"},
		VerifyConnection: func(state tls.ConnectionState) error {
			return policy().checkVerifiedPeer(state, expectedURI, time.Now())
		},
	}, nil
}

// Store rejects rollback within this process. An invalid replacement disables
// new calls immediately. Persist Version separately before accepting traffic
// when this is integrated; process restart otherwise loses the high-water mark.
type Store struct {
	mu        sync.RWMutex
	highWater uint64
	current   Policy
}

func (s *Store) Replace(p Policy, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p.Validate(now) != nil || p.Version <= s.highWater {
		s.current = Policy{}
		return ErrDenied
	}
	s.highWater = p.Version
	p.Peers = append([]Peer(nil), p.Peers...)
	s.current = p
	return nil
}

func (s *Store) Snapshot() Policy {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p := s.current
	p.Peers = append([]Peer(nil), p.Peers...)
	return p
}
