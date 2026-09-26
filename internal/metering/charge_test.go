package metering

import (
	"errors"
	"math"
	"testing"
)

func TestChargedCumulativeHalfRateAndOverflow(t *testing.T) {
	for _, tt := range []struct {
		raw, multiplier, want int64
	}{
		{0, 1000, 0}, {1, 500, 0}, {2, 500, 1},
		{3, 2000, 6}, {math.MaxInt64, 1000, math.MaxInt64},
	} {
		got, err := Charged(tt.raw, tt.multiplier)
		if err != nil || got != tt.want {
			t.Fatalf("charged(%d,%d)=%d,%v want %d", tt.raw, tt.multiplier, got, err, tt.want)
		}
	}
	for _, tt := range [][2]int64{{-1, 1000}, {1, 0}, {1, 100001}, {math.MaxInt64, 2000}} {
		if _, err := Charged(tt[0], tt[1]); !errors.Is(err, ErrInvalid) {
			t.Fatalf("charged(%d,%d)=%v", tt[0], tt[1], err)
		}
	}
}
