package api

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/arena/avaluna/internal/core"
	"github.com/arena/avaluna/internal/httpx"
)

type reportDef struct {
	Key         string
	Title       string
	TitleFa     string
	Query       string
	Description string
	SuperOnly   bool
}

var reportRegistry = []reportDef{
	{Key: "sales", Title: "Sales report", TitleFa: "گزارش فروش",
		Description: "Daily orders and revenue for the selected window",
		Query: `SELECT DATE(created_at) AS date, COUNT(*) AS orders,
		        COALESCE(SUM(grand_total),0) AS revenue,
		        COALESCE(CAST(AVG(grand_total) AS INTEGER),0) AS avg_order_value
		        FROM orders WHERE payment_status='paid' {{RANGE}}
		        GROUP BY DATE(created_at) ORDER BY date DESC`},

	{Key: "revenue", Title: "Gross & net revenue", TitleFa: "درآمد ناخالص و خالص",
		Description: "Revenue split into product, tax, shipping and discount components",
		Query: `SELECT COALESCE(SUM(subtotal),0) AS gross_product,
		        COALESCE(SUM(discount_total),0) AS discounts,
		        COALESCE(SUM(tax_total),0) AS taxes,
		        COALESCE(SUM(shipping_total),0) AS shipping,
		        COALESCE(SUM(grand_total),0) AS gross_total,
		        COALESCE(SUM(grand_total - tax_total - shipping_total),0) AS net_revenue
		        FROM orders WHERE payment_status='paid' {{RANGE}}`},

	{Key: "profit-margin", Title: "Profit margin by product", TitleFa: "حاشیه سود بر اساس محصول",
		Description: "Revenue, cost and margin percentage per product",
		Query: `SELECT oi.name AS product, SUM(oi.quantity) AS units,
		        SUM(oi.total) AS revenue, SUM(oi.cost_price*oi.quantity) AS cost,
		        SUM(oi.total) - SUM(oi.cost_price*oi.quantity) AS profit,
		        ROUND(CASE WHEN SUM(oi.total)=0 THEN 0 ELSE
		          (SUM(oi.total)-SUM(oi.cost_price*oi.quantity))*100.0/SUM(oi.total) END,2) AS margin_percent
		        FROM order_items oi JOIN orders o ON o.id=oi.order_id
		        WHERE o.payment_status='paid' {{RANGE_O}}
		        GROUP BY oi.product_id ORDER BY profit DESC`},

	{Key: "customer-ltv", Title: "Customer lifetime value", TitleFa: "ارزش طول عمر مشتری",
		Description: "Per-customer orders, spend and average order value",
		Query: `SELECT u.id, u.email, COUNT(o.id) AS orders,
		        COALESCE(SUM(o.grand_total),0) AS lifetime_value,
		        COALESCE(CAST(AVG(o.grand_total) AS INTEGER),0) AS avg_order_value,
		        MIN(o.created_at) AS first_order, MAX(o.created_at) AS last_order
		        FROM users u JOIN orders o ON o.user_id=u.id AND o.payment_status='paid'
		        WHERE u.deleted_at IS NULL GROUP BY u.id ORDER BY lifetime_value DESC`},

	{Key: "cohort-analysis", Title: "Cohort retention", TitleFa: "تحلیل کوهورت نگهداشت",
		Description: "Signup-month cohorts with order counts and revenue",
		Query: `SELECT strftime('%Y-%m', u.created_at) AS cohort,
		        COUNT(DISTINCT u.id) AS customers,
		        COUNT(DISTINCT o.user_id) AS purchasers,
		        COALESCE(SUM(o.grand_total),0) AS revenue,
		        ROUND(COUNT(DISTINCT o.user_id)*100.0/COUNT(DISTINCT u.id),1) AS conversion_percent
		        FROM users u LEFT JOIN orders o ON o.user_id=u.id AND o.payment_status='paid'
		        WHERE u.deleted_at IS NULL GROUP BY cohort ORDER BY cohort DESC`},

	{Key: "top-products", Title: "Best sellers", TitleFa: "پرفروش‌ترین محصولات",
		Description: "Units sold and revenue per product",
		Query: `SELECT oi.product_id, oi.name AS product, oi.sku,
		        SUM(oi.quantity) AS units, SUM(oi.total) AS revenue
		        FROM order_items oi JOIN orders o ON o.id=oi.order_id
		        WHERE o.payment_status='paid' {{RANGE_O}}
		        GROUP BY oi.product_id ORDER BY units DESC`},

	{Key: "slow-moving-products", Title: "Slow moving stock", TitleFa: "محصولات راکد",
		Description: "Published products with little or no sales",
		Query: `SELECT p.id, p.name, p.sku, p.price, p.sold_count, p.view_count, p.created_at,
		        COALESCE((SELECT SUM(i.quantity) FROM inventory i
		          JOIN product_variants v ON v.id=i.variant_id WHERE v.product_id=p.id),0) AS stock
		        FROM products p WHERE p.deleted_at IS NULL AND p.status='published'
		        ORDER BY p.sold_count ASC, p.view_count ASC`},

	{Key: "inventory-valuation", Title: "Inventory valuation", TitleFa: "ارزش‌گذاری موجودی انبار",
		Description: "Stock on hand valued at cost and at retail",
		Query: `SELECT w.name AS warehouse, COUNT(DISTINCT i.variant_id) AS variants,
		        SUM(i.quantity) AS units,
		        SUM(i.quantity * p.cost_price) AS cost_value,
		        SUM(i.quantity * p.price) AS retail_value
		        FROM inventory i
		        JOIN product_variants v ON v.id=i.variant_id
		        JOIN products p ON p.id=v.product_id
		        JOIN warehouses w ON w.id=i.warehouse_id
		        GROUP BY w.id ORDER BY retail_value DESC`},

	{Key: "abandoned-carts", Title: "Abandoned carts", TitleFa: "سبدهای رهاشده",
		Description: "Active carts idle for more than 24 hours",
		Query: `SELECT ca.id, COALESCE(u.email,'guest') AS email, COUNT(ci.id) AS items,
		        SUM(ci.quantity*ci.unit_price) AS value, ca.updated_at
		        FROM carts ca JOIN cart_items ci ON ci.cart_id=ca.id
		        LEFT JOIN users u ON u.id=ca.user_id
		        WHERE ca.status='active' AND ca.updated_at < datetime('now','-1 day')
		        GROUP BY ca.id ORDER BY value DESC`},

	{Key: "conversion-funnel", Title: "Conversion funnel", TitleFa: "قیف تبدیل",
		Description: "Views → carts → checkouts → paid orders",
		Query: `SELECT 'product_views' AS stage, COALESCE(SUM(view_count),0) AS count FROM products
		        UNION ALL SELECT 'carts_created', COUNT(*) FROM carts
		        UNION ALL SELECT 'carts_with_items', COUNT(DISTINCT cart_id) FROM cart_items
		        UNION ALL SELECT 'orders_placed', COUNT(*) FROM orders
		        UNION ALL SELECT 'orders_paid', COUNT(*) FROM orders WHERE payment_status='paid'`},

	{Key: "traffic-sources", Title: "Traffic sources", TitleFa: "منابع ترافیک",
		Description: "Analytics events grouped by acquisition source",
		Query: `SELECT source, COUNT(*) AS events, COUNT(DISTINCT session) AS sessions,
		        COALESCE(SUM(value),0) AS value FROM analytics_events
		        GROUP BY source ORDER BY events DESC`},

	{Key: "tax-summary", Title: "Tax summary", TitleFa: "گزارش مالیات",
		Description: "Tax collected per month",
		Query: `SELECT strftime('%Y-%m', created_at) AS period, COUNT(*) AS orders,
		        COALESCE(SUM(subtotal),0) AS taxable_base, COALESCE(SUM(tax_total),0) AS tax_collected
		        FROM orders WHERE payment_status='paid' GROUP BY period ORDER BY period DESC`},

	{Key: "vendor-commissions", Title: "Vendor commissions", TitleFa: "کمیسیون فروشندگان",
		Description: "Gross sales, commission and net payable per vendor",
		Query: `SELECT v.id, v.shop_name, v.commission_rate,
		        COALESCE(SUM(oi.total),0) AS gross,
		        CAST(COALESCE(SUM(oi.total),0)*v.commission_rate/100 AS INTEGER) AS commission,
		        CAST(COALESCE(SUM(oi.total),0)*(100-v.commission_rate)/100 AS INTEGER) AS net_payable
		        FROM vendors v LEFT JOIN order_items oi ON oi.vendor_id=v.id
		        LEFT JOIN orders o ON o.id=oi.order_id AND o.payment_status='paid'
		        GROUP BY v.id ORDER BY gross DESC`},

	{Key: "returns-refunds", Title: "Returns & refunds", TitleFa: "گزارش مرجوعی و بازگشت وجه",
		Description: "Return volumes and refunded amounts by status",
		Query: `SELECT status, COUNT(*) AS count, COALESCE(SUM(refund_amount),0) AS refunded
		        FROM returns GROUP BY status ORDER BY count DESC`},

	{Key: "coupon-performance", Title: "Coupon performance", TitleFa: "عملکرد کدهای تخفیف",
		Description: "Redemptions, discount given and revenue influenced",
		Query: `SELECT c.code, c.type, c.value, c.used_count,
		        COALESCE(SUM(cr.amount),0) AS discount_given,
		        COUNT(DISTINCT cr.order_id) AS orders,
		        COALESCE((SELECT SUM(o.grand_total) FROM orders o WHERE o.coupon_code=c.code),0) AS revenue
		        FROM coupons c LEFT JOIN coupon_redemptions cr ON cr.coupon_id=c.id
		        GROUP BY c.id ORDER BY discount_given DESC`},

	{Key: "payment-methods-breakdown", Title: "Payment method mix", TitleFa: "سهم درگاه‌های پرداخت",
		Description: "Transaction share per gateway",
		Query: `SELECT gateway, COUNT(*) AS transactions,
		        SUM(CASE WHEN status='succeeded' THEN 1 ELSE 0 END) AS succeeded,
		        SUM(CASE WHEN status='failed' THEN 1 ELSE 0 END) AS failed,
		        COALESCE(SUM(CASE WHEN status='succeeded' THEN amount ELSE 0 END),0) AS volume
		        FROM payments GROUP BY gateway ORDER BY volume DESC`},

	{Key: "loyalty", Title: "Loyalty programme", TitleFa: "باشگاه مشتریان",
		Description: "Points outstanding grouped by tier",
		Query: `SELECT tier, COUNT(*) AS members, COALESCE(SUM(points),0) AS points_outstanding,
		        COALESCE(SUM(lifetime),0) AS lifetime_points FROM loyalty_accounts
		        GROUP BY tier ORDER BY lifetime_points DESC`},

	{Key: "subscriptions", Title: "Subscription health", TitleFa: "سلامت اشتراک‌ها",
		Description: "Active, cancelled and past-due subscriptions per plan",
		Query: `SELECT sp.name AS plan, s.status, COUNT(*) AS count,
		        COALESCE(SUM(sp.price),0) AS mrr
		        FROM subscriptions s JOIN subscription_plans sp ON sp.id=s.plan_id
		        GROUP BY sp.id, s.status ORDER BY plan`},

	{Key: "search-terms", Title: "Search terms", TitleFa: "عبارات جستجوشده",
		Description: "Most searched queries and their result counts",
		Query: `SELECT query, COUNT(*) AS searches, AVG(results) AS avg_results,
		        SUM(CASE WHEN results=0 THEN 1 ELSE 0 END) AS zero_result_searches
		        FROM search_log GROUP BY query ORDER BY searches DESC`},

	{Key: "stock-movements", Title: "Stock movements", TitleFa: "گردش موجودی",
		Description: "Inventory deltas grouped by reason",
		Query: `SELECT reason, COUNT(*) AS movements, SUM(delta) AS net_units
		        FROM inventory_history GROUP BY reason ORDER BY movements DESC`},
}

