package app

import (
	"fmt"
	"html/template"
	"net/http"
	"strings"

	"github.com/arena/avaluna/internal/api"
	"github.com/arena/avaluna/internal/i18n"
	"github.com/arena/avaluna/internal/middleware"
)

const perPage = 12

func (a *App) productCard(row map[string]any, loc string) map[string]any {
	price := num(row["price"])
	compare := num(row["compare_price"])
	discount := 0
	if compare > price && compare > 0 {
		discount = (compare - price) * 100 / compare
	}
	thumb := str(row["thumbnail"])
	if thumb == "" {
		thumb = "/static/img/placeholder.svg"
	}
	card := map[string]any{
		"id":          num(row["id"]),
		"slug":        str(row["slug"]),
		"title":       localized(row, "name", loc),
		"thumbnail":   thumb,
		"price":       i18n.Money(loc, a.c.Cfg.DefaultCurrency, int64(price)),
		"stars":       stars(flt(row["rating_avg"])),
		"ratingCount": num(row["rating_count"]),
		"featured":    num(row["featured"]) == 1,
		"discount":    discount,
	}
	if discount > 0 {
		card["comparePrice"] = i18n.Money(loc, a.c.Cfg.DefaultCurrency, int64(compare))
	}
	return card
}

func (a *App) cards(rows []map[string]any, loc string) []map[string]any {
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		out = append(out, a.productCard(row, loc))
	}
	return out
}

func (a *App) handleHome(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	vd := a.base(w, r, "")
	loc := str(vd["Loc"])

	featured, _ := a.c.DB.Maps(ctx, `SELECT * FROM products
		WHERE status='published' AND deleted_at IS NULL AND featured=1 ORDER BY id DESC LIMIT 10`)
	best, _ := a.c.DB.Maps(ctx, `SELECT * FROM products
		WHERE status='published' AND deleted_at IS NULL ORDER BY sold_count DESC LIMIT 10`)
	cats, _ := a.c.DB.Maps(ctx, `SELECT slug,name,name_en,icon FROM categories
		WHERE active=1 AND deleted_at IS NULL AND parent_id IS NOT NULL ORDER BY sort_order LIMIT 12`)
	banners, _ := a.c.DB.Maps(ctx, `SELECT title,title_en,subtitle,subtitle_en,link FROM banners
		WHERE active=1 AND position='hero' ORDER BY sort_order LIMIT 4`)
	posts, _ := a.c.DB.Maps(ctx, `SELECT slug,title,title_en,excerpt,excerpt_en,published_at
		FROM blog_posts WHERE status='published' AND deleted_at IS NULL ORDER BY id DESC LIMIT 3`)

	catOut := make([]map[string]any, 0, len(cats))
	for _, row := range cats {
		catOut = append(catOut, map[string]any{
			"slug": row["slug"], "name": localized(row, "name", loc), "icon": row["icon"],
		})
	}
	bannerOut := make([]map[string]any, 0, len(banners))
	for _, row := range banners {
		bannerOut = append(bannerOut, map[string]any{
			"title": localized(row, "title", loc), "subtitle": localized(row, "subtitle", loc),
			"link": row["link"],
		})
	}
	postOut := make([]map[string]any, 0, len(posts))
	for _, row := range posts {
		postOut = append(postOut, map[string]any{
			"slug": row["slug"], "title": localized(row, "title", loc),
			"excerpt": localized(row, "excerpt", loc), "date": shortDate(str(row["published_at"])),
		})
	}

	nProducts, _ := a.c.DB.Count(ctx, `SELECT COUNT(*) FROM products WHERE status='published' AND deleted_at IS NULL`)
	nOrders, _ := a.c.DB.Count(ctx, `SELECT COUNT(*) FROM orders`)
	nUsers, _ := a.c.DB.Count(ctx, `SELECT COUNT(*) FROM users WHERE role='customer' AND deleted_at IS NULL`)

	vd["Featured"] = a.cards(featured, loc)
	vd["Bestsellers"] = a.cards(best, loc)
	vd["Categories"] = catOut
	vd["Banners"] = bannerOut
	vd["Posts"] = postOut
	vd["Stats"] = map[string]any{
		"products":  i18n.Num(loc, int64(nProducts)),
		"orders":    i18n.Num(loc, int64(nOrders)),
		"customers": i18n.Num(loc, int64(nUsers)),
	}
	vd["Trust"] = []map[string]string{
		{"Icon": "🚚", "Text": vd.T("home.trust.shipping")},
		{"Icon": "↩️", "Text": vd.T("home.trust.returns")},
		{"Icon": "🔒", "Text": vd.T("home.trust.secure")},
		{"Icon": "💬", "Text": vd.T("home.trust.support")},
	}
	a.render(w, r, "home", vd)
}

