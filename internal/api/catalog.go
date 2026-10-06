package api

import (
	"database/sql"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/arena/avaluna/internal/core"
	"github.com/arena/avaluna/internal/httpx"
)

func registerCatalogModule(rt *Router, c *core.Core) {
	rt.Module("03-catalog")

	categories := &Resource{
		Name: "catalog/categories", Table: "categories", SoftDelete: true,
		Fields: []Field{
			{Name: "parent_id", Kind: KindInt},
			{Name: "slug", Kind: KindSlug, Required: true, From: "name"},
			{Name: "name", Kind: KindString, Required: true},
			{Name: "name_en", Kind: KindString},
			{Name: "description", Kind: KindString},
			{Name: "image_url", Kind: KindString},
			{Name: "icon", Kind: KindString},
			{Name: "sort_order", Kind: KindInt},
			{Name: "active", Kind: KindBool, Default: 1},
		},
		Searchable: []string{"name", "name_en", "slug"},
		Filters:    []string{"parent_id", "active"},
		OrderBy:    "sort_order ASC, name ASC", WriteRole: roleAdmin, MaxLimit: 500,
	}
	categories.Mount(rt, c.DB)

	brands := &Resource{
		Name: "catalog/brands", Table: "brands",
		Fields: []Field{
			{Name: "slug", Kind: KindSlug, Required: true, From: "name"},
			{Name: "name", Kind: KindString, Required: true},
			{Name: "name_en", Kind: KindString},
			{Name: "logo_url", Kind: KindString},
			{Name: "country", Kind: KindString},
			{Name: "active", Kind: KindBool, Default: 1},
		},
		Searchable: []string{"name", "name_en"}, Filters: []string{"active"},
		OrderBy: "name ASC", WriteRole: roleAdmin, MaxLimit: 500,
	}
	brands.Mount(rt, c.DB)

	attributes := &Resource{
		Name: "catalog/attributes", Table: "attributes",
		Fields: []Field{
			{Name: "code", Kind: KindString, Required: true},
			{Name: "name", Kind: KindString, Required: true},
			{Name: "name_en", Kind: KindString},
			{Name: "type", Kind: KindString, Default: "select"},
			{Name: "values_json", Kind: KindJSON, Default: "[]"},
			{Name: "filterable", Kind: KindBool, Default: 1},
		},
		Searchable: []string{"name", "code"}, OrderBy: "id ASC", WriteRole: roleAdmin,
	}
	attributes.Mount(rt, c.DB)

	rt.Handle(http.MethodGet, "/catalog/products", "", handleProductList(c))
	rt.Handle(http.MethodGet, "/catalog/products/{id}", "", handleProductDetail(c))
	rt.Handle(http.MethodGet, "/catalog/categories/{id}/tree", "", handleCategoryTree(c))

	products := &Resource{
		Name: "catalog/products", Table: "products", SoftDelete: true,
		Fields: []Field{
			{Name: "slug", Kind: KindSlug, Required: true, From: "name"},
			{Name: "sku", Kind: KindString, Required: true},
			{Name: "name", Kind: KindString, Required: true},
			{Name: "name_en", Kind: KindString},
			{Name: "short_desc", Kind: KindString},
			{Name: "short_desc_en", Kind: KindString},
			{Name: "description", Kind: KindString},
			{Name: "description_en", Kind: KindString},
			{Name: "category_id", Kind: KindInt},
			{Name: "brand_id", Kind: KindInt},
			{Name: "vendor_id", Kind: KindInt},
			{Name: "price", Kind: KindInt, Required: true},
			{Name: "compare_price", Kind: KindInt},
			{Name: "cost_price", Kind: KindInt},
			{Name: "currency", Kind: KindString, Default: "IRR"},
			{Name: "tax_class", Kind: KindString, Default: "standard"},
			{Name: "weight_g", Kind: KindInt},
			{Name: "status", Kind: KindString, Default: "published"},
			{Name: "featured", Kind: KindBool},
			{Name: "thumbnail", Kind: KindString},
			{Name: "gallery_json", Kind: KindJSON, Default: "[]"},
			{Name: "attrs_json", Kind: KindJSON, Default: "{}"},
		},
		Filters: []string{"status", "category_id", "brand_id", "vendor_id", "featured"},
		OrderBy: "id DESC", WriteRole: roleVendor,
	}
	rt.Handle(http.MethodPost, "/catalog/products", roleVendor, products.Create(c.DB))
	rt.Handle(http.MethodPut, "/catalog/products/{id}", roleVendor, products.Update(c.DB))
	rt.Handle(http.MethodPatch, "/catalog/products/{id}", roleVendor, products.Update(c.DB))
	rt.Handle(http.MethodDelete, "/catalog/products/{id}", roleVendor, products.Delete(c.DB))

	variants := &Resource{
		Name: "catalog/variants", Table: "product_variants",
		Fields: []Field{
			{Name: "product_id", Kind: KindInt, Required: true},
			{Name: "sku", Kind: KindString, Required: true},
			{Name: "title", Kind: KindString},
			{Name: "options_json", Kind: KindJSON, Default: "{}"},
			{Name: "price", Kind: KindInt},
			{Name: "compare_price", Kind: KindInt},
			{Name: "barcode", Kind: KindString},
			{Name: "weight_g", Kind: KindInt},
			{Name: "active", Kind: KindBool, Default: 1},
		},
		Filters: []string{"product_id", "active"}, OrderBy: "id ASC", WriteRole: roleVendor,
	}
	variants.Mount(rt, c.DB)

	rt.Handle(http.MethodGet, "/catalog/products/{id}/variants", "", func(w http.ResponseWriter, r *http.Request) {
		pid, err := resolveProductID(c, r)
		if err != nil {
			httpx.Fail(w, httpx.CodeNotFound, "")
			return
		}
		rows, err := c.DB.Maps(r.Context(), `SELECT v.*, COALESCE(SUM(i.quantity - i.reserved),0) AS available
			FROM product_variants v LEFT JOIN inventory i ON i.variant_id = v.id
			WHERE v.product_id=? GROUP BY v.id ORDER BY v.id`, pid)
		if err != nil {
			httpx.Fail(w, httpx.CodeInternal, err.Error())
			return
		}
		writeOK(w, rows)
	})

	rt.Handle(http.MethodPost, "/catalog/products/{id}/variants", roleVendor, func(w http.ResponseWriter, r *http.Request) {
		pid, err := resolveProductID(c, r)
		if err != nil {
			httpx.Fail(w, httpx.CodeNotFound, "")
			return
		}
		m, err := body(r)
		if err != nil {
			httpx.Fail(w, httpx.CodeValidation, err.Error())
			return
		}
		sku := sVal(m, "sku")
		if sku == "" {
			sku = fmt.Sprintf("SKU-%d-%s", pid, strings.ToUpper(randHex(2)))
		}
		res, err := c.DB.Write(r.Context(),
			`INSERT INTO product_variants(product_id,sku,title,options_json,price,compare_price,barcode,weight_g,active)
			 VALUES(?,?,?,?,?,?,?,?,1)`,
			pid, sku, sVal(m, "title"), jsonOr(m["options"], "{}"),
			iVal(m, "price", 0), iVal(m, "compare_price", 0), sVal(m, "barcode"), iVal(m, "weight_g", 0))
		if err != nil {
			httpx.Fail(w, conflictOr(err), err.Error())
			return
		}
		id, _ := res.LastInsertId()
		row, _ := c.DB.MapOne(r.Context(), `SELECT * FROM product_variants WHERE id=?`, id)
		writeCreated(w, row)
	})
}

