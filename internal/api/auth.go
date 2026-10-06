package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/arena/avaluna/internal/auth"
	"github.com/arena/avaluna/internal/core"
	"github.com/arena/avaluna/internal/database"
	"github.com/arena/avaluna/internal/httpx"
	"github.com/arena/avaluna/internal/middleware"
)

func registerAuthModule(rt *Router, c *core.Core) {
	rt.Module("01-auth")
	authLimit := rt.Limit("auth", 5, time.Minute)

	rt.HandleWith(http.MethodPost, "/auth/register", "", handleRegister(c), authLimit)
	rt.HandleWith(http.MethodPost, "/auth/login", "", handleLogin(c), authLimit)
	rt.Handle(http.MethodPost, "/auth/logout", "", handleLogout(c))
	rt.HandleWith(http.MethodPost, "/auth/refresh-token", "", handleRefresh(c), rt.Limit("refresh", 30, time.Minute))
	rt.Handle(http.MethodPost, "/auth/verify-email", "", handleVerifyEmail(c))
	rt.HandleWith(http.MethodPost, "/auth/forgot-password", "", handleForgotPassword(c), authLimit)
	rt.HandleWith(http.MethodPost, "/auth/reset-password", "", handleResetPassword(c), authLimit)
	rt.Handle(http.MethodPost, "/auth/2fa/enable", RoleAuthenticated, handle2FAEnable(c))
	rt.Handle(http.MethodPost, "/auth/2fa/verify", RoleAuthenticated, handle2FAVerify(c))
	rt.Handle(http.MethodGet, "/auth/sessions", RoleAuthenticated, handleSessions(c))

	rt.Handle(http.MethodGet, "/users/me", RoleAuthenticated, handleMe(c))
	rt.Handle(http.MethodPatch, "/users/me", RoleAuthenticated, handleUpdateMe(c))
	rt.Handle(http.MethodDelete, "/users/me", RoleAuthenticated, handleDeleteMe(c))
	rt.Handle(http.MethodGet, "/users/me/orders", RoleAuthenticated, handleMyOrders(c))

	addresses := &Resource{
		Name: "users/me/addresses", Table: "addresses", SoftDelete: true, Owner: "user_id",
		Fields: []Field{
			{Name: "label", Kind: KindString, Default: "home"},
			{Name: "recipient", Kind: KindString, Required: true},
			{Name: "phone", Kind: KindString, Required: true},
			{Name: "country", Kind: KindString, Default: "IR"},
			{Name: "province", Kind: KindString},
			{Name: "city", Kind: KindString},
			{Name: "postal_code", Kind: KindString},
			{Name: "line1", Kind: KindString, Required: true},
			{Name: "line2", Kind: KindString},
			{Name: "is_default", Kind: KindBool},
		},
		ReadRole: RoleAuthenticated, WriteRole: RoleAuthenticated, OrderBy: "is_default DESC, id DESC",
	}
	addresses.Mount(rt, c.DB)
}

func RegisterHandler(c *core.Core) http.HandlerFunc { return handleRegister(c) }

func LoginHandler(c *core.Core) http.HandlerFunc { return handleLogin(c) }

