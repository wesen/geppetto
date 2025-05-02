package claude

import (
	"sort"

	"github.com/go-go-golems/geppetto/pkg/events"

	"github.com/go-go-golems/geppetto/pkg/conversation"
	"github.com/go-go-golems/geppetto/pkg/steps"
	"github.com/go-go-golems/geppetto/pkg/steps/ai/claude/api"
	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
)

// ContentBlockMerger manages the streaming response from Claude AI API for chat completion.
// It processes various event types to reconstruct the full message response, handling
// multiple content blocks, metadata updates, and error conditions.
//
// The merger accumulates content from text and tool use blocks, manages message metadata,
// and provides access to the reconstructed response and any errors encountered.
//
// Usage:
//  1. Create a new merger with NewContentBlockMerger()
//  2. For each streaming event, call Add() to process and update the internal state
//  3. Use Text() to get the current accumulated response text
//  4. Access the full response with Response() or any errors with Error()
//
// The merger handles parallel stream fragments, ensuring proper ordering and
// combination of content blocks in the final response.
type ContentBlockMerger struct {
	metadata      events.EventMetadata
	stepMetadata  *steps.StepMetadata
	response      *api.MessageResponse
	error         *api.Error
	contentBlocks map[int]*api.ContentBlock
	inputTokens   int // Track input tokens from start event

	// thinkingBlockIndex is a designated index for the thinking block, as thinking deltas don't have indices.
	thinkingBlockIndex int
}

const internalThinkingBlockIndex = -1

func NewContentBlockMerger(metadata events.EventMetadata, stepMetadata *steps.StepMetadata) *ContentBlockMerger {
	return &ContentBlockMerger{
		metadata:           metadata,
		stepMetadata:       stepMetadata,
		contentBlocks:      make(map[int]*api.ContentBlock),
		inputTokens:        0,
		thinkingBlockIndex: internalThinkingBlockIndex, // Use a dedicated index for accumulating thinking deltas
	}
}

// Text returns the accumulated text from *in-progress* blocks.
// It concatenates text blocks and includes a representation for tool use blocks.
// This is primarily useful for snapshotting state during streaming, but may not
// represent the final ordered output accurately until the message is stopped.
// Thinking and RedactedThinking blocks are ignored.
func (cbm *ContentBlockMerger) Text() string {
	res := ""
	// Create a slice to store the keys of in-progress blocks
	keys := make([]int, 0, len(cbm.contentBlocks))

	// Collect all keys from the map
	for k := range cbm.contentBlocks {
		// Ignore the internal thinking block index
		if k != cbm.thinkingBlockIndex {
			keys = append(keys, k)
		}
	}

	// Sort the keys in ascending order
	sort.Ints(keys)

	// Iterate over the sorted keys of in-progress blocks
	for _, k := range keys {
		block := cbm.contentBlocks[k]
		switch block.Type {
		case api.ContentTypeText:
			res += block.Text
		case api.ContentTypeToolUse:
			// Append a string representation for tool use blocks
			toolText := "Tool Call: " + block.Name + "\n" +
				"ID: " + block.ID + "\n" +
				block.Input // Assuming Input contains the JSON string
			res += toolText
		}
		// Ignore other types like Thinking, RedactedThinking for this view
	}

	return res
}

// Helper function to build the text representation from finalized blocks
func (cbm *ContentBlockMerger) getFinalizedText() string {
	res := ""
	if cbm.response == nil {
		return ""
	}
	for _, content := range cbm.response.Content {
		switch v := content.(type) {
		case api.TextContent:
			res += v.Text
		case api.ToolUseContent:
			// Consistent format with the in-progress representation
			toolText := "Tool Call: " + v.Name + "\n" +
				"ID: " + v.ID + "\n" +
				v.Input
			res += toolText
		}
		// Ignore ThinkingContent and RedactedThinkingContent
	}
	return res
}

func (cbm *ContentBlockMerger) Response() *api.MessageResponse {
	return cbm.response
}

func (cbm *ContentBlockMerger) Error() *api.Error {
	return cbm.error
}

