# Dependencies & Adding a New Platform

Source: SPEC.md §15-16.

## 15. Suggested Go dependencies

- `github.com/PaulSonOfLars/gotgbot/v2` — Telegram Bot API bindings + dispatcher.
- Standard library only for everything else: `os/exec`, `context`, `net/url`, `time`, `os`, `crypto/sha256`. No DB driver, ORM, or config framework — env vars + stdlib cover v1 fully. (`modernc.org/sqlite` was added afterward for channel-membership persistence only — see `CLAUDE.md` → "SQLite (channel membership)".)

## 16. Adding a new platform

The bot is source-agnostic by design: `internal/bot` only ever talks to `platform.Provider` and `platform.MediaFile` (and, for persistence, `internal/cache`, which is likewise provider-agnostic — it just caches whatever files a `Provider.Download` produced). "Instagram" as a name appears exactly once in the whole codebase: `cmd/igsave-bot/main.go`, at registration time.

```go
// internal/platform/provider.go
type Provider interface {
    Name() string
    Match(u *url.URL) bool
    Download(ctx context.Context, rawURL string, destDir string) ([]MediaFile, error)
}

type Registry struct{ providers []Provider }
func (r *Registry) Find(rawURL string) (Provider, error) // first Match() wins
```

`internal/bot` resolves whichever provider matches the pasted URL, checks/populates the cache, and sends back whatever `MediaFile`s came out of it (video/photo/audio) — same code path regardless of source.

**Case A — site already supported by yt-dlp** (YouTube, TikTok, Twitter/X, Reddit, ...): no new code at all. `internal/platform/ytdlp.go`'s `YtDlpProvider` is generic — name + host list, shells out to yt-dlp the same way for every site. Adding YouTube later is:

```go
// cmd/igsave-bot/main.go
registry := platform.NewRegistry(
    platform.NewYtDlpProvider("instagram", []string{"instagram.com", "www.instagram.com", "instagr.am"}, cfg.YtDlpPath, cfg.MaxUploadMB),
    platform.NewYtDlpProvider("youtube", []string{"youtube.com", "www.youtube.com", "youtu.be"}, cfg.YtDlpPath, cfg.MaxUploadMB),
    platform.NewYtDlpProvider("tiktok", []string{"tiktok.com", "www.tiktok.com", "vm.tiktok.com"}, cfg.YtDlpPath, cfg.MaxUploadMB),
)
```

One line per platform. `Registry.Find` checks providers in order, so put more specific host matches before broader ones if two ever overlap.

**Case B — site yt-dlp can't reach** (Spotify: DRM-protected streams, yt-dlp has no extractor for it): needs a real new `Provider` implementation backed by a different tool, e.g. `spotdl`. Same shape as `YtDlpProvider` — implement `Name`/`Match`/`Download`, shell out with an argument slice (never a shell string, per [download-cache.md](download-cache.md) §6), return `MediaFile{Kind: KindAudio}`. Drop it into `internal/platform/spotify.go`, register it in `main.go`, done — `internal/bot` and `internal/cache` need zero changes.

**What stays fixed as platforms are added:** the gate check, rate limiter, worker pool, disk cache, and Telegram send logic ([telegram-gate.md](telegram-gate.md), [download-cache.md](download-cache.md), [concurrency-limits.md](concurrency-limits.md)) are all provider-agnostic already. The only per-platform things are: (1) the `Provider` implementation, (2) its registration line, (3) whatever env vars its backend tool needs (e.g. `SPOTDL_PATH`), following the same `getEnvDefault` pattern as `YT_DLP_PATH`.
