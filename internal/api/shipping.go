package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/arena/avaluna/internal/core"
	"github.com/arena/avaluna/internal/httpx"
)

func registerShippingModule(rt *Router, c *core.Core) {
	rt.Module("08-shipping")

	zones := &Resource{
		Name: "shipping/zones", Table: "shipping_zones",
		Fields: []Field{
			{Name: "name", Kind: KindString, Required: true},
			{Name: "name_en", Kind: KindString},
			{Name: "countries", Kind: KindString, Default: "IR"},
			{Name: "provinces", Kind: KindString},
			{Name: "active", Kind: KindBool, Default: 1},
		},
		OrderBy: "id ASC", ReadRole: roleAdmin, WriteRole: roleAdmin,
	}
	zones.Mount(rt, c.DB)

	methods := &Resource{
		Name: "shipping/methods", Table: "shipping_methods",
		Fields: []Field{
			{Name: "zone_id", Kind: KindInt},
			{Name: "code", Kind: KindString, Required: true},
			{Name: "name", Kind: KindString, Required: true},
			{Name: "name_en", Kind: KindString},
			{Name: "carrier", Kind: KindString, Default: "post"},
			{Name: "base_cost", Kind: KindInt},
			{Name: "per_kg_cost", Kind: KindInt},
			{Name: "free_over", Kind: KindInt},
			{Name: "eta_days", Kind: KindInt, Default: 3},
			{Name: "active", Kind: KindBool, Default: 1},
			{Name: "sort_order", Kind: KindInt},
		},
		Filters: []string{"zone_id", "active", "carrier"},
		OrderBy: "sort_order ASC, id ASC", WriteRole: roleAdmin,
	}
	methods.Mount(rt, c.DB)

	rt.Handle(http.MethodPost, "/shipping/calculate", "", func(w http.ResponseWriter, r *http.Request) {
		m, _ := body(r)
		subtotal := iVal(m, "subtotal", 0)
		weight := iVal(m, "weightG", 0)
		country := sVal(m, "country")
		if country == "" {
			country = "IR"
		}
		province := sVal(m, "province")

		rows, err := c.DB.Maps(r.Context(), `
			SELECT sm.*, sz.countries, sz.provinces FROM shipping_methods sm
			LEFT JOIN shipping_zones sz ON sz.id = sm.zone_id
			WHERE sm.active = 1 ORDER BY sm.sort_order`)
		if err != nil {
			httpx.Fail(w, httpx.CodeInternal, err.Error())
			return
		}
		out := []map[string]any{}
		for _, row := range rows {
			zc := asStr(row["countries"])
			if zc != "" && !strings.Contains(strings.ToUpper(zc), strings.ToUpper(country)) {
				continue
			}
			zp := asStr(row["provinces"])
			if zp != "" && province != "" && !strings.Contains(zp, province) {
				continue
			}
			cost := computeShipping(asInt(row["base_cost"]), asInt(row["per_kg_cost"]),
				asInt(row["free_over"]), subtotal, weight)
			out = append(out, map[string]any{
				"id": row["id"], "code": row["code"], "name": row["name"], "nameEn": row["name_en"],
				"carrier": row["carrier"], "etaDays": row["eta_days"], "cost": cost,
				"free": cost == 0,
			})
		}
		if len(out) == 0 {
			httpx.Fail(w, httpx.CodeShippingUnavail, "No shipping method covers this destination")
			return
		}
		writeOK(w, out)
	})

	rt.Handle(http.MethodGet, "/shipping/track/{trackingCode}", "", func(w http.ResponseWriter, r *http.Request) {
		code := r.PathValue("trackingCode")
		row, err := c.DB.MapOne(r.Context(), `SELECT * FROM shipments WHERE tracking_code=?`, code)
		if err != nil {
			httpx.Fail(w, httpx.CodeNotFound, "Tracking code not found")
			return
		}
		var events []map[string]any
		_ = json.Unmarshal([]byte(asStr(row["events_json"])), &events)
		writeOK(w, map[string]any{
			"trackingCode": code, "carrier": row["carrier"], "status": row["status"],
			"shippedAt": row["shipped_at"], "deliveredAt": row["delivered_at"], "events": events,
		})
	})

	rt.Handle(http.MethodPost, "/shipping/webhook/{carrier}", "", func(w http.ResponseWriter, r *http.Request) {
		carrier := r.PathValue("carrier")
		m, err := body(r)
		if err != nil {
			httpx.Fail(w, httpx.CodeValidation, err.Error())
			return
		}
		code := sVal(m, "trackingCode")
		status := sVal(m, "status")
		if code == "" || status == "" {
			httpx.Fail(w, httpx.CodeValidation, "trackingCode and status are required")
			return
		}
		if err := UpdateShipment(r.Context(), c, code, status, carrier, sVal(m, "note")); err != nil {
			httpx.Fail(w, httpx.CodeNotFound, err.Error())
			return
		}
		httpx.NoBody(w, "Shipment updated")
	})

	rt.Handle(http.MethodPost, "/shipping/shipments", roleAdmin, func(w http.ResponseWriter, r *http.Request) {
		m, _ := body(r)
		orderID := iVal(m, "orderId", 0)
		if orderID == 0 {
			httpx.Fail(w, httpx.CodeValidation, "orderId is required")
			return
		}
		code, err := CreateShipment(r.Context(), c, orderID, sVal(m, "carrier"))
		if err != nil {
			httpx.Fail(w, httpx.CodeInternal, err.Error())
			return
		}
		audit(c, r, "shipment.create", "orders", toString(orderID), map[string]any{"tracking": code})
		writeCreated(w, map[string]any{"trackingCode": code, "orderId": orderID})
	})
}

