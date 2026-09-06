package config

import (
	"fmt"
	"os"
	"path/filepath"
)

type Config struct {
	Addr         string
	DataDir      string
	LogLevel     string
	DatabasePath string
}

func Load() (Config, error) {
	addr := envOr("APP_ADDR", "127.0.0.1:8080")
	logLevel := envOr("APP_LOG_LEVEL", "info")
	dataDir, err := dataDir()
	if err != nil {
		return Config{}, err
	}
	dataDir, err = filepath.Abs(dataDir)
	if err != nil {
		return Config{}, fmt.Errorf("resolve data directory: %w", err)
	}
	return Config{Addr: addr, DataDir: dataDir, LogLevel: logLevel, DatabasePath: filepath.Join(dataDir, "data", "app.db")}, nil
}

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func dataDir() (string, error) {
	if value := os.Getenv("APP_DATA_DIR"); value != "" {
		return value, nil
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve user data directory: %w", err)
	}
	return filepath.Join(base, "interview-memory-agent"), nil
}
