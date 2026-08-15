# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

igsave-bot: self-hosted Telegram bot (Go, single static binary). User sends an Instagram link, bot shells out to `yt-dlp`, sends media back. Gated by Telegram channel membership; disk-cached (TTL + size cap) so repeat links across all users are served without re-downloading. Deployed via systemd on a small VPS (1 CPU/1GB RAM/30GB disk is the tuned-for profile).

Full design spec: `SPEC.md`. Deployment/ops runbook: `README.md`. Read both before changing gate, cache, or config behavior — they're the source of truth, not just docs.

## Commands

```bash
go build -o igsave-bot ./cmd/igsave-bot
go vet ./...
go test ./...
go test ./internal/cache/ -run TestLookupMissThenHitThenExpiry -v   # single test
```

No lint config beyond `go vet`. Requires Go 1.24+ (see `go.mod`).

## Architecture

Everything hangs off one interface, `platform.Provider` (`internal/platform/provider.go`):

```go
type Provider interface {
    Name() string
    Match(u *url.URL) bool
    Download(ctx context.Context, rawURL string, destDir string) ([]MediaFile, error)
}
```

`internal/bot` (the Telegram update loop/handlers) and `internal/cache` (disk cache) only ever talk to `Provider` / `MediaFile` — they are provider-agnostic. "Instagram" as a string appears exactly once in the codebase: the registration call in `cmd/igsave-bot/main.go`. Adding a yt-dlp-backed platform (YouTube, TikTok, Twitter/X — anything yt-dlp already extracts) is one `platform.NewYtDlpProvider(name, hosts, ...)` registration line, nothing else. A platform yt-dlp can't reach (e.g. Spotify) needs a new `Provider` implementation (`internal/platform/spotify.go`-shaped), registered the same way — still zero changes to `internal/bot` or `internal/cache`. See SPEC.md §16 for the full walkthrough.

Request flow (SPEC.md §3 has the full diagram): incoming message → extract/validate IG URL → gate check (channel membership) → per-user rate limit → cache lookup (`sha256(url)` key) → on miss, enqueue to bounded worker pool → `yt-dlp` shells out via `internal/platform/ytdlp.go` → cache marks entry done (`.done` marker file, its mtime is the TTL clock) → send via `sendVideo`/`sendPhoto`/`sendMediaGroup`.

Key invariants worth knowing before touching this code:
- **No shell interpolation** of user input anywhere — `yt-dlp` is invoked via `exec.Command` with an argument slice, never a shell string. This is the one real injection surface (SPEC.md §14); don't introduce `sh -c` or string-built commands.
- **Gate caching is asymmetric** (`internal/bot/gate.go`): a positive membership result is cached 5 minutes; a negative one is *never* cached, so joining the channel takes effect on the user's very next message instead of waiting out a stale cache entry. Don't "fix" this into a symmetric cache — it's intentional.
- **Cache eviction never races an in-flight request**: both TTL and size-cap eviction (`internal/cache/cache.go`) take the same per-key lock (`keyedLock`, reference-counted) that a download/send holds, so a sweep can't delete out from under an active request.
- **Filesystem is the index** — no DB. Cache metadata is just `.done` marker mtimes under `CACHE_DIR/<sha256(url)>/`. Don't add a database for this; SPEC.md §2 explicitly scopes it out.
- Config (`internal/config/config.go`) is plain `os.Getenv` + small helpers — no config framework, no viper/env struct tags. Keep new env vars consistent with the existing `getEnvDefault`/`getEnvIntDefault` pattern.
- **Every client-facing message (replies, errors, captions) leads with an emoji.** Match existing tone: 🔒 gate/join, ❌ errors, ⬇️/📥 download/send status, ✅ success, ⚠️ transient failure, 🚫 unsupported input.

## Repo layout

```
cmd/igsave-bot/                      main.go — wiring only, registers providers
internal/bot/                        update loop, handlers, gate check, rate limit
internal/platform/                   Provider interface, registry, yt-dlp-backed provider
internal/cache/                      disk cache: TTL + size-cap eviction, per-key locking
internal/config/                     env config loading
igsave-bot.service, igsave-bot-ytdlp-update.service/.timer   systemd units (committed at repo root)
```