const ModelMetadataSlug = "claude_model"
const StopReasonMetadataSlug = "claude_stop_reason"
const StopSequenceMetadataSlug = "claude_stop_sequence"

// TODO(manuel, 2024-06-07) Unify counting usage across steps and LLM calls so that we can use it for openai and other completion APIs as well.

const MessageIdMetadataSlug = "claude_message_id"
const RoleMetadataSlug = "claude_role"

// updateUsage updates the usage statistics and metadata from an event usage.
// It accumulates output tokens and sets input tokens only once from the start event.
func (cbm *ContentBlockMerger) updateUsage(event api.StreamingEvent) {
	var currentInputTokens, currentOutputTokens int
	if cbm.metadata.Usage != nil {
		currentInputTokens = cbm.metadata.Usage.InputTokens
		currentOutputTokens = cbm.metadata.Usage.OutputTokens
	}

	newInputTokens := currentInputTokens
	newOutputTokens := currentOutputTokens
	usageUpdated := false

	// Prioritize Usage field from delta events for incremental output tokens
	if event.Usage != nil {
		// Input tokens from delta usage might be inaccurate or redundant, prefer stored value
		newOutputTokens += event.Usage.OutputTokens
		usageUpdated = true
	}

	// Use Message field for initial input tokens (MessageStart) or final totals (MessageStop)
	if event.Message != nil {
		if event.Type == api.MessageStartType && event.Message.Usage.InputTokens > 0 {
			newInputTokens = event.Message.Usage.InputTokens
			cbm.inputTokens = newInputTokens // Store initial input tokens
			// Reset output tokens at start
			newOutputTokens = 0
			usageUpdated = true
		} else if event.Type == api.MessageStopType {
			// Use final total output tokens if provided in stop message
			newOutputTokens = event.Message.Usage.OutputTokens
			// Ensure input tokens are also set correctly from the final message if needed
			if event.Message.Usage.InputTokens > 0 {
				newInputTokens = event.Message.Usage.InputTokens
			}
			usageUpdated = true
		}
	}

	// If usage was updated, create or update the metadata field
	if usageUpdated {
		// If input tokens were never set, use the stored value (should be set by MessageStart)
		if newInputTokens == 0 {
			newInputTokens = cbm.inputTokens
		}

		cbm.metadata.Usage = &conversation.Usage{
			InputTokens:  newInputTokens,
			OutputTokens: newOutputTokens,
			// ReasoningTokens: 0, // TODO: How to track reasoning tokens?
		}
	}
	// Don't initialize usage to a non-nil value if there are no updates and it's currently nil
	// This keeps the field as nil when no token counts have been seen
}