func handleRegister(c *core.Core) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		m, err := body(r)
		if err != nil {
			httpx.Fail(w, httpx.CodeValidation, err.Error())
			return
		}
		if issues := requireFields(m, "email", "password"); len(issues) > 0 {
			httpx.Fail(w, httpx.CodeValidation, "", issues...)
			return
		}
		email := strings.ToLower(sVal(m, "email"))
		pass := sVal(m, "password")
		if !validEmail(email) {
			httpx.Fail(w, httpx.CodeValidation, "", httpx.FieldIssue{Field: "email", Issue: "is not a valid address"})
			return
		}
		if len(pass) < 8 {
			httpx.Fail(w, httpx.CodeValidation, "", httpx.FieldIssue{Field: "password", Issue: "must be at least 8 characters"})
			return
		}
		n, _ := c.DB.Count(r.Context(), "SELECT COUNT(*) FROM users WHERE email = ?", email)
		if n > 0 {
			httpx.Fail(w, httpx.CodeConflict, "An account with this email already exists")
			return
		}
		hash, err := auth.HashPassword(pass, c.Cfg.ArgonPepper)
		if err != nil {
			httpx.Fail(w, httpx.CodeInternal, "Could not secure password")
			return
		}
		locale := sVal(m, "locale")
		if locale == "" {
			locale = middleware.Locale(r.Context())
		}
		refCode := strings.ToUpper(auth.RandomToken(4))
		res, err := c.DB.Write(r.Context(), `INSERT INTO users
			(uuid,email,phone,password_hash,first_name,last_name,role,status,locale,currency,referral_code)
			VALUES(?,?,?,?,?,?,'customer','active',?,?,?)`,
			auth.RandomToken(16), email, sVal(m, "phone"), hash,
			sVal(m, "firstName")+sVal(m, "first_name"), sVal(m, "lastName")+sVal(m, "last_name"),
			locale, c.Cfg.DefaultCurrency, refCode)
		if err != nil {
			httpx.Fail(w, httpx.CodeConflict, err.Error())
			return
		}
		id64, _ := res.LastInsertId()
		uid := int(id64)

		_, _ = c.DB.Write(r.Context(), `INSERT OR IGNORE INTO wallets(user_id,balance,currency) VALUES(?,0,?)`, uid, c.Cfg.DefaultCurrency)
		_, _ = c.DB.Write(r.Context(), `INSERT OR IGNORE INTO loyalty_accounts(user_id,points,tier) VALUES(?,0,'bronze')`, uid)
		_, _ = c.DB.Write(r.Context(), `INSERT OR IGNORE INTO notification_preferences(user_id) VALUES(?)`, uid)

		if ref := strings.ToUpper(sVal(m, "referralCode")); ref != "" {
			applyReferral(r.Context(), c, ref, uid)
		}
		c.Notify(r.Context(), uid, "account", "خوش آمدید / Welcome",
			"حساب شما با موفقیت ساخته شد. Your account is ready.", "/account")

		tokens, err := issueSession(r.Context(), c, w, uid, email, auth.RoleCustomer, locale, r)
		if err != nil {
			httpx.Fail(w, httpx.CodeInternal, err.Error())
			return
		}
		audit(c, r, "user.register", "users", toString(uid), map[string]any{"email": email})
		writeCreated(w, map[string]any{"user": map[string]any{
			"id": uid, "email": email, "role": "customer", "locale": locale, "referralCode": refCode,
		}, "tokens": tokens})
	}
}

func handleLogin(c *core.Core) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		m, err := body(r)
		if err != nil {
			httpx.Fail(w, httpx.CodeValidation, err.Error())
			return
		}
		email := strings.ToLower(sVal(m, "email"))
		pass := sVal(m, "password")
		if email == "" || pass == "" {
			httpx.Fail(w, httpx.CodeValidation, "Email and password are required")
			return
		}
		var (
			id                         int
			hash, role, status, locale string
			twofa                      int
			secret                     sql.NullString
			failed                     int
			lockedUntil                sql.NullString
		)
		err = c.DB.Row(r.Context(), `SELECT id,password_hash,role,status,locale,twofa_enabled,
			twofa_secret,failed_logins,locked_until FROM users WHERE email = ? AND deleted_at IS NULL`,
			[]any{email}, &id, &hash, &role, &status, &locale, &twofa, &secret, &failed, &lockedUntil)
		if errors.Is(err, database.ErrNotFound) {
			httpx.Fail(w, httpx.CodeUnauthorized, "Invalid email or password")
			return
		}
		if err != nil {
			httpx.Fail(w, httpx.CodeInternal, err.Error())
			return
		}
		if lockedUntil.Valid && lockedUntil.String > nowUTC() {
			httpx.Fail(w, httpx.CodeForbidden, "Account temporarily locked after repeated failures")
			return
		}
		if status == "banned" {
			httpx.Fail(w, httpx.CodeForbidden, "Account is suspended")
			return
		}
		if !auth.VerifyPassword(pass, hash, c.Cfg.ArgonPepper) {
			failed++
			lock := ""
			if failed >= 5 {
				lock = time.Now().UTC().Add(15 * time.Minute).Format("2006-01-02 15:04:05")
			}
			_, _ = c.DB.Write(r.Context(), `UPDATE users SET failed_logins=?, locked_until=NULLIF(?, '') WHERE id=?`, failed, lock, id)
			audit(c, r, "auth.login_failed", "users", toString(id), map[string]any{"attempts": failed})
			httpx.Fail(w, httpx.CodeUnauthorized, "Invalid email or password")
			return
		}

		if twofa == 1 && secret.Valid {
			code := sVal(m, "code")
			if code == "" {
				httpx.Success(w, http.StatusAccepted, "Two-factor code required",
					map[string]any{"twoFactorRequired": true, "userId": id}, nil)
				return
			}
			if !auth.VerifyTOTP(secret.String, code) {
				httpx.Fail(w, httpx.CodeUnauthorized, "Invalid two-factor code")
				return
			}
		}
		_, _ = c.DB.Write(r.Context(),
			`UPDATE users SET failed_logins=0, locked_until=NULL, last_login_at=datetime('now') WHERE id=?`, id)

		tokens, err := issueSession(r.Context(), c, w, id, email, role, locale, r)
		if err != nil {
			httpx.Fail(w, httpx.CodeInternal, err.Error())
			return
		}
		audit(c, r, "auth.login", "users", toString(id), nil)
		writeOK(w, map[string]any{
			"user":   map[string]any{"id": id, "email": email, "role": role, "locale": locale},
			"tokens": tokens,
		})
	}
}

