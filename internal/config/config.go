package config

import (
	"fmt"
	"os"
	"strconv"
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

	// Integer fields — os.Getenv returns strings so we parse these
	// explicitly. Load() returns an error if they are missing or not
	// valid integers so the server fails at startup rather than running
	// with a zero value silently.
	ProcessorWorkers  int
	ProcessorPollSecs int
}

// Load reads all configuration from environment variables.
// Returns an error if any required value is missing or invalid —
// the server should not start with incomplete configuration.
func Load() (*Config, error) {
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

// requireEnv reads an environment variable that must be set.
// Returns the value — missing required strings are caught in Load()
// via the error return so the server fails at startup.
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