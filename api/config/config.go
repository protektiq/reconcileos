package config

import (
	"os"
	"strings"
)

type Config struct {
	AppVersion         string
	Port               string
	SupabaseURL        string
	SupabaseAnonKey    string
	SupabaseServiceKey string
	AllowedOriginFly   string
}

func Load() Config {
	cfg := Config{
		AppVersion:         "0.1.0",
		Port:               getOrDefault("PORT", "8080"),
		SupabaseURL:        mustGet("SUPABASE_URL"),
		SupabaseAnonKey:    mustGet("SUPABASE_ANON_KEY"),
		SupabaseServiceKey: mustGet("SUPABASE_SERVICE_ROLE_KEY"),
		AllowedOriginFly:   strings.TrimSpace(os.Getenv("ALLOWED_ORIGIN_FLY")),
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
