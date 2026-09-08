package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/joho/godotenv"
)

type Config struct {
	Addr         string
	DataDir      string
	LogLevel     string
	DatabasePath string
	OpenAI       OpenAIConfig
}

// OpenAIConfig 仅包含进程本地的模型配置。APIKey 必须来自环境变量（直接设置或由
// 本地 .env 加载），不得来自前端、SQLite、日志或 HTTP 请求。
type OpenAIConfig struct {
	APIKey  string
	BaseURL string
	Model   string
}

func Load() (Config, error) {
	// .env 不存在时不报错：部署环境应通过环境变量或密钥管理器提供密钥。
	// godotenv.Load 不会覆盖进程已经提供的环境变量。
	if err := godotenv.Load(".env"); err != nil && !errors.Is(err, os.ErrNotExist) {
		return Config{}, fmt.Errorf("load local environment file: %w", err)
	}
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
	return Config{
		Addr:         addr,
		DataDir:      dataDir,
		LogLevel:     logLevel,
		DatabasePath: filepath.Join(dataDir, "data", "app.db"),
		OpenAI: OpenAIConfig{
			APIKey:  os.Getenv("OPENAI_API_KEY"),
			BaseURL: os.Getenv("OPENAI_BASE_URL"),
			Model:   os.Getenv("OPENAI_MODEL"),
		},
	}, nil
}

// ValidateForChat 由应用组装根在启用 Chat 运行时时调用。将它与 Load 分开，
// 可以让未接入用户 Agent 实现的非 AI CRUD 服务照常启动。
func (c Config) ValidateForChat() error {
	if strings.TrimSpace(c.OpenAI.APIKey) == "" {
		return errors.New("OPENAI_API_KEY is required when chat is enabled")
	}
	if strings.TrimSpace(c.OpenAI.Model) == "" {
		return errors.New("OPENAI_MODEL is required when chat is enabled")
	}
	return nil
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
