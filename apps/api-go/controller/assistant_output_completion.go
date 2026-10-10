package controller

import (
	"strings"
	"unicode"

	"github.com/gin-gonic/gin"
)

const (
	assistantOutputIncompleteCode = "ASSISTANT_OUTPUT_INCOMPLETE"
	assistantOutputIncompleteZH   = "输出达到长度上限，未完整。你可以要求继续生成剩余文字。"
	assistantOutputIncompleteEN   = "The output reached its length limit and is incomplete. You can ask for the remaining text."
)

func assistantOutputLengthLimited(response assistantOpenAIResponse) bool {
	return len(response.Choices) > 0 && response.Choices[0].FinishReason == "length"
}

// No automatic continuation: replaying the model/tool plan could repeat a
// completed write, and a new model call would consume another response budget.
// A visible notice and stable non-retryable outcome keep that boundary explicit.
func assistantIncompleteOutputContent(c *gin.Context, content string) string {
	if strings.HasPrefix(content, assistantOutputIncompleteZH) || strings.HasPrefix(content, assistantOutputIncompleteEN) {
		return content
	}
	languageText := content
	if c != nil {
		if latest := assistantUserContextFromGin(c).LatestUserRequest; latest != "" {
			languageText = latest
		}
	}
	notice := assistantOutputIncompleteEN
	if strings.ContainsFunc(languageText, func(r rune) bool { return unicode.Is(unicode.Han, r) }) {
		notice = assistantOutputIncompleteZH
	}
	if content == "" {
		return notice
	}
	return notice + "\n\n" + content
}
