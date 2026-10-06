package api

import (
	"net/http"
	"strings"

	"github.com/arena/avaluna/internal/core"
	"github.com/arena/avaluna/internal/httpx"
)

func registerQueueModule(rt *Router, c *core.Core) {
	rt.Module("30-queue")

	rt.Handle(http.MethodGet, "/admin/queues", roleAdmin, func(w http.ResponseWriter, r *http.Request) {
		rows, err := c.DB.Maps(r.Context(), `SELECT queue, status, COUNT(*) AS count,
			CAST(AVG(duration_ms) AS INTEGER) AS avg_ms FROM queue_jobs GROUP BY queue, status`)
		if err != nil {
			httpx.Fail(w, httpx.CodeInternal, err.Error())
			return
		}
		agg := map[string]map[string]any{}
		for _, row := range rows {
			q := asStr(row["queue"])
			if agg[q] == nil {
				agg[q] = map[string]any{"queue": q, "waiting": 0, "active": 0,
					"completed": 0, "failed": 0, "delayed": 0, "avgMs": 0}
			}
			agg[q][asStr(row["status"])] = asInt(row["count"])
			if asStr(row["status"]) == "completed" {
				agg[q]["avgMs"] = asInt(row["avg_ms"])
			}
		}
		out := make([]map[string]any, 0, len(agg))
		for _, v := range agg {
			out = append(out, v)
		}
		writeOK(w, map[string]any{"queues": out, "workers": c.Cfg.QueueConcurrency, "enabled": c.Cfg.EnableQueue})
	})

	rt.Handle(http.MethodGet, "/admin/queues/{name}/jobs", roleAdmin, func(w http.ResponseWriter, r *http.Request) {
		page, limit, offset := paginate(r)
		where, args := "WHERE queue=?", []any{r.PathValue("name")}
		if st := r.URL.Query().Get("status"); st != "" {
			where += " AND status=?"
			args = append(args, st)
		}
		total, _ := c.DB.Count(r.Context(), "SELECT COUNT(*) FROM queue_jobs "+where, args...)
		rows, _ := c.DB.Maps(r.Context(),
			"SELECT * FROM queue_jobs "+where+" ORDER BY id DESC LIMIT ? OFFSET ?",
			append(args, limit, offset)...)
		httpx.List(w, rows, httpx.MakePagination(page, limit, total))
	})

	rt.Handle(http.MethodGet, "/admin/queue-jobs/{id}", roleAdmin, func(w http.ResponseWriter, r *http.Request) {
		id, _ := httpx.PathInt(r, "id")
		row, err := c.DB.MapOne(r.Context(), `SELECT * FROM queue_jobs WHERE id=?`, id)
		if err != nil {
			httpx.Fail(w, httpx.CodeNotFound, "")
			return
		}
		writeOK(w, row)
	})

	rt.Handle(http.MethodPost, "/admin/queue-jobs", roleSuper, func(w http.ResponseWriter, r *http.Request) {
		m, _ := body(r)
		name := sVal(m, "name")
		if name == "" {
			httpx.Fail(w, httpx.CodeValidation, "name is required")
			return
		}
		queue := sVal(m, "queue")
		if queue == "" {
			queue = "default"
		}
		attempts := iVal(m, "maxAttempts", 3)
		if c.Queue != nil {
			id, err := c.Queue.Enqueue(r.Context(), queue, name, m["payload"], attempts)
			if err != nil {
				httpx.Fail(w, httpx.CodeInternal, err.Error())
				return
			}
			writeCreated(w, map[string]any{"id": id, "queue": queue, "name": name})
			return
		}
		res, err := c.DB.Write(r.Context(),
			`INSERT INTO queue_jobs(queue,name,payload_json,max_attempts) VALUES(?,?,?,?)`,
			queue, name, jsonStr(m["payload"]), attempts)
		if err != nil {
			httpx.Fail(w, httpx.CodeInternal, err.Error())
			return
		}
		id, _ := res.LastInsertId()
		writeCreated(w, map[string]any{"id": id, "queue": queue, "name": name})
	})

	rt.Handle(http.MethodPost, "/admin/queue-jobs/{id}/retry", roleAdmin, func(w http.ResponseWriter, r *http.Request) {
		id, _ := httpx.PathInt(r, "id")
		res, err := c.DB.Write(r.Context(),
			`UPDATE queue_jobs SET status='waiting', attempts=0, error='',
			 run_at=datetime('now'), started_at=NULL, finished_at=NULL WHERE id=?`, id)
		if err != nil {
			httpx.Fail(w, httpx.CodeInternal, err.Error())
			return
		}
		if n, _ := res.RowsAffected(); n == 0 {
			httpx.Fail(w, httpx.CodeNotFound, "")
			return
		}
		audit(c, r, "queue.retry", "queue_jobs", toString(id), nil)
		httpx.NoBody(w, "Job re-queued")
	})

	rt.Handle(http.MethodDelete, "/admin/queue-jobs/{id}", roleAdmin, func(w http.ResponseWriter, r *http.Request) {
		id, _ := httpx.PathInt(r, "id")
		_, _ = c.DB.Write(r.Context(), `DELETE FROM queue_jobs WHERE id=? AND status<>'active'`, id)
		audit(c, r, "queue.delete", "queue_jobs", toString(id), nil)
		httpx.NoBody(w, "Job removed")
	})

	rt.Handle(http.MethodPost, "/admin/queues/{name}/retry-failed", roleAdmin, func(w http.ResponseWriter, r *http.Request) {
		res, err := c.DB.Write(r.Context(),
			`UPDATE queue_jobs SET status='waiting', attempts=0, error='', run_at=datetime('now')
			 WHERE queue=? AND status='failed'`, r.PathValue("name"))
		if err != nil {
			httpx.Fail(w, httpx.CodeInternal, err.Error())
			return
		}
		n, _ := res.RowsAffected()
		audit(c, r, "queue.retry_failed", "queue_jobs", r.PathValue("name"), map[string]any{"count": n})
		httpx.Success(w, http.StatusOK, "Failed jobs re-queued", map[string]any{"requeued": n}, nil)
	})

	rt.Handle(http.MethodPost, "/admin/queues/{name}/purge", roleSuper, func(w http.ResponseWriter, r *http.Request) {
		status := r.URL.Query().Get("status")
		if status == "" {
			status = "completed"
		}
		if !map[string]bool{"completed": true, "failed": true, "waiting": true, "delayed": true}[status] {
			httpx.Fail(w, httpx.CodeValidation, "status must be completed|failed|waiting|delayed")
			return
		}
		res, err := c.DB.Write(r.Context(),
			`DELETE FROM queue_jobs WHERE queue=? AND status=?`, r.PathValue("name"), status)
		if err != nil {
			httpx.Fail(w, httpx.CodeInternal, err.Error())
			return
		}
		n, _ := res.RowsAffected()
		audit(c, r, "queue.purge", "queue_jobs", r.PathValue("name"), map[string]any{"status": status, "deleted": n})
		httpx.Success(w, http.StatusOK, "Queue purged", map[string]any{"deleted": n}, nil)
	})

	rt.Handle(http.MethodGet, "/admin/queues/failed", roleAdmin, func(w http.ResponseWriter, r *http.Request) {
		rows, _ := c.DB.Maps(r.Context(),
			`SELECT id,queue,name,attempts,max_attempts,error,finished_at FROM queue_jobs
			 WHERE status='failed' ORDER BY id DESC LIMIT 200`)
		writeOK(w, rows)
	})

	rt.Handle(http.MethodGet, "/admin/queues/throughput", roleAdmin, func(w http.ResponseWriter, r *http.Request) {
		rows, _ := c.DB.Maps(r.Context(), `SELECT strftime('%Y-%m-%d %H:00', finished_at) AS hour,
			queue, COUNT(*) AS completed, CAST(AVG(duration_ms) AS INTEGER) AS avg_ms
			FROM queue_jobs WHERE status='completed' AND finished_at >= datetime('now','-24 hours')
			GROUP BY hour, queue ORDER BY hour`)
		writeOK(w, rows)
	})
}

func QueueNames() []string {
	return []string{"default", "webhook", "email", "report", "media"}
}

func QueueNameValid(name string) bool {
	for _, q := range QueueNames() {
		if strings.EqualFold(q, name) {
			return true
		}
	}
	return false
}
