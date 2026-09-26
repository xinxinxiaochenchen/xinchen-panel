package agentclient

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"controlplane/internal/agentproto"
)

const outboxConnection = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
const outboxLease = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
const outboxBatch = "cccccccc-cccc-4ccc-8ccc-cccccccccccc"

func sampleUsageBatch() agentproto.UsageBatch {
	return agentproto.UsageBatch{BatchID: outboxBatch, Reports: []agentproto.UsageReport{{ConnectionID: outboxConnection, LeaseID: outboxLease, Sequence: 1, UploadedBytes: 20, DownloadedBytes: 30, ObservedAt: time.Date(2026, 9, 26, 1, 2, 3, 0, time.UTC)}}}
}

func TestFileUsageOutboxSurvivesRestartAndRequiresMatchingAck(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.json")
	store, err := NewFileUsageOutbox(path)
	if err != nil {
		t.Fatal(err)
	}
	batch := sampleUsageBatch()
	if err := store.Enqueue(batch); err != nil {
		t.Fatal(err)
	}
	if err := store.Enqueue(batch); err != nil {
		t.Fatalf("idempotent enqueue: %v", err)
	}
	changed := batch
	changed.Reports = append([]agentproto.UsageReport(nil), batch.Reports...)
	changed.Reports[0].UploadedBytes++
	if err := store.Enqueue(changed); err == nil {
		t.Fatal("accepted batch ID with different reports")
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("unsafe outbox: %+v %v", info, err)
	}
	closeUsageOutbox(t, store)
	restarted, err := NewFileUsageOutbox(path)
	if err != nil {
		t.Fatal(err)
	}
	pending := restarted.Pending()
	if len(pending) != 1 || pending[0].Reports[0].UploadedBytes != 20 {
		t.Fatalf("lost pending report: %+v", pending)
	}
	pending[0].Reports[0].UploadedBytes = 999
	if got := restarted.Pending()[0].Reports[0].UploadedBytes; got != 20 {
		t.Fatalf("mutable pending report: %d", got)
	}
	digest, err := agentproto.UsageBatchDigest(batch)
	if err != nil {
		t.Fatal(err)
	}
	if err := restarted.Acknowledge(agentproto.UsageAck{BatchID: batch.BatchID, SHA256: strings.Repeat("0", 64)}); err == nil {
		t.Fatal("deleted unacknowledged report")
	}
	if len(restarted.Pending()) != 1 {
		t.Fatal("mismatched ACK removed report")
	}
	if err := restarted.Acknowledge(agentproto.UsageAck{BatchID: batch.BatchID, SHA256: digest}); err != nil {
		t.Fatal(err)
	}
	closeUsageOutbox(t, restarted)
	again, err := NewFileUsageOutbox(path)
	if err != nil || len(again.Pending()) != 0 {
		t.Fatalf("ACK was not durable: %+v %v", again, err)
	}
	defer closeUsageOutbox(t, again)
}

