package oaichat

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/relayconvert/convmeta"
)

type ChatToResponsesStreamEvent struct {
	Type    string
	Payload dto.ResponsesStreamResponse
}

type ChatToResponsesStreamState struct {
	ID          string
	Model       string
	Created     int64
	Usage       *dto.Usage
	ToolMapping convmeta.ResponsesToolMap

	err error

	status            string
	incompleteDetails *dto.IncompleteDetails
	sentCreated       bool
	finalized         bool
	nextOutputIndex   int
	sentOutputCount   int
	pendingEvents     []ChatToResponsesStreamEvent
	toolsByIndex      map[int]*chatToResponsesStreamTool
	messageSegments   []*chatToResponsesMessageSegment
	reasoningSegments []*chatToResponsesReasoningSegment
	outputOrder       []chatToResponsesOutputRef
	text              strings.Builder
}

type chatToResponsesStreamTool struct {
	ChatIndex   int
	OutputIndex int
	ID          string
	Name        string
	Arguments   strings.Builder
	Started     bool
	Done        bool
}

// A Responses reasoning item is immutable after its done event. Chat
// Completions can resume reasoning after a tool-call turn, so each resumed
// segment needs its own output item rather than appending to the closed item.
type chatToResponsesReasoningSegment struct {
	OutputIndex int
	ID          string
	Status      string
	Text        strings.Builder
	Done        bool
}

// Ordinary message items follow the same immutable segment lifecycle as reasoning.
type chatToResponsesMessageSegment struct {
	OutputIndex int
	ID          string
	Status      string
	Text        strings.Builder
	Done        bool
}

type chatToResponsesOutputRef struct {
	Kind           string
	ToolIndex      int
	ReasoningIndex int
	MessageIndex   int
}

func NewChatToResponsesStreamState(id string, model string) *ChatToResponsesStreamState {
	return &ChatToResponsesStreamState{
		ID:           id,
		Model:        model,
		Created:      time.Now().Unix(),
		Usage:        &dto.Usage{},
		status:       "completed",
		toolsByIndex: make(map[int]*chatToResponsesStreamTool),
	}
}

func ChatCompletionsStreamChunkToResponsesEvents(chunk *dto.ChatCompletionsStreamResponse, state *ChatToResponsesStreamState) ([]ChatToResponsesStreamEvent, error) {
	if chunk == nil || state == nil {
		return nil, nil
	}
	if state.err != nil {
		return nil, state.err
	}
	if state.finalized {
		return nil, nil
	}
	if state.ID == "" {
		state.ID = chunk.Id
	}
	if state.Model == "" {
		state.Model = chunk.Model
	}
	if state.Created == 0 {
		state.Created = chunk.Created
	}
	if chunk.Usage != nil {
		state.Usage = UsageFromChatUsage(chunk.Usage)
	}

	events := make([]ChatToResponsesStreamEvent, 0)
	if !state.sentCreated {
		state.sentCreated = true
		events = append(events, responsesStreamEvent(responsesEventCreated, dto.ResponsesStreamResponse{
			Type:     responsesEventCreated,
			Response: state.createdResponse(),
		}))
	}
	for _, choice := range chunk.Choices {
		if choice.Delta.GetReasoningContent() != "" {
			events = append(events, state.appendReasoningDelta(choice.Delta.GetReasoningContent())...)
		}
		if choice.Delta.GetContentString() != "" {
			events = append(events, state.appendTextDelta(choice.Delta.GetContentString())...)
		}
		for _, toolCall := range choice.Delta.ToolCalls {
			toolEvents, err := state.appendToolCallDelta(toolCall)
			if err != nil {
				state.err = err
				return nil, err
			}
			events = append(events, toolEvents...)
		}
		if choice.FinishReason != nil && strings.TrimSpace(*choice.FinishReason) != "" {
			state.applyFinishReason(*choice.FinishReason)
			doneEvents, err := state.doneDeltaEvents()
			if err != nil {
				state.err = err
				return nil, err
			}
			events = append(events, doneEvents...)
		}
	}
	return state.orderedEvents(events), nil
}

func FinalizeChatCompletionsStreamToResponses(state *ChatToResponsesStreamState) []ChatToResponsesStreamEvent {
	if state == nil || state.finalized || state.err != nil {
		return nil
	}
	events, err := state.doneDeltaEvents()
	if err != nil {
		state.err = err
		return nil
	}
	resp, err := state.finalResponse()
	if err != nil {
		state.err = err
		return nil
	}
	state.finalized = true
	eventType := responsesEventCompleted
	if state.status == "incomplete" {
		eventType = responsesEventIncomplete
	}
	events = append(events, responsesStreamEvent(eventType, dto.ResponsesStreamResponse{
		Type:     eventType,
		Response: resp,
	}))
	return state.orderedEvents(events)
}

