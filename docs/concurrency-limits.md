# Concurrency, Rate Limiting & Upload Constraints

Source: SPEC.md §8-9.

## 8. Concurrency & rate limiting

- Bounded worker pool (buffered channel, default 3 workers via `WORKER_COUNT`) — VPS likely has limited CPU/bandwidth; don't spawn unbounded yt-dlp processes.
- Per-user rate limit: 5 downloads / 10 min, in-memory fixed-window counter keyed by Telegram user ID. Cache hits still count against this limit (keeps the limit meaningful as a per-user request cap, not just a download cap).
- If the job queue is full, reply "busy, try again shortly" instead of blocking indefinitely.

## 9. Telegram upload constraints

- Bot API upload limit: **50 MB** via `sendVideo`/`sendPhoto`/`sendDocument` on the public Bot API server (`TELEGRAM_MAX_UPLOAD_MB` default `50`).
- If a downloaded file would exceed that, `yt-dlp --max-filesize` aborts the download before it completes, and the bot replies with the "too large" error rather than downloading something it can't send.
- `sendMediaGroup` for carousels: max 10 items per group (bot chunks automatically), all photos+videos mixed is allowed, mixed with documents is not.

### Raising the ceiling: local Bot API server

Set `TELEGRAM_BOT_API_URL` (e.g. `http://127.0.0.1:8081`) and the ceiling becomes **2000 MB**. Setup runbook: README → "Local Bot API server". Two things change in the bot when that var is non-empty (`internal/bot/bot.go`):

1. **API base URL.** `gotgbot.NewBot` gets a `BaseBotClient` with `RequestOpts.APIURL` pointing at the local server, and a 15-minute request timeout — the default 5s covers a 50MB multipart upload, not a 2GB transfer that the local server is still pushing to Telegram while our POST hangs open.
2. **Files are handed over by path, not uploaded.** `mediaInput` returns a `file://` URI as the field value instead of opening the file and streaming it as multipart. The URI scheme is required: a bare absolute path reaches the server's URL parser and comes back as `Bad Request: invalid file HTTP URL specified: URL host is empty`. Nothing buffers the media in the bot's memory, which is the whole point on a small VPS — a 2GB send costs the bot process roughly nothing.

Consequences worth knowing:

- The server process must be able to **read `CACHE_DIR`**. `telegram-bot-api.service` runs as the same `igsave-bot` user for exactly this reason.
- `JOB_TIMEOUT_SECONDS` must grow too, or yt-dlp gets cancelled long before a 2GB download completes.
- Cache disk pressure scales with the new ceiling: at 2000 MB a `CACHE_MAX_MB=10000` cache holds ~5 entries. Eviction still works, the hit rate just drops.
- The local server keeps its own state under `/var/lib/telegram-bot-api` and does not release RAM after a transfer (tdlib/telegram-bot-api#514, #645) — hence `MemoryMax` + `Restart=always` in the unit.
