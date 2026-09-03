// Package config loads and validates the watcher configuration.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config is the top-level configuration for the watcher.
type Config struct {
	// Subreddits to poll, without the "r/" prefix.
	Subreddits []string `json:"subreddits"`
	// Listing is the sort order to poll: "new" (default) or "hot".
	Listing string `json:"listing"`
	// PollInterval is how long to wait between polling rounds, e.g. "60s".
	PollInterval Duration `json:"poll_interval"`
	// Limit is how many posts to request per poll (1-100).
	Limit int `json:"limit"`
	// StateFile holds the IDs of posts already seen, so restarts don't re-alert.
	StateFile string `json:"state_file"`
	// NotifyOnFirstRun controls whether the first poll of a fresh state file
	// alerts on everything it matches. Off by default so a cold start doesn't
	// dump the whole backlog into your notifications.
	NotifyOnFirstRun bool `json:"notify_on_first_run"`
	// StateRetention is how long a seen post ID is remembered.
	StateRetention Duration `json:"state_retention"`

	Match    Match    `json:"match"`
	Reddit   Reddit   `json:"reddit"`
	Notifier Notifier `json:"notifier"`
}

// Match describes which posts are interesting.
//
// A post matches when it contains every term in AllOf, at least one term in
// AnyOf (when AnyOf is non-empty), and no term in NoneOf.
type Match struct {
	AllOf  []string `json:"all_of"`
	AnyOf  []string `json:"any_of"`
	NoneOf []string `json:"none_of"`
	// Fields to search: any of "title", "selftext", "flair", "url", "author".
	Fields []string `json:"fields"`
	// CaseSensitive matching is off by default.
	CaseSensitive bool `json:"case_sensitive"`
	// WholeWord requires terms to sit on word boundaries, so "ticket" does not
	// match "ticketing" and, more usefully, "wts" does not match "howtws".
	WholeWord bool `json:"whole_word"`
}

// Reddit holds API credentials. All fields are optional: without credentials
// the client falls back to the public www.reddit.com JSON endpoints, which are
// rate limited more aggressively and are often blocked for cloud IPs.
type Reddit struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
	Username     string `json:"username"`
	Password     string `json:"password"`
	UserAgent    string `json:"user_agent"`
}

// Notifier configures where matches are delivered. Stdout is always on.
type Notifier struct {
	Telegram TelegramNotifier `json:"telegram"`
	Webhook  WebhookNotifier  `json:"webhook"`
}

// TelegramNotifier posts matches to a Telegram chat via a bot.
type TelegramNotifier struct {
	Enabled  bool   `json:"enabled"`
	BotToken string `json:"bot_token"`
	ChatID   string `json:"chat_id"`
}

// WebhookNotifier POSTs a JSON body describing the match to an arbitrary URL.
type WebhookNotifier struct {
	Enabled bool   `json:"enabled"`
	URL     string `json:"url"`
}

// Duration is a time.Duration that unmarshals from a JSON string ("90s").
type Duration time.Duration

func (d *Duration) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return fmt.Errorf("duration must be a string like \"60s\": %w", err)
	}
	parsed, err := time.ParseDuration(s)
	if err != nil {
		return err
	}
	*d = Duration(parsed)
	return nil
}

func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(time.Duration(d).String())
}

// D returns the value as a time.Duration.
func (d Duration) D() time.Duration { return time.Duration(d) }

// Default returns the configuration used when no file is supplied: the
// r/fontainesdc Berlin ticket watch this tool was built for.
func Default() Config {
	return Config{
		Subreddits:     []string{"fontainesdc"},
		Listing:        "new",
		PollInterval:   Duration(60 * time.Second),
		Limit:          50,
		StateFile:      "state.json",
		StateRetention: Duration(30 * 24 * time.Hour),
		Match: Match{
			AllOf:     []string{"berlin"},
			AnyOf:     []string{"ticket", "tickets", "spare", "selling", "sell", "wts"},
			Fields:    []string{"title", "selftext", "flair"},
			WholeWord: true,
		},
		Reddit: Reddit{UserAgent: "go:reddit-scrapper:0.1.0 (by /u/unknown)"},
	}
}