// orderedEvents holds events behind an output whose identity is still being
// assembled. Clients must receive output_item.added in output_index order and
// before that item's deltas. Already-started text continues streaming while a
// later tool is buffered; requests without an index gap pass through directly.
func (s *ChatToResponsesStreamState) orderedEvents(events []ChatToResponsesStreamEvent) []ChatToResponsesStreamEvent {
	s.pendingEvents = append(s.pendingEvents, events...)
	var ready []ChatToResponsesStreamEvent
	for len(s.pendingEvents) > 0 {
		blocked := s.pendingEvents[:0]
		progress := false
		for _, event := range s.pendingEvents {
			eligible := false
			if event.Payload.OutputIndex == nil {
				eligible = event.Type == responsesEventCreated || (len(blocked) == 0 && s.sentOutputCount == s.nextOutputIndex)
			} else if event.Type == responsesEventOutputItemAdded {
				eligible = *event.Payload.OutputIndex == s.sentOutputCount
				if eligible {
					s.sentOutputCount++
				}
			} else {
				eligible = *event.Payload.OutputIndex < s.sentOutputCount
			}
			if eligible {
				ready = append(ready, event)
				progress = true
			} else {
				blocked = append(blocked, event)
			}
		}
		s.pendingEvents = blocked
		if !progress {
			break
		}
	}
	if len(s.pendingEvents) == 0 {
		s.pendingEvents = nil
	}
	return ready
}

// Err reports terminal conversion errors, including validation performed when
// an upstream ends without a finish_reason chunk.
func (s *ChatToResponsesStreamState) Err() error {
	if s == nil {
		return nil
	}
	return s.err
}

func (s *ChatToResponsesStreamState) UsageText() string {
	if s == nil {
		return ""
	}
	return s.text.String()
}

func (s *ChatToResponsesStreamState) appendTextDelta(delta string) []ChatToResponsesStreamEvent {
	events := make([]ChatToResponsesStreamEvent, 0, 2)
	if delta == "" {
		return events
	}
	var segment *chatToResponsesMessageSegment
	if len(s.messageSegments) > 0 {
		segment = s.messageSegments[len(s.messageSegments)-1]
	}
	if segment == nil || segment.Done {
		segment = &chatToResponsesMessageSegment{
			OutputIndex: s.nextMessageIndex(len(s.messageSegments)),
			ID:          fmt.Sprintf("%s_msg_%d", s.ID, len(s.messageSegments)),
		}
		s.messageSegments = append(s.messageSegments, segment)
		events = append(events, responsesStreamEvent(responsesEventOutputItemAdded, dto.ResponsesStreamResponse{
			Type:        responsesEventOutputItemAdded,
			OutputIndex: intPtr(segment.OutputIndex),
			Item: &dto.ResponsesOutput{
				Type:    responsesOutputTypeMessage,
				ID:      segment.ID,
				Status:  "in_progress",
				Role:    "assistant",
				Content: []dto.ResponsesOutputContent{},
			},
		}))
	}
	segment.Text.WriteString(delta)
	// Preserve aggregate ordinary text for the existing usage-accounting path.
	s.text.WriteString(delta)
	events = append(events, responsesStreamEvent(responsesEventOutputTextDelta, dto.ResponsesStreamResponse{
		Type:         responsesEventOutputTextDelta,
		OutputIndex:  intPtr(segment.OutputIndex),
		ContentIndex: intPtr(0),
		Delta:        delta,
		ItemID:       segment.ID,
	}))
	return events
}

func (s *ChatToResponsesStreamState) appendReasoningDelta(delta string) []ChatToResponsesStreamEvent {
	events := make([]ChatToResponsesStreamEvent, 0, 2)
	if delta == "" {
		return events
	}
	var segment *chatToResponsesReasoningSegment
	if len(s.reasoningSegments) > 0 {
		segment = s.reasoningSegments[len(s.reasoningSegments)-1]
	}
	if segment == nil || segment.Done {
		segment = &chatToResponsesReasoningSegment{
			OutputIndex: s.nextReasoningIndex(len(s.reasoningSegments)),
			ID:          fmt.Sprintf("%s_reasoning_%d", s.ID, len(s.reasoningSegments)),
		}
		s.reasoningSegments = append(s.reasoningSegments, segment)
		events = append(events, responsesStreamEvent(responsesEventOutputItemAdded, dto.ResponsesStreamResponse{
			Type:        responsesEventOutputItemAdded,
			OutputIndex: intPtr(segment.OutputIndex),
			Item: &dto.ResponsesOutput{
				Type:    responsesOutputTypeReasoning,
				ID:      segment.ID,
				Status:  "in_progress",
				Content: []dto.ResponsesOutputContent{},
			},
		}))
	}
	segment.Text.WriteString(delta)
	events = append(events, responsesStreamEvent(responsesEventReasoningSummaryDelta, dto.ResponsesStreamResponse{
		Type:         responsesEventReasoningSummaryDelta,
		OutputIndex:  intPtr(segment.OutputIndex),
		SummaryIndex: intPtr(0),
		Delta:        delta,
		ItemID:       segment.ID,
	}))
	return events
}

