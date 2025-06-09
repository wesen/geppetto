package api

import (
	"encoding/json"

	"github.com/rs/zerolog"
)

type ContentType string

const (
	ContentTypeText       ContentType = "text"
	ContentTypeImage      ContentType = "image"
	ContentTypeToolUse    ContentType = "tool_use"
	ContentTypeToolResult ContentType = "tool_result"
)

type Content interface {
	Type() ContentType
}

type BaseContent struct {
	Type_ ContentType `json:"type"`
}

type TextContent struct {
	BaseContent
	Text string `json:"text"`
}

func (t TextContent) Type() ContentType {
	return ContentTypeText
}

type ImageContent struct {
	BaseContent
	Source ImageSource `json:"source"`
}

func (i ImageContent) Type() ContentType {
	return ContentTypeImage
}

type ImageSource struct {
	BaseContent
	Type      string `json:"type"`
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}

type ToolUseContent struct {
	BaseContent
	ID    string `json:"id"`
	Name  string `json:"name"`
	Input string `json:"input"`
}

func (t ToolUseContent) Type() ContentType {
	return ContentTypeToolUse
}

type ToolResultContent struct {
	BaseContent
	ToolUseID string `json:"tool_use_id"`
	Content   string `json:"content"`
}

func (t ToolResultContent) Type() ContentType {
	return ContentTypeToolResult
}

// GenericContent represents an unknown content type from the API
// This allows us to handle new content types gracefully
type GenericContent struct {
	BaseContent
	Data map[string]interface{} `json:"data"`
}

func (g GenericContent) Type() ContentType {
	return g.Type_
}

func NewTextContent(text string) Content {
	return TextContent{BaseContent: BaseContent{Type_: ContentTypeText}, Text: text}
}

func NewImageContent(mediaType, base64Data string) Content {
	return ImageContent{
		BaseContent: BaseContent{
			Type_: ContentTypeImage,
		},
		Source: ImageSource{
			Type:      "base64",
			MediaType: mediaType,
			Data:      base64Data,
		},
	}
}

func NewToolUseContent(toolID, toolName string, toolInput string) Content {
	return ToolUseContent{
		BaseContent: BaseContent{Type_: ContentTypeToolUse},
		ID:          toolID,
		Name:        toolName,
		Input:       toolInput,
	}
}

func NewToolResultContent(toolUseID, content string) Content {
	return ToolResultContent{
		BaseContent: BaseContent{Type_: ContentTypeToolResult},
		ToolUseID:   toolUseID,
		Content:     content,
	}
}

func (bc BaseContent) MarshalZerologObject(e *zerolog.Event) {
	e.Str("type", string(bc.Type_))
}

func (tc TextContent) MarshalZerologObject(e *zerolog.Event) {
	e.Object("base", tc.BaseContent)
	e.Str("text", tc.Text)
}

func (ic ImageContent) MarshalZerologObject(e *zerolog.Event) {
	e.Object("base", ic.BaseContent)
	e.Object("source", ic.Source)
}

func (is ImageSource) MarshalZerologObject(e *zerolog.Event) {
	e.Object("base", is.BaseContent)
	e.Str("type", is.Type)
	e.Str("media_type", is.MediaType)
	e.Str("data", is.Data)
}

func (tuc ToolUseContent) MarshalZerologObject(e *zerolog.Event) {
	e.Object("base", tuc.BaseContent)
	e.Str("id", tuc.ID)
	e.Str("name", tuc.Name)
	e.Str("input", tuc.Input)
}

func (trc ToolResultContent) MarshalZerologObject(e *zerolog.Event) {
	e.Object("base", trc.BaseContent)
	e.Str("tool_use_id", trc.ToolUseID)
	e.Str("content", trc.Content)
}

func (gc GenericContent) MarshalZerologObject(e *zerolog.Event) {
	e.Object("base", gc.BaseContent)
	e.Interface("data", gc.Data)
}

func UnmarshalContent(data []byte) (Content, error) {
	var base BaseContent
	if err := json.Unmarshal(data, &base); err != nil {
		return nil, err
	}

	switch base.Type_ {
	case ContentTypeText:
		var text TextContent
		if err := json.Unmarshal(data, &text); err != nil {
			return nil, err
		}
		return text, nil
	case ContentTypeImage:
		var image ImageContent
		if err := json.Unmarshal(data, &image); err != nil {
			return nil, err
		}
		return image, nil
	case ContentTypeToolUse:
		var toolUse ToolUseContent
		if err := json.Unmarshal(data, &toolUse); err != nil {
			return nil, err
		}
		return toolUse, nil
	case ContentTypeToolResult:
		var toolResult ToolResultContent
		if err := json.Unmarshal(data, &toolResult); err != nil {
			return nil, err
		}
		return toolResult, nil
	default:
		// For unknown content types, create a generic content object that preserves the type
		// This makes the system more resilient to API changes from Anthropic
		var rawContent map[string]interface{}
		if err := json.Unmarshal(data, &rawContent); err != nil {
			return nil, err
		}
		return GenericContent{
			BaseContent: BaseContent{Type_: base.Type_},
			Data:        rawContent,
		}, nil
	}
}
