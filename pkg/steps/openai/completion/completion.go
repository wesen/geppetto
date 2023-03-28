package completion

import (
	"context"
	"github.com/go-go-golems/geppetto/pkg/helpers"
	"github.com/go-go-golems/geppetto/pkg/steps"
	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
	"github.com/sashabaranov/go-openai"
	"golang.org/x/sync/errgroup"
	"io"
	"strings"
)

type StepState int

const (
	StepNotStarted StepState = iota
	StepRunning
	StepFinished
	StepClosed
)

type Step struct {
	output   chan helpers.Result[string]
	state    StepState
	settings *StepSettings
}

func NewStep(settings *StepSettings) *Step {
	return &Step{
		output:   make(chan helpers.Result[string]),
		settings: settings,
		state:    StepNotStarted,
	}
}

func (s *Step) Run(ctx context.Context, prompt string) error {
	s.state = StepRunning

	defer func() {
		s.state = StepClosed
		close(s.output)
	}()

	clientSettings := s.settings.ClientSettings
	if clientSettings == nil {
		s.output <- helpers.NewErrorResult[string](steps.ErrMissingClientSettings)
		return nil
	}

	if clientSettings.APIKey == nil {
		s.output <- helpers.NewErrorResult[string](steps.ErrMissingClientAPIKey)
		return nil
	}

	engine := ""
	if s.settings.Engine != nil {
		engine = *s.settings.Engine
	} else if clientSettings.DefaultEngine != nil {
		engine = *clientSettings.DefaultEngine
	} else {
		s.output <- helpers.NewErrorResult[string](errors.New("no engine specified"))
		return nil
	}

	if strings.HasPrefix(engine, "gpt-3.5-turbo") {
		log.Debug().Msg("using turbo (chat) engine")
		return s.RunChatCompletion(ctx, prompt, engine)
	} else {
		log.Debug().Msg("using regular engine")
		return s.RunCompletion(ctx, prompt, engine)
	}
}

func (s *Step) RunCompletion(ctx context.Context, prompt, engine string) error {
	temperature := 0.0
	if s.settings.Temperature != nil {
		temperature = *s.settings.Temperature
	}
	topP := 0.0
	if s.settings.TopP != nil {
		topP = *s.settings.TopP
	}
	maxTokens := 32
	if s.settings.MaxResponseTokens != nil {
		maxTokens = *s.settings.MaxResponseTokens
	}
	n := 1
	if s.settings.N != nil {
		n = *s.settings.N
	}
	stream := s.settings.Stream
	stop := s.settings.Stop
	logProbs := 0
	if s.settings.LogProbs != nil {
		logProbs = *s.settings.LogProbs
	}
	frequencyPenalty := 0.0
	if s.settings.FrequencyPenalty != nil {
		frequencyPenalty = *s.settings.FrequencyPenalty
	}
	presencePenalty := 0.0
	if s.settings.PresencePenalty != nil {
		presencePenalty = *s.settings.PresencePenalty
	}
	logitBias := s.settings.LogitBias
	bestOf := 0
	if s.settings.BestOf != nil {
		bestOf = *s.settings.BestOf
	}

	log.Debug().
		Str("engine", engine).
		Int("max_response_tokens", maxTokens).
		Float32("temperature", float32(temperature)).
		Float32("top_p", float32(topP)).
		Int("n", n).
		Int("log_probs", logProbs).
		Bool("stream", stream).
		Strs("stop", stop).
		Float32("frequency_penalty", float32(frequencyPenalty)).
		Float32("presence_penalty", float32(presencePenalty)).
		Interface("logit_bias", logitBias).
		Int("best_of", bestOf).
		Msg("sending completion request")

	// TODO(manuel, 2023-01-28) - handle multiple values
	if s.settings.N != nil && *s.settings.N != 1 {
		s.output <- helpers.NewErrorResult[string](errors.New("N > 1 is not supported yet"))
		return nil
	}

	client := openai.NewClient(*s.settings.ClientSettings.APIKey)

	req := openai.CompletionRequest{
		Model:            engine,
		Prompt:           prompt,
		MaxTokens:        maxTokens,
		Temperature:      float32(temperature),
		TopP:             float32(topP),
		N:                n,
		Stream:           stream,
		LogProbs:         logProbs,
		Echo:             false,
		Stop:             stop,
		PresencePenalty:  float32(presencePenalty),
		FrequencyPenalty: float32(frequencyPenalty),
		BestOf:           bestOf,
		LogitBias:        nil,
	}

	if stream {
		stream, err := client.CreateCompletionStream(ctx, req)
		if err != nil {
			s.output <- helpers.NewErrorResult[string](err)
			return nil
		}
		defer stream.Close()

		for {
			response, err := stream.Recv()
			if errors.Is(err, io.EOF) {
				s.state = StepFinished
				s.output <- helpers.NewValueResult[string]("")
				return nil
			}
			if err != nil {
				s.state = StepFinished
				s.output <- helpers.NewErrorResult[string](err)
				return nil
			}

			s.output <- helpers.NewPartialResult[string](response.Choices[0].Text)
		}

	} else {
		resp, err := client.CreateCompletion(ctx, req)
		if err != nil {
			s.output <- helpers.NewErrorResult[string](err)
			return nil
		}

		completion := ""

		s.state = StepFinished

		if err != nil {
			s.output <- helpers.NewErrorResult[string](err)
			return nil
		}

		// TODO(manuel, 2023-02-04) Handle multiple outputs
		// See https://github.com/wesen/geppetto/issues/23

		// TODO(manuel, 2023-03-38) Count usage
		// See https://github.com/go-go-golems/geppetto/issues/46

		if len(resp.Choices) == 0 {
			s.output <- helpers.NewErrorResult[string](errors.New("no choices returned from OpenAI"))
			return nil
		}

		completion = resp.Choices[0].Text

		s.output <- helpers.NewValueResult(completion)
	}

	return nil
}

