package billing

import (
	"errors"
	"math"
	"testing"
)

func TestChargeDeltaCumulativeRounding(t *testing.T) {
	prev := Counters{}
	var sum int64
	for i := int64(1); i <= 5; i++ {
		next := Counters{UploadedBytes: i, DownloadedBytes: i}
		delta, err := ChargeDelta(prev, next, 500)
		if err != nil {
			t.Fatal(err)
		}
		if delta.UploadedBytes != 1 || delta.DownloadedBytes != 1 || delta.ChargedBytes != 1 {
			t.Fatalf("report %d: %+v", i, delta)
		}
		sum += delta.ChargedBytes
		prev = next
	}
	if sum != 5 {
		t.Fatalf("charged %d, want 5", sum)
	}
}

func TestChargeDeltaDoesNotRoundEachReport(t *testing.T) {
	first, err := ChargeDelta(Counters{}, Counters{UploadedBytes: 1}, 500)
	if err != nil || first.ChargedBytes != 0 {
		t.Fatalf("first: %+v %v", first, err)
	}
	second, err := ChargeDelta(Counters{UploadedBytes: 1}, Counters{UploadedBytes: 2}, 500)
	if err != nil || second.ChargedBytes != 1 {
		t.Fatalf("second: %+v %v", second, err)
	}
}

func TestChargeDeltaMultiplierAndDirections(t *testing.T) {
	for _, m := range []int64{500, 1000, 2000} {
		delta, err := ChargeDelta(Counters{UploadedBytes: 4, DownloadedBytes: 6}, Counters{UploadedBytes: 14, DownloadedBytes: 26}, m)
		if err != nil {
			t.Fatal(err)
		}
		if delta.UploadedBytes != 10 || delta.DownloadedBytes != 20 || delta.ChargedBytes != 30*m/1000 {
			t.Fatalf("%d: %+v", m, delta)
		}
	}
}

func TestChargeDeltaRejectsInvalidOrOverflow(t *testing.T) {
	cases := []struct {
		prev, next Counters
		multiplier int64
	}{
		{Counters{}, Counters{UploadedBytes: -1}, 1000},
		{Counters{UploadedBytes: 2}, Counters{UploadedBytes: 1}, 1000},
		{Counters{DownloadedBytes: 2}, Counters{DownloadedBytes: 1}, 1000},
		{Counters{}, Counters{UploadedBytes: 1}, 0},
		{Counters{}, Counters{UploadedBytes: 1}, 100001},
		{Counters{}, Counters{UploadedBytes: math.MaxInt64, DownloadedBytes: 1}, 1000},
		{Counters{}, Counters{UploadedBytes: math.MaxInt64}, 2000},
	}
	for _, tt := range cases {
		if _, err := ChargeDelta(tt.prev, tt.next, tt.multiplier); !errors.Is(err, ErrInvalidMeter) {
			t.Fatalf("%+v: %v", tt, err)
		}
	}
}

func TestChargeDeltaAllowsLargeNonoverflowingTotals(t *testing.T) {
	delta, err := ChargeDelta(Counters{}, Counters{UploadedBytes: math.MaxInt64}, 1000)
	if err != nil || delta.ChargedBytes != math.MaxInt64 {
		t.Fatalf("intermediate multiplication overflow: %+v %v", delta, err)
	}
	delta, err = ChargeDelta(Counters{UploadedBytes: math.MaxInt64 - 1}, Counters{UploadedBytes: math.MaxInt64}, 500)
	if err != nil || delta.ChargedBytes != 0 {
		t.Fatalf("large final rounding: %+v %v", delta, err)
	}
}
