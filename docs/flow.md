# Request Flow

Source: SPEC.md §3.

```
User (in Telegram) --> sends message containing instagram.com link
        |
        v
  igsave-bot receives update (long polling)
        |
        v
  Extract + validate IG URL from message text (regex)
        |-- no valid link found --> ignore / reply usage hint
        v
  Gate check: is sender a member of GATE_CHANNEL?
        |-- no  --> reply "You need to join our channel to use this bot"
        |           with an inline button linking to the channel, stop
        |-- yes --> continue
        v
  Rate limit check (per user)
        |-- exceeded --> reply "slow down", stop
        v
  Cache lookup: sha256(raw URL) as key
        |-- fresh entry on disk (< CACHE_TTL_SECONDS old) --> skip straight to Send
        |-- miss / expired --> continue
        v
  Enqueue download job (bounded worker pool)
        |
        v
  Run: yt-dlp --no-playlist -o <cache_dir>/<key>/%(id)s.%(ext)s <url>
        |-- yt-dlp exit != 0 --> reply friendly error, discard partial cache entry, stop
        v
  Inspect output file(s): size, type (video/image), count (carousel = multiple files)
        |-- any file > TELEGRAM_MAX_UPLOAD_MB --> reply "file too large to send via Telegram", stop
        v
  Mark cache entry done (write .done marker, sets the TTL clock)
        |
        v
  Send:
    - video  -> sendVideo
    - photo  -> sendPhoto
    - carousel (>1 file) -> sendMediaGroup (chunks of <=10)
        |
        v
  Done. Files stay on disk until CACHE_TTL_SECONDS elapses; a background
  sweeper deletes expired entries. No per-request cleanup — that's the point
  of the cache.
```

Gate check detail: [telegram-gate.md](telegram-gate.md). Cache detail: [download-cache.md](download-cache.md). Worker pool / rate limit detail: [concurrency-limits.md](concurrency-limits.md).
