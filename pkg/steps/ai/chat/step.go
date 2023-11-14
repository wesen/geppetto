package chat

import (
	"github.com/go-go-golems/geppetto/pkg/context"
	"github.com/go-go-golems/geppetto/pkg/steps"
	"github.com/go-go-golems/geppetto/pkg/steps/ai/claude"
	"github.com/go-go-golems/geppetto/pkg/steps/ai/openai"
	"github.com/go-go-golems/geppetto/pkg/steps/ai/settings"
	"github.com/go-go-golems/glazed/pkg/cmds/layers"
	"github.com/pkg/errors"
)

type Step interface {
	steps.Step[[]*context.Message, string]
	SetStreaming(bool)
}

type StepFactory interface {
	NewStepFromLayers(layers map[string]*layers.ParsedParameterLayer) (Step, error)
}

type StandardStepFactory struct {
	Settings *settings.StepSettings
}

func (s *StandardStepFactory) NewStepFromLayers(layers map[string]*layers.ParsedParameterLayer) (Step, error) {
	settings_ := s.Settings.Clone()
	err := settings_.UpdateFromParsedLayers(layers)
	if err != nil {
		return nil, err
	}

	if settings_.Chat == nil || settings_.Chat.Engine == nil {
		return nil, errors.New("no chat engine specified")
	}

	if openai.IsOpenAiEngine(*settings_.Chat.Engine) {
		return &openai.ChatStep{
			Settings: settings_,
		}, nil
	}

	if claude.IsClaudeEngine(*settings_.Chat.Engine) {
		return &claude.Step{
			Settings: settings_,
		}, nil
	}

	return nil, errors.Errorf("unknown chat engine: %s", *settings_.Chat.Engine)
}
