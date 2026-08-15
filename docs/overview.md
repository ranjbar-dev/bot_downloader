# Overview — Purpose & Scope

Source: SPEC.md §1-2.

## 1. Purpose

Self-hosted Telegram bot on a personal VPS. User sends an Instagram link in a DM to the bot. Bot downloads the media (public content only) and sends it back in the same chat.

## 2. Scope

**In scope**
- Content types: Reels, single-video posts, photo posts, carousels (multi-image/video), IGTV.
- Stories: supported *only* when publicly viewable without login. Most IG stories require an authenticated session to fetch — see [limitations.md](limitations.md). No IG login is implemented in v1, so story support is best-effort and will fail for most real stories. Bot must reply with a clear error in that case, not hang.
- Access control: gated by Telegram channel membership (see [telegram-gate.md](telegram-gate.md)), open to any Telegram user who is a member of the gate channel.
- Disk cache: successfully downloaded media is kept on disk and reused for a configurable TTL, so a second request for the same link (from any user) is served instantly instead of re-downloaded (see [download-cache.md](download-cache.md)).
- Single VPS deployment, single bot token, no multi-tenant admin panel.

**Out of scope (v1)**
- Instagram login/session cookies (private accounts, most stories, age-gated content).
- Web UI / dashboard.
- Persistent database — cache metadata lives on the filesystem (mtime-based), no DB required.
- Payment/quota tiers.
