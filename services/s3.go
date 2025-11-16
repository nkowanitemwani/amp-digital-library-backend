package services

import(
	"bytes"
    "context"
    "io"

    "github.com/aws/aws-sdk-go-v2/aws"
    "github.com/aws/aws-sdk-go-v2/service/s3"
)

type S3Service struct {
    client     *s3.Client
    bucketName string
}

func NewS3Service(client *s3.Client, bucketName string) *S3Service {
    return &S3Service{
        client:     client,
        bucketName: bucketName,
    }
}

func (s *S3Service) UploadFile(ctx context.Context, key string, data []byte, contentType string) error {
    _, err := s.client.PutObject(ctx, &s3.PutObjectInput{
        Bucket:      aws.String(s.bucketName),
        Key:         aws.String(key),
        Body:        bytes.NewReader(data),
        ContentType: aws.String(contentType),
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