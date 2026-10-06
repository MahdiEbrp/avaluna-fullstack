package app

import (
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/arena/avaluna/internal/api"
	"github.com/arena/avaluna/internal/auth"
	"github.com/arena/avaluna/internal/core"
	"github.com/arena/avaluna/internal/i18n"
	"github.com/arena/avaluna/internal/middleware"
	"github.com/arena/avaluna/web"
)

type App struct {
	c      *core.Core
	mux    *http.ServeMux
	rt     *api.Router
	tpl    map[string]*template.Template
	static http.Handler
}

type VD map[string]any

func (v VD) T(key string, args ...any) string {
	loc, _ := v["Loc"].(string)
	return i18n.T(loc, key, args...)
}

func (v VD) Money(amount int) string {
	loc, _ := v["Loc"].(string)
	cur, _ := v["Currency"].(string)
	return i18n.Money(loc, cur, int64(amount))
}

func New(c *core.Core) (*App, error) {
	a := &App{c: c, mux: http.NewServeMux(), tpl: map[string]*template.Template{}}

	if err := a.parseTemplates(); err != nil {
		return nil, err
	}
	sub, err := fs.Sub(web.StaticFS, "static")
	if err != nil {
		return nil, err
	}
	a.static = http.StripPrefix("/static/", cacheControl(http.FileServer(http.FS(sub))))

	a.rt = api.RegisterAll(a.mux, c)
	a.routes()
	return a, nil
}

func (a *App) Router() *api.Router { return a.rt }

var funcMap = template.FuncMap{
	"inc":  func(n int) int { return n + 1 },
	"dec":  func(n int) int { return n - 1 },
	"safe": func(s string) template.HTML { return template.HTML(s) },
	"year": func() int { return time.Now().Year() },
}

func (a *App) parseTemplates() error {
	pages, err := fs.Glob(web.TemplatesFS, "templates/*.html")
	if err != nil {
		return err
	}
	adminPages, err := fs.Glob(web.TemplatesFS, "templates/admin/*.html")
	if err != nil {
		return err
	}
	layouts := map[string]bool{"templates/layout.html": true, "templates/admin_layout.html": true}

	parse := func(name, layout, page string) error {
		t, err := template.New("layout").Funcs(funcMap).ParseFS(web.TemplatesFS,
			layout, "templates/partials/*.html", page)
		if err != nil {
			return fmt.Errorf("parse %s: %w", page, err)
		}
		a.tpl[name] = t
		return nil
	}
	for _, p := range pages {
		if layouts[p] {
			continue
		}
		name := strings.TrimSuffix(strings.TrimPrefix(p, "templates/"), ".html")
		if err := parse(name, "templates/layout.html", p); err != nil {
			return err
		}
	}
	for _, p := range adminPages {
		name := strings.TrimSuffix(strings.TrimPrefix(p, "templates/"), ".html")
		if err := parse(name, "templates/admin_layout.html", p); err != nil {
			return err
		}
	}
	return nil
}

