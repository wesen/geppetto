package events

import (
	"encoding/json"
	"fmt"

	"github.com/go-go-golems/geppetto/pkg/conversation"
	"github.com/go-go-golems/geppetto/pkg/steps"
	"github.com/rs/zerolog"
)

type EventType string

const (
	// EventTypeStart to EventTypeFinal are for text completion, actually
	EventTypeStart             EventType = "start"
	EventTypeFinal             EventType = "final"
	EventTypePartialCompletion EventType = "partial"

	// TODO(manuel, 2024-07-04) I'm not sure if this is needed
	EventTypeStatus EventType = "status"

	// TODO(manuel, 2024-07-04) Should potentially have a EventTypeText for a block stop here
	EventTypeToolCall   EventType = "tool-call"
	EventTypeToolResult EventType = "tool-result"
	EventTypeError      EventType = "error"
	EventTypeInterrupt  EventType = "interrupt"

	// New types for reasoning/thinking:
	EventTypeReasoningSummary EventType = "reasoning-summary" // OpenAI: Final summary
	EventTypeThinkingDelta    EventType = "thinking-delta"    // Claude / OpenAI: Streamed thought chunk
	EventTypeSignatureDelta   EventType = "signature-delta"   // Claude: Optional signature
)

type Event interface {
	Type() EventType
	Metadata() EventMetadata
	StepMetadata() *steps.StepMetadata
	Payload() []byte
}

type EventImpl struct {
	Type_     EventType           `json:"type"`
	Error_    error               `json:"error,omitempty"`
	Metadata_ EventMetadata       `json:"meta,omitempty"`
	Step_     *steps.StepMetadata `json:"step,omitempty"`

	// store payload if the event was deserialized from JSON (see NewEventFromJson), not further used
	payload []byte
}

func (e *EventImpl) MarshalZerologObject(ev *zerolog.Event) {
	ev.Str("type", string(e.Type_))

	if e.Error_ != nil {
		ev.Err(e.Error_)
	}

	if e.Metadata_ != (EventMetadata{}) {
		ev.Object("meta", e.Metadata_)
	}

	if e.Step_ != nil {
		ev.Object("step", e.Step_)
	}
}

func (e *EventImpl) Type() EventType {
	return e.Type_
}

func (e *EventImpl) Error() error {
	return e.Error_
}

func (e *EventImpl) Metadata() EventMetadata {
	return e.Metadata_
}

func (e *EventImpl) StepMetadata() *steps.StepMetadata {
	return e.Step_
}

func (e *EventImpl) Payload() []byte {
	return e.payload
}

var _ Event = &EventImpl{}

type EventPartialCompletionStart struct {
	EventImpl
}

func NewStartEvent(metadata EventMetadata, stepMetadata *steps.StepMetadata) *EventPartialCompletionStart {
	return &EventPartialCompletionStart{
		EventImpl: EventImpl{
			Type_:     EventTypeStart,
			Step_:     stepMetadata,
			Metadata_: metadata,
			payload:   nil,
		},
	}
}

var _ Event = &EventPartialCompletionStart{}

type EventInterrupt struct {
	EventImpl
	Text string `json:"text"`
	// TODO(manuel, 2024-07-04) Add all collected tool calls so far
}

func NewInterruptEvent(metadata EventMetadata, stepMetadata *steps.StepMetadata, text string) *EventInterrupt {
	return &EventInterrupt{
		EventImpl: EventImpl{
			Type_:     EventTypeInterrupt,
			Step_:     stepMetadata,
			Metadata_: metadata,
			payload:   nil,
		},
		Text: text,
	}
}

var _ Event = &EventInterrupt{}

type EventFinal struct {
	EventImpl
	Text string `json:"text"`
	// TODO(manuel, 2024-07-04) Add all collected tool calls so far
}

func NewFinalEvent(metadata EventMetadata, stepMetadata *steps.StepMetadata, text string) *EventFinal {
	return &EventFinal{
		EventImpl: EventImpl{
			Type_:     EventTypeFinal,
			Step_:     stepMetadata,
			Metadata_: metadata,
			payload:   nil,
		},
		Text: text,
	}
}

var _ Event = &EventFinal{}

type EventError struct {
	EventImpl
	ErrorString string `json:"error_string"`
}

