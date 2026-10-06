package api

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/arena/avaluna/internal/core"
	"github.com/arena/avaluna/internal/httpx"
)

func registerSEOModule(rt *Router, c *core.Core) {
	rt.Module("25-seo")

	rt.Handle(http.MethodGet, "/admin/seo/meta/{entityType}/{id}", roleAdmin, func(w http.ResponseWriter, r *http.Request) {
		row, err := c.DB.MapOne(r.Context(), `SELECT * FROM seo_meta WHERE entity_type=? AND entity_id=?`,
			r.PathValue("entityType"), r.PathValue("id"))
		if err != nil {
			writeOK(w, map[string]any{"entityType": r.PathValue("entityType"), "entityId": r.PathValue("id"),
				"title": "", "description": "", "keywords": "", "ogImage": "", "canonical": "", "noIndex": false})
			return
		}
		writeOK(w, row)
	})

	rt.Handle(http.MethodPut, "/admin/seo/meta/{entityType}/{id}", roleAdmin, func(w http.ResponseWriter, r *http.Request) {
		m, _ := body(r)
		et, eid := r.PathValue("entityType"), r.PathValue("id")
		_, err := c.DB.Write(r.Context(), `
			INSERT INTO seo_meta(entity_type,entity_id,title,description,keywords,og_image,canonical,no_index,updated_at)
			VALUES(?,?,?,?,?,?,?,?,datetime('now'))
			ON CONFLICT(entity_type,entity_id) DO UPDATE SET title=excluded.title,
				description=excluded.description, keywords=excluded.keywords, og_image=excluded.og_image,
				canonical=excluded.canonical, no_index=excluded.no_index, updated_at=datetime('now')`,
			et, eid, sVal(m, "title"), sVal(m, "description"), sVal(m, "keywords"),
			sVal(m, "ogImage"), sVal(m, "canonical"), boolInt(bVal(m, "noIndex", false)))
		if err != nil {
			httpx.Fail(w, httpx.CodeInternal, err.Error())
			return
		}
		audit(c, r, "seo.meta_update", et, eid, m)
		httpx.NoBody(w, "SEO metadata saved")
	})

	redirects := &Resource{
		Name: "admin/seo/redirects", Table: "redirects",
		Fields: []Field{
			{Name: "from_path", Kind: KindString, Required: true},
			{Name: "to_path", Kind: KindString, Required: true},
			{Name: "code", Kind: KindInt, Default: 301},
		},
		Searchable: []string{"from_path", "to_path"},
		OrderBy:    "id DESC", ReadRole: roleAdmin, WriteRole: roleAdmin,
	}
	redirects.Mount(rt, c.DB)

	rt.Handle(http.MethodGet, "/admin/seo/broken-links", roleAdmin, func(w http.ResponseWriter, r *http.Request) {

		orphanProducts, _ := c.DB.Maps(r.Context(), `SELECT id,slug,name FROM products
			WHERE deleted_at IS NOT NULL AND slug IN (SELECT from_path FROM redirects) LIMIT 50`)
		missingImages, _ := c.DB.Maps(r.Context(), `SELECT id,slug,name FROM products
			WHERE (thumbnail='' OR thumbnail IS NULL) AND status='published' AND deleted_at IS NULL LIMIT 50`)
		emptyMeta, _ := c.DB.Maps(r.Context(), `SELECT id,slug,name FROM products p
			WHERE p.status='published' AND p.deleted_at IS NULL
			  AND NOT EXISTS (SELECT 1 FROM seo_meta s WHERE s.entity_type='product' AND s.entity_id=CAST(p.id AS TEXT))
			LIMIT 50`)
		writeOK(w, map[string]any{
			"deletedWithRedirect": orphanProducts,
			"missingImages":       missingImages,
			"missingSeoMeta":      emptyMeta,
			"scannedAt":           time.Now().UTC().Format(time.RFC3339),
		})
	})

	rt.Handle(http.MethodPost, "/admin/seo/sitemap/regenerate", roleAdmin, func(w http.ResponseWriter, r *http.Request) {
		n, err := RegenerateSitemap(r.Context(), c)
		if err != nil {
			httpx.Fail(w, httpx.CodeInternal, err.Error())
			return
		}
		audit(c, r, "seo.sitemap_regenerate", "sitemap", "", map[string]any{"entries": n})
		httpx.Success(w, http.StatusOK, "Sitemap regenerated", map[string]any{"entries": n}, nil)
	})
}

