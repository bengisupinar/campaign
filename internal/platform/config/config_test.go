package config_test

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/bengisupinar/campaign/internal/platform/config"
)

const testDSN = "postgres://campaign:s3cret@localhost:5432/campaign?sslmode=disable"

func TestLoad(t *testing.T) {
	tests := []struct {
		name     string
		environ  []string
		want     config.Config
		wantErrs []string // substrings the error must contain; empty means success
	}{
		{
			name:    "defaults",
			environ: []string{"DATABASE_URL=" + testDSN},
			want: config.Config{
				HTTPAddr:    ":8080",
				DatabaseURL: testDSN,
				LogLevel:    slog.LevelInfo,
				LogFormat:   config.LogFormatJSON,
			},
		},
		{
			name: "overrides",
			environ: []string{
				"DATABASE_URL=" + testDSN,
				"HTTP_ADDR=127.0.0.1:9000",
				"LOG_LEVEL=debug",
				"LOG_FORMAT=TEXT",
			},
			want: config.Config{
				HTTPAddr:    "127.0.0.1:9000",
				DatabaseURL: testDSN,
				LogLevel:    slog.LevelDebug,
				LogFormat:   config.LogFormatText,
			},
		},
		{
			name:     "missing database url",
			environ:  nil,
			wantErrs: []string{"DATABASE_URL"},
		},
		{
			name:     "empty database url",
			environ:  []string{"DATABASE_URL="},
			wantErrs: []string{"DATABASE_URL"},
		},
		{
			name:     "invalid log level",
			environ:  []string{"DATABASE_URL=" + testDSN, "LOG_LEVEL=verbose"},
			wantErrs: []string{"verbose"},
		},
		{
			name:     "invalid log format",
			environ:  []string{"DATABASE_URL=" + testDSN, "LOG_FORMAT=xml"},
			wantErrs: []string{"xml"},
		},
		{
			name:     "reports every error at once",
			environ:  []string{"LOG_FORMAT=xml"},
			wantErrs: []string{"DATABASE_URL", "xml"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := config.Load(tt.environ)

			if len(tt.wantErrs) > 0 {
				if err == nil {
					t.Fatalf("Load() = %+v, want an error", got)
				}
				for _, want := range tt.wantErrs {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("Load() error = %q, want it to mention %q", err, want)
					}
				}
				return
			}
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("Load() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestLoadIgnoresProcessEnvironment(t *testing.T) {
	t.Setenv("DATABASE_URL", testDSN)

	if _, err := config.Load(nil); err == nil {
		t.Fatal("Load(nil) succeeded using the process environment; want it to read only its argument")
	}
}

func TestConfigLogValueRedactsDatabaseURL(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	logger.Info("starting", slog.Any("config", config.Config{
		HTTPAddr:    ":8080",
		DatabaseURL: testDSN,
		LogFormat:   config.LogFormatJSON,
	}))

	if out := buf.String(); strings.Contains(out, "s3cret") || strings.Contains(out, "postgres://") {
		t.Fatalf("logged config leaks the database URL: %s", out)
	}
}
