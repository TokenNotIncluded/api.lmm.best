package openai

import (
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestOpenAIImageResponseCountUsesPayloads(t *testing.T) {
	tests := []struct {
		name string
		body string
		want int64
	}{
		{name: "object url and b64 is one image", body: `{"data":{"url":"https://example.com/a.png","b64_json":"first"}}`, want: 1},
		{name: "split url and b64 entries are one image", body: `{"data":[{"url":"https://example.com/a.png"},{"b64_json":"first"}]}`, want: 1},
		{name: "two b64 entries are two images", body: `{"data":[{"b64_json":"first"},{"b64_json":"second"}]}`, want: 2},
		{name: "metadata-only entry is not an image", body: `{"data":[{"revised_prompt":"draw a cat"}]}`, want: 0},
		{name: "missing data is zero", body: `{}`, want: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, openaiImageResponseCount([]byte(tt.body)))
		})
	}
}

func TestOpenaiImageJSONStreamUsesPayloadCount(t *testing.T) {
	oldMode := gin.Mode()
	gin.SetMode(gin.TestMode)
	t.Cleanup(func() { gin.SetMode(oldMode) })

	t.Run("object data is forwarded and billed once", func(t *testing.T) {
		body := `{"data":{"url":"https://example.com/a.png","b64_json":"first"}}`
		c, recorder, resp, info := newImageTestContext(t, body, "application/json", true)
		info.PriceData.UsePrice = true
		info.PriceData.AddOtherRatio("n", 3)

		_, err := OpenaiImageStreamHandler(c, info, resp)
		require.Nil(t, err)
		require.Equal(t, 1, strings.Count(recorder.Body.String(), "event: image_generation.completed"))
		require.Equal(t, 1.0, info.PriceData.OtherRatios()["n"])
		require.Equal(t, 1, info.ReceivedResponseCount)
	})

	t.Run("split payload entries forward both but bill one image", func(t *testing.T) {
		body := `{"data":[{"url":"https://example.com/a.png"},{"b64_json":"first"}]}`
		c, recorder, resp, info := newImageTestContext(t, body, "application/json", true)
		info.PriceData.UsePrice = true
		info.PriceData.AddOtherRatio("n", 3)

		_, err := OpenaiImageStreamHandler(c, info, resp)
		require.Nil(t, err)
		require.Equal(t, 2, strings.Count(recorder.Body.String(), "event: image_generation.completed"))
		require.Equal(t, 1.0, info.PriceData.OtherRatios()["n"])
		require.Equal(t, 2, info.ReceivedResponseCount)
	})

	t.Run("metadata-only entries emit nothing and keep requested charge", func(t *testing.T) {
		body := `{"data":[{"revised_prompt":"draw a cat"}]}`
		c, recorder, resp, info := newImageTestContext(t, body, "application/json", true)
		info.PriceData.UsePrice = true
		info.PriceData.AddOtherRatio("n", 3)

		_, err := OpenaiImageStreamHandler(c, info, resp)
		require.Nil(t, err)
		require.Equal(t, 0, strings.Count(recorder.Body.String(), "event: image_generation.completed"))
		require.Equal(t, 3.0, info.PriceData.OtherRatios()["n"])
		require.Equal(t, 0, info.ReceivedResponseCount)
	})
}