func (a *App) render(w http.ResponseWriter, r *http.Request, page string, vd VD) {
	t, ok := a.tpl[page]
	if !ok {
		http.Error(w, "template not found: "+page, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := t.ExecuteTemplate(w, "layout", vd); err != nil {
		a.c.Log.Error("render", "page", page, "err", err)
		a.c.LogError(r.Context(), "error", "TEMPLATE_RENDER", err.Error(), r.URL.Path, "")
	}
}

type menuItem struct {
	Label string
	URL   string
}

type localeOpt struct {
	Code  string
	Short string
}

func (a *App) base(w http.ResponseWriter, r *http.Request, title string) VD {
	ctx := r.Context()
	loc := middleware.Locale(ctx)
	if loc == "" {
		loc = a.c.Cfg.DefaultLocale
	}
	c := a.c

	vd := VD{
		"Loc":                loc,
		"Dir":                i18n.Dir(loc),
		"RTL":                i18n.IsRTL(loc),
		"Title":              title,
		"Path":               r.URL.Path,
		"Query":              r.URL.Query().Get("q"),
		"Currency":           c.Cfg.DefaultCurrency,
		"SiteName":           c.Setting("site_name", c.Cfg.AppName),
		"Tagline":            c.Setting("site_tagline", ""),
		"SupportEmail":       c.Setting("support_email", ""),
		"SupportPhone":       c.Setting("support_phone", ""),
		"MetaDescription":    c.Setting("site_tagline", c.Cfg.AppName),
		"Year":               time.Now().Year(),
		"Version":            api.Version,
		"Maintenance":        c.MaintenanceMode(),
		"MaintenanceMessage": c.Setting("maintenance_message", ""),
		"CSRF":               a.csrf(w, r),
		"Locales":            []localeOpt{{Code: "fa", Short: "فا"}, {Code: "en", Short: "EN"}},
	}
	if loc == "en" {
		vd["SiteName"] = c.Setting("site_name_en", "Arena Store")
		vd["Tagline"] = c.Setting("site_tagline_en", "")
		vd["MetaDescription"] = c.Setting("site_tagline_en", "Arena Store")
	}

	if uid := middleware.UserID(ctx); uid > 0 {
		if u, err := c.DB.MapOne(ctx,
			`SELECT id,email,first_name,last_name,role FROM users WHERE id=?`, uid); err == nil {
			vd["User"] = map[string]any{
				"ID": u["id"], "Email": u["email"], "FirstName": u["first_name"],
				"LastName": u["last_name"], "Role": u["role"],
			}
			vd["IsStaff"] = auth.IsStaff(str(u["role"]))
		}
	}

	vd["CartCount"] = a.cartCount(r)
	vd["FooterMenu"] = a.menu(ctx, "footer", loc)
	cats, _ := c.DB.Maps(ctx, `SELECT slug,name,name_en FROM categories
		WHERE active=1 AND parent_id IS NULL AND deleted_at IS NULL ORDER BY sort_order LIMIT 6`)
	foot := make([]map[string]any, 0, len(cats))
	for _, row := range cats {
		foot = append(foot, map[string]any{"slug": row["slug"], "name": localized(row, "name", loc)})
	}
	vd["FooterCats"] = foot
	return vd
}

func (a *App) menu(ctx context.Context, code, loc string) []menuItem {
	row, err := a.c.DB.MapOne(ctx, `SELECT items_json FROM menus WHERE code=?`, code)
	if err != nil {
		return nil
	}
	var raw []struct {
		Label   string `json:"label"`
		LabelEn string `json:"labelEn"`
		URL     string `json:"url"`
	}
	if err := json.Unmarshal([]byte(str(row["items_json"])), &raw); err != nil {
		return nil
	}
	out := make([]menuItem, 0, len(raw))
	for _, it := range raw {
		label := it.Label
		if loc == "en" && it.LabelEn != "" {
			label = it.LabelEn
		}
		out = append(out, menuItem{Label: label, URL: it.URL})
	}
	return out
}

func (a *App) cartCount(r *http.Request) int {
	ctx := r.Context()
	if uid := middleware.UserID(ctx); uid > 0 {
		n, _ := a.c.DB.Count(ctx, `SELECT COALESCE(SUM(ci.quantity),0) FROM cart_items ci
			JOIN carts c ON c.id=ci.cart_id WHERE c.user_id=? AND c.status='active'`, uid)
		return n
	}
	ck, err := r.Cookie(api.CartCookie)
	if err != nil {
		return 0
	}
	n, _ := a.c.DB.Count(ctx, `SELECT COALESCE(SUM(ci.quantity),0) FROM cart_items ci
		JOIN carts c ON c.id=ci.cart_id WHERE c.token=? AND c.status='active'`, ck.Value)
	return n
}

func (a *App) csrf(w http.ResponseWriter, r *http.Request) string {
	if ck, err := r.Cookie(a.c.Cfg.CSRFCookieName); err == nil && ck.Value != "" {
		return ck.Value
	}
	tok := auth.RandomToken(16)
	if w != nil {
		http.SetCookie(w, &http.Cookie{
			Name: a.c.Cfg.CSRFCookieName, Value: tok, Path: "/",
			HttpOnly: false, Secure: a.c.Cfg.CookieSecure,
			SameSite: http.SameSiteLaxMode, MaxAge: 12 * 3600,
		})
	}
	return tok
}

func (a *App) Handler() http.Handler {
	c := a.c
	lookup := func(ctx context.Context, id int) (string, string, string, error) {
		row, err := c.DB.MapOne(ctx, `SELECT email,role,status FROM users WHERE id=? AND deleted_at IS NULL`, id)
		if err != nil {
			return "", "", "", err
		}
		return str(row["email"]), str(row["role"]), str(row["status"]), nil
	}
	apiKeys := func(ctx context.Context, prefix, raw string) (string, bool) {
		row, err := c.DB.MapOne(ctx,
			`SELECT key_hash,scopes FROM api_keys WHERE prefix=? AND revoked_at IS NULL`, prefix)
		if err != nil || str(row["key_hash"]) != auth.HashToken(raw) {
			return "", false
		}
		_, _ = c.DB.Write(ctx, `UPDATE api_keys SET last_used_at=datetime('now') WHERE prefix=?`, prefix)
		return str(row["scopes"]), true
	}

	chain := middleware.Chain{
		middleware.RequestIDMW(),
		middleware.Recover(c.Log, func(path, msg, stack string) {
			c.LogError(context.Background(), "fatal", "PANIC", msg, path, stack)
		}),
		middleware.Logger(c.Log),
		middleware.SecurityHeaders(c.Cfg.AppEnv == "production"),
		middleware.CORS(strings.Split(c.Setting("cors_origins", "*"), ",")),
		middleware.Authenticate(c.Cfg.JWTSecret, c.Cfg.SessionCookieName, lookup),
		localeMW(c),
		middleware.APIKey(apiKeys),
		middleware.Maintenance(c.MaintenanceMode),
		c.RL.Limit("global", 300, time.Minute),
	}
	return chain.Then(a.mux)
}

func localeMW(c *core.Core) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {

			loc := i18n.Explicit(r)
			if loc == "" {
				loc = middleware.Locale(r.Context())
			}
			if loc == "" {
				loc = i18n.Resolve(r)
			}
			if loc == "" {
				loc = c.Cfg.DefaultLocale
			}
			next.ServeHTTP(w, r.WithContext(middleware.WithLocale(r.Context(), loc)))
		})
	}
}

