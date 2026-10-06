package api

import (
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/arena/avaluna/internal/auth"
	"github.com/arena/avaluna/internal/core"
	"github.com/arena/avaluna/internal/middleware"
)

const RoleAuthenticated = "@auth"

type Route struct {
	Method  string `json:"method"`
	Pattern string `json:"pattern"`
	Access  string `json:"access"`
	Module  string `json:"module"`
}

type Router struct {
	mux    *http.ServeMux
	c      *core.Core
	prefix string
	module string

	mu     sync.Mutex
	routes []Route
}

func NewRouter(mux *http.ServeMux, c *core.Core, prefix string) *Router {
	return &Router{mux: mux, c: c, prefix: strings.TrimSuffix(prefix, "/")}
}

func (rt *Router) Module(name string) *Router {
	rt.module = name
	return rt
}

func (rt *Router) Handle(method, pattern, role string, h http.HandlerFunc) {
	rt.HandleWith(method, pattern, role, h)
}

func (rt *Router) HandleWith(method, pattern, role string, h http.HandlerFunc,
	extra ...func(http.Handler) http.Handler) {
	var handler http.Handler = h
	for i := len(extra) - 1; i >= 0; i-- {
		handler = extra[i](handler)
	}
	switch role {
	case "", "public":

	case RoleAuthenticated:
		handler = middleware.RequireAuth(handler)
	default:
		handler = middleware.RequireRole(role)(handler)
	}
	full := method + " " + rt.prefix + pattern
	rt.mux.Handle(full, handler)

	access := role
	if access == "" {
		access = "public"
	}
	if access == RoleAuthenticated {
		access = "authenticated"
	}
	rt.mu.Lock()
	rt.routes = append(rt.routes, Route{Method: method, Pattern: rt.prefix + pattern, Access: access, Module: rt.module})
	rt.mu.Unlock()
}

func (rt *Router) Limit(scope string, n int, window time.Duration) func(http.Handler) http.Handler {
	return rt.c.RL.Limit(scope, n, window)
}

func (rt *Router) Routes() []Route {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	out := make([]Route, len(rt.routes))
	copy(out, rt.routes)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Module != out[j].Module {
			return out[i].Module < out[j].Module
		}
		return out[i].Pattern < out[j].Pattern
	})
	return out
}

func RegisterAll(mux *http.ServeMux, c *core.Core) *Router {
	MarkUpdatedAt(
		"users", "pages", "menus", "banners", "faqs", "categories", "brands",
		"attributes", "products", "product_variants", "warehouses", "carts",
		"cart_items", "orders", "payments", "coupons", "reviews", "blog_posts",
		"vendors", "returns", "cron_jobs", "shipments", "addresses",
	)

	rt := NewRouter(mux, c, c.Cfg.APIPrefix)

	registerAuthModule(rt, c)
	registerCMSModule(rt, c)
	registerCatalogModule(rt, c)
	registerInventoryModule(rt, c)
	registerCartModule(rt, c)
	registerCheckoutModule(rt, c)
	registerPaymentsModule(rt, c)
	registerShippingModule(rt, c)
	registerDiscountsModule(rt, c)
	registerTaxModule(rt, c)
	registerReviewsModule(rt, c)
	registerWishlistModule(rt, c)
	registerBlogModule(rt, c)
	registerVendorsModule(rt, c)
	registerReturnsModule(rt, c)
	registerMediaModule(rt, c)
	registerNotifyModule(rt, c)
	registerSearchModule(rt, c)
	registerAdminModule(rt, c)
	registerWebhookModule(rt, c)
	registerReportsModule(rt, c)
	registerCronModule(rt, c)
	registerHealthModule(rt, c)
	registerSystemModule(rt, c)
	registerSEOModule(rt, c)
	registerLocaleModule(rt, c)
	registerLoyaltyModule(rt, c)
	registerSubsModule(rt, c)
	registerImportModule(rt, c)
	registerQueueModule(rt, c)
	registerFlagsModule(rt, c)

	rt.Module("meta")
	rt.Handle(http.MethodGet, "/_routes", "", func(w http.ResponseWriter, r *http.Request) {
		writeOK(w, map[string]any{"count": len(rt.Routes()), "routes": rt.Routes()})
	})
	rt.Handle(http.MethodGet, "/_errors", "", func(w http.ResponseWriter, r *http.Request) {
		writeOK(w, errorCodeTable())
	})
	return rt
}

const (
	roleAdmin  = auth.RoleAdmin
	roleSuper  = auth.RoleSuperAdmin
	roleEditor = auth.RoleEditor
	roleVendor = auth.RoleVendor
)
