package config

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Address            string
	ServerName         string
	ServerID           string
	User               string
	Password           string
	AllowEmptyPassword bool
	AccessToken        string

	StashInternalURL string
	StashPublicURL   string
	StashAPIKey      string
	StashTimeout     time.Duration

	DefaultPageSize int
	MaxPageSize     int
	StreamStrategy  string
}

func Load() (Config, error) {
	cfg := Config{
		Address:          env("STASHFIN_ADDR", ":8096"),
		ServerName:       env("STASHFIN_SERVER_NAME", "Stashfin"),
		ServerID:         env("STASHFIN_SERVER_ID", ""),
		User:             env("STASHFIN_USER", "stashfin"),
		Password:         env("STASHFIN_PASSWORD", ""),
		StashInternalURL: trimRightSlash(env("STASH_INTERNAL_URL", "http://stash:9999")),
		StashPublicURL:   trimRightSlash(env("STASH_PUBLIC_URL", "")),
		StashAPIKey:      env("STASH_API_KEY", ""),
		StashTimeout:     envDuration("STASH_TIMEOUT", 15*time.Second),
		DefaultPageSize:  envInt("STASHFIN_DEFAULT_PAGE_SIZE", 50),
		MaxPageSize:      envInt("STASHFIN_MAX_PAGE_SIZE", 200),
		StreamStrategy:   strings.ToLower(env("STASHFIN_STREAM_STRATEGY", "redirect")),
	}

	cfg.AllowEmptyPassword = envBool("STASHFIN_ALLOW_EMPTY_PASSWORD", false)

	if cfg.ServerID == "" {
		cfg.ServerID = randomHex(16)
	}
	if cfg.AccessToken = env("STASHFIN_ACCESS_TOKEN", ""); cfg.AccessToken == "" {
		cfg.AccessToken = randomHex(24)
	}
	if cfg.StashPublicURL == "" {
		cfg.StashPublicURL = cfg.StashInternalURL
	}
	if cfg.StashAPIKey == "" {
		return cfg, fmt.Errorf("STASH_API_KEY is required")
	}
	if cfg.Password == "" && !cfg.AllowEmptyPassword {
		return cfg, fmt.Errorf("STASHFIN_PASSWORD is required unless STASHFIN_ALLOW_EMPTY_PASSWORD=true")
	}
	if cfg.DefaultPageSize < 1 {
		cfg.DefaultPageSize = 50
	}
	if cfg.MaxPageSize < cfg.DefaultPageSize {
		cfg.MaxPageSize = cfg.DefaultPageSize
	}

	return cfg, nil
}

func env(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok {
		return strings.TrimSpace(value)
	}
	return fallback
}

func envInt(key string, fallback int) int {
	raw := env(key, "")
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return value
}

func envBool(key string, fallback bool) bool {
	raw := strings.ToLower(env(key, ""))
	switch raw {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}

func envDuration(key string, fallback time.Duration) time.Duration {
	raw := env(key, "")
	if raw == "" {
		return fallback
	}
	if seconds, err := strconv.Atoi(raw); err == nil {
		return time.Duration(seconds) * time.Second
	}
	value, err := time.ParseDuration(raw)
	if err != nil {
		return fallback
	}
	return value
}

func trimRightSlash(value string) string {
	return strings.TrimRight(strings.TrimSpace(value), "/")
}

func randomHex(bytes int) string {
	buf := make([]byte, bytes)
	if _, err := rand.Read(buf); err != nil {
		panic(err)
	}
	return hex.EncodeToString(buf)
}
