# Tutorial: Adding Google Gemini Support to Geppetto AI Steps

## 1. Purpose and Scope

This document provides a step-by-step guide for integrating Google's Gemini models into the `geppetto` AI step framework. It assumes familiarity with the existing `geppetto` codebase, particularly the AI step architecture used for OpenAI and Claude.

The goal is to add a new AI provider backend using the `github.com/google/generative-ai-go/genai` SDK, allowing users to select Gemini models via configuration and interact with them through the standard `chat.Step` interface.

This tutorial focuses on:

- Understanding the relevant parts of the existing architecture.
- Defining necessary data structures and configurations.
- Outlining the implementation steps for a `GenAIChatStep`.
- Integrating the new step into the factory mechanism.
- Ensuring proper event publishing for streaming UI updates.

**Note:** This guide provides code sketches and architectural explanations, not a complete, copy-paste implementation.

## 2. Context and Existing Architecture

Geppetto uses a factory pattern to create AI chat steps based on configuration. Key components involved are:

- **`chat.Step` Interface (`geppetto/pkg/steps/ai/chat/interface.go`):** The core interface that all chat steps must implement. Its primary method is `Start`, which takes a `context.Context` and `conversation.Conversation` and returns a `steps.StepResult[*conversation.Message]`.
- **`ApiType` (`geppetto/pkg/steps/ai/types/types.go`):** An enum (`string` type) used to distinguish between different AI providers (e.g., `ApiTypeOpenAI`, `ApiTypeClaude`). We will add a new type here for Gemini.
- **`StepSettings` (`geppetto/pkg/steps/ai/settings/settings-step.go`):** A central struct holding configuration for all AI steps. It aggregates common settings (`ChatSettings`, `APISettings`, `ClientSettings`) and provider-specific settings (`OpenAI`, `Claude`, `Ollama`). We will add a field for Gemini-specific settings if needed, although the `genai` SDK seems primarily configured via the client, so we might initially rely only on `ChatSettings` and `APISettings`.
- **`ChatSettings` (`geppetto/pkg/steps/ai/settings/settings-chat.go`):** Contains common chat parameters like `Engine`, `MaxResponseTokens`, `Temperature`, `Stream`, `ApiType`, and caching settings. These are configurable via flags defined in `geppetto/pkg/steps/ai/settings/flags/chat.yaml`.
- **`APISettings` (`geppetto/pkg/steps/ai/settings/settings-step.go`):** Stores API keys and base URLs, keyed by the provider type (e.g., `genai-api-key`).
- **`StandardStepFactory` (`geppetto/pkg/steps/ai/factory.go`):** Responsible for creating the appropriate `chat.Step` implementation based on the `ApiType` specified in `ChatSettings`. It uses a `switch` statement on `ApiType` or infers the type based on the engine name prefix (e.g., `gpt-`, `claude-`).
- **Event Publishing (`geppetto/pkg/events`):** Steps use a `PublisherManager` to publish events (`StartEvent`, `PartialEvent`, `FinalEvent`, `ErrorEvent`, `InterruptEvent`) during their execution, especially for streaming responses. These events drive UI updates.
- **Conversation Model (`geppetto/pkg/conversation`):** Defines the `Conversation` (slice of `*Message`) and `Message` types used as input and output for chat steps. Includes different `MessageContentTypes` like `ChatMessageContent`, `ToolCallContent`, `ToolResultContent`.

## 3. Relevant Data Structures, Files, and Interfaces

Before starting, familiarize yourself with:

