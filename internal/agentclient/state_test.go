package agentclient

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"controlplane/internal/agentproto"
	"controlplane/internal/agentruntime"
)

func TestFileStateStoreSavesAtomicPrivateSnapshot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent-state.json")
	store, err := NewFileStateStore(path)
	if err != nil {
		t.Fatal(err)
	}
	_, digest, err := agentproto.CanonicalForwardConfig(nil)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := agentproto.ConfigSnapshot{Revision: 3, SHA256: digest,
		ValidUntil: time.Now().Add(time.Minute), ForwardConfig: []agentruntime.Rule{}}
	if err := store.Save(snapshot); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
		t.Fatalf("unsafe Agent state mode: %s", info.Mode())
	}
	loaded, err := store.Load()
	if err != nil || loaded.Revision != 3 || loaded.SHA256 != digest {
		t.Fatalf("loaded = %+v, %v", loaded, err)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil || len(entries) != 1 {
		t.Fatalf("temporary state files remained: %v, %v", entries, err)
	}
	if err := os.WriteFile(path, []byte(`{"revision":4,"sha256":"broken"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(); err == nil {
		t.Fatal("corrupt Agent state accepted")
	}
}
