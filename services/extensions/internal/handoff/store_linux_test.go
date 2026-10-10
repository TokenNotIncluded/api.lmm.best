package handoff

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	d := t.TempDir()
	if e := os.Chmod(d, 0700); e != nil {
		t.Fatal(e)
	}
	s, e := Init(d)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	return s
}
func TestDurableReceiveAndCollision(t *testing.T) {
	s := newStore(t)
	payload := []byte(`{"action":"deliver","payer":"test-a"}`)
	x, e := s.Receive("scope/event1", payload)
	if e != nil {
		t.Fatal(e)
	}
	s2, e := Open(s.dir)
	if e != nil {
		t.Fatal(e)
	}
	defer s2.Close()
	y, e := s2.Receive(x.Key, payload)
	if e != nil || y.Version != x.Version {
		t.Fatalf("duplicate %v", e)
	}
	if _, e = s2.Receive(x.Key, []byte("different")); !errors.Is(e, ErrConflict) {
		t.Fatalf("collision %v", e)
	}
	if _, e = Init(s.dir); e == nil {
		t.Fatal("overwrote existing store")
	}
}
func TestFencedTakeoverAndUnknown(t *testing.T) {
	s := newStore(t)
	_, e := s.Receive("tool-1", []byte("immutable-input"))
	if e != nil {
		t.Fatal(e)
	}
	old, e := s.Takeover("old")
	if e != nil {
		t.Fatal(e)
	}
	ticket, e := s.Claim(old, "tool-1")
	if e != nil {
		t.Fatal(e)
	}
	other, e := Open(s.dir)
	if e != nil {
		t.Fatal(e)
	}
	defer other.Close()
	fresh, e := other.Takeover("new")
	if e != nil {
		t.Fatal(e)
	}
	if fresh.Epoch <= old.Epoch {
		t.Fatal("epoch did not advance")
	}
	if _, e = s.Start(old, ticket); !errors.Is(e, ErrFenced) {
		t.Fatalf("old resumed %v", e)
	}
	ticket, e = other.Claim(fresh, "tool-1")
	if e != nil {
		t.Fatal(e)
	}
	started, e := other.Start(fresh, ticket)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = other.Start(fresh, ticket); !errors.Is(e, ErrFenced) {
		t.Fatalf("same generation stale task %v", e)
	}
	third, e := s.Takeover("rollback-v1")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Claim(third, "tool-1"); !errors.Is(e, ErrState) {
		t.Fatal("unknown retried")
	}
	if _, e = other.Complete(fresh, started, Receipt{started.Key, started.Digest, "provider-receipt"}); !errors.Is(e, ErrFenced) {
		t.Fatalf("stale result %v", e)
	}
	d, e := s.Snapshot()
	if e != nil {
		t.Fatal(e)
	}
	current := d.Tasks["tool-1"]
	if current.State != "uncertain" {
		t.Fatal("lost uncertainty")
	}
	if _, e = s.Complete(third, current, Receipt{current.Key, "wrong-digest", "receipt"}); !errors.Is(e, ErrConflict) {
		t.Fatal("wrong receipt accepted")
	}
	result, e := s.Complete(third, current, Receipt{current.Key, current.Digest, "verified-lookup"})
	if e != nil || result.State != "done" {
		t.Fatalf("reconcile %v", e)
	}
	if _, e = s.Claim(third, "tool-1"); !errors.Is(e, ErrState) {
		t.Fatal("done retried")
	}
}
func TestCorruptionAndBoundsFailClosed(t *testing.T) {
	s := newStore(t)
	if _, e := s.Receive("large", make([]byte, MaxPayload+1)); !errors.Is(e, ErrLimit) {
		t.Fatal("unbounded callback")
	}
	if e := os.WriteFile(filepath.Join(s.dir, "tasks.json"), []byte(`{"format":99,"tasks":{}}`), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Takeover("new"); e == nil {
		t.Fatal("future format opened")
	}
	if _, e := s.Receive("x", []byte("x")); e == nil {
		t.Fatal("corrupt storage acknowledged")
	}
}
func TestSymlinkAndMissingDataFailClosed(t *testing.T) {
	d := t.TempDir()
	os.Chmod(d, 0700)
	if _, e := Open(d); e == nil {
		t.Fatal("Open created task data")
	}
	if e := os.Symlink(filepath.Join(d, "missing"), filepath.Join(d, "tasks.json")); e != nil {
		t.Fatal(e)
	}
	if _, e := Open(d); e == nil {
		t.Fatal("followed symlink")
	}
}

// An older binary must not silently discard fields introduced by a newer
// storage writer, even if that writer incorrectly reused the format number.
func TestUnknownFieldsFailClosed(t *testing.T) {
	s := newStore(t)
	b := []byte(`{"format":1,"owner":{"id":"","epoch":0},"tasks":{},"future_field":true}`)
	if e := os.WriteFile(filepath.Join(s.dir, "tasks.json"), b, 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Takeover("rollback"); e == nil {
		t.Fatal("older reader discarded unknown fields")
	}
}
