package cron

import (
	"testing"
	"time"
)

func TestParseRejectsBadExpressions(t *testing.T) {
	for _, spec := range []string{"", "* * *", "60 * * * *", "* 24 * * *", "a b c d e"} {
		if _, err := Parse(spec); err == nil {
			t.Errorf("Parse(%q) should have failed", spec)
		}
	}
}

func TestParseAliases(t *testing.T) {
	for _, spec := range []string{"@hourly", "@daily", "@weekly", "@monthly", "@yearly"} {
		if _, err := Parse(spec); err != nil {
			t.Errorf("Parse(%q) = %v", spec, err)
		}
	}
}

func TestMatchesAndNext(t *testing.T) {
	s, err := Parse("*/15 * * * *")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	base := time.Date(2026, 10, 4, 10, 0, 0, 0, time.UTC)
	if !s.Matches(base) {
		t.Error("10:00 should match */15")
	}
	if s.Matches(base.Add(time.Minute)) {
		t.Error("10:01 should not match */15")
	}
	next := s.Next(base)
	if want := base.Add(15 * time.Minute); !next.Equal(want) {
		t.Errorf("Next = %v, want %v", next, want)
	}
}

func TestMonthlySchedule(t *testing.T) {
	s, err := Parse("0 5 1 * *")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !s.Matches(time.Date(2026, 11, 1, 5, 0, 0, 0, time.UTC)) {
		t.Error("the first of the month at 05:00 should match")
	}
	if s.Matches(time.Date(2026, 11, 2, 5, 0, 0, 0, time.UTC)) {
		t.Error("the second of the month should not match")
	}
}

func TestWeekdaySchedule(t *testing.T) {
	s, err := Parse("0 4 * * 1")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	monday := time.Date(2026, 10, 5, 4, 0, 0, 0, time.UTC)
	if monday.Weekday() != time.Monday {
		t.Fatalf("fixture is not a Monday: %v", monday.Weekday())
	}
	if !s.Matches(monday) {
		t.Error("Monday 04:00 should match")
	}
	if s.Matches(monday.AddDate(0, 0, 1)) {
		t.Error("Tuesday should not match")
	}
}