func NewErrorEvent(metadata EventMetadata, stepMetadata *steps.StepMetadata, err error) *EventError {
	return &EventError{
		EventImpl: EventImpl{
			Type_:     EventTypeError,
			Step_:     stepMetadata,
			Metadata_: metadata,
			payload:   nil,
		},
		ErrorString: err.Error(),
	}
}

var _ Event = &EventError{}

// TODO(manuel, 2024-07-05) This might be possible to delete
type EventText struct {
	EventImpl
	Text string `json:"text"`
	// TODO(manuel, 2024-06-04) Add ToolCall information here, and potentially multiple responses (see the claude API that allows multiple content blocks)
	// This is currently stored in the metadata uder the MetadataToolCallsSlug (see chat-with-tools-step.go in openai)
}

func NewTextEvent(metadata EventMetadata, stepMetadata *steps.StepMetadata, text string) *EventText {
	return &EventText{
		EventImpl: EventImpl{
			Type_:     EventTypeStart,
			Step_:     stepMetadata,
			Metadata_: metadata,
			payload:   nil,
		},
		Text: text,
	}
}

var _ Event = &EventText{}

type ToolCall struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Input string `json:"input"`
}

// TODO(manuel, 2024-07-04) Handle multiple tool calls
type EventToolCall struct {
	EventImpl
	ToolCall ToolCall `json:"tool_call"`
}

func NewToolCallEvent(metadata EventMetadata, stepMetadata *steps.StepMetadata, toolCall ToolCall) *EventToolCall {
	return &EventToolCall{
		EventImpl: EventImpl{
			Type_:     EventTypeToolCall,
			Step_:     stepMetadata,
			Metadata_: metadata,
			payload:   nil,
		},
		ToolCall: toolCall,
	}
}

var _ Event = &EventToolCall{}

type ToolResult struct {
	ID     string `json:"id"`
	Result string `json:"result"`
}

type EventToolResult struct {
	EventImpl
	ToolResult ToolResult `json:"tool_result"`
}

func NewToolResultEvent(metadata EventMetadata, stepMetadata *steps.StepMetadata, toolResult ToolResult) *EventToolResult {
	return &EventToolResult{
		EventImpl: EventImpl{
			Type_:     EventTypeToolResult,
			Step_:     stepMetadata,
			Metadata_: metadata,
			payload:   nil,
		},
		ToolResult: toolResult,
	}
}

var _ Event = &EventToolResult{}

// TODO(manuel, 2024-07-03) Then, we can add those to the openai step as well, and to the UI, and then we should have a good way to do auto tool calling

// EventPartialCompletion is the event type for textual partial completion. We don't support partial tool completion.
type EventPartialCompletion struct {
	EventImpl
	Delta string `json:"delta"`
	// This is the complete completion string so far (when using openai, this is currently also the toolcall json)
	Completion string `json:"completion"`

	// TODO(manuel, 2024-06-04) This might need partial tool completion if it is of interest,
	// this is less important than adding tool call information to the result above
}

func NewPartialCompletionEvent(metadata EventMetadata, stepMetadata *steps.StepMetadata, delta string, completion string) *EventPartialCompletion {
	return &EventPartialCompletion{
		EventImpl: EventImpl{
			Type_:     EventTypePartialCompletion,
			Step_:     stepMetadata,
			Metadata_: metadata,
			payload:   nil,
		},
		Delta:      delta,
		Completion: completion,
	}
}

var _ Event = &EventPartialCompletion{}

// MetadataToolCallsSlug is the slug used to store ToolCall metadata as returned by the openai API
// TODO(manuel, 2024-07-04) This needs to deleted once we have a good way to do tool calling
const MetadataToolCallsSlug = "tool-calls"

// EventMetadata contains all the information that is passed along with watermill message,
// specific to chat steps.
type EventMetadata struct {
	conversation.LLMMessageMetadata
	ID       conversation.NodeID `json:"message_id" yaml:"message_id" mapstructure:"message_id"`
	ParentID conversation.NodeID `json:"parent_id" yaml:"parent_id" mapstructure:"parent_id"`
}

func (em EventMetadata) MarshalZerologObject(e *zerolog.Event) {
	e.Str("message_id", em.ID.String())
	e.Str("parent_id", em.ParentID.String())
	if em.Engine != "" {
		e.Str("engine", em.Engine)
	}
	if em.StopReason != nil && *em.StopReason != "" {
		e.Str("stop_reason", *em.StopReason)
	}
	if em.Usage != nil {
		e.Int("input_tokens", em.Usage.InputTokens)
		e.Int("output_tokens", em.Usage.OutputTokens)
	}
}

