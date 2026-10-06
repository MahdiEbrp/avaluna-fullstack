package api

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/arena/avaluna/internal/core"
	"github.com/arena/avaluna/internal/httpx"
)

const Version = "1.0.0"

func registerHealthModule(rt *Router, c *core.Core) {
	rt.Module("23-health")

	rt.Handle(http.MethodGet, "/health", "", func(w http.ResponseWriter, r *http.Request) {
		writeOK(w, map[string]any{"status": "ok", "version": Version,
			"uptimeSeconds": int(c.Uptime().Seconds()), "time": time.Now().UTC().Format(time.RFC3339)})
	})

	rt.Handle(http.MethodGet, "/health/ready", "", func(w http.ResponseWriter, r *http.Request) {
		checks := map[string]any{}
		ready := true
		if err := c.DB.Healthy(r.Context()); err != nil {
			checks["database"] = "down: " + err.Error()
			ready = false
		} else {
			checks["database"] = "up"
		}
		checks["cache"] = "up"
		checks["scheduler"] = boolLabel(c.Cron != nil)
		checks["queue"] = boolLabel(c.Queue != nil)
		if !ready {
			httpx.FailStatus(w, http.StatusServiceUnavailable, "NOT_READY", "Service not ready")
			return
		}
		writeOK(w, map[string]any{"ready": true, "checks": checks})
	})

	rt.Handle(http.MethodGet, "/health/db", roleAdmin, func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		err := c.DB.Healthy(r.Context())
		stats, _ := c.DB.Stats(r.Context())
		size := int64(0)
		if fi, serr := os.Stat(c.DB.Path()); serr == nil {
			size = fi.Size()
		}
		writeOK(w, map[string]any{
			"driver": "modernc.org/sqlite (pure Go)", "path": c.DB.Path(),
			"healthy": err == nil, "latencyMs": time.Since(start).Milliseconds(),
			"fileSizeBytes": size, "tables": stats,
		})
	})

	rt.Handle(http.MethodGet, "/health/cache", roleAdmin, func(w http.ResponseWriter, r *http.Request) {
		writeOK(w, map[string]any{"driver": "in-process LRU-less TTL cache", "entries": c.CacheSize()})
	})

	rt.Handle(http.MethodGet, "/health/queue", roleAdmin, func(w http.ResponseWriter, r *http.Request) {
		if c.Queue == nil {
			writeOK(w, map[string]any{"enabled": false})
			return
		}
		stats, err := c.Queue.Stats(r.Context())
		if err != nil {
			httpx.Fail(w, httpx.CodeInternal, err.Error())
			return
		}
		writeOK(w, map[string]any{"enabled": true, "queues": stats})
	})

	rt.Handle(http.MethodGet, "/admin/system/info", roleSuper, func(w http.ResponseWriter, r *http.Request) {
		var mem runtime.MemStats
		runtime.ReadMemStats(&mem)
		writeOK(w, map[string]any{
			"app":           c.Cfg.AppName,
			"version":       Version,
			"environment":   c.Cfg.AppEnv,
			"goVersion":     runtime.Version(),
			"os":            runtime.GOOS + "/" + runtime.GOARCH,
			"cpus":          runtime.NumCPU(),
			"goroutines":    runtime.NumGoroutine(),
			"heapAllocMB":   mem.HeapAlloc / 1024 / 1024,
			"sysMB":         mem.Sys / 1024 / 1024,
			"gcCycles":      mem.NumGC,
			"uptimeSeconds": int(c.Uptime().Seconds()),
			"startedAt":     c.StartedAt.Format(time.RFC3339),
			"database":      c.DB.Path(),
			"cacheEntries":  c.CacheSize(),
			"maintenance":   c.MaintenanceMode(),
		})
	})

	rt.Handle(http.MethodGet, "/admin/system/metrics", roleSuper, func(w http.ResponseWriter, r *http.Request) {
		var mem runtime.MemStats
		runtime.ReadMemStats(&mem)
		counts, _ := c.DB.Stats(r.Context())
		keys := make([]string, 0, len(counts))
		for k := range counts {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		var b strings.Builder
		b.WriteString("# HELP ecms_uptime_seconds Process uptime in seconds\n# TYPE ecms_uptime_seconds gauge\n")
		fmt.Fprintf(&b, "ecms_uptime_seconds %d\n", int(c.Uptime().Seconds()))
		b.WriteString("# HELP ecms_goroutines Current goroutine count\n# TYPE ecms_goroutines gauge\n")
		fmt.Fprintf(&b, "ecms_goroutines %d\n", runtime.NumGoroutine())
		b.WriteString("# HELP ecms_heap_bytes Heap allocation in bytes\n# TYPE ecms_heap_bytes gauge\n")
		fmt.Fprintf(&b, "ecms_heap_bytes %d\n", mem.HeapAlloc)
		b.WriteString("# HELP ecms_table_rows Row count per table\n# TYPE ecms_table_rows gauge\n")
		for _, k := range keys {
			fmt.Fprintf(&b, "ecms_table_rows{table=%q} %d\n", k, counts[k])
		}
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		_, _ = w.Write([]byte(b.String()))
	})
}

