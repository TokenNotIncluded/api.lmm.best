package agent

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizeCallsRepairsDuplicatesAcrossRoundsWithoutMutatingInput(t *testing.T) {
	used := map[string]bool{"prior": true, "assistant-call-2-1": true}
	original := []Call{{ID: " prior "}, {ID: "same"}, {ID: "same"}, {}}
	calls := NormalizeCalls(original, 1, used)
	assert.Equal(t, " prior ", original[0].ID)
	seen := map[string]bool{}
	for _, call := range calls {
		require.NotEmpty(t, call.ID)
		assert.False(t, seen[call.ID])
		assert.NotEqual(t, "prior", call.ID)
		assert.Equal(t, "function", call.Type)
		seen[call.ID] = true
	}
}

func TestLoopGuardCanonicalizesArgumentsAndNeverReplaysSuccessfulWrites(t *testing.T) {
	var guard LoopGuard
	write := Call{Function: CallFunction{Name: "update", Arguments: `{"amount":10,"id":9007199254740993}`}}
	reordered := Call{Function: CallFunction{Name: "update", Arguments: `{ "id":9007199254740993, "amount":10 }`}}
	require.True(t, guard.Allow(write, false))
	guard.Complete(write, false, true)
	assert.False(t, guard.Allow(reordered, false))
	reordered.Function.Arguments = `{"id":9007199254740993,"amount":1e1}`
	assert.False(t, guard.Allow(reordered, false))
	reordered.Function.Arguments = `{"id":9007199254740993,"amount":"10"}`
	assert.True(t, guard.Allow(reordered, false), "a string is a different argument type")
	// Preserve full integer precision when matching calls.
	other := Call{Function: CallFunction{Name: "update", Arguments: `{"amount":10,"id":9007199254740992}`}}
	assert.True(t, guard.Allow(other, false))
}

func TestLoopGuardPermitsVerificationAfterMutationAndBoundsFailedRetries(t *testing.T) {
	var guard LoopGuard
	read := Call{Function: CallFunction{Name: "get", Arguments: `{}`}}
	write := Call{Function: CallFunction{Name: "update", Arguments: `{}`}}
	assert.True(t, guard.Allow(read, true))
	assert.True(t, guard.Allow(read, true))
	assert.False(t, guard.Allow(read, true))
	require.True(t, guard.Allow(write, false))
	guard.Complete(write, false, false)
	require.True(t, guard.Allow(write, false))
	guard.Complete(write, false, true)
	assert.True(t, guard.Allow(read, true))
	assert.False(t, guard.Allow(write, false))

	failed := Call{Function: CallFunction{Name: "failed", Arguments: `{}`}}
	assert.True(t, guard.Allow(failed, false))
	assert.True(t, guard.Allow(failed, false))
	assert.False(t, guard.Allow(failed, false))
}
