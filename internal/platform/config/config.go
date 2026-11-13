// Package config loads the API configuration from environment variables.
//
// The process environment is the only source: the binary never reads .env files.
// Loading fails fast and reports every missing or invalid variable at once.
package config

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/caarlos0/env/v11"
)

// Config is the API's runtime configuration.
type Config struct {
	// HTTPAddr is the address the HTTP server listens on.
	HTTPAddr string `env:"HTTP_ADDR" envDefault:":8080"`
	// DatabaseURL is the PostgreSQL connection string. It holds credentials and is never logged.
	DatabaseURL string `env:"DATABASE_URL,required,notEmpty"`
	// LogLevel is the minimum level logged: debug, info, warn or error.
	LogLevel slog.Level `env:"LOG_LEVEL" envDefault:"info"`
	// LogFormat selects JSON logs (production) or human-readable text (local development).
	LogFormat LogFormat `env:"LOG_FORMAT" envDefault:"json"`
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

// LogValue implements slog.LogValuer, so the configuration can be logged without leaking
// the database credentials.
func (c Config) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("http_addr", c.HTTPAddr),
		slog.String("log_level", c.LogLevel.String()),
		slog.String("log_format", string(c.LogFormat)),
	)
}
