package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")

	s, err := Open(path, time.Hour)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !s.IsFirstRun() {
		t.Fatal("a missing state file should report a first run")
	}
	s.Mark("fontainesdc/abc123")
	if !s.Seen("fontainesdc/abc123") {
		t.Fatal("marked key should be seen")
	}
	if err := s.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if s.IsFirstRun() {
		t.Fatal("after a save it is no longer a first run")
	}

	reopened, err := Open(path, time.Hour)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if reopened.IsFirstRun() {
		t.Fatal("an existing state file is not a first run")
	}
	if !reopened.Seen("fontainesdc/abc123") {
		t.Fatal("state did not survive a reload")
	}
	if reopened.Len() != 1 {
		t.Fatalf("Len = %d, want 1", reopened.Len())
	}
}

func TestRetentionPrunesOldEntries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	raw, err := json.Marshal(fileFormat{Seen: map[string]time.Time{
		"sub/old":   time.Now().Add(-48 * time.Hour),
		"sub/fresh": time.Now().Add(-time.Minute),
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}

	s, err := Open(path, 24*time.Hour)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if s.Seen("sub/old") {
		t.Fatal("entry older than retention should be dropped")
	}
	if !s.Seen("sub/fresh") {
		t.Fatal("recent entry should be kept")
	}
}

func TestSaveCreatesMissingDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "dir", "state.json")
	s, err := Open(path, time.Hour)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	s.Mark("a/b")
	if err := s.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("state file not written: %v", err)
	}
}
