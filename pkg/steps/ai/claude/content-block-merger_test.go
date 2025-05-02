package claude

import (
	"testing"

	"github.com/go-go-golems/geppetto/pkg/conversation"
	"github.com/go-go-golems/geppetto/pkg/events"
	"github.com/go-go-golems/geppetto/pkg/steps"
	"github.com/go-go-golems/geppetto/pkg/steps/ai/claude/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Helper function to get accumulated thinking text for assertions
func getAccumulatedThinking(merger *ContentBlockMerger) string {
	if block, exists := merger.contentBlocks[merger.thinkingBlockIndex]; exists && block.Type == api.ContentTypeThinking {
		return block.Thinking
	}
	return ""
}

// Helper function to get accumulated thinking signature for assertions
func getAccumulatedThinkingSignature(merger *ContentBlockMerger) string {
	if block, exists := merger.contentBlocks[merger.thinkingBlockIndex]; exists && block.Type == api.ContentTypeThinking {
		return block.Signature
	}
	return ""
}

func TestContentBlockMerger(t *testing.T) {
	toolCallResult := `{"operation": "add", "a": 5, "b": 3}`
	finalToolCallText := "Here's the result: Tool Call: calculator\nID: tool_1\n" + toolCallResult + " is the sum."
	dummySignature := "sig_12345abcde"
	redactedData := "encrypted_data_xyz"

	tests := []struct {
		name           string
		events         []api.StreamingEvent
		expectedEvents []events.Event
		expectedError  string
		checkMetadata  func(*testing.T, *steps.StepMetadata, events.EventMetadata)
		checkResponse  func(*testing.T, *api.MessageResponse)
		checkInternal  func(*testing.T, *ContentBlockMerger)
	}{
		{
			name: "Test NewContentBlockMerger initialization",
			events: []api.StreamingEvent{
				{Type: api.MessageStartType, Message: &api.MessageResponse{}},
			},
			expectedEvents: []events.Event{
				events.NewStartEvent(events.EventMetadata{}, &steps.StepMetadata{Metadata: make(map[string]interface{})}),
			},
		},
		{
			name: "Test Add method with MessageStartType event",
			events: []api.StreamingEvent{
				{
					Type: api.MessageStartType,
					Message: &api.MessageResponse{
						Model: "claude-3.5-sonnet",
						Usage: api.Usage{InputTokens: 10},
						ID:    "msg_123",
						Role:  "assistant",
					},
				},
			},
			expectedEvents: []events.Event{
				events.NewStartEvent(
					events.EventMetadata{
						LLMMessageMetadata: conversation.LLMMessageMetadata{
							Engine: "claude-3.5-sonnet",
							Usage:  &conversation.Usage{InputTokens: 10, OutputTokens: 0},
						},
					},
					&steps.StepMetadata{Metadata: map[string]interface{}{
						ModelMetadataSlug:     "claude-3.5-sonnet",
						MessageIdMetadataSlug: "msg_123",
						RoleMetadataSlug:      "assistant",
					}},
				),
			},
			checkMetadata: func(t *testing.T, stepMeta *steps.StepMetadata, eventMeta events.EventMetadata) {
				assert.Equal(t, "claude-3.5-sonnet", stepMeta.Metadata[ModelMetadataSlug])
				assert.Equal(t, "msg_123", stepMeta.Metadata[MessageIdMetadataSlug])
				assert.Equal(t, "assistant", stepMeta.Metadata[RoleMetadataSlug])
				assert.NotContains(t, stepMeta.Metadata, StopReasonMetadataSlug)
				assert.NotContains(t, stepMeta.Metadata, StopSequenceMetadataSlug)
				require.NotNil(t, eventMeta.Usage)
				assert.Equal(t, 10, eventMeta.Usage.InputTokens)
				assert.Equal(t, 0, eventMeta.Usage.OutputTokens)
			},
		},
		{
			name: "Test Add method with MessageStopType event (with stop reason and usage)",
			events: []api.StreamingEvent{
				{Type: api.MessageStartType, Message: &api.MessageResponse{Usage: api.Usage{InputTokens: 5}}},
				{
					Type: api.MessageStopType,
					Message: &api.MessageResponse{
						StopReason: "end_turn",
						Usage:      api.Usage{InputTokens: 5, OutputTokens: 25},
					},
				},
			},
			expectedEvents: []events.Event{
				events.NewStartEvent(
					events.EventMetadata{LLMMessageMetadata: conversation.LLMMessageMetadata{Usage: &conversation.Usage{InputTokens: 5}}},
					&steps.StepMetadata{Metadata: make(map[string]interface{})},
				),
				events.NewFinalEvent(
					events.EventMetadata{
						LLMMessageMetadata: conversation.LLMMessageMetadata{
							Usage:      &conversation.Usage{InputTokens: 5, OutputTokens: 25},
							StopReason: Ptr("end_turn"),
						},
					},
					&steps.StepMetadata{Metadata: map[string]interface{}{
						StopReasonMetadataSlug: "end_turn",
					}},
					"",
				),
			},
			checkMetadata: func(t *testing.T, stepMeta *steps.StepMetadata, eventMeta events.EventMetadata) {
				assert.Equal(t, "end_turn", stepMeta.Metadata[StopReasonMetadataSlug])
			},
		},
		{
			name: "Test single text content block",
			events: []api.StreamingEvent{
				{Type: api.MessageStartType, Message: &api.MessageResponse{Usage: api.Usage{InputTokens: 2}}},
				{
					Type:         api.ContentBlockStartType,
					Index:        0,
					ContentBlock: &api.ContentBlock{Type: api.ContentTypeText},
				},
				{
					Type:  api.ContentBlockDeltaType,
					Index: 0,
					Delta: &api.Delta{Type: api.TextDeltaType, Text: "Hello, "},
					Usage: &api.Usage{OutputTokens: 1},
				},
				{
					Type:  api.ContentBlockDeltaType,
					Index: 0,
					Delta: &api.Delta{Type: api.TextDeltaType, Text: "world!"},
					Usage: &api.Usage{OutputTokens: 1},
				},
				{
					Type:  api.ContentBlockStopType,
					Index: 0,
				},
				{
					Type: api.MessageStopType,
					Message: &api.MessageResponse{
						Usage:      api.Usage{InputTokens: 2, OutputTokens: 10},
						StopReason: "end_turn",
					},
				},
			},
			expectedEvents: []events.Event{
				events.NewStartEvent(events.EventMetadata{LLMMessageMetadata: conversation.LLMMessageMetadata{Usage: &conversation.Usage{InputTokens: 2}}}, &steps.StepMetadata{Metadata: make(map[string]interface{})}),
				events.NewPartialCompletionEvent(events.EventMetadata{LLMMessageMetadata: conversation.LLMMessageMetadata{Usage: &conversation.Usage{InputTokens: 2, OutputTokens: 1}}}, &steps.StepMetadata{Metadata: make(map[string]interface{})}, "Hello, ", "Hello, "),
				events.NewPartialCompletionEvent(events.EventMetadata{LLMMessageMetadata: conversation.LLMMessageMetadata{Usage: &conversation.Usage{InputTokens: 2, OutputTokens: 2}}}, &steps.StepMetadata{Metadata: make(map[string]interface{})}, "world!", "Hello, world!"),
				events.NewPartialCompletionEvent(events.EventMetadata{LLMMessageMetadata: conversation.LLMMessageMetadata{Usage: &conversation.Usage{InputTokens: 2, OutputTokens: 2}}}, &steps.StepMetadata{Metadata: make(map[string]interface{})}, "", "Hello, world!"),
				events.NewFinalEvent(events.EventMetadata{LLMMessageMetadata: conversation.LLMMessageMetadata{Usage: &conversation.Usage{InputTokens: 2, OutputTokens: 10}, StopReason: Ptr("end_turn")}}, &steps.StepMetadata{Metadata: map[string]interface{}{StopReasonMetadataSlug: "end_turn"}}, "Hello, world!"),
			},
			checkResponse: func(t *testing.T, response *api.MessageResponse) {
				require.NotNil(t, response)
				assert.Len(t, response.Content, 1)
				require.IsType(t, api.TextContent{}, response.Content[0])
				assert.Equal(t, "Hello, world!", response.Content[0].(api.TextContent).Text)
			},
		},
		{
			name: "Test multiple content blocks (text and tool use)",
			events: []api.StreamingEvent{
				{Type: api.MessageStartType, Message: &api.MessageResponse{Usage: api.Usage{InputTokens: 5}}},
				{Type: api.ContentBlockStartType, Index: 0, ContentBlock: &api.ContentBlock{Type: api.ContentTypeText}},
				{Type: api.ContentBlockDeltaType, Index: 0, Delta: &api.Delta{Type: api.TextDeltaType, Text: "Here's the result: "}, Usage: &api.Usage{OutputTokens: 3}},
				{Type: api.ContentBlockStopType, Index: 0},
				{Type: api.ContentBlockStartType, Index: 1, ContentBlock: &api.ContentBlock{Type: api.ContentTypeToolUse, ID: "tool_1", Name: "calculator"}},
				{Type: api.ContentBlockDeltaType, Index: 1, Delta: &api.Delta{Type: api.InputJSONDeltaType, PartialJSON: toolCallResult}, Usage: &api.Usage{OutputTokens: 10}},
				{Type: api.ContentBlockStopType, Index: 1},
				{Type: api.ContentBlockStartType, Index: 2, ContentBlock: &api.ContentBlock{Type: api.ContentTypeText}},
				{Type: api.ContentBlockDeltaType, Index: 2, Delta: &api.Delta{Type: api.TextDeltaType, Text: " is the sum."}, Usage: &api.Usage{OutputTokens: 2}},
				{Type: api.ContentBlockStopType, Index: 2},
				{Type: api.MessageStopType, Message: &api.MessageResponse{Usage: api.Usage{InputTokens: 5, OutputTokens: 15}, StopReason: "tool_use"}},
			},
			expectedEvents: []events.Event{
				events.NewStartEvent(events.EventMetadata{LLMMessageMetadata: conversation.LLMMessageMetadata{Usage: &conversation.Usage{InputTokens: 5}}}, &steps.StepMetadata{Metadata: make(map[string]interface{})}),
				events.NewPartialCompletionEvent(events.EventMetadata{LLMMessageMetadata: conversation.LLMMessageMetadata{Usage: &conversation.Usage{InputTokens: 5, OutputTokens: 3}}}, &steps.StepMetadata{Metadata: make(map[string]interface{})}, "Here's the result: ", "Here's the result: "),
				events.NewPartialCompletionEvent(events.EventMetadata{LLMMessageMetadata: conversation.LLMMessageMetadata{Usage: &conversation.Usage{InputTokens: 5, OutputTokens: 3}}}, &steps.StepMetadata{Metadata: make(map[string]interface{})}, "", "Here's the result: "),
				events.NewToolCallEvent(events.EventMetadata{LLMMessageMetadata: conversation.LLMMessageMetadata{Usage: &conversation.Usage{InputTokens: 5, OutputTokens: 13}}}, &steps.StepMetadata{Metadata: make(map[string]interface{})}, events.ToolCall{ID: "tool_1", Name: "calculator", Input: toolCallResult}),
				events.NewPartialCompletionEvent(events.EventMetadata{LLMMessageMetadata: conversation.LLMMessageMetadata{Usage: &conversation.Usage{InputTokens: 5, OutputTokens: 15}}}, &steps.StepMetadata{Metadata: make(map[string]interface{})}, " is the sum.", finalToolCallText),
				events.NewPartialCompletionEvent(events.EventMetadata{LLMMessageMetadata: conversation.LLMMessageMetadata{Usage: &conversation.Usage{InputTokens: 5, OutputTokens: 15}}}, &steps.StepMetadata{Metadata: make(map[string]interface{})}, "", finalToolCallText),
				events.NewFinalEvent(events.EventMetadata{LLMMessageMetadata: conversation.LLMMessageMetadata{Usage: &conversation.Usage{InputTokens: 5, OutputTokens: 15}, StopReason: Ptr("tool_use")}}, &steps.StepMetadata{Metadata: map[string]interface{}{StopReasonMetadataSlug: "tool_use"}}, finalToolCallText),
			},
			checkResponse: func(t *testing.T, response *api.MessageResponse) {
				require.NotNil(t, response)
				assert.Len(t, response.Content, 3)
				require.IsType(t, api.TextContent{}, response.Content[0])
				assert.Equal(t, "Here's the result: ", response.Content[0].(api.TextContent).Text)
				require.IsType(t, api.ToolUseContent{}, response.Content[1])
				toolUseContent := response.Content[1].(api.ToolUseContent)
				assert.Equal(t, "tool_1", toolUseContent.ID)
				assert.Equal(t, "calculator", toolUseContent.Name)
				assert.Equal(t, toolCallResult, toolUseContent.Input)
				require.IsType(t, api.TextContent{}, response.Content[2])
				assert.Equal(t, " is the sum.", response.Content[2].(api.TextContent).Text)
			},
		},
		{
			name: "Test thinking and signature delta events",
			events: []api.StreamingEvent{
				{Type: api.MessageStartType, Message: &api.MessageResponse{ID: "msg_think_1", Usage: api.Usage{InputTokens: 3}}},
				{
					Type:  api.ContentBlockDeltaType,
					Index: 0,
					Delta: &api.Delta{
						Type:     api.ThinkingDeltaType,
						Thinking: "Let's think step by step...",
					},
					Usage: &api.Usage{OutputTokens: 5},
				},
				{
					Type:  api.ContentBlockDeltaType,
					Index: 0,
					Delta: &api.Delta{
						Type:     api.ThinkingDeltaType,
						Thinking: " The problem involves...",
					},
					Usage: &api.Usage{OutputTokens: 4},
				},
				{
					Type:  api.ContentBlockDeltaType,
					Index: 0,
					Delta: &api.Delta{
						Type:      api.SignatureDeltaType,
						Signature: dummySignature,
					},
				},
				{
					Type:         api.ContentBlockStartType,
					Index:        0,
					ContentBlock: &api.ContentBlock{Type: api.ContentTypeText},
				},
				{
					Type:  api.ContentBlockDeltaType,
					Index: 0,
					Delta: &api.Delta{Type: api.TextDeltaType, Text: "Hello."},
					Usage: &api.Usage{OutputTokens: 1},
				},
				{
					Type:  api.ContentBlockStopType,
					Index: 0,
				},
				{
					Type: api.MessageStopType,
					Message: &api.MessageResponse{
						Usage:      api.Usage{InputTokens: 3, OutputTokens: 10},
						StopReason: "stop_sequence",
					},
				},
			},
			expectedEvents: []events.Event{
				events.NewStartEvent(events.EventMetadata{LLMMessageMetadata: conversation.LLMMessageMetadata{Usage: &conversation.Usage{InputTokens: 3}}}, &steps.StepMetadata{Metadata: make(map[string]interface{})}),
				events.NewThinkingDeltaEvent(events.EventMetadata{LLMMessageMetadata: conversation.LLMMessageMetadata{Usage: &conversation.Usage{InputTokens: 3, OutputTokens: 5}}}, &steps.StepMetadata{Metadata: make(map[string]interface{})}, "Let's think step by step...", "Let's think step by step..."),
				events.NewThinkingDeltaEvent(events.EventMetadata{LLMMessageMetadata: conversation.LLMMessageMetadata{Usage: &conversation.Usage{InputTokens: 3, OutputTokens: 9}}}, &steps.StepMetadata{Metadata: make(map[string]interface{})}, " The problem involves...", "Let's think step by step... The problem involves..."),
				events.NewPartialCompletionEvent(events.EventMetadata{LLMMessageMetadata: conversation.LLMMessageMetadata{Usage: &conversation.Usage{InputTokens: 3, OutputTokens: 10}}}, &steps.StepMetadata{Metadata: make(map[string]interface{})}, "Hello.", "Hello."),
				events.NewPartialCompletionEvent(events.EventMetadata{LLMMessageMetadata: conversation.LLMMessageMetadata{Usage: &conversation.Usage{InputTokens: 3, OutputTokens: 10}}}, &steps.StepMetadata{Metadata: make(map[string]interface{})}, "", "Hello."),
				events.NewFinalEvent(events.EventMetadata{LLMMessageMetadata: conversation.LLMMessageMetadata{Usage: &conversation.Usage{InputTokens: 3, OutputTokens: 10}, StopReason: Ptr("stop_sequence")}}, &steps.StepMetadata{Metadata: map[string]interface{}{StopReasonMetadataSlug: "stop_sequence"}}, "Hello."),
			},
			checkResponse: func(t *testing.T, response *api.MessageResponse) {
				require.NotNil(t, response)
				assert.Len(t, response.Content, 2)
				require.IsType(t, api.ThinkingContent{}, response.Content[0])
				thinkingContent := response.Content[0].(api.ThinkingContent)
				assert.Equal(t, "Let's think step by step... The problem involves...", thinkingContent.Text)
				assert.Equal(t, dummySignature, thinkingContent.Signature)

				require.IsType(t, api.TextContent{}, response.Content[1])
				assert.Equal(t, "Hello.", response.Content[1].(api.TextContent).Text)
			},
		},
		{
			name: "Test redacted thinking block",
			events: []api.StreamingEvent{
				{Type: api.MessageStartType, Message: &api.MessageResponse{Usage: api.Usage{InputTokens: 4}}},
				{
					Type:  api.ContentBlockStartType,
					Index: 0,
					ContentBlock: &api.ContentBlock{
						Type: api.ContentTypeRedactedThinking,
						Data: redactedData,
					},
				},
				{
					Type:  api.ContentBlockStopType,
					Index: 0,
				},
				{Type: api.MessageStopType, Message: &api.MessageResponse{Usage: api.Usage{InputTokens: 4, OutputTokens: 1}, StopReason: "end_turn"}},
			},
			expectedEvents: []events.Event{
				events.NewStartEvent(events.EventMetadata{LLMMessageMetadata: conversation.LLMMessageMetadata{Usage: &conversation.Usage{InputTokens: 4}}}, &steps.StepMetadata{Metadata: make(map[string]interface{})}),
				events.NewFinalEvent(events.EventMetadata{LLMMessageMetadata: conversation.LLMMessageMetadata{Usage: &conversation.Usage{InputTokens: 4, OutputTokens: 1}, StopReason: Ptr("end_turn")}}, &steps.StepMetadata{Metadata: map[string]interface{}{StopReasonMetadataSlug: "end_turn"}}, ""),
			},
			checkResponse: func(t *testing.T, response *api.MessageResponse) {
				require.NotNil(t, response)
				assert.Len(t, response.Content, 1)
				require.IsType(t, api.RedactedThinkingContent{}, response.Content[0])
				redactedContent := response.Content[0].(api.RedactedThinkingContent)
				assert.Equal(t, redactedData, redactedContent.Data)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			metadata := events.EventMetadata{}
			stepMetadata := &steps.StepMetadata{
				Metadata: make(map[string]interface{}),
			}
			merger := NewContentBlockMerger(metadata, stepMetadata)

			var events_ []events.Event
			var err error

			for _, event := range tt.events {
				newEvents, newErr := merger.Add(event)
				if newErr != nil {
					err = newErr
					break
				}
				events_ = append(events_, newEvents...)
			}

			if tt.expectedError != "" {
				require.EqualError(t, err, tt.expectedError)
				return
			}

			require.NoError(t, err)

			require.Equal(t, len(tt.expectedEvents), len(events_), "Number of events mismatch")
			for i, expectedEvent := range tt.expectedEvents {
				assert.Equal(t, expectedEvent.Type(), events_[i].Type(), "Event type mismatch at index %d", i)

				assert.Equal(t, expectedEvent.Metadata().Usage, events_[i].Metadata().Usage, "Event metadata usage mismatch at index %d", i)
				assert.Equal(t, expectedEvent.Metadata().StopReason, events_[i].Metadata().StopReason, "Event metadata stop reason mismatch at index %d", i)

				// Compare payload based on type
				switch expected := expectedEvent.(type) {
				case *events.EventPartialCompletionStart:
					// Already checked type and metadata
				case *events.EventPartialCompletion:
					actual, ok := events_[i].(*events.EventPartialCompletion)
					require.True(t, ok, "Event at index %d is not EventPartialCompletion", i)
					assert.Equal(t, expected.Delta, actual.Delta, "Delta mismatch at index %d", i)
					assert.Equal(t, expected.Completion, actual.Completion, "Completion mismatch at index %d", i)
				case *events.EventToolCall:
					actual, ok := events_[i].(*events.EventToolCall)
					require.True(t, ok, "Event at index %d is not EventToolCall", i)
					assert.Equal(t, expected.ToolCall, actual.ToolCall, "ToolCall mismatch at index %d", i)
				case *events.EventFinal:
					actual, ok := events_[i].(*events.EventFinal)
					require.True(t, ok, "Event at index %d is not EventFinal", i)
					assert.Equal(t, expected.Text, actual.Text, "Final text mismatch at index %d", i)
				case *events.EventThinkingDelta:
					actual, ok := events_[i].(*events.EventThinkingDelta)
					require.True(t, ok, "Event type mismatch at index %d, expected EventThinkingDelta", i)
					assert.Equal(t, expected.Delta, actual.Delta, "Thinking Delta mismatch at index %d", i)
					assert.Equal(t, expected.Full, actual.Full, "Thinking Full mismatch at index %d", i)
				case *events.EventError:
					actual, ok := events_[i].(*events.EventError)
					require.True(t, ok, "Event type mismatch at index %d, expected EventError", i)
					assert.Equal(t, expected.ErrorString, actual.ErrorString, "Error string mismatch at index %d", i)
				default:
					// Add cases for other event types if needed
				}
			}

			// Check final step/event metadata state
			if tt.checkMetadata != nil {
				// Pass both metadata types to the check function
				finalMetadata := events_[len(events_)-1].Metadata()
				finalStepMeta := events_[len(events_)-1].StepMetadata()
				tt.checkMetadata(t, finalStepMeta, finalMetadata)
			}

			// Check final response object
			if tt.checkResponse != nil {
				tt.checkResponse(t, merger.Response())
			}
		})
	}
}

// Helper function to create a pointer to a string
func Ptr(s string) *string {
	return &s
}
