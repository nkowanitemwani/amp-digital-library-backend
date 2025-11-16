package services


import (
    "context"
    "fmt"

    "github.com/aws/aws-sdk-go-v2/aws"
    "github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
    "github.com/aws/aws-sdk-go-v2/service/dynamodb"
    "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
    "github.com/nkowanitemwani/amp-digital-library-backend/models"
)

type DynamoDBService struct {
    client    *dynamodb.Client
    tableName string
}

func NewDynamoDBService(client *dynamodb.Client, tableName string) *DynamoDBService {
    return &DynamoDBService{
        client:    client,
        tableName: tableName,
    }
}

func (d *DynamoDBService) SaveFileMetadata(ctx context.Context, metadata *models.FileMetaData) error {
    item, err := attributevalue.MarshalMap(metadata)
    if err != nil {
        return fmt.Errorf("failed to marshal metadata: %w", err)
    }

    _, err = d.client.PutItem(ctx, &dynamodb.PutItemInput{
        TableName: aws.String(d.tableName),
        Item:      item,
    })

    return err
}

func (d *DynamoDBService) GetFileMetadata(ctx context.Context, fileID string) (*models.FileMetaData, error) {
    result, err := d.client.GetItem(ctx, &dynamodb.GetItemInput{
        TableName: aws.String(d.tableName),
        Key: map[string]types.AttributeValue{
            "FileID": &types.AttributeValueMemberS{Value: fileID},
        },
    })

    if err != nil {
        return nil, err
    }

    if result.Item == nil {
        return nil, fmt.Errorf("file not found")
    }

    var metadata models.FileMetaData
    err = attributevalue.UnmarshalMap(result.Item, &metadata)
    if err != nil {
        return nil, fmt.Errorf("failed to unmarshal metadata: %w", err)
    }

    return &metadata, nil
}