package services

import (
    "bytes"
    "context"
    "fmt"
    "strings"

    "github.com/ledongthuc/pdf"
)

type TextractService struct {
    // No AWS client needed
}

func NewTextractService() *TextractService {
    return &TextractService{}
}

func (t *TextractService) ExtractTextFromPDF(ctx context.Context, pdfData []byte) (string, error) {
    // Create a reader from the PDF bytes
    reader := bytes.NewReader(pdfData)
    
    pdfReader, err := pdf.NewReader(reader, int64(len(pdfData)))
    if err != nil {
        return "", fmt.Errorf("failed to create PDF reader: %w", err)
    }

    var textBuilder strings.Builder
    totalPages := pdfReader.NumPage()

    // Extract text from each page
    for pageNum := 1; pageNum <= totalPages; pageNum++ {
        page := pdfReader.Page(pageNum)
        if page.V.IsNull() {
            continue
        }

        text, err := page.GetPlainText(nil)
        if err != nil {
            // Log but continue with other pages
            fmt.Printf("Warning: failed to extract text from page %d: %v\n", pageNum, err)
            continue
        }

        textBuilder.WriteString(text)
        textBuilder.WriteString("\n")
    }

    extractedText := textBuilder.String()
    if len(extractedText) == 0 {
        return "", fmt.Errorf("no text extracted from PDF")
    }

    return extractedText, nil
}