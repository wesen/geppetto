package context

import (
	"encoding/json"
	"fmt"
	"github.com/pkg/errors"
	"github.com/sashabaranov/go-openai"
	"gopkg.in/yaml.v3"
	"os"
	"strings"
	"time"
)

type Message struct {
	Text string    `json:"text" yaml:"text"`
	Time time.Time `json:"time" yaml:"time"`
	Role string    `json:"role" yaml:"role"`
}

type FunctionDescription struct {
	Name        string `json:"name" yaml:"name"`
	Description string `json:"description" yaml:"description"`
	// JSON schema for the parameters
	Parameters map[string]interface{} `json:"parameters" yaml:"parameters"`
}

// here is the openai definition
// ChatCompletionRequestMessage is a message to use as the context for the chat completion API
//
//type ChatCompletionRequestMessage struct {
//	// Role is the role is the role of the the message. Can be "system", "user", or "assistant"
//	Role string `json:"role"`
//
//	// Content is the content of the message
//	Content string `json:"content"`
//}

// LoadFromFile loads messages from a json file or yaml file
func LoadFromFile(filename string) ([]*Message, error) {
	if strings.HasSuffix(filename, ".json") {
		return loadFromJSONFile(filename)
	} else if strings.HasSuffix(filename, ".yaml") || strings.HasSuffix(filename, ".yml") {
		return loadFromYAMLFile(filename)
	} else {
		return nil, nil
	}
}

func loadFromYAMLFile(filename string) ([]*Message, error) {
	f, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer func(f *os.File) {
		_ = f.Close()
	}(f)

	var messages []*Message
	err = yaml.NewDecoder(f).Decode(&messages)
	if err != nil {
		return nil, err
	}

	return messages, nil
}

func loadFromJSONFile(filename string) ([]*Message, error) {
	f, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer func(f *os.File) {
		_ = f.Close()
	}(f)

	var messages []*Message
	err = json.NewDecoder(f).Decode(&messages)
	if err != nil {
		return nil, err
	}

	return messages, nil
}

type Manager struct {
	Messages     []*Message
	Functions    []*FunctionDescription
	FunctionCall string
	SystemPrompt string
}

type ManagerOption func(*Manager)

func WithMessages(messages []*Message) ManagerOption {
	return func(m *Manager) {
		m.Messages = messages
	}
}

func WithSystemPrompt(systemPrompt string) ManagerOption {
	return func(m *Manager) {
		m.SystemPrompt = systemPrompt
	}
}

func NewManager(options ...ManagerOption) *Manager {
	ret := &Manager{}
	for _, option := range options {
		option(ret)
	}
	return ret
}

func (c *Manager) GetMessages() []*Message {
	return c.Messages
}

// GetMessagesWithSystemPrompt returns all messages with the system prompt prepended
func (c *Manager) GetMessagesWithSystemPrompt() []*Message {
	messages := []*Message{}

	if c.SystemPrompt != "" {
		messages = append(messages, &Message{
			Text: c.SystemPrompt,
			Time: time.Now(),
			Role: "system",
		})
	}

	messages = append(messages, c.Messages...)

	return messages
}

func (c *Manager) SetMessages(messages []*Message) {
	c.Messages = messages
}

func (c *Manager) AddMessages(messages ...*Message) {
	c.Messages = append(c.Messages, messages...)
}

func (c *Manager) AddFunctions(functions ...*FunctionDescription) {
	c.Functions = append(c.Functions, functions...)
}

func (c *Manager) GetSystemPrompt() string {
	return c.SystemPrompt
}

func (c *Manager) SetSystemPrompt(systemPrompt string) {
	c.SystemPrompt = systemPrompt
}

// GetSinglePrompt is a helper to use the context manager with a completion api.
// It just concatenates all the messages together with a prompt in front (if there are more than one message).
func (c *Manager) GetSinglePrompt() string {
	messages := c.GetMessagesWithSystemPrompt()
	if len(messages) == 0 {
		return ""
	}

	if len(messages) == 1 {
		return messages[0].Text
	}

	prompt := ""
	for _, message := range messages {
		prompt += fmt.Sprintf("[%s]: %s\n", message.Role, message.Text)
	}

	return prompt
}

func ConvertMessagesToOpenAIMessages(messages []*Message) ([]openai.ChatCompletionMessage, error) {
	res := make([]openai.ChatCompletionMessage, len(messages))
	for i, message := range messages {
		switch message.Role {
		case openai.ChatMessageRoleSystem:
		case openai.ChatMessageRoleAssistant:
		case openai.ChatMessageRoleUser:
		case openai.ChatMessageRoleFunction:
		default:
			return nil, errors.Errorf("invalid role: %s (should be one of system, assistant, user, function)", message.Role)
		}
		res[i] = openai.ChatCompletionMessage{
			Role:    message.Role,
			Content: message.Text,
		}
	}

	return res, nil
}

func ConvertFunctionsToOpenAIFunctions(functions []*FunctionDescription) ([]openai.FunctionDefinition, error) {
	res := make([]openai.FunctionDefinition, len(functions))
	for i, function := range functions {
		res[i] = openai.FunctionDefinition{
			Name:        function.Name,
			Description: function.Description,
			Parameters:  function.Parameters,
		}
	}

	return res, nil
}

func (c *Manager) GetFunctionCall() any {
	switch c.FunctionCall {
	case "":
		if len(c.Functions) == 0 {
			return nil
		} else {
			return "auto"
		}
	case "auto":
		return "auto"
	case "none":
		return "none"
	default:
		return map[string]string{
			"name": c.FunctionCall,
		}
	}
}