func handleLogout(c *core.Core) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if ck, err := r.Cookie("ecms_refresh"); err == nil && ck.Value != "" {
			_, _ = c.DB.Write(r.Context(),
				`UPDATE refresh_tokens SET revoked_at = datetime('now') WHERE token_hash = ?`,
				auth.HashToken(ck.Value))
		}
		clearCookie(w, c, c.Cfg.SessionCookieName)
		clearCookie(w, c, "ecms_refresh")
		httpx.NoBody(w, "Signed out")
	}
}

func handleRefresh(c *core.Core) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		raw := ""
		if ck, err := r.Cookie("ecms_refresh"); err == nil {
			raw = ck.Value
		}
		if raw == "" {
			if m, err := body(r); err == nil {
				raw = sVal(m, "refreshToken")
			}
		}
		if raw == "" {
			httpx.Fail(w, httpx.CodeUnauthorized, "Refresh token missing")
			return
		}
		hash := auth.HashToken(raw)
		var (
			id, userID int
			family     string
			revoked    sql.NullString
			expires    string
		)
		err := c.DB.Row(r.Context(),
			`SELECT id,user_id,family,revoked_at,expires_at FROM refresh_tokens WHERE token_hash = ?`,
			[]any{hash}, &id, &userID, &family, &revoked, &expires)
		if errors.Is(err, database.ErrNotFound) {
			httpx.Fail(w, httpx.CodeUnauthorized, "Refresh token not recognised")
			return
		}
		if err != nil {
			httpx.Fail(w, httpx.CodeInternal, err.Error())
			return
		}

		if revoked.Valid {
			_, _ = c.DB.Write(r.Context(),
				`UPDATE refresh_tokens SET revoked_at=datetime('now'), reused=1 WHERE family=?`, family)
			audit(c, r, "auth.refresh_reuse_detected", "users", toString(userID), map[string]any{"family": family})
			httpx.Fail(w, httpx.CodeUnauthorized, "Refresh token reuse detected — all sessions revoked")
			return
		}
		if expires < nowUTC() {
			httpx.Fail(w, httpx.CodeUnauthorized, "Refresh token expired")
			return
		}

		claim, err := c.DB.Write(r.Context(),
			`UPDATE refresh_tokens SET revoked_at=datetime('now') WHERE id=? AND revoked_at IS NULL`, id)
		if err != nil {
			httpx.Fail(w, httpx.CodeInternal, err.Error())
			return
		}
		if n, _ := claim.RowsAffected(); n == 0 {
			_, _ = c.DB.Write(r.Context(),
				`UPDATE refresh_tokens SET revoked_at=datetime('now'), reused=1 WHERE family=?`, family)
			audit(c, r, "auth.refresh_reuse_detected", "users", toString(userID), map[string]any{"family": family})
			httpx.Fail(w, httpx.CodeUnauthorized, "Refresh token reuse detected — all sessions revoked")
			return
		}

		u, err := userRow(r.Context(), c, userID)
		if err != nil {
			httpx.Fail(w, httpx.CodeUnauthorized, "User no longer active")
			return
		}
		tokens, err := issueSessionFamily(r.Context(), c, w, userID, asStr(u["email"]), asStr(u["role"]), asStr(u["locale"]), family, r)
		if err != nil {
			httpx.Fail(w, httpx.CodeInternal, err.Error())
			return
		}
		writeOK(w, map[string]any{"tokens": tokens})
	}
}

