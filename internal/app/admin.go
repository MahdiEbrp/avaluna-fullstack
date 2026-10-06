package app

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/arena/avaluna/internal/api"
	"github.com/arena/avaluna/internal/auth"
	"github.com/arena/avaluna/internal/i18n"
	"github.com/arena/avaluna/internal/middleware"
)

func (a *App) requireStaff(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		role := middleware.Role(r.Context())
		if !auth.IsStaff(role) {
			if middleware.UserID(r.Context()) == 0 {
				http.Redirect(w, r, "/login?next="+r.URL.Path, http.StatusSeeOther)
				return
			}
			vd := a.base(w, r, "403")
			vd["Code"] = 403
			vd["Message"] = "Staff access required"
			w.WriteHeader(http.StatusForbidden)
			a.render(w, r, "error", vd)
			return
		}
		h(w, r)
	}
}

type navItem struct {
	Section string
	Label   string
	Icon    string
	URL     string
	Active  bool
}

func (a *App) adminBase(w http.ResponseWriter, r *http.Request, title, section string) VD {
	vd := a.base(w, r, title)
	nav := []navItem{
		{Section: "Commerce"},
		{Label: vd.T("admin.dashboard"), Icon: "📊", URL: "/admin"},
		{Label: vd.T("admin.orders"), Icon: "🧾", URL: "/admin/orders"},
		{Label: vd.T("admin.products"), Icon: "📦", URL: "/admin/products"},
		{Label: vd.T("admin.inventory"), Icon: "🏭", URL: "/admin/inventory"},
		{Label: vd.T("admin.customers"), Icon: "👥", URL: "/admin/customers"},
		{Label: vd.T("admin.coupons"), Icon: "🏷️", URL: "/admin/coupons"},
		{Label: vd.T("admin.vendors"), Icon: "🏬", URL: "/admin/vendors"},
		{Label: vd.T("admin.returns"), Icon: "↩️", URL: "/admin/returns"},
		{Section: "Content"},
		{Label: vd.T("admin.blog"), Icon: "📝", URL: "/admin/blog"},
		{Label: vd.T("admin.reviews"), Icon: "⭐", URL: "/admin/reviews"},
		{Label: vd.T("admin.media"), Icon: "🖼️", URL: "/admin/media"},
		{Section: "Intelligence"},
		{Label: vd.T("admin.reports"), Icon: "📈", URL: "/admin/reports"},
		{Label: vd.T("admin.analytics"), Icon: "🔬", URL: "/admin/analytics"},
		{Label: vd.T("admin.audit"), Icon: "🛡️", URL: "/admin/audit"},
		{Section: "Operations"},
		{Label: vd.T("admin.cron"), Icon: "⏱️", URL: "/admin/cron"},
		{Label: vd.T("admin.queue"), Icon: "📬", URL: "/admin/queue"},
		{Label: vd.T("admin.webhooks"), Icon: "🔗", URL: "/admin/webhooks"},
		{Label: vd.T("admin.logs"), Icon: "🐞", URL: "/admin/logs"},
		{Label: vd.T("admin.settings"), Icon: "⚙️", URL: "/admin/settings"},
	}
	for i := range nav {
		if nav[i].URL != "" && nav[i].URL == r.URL.Path {
			nav[i].Active = true
		}
	}
	vd["Nav"] = nav
	vd["Section"] = section
	vd["Uptime"] = a.c.Uptime().Truncate(time.Second).String()
	if _, ok := vd["User"]; !ok {
		vd["User"] = map[string]any{"Email": "", "FirstName": "", "Role": ""}
	}
	return vd
}