func (e *EventImpl) ToReasoningSummary() (*EventReasoningSummary, bool) {
	if e.Type() != EventTypeReasoningSummary {
		return nil, false
	}
	var typedEvent EventReasoningSummary
	if err := json.Unmarshal(e.payload, &typedEvent); err != nil {
		// Handle error appropriately, maybe log it
		return nil, false
	}
	typedEvent.EventImpl = *e // Restore base fields
	return &typedEvent, true
}

func (e *EventImpl) ToThinkingDelta() (*EventThinkingDelta, bool) {
	if e.Type() != EventTypeThinkingDelta {
		return nil, false
	}
	var typedEvent EventThinkingDelta
	if err := json.Unmarshal(e.payload, &typedEvent); err != nil {
		return nil, false
	}
	typedEvent.EventImpl = *e // Restore base fields
	return &typedEvent, true
}

func (e *EventImpl) ToSignatureDelta() (*EventSignatureDelta, bool) {
	if e.Type() != EventTypeSignatureDelta {
		return nil, false
	}
	var typedEvent EventSignatureDelta
	if err := json.Unmarshal(e.payload, &typedEvent); err != nil {
		return nil, false
	}
	typedEvent.EventImpl = *e // Restore base fields
	return &typedEvent, true
}

func NewEventFromJson(b []byte) (Event, error) {
	var base EventImpl
	if err := json.Unmarshal(b, &base); err != nil {
		return nil, fmt.Errorf("failed to unmarshal base event: %w", err)
	}
	base.payload = b // Store raw payload

	switch base.Type() {
	case EventTypeStart:
		var typedEvent EventPartialCompletionStart
		if err := json.Unmarshal(base.Payload(), &typedEvent); err != nil {
			return nil, fmt.Errorf("failed to unmarshal EventPartialCompletionStart: %w", err)
		}
		typedEvent.EventImpl = base
		return &typedEvent, nil
	case EventTypeFinal:
		var typedEvent EventFinal
		if err := json.Unmarshal(base.Payload(), &typedEvent); err != nil {
			return nil, fmt.Errorf("failed to unmarshal EventFinal: %w", err)
		}
		typedEvent.EventImpl = base
		return &typedEvent, nil
	case EventTypePartialCompletion:
		var typedEvent EventPartialCompletion
		if err := json.Unmarshal(base.Payload(), &typedEvent); err != nil {
			return nil, fmt.Errorf("failed to unmarshal EventPartialCompletion: %w", err)
		}
		typedEvent.EventImpl = base
		return &typedEvent, nil
	case EventTypeToolCall:
		var typedEvent EventToolCall
		if err := json.Unmarshal(base.Payload(), &typedEvent); err != nil {
			return nil, fmt.Errorf("failed to unmarshal EventToolCall: %w", err)
		}
		typedEvent.EventImpl = base
		return &typedEvent, nil
	case EventTypeToolResult:
		var typedEvent EventToolResult
		if err := json.Unmarshal(base.Payload(), &typedEvent); err != nil {
			return nil, fmt.Errorf("failed to unmarshal EventToolResult: %w", err)
		}
		typedEvent.EventImpl = base
		return &typedEvent, nil
	case EventTypeError:
		var typedEvent EventError
		if err := json.Unmarshal(base.Payload(), &typedEvent); err != nil {
			return nil, fmt.Errorf("failed to unmarshal EventError: %w", err)
		}
		typedEvent.EventImpl = base
		return &typedEvent, nil
	case EventTypeInterrupt:
		var typedEvent EventInterrupt
		if err := json.Unmarshal(base.Payload(), &typedEvent); err != nil {
			return nil, fmt.Errorf("failed to unmarshal EventInterrupt: %w", err)
		}
		typedEvent.EventImpl = base
		return &typedEvent, nil

	case EventTypeReasoningSummary:
		if typedEvent, ok := base.ToReasoningSummary(); ok {
			return typedEvent, nil
		}
		return nil, fmt.Errorf("failed to cast to EventReasoningSummary")
	case EventTypeThinkingDelta:
		if typedEvent, ok := base.ToThinkingDelta(); ok {
			return typedEvent, nil
		}
		return nil, fmt.Errorf("failed to cast to EventThinkingDelta")
	case EventTypeSignatureDelta:
		if typedEvent, ok := base.ToSignatureDelta(); ok {
			return typedEvent, nil
		}
		return nil, fmt.Errorf("failed to cast to EventSignatureDelta")

	case EventTypeStatus:
		// TODO(manuel, 2024-07-19) Define payload and handler for EventTypeStatus if needed
		return nil, fmt.Errorf("unhandled event type: %s", base.Type())

	default:
		return nil, fmt.Errorf("unknown event type: %s", base.Type())
	}
}

