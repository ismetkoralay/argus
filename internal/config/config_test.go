package config

import (
	"testing"
)

func TestLoad(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		wantErr bool
		want    Config
	}{
		{
			name: "all present",
			env: map[string]string{
				"GITHUB_APP_ID":          "123",
				"GITHUB_APP_PRIVATE_KEY": "pem-content",
				"GITHUB_WEBHOOK_SECRET":  "shh",
				"PORT":                   "9090",
			},
			want: Config{
				Port:                "9090",
				GitHubAppID:         123,
				GitHubPrivateKeyPEM: []byte("pem-content"),
				GitHubWebhookSecret: []byte("shh"),
				OllamaBaseURL:       "http://localhost:11434",
				OllamaModel:         "qwen2.5-coder",
			},
		},
		{
			name: "port and ollama settings default when unset",
			env: map[string]string{
				"GITHUB_APP_ID":          "123",
				"GITHUB_APP_PRIVATE_KEY": "pem-content",
				"GITHUB_WEBHOOK_SECRET":  "shh",
			},
			want: Config{
				Port:                "8080",
				GitHubAppID:         123,
				GitHubPrivateKeyPEM: []byte("pem-content"),
				GitHubWebhookSecret: []byte("shh"),
				OllamaBaseURL:       "http://localhost:11434",
				OllamaModel:         "qwen2.5-coder",
			},
		},
		{
			name: "ollama settings overridden via env",
			env: map[string]string{
				"GITHUB_APP_ID":          "123",
				"GITHUB_APP_PRIVATE_KEY": "pem-content",
				"GITHUB_WEBHOOK_SECRET":  "shh",
				"OLLAMA_BASE_URL":        "http://ollama.internal:11434",
				"OLLAMA_MODEL":           "llama3.1",
			},
			want: Config{
				Port:                "8080",
				GitHubAppID:         123,
				GitHubPrivateKeyPEM: []byte("pem-content"),
				GitHubWebhookSecret: []byte("shh"),
				OllamaBaseURL:       "http://ollama.internal:11434",
				OllamaModel:         "llama3.1",
			},
		},
		{
			name: "escaped newlines in private key are unescaped",
			env: map[string]string{
				"GITHUB_APP_ID":          "123",
				"GITHUB_APP_PRIVATE_KEY": `-----BEGIN RSA PRIVATE KEY-----\nabc\n-----END RSA PRIVATE KEY-----`,
				"GITHUB_WEBHOOK_SECRET":  "shh",
			},
			want: Config{
				Port:                "8080",
				GitHubAppID:         123,
				GitHubPrivateKeyPEM: []byte("-----BEGIN RSA PRIVATE KEY-----\nabc\n-----END RSA PRIVATE KEY-----"),
				GitHubWebhookSecret: []byte("shh"),
				OllamaBaseURL:       "http://localhost:11434",
				OllamaModel:         "qwen2.5-coder",
			},
		},
		{
			name: "missing app id",
			env: map[string]string{
				"GITHUB_APP_PRIVATE_KEY": "pem-content",
				"GITHUB_WEBHOOK_SECRET":  "shh",
			},
			wantErr: true,
		},
		{
			name: "invalid app id",
			env: map[string]string{
				"GITHUB_APP_ID":          "not-a-number",
				"GITHUB_APP_PRIVATE_KEY": "pem-content",
				"GITHUB_WEBHOOK_SECRET":  "shh",
			},
			wantErr: true,
		},
		{
			name: "missing private key",
			env: map[string]string{
				"GITHUB_APP_ID":         "123",
				"GITHUB_WEBHOOK_SECRET": "shh",
			},
			wantErr: true,
		},
		{
			name: "missing webhook secret",
			env: map[string]string{
				"GITHUB_APP_ID":          "123",
				"GITHUB_APP_PRIVATE_KEY": "pem-content",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, key := range []string{"GITHUB_APP_ID", "GITHUB_APP_PRIVATE_KEY", "GITHUB_WEBHOOK_SECRET", "PORT", "OLLAMA_BASE_URL", "OLLAMA_MODEL"} {
				t.Setenv(key, tt.env[key])
			}

			cfg, err := Load()
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if cfg.Port != tt.want.Port || cfg.GitHubAppID != tt.want.GitHubAppID ||
				string(cfg.GitHubPrivateKeyPEM) != string(tt.want.GitHubPrivateKeyPEM) ||
				string(cfg.GitHubWebhookSecret) != string(tt.want.GitHubWebhookSecret) ||
				cfg.OllamaBaseURL != tt.want.OllamaBaseURL || cfg.OllamaModel != tt.want.OllamaModel {
				t.Fatalf("got %+v, want %+v", cfg, tt.want)
			}
		})
	}
}
