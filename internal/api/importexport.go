package api

import (
	"encoding/csv"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/arena/avaluna/internal/core"
	"github.com/arena/avaluna/internal/httpx"
	"github.com/arena/avaluna/internal/middleware"
)

type importSpec struct {
	Table    string
	Required []string
	Columns  []string
	Ints     map[string]bool
	Key      string
}

var importSpecs = map[string]importSpec{
	"products": {
		Table:    "products",
		Required: []string{"sku", "name", "price"},
		Columns:  []string{"sku", "name", "name_en", "slug", "short_desc", "description", "price", "compare_price", "cost_price", "status", "weight_g", "brand_id", "category_id"},
		Ints:     map[string]bool{"price": true, "compare_price": true, "cost_price": true, "weight_g": true, "brand_id": true, "category_id": true},
		Key:      "sku",
	},
	"categories": {
		Table:    "categories",
		Required: []string{"name"},
		Columns:  []string{"name", "name_en", "slug", "parent_id", "description", "active", "sort_order"},
		Ints:     map[string]bool{"parent_id": true, "active": true, "sort_order": true},
		Key:      "slug",
	},
	"customers": {
		Table:    "users",
		Required: []string{"email"},
		Columns:  []string{"email", "first_name", "last_name", "phone", "role", "status", "locale"},
		Key:      "email",
	},
	"inventory": {
		Table:    "inventory",
		Required: []string{"variant_id", "quantity"},
		Columns:  []string{"variant_id", "warehouse_id", "quantity", "reserved", "reorder_level"},
		Ints:     map[string]bool{"variant_id": true, "warehouse_id": true, "quantity": true, "reserved": true, "reorder_level": true},
	},
	"coupons": {
		Table:    "coupons",
		Required: []string{"code", "type", "value"},
		Columns:  []string{"code", "type", "value", "min_subtotal", "max_discount", "usage_limit", "per_user_limit", "starts_at", "expires_at", "active"},
		Ints:     map[string]bool{"value": true, "min_subtotal": true, "max_discount": true, "usage_limit": true, "per_user_limit": true, "active": true},
		Key:      "code",
	},
}

var exportQueries = map[string]string{
	"products":   `SELECT id,sku,name,name_en,slug,price,compare_price,status,sold_count,rating_avg,created_at FROM products WHERE deleted_at IS NULL`,
	"orders":     `SELECT id,number,user_id,email,status,payment_status,subtotal,discount_total,tax_total,shipping_total,grand_total,created_at FROM orders`,
	"customers":  `SELECT id,email,first_name,last_name,phone,role,status,created_at FROM users WHERE deleted_at IS NULL`,
	"inventory":  `SELECT i.id,i.variant_id,v.sku,i.warehouse_id,i.quantity,i.reserved,i.reorder_level FROM inventory i LEFT JOIN product_variants v ON v.id=i.variant_id`,
	"coupons":    `SELECT id,code,type,value,min_subtotal,usage_limit,used_count,starts_at,expires_at,active FROM coupons`,
	"reviews":    `SELECT id,product_id,user_id,rating,title,body,status,created_at FROM reviews`,
	"vendors":    `SELECT id,shop_name,slug,status,commission_rate,balance,created_at FROM vendors`,
	"blog-posts": `SELECT id,title,slug,status,views,published_at,created_at FROM blog_posts WHERE deleted_at IS NULL`,
}

