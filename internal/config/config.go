package config

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	AppName     string
	AppEnv      string
	Host        string
	Port        int
	BaseURL     string
	APIPrefix   string
	Timezone    string
	GracePeriod time.Duration

	DatabasePath string
	BackupDir    string
	UploadDir    string

	JWTSecret         []byte
	AccessTokenTTL    time.Duration
	RefreshTokenTTL   time.Duration
	ArgonPepper       []byte
	CookieSecure      bool
	CookieDomain      string
	SessionCookieName string
	CSRFCookieName    string

	jwtFromEnv    bool
	pepperFromEnv bool

	DefaultLocale    string
	SupportedLocales []string
	DefaultCurrency  string

	EnableCron       bool
	EnableQueue      bool
	QueueConcurrency int
	LogLevel         string
	RateLimitEnabled bool

	AutoMigrate  bool
	AutoSeed     bool
	AdminEmail   string
	AdminPass    string
	MaintenanceM bool
}

func Load() *Config {
	c := &Config{
		AppName:     env("APP_NAME", "Avaluna"),
		AppEnv:      env("APP_ENV", "development"),
		Host:        env("HOST", "0.0.0.0"),
		Port:        envInt("PORT", 3000),
		APIPrefix:   env("API_PREFIX", "/api/v1"),
		Timezone:    env("TZ", "UTC"),
		GracePeriod: envDur("SHUTDOWN_GRACE", 15*time.Second),

		DatabasePath: env("DATABASE_PATH", "data/store.db"),
		BackupDir:    env("BACKUP_DIR", "data/backups"),
		UploadDir:    env("UPLOAD_DIR", "data/uploads"),

		AccessTokenTTL:    envDur("ACCESS_TOKEN_TTL", 15*time.Minute),
		RefreshTokenTTL:   envDur("REFRESH_TOKEN_TTL", 720*time.Hour),
		CookieSecure:      envBool("COOKIE_SECURE", false),
		CookieDomain:      env("COOKIE_DOMAIN", ""),
		SessionCookieName: env("SESSION_COOKIE", "ecms_session"),
		CSRFCookieName:    env("CSRF_COOKIE", "ecms_csrf"),

		DefaultLocale:    env("DEFAULT_LOCALE", "fa"),
		SupportedLocales: strings.Split(env("SUPPORTED_LOCALES", "fa,en"), ","),
		DefaultCurrency:  env("DEFAULT_CURRENCY", "IRR"),

		EnableCron:       envBool("ENABLE_CRON", true),
		EnableQueue:      envBool("ENABLE_QUEUE", true),
		QueueConcurrency: envInt("QUEUE_CONCURRENCY", 4),
		LogLevel:         env("LOG_LEVEL", "info"),
		RateLimitEnabled: envBool("RATE_LIMIT", true),

		AutoMigrate: envBool("AUTO_MIGRATE", true),
		AutoSeed:    envBool("AUTO_SEED", true),
		AdminEmail:  env("ADMIN_EMAIL", "admin@yourstore.com"),
		AdminPass:   env("ADMIN_PASSWORD", "Admin@12345"),
	}

	c.JWTSecret, c.jwtFromEnv = secret("JWT_SECRET", 48)
	c.ArgonPepper, c.pepperFromEnv = secret("ARGON_PEPPER", 32)
	c.BaseURL = env("BASE_URL", "http://localhost:"+strconv.Itoa(c.Port))
	return c
}

func (c *Config) IsProduction() bool { return c.AppEnv == "production" }

func (c *Config) Addr() string { return c.Host + ":" + strconv.Itoa(c.Port) }

func (c *Config) LocaleSupported(loc string) bool {
	for _, l := range c.SupportedLocales {
		if strings.EqualFold(strings.TrimSpace(l), loc) {
			return true
		}
	}
	return false
}

func env(k, def string) string {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		return v
	}
	return def
}

func envInt(k string, def int) int {
	if v := os.Getenv(k); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func envBool(k string, def bool) bool {
	if v := os.Getenv(k); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return def
}

func envDur(k string, def time.Duration) time.Duration {
	if v := os.Getenv(k); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}

func secret(k string, n int) ([]byte, bool) {
	if v := os.Getenv(k); len(v) >= 16 {
		return []byte(v), true
	}
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return []byte("insecure-development-fallback-secret-key-000000"), false
	}
	return []byte(hex.EncodeToString(b)), false
}

const secretsFileName = ".secrets.json"

type persistedSecrets struct {
	JWTSecret   string `json:"jwtSecret"`
	ArgonPepper string `json:"argonPepper"`
}

func (c *Config) EnsurePersistentSecrets() (created bool, err error) {
	if c.jwtFromEnv && c.pepperFromEnv {
		return false, nil
	}

	dir := filepath.Dir(c.DatabasePath)
	if dir == "" || dir == "." {
		dir = "."
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return false, fmt.Errorf("create secrets dir: %w", err)
	}
	path := filepath.Join(dir, secretsFileName)

	var stored persistedSecrets
	switch raw, readErr := os.ReadFile(path); {
	case readErr == nil:
		if err := json.Unmarshal(raw, &stored); err != nil {
			return false, fmt.Errorf("parse %s: %w", path, err)
		}
	case !errors.Is(readErr, fs.ErrNotExist):
		return false, fmt.Errorf("read %s: %w", path, readErr)
	}

	if !c.jwtFromEnv && stored.JWTSecret != "" {
		c.JWTSecret = []byte(stored.JWTSecret)
	}
	if !c.pepperFromEnv && stored.ArgonPepper != "" {
		c.ArgonPepper = []byte(stored.ArgonPepper)
	}

	next := persistedSecrets{JWTSecret: stored.JWTSecret, ArgonPepper: stored.ArgonPepper}
	if !c.jwtFromEnv && next.JWTSecret == "" {
		next.JWTSecret = string(c.JWTSecret)
		created = true
	}
	if !c.pepperFromEnv && next.ArgonPepper == "" {
		next.ArgonPepper = string(c.ArgonPepper)
		created = true
	}
	if !created {
		return false, nil
	}

	blob, err := json.MarshalIndent(next, "", "  ")
	if err != nil {
		return false, err
	}
	if err := os.WriteFile(path, blob, 0o600); err != nil {
		return false, fmt.Errorf("write %s: %w", path, err)
	}
	return true, nil
}

func (c *Config) MissingProductionSecrets() []string {
	var missing []string
	if !c.jwtFromEnv {
		missing = append(missing, "JWT_SECRET")
	}
	if !c.pepperFromEnv {
		missing = append(missing, "ARGON_PEPPER")
	}
	return missing
}

func (c *Config) SecretsPath() string {
	dir := filepath.Dir(c.DatabasePath)
	if dir == "" {
		dir = "."
	}
	return filepath.Join(dir, secretsFileName)
}
