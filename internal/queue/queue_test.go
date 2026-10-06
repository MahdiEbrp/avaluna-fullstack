package queue_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/arena/avaluna/internal/core"
	"github.com/arena/avaluna/internal/queue"
	"github.com/arena/avaluna/internal/testsupport"
)

func TestDefaultHandlersRegistered(t *testing.T) {
	env := testsupport.New(t)
	q := queue.New(env.Core)
	want := []string{"webhook.deliver", "email.send", "invoice.render",
		"report.generate", "search.reindex", "media.optimize"}
	have := map[string]bool{}
	for _, h := range q.Handlers() {
		have[h] = true
	}
	for _, w := range want {
		if !have[w] {
			t.Errorf("handler %q is not registered", w)
		}
	}
}

func TestEnqueueAndProcess(t *testing.T) {
	env := testsupport.New(t)
	q := queue.New(env.Core)

	var ran atomic.Int32
	q.Register("test.ok", func(ctx context.Context, c *core.Core, payload map[string]any) (string, error) {
		ran.Add(1)
		return "done", nil
	})

	id, err := q.Enqueue(context.Background(), "default", "test.ok", map[string]any{"x": 1}, 1)
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if id == 0 {
		t.Fatal("enqueue returned a zero id")
	}

	q.Start()
	defer q.Stop()

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var status string
		if err := env.DB.Row(context.Background(),
			`SELECT status FROM queue_jobs WHERE id=?`, []any{id}, &status); err == nil && status == "completed" {
			if ran.Load() != 1 {
				t.Errorf("handler ran %d times", ran.Load())
			}
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("job never reached the completed state")
}

func TestFailedJobIsRescheduledWithBackoff(t *testing.T) {
	env := testsupport.New(t)
	q := queue.New(env.Core)

	var attempts atomic.Int32
	q.Register("test.fail", func(ctx context.Context, c *core.Core, payload map[string]any) (string, error) {
		attempts.Add(1)
		return "", errors.New("deliberate failure")
	})

	retried, err := q.Enqueue(context.Background(), "default", "test.fail", nil, 3)
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	terminal, err := q.Enqueue(context.Background(), "default", "test.fail", nil, 1)
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	q.Start()
	defer q.Stop()

	waitStatus := func(id int64, want string) string {
		deadline := time.Now().Add(15 * time.Second)
		var status string
		for time.Now().Before(deadline) {
			if err := env.DB.Row(context.Background(),
				`SELECT status FROM queue_jobs WHERE id=?`, []any{id}, &status); err == nil && status == want {
				return status
			}
			time.Sleep(100 * time.Millisecond)
		}
		return status
	}

	if got := waitStatus(retried, "delayed"); got != "delayed" {
		t.Errorf("retryable job status = %q, want delayed", got)
	}
	if got := waitStatus(terminal, "failed"); got != "failed" {
		t.Errorf("terminal job status = %q, want failed", got)
	}

	var n int
	if err := env.DB.Row(context.Background(),
		`SELECT attempts FROM queue_jobs WHERE id=?`, []any{terminal}, &n); err == nil && n != 1 {
		t.Errorf("terminal job attempts = %d, want 1", n)
	}
	if attempts.Load() < 2 {
		t.Errorf("handler ran %d times, want at least 2", attempts.Load())
	}
}

func TestPanicInHandlerIsRecovered(t *testing.T) {
	env := testsupport.New(t)
	q := queue.New(env.Core)
	q.Register("test.panic", func(ctx context.Context, c *core.Core, payload map[string]any) (string, error) {
		panic("boom")
	})
	id, err := q.Enqueue(context.Background(), "default", "test.panic", nil, 1)
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	q.Start()
	defer q.Stop()

	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		var status string
		if err := env.DB.Row(context.Background(),
			`SELECT status FROM queue_jobs WHERE id=?`, []any{id}, &status); err == nil && status == "failed" {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("a panicking handler should mark the job failed, not take the worker down")
}

func TestStatsGroupsByQueueAndStatus(t *testing.T) {
	env := testsupport.New(t)
	q := queue.New(env.Core)
	if _, err := q.Enqueue(context.Background(), "mail", "email.send",
		map[string]any{"to": "x@y.z"}, 1); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	stats, err := q.Stats(context.Background())
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if stats["mail"]["waiting"] != 1 {
		t.Errorf("stats = %+v", stats)
	}
}

func TestDelayedJobIsNotRunImmediately(t *testing.T) {
	env := testsupport.New(t)
	q := queue.New(env.Core)
	id, err := q.EnqueueIn(context.Background(), time.Hour, "default", "email.send", nil, 1)
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	var status string
	if err := env.DB.Row(context.Background(),
		`SELECT status FROM queue_jobs WHERE id=?`, []any{id}, &status); err != nil {
		t.Fatalf("read: %v", err)
	}
	if status != "delayed" && status != "waiting" {
		t.Errorf("status = %q", status)
	}
}

func qScalar(t *testing.T, env *testsupport.Env, q string, args ...any) string {
	t.Helper()
	v, err := env.DB.Scalar(context.Background(), q, args...)
	if err != nil {
		t.Fatalf("scalar %q: %v", q, err)
	}
	if !v.Valid {
		return ""
	}
	return v.String
}

func waitFor(t *testing.T, d time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestEnqueueRejectsUnknownHandlerAtRunTime(t *testing.T) {
	env := testsupport.New(t)
	q := queue.New(env.Core)

	id, err := q.Enqueue(context.Background(), "default", "no.such.handler", map[string]any{}, 1)
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	q.Start()
	defer q.Stop()

	waitFor(t, 5*time.Second, "job to fail", func() bool {
		return qScalar(t, env, `SELECT status FROM queue_jobs WHERE id=?`, id) == "failed"
	})
	if e := qScalar(t, env, `SELECT error FROM queue_jobs WHERE id=?`, id); !strings.Contains(e, "no handler") {
		t.Errorf("error = %q, want it to mention the missing handler", e)
	}
}

func TestJobPayloadRoundTrips(t *testing.T) {
	env := testsupport.New(t)
	q := queue.New(env.Core)

	got := make(chan map[string]any, 1)
	q.Register("payload.echo", func(_ context.Context, _ *core.Core, p map[string]any) (string, error) {
		got <- p
		return "echoed", nil
	})

	payload := map[string]any{
		"orderId": 42,
		"email":   "customer@example.com",
		"nested":  map[string]any{"k": "v"},
		"list":    []any{1, 2, 3},
	}
	id, err := q.Enqueue(context.Background(), "default", "payload.echo", payload, 1)
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	q.Start()
	defer q.Stop()

	select {
	case p := <-got:
		if p["email"] != "customer@example.com" {
			t.Errorf("email did not survive the round trip: %#v", p)
		}

		if fmt.Sprint(p["orderId"]) != "42" {
			t.Errorf("orderId = %#v, want 42", p["orderId"])
		}
		if nested, ok := p["nested"].(map[string]any); !ok || nested["k"] != "v" {
			t.Errorf("nested object lost: %#v", p["nested"])
		}
	case <-time.After(5 * time.Second):
		t.Fatal("handler never ran")
	}

	waitFor(t, 5*time.Second, "completion", func() bool {
		return qScalar(t, env, `SELECT status FROM queue_jobs WHERE id=?`, id) == "completed"
	})
	if r := qScalar(t, env, `SELECT result FROM queue_jobs WHERE id=?`, id); r != "echoed" {
		t.Errorf("result = %q, want echoed", r)
	}
	if d := qScalar(t, env, `SELECT duration_ms FROM queue_jobs WHERE id=?`, id); d == "" {
		t.Error("duration was not recorded")
	}
}

func TestPriorityOrdersTheQueue(t *testing.T) {
	env := testsupport.New(t)

	env.Core.Cfg.QueueConcurrency = 1
	q := queue.New(env.Core)

	var mu sync.Mutex
	var order []string
	done := make(chan struct{})
	q.Register("priority.probe", func(_ context.Context, _ *core.Core, p map[string]any) (string, error) {
		mu.Lock()
		order = append(order, fmt.Sprint(p["tag"]))
		n := len(order)
		mu.Unlock()
		if n == 3 {
			close(done)
		}
		return "", nil
	})

	ctx := context.Background()

	for _, tc := range []struct {
		tag      string
		priority int
	}{{"low", 0}, {"high", 10}, {"mid", 5}} {
		id, err := q.Enqueue(ctx, "default", "priority.probe", map[string]any{"tag": tc.tag}, 1)
		if err != nil {
			t.Fatalf("enqueue %s: %v", tc.tag, err)
		}
		if _, err := env.DB.Write(ctx, `UPDATE queue_jobs SET priority=? WHERE id=?`, tc.priority, id); err != nil {
			t.Fatalf("set priority: %v", err)
		}
	}

	q.Start()
	defer q.Stop()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		mu.Lock()
		got := append([]string(nil), order...)
		mu.Unlock()
		t.Fatalf("only %d of 3 jobs ran: %v", len(got), got)
	}
	mu.Lock()
	defer mu.Unlock()
	if want := []string{"high", "mid", "low"}; !slices.Equal(order, want) {
		t.Errorf("execution order = %v, want %v (priority before insertion order)", order, want)
	}
}

func TestStatsCountsEveryState(t *testing.T) {
	env := testsupport.New(t)
	q := queue.New(env.Core)
	ctx := context.Background()

	if _, err := q.Enqueue(ctx, "reports", "report.generate", map[string]any{}, 1); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if _, err := q.EnqueueIn(ctx, time.Hour, "reports", "report.generate", map[string]any{}, 1); err != nil {
		t.Fatalf("enqueue delayed: %v", err)
	}

	stats, err := q.Stats(ctx)
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	reports, ok := stats["reports"]
	if !ok {
		t.Fatalf("no stats for the reports queue: %#v", stats)
	}
	if reports["waiting"] != 1 {
		t.Errorf("waiting = %d, want 1", reports["waiting"])
	}
	if reports["delayed"] != 1 {
		t.Errorf("delayed = %d, want 1", reports["delayed"])
	}
}

func TestEnqueueInSchedulesForTheFuture(t *testing.T) {
	env := testsupport.New(t)
	q := queue.New(env.Core)

	id, err := q.EnqueueIn(context.Background(), 2*time.Hour, "default", "email.send", map[string]any{}, 1)
	if err != nil {
		t.Fatalf("EnqueueIn: %v", err)
	}
	if st := qScalar(t, env, `SELECT status FROM queue_jobs WHERE id=?`, id); st != "delayed" {
		t.Errorf("status = %q, want delayed", st)
	}

	future := qScalar(t, env,
		`SELECT CASE WHEN datetime(run_at) > datetime('now') THEN 'yes' ELSE 'no' END FROM queue_jobs WHERE id=?`, id)
	if future != "yes" {
		t.Error("a delayed job was scheduled in the past")
	}
}

func TestRetryExhaustionMarksJobFailed(t *testing.T) {
	env := testsupport.New(t)
	q := queue.New(env.Core)

	var attempts atomic.Int32
	q.Register("always.fails", func(context.Context, *core.Core, map[string]any) (string, error) {
		attempts.Add(1)
		return "", errors.New("nope")
	})

	id, err := q.Enqueue(context.Background(), "default", "always.fails", map[string]any{}, 1)
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	q.Start()
	defer q.Stop()

	waitFor(t, 5*time.Second, "terminal failure", func() bool {
		return qScalar(t, env, `SELECT status FROM queue_jobs WHERE id=?`, id) == "failed"
	})
	if got := attempts.Load(); got != 1 {
		t.Errorf("handler ran %d times, want 1", got)
	}
	if e := qScalar(t, env, `SELECT error FROM queue_jobs WHERE id=?`, id); !strings.Contains(e, "nope") {
		t.Errorf("stored error = %q", e)
	}
	if f := qScalar(t, env, `SELECT finished_at FROM queue_jobs WHERE id=?`, id); f == "" {
		t.Error("finished_at was not stamped on the failed job")
	}
}

func TestStartAndStopAreSafeToRepeat(t *testing.T) {
	env := testsupport.New(t)
	q := queue.New(env.Core)

	done := make(chan struct{})
	go func() { q.Stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop() on an unstarted queue blocked")
	}

	q.Start()
	q.Start()

	done = make(chan struct{})
	go func() {
		q.Stop()
		q.Stop()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("repeated Stop() did not return")
	}
}

func TestHandlersAreListedForTheAdminConsole(t *testing.T) {
	env := testsupport.New(t)
	q := queue.New(env.Core)
	before := len(q.Handlers())
	q.Register("custom.handler", func(context.Context, *core.Core, map[string]any) (string, error) {
		return "", nil
	})
	after := q.Handlers()
	if len(after) != before+1 {
		t.Errorf("handler count %d -> %d, want one more", before, len(after))
	}
	found := false
	for _, h := range after {
		if h == "custom.handler" {
			found = true
		}
	}
	if !found {
		t.Errorf("custom.handler missing from %v", after)
	}
}
