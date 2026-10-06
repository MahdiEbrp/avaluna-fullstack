package middleware

import (
	"context"
	"net"
	"net/http"
	"strings"
)

type ctxKey string

const (
	ctxUserID    ctxKey = "user_id"
	ctxUserEmail ctxKey = "user_email"
	ctxUserRole  ctxKey = "user_role"
	ctxLocale    ctxKey = "locale"
	ctxRequestID ctxKey = "request_id"
	ctxCartToken ctxKey = "cart_token"
	ctxCSRF      ctxKey = "csrf_token"
	ctxAPIKey    ctxKey = "api_key_scopes"
)

func WithUser(ctx context.Context, id int, email, role string) context.Context {
	ctx = context.WithValue(ctx, ctxUserID, id)
	ctx = context.WithValue(ctx, ctxUserEmail, email)
	return context.WithValue(ctx, ctxUserRole, role)
}

func UserID(ctx context.Context) int {
	if v, ok := ctx.Value(ctxUserID).(int); ok {
		return v
	}
	return 0
}

func UserEmail(ctx context.Context) string { return str(ctx, ctxUserEmail) }

func Role(ctx context.Context) string {
	if v := str(ctx, ctxUserRole); v != "" {
		return v
	}
	return "guest"
}

func Locale(ctx context.Context) string {
	if v := str(ctx, ctxLocale); v != "" {
		return v
	}
	return "fa"
}

func RequestID(ctx context.Context) string { return str(ctx, ctxRequestID) }

func CartToken(ctx context.Context) string { return str(ctx, ctxCartToken) }

func CSRFToken(ctx context.Context) string { return str(ctx, ctxCSRF) }

func APIKeyScopes(ctx context.Context) string { return str(ctx, ctxAPIKey) }

func WithLocale(ctx context.Context, loc string) context.Context {
	return context.WithValue(ctx, ctxLocale, loc)
}

func WithCartToken(ctx context.Context, tok string) context.Context {
	return context.WithValue(ctx, ctxCartToken, tok)
}

func str(ctx context.Context, k ctxKey) string {
	if v, ok := ctx.Value(k).(string); ok {
		return v
	}
	return ""
}

func ClientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		return strings.TrimSpace(strings.Split(xff, ",")[0])
	}
	if rip := r.Header.Get("X-Real-IP"); rip != "" {
		return rip
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