func (a *App) handleShop(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	vd := a.base(w, r, "")
	loc := str(vd["Loc"])
	q := r.URL.Query()

	where := []string{"p.status='published'", "p.deleted_at IS NULL"}
	args := []any{}
	if s := strings.TrimSpace(q.Get("q")); s != "" {
		where = append(where, "(p.name LIKE ? OR p.name_en LIKE ? OR p.sku LIKE ?)")
		like := "%" + s + "%"
		args = append(args, like, like, like)
	}
	if s := q.Get("category"); s != "" {
		where = append(where, `p.category_id IN (SELECT id FROM categories WHERE slug=?
			UNION SELECT id FROM categories WHERE parent_id=(SELECT id FROM categories WHERE slug=?))`)
		args = append(args, s, s)
	}
	if s := q.Get("brand"); s != "" {
		where = append(where, "p.brand_id=(SELECT id FROM brands WHERE slug=?)")
		args = append(args, s)
	}
	if n := qInt(r, "minPrice", 0); n > 0 {
		where = append(where, "p.price >= ?")
		args = append(args, n)
	}
	if n := qInt(r, "maxPrice", 0); n > 0 {
		where = append(where, "p.price <= ?")
		args = append(args, n)
	}
	if q.Get("featured") == "1" {
		where = append(where, "p.featured=1")
	}

	order := "p.id DESC"
	switch q.Get("sort") {
	case "popular":
		order = "p.sold_count DESC"
	case "rating":
		order = "p.rating_avg DESC"
	case "price_asc":
		order = "p.price ASC"
	case "price_desc":
		order = "p.price DESC"
	}

	clause := "WHERE " + strings.Join(where, " AND ")
	total, _ := a.c.DB.Count(ctx, "SELECT COUNT(*) FROM products p "+clause, args...)
	page := qInt(r, "page", 1)
	if page < 1 {
		page = 1
	}
	rows, _ := a.c.DB.Maps(ctx,
		fmt.Sprintf("SELECT p.* FROM products p %s ORDER BY %s LIMIT ? OFFSET ?", clause, order),
		append(append([]any{}, args...), perPage, (page-1)*perPage)...)

	cats, _ := a.c.DB.Maps(ctx, `SELECT slug,name,name_en FROM categories
		WHERE active=1 AND deleted_at IS NULL ORDER BY sort_order`)
	brands, _ := a.c.DB.Maps(ctx, `SELECT slug,name,name_en FROM brands WHERE active=1 ORDER BY name`)
	catOut := make([]map[string]any, 0, len(cats))
	for _, row := range cats {
		catOut = append(catOut, map[string]any{"slug": row["slug"], "name": localized(row, "name", loc)})
	}
	brandOut := make([]map[string]any, 0, len(brands))
	for _, row := range brands {
		brandOut = append(brandOut, map[string]any{"slug": row["slug"], "name": localized(row, "name", loc)})
	}

	sortVal := q.Get("sort")
	if sortVal == "" {
		sortVal = "newest"
	}
	heading := vd.T("nav.shop")
	if s := q.Get("category"); s != "" {
		for _, cRow := range catOut {
			if cRow["slug"] == s {
				heading = str(cRow["name"])
			}
		}
	}

	vd["Title"] = heading
	vd["Heading"] = heading
	vd["Products"] = a.cards(rows, loc)
	vd["Categories"] = catOut
	vd["Brands"] = brandOut
	vd["Total"] = i18n.Num(loc, int64(total))
	vd["Filters"] = map[string]string{
		"category": q.Get("category"), "brand": q.Get("brand"),
		"minPrice": q.Get("minPrice"), "maxPrice": q.Get("maxPrice"), "sort": sortVal,
	}
	vd["Pager"] = a.pager(r, page, total, vd)
	a.render(w, r, "shop", vd)
}

