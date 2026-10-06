package cron_test

import (
	"context"
	"sort"
	"testing"

	"github.com/arena/avaluna/internal/cron"
	"github.com/arena/avaluna/internal/database"
	"github.com/arena/avaluna/internal/testsupport"
)

const wantJobCount = 24

func TestRegisterAllInstalls24Jobs(t *testing.T) {
	env := testsupport.New(t)
	s := cron.New(env.Core)
	if err := cron.RegisterAll(s); err != nil {
		t.Fatalf("RegisterAll: %v", err)
	}
	jobs := s.Jobs()
	if len(jobs) != wantJobCount {
		t.Fatalf("registered %d jobs, want %d", len(jobs), wantJobCount)
	}

	want := []string{
		"abandoned-cart-reminder", "order-auto-cancel-unpaid", "inventory-low-stock-alert",
		"currency-exchange-rate-sync", "flash-sale-price-activator", "subscription-renewal-billing",
		"license-expiry-check", "coupon-expiry-cleanup", "vendor-payout-processing",
		"tax-report-generation", "database-backup", "cache-warmup", "sitemap-regeneration",
		"search-index-rebuild", "analytics-aggregation", "review-reminder-email",
		"session-cleanup", "fraud-detection-scan", "gift-card-expiry-check",
		"loyalty-points-expiry", "webhook-retry-failed", "invoice-pdf-retry",
		"log-rotation-cleanup", "health-check-ping",
	}
	have := map[string]bool{}
	for _, j := range jobs {
		have[j.Name] = true
	}
	for _, n := range want {
		if !have[n] {
			t.Errorf("job %q is not registered", n)
		}
	}
}

func TestEverySpecParses(t *testing.T) {
	env := testsupport.New(t)
	s := cron.New(env.Core)
	if err := cron.RegisterAll(s); err != nil {
		t.Fatalf("RegisterAll: %v", err)
	}
	for _, j := range s.Jobs() {
		if _, err := cron.Parse(j.Spec); err != nil {
			t.Errorf("job %s has an invalid spec %q: %v", j.Name, j.Spec, err)
		}
		if j.Run == nil {
			t.Errorf("job %s has no run function", j.Name)
		}
	}
}

func TestSeededRegistryMatchesSchedulerRegistry(t *testing.T) {
	env := testsupport.New(t)
	s := cron.New(env.Core)
	if err := cron.RegisterAll(s); err != nil {
		t.Fatalf("RegisterAll: %v", err)
	}
	seeded := database.CronRegistryNames()
	sort.Strings(seeded)

	live := make([]string, 0, len(s.Jobs()))
	for _, j := range s.Jobs() {
		live = append(live, j.Name)
	}
	sort.Strings(live)

	if len(seeded) != len(live) {
		t.Fatalf("seed lists %d jobs, scheduler registers %d", len(seeded), len(live))
	}
	for i := range seeded {
		if seeded[i] != live[i] {
			t.Errorf("registry drift at %d: seed=%q scheduler=%q", i, seeded[i], live[i])
		}
	}
}

func TestEveryJobRunsWithoutError(t *testing.T) {
	env := testsupport.New(t)
	s := cron.New(env.Core)
	if err := cron.RegisterAll(s); err != nil {
		t.Fatalf("RegisterAll: %v", err)
	}
	ctx := context.Background()
	for _, j := range s.Jobs() {
		out, err := s.RunNow(ctx, j.Name)
		if err != nil {
			t.Errorf("job %s failed: %v", j.Name, err)
			continue
		}
		if out == "" {
			t.Errorf("job %s produced no output summary", j.Name)
		}
	}
}

func TestRunNowRejectsUnknownJob(t *testing.T) {
	env := testsupport.New(t)
	s := cron.New(env.Core)
	if _, err := s.RunNow(context.Background(), "no-such-job"); err == nil {
		t.Error("RunNow should fail for an unknown job")
	}
}
