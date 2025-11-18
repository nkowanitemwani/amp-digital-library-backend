package handlers

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/nkowanitemwani/amp-digital-library-backend/models"
	"github.com/nkowanitemwani/amp-digital-library-backend/services"
)

type FileHandler struct {
	s3Service       *services.S3Service
	textractService *services.TextractService
	pollyService    *services.PollyService
	dynamoService   *services.DynamoDBService
}

func NewFileHandler(
	s3 *services.S3Service,
	textract *services.TextractService,
	polly *services.PollyService,
	dynamo *services.DynamoDBService,
) *FileHandler {
	return &FileHandler{
		s3Service:       s3,
		textractService: textract,
		pollyService:    polly,
		dynamoService:   dynamo,
	}
}

func (h *FileHandler) UploadFile(c *gin.Context) {
	file, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No file uploaded"})
		return
	}

	// Validate file type
	if file.Header.Get("Content-Type") != "application/pdf" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Only PDF files are allowed"})
		return
	}

	// Read file data
	fileData, err := file.Open()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to read file"})
		return
	}
	defer fileData.Close()

	pdfBytes := make([]byte, file.Size)
	_, err = fileData.Read(pdfBytes)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to read file data"})
		return
	}

	// Generate file ID
	fileID := uuid.New().String()
	pdfKey := fmt.Sprintf("pdfs/%s.pdf", fileID)

	ctx := context.Background()

	// Upload PDF to S3
	err = h.s3Service.UploadFile(ctx, pdfKey, pdfBytes, "application/pdf")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to upload PDF"})
		return
	}

	// Save initial metadata
	metadata := &models.FileMetaData{
		FileID:     fileID,
		FileName:   file.Filename,
		PDFPath:    pdfKey,
		Status:     "processing",
		UploadedAt: time.Now().Format(time.RFC3339),
	}

	err = h.dynamoService.SaveFileMetadata(ctx, metadata)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save metadata"})
		return
	}

	// Process in background (in production, use SQS or Lambda)
	go h.processFile(fileID, pdfBytes, pdfKey)

	c.JSON(http.StatusAccepted, gin.H{
		"file_id": fileID,
		"status":  "processing",
		"message": "File uploaded successfully, processing audio conversion",
	})
}

func (h *FileHandler) processFile(fileID string, pdfData []byte, pdfKey string) {
    ctx := context.Background()

    log.Printf("Starting processing for file: %s", fileID)

    // Extract text from PDF
    log.Printf("Extracting text from PDF for file: %s", fileID)
    text, err := h.textractService.ExtractTextFromPDF(ctx, pdfData)
    if err != nil {
        log.Printf("ERROR: Failed to extract text for file %s: %v", fileID, err)
        h.updateStatus(ctx, fileID, "failed")
        return
    }
    log.Printf("Successfully extracted %d characters of text for file: %s", len(text), fileID)

    // Convert text to speech
    log.Printf("Converting text to speech for file: %s", fileID)
    audioData, err := h.pollyService.TextToSpeech(ctx, text)
    if err != nil {
        log.Printf("ERROR: Failed to convert text to speech for file %s: %v", fileID, err)
        h.updateStatus(ctx, fileID, "failed")
        return
    }
    log.Printf("Successfully generated %d bytes of audio for file: %s", len(audioData), fileID)

    // Upload audio to S3
    log.Printf("Uploading audio to S3 for file: %s", fileID)
    audioKey := fmt.Sprintf("audio/%s.mp3", fileID)
    err = h.s3Service.UploadFile(ctx, audioKey, audioData, "audio/mpeg")
    if err != nil {
        log.Printf("ERROR: Failed to upload audio for file %s: %v", fileID, err)
        h.updateStatus(ctx, fileID, "failed")
        return
    }
    log.Printf("Successfully uploaded audio for file: %s", fileID)

    // Update metadata
    metadata, err := h.dynamoService.GetFileMetadata(ctx, fileID)
    if err != nil {
        log.Printf("ERROR: Failed to get metadata for file %s: %v", fileID, err)
        return
    }
    metadata.AudioPath = audioKey
    metadata.Status = "ready"
    metadata.ProcessedAt = time.Now().Format(time.RFC3339)

    err = h.dynamoService.SaveFileMetadata(ctx, metadata)
    if err != nil {
        log.Printf("ERROR: Failed to save metadata for file %s: %v", fileID, err)
        return
    }
    
    log.Printf("Successfully completed processing for file: %s", fileID)
}

func (h *FileHandler) updateStatus(ctx context.Context, fileID, status string) {
	metadata, err := h.dynamoService.GetFileMetadata(ctx, fileID)
	if err != nil {
		return
	}
	metadata.Status = status
	h.dynamoService.SaveFileMetadata(ctx, metadata)
}

func (h *FileHandler) GetFileStatus(c *gin.Context) {
	fileID := c.Param("id")
	ctx := context.Background()

	metadata, err := h.dynamoService.GetFileMetadata(ctx, fileID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "File not found"})
		return
	}

	c.JSON(http.StatusOK, metadata)
}

func (h *FileHandler) GetAudioURL(c *gin.Context) {
    fileID := c.Param("id")
    ctx := context.Background()

    metadata, err := h.dynamoService.GetFileMetadata(ctx, fileID)
    if err != nil {
        c.JSON(http.StatusNotFound, gin.H{"error": "File not found"})
        return
    }

    if metadata.Status != "ready" {
        c.JSON(http.StatusBadRequest, gin.H{"error": "Audio not ready yet"})
        return
    }

    // Use direct public URL instead of presigned URL
    url := h.s3Service.GetPublicURL(metadata.AudioPath)

    c.JSON(http.StatusOK, gin.H{
        "audio_url": url,
        "file_name": metadata.FileName,
    })
}
