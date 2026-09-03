// Package store remembers which posts have already been reported so a restart
// does not re-alert on the same thing.
package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Store is a JSON-file-backed set of seen post IDs.
type Store struct {
	path      string
	retention time.Duration

	mu    sync.Mutex
	seen  map[string]time.Time
	fresh bool // true when the file did not exist at load time
}

type fileFormat struct {
	Seen map[string]time.Time `json:"seen"`
}

// Open loads the store at path, creating an empty one if the file is missing.
// Entries older than retention are dropped on load.
func Open(path string, retention time.Duration) (*Store, error) {
	s := &Store{path: path, retention: retention, seen: map[string]time.Time{}}

	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		s.fresh = true
		return s, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read state %s: %w", path, err)
	}

	var f fileFormat
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil, fmt.Errorf("parse state %s: %w", path, err)
	}
	cutoff := time.Now().Add(-retention)
	for id, ts := range f.Seen {
		if retention <= 0 || ts.After(cutoff) {
			s.seen[id] = ts
		}
	}
	return s, nil
}

// IsFirstRun reports whether the state file was absent when the store loaded.
func (s *Store) IsFirstRun() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.fresh
}

// Seen reports whether the key has already been recorded.
func (s *Store) Seen(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.seen[key]
	return ok
}

// Mark records the key as seen. It does not persist until Save is called.
func (s *Store) Mark(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.seen[key]; !ok {
		s.seen[key] = time.Now().UTC()
	}
}

// Len returns the number of remembered keys.
func (s *Store) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.seen)
}

// Save writes the state atomically: a temp file in the same directory followed
// by a rename, so a crash mid-write cannot truncate the state.
func (s *Store) Save() error {
	s.mu.Lock()
	snapshot := fileFormat{Seen: make(map[string]time.Time, len(s.seen))}
	for k, v := range s.seen {
		snapshot.Seen[k] = v
	}
	s.fresh = false
	s.mu.Unlock()

	raw, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return err
	}

	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create state dir: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".state-*.json")
	if err != nil {
		return fmt.Errorf("create temp state: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once the rename succeeds

	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return fmt.Errorf("write temp state: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp state: %w", err)
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		return fmt.Errorf("replace state: %w", err)
	}
	return nil
}