// Load reads the config file at path (empty path means defaults only), applies
// environment overrides, fills in defaults and validates the result.
func Load(path string) (Config, error) {
	cfg := Default()
	if path != "" {
		raw, err := os.ReadFile(path)
		if err != nil {
			return Config{}, fmt.Errorf("read config: %w", err)
		}
		// Start from a zero value so the file is authoritative for the fields
		// it sets; applyDefaults fills the rest back in.
		var fromFile Config
		dec := json.NewDecoder(strings.NewReader(string(raw)))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&fromFile); err != nil {
			return Config{}, fmt.Errorf("parse config %s: %w", path, err)
		}
		cfg = fromFile
	}
	applyEnv(&cfg)
	applyDefaults(&cfg)
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// applyEnv layers environment variables over the config. Credentials in
// particular belong in the environment rather than in a committed file.
func applyEnv(cfg *Config) {
	setString(&cfg.Reddit.ClientID, "REDDIT_CLIENT_ID")
	setString(&cfg.Reddit.ClientSecret, "REDDIT_CLIENT_SECRET")
	setString(&cfg.Reddit.Username, "REDDIT_USERNAME")
	setString(&cfg.Reddit.Password, "REDDIT_PASSWORD")
	setString(&cfg.Reddit.UserAgent, "REDDIT_USER_AGENT")
	setString(&cfg.Notifier.Telegram.BotToken, "TELEGRAM_BOT_TOKEN")
	setString(&cfg.Notifier.Telegram.ChatID, "TELEGRAM_CHAT_ID")
	setString(&cfg.Notifier.Webhook.URL, "WEBHOOK_URL")
	setString(&cfg.StateFile, "STATE_FILE")

	if v := os.Getenv("SUBREDDITS"); v != "" {
		cfg.Subreddits = splitList(v)
	}
	if v := os.Getenv("MATCH_ALL_OF"); v != "" {
		cfg.Match.AllOf = splitList(v)
	}
	if v := os.Getenv("MATCH_ANY_OF"); v != "" {
		cfg.Match.AnyOf = splitList(v)
	}
	if v := os.Getenv("MATCH_NONE_OF"); v != "" {
		cfg.Match.NoneOf = splitList(v)
	}
	if v := os.Getenv("POLL_INTERVAL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			cfg.PollInterval = Duration(d)
		}
	}
	if v := os.Getenv("LIMIT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.Limit = n
		}
	}
	if cfg.Notifier.Telegram.BotToken != "" && cfg.Notifier.Telegram.ChatID != "" {
		cfg.Notifier.Telegram.Enabled = true
	}
	if cfg.Notifier.Webhook.URL != "" {
		cfg.Notifier.Webhook.Enabled = true
	}
}

func applyDefaults(cfg *Config) {
	def := Default()
	if cfg.Listing == "" {
		cfg.Listing = def.Listing
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = def.PollInterval
	}
	if cfg.Limit <= 0 {
		cfg.Limit = def.Limit
	}
	if cfg.StateFile == "" {
		cfg.StateFile = def.StateFile
	}
	if cfg.StateRetention <= 0 {
		cfg.StateRetention = def.StateRetention
	}
	if len(cfg.Match.Fields) == 0 {
		cfg.Match.Fields = def.Match.Fields
	}
	if cfg.Reddit.UserAgent == "" {
		cfg.Reddit.UserAgent = def.Reddit.UserAgent
	}
	for i, s := range cfg.Subreddits {
		cfg.Subreddits[i] = strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(s), "/r/"), "r/")
	}
}

// Validate reports configuration that would make the watcher useless or fail
// at runtime.
func (c Config) Validate() error {
	if len(c.Subreddits) == 0 {
		return fmt.Errorf("no subreddits configured")
	}
	for _, s := range c.Subreddits {
		if s == "" {
			return fmt.Errorf("empty subreddit name")
		}
	}
	if len(c.Match.AllOf) == 0 && len(c.Match.AnyOf) == 0 {
		return fmt.Errorf("match needs at least one term in all_of or any_of")
	}
	switch c.Listing {
	case "new", "hot", "rising":
	default:
		return fmt.Errorf("listing %q must be one of new, hot, rising", c.Listing)
	}
	if c.Limit < 1 || c.Limit > 100 {
		return fmt.Errorf("limit %d out of range 1-100", c.Limit)
	}
	if c.PollInterval.D() < 5*time.Second {
		return fmt.Errorf("poll_interval %s is too aggressive; use 5s or more", c.PollInterval.D())
	}
	// A client secret without an ID (or the reverse) is a misconfiguration that
	// silently degrades to unauthenticated polling, so call it out.
	if (c.Reddit.ClientID == "") != (c.Reddit.ClientSecret == "") {
		return fmt.Errorf("reddit client_id and client_secret must be set together")
	}
	if c.Notifier.Telegram.Enabled && (c.Notifier.Telegram.BotToken == "" || c.Notifier.Telegram.ChatID == "") {
		return fmt.Errorf("telegram notifier needs both bot_token and chat_id")
	}
	if c.Notifier.Webhook.Enabled && c.Notifier.Webhook.URL == "" {
		return fmt.Errorf("webhook notifier needs a url")
	}
	return nil
}

func setString(dst *string, env string) {
	if v := os.Getenv(env); v != "" {
		*dst = v
	}
}

func splitList(v string) []string {
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
