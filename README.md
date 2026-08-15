# igsave-bot

Telegram bot, self-hosted on your VPS. Send it an Instagram link (reel, video, photo/carousel, IGTV, or best-effort story), it downloads the media and sends it back to you in Telegram.

Full design/spec: [docs/index.md](docs/index.md)

## How it works (user's-eye view)

1. User sends the bot any message containing an `instagram.com` link.
2. Bot checks whether the sender is a member of your gate channel.
   - Not a member → bot replies "You need to join our channel to use this bot." with a **Join Channel** button. Nothing is downloaded.
   - Member → continues.
3. Bot checks its disk cache (keyed by the exact URL). If this link was downloaded within the last `CACHE_TTL_SECONDS` (default 1 hour) — by *any* user — it's served straight from disk, no re-download.
4. On a cache miss, the bot runs `yt-dlp` against the link, saves the result into the cache, and sends it back — as a photo/video, or as a media group for carousels.
5. Downloaded files stay on disk for the configured TTL, then a background sweeper deletes them. This is all configurable (§ below).

## Quick facts

- Language: Go (single static binary)
- Downloader: shells out to `yt-dlp`
- Access gate: Telegram channel membership, enforced with an inline "Join Channel" button
- Cache: on-disk, TTL-based, shared across all users
- Deployment: systemd service on a Linux VPS
- Scope: public Instagram content only (no IG login/session) — see [docs/limitations.md](docs/limitations.md) for what that excludes (most Stories, private accounts)
- Designed to add more platforms later (YouTube, TikTok, Spotify, ...) without touching the bot's core — see [docs/extending-platforms.md](docs/extending-platforms.md)

## Requirements on the VPS