func handleVerifyEmail(c *core.Core) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		m, _ := body(r)
		token := sVal(m, "token")
		if token == "" {
			httpx.Fail(w, httpx.CodeValidation, "Token is required")
			return
		}
		uid, ok := consumeAuthToken(r.Context(), c, "verify_email", token)
		if !ok {
			httpx.Fail(w, httpx.CodeValidation, "Token is invalid or expired")
			return
		}
		_, _ = c.DB.Write(r.Context(), `UPDATE users SET email_verified_at=datetime('now') WHERE id=?`, uid)
		httpx.NoBody(w, "Email verified")
	}
}

func handleForgotPassword(c *core.Core) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		m, _ := body(r)
		email := strings.ToLower(sVal(m, "email"))
		var uid int
		err := c.DB.Row(r.Context(), `SELECT id FROM users WHERE email=? AND deleted_at IS NULL`, []any{email}, &uid)

		if err == nil {
			tok := auth.RandomToken(24)
			_, _ = c.DB.Write(r.Context(),
				`INSERT INTO auth_tokens(user_id,purpose,token_hash,expires_at)
				 VALUES(?,'reset_password',?,datetime('now','+1 hour'))`, uid, auth.HashToken(tok))
			c.Log.Info("password reset issued", "user", uid, "token", tok)
			if c.Cfg.IsProduction() {
				writeOK(w, map[string]any{"sent": true})
				return
			}
			writeOK(w, map[string]any{"sent": true, "devToken": tok})
			return
		}
		writeOK(w, map[string]any{"sent": true})
	}
}

func handleResetPassword(c *core.Core) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		m, _ := body(r)
		token, pass := sVal(m, "token"), sVal(m, "password")
		if token == "" || len(pass) < 8 {
			httpx.Fail(w, httpx.CodeValidation, "Token and a password of 8+ characters are required")
			return
		}
		uid, ok := consumeAuthToken(r.Context(), c, "reset_password", token)
		if !ok {
			httpx.Fail(w, httpx.CodeValidation, "Token is invalid or expired")
			return
		}
		hash, err := auth.HashPassword(pass, c.Cfg.ArgonPepper)
		if err != nil {
			httpx.Fail(w, httpx.CodeInternal, "Could not secure password")
			return
		}
		_, _ = c.DB.Write(r.Context(), `UPDATE users SET password_hash=?, failed_logins=0, locked_until=NULL WHERE id=?`, hash, uid)
		_, _ = c.DB.Write(r.Context(), `UPDATE refresh_tokens SET revoked_at=datetime('now') WHERE user_id=?`, uid)
		audit(c, r, "auth.password_reset", "users", toString(uid), nil)
		httpx.NoBody(w, "Password updated")
	}
}

func handle2FAEnable(c *core.Core) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		uid := middleware.UserID(r.Context())
		secret := auth.NewTOTPSecret()
		_, err := c.DB.Write(r.Context(), `UPDATE users SET twofa_secret=? WHERE id=?`, secret, uid)
		if err != nil {
			httpx.Fail(w, httpx.CodeInternal, err.Error())
			return
		}
		issuer := c.Setting("site_name", c.Cfg.AppName)
		uri := "otpauth://totp/" + issuer + ":" + middleware.UserEmail(r.Context()) +
			"?secret=" + secret + "&issuer=" + strings.ReplaceAll(issuer, " ", "%20") + "&algorithm=SHA1&digits=6&period=30"
		writeOK(w, map[string]any{"secret": secret, "otpauthUrl": uri,
			"note": "Call /auth/2fa/verify with a valid code to activate."})
	}
}

func handle2FAVerify(c *core.Core) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		uid := middleware.UserID(r.Context())
		m, _ := body(r)
		code := sVal(m, "code")
		var secret sql.NullString
		if err := c.DB.Row(r.Context(), `SELECT twofa_secret FROM users WHERE id=?`, []any{uid}, &secret); err != nil {
			httpx.Fail(w, httpx.CodeInternal, err.Error())
			return
		}
		if !secret.Valid || !auth.VerifyTOTP(secret.String, code) {
			httpx.Fail(w, httpx.CodeUnauthorized, "Invalid two-factor code")
			return
		}
		_, _ = c.DB.Write(r.Context(), `UPDATE users SET twofa_enabled=1 WHERE id=?`, uid)
		audit(c, r, "auth.2fa_enabled", "users", toString(uid), nil)
		httpx.NoBody(w, "Two-factor authentication enabled")
	}
}

