package notify

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ItsSosha/reddit-scrapper/internal/reddit"
)

func sampleEvent() Event {
	return Event{
		Post: reddit.Post{
			ID:        "abc123",
			Subreddit: "fontainesdc",
			Title:     "Spare Berlin ticket",
			SelfText:  "face value, DM me",
			Flair:     "Tickets",
			Author:    "someone",
			Permalink: "https://www.reddit.com/r/fontainesdc/comments/abc123/x/",
		},
		Terms: []string{"berlin", "ticket"},
	}
}

func TestWebhookPayload(t *testing.T) {
	var body []byte
	var contentType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
		contentType = r.Header.Get("Content-Type")
	}))
	defer srv.Close()

	w := &Webhook{URL: srv.URL, HTTPClient: srv.Client()}
	if err := w.Notify(context.Background(), sampleEvent()); err != nil {
		t.Fatalf("Notify: %v", err)
	}
	if contentType != "application/json" {
		t.Fatalf("Content-Type = %q", contentType)
	}

	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("payload is not JSON: %v", err)
	}
	if got["id"] != "abc123" || got["subreddit"] != "fontainesdc" {
		t.Fatalf("payload = %v", got)
	}
	if terms, ok := got["matched_terms"].([]any); !ok || len(terms) != 2 {
		t.Fatalf("matched_terms = %v", got["matched_terms"])
	}
}

func TestWebhookReportsHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusBadRequest)
	}))
	defer srv.Close()

	w := &Webhook{URL: srv.URL, HTTPClient: srv.Client()}
	if err := w.Notify(context.Background(), sampleEvent()); err == nil {
		t.Fatal("expected an error on a non-2xx response")
	}
}

func TestTelegramMessage(t *testing.T) {
	var form string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		form = string(raw)
		if !strings.HasPrefix(r.URL.Path, "/bottoken/sendMessage") {
			t.Errorf("path = %q", r.URL.Path)
		}
	}))
	defer srv.Close()

	tg := &Telegram{BotToken: "token", ChatID: "42", HTTPClient: srv.Client(), endpoint: srv.URL}
	if err := tg.Notify(context.Background(), sampleEvent()); err != nil {
		t.Fatalf("Notify: %v", err)
	}
	if !strings.Contains(form, "chat_id=42") {
		t.Fatalf("form = %q", form)
	}
	if !strings.Contains(form, "Spare+Berlin+ticket") {
		t.Fatalf("message body missing the title: %q", form)
	}
}

func TestPlainTextIncludesLinkAndTerms(t *testing.T) {
	text := plainText(sampleEvent())
	for _, want := range []string{"r/fontainesdc", "Spare Berlin ticket", "matched: berlin, ticket", "https://www.reddit.com/r/fontainesdc/comments/abc123/x/"} {
		if !strings.Contains(text, want) {
			t.Fatalf("message %q missing %q", text, want)
		}
	}
}
