package metering

import (
	"errors"
	"math"
	"math/bits"
)

var ErrInvalid = errors.New("invalid or overflowing metered bytes")

// Charged applies the frozen thousandth multiplier to the cumulative
// effective payload count. Both Agent and control plane use this function so
// report splitting cannot change the charged result.
func Charged(totalRaw, multiplier int64) (int64, error) {
	if totalRaw < 0 || multiplier < 1 || multiplier > 100000 {
		return 0, ErrInvalid
	}
	hi, lo := bits.Mul64(uint64(totalRaw), uint64(multiplier))
	if hi >= 1000 {
		return 0, ErrInvalid
	}
	quotient, _ := bits.Div64(hi, lo, 1000)
	if quotient > math.MaxInt64 {
		return 0, ErrInvalid
	}
	return int64(quotient), nil
}