- **Go SDK:** `github.com/google/generative-ai-go/genai`
  - `genai.NewClient`: Creates the main client (requires `option.WithAPIKey`).
  - `client.GenerativeModel`: Represents a specific model (e.g., "gemini-1.5-flash").
  - `model.StartChat`: Initiates a chat session.
  - `chatSession.SendMessage`: Sends a message (non-streaming).
  - `chatSession.SendMessageStream`: Sends a message (streaming), returns `GenerateContentResponseIterator`.
  - `genai.Content`: Represents a message turn (contains `Role` and `Parts`).
  - `genai.Part`: Interface for message content (e.g., `genai.Text`, `genai.ImageData`, `genai.FunctionCall`, `genai.FunctionResponse`).
  - `genai.GenerateContentResponse`: The response object (contains `Candidates`, `PromptFeedback`, `UsageMetadata`).
- **Geppetto:**
  - `geppetto/pkg/steps/ai/chat/interface.go` (`chat.Step`)
  - `geppetto/pkg/steps/ai/types/types.go` (`ApiType`)
  - `geppetto/pkg/steps/ai/settings/settings-step.go` (`StepSettings`, `APISettings`)
  - `geppetto/pkg/steps/ai/settings/settings-chat.go` (`ChatSettings`)
  - `geppetto/pkg/steps/ai/settings/flags/chat.yaml`
  - `geppetto/pkg/steps/ai/factory.go` (`StandardStepFactory`)
  - `geppetto/pkg/events/events.go` (Event types, `PublisherManager`)
  - `geppetto/pkg/conversation/conversation.go` (`Conversation`, `Message`, `ChatMessageContent`, etc.)
  - `geppetto/pkg/steps/steps.go` (`StepResult`, `NewStepResult`, `Resolve`, `Reject`)
  - `geppetto/pkg/helpers/result.go` (`Result`, `NewValueResult`, `NewErrorResult`)
  - Reference Implementations: `geppetto/pkg/steps/ai/openai/chat-step.go`, `geppetto/pkg/steps/ai/claude/chat-step.go`

## 4. Step-by-Step Implementation Guide

### Step 4.1: Define New `ApiType`

1.  **Choose a Name:** Let's use `"genai"` for consistency with the SDK name.
2.  **Edit `geppetto/pkg/steps/ai/types/types.go`:** Add the new constant.

    ```go
    // geppetto/pkg/steps/ai/types/types.go
    package types

    type ApiType string

    const (
            ApiTypeOpenAI    ApiType = "openai"
            ApiTypeAnyScale  ApiType = "anyscale"
            ApiTypeFireworks ApiType = "fireworks"
            ApiTypeClaude    ApiType = "claude"
            ApiTypeGenai     ApiType = "genai" // <-- ADD THIS LINE
            // not implemented from here on down
            ApiTypeOllama     ApiType = "ollama"
            ApiTypeMistral    ApiType = "mistral"
            ApiTypePerplexity ApiType = "perplexity"
            // Cohere has connectors
            ApiTypeCohere ApiType = "cohere"
    )
    ```

### Step 4.2: Update Chat Flags Configuration

1.  **Edit `geppetto/pkg/steps/ai/settings/flags/chat.yaml`:** Add `"genai"` to the choices for the `ai-api-type` flag. Decide on a sensible default engine if `ai-api-type` is `genai` (or handle this in the factory/step logic). For now, we'll just add the choice.

    ```yaml
    # geppetto/pkg/steps/ai/settings/flags/chat.yaml
    # ...
    flags:
      # ...
      - name: ai-api-type
        type: choice
        choices:
          - "openai"
          - "anyscale"
          - "fireworks"
          - "claude"
          - "genai" # <-- ADD THIS LINE
          - "ollama"
          - "mistral"
          - "perplexity"
          - "cohere"
        help: The provider type to use for chat
        default: openai # Keep default or change if desired
      # ...
      - name: ai-engine
        type: string
        help: The model to use for chat
        default: "gpt-4" # Consider how to handle default based on api-type
      # ...
    ```

### Step 4.3: Create New Package and `ChatStep` Struct

