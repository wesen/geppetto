package ai

import (
	"strings"

	"github.com/go-go-golems/geppetto/pkg/steps/ai/chat"
	"github.com/go-go-golems/geppetto/pkg/steps/ai/claude"
	claude_api "github.com/go-go-golems/geppetto/pkg/steps/ai/claude/api"
	"github.com/go-go-golems/geppetto/pkg/steps/ai/genai"
	"github.com/go-go-golems/geppetto/pkg/steps/ai/openai"
	"github.com/go-go-golems/geppetto/pkg/steps/ai/settings"
	ai_types "github.com/go-go-golems/geppetto/pkg/steps/ai/types"
	genai_api "github.com/google/generative-ai-go/genai" 
	"github.com/pkg/errors"
)

// StandardStepFactory implements the StepFactory interface for standard AI steps.
// It uses StepSettings to configure and create the appropriate chat.Step.
//
// TODO(manuel, 2024-07-26) Add support for tool settings properly, right now tools need to be passed explicitly
// which is only done in the NewStep function. This should probably be handled by the caller
// in a cleaner fashion.
type StandardStepFactory struct {
	Settings *settings.StepSettings
	// TODO(manuel, 2024-07-26) Add Tool Definition registry?
}

func NewStandardStepFactory(settings *settings.StepSettings) *StandardStepFactory {
	return &StandardStepFactory{
		Settings: settings,
	}
}

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
			ret, err = openai.NewStep(settings_, options...)
			if err != nil {
				return nil, err
			}

		case ai_types.ApiTypeClaude:
			tools := []claude_api.Tool{}
			ret, err = claude.NewChatStep(settings_, tools, options...)
			if err != nil {
				return nil, err
			}

		case ai_types.ApiTypeGenai:
			tools := []*genai_api.Tool{}
			ret, err = genai.NewChatStep(settings_, tools, options...)
			if err != nil {
				return nil, err
			}

		case ai_types.ApiTypeOllama:
			return nil, errors.New("ollama is not supported")

		case ai_types.ApiTypeMistral:
			return nil, errors.New("mistral is not supported")

		case ai_types.ApiTypePerplexity:
			return nil, errors.New("perplexity is not supported")

		case ai_types.ApiTypeCohere:
			return nil, errors.New("cohere is not supported")
		default:
			return nil, errors.Errorf("unsupported api type: %s", *settings_.Chat.ApiType)
		}

	} else {
		switch {
		case openai.IsOpenAiEngine(*settings_.Chat.Engine):
			apiType := ai_types.ApiTypeOpenAI
			settings_.Chat.ApiType = &apiType
			ret, err = openai.NewStep(settings_, options...)
			if err != nil {
				return nil, err
			}

		case claude.IsClaudeEngine(*settings_.Chat.Engine):
			apiType := ai_types.ApiTypeClaude
			settings_.Chat.ApiType = &apiType
			tools := []claude_api.Tool{}
			ret, err = claude.NewChatStep(settings_, tools, options...)
			if err != nil {
				return nil, err
			}

		case genai.IsGenAiEngine(*settings_.Chat.Engine):
			apiType := ai_types.ApiTypeGenai
			settings_.Chat.ApiType = &apiType
			tools := []*genai_api.Tool{}
			ret, err = genai.NewChatStep(settings_, tools, options...)
			if err != nil {
				return nil, err
			}

		default:
			return nil, errors.Errorf("could not infer api type for engine: %s", *settings_.Chat.Engine)
		}
	}

	// Apply step options
	for _, option := range options {
		err := option(ret)
		if err != nil {
			return nil, err
		}
	}

	// Wrap with caching if configured
	if ret != nil && settings_.Chat != nil && settings_.Chat.CacheSettings != nil && settings_.Chat.CacheSettings.CacheType != "none" {
		ret, err = settings_.Chat.WrapWithCache(ret, options...)
		if err != nil {
			return nil, errors.Wrap(err, "failed to wrap step with cache")
		}
	}

	return ret, nil
}

func IsAnyScaleEngine(s string) bool {
	return true
}
