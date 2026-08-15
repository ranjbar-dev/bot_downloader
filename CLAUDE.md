# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Workflow

Default flow for prompts in this project: invoke minimal-brainstorm skill first, ask clarifying questions, understand need, then build plan and present for accept before coding. On accept, clear context then execute plan. Skip this whole flow if prompt says "no planning needed" or "do not plan for me" — go straight to coding.

When job done (build/vet/tests pass), commit changes into git.

## What this is

igsave-bot: self-hosted Telegram bot (Go, single static binary). User sends a link (Instagram, TikTok, YouTube, Pornhub, Spotify), bot shells out to `yt-dlp` (or, for Spotify, resolves the track then downloads matching audio from YouTube via `yt-dlp`), sends media back. YouTube/Pornhub prompt for a quality before downloading. Gated by Telegram channel membership; disk-cached (TTL + size cap, keyed on URL+quality) so repeat requests across all users are served without re-downloading. Deployed via systemd on a small VPS (1 CPU/1GB RAM/30GB disk is the tuned-for profile).

Full design spec: `docs/index.md` (routes to per-topic docs split out of the old `SPEC.md`). Deployment/ops runbook: `README.md`. Read both before changing gate, cache, or config behavior — they're the source of truth, not just docs.

## Commands

```bash
go build -o igsave-bot ./cmd/igsave-bot
go vet ./...
go test ./...
go test ./internal/cache/ -run TestLookupMissThenHitThenExpiry -v   # single test
```

No lint config beyond `go vet`. Requires Go 1.25+ (see `go.mod`).

## Architecture

Everything hangs off one interface, `platform.Provider` (`internal/platform/provider.go`):

```go
type Provider interface {
    Name() string
    Match(u *url.URL) bool
    Download(ctx context.Context, rawURL string, destDir string) ([]MediaFile, error)
}
```

`internal/bot` (the Telegram update loop/handlers) and `internal/cache` (disk cache) only ever talk to `Provider` / `MediaFile` — they are provider-agnostic. Providers registered today (`cmd/igsave-bot/main.go`): Instagram, TikTok, YouTube, Pornhub, Spotify. Adding a yt-dlp-backed platform (Twitter/X, Reddit — anything yt-dlp already extracts) is one `platform.NewYtDlpProvider(name, hosts, ...)` registration line, nothing else. A platform yt-dlp can't reach directly (e.g. Spotify, DRM'd) needs a new `Provider` implementation (`internal/platform/spotify.go`-shaped), registered the same way — still zero changes to `internal/bot` or `internal/cache`. Providers that offer more than one download option (YouTube, Pornhub) also implement `platform.QualityProvider` (`Qualities()`/`DownloadWithQuality`, see `internal/platform/ytdlp.go`); `internal/bot` type-asserts for it and shows an inline quality-picker before enqueuing. See `docs/extending-platforms.md` for the full walkthrough.

Request flow (`docs/flow.md` has the full diagram): incoming message → extract/validate URL → gate check (channel membership) → per-user rate limit → quality prompt if the matched provider is a `QualityProvider` (blocks on a button tap) → cache lookup (`sha256(url + "|" + quality)` key) → on miss, enqueue to bounded worker pool → `yt-dlp` shells out via `internal/platform/ytdlp.go` (or `internal/platform/spotify.go` for Spotify) → cache marks entry done (`.done` marker file, its mtime is the TTL clock) → send via `sendVideo`/`sendPhoto`/`sendMediaGroup`.