1.  **Create Directory:** `geppetto/pkg/steps/ai/genai/`
2.  **Create File:** `geppetto/pkg/steps/ai/genai/chat-step.go`
3.  **Define Struct:**

    ```go
    // geppetto/pkg/steps/ai/genai/chat-step.go
    package genai

    import (
            "context"
            // ... other necessary geppetto imports (conversation, steps, events, settings, helpers)
            "github.com/ThreeDotsLabs/watermill/message"
            "github.com/go-go-golems/geppetto/pkg/conversation"
            "github.com/go-go-golems/geppetto/pkg/events"
            geppetto_helpers "github.com/go-go-golems/geppetto/pkg/helpers"
            "github.com/go-go-golems/geppetto/pkg/steps"
            "github.com/go-go-golems/geppetto/pkg/steps/ai/chat"
            "github.com/go-go-golems/geppetto/pkg/steps/ai/settings"
            // ... google genai sdk imports
            "github.com/google/generative-ai-go/genai"
            "google.golang.org/api/iterator"
            "google.golang.org/api/option"
            // ... standard library imports (fmt, log, strings, etc.)
    )

    type ChatStep struct {
            Settings         *settings.StepSettings
            publisherManager *events.PublisherManager
            // Optional: Add parentID, messageID if needed for event correlation, similar to Claude step
            parentID  conversation.NodeID
            messageID conversation.NodeID
    }

    // Ensure interface compliance
    var _ chat.Step = (*ChatStep)(nil)

    // Constructor (similar to openai/claude steps)
    func NewChatStep(settings *settings.StepSettings, options ...chat.StepOption) (*ChatStep, error) {
            // Initialize default publisherManager
            // Apply options (like WithSubscriptionManager, potentially WithParentID, WithMessageID)
            // Set default messageID if not provided
            // ...
            return &ChatStep{
                    Settings:         settings,
                    publisherManager: events.NewPublisherManager(), // Placeholder
                    messageID:        conversation.NewNodeID(),   // Placeholder
            }, nil
    }

    func (cs *ChatStep) AddPublishedTopic(publisher message.Publisher, topic string) error {
            cs.publisherManager.RegisterPublisher(topic, publisher)
            return nil
    }

    // StepOption definition (if needed for custom options)
    // type StepOption func(*ChatStep) error
    // func WithSubscriptionManager(...) StepOption { ... }
    ```

### Step 4.4: Implement `chat.Step` Interface (`Start` Method)

This is the core logic. We'll outline the structure, focusing on streaming.

