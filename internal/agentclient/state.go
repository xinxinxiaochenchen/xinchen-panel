package agentclient

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"controlplane/internal/agentproto"
)

type FileStateStore struct{ path string }

func NewFileStateStore(path string) (*FileStateStore, error) {
	if !filepath.IsAbs(path) || filepath.Base(path) == "." || filepath.Base(path) == string(filepath.Separator) {
		return nil, errors.New("Agent state path must be an absolute file path")
	}
	return &FileStateStore{path: path}, nil
}

func (s *FileStateStore) Save(value agentproto.ConfigSnapshot) error {
	contents, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if _, err := agentproto.DecodeConfigSnapshot(contents, time.Now()); err != nil {
		return fmt.Errorf("invalid Agent state snapshot: %w", err)
	}
	directory := filepath.Dir(s.path)
	file, err := os.CreateTemp(directory, ".agent-state-*")
	if err != nil {
		return fmt.Errorf("create Agent state file: %w", err)
	}
	temporary := file.Name()
	defer os.Remove(temporary)
	if err := file.Chmod(0600); err != nil {
		file.Close()
		return err
	}
	if _, err := file.Write(contents); err != nil {
		file.Close()
		return fmt.Errorf("write Agent state: %w", err)
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return fmt.Errorf("sync Agent state: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close Agent state: %w", err)
	}
	if err := os.Rename(temporary, s.path); err != nil {
		return fmt.Errorf("replace Agent state: %w", err)
	}
	dir, err := os.Open(directory)
	if err != nil {
		return err
	}
	defer dir.Close()
	if err := dir.Sync(); err != nil {
		return fmt.Errorf("sync Agent state directory: %w", err)
	}
	return nil
}

func (s *FileStateStore) Load() (agentproto.ConfigSnapshot, error) {
	info, err := os.Lstat(s.path)
	if err != nil {
		return agentproto.ConfigSnapshot{}, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return agentproto.ConfigSnapshot{}, errors.New("Agent state file permissions are unsafe")
	}
	contents, err := os.ReadFile(s.path)
	if err != nil {
		return agentproto.ConfigSnapshot{}, err
	}
	return agentproto.DecodeConfigSnapshot(contents, time.Now())
}