func (s *ChatToResponsesStreamState) appendToolCallDelta(toolCall dto.ToolCallResponse) ([]ChatToResponsesStreamEvent, error) {
	chatIndex := 0
	if toolCall.Index != nil {
		chatIndex = *toolCall.Index
	}
	tool := s.toolsByIndex[chatIndex]
	events := make([]ChatToResponsesStreamEvent, 0, 2)
	if tool == nil {
		tool = &chatToResponsesStreamTool{
			ChatIndex:   chatIndex,
			OutputIndex: -1,
			ID:          strings.TrimSpace(toolCall.ID),
		}
		s.toolsByIndex[chatIndex] = tool
	}
	if tool.Done {
		return nil, fmt.Errorf("received delta for completed tool call %d", chatIndex)
	}
	if id := strings.TrimSpace(toolCall.ID); id != "" {
		if tool.Started && tool.ID != id {
			return nil, fmt.Errorf("tool call %d changed id after output_item.added", chatIndex)
		}
		tool.ID = id
	}
	if name := strings.TrimSpace(toolCall.Function.Name); name != "" {
		if tool.Started {
			if tool.Name != name {
				return nil, fmt.Errorf("tool call %d changed name after argument deltas", chatIndex)
			}
		} else {
			tool.Name += name
		}
	}
	if toolCall.Function.Arguments != "" {
		tool.Arguments.WriteString(toolCall.Function.Arguments)
	}
	if strings.TrimSpace(tool.Name) != "" && tool.OutputIndex < 0 {
		tool.Name = strings.TrimSpace(tool.Name)
		tool.OutputIndex = s.nextIndex("tool", chatIndex)
	}
	if !tool.Started {
		// A name may arrive in multiple chunks. Mapped identities (including
		// prefixes) stay buffered until completion so search calls never leak
		// function_call events. Ordinary functions start at their first args.
		if tool.OutputIndex < 0 || tool.Arguments.Len() == 0 || s.bufferToolIdentity(tool.Name) {
			return nil, nil
		}
		item, err := s.toolOutput(tool, "in_progress")
		if err != nil {
			return nil, err
		}
		item.Arguments = []byte(`""`)
		tool.Started = true
		events = append(events, s.toolItemEvent(responsesEventOutputItemAdded, tool, item))
		if tool.Arguments.Len() > 0 {
			events = append(events, responsesStreamEvent(responsesEventFunctionArgsDelta, dto.ResponsesStreamResponse{
				Type:        responsesEventFunctionArgsDelta,
				OutputIndex: intPtr(tool.OutputIndex),
				ItemID:      tool.ID,
				Delta:       tool.Arguments.String(),
			}))
		}
	} else if toolCall.Function.Arguments != "" {
		events = append(events, responsesStreamEvent(responsesEventFunctionArgsDelta, dto.ResponsesStreamResponse{
			Type:        responsesEventFunctionArgsDelta,
			OutputIndex: intPtr(tool.OutputIndex),
			ItemID:      tool.ID,
			Delta:       toolCall.Function.Arguments,
		}))
	}
	return events, nil
}

func (s *ChatToResponsesStreamState) bufferToolIdentity(name string) bool {
	if strings.TrimSpace(name) == "" {
		return true
	}
	for alias := range s.ToolMapping {
		if strings.HasPrefix(alias, name) {
			return true
		}
	}
	return false
}

func (s *ChatToResponsesStreamState) toolItemEvent(eventType string, tool *chatToResponsesStreamTool, item *dto.ResponsesOutput) ChatToResponsesStreamEvent {
	payload := dto.ResponsesStreamResponse{
		Type:        eventType,
		OutputIndex: intPtr(tool.OutputIndex),
		Item:        item,
	}
	if eventType == responsesEventOutputItemAdded {
		payload.ItemID = tool.ID
	}
	return responsesStreamEvent(eventType, payload)
}