Key invariants worth knowing before touching this code:
- **No shell interpolation** of user input anywhere — `yt-dlp` is invoked via `exec.Command` with an argument slice, never a shell string. This is the one real injection surface (`docs/errors-security.md` §14); don't introduce `sh -c` or string-built commands.
- **Gate membership is realtime, not polled** (`internal/bot/gate.go`, `internal/store/store.go`): a `chat_member` Telegram update fires on every join/leave/kick in the gate channel and is upserted into SQLite immediately (`bot.go`'s `handleChatMember`), so `gate.allowed` reads that table instead of calling `GetChatMember` per message. Leaving the channel takes effect on the user's very next message — no cache staleness window. `GetChatMember` is only called as a one-time bootstrap for a user ID the store has never seen an event for (e.g. they joined before the bot started listening), and that result is written back to the store so it isn't asked again.
- **Cache eviction never races an in-flight request**: both TTL and size-cap eviction (`internal/cache/cache.go`) take the same per-key lock (`keyedLock`, reference-counted) that a download/send holds, so a sweep can't delete out from under an active request.
- **Cache metadata is still filesystem-only** — `.done` marker mtimes under `CACHE_DIR/<sha256(url+quality)>/`, no DB. SQLite is scoped to channel membership only (see below); don't route cache metadata through it.
- Config (`internal/config/config.go`) is plain `os.Getenv` + small helpers — no config framework, no viper/env struct tags. Keep new env vars consistent with the existing `getEnvDefault`/`getEnvIntDefault` pattern.
- **`TELEGRAM_BOT_API_URL` switches two things at once** (`internal/bot/bot.go`): the API base URL (via `BotOpts.BotClient`, with a 15-minute request timeout instead of gotgbot's 5s default) *and* how media is sent — `mediaInput` hands the local server an absolute file path instead of streaming the file as multipart, so nothing buffers in memory. Empty = public API, unchanged behavior. Keep both halves in sync if you touch either; sending a path to `api.telegram.org` would silently be interpreted as a `file_id`. Runbook: README → "Local Bot API server".
- **Every client-facing message (replies, errors, captions) leads with an emoji.** Match existing tone: 🔒 gate/join, ❌ errors, ⬇️/📥 download/send status, ✅ success, ⚠️ transient failure, 🚫 unsupported input, 🎚 quality prompt.

## SQLite (channel membership)

`internal/store/store.go` holds a single table, `members(user_id, status, updated_at)`, backed by `modernc.org/sqlite` — a pure-Go driver (no cgo), so the single-static-binary build stays intact (`CGO_ENABLED=0` still works).

- **Source of truth, kept live by events**: `internal/bot/bot.go` requests `chat_member` in `PollingOpts.GetUpdatesOpts.AllowedUpdates` and registers `handlers.NewChatMember(chatmember.ChatId(cfg.GateChannel), bot.handleChatMember)`. Every join/leave/kick/promotion in the gate channel calls `store.Upsert(userID, status, updateDate)`, which only applies if `updateDate >= last stored updated_at` (out-of-order delivery can't regress a newer status).
- **Read path**: `gate.allowed` calls `store.Status(userID)`. Known → trust it, no API call. Unknown (never saw an event for this user) → one-time live `GetChatMember` call, result written back with `store.Upsert`, so it's answered from SQLite from then on.
- **DB file**: `DB_PATH` env var, default `/var/lib/igsave-bot/members.db` (see `internal/config/config.go`). `main.go` creates the parent dir and opens the store before starting the bot; `store.Open` creates the `members` table if missing — no migration tooling, it's one `CREATE TABLE IF NOT EXISTS`.
- **Concurrency**: `db.SetMaxOpenConns(1)` — SQLite allows one writer at a time, and upserts happen from both the poll loop's chat_member handler and a message handler's bootstrap path, so serializing through one connection avoids `SQLITE_BUSY` rather than adding retry logic.
- **Caveat**: `chat_member` events only exist from the moment the bot starts listening for them; membership for users who joined earlier is filled in lazily via the bootstrap path on their first message, not backfilled up front.

## Repo layout

```
cmd/igsave-bot/                      main.go — wiring only, registers providers, opens the member store
internal/bot/                        update loop, handlers, gate check (SQLite-backed), rate limit
internal/platform/                   Provider/QualityProvider interfaces, registry, yt-dlp-backed provider, Spotify provider
internal/cache/                      disk cache: TTL + size-cap eviction, per-key locking (filesystem, not SQLite)
internal/store/                      SQLite-backed channel membership table, fed by chat_member events
internal/config/                     env config loading
igsave-bot.service, igsave-bot-ytdlp-update.service/.timer   systemd units (committed at repo root)
telegram-bot-api.service             optional local Bot API server unit (lifts the 50MB upload cap to ~2GB)
```
