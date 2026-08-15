# igsave-bot — Spec

## 1. Purpose

Self-hosted Telegram bot on a personal VPS. User sends an Instagram link in a DM to the bot. Bot downloads the media (public content only) and sends it back in the same chat.

## 2. Scope

**In scope**
- Content types: Reels, single-video posts, photo posts, carousels (multi-image/video), IGTV.
- Stories: supported *only* when publicly viewable without login. Most IG stories require an authenticated session to fetch — see [§10 Limitations](#10-known-limitations). No IG login is implemented in v1, so story support is best-effort and will fail for most real stories. Bot must reply with a clear error in that case, not hang.
- Access control: gated by Telegram channel membership (see §5), open to any Telegram user who is a member of the gate channel.
- Disk cache: successfully downloaded media is kept on disk and reused for a configurable TTL, so a second request for the same link (from any user) is served instantly instead of re-downloaded (see §7).
- Single VPS deployment, single bot token, no multi-tenant admin panel.

**Out of scope (v1)**
- Instagram login/session cookies (private accounts, most stories, age-gated content).
- Web UI / dashboard.
- Persistent database — cache metadata lives on the filesystem (mtime-based), no DB required.
- Payment/quota tiers.

## 3. High-level flow

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

## 4. Telegram integration

- Library: `github.com/PaulSonOfLars/gotgbot/v2` — typed Bot API bindings + update dispatcher.
- Update mode: long polling (`getUpdates`). No public HTTPS endpoint needed on the VPS, avoids TLS/cert setup.
- Bot commands:
  - `/start` — usage instructions; includes the "Join Channel" button if the caller isn't gated yet (button is always shown on `/start`, harmless if already a member).
  - Plain message containing an `instagram.com` / `instagr.am` URL — triggers the flow in §3.
  - Any other message — ignored.
- gotgbot's client methods take `chatId` as `int64` everywhere (no `@username` overload), which is why `GATE_CHANNEL` is a numeric ID — see README "Finding your channel ID".

## 5. Access gate (channel membership)

- Config: `GATE_CHANNEL` = numeric channel chat ID (e.g. `-1001234567890`). `GATE_CHANNEL_INVITE_LINK` = the `https://t.me/...` link used in the button.
- On every incoming message with a valid link, call `getChatMember(chat_id=GATE_CHANNEL, user_id=sender.id)`.
- Allowed statuses: `member`, `administrator`, `creator`. Anything else (`left`, `kicked`, `restricted`) is rejected.
- Bot must itself be a member/admin of `GATE_CHANNEL` for this API call to succeed.
- A **positive** membership result is cached per user for 5 minutes (in-memory map) to avoid hammering `getChatMember` on repeated messages from an active member. A negative result (not yet a member) is never cached — it's always re-checked live, so joining the channel takes effect on the very next message instead of waiting out a stale cache entry.
- **Rejection reply**: text "You need to join our channel to use this bot." with one inline keyboard button:
  ```go
  gotgbot.InlineKeyboardMarkup{
      InlineKeyboard: [][]gotgbot.InlineKeyboardButton{
          {{Text: "Join Channel", Url: cfg.GateInviteLink}},
      },
  }
  ```
- Rate-limited to one such reply per user per minute so retries don't spam the chat.

## 6. Download engine (yt-dlp wrapper)

- Bot shells out to the `yt-dlp` binary per request via `os/exec`, argument slice only — **never** a shell string (`sh -c`) — the URL is one `exec.Command` argument, so nothing in it can break out into a shell command regardless of what the user pastes.
- URL validation before shelling out: must parse as a URL, host must be in the provider's allowed host list (`instagram.com`, `www.instagram.com`, `instagr.am`). Everything else is rejected before it ever reaches `yt-dlp`.
- Command shape (see `internal/platform/ytdlp.go`):
  ```
  yt-dlp --no-playlist --output "<dest>/%(id)s.%(ext)s" --no-warnings \
    --max-filesize <MaxUploadMB>M --print after_move:filepath <url>
  ```
  `--print after_move:filepath` gives us the exact output path(s) yt-dlp wrote, including all files for a multi-item carousel, without guessing filenames.
- Timeout: `context.WithTimeout` around the exec call, `JOB_TIMEOUT_SECONDS` (default 120s) — kills runaway yt-dlp processes (e.g. IG rate-limiting causing hangs).
- Destination directory is now the cache entry's directory (§7), not a throwaway tmp dir — the provider itself doesn't know or care that its output persists; that's the cache layer's decision.

## 7. Disk cache (persistence)

Downloaded media is kept on disk and reused across requests — if two different users (or the same user twice) send the same Instagram link, the second request is served from disk with no re-download.

- **Key**: `sha256(raw URL)` — implemented in `internal/cache.Key`. Same URL string → same key. Different query strings/tracking params are treated as distinct entries (documented limitation, not deduped).
- **Layout**: `CACHE_DIR/<key>/` holds the downloaded file(s) plus a `.done` marker file written after a successful download. The marker's mtime is the TTL clock.
- **TTL**: `CACHE_TTL_SECONDS` (default `3600` = 1 hour), fully configurable. `Lookup` treats an entry as a miss once `now - .done.mtime > TTL`, even if the files are still physically present — a fresh download then overwrites them.
- **Concurrency**: a per-key mutex (`internal/cache.keyedLock`) ensures that if two requests for the same link race in, only one actually downloads; the second waits on the lock and then hits the now-populated cache. The lock map is reference-counted so it doesn't grow unbounded over the bot's uptime.
- **Eviction (TTL)**: a background sweeper goroutine (`Cache.SweepLoop`) runs every `max(TTL/2, 1 minute)`, walks `CACHE_DIR`, and deletes any entry whose `.done` marker is missing or expired. This is what actually reclaims disk — `Lookup` alone never deletes anything (avoids racing a delete against an in-flight send).
- **Eviction (size cap)**: `CACHE_MAX_MB` (default `10000` = 10GB) is a hard ceiling on total cache size, enforced in the same sweep pass, after the TTL pass. If total size exceeds the cap, complete entries are evicted oldest-`.done`-mtime-first until back under it. This exists because TTL alone doesn't bound disk usage under bursty traffic — a flood of distinct links within one TTL window could otherwise fill the disk before anything expires. `CACHE_MAX_MB=0` disables the cap (TTL-only, the v1 behavior before this was added).
- Eviction of either kind takes the same per-key lock as an in-flight download/send, so it never deletes out from under an active request — it blocks until that request's lock releases, same as the TTL pass always did.
- **Failure handling**: if a download fails partway, `Cache.Abandon` removes the partial directory so it can't be mistaken for a valid (but marker-less) entry.
- No database — the filesystem *is* the index. Acceptable at personal-VPS scale; revisit only if `CACHE_DIR` needs to be inspected/queried from outside the bot process.

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

## 10. Known limitations

- **Stories**: Instagram serves stories only to authenticated sessions in virtually all cases; without IG login this will fail for real-world stories despite being nominally "in scope." Bot returns a clear "couldn't fetch, may require login" error rather than a generic failure.
- **IG anti-scraping changes**: yt-dlp's IG extractor breaks periodically when Instagram changes its API. Confirmed during spec validation: the `choco`-packaged yt-dlp (2025.07.21) could not be assumed current — always run a self-updating install (pip, or the standalone binary with the update timer in §12) rather than a distro/package-manager version that goes stale.
- **Age-restricted / login-required posts**: fail cleanly with an explanatory reply.
- **Cache key is the raw URL string**: two links that point at the same media but differ in query string (e.g. `?igsh=...` tracking params) are cached separately. Acceptable for v1; normalizing the URL (strip known tracking params) would be a small follow-up if cache-miss rate on effectively-duplicate links turns out to matter.
- Single VPS, single bot instance — no HA, acceptable for personal use.

## 11. Configuration

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

## 12. Deployment (VPS, systemd)

Unit files are committed at the repo root: `igsave-bot.service`, `igsave-bot-ytdlp-update.service` + `.timer`. Full install steps in README "Deploying to production."

```ini
# igsave-bot.service
[Unit]
Description=igsave-bot
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
EnvironmentFile=/opt/igsave-bot/config.env
ExecStart=/opt/igsave-bot/igsave-bot
Restart=on-failure
RestartSec=5
User=igsave-bot
Group=igsave-bot
NoNewPrivileges=true
ProtectSystem=strict
ReadWritePaths=/var/lib/igsave-bot
PrivateTmp=true
MemoryMax=700M

[Install]
WantedBy=multi-user.target
```

- Run as a dedicated non-root user (`useradd -r igsave-bot`).
- `CACHE_DIR` (default `/var/lib/igsave-bot/cache`) must be under the unit's `ReadWritePaths` — it's the only directory the bot writes to at runtime.
- `MemoryMax=700M` in the unit caps the bot + its yt-dlp/ffmpeg children (systemd puts forked children in the same cgroup) so a runaway job crashes only this service, not the whole VPS via the kernel OOM killer. Sized for a 1GB-RAM box (§12's README companion has the full small-VPS sizing guide); raise or remove on a bigger box.
- `igsave-bot-ytdlp-update.timer` runs `yt-dlp -U` weekly — mitigates §10's extractor-breakage risk. Enable both the `.service` and `.timer`.
- Logs via `journalctl -u igsave-bot`.

## 13. Error handling summary

| Failure | User-facing reply |
|---|---|
| Not a channel member | "You need to join our channel to use this bot." + Join Channel button |
| Invalid/non-IG URL | "That link isn't supported." |
| yt-dlp non-zero exit | "Couldn't download that — post may be private, removed, or too large." |
| File exceeds upload limit | Same as above (yt-dlp aborts the download itself via `--max-filesize`) |
| Rate limit hit | "Too many requests, slow down." |
| Worker queue full | "Bot's busy, try again in a moment." |
| Job timeout | Same as the yt-dlp-failure message (timeout surfaces as a non-zero exit) |
| Send succeeded download, Telegram send failed | "Downloaded it, but couldn't send it back — try again later." |

## 14. Security notes

- No shell interpolation of user input (§6) — this is the one real injection surface, treat it as load-bearing.
- Bot token and any future secrets only in `config.env`, `chmod 600`, never logged.
- `ProtectSystem=strict` + explicit `ReadWritePaths` in the systemd unit limits blast radius if yt-dlp or a dependency is ever compromised.
- No IG credentials stored anywhere in v1 (§2 scope) — removes an entire class of credential-leak risk.
- `CACHE_DIR` holds other users' downloaded media at rest, readable by whoever can read the `igsave-bot` user's files — fine for a single-operator personal VPS, worth revisiting only if the bot ever becomes multi-tenant with untrusted co-owners of the box.

## 15. Suggested Go dependencies

- `github.com/PaulSonOfLars/gotgbot/v2` — Telegram Bot API bindings + dispatcher.
- Standard library only for everything else: `os/exec`, `context`, `net/url`, `time`, `os`, `crypto/sha256`. No DB driver, ORM, or config framework — env vars + stdlib cover v1 fully.

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

**Case B — site yt-dlp can't reach** (Spotify: DRM-protected streams, yt-dlp has no extractor for it): needs a real new `Provider` implementation backed by a different tool, e.g. `spotdl`. Same shape as `YtDlpProvider` — implement `Name`/`Match`/`Download`, shell out with an argument slice (never a shell string, per §6), return `MediaFile{Kind: KindAudio}`. Drop it into `internal/platform/spotify.go`, register it in `main.go`, done — `internal/bot` and `internal/cache` need zero changes.

**What stays fixed as platforms are added:** the gate check, rate limiter, worker pool, disk cache, and Telegram send logic (§5, §7, §8, §9) are all provider-agnostic already. The only per-platform things are: (1) the `Provider` implementation, (2) its registration line, (3) whatever env vars its backend tool needs (e.g. `SPOTDL_PATH`), following the same `getEnvDefault` pattern as `YT_DLP_PATH`.