func (a *App) pager(r *http.Request, page, total int, vd VD) map[string]any {
	pages := (total + perPage - 1) / perPage
	q := r.URL.Query()
	q.Del("page")
	base := r.URL.Path + "?"
	if enc := q.Encode(); enc != "" {
		base += enc + "&"
	}
	return map[string]any{
		"Page": page, "Pages": pages, "Base": template.URL(base),
		"PrevLabel": vd.T("common.prev"), "NextLabel": vd.T("common.next"),
	}
}

func (a *App) handleSearch(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	vd := a.base(w, r, "")
	loc := str(vd["Loc"])
	q := strings.TrimSpace(r.URL.Query().Get("q"))

	rows := []map[string]any{}
	if q != "" {
		like := "%" + q + "%"
		rows, _ = a.c.DB.Maps(ctx, `SELECT * FROM products
			WHERE status='published' AND deleted_at IS NULL
			  AND (name LIKE ? OR name_en LIKE ? OR sku LIKE ? OR short_desc LIKE ?)
			ORDER BY sold_count DESC LIMIT 40`, like, like, like, like)
		_, _ = a.c.DB.Write(ctx, `INSERT INTO search_log(query,results,user_id) VALUES(?,?,?)`,
			q, len(rows), nilZero(middleware.UserID(ctx)))
	}
	vd["Title"] = vd.T("nav.search") + ": " + q
	vd["Query"] = q
	vd["Products"] = a.cards(rows, loc)
	vd["Total"] = i18n.Num(loc, int64(len(rows)))
	a.render(w, r, "search", vd)
}

func nilZero(n int) any {
	if n == 0 {
		return nil
	}
	return n
}

func (a *App) handleSuggest(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if len(q) < 2 {
		return
	}
	loc := middleware.Locale(r.Context())
	rows, _ := a.c.DB.Maps(r.Context(), `SELECT slug,name,name_en,thumbnail,price FROM products
		WHERE status='published' AND deleted_at IS NULL AND (name LIKE ? OR name_en LIKE ?)
		ORDER BY sold_count DESC LIMIT 6`, "%"+q+"%", "%"+q+"%")
	if len(rows) == 0 {
		return
	}
	var b strings.Builder
	b.WriteString(`<div class="card overflow-hidden p-1 shadow-soft">`)
	for _, row := range rows {
		b.WriteString(fmt.Sprintf(
			`<a href="/product/%s" class="flex items-center gap-2 rounded-lg px-2 py-1.5 hover:bg-slate-50">
			 <img src="%s" class="h-8 w-8 rounded object-cover" alt="">
			 <span class="flex-1 truncate text-xs text-slate-700">%s</span>
			 <span class="text-[11px] font-semibold text-slate-500">%s</span></a>`,
			template.HTMLEscapeString(str(row["slug"])),
			template.HTMLEscapeString(str(row["thumbnail"])),
			template.HTMLEscapeString(localized(row, "name", loc)),
			template.HTMLEscapeString(i18n.Money(loc, a.c.Cfg.DefaultCurrency, int64(num(row["price"]))))))
	}
	b.WriteString(`</div>`)
	_, _ = w.Write([]byte(b.String()))
}

