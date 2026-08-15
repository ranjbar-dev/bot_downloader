# Configuration

Source: SPEC.md §11.

Env vars (loaded via `config.env`, plain `os.Getenv` + a small loader — no config framework needed):

| Var | Required | Example | Purpose |
|---|---|---|---|
| `BOT_TOKEN` | yes | `123456:ABC-...` | Telegram bot token |
| `GATE_CHANNEL` | yes | `-1001234567890` | Numeric channel ID users must join |
| `GATE_CHANNEL_INVITE_LINK` | yes | `https://t.me/mychannel` | Used in the "Join Channel" button |
| `CACHE_DIR` | no (default `/var/lib/igsave-bot/cache`) | | Persistent media cache root |
| `CACHE_TTL_SECONDS` | no (default `3600`) | | How long a cached download stays reusable |
| `CACHE_MAX_MB` | no (default `10000`) | | Hard cap on cache disk usage; `0` = unlimited (TTL-only) |
| `YT_DLP_PATH` | no (default `yt-dlp` from `PATH`) | | Override binary location |
| `TELEGRAM_MAX_UPLOAD_MB` | no (default `50`) | | Set to `2000` only if a local Bot API server is running |
| `WORKER_COUNT` | no (default `2`) | | Concurrent download workers — set to `1` on a single-CPU VPS |
| `JOB_TIMEOUT_SECONDS` | no (default `120`) | | Per-download timeout |

Note: `DB_PATH` (SQLite membership store) was added after this spec section was written — see `CLAUDE.md` → "SQLite (channel membership)" and `internal/config/config.go` for the current default and behavior.
