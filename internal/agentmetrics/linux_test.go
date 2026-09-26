package agentmetrics

import (
	"errors"
	"testing"
)

func TestCollectorSamplesCPUAvailableMemoryAndNetworkBytes(t *testing.T) {
	stat := []byte("cpu  100 0 100 800 0 0 0 0\n")
	files := map[string][]byte{
		"/proc/stat":    stat,
		"/proc/meminfo": []byte("MemTotal: 2048 kB\nMemAvailable: 512 kB\n"),
		"/proc/net/dev": []byte("Inter-| Receive | Transmit\nface |bytes packets errs drop fifo frame compressed multicast|bytes packets errs drop fifo colls carrier compressed\n  lo: 9 0 0 0 0 0 0 0 8 0 0 0 0 0 0 0\neth0: 100 0 0 0 0 0 0 0 200 0 0 0 0 0 0 0\n"),
	}
	collector := NewCollector(func(path string) ([]byte, error) {
		value, ok := files[path]
		if !ok {
			return nil, errors.New("missing")
		}
		return value, nil
	})
	first, err := collector.Sample()
	if err != nil || first.CPUPct != 0 || first.MemoryUsedBytes != 1536*1024 || first.RXBytes != 100 || first.TXBytes != 200 {
		t.Fatalf("first sample = %+v, %v", first, err)
	}
	files["/proc/stat"] = []byte("cpu  150 0 150 900 0 0 0 0\n")
	second, err := collector.Sample()
	if err != nil || second.CPUPct != 50 {
		t.Fatalf("second CPU sample = %+v, %v", second, err)
	}
	files["/proc/meminfo"] = []byte("MemTotal: 10 kB\n")
	if _, err := collector.Sample(); err == nil {
		t.Fatal("incomplete /proc/meminfo accepted")
	}
}
