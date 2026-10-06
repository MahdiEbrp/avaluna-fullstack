package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/arena/avaluna/internal/api"
	"github.com/arena/avaluna/internal/core"
)

type Handler func(ctx context.Context, c *core.Core, payload map[string]any) (string, error)

type Queue struct {
	c           *core.Core
	concurrency int
	handlers    map[string]Handler

	mu      sync.Mutex
	started bool
	stop    chan struct{}
	wg      sync.WaitGroup
}

func New(c *core.Core) *Queue {
	n := c.Cfg.QueueConcurrency
	if n <= 0 {
		n = 4
	}
	q := &Queue{c: c, concurrency: n, handlers: map[string]Handler{}, stop: make(chan struct{})}
	q.registerDefaults()
	return q
}

func (q *Queue) Register(name string, h Handler) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.handlers[name] = h
}

func (q *Queue) Handlers() []string {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make([]string, 0, len(q.handlers))
	for k := range q.handlers {
		out = append(out, k)
	}
	return out
}

func (q *Queue) Enqueue(ctx context.Context, queueName, name string, payload any, maxAttempts int) (int64, error) {
	if queueName == "" {
		queueName = "default"
	}
	if maxAttempts <= 0 {
		maxAttempts = 3
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		raw = []byte("{}")
	}
	res, err := q.c.DB.Write(ctx,
		`INSERT INTO queue_jobs(queue,name,payload_json,max_attempts,status) VALUES(?,?,?,?,'waiting')`,
		queueName, name, string(raw), maxAttempts)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (q *Queue) EnqueueIn(ctx context.Context, d time.Duration, queueName, name string, payload any, maxAttempts int) (int64, error) {
	id, err := q.Enqueue(ctx, queueName, name, payload, maxAttempts)
	if err != nil {
		return 0, err
	}
	runAt := time.Now().UTC().Add(d).Format("2006-01-02 15:04:05")
	_, _ = q.c.DB.Write(ctx, `UPDATE queue_jobs SET status='delayed', run_at=? WHERE id=?`, runAt, id)
	return id, nil
}

func (q *Queue) Stats(ctx context.Context) (map[string]map[string]int, error) {
	rows, err := q.c.DB.Maps(ctx, `SELECT queue, status, COUNT(*) AS n FROM queue_jobs GROUP BY queue, status`)
	if err != nil {
		return nil, err
	}
	out := map[string]map[string]int{}
	for _, row := range rows {
		qn, _ := row["queue"].(string)
		st, _ := row["status"].(string)
		if out[qn] == nil {
			out[qn] = map[string]int{}
		}
		out[qn][st] = toInt(row["n"])
	}
	return out, nil
}

func (q *Queue) Start() {
	q.mu.Lock()
	if q.started {
		q.mu.Unlock()
		return
	}
	q.started = true
	q.mu.Unlock()

	for i := 0; i < q.concurrency; i++ {
		q.wg.Add(1)
		go q.worker(i + 1)
	}
	q.c.Log.Info("queue started", "workers", q.concurrency, "handlers", len(q.handlers))
}

func (q *Queue) Stop() {
	q.mu.Lock()
	if !q.started {
		q.mu.Unlock()
		return
	}
	q.started = false
	close(q.stop)
	q.mu.Unlock()
	q.wg.Wait()
	q.c.Log.Info("queue stopped")
}

func (q *Queue) worker(id int) {
	defer q.wg.Done()

	ticker := time.NewTicker(time.Duration(500+id*120) * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-q.stop:
			return
		case <-ticker.C:
			for i := 0; i < 5; i++ {
				if !q.claimAndRun(id) {
					break
				}
			}
		}
	}
}

