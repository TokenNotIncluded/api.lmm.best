package ali

import (
	"net/http/httptest"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/gin-gonic/gin"
)

func TestChoicesToOpenAIImageDateReturnsEveryImage(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want []struct {
			url    string
			b64    string
			prompt string
		}
	}{
		{
			name: "several images in one choice",
			raw:  `{"output":{"choices":[{"message":{"content":[{"image":"https://img/1.png"},{"image":"https://img/2.png"}]}}]}}`,
			want: []struct {
				url    string
				b64    string
				prompt string
			}{
				{url: "https://img/1.png"},
				{url: "https://img/2.png"},
			},
		},
		{
			name: "text applies to every image in the choice",
			raw:  `{"output":{"choices":[{"message":{"content":[{"image":"https://img/1.png"},{"text":"a red dot"},{"image":"aW1hZ2U="}]}}]}}`,
			want: []struct {
				url    string
				b64    string
				prompt string
			}{
				{url: "https://img/1.png", prompt: "a red dot"},
				{b64: "aW1hZ2U=", prompt: "a red dot"},
			},
		},
		{
			name: "text-only choice does not emit an empty image",
			raw:  `{"output":{"choices":[{"message":{"content":[{"text":"no image"}]}}]}}`,
			want: nil,
		},
		{
			name: "separate choices preserve image order",
			raw:  `{"output":{"choices":[{"message":{"content":[{"image":"https://img/1.png"}]}},{"message":{"content":[{"image":"https://img/2.png"}]}}]}}`,
			want: []struct {
				url    string
				b64    string
				prompt string
			}{
				{url: "https://img/1.png"},
				{url: "https://img/2.png"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var response AliResponse
			if err := common.Unmarshal([]byte(tt.raw), &response); err != nil {
				t.Fatalf("decode Ali response: %v", err)
			}

			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			got := response.Output.ChoicesToOpenAIImageDate(c, "url")
			if len(got) != len(tt.want) {
				t.Fatalf("image count = %d, want %d: %#v", len(got), len(tt.want), got)
			}
			for i := range tt.want {
				if got[i].Url != tt.want[i].url || got[i].B64Json != tt.want[i].b64 || got[i].RevisedPrompt != tt.want[i].prompt {
					t.Fatalf("image[%d] = %#v, want url=%q b64=%q prompt=%q", i, got[i], tt.want[i].url, tt.want[i].b64, tt.want[i].prompt)
				}
			}
		})
	}
}
