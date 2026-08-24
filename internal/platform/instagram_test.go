package platform

import "testing"

func TestIsProfileURL(t *testing.T) {
	p := NewInstagramProvider("yt-dlp", 50)
	cases := map[string]bool{
		"https://www.instagram.com/janedoe/":        true,
		"https://instagram.com/janedoe":              true,
		"https://www.instagram.com/p/Cabc123/":       false,
		"https://www.instagram.com/reel/Cabc123/":    false,
		"https://www.instagram.com/stories/janedoe/": false,
		"https://www.instagram.com/":                 false,
	}
	for url, want := range cases {
		if got := p.IsProfileURL(url); got != want {
			t.Errorf("IsProfileURL(%q) = %v, want %v", url, got, want)
		}
	}
}

func TestProfileCaption(t *testing.T) {
	desc := "146K Followers, 200 Following, 88 Posts - See Instagram photos and videos from Jane Doe (@janedoe)"
	want := "👤 Jane Doe\n👥 146K Followers · 200 Following · 88 Posts"
	if got := profileCaption(desc); got != want {
		t.Errorf("profileCaption() = %q, want %q", got, want)
	}

	if got := profileCaption("unparseable text"); got != "unparseable text" {
		t.Errorf("profileCaption() fallback = %q, want passthrough", got)
	}
}

func TestMetaContent(t *testing.T) {
	page := `<meta property="og:image" content="https://example.com/pic.jpg"><meta property="og:description" content="hi &amp; bye">`
	if got := metaContent(page, "og:image"); got != "https://example.com/pic.jpg" {
		t.Errorf("metaContent(og:image) = %q", got)
	}
	if got := metaContent(page, "og:description"); got != "hi & bye" {
		t.Errorf("metaContent(og:description) = %q", got)
	}
}
