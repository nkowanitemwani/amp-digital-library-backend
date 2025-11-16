package main

import (
    "context"
    "log"
    "os"

    "github.com/aws/aws-sdk-go-v2/config"
    "github.com/aws/aws-sdk-go-v2/service/dynamodb"
    "github.com/aws/aws-sdk-go-v2/service/polly"
    "github.com/aws/aws-sdk-go-v2/service/s3"
    "github.com/aws/aws-sdk-go-v2/service/textract"
    "github.com/gin-gonic/gin"
    "github.com/nkowanitemwani/amp-digital-library-backend/handlers"
    "github.com/nkowanitemwani/amp-digital-library-backend/services"
)

func main() {
    // Load AWS configuration
    cfg, err := config.LoadDefaultConfig(context.Background(),
        config.WithRegion("us-east-1"),
    )
    if err != nil {
        log.Fatalf("Failed to load AWS config: %v", err)
    }

    // Initialize AWS clients
    s3Client := s3.NewFromConfig(cfg)
    textractClient := textract.NewFromConfig(cfg)
    pollyClient := polly.NewFromConfig(cfg)
    dynamoClient := dynamodb.NewFromConfig(cfg)

    // Get configuration from environment
    bucketName := os.Getenv("S3_BUCKET_NAME")
    if bucketName == "" {
        bucketName = "your-library-bucket"
    }

    tableName := os.Getenv("DYNAMODB_TABLE_NAME")
    if tableName == "" {
        tableName = "digital-library-files"
    }

    // Initialize services
    s3Service := services.NewS3Service(s3Client, bucketName)
    textractService := services.NewTextractService(textractClient)
    pollyService := services.NewPollyService(pollyClient)
    dynamoService := services.NewDynamoDBService(dynamoClient, tableName)

    // Initialize handler
    fileHandler := handlers.NewFileHandler(s3Service, textractService, pollyService, dynamoService)

    // Set up Gin router
    router := gin.Default()

    // Routes
    router.POST("/upload", fileHandler.UploadFile)
    router.GET("/files/:id", fileHandler.GetFileStatus)
    router.GET("/files/:id/audio", fileHandler.GetAudioURL)

    // Start server
    port := os.Getenv("PORT")
    if port == "" {
        port = "8080"
    }

    log.Printf("Server starting on port %s", port)
    if err := router.Run(":" + port); err != nil {
        log.Fatalf("Failed to start server: %v", err)
    }
}