func (a *App) handleAdminDashboard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	vd := a.adminBase(w, r, "", "dashboard")
	loc := str(vd["Loc"])
	cur := a.c.Cfg.DefaultCurrency
	money := func(n int) string { return i18n.Money(loc, cur, int64(n)) }

	stats := api.DashboardStats(ctx, a.c)
	vd["Title"] = vd.T("admin.dashboard")
	vd["Kpis"] = []map[string]any{
		{"Label": vd.T("admin.revenue"), "Icon": "💰", "Value": money(num(stats["revenueTotal"])),
			"Hint": "today " + money(num(stats["revenueToday"]))},
		{"Label": vd.T("admin.orders"), "Icon": "🧾", "Value": i18n.Num(loc, int64(num(stats["ordersTotal"]))),
			"Hint": "pending " + fmt.Sprint(num(stats["ordersPending"]))},
		{"Label": vd.T("admin.customers"), "Icon": "👥", "Value": i18n.Num(loc, int64(num(stats["customers"]))),
			"Hint": "new 30d " + fmt.Sprint(num(stats["customersNew"]))},
		{"Label": vd.T("admin.products"), "Icon": "📦", "Value": i18n.Num(loc, int64(num(stats["products"]))),
			"Hint": "low stock " + fmt.Sprint(num(stats["lowStock"]))},
	}

	series, _ := a.c.DB.Maps(ctx, `SELECT DATE(created_at) AS d, COALESCE(SUM(grand_total),0) AS v
		FROM orders WHERE payment_status='paid' AND created_at >= datetime('now','-14 days')
		GROUP BY DATE(created_at) ORDER BY d`)
	maxV := 1
	for _, row := range series {
		if num(row["v"]) > maxV {
			maxV = num(row["v"])
		}
	}
	chart := make([]map[string]any, 0, len(series))
	for _, row := range series {
		h := num(row["v"]) * 170 / maxV
		if h < 3 {
			h = 3
		}
		chart = append(chart, map[string]any{
			"Height": h, "Label": str(row["d"]), "Value": money(num(row["v"])),
		})
	}
	vd["Chart"] = chart

	dbOK := "up"
	dbClass := "bg-emerald-100 text-emerald-700"
	if err := a.c.DB.Healthy(ctx); err != nil {
		dbOK, dbClass = "down", "bg-rose-100 text-rose-700"
	}
	cronJobs, _ := a.c.DB.Count(ctx, `SELECT COUNT(*) FROM cron_jobs WHERE enabled=1`)
	failedJobs, _ := a.c.DB.Count(ctx, `SELECT COUNT(*) FROM queue_jobs WHERE status='failed'`)
	vd["Health"] = []map[string]any{
		{"Label": "database", "Value": dbOK, "Class": dbClass},
		{"Label": "scheduler", "Value": fmt.Sprintf("%d jobs", cronJobs), "Class": "bg-cyan-100 text-cyan-700"},
		{"Label": "queue failed", "Value": fmt.Sprint(failedJobs),
			"Class": map[bool]string{true: "bg-rose-100 text-rose-700", false: "bg-emerald-100 text-emerald-700"}[failedJobs > 0]},
		{"Label": "uptime", "Value": str(vd["Uptime"]), "Class": "bg-slate-100 text-slate-700"},
		{"Label": "cache", "Value": fmt.Sprintf("%d keys", a.c.CacheSize()), "Class": "bg-slate-100 text-slate-700"},
		{"Label": "version", "Value": api.Version, "Class": "bg-slate-100 text-slate-700"},
	}

	recent, _ := a.c.DB.Maps(ctx,
		`SELECT number,email,status,grand_total FROM orders ORDER BY id DESC LIMIT 8`)
	for _, o := range recent {
		o["total"] = money(num(o["grand_total"]))
		o["statusClass"] = statusClass(str(o["status"]))
	}
	vd["RecentOrders"] = recent

	low, _ := a.c.DB.Maps(ctx, `SELECT p.name, v.sku, i.quantity FROM inventory i
		JOIN product_variants v ON v.id=i.variant_id JOIN products p ON p.id=v.product_id
		WHERE i.quantity <= i.reorder_level ORDER BY i.quantity LIMIT 8`)
	vd["LowStock"] = low

	counts := map[string]int{}
	for _, rt := range a.rt.Routes() {
		counts[rt.Module]++
	}
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	mods := make([]map[string]any, 0, len(keys))
	for _, k := range keys {
		mods = append(mods, map[string]any{"Name": k, "Routes": counts[k]})
	}
	vd["Modules"] = mods
	a.render(w, r, "admin/dashboard", vd)
}

