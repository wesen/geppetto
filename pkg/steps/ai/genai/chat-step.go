package genai

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/go-go-golems/geppetto/pkg/conversation"
	"github.com/go-go-golems/geppetto/pkg/events"
	geppetto_helpers "github.com/go-go-golems/geppetto/pkg/helpers"
	"github.com/go-go-golems/geppetto/pkg/steps"
	"github.com/go-go-golems/geppetto/pkg/steps/ai/chat"
	"github.com/go-go-golems/geppetto/pkg/steps/ai/settings"
	"github.com/go-go-golems/glazed/pkg/helpers/maps"
	"github.com/google/generative-ai-go/genai"
	"github.com/pkg/errors"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
)

const GeppettoGenAIAPIKey = "genai-api-key"
const GeppettoGenAIAPIType = "genai"

type ChatStep struct {
	Settings         *settings.StepSettings
	publisherManager *events.PublisherManager
	parentID         conversation.NodeID
	messageID        conversation.NodeID
	Tools            []*genai.Tool
}

// Ensure interface compliance
var _ chat.Step = (*ChatStep)(nil)
var _ chat.Step = &ChatStep{}

func NewChatStep(settings *settings.StepSettings, tools []*genai.Tool, options ...chat.StepOption) (*ChatStep, error) {
	if settings.Chat == nil {
		settings.Chat = defaults.NewChatSettings()
	}
	if settings.API == nil {
		settings.API = defaults.NewAPISettings()
	}
	if settings.Tool == nil {
		settings.Tool = defaults.NewToolSettings()
	}

	step := &ChatStep{
		Settings:         settings,
		publisherManager: events.NewPublisherManager(),
		messageID:        conversation.NewNodeID(), // Default message ID
		Tools:            tools,                    // TODO(manuel, 2024-07-26) Handle tools from settings as well
	}

	for _, option := range options {
		err := option(step)
		if err != nil {
			return nil, errors.Wrap(err, "failed to apply option")
		}
	}

	if step.publisherManager == nil {
		step.publisherManager = events.NewPublisherManager()
	}

	return step, nil
}

// StepOption implementations
func WithParentID(parentID conversation.NodeID) chat.StepOption {
	return func(step chat.Step) error {
		cs, ok := step.(*ChatStep)
		if !ok {
			return errors.New("invalid step type for WithParentID option")
		}
		cs.parentID = parentID
		return nil
	}
}

func WithMessageID(messageID conversation.NodeID) chat.StepOption {
	return func(step chat.Step) error {
		cs, ok := step.(*ChatStep)
		if !ok {
			return errors.New("invalid step type for WithMessageID option")
		}
		cs.messageID = messageID
		return nil
	}
}

func WithSubscriptionManager(pm *events.PublisherManager) chat.StepOption {
	return func(step chat.Step) error {
		cs, ok := step.(*ChatStep)
		if !ok {
			return errors.New("invalid step type for WithSubscriptionManager option")
		}
		cs.publisherManager = pm
		return nil
	}
}

func (cs *ChatStep) GetMetadata() map[string]interface{} {
	return map[string]interface{}{
		"step":     "genai-chat",
		"settings": cs.Settings.GetMetadata(),
		"tools":    len(cs.Tools) > 0, // Indicate if tools are present
	}
}

func (cs *ChatStep) AddPublishedTopic(publisher message.Publisher, topic string) error {
	cs.publisherManager.RegisterPublisher(topic, publisher)
	return nil
}

