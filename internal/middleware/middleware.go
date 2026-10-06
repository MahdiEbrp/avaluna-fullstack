package middleware

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/arena/avaluna/internal/auth"
	"github.com/arena/avaluna/internal/httpx"
)

type Chain []func(http.Handler) http.Handler

func (c Chain) Then(h http.Handler) http.Handler {
	for i := len(c) - 1; i >= 0; i-- {
		h = c[i](h)
	}
	return h
}

type responseWriter struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (w *responseWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
		w.ResponseWriter.WriteHeader(code)
	}
}

func (w *responseWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	n, err := w.ResponseWriter.Write(b)
	w.bytes += n
	return n, err
}

func (w *responseWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func RequestIDMW() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := r.Header.Get("X-Request-ID")
			if id == "" {
				id = auth.RandomToken(8)
			}
			w.Header().Set("X-Request-ID", id)
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxRequestID, id)))
		})
	}
}

func Logger(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rw := &responseWriter{ResponseWriter: w}
			next.ServeHTTP(rw, r)
			if rw.status == 0 {
				rw.status = 200
			}
			lvl := slog.LevelInfo
			if rw.status >= 500 {
				lvl = slog.LevelError
			} else if rw.status >= 400 {
				lvl = slog.LevelWarn
			}
			log.Log(r.Context(), lvl, "http",
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Int("status", rw.status),
				slog.Int("bytes", rw.bytes),
				slog.String("ip", ClientIP(r)),
				slog.String("rid", RequestID(r.Context())),
				slog.Duration("took", time.Since(start)),
			)
		})
	}
}

func Recover(log *slog.Logger, onPanic func(path, msg, stack string)) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					stack := string(debug.Stack())
					msg := fmt.Sprint(rec)
					log.Error("panic", slog.String("path", r.URL.Path), slog.String("error", msg))
					if onPanic != nil {
						onPanic(r.URL.Path, msg, stack)
					}
					httpx.Fail(w, httpx.CodeInternal, "Unexpected server error")
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

func SecurityHeaders(production bool) func(http.Handler) http.Handler {
	csp := strings.Join([]string{
		"default-src 'self'",
		"img-src 'self' data: blob: https:",
		"script-src 'self' 'unsafe-inline' 'unsafe-eval'",
		"style-src 'self' 'unsafe-inline'",
		"font-src 'self' data:",
		"connect-src 'self'",
		"frame-ancestors 'self' https://*.e2b.app",
		"base-uri 'self'",
		"form-action 'self'",
	}, "; ")
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("X-Frame-Options", "SAMEORIGIN")
			h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
			h.Set("Permissions-Policy", "geolocation=(), microphone=(), camera=()")
			h.Set("Content-Security-Policy", csp)
			if production {
				h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains; preload")
			}
			next.ServeHTTP(w, r)
		})
	}
}

