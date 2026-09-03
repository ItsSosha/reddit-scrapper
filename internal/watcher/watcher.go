// Package watcher polls subreddits and reports matching posts.
package watcher

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/ItsSosha/reddit-scrapper/internal/config"
	"github.com/ItsSosha/reddit-scrapper/internal/match"
	"github.com/ItsSosha/reddit-scrapper/internal/notify"
	"github.com/ItsSosha/reddit-scrapper/internal/reddit"
	"github.com/ItsSosha/reddit-scrapper/internal/store"
)

// Watcher ties together the client, matcher, state store and notifiers.
type Watcher struct {
	cfg       config.Config
	client    *reddit.Client
	matcher   *match.Matcher
	store     *store.Store
	notifiers []notify.Notifier
	logger    *slog.Logger

	// DryRun suppresses delivery and state writes; useful for tuning terms
	// against the current listing.
	DryRun bool
}

// New builds a Watcher.
func New(cfg config.Config, c *reddit.Client, m *match.Matcher, s *store.Store, n []notify.Notifier, logger *slog.Logger) *Watcher {
	return &Watcher{cfg: cfg, client: c, matcher: m, store: s, notifiers: n, logger: logger}
}

// Run polls once immediately, then on every tick until ctx is cancelled.
// Poll errors are logged and retried on the next tick rather than aborting the
// loop: a watcher that exits on the first 429 is useless.
func (w *Watcher) Run(ctx context.Context) error {
	ticker := time.NewTicker(w.cfg.PollInterval.D())
	defer ticker.Stop()

	if err := w.RunOnce(ctx); err != nil && ctx.Err() == nil {
		w.logger.Error("poll failed", "err", err)
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := w.RunOnce(ctx); err != nil && ctx.Err() == nil {
				w.logger.Error("poll failed", "err", err)
			}
		}
	}
}

// RunOnce polls every configured subreddit once. It returns an error only when
// every subreddit failed, so one dead subreddit does not mask the others.
func (w *Watcher) RunOnce(ctx context.Context) error {
	var (
		errs     []error
		failures int
	)
	// A dry run never seeds: the point is to see what the current listing
	// would have matched.
	seeding := w.store.IsFirstRun() && !w.cfg.NotifyOnFirstRun && !w.DryRun

	for _, sub := range w.cfg.Subreddits {
		if err := ctx.Err(); err != nil {
			return err
		}
		posts, err := w.client.Listing(ctx, sub, w.cfg.Listing, w.cfg.Limit)
		if err != nil {
			failures++
			errs = append(errs, err)
			var re *reddit.RetryableError
			if errors.As(err, &re) {
				w.logger.Warn("transient fetch error", "subreddit", sub, "err", err, "retry_after", re.RetryAfter)
				if re.RetryAfter > 0 {
					w.sleep(ctx, re.RetryAfter)
				}
			} else {
				w.logger.Error("fetch failed", "subreddit", sub, "err", err)
			}
			continue
		}
		w.handle(ctx, sub, posts, seeding)
	}

	if !w.DryRun {
		if err := w.store.Save(); err != nil {
			w.logger.Error("save state failed", "err", err)
		}
	}
	if failures == len(w.cfg.Subreddits) && failures > 0 {
		return errors.Join(errs...)
	}
	return nil
}

// handle processes one subreddit's listing. On the very first run with no
// state file, matches are recorded but not delivered so a cold start does not
// replay the backlog.
func (w *Watcher) handle(ctx context.Context, sub string, posts []reddit.Post, seeding bool) {
	var checked, matched int
	for _, p := range posts {
		key := p.Subreddit + "/" + p.ID
		if key == "/" || w.store.Seen(key) {
			continue
		}
		checked++
		res := w.matcher.Match(p)
		if !res.Matched {
			w.store.Mark(key)
			continue
		}
		matched++

		if seeding {
			w.logger.Debug("seeding state, not notifying", "post", p.Permalink)
			w.store.Mark(key)
			continue
		}
		if w.DryRun {
			w.logger.Info("dry-run match", "title", p.Title, "url", p.Permalink, "terms", res.Terms)
			continue
		}

		event := notify.Event{Post: p, Terms: res.Terms}
		delivered := true
		for _, n := range w.notifiers {
			if err := n.Notify(ctx, event); err != nil {
				delivered = false
				w.logger.Error("notify failed", "notifier", n.Name(), "post", p.Permalink, "err", err)
			}
		}
		// Only remember the post once every notifier accepted it, so a failed
		// delivery is retried on the next poll instead of being lost.
		if delivered {
			w.store.Mark(key)
		}
	}
	w.logger.Debug("polled", "subreddit", sub, "fetched", len(posts), "new", checked, "matched", matched)
}

func (w *Watcher) sleep(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}
