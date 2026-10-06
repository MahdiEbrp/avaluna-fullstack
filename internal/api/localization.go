package api

import (
	"net/http"
	"strings"

	"github.com/arena/avaluna/internal/core"
	"github.com/arena/avaluna/internal/httpx"
	"github.com/arena/avaluna/internal/i18n"
)

func registerLocaleModule(rt *Router, c *core.Core) {
	rt.Module("26-localization")

	rt.Handle(http.MethodGet, "/localization/currencies", "", func(w http.ResponseWriter, r *http.Request) {
		rows, err := c.DB.Maps(r.Context(),
			`SELECT code,name,symbol,rate,decimals,is_base FROM currencies WHERE active=1 ORDER BY is_base DESC, code`)
		if err != nil {
			httpx.Fail(w, httpx.CodeInternal, err.Error())
			return
		}
		writeOK(w, rows)
	})

	rt.Handle(http.MethodGet, "/localization/exchange-rates", "", func(w http.ResponseWriter, r *http.Request) {
		rows, _ := c.DB.Maps(r.Context(), `SELECT code,rate,updated_at FROM currencies WHERE active=1`)
		base := "IRR"
		for _, row := range rows {
			if asInt(row["is_base"]) == 1 {
				base = asStr(row["code"])
			}
		}
		writeOK(w, map[string]any{"base": base, "rates": rows})
	})

	rt.Handle(http.MethodPost, "/localization/convert", "", func(w http.ResponseWriter, r *http.Request) {
		m, _ := body(r)
		amount := iVal(m, "amount", 0)
		from := strings.ToUpper(sVal(m, "from"))
		to := strings.ToUpper(sVal(m, "to"))
		fr, err1 := currencyRate(c, r, from)
		tr, err2 := currencyRate(c, r, to)
		if err1 != nil || err2 != nil {
			httpx.Fail(w, httpx.CodeValidation, "Unknown currency code")
			return
		}
		converted := int(float64(amount) / fr * tr)
		writeOK(w, map[string]any{"amount": amount, "from": from, "to": to, "converted": converted})
	})

	rt.Handle(http.MethodPatch, "/admin/localization/currencies/{code}", roleAdmin, func(w http.ResponseWriter, r *http.Request) {
		code := strings.ToUpper(r.PathValue("code"))
		m, _ := body(r)
		sets, args := []string{}, []any{}
		if _, ok := m["active"]; ok {
			sets, args = append(sets, "active=?"), append(args, boolInt(bVal(m, "active", true)))
		}
		if _, ok := m["rate"]; ok {
			sets, args = append(sets, "rate=?"), append(args, asFloat(m["rate"]))
		}
		if _, ok := m["symbol"]; ok {
			sets, args = append(sets, "symbol=?"), append(args, sVal(m, "symbol"))
		}
		if len(sets) == 0 {
			httpx.Fail(w, httpx.CodeValidation, "No updatable fields supplied")
			return
		}
		args = append(args, code)
		if _, err := c.DB.Write(r.Context(),
			"UPDATE currencies SET "+strings.Join(sets, ",")+", updated_at=datetime('now') WHERE code=?", args...); err != nil {
			httpx.Fail(w, httpx.CodeInternal, err.Error())
			return
		}
		audit(c, r, "localization.currency_update", "currencies", code, m)
		row, _ := c.DB.MapOne(r.Context(), `SELECT * FROM currencies WHERE code=?`, code)
		httpx.Success(w, http.StatusOK, "Currency updated", row, nil)
	})

	rt.Handle(http.MethodGet, "/localization/languages", "", func(w http.ResponseWriter, r *http.Request) {
		out := make([]map[string]any, 0, len(i18n.Order))
		for _, code := range i18n.Order {
			l := i18n.Locales[code]
			out = append(out, map[string]any{
				"code": l.Code, "name": l.Name, "tag": l.NativeTag,
				"dir": l.Dir, "currency": l.Currency, "default": code == c.Cfg.DefaultLocale,
			})
		}
		writeOK(w, out)
	})

	rt.Handle(http.MethodGet, "/localization/translations/{locale}", "", func(w http.ResponseWriter, r *http.Request) {
		loc := r.PathValue("locale")
		if _, ok := i18n.Locales[loc]; !ok {
			httpx.Fail(w, httpx.CodeNotFound, "Unsupported locale")
			return
		}
		out := i18n.Keys(loc)

		rows, _ := c.DB.Maps(r.Context(), `SELECT key,value FROM translations WHERE locale=?`, loc)
		for _, row := range rows {
			out[asStr(row["key"])] = asStr(row["value"])
		}
		writeOK(w, map[string]any{"locale": loc, "dir": i18n.Dir(loc), "count": len(out), "messages": out})
	})

	rt.Handle(http.MethodPut, "/admin/localization/translations/{locale}", roleAdmin, func(w http.ResponseWriter, r *http.Request) {
		loc := r.PathValue("locale")
		if _, ok := i18n.Locales[loc]; !ok {
			httpx.Fail(w, httpx.CodeNotFound, "Unsupported locale")
			return
		}
		m, err := body(r)
		if err != nil {
			httpx.Fail(w, httpx.CodeValidation, err.Error())
			return
		}
		n := 0
		for k, v := range m {
			if _, err := c.DB.Write(r.Context(),
				`INSERT INTO translations(locale,key,value) VALUES(?,?,?)
				 ON CONFLICT(locale,key) DO UPDATE SET value=excluded.value`,
				loc, k, toString(v)); err == nil {
				n++
			}
		}
		audit(c, r, "localization.translations_update", "translations", loc, map[string]any{"keys": n})
		httpx.Success(w, http.StatusOK, "Translations saved", map[string]any{"updated": n}, nil)
	})
}

func currencyRate(c *core.Core, r *http.Request, code string) (float64, error) {
	var rate float64
	err := c.DB.Row(r.Context(), `SELECT rate FROM currencies WHERE code=? AND active=1`, []any{code}, &rate)
	if err != nil {
		return 0, err
	}
	if rate == 0 {
		rate = 1
	}
	return rate, nil
}