func jsonOr(v any, def string) string {
	if v == nil {
		return def
	}
	if s, ok := v.(string); ok {
		if strings.TrimSpace(s) == "" {
			return def
		}
		return s
	}
	return jsonStr(v)
}

func resolveProductID(c *core.Core, r *http.Request) (int, error) {
	key := r.PathValue("id")
	if n, err := strconv.Atoi(key); err == nil {
		return n, nil
	}
	var id int
	err := c.DB.Row(r.Context(), `SELECT id FROM products WHERE slug=? AND deleted_at IS NULL`, []any{key}, &id)
	return id, err
}

func handleProductList(c *core.Core) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		page, limit, offset := paginate(r)
		q := r.URL.Query()
		conds := []string{"p.deleted_at IS NULL"}
		args := []any{}

		if st := q.Get("status"); st != "" {
			conds, args = append(conds, "p.status = ?"), append(args, st)
		} else {
			conds = append(conds, "p.status = 'published'")
		}
		if v := q.Get("category"); v != "" {
			if id, err := strconv.Atoi(v); err == nil {
				conds, args = append(conds, "p.category_id = ?"), append(args, id)
			} else {
				conds = append(conds, "p.category_id IN (SELECT id FROM categories WHERE slug = ?)")
				args = append(args, v)
			}
		}
		if v := q.Get("brand"); v != "" {
			if id, err := strconv.Atoi(v); err == nil {
				conds, args = append(conds, "p.brand_id = ?"), append(args, id)
			} else {
				conds = append(conds, "p.brand_id IN (SELECT id FROM brands WHERE slug = ?)")
				args = append(args, v)
			}
		}
		if v := q.Get("vendor"); v != "" {
			conds, args = append(conds, "p.vendor_id = ?"), append(args, v)
		}
		if v := q.Get("minPrice"); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				conds, args = append(conds, "p.price >= ?"), append(args, n)
			}
		}
		if v := q.Get("maxPrice"); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				conds, args = append(conds, "p.price <= ?"), append(args, n)
			}
		}
		if q.Get("featured") == "1" || q.Get("featured") == "true" {
			conds = append(conds, "p.featured = 1")
		}
		if q.Get("onSale") == "1" {
			conds = append(conds, "p.compare_price > p.price")
		}
		if term := strings.TrimSpace(q.Get("q")); term != "" {
			conds = append(conds, "(p.name LIKE ? OR p.name_en LIKE ? OR p.sku LIKE ? OR p.short_desc LIKE ?)")
			like := "%" + term + "%"
			args = append(args, like, like, like, like)
		}

		for key, vals := range q {
			if strings.HasPrefix(key, "attr_") && len(vals) > 0 && vals[0] != "" {
				name := strings.TrimPrefix(key, "attr_")
				conds = append(conds, "p.attrs_json LIKE ?")
				args = append(args, "%\""+name+"\":\""+vals[0]+"\"%")
			}
		}

		where := "WHERE " + strings.Join(conds, " AND ")
		order := map[string]string{
			"-popularity": "p.sold_count DESC, p.view_count DESC",
			"popularity":  "p.sold_count ASC",
			"price":       "p.price ASC",
			"-price":      "p.price DESC",
			"-created_at": "p.id DESC",
			"created_at":  "p.id ASC",
			"-rating":     "p.rating_avg DESC, p.rating_count DESC",
			"name":        "p.name ASC",
		}[q.Get("sort")]
		if order == "" {
			order = "p.featured DESC, p.id DESC"
		}

		total, err := c.DB.Count(r.Context(), "SELECT COUNT(*) FROM products p "+where, args...)
		if err != nil {
			httpx.Fail(w, httpx.CodeInternal, err.Error())
			return
		}
		rows, err := c.DB.Maps(r.Context(), fmt.Sprintf(`
			SELECT p.id,p.slug,p.sku,p.name,p.name_en,p.short_desc,p.short_desc_en,p.price,
			       p.compare_price,p.currency,p.thumbnail,p.rating_avg,p.rating_count,p.sold_count,
			       p.featured,p.status,p.attrs_json,p.created_at,
			       c.name AS category_name, c.slug AS category_slug,
			       b.name AS brand_name, b.slug AS brand_slug,
			       COALESCE((SELECT SUM(i.quantity - i.reserved) FROM inventory i
			                 JOIN product_variants v ON v.id = i.variant_id
			                 WHERE v.product_id = p.id),0) AS stock
			FROM products p
			LEFT JOIN categories c ON c.id = p.category_id
			LEFT JOIN brands b ON b.id = p.brand_id
			%s ORDER BY %s LIMIT ? OFFSET ?`, where, order), append(args, limit, offset)...)
		if err != nil {
			httpx.Fail(w, httpx.CodeInternal, err.Error())
			return
		}
		httpx.Success(w, http.StatusOK, "OK", rows, &httpx.Meta{
			Pagination: ptr(httpx.MakePagination(page, limit, total)),
			Extra:      map[string]any{"filtersApplied": len(conds) - 2},
		})
	}
}

