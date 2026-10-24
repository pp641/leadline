// Package config loads runtime configuration from environment variables.
// Secrets are only ever read from the environment, never from files in the repo.
package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Env      string // "development" | "production" | "test"
	LogLevel string
	HTTPAddr string

	DatabaseURL      string
	DatabaseMaxConns int32
	RedisURL         string

	JWTSecret       []byte
	SessionSecret   []byte // signs unsubscribe tokens and other non-auth tokens
	AccessTokenTTL  time.Duration
	RefreshTokenTTL time.Duration
	EncryptionKey   []byte // 32 bytes, AES-256-GCM for integration credentials

	AppURL string // public URL of the frontend (used in emails)
	// SystemEmailFrom/Name send account emails (verification codes, invitations).
	SystemEmailFrom string
	SystemEmailName string
	APIURL string // public URL of the API

	StorageDir string

	EmailProvider string // "smtp" | "log"
	SMTPHost      string
	SMTPPort      int
	SMTPUsername  string
	SMTPPassword  string
	SMTPTLS       string // "none" | "starttls" | "tls"

	WebhookSecret string

	VerifierMXLookup        bool
	VerifierAllowTestDomain bool

	LLMProvider string
	LLMAPIKey   string
	LLMModel    string

	InboxPollInterval time.Duration

	// Read-only connection to the company-scraper database (companies to import as leads).
	ScraperDatabaseURL string

	WorkerConcurrency int
	SchedulerInterval time.Duration

	TrustProxy bool // honour X-Forwarded-For (only behind a proxy that sets it)
}

func Load() (*Config, error) {
	c := &Config{
		Env:                     get("APP_ENV", "development"),
		LogLevel:                get("LOG_LEVEL", "info"),
		HTTPAddr:                get("HTTP_ADDR", ":8080"),
		DatabaseURL:             get("DATABASE_URL", ""),
		DatabaseMaxConns:        int32(getInt("DATABASE_MAX_CONNS", 20)),
		RedisURL:                get("REDIS_URL", "redis://localhost:6379/0"),
		JWTSecret:               []byte(get("JWT_SECRET", "")),
		SessionSecret:           []byte(get("SESSION_SECRET", "")),
		AccessTokenTTL:          getDuration("ACCESS_TOKEN_TTL", 15*time.Minute),
		RefreshTokenTTL:         getDuration("REFRESH_TOKEN_TTL", 30*24*time.Hour),
		AppURL:                  strings.TrimRight(get("APP_URL", "http://localhost:3000"), "/"),
		SystemEmailFrom:         get("SYSTEM_EMAIL_FROM", "no-reply@leadline.test"),
		SystemEmailName:         get("SYSTEM_EMAIL_NAME", "Leadline"),
		APIURL:                  strings.TrimRight(get("API_URL", "http://localhost:8080"), "/"),
		StorageDir:              get("STORAGE_DIR", "./data/uploads"),
		EmailProvider:           get("EMAIL_PROVIDER", "log"),
		SMTPHost:                get("SMTP_HOST", "localhost"),
		SMTPPort:                getInt("SMTP_PORT", 1025),
		SMTPUsername:            get("SMTP_USERNAME", ""),
		SMTPPassword:            get("SMTP_PASSWORD", ""),
		SMTPTLS:                 get("SMTP_TLS", "none"),
		WebhookSecret:           get("WEBHOOK_SECRET", ""),
		VerifierMXLookup:        getBool("VERIFIER_MX_LOOKUP", true),
		VerifierAllowTestDomain: getBool("VERIFIER_ALLOW_TEST_DOMAINS", false),
		LLMProvider:             get("LLM_PROVIDER", "none"),
		LLMAPIKey:               get("LLM_API_KEY", ""),
		LLMModel:                get("LLM_MODEL", ""),
		InboxPollInterval:       getDuration("INBOX_POLL_INTERVAL", 2*time.Minute),
		ScraperDatabaseURL:      get("SCRAPER_DATABASE_URL", ""),
		WorkerConcurrency:       getInt("WORKER_CONCURRENCY", 8),
		SchedulerInterval:       getDuration("SCHEDULER_INTERVAL", 30*time.Second),
		TrustProxy:              getBool("TRUST_PROXY", false),
	}

	if key := get("ENCRYPTION_KEY", ""); key != "" {
		b, err := base64.StdEncoding.DecodeString(key)
		if err != nil || len(b) != 32 {
			return nil, errors.New("ENCRYPTION_KEY must be 32 bytes, base64-encoded")
		}
		c.EncryptionKey = b
	}

	var problems []string
	if c.DatabaseURL == "" {
		problems = append(problems, "DATABASE_URL is required")
	}
	if len(c.JWTSecret) < 32 {
		problems = append(problems, "JWT_SECRET must be at least 32 characters")
	}
	if len(c.SessionSecret) < 32 {
		problems = append(problems, "SESSION_SECRET must be at least 32 characters")
	}
	if c.IsProduction() && c.VerifierAllowTestDomain {
		problems = append(problems, "VERIFIER_ALLOW_TEST_DOMAINS must be false in production")
	}
	if c.IsProduction() && c.WebhookSecret == "" {
		problems = append(problems, "WEBHOOK_SECRET is required in production")
	}
	if len(problems) > 0 {
		return nil, fmt.Errorf("invalid configuration: %s", strings.Join(problems, "; "))
	}
	return c, nil
}

func (c *Config) IsProduction() bool { return c.Env == "production" }

func get(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

func getInt(key string, def int) int {
	if v, err := strconv.Atoi(get(key, "")); err == nil {
		return v
	}
	return def
}

func getBool(key string, def bool) bool {
	if v, err := strconv.ParseBool(get(key, "")); err == nil {
		return v
	}
	return def
}

func getDuration(key string, def time.Duration) time.Duration {
	if v, err := time.ParseDuration(get(key, "")); err == nil {
		return v
	}
	return def
}
