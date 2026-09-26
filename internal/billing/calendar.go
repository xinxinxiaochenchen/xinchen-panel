package billing

import (
	"errors"
	"time"
	_ "time/tzdata"
)

var (
	ErrInvalidSchedule   = errors.New("invalid billing schedule")
	ErrOutsideMembership = errors.New("outside membership")
)

// Schedule freezes the billing calendar for one membership. The original anchor
// day is retained even when a shorter month clamps one boundary to month end.
type Schedule struct {
	StartsAt     time.Time
	EndsAt       time.Time
	Timezone     string
	AnchorDay    int
	PeriodMonths int
}

type Window struct {
	StartsAt time.Time
	EndsAt   time.Time
}

func (s Schedule) PeriodAt(at time.Time) (Window, error) {
	if s.StartsAt.IsZero() || !s.EndsAt.After(s.StartsAt) || s.AnchorDay < 1 || s.AnchorDay > 31 || s.PeriodMonths < 1 || s.PeriodMonths > 120 || s.Timezone == "" {
		return Window{}, ErrInvalidSchedule
	}
	loc, err := time.LoadLocation(s.Timezone)
	if err != nil {
		return Window{}, ErrInvalidSchedule
	}
	if at.Before(s.StartsAt) || !at.Before(s.EndsAt) {
		return Window{}, ErrOutsideMembership
	}
	localStart := s.StartsAt.In(loc)
	y, m, _ := localStart.Date()
	first := anchorBoundary(y, m, s.AnchorDay, loc)
	if !first.After(s.StartsAt) {
		first = anchorBoundary(y, m+time.Month(s.PeriodMonths), s.AnchorDay, loc)
	}
	if at.Before(first) {
		return Window{s.StartsAt.UTC(), earlier(first, s.EndsAt).UTC()}, nil
	}
	firstLocal := first.In(loc)
	fy, fm, _ := firstLocal.Date()
	ay, am, _ := at.In(loc).Date()
	monthDiff := (ay-fy)*12 + int(am-fm)
	step := monthDiff / s.PeriodMonths
	if step < 0 {
		step = 0
	}
	boundary := func(index int) time.Time {
		return anchorBoundary(fy, fm+time.Month(index*s.PeriodMonths), s.AnchorDay, loc)
	}
	for step > 0 && boundary(step).After(at) {
		step--
	}
	for !boundary(step + 1).After(at) {
		step++
	}
	return Window{boundary(step).UTC(), earlier(boundary(step+1), s.EndsAt).UTC()}, nil
}

func earlier(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

func anchorBoundary(year int, month time.Month, day int, loc *time.Location) time.Time {
	first := time.Date(year, month, 1, 12, 0, 0, 0, loc)
	y, m, _ := first.Date()
	last := time.Date(y, m+1, 0, 12, 0, 0, 0, loc).Day()
	if day > last {
		day = last
	}
	candidate := time.Date(y, m, day, 0, 0, 0, 0, loc)
	// time.Date can resolve a skipped local midnight to the preceding day.
	// Move forward to the first instant on the requested civil date.
	for i := 0; i < 24*60; i++ {
		cy, cm, cd := candidate.In(loc).Date()
		if cy > y || (cy == y && (cm > m || (cm == m && cd >= day))) {
			break
		}
		candidate = candidate.Add(time.Minute)
	}
	return candidate
}
