package bot

import (
	"fmt"
	"log"
	"math/rand"
	"sync"
	"time"

	"github.com/PaulSonOfLars/gotgbot/v2"
)

type progressPhase int

const (
	phaseDownloading progressPhase = iota
	phaseProcessing
	phaseDirectLink
	phaseUploading
)

// statusTracker coalesces frequent downloader events into Telegram edits at
// randomized 3-8 second intervals, avoiding API spam while still showing life.
type statusTracker struct {
	j       job
	mu      sync.Mutex
	phase   progressPhase
	percent int
	last    string
	stopCh  chan struct{}
	doneCh  chan struct{}
	once    sync.Once
}

func newStatusTracker(j job) *statusTracker {
	t := &statusTracker{j: j, phase: phaseDownloading, stopCh: make(chan struct{}), doneCh: make(chan struct{})}
	go t.loop()
	return t
}

func (t *statusTracker) setDownloadPercent(percent int) {
	t.mu.Lock()
	t.phase = phaseDownloading
	t.percent = max(0, min(100, percent))
	t.mu.Unlock()
}

func (t *statusTracker) setPhase(phase progressPhase) {
	t.mu.Lock()
	t.phase = phase
	if phase == phaseUploading {
		t.percent = 5
	}
	t.mu.Unlock()
}

func (t *statusTracker) loop() {
	defer close(t.doneCh)
	for {
		timer := time.NewTimer(time.Duration(3+rand.Intn(6)) * time.Second)
		select {
		case <-timer.C:
			t.tick()
		case <-t.stopCh:
			if !timer.Stop() {
				<-timer.C
			}
			return
		}
	}
}

func (t *statusTracker) tick() {
	t.mu.Lock()
	if t.phase == phaseUploading && t.percent < 90 {
		t.percent = min(90, t.percent+8+rand.Intn(13))
	}
	text := progressText(t.phase, t.percent)
	if text == t.last {
		t.mu.Unlock()
		return
	}
	t.last = text
	t.mu.Unlock()

	if _, _, err := t.j.b.EditMessageText(text, &gotgbot.EditMessageTextOpts{ChatId: t.j.chatID, MessageId: t.j.statusMsgID}); err != nil {
		log.Println("edit progress message failed:", err)
	}
}

func (t *statusTracker) stop() {
	t.once.Do(func() { close(t.stopCh) })
	<-t.doneCh
}

func progressText(phase progressPhase, percent int) string {
	switch phase {
	case phaseProcessing:
		return "⚙️ Processing the downloaded media..."
	case phaseDirectLink:
		return "🔗 Making the direct download link..."
	case phaseUploading:
		return fmt.Sprintf("📤 Uploading to Telegram... %d%%", percent)
	default:
		if percent > 0 {
			return fmt.Sprintf("⬇️ Downloading... %d%%", percent)
		}
		return "⬇️ Downloading..."
	}
}
