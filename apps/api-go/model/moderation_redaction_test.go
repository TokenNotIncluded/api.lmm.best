package model

import (
	"math/rand"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestModerationRedactionMatchesAssistantPrivacy(t *testing.T) {
	corpus := []string{
		"ordinary technical conversation", "Bearer abcdefghij", "bEaReR abc123XYZ=", "password=never-store", "密钥：never-store", "Cookie: secret-cookie; other=ok",
		"https://example.test/path?api_key=secret&token=abc", "sk_abcdefghijklmnopqrstuvwx", "[model_providers.vendor.sk-abcdefghijklmnopqrstuvwx]",
		"eyJabcdefghijk.abcdefghijklmn.abcdefghijklmn", "Alice+tag@example.test", "+86 13812345678", "+1 555 123 4567", "4111 1111 1111 1111",
		"IP 192.168.1.20 and 2001:db8::1 and abcd::face", "-----BEGIN RSA PRIVATE KEY-----\nabc\n-----END RSA PRIVATE KEY-----",
		"session_id=abc Bearer abcdefg password:abc alice@example.test sk-abcdefghijklmnop", "apiKey=hello １２３４５６７８９ abc::def [providers.x.rk_abcdefghijklmnopqrstuvwx]",
	}
	for _, value := range corpus {
		require.Equal(t, RedactAssistantHistoryContent(value), RedactModerationContent(value))
	}
	// Random combinations exercise replacement interactions, not merely each
	// expression in isolation (one redaction can remove another one's markers).
	draw := rand.New(rand.NewSource(20261005))
	for index := 0; index < 80; index++ {
		var value strings.Builder
		for part := 0; part < 5; part++ {
			value.WriteString(corpus[draw.Intn(len(corpus))])
			value.WriteByte(' ')
		}
		require.Equal(t, RedactAssistantHistoryContent(value.String()), RedactModerationContent(value.String()))
	}
}

func BenchmarkModerationRedactionPlain256KiB(b *testing.B) {
	value := strings.Repeat("x", ModerationMaxPayloadBytes)
	b.SetBytes(int64(len(value)))
	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		_ = RedactModerationContent(value)
	}
}
