package claudemessages

import (
	"fmt"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/relayconvert/convmeta"
	kitutil "github.com/LIghtJUNction/api.lmm.best/relaykit/relayconvert/kitutil"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/relayconvert/reasoning"
)

const (
	webSearchMaxUsesLow    = 1
	webSearchMaxUsesMedium = 5
	webSearchMaxUsesHigh   = 10
)

type openRouterRequestReasoning struct {
	Enabled   bool   `json:"enabled"`
	Effort    string `json:"effort,omitempty"`
	MaxTokens int    `json:"max_tokens,omitempty"`
	Exclude   bool   `json:"exclude,omitempty"`
}

// OpenAI-compatible effort levels this compatibility path may emit. It is
// derived from the shared suffix table rather than restated, so a level the
// fork does not declare for OpenAI-compatible targets is never sent.
func gptCompatibilitySupportsEffort(effort string) bool {
	for _, suffix := range reasoning.OpenAIEffortSuffixes {
		if strings.TrimPrefix(suffix, "-") == effort {
			return true
		}
	}
	return false
}

// clampGPTCompatibilityEffort keeps an unsupported level from turning a
// request that previously succeeded into an upstream rejection. `max` is not
// declared by this path, so it is clamped to the strongest declared level.
func clampGPTCompatibilityEffort(effort string) string {
	if effort == "" || gptCompatibilitySupportsEffort(effort) {
		return effort
	}
	if effort == "max" {
		return "xhigh"
	}
	return ""
}

func mapClaudeReasoningEffortFromEffort(effort string) string {
	switch normalized := strings.ToLower(strings.TrimSpace(effort)); normalized {
	case "low", "medium", "high", "xhigh", "max":
		return clampGPTCompatibilityEffort(normalized)
	case "minimal":
		return "low"
	case "none":
		return "none"
	default:
		return ""
	}
}

// budgetEffortLadder is the semantic budget→effort mapping. Callers clamp the
// result to what the outbound compatibility path declares.
func budgetEffortLadder(budgetTokens int) string {
	switch {
	case budgetTokens <= 0:
		return "none"
	case budgetTokens <= 1024:
		return "low"
	case budgetTokens <= 8192:
		return "medium"
	case budgetTokens < 16000:
		return "high"
	case budgetTokens < 32000:
		return "xhigh"
	default:
		return "max"
	}
}

func mapClaudeThinkingToReasoningEffort(thinkingType string, budgetTokens int) string {
	switch strings.ToLower(strings.TrimSpace(thinkingType)) {
	case "adaptive":
		return "medium"
	case "enabled":
		return clampGPTCompatibilityEffort(budgetEffortLadder(budgetTokens))
	default:
		return ""
	}
}