func (q *Queue) claimAndRun(workerID int) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	_, _ = q.c.DB.Write(ctx,
		`UPDATE queue_jobs SET status='waiting' WHERE status='delayed' AND datetime(run_at) <= datetime('now')`)

	row, err := q.c.DB.MapOne(ctx,
		`SELECT id,queue,name,payload_json,attempts,max_attempts FROM queue_jobs
		 WHERE status='waiting' AND datetime(run_at) <= datetime('now')
		 ORDER BY priority DESC, id ASC LIMIT 1`)
	if err != nil || row == nil {
		return false
	}
	id := toInt(row["id"])
	res, err := q.c.DB.Write(ctx,
		`UPDATE queue_jobs SET status='active', attempts=attempts+1, started_at=datetime('now')
		 WHERE id=? AND status='waiting'`, id)
	if err != nil {
		return false
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return false
	}

	name := toStr(row["name"])
	attempts := toInt(row["attempts"]) + 1
	maxAttempts := toInt(row["max_attempts"])
	payload := map[string]any{}
	_ = json.Unmarshal([]byte(toStr(row["payload_json"])), &payload)

	q.mu.Lock()
	h, ok := q.handlers[name]
	q.mu.Unlock()

	start := time.Now()
	var out string
	var runErr error
	if !ok {
		runErr = fmt.Errorf("no handler registered for %q", name)
	} else {
		out, runErr = q.safeRun(ctx, h, payload)
	}
	dur := time.Since(start).Milliseconds()

	if runErr == nil {
		_, _ = q.c.DB.Write(ctx,
			`UPDATE queue_jobs SET status='completed', result=?, error='', duration_ms=?,
			 finished_at=datetime('now') WHERE id=?`, truncate(out, 2000), dur, id)
		return true
	}
	if attempts >= maxAttempts {
		_, _ = q.c.DB.Write(ctx,
			`UPDATE queue_jobs SET status='failed', error=?, duration_ms=?, finished_at=datetime('now')
			 WHERE id=?`, truncate(runErr.Error(), 2000), dur, id)
		q.c.LogError(ctx, "error", "QUEUE_JOB_FAILED", name+": "+runErr.Error(), "queue/"+toStr(row["queue"]), "")
		return true
	}
	backoff := time.Duration(math.Pow(2, float64(attempts))) * 10 * time.Second
	runAt := time.Now().UTC().Add(backoff).Format("2006-01-02 15:04:05")
	_, _ = q.c.DB.Write(ctx,
		`UPDATE queue_jobs SET status='delayed', error=?, duration_ms=?, run_at=? WHERE id=?`,
		truncate(runErr.Error(), 2000), dur, runAt, id)
	return true
}

func (q *Queue) safeRun(ctx context.Context, h Handler, payload map[string]any) (out string, err error) {
	defer func() {
		if rec := recover(); rec != nil {
			err = fmt.Errorf("panic: %v", rec)
		}
	}()
	return h(ctx, q.c, payload)
}

func (q *Queue) registerDefaults() {
	q.Register("webhook.deliver", func(ctx context.Context, c *core.Core, p map[string]any) (string, error) {
		event := toStr(p["event"])
		if event == "" {
			return "", fmt.Errorf("event is required")
		}
		api.DispatchWebhook(ctx, c, event, p["data"])
		return "dispatched " + event, nil
	})

	q.Register("email.send", func(ctx context.Context, c *core.Core, p map[string]any) (string, error) {
		to, subject := toStr(p["to"]), toStr(p["subject"])
		if to == "" {
			return "", fmt.Errorf("to is required")
		}

		c.Notify(ctx, toInt(p["userId"]), "email", subject, toStr(p["body"]), toStr(p["link"]))
		c.Log.Info("sandbox email", "to", to, "subject", subject)
		return "queued to sandbox transport: " + to, nil
	})

	q.Register("invoice.render", func(ctx context.Context, c *core.Core, p map[string]any) (string, error) {
		orderID := toInt(p["orderId"])
		if orderID == 0 {
			return "", fmt.Errorf("orderId is required")
		}
		html, err := api.RenderInvoice(ctx, c, orderID)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("invoice rendered (%d bytes)", len(html)), nil
	})

	q.Register("report.generate", func(ctx context.Context, c *core.Core, p map[string]any) (string, error) {
		key := toStr(p["reportKey"])
		if key == "" {
			return "", fmt.Errorf("reportKey is required")
		}
		rows, _, err := api.RunReportByKey(ctx, c, key, toStr(p["from"]), toStr(p["to"]), 5000)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("report %s produced %d rows", key, len(rows)), nil
	})

	q.Register("search.reindex", func(ctx context.Context, c *core.Core, _ map[string]any) (string, error) {
		return api.RebuildSearchIndex(ctx, c)
	})

	q.Register("media.optimize", func(ctx context.Context, c *core.Core, p map[string]any) (string, error) {
		id := toInt(p["mediaId"])
		if id == 0 {
			return "", fmt.Errorf("mediaId is required")
		}
		_, err := c.DB.Write(ctx, `UPDATE media SET optimized=1 WHERE id=?`, id)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("media %d marked optimized", id), nil
	})
}

func toInt(v any) int {
	switch t := v.(type) {
	case int:
		return t
	case int64:
		return int(t)
	case float64:
		return int(t)
	case string:
		var n int
		_, _ = fmt.Sscanf(t, "%d", &n)
		return n
	}
	return 0
}

func toStr(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