func registerReportsModule(rt *Router, c *core.Core) {
	rt.Module("21-reports")

	rt.Handle(http.MethodGet, "/admin/reports", roleAdmin, func(w http.ResponseWriter, r *http.Request) {
		out := make([]map[string]any, 0, len(reportRegistry))
		for _, d := range reportRegistry {
			out = append(out, map[string]any{
				"key": d.Key, "title": d.Title, "titleFa": d.TitleFa,
				"description": d.Description, "endpoint": c.Cfg.APIPrefix + "/admin/reports/" + d.Key,
			})
		}
		writeOK(w, out)
	})

	for i := range reportRegistry {
		def := reportRegistry[i]
		rt.Handle(http.MethodGet, "/admin/reports/"+def.Key, roleAdmin, func(w http.ResponseWriter, r *http.Request) {
			rows, meta, err := RunReport(r.Context(), c, def, r.URL.Query().Get("from"),
				r.URL.Query().Get("to"), httpx.QueryInt(r, "limit", 200, 1, 5000))
			if err != nil {
				httpx.Fail(w, httpx.CodeInternal, err.Error())
				return
			}
			if r.URL.Query().Get("format") == "csv" {
				writeRowsCSV(w, def.Key, rows)
				return
			}
			httpx.Success(w, http.StatusOK, def.Title, rows, &httpx.Meta{Extra: meta})
		})
		rt.Handle(http.MethodPost, "/admin/reports/"+def.Key+"/export", roleAdmin, func(w http.ResponseWriter, r *http.Request) {
			rows, _, err := RunReport(r.Context(), c, def, r.URL.Query().Get("from"), r.URL.Query().Get("to"), 5000)
			if err != nil {
				httpx.Fail(w, httpx.CodeInternal, err.Error())
				return
			}
			writeRowsCSV(w, def.Key, rows)
		})
	}

	rt.Handle(http.MethodPost, "/admin/reports/custom-query", roleSuper, func(w http.ResponseWriter, r *http.Request) {
		m, _ := body(r)
		q := strings.TrimSpace(sVal(m, "query"))
		if q == "" {
			httpx.Fail(w, httpx.CodeValidation, "query is required")
			return
		}
		if err := assertReadOnlySQL(q); err != nil {
			httpx.Fail(w, httpx.CodeForbidden, err.Error())
			return
		}
		if !strings.Contains(strings.ToUpper(q), "LIMIT") {
			q += " LIMIT 1000"
		}
		rows, err := c.DB.Maps(r.Context(), q)
		if err != nil {
			httpx.Fail(w, httpx.CodeValidation, err.Error())
			return
		}
		audit(c, r, "report.custom_query", "reports", "", map[string]any{"query": q})
		httpx.Success(w, http.StatusOK, "Custom report", rows,
			&httpx.Meta{Extra: map[string]any{"rows": len(rows)}})
	})

	rt.Handle(http.MethodGet, "/admin/reports/scheduled", roleAdmin, func(w http.ResponseWriter, r *http.Request) {
		rows, _ := c.DB.Maps(r.Context(), `SELECT * FROM scheduled_reports ORDER BY id DESC`)
		writeOK(w, rows)
	})

	rt.Handle(http.MethodPost, "/admin/reports/{key}/schedule", roleAdmin, func(w http.ResponseWriter, r *http.Request) {
		key := r.PathValue("key")
		if findReport(key) == nil {
			httpx.Fail(w, httpx.CodeNotFound, "Unknown report key")
			return
		}
		m, _ := body(r)
		cron := sVal(m, "cron")
		if cron == "" {
			cron = "0 6 * * 1"
		}
		format := sVal(m, "format")
		if format == "" {
			format = "csv"
		}
		res, err := c.DB.Write(r.Context(),
			`INSERT INTO scheduled_reports(report_key,cron,format,recipients,active) VALUES(?,?,?,?,1)`,
			key, cron, format, sVal(m, "recipients"))
		if err != nil {
			httpx.Fail(w, httpx.CodeInternal, err.Error())
			return
		}
		id, _ := res.LastInsertId()
		audit(c, r, "report.schedule", "scheduled_reports", toString(id), map[string]any{"key": key, "cron": cron})
		writeCreated(w, map[string]any{"id": id, "reportKey": key, "cron": cron, "format": format})
	})

	rt.Handle(http.MethodDelete, "/admin/reports/scheduled/{id}", roleAdmin, func(w http.ResponseWriter, r *http.Request) {
		id, _ := httpx.PathInt(r, "id")
		_, _ = c.DB.Write(r.Context(), `DELETE FROM scheduled_reports WHERE id=?`, id)
		httpx.NoBody(w, "Scheduled report removed")
	})
}