func (a *App) handleProduct(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	slug := r.PathValue("slug")
	row, err := a.c.DB.MapOne(ctx,
		`SELECT p.*, b.name AS brand_name, b.name_en AS brand_name_en,
		        c.name AS cat_name, c.name_en AS cat_name_en
		 FROM products p LEFT JOIN brands b ON b.id=p.brand_id
		 LEFT JOIN categories c ON c.id=p.category_id
		 WHERE p.slug=? AND p.deleted_at IS NULL`, slug)
	if err != nil {
		a.notFound(w, r)
		return
	}
	_, _ = a.c.DB.Write(ctx, `UPDATE products SET view_count = view_count + 1 WHERE id=?`, row["id"])

	vd := a.base(w, r, "")
	loc := str(vd["Loc"])
	card := a.productCard(row, loc)
	card["sku"] = row["sku"]
	card["sold"] = num(row["sold_count"])
	card["shortDesc"] = localized(row, "short_desc", loc)
	card["brand"] = localized(row, "brand_name", loc)
	card["category"] = localized(row, "cat_name", loc)

	variants, _ := a.c.DB.Maps(ctx,
		`SELECT id,title,price FROM product_variants WHERE product_id=? AND active=1`, row["id"])
	varOut := make([]map[string]any, 0, len(variants))
	totalStock := 0
	for _, v := range variants {
		price := num(v["price"])
		if price == 0 {
			price = num(row["price"])
		}
		stock := api.AvailableStock(ctx, a.c, num(v["id"]))
		totalStock += stock
		varOut = append(varOut, map[string]any{
			"id": v["id"], "title": str(v["title"]),
			"price": i18n.Money(loc, a.c.Cfg.DefaultCurrency, int64(price)),
		})
	}

	reviews, _ := a.c.DB.Maps(ctx, `SELECT r.rating,r.title,r.body,r.created_at,
		COALESCE(u.first_name,'—') AS author FROM reviews r LEFT JOIN users u ON u.id=r.user_id
		WHERE r.product_id=? AND r.status='approved' ORDER BY r.id DESC LIMIT 12`, row["id"])
	revOut := make([]map[string]any, 0, len(reviews))
	for _, rv := range reviews {
		revOut = append(revOut, map[string]any{
			"title": rv["title"], "body": rv["body"], "author": rv["author"],
			"date": shortDate(str(rv["created_at"])), "stars": stars(flt(rv["rating"])),
		})
	}

	related, _ := a.c.DB.Maps(ctx, `SELECT * FROM products
		WHERE category_id=? AND id<>? AND status='published' AND deleted_at IS NULL
		ORDER BY sold_count DESC LIMIT 5`, row["category_id"], row["id"])

	stockLabel := fmt.Sprintf("%d", totalStock)
	if totalStock <= 0 {
		stockLabel = vd.T("common.none")
	}

	vd["Title"] = localized(row, "name", loc)
	vd["MetaDescription"] = localized(row, "short_desc", loc)
	vd["P"] = card
	vd["Variants"] = varOut
	vd["Reviews"] = revOut
	vd["Related"] = a.cards(related, loc)
	vd["Stock"] = stockLabel
	vd["RatingOptions"] = []int{5, 4, 3, 2, 1}
	vd["Description"] = template.HTML(localized(row, "description", loc))
	a.render(w, r, "product", vd)
}

func (a *App) handleReviewCreate(w http.ResponseWriter, r *http.Request) {
	uid := middleware.UserID(r.Context())
	if uid == 0 {
		a.fragment(w, false, "Please sign in first.")
		return
	}
	_ = r.ParseForm()
	pid := r.PathValue("id")
	rating := r.FormValue("rating")
	if _, err := a.c.DB.Write(r.Context(),
		`INSERT INTO reviews(product_id,user_id,rating,title,body,status) VALUES(?,?,?,?,?,'pending')`,
		pid, uid, rating, r.FormValue("title"), r.FormValue("body")); err != nil {
		a.fragment(w, false, err.Error())
		return
	}
	a.fragment(w, true, "Thanks! Your review is awaiting moderation.")
}

func (a *App) handleBlog(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	vd := a.base(w, r, "")
	loc := str(vd["Loc"])
	page := qInt(r, "page", 1)
	if page < 1 {
		page = 1
	}
	total, _ := a.c.DB.Count(ctx,
		`SELECT COUNT(*) FROM blog_posts WHERE status='published' AND deleted_at IS NULL`)
	rows, _ := a.c.DB.Maps(ctx, `SELECT p.*, c.name AS cat, c.name_en AS cat_en
		FROM blog_posts p LEFT JOIN blog_categories c ON c.id=p.category_id
		WHERE p.status='published' AND p.deleted_at IS NULL ORDER BY p.id DESC LIMIT ? OFFSET ?`,
		perPage, (page-1)*perPage)
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		out = append(out, map[string]any{
			"slug": row["slug"], "title": localized(row, "title", loc),
			"excerpt": localized(row, "excerpt", loc), "views": row["views"],
			"category": localized(row, "cat", loc), "date": shortDate(str(row["published_at"])),
		})
	}
	vd["Title"] = vd.T("nav.blog")
	vd["Posts"] = out
	vd["Pager"] = a.pager(r, page, total, vd)
	a.render(w, r, "blog", vd)
}

