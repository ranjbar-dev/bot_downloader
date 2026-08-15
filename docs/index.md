# Docs Index

Split from the original `SPEC.md`. Each file below is scoped to one concern — read only the ones relevant to what you're touching, not the whole set every time.

| Doc | Covers | Read/use when... |
|---|---|---|
| [overview.md](overview.md) | Purpose, in/out of scope for v1 | Deciding whether a feature request fits v1 or is explicitly out of scope |
| [flow.md](flow.md) | End-to-end request flow, message → send | Tracing a bug through the pipeline, or adding a new step to the request path |
| [telegram-gate.md](telegram-gate.md) | Telegram bot wiring, channel-membership gate | Touching `internal/bot`'s update handlers, `/start`, or gate/membership logic. **Note**: gate design has moved on from the spec text — see the note at the top of the file and `CLAUDE.md`'s SQLite section for current behavior |
| [download-cache.md](download-cache.md) | yt-dlp shell-out, disk cache (key/TTL/eviction/locking) | Touching `internal/platform/ytdlp.go` or `internal/cache` — cache invariants (per-key locking, TTL vs size-cap eviction) are load-bearing, don't change without reading this |
| [concurrency-limits.md](concurrency-limits.md) | Worker pool, per-user rate limiting, Telegram upload size limits | Changing `WORKER_COUNT` behavior, rate-limit windows, or anything touching `TELEGRAM_MAX_UPLOAD_MB` |
| [limitations.md](limitations.md) | Known v1 limitations (stories, IG extractor breakage, cache-key dedup) | Deciding whether odd behavior is a known limitation vs. an actual bug before spending time debugging it |
| [config.md](config.md) | Full env var reference | Adding/changing a config value — keep `internal/config/config.go` and this table in sync |
| [deployment.md](deployment.md) | systemd units, VPS sizing, ops notes | Changing the systemd unit files at repo root, or anything about how the bot runs in production |
| [errors-security.md](errors-security.md) | User-facing error message table, security invariants | Adding a new failure path (needs a table entry) or touching anything security-sensitive (shell exec, secrets, filesystem permissions) |
| [extending-platforms.md](extending-platforms.md) | Go deps, `Provider` interface, how to add a new platform (YouTube/TikTok/Spotify/...) | Adding support for a new site — read this before writing any code, it's a one-line change for anything yt-dlp already supports |

## When to read `SPEC.md` history vs. `CLAUDE.md`

`CLAUDE.md` (repo root) is the higher-level, currently-accurate guide — architecture, invariants, repo layout — and takes precedence where it conflicts with these docs (e.g. the SQLite membership store post-dates the original spec text). These docs are the detailed spec: read them for the *why* and the full design rationale behind an area CLAUDE.md only summarizes.
