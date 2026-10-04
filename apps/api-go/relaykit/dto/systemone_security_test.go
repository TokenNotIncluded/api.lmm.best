package dto

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSystemOneSecurityTextDoesNotHideMediaNamedTextFields(t *testing.T) {
	request, err := decodeAndValidateSystemOne([]byte(`{"model":"jev-latest","state":{"audio":"audio content","input_audio":"input content","image_url":"image content","base64":"base content","file_uri":"uri content","\u5371\u9669":"key content"},"questions":{"judge":{"type":"choice","instructions":{"file_url":"instruction content"},"criteria":{"yes":{"base64_data":"criterion content","unicode":"\u4e2d\u6587"}}}}}`))
	require.NoError(t, err)
	text := request.GetSecurityText()
	for _, fragment := range []string{"audio content", "input content", "image content", "base content", "uri content", "危险", "key content", "instruction content", "criterion content", "中文"} {
		require.Contains(t, text, fragment)
		require.Contains(t, request.GetTokenCountMeta().CombineText, fragment)
	}
	require.NotContains(t, text, "jev-latest")
	request.State = []byte(`{"text":"line\none\tvalue","number":42}`)
	require.Contains(t, request.GetSecurityText(), "line\none\tvalue")
	require.Contains(t, request.GetSecurityText(), "42")
}
