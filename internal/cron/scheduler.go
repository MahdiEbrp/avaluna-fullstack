package cron

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/arena/avaluna/internal/core"
)

type JobFunc func(ctx context.Context, c *core.Core) (string, error)

type Job struct {
	Name        string
	Spec        string
	Description string
	Retry       string
	MaxAttempts int
	Backoff     bool
	Timeout     time.Duration
	Run         JobFunc
}

type Scheduler struct {
	c     *core.Core
	log   *slog.Logger
	mu    sync.RWMutex
	jobs  map[string]*Job
	sched map[string]*Schedule

	running   map[string]bool
	runningMu sync.Mutex

	stop    chan struct{}
	done    chan struct{}
	startMu sync.Mutex
	started bool
}

func New(c *core.Core) *Scheduler {
	return &Scheduler{
		c: c, log: c.Log.With("component", "cron"),
		jobs: map[string]*Job{}, sched: map[string]*Schedule{},
		running: map[string]bool{},
		stop:    make(chan struct{}), done: make(chan struct{}),
	}
}

func (s *Scheduler) Register(j Job) error {
	sc, err := Parse(j.Spec)
	if err != nil {
		return fmt.Errorf("job %s: %w", j.Name, err)
	}
	if j.MaxAttempts <= 0 {
		j.MaxAttempts = 1
	}
	if j.Timeout <= 0 {
		j.Timeout = 2 * time.Minute
	}
	s.mu.Lock()
	s.jobs[j.Name] = &j
	s.sched[j.Name] = sc
	s.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	next := sc.Next(time.Now().UTC())
	_, err = s.c.DB.Write(ctx, `
		INSERT INTO cron_jobs(name,schedule,description,retry_policy,enabled,next_run_at)
		VALUES(?,?,?,?,1,?)
		ON CONFLICT(name) DO UPDATE SET description=excluded.description,
		   retry_policy=excluded.retry_policy, next_run_at=excluded.next_run_at,
		   updated_at=datetime('now')`,
		j.Name, j.Spec, j.Description, j.Retry, next.Format("2006-01-02 15:04:05"))
	return err
}

func (s *Scheduler) Registry() []core.JobMeta {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]core.JobMeta, 0, len(s.jobs))
	for _, j := range s.jobs {
		out = append(out, core.JobMeta{Name: j.Name, Schedule: j.Spec, Description: j.Description, Retry: j.Retry})
	}
	return out
}

func (s *Scheduler) Jobs() []Job {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Job, 0, len(s.jobs))
	for _, j := range s.jobs {
		out = append(out, *j)
	}
	sort.Slice(out, func(i, k int) bool { return out[i].Name < out[k].Name })
	return out
}

func (s *Scheduler) Start() {

	s.startMu.Lock()
	if s.started {
		s.startMu.Unlock()
		s.log.Warn("cron scheduler already started, ignoring duplicate Start")
		return
	}
	s.started = true
	s.startMu.Unlock()

	go func() {
		defer close(s.done)

		now := time.Now().UTC()
		first := now.Truncate(time.Minute).Add(time.Minute)
		timer := time.NewTimer(time.Until(first))
		defer timer.Stop()
		for {
			select {
			case <-s.stop:
				return
			case <-timer.C:
				s.tick(time.Now().UTC())
				timer.Reset(time.Until(time.Now().UTC().Truncate(time.Minute).Add(time.Minute)))
			}
		}
	}()
	s.log.Info("cron scheduler started", "jobs", len(s.jobs))
}

func (s *Scheduler) Stop() {
	s.startMu.Lock()
	started := s.started
	s.started = false
	s.startMu.Unlock()

	select {
	case <-s.stop:
	default:
		close(s.stop)
	}
	if !started {

		return
	}
	select {
	case <-s.done:
	case <-time.After(3 * time.Second):
		s.log.Warn("cron scheduler did not stop within the grace period")
	}
}

func (s *Scheduler) tick(now time.Time) {
	s.mu.RLock()
	type due struct {
		job *Job
		sc  *Schedule
	}
	list := []due{}
	for name, j := range s.jobs {
		if sc := s.sched[name]; sc != nil && sc.Matches(now) {
			list = append(list, due{j, sc})
		}
	}
	s.mu.RUnlock()

	for _, d := range list {
		if !s.enabled(d.job.Name) {
			continue
		}
		job := d.job
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), job.Timeout)
			defer cancel()
			_, _ = s.execute(ctx, job, "scheduler")
		}()
	}
}

func (s *Scheduler) enabled(name string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	n, err := s.c.DB.Count(ctx, `SELECT COALESCE(enabled,1) FROM cron_jobs WHERE name=?`, name)
	if err != nil {
		return true
	}
	return n == 1
}

