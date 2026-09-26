package agentclient

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	"controlplane/internal/agentproto"
)

const MaxPendingUsageBatches = 128

var ErrUsageOutboxFull = errors.New("Agent usage outbox full")
var ErrUsageAckMismatch = errors.New("Agent usage acknowledgement mismatch")
var ErrUsageOutboxClosed = errors.New("Agent usage outbox closed")

// FileUsageOutbox durably retains unacknowledged cumulative usage. Enqueue
// must succeed before the caller permits corresponding traffic to continue.
// Replaying an ACKed batch is safe because the control-plane ledger deduplicates
// connection ID and sequence.
type FileUsageOutbox struct {
	mu            sync.Mutex
	path          string
	batches       []agentproto.UsageBatch
	lock          *os.File
	closed        bool
	failed        error
	syncDirectory func(string) error
}

const maxUsageOutboxFileBytes = int64(MaxPendingUsageBatches) * (agentproto.MaxUsagePayloadBytes + 128)

func NewFileUsageOutbox(path string) (*FileUsageOutbox, error) {
	if !filepath.IsAbs(path) || filepath.Base(path) == "." || filepath.Base(path) == string(filepath.Separator) {
		return nil, errors.New("Agent usage outbox path must be an absolute file path")
	}
	lock, err := lockUsageOutbox(path + ".lock")
	if err != nil {
		return nil, err
	}
	outbox := &FileUsageOutbox{path: path, lock: lock, syncDirectory: syncUsageOutboxDirectory}
	if err := outbox.load(); err != nil {
		_ = outbox.Close()
		return nil, err
	}
	return outbox, nil
}

func (s *FileUsageOutbox) load() error {
	file, err := openUsageOutboxFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > maxUsageOutboxFileBytes {
		return errors.New("Agent usage outbox file is unsafe")
	}
	data, err := io.ReadAll(io.LimitReader(file, maxUsageOutboxFileBytes+1))
	if err != nil {
		return err
	}
	if int64(len(data)) > maxUsageOutboxFileBytes {
		return errors.New("Agent usage outbox file is too large")
	}
	saved, err := decodeSavedUsageBatches(data)
	if err != nil {
		return err
	}
	seen := make(map[string]struct{}, len(saved))
	for _, raw := range saved {
		batch, err := agentproto.DecodeUsageBatch(raw)
		if err != nil {
			return fmt.Errorf("invalid saved usage batch: %w", err)
		}
		if _, duplicate := seen[batch.BatchID]; duplicate {
			return errors.New("duplicate saved usage batch")
		}
		seen[batch.BatchID] = struct{}{}
		s.batches = append(s.batches, batch)
	}
	return nil
}

func decodeSavedUsageBatches(data []byte) ([]json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	start, err := decoder.Token()
	if err != nil || start != json.Delim('{') {
		return nil, errors.New("invalid Agent usage outbox root")
	}
	var saved []json.RawMessage
	found := false
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil || key != "batches" || found {
			return nil, errors.New("invalid Agent usage outbox field")
		}
		found = true
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil || len(raw) == 0 || raw[0] != '[' {
			return nil, errors.New("invalid Agent usage outbox batches")
		}
		if err := json.Unmarshal(raw, &saved); err != nil || len(saved) > MaxPendingUsageBatches {
			return nil, errors.New("invalid Agent usage outbox batches")
		}
	}
	end, err := decoder.Token()
	if err != nil || end != json.Delim('}') || !found {
		return nil, errors.New("invalid Agent usage outbox root")
	}
	if _, err = decoder.Token(); err != io.EOF {
		return nil, errors.New("trailing Agent usage outbox data")
	}
	return saved, nil
}

// Close releases the process lock. Pending remains readable for diagnostics;
// mutations fail after Close, and the caller must reopen to resume processing.
func (s *FileUsageOutbox) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	if s.lock == nil {
		return nil
	}
	err := unlockUsageOutbox(s.lock)
	s.lock = nil
	return err
}

func (s *FileUsageOutbox) writable() error {
	if s.closed {
		return ErrUsageOutboxClosed
	}
	if s.failed != nil {
		return fmt.Errorf("Agent usage outbox persistence failed: %w", s.failed)
	}
	return nil
}

func (s *FileUsageOutbox) Pending() []agentproto.UsageBatch {
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneUsageBatches(s.batches)
}

func (s *FileUsageOutbox) Enqueue(batch agentproto.UsageBatch) error {
	encoded, err := json.Marshal(batch)
	if err != nil {
		return err
	}
	batch, err = agentproto.DecodeUsageBatch(encoded)
	if err != nil {
		return err
	}
	digest, err := agentproto.UsageBatchDigest(batch)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.writable(); err != nil {
		return err
	}
	for _, existing := range s.batches {
		if existing.BatchID != batch.BatchID {
			continue
		}
		priorDigest, err := agentproto.UsageBatchDigest(existing)
		if err != nil {
			return err
		}
		if priorDigest == digest {
			return nil
		}
		return errors.New("usage batch ID already has different reports")
	}
	if len(s.batches) >= MaxPendingUsageBatches {
		return ErrUsageOutboxFull
	}
	next := append(cloneUsageBatches(s.batches), batch)
	if err := s.persist(next); err != nil {
		s.failed = err
		return err
	}
	s.batches = next
	return nil
}

func (s *FileUsageOutbox) Acknowledge(ack agentproto.UsageAck) error {
	encoded, err := json.Marshal(ack)
	if err != nil {
		return err
	}
	ack, err = agentproto.DecodeUsageAck(encoded)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.writable(); err != nil {
		return err
	}
	for i, batch := range s.batches {
		if batch.BatchID != ack.BatchID {
			continue
		}
		digest, err := agentproto.UsageBatchDigest(batch)
		if err != nil {
			return err
		}
		if digest != ack.SHA256 {
			return ErrUsageAckMismatch
		}
		next := append(cloneUsageBatches(s.batches[:i]), cloneUsageBatches(s.batches[i+1:])...)
		if err := s.persist(next); err != nil {
			s.failed = err
			return err
		}
		s.batches = next
		return nil
	}
	return nil
}

func cloneUsageBatches(input []agentproto.UsageBatch) []agentproto.UsageBatch {
	result := make([]agentproto.UsageBatch, len(input))
	for i, batch := range input {
		result[i] = batch
		result[i].Reports = append([]agentproto.UsageReport(nil), batch.Reports...)
	}
	return result
}

func (s *FileUsageOutbox) persist(batches []agentproto.UsageBatch) error {
	data, err := json.Marshal(struct {
		Batches []agentproto.UsageBatch `json:"batches"`
	}{batches})
	if err != nil {
		return err
	}
	directory := filepath.Dir(s.path)
	file, err := os.CreateTemp(directory, ".agent-usage-*")
	if err != nil {
		return err
	}
	temporary := file.Name()
	defer os.Remove(temporary)
	if err := file.Chmod(0600); err != nil {
		file.Close()
		return err
	}
	if _, err := file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporary, s.path); err != nil {
		return err
	}
	// Rename already changed the visible file. Match that state even if the
	// directory sync fails; the caller latches the error and rejects all writes
	// until a fresh open recovers whichever state is durable.
	s.batches = cloneUsageBatches(batches)
	return s.syncDirectory(directory)
}

func syncUsageOutboxDirectory(directory string) error {
	dir, err := os.Open(directory)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