func boolLabel(b bool) string {
	if b {
		return "up"
	}
	return "disabled"
}

func registerSystemModule(rt *Router, c *core.Core) {
	rt.Module("24-maintenance")

	rt.Handle(http.MethodPost, "/admin/system/backup", roleSuper, func(w http.ResponseWriter, r *http.Request) {
		if _, busy := c.CacheGet("backup:running"); busy {
			httpx.Fail(w, httpx.CodeBackupInProgress, "")
			return
		}
		out, err := CreateBackup(r.Context(), c, "manual")
		if err != nil {
			httpx.Fail(w, httpx.CodeInternal, err.Error())
			return
		}
		audit(c, r, "system.backup", "backups", "", map[string]any{"result": out})
		httpx.Success(w, http.StatusCreated, "Backup created", map[string]any{"result": out}, nil)
	})

	rt.Handle(http.MethodGet, "/admin/system/backups", roleSuper, func(w http.ResponseWriter, r *http.Request) {
		rows, _ := c.DB.Maps(r.Context(), `SELECT * FROM backups ORDER BY id DESC LIMIT 100`)
		writeOK(w, rows)
	})

	rt.Handle(http.MethodPost, "/admin/system/backup/{id}/restore", roleSuper, func(w http.ResponseWriter, r *http.Request) {
		id, _ := httpx.PathInt(r, "id")
		row, err := c.DB.MapOne(r.Context(), `SELECT * FROM backups WHERE id=?`, id)
		if err != nil {
			httpx.Fail(w, httpx.CodeNotFound, "")
			return
		}
		path := asStr(row["path"])
		if _, err := os.Stat(path); err != nil {
			httpx.Fail(w, httpx.CodeNotFound, "Backup file is missing from disk")
			return
		}

		staged := c.DB.Path() + ".restore"
		data, err := os.ReadFile(path)
		if err != nil {
			httpx.Fail(w, httpx.CodeInternal, err.Error())
			return
		}
		if err := os.WriteFile(staged, data, 0o600); err != nil {
			httpx.Fail(w, httpx.CodeInternal, err.Error())
			return
		}
		audit(c, r, "system.restore_staged", "backups", toString(id), map[string]any{"staged": staged})
		httpx.Success(w, http.StatusOK, "Backup staged for restore", map[string]any{
			"stagedPath": staged,
			"next":       "Enable maintenance mode, stop the service, replace the database file with the staged snapshot, then restart.",
		}, nil)
	})

	rt.Handle(http.MethodDelete, "/admin/system/backup/{id}", roleSuper, func(w http.ResponseWriter, r *http.Request) {
		id, _ := httpx.PathInt(r, "id")
		row, err := c.DB.MapOne(r.Context(), `SELECT path FROM backups WHERE id=?`, id)
		if err != nil {
			httpx.Fail(w, httpx.CodeNotFound, "")
			return
		}
		_ = os.Remove(asStr(row["path"]))
		_, _ = c.DB.Write(r.Context(), `DELETE FROM backups WHERE id=?`, id)
		audit(c, r, "system.backup_delete", "backups", toString(id), nil)
		httpx.NoBody(w, "Backup deleted")
	})

	rt.Handle(http.MethodPost, "/admin/system/cache/clear", roleAdmin, func(w http.ResponseWriter, r *http.Request) {
		n := c.CacheClear("")
		_ = c.ReloadSettings(r.Context())
		audit(c, r, "system.cache_clear", "cache", "", map[string]any{"cleared": n})
		httpx.Success(w, http.StatusOK, "Cache cleared", map[string]any{"cleared": n}, nil)
	})

	rt.Handle(http.MethodPost, "/admin/system/cache/clear/{key}", roleAdmin, func(w http.ResponseWriter, r *http.Request) {
		n := c.CacheClear(r.PathValue("key"))
		httpx.Success(w, http.StatusOK, "Cache segment cleared", map[string]any{"cleared": n}, nil)
	})

	rt.Handle(http.MethodGet, "/admin/system/maintenance-mode", roleAdmin, func(w http.ResponseWriter, r *http.Request) {
		writeOK(w, map[string]any{
			"enabled": c.MaintenanceMode(),
			"message": c.Setting("maintenance_message", "We will be back shortly."),
		})
	})

	rt.Handle(http.MethodPatch, "/admin/system/maintenance-mode", roleSuper, func(w http.ResponseWriter, r *http.Request) {
		m, _ := body(r)
		enabled := bVal(m, "enabled", !c.MaintenanceMode())
		if err := c.SetSetting(r.Context(), "maintenance_mode", toString(enabled), "private"); err != nil {
			httpx.Fail(w, httpx.CodeInternal, err.Error())
			return
		}
		if msg := sVal(m, "message"); msg != "" {
			_ = c.SetSetting(r.Context(), "maintenance_message", msg, "private")
		}
		audit(c, r, "system.maintenance", "settings", "maintenance_mode", map[string]any{"enabled": enabled})
		httpx.Success(w, http.StatusOK, "Maintenance mode updated", map[string]any{"enabled": enabled}, nil)
	})

	rt.Handle(http.MethodPost, "/admin/system/optimize", roleSuper, func(w http.ResponseWriter, r *http.Request) {
		if _, err := c.DB.Write(r.Context(), "PRAGMA optimize"); err != nil {
			httpx.Fail(w, httpx.CodeInternal, err.Error())
			return
		}
		_, _ = c.DB.Write(r.Context(), "PRAGMA wal_checkpoint(TRUNCATE)")
		audit(c, r, "system.optimize", "database", "", nil)
		httpx.NoBody(w, "Database optimised")
	})
}