func (cs *ChatStep) SetStreaming(streaming bool) {
	cs.Settings.Chat.Stream = streaming
}

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
		return steps.Reject[*conversation.Message](errors.New("missing engine setting")), nil
	}
	engine := *cs.Settings.Chat.Engine
	apiKey := cs.Settings.API.APIKeys[GeppettoGenAIAPIKey]
	if apiKey == "" {
		return steps.Reject[*conversation.Message](errors.Errorf("missing API key for %s", GeppettoGenAIAPIKey)), nil
	}

	// 3. Initialize GenAI Client
	// TODO(manuel, 2024-07-26) Consider API Base URL if needed/supported by SDK
	client, err := genai.NewClient(cancellableCtx, option.WithAPIKey(apiKey))
	if err != nil {
		return steps.Reject[*conversation.Message](errors.Wrap(err, "failed to create genai client")), nil
	}

	// 4. Prepare Model and Chat Session
	model := client.GenerativeModel(engine)
	cs.configureModel(model) // Apply settings like Temperature, TopP, MaxTokens, etc.
	if len(cs.Tools) > 0 {
		model.Tools = cs.Tools
	}

	chatSession := model.StartChat()
	genaiHistory, err := ConvertConversationToGenaiHistory(messages)
	if err != nil {
		// Close client on error
		_ = client.Close()
		return steps.Reject[*conversation.Message](errors.Wrap(err, "failed to convert conversation history")), nil
	}
	chatSession.History = genaiHistory

	// 5. Extract last user message parts for the current turn
	lastUserMessageParts, err := ExtractLastUserMessageParts(messages)
	if err != nil {
		_ = client.Close()
		return steps.Reject[*conversation.Message](errors.Wrap(err, "failed to extract last user message parts")), nil
	}

	// 6. Prepare Metadata for Events
	stepMetadata := cs.createStepMetadata()
	eventMetadata := cs.createEventMetadata(messages, model)

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
			defer func() {
				err := client.Close()
				if err != nil {
					// Log or handle client close error if necessary
					fmt.Printf("Error closing genai client: %v", err)
				}
			}()

			var fullResponseText strings.Builder
			var finalResponse *genai.GenerateContentResponse
			var finalMessage *conversation.Message

			for {
				select {
				case <-cancellableCtx.Done():
					cs.publisherManager.PublishBlind(
						events.NewInterruptEvent(eventMetadata, stepMetadata, fullResponseText.String()))
					c <- geppetto_helpers.NewErrorResult[*conversation.Message](cancellableCtx.Err())
					return
				default:
					resp, err := streamIterator.Next()
					if err == iterator.Done {
						// Stream finished successfully
						finalResponse = streamIterator.MergedResponse()

						// Update metadata with usage info from finalResponse.UsageMetadata if available
						if finalResponse != nil && finalResponse.UsageMetadata != nil {
							eventMetadata.Usage = &conversation.Usage{
								InputTokens:  int(finalResponse.UsageMetadata.PromptTokenCount),
								OutputTokens: int(finalResponse.UsageMetadata.CandidatesTokenCount),
							}
							stepMetadata.Metadata["usage"] = finalResponse.UsageMetadata // Add to step metadata too
						}
						// Extract FinishReason if available
						finishReasonStr := "unknown"
						if finalResponse != nil && len(finalResponse.Candidates) > 0 {
							finishReasonStr = finalResponse.Candidates[0].FinishReason.String()
							if finalResponse.Candidates[0].FinishReason == genai.FinishReasonToolExecution {
							}
						}
						eventMetadata.StopReason = &finishReasonStr
						stepMetadata.Metadata["finish_reason"] = finishReasonStr

						// Create final message content (either text or tool call)
						var msgContent conversation.MessageContent
						msgContent = conversation.NewChatMessageContent(conversation.RoleAssistant, fullResponseText.String(), nil)

						finalMessage = conversation.NewMessage(
							msgContent,
							conversation.WithID(cs.messageID),
							conversation.WithParentID(cs.parentID),
							conversation.WithLLMMessageMetadata(&eventMetadata.LLMMessageMetadata),
						)

						cs.publisherManager.PublishBlind(
							events.NewFinalEvent(eventMetadata, stepMetadata, finalMessage)) // Send the full final message

						c <- geppetto_helpers.NewValueResult[*conversation.Message](finalMessage)
						return
					}
					if err != nil {
						cs.publisherManager.PublishBlind(
							events.NewErrorEvent(eventMetadata, stepMetadata, err))
						c <- geppetto_helpers.NewErrorResult[*conversation.Message](err)
						return
					}

					// Process partial response
					partialText, partialToolCalls := ExtractTextAndToolsFromResponse(resp)
					if partialText != "" {
						fullResponseText.WriteString(partialText)
						// Publish Partial text event
						cs.publisherManager.PublishBlind(
							events.NewPartialCompletionEvent(eventMetadata, stepMetadata, partialText))
					}
					if len(partialToolCalls) > 0 {
						toolCalls = append(toolCalls, partialToolCalls...)
						// Publish Partial tool call event? Or wait for final?
						// For now, collect them and send in FinalEvent.
						// We could potentially stream tool calls too if needed.
					}
				}
			}
		}()

		return ret, nil

	} else {
		// === Non-Streaming Logic ===
		resp, err := chatSession.SendMessage(cancellableCtx, lastUserMessageParts...)
		// Close client immediately after non-streaming call
		closeErr := client.Close()
		if closeErr != nil {
			fmt.Printf("Error closing genai client after non-streaming call: %v", closeErr)
		}

		if err != nil {
			cs.publisherManager.PublishBlind(events.NewErrorEvent(eventMetadata, stepMetadata, err))
			return steps.Reject[*conversation.Message](errors.Wrap(err, "failed to send message")), nil
		}

		// Update metadata with usage info from resp.UsageMetadata if available
		if resp != nil && resp.UsageMetadata != nil {
			eventMetadata.Usage = &conversation.Usage{
				InputTokens:  int(resp.UsageMetadata.PromptTokenCount),
				OutputTokens: int(resp.UsageMetadata.CandidatesTokenCount),
			}
			stepMetadata.Metadata["usage"] = resp.UsageMetadata
		}
		// Extract FinishReason if available
		finishReasonStr := "unknown"
		if resp != nil && len(resp.Candidates) > 0 {
			finishReasonStr = resp.Candidates[0].FinishReason.String()
		}
		eventMetadata.StopReason = &finishReasonStr
		stepMetadata.Metadata["finish_reason"] = finishReasonStr

		// Extract full text and potential tool calls
		fullText, _ := ExtractTextAndToolsFromResponse(resp)

		// Create final conversation.Message
		var msgContent conversation.MessageContent
		msgContent = conversation.NewChatMessageContent(conversation.RoleAssistant, fullText, nil)

		finalMessage := conversation.NewMessage(
			msgContent,
			conversation.WithID(cs.messageID),
			conversation.WithParentID(cs.parentID),
			conversation.WithLLMMessageMetadata(&eventMetadata.LLMMessageMetadata),
		)

		cs.publisherManager.PublishBlind(events.NewFinalEvent(eventMetadata, stepMetadata, finalMessage))

		return steps.Resolve(finalMessage), nil
	}
}