func handleSessions(c *core.Core) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rows, err := c.DB.Maps(r.Context(),
			`SELECT id,user_agent,ip,created_at,expires_at,revoked_at FROM refresh_tokens
			 WHERE user_id=? ORDER BY id DESC LIMIT 50`, middleware.UserID(r.Context()))
		if err != nil {
			httpx.Fail(w, httpx.CodeInternal, err.Error())
			return
		}
		writeOK(w, rows)
	}
}

func handleMe(c *core.Core) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, err := userRow(r.Context(), c, middleware.UserID(r.Context()))
		if err != nil {
			httpx.Fail(w, httpx.CodeNotFound, "")
			return
		}
		wallet, _ := c.DB.MapOne(r.Context(), `SELECT balance,currency FROM wallets WHERE user_id=?`, u["id"])
		loyal, _ := c.DB.MapOne(r.Context(), `SELECT points,tier,lifetime FROM loyalty_accounts WHERE user_id=?`, u["id"])
		writeOK(w, map[string]any{"profile": u, "wallet": wallet, "loyalty": loyal})
	}
}

func handleUpdateMe(c *core.Core) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		uid := middleware.UserID(r.Context())
		m, err := body(r)
		if err != nil {
			httpx.Fail(w, httpx.CodeValidation, err.Error())
			return
		}
		allowed := map[string]string{
			"firstName": "first_name", "first_name": "first_name",
			"lastName": "last_name", "last_name": "last_name",
			"phone": "phone", "locale": "locale", "currency": "currency",
		}
		sets, args := []string{}, []any{}
		for k, col := range allowed {
			if v, ok := m[k]; ok {
				sets = append(sets, col+"=?")
				args = append(args, toString(v))
			}
		}
		if pw := sVal(m, "password"); pw != "" {
			if len(pw) < 8 {
				httpx.Fail(w, httpx.CodeValidation, "", httpx.FieldIssue{Field: "password", Issue: "must be at least 8 characters"})
				return
			}
			h, err := auth.HashPassword(pw, c.Cfg.ArgonPepper)
			if err != nil {
				httpx.Fail(w, httpx.CodeInternal, "hash failure")
				return
			}
			sets = append(sets, "password_hash=?")
			args = append(args, h)
		}
		if len(sets) == 0 {
			httpx.Fail(w, httpx.CodeValidation, "No updatable fields supplied")
			return
		}
		args = append(args, uid)
		if _, err := c.DB.Write(r.Context(),
			"UPDATE users SET "+strings.Join(sets, ",")+", updated_at=datetime('now') WHERE id=?", args...); err != nil {
			httpx.Fail(w, httpx.CodeInternal, err.Error())
			return
		}
		u, _ := userRow(r.Context(), c, uid)
		httpx.Success(w, http.StatusOK, "Profile updated", u, nil)
	}
}

func handleDeleteMe(c *core.Core) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		uid := middleware.UserID(r.Context())
		if _, err := c.DB.Write(r.Context(),
			`UPDATE users SET deleted_at=datetime('now'), status='banned',
			 email = email || '.deleted.' || id WHERE id=?`, uid); err != nil {
			httpx.Fail(w, httpx.CodeInternal, err.Error())
			return
		}
		_, _ = c.DB.Write(r.Context(), `UPDATE refresh_tokens SET revoked_at=datetime('now') WHERE user_id=?`, uid)
		clearCookie(w, c, c.Cfg.SessionCookieName)
		clearCookie(w, c, "ecms_refresh")
		audit(c, r, "user.soft_delete", "users", toString(uid), nil)
		httpx.NoBody(w, "Account deleted (soft)")
	}
}

func handleMyOrders(c *core.Core) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		page, limit, offset := paginate(r)
		uid := middleware.UserID(r.Context())
		total, _ := c.DB.Count(r.Context(), `SELECT COUNT(*) FROM orders WHERE user_id=?`, uid)
		rows, err := c.DB.Maps(r.Context(),
			`SELECT id,number,status,payment_status,grand_total,currency,created_at
			 FROM orders WHERE user_id=? ORDER BY id DESC LIMIT ? OFFSET ?`, uid, limit, offset)
		if err != nil {
			httpx.Fail(w, httpx.CodeInternal, err.Error())
			return
		}
		httpx.List(w, rows, httpx.MakePagination(page, limit, total))
	}
}

