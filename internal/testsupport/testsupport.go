package testsupport

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arena/avaluna/internal/app"
	"github.com/arena/avaluna/internal/config"
	"github.com/arena/avaluna/internal/core"
	"github.com/arena/avaluna/internal/database"
)

type Env struct {
	T       *testing.T
	Cfg     *config.Config
	DB      *database.DB
	Core    *core.Core
	Handler http.Handler
}

type Option func(*config.Config)

func NoRateLimit() Option {
	return func(c *config.Config) { c.RateLimitEnabled = false }
}

func WithConfig(fn func(*config.Config)) Option { return Option(fn) }

func New(t *testing.T, opts ...Option) *Env {
	t.Helper()

	dir := t.TempDir()
	cfg := config.Load()
	cfg.DatabasePath = filepath.Join(dir, "test.db")
	cfg.BackupDir = filepath.Join(dir, "backups")
	cfg.UploadDir = filepath.Join(dir, "uploads")
	cfg.AppEnv = "test"
	cfg.EnableCron = false
	cfg.EnableQueue = false
	cfg.CookieSecure = false
	for _, o := range opts {
		o(cfg)
	}

	db, err := database.Open(cfg.DatabasePath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	ctx := context.Background()
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := database.Seed(ctx, db, cfg); err != nil {
		t.Fatalf("seed: %v", err)
	}

	log := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
	c := core.New(cfg, db, log)
	if err := c.ReloadSettings(ctx); err != nil {
		t.Fatalf("settings: %v", err)
	}

	a, err := app.New(c)
	if err != nil {
		t.Fatalf("app: %v", err)
	}
	return &Env{T: t, Cfg: cfg, DB: db, Core: c, Handler: a.Handler()}
}

type Client struct {
	env     *Env
	cookies map[string]string
}

func (e *Env) Browser() *Client { return &Client{env: e, cookies: map[string]string{}} }

func (cl *Client) Do(method, target string, body io.Reader, contentType string) *httptest.ResponseRecorder {
	cl.env.T.Helper()
	req := httptest.NewRequest(method, target, body)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	for k, v := range cl.cookies {
		req.AddCookie(&http.Cookie{Name: k, Value: v})
	}
	rec := httptest.NewRecorder()
	cl.env.Handler.ServeHTTP(rec, req)
	for _, ck := range rec.Result().Cookies() {
		if ck.MaxAge < 0 {
			delete(cl.cookies, ck.Name)
			continue
		}
		cl.cookies[ck.Name] = ck.Value
	}
	return rec
}

func (cl *Client) Get(target string) *httptest.ResponseRecorder {
	return cl.Do(http.MethodGet, target, nil, "")
}

func (cl *Client) PostForm(target string, form interface{ Encode() string }) *httptest.ResponseRecorder {
	return cl.Do(http.MethodPost, target, stringsReader(form.Encode()),
		"application/x-www-form-urlencoded")
}

func (cl *Client) PostJSON(target, body string) *httptest.ResponseRecorder {
	return cl.Do(http.MethodPost, target, stringsReader(body), "application/json")
}

func (cl *Client) Cookie(name string) string { return cl.cookies[name] }

func (e *Env) Request(method, target, body string, hdr map[string]string) *httptest.ResponseRecorder {
	var rdr io.Reader = strings.NewReader(body)
	req := httptest.NewRequest(method, target, rdr)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	e.Handler.ServeHTTP(rec, req)
	return rec
}

func stringsReader(s string) io.Reader { return strings.NewReader(s) }
