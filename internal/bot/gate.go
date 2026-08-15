package bot

import (
	"sync"
	"time"

	"github.com/PaulSonOfLars/gotgbot/v2"

	"igsave-bot/internal/store"
)

// memberStatuses that count as "in the channel". Kept in sync with the
// status strings Telegram sends in chat_member updates and getChatMember.
func isMemberStatus(status string) bool {
	return status == "member" || status == "administrator" || status == "creator"
}

// gate checks Telegram channel membership. The source of truth is the
// SQLite store, kept live by chat_member update events (bot.go's
// handleChatMember) — so a user who leaves is rejected on their very next
// message, no cache staleness window. A live GetChatMember call is only
// made as a one-time bootstrap for a user the store has never seen (eg.
// they joined before this bot started listening for events).
type gate struct {
	channel    int64
	inviteLink string
	store      *store.Store

	mu           sync.Mutex
	lastNotified map[int64]time.Time
	notifyCool   time.Duration
}

func newGate(channel int64, inviteLink string, s *store.Store) *gate {
	return &gate{
		channel:      channel,
		inviteLink:   inviteLink,
		store:        s,
		lastNotified: make(map[int64]time.Time),
		notifyCool:   time.Minute,
	}
}

func (g *gate) allowed(b *gotgbot.Bot, userID int64) (bool, error) {
	status, known, err := g.store.Status(userID)
	if err != nil {
		return false, err
	}
	if known {
		return isMemberStatus(status), nil
	}

	// Bootstrap: no chat_member event seen for this user yet. Ask Telegram
	// directly and seed the store so future messages are answered locally.
	member, err := b.GetChatMember(g.channel, userID, nil)
	if err != nil {
		return false, err
	}
	liveStatus := member.GetStatus()
	if err := g.store.Upsert(userID, liveStatus, time.Now().Unix()); err != nil {
		return false, err
	}
	return isMemberStatus(liveStatus), nil
}

// shouldNotify reports whether a "join the channel" reply should be sent now
// (rate-limited so retries don't spam the user).
func (g *gate) shouldNotify(userID int64) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if last, ok := g.lastNotified[userID]; ok && time.Since(last) < g.notifyCool {
		return false
	}
	g.lastNotified[userID] = time.Now()
	return true
}
