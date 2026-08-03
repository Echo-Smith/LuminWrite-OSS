package config

import (
	"encoding/json"
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
	Kb        KbInternalConfig
	Zhihu     ZhihuConfig
	Tavily    TavilyConfig
	Tencent   TencentConfig
	Weibo      WeiboConfig
	ExtraHot   ExtraHotConfig
	Bing       BingConfig
	AnySearch  AnySearchConfig
	WebAuthn  WebAuthnConfig
	Jiaozhen  JiaozhenConfig
	Log       LogConfig
	HotTopics HotTopicsConfig
	Agent     AgentConfig
	MCPServers []MCPServerConfig
	MCPServer  InProcessMCPServerConfig
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
	BaseURL            string
	APIKey             string
	DefaultModel       string
	Timeout            time.Duration
	MaxTokens          int
	Temperature        float64
	ResponsesAPIRatio  float64 // A/B test ratio for Responses API (0.0=off, 1.0=full)
}

type DashscopeConfig struct {
	APIKey    string
	BaseURL   string // OpenAI-compatible base URL (e.g. https://xxx.maas.aliyuncs.com/compatible-mode/v1)
	Model     string
	Dimension int
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

type ExtraHotConfig struct {
	Enabled bool
	BaseURL string
	Timeout time.Duration
}

type BingConfig struct {
	Enabled bool
	BaseURL string
	Timeout time.Duration
}

type AnySearchConfig struct {
	APIKey   string
	Endpoint string
	Timeout  time.Duration
}

// KbInternalConfig holds configuration for the internal knowledge base
// (replaces the external WeKnora integration).
type KbInternalConfig struct {
	// Docreader gRPC sidecar address (for PDF/Word/image parsing)
	DocreaderAddr     string
	DocreaderTransport string
	// Chunking configuration
	ChunkSize  int
	ChunkOverlap int
	// Hybrid search weights (BM25 + Dense + GraphRAG)
	BM25Weight   float64
	DenseWeight  float64
	GraphWeight  float64
}

type HotTopicsConfig struct {
	FetchInterval time.Duration
}

type LogConfig struct {
	Level  string
	Format string
}

// AgentConfig controls the agent execution mode and exit mechanisms.
//   - "pipeline" (default): use the fixed []Step pipeline (AgentEngine)
//   - "unified": use the LLM-driven ReAct loop (UnifiedAgent)
type AgentConfig struct {
	Mode                string        // "pipeline" | "unified"
	Timeout             time.Duration // global agent execution timeout (default 5m)
	MaxTokens           int           // token budget per execution (default 300000, 0=unlimited)
	MaxFixAttempts      int           // max review→fix loop iterations (default 2)
	MaxConcurrent       int           // max concurrent agent executions globally (default 10)
	MaxConcurrentPerUser int          // max concurrent per user (default 1)
	ConfirmTimeout      time.Duration // user confirm (await_input) timeout (default 5m)
	CircuitBreakerFails int           // consecutive LLM failures before tripping (default 3)
}

// MCPServerConfig holds configuration for a single MCP server.
type MCPServerConfig struct {
	Name      string   `json:"name"`
	Transport string   `json:"transport"` // "stdio" | "sse"`
	Command   string   `json:"command,omitempty"`
	Args      []string `json:"args,omitempty"`
	Env       []string `json:"env,omitempty"`
	URL       string   `json:"url,omitempty"`
}

// InProcessMCPServerConfig configures the in-process MCP server.
// When enabled, the application exposes its built-in tools (search,
// knowledge base, memory, etc.) via the MCP JSON-RPC protocol,
// allowing external MCP clients to discover and call them.
type InProcessMCPServerConfig struct {
	Enabled  bool   // master switch (default false)
	HTTPAddr string // HTTP listen address for HTTP transport (default ":9090")
	Stdio    bool   // if true, also serve over stdio (for CLI mode)
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
BaseURL:           getEnv("DEEPSEEK_BASE_URL", "https://api.deepseek.com"),
APIKey:            getEnv("AI_API_KEY", ""),
DefaultModel:      getEnv("DEEPSEEK_DEFAULT_MODEL", "deepseek-v4-flash"),
Timeout:           getEnvDuration("DEEPSEEK_TIMEOUT", 120*time.Second),
MaxTokens:         getEnvInt("DEEPSEEK_MAX_TOKENS", 16384),
Temperature:       getEnvFloat("DEEPSEEK_TEMPERATURE", 0.7),
ResponsesAPIRatio: getEnvFloat("DEEPSEEK_RESPONSES_API_RATIO", 0),
},
		Dashscope: DashscopeConfig{
			APIKey:    getEnv("DASHSCOPE_API_KEY", ""),
			BaseURL:   getEnv("DASHSCOPE_BASE_URL", "https://dashscope.aliyuncs.com/compatible-mode/v1"),
			Model:     getEnv("DASHSCOPE_MODEL", "text-embedding-v3"),
			Dimension: getEnvInt("DASHSCOPE_DIMENSION", 1024),
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
		ExtraHot: ExtraHotConfig{
			Enabled: getEnvBool("EXTRA_HOT_ENABLED", true),
			BaseURL: getEnv("EXTRA_HOT_BASE_URL", "https://api.vvhan.com/api/hotlist"),
			Timeout: getEnvDuration("EXTRA_HOT_TIMEOUT", 15*time.Second),
		},
		Bing: BingConfig{
			Enabled: getEnvBool("BING_ENABLED", true),
			BaseURL: getEnv("BING_BASE_URL", "https://cn.bing.com"),
			Timeout: getEnvDuration("BING_TIMEOUT", 15*time.Second),
		},
		AnySearch: AnySearchConfig{
			APIKey:   getEnv("ANYSEARCH_API_KEY", ""),
			Endpoint: getEnv("ANYSEARCH_ENDPOINT", "https://api.anysearch.com/v1/search"),
			Timeout:  getEnvDuration("ANYSEARCH_TIMEOUT", 15*time.Second),
		},
		Kb: KbInternalConfig{
			DocreaderAddr:     getEnv("DOCREADER_ADDR", "docreader:50051"),
			DocreaderTransport: getEnv("DOCREADER_TRANSPORT", "grpc"),
			ChunkSize:         getEnvInt("KB_CHUNK_SIZE", 512),
			ChunkOverlap:      getEnvInt("KB_CHUNK_OVERLAP", 50),
			BM25Weight:       getEnvFloat("KB_BM25_WEIGHT", 0.3),
			DenseWeight:      getEnvFloat("KB_DENSE_WEIGHT", 0.5),
			GraphWeight:      getEnvFloat("KB_GRAPH_WEIGHT", 0.2),
		},
		WebAuthn: WebAuthnConfig{
			RPID:     getEnv("WEBAUTHN_RP_ID", "localhost"),
			RPName:   getEnv("WEBAUTHN_RP_NAME", "笔润智谈"),
			RPOrigin: getEnv("WEBAUTHN_RP_ORIGIN", "http://localhost:5173"),
		},
		Jiaozhen: JiaozhenConfig{
			Enabled:     getEnvBool("JIAOZHEN_ENABLED", true),
			CLIPath:     getEnv("JIAOZHEN_CLI_PATH", getEnv("TENCENT_NEWS_CLI_PATH", "")),
			CommandArgs: splitArgs(getEnv("JIAOZHEN_COMMAND_ARGS", "jiaozhen")),
			APIKey:      getEnv("JIAOZHEN_API_KEY", getEnv("TENCENT_NEWS_API_KEY", "")),
			Timeout:     getEnvDuration("JIAOZHEN_TIMEOUT", 30*time.Second),
			MaxClaims:   getEnvInt("JIAOZHEN_MAX_CLAIMS", 2),
		},
		Log: LogConfig{
			Level:  getEnv("LOG_LEVEL", "info"),
			Format: getEnv("LOG_FORMAT", "json"),
		},
		HotTopics: HotTopicsConfig{
			FetchInterval: getEnvDuration("HOT_TOPICS_FETCH_INTERVAL", 10*time.Minute),
		},
		Agent: AgentConfig{
			Mode:                getEnv("AGENT_MODE", "pipeline"),
			Timeout:             getEnvDuration("AGENT_TIMEOUT", 5*time.Minute),
			MaxTokens:           getEnvInt("AGENT_MAX_TOKENS", 300000),
			MaxFixAttempts:      getEnvInt("AGENT_MAX_FIX_ATTEMPTS", 2),
			MaxConcurrent:       getEnvInt("AGENT_MAX_CONCURRENT", 10),
			MaxConcurrentPerUser: getEnvInt("AGENT_MAX_CONCURRENT_PER_USER", 3),
			ConfirmTimeout:      getEnvDuration("AGENT_CONFIRM_TIMEOUT", 5*time.Minute),
			CircuitBreakerFails: getEnvInt("AGENT_CIRCUIT_BREAKER_FAILS", 3),
		},
		MCPServers: loadMCPServers(),
		MCPServer: InProcessMCPServerConfig{
			Enabled:  getEnvBool("MCP_SERVER_ENABLED", false),
			HTTPAddr: getEnv("MCP_SERVER_HTTP_ADDR", ":9090"),
			Stdio:    getEnvBool("MCP_SERVER_STDIO", false),
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

// loadMCPServers loads MCP server configurations from the MCP_SERVERS env var.
// The env var should contain a JSON array of MCPServerConfig objects.
// Example: [{"name":"fs","transport":"stdio","command":"npx","args":["-y","@anthropic/mcp-filesystem"]}]
func loadMCPServers() []MCPServerConfig {
	raw := os.Getenv("MCP_SERVERS")
	if raw == "" {
		return nil
	}
	var servers []MCPServerConfig
	if err := json.Unmarshal([]byte(raw), &servers); err != nil {
		slog.Warn("failed to parse MCP_SERVERS env var", "error", err)
		return nil
	}
	return servers
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
