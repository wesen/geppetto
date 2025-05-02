# Plan for Implementing Claude Extended Thinking API

This document outlines the steps to update the `geppetto` codebase to fully support Claude's extended thinking feature based on the official documentation.

## 1. Analyze Existing Code

- **[x]** Reviewed `geppetto/pkg/steps/ai/claude/api/messages.go`: Found existing `ThinkingConfiguration` and `ThinkingContent`. `MessageResponse.FullReasoning()` exists. Need to verify/add handling for `redacted_thinking` and `signature` in `UnmarshalContent` and streaming (`streamEvents`).
- **[x]** Reviewed `geppetto/pkg/events/chat-events.go`: Found existing `EventTypeThinkingDelta`, `EventTypeSignatureDelta`, `EventThinkingDelta`, `EventSignatureDelta`. `NewEventFromJson` handles these types.
- **[x]** Reviewed `geppetto/pkg/steps/ai/claude/content-block-merger.go`: Found existing handling for `ThinkingDeltaType` (accumulates text, emits event) and `SignatureDeltaType` (logs and ignores). Adds thinking block to final response on `MessageStopType`. `Text()` ignores thinking blocks.

## 2. Update API Definitions (`geppetto/pkg/steps/ai/claude/api/`)

-   **[ ] `content.go` / `messages.go`:**
    *   Add `RedactedThinkingContent` struct to represent `{ "type": "redacted_thinking", "data": "..." }`.
    *   Add `Signature string` field to `ContentBlock` struct to store the signature associated with a thinking block.
    *   Update `UnmarshalContent` to handle `"redacted_thinking"` type and populate the new struct.
    *   Ensure `UnmarshalContent` or related logic correctly populates the `Signature` field when a signature is present (likely from `signature_delta` during streaming).

-   **[ ] `stream.go` (or wherever `streamEvents` is defined):**
    *   Verify `streamEvents` correctly parses `thinking_delta` events (current implementation seems likely correct based on merger behavior).
    *   Verify `streamEvents` correctly parses `signature_delta` events and potentially includes the signature data in the emitted `StreamingEvent`. The event structure might need adjustment (`api.StreamingEvent` needs a field for the signature payload if it doesn't have one).
    *   Add logic to handle potential `redacted_thinking` events if they appear in the stream (docs are slightly ambiguous, saying they are sent as a single event). Define how a redacted block is represented in `api.StreamingEvent`.

## 3. Update Content Block Merger (`geppetto/pkg/steps/ai/claude/content-block-merger.go`)

-   **[ ] State:** No major changes needed unless we need fine-grained signature tracking *before* `ContentBlockStop`. The `thinkingBlockIndex` approach seems viable.
-   **[ ] `Add` Method:**
    *   **`case api.ThinkingDeltaType`:**
        -   No changes needed; current implementation correctly accumulates text and emits `events.NewThinkingDeltaEvent`.
    *   **`case api.SignatureDeltaType`:**
        -   Modify to find the thinking block (using `cbm.thinkingBlockIndex`).
        -   Store the received signature (from `event.Signature` payload) into the `Signature` field of the `thinkingBlock`.
        -   Keep emitting `events.NewSignatureDeltaEvent` (or remove if deemed unnecessary noise, TBD).
        ```go
        // Pseudocode
        case api.SignatureDeltaType:
            thinkingBlock, exists := cbm.contentBlocks[cbm.thinkingBlockIndex]
            if !exists || thinkingBlock.Type != api.ContentTypeThinking {
                // Log error/warning - signature delta without preceding thinking block?
                return ...
            }
            // Assuming event.Signature holds the signature string payload
            thinkingBlock.Signature = event.Signature // <-- Store the signature
            log.Trace().Interface("signature_delta", event.Signature).Msg("Received signature delta event")
            // Optionally emit event:
            // return []events.Event{events.NewSignatureDeltaEvent(cbm.metadata, cbm.stepMetadata /*, event.Signature */)}, nil 
            return []events.Event{}, nil
        ```
    *   **`case api.ContentBlockStartType`:**
        -   Add handling for `event.ContentBlock.Type == api.ContentTypeRedactedThinking` (if `streamEvents` can produce this). Store the block. Decide if a specific event needs emission.
        ```go
        // Pseudocode inside ContentBlockStartType
        if event.ContentBlock.Type == api.ContentTypeRedactedThinking {
             // Store the block as is
             cbm.contentBlocks[event.Index] = event.ContentBlock
             // Optional: Emit a specific event?
             // return []events.Event{events.NewRedactedThinkingBlockEvent(...) } , nil
             return []events.Event{}, nil 
        }
        ```
    *   **`case api.ContentBlockStopType`:**
        -   Add specific handling for `cb.Type == api.ContentTypeThinking`. Since `MessageStopType` adds the block, this case might just need logging for clarity or can be a no-op.
        -   Add specific handling for `cb.Type == api.ContentTypeRedactedThinking`. Append the `RedactedThinkingContent` block to `cbm.response.Content`.
        ```go
        // Pseudocode inside ContentBlockStopType
        switch cb.Type {
        // ... existing cases ...
        case api.ContentTypeThinking:
             // Added in MessageStopType, maybe just log?
             log.Trace().Int("index", event.Index).Msg("Stopping thinking block")
             return []events.Event{}, nil // Or return partial completion if needed?
        case api.ContentTypeRedactedThinking:
             // Assuming RedactedThinkingContent has 'Data' field
             cbm.response.Content = append(cbm.response.Content, api.NewRedactedThinkingContent(cb.Data)) // Need constructor
             // Optional: Emit block stop event?
             return []events.Event{}, nil 
        }
        ```
    *   **`case api.MessageStopType`:**
        -   Current logic correctly appends the accumulated `thinkingBlock` if it exists. Ensure it also appends any collected `redactedThinkingBlock`s correctly (might require iterating `contentBlocks` instead of just checking `thinkingBlockIndex`). *Correction:* `ContentBlockStopType` for redacted blocks should handle appending them. `MessageStopType` should only need to handle the potentially *unclosed* stream of `thinking_delta`s via `thinkingBlockIndex`. The current logic seems sufficient for that.

## 4. Update Event Definitions (`geppetto/pkg/events/chat-events.go`) (Optional)

-   **[ ]** Consider adding `EventRedactedThinkingBlock` if specific downstream handling is needed beyond just having it in the final `MessageResponse`.
-   **[ ]** Add signature payload to `EventSignatureDelta` if needed for observability.

## 5. Testing

-   **[ ]** Add unit tests for `ContentBlockMerger` covering:
    *   Stream with `thinking_delta` and `signature_delta`.
    *   Stream with `redacted_thinking` block (mocked).
    *   Final `MessageResponse` containing `ThinkingContent` with signature.
    *   Final `MessageResponse` containing `RedactedThinkingContent`.
-   **[ ]** Integration test using the actual Claude API with the `thinking` parameter enabled.
-   **[ ]** Test with the magic string `ANTHROPIC_MAGIC_STRING_TRIGGER_REDACTED_THINKING_...` to verify redacted handling. 