func computeShipping(base, perKg, freeOver, subtotal, weightG int) int {
	if freeOver > 0 && subtotal >= freeOver {
		return 0
	}
	kg := (weightG + 999) / 1000
	if kg < 1 {
		kg = 1
	}
	return base + perKg*(kg-1)
}

func ShippingCost(ctx context.Context, c *core.Core, methodID, subtotal, weightG int) int {
	row, err := c.DB.MapOne(ctx, `SELECT base_cost,per_kg_cost,free_over FROM shipping_methods WHERE id=? AND active=1`, methodID)
	if err != nil {
		return 0
	}
	return computeShipping(asInt(row["base_cost"]), asInt(row["per_kg_cost"]), asInt(row["free_over"]), subtotal, weightG)
}

func CreateShipment(ctx context.Context, c *core.Core, orderID int, carrier string) (string, error) {
	if carrier == "" {
		carrier = "post"
	}
	code := "TRK" + strings.ToUpper(randHex(5))
	events := []map[string]any{{"status": "label_created", "at": time.Now().UTC().Format(time.RFC3339)}}
	if _, err := c.DB.Write(ctx,
		`INSERT INTO shipments(order_id,carrier,tracking_code,status,events_json,shipped_at)
		 VALUES(?,?,?,'shipped',?,datetime('now'))`, orderID, carrier, code, jsonStr(events)); err != nil {
		return "", err
	}
	_, _ = c.DB.Write(ctx,
		`UPDATE orders SET status='shipped', fulfillment_status='fulfilled', tracking_code=?,
		 shipped_at=datetime('now'), updated_at=datetime('now') WHERE id=?`, code, orderID)
	_, _ = c.DB.Write(ctx, `INSERT INTO order_events(order_id,status,note) VALUES(?,'shipped',?)`, orderID, "Tracking "+code)

	items, _ := c.DB.Maps(ctx, `SELECT COALESCE(variant_id,0) AS variant_id,quantity FROM order_items WHERE order_id=?`, orderID)
	for _, it := range items {
		if vid := asInt(it["variant_id"]); vid > 0 {
			_ = ReleaseStock(ctx, c, vid, asInt(it["quantity"]), true, code)
		}
	}
	var uid int
	_ = c.DB.Row(ctx, `SELECT COALESCE(user_id,0) FROM orders WHERE id=?`, []any{orderID}, &uid)
	c.Notify(ctx, uid, "shipping", "سفارش ارسال شد / Order shipped", "کد رهگیری: "+code, "/account/orders/"+toString(orderID))
	DispatchWebhook(ctx, c, "order.shipped", map[string]any{"orderId": orderID, "trackingCode": code})
	return code, nil
}

func UpdateShipment(ctx context.Context, c *core.Core, code, status, carrier, note string) error {
	row, err := c.DB.MapOne(ctx, `SELECT id,order_id,events_json FROM shipments WHERE tracking_code=?`, code)
	if err != nil {
		return err
	}
	var events []map[string]any
	_ = json.Unmarshal([]byte(asStr(row["events_json"])), &events)
	events = append(events, map[string]any{
		"status": status, "carrier": carrier, "note": note, "at": time.Now().UTC().Format(time.RFC3339),
	})
	delivered := ""
	if status == "delivered" {
		delivered = "datetime('now')"
	}
	q := `UPDATE shipments SET status=?, events_json=?, updated_at=datetime('now')`
	if delivered != "" {
		q += `, delivered_at=datetime('now')`
	}
	q += ` WHERE id=?`
	if _, err := c.DB.Write(ctx, q, status, jsonStr(events), row["id"]); err != nil {
		return err
	}
	if status == "delivered" {
		_, _ = c.DB.Write(ctx,
			`UPDATE orders SET status='delivered', delivered_at=datetime('now'), updated_at=datetime('now') WHERE id=?`,
			row["order_id"])
		_, _ = c.DB.Write(ctx, `INSERT INTO order_events(order_id,status,note) VALUES(?,'delivered',?)`, row["order_id"], note)
		DispatchWebhook(ctx, c, "order.delivered", map[string]any{"orderId": row["order_id"]})
	}
	return nil
}
