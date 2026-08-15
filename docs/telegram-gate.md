# Telegram Integration & Access Gate

Source: SPEC.md §4-5.

Note: §5 below describes the v1 in-memory 5-minute positive-cache design. It has since been superseded by a SQLite-backed, event-driven membership store — see `CLAUDE.md` → "SQLite (channel membership)" for the current, authoritative behavior (`internal/store/store.go`, `internal/bot/gate.go`).

## 4. Telegram integration

- Library: `github.com/PaulSonOfLars/gotgbot/v2` — typed Bot API bindings + update dispatcher.
- Update mode: long polling (`getUpdates`). No public HTTPS endpoint needed on the VPS, avoids TLS/cert setup.
- Bot commands:
  - `/start` — usage instructions; includes the "Join Channel" button if the caller isn't gated yet (button is always shown on `/start`, harmless if already a member).
  - Plain message containing an `instagram.com` / `instagr.am` URL — triggers the flow in [flow.md](flow.md).
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
