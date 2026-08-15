# Error Handling & Security Notes

Source: SPEC.md §13-14.

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

- No shell interpolation of user input (see [download-cache.md](download-cache.md) §6) — this is the one real injection surface, treat it as load-bearing.
- Bot token and any future secrets only in `config.env`, `chmod 600`, never logged.
- `ProtectSystem=strict` + explicit `ReadWritePaths` in the systemd unit limits blast radius if yt-dlp or a dependency is ever compromised.
- No IG credentials stored anywhere in v1 (see [overview.md](overview.md) §2 scope) — removes an entire class of credential-leak risk.
- `CACHE_DIR` holds other users' downloaded media at rest, readable by whoever can read the `igsave-bot` user's files — fine for a single-operator personal VPS, worth revisiting only if the bot ever becomes multi-tenant with untrusted co-owners of the box.
