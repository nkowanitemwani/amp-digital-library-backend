package services

import (
    "bytes"
    "context"
    "fmt"
    "io"

    "github.com/aws/aws-sdk-go-v2/aws"
    "github.com/aws/aws-sdk-go-v2/service/s3"
)

type S3Service struct {
    client     *s3.Client
    bucketName string
    region     string
}

func NewS3Service(client *s3.Client, bucketName, region string) *S3Service {
    return &S3Service{
        client:     client,
        bucketName: bucketName,
        region:     region,
    }
}

func (s *S3Service) UploadFile(ctx context.Context, key string, data []byte, contentType string) error {
    _, err := s.client.PutObject(ctx, &s3.PutObjectInput{
        Bucket:      aws.String(s.bucketName),
        Key:         aws.String(key),
        Body:        bytes.NewReader(data),
        ContentType: aws.String(contentType),
        ACL:         "public-read", // Make object publicly readable
    })
    return err
}

func (s *S3Service) GetFile(ctx context.Context, key string) ([]byte, error) {
    result, err := s.client.GetObject(ctx, &s3.GetObjectInput{
        Bucket: aws.String(s.bucketName),
        Key:    aws.String(key),
    })
    if err != nil {
        return nil, err
    }
    defer result.Body.Close()

    return io.ReadAll(result.Body)
}

// Return direct public URL instead of presigned URL
func (s *S3Service) GetPublicURL(key string) string {
    return fmt.Sprintf("https://%s.s3.%s.amazonaws.com/%s", s.bucketName, s.region, key)
}

// Keep the presigned URL method for backward compatibility
func (s *S3Service) GetPresignedURL(ctx context.Context, key string) (string, error) {
    presignClient := s3.NewPresignClient(s.client)
    
    presignResult, err := presignClient.PresignGetObject(ctx, &s3.GetObjectInput{
        Bucket: aws.String(s.bucketName),
        Key:    aws.String(key),
    })
    
    if err != nil {
        return "", err
    }
    
    return presignResult.URL, nil
}