func cacheControl(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=3600")
		h.ServeHTTP(w, r)
	})
}

func str(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case []byte:
		return string(t)
	default:
		return fmt.Sprint(t)
	}
}

func num(v any) int {
	switch t := v.(type) {
	case int:
		return t
	case int64:
		return int(t)
	case float64:
		return int(t)
	case string:
		n, _ := strconv.Atoi(t)
		return n
	}
	return 0
}

func flt(v any) float64 {
	switch t := v.(type) {
	case float64:
		return t
	case int64:
		return float64(t)
	case int:
		return float64(t)
	case string:
		f, _ := strconv.ParseFloat(t, 64)
		return f
	}
	return 0
}

func localized(row map[string]any, field, loc string) string {
	if loc == "en" {
		if v := str(row[field+"_en"]); v != "" {
			return v
		}
	}
	return str(row[field])
}

func stars(avg float64) string {
	full := int(avg + 0.5)
	if full > 5 {
		full = 5
	}
	if full < 0 {
		full = 0
	}
	return strings.Repeat("★", full) + strings.Repeat("☆", 5-full)
}

func shortDate(s string) string {
	if len(s) >= 10 {
		return s[:10]
	}
	return s
}

func qInt(r *http.Request, key string, def int) int {
	n, err := strconv.Atoi(r.URL.Query().Get(key))
	if err != nil {
		return def
	}
	return n
}

type capture struct {
	status int
	buf    strings.Builder
	hdr    http.Header
}

func (c *capture) Header() http.Header {
	if c.hdr == nil {
		c.hdr = http.Header{}
	}
	return c.hdr
}
func (c *capture) Write(p []byte) (int, error) { return c.buf.Write(p) }
func (c *capture) WriteHeader(s int)           { c.status = s }

func (a *App) callAPI(w http.ResponseWriter, r *http.Request, h http.HandlerFunc,
	payload map[string]any) (int, map[string]any) {
	raw, _ := json.Marshal(payload)
	req := r.Clone(r.Context())
	req.Method = http.MethodPost
	req.Body = io.NopCloser(strings.NewReader(string(raw)))
	req.ContentLength = int64(len(raw))
	req.Header.Set("Content-Type", "application/json")

	cap := &capture{status: http.StatusOK}
	h(cap, req)

	if cap.hdr != nil {
		for _, ck := range cap.hdr.Values("Set-Cookie") {
			w.Header().Add("Set-Cookie", ck)
		}
	}
	var env map[string]any
	_ = json.Unmarshal([]byte(cap.buf.String()), &env)
	return cap.status, env
}

func envError(env map[string]any) string {
	if env == nil {
		return "unexpected error"
	}
	if e, ok := env["error"].(map[string]any); ok {
		return str(e["message"])
	}
	return str(env["message"])
}

func envData(env map[string]any) map[string]any {
	if env == nil {
		return map[string]any{}
	}
	if d, ok := env["data"].(map[string]any); ok {
		return d
	}
	return map[string]any{}
}
