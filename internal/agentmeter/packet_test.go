package agentmeter

import (
	"bytes"
	"errors"
	"testing"
	"time"
)

func TestWritePacketDropsWholeDatagramWhenQuotaIsShort(t *testing.T) {
	budget, err := NewBudget(1000, 4, 0, 0, time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if n, err := WritePacket(&output, []byte("abc"), budget, Upload); err != nil || n != 3 {
		t.Fatalf("first packet=%d %v", n, err)
	}
	if n, err := WritePacket(&output, []byte("xy"), budget, Download); !errors.Is(err, ErrExhausted) || n != 0 {
		t.Fatalf("partial packet sent=%d %v", n, err)
	}
	if output.String() != "abc" || budget.Snapshot().DownloadedBytes != 0 {
		t.Fatalf("partial packet output=%q state=%+v", output.String(), budget.Snapshot())
	}
}