func (s *ChatToResponsesStreamState) doneDeltaEvents() ([]ChatToResponsesStreamEvent, error) {
	events := make([]ChatToResponsesStreamEvent, 0)
	status := s.outputStatus()
	outputs := make(map[int]*dto.ResponsesOutput, len(s.toolsByIndex))
	for _, tool := range s.sortedTools() {
		if tool.Done || strings.TrimSpace(tool.Name) == "" {
			continue
		}
		tool.Name = strings.TrimSpace(tool.Name)
		if tool.OutputIndex < 0 {
			tool.OutputIndex = s.nextIndex("tool", tool.ChatIndex)
		}
		item, err := s.toolOutput(tool, status)
		if err != nil {
			return nil, err
		}
		outputs[tool.ChatIndex] = item
	}
	if len(s.messageSegments) > 0 {
		segment := s.messageSegments[len(s.messageSegments)-1]
		if !segment.Done {
			segment.Done = true
			segment.Status = status
			events = append(events, responsesStreamEvent("response.output_text.done", dto.ResponsesStreamResponse{
				Type:         "response.output_text.done",
				OutputIndex:  intPtr(segment.OutputIndex),
				ContentIndex: intPtr(0),
				ItemID:       segment.ID,
			}))
			events = append(events, responsesStreamEvent(responsesEventOutputItemDone, dto.ResponsesStreamResponse{
				Type:        responsesEventOutputItemDone,
				OutputIndex: intPtr(segment.OutputIndex),
				Item:        s.messageOutput(segment, status),
			}))
		}
	}
	if len(s.reasoningSegments) > 0 {
		segment := s.reasoningSegments[len(s.reasoningSegments)-1]
		if !segment.Done {
			segment.Done = true
			segment.Status = status
			events = append(events, responsesStreamEvent(responsesEventReasoningSummaryDone, dto.ResponsesStreamResponse{
				Type:         responsesEventReasoningSummaryDone,
				OutputIndex:  intPtr(segment.OutputIndex),
				SummaryIndex: intPtr(0),
				ItemID:       segment.ID,
				Part: &dto.ResponsesReasoningSummaryPart{
					Type: "summary_text",
					Text: segment.Text.String(),
				},
			}))
			events = append(events, responsesStreamEvent(responsesEventOutputItemDone, dto.ResponsesStreamResponse{
				Type:        responsesEventOutputItemDone,
				OutputIndex: intPtr(segment.OutputIndex),
				Item:        s.reasoningOutput(segment, status),
			}))
		}
	}
	for _, tool := range s.sortedTools() {
		if tool.Done || tool.OutputIndex < 0 {
			continue
		}
		item := outputs[tool.ChatIndex]
		if item == nil {
			continue
		}
		if !tool.Started {
			added := *item
			added.Status = "in_progress"
			if item.Type == responsesOutputTypeFunctionCall {
				added.Arguments = []byte(`""`)
			}
			events = append(events, s.toolItemEvent(responsesEventOutputItemAdded, tool, &added))
			tool.Started = true
			if item.Type == responsesOutputTypeFunctionCall && tool.Arguments.Len() > 0 {
				events = append(events, responsesStreamEvent(responsesEventFunctionArgsDelta, dto.ResponsesStreamResponse{
					Type:        responsesEventFunctionArgsDelta,
					OutputIndex: intPtr(tool.OutputIndex),
					ItemID:      tool.ID,
					Delta:       tool.Arguments.String(),
				}))
			}
		}
		tool.Done = true
		if item.Type == responsesOutputTypeFunctionCall {
			payload := dto.ResponsesStreamResponse{
				Type:        responsesEventFunctionArgsDone,
				OutputIndex: intPtr(tool.OutputIndex),
				ItemID:      tool.ID,
			}
			if _, mapped := s.ToolMapping[tool.Name]; mapped {
				payload.Arguments = item.Arguments
			}
			events = append(events, responsesStreamEvent(responsesEventFunctionArgsDone, payload))
		}
		events = append(events, s.toolItemEvent(responsesEventOutputItemDone, tool, item))
	}
	return events, nil
}

func (s *ChatToResponsesStreamState) applyFinishReason(finishReason string) {
	if status, details := ResponsesStatusFromChatFinishReason(finishReason); status != "" {
		s.status = status
		s.incompleteDetails = details
	}
}