func ClaudeMessagesRequestToOpenAIChat(claudeRequest dto.ClaudeRequest, info convmeta.Meta) (*dto.GeneralOpenAIRequest, error) {
	openAIRequest := dto.GeneralOpenAIRequest{
		Model:       claudeRequest.Model,
		Temperature: claudeRequest.Temperature,
	}
	if claudeRequest.MaxTokens != nil {
		openAIRequest.MaxTokens = kitutil.GetPointer(*claudeRequest.MaxTokens)
	}
	if claudeRequest.TopP != nil {
		openAIRequest.TopP = kitutil.GetPointer(*claudeRequest.TopP)
	}
	if claudeRequest.TopK != nil {
		openAIRequest.TopK = kitutil.GetPointer(*claudeRequest.TopK)
	}
	if claudeRequest.Stream != nil {
		openAIRequest.Stream = kitutil.GetPointer(*claudeRequest.Stream)
	}

	options := convmeta.OptionsOf(info)
	isOpenRouter := options.OpenRouterDialect
	if isOpenRouter {
		if effort := claudeRequest.GetEfforts(); effort != "" {
			effortBytes, _ := kitutil.Marshal(effort)
			openAIRequest.Verbosity = effortBytes
		}
		if claudeRequest.Thinking != nil {
			var reasoningConfig openRouterRequestReasoning
			if claudeRequest.Thinking.Type == "enabled" {
				reasoningConfig = openRouterRequestReasoning{
					Enabled:   true,
					MaxTokens: claudeRequest.Thinking.GetBudgetTokens(),
				}
			} else if claudeRequest.Thinking.Type == "adaptive" {
				reasoningConfig = openRouterRequestReasoning{
					Enabled: true,
				}
			}
			reasoningJSON, err := kitutil.Marshal(reasoningConfig)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal reasoning: %w", err)
			}
			openAIRequest.Reasoning = reasoningJSON
		}
	} else if info != nil {
		thinkingSuffix := "-thinking"
		if strings.HasSuffix(info.GetOriginModelName(), thinkingSuffix) &&
			!strings.HasSuffix(openAIRequest.Model, thinkingSuffix) {
			openAIRequest.Model = openAIRequest.Model + thinkingSuffix
		}

		if options.EnableMessagesToGPTCompatibility {
			if effort := claudeRequest.GetEfforts(); effort != "" {
				openAIRequest.ReasoningEffort = mapClaudeReasoningEffortFromEffort(effort)
			}
			if openAIRequest.ReasoningEffort == "" && claudeRequest.Thinking != nil {
				openAIRequest.ReasoningEffort = mapClaudeThinkingToReasoningEffort(
					claudeRequest.Thinking.Type,
					claudeRequest.Thinking.GetBudgetTokens(),
				)
			}
		}
	}

	if len(claudeRequest.StopSequences) == 1 {
		openAIRequest.Stop = claudeRequest.StopSequences[0]
	} else if len(claudeRequest.StopSequences) > 1 {
		openAIRequest.Stop = claudeRequest.StopSequences
	}

	tools, _ := kitutil.Any2Type[[]dto.Tool](claudeRequest.Tools)
	openAITools := make([]dto.ToolCallRequest, 0)
	for _, claudeTool := range tools {
		openAITool := dto.ToolCallRequest{
			Type: "function",
			Function: dto.FunctionRequest{
				Name:        claudeTool.Name,
				Description: claudeTool.Description,
				Parameters:  claudeTool.InputSchema,
			},
		}
		openAITools = append(openAITools, openAITool)
	}
	openAIRequest.Tools = openAITools

	openAIMessages := make([]dto.Message, 0)
	if claudeRequest.System != nil {
		if claudeRequest.IsStringSystem() && claudeRequest.GetStringSystem() != "" {
			openAIMessage := dto.Message{
				Role: "system",
			}
			openAIMessage.SetStringContent(claudeRequest.GetStringSystem())
			openAIMessages = append(openAIMessages, openAIMessage)
		} else {
			systems := claudeRequest.ParseSystem()
			if len(systems) > 0 {
				openAIMessage := dto.Message{
					Role: "system",
				}
				isOpenRouterClaude := isOpenRouter && strings.HasPrefix(convmeta.UpstreamModelName(info), "anthropic/claude")
				if isOpenRouterClaude {
					systemMediaMessages := make([]dto.MediaContent, 0, len(systems))
					for _, system := range systems {
						message := dto.MediaContent{
							Type:         "text",
							Text:         system.GetText(),
							CacheControl: system.CacheControl,
						}
						systemMediaMessages = append(systemMediaMessages, message)
					}
					openAIMessage.SetMediaContent(systemMediaMessages)
				} else {
					systemStr := ""
					for _, system := range systems {
						if system.Text != nil {
							systemStr += *system.Text
						}
					}
					openAIMessage.SetStringContent(systemStr)
				}
				openAIMessages = append(openAIMessages, openAIMessage)
			}
		}
	}

	type unnamedToolResult struct {
		index int
		id    string
	}
	toolNames := make(map[string]string)
	var unnamedToolResults []unnamedToolResult

	for _, claudeMessage := range claudeRequest.Messages {
		openAIMessage := dto.Message{
			Role: claudeMessage.Role,
		}
		if claudeMessage.IsStringContent() {
			openAIMessage.SetStringContent(claudeMessage.GetStringContent())
		} else {
			content, err := claudeMessage.ParseContent()
			if err != nil {
				return nil, err
			}
			var toolCalls []dto.ToolCallRequest
			mediaMessages := make([]dto.MediaContent, 0, len(content))

			for _, mediaMsg := range content {
				if _, exists := toolNames[mediaMsg.Id]; !exists {
					toolNames[mediaMsg.Id] = mediaMsg.Name
				}
				switch mediaMsg.Type {
				case "text", "input_text":
					message := dto.MediaContent{
						Type:         "text",
						Text:         mediaMsg.GetText(),
						CacheControl: mediaMsg.CacheControl,
					}
					mediaMessages = append(mediaMessages, message)
				case "image":
					imageData := fmt.Sprintf("data:%s;base64,%s", mediaMsg.Source.MediaType, mediaMsg.Source.Data)
					mediaMessage := dto.MediaContent{
						Type:     "image_url",
						ImageUrl: &dto.MessageImageUrl{Url: imageData},
					}
					mediaMessages = append(mediaMessages, mediaMessage)
				case "tool_use":
					toolCall := dto.ToolCallRequest{
						ID:   mediaMsg.Id,
						Type: "function",
						Function: dto.FunctionRequest{
							Name:      mediaMsg.Name,
							Arguments: requestToJSONString(mediaMsg.Input),
						},
					}
					toolCalls = append(toolCalls, toolCall)
				case "tool_result":
					toolName := mediaMsg.Name
					if toolName == "" {
						unnamedToolResults = append(unnamedToolResults, unnamedToolResult{index: len(openAIMessages), id: mediaMsg.ToolUseId})
					}
					oaiToolMessage := dto.Message{
						Role:       "tool",
						Name:       &toolName,
						ToolCallId: mediaMsg.ToolUseId,
					}
					if mediaMsg.IsStringContent() {
						oaiToolMessage.SetStringContent(mediaMsg.GetStringContent())
					} else {
						content, media := claudeToolResultToChat(mediaMsg.ParseMediaContent())
						oaiToolMessage.SetStringContent(content)
						// Tool messages only carry text in Chat Completions. Keep media on
						// the source user message so all parallel tool replies stay contiguous.
						mediaMessages = append(mediaMessages, media...)
					}
					openAIMessages = append(openAIMessages, oaiToolMessage)
				case "thinking", "redacted_thinking":
					continue
				}
			}

			if len(toolCalls) > 0 {
				openAIMessage.SetToolCalls(toolCalls)
			}
			if len(mediaMessages) > 0 {
				openAIMessage.SetMediaContent(mediaMessages)
			}
		}
		if len(openAIMessage.ParseContent()) > 0 || len(openAIMessage.ToolCalls) > 0 {
			openAIMessages = append(openAIMessages, openAIMessage)
		}
	}

	for _, result := range unnamedToolResults {
		*openAIMessages[result.index].Name = toolNames[result.id]
	}

	openAIRequest.Messages = openAIMessages
	return &openAIRequest, nil
}

