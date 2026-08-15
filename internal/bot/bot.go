// Package bot wires Telegram updates to the platform.Registry. It knows
// nothing about Instagram/YouTube/etc specifically — it only deals in
// platform.Provider and platform.MediaFile, so new sources need zero changes
// here.
package bot

import (
	"context"
	"fmt"
	"log"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/PaulSonOfLars/gotgbot/v2"
	"github.com/PaulSonOfLars/gotgbot/v2/ext"
	"github.com/PaulSonOfLars/gotgbot/v2/ext/handlers"
	"github.com/PaulSonOfLars/gotgbot/v2/ext/handlers/filters/callbackquery"
	"github.com/PaulSonOfLars/gotgbot/v2/ext/handlers/filters/chatmember"
	"github.com/PaulSonOfLars/gotgbot/v2/ext/handlers/filters/message"

	"igsave-bot/internal/cache"
	"igsave-bot/internal/config"
	"igsave-bot/internal/platform"
	"igsave-bot/internal/store"
)

var urlRe = regexp.MustCompile(`https?://\S+`)

const checkJoinCallback = "check_join"
const qualityCallbackPrefix = "q:"

type Bot struct {
	cfg         *config.Config
	registry    *platform.Registry
	gate        *gate
	limiter     *rateLimiter
	cache       *cache.Cache
	jobs        chan job
	botUsername string

	pendingMu      sync.Mutex
	pending        map[int64]job // last blocked request per user, retried from the "I joined" button
	pendingQuality map[int64]job // last request per user awaiting a quality pick
}

type job struct {
	b           *gotgbot.Bot
	chatID      int64
	userID      int64
	rawURL      string
	provider    platform.Provider
	quality     string // Value from platform.Quality, "" if provider has no quality choice
	statusMsgID int64
}

func New(cfg *config.Config, registry *platform.Registry, members *store.Store) *Bot {
	return &Bot{
		cfg:            cfg,
		registry:       registry,
		gate:           newGate(cfg.GateChannel, cfg.GateInviteLink, members),
		limiter:        newRateLimiter(5, 10*time.Minute),
		cache:          cache.New(cfg.CacheDir, time.Duration(cfg.CacheTTLSeconds)*time.Second, int64(cfg.CacheMaxMB)*1024*1024),
		jobs:           make(chan job, 50),
		pending:        make(map[int64]job),
		pendingQuality: make(map[int64]job),
	}
}

func (bot *Bot) Run() error {
	b, err := gotgbot.NewBot(bot.cfg.BotToken, nil)
	if err != nil {
		return fmt.Errorf("new bot: %w", err)
	}

	for i := 0; i < bot.cfg.WorkerCount; i++ {
		go bot.worker()
	}
	go bot.cache.SweepLoop(nil)

	dispatcher := ext.NewDispatcher(&ext.DispatcherOpts{
		Error: func(b *gotgbot.Bot, ctx *ext.Context, err error) ext.DispatcherAction {
			log.Println("update error:", err)
			return ext.DispatcherActionNoop
		},
	})
	dispatcher.AddHandler(handlers.NewCommand("start", bot.handleStart))
	dispatcher.AddHandler(handlers.NewMessage(message.Text, bot.handleMessage))
	dispatcher.AddHandler(handlers.NewCallback(callbackquery.Equal(checkJoinCallback), bot.handleCheckJoin))
	dispatcher.AddHandler(handlers.NewCallback(callbackquery.Prefix(qualityCallbackPrefix), bot.handleQuality))
	dispatcher.AddHandler(handlers.NewChatMember(chatmember.ChatId(bot.cfg.GateChannel), bot.handleChatMember))

	updater := ext.NewUpdater(dispatcher, nil)
	if err := updater.StartPolling(b, &ext.PollingOpts{
		DropPendingUpdates: true,
		GetUpdatesOpts: &gotgbot.GetUpdatesOpts{
			// chat_member must be requested explicitly - it's not in Telegram's
			// default update set. This is what keeps the gate's member store
			// live as users join/leave the channel.
			AllowedUpdates: []string{"message", "callback_query", "chat_member"},
		},
	}); err != nil {
		return fmt.Errorf("start polling: %w", err)
	}
	bot.botUsername = b.User.Username
	log.Println("igsave-bot running as", b.User.Username)
	updater.Idle()
	return nil
}