func statusClass(s string) string {
	switch s {
	case "delivered", "paid", "success", "approved", "active", "completed":
		return "bg-emerald-100 text-emerald-700"
	case "pending", "processing", "requested", "waiting", "running":
		return "bg-amber-100 text-amber-800"
	case "cancelled", "failed", "rejected", "refunded":
		return "bg-rose-100 text-rose-700"
	case "shipped":
		return "bg-cyan-100 text-cyan-700"
	default:
		return "bg-slate-100 text-slate-600"
	}
}

func (a *App) handleAdminSection(w http.ResponseWriter, r *http.Request) {
	section := r.PathValue("section")
	switch section {
	case "cron":
		a.adminCron(w, r)
		return
	case "reports":
		a.adminReports(w, r)
		return
	case "settings":
		a.adminSettings(w, r)
		return
	}

	ctx := r.Context()
	vd := a.adminBase(w, r, "", section)
	loc := str(vd["Loc"])
	cur := a.c.Cfg.DefaultCurrency
	money := func(n int) string { return i18n.Money(loc, cur, int64(n)) }
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	st := r.URL.Query().Get("status")
	like := "%" + q + "%"

	var (
		heading  string
		cols     []string
		base     string
		where    []string
		args     []any
		order    = "id DESC"
		statuses []string
		export   string
		project  func(map[string]any) []any
	)

	switch section {
	case "orders":
		heading, cols = vd.T("admin.orders"), []string{"#", vd.T("common.email"), vd.T("common.status"), "payment", vd.T("common.total"), vd.T("common.date")}
		base = "orders"
		statuses = []string{"pending", "paid", "processing", "shipped", "delivered", "cancelled", "refunded"}
		export = "/api/v1/admin/export/orders"
		if q != "" {
			where, args = append(where, "(number LIKE ? OR email LIKE ?)"), append(args, like, like)
		}
		project = func(m map[string]any) []any {
			return []any{m["number"], m["email"], badge(str(m["status"])), badge(str(m["payment_status"])),
				money(num(m["grand_total"])), shortDate(str(m["created_at"]))}
		}
	case "products":
		heading, cols = vd.T("admin.products"), []string{"SKU", vd.T("common.name"), vd.T("common.price"), vd.T("common.status"), "sold", vd.T("product.rating")}
		base = "products"
		statuses = []string{"draft", "published", "archived"}
		export = "/api/v1/admin/export/products"
		where = append(where, "deleted_at IS NULL")
		if q != "" {
			where, args = append(where, "(name LIKE ? OR name_en LIKE ? OR sku LIKE ?)"), append(args, like, like, like)
		}
		project = func(m map[string]any) []any {
			return []any{m["sku"], localized(m, "name", loc), money(num(m["price"])), badge(str(m["status"])),
				m["sold_count"], fmt.Sprintf("%.1f", flt(m["rating_avg"]))}
		}
	case "customers":
		heading, cols = vd.T("admin.customers"), []string{vd.T("common.email"), vd.T("common.name"), "role", vd.T("common.status"), vd.T("common.date")}
		base = "users"
		statuses = []string{"active", "banned", "pending"}
		export = "/api/v1/admin/export/customers"
		where = append(where, "deleted_at IS NULL")
		if q != "" {
			where, args = append(where, "(email LIKE ? OR first_name LIKE ? OR last_name LIKE ?)"), append(args, like, like, like)
		}
		project = func(m map[string]any) []any {
			return []any{m["email"], str(m["first_name"]) + " " + str(m["last_name"]),
				m["role"], badge(str(m["status"])), shortDate(str(m["created_at"]))}
		}
	case "inventory":
		heading, cols = vd.T("admin.inventory"), []string{"SKU", vd.T("common.name"), "warehouse", vd.T("common.quantity"), "reserved", "reorder"}
		base = `inventory i JOIN product_variants v ON v.id=i.variant_id
			JOIN products p ON p.id=v.product_id JOIN warehouses wh ON wh.id=i.warehouse_id`
		order = "i.quantity ASC"
		if q != "" {
			where, args = append(where, "(v.sku LIKE ? OR p.name LIKE ?)"), append(args, like, like)
		}
		project = func(m map[string]any) []any {
			return []any{m["sku"], localized(m, "name", loc), m["warehouse"], m["quantity"], m["reserved"], m["reorder_level"]}
		}
	case "coupons":
		heading, cols = vd.T("admin.coupons"), []string{vd.T("common.code"), vd.T("common.type"), "value", "used", "limit", "expires"}
		base = "coupons"
		export = "/api/v1/admin/export/coupons"
		if q != "" {
			where, args = append(where, "code LIKE ?"), append(args, like)
		}
		project = func(m map[string]any) []any {
			return []any{m["code"], m["type"], m["value"], m["used_count"], m["usage_limit"], shortDate(str(m["expires_at"]))}
		}
	case "vendors":
		heading, cols = vd.T("admin.vendors"), []string{vd.T("common.name"), vd.T("common.status"), "commission", "balance", vd.T("product.rating")}
		base = "vendors"
		statuses = []string{"pending", "approved", "suspended"}
		export = "/api/v1/admin/export/vendors"
		if q != "" {
			where, args = append(where, "(shop_name LIKE ? OR slug LIKE ?)"), append(args, like, like)
		}
		project = func(m map[string]any) []any {
			return []any{localized(m, "shop_name", loc), badge(str(m["status"])),
				fmt.Sprintf("%.1f%%", flt(m["commission_rate"])), money(num(m["balance"])),
				fmt.Sprintf("%.1f", flt(m["rating"]))}
		}
	case "returns":
		heading, cols = vd.T("admin.returns"), []string{"#", "order", vd.T("common.status"), "refund", vd.T("common.date")}
		base = "returns"
		statuses = []string{"requested", "approved", "rejected", "refunded"}
		project = func(m map[string]any) []any {
			return []any{m["number"], m["order_id"], badge(str(m["status"])), money(num(m["refund_amount"])), shortDate(str(m["created_at"]))}
		}
	case "reviews":
		heading, cols = vd.T("admin.reviews"), []string{"product", vd.T("product.rating"), vd.T("common.name"), vd.T("common.status"), vd.T("common.date")}
		base = "reviews r JOIN products p ON p.id=r.product_id"
		statuses = []string{"pending", "approved", "rejected"}
		export = "/api/v1/admin/export/reviews"
		if q != "" {
			where, args = append(where, "(r.title LIKE ? OR p.name LIKE ?)"), append(args, like, like)
		}
		project = func(m map[string]any) []any {
			return []any{localized(m, "name", loc), stars(flt(m["rating"])), m["title"], badge(str(m["status"])), shortDate(str(m["created_at"]))}
		}
	case "blog":
		heading, cols = vd.T("admin.blog"), []string{vd.T("common.name"), "slug", vd.T("common.status"), "views", vd.T("common.date")}
		base = "blog_posts"
		statuses = []string{"draft", "published"}
		export = "/api/v1/admin/export/blog-posts"
		where = append(where, "deleted_at IS NULL")
		if q != "" {
			where, args = append(where, "(title LIKE ? OR slug LIKE ?)"), append(args, like, like)
		}
		project = func(m map[string]any) []any {
			return []any{localized(m, "title", loc), m["slug"], badge(str(m["status"])), m["views"], shortDate(str(m["published_at"]))}
		}
	case "media":
		heading, cols = vd.T("admin.media"), []string{vd.T("common.name"), "mime", "size", "url", vd.T("common.date")}
		base = "media"
		if q != "" {
			where, args = append(where, "filename LIKE ?"), append(args, like)
		}
		project = func(m map[string]any) []any {
			return []any{m["filename"], m["mime"], fmt.Sprintf("%d KB", num(m["size_bytes"])/1024),
				m["url"], shortDate(str(m["created_at"]))}
		}
	case "webhooks":
		heading, cols = vd.T("admin.webhooks"), []string{vd.T("common.name"), "url", "events", vd.T("common.status"), "failures"}
		base = "webhooks"
		project = func(m map[string]any) []any {
			state := "disabled"
			if num(m["active"]) == 1 {
				state = "active"
			}
			return []any{m["name"], m["url"], m["events"], badge(state), m["failures"]}
		}
	case "queue":
		heading, cols = vd.T("admin.queue"), []string{"#", "queue", vd.T("common.name"), vd.T("common.status"), "attempts", "ms"}
		base = "queue_jobs"
		statuses = []string{"waiting", "active", "completed", "failed", "delayed"}
		project = func(m map[string]any) []any {
			return []any{m["id"], m["queue"], m["name"], badge(str(m["status"])),
				fmt.Sprintf("%d/%d", num(m["attempts"]), num(m["max_attempts"])), m["duration_ms"]}
		}
	case "logs":
		heading, cols = vd.T("admin.logs"), []string{vd.T("common.date"), "level", vd.T("common.code"), "message", "path"}
		base = "error_log"
		if q != "" {
			where, args = append(where, "message LIKE ?"), append(args, like)
		}
		project = func(m map[string]any) []any {
			return []any{shortDate(str(m["created_at"])), badge(str(m["level"])), m["code"],
				truncate(str(m["message"]), 90), m["path"]}
		}
	case "audit":
		heading, cols = vd.T("admin.audit"), []string{vd.T("common.date"), "actor", "action", "entity", "ip"}
		base = "audit_log"
		if q != "" {
			where, args = append(where, "action LIKE ?"), append(args, like)
		}
		project = func(m map[string]any) []any {
			return []any{shortDate(str(m["created_at"])), m["actor_role"], m["action"],
				str(m["entity"]) + "#" + str(m["entity_id"]), m["ip"]}
		}
	case "analytics":
		heading, cols = vd.T("admin.analytics"), []string{vd.T("common.name"), "source", "count", "value"}
		base = "analytics_events"
		order = "COUNT(*) DESC"
		project = func(m map[string]any) []any {
			return []any{m["name"], m["source"], m["n"], m["v"]}
		}
	default:
		a.notFound(w, r)
		return
	}

	if st != "" && len(statuses) > 0 {
		col := "status"
		if section == "reviews" {
			col = "r.status"
		}
		where, args = append(where, col+"=?"), append(args, st)
	}
	clause := ""
	if len(where) > 0 {
		clause = " WHERE " + strings.Join(where, " AND ")
	}

	page := qInt(r, "page", 1)
	if page < 1 {
		page = 1
	}
	var rows []map[string]any
	total := 0

	if section == "analytics" {
		rows, _ = a.c.DB.Maps(ctx, `SELECT name, source, COUNT(*) AS n, COALESCE(SUM(value),0) AS v
			FROM analytics_events GROUP BY name, source ORDER BY n DESC LIMIT 50`)
		total = len(rows)
	} else {
		sel := "*"
		switch section {
		case "inventory":
			sel = "v.sku, p.name, p.name_en, wh.name AS warehouse, i.quantity, i.reserved, i.reorder_level"
		case "reviews":
			sel = "r.*, p.name, p.name_en"
		}
		total, _ = a.c.DB.Count(ctx, "SELECT COUNT(*) FROM "+base+clause, args...)
		rows, _ = a.c.DB.Maps(ctx,
			fmt.Sprintf("SELECT %s FROM %s%s ORDER BY %s LIMIT ? OFFSET ?", sel, base, clause, order),
			append(append([]any{}, args...), perPage*2, (page-1)*perPage*2)...)
	}

	out := make([][]any, 0, len(rows))
	for _, row := range rows {
		out = append(out, project(row))
	}

	vd["Title"] = heading
	vd["Heading"] = heading
	vd["Columns"] = cols
	vd["Rows"] = out
	vd["Query"] = q
	vd["Status"] = st
	vd["StatusOptions"] = statuses
	vd["ExportURL"] = export
	pg := a.pager(r, page, total, vd)
	pg["Pages"] = (total + perPage*2 - 1) / (perPage * 2)
	vd["Pager"] = pg
	a.render(w, r, "admin/table", vd)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func (a *App) adminCron(w http.ResponseWriter, r *http.Request) {
	vd := a.adminBase(w, r, "", "cron")
	jobs, _ := a.c.DB.Maps(r.Context(), `SELECT * FROM cron_jobs ORDER BY name`)
	for _, j := range jobs {
		j["enabledBool"] = num(j["enabled"]) == 1
		j["statusClass"] = statusClass(str(j["last_status"]))
		j["last_run_at"] = shortDate(str(j["last_run_at"]))
	}
	vd["Title"] = vd.T("admin.cron")
	vd["Jobs"] = jobs
	vd["SchedulerUp"] = a.c.Cron != nil
	a.render(w, r, "admin/cron", vd)
}

func (a *App) handleAdminCronAction(w http.ResponseWriter, r *http.Request) {
	name, action := r.PathValue("name"), r.PathValue("action")
	if a.c.Cron == nil {
		a.fragment(w, false, "Scheduler is not running")
		return
	}
	switch action {
	case "run":
		out, err := a.c.Cron.RunNow(r.Context(), name)
		if err != nil {
			a.fragment(w, false, name+": "+err.Error())
			return
		}
		a.fragment(w, true, name+" → "+out)
	case "pause", "resume":
		if err := a.c.Cron.SetEnabled(r.Context(), name, action == "resume"); err != nil {
			a.fragment(w, false, err.Error())
			return
		}
		a.fragment(w, true, name+" "+action+"d — reload to refresh the table")
	default:
		a.fragment(w, false, "unknown action")
	}
}

func (a *App) adminReports(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	vd := a.adminBase(w, r, "", "reports")
	key := r.URL.Query().Get("key")
	keys := api.ReportKeys()
	if key == "" && len(keys) > 0 {
		key = keys[0]
	}
	items := make([]map[string]any, 0, len(keys))
	for _, k := range keys {
		items = append(items, map[string]any{
			"Key": k, "Title": strings.ReplaceAll(k, "-", " "), "Active": k == key,
		})
	}
	from := r.URL.Query().Get("from")
	to := r.URL.Query().Get("to")

	rows, meta, err := api.RunReportByKey(ctx, a.c, key, from, to, 500)
	cols := []string{}
	out := [][]any{}
	if err == nil && len(rows) > 0 {
		for k := range rows[0] {
			cols = append(cols, k)
		}
		sort.Strings(cols)
		for _, row := range rows {
			line := make([]any, 0, len(cols))
			for _, cName := range cols {
				line = append(line, row[cName])
			}
			out = append(out, line)
		}
	}
	desc := ""
	if err != nil {
		desc = "error: " + err.Error()
	} else if meta != nil {
		desc = fmt.Sprintf("%v — %v rows", meta["title"], meta["rows"])
	}

	vd["Title"] = vd.T("admin.reports")
	vd["Heading"] = strings.ReplaceAll(key, "-", " ")
	vd["Reports"] = items
	vd["Key"] = key
	vd["From"] = from
	vd["To"] = to
	vd["Columns"] = cols
	vd["Rows"] = out
	vd["Description"] = desc
	a.render(w, r, "admin/reports", vd)
}

var editableSettings = []string{
	"site_name", "site_name_en", "site_tagline", "site_tagline_en", "site_url",
	"support_email", "support_phone", "free_shipping_over", "low_stock_warn",
	"loyalty_points_per_unit", "loyalty_value_per_point", "referral_reward",
	"unpaid_order_window_minutes", "abandoned_cart_hours", "backup_keep",
	"log_retention_days", "subscription_max_failures", "review_auto_approve",
}

func (a *App) adminSettings(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	vd := a.adminBase(w, r, "", "settings")

	items := make([]map[string]string, 0, len(editableSettings))
	for _, k := range editableSettings {
		items = append(items, map[string]string{"Key": k, "Value": a.c.Setting(k, "")})
	}
	flags, _ := a.c.DB.Maps(ctx, `SELECT key,description,enabled,rollout FROM feature_flags ORDER BY key`)
	for _, f := range flags {
		f["enabledBool"] = num(f["enabled"]) == 1
	}
	qrows, _ := a.c.DB.Maps(ctx, `SELECT queue, status, COUNT(*) AS n FROM queue_jobs GROUP BY queue, status`)
	agg := map[string]map[string]int{}
	for _, row := range qrows {
		qn := str(row["queue"])
		if agg[qn] == nil {
			agg[qn] = map[string]int{}
		}
		agg[qn][str(row["status"])] = num(row["n"])
	}
	names := make([]string, 0, len(agg))
	for k := range agg {
		names = append(names, k)
	}
	sort.Strings(names)
	queues := make([]map[string]any, 0, len(names))
	for _, n := range names {
		queues = append(queues, map[string]any{
			"Name": n, "Waiting": agg[n]["waiting"], "Active": agg[n]["active"],
			"Completed": agg[n]["completed"], "Failed": agg[n]["failed"],
		})
	}

	vd["Title"] = vd.T("admin.settings")
	vd["Settings"] = items
	vd["Flags"] = flags
	vd["Queues"] = queues
	a.render(w, r, "admin/settings", vd)
}

func (a *App) handleAdminSettingsSave(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	n := 0
	for _, k := range editableSettings {
		if v := r.FormValue(k); v != "" {
			if err := a.c.SetSetting(r.Context(), k, v, "public"); err == nil {
				n++
			}
		}
	}
	a.fragment(w, true, fmt.Sprintf("Saved %d settings", n))
}

func (a *App) handleAdminFlagToggle(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	if _, err := a.c.DB.Write(r.Context(),
		`UPDATE feature_flags SET enabled = 1 - enabled, updated_at=datetime('now') WHERE key=?`, key); err != nil {
		a.fragment(w, false, err.Error())
		return
	}
	var enabled int
	_ = a.c.DB.Row(r.Context(), `SELECT enabled FROM feature_flags WHERE key=?`, []any{key}, &enabled)
	a.fragment(w, true, fmt.Sprintf("%s → %v (reload to refresh)", key, enabled == 1))
}

func (a *App) handleAdminSystemAction(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	switch r.PathValue("action") {
	case "cache-clear":
		n := a.c.CacheClear("")
		_ = a.c.ReloadSettings(ctx)
		a.fragment(w, true, fmt.Sprintf("Cleared %d cache entries", n))
	case "backup":
		out, err := api.CreateBackup(ctx, a.c, "admin-ui")
		if err != nil {
			a.fragment(w, false, err.Error())
			return
		}
		a.fragment(w, true, out)
	case "sitemap":
		n, err := api.RegenerateSitemap(ctx, a.c)
		if err != nil {
			a.fragment(w, false, err.Error())
			return
		}
		a.fragment(w, true, fmt.Sprintf("Sitemap regenerated with %d URLs", n))
	case "reindex":
		out, err := api.RebuildSearchIndex(ctx, a.c)
		if err != nil {
			a.fragment(w, false, err.Error())
			return
		}
		a.fragment(w, true, out)
	case "optimize":
		if _, err := a.c.DB.Write(ctx, "PRAGMA optimize"); err != nil {
			a.fragment(w, false, err.Error())
			return
		}
		_, _ = a.c.DB.Write(ctx, "PRAGMA wal_checkpoint(TRUNCATE)")
		a.fragment(w, true, "Database optimised")
	case "maintenance":
		on := !a.c.MaintenanceMode()
		if err := a.c.SetSetting(ctx, "maintenance_mode", fmt.Sprint(on), "private"); err != nil {
			a.fragment(w, false, err.Error())
			return
		}
		a.fragment(w, true, fmt.Sprintf("Maintenance mode: %v", on))
	default:
		a.fragment(w, false, "unknown action")
	}
}
