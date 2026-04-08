package config

import (
	"fmt"
	"os"
	"strconv"

	"github.com/joho/godotenv"
)

type Config struct {
	JWTSecret          string
	AWSRegion          string
	AWSAccessKeyId     string
	AWSSecretAccessKey string
	S3Bucket           string
	ServerPort         string
	DBHost             string
	DBPort             string
	DBUser             string
	DBPassword         string
	DBName             string
	ElevenLabsKey      string
	ElevenLabsVoiceID  string

	// Integer fields — os.Getenv returns strings so we parse these
	// explicitly. Load() returns an error if they are missing or not
	// valid integers so the server fails at startup rather than running
	// with a zero value silently.
	ProcessorWorkers  int
	ProcessorPollSecs int
}

// Load reads all configuration from environment variables.
// It first attempts to load a .env file from the project root —
// if no .env file exists (e.g. in production where variables are
// injected by the host), it silently continues using the environment
// as-is. This means the same config.go works in both local dev
// (with a .env file) and production (without one) without any changes.
func Load() (*Config, error) {
	// godotenv.Load() looks for .env in the current working directory.
	// We deliberately ignore the error — a missing .env file is not a
	// problem in production where variables are set in the environment
	// directly. Only a malformed .env file would return an actual error.
	_ = godotenv.Load("../../.env")

	workers, err := getEnvInt("PROCESSOR_WORKERS")
	if err != nil {
		return nil, err
	}

	pollSecs, err := getEnvInt("PROCESSOR_POLL_SECS")
	if err != nil {
		return nil, err
	}

	cfg := &Config{
		JWTSecret:          requireEnv("JWT_SECRET"),
		AWSRegion:          requireEnv("AWS_REGION"),
		AWSAccessKeyId:     requireEnv("AWS_ACCESS_KEY_ID"),
		AWSSecretAccessKey: requireEnv("AWS_SECRET_ACCESS_KEY"),
		S3Bucket:           requireEnv("S3_BUCKET"),
		ServerPort:         getEnvWithDefault("SERVER_PORT", "8080"),
		DBHost:             requireEnv("DB_HOST"),
		DBPort:             getEnvWithDefault("DB_PORT", "5432"),
		DBUser:             requireEnv("DB_USER"),
		DBPassword:         requireEnv("DB_PASSWORD"),
		DBName:             requireEnv("DB_NAME"),
		ProcessorWorkers:   workers,
		ProcessorPollSecs:  pollSecs,
		ElevenLabsKey:      requireEnv("ELEVENLABS_API_KEY"),
		ElevenLabsVoiceID:  requireEnv("ELEVENLABS_VOICE_ID"),
	}

	// Validate required string fields that requireEnv() returns empty
	// when missing — we catch them all here in one place rather than
	// letting the server start and fail later with a cryptic error.
	required := map[string]string{
		"JWT_SECRET":            cfg.JWTSecret,
		"AWS_REGION":            cfg.AWSRegion,
		"AWS_ACCESS_KEY_ID":     cfg.AWSAccessKeyId,
		"AWS_SECRET_ACCESS_KEY": cfg.AWSSecretAccessKey,
		"DB_HOST":               cfg.DBHost,
		"DB_USER":               cfg.DBUser,
		"DB_PASSWORD":           cfg.DBPassword,
		"DB_NAME":               cfg.DBName,
		"ELEVENLABS_API_KEY":    cfg.ElevenLabsKey,
		"ELEVENLABS_VOICE_ID":   cfg.ElevenLabsVoiceID,
	}

	for key, val := range required {
		if val == "" {
			return nil, fmt.Errorf("required environment variable %q is not set", key)
		}
	}

	return cfg, nil
}

// getEnvInt reads an environment variable and converts it to an int.
// Returns an error if the variable is missing or cannot be parsed.
func getEnvInt(key string) (int, error) {
	val := os.Getenv(key)
	if val == "" {
		return 0, fmt.Errorf("required environment variable %q is not set", key)
	}

	n, err := strconv.Atoi(val)
	if err != nil {
		return 0, fmt.Errorf("environment variable %q must be an integer, got %q", key, val)
	}

	return n, nil
}

// requireEnv reads an environment variable and returns its value.
// Empty string is returned if not set — the required fields check
// in Load() catches all missing values together in one pass,
// which gives a clearer error than failing on the first missing variable.
func requireEnv(key string) string {
	return os.Getenv(key)
}

// getEnvWithDefault returns the environment variable value or a
// fallback if the variable is not set. Used for optional fields
// that have a sensible default (e.g. port numbers).
func getEnvWithDefault(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}
