package main

import (
	// "bytes"
	// "context"
	// "log"
	// "os"
	"fmt"
	"log"
	"os"

	"github.com/joho/godotenv"
	"github.com/nkowanitemwani/amp-digital-library-backend/internal/config"
	"github.com/nkowanitemwani/amp-digital-library-backend/internal/db"
	// "github.com/aws/aws-sdk-go-v2/aws"
	// "github.com/aws/aws-sdk-go-v2/config"
	// "github.com/aws/aws-sdk-go-v2/service/dynamodb"
	// "github.com/aws/aws-sdk-go-v2/service/polly"
	// "github.com/aws/aws-sdk-go-v2/service/s3"
	// "github.com/gin-contrib/cors"
	// "github.com/gin-gonic/gin"
)

// func testS3Connection(s3Client *s3.Client, bucketName string) {
// 	ctx := context.Background()

// 	log.Printf("Testing S3 connection to bucket: %s", bucketName)

// 	// Test 1: Check if bucket exists
// 	_, err := s3Client.HeadBucket(ctx, &s3.HeadBucketInput{
// 		Bucket: aws.String(bucketName),
// 	})
// 	if err != nil {
// 		log.Fatalf("Cannot access bucket '%s': %v", bucketName, err)
// 	}
// 	log.Printf("Bucket '%s' exists and is accessible", bucketName)

// 	// Test 2: Try to upload a test file
// 	testKey := "test/connection-test.txt"
// 	testData := []byte("Connection test")

// 	_, err = s3Client.PutObject(ctx, &s3.PutObjectInput{
// 		Bucket: aws.String(bucketName),
// 		Key:    aws.String(testKey),
// 		Body:   bytes.NewReader(testData),
// 	})
// 	if err != nil {
// 		log.Fatalf("Cannot upload to bucket: %v", err)
// 	}
// 	log.Printf("Successfully uploaded test file")

// 	_, err = s3Client.DeleteObject(ctx, &s3.DeleteObjectInput{
// 		Bucket: aws.String(bucketName),
// 		Key:    aws.String(testKey),
// 	})
// 	if err != nil {
// 		log.Printf("Warning: Could not delete test file: %v", err)
// 	}

// 	log.Println("S3 connection test passed")
// }

func main() {

	//get enviroment variable from .env file
	godotenv.Load("../../.env")

	//pass down environment variable
	cfg :=config.Load()

	//Initialize Database
	db,err := db.InitializeDatabase(cfg);
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	// // Load AWS configuration
	// cfg, err := config.LoadDefaultConfig(context.Background(),
	// 	config.WithRegion("us-east-1"),
	// )
	// if err != nil {
	// 	log.Fatalf("Failed to load AWS config: %v", err)
	// }

	// // Initialize AWS clients
	// s3Client := s3.NewFromConfig(cfg)
	// pollyClient := polly.NewFromConfig(cfg)
	// dynamoClient := dynamodb.NewFromConfig(cfg)

	// // Get configuration from environment
	// bucketName := os.Getenv("S3_BUCKET_NAME")
	// if bucketName == "" {
	// 	bucketName = "amp-digital-library-bucket"
	// }

	// tableName := os.Getenv("DYNAMODB_TABLE_NAME")
	// if tableName == "" {
	// 	tableName = "amp-digital-library-table"
	// }

	// testS3Connection(s3Client, bucketName)

	// // Initialize services
	// s3Service := services.NewS3Service(s3Client, bucketName, "us-east-1")
	// textractService := services.NewTextractService()
	// pollyService := services.NewPollyService(pollyClient)
	// dynamoService := services.NewDynamoDBService(dynamoClient, tableName)

	// // Initialize handler
	// fileHandler := handlers.NewFileHandler(s3Service, textractService, pollyService, dynamoService)

	// // Set up Gin router
	// router := gin.Default()

	// router.Use(cors.New(cors.Config{
	// 	AllowOrigins:     []string{"*"},
	// 	AllowMethods:     []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
	// 	AllowHeaders:     []string{"Origin", "Content-Type", "Accept", "Authorization"},
	// 	ExposeHeaders:    []string{"Content-Length"},
	// 	AllowCredentials: true,
	// }))

	// // Routes
	// router.POST("/upload", fileHandler.UploadFile)
	// router.GET("/files", fileHandler.GetAllFiles)
	// router.GET("/files/:id", fileHandler.GetFileStatus)
	// router.GET("/files/:id/audio", fileHandler.GetAudioURL)

	// // Start server
	// port := os.Getenv("PORT")
	// if port == "" {
	// 	port = "8080"
	// }

	// log.Printf("Server starting on port %s", port)
	// if err := router.Run(":" + port); err != nil {
	// 	log.Fatalf("Failed to start server: %v", err)
	// }
}
