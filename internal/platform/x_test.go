package platform

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"
)

func TestXRegistryRouting(t *testing.T) {
	x := NewXProvider("yt-dlp", 50)
	registry := NewRegistry(x)
	for _, rawURL := range []string{
		"https://x.com/r0f0ell/status/2096875398680162465?s=46",
		"https://www.x.com/user/status/123/video/1",
		"https://m.x.com/user/status/123",
		"https://mobile.x.com/user/status/123",
		"https://twitter.com/user/status/123",
		"https://www.twitter.com/user/status/123",
		"https://m.twitter.com/user/status/123",
		"https://mobile.twitter.com/user/status/123?s=20",
		"https://X.COM/i/web/status/123",
	} {
		t.Run(rawURL, func(t *testing.T) {
			provider, err := registry.Find(rawURL)
			if err != nil || provider != x {
				t.Fatalf("Find(%q) = %v, %v; want X provider", rawURL, provider, err)
			}
		})
	}
	for _, rawURL := range []string{
		"https://example.com/?url=https://x.com/user/status/123",
		"https://x.com.example.com/user/status/123",
		"https://twitter.com.example.com/user/status/123",
		"https://notx.com/user/status/123",
		"https://x.com@example.com/user/status/123",
		"https://youtube.com/watch?v=123",
	} {
		t.Run(rawURL, func(t *testing.T) {
			if _, err := registry.Find(rawURL); !errors.Is(err, ErrNoProvider) {
				t.Fatalf("Find(%q) error = %v; want ErrNoProvider", rawURL, err)
			}
		})
	}
}

// Run explicitly with X_TEST_URL set to a public video post. This exercises
// the real downloader without making the normal test suite depend on X.
func TestXDownloadIntegration(t *testing.T) {
	rawURL := os.Getenv("X_TEST_URL")
	if rawURL == "" {
		t.Skip("set X_TEST_URL to run a live X download (requires yt-dlp)")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	files, err := NewXProvider("yt-dlp", 50).Download(ctx, rawURL, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("download returned no media")
	}
	for _, file := range files {
		info, err := os.Stat(file.Path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Size() == 0 || file.Kind != KindVideo {
			t.Fatalf("expected nonempty video, got %+v (%d bytes)", file, info.Size())
		}
	}
}
