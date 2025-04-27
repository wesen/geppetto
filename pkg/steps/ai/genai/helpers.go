package genai

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/go-go-golems/geppetto/pkg/conversation"
	"github.com/google/generative-ai-go/genai"
	"github.com/pkg/errors"
)

// IsGenAiEngine checks if the engine name suggests a Gemini model.
func IsGenAiEngine(engine string) bool {
	return strings.HasPrefix(engine, "gemini-")
}

// ConvertConversationToGenaiHistory converts geppetto conversation history to []*genai.Content.
// It skips the last message as that's typically the current user prompt.
// It also skips system messages for now, as their handling might need specific logic.
func ConvertConversationToGenaiHistory(messages conversation.Conversation) ([]*genai.Content, error) {
	history := []*genai.Content{}

	// Iterate up to the second to last message
	limit := len(messages)
	if limit > 0 {
		limit-- // Don't include the last message in history
	}

	for i := 0; i < limit; i++ {
		msg := messages[i]
		genaiRole := ""
		switch msg.GetRole() {
		case conversation.RoleUser:
			genaiRole = "user"
		case conversation.RoleAssistant:
			genaiRole = "model"
		case conversation.RoleTool:
			genaiRole = "function" // Or "tool"? genai uses "function" for FunctionResponse
		case conversation.RoleSystem:
			// TODO(manuel, 2024-07-26) Handle system messages appropriately. Gemini API might have
			// a dedicated system instruction field or expect it differently than OpenAI/Claude.
			// Skipping for now.
			continue
		default:
			// Skip unknown roles
			continue
		}

		parts, err := ConvertMessageContentToGenaiParts(msg.Content)
		if err != nil {
			// Log or handle error, potentially skip message
			fmt.Printf("Error converting message %s to genai parts: %v\n", msg.ID, err)
			continue
		}

		// Skip empty parts, which can happen with skipped roles or content types
		if len(parts) == 0 {
			continue
		}

		history = append(history, &genai.Content{Role: genaiRole, Parts: parts})
	}
	return history, nil
}

// ConvertMessageContentToGenaiParts converts a single geppetto message content to []genai.Part.
func ConvertMessageContentToGenaiParts(content conversation.MessageContent) ([]genai.Part, error) {
	parts := []genai.Part{}

	switch c := content.(type) {
	case *conversation.ChatMessageContent:
		if c.Text != "" {
			parts = append(parts, genai.Text(c.Text))
		}
		// TODO(manuel, 2024-07-26) Handle images (c.Images) conversion to genai.ImageData

	case *conversation.ToolCallContent:
		for _, toolCall := range c.ToolCalls {
			argsJSON, err := json.Marshal(toolCall.Arguments)
			if err != nil {
				return nil, errors.Wrapf(err, "failed to marshal tool call arguments for %s", toolCall.Name)
			}
			parts = append(parts, genai.FunctionCall{
				Name: toolCall.Name,
				Args: json.RawMessage(argsJSON),
			})
		}
		// Gemini expects FunctionCalls to be the *only* part from the 'model' role.
		// Ensure no text part is accidentally included from the assistant message containing the tool call.

	case *conversation.ToolResultContent:
		for _, toolResult := range c.ToolResults {
			// Assume result is JSON string, attempt to marshal it as raw message
			// TODO(manuel, 2024-07-26) Handle non-JSON results? Gemini expects structured data.
			// We might need to parse the result string if it's not already valid JSON.
			var rawResult json.RawMessage
			if err := json.Unmarshal([]byte(toolResult.Result), &rawResult); err != nil {
				// If unmarshal fails, treat it as a simple string response?
				// Gemini FunctionResponse seems to expect structured data, this might cause issues.
				// Wrapping as a simple JSON string for now.
				stringResultBytes, _ := json.Marshal(toolResult.Result)
				rawResult = json.RawMessage(stringResultBytes)
			}

			parts = append(parts, genai.FunctionResponse{
				Name:     toolResult.Name,
				Response: rawResult,
			})
		}

	default:
		return nil, errors.Errorf("unsupported message content type: %T", content)
	}

	return parts, nil
}

// ExtractLastUserMessageParts gets the genai.Part slice for the last message if it's from the user.
func ExtractLastUserMessageParts(messages conversation.Conversation) ([]genai.Part, error) {
	if len(messages) == 0 {
		return nil, errors.New("cannot send empty conversation")
	}
	lastMsg := messages[len(messages)-1]
	// Allow user or tool messages as the last message, as a tool result might be the trigger.
	if lastMsg.GetRole() != conversation.RoleUser && lastMsg.GetRole() != conversation.RoleTool {
		// This might need refinement depending on how tool interactions are structured.
		// If the flow is always User -> Assistant(ToolCall) -> ToolResult -> Assistant(Response),
		// then the last message sent *to* the model would be the ToolResult.
		// For now, we assume the triggering message (last in the input list)
		// contains the parts to send.
		return nil, errors.Errorf("last message role must be user or tool, got: %s", lastMsg.GetRole())
	}
	return ConvertMessageContentToGenaiParts(lastMsg.Content)
}

// ExtractTextAndToolsFromResponse extracts text content and tool calls from a genai response.
func ExtractTextAndToolsFromResponse(resp *genai.GenerateContentResponse) (string, []*conversation.ToolCall) {
	var text strings.Builder
	var toolCalls []*conversation.ToolCall

	if resp == nil {
		return "", nil
	}

	for _, cand := range resp.Candidates {
		if cand.Content != nil {
			for _, part := range cand.Content.Parts {
				switch p := part.(type) {
				case genai.Text:
					text.WriteString(string(p))
				case genai.FunctionCall:
					// Attempt to unmarshal args back into map[string]interface{}
					var args map[string]interface{}
					if err := json.Unmarshal(p.Args, &args); err != nil {
						// Log error? Could happen if Args aren't valid JSON object
						fmt.Printf("Warning: Failed to unmarshal tool call args for %s: %v\n", p.Name, err)
						// Store raw args as string? For now, pass nil args.
						args = nil
					}
					toolCalls = append(toolCalls, &conversation.ToolCall{
						// TODO(manuel, 2024-07-26) Need a unique ID for each tool call.
						// Using Name for now, but this is not guaranteed unique if called multiple times.
						ID:        p.Name, // Placeholder ID
						Name:      p.Name,
						Arguments: args,
					})
				// Handle other part types if needed (e.g., genai.FileData)
				default:
					fmt.Printf("Warning: Unhandled part type in response: %T\n", part)
				}
			}
		}
	}
	return text.String(), toolCalls
}
