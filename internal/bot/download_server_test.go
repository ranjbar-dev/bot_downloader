package bot

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"igsave-bot/internal/config"
	"igsave-bot/internal/platform"
)

func TestCaptionForLargeVideoIncludesDirectDownload(t *testing.T) {
	cacheDir := t.TempDir()
	key := strings.Repeat("a", 64)
	dir := filepath.Join(cacheDir, key)
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	videoPath := filepath.Join(dir, "large clip.mp4")
	file, err := os.Create(videoPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(largeVideoThreshold + 1); err != nil {
		t.Fatal(err)
	}
	file.Close()

	bot := &Bot{cfg: &config.Config{CacheDir: cacheDir, DirectDownloadBaseURL: "https://files.example.com/"}}
	got := bot.captionForFile(job{rawURL: "https://example.com/post", delivery: deliveryBoth}, platform.MediaFile{Path: videoPath, Kind: platform.KindVideo})
	wantLink := "⬇️ Direct download: https://files.example.com/downloads/" + key + "/large%20clip.mp4"
	if !strings.Contains(got, wantLink) {
		t.Fatalf("caption %q does not contain %q", got, wantLink)
	}
	telegramOnly := bot.captionForFile(job{rawURL: "https://example.com/post", delivery: deliveryTelegram}, platform.MediaFile{Path: videoPath, Kind: platform.KindVideo})
	if strings.Contains(telegramOnly, "Direct download") {
		t.Fatalf("Telegram-only caption unexpectedly contains a direct link: %q", telegramOnly)
	}

	file, err = os.OpenFile(videoPath, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(largeVideoThreshold); err != nil {
		t.Fatal(err)
	}
	file.Close()
	got = bot.captionForFile(job{rawURL: "https://example.com/post", delivery: deliveryBoth}, platform.MediaFile{Path: videoPath, Kind: platform.KindVideo})
	if strings.Contains(got, "Direct download") {
		t.Fatalf("exactly 50 MiB must not get a direct link: %q", got)
	}
}

func TestServeDownloadOnlyServesCompletedFreshCacheFiles(t *testing.T) {
	cacheDir := t.TempDir()
	key := strings.Repeat("b", 64)
	dir := filepath.Join(cacheDir, key)
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "clip.mp4"), []byte("video"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".done"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	bot := &Bot{cfg: &config.Config{CacheDir: cacheDir, CacheTTLSeconds: int(time.Hour / time.Second)}}

	req := httptest.NewRequest(http.MethodGet, "/downloads/"+key+"/clip.mp4", nil)
	rec := httptest.NewRecorder()
	bot.serveDownload(rec, req)
	if rec.Code != http.StatusOK || rec.Body.String() != "video" {
		t.Fatalf("completed file: status=%d body=%q", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/downloads/"+key+"/.done", nil)
	rec = httptest.NewRecorder()
	bot.serveDownload(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("marker file status = %d, want 404", rec.Code)
	}

	if err := os.Remove(filepath.Join(dir, ".done")); err != nil {
		t.Fatal(err)
	}
	req = httptest.NewRequest(http.MethodGet, "/downloads/"+key+"/clip.mp4", nil)
	rec = httptest.NewRecorder()
	bot.serveDownload(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("incomplete cache entry status = %d, want 404", rec.Code)
	}

	markerPath := filepath.Join(dir, ".done")
	if err := os.WriteFile(markerPath, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	stale := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(markerPath, stale, stale); err != nil {
		t.Fatal(err)
	}
	req = httptest.NewRequest(http.MethodGet, "/downloads/"+key+"/clip.mp4", nil)
	rec = httptest.NewRecorder()
	bot.serveDownload(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expired cache entry status = %d, want 404", rec.Code)
	}
}