```go
// geppetto/pkg/steps/ai/genai/chat-step.go (continued)

func (cs *ChatStep) Start(
        ctx context.Context,
        messages conversation.Conversation,
) (steps.StepResult[*conversation.Message], error) {

        // 1. Setup Context and Cancellation
        var cancel context.CancelFunc
        cancellableCtx, cancel := context.WithCancel(ctx)
        go func() {
                <-ctx.Done()
                cancel()
        }()

        // 2. Validate Settings
        if cs.Settings.Chat == nil || cs.Settings.Chat.Engine == nil {
                // Handle missing engine error
                return steps.Reject[*conversation.Message](/* ... */), nil
        }
        engine := *cs.Settings.Chat.Engine
        apiKey := cs.Settings.API.APIKeys["genai-api-key"] // Adjust key name if needed
        if apiKey == "" {
                // Handle missing API key error
                return steps.Reject[*conversation.Message](/* ... */), nil
        }

        // 3. Initialize GenAI Client
        client, err := genai.NewClient(cancellableCtx, option.WithAPIKey(apiKey))
        if err != nil {
                // Handle client creation error
                return steps.Reject[*conversation.Message](err), nil
        }
        // Note: client.Close() should be called eventually, but since the step
        // might be long-running (streaming), managing its lifecycle needs care.
        // The context cancellation should handle interrupting streams.

        // 4. Prepare Model and Chat Session
        model := client.GenerativeModel(engine)
        cs.configureModel(model) // Apply settings like Temperature, TopP, MaxTokens, etc.
        chatSession := model.StartChat()
        chatSession.History = convertConversationToGenaiHistory(messages) // Convert geppetto messages to genai.Content history

        // 5. Extract last user message parts for the current turn
        // The genai SDK's StartChat().SendMessage expects the *current* turn's parts.
        // The history is managed separately in chatSession.History.
        lastUserMessageParts, err := extractLastUserMessageParts(messages)
        if err != nil {
             return steps.Reject[*conversation.Message](err), nil
        }


        // 6. Prepare Metadata for Events
        stepMetadata := cs.createStepMetadata()
        eventMetadata := cs.createEventMetadata(messages, model) // Include engine, temp, topP etc.

        // 7. Handle Streaming vs. Non-Streaming
        stream := cs.Settings.Chat.Stream
        cs.publisherManager.PublishBlind(events.NewStartEvent(eventMetadata, stepMetadata))

        if stream {
                // === Streaming Logic ===
                streamIterator := chatSession.SendMessageStream(cancellableCtx, lastUserMessageParts...)

                c := make(chan geppetto_helpers.Result[*conversation.Message])
                ret := steps.NewStepResult[*conversation.Message](
                        c,
                        steps.WithCancel[*conversation.Message](cancel),
                        steps.WithMetadata[*conversation.Message](stepMetadata),
                )

                go func() {
                        defer close(c)
                        defer client.Close() // Close client when goroutine finishes

                        var fullResponse strings.Builder
                        var finalResponse *genai.GenerateContentResponse

                        for {
                                select {
                                case <-cancellableCtx.Done():
                                        // Publish Interrupt event
                                        cs.publisherManager.PublishBlind(
                                            events.NewInterruptEvent(eventMetadata, stepMetadata, fullResponse.String()))
                                        c <- geppetto_helpers.NewErrorResult[*conversation.Message](cancellableCtx.Err())
                                        return
                                default:
                                        resp, err := streamIterator.Next()
                                        if err == iterator.Done {
                                                // Stream finished successfully
                                                finalResponse = streamIterator.MergedResponse() // Get combined response for metadata

                                                // Update metadata with usage info from finalResponse.UsageMetadata if available
                                                if finalResponse != nil && finalResponse.UsageMetadata != nil {
                                                   // Extract PromptTokens, CandidateTokens, TotalTokens
                                                   eventMetadata.Usage = &conversation.Usage{
                                                         // ... fill from finalResponse.UsageMetadata
                                                   }
                                                   stepMetadata.Metadata["usage"] = finalResponse.UsageMetadata // Add to step metadata too
                                                }
                                                // Extract FinishReason if available
                                                if finalResponse != nil && len(finalResponse.Candidates) > 0 {
                                                    finishReason := finalResponse.Candidates[0].FinishReason.String()
                                                    eventMetadata.StopReason = &finishReason
                                                    stepMetadata.Metadata["finish_reason"] = finishReason
                                                }


                                                // Publish Final event
                                                cs.publisherManager.PublishBlind(
                                                    events.NewFinalEvent(eventMetadata, stepMetadata, fullResponse.String()))

                                                // Create final conversation.Message
                                                msgContent := conversation.NewChatMessageContent(conversation.RoleAssistant, fullResponse.String(), nil)
                                                finalMessage := conversation.NewMessage(
                                                        msgContent,
                                                        conversation.WithID(cs.messageID),
                                                        conversation.WithParentID(cs.parentID),
                                                        conversation.WithLLMMessageMetadata(&eventMetadata.LLMMessageMetadata),
                                                )
                                                c <- geppetto_helpers.NewValueResult[*conversation.Message](finalMessage)
                                                return
                                        }
                                        if err != nil {
                                                // Handle stream error
                                                // Publish Error event
                                                cs.publisherManager.PublishBlind(
                                                    events.NewErrorEvent(eventMetadata, stepMetadata, err))
                                                c <- geppetto_helpers.NewErrorResult[*conversation.Message](err)
                                                return
                                        }

                                        // Process partial response
                                        partialText := extractTextFromResponse(resp) // Helper to get text
                                        fullResponse.WriteString(partialText)

                                        // Publish Partial event
                                        cs.publisherManager.PublishBlind(
                                            events.NewPartialEvent(eventMetadata, stepMetadata, partialText))

                                        // NOTE: The channel `c` in geppetto currently expects only the *final*
                                        // result or an error. Partial results are sent via the publisherManager.
                                }
                        }
                }()

                return ret, nil

        } else {
                // === Non-Streaming Logic ===
                resp, err := chatSession.SendMessage(cancellableCtx, lastUserMessageParts...)
                 defer client.Close() // Close client after non-streaming call

                if err != nil {
                        // Handle error, publish ErrorEvent
                         cs.publisherManager.PublishBlind(events.NewErrorEvent(eventMetadata, stepMetadata, err))
                        return steps.Reject[*conversation.Message](err), nil
                }

                // Update metadata with usage info from resp.UsageMetadata if available
                if resp != nil && resp.UsageMetadata != nil {
                   // ... extract usage ...
                   eventMetadata.Usage = &conversation.Usage{ /* ... */ }
                   stepMetadata.Metadata["usage"] = resp.UsageMetadata
                }
                // Extract FinishReason if available
                 if resp != nil && len(resp.Candidates) > 0 {
                    finishReason := resp.Candidates[0].FinishReason.String()
                    eventMetadata.StopReason = &finishReason
                    stepMetadata.Metadata["finish_reason"] = finishReason
                }

                fullText := extractTextFromResponse(resp) // Helper to get full text

                // Publish Final event
                 cs.publisherManager.PublishBlind(events.NewFinalEvent(eventMetadata, stepMetadata, fullText))

                // Create final conversation.Message
                msgContent := conversation.NewChatMessageContent(conversation.RoleAssistant, fullText, nil)
                finalMessage := conversation.NewMessage(
                        msgContent,
                        conversation.WithID(cs.messageID),
                        conversation.WithParentID(cs.parentID),
                        conversation.WithLLMMessageMetadata(&eventMetadata.LLMMessageMetadata),
                )

                return steps.Resolve(finalMessage), nil
        }
}

// Helper function to configure the genai.GenerativeModel based on ChatSettings
func (cs *ChatStep) configureModel(model *genai.GenerativeModel) {
    if cs.Settings.Chat.Temperature != nil {
        model.SetTemperature(float32(*cs.Settings.Chat.Temperature))
    }
    if cs.Settings.Chat.TopP != nil {
        model.SetTopP(float32(*cs.Settings.Chat.TopP))
    }
     // NOTE: genai SDK uses TopK, not TopP directly in GenerationConfig struct,
     // but TopP is available. Check SDK for exact method.
     // if cs.Settings.Chat.TopP != nil { model.SetTopP(...) }

     // TopK - Need to decide if we map geppetto's TopP to TopK or add TopK setting
     // model.SetTopK(...)

    if cs.Settings.Chat.MaxResponseTokens != nil {
        model.SetMaxOutputTokens(int32(*cs.Settings.Chat.MaxResponseTokens))
    }
     if len(cs.Settings.Chat.Stop) > 0 {
        model.StopSequences = cs.Settings.Chat.Stop
    }
    // Add other settings like CandidateCount if needed/exposed
}

// Helper function to convert geppetto conversation history to genai.Content slice
func convertConversationToGenaiHistory(messages conversation.Conversation) []*genai.Content {
    history := []*genai.Content{}
    // Iterate through messages (excluding the very last one, which is the current prompt)
    // Convert each message's Role and Parts (Text, Images) to genai.Content format.
    // Handle role mapping ("assistant" -> "model", "user" -> "user").
    // Handle multi-part messages if necessary (e.g., text + image).
    // Images need to be converted to genai.Blob or genai.FileData parts.
    // Tool calls/results need conversion to genai.FunctionCall / genai.FunctionResponse parts.
    // This requires careful mapping based on conversation.MessageContentType.
    // Sketch:
    for _, msg := range messages[:len(messages)-1] { // Exclude last message
        genaiRole := ""
        switch msg.GetRole() {
            case conversation.RoleUser: genaiRole = "user"
            case conversation.RoleAssistant: genaiRole = "model"
            // Handle system, tool roles if necessary
            default: continue // Skip unsupported roles for history
        }

        parts, err := convertMessageContentToGenaiParts(msg.Content)
        if err != nil {
            // log error and skip message?
            continue
        }

        history = append(history, &genai.Content{Role: genaiRole, Parts: parts})
    }
    return history
}

// Helper to convert a single geppetto message content to genai Parts
func convertMessageContentToGenaiParts(content conversation.MessageContent) ([]genai.Part, error) {
    // Handle different content types: ChatMessageContent, ToolCallContent, ToolResultContent
    // For ChatMessageContent: Create genai.Text part. If images exist, create genai.ImageData parts.
    // For ToolCallContent: Convert to genai.FunctionCall part(s).
    // For ToolResultContent: Convert to genai.FunctionResponse part(s).
    // Return []genai.Part, error
    return []genai.Part{ /* ... placeholder ... */ }, nil
}

// Helper to extract the parts of the last user message for the current turn
func extractLastUserMessageParts(messages conversation.Conversation) ([]genai.Part, error) {
     if len(messages) == 0 {
         return nil, fmt.Errorf("cannot send empty conversation")
     }
     lastMsg := messages[len(messages)-1]
     if lastMsg.GetRole() != conversation.RoleUser {
        // Or handle cases where last message isn't from user, depending on desired behavior
        return nil, fmt.Errorf("last message must be from user")
     }
     return convertMessageContentToGenaiParts(lastMsg.Content)
}


// Helper function to extract text from a genai.GenerateContentResponse
func extractTextFromResponse(resp *genai.GenerateContentResponse) string {
    var text strings.Builder
    if resp != nil {
        for _, cand := range resp.Candidates {
            if cand.Content != nil {
                for _, part := range cand.Content.Parts {
                    if txt, ok := part.(genai.Text); ok {
                        text.WriteString(string(txt))
                    }
                    // Potentially handle other part types if needed
                }
            }
        }
    }
    return text.String()
}


// Helper to create step metadata
func (cs *ChatStep) createStepMetadata() *steps.StepMetadata {
     return &steps.StepMetadata{
                StepID:     /* uuid.New() */, // Generate a unique ID
                Type:       "genai-chat",
                InputType:  "conversation.Conversation",
                OutputType: "conversation.Message",
                Metadata: map[string]interface{}{
                        steps.MetadataSettingsSlug: cs.Settings.GetMetadata(), // Include sanitized settings
                        // Add any other relevant step-specific info
                },
        }
}

// Helper to create event metadata
func (cs *ChatStep) createEventMetadata(messages conversation.Conversation, model *genai.GenerativeModel) events.EventMetadata {
    parentID := cs.parentID
    if parentID == conversation.NullNode && len(messages) > 0 {
        parentID = messages[len(messages)-1].ID
    }

    metadata := events.EventMetadata{
            ID:       cs.messageID, // Use the step's message ID
            ParentID: parentID,
            LLMMessageMetadata: conversation.LLMMessageMetadata{
                    Engine:      *cs.Settings.Chat.Engine,
                    // Usage will be filled in later
                    // StopReason will be filled in later
            },
            // Add Temperature, TopP, MaxTokens from settings
    }
    if cs.Settings.Chat.Temperature != nil {
         metadata.LLMMessageMetadata.Temperature = cs.Settings.Chat.Temperature
    }
     if cs.Settings.Chat.TopP != nil {
         metadata.LLMMessageMetadata.TopP = cs.Settings.Chat.TopP
    }
     if cs.Settings.Chat.MaxResponseTokens != nil {
         metadata.LLMMessageMetadata.MaxTokens = cs.Settings.Chat.MaxResponseTokens
    }
    return metadata
}
```

