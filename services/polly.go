package services

import (
    "context"
    "fmt"
    "io"

    "github.com/aws/aws-sdk-go-v2/aws"
    "github.com/aws/aws-sdk-go-v2/service/polly"
    "github.com/aws/aws-sdk-go-v2/service/polly/types"
)

type PollyService struct {
    client *polly.Client
}

func NewPollyService(client *polly.Client) *PollyService {
    return &PollyService{client: client}
}

func (p *PollyService) TextToSpeech(ctx context.Context, text string) ([]byte, error) {
    // Polly has a 3000 character limit per request
    // For production, you'd need to split text and concatenate audio
    if len(text) > 3000 {
        text = text[:3000]
    }

    input := &polly.SynthesizeSpeechInput{
        OutputFormat: types.OutputFormatMp3,
        Text:         aws.String(text),
        VoiceId:      types.VoiceIdJoanna,
        Engine:       types.EngineNeural,
    }

    result, err := p.client.SynthesizeSpeech(ctx, input)
    if err != nil {
        return nil, fmt.Errorf("failed to synthesize speech: %w", err)
    }
    defer result.AudioStream.Close()

    return io.ReadAll(result.AudioStream)
}