func findReport(key string) *reportDef {
	for i := range reportRegistry {
		if reportRegistry[i].Key == key {
			return &reportRegistry[i]
		}
	}
	return nil
}

func RunReport(ctx context.Context, c *core.Core, def reportDef, from, to string, limit int) ([]map[string]any, map[string]any, error) {
	q := def.Query
	rangeSQL, rangeO := "", ""
	if from != "" && to != "" {
		rangeSQL = fmt.Sprintf(" AND created_at BETWEEN '%s' AND '%s' ", sqlSafe(from), sqlSafe(to))
		rangeO = fmt.Sprintf(" AND o.created_at BETWEEN '%s' AND '%s' ", sqlSafe(from), sqlSafe(to))
	}
	q = strings.ReplaceAll(q, "{{RANGE}}", rangeSQL)
	q = strings.ReplaceAll(q, "{{RANGE_O}}", rangeO)
	if !strings.Contains(strings.ToUpper(q), "LIMIT") {
		q += fmt.Sprintf(" LIMIT %d", limit)
	}
	rows, err := c.DB.Maps(ctx, q)
	if err != nil {
		return nil, nil, err
	}
	return rows, map[string]any{
		"report": def.Key, "title": def.Title, "titleFa": def.TitleFa,
		"rows": len(rows), "from": from, "to": to,
	}, nil
}

