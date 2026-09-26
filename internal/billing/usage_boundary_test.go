package billing

import (
	"testing"
	"time"
)

func TestUsageObservationAllowsFinalSnapshotAtPeriodEnd(t *testing.T) {
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(30 * 24 * time.Hour)
	if !validUsageObservation(start, end, start) {
		t.Fatal("rejected first observation")
	}
	if !validUsageObservation(start, end, end) {
		t.Fatal("rejected final boundary snapshot")
	}
	if validUsageObservation(start, end, end.Add(time.Microsecond)) {
		t.Fatal("accepted traffic after period")
	}
	if validUsageObservation(start, end, start.Add(-time.Microsecond)) {
		t.Fatal("accepted traffic before connection")
	}
}
