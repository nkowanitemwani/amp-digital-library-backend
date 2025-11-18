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
    client     *textract.Client
    bucketName string
}

func NewTextractService(client *textract.Client, bucketName string) *TextractService {
    return &TextractService{
        client:     client,
        bucketName: bucketName,
    }
}

func (t *TextractService) ExtractTextFromPDF(ctx context.Context, pdfData []byte, s3Key string) (string, error) {
    // If PDF is less than 5MB, use direct upload
    if len(pdfData) < 5*1024*1024 {
        return t.extractFromBytes(ctx, pdfData)
    }
    
    // Otherwise use S3 reference
    return t.extractFromS3(ctx, s3Key)
}

func (t *TextractService) extractFromBytes(ctx context.Context, pdfData []byte) (string, error) {
    input := &textract.DetectDocumentTextInput{
        Document: &types.Document{
            Bytes: pdfData,
        },
    }

    result, err := t.client.DetectDocumentText(ctx, input)
    if err != nil {
        return "", fmt.Errorf("failed to extract text from bytes: %w", err)
    }

    return t.extractTextFromBlocks(result.Blocks), nil
}

func (t *TextractService) extractFromS3(ctx context.Context, s3Key string) (string, error) {
    input := &textract.DetectDocumentTextInput{
        Document: &types.Document{
            S3Object: &types.S3Object{
                Bucket: aws.String(t.bucketName),
                Name:   aws.String(s3Key),
            },
        },
    }

    result, err := t.client.DetectDocumentText(ctx, input)
    if err != nil {
        return "", fmt.Errorf("failed to extract text from S3: %w", err)
    }

    return t.extractTextFromBlocks(result.Blocks), nil
}

func (t *TextractService) extractTextFromBlocks(blocks []types.Block) string {
    var textBuilder strings.Builder
    for _, block := range blocks {
        if block.BlockType == types.BlockTypeLine {
            textBuilder.WriteString(aws.ToString(block.Text))
            textBuilder.WriteString(" ")
        }
    }
    return textBuilder.String()
}