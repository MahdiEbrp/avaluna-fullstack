package api

import (
	"encoding/json"
	"net/http"

	"github.com/arena/avaluna/internal/core"
	"github.com/arena/avaluna/internal/httpx"
)

func registerCMSModule(rt *Router, c *core.Core) {
	rt.Module("02-cms")

	pages := &Resource{
		Name: "cms/pages", Table: "pages", SoftDelete: true,
		Fields: []Field{
			{Name: "slug", Kind: KindSlug, Required: true, From: "title"},
			{Name: "title", Kind: KindString, Required: true},
			{Name: "title_en", Kind: KindString},
			{Name: "body", Kind: KindString},
			{Name: "body_en", Kind: KindString},
			{Name: "status", Kind: KindString, Default: "published"},
			{Name: "template", Kind: KindString, Default: "default"},
		},
		Searchable: []string{"title", "title_en", "slug"},
		Filters:    []string{"status", "template"},
		OrderBy:    "id DESC", WriteRole: roleEditor,
	}
	pages.Mount(rt, c.DB)

	banners := &Resource{
		Name: "cms/banners", Table: "banners",
		Fields: []Field{
			{Name: "title", Kind: KindString, Required: true},
			{Name: "title_en", Kind: KindString},
			{Name: "subtitle", Kind: KindString},
			{Name: "subtitle_en", Kind: KindString},
			{Name: "image_url", Kind: KindString},
			{Name: "link", Kind: KindString},
			{Name: "position", Kind: KindString, Default: "hero"},
			{Name: "sort_order", Kind: KindInt},
			{Name: "active", Kind: KindBool, Default: 1},
			{Name: "starts_at", Kind: KindString},
			{Name: "ends_at", Kind: KindString},
		},
		Filters: []string{"position", "active"},
		OrderBy: "sort_order ASC, id DESC", WriteRole: roleEditor,
	}
	banners.Mount(rt, c.DB)

	faqs := &Resource{
		Name: "cms/faqs", Table: "faqs",
		Fields: []Field{
			{Name: "question", Kind: KindString, Required: true},
			{Name: "question_en", Kind: KindString},
			{Name: "answer", Kind: KindString, Required: true},
			{Name: "answer_en", Kind: KindString},
			{Name: "category", Kind: KindString, Default: "general"},
			{Name: "sort_order", Kind: KindInt},
			{Name: "active", Kind: KindBool, Default: 1},
		},
		Searchable: []string{"question", "question_en"},
		Filters:    []string{"category", "active"},
		OrderBy:    "sort_order ASC, id ASC", WriteRole: roleEditor,
	}
	faqs.Mount(rt, c.DB)

	menus := &Resource{
		Name: "cms/menus", Table: "menus",
		Fields: []Field{
			{Name: "code", Kind: KindString, Required: true},
			{Name: "name", Kind: KindString, Required: true},
			{Name: "items_json", Kind: KindJSON, Default: "[]"},
		},
		OrderBy: "id ASC", WriteRole: roleAdmin,
	}
	menus.Mount(rt, c.DB)

	rt.Handle(http.MethodGet, "/cms/settings", "", func(w http.ResponseWriter, r *http.Request) {
		writeOK(w, settingsPublic(c))
	})
	rt.Handle(http.MethodPatch, "/cms/settings", roleSuper, func(w http.ResponseWriter, r *http.Request) {
		m, err := body(r)
		if err != nil {
			httpx.Fail(w, httpx.CodeValidation, err.Error())
			return
		}
		for k, v := range m {

			if err := c.SetSetting(r.Context(), k, toString(v), "public"); err != nil {
				httpx.Fail(w, httpx.CodeInternal, err.Error())
				return
			}
		}
		audit(c, r, "cms.settings_update", "settings", "", m)
		httpx.Success(w, http.StatusOK, "Settings updated", settingsPublic(c), nil)
	})

	rt.Handle(http.MethodGet, "/cms/menus/{code}/tree", "", func(w http.ResponseWriter, r *http.Request) {
		row, err := c.DB.MapOne(r.Context(), `SELECT code,name,items_json FROM menus WHERE code=?`, r.PathValue("code"))
		if err != nil {
			httpx.Fail(w, httpx.CodeNotFound, "")
			return
		}
		var items []map[string]any
		_ = json.Unmarshal([]byte(asStr(row["items_json"])), &items)
		writeOK(w, map[string]any{"code": row["code"], "name": row["name"], "items": items})
	})
}