func RegenerateSitemap(ctx context.Context, c *core.Core) (int, error) {
	xml, n, err := BuildSitemap(ctx, c)
	if err != nil {
		return 0, err
	}
	c.CacheSet("seo:sitemap", xml, 24*time.Hour)
	return n, nil
}

func BuildSitemap(ctx context.Context, c *core.Core) (string, int, error) {
	base := strings.TrimSuffix(c.Setting("site_url", c.Cfg.BaseURL), "/")
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">` + "\n")
	count := 0
	add := func(loc, lastmod, freq, prio string) {
		b.WriteString("  <url><loc>" + xmlEscape(base+loc) + "</loc>")
		if lastmod != "" {
			b.WriteString("<lastmod>" + xmlEscape(strings.Split(lastmod, " ")[0]) + "</lastmod>")
		}
		b.WriteString("<changefreq>" + freq + "</changefreq><priority>" + prio + "</priority></url>\n")
		count++
	}
	for _, p := range []string{"/", "/shop", "/blog", "/faq", "/contact"} {
		add(p, "", "daily", "0.9")
	}
	products, err := c.DB.Maps(ctx,
		`SELECT slug,updated_at FROM products WHERE status='published' AND deleted_at IS NULL LIMIT 5000`)
	if err != nil {
		return "", 0, err
	}
	for _, row := range products {
		add("/product/"+asStr(row["slug"]), asStr(row["updated_at"]), "weekly", "0.8")
	}
	cats, _ := c.DB.Maps(ctx, `SELECT slug,updated_at FROM categories WHERE active=1 AND deleted_at IS NULL`)
	for _, row := range cats {
		add("/shop?category="+asStr(row["slug"]), asStr(row["updated_at"]), "weekly", "0.7")
	}
	posts, _ := c.DB.Maps(ctx,
		`SELECT slug,updated_at FROM blog_posts WHERE status='published' AND deleted_at IS NULL LIMIT 2000`)
	for _, row := range posts {
		add("/blog/"+asStr(row["slug"]), asStr(row["updated_at"]), "monthly", "0.6")
	}
	pages, _ := c.DB.Maps(ctx, `SELECT slug,updated_at FROM pages WHERE status='published' AND deleted_at IS NULL`)
	for _, row := range pages {
		add("/page/"+asStr(row["slug"]), asStr(row["updated_at"]), "monthly", "0.5")
	}
	b.WriteString("</urlset>")
	return b.String(), count, nil
}

func SitemapHandler(c *core.Core) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if v, ok := c.CacheGet("seo:sitemap"); ok {
			if s, ok := v.(string); ok {
				w.Header().Set("Content-Type", "application/xml; charset=utf-8")
				_, _ = w.Write([]byte(s))
				return
			}
		}
		xml, _, err := BuildSitemap(r.Context(), c)
		if err != nil {
			httpx.Fail(w, httpx.CodeInternal, err.Error())
			return
		}
		c.CacheSet("seo:sitemap", xml, 6*time.Hour)
		w.Header().Set("Content-Type", "application/xml; charset=utf-8")
		_, _ = w.Write([]byte(xml))
	}
}

func RobotsHandler(c *core.Core) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		base := strings.TrimSuffix(c.Setting("site_url", c.Cfg.BaseURL), "/")
		body := c.Setting("robots_txt", "")
		if body == "" {
			body = fmt.Sprintf(`User-agent: *
Allow: /
Disallow: /admin
Disallow: /account
Disallow: /cart
Disallow: /checkout
Disallow: /api/

Sitemap: %s/sitemap.xml
`, base)
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte(body))
	}
}

func RebuildSearchIndex(ctx context.Context, c *core.Core) (string, error) {
	if _, err := c.DB.Write(ctx, `DELETE FROM products_fts`); err != nil {
		return "", err
	}
	res, err := c.DB.Write(ctx, `INSERT INTO products_fts(name,name_en,short_desc,description,sku)
		SELECT name,name_en,short_desc,description,sku FROM products
		WHERE status='published' AND deleted_at IS NULL`)
	if err != nil {
		return "", err
	}
	n, _ := res.RowsAffected()
	return fmt.Sprintf("indexed products: %d", n), nil
}
