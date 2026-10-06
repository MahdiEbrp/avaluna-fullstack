package api

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"

	"github.com/arena/avaluna/internal/core"
	"github.com/arena/avaluna/internal/database"
	"github.com/arena/avaluna/internal/httpx"
	"github.com/arena/avaluna/internal/middleware"
)

func registerInventoryModule(rt *Router, c *core.Core) {
	rt.Module("04-inventory")

	warehouses := &Resource{
		Name: "inventory/warehouses", Table: "warehouses",
		Fields: []Field{
			{Name: "code", Kind: KindString, Required: true},
			{Name: "name", Kind: KindString, Required: true},
			{Name: "city", Kind: KindString},
			{Name: "address", Kind: KindString},
			{Name: "active", Kind: KindBool, Default: 1},
		},
		OrderBy: "id ASC", ReadRole: roleAdmin, WriteRole: roleAdmin,
	}
	warehouses.Mount(rt, c.DB)

	rt.Handle(http.MethodGet, "/inventory/low-stock", roleAdmin, func(w http.ResponseWriter, r *http.Request) {
		rows, err := c.DB.Maps(r.Context(), `
			SELECT i.variant_id, v.sku, p.name AS product, w.name AS warehouse,
			       i.quantity, i.reserved, i.reorder_level, (i.quantity - i.reserved) AS available
			FROM inventory i
			JOIN product_variants v ON v.id = i.variant_id
			JOIN products p ON p.id = v.product_id
			JOIN warehouses w ON w.id = i.warehouse_id
			WHERE (i.quantity - i.reserved) <= i.reorder_level
			ORDER BY available ASC LIMIT 200`)
		if err != nil {
			httpx.Fail(w, httpx.CodeInternal, err.Error())
			return
		}
		writeOK(w, rows)
	})

	rt.Handle(http.MethodGet, "/inventory/{variantId}", roleVendor, func(w http.ResponseWriter, r *http.Request) {
		vid, ok := httpx.PathInt(r, "variantId")
		if !ok {
			httpx.Fail(w, httpx.CodeValidation, "Invalid variant id")
			return
		}
		rows, err := c.DB.Maps(r.Context(), `
			SELECT i.*, w.name AS warehouse_name, w.code AS warehouse_code
			FROM inventory i JOIN warehouses w ON w.id = i.warehouse_id
			WHERE i.variant_id = ?`, vid)
		if err != nil {
			httpx.Fail(w, httpx.CodeInternal, err.Error())
			return
		}
		total := 0
		for _, row := range rows {
			total += asInt(row["quantity"]) - asInt(row["reserved"])
		}
		writeOK(w, map[string]any{"variantId": vid, "available": total, "locations": rows})
	})

	rt.Handle(http.MethodPatch, "/inventory/{variantId}", roleVendor, func(w http.ResponseWriter, r *http.Request) {
		vid, ok := httpx.PathInt(r, "variantId")
		if !ok {
			httpx.Fail(w, httpx.CodeValidation, "Invalid variant id")
			return
		}
		m, err := body(r)
		if err != nil {
			httpx.Fail(w, httpx.CodeValidation, err.Error())
			return
		}
		wid := iVal(m, "warehouseId", iVal(m, "warehouse_id", 1))
		qty := iVal(m, "quantity", -1)
		delta := iVal(m, "delta", 0)
		reorder := iVal(m, "reorderLevel", -1)

		if qty < 0 && delta == 0 && reorder < 0 {
			httpx.Fail(w, httpx.CodeValidation, "Provide quantity, delta or reorderLevel")
			return
		}
		if qty >= 0 {
			if err := setStock(r.Context(), c, vid, wid, qty, "manual_set", middleware.UserID(r.Context())); err != nil {
				httpx.Fail(w, httpx.CodeInternal, err.Error())
				return
			}
		}
		if delta != 0 {
			if err := adjustStock(r.Context(), c, vid, wid, delta, "manual_adjust", "", middleware.UserID(r.Context())); err != nil {
				httpx.Fail(w, httpx.CodeInternal, err.Error())
				return
			}
		}
		if reorder >= 0 {
			_, _ = c.DB.Write(r.Context(), `UPDATE inventory SET reorder_level=? WHERE variant_id=? AND warehouse_id=?`, reorder, vid, wid)
		}
		audit(c, r, "inventory.update", "inventory", toString(vid), m)
		rows, _ := c.DB.Maps(r.Context(), `SELECT * FROM inventory WHERE variant_id=?`, vid)
		httpx.Success(w, http.StatusOK, "Inventory updated", rows, nil)
	})

	rt.Handle(http.MethodGet, "/inventory/history/{variantId}", roleAdmin, func(w http.ResponseWriter, r *http.Request) {
		vid, _ := httpx.PathInt(r, "variantId")
		page, limit, offset := paginate(r)
		total, _ := c.DB.Count(r.Context(), `SELECT COUNT(*) FROM inventory_history WHERE variant_id=?`, vid)
		rows, err := c.DB.Maps(r.Context(),
			`SELECT * FROM inventory_history WHERE variant_id=? ORDER BY id DESC LIMIT ? OFFSET ?`, vid, limit, offset)
		if err != nil {
			httpx.Fail(w, httpx.CodeInternal, err.Error())
			return
		}
		httpx.List(w, rows, httpx.MakePagination(page, limit, total))
	})

	rt.Handle(http.MethodPost, "/inventory/transfer", roleAdmin, func(w http.ResponseWriter, r *http.Request) {
		m, err := body(r)
		if err != nil {
			httpx.Fail(w, httpx.CodeValidation, err.Error())
			return
		}
		vid := iVal(m, "variantId", 0)
		from := iVal(m, "fromWarehouseId", 0)
		to := iVal(m, "toWarehouseId", 0)
		qty := iVal(m, "quantity", 0)
		if vid == 0 || from == 0 || to == 0 || qty <= 0 {
			httpx.Fail(w, httpx.CodeValidation, "variantId, fromWarehouseId, toWarehouseId and quantity are required")
			return
		}
		avail, _ := availableAt(r.Context(), c, vid, from)
		if avail < qty {
			httpx.Fail(w, httpx.CodeOutOfStock, "Source warehouse does not hold enough stock",
				httpx.FieldIssue{Field: "quantity", Issue: "only " + toString(avail) + " available"})
			return
		}
		uid := middleware.UserID(r.Context())
		if err := adjustStock(r.Context(), c, vid, from, -qty, "transfer_out", toString(to), uid); err != nil {
			httpx.Fail(w, httpx.CodeInternal, err.Error())
			return
		}
		if err := adjustStock(r.Context(), c, vid, to, qty, "transfer_in", toString(from), uid); err != nil {
			httpx.Fail(w, httpx.CodeInternal, err.Error())
			return
		}
		audit(c, r, "inventory.transfer", "inventory", toString(vid), m)
		httpx.NoBody(w, "Stock transferred")
	})
}

