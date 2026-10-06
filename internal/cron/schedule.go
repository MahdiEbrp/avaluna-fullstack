package cron

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

type Schedule struct {
	Spec    string
	Minutes map[int]bool
	Hours   map[int]bool
	Days    map[int]bool
	Months  map[int]bool
	Weekday map[int]bool
}

func Parse(spec string) (*Schedule, error) {
	spec = strings.TrimSpace(spec)
	switch spec {
	case "@hourly":
		spec = "0 * * * *"
	case "@daily", "@midnight":
		spec = "0 0 * * *"
	case "@weekly":
		spec = "0 0 * * 0"
	case "@monthly":
		spec = "0 0 1 * *"
	case "@yearly", "@annually":
		spec = "0 0 1 1 *"
	}
	fields := strings.Fields(spec)
	if len(fields) != 5 {
		return nil, fmt.Errorf("cron expression must have 5 fields, got %d", len(fields))
	}
	mins, err := parseField(fields[0], 0, 59)
	if err != nil {
		return nil, fmt.Errorf("minute: %w", err)
	}
	hours, err := parseField(fields[1], 0, 23)
	if err != nil {
		return nil, fmt.Errorf("hour: %w", err)
	}
	days, err := parseField(fields[2], 1, 31)
	if err != nil {
		return nil, fmt.Errorf("day-of-month: %w", err)
	}
	months, err := parseField(fields[3], 1, 12)
	if err != nil {
		return nil, fmt.Errorf("month: %w", err)
	}
	dows, err := parseField(fields[4], 0, 6)
	if err != nil {
		return nil, fmt.Errorf("day-of-week: %w", err)
	}
	return &Schedule{Spec: spec, Minutes: mins, Hours: hours, Days: days, Months: months, Weekday: dows}, nil
}

func (s *Schedule) Matches(t time.Time) bool {
	t = t.UTC()
	return s.Minutes[t.Minute()] && s.Hours[t.Hour()] &&
		s.Days[t.Day()] && s.Months[int(t.Month())] && s.Weekday[int(t.Weekday())]
}

func (s *Schedule) Next(from time.Time) time.Time {
	t := from.UTC().Truncate(time.Minute).Add(time.Minute)
	limit := t.Add(366 * 24 * time.Hour)
	for t.Before(limit) {
		if s.Matches(t) {
			return t
		}
		t = t.Add(time.Minute)
	}
	return time.Time{}
}

func parseField(field string, min, max int) (map[int]bool, error) {
	out := map[int]bool{}
	for _, part := range strings.Split(field, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			return nil, fmt.Errorf("empty element")
		}
		step := 1
		if i := strings.Index(part, "/"); i >= 0 {
			n, err := strconv.Atoi(part[i+1:])
			if err != nil || n <= 0 {
				return nil, fmt.Errorf("invalid step %q", part[i+1:])
			}
			step = n
			part = part[:i]
		}
		lo, hi := min, max
		switch {
		case part == "*" || part == "":

		case strings.Contains(part, "-"):
			bits := strings.SplitN(part, "-", 2)
			a, err1 := strconv.Atoi(bits[0])
			b, err2 := strconv.Atoi(bits[1])
			if err1 != nil || err2 != nil {
				return nil, fmt.Errorf("invalid range %q", part)
			}
			lo, hi = a, b
		default:
			n, err := strconv.Atoi(part)
			if err != nil {
				return nil, fmt.Errorf("invalid value %q", part)
			}
			lo, hi = n, n
		}
		if lo < min || hi > max || lo > hi {
			return nil, fmt.Errorf("value out of range [%d-%d]", min, max)
		}
		for v := lo; v <= hi; v += step {
			out[v] = true
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no values matched")
	}
	return out, nil
}

func Describe(spec string) string {
	switch spec {
	case "* * * * *":
		return "every minute"
	case "0 * * * *":
		return "hourly"
	case "0 0 * * *":
		return "daily at midnight UTC"
	}
	f := strings.Fields(spec)
	if len(f) == 5 && strings.HasPrefix(f[0], "*/") && f[1] == "*" {
		return "every " + strings.TrimPrefix(f[0], "*/") + " minutes"
	}
	if len(f) == 5 && f[0] != "*" && strings.HasPrefix(f[1], "*/") {
		return "every " + strings.TrimPrefix(f[1], "*/") + " hours"
	}
	return spec
}
