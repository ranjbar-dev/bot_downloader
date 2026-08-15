# Deployment (VPS, systemd)

Source: SPEC.md §12. Full install steps in `README.md` → "Deploying to production."

Unit files are committed at the repo root: `igsave-bot.service`, `igsave-bot-ytdlp-update.service` + `.timer`.

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
- `MemoryMax=700M` in the unit caps the bot + its yt-dlp/ffmpeg children (systemd puts forked children in the same cgroup) so a runaway job crashes only this service, not the whole VPS via the kernel OOM killer. Sized for a 1GB-RAM box (README has the full small-VPS sizing guide); raise or remove on a bigger box.
- `igsave-bot-ytdlp-update.timer` runs `yt-dlp -U` weekly — mitigates the extractor-breakage risk in [limitations.md](limitations.md). Enable both the `.service` and `.timer`.
- Logs via `journalctl -u igsave-bot`.
- Optional second unit, `telegram-bot-api.service`: a self-hosted Bot API server that raises the 50MB upload cap to ~2GB. Runs as the same `igsave-bot` user (it reads media out of `CACHE_DIR` by path), binds to `127.0.0.1:8081` only, and needs a one-time `logOut` against api.telegram.org before Telegram will let it serve the bot. Full runbook including the source build: README → "Local Bot API server". Behavior details: [concurrency-limits.md](concurrency-limits.md) §9.