- A Linux VPS (Ubuntu/Debian assumed below) with outbound internet access
- Go 1.24+ (build time only; the running service just needs the compiled binary)
- `yt-dlp` installed and kept up to date (Instagram's site changes frequently enough that a stale `yt-dlp` is the #1 cause of "downloads stopped working" — see the update timer below), plus `ffmpeg` for video/audio muxing
- A Telegram bot token from [@BotFather](https://t.me/BotFather)
- A Telegram channel you control, with the bot added as **admin** (needed for `getChatMember` to work)

## Finding your channel ID

gotgbot (the library this bot uses) requires a **numeric** channel ID for `GATE_CHANNEL` — `@username` will not work:

1. Add your bot as admin of the channel.
2. Post any message in the channel.
3. Forward that message to [@RawDataBot](https://t.me/RawDataBot) (or open `https://api.telegram.org/bot<TOKEN>/getUpdates` right after posting) and read `chat.id` from the response. Channel IDs look like `-1001234567890`.

## Local build & smoke test

```bash
go build -o igsave-bot ./cmd/igsave-bot
go vet ./...
go test ./...
```

## Deploying to production

These steps assume a fresh Ubuntu/Debian VPS and that you'll build the binary on the VPS itself (simplest — no cross-compile/artifact transfer to manage).

### 1. Install runtime dependencies

```bash
sudo apt update
sudo apt install -y ffmpeg git

# Debian/Ubuntu's packaged Go is usually older than the 1.24 this module
# requires — install from the official tarball instead of apt's golang-go.
curl -sL https://go.dev/dl/go1.24.0.linux-amd64.tar.gz | sudo tar -C /usr/local -xz
echo 'export PATH=$PATH:/usr/local/go/bin' | sudo tee /etc/profile.d/go.sh
source /etc/profile.d/go.sh
go version   # sanity check, should print go1.24+

# yt-dlp as the standalone self-updating binary, NOT apt/choco/pip — apt
# and choco versions go stale and break against Instagram's frequently
# changing site, and Debian 12+/Ubuntu 23.10+ block system-wide pip installs
# (PEP 668 "externally-managed-environment") without extra venv/pipx setup
# this doesn't need. `yt-dlp -U` (wired into the systemd timer below) self-
# updates this binary in place.
sudo curl -sL https://github.com/yt-dlp/yt-dlp/releases/latest/download/yt-dlp -o /usr/local/bin/yt-dlp
sudo chmod +x /usr/local/bin/yt-dlp
yt-dlp --version   # sanity check
```

### 2. Create a dedicated system user

```bash
sudo useradd -r -m -d /opt/igsave-bot -s /usr/sbin/nologin igsave-bot
sudo mkdir -p /var/lib/igsave-bot/cache
sudo chown -R igsave-bot:igsave-bot /var/lib/igsave-bot
```

### 3. Build and install the binary

```bash
git clone <your-repo-url> /opt/igsave-bot/src
cd /opt/igsave-bot/src
go build -o /opt/igsave-bot/igsave-bot ./cmd/igsave-bot
sudo chown igsave-bot:igsave-bot /opt/igsave-bot/igsave-bot
```

### 4. Configure

```bash
sudo cp config.example.env /opt/igsave-bot/config.env
sudo nano /opt/igsave-bot/config.env
```

Fill in at minimum `BOT_TOKEN`, `GATE_CHANNEL` (numeric, see above), `GATE_CHANNEL_INVITE_LINK`. Defaults are sane for a small VPS out of the box (1 hour cache TTL, 10GB cache cap, 2 workers, 50MB upload cap — see [docs/config.md](docs/config.md) for the full list and what each one does). If your box is smaller than that, see "Sizing for a small VPS" below before you start the service.

```bash
sudo chown igsave-bot:igsave-bot /opt/igsave-bot/config.env
sudo chmod 600 /opt/igsave-bot/config.env
```

### 5. Install the systemd units

```bash
sudo cp igsave-bot.service /etc/systemd/system/
sudo cp igsave-bot-ytdlp-update.service /etc/systemd/system/
sudo cp igsave-bot-ytdlp-update.timer /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now igsave-bot.service
sudo systemctl enable --now igsave-bot-ytdlp-update.timer
```

### 6. Verify

```bash
sudo systemctl status igsave-bot          # should be "active (running)"
sudo journalctl -u igsave-bot -f          # watch logs live
```

Then, in Telegram: DM the bot `/start`, and try a real Instagram link. If you're not yet a channel member you should see the Join Channel button; after joining, a link should download and come back within a few seconds.

### Updating after a code change

```bash
cd /opt/igsave-bot/src && git pull
go build -o /opt/igsave-bot/igsave-bot ./cmd/igsave-bot
sudo systemctl restart igsave-bot
```

### Operational notes

- **Cache location**: `/var/lib/igsave-bot/cache` by default (`CACHE_DIR`). It's the only directory the service can write to (`ReadWritePaths` in the unit) — if you change `CACHE_DIR`, update the unit file to match.
- **Disk usage**: capped by `CACHE_MAX_MB` (default 10GB) — oldest entries are evicted first once exceeded, checked every sweep cycle (`max(CACHE_TTL_SECONDS/2, 1 minute)`). Usage can transiently run over the cap between sweeps, so leave headroom (see sizing section below).
- **yt-dlp staleness**: the `igsave-bot-ytdlp-update.timer` runs `yt-dlp -U` weekly. If downloads start failing across the board (not just one post), run `sudo -u igsave-bot yt-dlp -U` manually first — it's the most common cause.
- **Restarting**: `Restart=on-failure` in the unit means a crash auto-recovers; in-flight downloads at the moment of a restart are lost (not resumed), which is acceptable for personal use.

## Sizing for a small VPS (1 CPU / 1GB RAM / 30GB disk)

This is the profile the defaults are tuned for. If your box looks like this:

- **`WORKER_COUNT=1`**: with a single CPU, running multiple `yt-dlp`/`ffmpeg` processes concurrently doesn't get you real parallelism, only context-switch overhead and RAM pressure. The shipped default is `2` (safe for slightly bigger boxes); on a genuine 1-CPU/1GB box, set it to `1` in `config.env`.
- **`CACHE_MAX_MB`**: default `10000` (10GB) leaves ~20GB of your 30GB disk for the OS, Go toolchain, yt-dlp/ffmpeg, and logs — reasonable headroom, no change needed. Lower it (e.g. `5000`) if you'd rather trade cache-hit rate for more free disk.
- **`MemoryMax=700M`** is already set in `igsave-bot.service` — on a 1GB box this caps the bot and its download processes so a large/unusual video can't OOM-kill sshd or other services, only this one (systemd restarts it per `Restart=on-failure`).
- **Add swap.** 1GB RAM with no swap means any memory spike (a large carousel, an oddly-encoded video ffmpeg has to work harder on) risks the kernel OOM-killer making a bad call before `MemoryMax` even kicks in. A 1–2GB swap file is cheap insurance:
  ```bash
  sudo fallocate -l 2G /swapfile
  sudo chmod 600 /swapfile
  sudo mkswap /swapfile
  sudo swapon /swapfile
  echo '/swapfile none swap sw 0 0' | sudo tee -a /etc/fstab
  ```
- **Watch disk in practice**: `du -sh /var/lib/igsave-bot/cache` occasionally at first to confirm the cap is behaving as expected before you trust it unattended.

## Repo layout

```
cmd/igsave-bot/                      main.go — wiring only, registers providers
internal/bot/                        telegram update loop, handlers, gate check, rate limit
internal/platform/                   Provider interface, registry, yt-dlp-backed provider
internal/cache/                      disk cache with TTL + per-key locking
internal/config/                     env config loading
config.example.env
igsave-bot.service                   main systemd unit
igsave-bot-ytdlp-update.service/.timer   weekly yt-dlp self-update
docs/                                 full design spec, split by topic — start at docs/index.md
```

## Adding a new platform later

See [docs/extending-platforms.md](docs/extending-platforms.md) — YouTube, TikTok, Twitter/X are a one-line addition since they already work through yt-dlp; sites yt-dlp can't reach (Spotify) need a small new `Provider` implementation. Neither case touches `internal/bot` or `internal/cache`.