func TestFileUsageOutboxRejectsUnsafeOrCorruptState(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "usage.json")
	if err := os.WriteFile(path, []byte(`{"batches":[{}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewFileUsageOutbox(path); err == nil {
		t.Fatal("accepted corrupt usage")
	}
	body, _ := json.Marshal(struct {
		Batches []agentproto.UsageBatch `json:"batches"`
	}{[]agentproto.UsageBatch{sampleUsageBatch()}})
	if err := os.WriteFile(path, body, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := NewFileUsageOutbox(path); err == nil {
		t.Fatal("accepted world-readable usage")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "target"), path); err != nil {
		t.Fatal(err)
	}
	if _, err := NewFileUsageOutbox(path); err == nil {
		t.Fatal("accepted symlink")
	}
	if _, err := NewFileUsageOutbox("relative.json"); err == nil {
		t.Fatal("accepted relative path")
	}
}

func TestFileUsageOutboxCapacityFailsClosed(t *testing.T) {
	store, err := NewFileUsageOutbox(filepath.Join(t.TempDir(), "usage.json"))
	if err != nil {
		t.Fatal(err)
	}
	defer closeUsageOutbox(t, store)
	batch := sampleUsageBatch()
	for i := 0; i < MaxPendingUsageBatches; i++ {
		batch.BatchID = fmtUUID(i)
		if err := store.Enqueue(batch); err != nil {
			t.Fatalf("batch %d: %v", i, err)
		}
	}
	batch.BatchID = fmtUUID(MaxPendingUsageBatches)
	if err := store.Enqueue(batch); !errors.Is(err, ErrUsageOutboxFull) {
		t.Fatalf("capacity error = %v", err)
	}
	if len(store.Pending()) != MaxPendingUsageBatches {
		t.Fatal("queue changed when full")
	}
}

func fmtUUID(i int) string {
	return "cccccccc-cccc-4ccc-8ccc-" + fmt.Sprintf("%012x", i)
}

func closeUsageOutbox(t *testing.T, store *FileUsageOutbox) {
	t.Helper()
	closer, ok := any(store).(io.Closer)
	if !ok {
		t.Fatal("outbox cannot release its file lock")
	}
	if err := closer.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestFileUsageOutboxStrictRoot(t *testing.T) {
	for _, body := range []string{`{}`, `null`, `{"batches":null}`, `{"batches":[],"batches":[]}`, `{"batches":[]} garbage`, `{"batches":[]} {}`, `{"Batches":[]}`, `{"batches":[],"unknown":0}`} {
		t.Run(body, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "usage.json")
			if err := os.WriteFile(path, []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
			if store, err := NewFileUsageOutbox(path); err == nil {
				if closer, ok := any(store).(io.Closer); ok {
					closer.Close()
				}
				t.Fatal("accepted malformed root")
			}
		})
	}
}

func TestFileUsageOutboxExclusiveAndClose(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.json")
	store, err := NewFileUsageOutbox(path)
	if err != nil {
		t.Fatal(err)
	}
	if second, err := NewFileUsageOutbox(path); err == nil {
		if closer, ok := any(second).(io.Closer); ok {
			closer.Close()
		}
		t.Fatal("opened outbox twice")
	}
	closeUsageOutbox(t, store)
	closeUsageOutbox(t, store)
	if err := store.Enqueue(sampleUsageBatch()); err == nil {
		t.Fatal("write succeeded after close")
	}
	digest, _ := agentproto.UsageBatchDigest(sampleUsageBatch())
	if err := store.Acknowledge(agentproto.UsageAck{BatchID: outboxBatch, SHA256: digest}); err == nil {
		t.Fatal("ACK succeeded after close")
	}
	reopened, err := NewFileUsageOutbox(path)
	if err != nil {
		t.Fatal(err)
	}
	closeUsageOutbox(t, reopened)
}

func TestFileUsageOutboxLockOtherProcess(t *testing.T) {
	if path := os.Getenv("CONTROL_OUTBOX_LOCK_TEST_PATH"); path != "" {
		if store, err := NewFileUsageOutbox(path); err == nil {
			if closer, ok := any(store).(io.Closer); ok {
				closer.Close()
			}
			t.Fatal("child acquired parent lock")
		}
		return
	}
	path := filepath.Join(t.TempDir(), "usage.json")
	store, err := NewFileUsageOutbox(path)
	if err != nil {
		t.Fatal(err)
	}
	defer closeUsageOutbox(t, store)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(executable, "-test.run=^TestFileUsageOutboxLockOtherProcess$")
	command.Env = append(os.Environ(), "CONTROL_OUTBOX_LOCK_TEST_PATH="+path)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("cross-process exclusion: %v %s", err, output)
	}
}

func TestFileUsageOutboxConcurrentEnqueue(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.json")
	store, err := NewFileUsageOutbox(path)
	if err != nil {
		t.Fatal(err)
	}
	var workers sync.WaitGroup
	for i := 0; i < 24; i++ {
		workers.Add(1)
		go func(i int) {
			defer workers.Done()
			batch := sampleUsageBatch()
			batch.BatchID = fmtUUID(i)
			if err := store.Enqueue(batch); err != nil {
				t.Error(err)
			}
		}(i)
	}
	workers.Wait()
	if len(store.Pending()) != 24 {
		t.Fatal("lost concurrent batches")
	}
	closeUsageOutbox(t, store)
	reopened, err := NewFileUsageOutbox(path)
	if err != nil {
		t.Fatal(err)
	}
	defer closeUsageOutbox(t, reopened)
	if len(reopened.Pending()) != 24 {
		t.Fatal("concurrent batches not durable")
	}
}

func TestFileUsageOutboxRenameFailureRetainsReports(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "usage.json")
	store, err := NewFileUsageOutbox(path)
	if err != nil {
		t.Fatal(err)
	}
	defer closeUsageOutbox(t, store)
	batch := sampleUsageBatch()
	if err := store.Enqueue(batch); err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(dir, "saved.json")
	if err := os.Rename(path, backup); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	digest, _ := agentproto.UsageBatchDigest(batch)
	if err := store.Acknowledge(agentproto.UsageAck{BatchID: batch.BatchID, SHA256: digest}); err == nil {
		t.Fatal("ACK accepted failed rename")
	}
	if len(store.Pending()) != 1 {
		t.Fatal("failed persistence dropped pending report")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(backup, path); err != nil {
		t.Fatal(err)
	}
	closeUsageOutbox(t, store)
	reopened, err := NewFileUsageOutbox(path)
	if err != nil {
		t.Fatal(err)
	}
	defer closeUsageOutbox(t, reopened)
	if len(reopened.Pending()) != 1 {
		t.Fatal("disk lost report after failed persistence")
	}
}

func TestFileUsageOutboxDirectorySyncFailureStopsWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.json")
	store, err := NewFileUsageOutbox(path)
	if err != nil {
		t.Fatal(err)
	}
	defer closeUsageOutbox(t, store)
	failure := errors.New("directory sync failed")
	store.syncDirectory = func(string) error { return failure }
	batch := sampleUsageBatch()
	if err := store.Enqueue(batch); !errors.Is(err, failure) {
		t.Fatalf("sync error = %v", err)
	}
	if len(store.Pending()) != 1 {
		t.Fatal("renamed report missing from in-memory recovery state")
	}
	store.syncDirectory = syncUsageOutboxDirectory
	second := sampleUsageBatch()
	second.BatchID = fmtUUID(44)
	if err := store.Enqueue(second); err == nil {
		t.Fatal("continued writing after uncertain persistence")
	}
	digest, _ := agentproto.UsageBatchDigest(batch)
	if err := store.Acknowledge(agentproto.UsageAck{BatchID: batch.BatchID, SHA256: digest}); err == nil {
		t.Fatal("continued ACK after uncertain persistence")
	}
	closeUsageOutbox(t, store)
	reopened, err := NewFileUsageOutbox(path)
	if err != nil {
		t.Fatal(err)
	}
	defer closeUsageOutbox(t, reopened)
	if len(reopened.Pending()) != 1 {
		t.Fatal("lost renamed report during recovery")
	}
}

func TestFileUsageOutboxRejectsUnsafeLockAndOversize(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.json")
	if err := os.Symlink(filepath.Join(t.TempDir(), "target"), path+".lock"); err != nil {
		t.Fatal(err)
	}
	if _, err := NewFileUsageOutbox(path); err == nil {
		t.Fatal("accepted symlink lock")
	}
	if err := os.Remove(path + ".lock"); err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(maxUsageOutboxFileBytes + 1); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := NewFileUsageOutbox(path); err == nil {
		t.Fatal("accepted oversized state")
	}
	if err := os.WriteFile(path, []byte(`{"batches":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	store, err := NewFileUsageOutbox(path)
	if err != nil {
		t.Fatalf("failed open leaked its lock: %v", err)
	}
	closeUsageOutbox(t, store)
}

func TestFileUsageOutboxRejectsFIFOWithoutBlocking(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.json")
	if err := syscall.Mkfifo(path, 0600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		store, err := NewFileUsageOutbox(path)
		if err == nil {
			store.Close()
		}
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("accepted FIFO")
		}
	case <-time.After(200 * time.Millisecond):
		fd, err := syscall.Open(path, syscall.O_RDWR|syscall.O_NONBLOCK, 0)
		if err != nil {
			t.Fatal(err)
		}
		<-done
		syscall.Close(fd)
		t.Fatal("opening non-regular outbox blocked")
	}
}
