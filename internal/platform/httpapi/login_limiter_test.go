package httpapi

import (
	"fmt"
	"testing"
	"time"
)

func TestLoginLimiterGlobalAndWindowReset(t *testing.T) {
	limiter := newLoginLimiter()
	now := time.Date(2026, 9, 26, 0, 0, 0, 0, time.UTC)
	limiter.now = func() time.Time { return now }
	for i := 0; i < 60; i++ {
		if wait := limiter.Allow(fmt.Sprintf("user-%d@example.com", i)); wait != 0 {
			t.Fatalf("request %d unexpectedly limited: %s", i, wait)
		}
	}
	if wait := limiter.Allow("another@example.com"); wait <= 0 {
		t.Fatal("global limit did not engage")
	}
	now = now.Add(time.Minute)
	if wait := limiter.Allow("another@example.com"); wait != 0 {
		t.Fatalf("global window did not reset: %s", wait)
	}
}
