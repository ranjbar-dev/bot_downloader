package platform

import (
	"context"
	"fmt"
	"html"
	"io"
	"net/http"
	"regexp"
	"time"
)

// pornflipSourceRe extracts the HLS master playlist URL from a video page's
// <source type="application/x-mpegURL"> tag.
var pornflipSourceRe = regexp.MustCompile(`<source src="([^"]+)"[^>]*type="application/x-mpegURL"`)

// PornFlipProvider downloads from PornFlip by pulling the HLS master playlist
// URL out of the video page itself, then handing that URL to yt-dlp.
//
// yt-dlp's own PornFlip extractor looks for a `mpd_url` the site no longer
// serves (it switched to HLS) and errors out (verified against stable
// 2026.07.04). The <source type="application/x-mpegURL"> tag on the same page
// still has a working master.m3u8 URL, which yt-dlp's generic HLS handling
// downloads fine, so that's what this reads.
type PornFlipProvider struct {
	*YtDlpProvider
	client *http.Client
}

func NewPornFlipProvider(binPath string, maxSizeMB int) *PornFlipProvider {
	return &PornFlipProvider{
		YtDlpProvider: NewYtDlpProvider("pornflip",
			[]string{"pornflip.com", "www.pornflip.com"},
			binPath, maxSizeMB),
		client: &http.Client{Timeout: 60 * time.Second},
	}
}

// Download resolves rawURL to its HLS master playlist and downloads that. If
// the page can't be parsed — site redesign, or yt-dlp's extractor started
// working again — it falls back to pointing yt-dlp at the original page URL.
func (p *PornFlipProvider) Download(ctx context.Context, rawURL, destDir string) ([]MediaFile, error) {
	streamURL, err := p.streamURL(ctx, rawURL)
	if err != nil {
		return p.YtDlpProvider.download(ctx, rawURL, destDir, "")
	}
	return p.YtDlpProvider.download(ctx, streamURL, destDir, "")
}

func (p *PornFlipProvider) streamURL(ctx context.Context, rawURL string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", browserUA)

	resp, err := p.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch page: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("fetch page: HTTP %d", resp.StatusCode)
	}
	// ponytail: the page is well under 1MB; cap the read so a redirect to
	// something huge can't balloon memory on a 1GB VPS.
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return "", fmt.Errorf("read page: %w", err)
	}

	m := pornflipSourceRe.FindSubmatch(body)
	if m == nil {
		return "", fmt.Errorf("no HLS source found in page")
	}
	return html.UnescapeString(string(m[1])), nil
}
