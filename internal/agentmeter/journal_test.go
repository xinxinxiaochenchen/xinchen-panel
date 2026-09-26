package agentmeter

import (
	"bytes"
	"errors"
	"io"
	"testing"
	"time"
)

type journalRecorder struct {
	before func(Direction, int64) error
	after  func(Direction, int64, int64) error
}

func (j journalRecorder) BeforeWrite(direction Direction, planned int64) error {
	return j.before(direction, planned)
}
func (j journalRecorder) AfterWrite(direction Direction, planned, actual int64) error {
	return j.after(direction, planned, actual)
}

func TestCopyMeteredWithJournalPersistsIntentBeforeWriting(t *testing.T) {
	budget, err := NewBudget(1000, 8, 0, 0, time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	var written bytes.Buffer
	var before, after bool
	journal := journalRecorder{
		before: func(direction Direction, planned int64) error {
			if direction != Upload || planned != 5 || written.Len() != 0 {
				t.Fatalf("before = %d %d", direction, planned)
			}
			before = true
			return nil
		},
		after: func(direction Direction, planned, actual int64) error {
			if !before || direction != Upload || planned != 5 || actual != 5 || written.String() != "hello" {
				t.Fatalf("after = %d %d/%d", direction, planned, actual)
			}
			after = true
			return nil
		},
	}
	n, err := CopyMeteredWithJournal(&written, bytes.NewBufferString("hello"), budget, Upload, journal)
	if err != nil || n != 5 || !after {
		t.Fatalf("copy = %d %v before=%v after=%v", n, err, before, after)
	}
}

func TestJournalFailurePreventsTransferAndReleasesReservation(t *testing.T) {
	budget, err := NewBudget(1000, 4, 0, 0, time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	denied := errors.New("durable journal unavailable")
	journal := journalRecorder{before: func(Direction, int64) error { return denied }, after: func(Direction, int64, int64) error { t.Fatal("after called"); return nil }}
	var written bytes.Buffer
	n, err := CopyMeteredWithJournal(&written, bytes.NewBufferString("abcd"), budget, Upload, journal)
	if !errors.Is(err, denied) || n != 0 || written.Len() != 0 {
		t.Fatalf("copy = %d %v %q", n, err, written.String())
	}
	if reservation, err := budget.Reserve(Upload, 4); err != nil || reservation.Bytes != 4 {
		t.Fatalf("reservation leaked: %+v %v", reservation, err)
	}
}

func TestPacketJournalNeverSendsPartialDatagram(t *testing.T) {
	budget, err := NewBudget(1000, 3, 0, 0, time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	called := false
	journal := journalRecorder{before: func(Direction, int64) error { called = true; return nil }, after: func(Direction, int64, int64) error { called = true; return nil }}
	var written bytes.Buffer
	n, err := WritePacketWithJournal(&written, []byte("abcd"), budget, Upload, journal)
	if !errors.Is(err, ErrExhausted) || n != 0 || called || written.Len() != 0 {
		t.Fatalf("packet = %d %v called=%v", n, err, called)
	}
	budget, err = NewBudget(1000, 4, 0, 0, time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	journal.before = func(direction Direction, planned int64) error {
		if planned != 4 || written.Len() != 0 {
			t.Fatal("intent after packet")
		}
		return nil
	}
	journal.after = func(direction Direction, planned, actual int64) error {
		if planned != 4 || actual != 4 || written.String() != "abcd" {
			t.Fatal("actual mismatch")
		}
		return nil
	}
	n, err = WritePacketWithJournal(&written, []byte("abcd"), budget, Upload, journal)
	if err != nil || n != 4 {
		t.Fatalf("packet = %d %v", n, err)
	}
}

func TestJournalAfterFailureReturnsErrorAfterTransferredBytes(t *testing.T) {
	budget, err := NewBudget(1000, 4, 0, 0, time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	failed := errors.New("checkpoint failed")
	journal := journalRecorder{before: func(Direction, int64) error { return nil }, after: func(Direction, int64, int64) error { return failed }}
	n, err := CopyMeteredWithJournal(io.Discard, bytes.NewBufferString("abcd"), budget, Upload, journal)
	if n != 4 || !errors.Is(err, failed) || budget.Snapshot().UploadedBytes != 4 {
		t.Fatalf("copy = %d %v %+v", n, err, budget.Snapshot())
	}
}
