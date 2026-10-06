package cron_test

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/arena/avaluna/internal/core"
	"github.com/arena/avaluna/internal/cron"
	"github.com/arena/avaluna/internal/testsupport"
)

func scalar(t *testing.T, env *testsupport.Env, q string, args ...any) string {
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

func TestRegisterUpsertsAndRejectsBadSpecs(t *testing.T) {
	env := testsupport.New(t)
	s := cron.New(env.Core)

	job := cron.Job{
		Name: "unit-test-job", Spec: "* * * * *", MaxAttempts: 1,
		Run: func(context.Context, *core.Core) (string, error) { return "ok", nil },
	}
	if err := s.Register(job); err != nil {
		t.Fatalf("first registration: %v", err)
	}

	job.Description = "updated description"
	job.Spec = "*/30 * * * *"
	if err := s.Register(job); err != nil {
		t.Fatalf("re-registration should update in place: %v", err)
	}
	if n := scalar(t, env, `SELECT COUNT(*) FROM cron_jobs WHERE name='unit-test-job'`); n != "1" {
		t.Errorf("re-registration created %s rows, want 1", n)
	}
	if d := scalar(t, env, `SELECT description FROM cron_jobs WHERE name='unit-test-job'`); d != "updated description" {
		t.Errorf("description after re-registration = %q", d)
	}

	bad := cron.Job{
		Name: "bad-spec-job", Spec: "not a cron expression", MaxAttempts: 1,
		Run: func(context.Context, *core.Core) (string, error) { return "", nil },
	}
	if err := s.Register(bad); err == nil {
		t.Error("an unparseable schedule should be rejected at registration")
	}

	if got := len(s.Jobs()); got != 1 {
		t.Errorf("scheduler holds %d jobs, want only the valid one", got)
	}
}

func TestRegistryDescribesEveryJob(t *testing.T) {
	env := testsupport.New(t)
	s := cron.New(env.Core)
	if err := cron.RegisterAll(s); err != nil {
		t.Fatalf("RegisterAll: %v", err)
	}
	reg := s.Registry()
	if len(reg) != 24 {
		t.Fatalf("registry has %d entries, want 24", len(reg))
	}
	for _, m := range reg {
		if m.Name == "" || m.Schedule == "" {
			t.Errorf("incomplete registry entry: %+v", m)
		}

		if d := cron.Describe(m.Schedule); d == "" {
			t.Errorf("job %q has no human description for %q", m.Name, m.Schedule)
		}
	}
}

func TestRunNowRecordsSuccessAndUpdatesCounters(t *testing.T) {
	env := testsupport.New(t)
	s := cron.New(env.Core)

	var runs atomic.Int32
	_ = s.Register(cron.Job{
		Name: "counting-job", Spec: "0 0 * * *", MaxAttempts: 1,
		Run: func(context.Context, *core.Core) (string, error) {
			runs.Add(1)
			return "did the thing", nil
		},
	})

	out, err := s.RunNow(context.Background(), "counting-job")
	if err != nil {
		t.Fatalf("RunNow: %v", err)
	}
	if out != "did the thing" {
		t.Errorf("output = %q", out)
	}
	if runs.Load() != 1 {
		t.Errorf("job body ran %d times, want 1", runs.Load())
	}

	if st := scalar(t, env, `SELECT last_status FROM cron_jobs WHERE name='counting-job'`); st != "success" {
		t.Errorf("last_status = %q, want success", st)
	}
	if n := scalar(t, env, `SELECT success_count FROM cron_jobs WHERE name='counting-job'`); n != "1" {
		t.Errorf("success_count = %q, want 1", n)
	}
	if n := scalar(t, env, `SELECT running FROM cron_jobs WHERE name='counting-job'`); n != "0" {
		t.Errorf("running flag left at %q after completion", n)
	}
	if n := scalar(t, env, `SELECT COUNT(*) FROM cron_runs WHERE job_id=(
		SELECT id FROM cron_jobs WHERE name='counting-job') AND status='success'`); n != "1" {
		t.Errorf("cron_runs rows = %q, want 1", n)
	}

	if next := scalar(t, env, `SELECT next_run_at FROM cron_jobs WHERE name='counting-job'`); next == "" {
		t.Error("next_run_at was not updated")
	}
}

func TestFailingJobIsRetriedThenRecordedAsFailed(t *testing.T) {
	env := testsupport.New(t)
	s := cron.New(env.Core)

	var attempts atomic.Int32
	_ = s.Register(cron.Job{
		Name: "always-fails", Spec: "0 0 * * *", MaxAttempts: 3,
		Run: func(context.Context, *core.Core) (string, error) {
			attempts.Add(1)
			return "", errors.New("boom")
		},
	})

	out, err := s.RunNow(context.Background(), "always-fails")
	if err == nil {
		t.Fatal("a job that always errors should report failure")
	}
	if got := attempts.Load(); got != 3 {
		t.Errorf("ran %d attempts, want MaxAttempts=3", got)
	}
	if !strings.Contains(out+err.Error(), "boom") {
		t.Errorf("the underlying error was lost: out=%q err=%v", out, err)
	}
	if st := scalar(t, env, `SELECT last_status FROM cron_jobs WHERE name='always-fails'`); st != "failed" {
		t.Errorf("last_status = %q, want failed", st)
	}
	if n := scalar(t, env, `SELECT failure_count FROM cron_jobs WHERE name='always-fails'`); n != "1" {
		t.Errorf("failure_count = %q, want 1", n)
	}
	if n := scalar(t, env, `SELECT running FROM cron_jobs WHERE name='always-fails'`); n != "0" {
		t.Error("the running flag was left set after a failure")
	}
}

func TestJobThatSucceedsOnRetryIsRecordedAsSuccess(t *testing.T) {
	env := testsupport.New(t)
	s := cron.New(env.Core)

	var attempts atomic.Int32
	_ = s.Register(cron.Job{
		Name: "flaky-job", Spec: "0 0 * * *", MaxAttempts: 3,
		Run: func(context.Context, *core.Core) (string, error) {
			if attempts.Add(1) < 2 {
				return "", errors.New("transient")
			}
			return "recovered", nil
		},
	})

	out, err := s.RunNow(context.Background(), "flaky-job")
	if err != nil {
		t.Fatalf("a job that recovers on retry should succeed: %v", err)
	}
	if out != "recovered" {
		t.Errorf("output = %q", out)
	}
	if got := attempts.Load(); got != 2 {
		t.Errorf("attempts = %d, want 2 (stop as soon as it succeeds)", got)
	}
	if st := scalar(t, env, `SELECT last_status FROM cron_jobs WHERE name='flaky-job'`); st != "success" {
		t.Errorf("last_status = %q, want success", st)
	}
}

func TestPanicInJobIsContainedAndReported(t *testing.T) {
	env := testsupport.New(t)
	s := cron.New(env.Core)

	_ = s.Register(cron.Job{
		Name: "panicking-job", Spec: "0 0 * * *", MaxAttempts: 1,
		Run: func(context.Context, *core.Core) (string, error) {
			panic("scheduler should survive this")
		},
	})

	_, err := s.RunNow(context.Background(), "panicking-job")
	if err == nil {
		t.Fatal("a panicking job should be reported as failed")
	}
	if !strings.Contains(err.Error(), "panic") {
		t.Errorf("error should identify the panic, got %v", err)
	}
	if st := scalar(t, env, `SELECT last_status FROM cron_jobs WHERE name='panicking-job'`); st != "failed" {
		t.Errorf("last_status = %q, want failed", st)
	}
}

func TestConcurrentRunsOfTheSameJobAreRejected(t *testing.T) {
	env := testsupport.New(t)
	s := cron.New(env.Core)

	release := make(chan struct{})
	started := make(chan struct{})
	_ = s.Register(cron.Job{
		Name: "slow-job", Spec: "0 0 * * *", MaxAttempts: 1,
		Run: func(context.Context, *core.Core) (string, error) {
			close(started)
			<-release
			return "done", nil
		},
	})

	go func() { _, _ = s.RunNow(context.Background(), "slow-job") }()
	<-started

	if _, err := s.RunNow(context.Background(), "slow-job"); err == nil {
		t.Error("a second concurrent run should be refused")
	} else if !strings.Contains(err.Error(), "already running") {
		t.Errorf("unexpected rejection reason: %v", err)
	}
	close(release)
}

func TestSetEnabledTogglesSchedulingAndPersists(t *testing.T) {
	env := testsupport.New(t)
	s := cron.New(env.Core)
	_ = s.Register(cron.Job{
		Name: "toggle-job", Spec: "* * * * *", MaxAttempts: 1,
		Run: func(context.Context, *core.Core) (string, error) { return "ok", nil },
	})

	ctx := context.Background()
	if err := s.SetEnabled(ctx, "toggle-job", false); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if got := scalar(t, env, `SELECT enabled FROM cron_jobs WHERE name='toggle-job'`); got != "0" {
		t.Errorf("enabled = %q after disabling", got)
	}
	if err := s.SetEnabled(ctx, "toggle-job", true); err != nil {
		t.Fatalf("enable: %v", err)
	}
	if got := scalar(t, env, `SELECT enabled FROM cron_jobs WHERE name='toggle-job'`); got != "1" {
		t.Errorf("enabled = %q after enabling", got)
	}
	if err := s.SetEnabled(ctx, "no-such-job", true); err == nil {
		t.Error("toggling an unknown job should fail")
	}

	_ = s.SetEnabled(ctx, "toggle-job", false)
	if _, err := s.RunNow(ctx, "toggle-job"); err != nil {
		t.Errorf("manual run of a disabled job should still work: %v", err)
	}
}

func TestRescheduleValidatesAndPersists(t *testing.T) {
	env := testsupport.New(t)
	s := cron.New(env.Core)
	_ = s.Register(cron.Job{
		Name: "reschedule-job", Spec: "0 0 * * *", MaxAttempts: 1,
		Run: func(context.Context, *core.Core) (string, error) { return "ok", nil },
	})

	ctx := context.Background()
	if err := s.Reschedule(ctx, "reschedule-job", "*/5 * * * *"); err != nil {
		t.Fatalf("reschedule: %v", err)
	}
	if got := scalar(t, env, `SELECT schedule FROM cron_jobs WHERE name='reschedule-job'`); got != "*/5 * * * *" {
		t.Errorf("stored schedule = %q", got)
	}

	if err := s.Reschedule(ctx, "reschedule-job", "99 99 * * *"); err == nil {
		t.Error("an invalid schedule should be refused")
	}
	if got := scalar(t, env, `SELECT schedule FROM cron_jobs WHERE name='reschedule-job'`); got != "*/5 * * * *" {
		t.Errorf("a rejected reschedule changed the stored schedule to %q", got)
	}
	if err := s.Reschedule(ctx, "no-such-job", "* * * * *"); err == nil {
		t.Error("rescheduling an unknown job should fail")
	}
}

func TestRunNowRejectsUnknownJobName(t *testing.T) {
	env := testsupport.New(t)
	s := cron.New(env.Core)
	if _, err := s.RunNow(context.Background(), "does-not-exist"); err == nil {
		t.Error("RunNow should fail for an unregistered job")
	}
}

func TestStartAndStopAreIdempotent(t *testing.T) {
	env := testsupport.New(t)

	t.Run("stop without start returns promptly", func(t *testing.T) {
		s := cron.New(env.Core)
		done := make(chan struct{})
		go func() { s.Stop(); close(done) }()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("Stop() on a scheduler that never started blocked")
		}
	})

	t.Run("repeated start and stop do not panic", func(t *testing.T) {
		s := cron.New(env.Core)
		_ = s.Register(cron.Job{
			Name: "ticking-job", Spec: "* * * * *", MaxAttempts: 1,
			Run: func(context.Context, *core.Core) (string, error) { return "tick", nil },
		})

		s.Start()
		s.Start()
		time.Sleep(50 * time.Millisecond)

		done := make(chan struct{})
		go func() {
			s.Stop()
			s.Stop()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("Stop() did not return")
		}
	})
}

func TestDescribeRendersCommonSchedules(t *testing.T) {
	for spec, wantFragment := range map[string]string{
		"* * * * *":    "minute",
		"0 * * * *":    "hour",
		"0 0 * * *":    "daily",
		"*/15 * * * *": "15",
	} {
		got := strings.ToLower(cron.Describe(spec))
		if got == "" {
			t.Errorf("Describe(%q) returned nothing", spec)
			continue
		}
		if !strings.Contains(got, wantFragment) {
			t.Errorf("Describe(%q) = %q, expected it to mention %q", spec, got, wantFragment)
		}
	}

	if cron.Describe("nonsense") == "" {
		t.Error("Describe should never return an empty string")
	}
}
