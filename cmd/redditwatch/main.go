// Command redditwatch polls subreddits for new posts matching configured
// terms and reports the ones that match.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/ItsSosha/reddit-scrapper/internal/config"
	"github.com/ItsSosha/reddit-scrapper/internal/match"
	"github.com/ItsSosha/reddit-scrapper/internal/notify"
	"github.com/ItsSosha/reddit-scrapper/internal/reddit"
	"github.com/ItsSosha/reddit-scrapper/internal/store"
	"github.com/ItsSosha/reddit-scrapper/internal/watcher"
)

func main() {
	var (
		configPath = flag.String("config", "", "path to a JSON config file (defaults are used when empty)")
		once       = flag.Bool("once", false, "poll a single time and exit")
		dryRun     = flag.Bool("dry-run", false, "print matches without notifying or writing state")
		verbose    = flag.Bool("v", false, "verbose (debug) logging")
	)
	flag.Parse()

	if err := run(*configPath, *once, *dryRun, *verbose); err != nil {
		if errors.Is(err, context.Canceled) {
			return
		}
		fmt.Fprintf(os.Stderr, "redditwatch: %v\n", err)
		os.Exit(1)
	}
}

func run(configPath string, once, dryRun, verbose bool) error {
	level := slog.LevelInfo
	if verbose {
		level = slog.LevelDebug
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))

	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}

	matcher, err := match.New(cfg.Match)
	if err != nil {
		return err
	}
	st, err := store.Open(cfg.StateFile, cfg.StateRetention.D())
	if err != nil {
		return err
	}
	client := reddit.New(reddit.Options{
		ClientID:     cfg.Reddit.ClientID,
		ClientSecret: cfg.Reddit.ClientSecret,
		Username:     cfg.Reddit.Username,
		Password:     cfg.Reddit.Password,
		UserAgent:    cfg.Reddit.UserAgent,
	})
	notifiers := notify.Build(cfg.Notifier, logger, nil)

	names := make([]string, 0, len(notifiers))
	for _, n := range notifiers {
		names = append(names, n.Name())
	}
	logger.Info("starting",
		"subreddits", strings.Join(cfg.Subreddits, ","),
		"listing", cfg.Listing,
		"interval", cfg.PollInterval.D().String(),
		"authenticated", client.Authenticated(),
		"notifiers", strings.Join(names, ","),
		"state", cfg.StateFile,
		"known_posts", st.Len(),
	)
	if !client.Authenticated() {
		logger.Warn("no reddit credentials: using the public JSON API, which is rate limited and often blocked from cloud IPs")
	}
	if st.IsFirstRun() && !cfg.NotifyOnFirstRun && !dryRun {
		logger.Info("no state file yet: first poll seeds the backlog without notifying")
	}

	w := watcher.New(cfg, client, matcher, st, notifiers, logger)
	w.DryRun = dryRun

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if once {
		return w.RunOnce(ctx)
	}
	err = w.Run(ctx)
	if errors.Is(err, context.Canceled) {
		logger.Info("shutting down")
		return nil
	}
	return err
}