func RunReportByKey(ctx context.Context, c *core.Core, key, from, to string, limit int) ([]map[string]any, map[string]any, error) {
	def := findReport(key)
	if def == nil {
		return nil, nil, fmt.Errorf("unknown report %q", key)
	}
	return RunReport(ctx, c, *def, from, to, limit)
}

func ReportKeys() []string {
	out := make([]string, 0, len(reportRegistry))
	for _, d := range reportRegistry {
		out = append(out, d.Key)
	}
	return out
}

func sqlSafe(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\'' || r == ';' || r == '-' && false {
			return -1
		}
		return r
	}, s)
}

func assertReadOnlySQL(q string) error {
	up := strings.ToUpper(strings.TrimSpace(q))
	if !strings.HasPrefix(up, "SELECT") && !strings.HasPrefix(up, "WITH") {
		return fmt.Errorf("only SELECT/WITH statements are allowed")
	}
	banned := []string{"INSERT ", "UPDATE ", "DELETE ", "DROP ", "ALTER ", "CREATE ",
		"ATTACH ", "DETACH ", "PRAGMA ", "VACUUM", "REPLACE ", "TRUNCATE "}
	for _, b := range banned {
		if strings.Contains(up, b) {
			return fmt.Errorf("statement contains the forbidden keyword %q", strings.TrimSpace(b))
		}
	}
	if strings.Count(q, ";") > 1 || (strings.Contains(q, ";") && !strings.HasSuffix(strings.TrimSpace(q), ";")) {
		return fmt.Errorf("multiple statements are not allowed")
	}
	return nil
}

func writeRowsCSV(w http.ResponseWriter, name string, rows []map[string]any) {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.csv"`, name))
	_, _ = w.Write([]byte{0xEF, 0xBB, 0xBF})
	if len(rows) == 0 {
		_, _ = w.Write([]byte("no data\n"))
		return
	}
	cols := make([]string, 0, len(rows[0]))
	for k := range rows[0] {
		cols = append(cols, k)
	}
	sort.Strings(cols)
	_, _ = w.Write([]byte(strings.Join(cols, ",") + "\n"))
	for _, row := range rows {
		vals := make([]string, len(cols))
		for i, cname := range cols {
			v := sanitizeCSV(asStr(row[cname]))
			if strings.ContainsAny(v, ",\"\n") {
				v = `"` + strings.ReplaceAll(v, `"`, `""`) + `"`
			}
			vals[i] = v
		}
		_, _ = w.Write([]byte(strings.Join(vals, ",") + "\n"))
	}
}
