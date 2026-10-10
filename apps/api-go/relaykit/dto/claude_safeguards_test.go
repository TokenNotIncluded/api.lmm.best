package dto

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestClaudeRequestSafeguardsRoundTrip(t *testing.T) {
	raw := []byte(`{"model":"claude-opus-4-6","max_tokens":16,"safeguards":{"mode":"auto","policy":{"id":"p1"}}}`)
	var request ClaudeRequest
	require.NoError(t, json.Unmarshal(raw, &request))
	require.JSONEq(t, `{"mode":"auto","policy":{"id":"p1"}}`, string(request.Safeguards))

	encoded, err := json.Marshal(request)
	require.NoError(t, err)
	var again ClaudeRequest
	require.NoError(t, json.Unmarshal(encoded, &again))
	require.JSONEq(t, string(request.Safeguards), string(again.Safeguards))
	require.JSONEq(t, `{"mode":"auto","policy":{"id":"p1"}}`, string(again.Safeguards))

	var omitted ClaudeRequest
	require.NoError(t, json.Unmarshal([]byte(`{"model":"claude-opus-4-6"}`), &omitted))
	require.Empty(t, omitted.Safeguards)
	encoded, err = json.Marshal(omitted)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "safeguards")
}
