// Package state persists runtime settings changed from Telegram (e.g. the
// model selected by an admin) so they survive restarts and config reloads.
package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
)

// MaxRecent is how many recently used models are remembered.
const MaxRecent = 8

type data struct {
	Model        string   `json:"model,omitempty"`
	RecentModels []string `json:"recent_models,omitempty"`
}

type Store struct {
	path string
	mu   sync.RWMutex
	data data
}

// Open loads the state file, creating an empty state if it does not exist.
// An empty path gives an in-memory store.
func Open(path string) (*Store, error) {
	s := &Store{path: path}
	if path == "" {
		return s, nil
	}
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read state: %w", err)
	}
	if err := json.Unmarshal(raw, &s.data); err != nil {
		return nil, fmt.Errorf("parse state %s: %w", path, err)
	}
	return s, nil
}

// Model returns the admin-selected model, or "" when the config default is used.
func (s *Store) Model() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.data.Model
}

// EffectiveModel returns the selected model or the fallback from config.
func (s *Store) EffectiveModel(fallback string) string {
	if m := s.Model(); m != "" {
		return m
	}
	return fallback
}

// Recent returns recently selected models, newest first.
func (s *Store) Recent() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]string(nil), s.data.RecentModels...)
}

// SetModel selects a model and remembers it in the recent list.
func (s *Store) SetModel(model string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.Model = model
	recent := []string{model}
	for _, m := range s.data.RecentModels {
		if m != model && len(recent) < MaxRecent {
			recent = append(recent, m)
		}
	}
	s.data.RecentModels = recent
	return s.saveLocked()
}

// ResetModel returns to the model from the config.
func (s *Store) ResetModel() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.Model = ""
	return s.saveLocked()
}

func (s *Store) saveLocked() error {
	if s.path == "" {
		return nil
	}
	raw, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}
