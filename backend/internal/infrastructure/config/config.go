package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

type Config struct {
	Addr         string
	DataDir      string
	LogLevel     string
	AgentDebug   bool
	DatabasePath string
	OpenAI       OpenAIConfig
	CozeLoop     CozeLoopConfig
}

// OpenAIConfig 仅包含进程本地的模型配置。APIKey 必须来自环境变量（直接设置或由
// 本地 .env 加载），不得来自前端、SQLite、日志或 HTTP 请求。
type OpenAIConfig struct {
	APIKey  string
	BaseURL string
	Model   string
}

// CozeLoopConfig 包含 CozeLoop 基础 Client 和 Prompt Hub 的进程级配置。
// APIToken 只能来自环境变量或本地 .env，不得进入日志、SQLite 或 HTTP 响应。
type CozeLoopConfig struct {
	Enabled                        bool
	EvaluationEnabled              bool
	EvaluationContentUploadEnabled bool
	APIBaseURL                     string
	ConsentPath                    string
	WorkspaceID                    string
	APIToken                       string
	Environment                    string
	ServiceName                    string
	CaptureContent                 bool
	PromptEnabled                  bool
	PromptKey                      string
	PromptVersion                  string
	PromptLabel                    string
	PromptCacheSize                int
	PromptRefreshInterval          time.Duration
}

func Load() (Config, error) {
	// .env 不存在时不报错：部署环境应通过环境变量或密钥管理器提供密钥。
	// godotenv.Load 不会覆盖进程已经提供的环境变量。
	if err := godotenv.Load(".env"); err != nil && !errors.Is(err, os.ErrNotExist) {
		return Config{}, fmt.Errorf("load local environment file: %w", err)
	}
	addr := envOr("APP_ADDR", "127.0.0.1:8080")
	logLevel := envOr("APP_LOG_LEVEL", "info")
	agentDebug, err := envBool("AGENT_DEBUG", false)
	if err != nil {
		return Config{}, fmt.Errorf("parse AGENT_DEBUG: %w", err)
	}
	cozeLoopEnabled, err := envBool("COZELOOP_ENABLED", false)
	if err != nil {
		return Config{}, fmt.Errorf("parse COZELOOP_ENABLED: %w", err)
	}
	cozeLoopEvaluationEnabled, err := envBool("COZELOOP_EVALUATION_ENABLED", false)
	if err != nil {
		return Config{}, fmt.Errorf("parse COZELOOP_EVALUATION_ENABLED: %w", err)
	}
	cozeLoopEvaluationContentUploadEnabled, err := envBool("COZELOOP_EVALUATION_CONTENT_UPLOAD_ENABLED", false)
	if err != nil {
		return Config{}, fmt.Errorf("parse COZELOOP_EVALUATION_CONTENT_UPLOAD_ENABLED: %w", err)
	}
	cozeLoopCaptureContent, err := envBool("COZELOOP_CAPTURE_CONTENT", false)
	if err != nil {
		return Config{}, fmt.Errorf("parse COZELOOP_CAPTURE_CONTENT: %w", err)
	}
	cozeLoopPromptEnabled, err := envBool("COZELOOP_PROMPT_ENABLED", false)
	if err != nil {
		return Config{}, fmt.Errorf("parse COZELOOP_PROMPT_ENABLED: %w", err)
	}
	promptCacheSize, err := envInt("COZELOOP_PROMPT_CACHE_SIZE", 100)
	if err != nil {
		return Config{}, fmt.Errorf("parse COZELOOP_PROMPT_CACHE_SIZE: %w", err)
	}
	promptRefreshInterval, err := envDuration("COZELOOP_PROMPT_REFRESH_INTERVAL", 10*time.Minute)
	if err != nil {
		return Config{}, fmt.Errorf("parse COZELOOP_PROMPT_REFRESH_INTERVAL: %w", err)
	}
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
		AgentDebug:   agentDebug,
		DatabasePath: filepath.Join(dataDir, "data", "app.db"),
		OpenAI: OpenAIConfig{
			APIKey:  os.Getenv("OPENAI_API_KEY"),
			BaseURL: os.Getenv("OPENAI_BASE_URL"),
			Model:   os.Getenv("OPENAI_MODEL"),
		},
		CozeLoop: CozeLoopConfig{
			Enabled:                        cozeLoopEnabled,
			EvaluationEnabled:              cozeLoopEvaluationEnabled,
			EvaluationContentUploadEnabled: cozeLoopEvaluationContentUploadEnabled,
			APIBaseURL:                     envTrimmedOr("COZELOOP_API_BASE_URL", "https://api.coze.cn"),
			ConsentPath:                    filepath.Join(dataDir, "settings", "cozeloop-content-consent.json"),
			WorkspaceID:                    strings.TrimSpace(os.Getenv("COZELOOP_WORKSPACE_ID")),
			APIToken:                       strings.TrimSpace(os.Getenv("COZELOOP_API_TOKEN")),
			Environment:                    envTrimmedOr("COZELOOP_ENVIRONMENT", "local"),
			ServiceName:                    envTrimmedOr("COZELOOP_SERVICE_NAME", "interview-memory-agent"),
			CaptureContent:                 cozeLoopCaptureContent,
			PromptEnabled:                  cozeLoopPromptEnabled,
			PromptKey:                      envTrimmedOr("AGENT_PROMPT_KEY", "interview-review-agent"),
			PromptVersion:                  strings.TrimSpace(os.Getenv("AGENT_PROMPT_VERSION")),
			PromptLabel:                    envTrimmedOr("AGENT_PROMPT_LABEL", "development"),
			PromptCacheSize:                promptCacheSize,
			PromptRefreshInterval:          promptRefreshInterval,
		},
	}, nil
}