// claudeToolResultToChat maps structured Claude tool_result content onto Chat
// Completions. Text remains on the tool message; images are returned for the
// following user message. Unknown block types keep the historical JSON fallback.
func claudeToolResultToChat(blocks []dto.ClaudeMediaMessage) (string, []dto.MediaContent) {
	texts := make([]string, 0, len(blocks))
	media := make([]dto.MediaContent, 0, len(blocks))
	for _, block := range blocks {
		switch {
		case block.Type == "text" || block.Type == "input_text":
			if text := block.GetText(); text != "" {
				texts = append(texts, text)
			}
		case block.Type == "image" && block.Source != nil:
			url := block.Source.Url
			if url == "" {
				url = fmt.Sprintf("data:%s;base64,%s", block.Source.MediaType, kitutil.Interface2String(block.Source.Data))
			}
			media = append(media, dto.MediaContent{Type: "image_url", ImageUrl: &dto.MessageImageUrl{Url: url}})
		default:
			return requestToJSONString(blocks), nil
		}
	}
	switch {
	case len(texts) == 0 && len(media) == 0:
		return requestToJSONString(blocks), nil
	case len(texts) == 0:
		return "[image]", media
	default:
		return strings.Join(texts, "\n"), media
	}
}

func requestToJSONString(v interface{}) string {
	b, err := kitutil.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}
