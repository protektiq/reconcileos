package config

import (
	"os"
	"strings"
)

type Config struct {
	AppVersion          string
	Port                string
	SupabaseURL         string
	SupabaseAnonKey     string
	SupabaseServiceKey  string
	AllowedOriginFly    string
	RekorURL            string
	FlyOIDCToken        string
	GitHubAppID         string
	GitHubWebhookSecret string
	GitHubPrivateKey    string
	GitHubClientID      string
	GitHubClientSecret  string
	GitHubAPIBaseURL    string
	AnthropicAPIKey     string
	AnthropicAPIBaseURL string
	AnthropicModel      string
}

func Load() Config {
	cfg := Config{
		AppVersion:          "0.1.0",
		Port:                getOrDefault("PORT", "8080"),
		SupabaseURL:         mustGet("SUPABASE_URL"),
		SupabaseAnonKey:     mustGet("SUPABASE_ANON_KEY"),
		SupabaseServiceKey:  mustGet("SUPABASE_SERVICE_ROLE_KEY"),
		AllowedOriginFly:    strings.TrimSpace(os.Getenv("ALLOWED_ORIGIN_FLY")),
		RekorURL:            getOrDefault("REKOR_URL", "https://rekor.sigstore.dev"),
		FlyOIDCToken:        strings.TrimSpace(os.Getenv("FLY_OIDC_TOKEN")),
		GitHubAppID:         mustGet("GITHUB_APP_ID"),
		GitHubWebhookSecret: mustGet("GITHUB_APP_WEBHOOK_SECRET"),
		GitHubPrivateKey:    mustGet("GITHUB_APP_PRIVATE_KEY"),
		GitHubClientID:      mustGet("GITHUB_CLIENT_ID"),
		GitHubClientSecret:  mustGet("GITHUB_CLIENT_SECRET"),
		GitHubAPIBaseURL:    getOrDefault("GITHUB_API_BASE_URL", "https://api.github.com"),
		AnthropicAPIKey:     mustGet("ANTHROPIC_API_KEY"),
		AnthropicAPIBaseURL: getOrDefault("ANTHROPIC_API_BASE_URL", "https://api.anthropic.com"),
		AnthropicModel:      getOrDefault("ANTHROPIC_MODEL", "claude-sonnet-4-20250514"),
	}

	return cfg
}

func mustGet(key string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		panic("missing required environment variable: " + key)
	}

	return value
}

func getOrDefault(key, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}

	return value
}
