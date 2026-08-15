# Concurrency, Rate Limiting & Upload Constraints

Source: SPEC.md §8-9.

## 8. Concurrency & rate limiting

- Bounded worker pool (buffered channel, default 3 workers via `WORKER_COUNT`) — VPS likely has limited CPU/bandwidth; don't spawn unbounded yt-dlp processes.
- Per-user rate limit: 5 downloads / 10 min, in-memory fixed-window counter keyed by Telegram user ID. Cache hits still count against this limit (keeps the limit meaningful as a per-user request cap, not just a download cap).
- If the job queue is full, reply "busy, try again shortly" instead of blocking indefinitely.

## 9. Telegram upload constraints

- Bot API upload limit: **50 MB** via `sendVideo`/`sendPhoto`/`sendDocument` on the public Bot API server (`TELEGRAM_MAX_UPLOAD_MB` default `50`).
- If a downloaded file would exceed that, `yt-dlp --max-filesize` aborts the download before it completes, and the bot replies with the "too large" error rather than downloading something it can't send.
- Options to raise the ceiling later (not implemented in v1, documented for when it's needed):
  1. Run a **local Bot API server** (`telegram-bot-api` binary, self-hosted) — raises the limit to 2000 MB. Adds a second process to operate on the VPS.
- `sendMediaGroup` for carousels: max 10 items per group (bot chunks automatically), all photos+videos mixed is allowed, mixed with documents is not.