func availableAt(ctx context.Context, c *core.Core, variantID, warehouseID int) (int, error) {
	var q, res int
	err := c.DB.Row(ctx, `SELECT quantity,reserved FROM inventory WHERE variant_id=? AND warehouse_id=?`,
		[]any{variantID, warehouseID}, &q, &res)
	if err == database.ErrNotFound {
		return 0, nil
	}
	return q - res, err
}

func AvailableStock(ctx context.Context, c *core.Core, variantID int) int {
	n, _ := c.DB.Count(ctx,
		`SELECT COALESCE(SUM(quantity - reserved),0) FROM inventory WHERE variant_id=?`, variantID)
	return n
}

func ProductStock(ctx context.Context, c *core.Core, productID int) int {
	n, _ := c.DB.Count(ctx, `SELECT COALESCE(SUM(i.quantity - i.reserved),0) FROM inventory i
		JOIN product_variants v ON v.id=i.variant_id WHERE v.product_id=?`, productID)
	return n
}

func setStock(ctx context.Context, c *core.Core, variantID, warehouseID, qty int, reason string, actor int) error {
	before, _ := availableAt(ctx, c, variantID, warehouseID)
	_, err := c.DB.Write(ctx, `INSERT INTO inventory(variant_id,warehouse_id,quantity,updated_at)
		VALUES(?,?,?,datetime('now'))
		ON CONFLICT(variant_id,warehouse_id) DO UPDATE SET quantity=excluded.quantity, updated_at=datetime('now')`,
		variantID, warehouseID, qty)
	if err != nil {
		return err
	}
	_, _ = c.DB.Write(ctx, `INSERT INTO inventory_history(variant_id,warehouse_id,delta,reason,actor_id)
		VALUES(?,?,?,?,?)`, variantID, warehouseID, qty-before, reason, nullIf(actor))
	return nil
}

