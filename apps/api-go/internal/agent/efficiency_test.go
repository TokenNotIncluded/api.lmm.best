// Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later
package agent

import "testing"

func TestSuccessfulReadRunsOnceUntilMutation(t *testing.T) {
	var guard LoopGuard
	read := Call{Function: CallFunction{Name: "read", Arguments: `{"id":42,"amount":10}`}}
	if !guard.Allow(read, true) {
		t.Fatal("first read denied")
	}
	guard.Complete(read, true, true)
	reordered := Call{Function: CallFunction{Name: "read", Arguments: `{"amount":1e1,"id":42}`}}
	if guard.Allow(reordered, true) {
		t.Fatal("successful equivalent read was repeated")
	}
	other := Call{Function: CallFunction{Name: "read", Arguments: `{"id":43,"amount":10}`}}
	if !guard.Allow(other, true) {
		t.Fatal("distinct read denied")
	}
	guard.Complete(other, true, true)
	if guard.Allow(read, true) {
		t.Fatal("another read must not invalidate receipts")
	}
	write := Call{Function: CallFunction{Name: "write", Arguments: `{}`}}
	if !guard.Allow(write, false) {
		t.Fatal("first write denied")
	}
	guard.Complete(write, false, false)
	if guard.Allow(read, true) {
		t.Fatal("failed write must not invalidate reads")
	}
	if !guard.Allow(write, false) {
		t.Fatal("one retry of failed write denied")
	}
	guard.Complete(write, false, true)
	if !guard.Allow(read, true) {
		t.Fatal("verification after mutation denied")
	}
	guard.Complete(read, true, true)
	if guard.Allow(read, true) {
		t.Fatal("verification repeated without a state change")
	}
	if guard.Allow(write, false) {
		t.Fatal("successful write was repeated")
	}
}

func TestFailedReadKeepsOneBoundedRetry(t *testing.T) {
	var guard LoopGuard
	read := Call{Function: CallFunction{Name: "read", Arguments: `{}`}}
	for i := 0; i < 2; i++ {
		if !guard.Allow(read, true) {
			t.Fatalf("attempt %d denied", i+1)
		}
		guard.Complete(read, true, false)
	}
	if guard.Allow(read, true) {
		t.Fatal("unbounded failure retry")
	}
}
