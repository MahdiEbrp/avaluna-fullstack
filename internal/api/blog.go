package api

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/arena/avaluna/internal/core"
	"github.com/arena/avaluna/internal/httpx"
	"github.com/arena/avaluna/internal/middleware"
)

func registerBlogModule(rt *Router, c *core.Core) {
	rt.Module("13-blog")

	cats := &Resource{
		Name: "blog/categories", Table: "blog_categories",
		Fields: []Field{
			{Name: "slug", Kind: KindSlug, Required: true, From: "name"},
			{Name: "name", Kind: KindString, Required: true},
			{Name: "name_en", Kind: KindString},
		},
		OrderBy: "id ASC", WriteRole: roleEditor,
	}
	cats.Mount(rt, c.DB)

	posts := &Resource{
		Name: "blog/posts", Table: "blog_posts", SoftDelete: true,
		Fields: []Field{
			{Name: "slug", Kind: KindSlug, Required: true, From: "title"},
			{Name: "title", Kind: KindString, Required: true},
			{Name: "title_en", Kind: KindString},
			{Name: "excerpt", Kind: KindString},
			{Name: "excerpt_en", Kind: KindString},
			{Name: "body", Kind: KindString},
			{Name: "body_en", Kind: KindString},
			{Name: "cover_url", Kind: KindString},
			{Name: "category_id", Kind: KindInt},
			{Name: "author_id", Kind: KindInt},
			{Name: "tags", Kind: KindString},
			{Name: "status", Kind: KindString, Default: "published"},
			{Name: "published_at", Kind: KindString},
		},
		Searchable: []string{"title", "title_en", "excerpt", "tags"},
		Filters:    []string{"status", "category_id", "author_id"},
		OrderBy:    "published_at DESC, id DESC", WriteRole: roleEditor,
	}
	rt.Handle(http.MethodPost, "/blog/posts", roleEditor, posts.Create(c.DB))
	rt.Handle(http.MethodPut, "/blog/posts/{id}", roleEditor, posts.Update(c.DB))
	rt.Handle(http.MethodPatch, "/blog/posts/{id}", roleEditor, posts.Update(c.DB))
	rt.Handle(http.MethodDelete, "/blog/posts/{id}", roleAdmin, posts.Delete(c.DB))

	rt.Handle(http.MethodGet, "/blog/posts", "", func(w http.ResponseWriter, r *http.Request) {
		page, limit, offset := paginate(r)
		conds := []string{"bp.deleted_at IS NULL"}
		args := []any{}
		if s := r.URL.Query().Get("status"); s != "" {
			conds, args = append(conds, "bp.status=?"), append(args, s)
		} else {
			conds = append(conds, "bp.status='published'")
		}
		if v := r.URL.Query().Get("category"); v != "" {
			conds = append(conds, "bp.category_id IN (SELECT id FROM blog_categories WHERE slug=? OR id=?)")
			args = append(args, v, v)
		}
		if v := strings.TrimSpace(r.URL.Query().Get("q")); v != "" {
			conds = append(conds, "(bp.title LIKE ? OR bp.title_en LIKE ? OR bp.tags LIKE ?)")
			args = append(args, "%"+v+"%", "%"+v+"%", "%"+v+"%")
		}
		where := "WHERE " + strings.Join(conds, " AND ")
		total, _ := c.DB.Count(r.Context(), "SELECT COUNT(*) FROM blog_posts bp "+where, args...)
		rows, err := c.DB.Maps(r.Context(), fmt.Sprintf(`
			SELECT bp.id,bp.slug,bp.title,bp.title_en,bp.excerpt,bp.excerpt_en,bp.cover_url,bp.tags,
			       bp.views,bp.published_at,bp.created_at,
			       bc.name AS category_name, bc.slug AS category_slug,
			       COALESCE(NULLIF(TRIM(u.first_name||' '||u.last_name),''),'Editor') AS author
			FROM blog_posts bp
			LEFT JOIN blog_categories bc ON bc.id=bp.category_id
			LEFT JOIN users u ON u.id=bp.author_id
			%s ORDER BY COALESCE(bp.published_at,bp.created_at) DESC LIMIT ? OFFSET ?`, where),
			append(args, limit, offset)...)
		if err != nil {
			httpx.Fail(w, httpx.CodeInternal, err.Error())
			return
		}
		httpx.List(w, rows, httpx.MakePagination(page, limit, total))
	})

	rt.Handle(http.MethodGet, "/blog/posts/{id}", "", func(w http.ResponseWriter, r *http.Request) {
		key := r.PathValue("id")
		col := "slug"
		if _, err := strconv.Atoi(key); err == nil {
			col = "id"
		}
		post, err := c.DB.MapOne(r.Context(), fmt.Sprintf(`
			SELECT bp.*, bc.name AS category_name, bc.slug AS category_slug,
			       COALESCE(NULLIF(TRIM(u.first_name||' '||u.last_name),''),'Editor') AS author
			FROM blog_posts bp
			LEFT JOIN blog_categories bc ON bc.id=bp.category_id
			LEFT JOIN users u ON u.id=bp.author_id
			WHERE bp.%s=? AND bp.deleted_at IS NULL`, col), key)
		if err != nil {
			httpx.Fail(w, httpx.CodeNotFound, "")
			return
		}
		_, _ = c.DB.Write(r.Context(), `UPDATE blog_posts SET views=views+1 WHERE id=?`, post["id"])
		comments, _ := c.DB.Maps(r.Context(), `
			SELECT bcm.id,bcm.body,bcm.created_at,
			       COALESCE(NULLIF(TRIM(u.first_name||' '||u.last_name),''),'Guest') AS author
			FROM blog_comments bcm LEFT JOIN users u ON u.id=bcm.user_id
			WHERE bcm.post_id=? AND bcm.status='approved' ORDER BY bcm.id DESC`, post["id"])
		related, _ := c.DB.Maps(r.Context(),
			`SELECT id,slug,title,title_en,cover_url FROM blog_posts
			 WHERE category_id=? AND id<>? AND status='published' AND deleted_at IS NULL LIMIT 4`,
			post["category_id"], post["id"])
		writeOK(w, map[string]any{"post": post, "comments": comments, "related": related})
	})

	rt.Handle(http.MethodPost, "/blog/posts/{id}/comments", RoleAuthenticated, func(w http.ResponseWriter, r *http.Request) {
		id, ok := httpx.PathInt(r, "id")
		if !ok {
			httpx.Fail(w, httpx.CodeValidation, "Invalid post id")
			return
		}
		m, _ := body(r)
		text := sVal(m, "body")
		if len(text) < 3 {
			httpx.Fail(w, httpx.CodeValidation, "", httpx.FieldIssue{Field: "body", Issue: "is too short"})
			return
		}
		status := "pending"
		if c.SettingBool("blog_auto_approve_comments", true) {
			status = "approved"
		}
		res, err := c.DB.Write(r.Context(), `INSERT INTO blog_comments(post_id,user_id,body,status) VALUES(?,?,?,?)`,
			id, middleware.UserID(r.Context()), text, status)
		if err != nil {
			httpx.Fail(w, httpx.CodeInternal, err.Error())
			return
		}
		cid, _ := res.LastInsertId()
		writeCreated(w, map[string]any{"id": cid, "status": status})
	})

	rt.Handle(http.MethodGet, "/blog/rss", "", func(w http.ResponseWriter, r *http.Request) {
		rows, _ := c.DB.Maps(r.Context(), `SELECT slug,title,excerpt,COALESCE(published_at,created_at) AS pub
			FROM blog_posts WHERE status='published' AND deleted_at IS NULL
			ORDER BY COALESCE(published_at,created_at) DESC LIMIT 50`)
		base := c.Setting("site_url", c.Cfg.BaseURL)
		var b strings.Builder
		b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
		b.WriteString(`<rss version="2.0"><channel>` + "\n")
		b.WriteString("<title>" + xmlEscape(c.Setting("site_name", c.Cfg.AppName)) + "</title>\n")
		b.WriteString("<link>" + xmlEscape(base) + "/blog</link>\n")
		b.WriteString("<description>" + xmlEscape(c.Setting("site_description", "Blog feed")) + "</description>\n")
		for _, row := range rows {
			b.WriteString("<item><title>" + xmlEscape(asStr(row["title"])) + "</title>")
			b.WriteString("<link>" + xmlEscape(base+"/blog/"+asStr(row["slug"])) + "</link>")
			b.WriteString("<description>" + xmlEscape(asStr(row["excerpt"])) + "</description>")
			b.WriteString("<pubDate>" + xmlEscape(asStr(row["pub"])) + "</pubDate></item>\n")
		}
		b.WriteString("</channel></rss>")
		w.Header().Set("Content-Type", "application/rss+xml; charset=utf-8")
		_, _ = w.Write([]byte(b.String()))
	})
}

func xmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;")
	return r.Replace(s)
}