func ptr[T any](v T) *T { return &v }

func handleProductDetail(c *core.Core) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := r.PathValue("id")
		col := "slug"
		if _, err := strconv.Atoi(key); err == nil {
			col = "id"
		}
		p, err := c.DB.MapOne(r.Context(), fmt.Sprintf(`
			SELECT p.*, c.name AS category_name, c.slug AS category_slug,
			       b.name AS brand_name, b.slug AS brand_slug
			FROM products p
			LEFT JOIN categories c ON c.id = p.category_id
			LEFT JOIN brands b ON b.id = p.brand_id
			WHERE p.%s = ? AND p.deleted_at IS NULL`, col), key)
		if err != nil {
			httpx.Fail(w, httpx.CodeNotFound, "")
			return
		}
		pid := asInt(p["id"])
		_, _ = c.DB.Write(r.Context(), `UPDATE products SET view_count = view_count + 1 WHERE id = ?`, pid)

		variants, _ := c.DB.Maps(r.Context(), `SELECT v.*, COALESCE(SUM(i.quantity - i.reserved),0) AS available
			FROM product_variants v LEFT JOIN inventory i ON i.variant_id=v.id
			WHERE v.product_id=? GROUP BY v.id`, pid)
		reviews, _ := c.DB.Maps(r.Context(), `SELECT r.id,r.rating,r.title,r.body,r.created_at,r.verified,
			COALESCE(u.first_name || ' ' || u.last_name,'Guest') AS author
			FROM reviews r LEFT JOIN users u ON u.id=r.user_id
			WHERE r.product_id=? AND r.status='approved' ORDER BY r.id DESC LIMIT 10`, pid)
		related, _ := c.DB.Maps(r.Context(), `SELECT id,slug,name,name_en,price,compare_price,thumbnail,rating_avg
			FROM products WHERE category_id = ? AND id <> ? AND status='published' AND deleted_at IS NULL
			ORDER BY sold_count DESC LIMIT 8`, p["category_id"], pid)
		var stock sql.NullInt64
		_ = c.DB.QueryRowContext(r.Context(), `SELECT SUM(i.quantity - i.reserved) FROM inventory i
			JOIN product_variants v ON v.id=i.variant_id WHERE v.product_id=?`, pid).Scan(&stock)

		writeOK(w, map[string]any{
			"product": p, "variants": variants, "reviews": reviews,
			"related": related, "stock": stock.Int64,
		})
	}
}

func handleCategoryTree(c *core.Core) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rows, err := c.DB.Maps(r.Context(),
			`SELECT id,parent_id,slug,name,name_en,icon,sort_order FROM categories
			 WHERE deleted_at IS NULL AND active=1 ORDER BY sort_order, name`)
		if err != nil {
			httpx.Fail(w, httpx.CodeInternal, err.Error())
			return
		}
		writeOK(w, BuildTree(rows, 0))
	}
}

func BuildTree(rows []map[string]any, parent int) []map[string]any {
	out := []map[string]any{}
	for _, row := range rows {
		p := 0
		if row["parent_id"] != nil {
			p = asInt(row["parent_id"])
		}
		if p == parent {
			node := map[string]any{}
			for k, v := range row {
				node[k] = v
			}
			node["children"] = BuildTree(rows, asInt(row["id"]))
			out = append(out, node)
		}
	}
	return out
}