func (s *Scheduler) RunNow(ctx context.Context, name string) (string, error) {
	s.mu.RLock()
	job := s.jobs[name]
	s.mu.RUnlock()
	if job == nil {
		return "", errors.New("cron job not found")
	}
	return s.execute(ctx, job, "manual")
}

func (s *Scheduler) SetEnabled(ctx context.Context, name string, enabled bool) error {
	s.mu.RLock()
	_, ok := s.jobs[name]
	s.mu.RUnlock()
	if !ok {
		return errors.New("cron job not found")
	}
	v := 0
	if enabled {
		v = 1
	}
	_, err := s.c.DB.Write(ctx, `UPDATE cron_jobs SET enabled=?, updated_at=datetime('now') WHERE name=?`, v, name)
	return err
}

func (s *Scheduler) Reschedule(ctx context.Context, name, spec string) error {
	sc, err := Parse(spec)
	if err != nil {
		return err
	}
	s.mu.Lock()
	job, ok := s.jobs[name]
	if !ok {
		s.mu.Unlock()
		return errors.New("cron job not found")
	}
	job.Spec = spec
	s.sched[name] = sc
	s.mu.Unlock()

	_, err = s.c.DB.Write(ctx, `UPDATE cron_jobs SET schedule=?, next_run_at=?, updated_at=datetime('now') WHERE name=?`,
		spec, sc.Next(time.Now().UTC()).Format("2006-01-02 15:04:05"), name)
	return err
}

func (s *Scheduler) execute(ctx context.Context, job *Job, trigger string) (string, error) {

	s.runningMu.Lock()
	if s.running[job.Name] {
		s.runningMu.Unlock()
		return "", errors.New("cron job is already running")
	}
	s.running[job.Name] = true
	s.runningMu.Unlock()
	defer func() {
		s.runningMu.Lock()
		delete(s.running, job.Name)
		s.runningMu.Unlock()
	}()

	var jobID int
	if err := s.c.DB.Row(ctx, `SELECT id FROM cron_jobs WHERE name=?`, []any{job.Name}, &jobID); err != nil {
		jobID = 0
	}
	_, _ = s.c.DB.Write(ctx, `UPDATE cron_jobs SET running=1, last_status='running' WHERE name=?`, job.Name)

	start := time.Now()
	var output string
	var err error
	for attempt := 1; attempt <= job.MaxAttempts; attempt++ {
		output, err = runGuarded(ctx, job, s.c)
		if err == nil {
			break
		}
		s.log.Warn("cron attempt failed", "job", job.Name, "attempt", attempt, "error", err.Error())
		if attempt < job.MaxAttempts {
			delay := time.Duration(attempt) * time.Second
			if job.Backoff {
				delay = time.Duration(1<<attempt) * time.Second
			}
			select {
			case <-ctx.Done():
				err = ctx.Err()
			case <-time.After(delay):
			}
		}
	}
	dur := time.Since(start)
	status := "success"
	if err != nil {
		status = "failed"
		output = err.Error()
	}

	s.mu.RLock()
	sc := s.sched[job.Name]
	s.mu.RUnlock()
	next := ""
	if sc != nil {
		next = sc.Next(time.Now().UTC()).Format("2006-01-02 15:04:05")
	}
	if jobID > 0 {
		_, _ = s.c.DB.Write(ctx,
			`INSERT INTO cron_runs(job_id,status,duration_ms,output,triggered_by) VALUES(?,?,?,?,?)`,
			jobID, status, dur.Milliseconds(), truncate(output, 2000), trigger)
	}
	col := "success_count"
	if status == "failed" {
		col = "failure_count"
	}
	_, _ = s.c.DB.Write(ctx, fmt.Sprintf(`UPDATE cron_jobs SET running=0, last_status=?,
		last_run_at=datetime('now'), next_run_at=?, %s = %s + 1,
		avg_ms = CASE WHEN avg_ms = 0 THEN ? ELSE (avg_ms + ?)/2 END,
		updated_at=datetime('now') WHERE name=?`, col, col),
		status, next, dur.Milliseconds(), dur.Milliseconds(), job.Name)

	s.c.Audit(ctx, 0, "system", "cron."+status, "cron_jobs", job.Name, "", map[string]any{
		"trigger": trigger, "durationMs": dur.Milliseconds(), "output": truncate(output, 300),
	})
	if err != nil {
		s.log.Error("cron job failed", "job", job.Name, "error", err.Error(), "took", dur)
		return output, err
	}
	s.log.Info("cron job finished", "job", job.Name, "took", dur, "output", truncate(output, 120))
	return output, nil
}

func runGuarded(ctx context.Context, job *Job, c *core.Core) (out string, err error) {
	defer func() {
		if rec := recover(); rec != nil {
			err = fmt.Errorf("panic: %v", rec)
		}
	}()
	return job.Run(ctx, c)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

var _ core.Scheduler = (*Scheduler)(nil)