// ValidateForCozeLoop 由应用组装根在启用 CozeLoop 时调用。关闭 CozeLoop 时，
// 即使没有平台凭据也不应影响本地服务启动。
func (c Config) ValidateForCozeLoop() error {
	return c.CozeLoop.Validate()
}

// Validate 校验启用 CozeLoop Client 所需的最小配置。错误只包含配置字段名，
// 不包含 APIToken 的值。
func (c CozeLoopConfig) Validate() error {
	if c.CaptureContent && !c.Enabled {
		return errors.New("COZELOOP_CAPTURE_CONTENT requires COZELOOP_ENABLED")
	}
	if (c.EvaluationEnabled || c.EvaluationContentUploadEnabled) && !c.Enabled {
		return errors.New("CozeLoop evaluation requires COZELOOP_ENABLED")
	}
	if c.EvaluationContentUploadEnabled && !c.EvaluationEnabled {
		return errors.New("COZELOOP_EVALUATION_CONTENT_UPLOAD_ENABLED requires COZELOOP_EVALUATION_ENABLED")
	}
	if c.PromptEnabled && !c.Enabled {
		return errors.New("COZELOOP_PROMPT_ENABLED requires COZELOOP_ENABLED")
	}
	if c.PromptEnabled {
		if strings.TrimSpace(c.PromptKey) == "" {
			return errors.New("AGENT_PROMPT_KEY is required when CozeLoop Prompt Hub is enabled")
		}
		if c.PromptCacheSize < 1 {
			return errors.New("COZELOOP_PROMPT_CACHE_SIZE must be greater than zero")
		}
		if c.PromptRefreshInterval <= 0 {
			return errors.New("COZELOOP_PROMPT_REFRESH_INTERVAL must be greater than zero")
		}
	}
	if c.Enabled || c.EvaluationEnabled {
		apiBaseURL := strings.TrimSpace(c.APIBaseURL)
		if apiBaseURL == "" {
			apiBaseURL = "https://api.coze.cn"
		}
		parsed, err := url.Parse(apiBaseURL)
		if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
			return errors.New("COZELOOP_API_BASE_URL must be an http(s) URL without credentials, query, or fragment")
		}
		if parsed.Scheme != "https" && parsed.Hostname() != "localhost" && parsed.Hostname() != "127.0.0.1" && parsed.Hostname() != "::1" {
			return errors.New("COZELOOP_API_BASE_URL must use HTTPS except for loopback hosts")
		}
	}
	if !c.Enabled {
		return nil
	}
	missing := make([]string, 0, 2)
	if strings.TrimSpace(c.WorkspaceID) == "" {
		missing = append(missing, "COZELOOP_WORKSPACE_ID")
	}
	if strings.TrimSpace(c.APIToken) == "" {
		missing = append(missing, "COZELOOP_API_TOKEN")
	}
	if len(missing) > 0 {
		return fmt.Errorf("CozeLoop is enabled; required configuration is missing: %s", strings.Join(missing, ", "))
	}
	return nil
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

func envTrimmedOr(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func envBool(key string, fallback bool) (bool, error) {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf("%s must be a boolean: %w", key, err)
	}
	return parsed, nil
}

func envInt(key string, fallback int) (int, error) {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer: %w", key, err)
	}
	return parsed, nil
}

func envDuration(key string, fallback time.Duration) (time.Duration, error) {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback, nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("%s must be a duration: %w", key, err)
	}
	return parsed, nil
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