func (a *App) handlePost(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	row, err := a.c.DB.MapOne(ctx,
		`SELECT * FROM blog_posts WHERE slug=? AND deleted_at IS NULL`, r.PathValue("slug"))
	if err != nil {
		a.notFound(w, r)
		return
	}
	_, _ = a.c.DB.Write(ctx, `UPDATE blog_posts SET views = views + 1 WHERE id=?`, row["id"])
	vd := a.base(w, r, "")
	loc := str(vd["Loc"])
	comments, _ := a.c.DB.Maps(ctx, `SELECT c.body, COALESCE(u.first_name,'—') AS author
		FROM blog_comments c LEFT JOIN users u ON u.id=c.user_id
		WHERE c.post_id=? AND c.status='approved' ORDER BY c.id DESC LIMIT 30`, row["id"])
	vd["Title"] = localized(row, "title", loc)
	vd["Post"] = map[string]any{
		"slug": row["slug"], "title": localized(row, "title", loc),
		"views": row["views"], "tags": row["tags"], "date": shortDate(str(row["published_at"])),
	}
	vd["Body"] = template.HTML(localized(row, "body", loc))
	vd["Comments"] = comments
	a.render(w, r, "post", vd)
}

func (a *App) handleComment(w http.ResponseWriter, r *http.Request) {
	uid := middleware.UserID(r.Context())
	if uid == 0 {
		a.fragment(w, false, "Please sign in first.")
		return
	}
	_ = r.ParseForm()
	var pid int
	if err := a.c.DB.Row(r.Context(), `SELECT id FROM blog_posts WHERE slug=?`,
		[]any{r.PathValue("slug")}, &pid); err != nil {
		a.fragment(w, false, "Post not found")
		return
	}
	if _, err := a.c.DB.Write(r.Context(),
		`INSERT INTO blog_comments(post_id,user_id,body,status) VALUES(?,?,?,'pending')`,
		pid, uid, r.FormValue("body")); err != nil {
		a.fragment(w, false, err.Error())
		return
	}
	a.fragment(w, true, "Comment submitted for moderation.")
}

func (a *App) handlePage(w http.ResponseWriter, r *http.Request) {
	row, err := a.c.DB.MapOne(r.Context(),
		`SELECT * FROM pages WHERE slug=? AND status='published' AND deleted_at IS NULL`,
		r.PathValue("slug"))
	if err != nil {
		a.notFound(w, r)
		return
	}
	vd := a.base(w, r, "")
	loc := str(vd["Loc"])
	vd["Title"] = localized(row, "title", loc)
	vd["Page"] = map[string]any{"title": localized(row, "title", loc)}
	vd["Body"] = template.HTML(localized(row, "body", loc))
	a.render(w, r, "page", vd)
}

func (a *App) handleFAQ(w http.ResponseWriter, r *http.Request) {
	vd := a.base(w, r, "")
	loc := str(vd["Loc"])
	rows, _ := a.c.DB.Maps(r.Context(),
		`SELECT question,question_en,answer,answer_en FROM faqs WHERE active=1 ORDER BY sort_order`)
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		out = append(out, map[string]any{
			"question": localized(row, "question", loc), "answer": localized(row, "answer", loc),
		})
	}
	vd["Title"] = vd.T("nav.faq")
	vd["FAQs"] = out
	a.render(w, r, "faq", vd)
}

func (a *App) handleLang(w http.ResponseWriter, r *http.Request) {
	code := r.PathValue("code")
	if _, ok := i18n.Locales[code]; !ok {
		code = a.c.Cfg.DefaultLocale
	}
	http.SetCookie(w, &http.Cookie{
		Name: i18n.CookieName, Value: code, Path: "/", MaxAge: 365 * 24 * 3600,
		SameSite: http.SameSiteLaxMode, Secure: a.c.Cfg.CookieSecure,
	})
	next := r.URL.Query().Get("next")
	if next == "" || !strings.HasPrefix(next, "/") {
		next = "/"
	}
	http.Redirect(w, r, next, http.StatusSeeOther)
}

func (a *App) notFound(w http.ResponseWriter, r *http.Request) {
	vd := a.base(w, r, "404")
	vd["Code"] = 404
	vd["Message"] = vd.T("common.empty")
	w.WriteHeader(http.StatusNotFound)
	a.render(w, r, "error", vd)
}

func (a *App) fragment(w http.ResponseWriter, ok bool, msg string) {
	cls := "rounded-lg bg-rose-50 px-3 py-2 text-xs text-rose-700"
	if ok {
		cls = "rounded-lg bg-emerald-50 px-3 py-2 text-xs text-emerald-700"
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = fmt.Fprintf(w, `<div class="%s">%s</div>`, cls, template.HTMLEscapeString(msg))
}
