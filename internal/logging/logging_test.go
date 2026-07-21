package logging

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
)

func TestNew_RedactsSensitiveKeys(t *testing.T) {
	tests := []struct {
		name  string
		key   string
		value string
	}{
		{"webhook secret", "webhook_secret", "wh-super-secret-value"},
		{"github app private key", "github_app_private_key", "-----BEGIN RSA PRIVATE KEY-----"},
		{"installation token", "installation_token", "ghs_abc123def456"},
		{"password", "password", "hunter2"},
		{"api key mixed case", "APIKey", "sk-abcdef"},
		{"key containing TOKEN uppercase", "AuthToken", "tok_value"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			logger := New("info", &buf)
			logger.Info("test message", tt.key, tt.value, "safe_field", "ok")

			out := buf.String()
			if strings.Contains(out, tt.value) {
				t.Fatalf("log output leaked sensitive value %q for key %q: %s", tt.value, tt.key, out)
			}
			if !strings.Contains(out, redacted) {
				t.Fatalf("expected redacted marker %q in output for key %q, got: %s", redacted, tt.key, out)
			}
			if !strings.Contains(out, `"safe_field":"ok"`) {
				t.Fatalf("expected unrelated field to pass through unchanged, got: %s", out)
			}
		})
	}
}

func TestNew_LevelGatesVerboseLogging(t *testing.T) {
	var infoBuf bytes.Buffer
	infoLogger := New("info", &infoBuf)
	infoLogger.Debug("full diff content", "diff", "- old line\n+ new line")
	if infoBuf.Len() != 0 {
		t.Fatalf("expected debug log to be suppressed at info level, got: %s", infoBuf.String())
	}

	var debugBuf bytes.Buffer
	debugLogger := New("debug", &debugBuf)
	debugLogger.Debug("full diff content", "diff", "- old line\n+ new line")
	if !strings.Contains(debugBuf.String(), "full diff content") {
		t.Fatalf("expected debug log to be emitted at debug level, got: %s", debugBuf.String())
	}
}

func TestNew_UnrecognizedLevelDefaultsToInfo(t *testing.T) {
	var buf bytes.Buffer
	logger := New("not-a-real-level", &buf)
	logger.Debug("should be suppressed")
	if buf.Len() != 0 {
		t.Fatalf("expected unrecognized level to default to info, got: %s", buf.String())
	}
	logger.Info("should appear")
	if !strings.Contains(buf.String(), "should appear") {
		t.Fatal("expected info log to be emitted under default level")
	}
}

func TestFromContext_ReturnsStoredLogger(t *testing.T) {
	var buf bytes.Buffer
	stored := New("info", &buf)

	ctx := WithLogger(context.Background(), stored)
	got := FromContext(ctx)
	got.Info("hello")

	if !strings.Contains(buf.String(), "hello") {
		t.Fatalf("expected FromContext to return the stored logger, got: %s", buf.String())
	}
}

func TestFromContext_FallsBackWhenEmpty(t *testing.T) {
	var buf bytes.Buffer
	fallback := New("info", &buf)

	got := FromContext(context.Background(), fallback)
	got.Info("hello")

	if !strings.Contains(buf.String(), "hello") {
		t.Fatalf("expected fallback logger to be used when ctx has none, got: %s", buf.String())
	}
}

func TestFromContext_DefaultsWhenNoFallback(t *testing.T) {
	got := FromContext(context.Background())
	if got == nil {
		t.Fatal("expected a non-nil default logger")
	}
	if got != slog.Default() {
		t.Fatal("expected slog.Default() when ctx and fallback are both empty")
	}
}
