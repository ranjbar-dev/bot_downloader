# Download Engine & Disk Cache

Source: SPEC.md §6-7.

## 6. Download engine (yt-dlp wrapper)

- Bot shells out to the `yt-dlp` binary per request via `os/exec`, argument slice only — **never** a shell string (`sh -c`) — the URL is one `exec.Command` argument, so nothing in it can break out into a shell command regardless of what the user pastes.
- URL validation before shelling out: must parse as a URL, host must be in the provider's allowed host list (per platform — Instagram, TikTok, YouTube, Pornhub each has its own; see `cmd/igsave-bot/main.go`). Everything else is rejected before it ever reaches `yt-dlp`.
- Command shape (see `internal/platform/ytdlp.go`):
  ```
  yt-dlp --no-playlist --output "<dest>/%(id)s.%(ext)s" --no-warnings \
    --max-filesize <MaxUploadMB>M [<quality args>] --print after_move:filepath <url>
  ```
  `--print after_move:filepath` gives us the exact output path(s) yt-dlp wrote, including all files for a multi-item carousel, without guessing filenames. `<quality args>` is only present for providers built with `NewYtDlpProviderWithQuality` (YouTube, Pornhub) after the user picks a quality button — see [extending-platforms.md](extending-platforms.md).
- Spotify is not yt-dlp-reachable (DRM); `internal/platform/spotify.go` resolves the track title via Spotify's oEmbed endpoint and downloads matching audio from YouTube instead — see [extending-platforms.md](extending-platforms.md) Case B.
- Timeout: `context.WithTimeout` around the exec call, `JOB_TIMEOUT_SECONDS` (default 120s) — kills runaway yt-dlp processes (e.g. IG rate-limiting causing hangs).
- Destination directory is now the cache entry's directory (§7 below), not a throwaway tmp dir — the provider itself doesn't know or care that its output persists; that's the cache layer's decision.

## 7. Disk cache (persistence)

Downloaded media is kept on disk and reused across requests — if two different users (or the same user twice) send the same link, the second request is served from disk with no re-download.

- **Key**: `sha256(raw URL + "|" + quality)` — implemented in `internal/cache.Key`. Same URL and same quality pick → same key; a different quality choice (YouTube/Pornhub) is a distinct cache entry, since it's a different downloaded file. `quality` is `""` for providers with no quality prompt. Different query strings/tracking params on the URL are likewise treated as distinct entries (documented limitation, not deduped).
- **Layout**: `CACHE_DIR/<key>/` holds the downloaded file(s) plus a `.done` marker file written after a successful download. The marker's mtime is the TTL clock.
- **TTL**: `CACHE_TTL_SECONDS` (default `3600` = 1 hour), fully configurable. `Lookup` treats an entry as a miss once `now - .done.mtime > TTL`, even if the files are still physically present — a fresh download then overwrites them.
- **Concurrency**: a per-key mutex (`internal/cache.keyedLock`) ensures that if two requests for the same link race in, only one actually downloads; the second waits on the lock and then hits the now-populated cache. The lock map is reference-counted so it doesn't grow unbounded over the bot's uptime.
- **Eviction (TTL)**: a background sweeper goroutine (`Cache.SweepLoop`) runs every `max(TTL/2, 1 minute)`, walks `CACHE_DIR`, and deletes any entry whose `.done` marker is missing or expired. This is what actually reclaims disk — `Lookup` alone never deletes anything (avoids racing a delete against an in-flight send).
- **Eviction (size cap)**: `CACHE_MAX_MB` (default `10000` = 10GB) is a hard ceiling on total cache size, enforced in the same sweep pass, after the TTL pass. If total size exceeds the cap, complete entries are evicted oldest-`.done`-mtime-first until back under it. This exists because TTL alone doesn't bound disk usage under bursty traffic — a flood of distinct links within one TTL window could otherwise fill the disk before anything expires. `CACHE_MAX_MB=0` disables the cap (TTL-only, the v1 behavior before this was added).
- Eviction of either kind takes the same per-key lock as an in-flight download/send, so it never deletes out from under an active request — it blocks until that request's lock releases, same as the TTL pass always did.
- **Failure handling**: if a download fails partway, `Cache.Abandon` removes the partial directory so it can't be mistaken for a valid (but marker-less) entry.
- No database — the filesystem *is* the index. Acceptable at personal-VPS scale; revisit only if `CACHE_DIR` needs to be inspected/queried from outside the bot process. (This is scoped to cache metadata only — channel membership does use SQLite, see [telegram-gate.md](telegram-gate.md).)
