// Package config loads and validates Argus runtime configuration from the
// process environment. Secrets (App private key, webhook secret) are never
// defaulted and must be supplied by the caller's environment.
package config

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/ismetkoralay/argus/internal/logging"
)

// Config holds the runtime configuration for the service.
type Config struct {
	Port                string
	GitHubAppID         int64
	GitHubPrivateKeyPEM []byte
	GitHubWebhookSecret []byte
	OllamaBaseURL       string
	OllamaModel         string
	LogLevel            string
	// DatabaseURL enables optional Postgres review-history persistence
	// (see internal/history) when set. Empty is valid and means the
	// feature is off; Argus runs exactly as it does with no database.
	DatabaseURL string
}

// Load reads configuration from the process environment, returning an error
// that wraps the first missing or invalid variable it finds.
func Load() (Config, error) {
	appIDRaw, err := requireEnv("GITHUB_APP_ID")
	if err != nil {
		return Config{}, err
	}
	appID, err := strconv.ParseInt(appIDRaw, 10, 64)
	if err != nil {
		return Config{}, fmt.Errorf("parse GITHUB_APP_ID: %w", err)
	}
	if appID <= 0 {
		return Config{}, fmt.Errorf("invalid GITHUB_APP_ID %q: must be a positive integer", appIDRaw)
	}

	privateKey, err := requireEnv("GITHUB_APP_PRIVATE_KEY")
	if err != nil {
		return Config{}, err
	}
	// .env files and some secret stores can't hold real newlines cleanly, so
	// the PEM may be supplied as a single line with literal "\n" escapes.
	privateKey = strings.ReplaceAll(privateKey, `\n`, "\n")

	webhookSecret, err := requireEnv("GITHUB_WEBHOOK_SECRET")
	if err != nil {
		return Config{}, err
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	if p, err := strconv.Atoi(port); err != nil || p < 1 || p > 65535 {
		return Config{}, fmt.Errorf("invalid PORT %q: must be an integer between 1 and 65535", port)
	}

	ollamaBaseURL := os.Getenv("OLLAMA_BASE_URL")
	if ollamaBaseURL == "" {
		ollamaBaseURL = "http://localhost:11434"
	}
	if u, err := url.Parse(ollamaBaseURL); err != nil || u.Scheme == "" || u.Host == "" {
		return Config{}, fmt.Errorf("invalid OLLAMA_BASE_URL %q: must be an absolute http(s) URL", ollamaBaseURL)
	}

	ollamaModel := os.Getenv("OLLAMA_MODEL")
	if ollamaModel == "" {
		ollamaModel = "qwen2.5-coder"
	}

	logLevel := os.Getenv("LOG_LEVEL")
	if _, err := logging.ParseLevel(logLevel); err != nil {
		return Config{}, fmt.Errorf("invalid LOG_LEVEL: %w", err)
	}

	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL != "" {
		// The value itself is never included in the error (it carries a
		// password) — main.go logs config-load errors verbatim.
		u, err := url.Parse(databaseURL)
		if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.Host == "" {
			return Config{}, fmt.Errorf("invalid DATABASE_URL: must be a postgres:// or postgresql:// URL")
		}
	}

	return Config{
		Port:                port,
		GitHubAppID:         appID,
		GitHubPrivateKeyPEM: []byte(privateKey),
		GitHubWebhookSecret: []byte(webhookSecret),
		OllamaBaseURL:       ollamaBaseURL,
		OllamaModel:         ollamaModel,
		LogLevel:            logLevel,
		DatabaseURL:         databaseURL,
	}, nil
}

func requireEnv(key string) (string, error) {
	val := os.Getenv(key)
	if val == "" {
		return "", fmt.Errorf("missing required env var %s", key)
	}
	return val, nil
}
