package bot

import (
	"sync"
	"time"

	"github.com/PaulSonOfLars/gotgbot/v2"
)

// gate checks Telegram channel membership, cached briefly so an active
// chatter doesn't trigger a getChatMember call on every message.
type gate struct {
	channel      int64
	inviteLink   string
	ttl          time.Duration
	mu           sync.Mutex
	allowedUntil map[int64]time.Time // only positive results are cached
	lastNotified map[int64]time.Time
	notifyCool   time.Duration
}

func newGate(channel int64, inviteLink string) *gate {
	return &gate{
		channel:      channel,
		inviteLink:   inviteLink,
		ttl:          5 * time.Minute,
		allowedUntil: make(map[int64]time.Time),
		lastNotified: make(map[int64]time.Time),
		notifyCool:   time.Minute,
	}
}

func (g *gate) allowed(b *gotgbot.Bot, userID int64) (bool, error) {
	g.mu.Lock()
	// Never cache "not a member" - that would keep rejecting a user for up
	// to g.ttl after they actually join. Only a cached "allowed" is trusted.
	if until, ok := g.allowedUntil[userID]; ok && time.Now().Before(until) {
		g.mu.Unlock()
		return true, nil
	}
	g.mu.Unlock()

	member, err := b.GetChatMember(g.channel, userID, nil)
	if err != nil {
		return false, err
	}
	status := member.GetStatus()
	ok := status == "member" || status == "administrator" || status == "creator"

	if ok {
		g.mu.Lock()
		g.allowedUntil[userID] = time.Now().Add(g.ttl)
		g.mu.Unlock()
	}
	return ok, nil
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
