package app

import (
	"net/http"

	"github.com/arena/avaluna/internal/api"
)

func (a *App) routes() {
	m := a.mux

	m.Handle("GET /static/", a.static)
	m.HandleFunc("GET /sitemap.xml", api.SitemapHandler(a.c))
	m.HandleFunc("GET /robots.txt", api.RobotsHandler(a.c))
	m.HandleFunc("GET /favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/static/img/favicon.svg", http.StatusFound)
	})

	m.HandleFunc("GET /{$}", a.handleHome)
	m.HandleFunc("GET /shop", a.handleShop)
	m.HandleFunc("GET /search", a.handleSearch)
	m.HandleFunc("GET /product/{slug}", a.handleProduct)
	m.HandleFunc("POST /product/{id}/review", a.handleReviewCreate)
	m.HandleFunc("GET /blog", a.handleBlog)
	m.HandleFunc("GET /blog/{slug}", a.handlePost)
	m.HandleFunc("POST /blog/{slug}/comment", a.handleComment)
	m.HandleFunc("GET /page/{slug}", a.handlePage)
	m.HandleFunc("GET /faq", a.handleFAQ)
	m.HandleFunc("GET /lang/{code}", a.handleLang)
	m.HandleFunc("GET /partials/suggest", a.handleSuggest)

	m.HandleFunc("GET /cart", a.handleCart)
	m.HandleFunc("POST /cart/add", a.handleCartAdd)
	m.HandleFunc("POST /cart/update", a.handleCartUpdate)
	m.HandleFunc("POST /cart/remove", a.handleCartRemove)
	m.HandleFunc("POST /cart/coupon", a.handleCartCoupon)
	m.HandleFunc("GET /checkout", a.handleCheckoutPage)
	m.HandleFunc("POST /checkout", a.handleCheckoutSubmit)
	m.HandleFunc("GET /order/{number}", a.handleOrder)
	m.HandleFunc("POST /pay/simulate", a.handlePaySimulate)

	m.HandleFunc("GET /login", a.handleLoginPage)
	m.HandleFunc("POST /login", a.handleLoginSubmit)
	m.HandleFunc("GET /register", a.handleRegisterPage)
	m.HandleFunc("POST /register", a.handleRegisterSubmit)
	m.HandleFunc("GET /forgot", a.handleForgot)
	m.HandleFunc("GET /logout", a.handleLogout)
	m.HandleFunc("GET /account", a.handleAccount)
	m.HandleFunc("GET /account/{tab}", a.handleAccount)
	m.HandleFunc("POST /account/wishlist/{id}", a.handleWishlistAdd)
	m.HandleFunc("POST /compare/{id}", a.handleCompareAdd)

	m.HandleFunc("GET /admin", a.requireStaff(a.handleAdminDashboard))
	m.HandleFunc("GET /admin/{section}", a.requireStaff(a.handleAdminSection))
	m.HandleFunc("POST /admin/settings", a.requireStaff(a.handleAdminSettingsSave))
	m.HandleFunc("POST /admin/flags/{key}/toggle", a.requireStaff(a.handleAdminFlagToggle))
	m.HandleFunc("POST /admin/cron/{name}/{action}", a.requireStaff(a.handleAdminCronAction))
	m.HandleFunc("POST /admin/system/{action}", a.requireStaff(a.handleAdminSystemAction))
}