func registerImportModule(rt *Router, c *core.Core) {
	rt.Module("29-import-export")

	rt.Handle(http.MethodGet, "/admin/import/types", roleAdmin, func(w http.ResponseWriter, r *http.Request) {
		out := []map[string]any{}
		for name, spec := range importSpecs {
			out = append(out, map[string]any{"type": name, "required": spec.Required,
				"columns": spec.Columns, "upsertKey": spec.Key})
		}
		writeOK(w, map[string]any{"import": out, "export": keysOf(exportQueries)})
	})

	rt.Handle(http.MethodGet, "/admin/import/{type}/template", roleAdmin, func(w http.ResponseWriter, r *http.Request) {
		spec, ok := importSpecs[r.PathValue("type")]
		if !ok {
			httpx.Fail(w, httpx.CodeNotFound, "Unknown import type")
			return
		}
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition",
			fmt.Sprintf(`attachment; filename="%s-template.csv"`, r.PathValue("type")))
		_, _ = w.Write([]byte("\xEF\xBB\xBF" + strings.Join(spec.Columns, ",") + "\n"))
	})

	rt.Handle(http.MethodPost, "/admin/import/{type}", roleAdmin, func(w http.ResponseWriter, r *http.Request) {
		typ := r.PathValue("type")
		spec, ok := importSpecs[typ]
		if !ok {
			httpx.Fail(w, httpx.CodeNotFound, "Unknown import type")
			return
		}
		dryRun := r.URL.Query().Get("dryRun") == "true"
		data, filename, err := readCSVUpload(r)
		if err != nil {
			httpx.Fail(w, httpx.CodeValidation, err.Error())
			return
		}
		records, err := csv.NewReader(strings.NewReader(strings.TrimPrefix(string(data), "\xEF\xBB\xBF"))).ReadAll()
		if err != nil || len(records) < 2 {
			httpx.Fail(w, httpx.CodeImportFailed, "CSV must contain a header row and at least one data row")
			return
		}
		header := records[0]
		for i := range header {
			header[i] = strings.TrimSpace(strings.ToLower(header[i]))
		}
		for _, req := range spec.Required {
			if indexOf(header, req) < 0 {
				httpx.Fail(w, httpx.CodeImportFailed, "Missing required column: "+req)
				return
			}
		}
		res, _ := c.DB.Write(r.Context(),
			`INSERT INTO import_jobs(type,filename,total_rows,status,created_by) VALUES(?,?,?,'running',?)`,
			typ, filename, len(records)-1, middleware.UserID(r.Context()))
		jobID, _ := res.LastInsertId()

		okCount, failCount := 0, 0
		issues := []map[string]any{}
		for rowNo, rec := range records[1:] {
			cols, args, placeholders := []string{}, []any{}, []string{}
			for _, col := range spec.Columns {
				idx := indexOf(header, col)
				if idx < 0 || idx >= len(rec) {
					continue
				}
				val := strings.TrimSpace(rec[idx])
				if val == "" {
					continue
				}
				cols = append(cols, col)
				placeholders = append(placeholders, "?")
				if spec.Ints[col] {
					n, err := strconv.Atoi(val)
					if err != nil {
						issues = append(issues, map[string]any{"row": rowNo + 2, "column": col, "error": "not an integer"})
					}
					args = append(args, n)
				} else {
					args = append(args, val)
				}
			}
			if len(cols) == 0 {
				failCount++
				continue
			}
			if spec.Table == "products" && indexOf(cols, "slug") < 0 {
				cols = append(cols, "slug")
				placeholders = append(placeholders, "?")
				args = append(args, Slugify(toString(args[indexOf(cols, "name")])))
			}
			if spec.Table == "users" {
				cols = append(cols, "uuid", "password_hash")
				placeholders = append(placeholders, "?", "?")
				args = append(args, randHex(16), "!imported")
			}
			query := fmt.Sprintf("INSERT INTO %s(%s) VALUES(%s)",
				spec.Table, strings.Join(cols, ","), strings.Join(placeholders, ","))
			if spec.Key != "" {
				sets := []string{}
				for _, col := range cols {
					if col != spec.Key {
						sets = append(sets, col+"=excluded."+col)
					}
				}
				query += fmt.Sprintf(" ON CONFLICT(%s) DO UPDATE SET %s", spec.Key, strings.Join(sets, ","))
			}
			if dryRun {
				okCount++
				continue
			}
			if _, err := c.DB.Write(r.Context(), query, args...); err != nil {
				failCount++
				if len(issues) < 50 {
					issues = append(issues, map[string]any{"row": rowNo + 2, "error": err.Error()})
				}
				continue
			}
			okCount++
		}
		status := "completed"
		if failCount > 0 && okCount == 0 {
			status = "failed"
		}
		_, _ = c.DB.Write(r.Context(),
			`UPDATE import_jobs SET processed=?,succeeded=?,failed=?,status=?,errors_json=?,finished_at=datetime('now')
			 WHERE id=?`, okCount+failCount, okCount, failCount, status, jsonStr(issues), jobID)
		if typ == "products" && !dryRun {
			_, _ = RebuildSearchIndex(r.Context(), c)
		}
		audit(c, r, "import."+typ, "import_jobs", toString(jobID),
			map[string]any{"ok": okCount, "failed": failCount, "dryRun": dryRun})
		httpx.Success(w, http.StatusOK, "Import finished", map[string]any{
			"jobId": jobID, "dryRun": dryRun, "total": len(records) - 1,
			"succeeded": okCount, "failed": failCount, "issues": issues,
		}, nil)
	})

	rt.Handle(http.MethodGet, "/admin/import-jobs", roleAdmin, func(w http.ResponseWriter, r *http.Request) {
		page, limit, offset := paginate(r)
		total, _ := c.DB.Count(r.Context(), `SELECT COUNT(*) FROM import_jobs`)
		rows, _ := c.DB.Maps(r.Context(), `SELECT * FROM import_jobs ORDER BY id DESC LIMIT ? OFFSET ?`, limit, offset)
		httpx.List(w, rows, httpx.MakePagination(page, limit, total))
	})

	rt.Handle(http.MethodGet, "/admin/import-jobs/{id}", roleAdmin, func(w http.ResponseWriter, r *http.Request) {
		id, _ := httpx.PathInt(r, "id")
		row, err := c.DB.MapOne(r.Context(), `SELECT * FROM import_jobs WHERE id=?`, id)
		if err != nil {
			httpx.Fail(w, httpx.CodeNotFound, "")
			return
		}
		writeOK(w, row)
	})

	rt.Handle(http.MethodGet, "/admin/export/{type}", roleAdmin, func(w http.ResponseWriter, r *http.Request) {
		typ := r.PathValue("type")
		query, ok := exportQueries[typ]
		if !ok {
			httpx.Fail(w, httpx.CodeNotFound, "Unknown export type")
			return
		}
		if from := r.URL.Query().Get("from"); from != "" && strings.Contains(query, "created_at") {
			query += " AND created_at >= '" + sqlSafe(from) + "'"
			if !strings.Contains(strings.ToUpper(query), " WHERE ") {
				query = strings.Replace(query, " AND created_at", " WHERE created_at", 1)
			}
		}
		query += " ORDER BY 1 DESC LIMIT 50000"
		if r.URL.Query().Get("format") == "json" {
			rows, err := c.DB.Maps(r.Context(), query)
			if err != nil {
				httpx.Fail(w, httpx.CodeInternal, err.Error())
				return
			}
			writeOK(w, rows)
			return
		}
		exportCSV(w, r, c, typ, query)
	})

	rt.Handle(http.MethodPost, "/admin/bulk/products", roleAdmin, func(w http.ResponseWriter, r *http.Request) {
		m, _ := body(r)
		action := sVal(m, "action")
		ids := intsOf(m["ids"])
		if len(ids) == 0 {
			httpx.Fail(w, httpx.CodeValidation, "ids must be a non-empty array")
			return
		}
		ph := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
		args := make([]any, 0, len(ids)+2)
		var query string
		switch action {
		case "publish", "unpublish", "archive":
			status := map[string]string{"publish": "published", "unpublish": "draft", "archive": "archived"}[action]
			query = "UPDATE products SET status=?, updated_at=datetime('now') WHERE id IN (" + ph + ")"
			args = append(args, status)
		case "delete":
			query = "UPDATE products SET deleted_at=datetime('now') WHERE id IN (" + ph + ")"
		case "restore":
			query = "UPDATE products SET deleted_at=NULL WHERE id IN (" + ph + ")"
		case "price-adjust":
			pct := asFloat(m["percent"])
			if pct == 0 {
				httpx.Fail(w, httpx.CodeValidation, "percent is required for price-adjust")
				return
			}
			query = "UPDATE products SET price=CAST(price * ? AS INTEGER), updated_at=datetime('now') WHERE id IN (" + ph + ")"
			args = append(args, 1+pct/100)
		case "assign-category":
			cat := iVal(m, "categoryId", 0)
			query = "UPDATE products SET category_id=?, updated_at=datetime('now') WHERE id IN (" + ph + ")"
			args = append(args, cat)
		default:
			httpx.Fail(w, httpx.CodeValidation,
				"action must be one of publish|unpublish|archive|delete|restore|price-adjust|assign-category")
			return
		}
		for _, id := range ids {
			args = append(args, id)
		}
		res, err := c.DB.Write(r.Context(), query, args...)
		if err != nil {
			httpx.Fail(w, httpx.CodeInternal, err.Error())
			return
		}
		n, _ := res.RowsAffected()
		_, _ = RebuildSearchIndex(r.Context(), c)
		audit(c, r, "bulk.products."+action, "products", "", map[string]any{"ids": ids, "affected": n})
		httpx.Success(w, http.StatusOK, "Bulk operation applied",
			map[string]any{"action": action, "affected": n}, nil)
	})

	rt.Handle(http.MethodPost, "/admin/bulk/orders", roleAdmin, func(w http.ResponseWriter, r *http.Request) {
		m, _ := body(r)
		ids := intsOf(m["ids"])
		status := sVal(m, "status")
		allowed := map[string]bool{"processing": true, "shipped": true, "delivered": true, "cancelled": true}
		if len(ids) == 0 || !allowed[status] {
			httpx.Fail(w, httpx.CodeValidation, "ids and a valid status are required")
			return
		}
		n := 0
		for _, id := range ids {
			if status == "cancelled" {
				if err := CancelOrder(r.Context(), c, id, "bulk cancel"); err == nil {
					n++
				}
				continue
			}
			if _, err := c.DB.Write(r.Context(),
				`UPDATE orders SET status=?, updated_at=datetime('now') WHERE id=?`, status, id); err == nil {
				_, _ = c.DB.Write(r.Context(),
					`INSERT INTO order_events(order_id,status,note) VALUES(?,?,'bulk update')`, id, status)
				n++
			}
		}
		audit(c, r, "bulk.orders", "orders", "", map[string]any{"ids": ids, "status": status})
		httpx.Success(w, http.StatusOK, "Bulk operation applied", map[string]any{"affected": n}, nil)
	})

	rt.Handle(http.MethodPost, "/admin/bulk/inventory", roleAdmin, func(w http.ResponseWriter, r *http.Request) {
		m, _ := body(r)
		items, _ := m["items"].([]any)
		if len(items) == 0 {
			httpx.Fail(w, httpx.CodeValidation, "items must be a non-empty array")
			return
		}
		n := 0
		for _, raw := range items {
			it, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			vid, qty := iVal(it, "variantId", 0), iVal(it, "quantity", 0)
			wh := iVal(it, "warehouseId", 1)
			if vid == 0 {
				continue
			}
			if _, err := c.DB.Write(r.Context(),
				`INSERT INTO inventory(variant_id,warehouse_id,quantity) VALUES(?,?,?)
				 ON CONFLICT(variant_id,warehouse_id) DO UPDATE SET quantity=excluded.quantity,
				 updated_at=datetime('now')`,
				vid, wh, qty); err != nil {
				continue
			}
			_, _ = c.DB.Write(r.Context(),
				`INSERT INTO inventory_history(variant_id,warehouse_id,delta,reason) VALUES(?,?,?,'bulk-set')`,
				vid, wh, qty)
			n++
		}
		audit(c, r, "bulk.inventory", "inventory", "", map[string]any{"updated": n})
		httpx.Success(w, http.StatusOK, "Inventory updated", map[string]any{"updated": n}, nil)
	})
}

func readCSVUpload(r *http.Request) ([]byte, string, error) {
	ct := r.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "multipart/form-data") {
		if err := r.ParseMultipartForm(maxUploadBytes); err != nil {
			return nil, "", err
		}
		f, fh, err := r.FormFile("file")
		if err != nil {
			return nil, "", fmt.Errorf("file field is required")
		}
		defer func() { _ = f.Close() }()
		data, err := io.ReadAll(io.LimitReader(f, maxUploadBytes))
		return data, fh.Filename, err
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, maxUploadBytes))
	if err != nil {
		return nil, "", err
	}
	if len(data) == 0 {
		return nil, "", fmt.Errorf("request body is empty; send CSV as multipart 'file' or raw text/csv")
	}
	return data, "inline.csv", nil
}

func indexOf(ss []string, s string) int {
	for i, v := range ss {
		if v == s {
			return i
		}
	}
	return -1
}

func keysOf[T any](m map[string]T) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func intsOf(v any) []int {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]int, 0, len(arr))
	for _, item := range arr {
		if n := asInt(item); n > 0 {
			out = append(out, n)
		}
	}
	return out
}
