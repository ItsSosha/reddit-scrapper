# reddit-scrapper

A small Go service that polls subreddits for new posts matching configured
terms and reports the matches. It was built to catch Berlin ticket sales in
[r/fontainesdc](https://www.reddit.com/r/fontainesdc/), but the subreddit and
the search terms are configuration, not code.

## Quick start

```sh
cp config.example.json config.json
make build

# See what the current listing would match, without notifying or saving state.
./redditwatch -config config.json -once -dry-run -v

# Run continuously.
./redditwatch -config config.json
```

Without credentials the tool uses the public `www.reddit.com/r/<sub>/new.json`
endpoint. That works from a home connection but is rate limited and frequently
returns `403` from cloud IPs, so for anything long-running create a Reddit app
(https://www.reddit.com/prefs/apps, type "script") and export:

```sh
export REDDIT_CLIENT_ID=...
export REDDIT_CLIENT_SECRET=...
export REDDIT_USER_AGENT='go:reddit-scrapper:0.1.0 (by /u/your_username)'
# Optional: a username/password pair switches to the "script" password grant.
export REDDIT_USERNAME=...
export REDDIT_PASSWORD=...
```

## Configuration

`config.example.json` documents every field. The important ones:

| Field | Meaning |
| --- | --- |
| `subreddits` | Subreddits to poll, `r/` prefix optional. |
| `listing` | `new` (default), `hot` or `rising`. |
| `poll_interval` | Time between rounds, e.g. `"60s"`. Minimum 5s. |
| `limit` | Posts fetched per poll, 1-100. |
| `state_file` | Where seen post IDs are stored so restarts don't re-alert. |
| `notify_on_first_run` | When false (default), the first poll on a fresh state file records the backlog silently. |
| `match.all_of` | Every term must appear. |
| `match.any_of` | At least one term must appear (when non-empty). |
| `match.none_of` | No term may appear. |
| `match.fields` | Which fields to search: `title`, `selftext`, `flair`, `author`, `url`. |
| `match.whole_word` | Require word boundaries, so `ticket` doesn't match `ticketing`. |

Terms are literal text, not regular expressions, and matching is
case-insensitive unless `case_sensitive` is set. Fields are searched
individually, so a term never matches across a title/body boundary.

The default match — `all_of: ["berlin"]` plus a ticket-word `any_of`, minus an
`none_of` of buyer phrases — reports "spare Berlin ticket" and skips both
"Berlin setlist thread" and "looking for a Berlin ticket".

### Environment overrides

Every credential and the common knobs can come from the environment, which is
useful in containers and systemd units:

`SUBREDDITS`, `MATCH_ALL_OF`, `MATCH_ANY_OF`, `MATCH_NONE_OF` (comma-separated),
`POLL_INTERVAL`, `LIMIT`, `STATE_FILE`, `REDDIT_CLIENT_ID`,
`REDDIT_CLIENT_SECRET`, `REDDIT_USERNAME`, `REDDIT_PASSWORD`,
`REDDIT_USER_AGENT`, `TELEGRAM_BOT_TOKEN`, `TELEGRAM_CHAT_ID`, `WEBHOOK_URL`.

Running with no config file at all uses the built-in defaults (r/fontainesdc,
Berlin ticket terms), so `SUBREDDITS=... MATCH_ALL_OF=... ./redditwatch` is a
complete configuration on its own.

## Notifications

Matches are always written to the structured log. A post is only marked as seen
once every notifier accepted it, so a failed delivery is retried on the next
poll rather than being lost.

### Telegram

1. Message [@BotFather](https://t.me/BotFather), send `/newbot`, and copy the
   token it gives you.
2. Send your new bot any message (a bot cannot start a conversation with you).
3. Open `https://api.telegram.org/bot<TOKEN>/getUpdates` and read
   `result[0].message.chat.id`.
4. Export both values; the notifier enables itself when they are present:

```sh
export TELEGRAM_BOT_TOKEN=123456:ABC...
export TELEGRAM_CHAT_ID=987654321
```

### Webhook

Setting `WEBHOOK_URL` POSTs a JSON body describing the match (post fields, the
matched terms, and a prerendered `text`) to that URL — useful for a Discord or
Slack relay, or your own script.

## Docker

```sh
cp config.example.json config.json   # your match rules
cp .env.example .env                 # your credentials
docker compose up -d --build
docker compose logs -f
```

Both files must exist before the first `up`: Docker creates a *directory* in
place of a missing bind-mounted file, and the container then fails to start.

State lives in the named `state` volume mounted at `/data`, so restarts and
image rebuilds don't replay the backlog. The image is a distroless static
build running as `nonroot`; `make docker` builds it without compose.

To run purely on environment variables, delete the `config.json` mount and the
`command:` line from `docker-compose.yml` — the built-in defaults are exactly
this Berlin ticket watch.

## Flags

| Flag | Effect |
| --- | --- |
| `-config` | Path to a JSON config file. Defaults are used when omitted. |
| `-once` | Poll a single time and exit (handy under cron). |
| `-dry-run` | Print matches without notifying or writing state. |
| `-v` | Debug logging, including per-poll counts. |

## Layout

```
cmd/redditwatch      entrypoint, flags, wiring
internal/config      config file, env overrides, validation
internal/reddit      OAuth + public listing client
internal/match       term matching rules
internal/store       seen-post state, atomically persisted
internal/notify      log / Telegram / webhook delivery
internal/watcher     the poll loop
```

## Development

```sh
make test
make lint
```

The project has no third-party dependencies.
