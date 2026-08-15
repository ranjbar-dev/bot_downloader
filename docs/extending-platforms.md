# Dependencies & Adding a New Platform

Source: SPEC.md §15-16.

## 15. Suggested Go dependencies

- `github.com/PaulSonOfLars/gotgbot/v2` — Telegram Bot API bindings + dispatcher.
- Standard library only for everything else: `os/exec`, `context`, `net/url`, `time`, `os`, `crypto/sha256`. No DB driver, ORM, or config framework — env vars + stdlib cover v1 fully. (`modernc.org/sqlite` was added afterward for channel-membership persistence only — see `CLAUDE.md` → "SQLite (channel membership)".)

## 16. Adding a new platform

The bot is source-agnostic by design: `internal/bot` only ever talks to `platform.Provider` / `platform.QualityProvider` and `platform.MediaFile` (and, for persistence, `internal/cache`, which is likewise provider-agnostic — it just caches whatever files a `Provider.Download` produced). Registered platforms (`cmd/igsave-bot/main.go`): Instagram, TikTok, YouTube, Pornhub, xHamster, wow.xxx, Spotify. A host entry containing `*` is treated as a glob (`path.Match`), for sites that rotate mirror domains — xHamster is registered as `*xhamster*`, which covers `xhamster.com`, `xhamster46.desi`, `ge.xhamster46.desi`, and so on.

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

**Case A — site already supported by yt-dlp** (YouTube, TikTok, Twitter/X, Reddit, Pornhub, Instagram, ...): no new code at all. `internal/platform/ytdlp.go`'s `YtDlpProvider` is generic — name + host list, shells out to yt-dlp the same way for every site:

```go
// cmd/igsave-bot/main.go
registry := platform.NewRegistry(
    platform.NewYtDlpProvider("instagram", []string{"instagram.com", "www.instagram.com", "instagr.am"}, cfg.YtDlpPath, cfg.MaxUploadMB),
    platform.NewYtDlpProvider("tiktok", []string{"tiktok.com", "www.tiktok.com", "m.tiktok.com", "vm.tiktok.com", "vt.tiktok.com"}, cfg.YtDlpPath, cfg.MaxUploadMB),
    platform.NewYtDlpProviderWithQuality("youtube", []string{"youtube.com", "www.youtube.com", "m.youtube.com", "music.youtube.com", "youtu.be"}, cfg.YtDlpPath, cfg.MaxUploadMB, platform.YouTubeQualities),
    platform.NewYtDlpProviderWithQuality("pornhub", []string{"pornhub.com", "www.pornhub.com"}, cfg.YtDlpPath, cfg.MaxUploadMB, platform.VideoQualities),
    platform.NewSpotifyProvider(cfg.YtDlpPath, cfg.MaxUploadMB),
)
```

One line per platform. `Registry.Find` checks providers in order, so put more specific host matches before broader ones if two ever overlap.

**Optional: quality selection.** A `YtDlpProvider` built with `NewYtDlpProviderWithQuality(..., qualities)` also implements `platform.QualityProvider` (`Qualities() []Quality`, `DownloadWithQuality(ctx, rawURL, destDir, quality string)`). `internal/bot` type-asserts for this interface after the gate/rate-limit checks; if `Qualities()` is non-empty it replies with one inline button per option (callback data carries only the option's *index*, never the format string) instead of enqueuing immediately, then enqueues once the user picks one. `Quality.Value` is a literal, whitespace-split yt-dlp argument fragment (`-f bv*[height<=720]+ba/b[height<=720]`, or `-x --audio-format mp3` for audio-only) — see `platform.VideoQualities` / `platform.YouTubeQualities`. Providers built with the plain `NewYtDlpProvider` have a nil `qualities` slice and are never prompted.

**Case A½ — yt-dlp has an extractor but it's broken** (xHamster): `internal/platform/xhamster.go`. yt-dlp's `XHamster` extractor reads the video sources out of `window.initials.xplayerSettings`, which the site has served as `null` since ~2026-07, so every URL fails with `No video formats found` (checked against stable 2026.07.04 and nightly). The signed mp4 URLs are still in the very same page under `initials.downloadDropdownComponent.sources.mp4` — one per rendition (144p/240p/480p/720p logged-out; 1080p+ needs an account), with byte sizes alongside in `sources.download`. `XHamsterProvider` therefore embeds a `*YtDlpProvider` (free `Name`/`Match`, and reuse of the download plumbing: `--max-filesize`, output template, printed-path parsing) and overrides `Qualities`/`Download`/`DownloadWithQuality`: it GETs the page with a browser `User-Agent` (the sources are omitted for clients that don't look like one), pulls the JSON blob with `json.Decoder` (which stops at the end of the first value, so the rest of the page is ignored), and hands the chosen rendition's direct URL to yt-dlp. `Quality.Value` here is a plain height label (`"720p"`), *not* a yt-dlp argument fragment — this provider interprets it itself. Two properties of those URLs matter: they expire after a few hours, and they are bound to the IP that fetched the page, so resolve and download must run on the same host (they do). If the page can't be parsed at all, it falls back to pointing yt-dlp at the original page URL, so a fixed upstream extractor takes over automatically.

**Case B — site yt-dlp can't reach** (Spotify: DRM-protected streams, yt-dlp has no extractor for it): needs a real new `Provider` implementation. `internal/platform/spotify.go`'s `SpotifyProvider` resolves the track title via Spotify's public oEmbed endpoint (`https://open.spotify.com/oembed?url=...`, no API key needed), then downloads the best-matching audio from YouTube via `yt-dlp "ytsearch1:<title>" -x --audio-format mp3` — no extra binary dependency (`spotdl` was considered but yt-dlp alone covers it). Same shape as `YtDlpProvider` otherwise — implement `Name`/`Match`/`Download`, shell out with an argument slice (never a shell string, per [download-cache.md](download-cache.md) §6), return `MediaFile{Kind: KindAudio}`. `internal/bot` and `internal/cache` need zero changes.

**What stays fixed as platforms are added:** the gate check, rate limiter, worker pool, disk cache, and Telegram send logic ([telegram-gate.md](telegram-gate.md), [download-cache.md](download-cache.md), [concurrency-limits.md](concurrency-limits.md)) are all provider-agnostic already. The only per-platform things are: (1) the `Provider` implementation, (2) its registration line, (3) whatever env vars its backend tool needs, following the same `getEnvDefault` pattern as `YT_DLP_PATH`.
