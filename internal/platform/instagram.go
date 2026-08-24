package platform

import (
	"context"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"regexp"
	"strings"
	"time"
)

// igReservedPaths are first path segments on instagram.com that are site
// sections, not usernames — a link to one of these is a post/reel/other
// content link, never a profile link.
var igReservedPaths = map[string]bool{
	"p": true, "reel": true, "reels": true, "tv": true, "stories": true,
	"explore": true, "accounts": true, "direct": true, "about": true,
	"developer": true, "legal": true, "privacy": true, "terms": true,
}

// igDescriptionRe pulls follower/following/post counts and the display name
// out of Instagram's og:description meta tag, which reads e.g.
// "146K Followers, 200 Following, 88 Posts - See Instagram photos and videos
// from Jane Doe (@janedoe)".
var igDescriptionRe = regexp.MustCompile(`^([\d.,KMkm]+) Followers, ([\d.,KMkm]+) Following, ([\d.,KMkm]+) Posts - See Instagram photos and videos from (.+) \(@[^)]+\)$`)

// InstagramProfile is implemented by providers that can also fetch profile
// avatar+bio metadata for a plain profile link, as opposed to downloading
// post/reel/story content. The bot type-asserts for this to show the
// story-vs-profile-info prompt.
type InstagramProfile interface {
	IsProfileURL(rawURL string) bool
	DownloadProfile(ctx context.Context, rawURL, destDir string) ([]MediaFile, error)
}

// InstagramProvider downloads posts/reels/stories via yt-dlp as usual, and
// additionally scrapes public profile pages (avatar + bio) without needing a
// login — the og:image/og:description meta tags are rendered server-side for
// link previews even behind Instagram's logged-out content wall.
type InstagramProvider struct {
	*YtDlpProvider
	client *http.Client
}

func NewInstagramProvider(binPath string, maxSizeMB int) *InstagramProvider {
	return &InstagramProvider{
		YtDlpProvider: NewYtDlpProvider("instagram",
			[]string{"instagram.com", "www.instagram.com", "instagr.am"},
			binPath, maxSizeMB),
		client: &http.Client{Timeout: 30 * time.Second},
	}
}

// IsProfileURL reports whether rawURL points at a profile (instagram.com/<username>)
// rather than a post, reel, story, or other site section.
func (p *InstagramProvider) IsProfileURL(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	segs := strings.Split(strings.Trim(u.Path, "/"), "/")
	return len(segs) == 1 && segs[0] != "" && !igReservedPaths[strings.ToLower(segs[0])]
}

// DownloadProfile fetches rawURL's public page, pulls the avatar image and
// bio out of its meta tags, and returns the avatar as a single photo with
// the bio as its caption.
func (p *InstagramProvider) DownloadProfile(ctx context.Context, rawURL, destDir string) ([]MediaFile, error) {
	page, err := p.fetch(ctx, rawURL)
	if err != nil {
		return nil, fmt.Errorf("fetch profile page: %w", err)
	}

	avatarURL := metaContent(page, "og:image")
	if avatarURL == "" {
		return nil, fmt.Errorf("no avatar found on profile page")
	}
	caption := profileCaption(metaContent(page, "og:description"))

	imgPath := path.Join(destDir, "avatar.jpg")
	if err := p.download(ctx, avatarURL, imgPath); err != nil {
		return nil, fmt.Errorf("download avatar: %w", err)
	}
	return []MediaFile{{Path: imgPath, Kind: KindPhoto, Caption: caption}}, nil
}

func (p *InstagramProvider) fetch(ctx context.Context, rawURL string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", browserUA)

	resp, err := p.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	// ponytail: profile pages run a few hundred KB; cap the read so a
	// redirect to something huge can't balloon memory on a 1GB VPS.
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return "", err
	}
	return string(body), nil
}

func (p *InstagramProvider) download(ctx context.Context, srcURL, destPath string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srcURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", browserUA)

	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	f, err := os.Create(destPath)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, io.LimitReader(resp.Body, 32<<20))
	return err
}

var metaTagRe = regexp.MustCompile(`<meta\s+property="([^"]+)"\s+content="([^"]*)"`)

// metaContent returns the (HTML-unescaped) content of the first
// <meta property="..."> tag matching prop.
func metaContent(page, prop string) string {
	for _, m := range metaTagRe.FindAllStringSubmatch(page, -1) {
		if m[1] == prop {
			return html.UnescapeString(m[2])
		}
	}
	return ""
}

// profileCaption turns og:description ("146K Followers, 200 Following, 88
// Posts - See Instagram photos and videos from Jane Doe (@janedoe)") into a
// client-facing caption. Falls back to the raw description if the format
// doesn't match (Instagram changed it, or the logged-out wall replaced it).
func profileCaption(desc string) string {
	if m := igDescriptionRe.FindStringSubmatch(desc); m != nil {
		return fmt.Sprintf("👤 %s\n👥 %s Followers · %s Following · %s Posts", m[4], m[1], m[2], m[3])
	}
	return desc
}
