package app

import (
	"html/template"
	"net/http"
	"strings"

	"github.com/arena/avaluna/internal/api"
	"github.com/arena/avaluna/internal/auth"
	"github.com/arena/avaluna/internal/i18n"
	"github.com/arena/avaluna/internal/middleware"
)

func (a *App) handleLoginPage(w http.ResponseWriter, r *http.Request) {
	if middleware.UserID(r.Context()) > 0 {
		http.Redirect(w, r, "/account", http.StatusSeeOther)
		return
	}
	vd := a.base(w, r, "")
	vd["Title"] = vd.T("auth.login.title")
	vd["Email"] = r.URL.Query().Get("email")
	vd["Error"] = ""
	a.render(w, r, "login", vd)
}

func (a *App) handleLoginSubmit(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	payload := map[string]any{
		"email":    strings.TrimSpace(r.FormValue("email")),
		"password": r.FormValue("password"),
		"code":     strings.TrimSpace(r.FormValue("code")),
	}
	status, env := a.callAPI(w, r, api.LoginHandler(a.c), payload)
	if status >= 400 {
		vd := a.base(w, r, "")
		vd["Title"] = vd.T("auth.login.title")
		vd["Email"] = payload["email"]
		vd["Error"] = envError(env)
		w.WriteHeader(http.StatusOK)
		a.render(w, r, "login", vd)
		return
	}
	next := r.URL.Query().Get("next")
	if next == "" || !strings.HasPrefix(next, "/") {
		next = "/account"
	}
	data := envData(env)
	if u, ok := data["user"].(map[string]any); ok && auth.IsStaff(str(u["role"])) && next == "/account" {
		next = "/admin"
	}
	http.Redirect(w, r, next, http.StatusSeeOther)
}

func (a *App) handleRegisterPage(w http.ResponseWriter, r *http.Request) {
	vd := a.base(w, r, "")
	vd["Title"] = vd.T("auth.register.title")
	vd["Referral"] = r.URL.Query().Get("ref")
	vd["Error"] = ""
	a.render(w, r, "register", vd)
}

func (a *App) handleRegisterSubmit(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	payload := map[string]any{
		"email":        strings.TrimSpace(r.FormValue("email")),
		"password":     r.FormValue("password"),
		"firstName":    r.FormValue("firstName"),
		"lastName":     r.FormValue("lastName"),
		"phone":        r.FormValue("phone"),
		"referralCode": r.FormValue("referral"),
	}
	status, env := a.callAPI(w, r, api.RegisterHandler(a.c), payload)
	if status >= 400 {
		vd := a.base(w, r, "")
		vd["Title"] = vd.T("auth.register.title")
		vd["Referral"] = r.FormValue("referral")
		vd["Error"] = envError(env)
		w.WriteHeader(http.StatusOK)
		a.render(w, r, "register", vd)
		return
	}
	http.Redirect(w, r, "/account", http.StatusSeeOther)
}

