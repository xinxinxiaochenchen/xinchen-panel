package agentmeter

import (
	"bytes"
	"errors"
	"io"
	"testing"
	"time"
)

func TestCopyMeteredStopsAtChargedQuotaAndCountsDeliveredBytes(t *testing.T) {
	budget, err := NewBudget(2000, 5, 0, 0, time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	n, err := CopyMetered(&output, bytes.NewBufferString("abcdefghij"), budget, Upload)
	if !errors.Is(err, ErrExhausted) || n != 2 || output.String() != "ab" {
		t.Fatalf("copied=%d output=%q error=%v", n, output.String(), err)
	}
	if state := budget.Snapshot(); state.UploadedBytes != 2 || state.ChargedBytes != 4 {
		t.Fatalf("state=%+v", state)
	}
}

type partialWriter struct {
	data  bytes.Buffer
	calls int
}

type chunkReader struct {
	data []byte
	step int
}

func (r *chunkReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, io.EOF
	}
	limit := len(r.data)
	if r.step == 0 {
		limit = min(limit, 2)
	}
	r.step++
	n := copy(p, r.data[:limit])
	r.data = r.data[n:]
	return n, nil
}

func (w *partialWriter) Write(p []byte) (int, error) {
	w.calls++
	if w.calls == 2 {
		return 1, io.ErrClosedPipe
	}
	return w.data.Write(p[:min(len(p), 2)])
}

func TestCopyMeteredAccountsPartialWrites(t *testing.T) {
	budget, err := NewBudget(1000, 10, 0, 0, time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	w := &partialWriter{}
	n, err := CopyMetered(w, &chunkReader{data: []byte("abcdef")}, budget, Download)
	if !errors.Is(err, io.ErrClosedPipe) || n != 3 {
		t.Fatalf("copied=%d error=%v", n, err)
	}
	if state := budget.Snapshot(); state.DownloadedBytes != 3 || state.ChargedBytes != 3 {
		t.Fatalf("state=%+v", state)
	}
}

func TestCopyMeteredReturnsEOFWithoutChargingExtra(t *testing.T) {
	budget, err := NewBudget(500, 10, 0, 0, time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	n, err := CopyMetered(&output, bytes.NewBufferString("abc"), budget, Upload)
	if err != nil || n != 3 || output.String() != "abc" || budget.Snapshot().ChargedBytes != 1 {
		t.Fatalf("copied=%d output=%q state=%+v error=%v", n, output.String(), budget.Snapshot(), err)
	}
}
