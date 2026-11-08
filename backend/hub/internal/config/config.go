package config

import (
	"fmt"
	"os"
	"strings"
	"time"
)

// Config holds all hub runtime configuration.
// Loaded from env vars (12-factor) with sensible defaults for dev.
type Config struct {
	// gRPC server (receives entries from sniffers)
	GRPCPort int `env:"GRPC_PORT" default:"9090"`

	// socket.io server (relays enriched entries to the dashboard UI)
	WSPort int `env:"WS_PORT" default:"3030"`

	// Prometheus metrics port
	MetricsPort int `env:"METRICS_PORT" default:"49100"`

	// K8s in-cluster config — when true, uses ServiceAccount token
	InCluster bool `env:"IN_CLUSTER" default:"true"`

	// BoltDB persistence
	StoragePath string `env:"STORAGE_PATH" default:"/var/lib/bullseye/hub.db"`
	// Retention: entries older than this are deleted on a sweep interval
	RetentionTTL time.Duration `env:"RETENTION_TTL" default:"24h"`
	// Max entries to keep in memory ring buffer (sent to UI on connect)
	InMemoryBuffer int `env:"IN_MEMORY_BUFFER" default:"5000"`

	// PII redaction
	RedactEnabled bool `env:"REDACT_ENABLED" default:"true"`
	// Comma-separated list of header names to redact (case-insensitive)
	RedactHeaders string `env:"REDACT_HEADERS" default:"authorization,cookie,set-cookie,x-api-key,x-auth-token,proxy-authorization"`
	// Body regex redaction rules (comma-separated Go regex)
	RedactBodyPatterns string `env:"REDACT_BODY_PATTERNS" default:""`

	// OIDC auth (optional — disabled in dev)
	OIDCIssuer string `env:"OIDC_ISSUER" default:""`
	OIDCClientID string `env:"OIDC_CLIENT_ID" default:""`
	OIDCClientSecret string `env:"OIDC_CLIENT_SECRET" default:""`

	// Logging
	LogLevel string `env:"LOG_LEVEL" default:"info"`
}

// Load reads configuration from environment variables.
func Load() (*Config, error) {
	c := &Config{
		GRPCPort:           getenvInt("GRPC_PORT", 9090),
		WSPort:             getenvInt("WS_PORT", 3030),
		MetricsPort:        getenvInt("METRICS_PORT", 49100),
		InCluster:          getenvBool("IN_CLUSTER", true),
		StoragePath:        getenvStr("STORAGE_PATH", "/var/lib/bullseye/hub.db"),
		RetentionTTL:       getenvDuration("RETENTION_TTL", 24*time.Hour),
		InMemoryBuffer:     getenvInt("IN_MEMORY_BUFFER", 5000),
		RedactEnabled:      getenvBool("REDACT_ENABLED", true),
		RedactHeaders:      getenvStr("REDACT_HEADERS", "authorization,cookie,set-cookie,x-api-key,x-auth-token,proxy-authorization"),
		RedactBodyPatterns: getenvStr("REDACT_BODY_PATTERNS", ""),
		OIDCIssuer:         getenvStr("OIDC_ISSUER", ""),
		OIDCClientID:       getenvStr("OIDC_CLIENT_ID", ""),
		OIDCClientSecret:   getenvStr("OIDC_CLIENT_SECRET", ""),
		LogLevel:           getenvStr("LOG_LEVEL", "info"),
	}

	if err := c.validate(); err != nil {
		return nil, fmt.Errorf("config validation: %w", err)
	}
	return c, nil
}

func (c *Config) validate() error {
	if c.GRPCPort < 1 || c.GRPCPort > 65535 {
		return fmt.Errorf("invalid GRPC_PORT: %d", c.GRPCPort)
	}
	if c.WSPort < 1 || c.WSPort > 65535 {
		return fmt.Errorf("invalid WS_PORT: %d", c.WSPort)
	}
	if c.GRPCPort == c.WSPort {
		return fmt.Errorf("GRPC_PORT and WS_PORT must differ")
	}
	if c.InMemoryBuffer < 100 {
		return fmt.Errorf("IN_MEMORY_BUFFER must be >= 100")
	}
	if c.RetentionTTL < 1*time.Minute {
		return fmt.Errorf("RETENTION_TTL must be >= 1m")
	}
	return nil
}

// RedactHeaderSet returns the redacted headers as a set (lowercased).
func (c *Config) RedactHeaderSet() map[string]struct{} {
	set := make(map[string]struct{})
	for _, h := range strings.Split(c.RedactHeaders, ",") {
		h = strings.TrimSpace(strings.ToLower(h))
		if h != "" {
			set[h] = struct{}{}
		}
	}
	return set
}

func getenvStr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func getenvInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		var n int
		if _, err := fmt.Sscanf(v, "%d", &n); err == nil {
			return n
		}
	}
	return def
}

func getenvBool(key string, def bool) bool {
	if v := os.Getenv(key); v != "" {
		switch strings.ToLower(v) {
		case "true", "1", "yes":
			return true
		case "false", "0", "no":
			return false
		}
	}
	return def
}

func getenvDuration(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}
