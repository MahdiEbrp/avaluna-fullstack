package config

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadUsesDocumentedDefaults(t *testing.T) {

	c := Load()

	cases := []struct{ name, got, want string }{
		{"AppEnv", c.AppEnv, "development"},
		{"Host", c.Host, "0.0.0.0"},
		{"APIPrefix", c.APIPrefix, "/api/v1"},
		{"DatabasePath", c.DatabasePath, "data/store.db"},
		{"BackupDir", c.BackupDir, "data/backups"},
		{"UploadDir", c.UploadDir, "data/uploads"},
		{"SessionCookieName", c.SessionCookieName, "ecms_session"},
		{"CSRFCookieName", c.CSRFCookieName, "ecms_csrf"},
		{"DefaultLocale", c.DefaultLocale, "fa"},
		{"DefaultCurrency", c.DefaultCurrency, "IRR"},
		{"AdminEmail", c.AdminEmail, "admin@yourstore.com"},
	}
	for _, tc := range cases {
		if tc.got != tc.want {
			t.Errorf("%s = %q, want %q", tc.name, tc.got, tc.want)
		}
	}
	if c.Port != 3000 {
		t.Errorf("Port = %d, want 3000", c.Port)
	}
	if c.AccessTokenTTL != 15*time.Minute {
		t.Errorf("AccessTokenTTL = %v, want 15m", c.AccessTokenTTL)
	}
	if c.QueueConcurrency != 4 {
		t.Errorf("QueueConcurrency = %d, want 4", c.QueueConcurrency)
	}
	if len(c.SupportedLocales) != 2 || c.SupportedLocales[0] != "fa" || c.SupportedLocales[1] != "en" {
		t.Errorf("SupportedLocales = %v, want [fa en]", c.SupportedLocales)
	}

	if len(c.JWTSecret) < 16 || len(c.ArgonPepper) < 16 {
		t.Errorf("generated secrets are too short: jwt=%d pepper=%d", len(c.JWTSecret), len(c.ArgonPepper))
	}
}

func TestGeneratedSecretsDifferBetweenLoads(t *testing.T) {

	a, b := Load(), Load()
	if string(a.JWTSecret) == string(b.JWTSecret) {
		t.Error("two Loads produced the same random JWT secret")
	}
	if string(a.ArgonPepper) == string(b.ArgonPepper) {
		t.Error("two Loads produced the same random Argon pepper")
	}
}

func TestEnvironmentOverridesEveryKnob(t *testing.T) {
	t.Setenv("APP_NAME", "Override Store")
	t.Setenv("APP_ENV", "production")
	t.Setenv("HOST", "127.0.0.1")
	t.Setenv("PORT", "8080")
	t.Setenv("API_PREFIX", "/api/v2")
	t.Setenv("DATABASE_PATH", "/tmp/other.db")
	t.Setenv("ACCESS_TOKEN_TTL", "45m")
	t.Setenv("SHUTDOWN_GRACE", "30s")
	t.Setenv("COOKIE_SECURE", "true")
	t.Setenv("SUPPORTED_LOCALES", "en,fr,de")
	t.Setenv("DEFAULT_LOCALE", "en")
	t.Setenv("ENABLE_CRON", "false")
	t.Setenv("QUEUE_CONCURRENCY", "16")
	t.Setenv("RATE_LIMIT", "false")
	t.Setenv("JWT_SECRET", "a-sufficiently-long-explicit-secret")

	c := Load()
	if c.AppName != "Override Store" || c.AppEnv != "production" {
		t.Errorf("string overrides ignored: %q / %q", c.AppName, c.AppEnv)
	}
	if c.Port != 8080 {
		t.Errorf("Port = %d, want 8080", c.Port)
	}
	if c.AccessTokenTTL != 45*time.Minute || c.GracePeriod != 30*time.Second {
		t.Errorf("duration overrides ignored: %v / %v", c.AccessTokenTTL, c.GracePeriod)
	}
	if !c.CookieSecure || c.EnableCron || c.RateLimitEnabled {
		t.Errorf("bool overrides ignored: secure=%v cron=%v rl=%v",
			c.CookieSecure, c.EnableCron, c.RateLimitEnabled)
	}
	if c.QueueConcurrency != 16 {
		t.Errorf("QueueConcurrency = %d, want 16", c.QueueConcurrency)
	}
	if len(c.SupportedLocales) != 3 {
		t.Errorf("SupportedLocales = %v, want three entries", c.SupportedLocales)
	}
	if string(c.JWTSecret) != "a-sufficiently-long-explicit-secret" {
		t.Error("an explicit JWT_SECRET was not used verbatim")
	}
	if !c.IsProduction() {
		t.Error("IsProduction should be true for APP_ENV=production")
	}
	if got := c.Addr(); got != "127.0.0.1:8080" {
		t.Errorf("Addr = %q", got)
	}

	if !strings.Contains(c.BaseURL, "8080") {
		t.Errorf("BaseURL = %q, should follow the configured port", c.BaseURL)
	}
}

func TestMalformedEnvironmentValuesFallBackToDefaults(t *testing.T) {

	t.Setenv("PORT", "not-a-number")
	t.Setenv("ACCESS_TOKEN_TTL", "fifteen minutes")
	t.Setenv("COOKIE_SECURE", "perhaps")
	t.Setenv("QUEUE_CONCURRENCY", "")

	c := Load()
	if c.Port != 3000 {
		t.Errorf("Port = %d, want the 3000 default after a bad value", c.Port)
	}
	if c.AccessTokenTTL != 15*time.Minute {
		t.Errorf("AccessTokenTTL = %v, want the 15m default", c.AccessTokenTTL)
	}
	if c.CookieSecure {
		t.Error("CookieSecure should fall back to false after an unparseable value")
	}
	if c.QueueConcurrency != 4 {
		t.Errorf("QueueConcurrency = %d, want the default for an empty value", c.QueueConcurrency)
	}
}

