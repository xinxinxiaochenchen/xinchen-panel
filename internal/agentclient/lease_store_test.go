package agentclient

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testLeaseState() LeaseState {
	issued := time.Date(2026, 9, 26, 1, 0, 0, 0, time.UTC)
	return LeaseState{
		ConnectionID:    "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
		LeaseID:         "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb",
		ResourceKind:    "forward",
		ResourceID:      "cccccccc-cccc-4ccc-8ccc-cccccccccccc",
		Revision:        3,
		PeriodID:        "dddddddd-dddd-4ddd-8ddd-dddddddddddd",
		MultiplierMilli: 1500,
		GrantedBytes:    4096,
		IssuedAt:        issued,
		ExpiresAt:       issued.Add(30 * time.Second),
		PeriodEndsAt:    issued.Add(time.Hour),
	}
}

func TestFileLeaseStoreAcceptsLeaseEndingAtPeriodBoundary(t *testing.T) {
	store, err := NewFileLeaseStore(filepath.Join(t.TempDir(), "leases.json"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	lease := testLeaseState()
	lease.PeriodEndsAt = lease.ExpiresAt
	if err := store.Put(lease); err != nil {
		t.Fatalf("valid boundary rejected: %v", err)
	}
}

func TestFileLeaseStoreRejectsCapacityOverflow(t *testing.T) {
	store, err := NewFileLeaseStore(filepath.Join(t.TempDir(), "leases.json"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for i := 0; i < 1024; i++ {
		lease := testLeaseState()
		lease.LeaseID = fmt.Sprintf("bbbbbbbb-bbbb-4bbb-8bbb-%012x", i)
		store.leases[leaseKey(lease.ConnectionID, lease.LeaseID)] = lease
	}
	lease := testLeaseState()
	lease.LeaseID = "ffffffff-ffff-4fff-8fff-ffffffffffff"
	if err := store.Put(lease); err == nil {
		t.Fatal("accepted state that cannot be reopened")
	}
}

func TestFileLeaseStoreSurvivesRestartAndRejectsRollback(t *testing.T) {
	path := filepath.Join(t.TempDir(), "leases.json")
	store, err := NewFileLeaseStore(path)
	if err != nil {
		t.Fatal(err)
	}
	lease := testLeaseState()
	if err := store.Put(lease); err != nil {
		t.Fatal(err)
	}
	lease.UploadedBytes, lease.DownloadedBytes, lease.ConsumedBytes, lease.Sequence = 20, 30, 75, 1
	if err := store.Put(lease); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("unsafe state: %v %v", info, err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewFileLeaseStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	pending := reopened.Pending()
	if len(pending) != 1 || pending[0].UploadedBytes != 20 || pending[0].Sequence != 1 {
		t.Fatalf("lost lease: %+v", pending)
	}
	pending[0].UploadedBytes = 900
	if reopened.Pending()[0].UploadedBytes != 20 {
		t.Fatal("pending aliases store")
	}
	rollback := lease
	rollback.Sequence = 0
	if err := reopened.Put(rollback); err == nil {
		t.Fatal("accepted sequence rollback")
	}
	changed := lease
	changed.ResourceID = "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee"
	if err := reopened.Put(changed); err == nil {
		t.Fatal("accepted lease identity change")
	}
	if err := reopened.Delete(lease.ConnectionID, lease.LeaseID); err != nil {
		t.Fatal(err)
	}
	if len(reopened.Pending()) != 0 {
		t.Fatal("lease not deleted")
	}
}

func TestFileLeaseStoreRecoversInterruptedWriteIntent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "leases.json")
	store, err := NewFileLeaseStore(path)
	if err != nil {
		t.Fatal(err)
	}
	lease := testLeaseState()
	lease.InFlightUploadBytes = 16
	if err := store.Put(lease); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewFileLeaseStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	pending := reopened.Pending()
	if len(pending) != 1 || pending[0].InFlightUploadBytes != 16 {
		t.Fatalf("lost write intent: %+v", pending)
	}
	lease.InFlightUploadBytes = 0
	lease.InFlightDownloadBytes = 0
	lease.UploadedBytes = 5
	lease.ConsumedBytes = 5
	if err := reopened.Put(lease); err != nil {
		t.Fatalf("commit actual write: %v", err)
	}
	if reopened.Pending()[0].InFlightUploadBytes != 0 {
		t.Fatal("write intent not cleared")
	}
}

func TestFileLeaseStoreRejectsUnsafeFilesAndDoubleOpen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "leases.json")
	store, err := NewFileLeaseStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if other, err := NewFileLeaseStore(path); err == nil {
		other.Close()
		t.Fatal("double open")
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"leases":[{}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if store, err := NewFileLeaseStore(path); err == nil {
		store.Close()
		t.Fatal("accepted corrupt lease")
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if store, err := NewFileLeaseStore(path); err == nil {
		store.Close()
		t.Fatal("accepted readable lease")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(t.TempDir(), "target"), path); err != nil {
		t.Fatal(err)
	}
	if store, err := NewFileLeaseStore(path); err == nil {
		store.Close()
		t.Fatal("accepted symlink")
	}
}

func TestFileLeaseStoreRejectsMalformedRootAndLeaseFields(t *testing.T) {
	lease := testLeaseState()
	valid, err := json.Marshal(lease)
	if err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{
		`{}`, `null`, `{"leases":null}`, `{"leases":[],"leases":[]}`,
		`{"leases":[],"other":1}`, `{"Leases":[]}`,
		`{"leases":[` + string(valid) + `],"leases":[]}`,
		`{"leases":[` + strings.Replace(string(valid), `"sequence":0`, `"sequence":0,"sequence":1`, 1) + `]}`,
		`{"leases":[` + strings.Replace(string(valid), `"lease_id":"bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"`, `"lease_id":"not-a-valid-uuid-but-36-chars-long!!!"`, 1) + `]}`,
	} {
		t.Run(body, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "leases.json")
			if err := os.WriteFile(path, []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
			if store, err := NewFileLeaseStore(path); err == nil {
				store.Close()
				t.Fatal("accepted malformed lease file")
			}
		})
	}
}