### Step 4.5: Register in Factory

1.  **Edit `geppetto/pkg/steps/ai/factory.go`:**

    - Import the new `genai` package.
    - Add a case for `ai_types.ApiTypeGenai` in the `switch *settings_.Chat.ApiType` block.
    - Instantiate the `genai.NewChatStep`.
    - Optionally, add logic to infer `ApiTypeGenai` based on engine prefixes (e.g., `gemini-`) in the `else` block, similar to how OpenAI and Claude are handled.

    ```go
    // geppetto/pkg/steps/ai/factory.go
    package ai

    import (
            // ... other imports
            "github.com/go-go-golems/geppetto/pkg/steps/ai/claude"
            "github.com/go-go-golems/geppetto/pkg/steps/ai/genai" // <-- ADD IMPORT
            "github.com/go-go-golems/geppetto/pkg/steps/ai/openai"
            // ...
    )

    // ... StandardStepFactory struct ...

    func (s *StandardStepFactory) NewStep(
            options ...chat.StepOption,
    ) (chat.Step, error) {
            settings_ := s.Settings.Clone()

            if settings_.Chat == nil || settings_.Chat.Engine == nil {
                    return nil, errors.New("no chat engine specified")
            }

            var ret chat.Step
            var err error
            if settings_.Chat.ApiType != nil {
                    switch *settings_.Chat.ApiType {
                    case ai_types.ApiTypeOpenAI, ai_types.ApiTypeAnyScale, ai_types.ApiTypeFireworks:
                            ret, err = openai.NewStep(settings_, options...) // Pass options
                            if err != nil { return nil, err }

                    case ai_types.ApiTypeClaude:
                            // Assuming NewChatStep exists and takes settings/tools/options
                            // tools := []claude_api.Tool{} // Placeholder for Claude tools
                            ret, err = claude.NewChatStep(settings_, []claude_api.Tool{}, options...) // Pass options
                            if err != nil { return nil, err }

                    case ai_types.ApiTypeGenai: // <-- ADD CASE
                            ret, err = genai.NewChatStep(settings_, options...) // Pass options
                            if err != nil { return nil, err }

                    // ... other cases (ollama, mistral, etc.) ...
                    default:
                         return nil, errors.Errorf("unsupported api type: %s", *settings_.Chat.ApiType)

                    }

            } else {
                    // Infer API type from engine name
                    switch {
                    case openai.IsOpenAiEngine(*settings_.Chat.Engine):
                            apiType := ai_types.ApiTypeOpenAI
                            settings_.Chat.ApiType = &apiType
                            ret, err = openai.NewStep(settings_, options...) // Pass options
                            if err != nil { return nil, err }

                    case claude.IsClaudeEngine(*settings_.Chat.Engine):
                            apiType := ai_types.ApiTypeClaude
                            settings_.Chat.ApiType = &apiType
                            // Assuming NewStep exists and takes settings/options
                            ret, err = claude.NewStep(settings_, options...) // Pass options
                            if err != nil { return nil, err }

                    case IsGenAiEngine(*settings_.Chat.Engine): // <-- ADD HELPER CHECK
                             apiType := ai_types.ApiTypeGenai
                             settings_.Chat.ApiType = &apiType
                             ret, err = genai.NewChatStep(settings_, options...) // Pass options
                             if err != nil { return nil, err }

                    default:
                         // Maybe default to OpenAI or return error?
                         return nil, errors.Errorf("could not infer api type for engine: %s", *settings_.Chat.Engine)
                    }
            }

            // Apply step options (this might be redundant if passed to NewStep funcs)
            // for _, option := range options { ... }


            // Wrap with caching if configured
            if ret != nil && settings_.Chat != nil && settings_.Chat.CacheType != "none" {
                    ret, err = settings_.Chat.WrapWithCache(ret, options...) // Pass options to wrapper
                    if err != nil {
                            return nil, errors.Wrap(err, "failed to wrap step with cache")
                    }
            }


            return ret, nil
    }

    // Helper function (can live here or in geppetto/pkg/steps/ai/genai/helpers.go)
    func IsGenAiEngine(engine string) bool {
        // Define logic to identify Gemini models (e.g., prefix "gemini-")
        return strings.HasPrefix(engine, "gemini-")
    }

    // ... other factory code ...
    ```

