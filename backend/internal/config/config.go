package config

import (
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

// Config holds all application configuration.
type Config struct {
	Server    ServerConfig
	Database  DatabaseConfig
	Redis     RedisConfig
	JWT       JWTConfig
	Admin     AdminConfig
	WS        WSConfig
	RateLimit RateLimitConfig
	DeepSeek  DeepSeekConfig
	Dashscope DashscopeConfig
	IMA       IMAConfig
	Zhihu     ZhihuConfig
	Tavily    TavilyConfig
	Tencent   TencentConfig
Weibo     WeiboConfig
WebAuthn  WebAuthnConfig
Jiaozhen  JiaozhenConfig
Log       LogConfig
}

type WebAuthnConfig struct {
RPID     string
RPName   string
RPOrigin string
}

type JiaozhenConfig struct {
Enabled     bool
CLIPath     string
CommandArgs []string
APIKey      string
Timeout     time.Duration
MaxClaims   int
}

type ServerConfig struct {
	Host         string
	Port         int
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
}

type DatabaseConfig struct {
	URL          string
	MaxOpenConns int
	MaxIdleConns int
}

type RedisConfig struct {
	URL     string
	Enabled bool
}

type JWTConfig struct {
	Secret string
	Expiry time.Duration
}

type AdminConfig struct {
Token         string
EncryptionKey string // 32-byte key for AES-256 API key encryption
}

type WSConfig struct {
	AuthEnabled bool
}

type RateLimitConfig struct {
	Enabled  bool
	Requests int
	Window   time.Duration
}

type DeepSeekConfig struct {
	BaseURL      string
	APIKey       string
	DefaultModel string
	Timeout      time.Duration
	MaxTokens    int
	Temperature  float64
}

type DashscopeConfig struct {
	APIKey    string
	Model     string
	Dimension int
}

type IMAConfig struct {
	BaseURL   string
	ClientID  string
	APIKey    string
	KBID      string
	Timeout   time.Duration
}

type ZhihuConfig struct {
	Enabled      bool
	BaseURL      string
	AccessSecret string
	Timeout      time.Duration
}

type TavilyConfig struct {
	APIKey   string
	Endpoint string
	Timeout  time.Duration
}

type TencentConfig struct {
	Enabled bool
	BaseURL string
	Timeout time.Duration
}

type WeiboConfig struct {
	Enabled bool
	BaseURL string
	Timeout time.Duration
}

type LogConfig struct {
	Level  string
	Format string
}

// Load reads configuration from environment variables with sensible defaults.
func Load() *Config {
	// Load .env file if present (non-fatal if missing)
	_ = godotenv.Load()
	return &Config{
		Server: ServerConfig{
			Host:         getEnv("SERVER_HOST", "0.0.0.0"),
			Port:         getEnvInt("SERVER_PORT", 8080),
			ReadTimeout:  getEnvDuration("SERVER_READ_TIMEOUT", 30*time.Second),
			WriteTimeout: getEnvDuration("SERVER_WRITE_TIMEOUT", 120*time.Second),
		},
		Database: DatabaseConfig{
			URL:          getEnv("DATABASE_URL", "postgres://postgres:postgres@localhost:5432/writing_agent_v2?sslmode=disable"),
			MaxOpenConns: getEnvInt("DB_MAX_OPEN_CONNS", 25),
			MaxIdleConns: getEnvInt("DB_MAX_IDLE_CONNS", 5),
		},
		Redis: RedisConfig{
			URL:     getEnv("REDIS_URL", "redis://localhost:6379/0"),
			Enabled: getEnvBool("REDIS_ENABLED", false),
		},
		JWT: JWTConfig{
			Secret: getEnv("JWT_SECRET", "dev-secret-change-in-production"),
			Expiry: getEnvDuration("JWT_EXPIRY", 24*time.Hour),
		},
Admin: AdminConfig{
Token: getEnv("ADMIN_TOKEN", "dev-admin-token"),
EncryptionKey: getEnv("API_KEY_ENCRYPTION_KEY", ""),
},
		WS: WSConfig{
			AuthEnabled: getEnvBool("WS_AUTH_ENABLED", false),
		},
		RateLimit: RateLimitConfig{
			Enabled:  getEnvBool("RATE_LIMIT_ENABLED", true),
			Requests: getEnvInt("RATE_LIMIT_REQUESTS", 120),
			Window:   getEnvDuration("RATE_LIMIT_WINDOW", time.Minute),
		},
		DeepSeek: DeepSeekConfig{
			BaseURL:      getEnv("DEEPSEEK_BASE_URL", "https://api.deepseek.com"),
			APIKey:       getEnv("AI_API_KEY", ""),
			DefaultModel: getEnv("DEEPSEEK_DEFAULT_MODEL", "deepseek-v4-flash"),
			Timeout:      getEnvDuration("DEEPSEEK_TIMEOUT", 120*time.Second),
			MaxTokens:    getEnvInt("DEEPSEEK_MAX_TOKENS", 8192),
			Temperature:  getEnvFloat("DEEPSEEK_TEMPERATURE", 0.7),
		},
		Dashscope: DashscopeConfig{
			APIKey:    getEnv("DASHSCOPE_API_KEY", ""),
			Model:     getEnv("DASHSCOPE_MODEL", "text-embedding-v3"),
			Dimension: getEnvInt("DASHSCOPE_DIMENSION", 1024),
		},
		IMA: IMAConfig{
			BaseURL:  getEnv("IMA_BASE_URL", "https://ima.qq.com"),
			ClientID: getEnv("IMA_CLIENT_ID", ""),
			APIKey:   getEnv("IMA_API_KEY", ""),
			KBID:     getEnv("IMA_KB_ID", ""),
			Timeout:  getEnvDuration("IMA_TIMEOUT", 15*time.Second),
		},
		Zhihu: ZhihuConfig{
			Enabled:      getEnvBool("ZHIHU_ENABLED", false),
			BaseURL:      getEnv("ZHIHU_BASE_URL", "https://developer.zhihu.com"),
			AccessSecret: getEnv("ZHIHU_ACCESS_SECRET", ""),
			Timeout:      getEnvDuration("ZHIHU_TIMEOUT", 15*time.Second),
		},
		Tavily: TavilyConfig{
			APIKey:   getEnv("TAVILY_API_KEY", ""),
			Endpoint: getEnv("TAVILY_ENDPOINT", "https://api.tavily.com/search"),
			Timeout:  getEnvDuration("TAVILY_TIMEOUT", 20*time.Second),
		},
		Tencent: TencentConfig{
			Enabled: getEnvBool("TENCENT_ENABLED", false),
			BaseURL: getEnv("TENCENT_BASE_URL", "https://r.inews.qq.com"),
			Timeout: getEnvDuration("TENCENT_TIMEOUT", 15*time.Second),
		},
		Weibo: WeiboConfig{
			Enabled: getEnvBool("WEIBO_ENABLED", false),
			BaseURL: getEnv("WEIBO_BASE_URL", "https://weibo.com/ajax"),
			Timeout: getEnvDuration("WEIBO_TIMEOUT", 15*time.Second),
		},
WebAuthn: WebAuthnConfig{
RPID:     getEnv("WEBAUTHN_RP_ID", "localhost"),
RPName:   getEnv("WEBAUTHN_RP_NAME", "笔润智谈"),
RPOrigin: getEnv("WEBAUTHN_RP_ORIGIN", "http://localhost:5173"),
},
Jiaozhen: JiaozhenConfig{
Enabled:     getEnvBool("JIAOZHEN_ENABLED", false),
CLIPath:     getEnv("JIAOZHEN_CLI_PATH", ""),
CommandArgs: splitArgs(getEnv("JIAOZHEN_COMMAND_ARGS", "jiaozhen")),
APIKey:      getEnv("JIAOZHEN_API_KEY", ""),
Timeout:     getEnvDuration("JIAOZHEN_TIMEOUT", 15*time.Second),
MaxClaims:   getEnvInt("JIAOZHEN_MAX_CLAIMS", 2),
},
		Log: LogConfig{
			Level:  getEnv("LOG_LEVEL", "info"),
			Format: getEnv("LOG_FORMAT", "json"),
		},
	}
}

func (c *Config) LogLevel() slog.Level {
	switch strings.ToLower(c.Log.Level) {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

func (c *Config) ListenAddr() string {
	return fmt.Sprintf("%s:%d", c.Server.Host, c.Server.Port)
}

// Helper functions

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return fallback
}

func getEnvBool(key string, fallback bool) bool {
	if v := os.Getenv(key); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return fallback
}

func splitArgs(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Fields(s)
}

func getEnvFloat(key string, fallback float64) float64 {
	if v := os.Getenv(key); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return fallback
}

func getEnvDuration(key string, fallback time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return fallback
}