func (a *App) handleLogout(w http.ResponseWriter, r *http.Request) {
	for _, name := range []string{a.c.Cfg.SessionCookieName, "ecms_refresh"} {
		http.SetCookie(w, &http.Cookie{
			Name: name, Value: "", Path: "/", MaxAge: -1,
			HttpOnly: true, Secure: a.c.Cfg.CookieSecure,
		})
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (a *App) handleForgot(w http.ResponseWriter, r *http.Request) {
	vd := a.base(w, r, "")
	vd["Title"] = vd.T("auth.forgot")
	vd["Code"] = 200
	vd["Message"] = "POST " + a.c.Cfg.APIPrefix + "/auth/forgot-password {email} — a reset token is issued via the API."
	a.render(w, r, "error", vd)
}

type accountTab struct {
	Key    string
	Label  string
	Active bool
}

func (a *App) handleAccount(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := middleware.UserID(ctx)
	if uid == 0 {
		http.Redirect(w, r, "/login?next="+r.URL.Path, http.StatusSeeOther)
		return
	}
	tab := r.PathValue("tab")
	if tab == "" {
		tab = "dashboard"
	}
	vd := a.base(w, r, "")
	loc := str(vd["Loc"])
	cur := a.c.Cfg.DefaultCurrency
	money := func(n int) string { return i18n.Money(loc, cur, int64(n)) }

	tabs := []accountTab{
		{"dashboard", vd.T("account.dashboard"), false},
		{"orders", vd.T("account.orders"), false},
		{"wallet", vd.T("account.wallet"), false},
		{"loyalty", vd.T("account.loyalty"), false},
		{"wishlist", vd.T("nav.wishlist"), false},
		{"addresses", vd.T("account.addresses"), false},
		{"returns", vd.T("account.returns"), false},
		{"subscriptions", vd.T("account.subscriptions"), false},
		{"giftcards", vd.T("account.giftcards"), false},
		{"referral", vd.T("account.referral"), false},
		{"notifications", vd.T("account.notifications"), false},
	}
	for i := range tabs {
		tabs[i].Active = tabs[i].Key == tab
	}

	orders, _ := a.c.DB.Count(ctx, `SELECT COUNT(*) FROM orders WHERE user_id=?`, uid)
	spent, _ := a.c.DB.Count(ctx,
		`SELECT COALESCE(SUM(grand_total),0) FROM orders WHERE user_id=? AND payment_status='paid'`, uid)
	balance := api.WalletBalance(ctx, a.c, uid)
	points, _ := a.c.DB.Count(ctx, `SELECT COALESCE(points,0) FROM loyalty_accounts WHERE user_id=?`, uid)

	vd["Title"] = vd.T("nav.account")
	vd["Tabs"] = tabs
	vd["Cards"] = []map[string]any{
		{"Label": vd.T("account.orders"), "Value": i18n.Num(loc, int64(orders))},
		{"Label": vd.T("common.total"), "Value": money(spent)},
		{"Label": vd.T("account.wallet"), "Value": money(balance)},
		{"Label": vd.T("account.loyalty"), "Value": i18n.Num(loc, int64(points))},
	}

	var cols []string
	var rows [][]any
	title := vd.T("account.dashboard")

	switch tab {
	case "orders", "dashboard":
		title = vd.T("account.orders")
		cols = []string{"#", vd.T("common.date"), vd.T("common.status"), vd.T("common.total")}
		list, _ := a.c.DB.Maps(ctx,
			`SELECT number,created_at,status,grand_total FROM orders WHERE user_id=? ORDER BY id DESC LIMIT 25`, uid)
		for _, o := range list {
			link := template.HTML(`<a class="link font-mono" href="/order/` +
				template.HTMLEscapeString(str(o["number"])) + `">` +
				template.HTMLEscapeString(str(o["number"])) + `</a>`)
			rows = append(rows, []any{link, shortDate(str(o["created_at"])),
				badge(str(o["status"])), money(num(o["grand_total"]))})
		}
	case "wallet":
		title = vd.T("account.wallet")
		cols = []string{vd.T("common.date"), vd.T("common.type"), vd.T("common.total"), "balance"}
		list, _ := a.c.DB.Maps(ctx,
			`SELECT created_at,type,amount,balance_after FROM wallet_transactions WHERE user_id=? ORDER BY id DESC LIMIT 30`, uid)
		for _, t := range list {
			rows = append(rows, []any{shortDate(str(t["created_at"])), t["type"], money(num(t["amount"])), money(num(t["balance_after"]))})
		}
	case "loyalty":
		title = vd.T("account.loyalty")
		cols = []string{vd.T("common.date"), "points", vd.T("common.description"), "expires"}
		list, _ := a.c.DB.Maps(ctx,
			`SELECT created_at,points,reason,expires_at FROM loyalty_transactions WHERE user_id=? ORDER BY id DESC LIMIT 30`, uid)
		for _, t := range list {
			rows = append(rows, []any{shortDate(str(t["created_at"])), t["points"], t["reason"], shortDate(str(t["expires_at"]))})
		}
	case "wishlist":
		title = vd.T("nav.wishlist")
		cols = []string{vd.T("common.name"), vd.T("common.price"), vd.T("common.actions")}
		list, _ := a.c.DB.Maps(ctx, `SELECT p.slug,p.name,p.name_en,p.price FROM wishlist_items wi
			JOIN products p ON p.id=wi.product_id WHERE wi.user_id=? ORDER BY wi.id DESC`, uid)
		for _, p := range list {
			rows = append(rows, []any{localized(p, "name", loc), money(num(p["price"])),
				template.HTML(`<a class="link" href="/product/` + template.HTMLEscapeString(str(p["slug"])) +
					`">` + template.HTMLEscapeString(vd.T("common.view")) + `</a>`)})
		}
	case "addresses":
		title = vd.T("account.addresses")
		cols = []string{vd.T("common.name"), vd.T("common.phone"), "city", "address"}
		list, _ := a.c.DB.Maps(ctx,
			`SELECT recipient,phone,city,line1 FROM addresses WHERE user_id=? AND deleted_at IS NULL`, uid)
		for _, ad := range list {
			rows = append(rows, []any{ad["recipient"], ad["phone"], ad["city"], ad["line1"]})
		}
	case "returns":
		title = vd.T("account.returns")
		cols = []string{"#", vd.T("common.date"), vd.T("common.status"), vd.T("common.total")}
		list, _ := a.c.DB.Maps(ctx,
			`SELECT number,created_at,status,refund_amount FROM returns WHERE user_id=? ORDER BY id DESC`, uid)
		for _, rr := range list {
			rows = append(rows, []any{rr["number"], shortDate(str(rr["created_at"])), badge(str(rr["status"])), money(num(rr["refund_amount"]))})
		}
	case "subscriptions":
		title = vd.T("account.subscriptions")
		cols = []string{vd.T("common.name"), vd.T("common.status"), "renews", vd.T("common.price")}
		list, _ := a.c.DB.Maps(ctx, `SELECT p.name,p.name_en,s.status,s.renews_at,p.price
			FROM subscriptions s JOIN subscription_plans p ON p.id=s.plan_id WHERE s.user_id=? ORDER BY s.id DESC`, uid)
		for _, s := range list {
			rows = append(rows, []any{localized(s, "name", loc), badge(str(s["status"])), shortDate(str(s["renews_at"])), money(num(s["price"]))})
		}
	case "giftcards":
		title = vd.T("account.giftcards")
		cols = []string{vd.T("common.code"), "balance", vd.T("common.status"), "expires"}
		list, _ := a.c.DB.Maps(ctx,
			`SELECT code,balance,status,expires_at FROM gift_cards WHERE owner_id=? OR redeemed_by=? ORDER BY id DESC`, uid, uid)
		for _, g := range list {
			rows = append(rows, []any{g["code"], money(num(g["balance"])), badge(str(g["status"])), shortDate(str(g["expires_at"]))})
		}
	case "referral":
		title = vd.T("account.referral")
		cols = []string{vd.T("common.email"), vd.T("common.status"), "reward", vd.T("common.date")}
		list, _ := a.c.DB.Maps(ctx, `SELECT COALESCE(u.email,'—') AS email,r.status,r.reward,r.created_at
			FROM referrals r LEFT JOIN users u ON u.id=r.referee_id WHERE r.referrer_id=? ORDER BY r.id DESC`, uid)
		for _, rf := range list {
			rows = append(rows, []any{rf["email"], badge(str(rf["status"])), money(num(rf["reward"])), shortDate(str(rf["created_at"]))})
		}
		code, _ := a.c.DB.MapOne(ctx, `SELECT referral_code FROM users WHERE id=?`, uid)
		vd["PanelAction"] = map[string]string{
			"Label": str(code["referral_code"]),
			"URL":   "/register?ref=" + str(code["referral_code"]),
		}
	case "notifications":
		title = vd.T("account.notifications")
		cols = []string{vd.T("common.date"), vd.T("common.type"), vd.T("common.name")}
		list, _ := a.c.DB.Maps(ctx,
			`SELECT created_at,type,title FROM notifications WHERE user_id=? ORDER BY id DESC LIMIT 40`, uid)
		for _, n := range list {
			rows = append(rows, []any{shortDate(str(n["created_at"])), n["type"], n["title"]})
		}
	}

	vd["PanelTitle"] = title
	vd["Columns"] = cols
	vd["Rows"] = rows
	a.render(w, r, "account", vd)
}

func (a *App) handleWishlistAdd(w http.ResponseWriter, r *http.Request) {
	uid := middleware.UserID(r.Context())
	if uid == 0 {
		a.fragment(w, false, "Please sign in first.")
		return
	}
	_, _ = a.c.DB.Write(r.Context(),
		`INSERT OR IGNORE INTO wishlist_items(user_id,product_id) VALUES(?,?)`, uid, r.PathValue("id"))
	a.fragment(w, true, "Added to wishlist")
}

func (a *App) handleCompareAdd(w http.ResponseWriter, r *http.Request) {
	token := ""
	if ck, err := r.Cookie(api.CompareCookie); err == nil {
		token = ck.Value
	}
	if token == "" {
		token = auth.RandomToken(12)
		http.SetCookie(w, &http.Cookie{
			Name: api.CompareCookie, Value: token, Path: "/", MaxAge: 7 * 24 * 3600,
			HttpOnly: true, Secure: a.c.Cfg.CookieSecure, SameSite: http.SameSiteLaxMode,
		})
	}
	_, _ = a.c.DB.Write(r.Context(),
		`INSERT OR IGNORE INTO compare_items(token,product_id) VALUES(?,?)`, token, r.PathValue("id"))
	a.fragment(w, true, "Added to compare")
}

func badge(status string) template.HTML {
	if status == "" {
		return template.HTML("—")
	}
	return template.HTML(`<span class="badge ` + statusClass(status) + `">` +
		template.HTMLEscapeString(status) + `</span>`)
}