func CORS(allowed []string) func(http.Handler) http.Handler {
	allow := map[string]bool{}
	for _, o := range allowed {
		allow[strings.TrimSpace(o)] = true
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if origin != "" && (allow["*"] || allow[origin] || strings.HasSuffix(origin, ".e2b.app")) {
				h := w.Header()
				h.Set("Access-Control-Allow-Origin", origin)
				h.Set("Vary", "Origin")
				h.Set("Access-Control-Allow-Credentials", "true")
				h.Set("Access-Control-Allow-Methods", "GET,POST,PUT,PATCH,DELETE,OPTIONS")
				h.Set("Access-Control-Allow-Headers",
					"Authorization,Content-Type,X-API-Key,X-CSRF-Token,Idempotency-Key,HX-Request,HX-Current-URL,HX-Target,HX-Trigger")
				h.Set("Access-Control-Max-Age", "600")
			}
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

type UserLookup func(ctx context.Context, id int) (email, role, status string, err error)

func Authenticate(secret []byte, cookieName string, lookup UserLookup) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tok := bearer(r)
			if tok == "" {
				if c, err := r.Cookie(cookieName); err == nil {
					tok = c.Value
				}
			}
			if tok != "" {
				if claims, err := auth.Parse(secret, tok); err == nil {
					role, email := claims.Role, claims.Email
					if lookup != nil {
						if e, rl, status, err := lookup(r.Context(), claims.UserID); err == nil {
							if status == "banned" {
								httpx.Fail(w, httpx.CodeForbidden, "Account is suspended")
								return
							}
							email, role = e, rl
						}
					}
					ctx := WithUser(r.Context(), claims.UserID, email, role)
					if claims.Locale != "" {
						ctx = context.WithValue(ctx, ctxLocale, claims.Locale)
					}
					r = r.WithContext(ctx)
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

func bearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if strings.HasPrefix(strings.ToLower(h), "bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return ""
}

type APIKeyLookup func(ctx context.Context, prefix, raw string) (scopes string, ok bool)

func APIKey(lookup APIKeyLookup) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw := r.Header.Get("X-API-Key")
			if raw != "" && lookup != nil {
				prefix := raw
				if i := strings.Index(raw, "."); i > 0 {
					prefix = raw[:i]
				}
				if scopes, ok := lookup(r.Context(), prefix, raw); ok {
					r = r.WithContext(context.WithValue(r.Context(), ctxAPIKey, scopes))
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

func RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if UserID(r.Context()) == 0 {
			httpx.Fail(w, httpx.CodeUnauthorized, "")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func RequireRole(min string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if UserID(r.Context()) == 0 {
				httpx.Fail(w, httpx.CodeUnauthorized, "")
				return
			}
			if !auth.RoleAtLeast(Role(r.Context()), min) {
				httpx.Fail(w, httpx.CodeForbidden,
					fmt.Sprintf("Role '%s' is required", min))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

type bucket struct {
	hits  int
	start time.Time
}

type RateLimiter struct {
	mu      sync.Mutex
	buckets map[string]*bucket
	enabled bool
}

func NewRateLimiter(enabled bool) *RateLimiter {
	rl := &RateLimiter{buckets: map[string]*bucket{}, enabled: enabled}
	go rl.gc()
	return rl
}

func (rl *RateLimiter) gc() {
	t := time.NewTicker(5 * time.Minute)
	for range t.C {
		rl.mu.Lock()
		for k, b := range rl.buckets {
			if time.Since(b.start) > 10*time.Minute {
				delete(rl.buckets, k)
			}
		}
		rl.mu.Unlock()
	}
}

func (rl *RateLimiter) Allow(key string, limit int, window time.Duration) (bool, int) {
	if !rl.enabled || limit <= 0 {
		return true, limit
	}
	rl.mu.Lock()
	defer rl.mu.Unlock()
	b, ok := rl.buckets[key]
	now := time.Now()
	if !ok || now.Sub(b.start) >= window {
		rl.buckets[key] = &bucket{hits: 1, start: now}
		return true, limit - 1
	}
	if b.hits >= limit {
		return false, 0
	}
	b.hits++
	return true, limit - b.hits
}

func (rl *RateLimiter) Limit(scope string, limit int, window time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := scope + "|" + ClientIP(r)
			if uid := UserID(r.Context()); uid > 0 {
				key = fmt.Sprintf("%s|u%d", scope, uid)
			}
			ok, remaining := rl.Allow(key, limit, window)
			w.Header().Set("X-RateLimit-Limit", fmt.Sprint(limit))
			w.Header().Set("X-RateLimit-Remaining", fmt.Sprint(remaining))
			if !ok {
				w.Header().Set("Retry-After", fmt.Sprint(int(window.Seconds())))
				httpx.Fail(w, httpx.CodeRateLimit, "")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func CSRF(cookieName string, secure bool) func(http.Handler) http.Handler {
	safe := map[string]bool{"GET": true, "HEAD": true, "OPTIONS": true, "TRACE": true}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := ""
			if c, err := r.Cookie(cookieName); err == nil {
				token = c.Value
			}
			if token == "" {
				token = auth.RandomToken(16)
				http.SetCookie(w, &http.Cookie{
					Name: cookieName, Value: token, Path: "/",
					HttpOnly: false, Secure: secure, SameSite: http.SameSiteLaxMode,
					MaxAge: 86400,
				})
			}
			if !safe[r.Method] && bearer(r) == "" && r.Header.Get("X-API-Key") == "" {
				sent := r.Header.Get("X-CSRF-Token")
				if sent == "" {
					sent = r.FormValue("csrf_token")
				}
				if sent != token {
					httpx.Fail(w, httpx.CodeForbidden, "CSRF token mismatch")
					return
				}
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxCSRF, token)))
		})
	}
}

type IdempotencyStore interface {
	Lookup(ctx context.Context, key string) (status int, body string, found bool)
	Reserve(ctx context.Context, key string, userID int, endpoint string) (acquired bool, err error)
	Save(ctx context.Context, key string, userID int, endpoint string, status int, body string) error
	Release(ctx context.Context, key string) error
}

func Idempotency(store IdempotencyStore, required bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodGet || store == nil {
				next.ServeHTTP(w, r)
				return
			}
			key := r.Header.Get("Idempotency-Key")
			if key == "" {
				if !required {
					next.ServeHTTP(w, r)
					return
				}
				httpx.Fail(w, httpx.CodeValidation, "Idempotency-Key header is required",
					httpx.FieldIssue{Field: "Idempotency-Key", Issue: "missing"})
				return
			}
			if status, body, found := store.Lookup(r.Context(), key); found && status > 0 {
				replayStored(w, status, body)
				return
			}

			acquired, err := store.Reserve(r.Context(), key, UserID(r.Context()), r.URL.Path)
			if err != nil {
				httpx.Fail(w, httpx.CodeInternal, "could not record the idempotency key")
				return
			}
			if !acquired {
				if status, body, found := store.Lookup(r.Context(), key); found && status > 0 {
					replayStored(w, status, body)
					return
				}
				httpx.Fail(w, httpx.CodeIdempotency,
					"a request with this Idempotency-Key is already being processed")
				return
			}

			rec := &recordingWriter{ResponseWriter: w}
			next.ServeHTTP(rec, r)
			if rec.status >= 200 && rec.status < 300 {
				_ = store.Save(r.Context(), key, UserID(r.Context()), r.URL.Path, rec.status, rec.body.String())
				return
			}

			_ = store.Release(r.Context(), key)
		})
	}
}

func replayStored(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Idempotent-Replay", "true")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body))
}

type recordingWriter struct {
	http.ResponseWriter
	status int
	body   strings.Builder
}

func (w *recordingWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
		w.ResponseWriter.WriteHeader(code)
	}
}

func (w *recordingWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	w.body.Write(b)
	return w.ResponseWriter.Write(b)
}

func Maintenance(isOn func() bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if isOn != nil && isOn() &&
				!auth.RoleAtLeast(Role(r.Context()), auth.RoleEditor) &&
				!maintenanceAllowed(r.URL.Path) {
				if strings.HasPrefix(r.URL.Path, "/api/") {
					httpx.Fail(w, httpx.CodeMaintenance, "")
					return
				}
				w.Header().Set("Retry-After", "600")
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = w.Write([]byte(maintenanceHTML))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func maintenanceAllowed(path string) bool {
	switch {
	case strings.Contains(path, "/health"),
		strings.HasPrefix(path, "/static/"),
		strings.Contains(path, "/auth/login"),
		strings.Contains(path, "/auth/refresh"),
		path == "/login",
		path == "/logout",
		path == "/favicon.ico",
		path == "/robots.txt":
		return true
	}
	return false
}

const maintenanceHTML = `<!doctype html><html><head><meta charset="utf-8">
<title>Maintenance</title><style>body{font-family:system-ui;display:grid;place-items:center;
height:100vh;margin:0;background:#0f172a;color:#e2e8f0}div{text-align:center}</style></head>
<body><div><h1>🛠️ در حال به‌روزرسانی</h1><p>Store is under maintenance. Please check back shortly.</p></div></body></html>`

func JSONBody(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