func (bot *Bot) handleStart(b *gotgbot.Bot, ctx *ext.Context) error {
	_, err := ctx.EffectiveMessage.Reply(b,
		"👋 Send an Instagram, TikTok, YouTube, Pornhub, or Spotify link to download it. You need to be a member of our channel to use this bot.",
		&gotgbot.SendMessageOpts{ReplyMarkup: bot.joinKeyboard()})
	return err
}

// joinKeyboard is the inline keyboard attached to the "please join" reply:
// a link to the channel plus a callback button to re-check membership.
func (bot *Bot) joinKeyboard() gotgbot.InlineKeyboardMarkup {
	return gotgbot.InlineKeyboardMarkup{
		InlineKeyboard: [][]gotgbot.InlineKeyboardButton{
			{{Text: "📢 Join Channel", Url: bot.cfg.GateInviteLink}},
			{{Text: "✅ I joined the channel", CallbackData: checkJoinCallback}},
		},
	}
}

// handleChatMember keeps the gate's SQLite member store live: every join,
// leave, kick, or promotion in the gate channel updates the row for that
// user immediately, so gate.allowed can answer from the store instead of
// calling GetChatMember on every message.
func (bot *Bot) handleChatMember(b *gotgbot.Bot, ctx *ext.Context) error {
	cm := ctx.ChatMember
	userID := cm.NewChatMember.GetUser().Id
	status := cm.NewChatMember.GetStatus()
	return bot.gate.store.Upsert(userID, status, cm.Date)
}

func (bot *Bot) handleMessage(b *gotgbot.Bot, ctx *ext.Context) error {
	msg := ctx.EffectiveMessage
	userID := ctx.EffectiveUser.Id

	rawURL := urlRe.FindString(msg.Text)
	if rawURL == "" {
		return nil
	}

	provider, err := bot.registry.Find(rawURL)
	if err != nil {
		_, replyErr := msg.Reply(b, "🚫 That link isn't supported.", nil)
		return replyErr
	}

	j := job{b: b, chatID: ctx.EffectiveChat.Id, userID: userID, rawURL: rawURL, provider: provider}

	allowed, err := bot.gate.allowed(b, userID)
	if err != nil {
		log.Println("gate check failed:", err)
		_, replyErr := msg.Reply(b, "⚠️ Couldn't verify membership, try again shortly.", nil)
		return replyErr
	}
	if !allowed {
		bot.setPending(userID, j)
		if bot.gate.shouldNotify(userID) {
			_, replyErr := msg.Reply(b, "🔒 You need to join our channel to use this bot.",
				&gotgbot.SendMessageOpts{ReplyMarkup: bot.joinKeyboard()})
			return replyErr
		}
		return nil
	}

	if !bot.limiter.allow(userID) {
		_, replyErr := msg.Reply(b, "⏳ Too many requests, slow down.", nil)
		return replyErr
	}

	if qp, ok := provider.(platform.QualityProvider); ok {
		if qualities := qp.Qualities(); len(qualities) > 0 {
			bot.setPendingQuality(userID, j)
			_, replyErr := msg.Reply(b, "🎚 Choose quality:", &gotgbot.SendMessageOpts{ReplyMarkup: qualityKeyboard(qualities)})
			return replyErr
		}
	}

	return bot.enqueue(b, j)
}

// qualityKeyboard renders each Quality as its own button row, the button's
// callback data carrying only the option's index into that provider's
// Qualities() slice (never the format string itself, which can run long).
func qualityKeyboard(qualities []platform.Quality) gotgbot.InlineKeyboardMarkup {
	rows := make([][]gotgbot.InlineKeyboardButton, 0, len(qualities))
	for i, q := range qualities {
		rows = append(rows, []gotgbot.InlineKeyboardButton{
			{Text: q.Label, CallbackData: fmt.Sprintf("%s%d", qualityCallbackPrefix, i)},
		})
	}
	return gotgbot.InlineKeyboardMarkup{InlineKeyboard: rows}
}

// enqueue sends the "Downloading..." status message, then submits j to the
// worker pool with that message's ID attached so process() can delete it
// once the content is sent. If the pool is full, the status message is
// edited to a busy notice instead and the job is dropped.
func (bot *Bot) enqueue(b *gotgbot.Bot, j job) error {
	status, err := b.SendMessage(j.chatID, "⬇️ Downloading...", nil)
	if err != nil {
		return err
	}
	j.statusMsgID = status.MessageId

	select {
	case bot.jobs <- j:
		return nil
	default:
		_, _, err := status.EditText(b, "🚦 Bot's busy, try again in a moment.", nil)
		return err
	}
}

