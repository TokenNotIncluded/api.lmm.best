package service

import (
	"math"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/constant"
	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestEstimateTokenUnicodeEscapesMatchDecodedText(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		raw     string
		escaped string
	}{
		{name: "cjk", raw: "中文测试", escaped: `\u4e2d\u6587\u6d4b\u8bd5`},
		{name: "mixed", raw: "中A文9", escaped: `\u4e2dA\u65879`},
		{name: "surrogate_pair", raw: "😀", escaped: `\ud83d\ude00`},
	}

	providers := []Provider{OpenAI, Gemini, Claude}
	for _, provider := range providers {
		provider := provider
		for _, tc := range cases {
			tc := tc
			t.Run(string(provider)+"/"+tc.name, func(t *testing.T) {
				t.Parallel()
				rawTokens := EstimateToken(provider, tc.raw)
				escapedTokens := EstimateToken(provider, tc.escaped)
				require.Equal(t, rawTokens, escapedTokens)
			})
		}
	}
}

func TestEstimateTokenEscapedBackslashKeepsLiteralUnicodeEscape(t *testing.T) {
	t.Parallel()

	// Two backslashes represent a literal backslash followed by "u4e2d" in
	// serialized JSON. It must not be treated as an encoded Chinese rune.
	literal := `\\u4e2d`
	require.NotEqual(t, EstimateToken(OpenAI, "中"), EstimateToken(OpenAI, literal))
}

func TestEstimateTokenNumericRunsScaleByLength(t *testing.T) {
	t.Parallel()

	providers := []Provider{OpenAI, Gemini, Claude}
	lengths := []int{1, 3, 4, 6, 30, 300}
	for _, provider := range providers {
		provider := provider
		t.Run(string(provider), func(t *testing.T) {
			t.Parallel()
			m := getMultipliers(provider)
			previous := 0
			for _, length := range lengths {
				got := EstimateToken(provider, strings.Repeat("7", length))
				chunks := (length + numericRunChunkSize - 1) / numericRunChunkSize
				want := int(math.Ceil(float64(chunks) * m.Number)) + m.BasePad
				require.Equal(t, want, got, "length=%d", length)
				require.GreaterOrEqual(t, got, previous, "length=%d", length)
				previous = got
			}

			legacyConstantRun := int(math.Ceil(m.Number)) + m.BasePad
			fixedLongRun := EstimateToken(provider, strings.Repeat("7", 30))
			t.Logf("%s 30-digit estimate: legacy=%d fixed=%d", provider, legacyConstantRun, fixedLongRun)
			require.Greater(t, fixedLongRun, legacyConstantRun)
		})
	}
}

func TestEstimateTokenRepresentativePlaintextStable(t *testing.T) {
	t.Parallel()

	// These exact values are the pre-fix estimator outputs. The correction is
	// intentionally local to valid Unicode escapes and numeric runs longer than
	// three digits.
	cases := []struct {
		text                    string
		openAI, gemini, claude int
	}{
		{text: "hello world", openAI: 3, gemini: 3, claude: 3},
		{text: "func add(a, b int) int { return a + b }", openAI: 16, gemini: 15, claude: 17},
		{text: `{"name":"alice","ok":true}`, openAI: 10, gemini: 11, claude: 11},
		{text: "https://api.example.com/v1/models?q=test", openAI: 18, gemini: 22, claude: 21},
		{text: "version 3.5 beta", openAI: 7, gemini: 9, claude: 7},
	}

	for _, tc := range cases {
		require.Equal(t, tc.openAI, EstimateToken(OpenAI, tc.text), tc.text)
		require.Equal(t, tc.gemini, EstimateToken(Gemini, tc.text), tc.text)
		require.Equal(t, tc.claude, EstimateToken(Claude, tc.text), tc.text)
	}
}

func TestEstimateRequestTokenPropagatesCorrectedNonOpenAIEstimate(t *testing.T) {
	oldCountToken := constant.CountToken
	constant.CountToken = true
	t.Cleanup(func() { constant.CountToken = oldCountToken })

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	common.SetContextKey(c, constant.ContextKeyOriginalModel, "deepseek-chat")

	text := strings.Repeat("7", 30)
	meta := &types.TokenCountMeta{CombineText: text}
	info := &relaycommon.RelayInfo{}

	got, err := EstimateRequestToken(c, meta, info)
	require.NoError(t, err)
	require.Equal(t, EstimateTokenByModel("deepseek-chat", text), got)
	require.Greater(t, got, 2, "legacy estimator charged an arbitrarily long digit run as two tokens")

	info.SetEstimatePromptTokens(got)
	usage := ResponseText2Usage(c, "ok", "deepseek-chat", info.GetEstimatePromptTokens())
	require.Equal(t, got, usage.PromptTokens)
	require.True(t, common.GetContextKeyBool(c, constant.ContextKeyLocalCountTokens))
}
