package relay

import (
	"bytes"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/jinzhu/copier"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCopyRequestForRelayPassthroughKeepsMetadataIsolated(t *testing.T) {
	original := &dto.OpenAIResponsesRequest{
		Model: "original", Input: json.RawMessage(`[{"role":"user","content":"payload"}]`),
		Tools:         json.RawMessage(`[{"type":"function"}]`),
		StreamOptions: &dto.StreamOptions{IncludeUsage: true},
	}
	copy, err := copyRequestForRelay(original, true)
	require.NoError(t, err)
	assert.NotSame(t, original, copy)
	assert.Equal(t, original, copy)
	assert.Same(t, &original.Input[0], &copy.Input[0], "passthrough must not copy the immutable payload")
	assert.Same(t, &original.Tools[0], &copy.Tools[0])
	copy.Model = "mapped"
	copy.StreamOptions = &dto.StreamOptions{IncludeUsage: false}
	assert.Equal(t, "original", original.Model)
	assert.True(t, original.StreamOptions.IncludeUsage)
}

func TestCopyRequestForRelayTextPassthroughDoesNotCloneMessages(t *testing.T) {
	original := &dto.GeneralOpenAIRequest{
		Model: "original", Messages: []dto.Message{{Role: "user", Content: "payload"}},
		Metadata:      json.RawMessage(`{"trace":"retained"}`),
		StreamOptions: &dto.StreamOptions{IncludeUsage: true},
	}
	copy, err := copyRequestForRelay(original, true)
	require.NoError(t, err)
	assert.Same(t, &original.Messages[0], &copy.Messages[0])
	assert.Same(t, &original.Metadata[0], &copy.Metadata[0])
	copy.Model = "mapped"
	copy.StreamOptions = nil
	assert.Equal(t, "original", original.Model)
	require.NotNil(t, original.StreamOptions)
	assert.True(t, original.StreamOptions.IncludeUsage)
}

func TestCopyRequestForRelayConvertedRequestsKeepDeepCopy(t *testing.T) {
	original := &dto.OpenAIResponsesRequest{
		Model: "original", Input: json.RawMessage(`"payload"`),
		Reasoning:     &dto.Reasoning{Effort: "high", Context: json.RawMessage(`"original"`)},
		StreamOptions: &dto.StreamOptions{IncludeUsage: true},
	}
	copy, err := copyRequestForRelay(original, false)
	require.NoError(t, err)
	assert.Equal(t, original, copy)
	copy.Input[1] = 'X'
	copy.Reasoning.Effort = "low"
	copy.Reasoning.Context[1] = 'X'
	copy.StreamOptions.IncludeUsage = false
	assert.JSONEq(t, `"payload"`, string(original.Input))
	assert.Equal(t, "high", original.Reasoning.Effort)
	assert.JSONEq(t, `"original"`, string(original.Reasoning.Context))
	assert.True(t, original.StreamOptions.IncludeUsage)
}

func TestCopyRequestForRelayRejectsNil(t *testing.T) {
	for _, passthrough := range []bool{false, true} {
		copy, err := copyRequestForRelay[dto.OpenAIResponsesRequest](nil, passthrough)
		require.Error(t, err)
		assert.Nil(t, copy)
	}
}

var requestCopyBenchmarkSink *dto.OpenAIResponsesRequest

// Compare the old copier options, optimized deep copy, and passthrough on the
// exact same DTO. Keep the legacy baseline here rather than in production code.
func BenchmarkCopyRequestForRelay(b *testing.B) {
	modes := []struct {
		name string
		copy func(*dto.OpenAIResponsesRequest) (*dto.OpenAIResponsesRequest, error)
	}{
		{"deep_copy_baseline", func(src *dto.OpenAIResponsesRequest) (*dto.OpenAIResponsesRequest, error) {
			var dst dto.OpenAIResponsesRequest
			err := copier.CopyWithOption(&dst, src, copier.Option{DeepCopy: true, IgnoreEmpty: true})
			return &dst, err
		}},
		{"deep_copy_optimized", func(src *dto.OpenAIResponsesRequest) (*dto.OpenAIResponsesRequest, error) {
			return copyRequestForRelay(src, false)
		}},
		{"passthrough", func(src *dto.OpenAIResponsesRequest) (*dto.OpenAIResponsesRequest, error) {
			return copyRequestForRelay(src, true)
		}},
	}
	for _, size := range []int{0, 64 << 10, 1 << 20, 28 << 20} {
		var payload json.RawMessage
		if size > 0 {
			payload = bytes.Repeat([]byte("x"), size)
			payload[0], payload[len(payload)-1] = '"', '"'
		}
		original := &dto.OpenAIResponsesRequest{Model: "benchmark", Input: payload}
		for _, mode := range modes {
			b.Run(fmt.Sprintf("%dKiB/%s", size>>10, mode.name), func(b *testing.B) {
				b.ReportAllocs()
				b.SetBytes(int64(size))
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					copy, err := mode.copy(original)
					if err != nil {
						b.Fatal(err)
					}
					requestCopyBenchmarkSink = copy
				}
			})
		}
	}
}
