package api

import (
	"context"
	"net/http"

	"github.com/arena/avaluna/internal/core"
)

func registerTaxModule(rt *Router, c *core.Core) {
	rt.Module("10-tax")

	rules := &Resource{
		Name: "tax/rules", Table: "tax_rules",
		Fields: []Field{
			{Name: "name", Kind: KindString, Required: true},
			{Name: "country", Kind: KindString, Default: "IR"},
			{Name: "province", Kind: KindString},
			{Name: "tax_class", Kind: KindString, Default: "standard"},
			{Name: "rate", Kind: KindFloat, Required: true},
			{Name: "compound", Kind: KindBool},
			{Name: "priority", Kind: KindInt},
			{Name: "active", Kind: KindBool, Default: 1},
		},
		Filters: []string{"country", "tax_class", "active"},
		OrderBy: "priority ASC, id ASC", ReadRole: roleAdmin, WriteRole: roleAdmin,
	}
	rules.Mount(rt, c.DB)

	rt.Handle(http.MethodPost, "/tax/calculate", "", func(w http.ResponseWriter, r *http.Request) {
		m, _ := body(r)
		amount := iVal(m, "amount", 0)
		country := sVal(m, "country")
		if country == "" {
			country = "IR"
		}
		province := sVal(m, "province")
		class := sVal(m, "taxClass")
		if class == "" {
			class = "standard"
		}
		rows, _ := c.DB.Maps(r.Context(), `SELECT name,rate,compound FROM tax_rules
			WHERE active=1 AND country=? AND (province='' OR province=?) AND tax_class=?
			ORDER BY priority ASC`, country, province, class)
		tax := CalculateTaxClass(r.Context(), c, amount, country, province, class)
		writeOK(w, map[string]any{
			"amount": amount, "tax": tax, "total": amount + tax,
			"country": country, "province": province, "taxClass": class, "rulesApplied": rows,
		})
	})
}

func CalculateTax(ctx context.Context, c *core.Core, amount int, country, province string) int {
	return CalculateTaxClass(ctx, c, amount, country, province, "standard")
}

func CalculateTaxClass(ctx context.Context, c *core.Core, amount int, country, province, class string) int {
	if amount <= 0 {
		return 0
	}
	if !c.SettingBool("tax_enabled", true) {
		return 0
	}
	rows, err := c.DB.Maps(ctx, `SELECT rate,compound FROM tax_rules
		WHERE active=1 AND country=? AND (province='' OR province=?) AND tax_class=?
		ORDER BY priority ASC`, country, province, class)
	if err != nil || len(rows) == 0 {
		rate := float64(c.SettingInt("default_tax_percent", 9))
		return int(float64(amount) * rate / 100)
	}
	base := float64(amount)
	total := 0.0
	for _, row := range rows {
		rate := asFloat(row["rate"])
		if asInt(row["compound"]) == 1 {
			total += (base + total) * rate / 100
		} else {
			total += base * rate / 100
		}
	}
	return int(total)
}
