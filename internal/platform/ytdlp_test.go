package platform

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

func TestMatchHostsAndGlobs(t *testing.T) {
	p := NewYtDlpProvider("xhamster", []string{"xhamster.com", "*xhamster*", "*xhvid.*"}, "yt-dlp", 0)

	cases := map[string]bool{
		"https://xhamster.com/videos/slug-xhAbC":                true,
		"https://ge.xhamster46.desi/videos/slug-xhAbC":          true,
		"https://www.xhamster42.xxx/videos/slug-xhAbC":          true,
		"https://xhvid.com/videos/slug-xhAbC":                   true,
		"https://youtube.com/watch?v=x":                         false,
		"https://notxhamsterlike.example.com/videos/slug":       true, // glob is deliberately loose
		"https://example.com/redirect?to=https://xhamster.com/": false,
	}
	for raw, want := range cases {
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatalf("parse %s: %v", raw, err)
		}
		if got := p.Match(u); got != want {
			t.Errorf("Match(%s) = %v, want %v", raw, got, want)
		}
	}
}

// TestOptimizeVideoFallsBackWithoutFfmpeg guards the contract callers rely
// on: when ffmpeg is missing/fails, optimizeVideo errors and leaves the
// original file untouched instead of deleting/corrupting it.
func TestOptimizeVideoFallsBackWithoutFfmpeg(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "video.mp4")
	if err := os.WriteFile(in, []byte("not a real video"), 0o644); err != nil {
		t.Fatal(err)
	}

	origPath := os.Getenv("PATH")
	os.Setenv("PATH", "") // force "ffmpeg not found"
	defer os.Setenv("PATH", origPath)

	if _, err := optimizeVideo(context.Background(), in); err == nil {
		t.Fatal("expected error when ffmpeg is unavailable")
	}
	if _, err := os.Stat(in); err != nil {
		t.Fatalf("original file should still exist: %v", err)
	}
}
