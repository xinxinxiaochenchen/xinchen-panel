package billing

import (
	"errors"
	"math"
	"math/bits"
)

var ErrInvalidMeter = errors.New("invalid or overflowing usage counters")

// Counters are cumulative payload bytes at the first ingress, excluding control
// traffic and intermediate relay copies. They never reset within a connection.
type Counters struct {
	UploadedBytes   int64
	DownloadedBytes int64
}

type Delta struct {
	UploadedBytes   int64
	DownloadedBytes int64
	ChargedBytes    int64
}

// ChargeDelta uses a frozen integer multiplier (1000 = 1x). Rounding is applied
// to cumulative totals, so report frequency cannot change charged bytes.
func ChargeDelta(previous, current Counters, multiplier int64) (Delta, error) {
	if current.UploadedBytes < previous.UploadedBytes || current.DownloadedBytes < previous.DownloadedBytes {
		return Delta{}, ErrInvalidMeter
	}
	before, err := charged(previous, multiplier)
	if err != nil {
		return Delta{}, err
	}
	after, err := charged(current, multiplier)
	if err != nil {
		return Delta{}, err
	}
	return Delta{current.UploadedBytes - previous.UploadedBytes, current.DownloadedBytes - previous.DownloadedBytes, after - before}, nil
}

func charged(c Counters, multiplier int64) (int64, error) {
	if c.UploadedBytes < 0 || c.DownloadedBytes < 0 || multiplier < 1 || multiplier > 100000 || c.DownloadedBytes > math.MaxInt64-c.UploadedBytes {
		return 0, ErrInvalidMeter
	}
	raw := uint64(c.UploadedBytes + c.DownloadedBytes)
	hi, lo := bits.Mul64(raw, uint64(multiplier))
	if hi >= 1000 {
		return 0, ErrInvalidMeter
	}
	quotient, _ := bits.Div64(hi, lo, 1000)
	if quotient > math.MaxInt64 {
		return 0, ErrInvalidMeter
	}
	return int64(quotient), nil
}