func CreateBackup(ctx context.Context, c *core.Core, trigger string) (string, error) {
	c.CacheSet("backup:running", true, 10*time.Minute)
	defer c.CacheClear("backup:running")

	name := fmt.Sprintf("backup-%s.db", time.Now().UTC().Format("20060102-150405"))
	dest := filepath.Join(c.Cfg.BackupDir, name)
	if err := c.DB.Backup(ctx, dest); err != nil {
		return "", err
	}
	size := int64(0)
	if fi, err := os.Stat(dest); err == nil {
		size = fi.Size()
	}
	if _, err := c.DB.Write(ctx,
		`INSERT INTO backups(filename,path,size_bytes,checksum,encrypted,"trigger") VALUES(?,?,?,?,?,?)`,
		name, dest, size, "sha256:on-write", boolInt(c.SettingBool("backup_encrypt", true)), trigger); err != nil {
		return "", err
	}

	keep := c.SettingInt("backup_keep", 7)
	old, _ := c.DB.Maps(ctx, `SELECT id,path FROM backups ORDER BY id DESC LIMIT -1 OFFSET ?`, keep)
	for _, row := range old {
		_ = os.Remove(asStr(row["path"]))
		_, _ = c.DB.Write(ctx, `DELETE FROM backups WHERE id=?`, row["id"])
	}
	return fmt.Sprintf("%s (%d bytes), rotated %d old snapshots", name, size, len(old)), nil
}

func RenderInvoice(ctx context.Context, c *core.Core, orderID int) (string, error) {
	order, err := c.DB.MapOne(ctx, `SELECT * FROM orders WHERE id=?`, orderID)
	if err != nil {
		return "", err
	}
	items, err := c.DB.Maps(ctx, `SELECT name,sku,quantity,unit_price,total FROM order_items WHERE order_id=?`, orderID)
	if err != nil {
		return "", err
	}
	return renderInvoiceHTML(c, order, items), nil
}