func adjustStock(ctx context.Context, c *core.Core, variantID, warehouseID, delta int, reason, ref string, actor int) error {
	_, err := c.DB.Write(ctx, `INSERT INTO inventory(variant_id,warehouse_id,quantity,updated_at)
		VALUES(?,?,?,datetime('now'))
		ON CONFLICT(variant_id,warehouse_id) DO UPDATE SET
		quantity = MAX(0, inventory.quantity + ?), updated_at=datetime('now')`,
		variantID, warehouseID, maxInt(delta, 0), delta)
	if err != nil {
		return err
	}
	_, err = c.DB.Write(ctx, `INSERT INTO inventory_history(variant_id,warehouse_id,delta,reason,reference,actor_id)
		VALUES(?,?,?,?,?,?)`, variantID, warehouseID, delta, reason, ref, nullIf(actor))
	return err
}

func ReserveStock(ctx context.Context, c *core.Core, variantID, qty int, ref string) error {
	rows, err := c.DB.Maps(ctx,
		`SELECT id,warehouse_id,quantity,reserved FROM inventory WHERE variant_id=? ORDER BY (quantity-reserved) DESC`, variantID)
	if err != nil {
		return err
	}
	need := qty
	for _, row := range rows {
		free := asInt(row["quantity"]) - asInt(row["reserved"])
		if free <= 0 {
			continue
		}
		take := minInt(free, need)

		res, err := c.DB.Write(ctx,
			`UPDATE inventory SET reserved = reserved + ?, updated_at=datetime('now')
			 WHERE id = ? AND quantity - reserved >= ?`, take, row["id"], take)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {

			continue
		}
		_, _ = c.DB.Write(ctx, `INSERT INTO inventory_history(variant_id,warehouse_id,delta,reason,reference)
			VALUES(?,?,?,'reserve',?)`, variantID, row["warehouse_id"], -take, ref)
		need -= take
		if need == 0 {
			break
		}
	}
	if need > 0 {
		return fmt.Errorf("%w: variant %d short by %d of %d", ErrInsufficientStock, variantID, need, qty)
	}
	return nil
}

func ReleaseStock(ctx context.Context, c *core.Core, variantID, qty int, commit bool, ref string) error {
	rows, err := c.DB.Maps(ctx, `SELECT id,warehouse_id,reserved FROM inventory WHERE variant_id=? AND reserved > 0`, variantID)
	if err != nil {
		return err
	}
	need := qty
	for _, row := range rows {
		take := minInt(asInt(row["reserved"]), need)
		if take <= 0 {
			continue
		}

		var res sql.Result
		var err error
		if commit {
			res, err = c.DB.Write(ctx,
				`UPDATE inventory SET reserved = reserved - ?, quantity = quantity - ?, updated_at=datetime('now')
				 WHERE id = ? AND reserved >= ? AND quantity >= ?`, take, take, row["id"], take, take)
		} else {
			res, err = c.DB.Write(ctx,
				`UPDATE inventory SET reserved = reserved - ?, updated_at=datetime('now')
				 WHERE id = ? AND reserved >= ?`, take, row["id"], take)
		}
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			continue
		}
		reason := "release"
		if commit {
			reason = "fulfil"
		}
		_, _ = c.DB.Write(ctx, `INSERT INTO inventory_history(variant_id,warehouse_id,delta,reason,reference)
			VALUES(?,?,?,?,?)`, variantID, row["warehouse_id"], take, reason, ref)
		need -= take
		if need == 0 {
			break
		}
	}
	if need > 0 {
		return fmt.Errorf("%w: variant %d had %d of %d units reserved", ErrReservationMismatch, variantID, qty-need, qty)
	}
	return nil
}

var (
	ErrInsufficientStock = errors.New("insufficient stock")

	ErrReservationMismatch = errors.New("reservation mismatch")
)

func nullIf(v int) any {
	if v <= 0 {
		return nil
	}
	return v
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
