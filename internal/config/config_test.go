package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadDefaults(t *testing.T) {
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Subreddits) != 1 || cfg.Subreddits[0] != "fontainesdc" {
		t.Fatalf("Subreddits = %v", cfg.Subreddits)
	}
	if cfg.PollInterval.D() != time.Minute {
		t.Fatalf("PollInterval = %s", cfg.PollInterval.D())
	}
}

func TestLoadFileFillsDefaults(t *testing.T) {
	path := writeConfig(t, `{"subreddits":["r/Berlin"],"match":{"any_of":["ticket"]}}`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Subreddits[0] != "Berlin" {
		t.Fatalf("the r/ prefix should be stripped, got %q", cfg.Subreddits[0])
	}
	if cfg.Listing != "new" || cfg.Limit == 0 || cfg.StateFile == "" {
		t.Fatalf("defaults not applied: %+v", cfg)
	}
	if len(cfg.Match.Fields) == 0 {
		t.Fatal("default match fields not applied")
	}
}

func TestEnvOverrides(t *testing.T) {
	t.Setenv("SUBREDDITS", "fontainesdc, indieheads")
	t.Setenv("MATCH_ALL_OF", "berlin")
	t.Setenv("POLL_INTERVAL", "30s")
	t.Setenv("TELEGRAM_BOT_TOKEN", "token")
	t.Setenv("TELEGRAM_CHAT_ID", "42")

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Subreddits) != 2 || cfg.Subreddits[1] != "indieheads" {
		t.Fatalf("Subreddits = %v", cfg.Subreddits)
	}
	if cfg.PollInterval.D() != 30*time.Second {
		t.Fatalf("PollInterval = %s", cfg.PollInterval.D())
	}
	if !cfg.Notifier.Telegram.Enabled {
		t.Fatal("telegram should auto-enable when token and chat id are present")
	}
}

func TestValidationErrors(t *testing.T) {
	tests := []struct{ name, body string }{
		{"no terms", `{"subreddits":["x"],"match":{}}`},
		{"no subreddits", `{"subreddits":[],"match":{"any_of":["a"]}}`},
		{"bad listing", `{"subreddits":["x"],"listing":"top","match":{"any_of":["a"]}}`},
		{"interval too small", `{"subreddits":["x"],"poll_interval":"1s","match":{"any_of":["a"]}}`},
		{"half credentials", `{"subreddits":["x"],"match":{"any_of":["a"]},"reddit":{"client_id":"only-id"}}`},
		{"unknown field", `{"subreddits":["x"],"match":{"any_of":["a"]},"typo":true}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Load(writeConfig(t, tc.body)); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

// The example config ships with the repo, so it must stay loadable: an unknown
// or renamed field would otherwise only fail for whoever copies it first.
func TestExampleConfigIsValid(t *testing.T) {
	cfg, err := Load(filepath.Join("..", "..", "config.example.json"))
	if err != nil {
		t.Fatalf("config.example.json: %v", err)
	}
	if len(cfg.Subreddits) == 0 || len(cfg.Match.AllOf) == 0 {
		t.Fatalf("example config looks empty: %+v", cfg)
	}
}
