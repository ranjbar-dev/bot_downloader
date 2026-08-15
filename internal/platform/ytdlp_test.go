package platform

import (
	"net/url"
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