func (s *ChatToResponsesStreamState) finalResponse() (*dto.OpenAIResponsesResponse, error) {
	output := make([]dto.ResponsesOutput, 0, len(s.outputOrder))
	status := s.outputStatus()
	for _, ref := range s.outputOrder {
		switch ref.Kind {
		case "message":
			output = append(output, *s.messageOutput(s.messageSegments[ref.MessageIndex], status))
		case "reasoning":
			output = append(output, *s.reasoningOutput(s.reasoningSegments[ref.ReasoningIndex], status))
		case "tool":
			if tool := s.toolsByIndex[ref.ToolIndex]; tool != nil {
				item, err := s.toolOutput(tool, status)
				if err != nil {
					return nil, err
				}
				output = append(output, *item)
			}
		}
	}
	return &dto.OpenAIResponsesResponse{
		ID:                s.ID,
		Object:            "response",
		CreatedAt:         int(s.Created),
		Status:            []byte(fmt.Sprintf("%q", s.status)),
		IncompleteDetails: s.incompleteDetails,
		Model:             s.Model,
		Output:            output,
		Usage:             s.Usage,
	}, nil
}

func (s *ChatToResponsesStreamState) createdResponse() *dto.OpenAIResponsesResponse {
	return &dto.OpenAIResponsesResponse{
		ID:        s.ID,
		Object:    "response",
		CreatedAt: int(s.Created),
		Status:    []byte(`"in_progress"`),
		Model:     s.Model,
		Output:    []dto.ResponsesOutput{},
	}
}

func (s *ChatToResponsesStreamState) nextIndex(kind string, toolIndex int) int {
	index := s.nextOutputIndex
	s.nextOutputIndex++
	s.outputOrder = append(s.outputOrder, chatToResponsesOutputRef{Kind: kind, ToolIndex: toolIndex, ReasoningIndex: -1, MessageIndex: -1})
	return index
}

func (s *ChatToResponsesStreamState) nextReasoningIndex(reasoningIndex int) int {
	index := s.nextOutputIndex
	s.nextOutputIndex++
	s.outputOrder = append(s.outputOrder, chatToResponsesOutputRef{
		Kind:           "reasoning",
		ReasoningIndex: reasoningIndex,
		ToolIndex:      -1,
		MessageIndex:   -1,
	})
	return index
}

func (s *ChatToResponsesStreamState) nextMessageIndex(messageIndex int) int {
	index := s.nextOutputIndex
	s.nextOutputIndex++
	s.outputOrder = append(s.outputOrder, chatToResponsesOutputRef{
		Kind:           "message",
		MessageIndex:   messageIndex,
		ReasoningIndex: -1,
		ToolIndex:      -1,
	})
	return index
}

func (s *ChatToResponsesStreamState) sortedTools() []*chatToResponsesStreamTool {
	indexes := make([]int, 0, len(s.toolsByIndex))
	for index := range s.toolsByIndex {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	tools := make([]*chatToResponsesStreamTool, 0, len(indexes))
	for _, index := range indexes {
		tools = append(tools, s.toolsByIndex[index])
	}
	return tools
}

func (s *ChatToResponsesStreamState) outputStatus() string {
	if s.status == "incomplete" {
		return "incomplete"
	}
	return "completed"
}

func (s *ChatToResponsesStreamState) messageOutput(segment *chatToResponsesMessageSegment, status string) *dto.ResponsesOutput {
	if segment.Done {
		status = segment.Status
	}
	return &dto.ResponsesOutput{
		Type:   responsesOutputTypeMessage,
		ID:     segment.ID,
		Status: status,
		Role:   "assistant",
		Content: []dto.ResponsesOutputContent{
			{
				Type:        "output_text",
				Text:        segment.Text.String(),
				Annotations: []interface{}{},
			},
		},
	}
}

func (s *ChatToResponsesStreamState) reasoningOutput(segment *chatToResponsesReasoningSegment, status string) *dto.ResponsesOutput {
	if segment.Done {
		status = segment.Status
	}
	return &dto.ResponsesOutput{
		Type:   responsesOutputTypeReasoning,
		ID:     segment.ID,
		Status: status,
		Content: []dto.ResponsesOutputContent{
			{
				Type: "summary_text",
				Text: segment.Text.String(),
			},
		},
	}
}

func (s *ChatToResponsesStreamState) toolOutput(tool *chatToResponsesStreamTool, status string) (*dto.ResponsesOutput, error) {
	if tool.ID == "" {
		tool.ID = fmt.Sprintf("%s_call_%d", s.ID, tool.ChatIndex)
	}
	item := &dto.ResponsesOutput{
		Type:      responsesOutputTypeFunctionCall,
		ID:        tool.ID,
		Status:    status,
		CallId:    tool.ID,
		Name:      tool.Name,
		Arguments: chatArgumentsRawMessage(tool.Arguments.String()),
	}
	if err := restoreResponsesToolItem(item, s.ToolMapping); err != nil {
		return nil, err
	}
	return item, nil
}