func (bot *Bot) setPending(userID int64, j job) {
	bot.pendingMu.Lock()
	bot.pending[userID] = j
	bot.pendingMu.Unlock()
}

func (bot *Bot) takePending(userID int64) (job, bool) {
	bot.pendingMu.Lock()
	defer bot.pendingMu.Unlock()
	j, ok := bot.pending[userID]
	if ok {
		delete(bot.pending, userID)
	}
	return j, ok
}

func (bot *Bot) setPendingQuality(userID int64, j job) {
	bot.pendingMu.Lock()
	bot.pendingQuality[userID] = j
	bot.pendingMu.Unlock()
}

func (bot *Bot) takePendingQuality(userID int64) (job, bool) {
	bot.pendingMu.Lock()
	defer bot.pendingMu.Unlock()
	j, ok := bot.pendingQuality[userID]
	if ok {
		delete(bot.pendingQuality, userID)
	}
	return j, ok
}

// handleQuality responds to a quality-selection button: resolves the picked
// index against the pending job's own provider.Qualities() (never trusting
// the callback's format string directly) and enqueues the download.
func (bot *Bot) handleQuality(b *gotgbot.Bot, ctx *ext.Context) error {
	cq := ctx.CallbackQuery
	userID := ctx.EffectiveUser.Id

	j, ok := bot.takePendingQuality(userID)
	if !ok {
		_, _ = cq.Answer(b, &gotgbot.AnswerCallbackQueryOpts{Text: "⌛ That request expired, send the link again.", ShowAlert: true})
		return nil
	}
	qp, ok := j.provider.(platform.QualityProvider)
	if !ok {
		return nil
	}
	qualities := qp.Qualities()
	idx, err := strconv.Atoi(strings.TrimPrefix(cq.Data, qualityCallbackPrefix))
	if err != nil || idx < 0 || idx >= len(qualities) {
		_, _ = cq.Answer(b, &gotgbot.AnswerCallbackQueryOpts{Text: "❌ Invalid choice.", ShowAlert: true})
		return nil
	}

	j.quality = qualities[idx].Value
	_, _ = cq.Answer(b, &gotgbot.AnswerCallbackQueryOpts{Text: "✅ " + qualities[idx].Label})
	return bot.enqueue(b, j)
}

// handleCheckJoin responds to the "I joined the channel" button: re-checks
// membership and, if it now passes, runs the request that triggered the gate.
func (bot *Bot) handleCheckJoin(b *gotgbot.Bot, ctx *ext.Context) error {
	cq := ctx.CallbackQuery
	userID := ctx.EffectiveUser.Id

	allowed, err := bot.gate.allowed(b, userID)
	if err != nil {
		log.Println("gate check failed:", err)
		_, _ = cq.Answer(b, &gotgbot.AnswerCallbackQueryOpts{Text: "⚠️ Couldn't verify membership, try again shortly.", ShowAlert: true})
		return nil
	}
	if !allowed {
		_, _ = cq.Answer(b, &gotgbot.AnswerCallbackQueryOpts{Text: "❌ You haven't joined the channel yet.", ShowAlert: true})
		return nil
	}

	j, ok := bot.takePending(userID)
	if !ok {
		_, _ = cq.Answer(b, &gotgbot.AnswerCallbackQueryOpts{Text: "✅ You're in! Send a link to get started.", ShowAlert: true})
		return nil
	}

	err = bot.enqueue(b, j)
	_, _ = cq.Answer(b, &gotgbot.AnswerCallbackQueryOpts{Text: "✅ Joined!"})
	return err
}

func (bot *Bot) worker() {
	for j := range bot.jobs {
		bot.process(j)
	}
}

