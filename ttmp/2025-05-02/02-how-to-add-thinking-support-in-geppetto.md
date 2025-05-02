# Integrating "Thinking" Features from OpenAI and Anthropic Claude into Geppetto

## 1. Executive Summary: Embracing Advanced Reasoning in Geppetto

Modern Large Language Models (LLMs) like OpenAI's o-series and Anthropic's Claude 3.7 are introducing powerful "thinking" capabilities. These features allow the models to perform more complex reasoning, chain-of-thought processes, and detailed analysis before generating a final response. Integrating these capabilities into Geppetto requires updates to both our API client interactions and our internal event handling system (`pkg/events`). This guide outlines the necessary steps, from identifying the right Go libraries and understanding the new API surfaces to modifying our event structures and handlers to seamlessly accommodate these advanced reasoning streams. By the end of this process, Geppetto will be equipped to leverage the full potential of these next-generation models, offering users richer and more nuanced interactions.

## 2. Table of Contents

*   [1. Executive Summary: Embracing Advanced Reasoning in Geppetto](#1-executive-summary-embracing-advanced-reasoning-in-geppetto)
*   [2. Table of Contents](#2-table-of-contents)
*   [3. Identifying Your Go OpenAI Client](#3-identifying-your-go-openai-client)
*   [4. Understanding the New API Surfaces](#4-understanding-the-new-api-surfaces)
    *   [4.1 OpenAI o-series Features](#41-openai-o-series-features)
    *   [4.2 Claude 3.7 "Extended Thinking"](#42-claude-37-extended-thinking)
*   [5. Implementation Roadmap: Integrating Thinking Support](#5-implementation-roadmap-integrating-thinking-support)
    *   [5.1 Phase 1: API Schema & Client Updates](#51-phase-1-api-schema--client-updates)
        *   [5.1.1 OpenAI Client Update](#511-openai-client-update)
        *   [5.1.2 Claude Client Schema Patch](#512-claude-client-schema-patch)
    *   [5.2 Phase 2: Geppetto Event Layer Upgrade (`pkg/events`)](#52-phase-2-geppetto-event-layer-upgrade-pkgevents)
        *   [5.2.1 Extending the `EventType` Enum](#521-extending-the-eventtype-enum)
        *   [5.2.2 Defining New Event Payload Structs](#522-defining-new-event-payload-structs)
        *   [5.2.3 Updating the Event Factory (`NewEventFromJson`)](#523-updating-the-event-factory-neweventfromjson)
        *   [5.2.4 Extending the `ChatEventHandler` Interface](#524-extending-the-chateventhandler-interface)
        *   [5.2.5 Updating Event Printers](#525-updating-event-printers)
        *   [5.2.6 Enhancing Metadata (Optional)](#526-enhancing-metadata-optional)
    *   [5.3 Phase 3: Connecting APIs to Events](#53-phase-3-connecting-apis-to-events)
        *   [5.3.1 OpenAI Stream Adapter](#531-openai-stream-adapter)
        *   [5.3.2 Claude Stream Adapter](#532-claude-stream-adapter)
    *   [5.4 Phase 4: Testing and Validation](#54-phase-4-testing-and-validation)
*   [6. Rollout Plan & Timeline](#6-rollout-plan--timeline)
*   [7. Conclusion & Next Steps](#7-conclusion--next-steps)
*   [8. Resources & References](#8-resources--references)

## 3. Identifying Your Go OpenAI Client

Before diving into implementation, it's crucial to confirm which Go client library Geppetto is using for OpenAI interactions. Our analysis suggests Geppetto vendors **`github.com/sashabaranov/go-openai`**.

Key characteristics of this library (as of v1.37.0+):

*   **Built-in Support:** Already includes fields for o-series features like `ReasoningEffort`, `ReasoningSummary`, `tool_choice`, and parallel `tool_calls`.
*   **Stable Schema:** Retains the `ChatCompletionRequest` struct name, simplifying migration.

Here's a quick example of how you might use these new fields with this library:

```go
import "github.com/sashabaranov/go-openai"

// ... inside your API call logic ...

req := openai.ChatCompletionRequest{
    Model:           openai.GPT4o, // Or other o-series models
    Messages:        []openai.ChatCompletionMessage{ /* ... */ },
    Tools:           []openai.Tool{ /* ... */ }, // Replaces Functions
    ToolChoice:      openai.ToolChoiceAuto,       // "auto", "required", etc.
    ReasoningEffort: "high",                     // Pulled from settings.OpenAI.ReasoningEffort
    ReasoningSummary: "concise",                 // Pulled from settings.OpenAI.ReasoningSummary
    Stream:          true,                       // Deltas now include tool_calls and reasoning_summary
}

// client is your *openai.Client instance
stream, err := client.CreateChatCompletionStream(ctx, req)
// ... handle stream ...
```

**Action:** Verify the `go.mod` file confirms `github.com/sashabaranov/go-openai`. If a different library (like the official `openai/openai-go`) is used, the field names are generally identical, requiring only an import path change. Update to at least `v1.37.0` (latest is `v1.39.0`) using:

```bash
go get -u github.com/sashabaranov/go-openai@v1.39.0
```

## 4. Understanding the New API Surfaces

Both OpenAI and Anthropic introduce distinct changes to their APIs to support thinking features.

### 4.1 OpenAI o-series Features

*   **Request Body:** New fields added directly to `ChatCompletionRequest` (as shown above): `ReasoningEffort`, `ReasoningSummary`, `ToolChoice`, `Tools` (replacing `Functions`). These are controlled via flags defined in `pkg/steps/ai/settings/openai/chat.yaml`.
*   **Streaming:** The final chunk of the stream *may* contain a `ReasoningSummary` field alongside the usual `content` delta or `tool_calls`. Note: OpenAI does *not* stream the intermediate thinking steps, only the final summary.
*   **Billing:** A new `reasoning_tokens` field appears in the `usage` object of the response, billed separately.

### 4.2 Claude 3.7 "Extended Thinking"

Claude's approach involves more significant changes, particularly for streaming:

*   **Request Body:** A new top-level `thinking` object is added, controlled via flags defined in `pkg/steps/ai/settings/claude/claude.yaml`:
    ```json
    {
      "model": "claude-3.7-sonnet-20250424",
      "messages": [ ... ],
      "max_tokens": 4096,
      "thinking": { // Enabled via --claude-enable-thinking
        "type": "enabled",
        "budget_tokens": 4096 // Set via --claude-thinking-budget
      },
      "stream": true
    }
    ```
*   **Streaming:** This is the major difference. Claude interleaves new Server-Sent Event (SSE) types within the existing stream:
    *   `thinking_delta`: Contains chunks of the model's internal reasoning process.
    *   `signature_delta`: A cryptographic signature related to the thinking block (optional to process).
    *   These appear *before* the usual `content_block_delta` events for the final answer.
*   **Validation & Billing:** The API validates `budget_tokens`. Crucially, these thinking tokens count towards the standard `usage.output_tokens` – there's no separate `reasoning_tokens` field like OpenAI.
*   **Multi-turn Preservation:** When continuing a conversation where the previous assistant message included thinking, the *raw* thinking block JSON must be resent verbatim in the next user request to avoid errors.

## 5. Implementation Roadmap: Integrating Thinking Support

This roadmap breaks down the integration into manageable phases, covering both API client adjustments and the necessary updates to Geppetto's internal event system.

### 5.1 Phase 1: API Schema & Client Updates

#### 5.1.1 OpenAI Client Update

*   **[ ] Action:** Ensure `github.com/sashabaranov/go-openai` is updated to `v1.39.0` or later.
*   **[ ] Action:** Add the new fields to the `openai.Settings` struct in `pkg/steps/ai/settings/openai/settings.go`:
    ```go
    // In geppetto/pkg/steps/ai/settings/openai/settings.go
    type Settings struct {
        // ... existing fields ...
        ReasoningEffort  *string           `yaml:"reasoning_effort,omitempty\" glazed.parameter:\"openai-reasoning-effort\"`
        ReasoningSummary *string           `yaml:"reasoning_summary,omitempty\" glazed.parameter:\"openai-reasoning-summary\"`
    }
    // Update NewSettings to provide defaults (\"auto\")
    ```
*   **[ ] Action:** Define the corresponding flags in `pkg/steps/ai/settings/openai/chat.yaml`:
    ```yaml
    # In geppetto/pkg/steps/ai/settings/openai/chat.yaml (add to existing flags)
    flags:
      # ... existing flags ...
      - name: openai-reasoning-effort
        type: choice # Assuming glazed supports choice type, otherwise use string
        help: Reasoning effort level (OpenAI o-series)
        default: auto
        choices:
         - auto
         - low
         - medium
         - high
      - name: openai-reasoning-summary
        type: choice # Assuming glazed supports choice type, otherwise use string
        help: Reasoning summary detail level (OpenAI o-series)
        default: auto
        choices:
         - auto
         - concise
         - detailed
    ```
*   **[ ] Action:** Modify the code that constructs `openai.ChatCompletionRequest` to read `*settings.OpenAI.ReasoningEffort` and `*settings.OpenAI.ReasoningSummary` and include them in the request if they are not the default "auto" value (or handle "auto" appropriately based on API behavior).

#### 5.1.2 Claude Client Schema Patch

*(Assuming an in-house client for Claude, as no official Go SDK exists yet)*

*   **[ ] Action:** Define Go structs mirroring the new `thinking` request parameter (if not already done as part of client implementation):
    ```go
    // In your Claude client package
    // ThinkingCfg specifies the configuration for extended thinking mode.
    type ThinkingCfg struct {
        Type   string `json:"type"`             // Should be "enabled"
        Budget int    `json:"budget_tokens"`    // Min 1024, <= max_tokens
    }
    ```
*   **[ ] Action:** Add the new fields to the `claude.Settings` struct in `pkg/steps/ai/settings/claude/settings.go`:
    ```go
    // In geppetto/pkg/steps/ai/settings/claude/settings.go
    type Settings struct {
        // ... existing fields ...
        EnableThinking bool    `yaml:"enable_thinking,omitempty" glazed.parameter:"claude-enable-thinking"`
        ThinkingBudget *int    `yaml:"thinking_budget,omitempty" glazed.parameter:"claude-thinking-budget"`
    }
    // Update NewSettings to provide defaults (EnableThinking: false, ThinkingBudget: 4096)
    ```
*   **[ ] Action:** Define the corresponding flags in `pkg/steps/ai/settings/claude/claude.yaml`:
    ```yaml
    # In geppetto/pkg/steps/ai/settings/claude/claude.yaml (add to existing flags)
    flags:
      # ... existing flags ...
      - name: claude-enable-thinking
        type: bool
        help: Enable Claude 3.7 extended thinking mode
        default: false
      - name: claude-thinking-budget
        type: int
        help: Token budget for Claude extended thinking (min 1024, requires --claude-enable-thinking)
        default: 4096
    ```
*   **[ ] Action:** Modify the code that constructs the Claude API request:
    *   Check if `settings.Claude.EnableThinking` is true.
    *   If true, add the `thinking` object to the request JSON, using `settings.Claude.ThinkingBudget` for `budget_tokens`.
    *   Add validation logic: ensure `*settings.Claude.ThinkingBudget >= 1024` and `*settings.Claude.ThinkingBudget <= max_tokens` *before* sending the request.
*   **[ ] Action:** Implement logic to cache and resend the raw `thinking` or `redacted_thinking` blocks from previous assistant turns in multi-turn chats.

### 5.2 Phase 2: Geppetto Event Layer Upgrade (`pkg/events`)

This is crucial for propagating the new information from the API clients to downstream consumers like the CLI or UI. We'll modify files within `geppetto/pkg/events/`.

#### 5.2.1 Extending the `EventType` Enum

*   **[ ] Action:** Add new constants to `EventType` in `chat-events.go`:
    ```go
    // In geppetto/pkg/events/chat-events.go
    const (
        // ... existing event types ...
        EventTypeStart             EventType = "start"
        EventTypeFinal             EventType = "final"
        EventTypePartialCompletion EventType = "partial"
        EventTypeToolCall          EventType = "tool-call"
        EventTypeToolResult        EventType = "tool-result"
        EventTypeError             EventType = "error"
        EventTypeInterrupt         EventType = "interrupt"

        // New types for reasoning/thinking:
        EventTypeReasoningSummary EventType = "reasoning-summary" // OpenAI: Final summary
        EventTypeThinkingDelta    EventType = "thinking-delta"    // Claude: Streamed thought chunk
        EventTypeSignatureDelta   EventType = "signature-delta"   // Claude: Optional signature
    )
    ```

#### 5.2.2 Defining New Event Payload Structs

*   **[ ] Action:** Define new structs in `chat-events.go` to carry the specific payloads for these event types. They should embed `EventImpl` like existing events.
    ```go
    // In geppetto/pkg/events/chat-events.go

    // EventReasoningSummary carries the final reasoning summary from OpenAI.
    type EventReasoningSummary struct {
        EventImpl
        Summary string `json:"summary"`
    }

    // NewReasoningSummaryEvent creates a new EventReasoningSummary.
    func NewReasoningSummaryEvent(md EventMetadata, step *steps.StepMetadata, s string) *EventReasoningSummary {
        // Implementation similar to other New...Event functions
        return &EventReasoningSummary{
            EventImpl: EventImpl{ /* ... Type_: EventTypeReasoningSummary ... */ },
            Summary:   s,
        }
    }
    var _ Event = &EventReasoningSummary{} // Interface check

    // EventThinkingDelta carries a chunk of streamed thought from Claude.
    // Mimics EventPartialCompletion structure for UI consistency.
    type EventThinkingDelta struct {
        EventImpl
        Delta string `json:"delta"`         // The new chunk of text
        Full  string `json:"full_so_far"`   // Accumulated thought text so far
    }

    // NewThinkingDeltaEvent creates a new EventThinkingDelta.
    func NewThinkingDeltaEvent(md EventMetadata, step *steps.StepMetadata, delta string, full string) *EventThinkingDelta {
        // Implementation similar to NewPartialCompletionEvent
        return &EventThinkingDelta{
             EventImpl: EventImpl{ /* ... Type_: EventTypeThinkingDelta ... */ },
             Delta:     delta,
             Full:      full,
        }
    }
    var _ Event = &EventThinkingDelta{} // Interface check

    // Optional: If you need to surface the signature separately
    type EventSignatureDelta struct {
        EventImpl
        // Add fields if needed, maybe just the raw signature?
    }
    // func NewSignatureDeltaEvent(...) ...
    // var _ Event = &EventSignatureDelta{}
    ```

#### 5.2.3 Updating the Event Factory (`NewEventFromJson`)

*   **[ ] Action:** Modify the `NewEventFromJson` function in (likely) `chat-events.go` or a dedicated `json.go` file to handle the new types:
    ```go
    // In geppetto/pkg/events/json.go (or wherever NewEventFromJson resides)
    func NewEventFromJson(b []byte) (Event, error) {
        var base EventImpl
        if err := json.Unmarshal(b, &base); err != nil {
            return nil, fmt.Errorf("failed to unmarshal base event: %w", err)
        }
        base.payload = b // Store raw payload

        switch base.Type() {
        // ... existing cases ...
        case EventTypePartialCompletion:
             return ToTypedEvent[EventPartialCompletion](&base)
        case EventTypeToolCall:
             return ToTypedEvent[EventToolCall](&base)

        // Add new cases:
        case EventTypeThinkingDelta:
            // Use a helper that properly unmarshals the payload into EventThinkingDelta
            typedEvent, err := base.ToThinkingDelta()
            if err != nil { return nil, err } // Define ToThinkingDelta helper
            return typedEvent, nil
        case EventTypeReasoningSummary:
            // Use a helper that properly unmarshals the payload into EventReasoningSummary
            typedEvent, err := base.ToReasoningSummary()
            if err != nil { return nil, err } // Define ToReasoningSummary helper
            return typedEvent, nil
        case EventTypeSignatureDelta: // Optional
            // Use a helper that properly unmarshals the payload into EventSignatureDelta
            typedEvent, err := base.ToSignatureDelta() // Define ToSignatureDelta helper
            if err != nil { return nil, err }
            return typedEvent, nil


        default:
            // Maybe return base or an unknown event type error
            return &base, fmt.Errorf("unknown event type: %s", base.Type())
        }
    }

    // Define necessary helper methods on EventImpl or elsewhere
    func (e *EventImpl) ToThinkingDelta() (*EventThinkingDelta, error) {
        var typedEvent EventThinkingDelta
        if err := json.Unmarshal(e.payload, &typedEvent); err != nil {
            return nil, fmt.Errorf("failed to unmarshal thinking delta payload: %w", err)
        }
        // Copy base fields if not automatically handled by embedding unmarshal
        typedEvent.EventImpl = *e
        return &typedEvent, nil
    }

    // Similar helpers for ToReasoningSummary, ToSignatureDelta...
    ```

#### 5.2.4 Extending the `ChatEventHandler` Interface

*   **[ ] Action:** Add handler methods for the new event types to the `ChatEventHandler` interface (likely in `event-router.go` or `chat-events.go`):
    ```go
    // In geppetto/pkg/events/event-router.go (or similar)
    type ChatEventHandler interface {
        // ... existing methods like HandlePartialCompletion, HandleToolCall ...
        HandleStart(ctx context.Context, e *EventPartialCompletionStart) error
        HandleFinal(ctx context.Context, e *EventFinal) error
        HandlePartialCompletion(ctx context.Context, e *EventPartialCompletion) error
        HandleToolCall(ctx context.Context, e *EventToolCall) error
        HandleToolResult(ctx context.Context, e *EventToolResult) error
        HandleError(ctx context.Context, e *EventError) error
        HandleInterrupt(ctx context.Context, e *EventInterrupt) error

        // Add new handlers:
        HandleThinkingDelta(ctx context.Context, e *EventThinkingDelta) error
        HandleReasoningSummary(ctx context.Context, e *EventReasoningSummary) error
        // Optional: HandleSignatureDelta(ctx context.Context, e *EventSignatureDelta) error
    }
    ```
*   **[ ] Action:** Update the router dispatch logic (e.g., in `createChatDispatchHandler` in `event-router.go`) to call these new handler methods based on the event type.

#### 5.2.5 Updating Event Printers

*   **[ ] Action:** Modify the printer implementations (`printer.go` for structured output, `step-printer-func.go` for CLI) to handle the new event types gracefully.
    *   **Text/CLI Mode:** Treat `ThinkingDelta` similarly to `PartialCompletion` (stream the `Delta`). Display `ReasoningSummary` clearly at the end, perhaps formatted as YAML or a distinct block.
    *   **JSON/YAML Mode:** Add cases to the type switches to serialize `EventThinkingDelta` and `EventReasoningSummary` appropriately.

#### 5.2.6 Enhancing Metadata (Optional)

*   **[ ] Action:** Consider adding a `ReasoningTokens` field to the `UsageInfo` struct (if one exists within `EventMetadata` or a similar structure) to capture OpenAI's specific usage data.
    ```go
    // Potentially in geppetto/pkg/events/chat-events.go or similar
    type UsageInfo struct {
        InputTokens     int `json:"input_tokens"`
        OutputTokens    int `json:"output_tokens"`
        ReasoningTokens int `json:"reasoning_tokens,omitempty"` // OpenAI only
    }

    type EventMetadata struct {
        // ... existing fields ...
        Usage *UsageInfo `json:"usage,omitempty" yaml:"usage,omitempty"`
        // ... other metadata ...
    }
    ```
    Populate this field when processing the final response from OpenAI. Claude's thinking tokens are already included in `OutputTokens`.

### 5.3 Phase 3: Connecting APIs to Events

This involves modifying the code that processes the streaming responses from the APIs and publishes the corresponding Geppetto events using the `Publisher` interface (likely found via `publish.go`).

#### 5.3.1 OpenAI Stream Adapter

*   **[ ] Action:** In the code handling the `go-openai` stream, check the final stream chunk for the `delta.ReasoningSummary` field. If present, publish an `EventReasoningSummary`:
    ```go
    // Example within OpenAI stream processing loop
    if streamResp.Choices[0].Delta.ReasoningSummary != "" {
        // publisher is your events.Publisher instance
        // md is the EventMetadata, step is the *steps.StepMetadata
        evt := events.NewReasoningSummaryEvent(md, step, streamResp.Choices[0].Delta.ReasoningSummary)
        if err := publisher.Publish(ctx, evt); err != nil {
             // Handle error
        }
        // Optionally populate UsageInfo.ReasoningTokens from streamResp.Usage here
    }
    ```

#### 5.3.2 Claude Stream Adapter

*   **[ ] Action:** Enhance the SSE scanner/parser for Claude streams. Add cases for the new event types (`thinking_delta`, `signature_delta`). Maintain an accumulator for the `Full` field of `EventThinkingDelta`.
    ```go
    // Example within Claude SSE processing loop
    var accumulatedThinking string // Keep track per turn

    switch eventType { // Determined from SSE event.Event field
    case "thinking_delta":
        var thinkingData struct { Text string `json:"text"` } // Adjust based on actual payload
        _ = json.Unmarshal([]byte(eventData), &thinkingData) // eventData is SSE event.Data
        accumulatedThinking += thinkingData.Text
        evt := events.NewThinkingDeltaEvent(md, step, thinkingData.Text, accumulatedThinking)
        if err := publisher.Publish(ctx, evt); err != nil { /* Handle error */ }

    case "signature_delta":
        // Optional: Parse signature, create and publish EventSignatureDelta if needed
        // You might choose to simply ignore this event type if the signature isn't used.
        // Reset accumulatedThinking *after* content_block_stop or message_stop? Verify API docs.

    case "content_block_delta":
        // Existing logic for partial completion
        // Ensure accumulatedThinking is handled correctly across turns or reset appropriately

    case "message_stop":
         // Final event, ensure usage is captured correctly (output_tokens includes thinking)
         accumulatedThinking = "" // Reset for next turn

    // ... other cases like content_block_start, message_start, error ...
    }
    ```
    **Gotcha:** Pay close attention to the exact event sequence (`thinking_delta` → `signature_delta` → `content_block_start` → `content_block_delta` → `content_block_stop` → `message_stop`) and manage the `accumulatedThinking` state correctly, especially around `content_block_stop` or `message_stop`.

### 5.4 Phase 4: Testing and Validation

*   **[ ] Action:** Write unit tests for:
    *   Marshaling/unmarshaling new event types (`EventReasoningSummary`, `EventThinkingDelta`).
    *   `NewEventFromJson` correctly identifying and parsing new types.
    *   Router dispatching new event types to the correct (mock) handlers.
    *   Printer output correctness for new event types (snapshot testing).
    *   Claude SSE parser correctly generating `ThinkingDelta` events from mock SSE streams.
    *   Claude request builder validation (budget checks, model compatibility).
*   **[ ] Action:** Write integration tests:
    *   OpenAI call successfully emitting `ReasoningSummary` event when requested.
    *   Claude call (behind feature flag) successfully emitting `ThinkingDelta` events from a real or mocked API response.
    *   Test multi-turn Claude conversations preserving the `thinking` block.
*   **[ ] Action:** Perform end-to-end (e2e) testing using the CLI or UI to ensure the thinking streams and summaries are displayed correctly.

## 6. Rollout Plan & Timeline

| Day   | Deliverable(s)                                                              | Testing       | Status |
| :---- | :-------------------------------------------------------------------------- | :------------ | :----- |
| **1** | Update OpenAI lib, Define Claude structs & YAML flags, Add EventType enum values | Unit          | `[ ]`  |
| **2** | Define Event payload structs & builders, Update `NewEventFromJson` & helpers | Unit          | `[ ]`  |
| **3** | Extend `ChatEventHandler`, Update Router, Update Printers                 | Unit          | `[ ]`  |
| **4** | Implement OpenAI adapter changes (emit `ReasoningSummary`)                | Integration   | `[ ]`  |
| **5** | Implement Claude schema patch (request building, validation using flags)    | Unit          | `[ ]`  |
| **6-7** | Implement Claude SSE adapter (emit `ThinkingDelta`), feature flag         | Integration   | `[ ]`  |
| **8** | Add metadata enhancements (optional), Comprehensive testing (Unit, Integ) | Unit, Integ | `[ ]`  |
| **9** | E2E testing (CLI/UI), Documentation updates (READMEs, examples)           | E2E           | `[ ]`  |
| **10**| Merge feature branches, Tag release (e.g., `geppetto v0.6.0`)             | Regression    | `[ ]`  |

**Recommendation:** Use feature flags, especially for the Claude extended thinking integration, to allow for gradual rollout and testing in production environments without impacting all users immediately.

## 7. Conclusion & Next Steps

Integrating these advanced reasoning features positions Geppetto at the forefront of LLM interaction tools. By carefully updating our API clients and extending our robust event system, we can provide users with transparent access to the "thinking" processes of models like OpenAI's o-series and Claude 3.7.

**Immediate Next Steps:**

1.  Confirm the OpenAI library and update it (`go get`). Add corresponding Go struct fields and YAML flag definitions.
2.  Add Claude Go struct fields and YAML flag definitions.
3.  Create feature branches (`openai-reasoning`, `claude-thinking`).
4.  Begin implementing Phase 1 (API updates using new settings) and Phase 2 (Event layer extensions) concurrently or sequentially based on developer availability.
5.  Focus heavily on testing, particularly the Claude SSE parsing and multi-turn state management.

This foundational work enables future enhancements, such as visualizing the thinking process in the UI or allowing users finer-grained control over reasoning parameters.

## 8. Resources & References

*   **Go OpenAI Library (sashabaranov):**
    *   [GitHub Repository](https://github.com/sashabaranov/go-openai)
    *   [Go Packages](https://pkg.go.dev/github.com/sashabaranov/go-openai)
    *   [Releases (v1.39.0+)](https://github.com/sashabaranov/go-openai/releases)
*   **Anthropic Claude Documentation:**
    *   [Messages API (Streaming)](https://docs.anthropic.com/en/api/messages-streaming)
    *   [Claude 3.7 Sonnet Parameters (AWS Bedrock - good reference)](https://docs.aws.amazon.com/bedrock/latest/userguide/model-parameters-anthropic-claude-37.html)
*   **Geppetto Codebase:**
    *   `pkg/events/`: Core event definitions, router, printers.
    *   `pkg/steps/ai/settings/`: Location of settings structs and YAML flag definitions.
        *   `openai/settings.go` & `openai/chat.yaml`
        *   `claude/settings.go` & `claude/claude.yaml`
    *   API Client Packages: (e.g., `pkg/openai/`, `pkg/claude/` - *location to be confirmed*)
    *   Command Packages: (e.g., `pkg/cmds/`) - Where API clients are likely invoked.
*   **Related Issues:**
    *   [crewAI Bug (Thinking Block Preservation)](https://github.com/crewAIInc/crewAI/issues/2323) - Illustrates the importance of multi-turn state.

---
*This document consolidates planning notes and incorporates details from the Geppetto codebase (`pkg/events/`, `pkg/steps/ai/settings/`). Refer to the specific files mentioned for the most up-to-date implementation details.* 