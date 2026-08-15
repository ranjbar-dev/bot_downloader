package platform

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

// browserUA is sent when scraping an xHamster video page. The page renders
// without the source URLs for clients that don't look like a browser.
const browserUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/151.0.0.0 Safari/537.36"

// initialsMarker prefixes the JSON blob the video page embeds all its
// server-rendered state in (`<script>window.initials={...}</script>`).
const initialsMarker = "window.initials="

// XHamsterProvider downloads from xHamster by pulling the signed mp4 URLs out
// of the video page itself, then handing the direct URL to yt-dlp.
//
// yt-dlp's own XHamster extractor reads `initials.xplayerSettings`, which the
// site has served as null since ~2026-07 — it finds no formats and errors out
// (verified against stable 2026.07.04 and nightly). The URLs are still in the
// same page under `initials.downloadDropdownComponent.sources.mp4`, no login
// or JS needed, so that's what this reads.
//
// The URLs it returns are signed, expire in a few hours, and are bound to the
// IP that fetched the page — resolve and download must happen on the same
// host, which they do (both run here).
type XHamsterProvider struct {
	*YtDlpProvider
	client *http.Client
}

func NewXHamsterProvider(binPath string, maxSizeMB int) *XHamsterProvider {
	return &XHamsterProvider{
		YtDlpProvider: NewYtDlpProviderWithQuality("xhamster",
			[]string{"*xhamster*", "*xhday.*", "*xhvid.*"},
			binPath, maxSizeMB, VideoQualities),
		client: &http.Client{Timeout: 60 * time.Second},
	}
}

// xhSource is one downloadable rendition of a video.
type xhSource struct {
	Height int    // 720, from the "720p" key
	URL    string // signed, IP-bound, expires
	Size   int64  // bytes, as reported by the page (0 if absent)
}

// Qualities lists the renditions the page actually offers. A nil return means
// no quality prompt — the bot then calls Download, which falls back to letting
// yt-dlp try the page URL itself.
func (p *XHamsterProvider) Qualities(ctx context.Context, rawURL string) []Quality {
	sources, err := p.sources(ctx, rawURL)
	if err != nil || len(sources) == 0 {
		return nil
	}
	qualities := make([]Quality, 0, len(sources))
	for _, s := range sources {
		label := fmt.Sprintf("%dp", s.Height)
		qualities = append(qualities, Quality{Label: sizedLabel(label, s.Size), Value: label})
	}
	return qualities
}

func (p *XHamsterProvider) Download(ctx context.Context, rawURL, destDir string) ([]MediaFile, error) {
	return p.DownloadWithQuality(ctx, rawURL, destDir, "")
}

// DownloadWithQuality resolves quality ("720p", or "" for the best available)
// to a direct mp4 URL and downloads that. If the page can't be parsed — site
// redesign, or yt-dlp's extractor started working again — it falls back to
// pointing yt-dlp at the original page URL.
func (p *XHamsterProvider) DownloadWithQuality(ctx context.Context, rawURL, destDir, quality string) ([]MediaFile, error) {
	sources, err := p.sources(ctx, rawURL)
	if err != nil || len(sources) == 0 {
		return p.YtDlpProvider.download(ctx, rawURL, destDir, "")
	}
	direct := sources[0].URL // sources are sorted best-first
	for _, s := range sources {
		if fmt.Sprintf("%dp", s.Height) == quality {
			direct = s.URL
			break
		}
	}
	return p.YtDlpProvider.download(ctx, direct, destDir, "")
}

// sources fetches the video page and returns its renditions, best first.
func (p *XHamsterProvider) sources(ctx context.Context, rawURL string) ([]xhSource, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", browserUA)

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch page: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch page: HTTP %d", resp.StatusCode)
	}
	// ponytail: the page is ~300KB; cap the read so a redirect to something
	// huge can't balloon memory on a 1GB VPS.
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, fmt.Errorf("read page: %w", err)
	}
	return parseXHamsterSources(string(body))
}

// parseXHamsterSources pulls the rendition list out of a video page's
// window.initials blob. Split out from sources() so it can be tested without
// hitting the network.
func parseXHamsterSources(page string) ([]xhSource, error) {
	idx := strings.Index(page, initialsMarker)
	if idx < 0 {
		return nil, fmt.Errorf("no %s found in page", initialsMarker)
	}

	// json.Decoder stops at the end of the first complete value, so the
	// trailing `;</script>...` and the rest of the page are simply ignored.
	var initials struct {
		DownloadDropdown struct {
			Sources struct {
				MP4      map[string]string `json:"mp4"`
				Download map[string]struct {
					Size float64 `json:"size"`
				} `json:"download"`
			} `json:"sources"`
		} `json:"downloadDropdownComponent"`
	}
	dec := json.NewDecoder(strings.NewReader(page[idx+len(initialsMarker):]))
	if err := dec.Decode(&initials); err != nil {
		return nil, fmt.Errorf("parse initials: %w", err)
	}

	src := initials.DownloadDropdown.Sources
	sources := make([]xhSource, 0, len(src.MP4))
	for label, u := range src.MP4 {
		height, err := strconv.Atoi(strings.TrimSuffix(label, "p"))
		if err != nil || u == "" {
			continue
		}
		sources = append(sources, xhSource{Height: height, URL: u, Size: int64(src.Download[label].Size)})
	}
	if len(sources) == 0 {
		return nil, fmt.Errorf("no mp4 sources in page")
	}
	sort.Slice(sources, func(i, j int) bool { return sources[i].Height > sources[j].Height })
	return sources, nil
}