func (cbm *ContentBlockMerger) Add(event api.StreamingEvent) ([]events.Event, error) {
	log.Trace().Object("event", event).Msg("ContentBlockMerger.Add")

	switch event.Type {
	case api.PingType:
		return []events.Event{}, nil

	case api.MessageStartType:
		if event.Message == nil {
			return nil, errors.New("MessageStartType event must have a message")
		}
		cbm.response = event.Message
		cbm.stepMetadata.Metadata[ModelMetadataSlug] = event.Message.Model
		cbm.stepMetadata.Metadata[MessageIdMetadataSlug] = event.Message.ID
		cbm.stepMetadata.Metadata[RoleMetadataSlug] = event.Message.Role

		// Update event metadata with common fields
		cbm.metadata.Engine = event.Message.Model
		cbm.updateUsage(event)

		return []events.Event{events.NewStartEvent(cbm.metadata, cbm.stepMetadata)}, nil

	case api.MessageDeltaType:
		if event.Delta == nil {
			return nil, errors.New("MessageDeltaType event must have a delta")
		}
		if event.Delta.StopReason != "" {
			cbm.stepMetadata.Metadata[StopReasonMetadataSlug] = event.Delta.StopReason
			cbm.metadata.StopReason = &event.Delta.StopReason
		}
		if event.Delta.StopSequence != "" {
			cbm.stepMetadata.Metadata[StopSequenceMetadataSlug] = event.Delta.StopSequence
		}

		cbm.updateUsage(event)

		return []events.Event{events.NewPartialCompletionEvent(cbm.metadata, cbm.stepMetadata, "", cbm.Text())}, nil

	case api.MessageStopType:
		if cbm.response == nil {
			return nil, errors.New("MessageStopType event received before MessageStartType")
		}

		// Finalize metadata from the stop event message if present
		if event.Message != nil {
			if event.Message.StopReason != "" {
				cbm.stepMetadata.Metadata[StopReasonMetadataSlug] = event.Message.StopReason
				cbm.metadata.StopReason = &event.Message.StopReason
			}
			if event.Message.StopSequence != "" {
				cbm.stepMetadata.Metadata[StopSequenceMetadataSlug] = event.Message.StopSequence
			}
			// Update usage one last time from the final message
			cbm.updateUsage(event)
		}

		// Create a potentially reordered final content slice
		finalContent := cbm.response.Content // Get content added by block stops

		// Prepend the thinking block if it exists and has content
		if thinkingBlock, exists := cbm.contentBlocks[cbm.thinkingBlockIndex]; exists {
			if thinkingBlock.Type == api.ContentTypeThinking && thinkingBlock.Thinking != "" {
				finalThinkingBlock := api.NewThinkingContent(thinkingBlock.Thinking, thinkingBlock.Signature)
				// Prepend the thinking block
				finalContent = append([]api.Content{finalThinkingBlock}, finalContent...)
			}
			// Remove the thinking block from the map now it's finalized
			delete(cbm.contentBlocks, cbm.thinkingBlockIndex)
		}

		// Update the response content with the potentially reordered list
		cbm.response.Content = finalContent

		// Use helper function for final text (based on the potentially reordered content)
		finalText := cbm.getFinalizedText()
		return []events.Event{events.NewFinalEvent(cbm.metadata, cbm.stepMetadata, finalText)}, nil

	case api.ContentBlockStartType:
		if cbm.response == nil {
			return nil, errors.New("ContentBlockStartType event received before MessageStartType")
		}
		if event.ContentBlock == nil {
			return nil, errors.New("ContentBlockStartType event must have a content block")
		}
		if event.Index < 0 {
			return nil, errors.New("ContentBlockStartType event must have a non-negative index")
		}
		if _, exists := cbm.contentBlocks[event.Index]; exists {
			return nil, errors.Errorf("ContentBlockStartType event with index %d already exists", event.Index)
		}

		// Store the started content block
		cbm.contentBlocks[event.Index] = event.ContentBlock

		// Handle specific start types if necessary (e.g., logging)
		switch event.ContentBlock.Type {
		case api.ContentTypeText:
			log.Trace().Int("index", event.Index).Msg("Started text block")
		case api.ContentTypeToolUse:
			log.Trace().Int("index", event.Index).Str("tool_name", event.ContentBlock.Name).Msg("Started tool use block")
		case api.ContentTypeThinking:
			// NOTE: We don't expect ContentBlockStart for 'thinking' based on stream examples.
			// Thinking deltas don't have an index and are handled separately.
			// If this occurs, log a warning.
			log.Warn().Int("index", event.Index).Msg("Unexpected ContentBlockStartType for thinking block")
		case api.ContentTypeRedactedThinking:
			// This block type arrives whole in the start event according to docs.
			log.Trace().Int("index", event.Index).Msg("Started (and received) redacted thinking block")
			// Optional: Emit an event here if needed?
		default:
			log.Warn().Int("index", event.Index).Str("type", string(event.ContentBlock.Type)).Msg("Started unknown content block type")
		}

		// TODO(manuel, 2024-07-04) We should have a proper BlockStart message here
		return []events.Event{}, nil

	case api.ContentBlockDeltaType:
		if cbm.response == nil {
			return nil, errors.New("ContentBlockDeltaType event received before MessageStartType")
		}
		if event.Delta == nil {
			return nil, errors.New("ContentBlockDeltaType event must have a delta")
		}

		cbm.updateUsage(event) // Update usage from delta event

		deltaText := ""
		accumulatedThinking := ""

		// Handle thinking/signature deltas separately as they don't have an index
		switch event.Delta.Type {
		case api.ThinkingDeltaType:
			if event.Delta.Thinking == "" {
				return []events.Event{}, nil // Ignore empty thinking deltas
			}
			deltaText = event.Delta.Thinking
			// Find or create the thinking block at the designated internal index
			thinkingBlock, exists := cbm.contentBlocks[cbm.thinkingBlockIndex]
			if !exists {
				thinkingBlock = &api.ContentBlock{Type: api.ContentTypeThinking}
				cbm.contentBlocks[cbm.thinkingBlockIndex] = thinkingBlock
			}
			if thinkingBlock.Type == api.ContentTypeThinking {
				thinkingBlock.Thinking += deltaText // Accumulate to Thinking field
				accumulatedThinking = thinkingBlock.Thinking
			} else {
				log.Warn().Int("index", cbm.thinkingBlockIndex).Msg("Block at designated thinking index is not of type Thinking")
				accumulatedThinking = deltaText // Fallback
			}
			return []events.Event{events.NewThinkingDeltaEvent(cbm.metadata, cbm.stepMetadata, deltaText, accumulatedThinking)}, nil

		case api.SignatureDeltaType:
			if event.Delta.Signature == "" {
				return []events.Event{}, nil // Ignore empty signature deltas
			}
			// Find the thinking block at the designated internal index
			thinkingBlock, exists := cbm.contentBlocks[cbm.thinkingBlockIndex]
			if !exists || thinkingBlock.Type != api.ContentTypeThinking {
				log.Warn().Int("index", cbm.thinkingBlockIndex).Msg("Received SignatureDeltaType without preceding/active thinking block")
				return []events.Event{}, nil
			}
			// Store the signature on the thinking block
			thinkingBlock.Signature = event.Delta.Signature
			log.Trace().Int("index", cbm.thinkingBlockIndex).Msg("Stored signature on thinking block")
			// Optionally emit event (currently disabled in plan)
			// return []events.Event{events.NewSignatureDeltaEvent(cbm.metadata, cbm.stepMetadata, event.Delta.Signature)}, nil
			return []events.Event{}, nil
		}

		// Handle indexed deltas (text, input_json)
		cb, exists := cbm.contentBlocks[event.Index]
		if !exists {
			// This might happen if delta arrives before start for some reason? Log warning.
			log.Warn().Int("index", event.Index).Str("delta_type", string(event.Delta.Type)).Msg("ContentBlockDeltaType event received for non-existent index")
			return nil, errors.Errorf("ContentBlockDeltaType event with index %d does not exist", event.Index)
		}

		switch event.Delta.Type {
		case api.TextDeltaType:
			if cb.Type != api.ContentTypeText {
				log.Warn().Int("index", event.Index).Str("block_type", string(cb.Type)).Msg("Received TextDeltaType for non-text block")
				// Attempt to append anyway? Or return error?
			}
			delta := event.Delta.Text
			cb.Text += delta // cb.Text now holds the full text for this block so far

			// Construct completion text: finalized blocks (from response) + current block's full text
			completionText := cbm.getFinalizedText() + cb.Text

			return []events.Event{events.NewPartialCompletionEvent(cbm.metadata, cbm.stepMetadata, delta, completionText)}, nil
		case api.InputJSONDeltaType:
			if cb.Type != api.ContentTypeToolUse {
				log.Warn().Int("index", event.Index).Str("block_type", string(cb.Type)).Msg("Received InputJSONDeltaType for non-tool_use block")
				// Attempt to append anyway?
			}
			delta := event.Delta.PartialJSON
			cb.Input += delta
			// TODO(manuel, 2024-07-04) This is where we would do partial tool call streaming
			_ = delta                    // Keep delta var used
			return []events.Event{}, nil // No event for partial tool input yet
		default:
			log.Warn().Int("index", event.Index).Str("delta_type", string(event.Delta.Type)).Msg("Unhandled delta type for indexed block")
			return []events.Event{}, nil
		}

	case api.ContentBlockStopType:
		if cbm.response == nil {
			return nil, errors.New("ContentBlockStopType event received before MessageStartType")
		}
		cb, exists := cbm.contentBlocks[event.Index]
		if !exists {
			// This may happen if we've already processed a stop event for this index
			// and removed it from contentBlocks. Log a warning but don't error out.
			log.Warn().Int("index", event.Index).Msg("ContentBlockStopType event received for non-existent index (may be duplicate stop)")
			return []events.Event{}, nil // Return empty events instead of error for duplicate stops
		}

		evts := []events.Event{}

		switch cb.Type {
		case api.ContentTypeText:
			log.Trace().Int("index", event.Index).Msg("Stopping text block")
			finalizedBlock := api.NewTextContent(cb.Text)
			cbm.response.Content = append(cbm.response.Content, finalizedBlock)
			// Construct completion text using finalized blocks + this one
			completionText := cbm.getFinalizedText() // Includes the block just added
			evts = append(evts, events.NewPartialCompletionEvent(cbm.metadata, cbm.stepMetadata, "", completionText))

		case api.ContentTypeToolUse:
			log.Trace().Int("index", event.Index).Str("tool_name", cb.Name).Msg("Stopping tool use block")
			finalizedBlock := api.NewToolUseContent(cb.ID, cb.Name, cb.Input)
			cbm.response.Content = append(cbm.response.Content, finalizedBlock)
			evts = append(evts, events.NewToolCallEvent(cbm.metadata, cbm.stepMetadata, events.ToolCall{
				ID:    cb.ID,
				Name:  cb.Name,
				Input: cb.Input,
			}))

		case api.ContentTypeThinking:
			// This stop event comes *after* signature_delta according to docs.
			// We accumulate thinking deltas using internalThinkingBlockIndex.
			// This indexed stop event might correspond to that internal block.
			log.Trace().Int("index", event.Index).Msg("Stopping thinking block stream")
			// The block is added to response content in MessageStopType. No action needed here.

		case api.ContentTypeRedactedThinking:
			// Redacted blocks arrive whole in ContentBlockStart.
			log.Trace().Int("index", event.Index).Msg("Stopping redacted thinking block")
			finalizedBlock := api.NewRedactedThinkingContent(cb.Data)
			cbm.response.Content = append(cbm.response.Content, finalizedBlock)
			// Optional: Emit a specific event?

		case api.ContentTypeImage, api.ContentTypeToolResult:
			// These are input types, shouldn't appear in response stream.
			log.Error().Str("type", string(cb.Type)).Int("index", event.Index).Msg("Unexpected content block type in response stream stop event")
			return nil, errors.Errorf("Unsupported content block type in stop event: %s", cb.Type)

		default:
			log.Warn().Str("type", string(cb.Type)).Int("index", event.Index).Msg("Stopping unknown content block type")
			return nil, errors.Errorf("Unknown content block type in stop event: %s", cb.Type)
		}

		// Remove the block from the map now that it's stopped and added to response
		// Keep the thinking block though, as it's added during MessageStop
		if event.Index != cbm.thinkingBlockIndex {
			delete(cbm.contentBlocks, event.Index)
		}

		return evts, nil

	case api.ErrorType:
		if event.Error == nil {
			return nil, errors.New("ErrorType event must have an error payload")
		}
		cbm.error = event.Error
		// TODO(manuel, 2024-07-24) Should this return the error or just the event?
		// Returning just the event allows the caller to decide how to handle it.
		return []events.Event{events.NewErrorEvent(cbm.metadata, cbm.stepMetadata, errors.New(event.Error.Message))}, nil

	default:
		// Use %v for potentially unknown event types
		log.Warn().Interface("event_type", event.Type).Msg("Unknown event type received")
		return nil, errors.Errorf("Unknown event type: %v", event.Type)
	}
}
