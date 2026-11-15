// Package config loads the configuration of the api and seed commands from environment
// variables.
//
// The process environment is the only source: the binaries never read .env files.
// Loading fails fast and reports every missing or invalid variable at once.
package config

import (
	"fmt"
	"io"
	"log/slog"
	"strings"

	"github.com/caarlos0/env/v11"
)

// Config is the runtime configuration shared by the api and seed commands.
type Config struct {
	// HTTPAddr is the address the API's HTTP server listens on.
	HTTPAddr string `env:"HTTP_ADDR" envDefault:":8080"`
	// DatabaseURL is the PostgreSQL connection string. It holds credentials and is never logged.
	DatabaseURL string `env:"DATABASE_URL,required,notEmpty"`
	// LogLevel is the minimum level logged: debug, info, warn or error.
	LogLevel slog.Level `env:"LOG_LEVEL" envDefault:"info"`
	// LogFormat selects JSON logs (production) or human-readable text (local development).
	LogFormat LogFormat `env:"LOG_FORMAT" envDefault:"json"`
	// LLMProvider selects the AI assistant's model: the offline mock, or Claude. Claude
	// without an API key falls back to the mock: see AssistantProvider.
	LLMProvider LLMProvider `env:"LLM_PROVIDER" envDefault:"mock"`
	// AnthropicAPIKey authenticates Claude API requests. It is a secret and is never logged.
	AnthropicAPIKey string `env:"ANTHROPIC_API_KEY"`
	// AnthropicModel is the Claude model the assistant uses.
	AnthropicModel string `env:"ANTHROPIC_MODEL" envDefault:"claude-haiku-5-5"`
}

// LLMProvider is the AI assistant's language model.
type LLMProvider string

// Supported LLM providers.
const (
	LLMProviderMock   LLMProvider = "mock"   // scripted and offline, for development and tests
	LLMProviderClaude LLMProvider = "claude" // the Anthropic API; needs ANTHROPIC_API_KEY
)

// UnmarshalText implements encoding.TextUnmarshaler and rejects unknown providers.
func (p *LLMProvider) UnmarshalText(text []byte) error {
	switch v := LLMProvider(strings.ToLower(string(text))); v {
	case LLMProviderMock, LLMProviderClaude:
		*p = v
		return nil
	default:
		return fmt.Errorf("unknown LLM provider %q: want %q or %q", text, LLMProviderMock, LLMProviderClaude)
	}
}

// LogFormat is the log output encoding.
type LogFormat string

// Supported log formats.
const (
	LogFormatJSON LogFormat = "json"
	LogFormatText LogFormat = "text"
)

// UnmarshalText implements encoding.TextUnmarshaler and rejects unknown formats.
func (f *LogFormat) UnmarshalText(text []byte) error {
	switch v := LogFormat(strings.ToLower(string(text))); v {
	case LogFormatJSON, LogFormatText:
		*f = v
		return nil
	default:
		return fmt.Errorf("unknown log format %q: want %q or %q", text, LogFormatJSON, LogFormatText)
	}
}

// Load parses the configuration from environ, given in os.Environ's "KEY=value" form.
// Only environ is consulted, which keeps tests independent of the process environment.
func Load(environ []string) (Config, error) {
	cfg, err := env.ParseAsWithOptions[Config](env.Options{Environment: env.ToMap(environ)})
	if err != nil {
		return Config{}, fmt.Errorf("load config: %w", err)
	}
	return cfg, nil
}

// AssistantProvider returns the provider the assistant actually uses: the configured one,
// except that Claude without an API key falls back to the offline mock.
func (c Config) AssistantProvider() LLMProvider {
	if c.LLMProvider == LLMProviderClaude && strings.TrimSpace(c.AnthropicAPIKey) == "" {
		return LLMProviderMock
	}
	return c.LLMProvider
}

// NewLogger returns a logger that writes to w in the configured format, from the configured
// level up.
func (c Config) NewLogger(w io.Writer) *slog.Logger {
	opts := &slog.HandlerOptions{Level: c.LogLevel}
	if c.LogFormat == LogFormatText {
		return slog.New(slog.NewTextHandler(w, opts))
	}
	return slog.New(slog.NewJSONHandler(w, opts))
}

// LogValue implements slog.LogValuer, so the configuration can be logged without leaking
// the database credentials or the API key.
func (c Config) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("http_addr", c.HTTPAddr),
		slog.String("log_level", c.LogLevel.String()),
		slog.String("log_format", string(c.LogFormat)),
		slog.String("llm_provider", string(c.AssistantProvider())),
		slog.String("anthropic_model", c.AnthropicModel),
	)
}
