package api

import (
	"net/http"

	"github.com/arena/avaluna/internal/core"
	"github.com/arena/avaluna/internal/httpx"
)

func registerCronModule(rt *Router, c *core.Core) {
	rt.Module("22-cron")

	rt.Handle(http.MethodGet, "/admin/cron/jobs", roleSuper, func(w http.ResponseWriter, r *http.Request) {
		rows, err := c.DB.Maps(r.Context(), `SELECT * FROM cron_jobs ORDER BY name`)
		if err != nil {
			httpx.Fail(w, httpx.CodeInternal, err.Error())
			return
		}
		writeOK(w, rows)
	})

	rt.Handle(http.MethodGet, "/admin/cron/jobs/{id}", roleSuper, func(w http.ResponseWriter, r *http.Request) {
		job, err := cronJob(c, r)
		if err != nil {
			httpx.Fail(w, httpx.CodeCronNotFound, "")
			return
		}
		runs, _ := c.DB.Maps(r.Context(),
			`SELECT status,duration_ms,output,triggered_by,created_at FROM cron_runs
			 WHERE job_id=? ORDER BY id DESC LIMIT 20`, job["id"])
		writeOK(w, map[string]any{"job": job, "recentRuns": runs})
	})

	rt.Handle(http.MethodPost, "/admin/cron/jobs/{id}/run", roleSuper, func(w http.ResponseWriter, r *http.Request) {
		job, err := cronJob(c, r)
		if err != nil {
			httpx.Fail(w, httpx.CodeCronNotFound, "")
			return
		}
		if asInt(job["running"]) == 1 {
			httpx.Fail(w, httpx.CodeCronRunning, "")
			return
		}
		if c.Cron == nil {
			schedulerUnavailable(w)
			return
		}
		out, err := c.Cron.RunNow(r.Context(), asStr(job["name"]))
		audit(c, r, "cron.manual_run", "cron_jobs", asStr(job["name"]), map[string]any{"output": out})
		if err != nil {
			httpx.FailStatus(w, http.StatusOK, "CRON_JOB_FAILED", err.Error())
			return
		}
		httpx.Success(w, http.StatusOK, "Job executed", map[string]any{"output": out}, nil)
	})

	for _, action := range []string{"pause", "resume"} {
		act := action
		rt.Handle(http.MethodPatch, "/admin/cron/jobs/{id}/"+act, roleSuper, func(w http.ResponseWriter, r *http.Request) {
			job, err := cronJob(c, r)
			if err != nil {
				httpx.Fail(w, httpx.CodeCronNotFound, "")
				return
			}
			enabled := act == "resume"
			if c.Cron != nil {
				if err := c.Cron.SetEnabled(r.Context(), asStr(job["name"]), enabled); err != nil {
					httpx.Fail(w, httpx.CodeInternal, err.Error())
					return
				}
			} else {
				_, _ = c.DB.Write(r.Context(), `UPDATE cron_jobs SET enabled=? WHERE id=?`, boolInt(enabled), job["id"])
			}
			audit(c, r, "cron."+act, "cron_jobs", asStr(job["name"]), nil)
			httpx.NoBody(w, "Job "+act+"d")
		})
	}

	rt.Handle(http.MethodPatch, "/admin/cron/jobs/{id}/schedule", roleSuper, func(w http.ResponseWriter, r *http.Request) {
		job, err := cronJob(c, r)
		if err != nil {
			httpx.Fail(w, httpx.CodeCronNotFound, "")
			return
		}
		m, _ := body(r)
		spec := sVal(m, "schedule")
		if spec == "" {
			httpx.Fail(w, httpx.CodeValidation, "schedule is required")
			return
		}
		if c.Cron == nil {
			schedulerUnavailable(w)
			return
		}
		if err := c.Cron.Reschedule(r.Context(), asStr(job["name"]), spec); err != nil {
			httpx.Fail(w, httpx.CodeValidation, err.Error())
			return
		}
		audit(c, r, "cron.reschedule", "cron_jobs", asStr(job["name"]), map[string]any{"schedule": spec})
		httpx.NoBody(w, "Schedule updated")
	})

	rt.Handle(http.MethodGet, "/admin/cron/jobs/{id}/logs", roleSuper, func(w http.ResponseWriter, r *http.Request) {
		job, err := cronJob(c, r)
		if err != nil {
			httpx.Fail(w, httpx.CodeCronNotFound, "")
			return
		}
		page, limit, offset := paginate(r)
		total, _ := c.DB.Count(r.Context(), `SELECT COUNT(*) FROM cron_runs WHERE job_id=?`, job["id"])
		rows, _ := c.DB.Maps(r.Context(),
			`SELECT * FROM cron_runs WHERE job_id=? ORDER BY id DESC LIMIT ? OFFSET ?`, job["id"], limit, offset)
		httpx.List(w, rows, httpx.MakePagination(page, limit, total))
	})

	rt.Handle(http.MethodGet, "/admin/cron/jobs/{id}/history", roleSuper, func(w http.ResponseWriter, r *http.Request) {
		job, err := cronJob(c, r)
		if err != nil {
			httpx.Fail(w, httpx.CodeCronNotFound, "")
			return
		}
		hist, _ := c.DB.Maps(r.Context(), `SELECT DATE(created_at) AS date, status, COUNT(*) AS count,
			CAST(AVG(duration_ms) AS INTEGER) AS avg_ms FROM cron_runs WHERE job_id=?
			GROUP BY DATE(created_at), status ORDER BY date DESC LIMIT 60`, job["id"])
		writeOK(w, map[string]any{
			"job": map[string]any{"name": job["name"], "successCount": job["success_count"],
				"failureCount": job["failure_count"], "avgMs": job["avg_ms"]},
			"history": hist,
		})
	})

	rt.Handle(http.MethodGet, "/admin/cron/registry", roleSuper, func(w http.ResponseWriter, r *http.Request) {
		if c.Cron == nil {
			writeOK(w, []any{})
			return
		}
		writeOK(w, c.Cron.Registry())
	})
}

func cronJob(c *core.Core, r *http.Request) (map[string]any, error) {
	key := r.PathValue("id")
	return c.DB.MapOne(r.Context(), `SELECT * FROM cron_jobs WHERE id=? OR name=?`, key, key)
}

func schedulerUnavailable(w http.ResponseWriter) {
	httpx.FailStatus(w, http.StatusServiceUnavailable, "CRON_SCHEDULER_UNAVAILABLE",
		"The cron scheduler is not running in this process")
}
