package bot

import (
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/PaulSonOfLars/gotgbot/v2"

	"igsave-bot/internal/platform"
)

const (
	directDownloadPathPrefix = "/downloads/"
	largeVideoThreshold      = int64(50 * 1024 * 1024)
)

var cacheKeyRE = regexp.MustCompile(`^[0-9a-f]{64}$`)

// startDownloadServer exposes completed cache files when a public base URL is
// configured. It binds before returning so a bad address fails startup instead
// of leaving users with dead links in captions.
func (bot *Bot) startDownloadServer() error {
	if bot.cfg.DirectDownloadBaseURL == "" {
		return nil
	}

	listener, err := net.Listen("tcp", bot.cfg.DirectDownloadListenAddr)
	if err != nil {
		return fmt.Errorf("listen for direct downloads: %w", err)
	}
	server := &http.Server{
		Handler:           http.HandlerFunc(bot.serveDownload),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       time.Minute,
	}
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("direct download server failed: %v", err)
		}
	}()
	log.Printf("direct downloads listening on %s", bot.cfg.DirectDownloadListenAddr)
	return nil
}

func (bot *Bot) serveDownload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	escapedPath := r.URL.EscapedPath()
	if !strings.HasPrefix(escapedPath, directDownloadPathPrefix) {
		http.NotFound(w, r)
		return
	}
	relURL, err := url.PathUnescape(strings.TrimPrefix(escapedPath, directDownloadPathPrefix))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	parts := strings.Split(relURL, "/")
	if len(parts) != 2 || !cacheKeyRE.MatchString(parts[0]) || parts[1] == "" || parts[1] == ".done" || filepath.Base(parts[1]) != parts[1] {
		http.NotFound(w, r)
		return
	}

	marker, err := os.Stat(filepath.Join(bot.cfg.CacheDir, parts[0], ".done"))
	if err != nil || time.Since(marker.ModTime()) > time.Duration(bot.cfg.CacheTTLSeconds)*time.Second {
		http.NotFound(w, r)
		return
	}
	file, err := os.Open(filepath.Join(bot.cfg.CacheDir, parts[0], parts[1]))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename*=UTF-8''%s", url.PathEscape(parts[1])))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, parts[1], info.ModTime(), file)
}

func (bot *Bot) directDownloadURL(filePath string) (string, bool) {
	if bot.cfg.DirectDownloadBaseURL == "" {
		return "", false
	}
	absCache, err := filepath.Abs(bot.cfg.CacheDir)
	if err != nil {
		return "", false
	}
	absFile, err := filepath.Abs(filePath)
	if err != nil {
		return "", false
	}
	rel, err := filepath.Rel(absCache, absFile)
	if err != nil {
		return "", false
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	if len(parts) != 2 || !cacheKeyRE.MatchString(parts[0]) || filepath.Base(parts[1]) != parts[1] {
		return "", false
	}
	return strings.TrimRight(bot.cfg.DirectDownloadBaseURL, "/") + directDownloadPathPrefix +
		url.PathEscape(parts[0]) + "/" + url.PathEscape(parts[1]), true
}

func (bot *Bot) captionForFile(j job, f platform.MediaFile) string {
	caption := f.Caption
	if caption == "" {
		caption = bot.caption(j)
	}
	if j.delivery != deliveryBoth || f.Kind != platform.KindVideo {
		return caption
	}
	info, err := os.Stat(f.Path)
	if err != nil || info.Size() <= largeVideoThreshold {
		return caption
	}
	downloadURL, ok := bot.directDownloadURL(f.Path)
	if !ok {
		return caption
	}
	return caption + "\n⬇️ Direct download: " + downloadURL
}

func (bot *Bot) finishWithDirectLinks(j job, files []platform.MediaFile, text string) error {
	rows := make([][]gotgbot.InlineKeyboardButton, 0, len(files))
	for i, file := range files {
		downloadURL, ok := bot.directDownloadURL(file.Path)
		if !ok {
			return fmt.Errorf("file %q is outside the completed cache", file.Path)
		}
		label := filepath.Base(file.Path)
		if len(files) == 1 {
			label = "⬇️ Download file"
		} else {
			label = fmt.Sprintf("⬇️ Download %d: %s", i+1, label)
		}
		rows = append(rows, []gotgbot.InlineKeyboardButton{{Text: label, Url: downloadURL}})
	}
	if len(rows) == 0 {
		return fmt.Errorf("no downloaded files")
	}
	_, _, err := j.b.EditMessageText(text, &gotgbot.EditMessageTextOpts{
		ChatId: j.chatID, MessageId: j.statusMsgID,
		ReplyMarkup: gotgbot.InlineKeyboardMarkup{InlineKeyboard: rows},
	})
	return err
}