func TestShortJWTSecretIsReplacedNotTrusted(t *testing.T) {

	t.Setenv("JWT_SECRET", "tooshort")
	c := Load()
	if string(c.JWTSecret) == "tooshort" {
		t.Error("an 8-character JWT secret was accepted")
	}
	if len(c.JWTSecret) < 16 {
		t.Errorf("replacement secret is only %d bytes", len(c.JWTSecret))
	}
}

func TestLocaleSupported(t *testing.T) {
	c := Load()
	for _, loc := range []string{"fa", "en"} {
		if !c.LocaleSupported(loc) {
			t.Errorf("%q should be supported", loc)
		}
	}
	for _, loc := range []string{"de", "", "FA-IR", "xx"} {
		if c.LocaleSupported(loc) {
			t.Errorf("%q should not be supported", loc)
		}
	}
}

func TestWhitespaceOnlyEnvValuesUseDefaults(t *testing.T) {
	t.Setenv("APP_NAME", "   ")
	if got := Load().AppName; got != "Avaluna" {
		t.Errorf("AppName = %q, want the default for a blank override", got)
	}
}

func TestGeneratedSecretsSurviveARestart(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("JWT_SECRET", "")
	t.Setenv("ARGON_PEPPER", "")

	first := Load()
	first.DatabasePath = filepath.Join(dir, "store.db")
	created, err := first.EnsurePersistentSecrets()
	if err != nil {
		t.Fatalf("first boot: %v", err)
	}
	if !created {
		t.Fatal("the first boot did not persist its generated secrets")
	}

	second := Load()
	second.DatabasePath = filepath.Join(dir, "store.db")
	if _, err := second.EnsurePersistentSecrets(); err != nil {
		t.Fatalf("second boot: %v", err)
	}

	if string(first.ArgonPepper) != string(second.ArgonPepper) {
		t.Error("the Argon pepper changed across a restart; every password hash would break")
	}
	if string(first.JWTSecret) != string(second.JWTSecret) {
		t.Error("the JWT secret changed across a restart; every session would be invalidated")
	}

	third := Load()
	third.DatabasePath = filepath.Join(t.TempDir(), "other.db")
	if _, err := third.EnsurePersistentSecrets(); err != nil {
		t.Fatalf("third boot: %v", err)
	}
	if string(third.ArgonPepper) == string(first.ArgonPepper) {
		t.Error("a different data directory unexpectedly reused the same pepper")
	}
}

func TestSecretsFileIsPrivate(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("JWT_SECRET", "")
	t.Setenv("ARGON_PEPPER", "")

	c := Load()
	c.DatabasePath = filepath.Join(dir, "store.db")
	if _, err := c.EnsurePersistentSecrets(); err != nil {
		t.Fatalf("persist: %v", err)
	}

	info, err := os.Stat(c.SecretsPath())
	if err != nil {
		t.Fatalf("stat: %v", err)
	}

	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("secrets file mode = %04o, want 0600", perm)
	}
}

func TestExplicitSecretsAreNeverPersisted(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("JWT_SECRET", "an-explicit-jwt-secret-value")
	t.Setenv("ARGON_PEPPER", "an-explicit-argon-pepper-val")

	c := Load()
	c.DatabasePath = filepath.Join(dir, "store.db")
	created, err := c.EnsurePersistentSecrets()
	if err != nil {
		t.Fatalf("persist: %v", err)
	}
	if created {
		t.Error("a fully configured instance still wrote a secrets file")
	}
	if _, err := os.Stat(c.SecretsPath()); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("secrets file exists when both secrets came from the environment (%v)", err)
	}
	if string(c.ArgonPepper) != "an-explicit-argon-pepper-val" {
		t.Error("the explicit pepper was not used")
	}
	if got := c.MissingProductionSecrets(); len(got) != 0 {
		t.Errorf("MissingProductionSecrets = %v, want none", got)
	}
}

func TestMissingProductionSecretsAreReported(t *testing.T) {
	t.Setenv("JWT_SECRET", "")
	t.Setenv("ARGON_PEPPER", "")
	missing := Load().MissingProductionSecrets()
	if len(missing) != 2 {
		t.Fatalf("MissingProductionSecrets = %v, want both names", missing)
	}

	t.Setenv("JWT_SECRET", "an-explicit-jwt-secret-value")
	missing = Load().MissingProductionSecrets()
	if len(missing) != 1 || missing[0] != "ARGON_PEPPER" {
		t.Errorf("MissingProductionSecrets = %v, want just ARGON_PEPPER", missing)
	}
}

func TestOnlyTheMissingSecretIsBackfilled(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("JWT_SECRET", "")
	t.Setenv("ARGON_PEPPER", "")

	first := Load()
	first.DatabasePath = filepath.Join(dir, "store.db")
	if _, err := first.EnsurePersistentSecrets(); err != nil {
		t.Fatalf("first: %v", err)
	}

	t.Setenv("JWT_SECRET", "a-new-explicit-jwt-secret-xx")
	second := Load()
	second.DatabasePath = filepath.Join(dir, "store.db")
	if _, err := second.EnsurePersistentSecrets(); err != nil {
		t.Fatalf("second: %v", err)
	}
	if string(second.JWTSecret) != "a-new-explicit-jwt-secret-xx" {
		t.Error("the explicit JWT secret was overridden by the stored one")
	}
	if string(second.ArgonPepper) != string(first.ArgonPepper) {
		t.Error("the stored pepper was not reused, so passwords would break")
	}
}
