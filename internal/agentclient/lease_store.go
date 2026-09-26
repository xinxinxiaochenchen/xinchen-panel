package agentclient

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"controlplane/internal/agentproto"
)

var leaseUUIDPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// LeaseState is the durable portion of one active connection lease. The
// counters are cumulative so a restart can replay the last report without
// changing its sequence or charging the same bytes twice.
type LeaseState struct {
	ConnectionID            string    `json:"connection_id"`
	LeaseID                 string    `json:"lease_id"`
	ResourceKind            string    `json:"resource_kind"`
	ResourceID              string    `json:"resource_id"`
	Revision                int64     `json:"revision"`
	PeriodID                string    `json:"period_id"`
	MultiplierMilli         int64     `json:"multiplier_milli"`
	GrantedBytes            int64     `json:"granted_bytes"`
	UploadedBytes           int64     `json:"uploaded_bytes"`
	DownloadedBytes         int64     `json:"downloaded_bytes"`
	ReportedUploadedBytes   int64     `json:"reported_uploaded_bytes"`
	ReportedDownloadedBytes int64     `json:"reported_downloaded_bytes"`
	ConsumedBytes           int64     `json:"consumed_bytes"`
	Sequence                int64     `json:"sequence"`
	InFlightUploadBytes     int64     `json:"in_flight_upload_bytes"`
	InFlightDownloadBytes   int64     `json:"in_flight_download_bytes"`
	InFlightUploadBase      int64     `json:"in_flight_upload_base"`
	InFlightDownloadBase    int64     `json:"in_flight_download_base"`
	IssuedAt                time.Time `json:"issued_at"`
	ExpiresAt               time.Time `json:"expires_at"`
	PeriodEndsAt            time.Time `json:"period_ends_at"`
}

type FileLeaseStore struct {
	mu            sync.Mutex
	path          string
	lock          *os.File
	leases        map[string]LeaseState
	closed        bool
	failed        error
	syncDirectory func(string) error
}

func NewFileLeaseStore(path string) (*FileLeaseStore, error) {
	if !filepath.IsAbs(path) || filepath.Base(path) == "." || filepath.Base(path) == string(filepath.Separator) {
		return nil, errors.New("Agent lease state path must be an absolute file path")
	}
	lock, err := lockUsageOutbox(path + ".lock")
	if err != nil {
		return nil, err
	}
	store := &FileLeaseStore{path: path, lock: lock, leases: make(map[string]LeaseState), syncDirectory: syncUsageOutboxDirectory}
	if err := store.load(); err != nil {
		_ = store.Close()
		return nil, err
	}
	return store, nil
}

func leaseKey(connectionID, leaseID string) string {
	return strings.ToLower(connectionID) + "/" + strings.ToLower(leaseID)
}

func (s *FileLeaseStore) load() error {
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
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 8<<20 {
		return errors.New("Agent lease state file is unsafe")
	}
	data, err := io.ReadAll(io.LimitReader(file, 8<<20+1))
	if err != nil {
		return err
	}
	if len(data) > 8<<20 {
		return errors.New("Agent lease state file is too large")
	}
	if !hasExactJSONFields(data, "leases") {
		return errors.New("invalid Agent lease state root fields")
	}
	var root struct {
		Leases []LeaseState `json:"leases"`
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&root); err != nil {
		return errors.New("invalid Agent lease state root")
	}
	if _, err := decoder.Token(); err != io.EOF {
		return errors.New("trailing Agent lease state data")
	}
	if len(root.Leases) > 1024 {
		return errors.New("too many Agent lease states")
	}
	var rawRoot struct {
		Leases []json.RawMessage `json:"leases"`
	}
	if err := json.Unmarshal(data, &rawRoot); err != nil || len(rawRoot.Leases) != len(root.Leases) {
		return errors.New("invalid Agent lease state entries")
	}
	leaseFields := []string{"connection_id", "lease_id", "resource_kind", "resource_id", "revision", "period_id", "multiplier_milli", "granted_bytes", "uploaded_bytes", "downloaded_bytes", "reported_uploaded_bytes", "reported_downloaded_bytes", "consumed_bytes", "sequence", "in_flight_upload_bytes", "in_flight_download_bytes", "in_flight_upload_base", "in_flight_download_base", "issued_at", "expires_at", "period_ends_at"}
	for i, lease := range root.Leases {
		if !hasExactJSONFields(rawRoot.Leases[i], leaseFields...) {
			return errors.New("invalid Agent lease state fields")
		}
		if err := validateLeaseState(&lease); err != nil {
			return err
		}
		key := leaseKey(lease.ConnectionID, lease.LeaseID)
		if _, exists := s.leases[key]; exists {
			return errors.New("duplicate Agent lease state")
		}
		s.leases[key] = lease
	}
	return nil
}

func hasExactJSONFields(payload []byte, fields ...string) bool {
	allowed := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		allowed[field] = struct{}{}
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	start, err := decoder.Token()
	if err != nil || start != json.Delim('{') {
		return false
	}
	seen := make(map[string]struct{}, len(fields))
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return false
		}
		key, ok := keyToken.(string)
		if !ok {
			return false
		}
		if _, ok := allowed[key]; !ok {
			return false
		}
		if _, duplicate := seen[key]; duplicate {
			return false
		}
		seen[key] = struct{}{}
		var value json.RawMessage
		if decoder.Decode(&value) != nil {
			return false
		}
		if key == "leases" && (len(value) == 0 || value[0] != '[') {
			return false
		}
	}
	end, err := decoder.Token()
	if err != nil || end != json.Delim('}') || len(seen) != len(fields) {
		return false
	}
	_, err = decoder.Token()
	return err == io.EOF
}

