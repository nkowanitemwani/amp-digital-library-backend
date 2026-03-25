package config

import "os"

type Config struct {
	JWTSecret      string
	AWSRegion      string
	AWSAccessKeyId string
	S3Bucket       string
	ServerPort     string
	DBHost         string
	DBPort         string
	DBUser         string
	DBPassword     string
	DBName         string
}

func Load() *Config {
	return &Config{
		JWTSecret:      os.Getenv("JWT_SECRET"),
		AWSRegion:      os.Getenv("AWS_REGION"),
		AWSAccessKeyId: os.Getenv("AWS_ACCESS_KEY_ID"),
		S3Bucket:       os.Getenv("S3_BUCKET"),
		ServerPort:     os.Getenv("SERVER_PORT"),
		DBHost:         os.Getenv("DB_HOST"),
		DBPort:         os.Getenv("DB_PORT"),
		DBUser:         os.Getenv("DB_USER"),
		DBPassword:     os.Getenv("DB_PASSWORD"),
		DBName:         os.Getenv("DB_NAME"),
	}
}