func (bot *Bot) process(j job) {
	key := cache.Key(j.rawURL, j.quality)
	release := bot.cache.Lock(key)
	defer release()

	files, hit := bot.cache.Lookup(key)
	if !hit {
		var err error
		files, err = bot.download(j, key)
		if err != nil {
			log.Printf("download failed [%s]: %v", j.provider.Name(), err)
			bot.sendError(j, "❌ Couldn't download that — post may be private, removed, or too large.")
			return
		}
	}

	if err := bot.sendFiles(j, files); err != nil {
		log.Println("send failed:", err)
		bot.sendError(j, "❌ Downloaded it, but couldn't send it back — try again later.")
		return
	}

	if _, err := j.b.DeleteMessage(j.chatID, j.statusMsgID, nil); err != nil {
		log.Println("delete status message failed:", err)
	}
}

// download runs the provider into the cache dir for key and marks the
// entry done on success. Caller must hold the key's cache lock.
func (bot *Bot) download(j job, key string) ([]platform.MediaFile, error) {
	dir, err := bot.cache.PrepareDir(key)
	if err != nil {
		return nil, fmt.Errorf("prepare cache dir: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(bot.cfg.JobTimeoutSeconds)*time.Second)
	defer cancel()

	var files []platform.MediaFile
	if j.quality != "" {
		files, err = j.provider.(platform.QualityProvider).DownloadWithQuality(ctx, j.rawURL, dir, j.quality)
	} else {
		files, err = j.provider.Download(ctx, j.rawURL, dir)
	}
	if err != nil {
		bot.cache.Abandon(key)
		return nil, err
	}
	if err := bot.cache.MarkDone(key); err != nil {
		log.Println("cache: mark done failed:", err)
	}
	return files, nil
}

func (bot *Bot) sendError(j job, text string) {
	if _, err := j.b.SendMessage(j.chatID, text, nil); err != nil {
		log.Println("send error message failed:", err)
	}
}

func (bot *Bot) sendFiles(j job, files []platform.MediaFile) error {
	if len(files) == 1 {
		return bot.sendSingle(j, files[0])
	}
	return bot.sendGroup(j, files)
}

// caption builds the client-facing caption attached to sent content: which
// bot served it and the original link it came from.
func (bot *Bot) caption(j job) string {
	if bot.botUsername == "" {
		return fmt.Sprintf("🔗 %s", j.rawURL)
	}
	return fmt.Sprintf("📥 via @%s\n🔗 %s", bot.botUsername, j.rawURL)
}

func (bot *Bot) sendSingle(j job, f platform.MediaFile) error {
	file, err := os.Open(f.Path)
	if err != nil {
		return err
	}
	defer file.Close()

	input := gotgbot.InputFileByReader(fileBase(f.Path), file)
	caption := bot.caption(j)
	switch f.Kind {
	case platform.KindPhoto:
		_, err = j.b.SendPhoto(j.chatID, input, &gotgbot.SendPhotoOpts{Caption: caption})
	case platform.KindAudio:
		_, err = j.b.SendAudio(j.chatID, input, &gotgbot.SendAudioOpts{Caption: caption})
	default:
		_, err = j.b.SendVideo(j.chatID, input, &gotgbot.SendVideoOpts{Caption: caption})
	}
	return err
}

func (bot *Bot) sendGroup(j job, files []platform.MediaFile) error {
	const chunkSize = 10
	caption := bot.caption(j)
	for i := 0; i < len(files); i += chunkSize {
		end := min(i+chunkSize, len(files))
		chunk := files[i:end]

		media := make([]gotgbot.InputMedia, 0, len(chunk))
		openFiles := make([]*os.File, 0, len(chunk))
		for idx, f := range chunk {
			file, err := os.Open(f.Path)
			if err != nil {
				closeAll(openFiles)
				return err
			}
			openFiles = append(openFiles, file)
			input := gotgbot.InputFileByReader(fileBase(f.Path), file)

			// Telegram renders only the first item's caption as the album caption.
			itemCaption := ""
			if i == 0 && idx == 0 {
				itemCaption = caption
			}
			if f.Kind == platform.KindPhoto {
				media = append(media, gotgbot.InputMediaPhoto{Media: input, Caption: itemCaption})
			} else {
				media = append(media, gotgbot.InputMediaVideo{Media: input, Caption: itemCaption})
			}
		}

		_, err := j.b.SendMediaGroup(j.chatID, media, nil)
		closeAll(openFiles)
		if err != nil {
			return err
		}
	}
	return nil
}

func closeAll(files []*os.File) {
	for _, f := range files {
		f.Close()
	}
}

func fileBase(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' || path[i] == '\\' {
			return path[i+1:]
		}
	}
	return path
}