func (e EventPartialCompletionStart) MarshalZerologObject(ev *zerolog.Event) {
	e.EventImpl.MarshalZerologObject(ev)
}

func (e EventInterrupt) MarshalZerologObject(ev *zerolog.Event) {
	e.EventImpl.MarshalZerologObject(ev)
	ev.Str("text", e.Text)
}

func (e EventFinal) MarshalZerologObject(ev *zerolog.Event) {
	e.EventImpl.MarshalZerologObject(ev)
	ev.Str("text", e.Text)
}

func (e EventError) MarshalZerologObject(ev *zerolog.Event) {
	e.EventImpl.MarshalZerologObject(ev)
	ev.Str("error", e.ErrorString)
}

func (e EventText) MarshalZerologObject(ev *zerolog.Event) {
	e.EventImpl.MarshalZerologObject(ev)
	ev.Str("text", e.Text)
}

func (tc ToolCall) MarshalZerologObject(ev *zerolog.Event) {
	ev.Str("id", tc.ID).Str("name", tc.Name).Str("input", tc.Input)
}

func (e EventToolCall) MarshalZerologObject(ev *zerolog.Event) {
	e.EventImpl.MarshalZerologObject(ev)
	ev.Object("tool_call", e.ToolCall)
}

func (tr ToolResult) MarshalZerologObject(ev *zerolog.Event) {
	ev.Str("id", tr.ID).Str("result", tr.Result)
}

func (e EventToolResult) MarshalZerologObject(ev *zerolog.Event) {
	e.EventImpl.MarshalZerologObject(ev)
	ev.Object("tool_result", e.ToolResult)
}

func (e EventPartialCompletion) MarshalZerologObject(ev *zerolog.Event) {
	e.EventImpl.MarshalZerologObject(ev)
	ev.Str("delta", e.Delta).Str("completion", e.Completion)
}

// EventReasoningSummary carries the final reasoning summary from OpenAI.
type EventReasoningSummary struct {
	EventImpl
	Summary string `json:"summary"`
}

// NewReasoningSummaryEvent creates a new EventReasoningSummary.
func NewReasoningSummaryEvent(md EventMetadata, step *steps.StepMetadata, s string) *EventReasoningSummary {
	return &EventReasoningSummary{
		EventImpl: EventImpl{
			Type_:     EventTypeReasoningSummary,
			Metadata_: md,
			Step_:     step,
			payload:   nil,
		},
		Summary: s,
	}
}

var _ Event = &EventReasoningSummary{}

// EventThinkingDelta carries a chunk of streamed thought process (e.g., from Claude or OpenAI Reasoning).
// Mimics EventPartialCompletion structure for UI consistency.
type EventThinkingDelta struct {
	EventImpl
	Delta string `json:"delta"`       // The new chunk of text
	Full  string `json:"full_so_far"` // Accumulated thought text so far
}

// NewThinkingDeltaEvent creates a new EventThinkingDelta.
func NewThinkingDeltaEvent(md EventMetadata, step *steps.StepMetadata, delta string, full string) *EventThinkingDelta {
	return &EventThinkingDelta{
		EventImpl: EventImpl{
			Type_:     EventTypeThinkingDelta,
			Metadata_: md,
			Step_:     step,
			payload:   nil,
		},
		Delta: delta,
		Full:  full,
	}
}

var _ Event = &EventThinkingDelta{}

// EventSignatureDelta carries the optional signature from Claude.
type EventSignatureDelta struct {
	EventImpl
	// Add signature fields if needed, e.g., Signature string `json:"signature,omitempty"`
}

// NewSignatureDeltaEvent creates a new EventSignatureDelta.
func NewSignatureDeltaEvent(md EventMetadata, step *steps.StepMetadata /*, signature string */) *EventSignatureDelta {
	return &EventSignatureDelta{
		EventImpl: EventImpl{
			Type_:     EventTypeSignatureDelta,
			Metadata_: md,
			Step_:     step,
			payload:   nil,
		},
		// Signature: signature,
	}
}

var _ Event = &EventSignatureDelta{}