### Step 4.6: Add Helper Functions

Create `geppetto/pkg/steps/ai/genai/helpers.go` (or similar) for functions like:

- `IsGenAiEngine` (if not placed in `factory.go`).
- `convertConversationToGenaiHistory`.
- `convertMessageContentToGenaiParts`.
- `extractTextFromResponse`.
- Potentially functions to handle `genai.FunctionCall` and `genai.FunctionResponse` conversion to/from `geppetto` types if tool use is implemented.

### Step 4.7: Handle API Keys and Specific Settings

- **API Keys:** Ensure the `genai-api-key` (or chosen name) is correctly retrieved from `APISettings`. Users will need to configure this key.
- **Gemini Settings:** If Gemini introduces specific parameters not covered by `ChatSettings` (e.g., unique safety settings, specific tool configurations), create a `settings/genai/settings.go` file defining a `Settings` struct, add it to the main `StepSettings`, and load/use these settings within the `GenAIChatStep`. Initially, this might not be necessary.

## 5. Design Decisions and Architecture Considerations

- **`ApiType` Name:** Using `"genai"` aligns with the Go SDK name. `"gemini"` is also viable but might be too specific if Google offers other models via this SDK later. `"google"` is broader but less specific to the current models.
- **Package Structure:** Creating a dedicated `geppetto/pkg/steps/ai/genai` package follows the established pattern for provider-specific logic.
- **Settings:** Leveraging the existing `ChatSettings` and `APISettings` minimizes initial complexity. Provider-specific settings (`settings/genai/`) can be added later if required.
- **Message Conversion:** The most complex part is accurately converting between `geppetto`'s `conversation.Message` format (including roles, text, images, tool calls/results) and `genai`'s `Content`/`Part` structure. This requires careful handling of different `MessageContentTypes`.
- **Streaming and Events:** Implementing the streaming loop with correct event publishing (`Start`, `Partial`, `Final`, `Error`, `Interrupt`) is crucial for UI integration. Ensure metadata (usage, finish reason) is captured and included in `FinalEvent`.
- **Error Handling:** Robust error handling is needed for API calls, stream iteration, and configuration issues, publishing `ErrorEvent` when appropriate and returning `steps.Reject`.
- **Caching:** The factory automatically wraps the created step with caching based on `ChatSettings`. Ensure the `GenAIChatStep` itself doesn't interfere with this.
- **Tool Use:** This initial guide focuses on chat. Implementing tool use would require:
  - Converting `geppetto` tool definitions to `genai.Tool` / `genai.FunctionDeclaration`.
  - Passing these tools to the `genai.GenerativeModel`.
  - Handling `genai.FunctionCall` parts in the response, converting them to `conversation.ToolCallContent`, and yielding them.
  - Accepting `conversation.ToolResultContent` in subsequent calls and converting them to `genai.FunctionResponse` parts to send back to the model. This adds significant complexity to the message conversion logic.

## 6. Next Steps

1.  Implement the sketched code, filling in the details for helper functions, error handling, and specific SDK calls.
2.  Thoroughly test both streaming and non-streaming modes.
3.  Test with different Gemini models (Flash, Pro).
4.  Verify event publishing with a connected UI or event listener.
5.  Consider adding support for multi-modal inputs (images) within the message conversion logic.
6.  (Optional) Implement tool use support.
7.  Add unit and integration tests.
