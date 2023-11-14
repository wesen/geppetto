package openai

import (
	"context"
	"fmt"
	openai3 "github.com/go-go-golems/geppetto/pkg/openai"
	openai2 "github.com/go-go-golems/geppetto/pkg/steps/ai/settings/openai"
	"github.com/go-go-golems/glazed/pkg/cmds"
	"github.com/go-go-golems/glazed/pkg/cmds/layers"
	"github.com/go-go-golems/glazed/pkg/cmds/parameters"
	"github.com/go-go-golems/glazed/pkg/middlewares"
	"github.com/go-go-golems/glazed/pkg/settings"
	"github.com/go-go-golems/glazed/pkg/types"
	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
	"github.com/sashabaranov/go-openai"
	"os"
	"path/filepath"
	"sync"
)

type TranscribeCommand struct {
	*cmds.CommandDescription
}

func NewTranscribeCommand() (*TranscribeCommand, error) {
	layer, err := openai2.NewParameterLayer()
	if err != nil {
		return nil, errors.Wrap(err, "could not create OpenAI parameter layer")
	}

	glazedParameterLayer, err := settings.NewGlazedParameterLayers()
	if err != nil {
		return nil, errors.Wrap(err, "could not create Glazed parameter layer")
	}

	return &TranscribeCommand{
		CommandDescription: cmds.NewCommandDescription(
			"transcribe",
			cmds.WithShort("Transcribe MP3 files using OpenAI"),
			cmds.WithFlags(
				parameters.NewParameterDefinition(
					"dir",
					parameters.ParameterTypeString,
					parameters.WithHelp("Path to the directory containing MP3 files"),
					parameters.WithDefault(""),
				),
				parameters.NewParameterDefinition(
					"file",
					parameters.ParameterTypeString,
					parameters.WithHelp("Path to the MP3 file to transcribe"),
					parameters.WithDefault(""),
				),
				parameters.NewParameterDefinition(
					"workers",
					parameters.ParameterTypeInteger,
					parameters.WithHelp("Number of parallel workers"),
					parameters.WithDefault(4),
				),
				parameters.NewParameterDefinition(
					"model",
					parameters.ParameterTypeString,
					parameters.WithHelp("Model used for transcription"),
					parameters.WithDefault(openai.Whisper1),
				),
				parameters.NewParameterDefinition(
					"prompt",
					parameters.ParameterTypeString,
					parameters.WithHelp("Prompt for the transcription model"),
					parameters.WithDefault(""),
				),
				parameters.NewParameterDefinition(
					"language",
					parameters.ParameterTypeString,
					parameters.WithHelp("Language for the transcription model"),
					parameters.WithDefault(""),
				),
				parameters.NewParameterDefinition(
					"temperature",
					parameters.ParameterTypeFloat,
					parameters.WithHelp("Temperature for the transcription model"),
					parameters.WithDefault(0.0),
				),
				parameters.NewParameterDefinition(
					"with-segments",
					parameters.ParameterTypeBool,
					parameters.WithHelp("Whether to output individual segments in the output"),
					parameters.WithDefault(true),
				)),
			cmds.WithLayers(layer, glazedParameterLayer),
		),
	}, nil
}

func (c *TranscribeCommand) Run(
	ctx context.Context,
	parsedLayers map[string]*layers.ParsedParameterLayer,
	ps map[string]interface{},
	gp middlewares.Processor,
) error {
	// Fetching parsed flags
	dirPath := ps["dir"].(string)
	filePath := ps["file"].(string)
	workers := ps["workers"].(int)
	model := ps["model"].(string)
	prompt := ps["prompt"].(string)
	language := ps["language"].(string)
	temperature := ps["temperature"].(float64)
	withSegments := ps["with-segments"].(bool)

	openaiSettings, err := openai2.NewSettingsFromParsedLayer(
		parsedLayers["openai-chat"],
	)
	if err != nil {
		return errors.Wrap(err, "could not create OpenAI settings")
	}
	if openaiSettings.APIKey == nil || *openaiSettings.APIKey == "" {
		return errors.New("OpenAI API key is required")
	}

	// Create the TranscriptionClient
	tc := openai3.NewTranscriptionClient(*openaiSettings.APIKey, model, prompt, language, float32(temperature))

	var files []string
	if filePath != "" {
		files = append(files, filePath)
	}
	if dirPath != "" {
		// Read the directory
		files_, err := os.ReadDir(dirPath)
		if err != nil {
			return fmt.Errorf("Failed to read the directory: %v", err)
		}

		for _, file := range files_ {
			files = append(files, filepath.Join(dirPath, file.Name()))
		}
	}

	if len(files) == 0 {
		return errors.New("No files found")
	}

	var wg sync.WaitGroup
	out := make(chan openai3.Transcription, len(files))

	transcriptions := map[string]openai3.Transcription{}

	for _, file := range files {
		wg.Add(1)
		go tc.TranscribeFile(file, out, &wg)

		// Limit concurrent workers
		for len(out) >= workers {
			transcription := <-out
			transcriptions[transcription.File] = transcription
		}
	}

	wg.Wait()
	close(out)

	for transcription := range out {
		transcriptions[transcription.File] = transcription
	}

	for _, file := range files {
		transcription, ok := transcriptions[file]
		if !ok {
			log.Warn().Str("file", file).Msg("No transcription found")
			continue
		}
		// Convert Transcription to Row and add to Processor
		if withSegments {
			for _, segment := range transcription.Response.Segments {
				row := types.NewRow(
					types.MRP("file", transcription.File),
					types.MRP("start_sec", segment.Start),
					types.MRP("end_sec", segment.End),
					types.MRP("transient", segment.Transient),
					types.MRP("seek", segment.Seek),
					types.MRP("temperature", segment.Temperature),
					types.MRP("avg_logprob", segment.AvgLogprob),
					types.MRP("compression_ratio", segment.CompressionRatio),
					types.MRP("no_speech_prob", segment.NoSpeechProb),
					types.MRP("response", segment.Text),
				)
				if err := gp.AddRow(ctx, row); err != nil {
					return err
				}
			}
		} else {
			row := types.NewRow(
				types.MRP("file", transcription.File),
				types.MRP("response", transcription.Response.Text),
			)
			if err := gp.AddRow(ctx, row); err != nil {
				return err
			}
		}
	}

	return nil
}
