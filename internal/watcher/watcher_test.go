package watcher

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/ItsSosha/reddit-scrapper/internal/config"
	"github.com/ItsSosha/reddit-scrapper/internal/match"
	"github.com/ItsSosha/reddit-scrapper/internal/notify"
	"github.com/ItsSosha/reddit-scrapper/internal/reddit"
	"github.com/ItsSosha/reddit-scrapper/internal/store"
)

// recorder is a Notifier that captures what it was asked to deliver, and can
// be told to fail.
type recorder struct {
	mu     sync.Mutex
	events []notify.Event
	fail   bool
}

func (r *recorder) Name() string { return "recorder" }

func (r *recorder) Notify(_ context.Context, e notify.Event) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.fail {
		return fmt.Errorf("delivery failed")
	}
	r.events = append(r.events, e)
	return nil
}

func (r *recorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.events)
}

func listingJSON(ids ...string) string {
	out := `{"data":{"children":[`
	for i, id := range ids {
		if i > 0 {
			out += ","
		}
		out += fmt.Sprintf(`{"data":{"id":%q,"subreddit":"fontainesdc","title":"Spare Berlin ticket %s","permalink":"/r/fontainesdc/comments/%s/x/","created_utc":1700000000}}`, id, id, id)
	}
	return out + `]}}`
}

func newTestWatcher(t *testing.T, body func() string, opts ...func(*config.Config)) (*Watcher, *recorder) {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, body())
	}))
	t.Cleanup(srv.Close)

	cfg := config.Default()
	cfg.StateFile = filepath.Join(t.TempDir(), "state.json")
	cfg.NotifyOnFirstRun = true
	cfg.Match = config.Match{AnyOf: []string{"berlin"}, Fields: []string{"title"}}
	for _, o := range opts {
		o(&cfg)
	}

	m, err := match.New(cfg.Match)
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(cfg.StateFile, cfg.StateRetention.D())
	if err != nil {
		t.Fatal(err)
	}
	rec := &recorder{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	client := reddit.New(reddit.Options{BaseURL: srv.URL})
	return New(cfg, client, m, st, []notify.Notifier{rec}, logger), rec
}

func TestRunOnceNotifiesEachPostOnce(t *testing.T) {
	w, rec := newTestWatcher(t, func() string { return listingJSON("a1", "a2") })

	if err := w.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if rec.count() != 2 {
		t.Fatalf("got %d notifications, want 2", rec.count())
	}
	// The same listing again must not re-notify.
	if err := w.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if rec.count() != 2 {
		t.Fatalf("got %d notifications after a repeat poll, want 2", rec.count())
	}
}

func TestSeedingSuppressesFirstRun(t *testing.T) {
	posts := listingJSON("a1")
	w, rec := newTestWatcher(t, func() string { return posts }, func(c *config.Config) {
		c.NotifyOnFirstRun = false
	})

	if err := w.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if rec.count() != 0 {
		t.Fatalf("the seeding run should not notify, got %d", rec.count())
	}

	posts = listingJSON("a1", "a2")
	if err := w.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if rec.count() != 1 {
		t.Fatalf("only the post new since seeding should notify, got %d", rec.count())
	}
}

func TestFailedDeliveryIsRetried(t *testing.T) {
	w, rec := newTestWatcher(t, func() string { return listingJSON("a1") })
	rec.fail = true

	if err := w.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	rec.fail = false
	if err := w.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if rec.count() != 1 {
		t.Fatalf("a post whose delivery failed should be retried, got %d", rec.count())
	}
}

func TestDryRunDoesNotNotifyOrPersist(t *testing.T) {
	w, rec := newTestWatcher(t, func() string { return listingJSON("a1") })
	w.DryRun = true

	if err := w.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if rec.count() != 0 {
		t.Fatalf("a dry run should not notify, got %d", rec.count())
	}
	if !w.store.IsFirstRun() {
		t.Fatal("a dry run should not write state")
	}
}

func TestRunOnceReportsTotalFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusInternalServerError)
	}))
	defer srv.Close()

	cfg := config.Default()
	cfg.StateFile = filepath.Join(t.TempDir(), "state.json")
	cfg.Match = config.Match{AnyOf: []string{"berlin"}, Fields: []string{"title"}}
	m, _ := match.New(cfg.Match)
	st, _ := store.Open(cfg.StateFile, time.Hour)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	w := New(cfg, reddit.New(reddit.Options{BaseURL: srv.URL}), m, st, nil, logger)

	if err := w.RunOnce(context.Background()); err == nil {
		t.Fatal("expected an error when every subreddit fails")
	}
}
