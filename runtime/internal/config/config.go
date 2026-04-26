package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	SupabaseURL        string
	SupabaseServiceKey string
	DispatcherInterval time.Duration
	ExecutorInterval   time.Duration
	DispatcherLockKey  int64
	ExecutorLockKey    int64
	TmpRoot            string
}

func LoadFromEnv() (Config, error) {
	supabaseURL := strings.TrimSpace(os.Getenv("SUPABASE_URL"))
	if supabaseURL == "" || len(supabaseURL) > 2048 {
		return Config{}, errors.New("SUPABASE_URL is required and must be <= 2048 chars")
	}
	if !strings.HasPrefix(supabaseURL, "http://") && !strings.HasPrefix(supabaseURL, "https://") {
		return Config{}, errors.New("SUPABASE_URL must start with http:// or https://")
	}

	serviceKey := strings.TrimSpace(os.Getenv("SUPABASE_SERVICE_ROLE_KEY"))
	if serviceKey == "" || len(serviceKey) > 8192 {
		return Config{}, errors.New("SUPABASE_SERVICE_ROLE_KEY is required and must be <= 8192 chars")
	}

	dispatcherInterval, err := readDurationEnv("RUNTIME_DISPATCHER_INTERVAL", 5*time.Second, time.Second, 30*time.Second)
	if err != nil {
		return Config{}, err
	}
	executorInterval, err := readDurationEnv("RUNTIME_EXECUTOR_INTERVAL", 2*time.Second, 500*time.Millisecond, 30*time.Second)
	if err != nil {
		return Config{}, err
	}

	dispatcherLockKey, err := readInt64Env("RUNTIME_DISPATCHER_LOCK_KEY", 74001)
	if err != nil {
		return Config{}, err
	}
	executorLockKey, err := readInt64Env("RUNTIME_EXECUTOR_LOCK_KEY", 74002)
	if err != nil {
		return Config{}, err
	}

	tmpRoot := strings.TrimSpace(os.Getenv("RUNTIME_TMP_ROOT"))
	if tmpRoot == "" {
		tmpRoot = "/tmp/reconcileos"
	}
	if len(tmpRoot) > 1024 {
		return Config{}, errors.New("RUNTIME_TMP_ROOT must be <= 1024 chars")
	}

	return Config{
		SupabaseURL:        supabaseURL,
		SupabaseServiceKey: serviceKey,
		DispatcherInterval: dispatcherInterval,
		ExecutorInterval:   executorInterval,
		DispatcherLockKey:  dispatcherLockKey,
		ExecutorLockKey:    executorLockKey,
		TmpRoot:            tmpRoot,
	}, nil
}

func readDurationEnv(key string, fallback, minValue, maxValue time.Duration) (time.Duration, error) {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback, nil
	}
	if len(value) > 64 {
		return 0, fmt.Errorf("%s is too long", key)
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("%s must be a valid duration: %w", key, err)
	}
	if parsed < minValue || parsed > maxValue {
		return 0, fmt.Errorf("%s must be between %s and %s", key, minValue, maxValue)
	}
	return parsed, nil
}

func readInt64Env(key string, fallback int64) (int64, error) {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback, nil
	}
	if len(value) > 32 {
		return 0, fmt.Errorf("%s is too long", key)
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s must be a valid int64: %w", key, err)
	}
	return parsed, nil
}
