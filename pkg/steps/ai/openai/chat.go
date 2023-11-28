package openai

import (
	"context"
	geppetto_context "github.com/go-go-golems/geppetto/pkg/context"
	"github.com/go-go-golems/geppetto/pkg/helpers"
	"github.com/go-go-golems/geppetto/pkg/steps"
	"github.com/go-go-golems/geppetto/pkg/steps/ai/settings"
	"github.com/pkg/errors"
	"io"
)

var _ steps.Step[[]*geppetto_context.Message, string] = &Step{}

type Step struct {
	Settings *settings.StepSettings
}

func (csf *Step) SetStreaming(b bool) {
	csf.Settings.Chat.Stream = b
}

func (csf *Step) Start(
	ctx context.Context,
	messages []*geppetto_context.Message,
) (*steps.StepResult[string], error) {
	client := makeClient(csf.Settings.OpenAI)

	req, err := makeCompletionRequest(csf.Settings, messages)
	if err != nil {
		return nil, err
	}

	stream := csf.Settings.Chat.Stream

	if stream {
		stream, err := client.CreateChatCompletionStream(ctx, *req)
		if err != nil {
			return steps.Reject[string](err), nil
		}
		c := make(chan helpers.Result[string])
		ret := steps.NewStepResult[string](c)

		// TODO(manuel, 2023-11-28) We need to collect this goroutine in Close(), or at least I think so?
		go func() {
			defer close(c)
			defer stream.Close()

			for {
				select {
				case <-ctx.Done():
					return
				default:
					response, err := stream.Recv()
					if errors.Is(err, io.EOF) {
						c <- helpers.NewValueResult[string]("")

						return
					}
					if err != nil {
						c <- helpers.NewErrorResult[string](err)
						return
					}

					c <- helpers.NewPartialResult[string](response.Choices[0].Delta.Content)
				}
			}
		}()

		return ret, nil
	} else {
		resp, err := client.CreateChatCompletion(ctx, *req)

		if err != nil {
			return steps.Reject[string](err), nil
		}

		return steps.Resolve(string(resp.Choices[0].Message.Content)), nil
	}
}

// Close is only called after the returned monad has been entirely consumed
func (csf *Step) Close(ctx context.Context) error {
	return nil
}
