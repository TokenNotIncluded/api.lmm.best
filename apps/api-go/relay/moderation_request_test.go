package relay

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestModerationOutboundSchemaPreservesNativeInput(t *testing.T) {
	// A strict upstream rejects shared chat parameters, even stream:false.
	body, err := nativeModerationRequestBody([]byte(`{"model":"omni-moderation-latest","input":[{"type":"text","text":"hello"},{"type":"image_url","image_url":{"url":"data:image/png;base64,aGVsbG8="}}],"messages":[{"role":"system","content":"channel prompt"}],"stream":false,"max_tokens":1,"temperature":0}`))
	require.NoError(t, err)
	require.JSONEq(t, `{"model":"omni-moderation-latest","input":[{"type":"text","text":"hello"},{"type":"image_url","image_url":{"url":"data:image/png;base64,aGVsbG8="}}]}`, string(body))
}

func TestModerationOutboundRejectsInvalidOverridesBeforeUpstream(t *testing.T) {
	for _, body := range []string{
		`{"model":"omni-moderation-latest","input":"hello","stream":true}`,
		`{"model":"omni-moderation-latest","input":"hello","stream":"false"}`,
		`{"model":"omni-moderation-latest","input":null}`,
		`{"model":"omni-moderation-latest","messages":[]}`,
		`{"model":"","input":"hello"}`,
	} {
		_, err := nativeModerationRequestBody([]byte(body))
		require.Error(t, err, body)
	}
}
