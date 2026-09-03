# CLAUDE.md

Guidance for Claude Code working in this repository.

## What this is

`redditwatch` polls subreddit listings, matches new posts against configured
terms, and delivers the matches. Everything about *what* is being watched is
configuration — subreddits, terms, fields, interval, notifiers — so resist
hard-coding any particular subreddit or keyword into the packages.

## Commands

```sh
make test        # go test ./...
make lint        # gofmt -l . && go vet ./...
make build       # -> ./redditwatch
make dry-run     # one poll, printing matches, no delivery and no state write
make docker      # build the image
```

Run `make lint` and `make test` before every commit; both are fast.

## Layout and flow

```
cmd/redditwatch   flags, logger, wiring
internal/config   file + env + defaults, then Validate
internal/reddit   listing client (OAuth or public JSON), Post type
internal/match    all_of / any_of / none_of over selected post fields
internal/store    seen-post IDs, atomically persisted
internal/notify   Notifier interface: log, Telegram, webhook
internal/watcher  the poll loop tying the above together
```

One poll: `watcher.RunOnce` → `reddit.Client.Listing` per subreddit → skip IDs
already in the store → `match.Matcher.Match` → deliver through every notifier →
mark seen → `store.Save`.

## Invariants worth preserving

These encode decisions that are easy to undo by accident:

- **No third-party dependencies.** `go.mod` has none. Adding one is a
  deliberate call, not a convenience — ask first.
- **Config precedence is file → env → defaults → validate.** Env wins over the
  file (`applyEnv` runs before `applyDefaults`). `Load` uses
  `DisallowUnknownFields`, so a typo in a user's config is an error rather than
  a silently ignored setting — which also means **every new config field needs
  adding to `config.example.json`** (a test loads it) and to the README table.
- **A post is marked seen only after every notifier accepted it.** A failed
  delivery must retry on the next poll, not be lost.
- **A fresh state file seeds silently** unless `notify_on_first_run` is set, so
  a cold start doesn't replay the backlog into someone's phone.
- **The poll loop never exits on a fetch error.** `RunOnce` returns an error
  only when *every* subreddit failed; the loop logs and waits for the next
  tick. Rate limits and 5xx come back as `*reddit.RetryableError`.
- **Match terms are literal text, not patterns** — they go through
  `regexp.QuoteMeta`. `\b` in Go's regexp is ASCII-only, hence the
  `leadingWord`/`trailingWord` guards that keep boundaries off terms starting
  or ending with punctuation or non-ASCII letters.
- **Fields are joined with newlines** before matching, so a term can never span
  a title/body boundary.
- **`store.Save` writes via temp file + rename.** Don't replace it with a plain
  `os.WriteFile`; a crash mid-write would truncate the state.

## Testing

All tests are hermetic: `httptest` servers stand in for Reddit, Telegram and
webhook endpoints. `reddit.Options.BaseURL` / `TokenURL` exist for exactly that
(and for proxies) — use them rather than reaching for a global.

Add a new notifier by implementing `notify.Notifier` and registering it in
`notify.Build`; the watcher needs no changes.

## Environment limits in Claude Code web sessions

- `www.reddit.com` is blocked by the sandbox network policy (the proxy answers
  403 to CONNECT), so a live end-to-end run is not possible here. Don't chase
  it as a bug; verify against the fake servers and say what stayed unverified.
- There is no Docker daemon, so `Dockerfile` changes can't be built here.

## Git

Work on the session's designated `claude/*` branch and push there. Never push
to `main`.
