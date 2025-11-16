package models

import "time"

type FileMetaData struct {
	FileID      string    `json:"file_id" dynamodbav:"FileID"`
	FileName    string    `json:"file_name" dynamodbav:"FileName"`
	PDFPath     string    `json:"pdf_path" dynamodbav:"PDFPath"`
	AudioPath   string    `json:"audio_path" dynamodbav:"AudioPath"`
	Status      string    `json:"status" dynamodbav:"Status"` // "processing", "ready", "failed"
	UploadedAt  time.Time `json:"uploaded_at" dynamodbav:"UploadedAt"`
	ProcessedAt time.Time `json:"processed_at,omitempty" dynamodbav:"ProcessedAt,omitempty"`
}