// Helper function to configure the genai.GenerativeModel based on ChatSettings
func (cs *ChatStep) configureModel(model *genai.GenerativeModel) {
	genConfig := &genai.GenerationConfig{}
	changed := false

	if cs.Settings.Chat.Temperature != nil {
		genConfig.Temperature = float32Ptr(*cs.Settings.Chat.Temperature)
		changed = true
	}
	if cs.Settings.Chat.TopP != nil {
		genConfig.TopP = float32Ptr(*cs.Settings.Chat.TopP)
		changed = true
	}
	if cs.Settings.Chat.TopK != nil {
		genConfig.TopK = int32Ptr(*cs.Settings.Chat.TopK)
		changed = true
	}

	if cs.Settings.Chat.MaxResponseTokens != nil {
		genConfig.MaxOutputTokens = int32Ptr(*cs.Settings.Chat.MaxResponseTokens)
		changed = true
	}
	if len(cs.Settings.Chat.Stop) > 0 {
		genConfig.StopSequences = cs.Settings.Chat.Stop
		changed = true
	}
	// Add other settings like CandidateCount if needed/exposed
	// genConfig.CandidateCount = ...

	if changed {
		model.GenerationConfig = *genConfig
	}

	// Safety Settings - TODO(manuel, 2024-07-26) Expose these via settings
	// model.SafetySettings = []*genai.SafetySetting{...}
}

// Helper to create step metadata
func (cs *ChatStep) createStepMetadata() *steps.StepMetadata {
	settingsMeta := cs.Settings.GetMetadata()
	maps.SetField(settingsMeta, "api_type", GeppettoGenAIAPIType)

	// Generate a unique ID - borrowing from claude step
	id := conversation.NodeID("genai-chat-" + time.Now().Format(time.RFC3339Nano))

	return &steps.StepMetadata{
		StepID:     id, // Generate a unique ID
		Type:       "genai-chat",
		InputType:  "conversation.Conversation",
		OutputType: "conversation.Message",
		Metadata:   settingsMeta, // Include sanitized settings
	}
}

// Helper to create event metadata
func (cs *ChatStep) createEventMetadata(messages conversation.Conversation, model *genai.GenerativeModel) events.EventMetadata {
	parentID := cs.parentID
	if parentID == conversation.NullNode && len(messages) > 0 {
		// Find the last message that is not a system message or a tool result without prior tool call?
		// For simplicity, use the ID of the last message for now.
		parentID = messages[len(messages)-1].ID
	}

	metadata := events.EventMetadata{
		ID:       cs.messageID, // Use the step's message ID
		ParentID: parentID,
		StepID:   cs.createStepMetadata().StepID, // Include step ID
		LLMMessageMetadata: conversation.LLMMessageMetadata{
			Engine: *cs.Settings.Chat.Engine,
			// Usage and StopReason will be filled in later
		},
	}
	if cs.Settings.Chat.Temperature != nil {
		metadata.LLMMessageMetadata.Temperature = cs.Settings.Chat.Temperature
	}
	if cs.Settings.Chat.TopP != nil {
		metadata.LLMMessageMetadata.TopP = cs.Settings.Chat.TopP
	}
	if cs.Settings.Chat.TopK != nil {
		// TODO(manuel, 2024-07-26) Add TopK to LLMMessageMetadata if desired
		// metadata.LLMMessageMetadata.TopK = cs.Settings.Chat.TopK
	}
	if cs.Settings.Chat.MaxResponseTokens != nil {
		metadata.LLMMessageMetadata.MaxTokens = cs.Settings.Chat.MaxResponseTokens
	}
	return metadata
}

// Helper functions for pointers
func float32Ptr(f float64) *float32 {
	v := float32(f)
	return &v
}

func int32Ptr(i int) *int32 {
	v := int32(i)
	return &v
}

func boolPtr(b bool) *bool {
	return &b
}
