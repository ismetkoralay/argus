// Package config loads and validates Argus runtime configuration from the
// process environment. Secrets (App private key, webhook secret) are never
// defaulted and must be supplied by the caller's environment.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Config holds the runtime configuration for the service.
type Config struct {
	Port                string
	GitHubAppID         int64
	GitHubPrivateKeyPEM []byte
	GitHubWebhookSecret []byte
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

	return Config{
		Port:                port,
		GitHubAppID:         appID,
		GitHubPrivateKeyPEM: []byte(privateKey),
		GitHubWebhookSecret: []byte(webhookSecret),
	}, nil
}

func requireEnv(key string) (string, error) {
	val := os.Getenv(key)
	if val == "" {
		return "", fmt.Errorf("missing required env var %s", key)
	}
	return val, nil
}
