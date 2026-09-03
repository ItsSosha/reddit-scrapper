// Package notify delivers matched posts to the outside world.
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/ItsSosha/reddit-scrapper/internal/config"
	"github.com/ItsSosha/reddit-scrapper/internal/reddit"
)

// Event is a single matched post plus the terms that matched it.
type Event struct {
	Post  reddit.Post
	Terms []string
}

// Notifier delivers an event. Implementations should be safe to call
// sequentially and should respect ctx cancellation.
type Notifier interface {
	Name() string
	Notify(ctx context.Context, e Event) error
}

// Build assembles the enabled notifiers. The logger notifier is always first
// so a match is recorded even when a delivery fails.
func Build(cfg config.Notifier, logger *slog.Logger, hc *http.Client) []Notifier {
	if hc == nil {
		hc = &http.Client{Timeout: 15 * time.Second}
	}
	out := []Notifier{&Log{Logger: logger}}
	if cfg.Telegram.Enabled {
		out = append(out, &Telegram{
			BotToken:   cfg.Telegram.BotToken,
			ChatID:     cfg.Telegram.ChatID,
			HTTPClient: hc,
		})
	}
	if cfg.Webhook.Enabled {
		out = append(out, &Webhook{URL: cfg.Webhook.URL, HTTPClient: hc})
	}
	return out
}

// Log writes matches to the structured logger.
type Log struct{ Logger *slog.Logger }

func (l *Log) Name() string { return "log" }

func (l *Log) Notify(_ context.Context, e Event) error {
	l.Logger.Info("match",
		"subreddit", e.Post.Subreddit,
		"title", e.Post.Title,
		"author", e.Post.Author,
		"terms", strings.Join(e.Terms, ","),
		"created", e.Post.Created.Format(time.RFC3339),
		"url", e.Post.Permalink,
	)
	return nil
}

// Telegram sends a message to a chat through the Bot API.
type Telegram struct {
	BotToken   string
	ChatID     string
	HTTPClient *http.Client
	// endpoint overrides the Bot API host; used by tests.
	endpoint string
}

func (t *Telegram) Name() string { return "telegram" }

func (t *Telegram) Notify(ctx context.Context, e Event) error {
	form := url.Values{}
	form.Set("chat_id", t.ChatID)
	form.Set("text", plainText(e))
	form.Set("disable_web_page_preview", "false")

	host := t.endpoint
	if host == "" {
		host = "https://api.telegram.org"
	}
	endpoint := fmt.Sprintf("%s/bot%s/sendMessage", host, t.BotToken)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return check(t.HTTPClient.Do(req))
}

// Webhook POSTs a JSON description of the match.
type Webhook struct {
	URL        string
	HTTPClient *http.Client
}

func (w *Webhook) Name() string { return "webhook" }

func (w *Webhook) Notify(ctx context.Context, e Event) error {
	payload := struct {
		Subreddit string    `json:"subreddit"`
		ID        string    `json:"id"`
		Title     string    `json:"title"`
		Author    string    `json:"author"`
		Body      string    `json:"body"`
		Flair     string    `json:"flair,omitempty"`
		URL       string    `json:"url"`
		Created   time.Time `json:"created_utc"`
		Terms     []string  `json:"matched_terms"`
		Text      string    `json:"text"`
	}{
		Subreddit: e.Post.Subreddit,
		ID:        e.Post.ID,
		Title:     e.Post.Title,
		Author:    e.Post.Author,
		Body:      e.Post.SelfText,
		Flair:     e.Post.Flair,
		URL:       e.Post.Permalink,
		Created:   e.Post.Created,
		Terms:     e.Terms,
		Text:      plainText(e),
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.URL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	return check(w.HTTPClient.Do(req))
}

// plainText renders an event as the message body shared by the notifiers.
func plainText(e Event) string {
	var b strings.Builder
	fmt.Fprintf(&b, "r/%s: %s\n", e.Post.Subreddit, e.Post.Title)
	if e.Post.Flair != "" {
		fmt.Fprintf(&b, "[%s] ", e.Post.Flair)
	}
	fmt.Fprintf(&b, "by u/%s · %s\n", e.Post.Author, e.Post.Created.Format(time.RFC1123))
	if len(e.Terms) > 0 {
		fmt.Fprintf(&b, "matched: %s\n", strings.Join(e.Terms, ", "))
	}
	if body := strings.TrimSpace(e.Post.SelfText); body != "" {
		fmt.Fprintf(&b, "\n%s\n", truncate(body, 600))
	}
	fmt.Fprintf(&b, "\n%s", e.Post.Permalink)
	return b.String()
}

func truncate(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "..."
}

func check(resp *http.Response, err error) error {
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("%s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	return nil
}