func (s *Step) RunChatCompletion(ctx context.Context, prompt, engine string) error {
	temperature := 0.0
	if s.settings.Temperature != nil {
		temperature = *s.settings.Temperature
	}
	topP := 0.0
	if s.settings.TopP != nil {
		topP = *s.settings.TopP
	}
	maxTokens := 32
	if s.settings.MaxResponseTokens != nil {
		maxTokens = *s.settings.MaxResponseTokens
	}
	n := 1
	if s.settings.N != nil {
		n = *s.settings.N
	}
	stream := s.settings.Stream
	stop := s.settings.Stop
	frequencyPenalty := 0.0
	if s.settings.FrequencyPenalty != nil {
		frequencyPenalty = *s.settings.FrequencyPenalty
	}
	presencePenalty := 0.0
	if s.settings.PresencePenalty != nil {
		presencePenalty = *s.settings.PresencePenalty
	}
	logitBias := s.settings.LogitBias

	log.Debug().
		Str("engine", engine).
		Int("max_response_tokens", maxTokens).
		Float32("temperature", float32(temperature)).
		Float32("top_p", float32(topP)).
		Int("n", n).
		Bool("stream", stream).
		Strs("stop", stop).
		Float32("frequency_penalty", float32(frequencyPenalty)).
		Float32("presence_penalty", float32(presencePenalty)).
		Interface("logit_bias", logitBias).
		Msg("sending completion request")

	// TODO(manuel, 2023-01-28) - handle multiple values
	if s.settings.N != nil && *s.settings.N != 1 {
		s.output <- helpers.NewErrorResult[string](errors.New("N > 1 is not supported yet"))
		return nil
	}

	client := openai.NewClient(*s.settings.ClientSettings.APIKey)

	req := openai.ChatCompletionRequest{
		Model:            engine,
		MaxTokens:        maxTokens,
		Temperature:      float32(temperature),
		TopP:             float32(topP),
		N:                n,
		Stream:           stream,
		Stop:             stop,
		PresencePenalty:  float32(presencePenalty),
		FrequencyPenalty: float32(frequencyPenalty),
		LogitBias:        nil,
		Messages: []openai.ChatCompletionMessage{
			{
				Role:    openai.ChatMessageRoleUser,
				Content: prompt,
			},
		},
	}

	// TODO(manuel, 2023-02-04) Handle multiple outputs
	// See https://github.com/wesen/geppetto/issues/23

	// TODO(manuel, 2023-03-38) Count usage
	// See https://github.com/go-go-golems/geppetto/issues/46

	if stream {
		stream, err := client.CreateChatCompletionStream(ctx, req)
		if err != nil {
			s.output <- helpers.NewErrorResult[string](err)
			return nil
		}
		defer stream.Close()

		for {
			response, err := stream.Recv()
			if errors.Is(err, io.EOF) {
				s.state = StepFinished
				s.output <- helpers.NewValueResult[string]("")
				return nil
			}
			if err != nil {
				s.state = StepFinished
				s.output <- helpers.NewErrorResult[string](err)
				return nil
			}

			s.output <- helpers.NewPartialResult[string](response.Choices[0].Delta.Content)
		}
	} else {
		resp, err := client.CreateChatCompletion(ctx, req)
		s.state = StepFinished

		if err != nil {
			s.output <- helpers.NewErrorResult[string](err)
			return nil
		}

		// TODO(manuel, 2023-03-28) Properly handle message formats
		s.output <- helpers.NewValueResult[string](resp.Choices[0].Message.Content)

	}

	return nil
}

func (s *Step) GetOutput() <-chan helpers.Result[string] {
	return s.output
}

func (s *Step) GetState() interface{} {
	return s.state
}

func (s *Step) IsFinished() bool {
	return s.state == StepFinished || s.state == StepClosed
}

// TODO(manuel, 2023-02-04) This could be generic, and take a factory

// MultiCompletionStep runs multiple completion steps in parallel
type MultiCompletionStep struct {
	output   chan helpers.Result[[]string]
	state    StepState
	settings *StepSettings
}

func NewMultiCompletionStep(settings *StepSettings) *MultiCompletionStep {
	return &MultiCompletionStep{
		output:   make(chan helpers.Result[[]string]),
		settings: settings,
		state:    StepNotStarted,
	}
}

func (mc *MultiCompletionStep) Run(ctx context.Context, prompts []string) error {
	completionSteps := make([]*Step, len(prompts))
	chans := make([]<-chan helpers.Result[string], len(prompts))
	for i := range prompts {
		completionSteps[i] = NewStep(mc.settings)
		chans[i] = completionSteps[i].GetOutput()
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	eg, ctx2 := errgroup.WithContext(ctx)

	results := []string{}
	eg.Go(func() error {
		mergeChannel := helpers.MergeChannels(chans...)
		for {
			select {
			case <-ctx2.Done():
				return ctx2.Err()
			case result := <-mergeChannel:
				v, err := result.Value()
				// if we have an error, just store the "" string
				if err != nil {
					v = ""
				}
				results = append(results, v)
			}
		}
	})

	for i, prompt := range prompts {
		j := i
		prompt_ := prompt
		eg.Go(func() error {
			return completionSteps[j].Run(ctx2, prompt_)
		})
	}

	return eg.Wait()
}

func (mc *MultiCompletionStep) GetOutput() <-chan helpers.Result[[]string] {
	return mc.output
}

func (mc *MultiCompletionStep) GetState() interface{} {
	return mc.state
}

func (mc *MultiCompletionStep) IsFinished() bool {
	return mc.state == StepFinished
}