type tokenPair struct {
	AccessToken  string `json:"accessToken"`
	TokenType    string `json:"tokenType"`
	ExpiresIn    int    `json:"expiresIn"`
	RefreshToken string `json:"refreshToken,omitempty"`
}

func issueSession(ctx context.Context, c *core.Core, w http.ResponseWriter,
	uid int, email, role, locale string, r *http.Request) (tokenPair, error) {
	return issueSessionFamily(ctx, c, w, uid, email, role, locale, auth.RandomToken(8), r)
}

func issueSessionFamily(ctx context.Context, c *core.Core, w http.ResponseWriter,
	uid int, email, role, locale, family string, r *http.Request) (tokenPair, error) {
	access, err := auth.Issue(c.Cfg.JWTSecret, uid, email, role, locale, c.Cfg.AccessTokenTTL)
	if err != nil {
		return tokenPair{}, err
	}
	refresh := auth.RandomToken(32)
	exp := time.Now().UTC().Add(c.Cfg.RefreshTokenTTL)
	if _, err := c.DB.Write(ctx,
		`INSERT INTO refresh_tokens(user_id,token_hash,family,user_agent,ip,expires_at)
		 VALUES(?,?,?,?,?,?)`,
		uid, auth.HashToken(refresh), family, r.UserAgent(), middleware.ClientIP(r),
		exp.Format("2006-01-02 15:04:05")); err != nil {
		return tokenPair{}, err
	}
	setCookie(w, c, c.Cfg.SessionCookieName, access, int(c.Cfg.AccessTokenTTL.Seconds()), false)
	setCookie(w, c, "ecms_refresh", refresh, int(c.Cfg.RefreshTokenTTL.Seconds()), true)
	return tokenPair{
		AccessToken: access, TokenType: "Bearer",
		ExpiresIn: int(c.Cfg.AccessTokenTTL.Seconds()), RefreshToken: refresh,
	}, nil
}

func setCookie(w http.ResponseWriter, c *core.Core, name, value string, maxAge int, strict bool) {
	ss := http.SameSiteLaxMode
	if strict {
		ss = http.SameSiteStrictMode
	}
	http.SetCookie(w, &http.Cookie{
		Name: name, Value: value, Path: "/", Domain: c.Cfg.CookieDomain,
		HttpOnly: true, Secure: c.Cfg.CookieSecure, SameSite: ss, MaxAge: maxAge,
	})
}

func clearCookie(w http.ResponseWriter, c *core.Core, name string) {
	http.SetCookie(w, &http.Cookie{
		Name: name, Value: "", Path: "/", Domain: c.Cfg.CookieDomain,
		HttpOnly: true, Secure: c.Cfg.CookieSecure, MaxAge: -1,
	})
}

func consumeAuthToken(ctx context.Context, c *core.Core, purpose, token string) (int, bool) {
	var id, uid int
	err := c.DB.Row(ctx, `SELECT id,user_id FROM auth_tokens
		WHERE purpose=? AND token_hash=? AND used_at IS NULL AND expires_at > datetime('now')`,
		[]any{purpose, auth.HashToken(token)}, &id, &uid)
	if err != nil {
		return 0, false
	}
	_, _ = c.DB.Write(ctx, `UPDATE auth_tokens SET used_at=datetime('now') WHERE id=?`, id)
	return uid, true
}

func UserLookupFn(c *core.Core) middleware.UserLookup {
	return func(ctx context.Context, id int) (string, string, string, error) {
		var email, role, status string
		err := c.DB.Row(ctx, `SELECT email,role,status FROM users WHERE id=? AND deleted_at IS NULL`,
			[]any{id}, &email, &role, &status)
		return email, role, status, err
	}
}

func APIKeyLookupFn(c *core.Core) middleware.APIKeyLookup {
	return func(ctx context.Context, prefix, raw string) (string, bool) {
		var hash, scopes string
		err := c.DB.Row(ctx, `SELECT key_hash,scopes FROM api_keys WHERE prefix=? AND revoked_at IS NULL`,
			[]any{prefix}, &hash, &scopes)
		if err != nil || auth.HashToken(raw) != hash {
			return "", false
		}
		_, _ = c.DB.Write(ctx, `UPDATE api_keys SET last_used_at=datetime('now') WHERE prefix=?`, prefix)
		return scopes, true
	}
}
