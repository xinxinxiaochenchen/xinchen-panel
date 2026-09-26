package billing

import (
	"errors"
	"testing"
	"time"
)

func instant(t *testing.T, s string) time.Time {
	t.Helper()
	v, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestMonthlyPeriodBoundaries(t *testing.T) {
	tests := []struct {
		name, start, end, zone string
		anchor, months         int
		at, wantStart, wantEnd string
	}{
		{"January 31", "2025-01-31T10:00:00+08:00", "2026-06-01T00:00:00+08:00", "Asia/Shanghai", 31, 1, "2025-02-27T23:59:59+08:00", "2025-01-31T10:00:00+08:00", "2025-02-28T00:00:00+08:00"},
		{"restore March 31", "2025-01-31T10:00:00+08:00", "2026-06-01T00:00:00+08:00", "Asia/Shanghai", 31, 1, "2025-02-28T00:00:00+08:00", "2025-02-28T00:00:00+08:00", "2025-03-31T00:00:00+08:00"},
		{"April 30", "2025-01-31T00:00:00Z", "2026-06-01T00:00:00Z", "UTC", 31, 1, "2025-04-30T00:00:00Z", "2025-04-30T00:00:00Z", "2025-05-31T00:00:00Z"},
		{"leap 29", "2024-01-29T00:00:00Z", "2026-06-01T00:00:00Z", "UTC", 29, 1, "2024-02-29T00:00:00Z", "2024-02-29T00:00:00Z", "2024-03-29T00:00:00Z"},
		{"restore 30", "2025-01-30T00:00:00Z", "2026-06-01T00:00:00Z", "UTC", 30, 1, "2025-03-29T00:00:00Z", "2025-02-28T00:00:00Z", "2025-03-30T00:00:00Z"},
		{"custom first partial", "2025-01-15T12:34:56Z", "2026-06-01T00:00:00Z", "UTC", 20, 1, "2025-01-19T00:00:00Z", "2025-01-15T12:34:56Z", "2025-01-20T00:00:00Z"},
		{"last partial", "2025-01-15T00:00:00Z", "2025-03-08T12:00:00Z", "UTC", 15, 1, "2025-03-01T00:00:00Z", "2025-02-15T00:00:00Z", "2025-03-08T12:00:00Z"},
		{"quarter restores anchor", "2025-01-31T00:00:00Z", "2026-06-01T00:00:00Z", "UTC", 31, 3, "2025-05-01T00:00:00Z", "2025-04-30T00:00:00Z", "2025-07-31T00:00:00Z"},
		{"DST local midnight", "2025-02-09T00:00:00-05:00", "2026-01-01T00:00:00Z", "America/New_York", 9, 1, "2025-03-09T07:00:00Z", "2025-03-09T00:00:00-05:00", "2025-04-09T00:00:00-04:00"},
		{"DST skipped midnight", "2018-10-04T00:00:00-03:00", "2019-01-01T00:00:00Z", "America/Sao_Paulo", 4, 1, "2018-11-04T03:00:00Z", "2018-11-04T01:00:00-02:00", "2018-12-04T00:00:00-02:00"},
		{"year rollover", "2025-12-31T00:00:00Z", "2027-01-01T00:00:00Z", "UTC", 31, 1, "2026-01-31T00:00:00Z", "2026-01-31T00:00:00Z", "2026-02-28T00:00:00Z"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := Schedule{StartsAt: instant(t, tt.start), EndsAt: instant(t, tt.end), Timezone: tt.zone, AnchorDay: tt.anchor, PeriodMonths: tt.months}
			got, err := s.PeriodAt(instant(t, tt.at))
			if err != nil {
				t.Fatal(err)
			}
			if !got.StartsAt.Equal(instant(t, tt.wantStart)) || !got.EndsAt.Equal(instant(t, tt.wantEnd)) {
				t.Fatalf("got %s to %s; want %s to %s", got.StartsAt, got.EndsAt, tt.wantStart, tt.wantEnd)
			}
			if got.StartsAt.Location() != time.UTC || got.EndsAt.Location() != time.UTC {
				t.Fatal("expected canonical UTC boundaries")
			}
		})
	}
}

func TestPeriodOutsideMembership(t *testing.T) {
	s := Schedule{StartsAt: instant(t, "2025-01-31T00:00:00Z"), EndsAt: instant(t, "2025-03-02T00:00:00Z"), Timezone: "UTC", AnchorDay: 31, PeriodMonths: 1}
	for _, at := range []time.Time{s.StartsAt.Add(-time.Nanosecond), s.EndsAt, s.EndsAt.Add(time.Hour)} {
		if _, err := s.PeriodAt(at); !errors.Is(err, ErrOutsideMembership) {
			t.Fatalf("%s: %v", at, err)
		}
	}
}

func TestInvalidSchedule(t *testing.T) {
	base := Schedule{StartsAt: instant(t, "2025-01-31T00:00:00Z"), EndsAt: instant(t, "2025-03-02T00:00:00Z"), Timezone: "UTC", AnchorDay: 31, PeriodMonths: 1}
	for _, mutate := range []func(*Schedule){
		func(s *Schedule) { s.AnchorDay = 0 }, func(s *Schedule) { s.AnchorDay = 32 },
		func(s *Schedule) { s.PeriodMonths = 0 }, func(s *Schedule) { s.PeriodMonths = 121 },
		func(s *Schedule) { s.Timezone = "" }, func(s *Schedule) { s.Timezone = "Unknown/Zone" },
		func(s *Schedule) { s.EndsAt = s.StartsAt }, func(s *Schedule) { s.StartsAt = time.Time{} },
	} {
		s := base
		mutate(&s)
		if _, err := s.PeriodAt(base.StartsAt); !errors.Is(err, ErrInvalidSchedule) {
			t.Fatalf("%+v: %v", s, err)
		}
	}
}
