package services

import (
    "context"
    "fmt"
    "strings"

    "github.com/aws/aws-sdk-go-v2/aws"
    "github.com/aws/aws-sdk-go-v2/service/textract"
    "github.com/aws/aws-sdk-go-v2/service/textract/types"
)

type TextractService struct {
    client *textract.Client
}

func NewTextractService(client *textract.Client) *TextractService {
    return &TextractService{client: client}
}

func (t *TextractService) ExtractTextFromPDF(ctx context.Context, pdfData []byte) (string, error) {
    input := &textract.DetectDocumentTextInput{
        Document: &types.Document{
            Bytes: pdfData,
        },
    }

    result, err := t.client.DetectDocumentText(ctx, input)
    if err != nil {
        return "", fmt.Errorf("failed to extract text: %w", err)
    }

    var textBuilder strings.Builder
    for _, block := range result.Blocks {
        if block.BlockType == types.BlockTypeLine {
            textBuilder.WriteString(aws.ToString(block.Text))
            textBuilder.WriteString(" ")
        }
    }

    return textBuilder.String(), nil
}