package agentmeter

import (
	"errors"
	"sync"
	"testing"
	"time"
)

func TestBudgetCapsConcurrentDirectionsByChargedBytes(t *testing.T) {
	budget, err := NewBudget(2000, 5, 0, 0, time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	first, err := budget.Reserve(Upload, 10)
	if err != nil || first.Bytes != 2 {
		t.Fatalf("upload reservation=%+v %v", first, err)
	}
	if _, err := budget.Reserve(Download, 10); !errors.Is(err, ErrExhausted) {
		t.Fatalf("download overspent pending upload=%v", err)
	}
	if err := budget.Commit(first, 1); err != nil {
		t.Fatal(err)
	}
	second, err := budget.Reserve(Download, 10)
	if err != nil || second.Bytes != 1 {
		t.Fatalf("download reservation=%+v %v", second, err)
	}
	if err := budget.Commit(second, 1); err != nil {
		t.Fatal(err)
	}
	state := budget.Snapshot()
	if state.UploadedBytes != 1 || state.DownloadedBytes != 1 || state.ChargedBytes != 4 {
		t.Fatalf("metered state=%+v", state)
	}
}

func TestBudgetUsesCumulativeRoundingAcrossLeaseBoundary(t *testing.T) {
	budget, err := NewBudget(500, 1, 1, 0, time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	reservation, err := budget.Reserve(Download, 10)
	if err != nil || reservation.Bytes != 2 {
		t.Fatalf("fractional reservation=%+v %v", reservation, err)
	}
	if err := budget.Commit(reservation, 2); err != nil {
		t.Fatal(err)
	}
	state := budget.Snapshot()
	if state.UploadedBytes != 1 || state.DownloadedBytes != 2 || state.ChargedBytes != 1 {
		t.Fatalf("rounded state=%+v", state)
	}
	if _, err := budget.Reserve(Upload, 1); !errors.Is(err, ErrExhausted) {
		t.Fatalf("quota exceeded=%v", err)
	}
}

func TestBudgetAbortAndExpiryKeepActualBytes(t *testing.T) {
	now := time.Now()
	budget, err := NewBudget(1000, 10, 0, 0, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	budget.clock = func() time.Time { return now }
	first, err := budget.Reserve(Upload, 10)
	if err != nil {
		t.Fatal(err)
	}
	if err := budget.Commit(first, 3); err != nil {
		t.Fatal(err)
	}
	second, err := budget.Reserve(Download, 10)
	if err != nil || second.Bytes != 7 {
		t.Fatalf("released unused bytes=%+v %v", second, err)
	}
	budget.clock = func() time.Time { return now.Add(2 * time.Second) }
	if _, err := budget.Reserve(Upload, 1); !errors.Is(err, ErrExpired) {
		t.Fatalf("expired reservation=%v", err)
	}
	if err := budget.Commit(second, 2); err != nil {
		t.Fatal(err)
	}
	state := budget.Snapshot()
	if state.UploadedBytes != 3 || state.DownloadedBytes != 2 || state.ChargedBytes != 5 {
		t.Fatalf("actual bytes after expiry=%+v", state)
	}
	if err := budget.Commit(second, 2); !errors.Is(err, ErrInvalidReservation) {
		t.Fatalf("duplicate commit=%v", err)
	}
}

func TestBudgetConcurrentReservationsNeverOvercommit(t *testing.T) {
	budget, err := NewBudget(1000, 1000, 0, 0, time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	reservations := make(chan Reservation, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if r, err := budget.Reserve(Upload, 1000); err == nil {
				reservations <- r
			}
		}()
	}
	wg.Wait()
	close(reservations)
	var total int64
	for reservation := range reservations {
		total += reservation.Bytes
		if err := budget.Commit(reservation, reservation.Bytes); err != nil {
			t.Fatal(err)
		}
	}
	if total != 1000 || budget.Snapshot().ChargedBytes != 1000 {
		t.Fatalf("reserved=%d state=%+v", total, budget.Snapshot())
	}
}

func TestBudgetRejectsOversizedGrantAndReturnsLeaseConsumption(t *testing.T) {
	if _, err := NewBudget(1000, 1<<20+1, 0, 0, time.Now().Add(time.Minute)); !errors.Is(err, ErrInvalidReservation) {
		t.Fatalf("oversized lease accepted: %v", err)
	}
	budget, err := NewBudget(500, 1, 2, 0, time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	reservation, err := budget.Reserve(Download, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := budget.Commit(reservation, 1); err != nil {
		t.Fatal(err)
	}
	if budget.LeaseConsumed() != 0 {
		t.Fatalf("fractional carry incorrectly charged: %d", budget.LeaseConsumed())
	}
}
