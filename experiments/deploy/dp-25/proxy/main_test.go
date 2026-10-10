package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"testing"
)

func frame(kind byte, payload string) []byte {
	b := make([]byte, 5+len(payload))
	b[0] = kind
	binary.BigEndian.PutUint32(b[1:5], uint32(len(payload)+4))
	copy(b[5:], payload)
	return b
}

func TestDropsOnlyCommitAcknowledgement(t *testing.T) {
	before := append(frame('R', "\x00\x00\x00\x00"), frame('D', "test result")...)
	wire := append(append(append([]byte{}, before...), frame('C', "COMMIT\x00")...), frame('Z', "I")...)
	var out bytes.Buffer
	dropped, err := copyResponses(&out, bytes.NewReader(wire))
	if err != nil || !dropped || !bytes.Equal(out.Bytes(), before) {
		t.Fatalf("dropped=%v err=%v output=%x", dropped, err, out.Bytes())
	}
}

func TestOrdinaryCommandCompleteIsForwarded(t *testing.T) {
	wire := append(frame('C', "SELECT 1\x00"), frame('Z', "I")...)
	var out bytes.Buffer
	dropped, err := copyResponses(&out, bytes.NewReader(wire))
	if dropped || !errors.Is(err, io.EOF) || !bytes.Equal(out.Bytes(), wire) {
		t.Fatalf("dropped=%v err=%v", dropped, err)
	}
}

func TestRejectsMalformedFrame(t *testing.T) {
	for _, n := range []uint32{0, 3, maxFrame + 1} {
		wire := make([]byte, 5)
		wire[0] = 'D'
		binary.BigEndian.PutUint32(wire[1:], n)
		if dropped, err := copyResponses(io.Discard, bytes.NewReader(wire)); dropped || err == nil {
			t.Fatalf("accepted length=%d", n)
		}
	}
}

func TestTruncatedPayloadIsNotACommit(t *testing.T) {
	wire := frame('C', "COMMIT\x00")
	if dropped, err := copyResponses(io.Discard, bytes.NewReader(wire[:len(wire)-1])); dropped || err == nil {
		t.Fatalf("dropped=%v err=%v", dropped, err)
	}
}

type failedWriter struct{}

func (failedWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestForwardFailureIsReturned(t *testing.T) {
	dropped, err := copyResponses(failedWriter{}, bytes.NewReader(frame('D', "test")))
	if dropped || !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("dropped=%v err=%v", dropped, err)
	}
}