func validateLeaseState(lease *LeaseState) error {
	for _, value := range []*string{&lease.ConnectionID, &lease.LeaseID, &lease.ResourceID, &lease.PeriodID} {
		if !leaseUUIDPattern.MatchString(*value) {
			return errors.New("invalid Agent lease identity")
		}
	}
	if lease.ResourceKind != "forward" && lease.ResourceKind != "proxy" || lease.Revision <= 0 || lease.MultiplierMilli < 1 || lease.MultiplierMilli > 100000 || lease.GrantedBytes < 1 || lease.GrantedBytes > agentproto.MaxQuotaGrantBytes || lease.UploadedBytes < 0 || lease.DownloadedBytes < 0 || lease.ReportedUploadedBytes < 0 || lease.ReportedDownloadedBytes < 0 || lease.ReportedUploadedBytes > lease.UploadedBytes || lease.ReportedDownloadedBytes > lease.DownloadedBytes || lease.ConsumedBytes < 0 || lease.Sequence < 0 || lease.InFlightUploadBytes < 0 || lease.InFlightDownloadBytes < 0 || lease.InFlightUploadBytes > agentproto.MaxQuotaGrantBytes || lease.InFlightDownloadBytes > agentproto.MaxQuotaGrantBytes || lease.InFlightUploadBase < 0 || lease.InFlightDownloadBase < 0 || lease.InFlightUploadBase > lease.UploadedBytes || lease.InFlightDownloadBase > lease.DownloadedBytes {
		return errors.New("invalid Agent lease state")
	}
	if lease.UploadedBytes > int64(^uint64(0)>>1)-lease.DownloadedBytes || lease.IssuedAt.IsZero() || lease.ExpiresAt.IsZero() || lease.PeriodEndsAt.IsZero() || !lease.IssuedAt.Before(lease.ExpiresAt) || lease.ExpiresAt.After(lease.PeriodEndsAt) {
		return errors.New("invalid Agent lease state time or counters")
	}
	lease.IssuedAt = lease.IssuedAt.UTC().Truncate(time.Microsecond)
	lease.ExpiresAt = lease.ExpiresAt.UTC().Truncate(time.Microsecond)
	lease.PeriodEndsAt = lease.PeriodEndsAt.UTC().Truncate(time.Microsecond)
	return nil
}

func (s *FileLeaseStore) writable() error {
	if s.closed {
		return errors.New("Agent lease state store closed")
	}
	if s.failed != nil {
		return fmt.Errorf("Agent lease state persistence failed: %w", s.failed)
	}
	return nil
}

func (s *FileLeaseStore) Pending() []LeaseState {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]LeaseState, 0, len(s.leases))
	for _, lease := range s.leases {
		result = append(result, lease)
	}
	return result
}

func (s *FileLeaseStore) Put(lease LeaseState) error {
	if err := validateLeaseState(&lease); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.writable(); err != nil {
		return err
	}
	key := leaseKey(lease.ConnectionID, lease.LeaseID)
	if _, exists := s.leases[key]; !exists && len(s.leases) >= 1024 {
		return errors.New("Agent lease state capacity exceeded")
	}
	if prior, exists := s.leases[key]; exists {
		if prior.ConnectionID != lease.ConnectionID || prior.LeaseID != lease.LeaseID || prior.ResourceKind != lease.ResourceKind || prior.ResourceID != lease.ResourceID || prior.Revision != lease.Revision || prior.PeriodID != lease.PeriodID || prior.MultiplierMilli != lease.MultiplierMilli || prior.GrantedBytes != lease.GrantedBytes || !prior.IssuedAt.Equal(lease.IssuedAt) || !prior.ExpiresAt.Equal(lease.ExpiresAt) || !prior.PeriodEndsAt.Equal(lease.PeriodEndsAt) || lease.Sequence < prior.Sequence || lease.UploadedBytes < prior.UploadedBytes || lease.DownloadedBytes < prior.DownloadedBytes || lease.ReportedUploadedBytes < prior.ReportedUploadedBytes || lease.ReportedDownloadedBytes < prior.ReportedDownloadedBytes || lease.ConsumedBytes < prior.ConsumedBytes {
			return errors.New("Agent lease identity or counters changed backwards")
		}
	}
	next := cloneLeaseMap(s.leases)
	next[key] = lease
	if err := s.persist(next); err != nil {
		s.failed = err
		return err
	}
	s.leases = next
	return nil
}

func (s *FileLeaseStore) Delete(connectionID, leaseID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.writable(); err != nil {
		return err
	}
	key := leaseKey(connectionID, leaseID)
	if _, exists := s.leases[key]; !exists {
		return nil
	}
	next := cloneLeaseMap(s.leases)
	delete(next, key)
	if err := s.persist(next); err != nil {
		s.failed = err
		return err
	}
	s.leases = next
	return nil
}

func cloneLeaseMap(input map[string]LeaseState) map[string]LeaseState {
	output := make(map[string]LeaseState, len(input))
	for key, value := range input {
		output[key] = value
	}
	return output
}

func (s *FileLeaseStore) persist(leases map[string]LeaseState) error {
	values := make([]LeaseState, 0, len(leases))
	for _, lease := range leases {
		values = append(values, lease)
	}
	data, err := json.Marshal(struct {
		Leases []LeaseState `json:"leases"`
	}{values})
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(s.path), ".agent-leases-*")
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
	return s.syncDirectory(filepath.Dir(s.path))
}

func (s *FileLeaseStore) Close() error {
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
