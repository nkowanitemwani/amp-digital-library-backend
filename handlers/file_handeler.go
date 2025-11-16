package handlers

import (
    "context"
    "fmt"
    "net/http"
    "time"

    "github.com/gin-gonic/gin"
    "github.com/google/uuid"
    "github.com/yourusername/digital-library-backend/models"
    "github.com/yourusername/digital-library-backend/services"
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
    metadata := &models.FileMetadata{
        FileID:     fileID,
        FileName:   file.Filename,
        PDFPath:    pdfKey,
        Status:     "processing",
        UploadedAt: time.Now(),
    }

    err = h.dynamoService.SaveFileMetadata(ctx, metadata)
    if err != nil {
        c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save metadata"})
        return
    }

    // Process in background (in production, use SQS or Lambda)
    go h.processFile(fileID, pdfBytes)

    c.JSON(http.StatusAccepted, gin.H{
        "file_id": fileID,
        "status":  "processing",
        "message": "File uploaded successfully, processing audio conversion",
    })
}

func (h *FileHandler) processFile(fileID string, pdfData []byte) {
    ctx := context.Background()

    // Extract text from PDF
    text, err := h.textractService.ExtractTextFromPDF(ctx, pdfData)
    if err != nil {
        h.updateStatus(ctx, fileID, "failed")
        return
    }

    // Convert text to speech
    audioData, err := h.pollyService.TextToSpeech(ctx, text)
    if err != nil {
        h.updateStatus(ctx, fileID, "failed")
        return
    }

    // Upload audio to S3
    audioKey := fmt.Sprintf("audio/%s.mp3", fileID)
    err = h.s3Service.UploadFile(ctx, audioKey, audioData, "audio/mpeg")
    if err != nil {
        h.updateStatus(ctx, fileID, "failed")
        return
    }

    // Update metadata
    metadata, _ := h.dynamoService.GetFileMetadata(ctx, fileID)
    metadata.AudioPath = audioKey
    metadata.Status = "ready"
    metadata.ProcessedAt = time.Now()

    h.dynamoService.SaveFileMetadata(ctx, metadata)
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

    // Generate presigned URL for audio
    url, err := h.s3Service.GetPresignedURL(ctx, metadata.AudioPath)
    if err != nil {
        c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate URL"})
        return
    }

    c.JSON(http.StatusOK, gin.H{
        "audio_url": url,
        "file_name": metadata.FileName,
    })
}