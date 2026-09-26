package agentmeter

import (
	"errors"
	"math"
	"sync"
	"time"

	"controlplane/internal/metering"
)

var ErrExhausted = errors.New("Agent quota lease exhausted")
var ErrExpired = errors.New("Agent quota lease expired")
var ErrInvalidReservation = errors.New("invalid Agent quota reservation")

type Direction uint8

const (
	Upload Direction = iota + 1
	Download
)

type Reservation struct {
	ID    uint64
	Bytes int64
}

type State struct {
	UploadedBytes   int64
	DownloadedBytes int64
	ChargedBytes    int64
}

type pendingReservation struct {
	direction Direction
	bytes     int64
}

// Budget reserves charged bytes across both directions before each write.
// Commit records only the bytes actually written and releases any unused part.
// It is deliberately limited to a single lease; a new lease starts with the
// connection's cumulative counters so fractional multipliers keep their carry.
type Budget struct {
	mu                sync.Mutex
	multiplier        int64
	granted           int64
	baseCharged       int64
	uploaded          int64
	downloaded        int64
	pendingUploaded   int64
	pendingDownloaded int64
	pending           map[uint64]pendingReservation
	nextID            uint64
	expiresAt         time.Time
	clock             func() time.Time
}

func NewBudget(multiplier, granted, uploaded, downloaded int64, expiresAt time.Time) (*Budget, error) {
	if multiplier < 1 || multiplier > 100000 || granted < 1 || granted > 1<<20 || uploaded < 0 || downloaded < 0 || expiresAt.IsZero() {
		return nil, ErrInvalidReservation
	}
	base, err := charge(uploaded, downloaded, multiplier)
	if err != nil {
		return nil, err
	}
	return &Budget{multiplier: multiplier, granted: granted, baseCharged: base,
		uploaded: uploaded, downloaded: downloaded, expiresAt: expiresAt,
		pending: make(map[uint64]pendingReservation), clock: time.Now}, nil
}

func charge(uploaded, downloaded, multiplier int64) (int64, error) {
	if uploaded < 0 || downloaded < 0 || downloaded > math.MaxInt64-uploaded {
		return 0, metering.ErrInvalid
	}
	return metering.Charged(uploaded+downloaded, multiplier)
}

func (b *Budget) Reserve(direction Direction, requested int64) (Reservation, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if direction != Upload && direction != Download || requested < 1 {
		return Reservation{}, ErrInvalidReservation
	}
	if !b.clock().Before(b.expiresAt) {
		return Reservation{}, ErrExpired
	}
	canFit := func(n int64) bool {
		up, down := b.uploaded+b.pendingUploaded, b.downloaded+b.pendingDownloaded
		if up < 0 || down < 0 {
			return false
		}
		if direction == Upload {
			if n > math.MaxInt64-up {
				return false
			}
			up += n
		} else {
			if n > math.MaxInt64-down {
				return false
			}
			down += n
		}
		charged, err := charge(up, down, b.multiplier)
		return err == nil && charged-b.baseCharged <= b.granted
	}
	if !canFit(1) {
		return Reservation{}, ErrExhausted
	}
	lo, hi := int64(1), requested
	for lo < hi {
		mid := lo + (hi-lo+1)/2
		if canFit(mid) {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	b.nextID++
	b.pending[b.nextID] = pendingReservation{direction: direction, bytes: lo}
	if direction == Upload {
		b.pendingUploaded += lo
	} else {
		b.pendingDownloaded += lo
	}
	return Reservation{ID: b.nextID, Bytes: lo}, nil
}

func (b *Budget) Commit(reservation Reservation, actual int64) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	pending, exists := b.pending[reservation.ID]
	if !exists || pending.bytes != reservation.Bytes || actual < 0 || actual > pending.bytes {
		return ErrInvalidReservation
	}
	delete(b.pending, reservation.ID)
	if pending.direction == Upload {
		b.pendingUploaded -= pending.bytes
		b.uploaded += actual
	} else {
		b.pendingDownloaded -= pending.bytes
		b.downloaded += actual
	}
	return nil
}

func (b *Budget) Snapshot() State {
	b.mu.Lock()
	defer b.mu.Unlock()
	charged, _ := charge(b.uploaded, b.downloaded, b.multiplier)
	return State{UploadedBytes: b.uploaded, DownloadedBytes: b.downloaded, ChargedBytes: charged}
}

// LeaseConsumed is the charged delta attributable to this lease, preserving
// the cumulative rounding carry from previous leases on the connection.
func (b *Budget) LeaseConsumed() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	charged, _ := charge(b.uploaded, b.downloaded, b.multiplier)
	return charged - b.baseCharged
}
