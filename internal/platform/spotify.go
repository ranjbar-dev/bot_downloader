package platform

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os/exec"
	"path/filepath"
	"strings"
)

// SpotifyProvider handles open.spotify.com track links. Spotify's own audio
// is DRM-protected and yt-dlp has no extractor for it, so this resolves the
// track title via Spotify's public oEmbed endpoint (no API key required)
// and downloads the best-matching audio from YouTube instead.
type SpotifyProvider struct {
	binPath   string
	maxSizeMB int
}

func NewSpotifyProvider(binPath string, maxSizeMB int) *SpotifyProvider {
	if binPath == "" {
		binPath = "yt-dlp"
	}
	return &SpotifyProvider{binPath: binPath, maxSizeMB: maxSizeMB}
}

func (p *SpotifyProvider) Name() string { return "spotify" }

func (p *SpotifyProvider) Match(u *url.URL) bool {
	host := strings.ToLower(u.Hostname())
	return (host == "open.spotify.com" || host == "spotify.com") && strings.Contains(u.Path, "/track/")
}

func (p *SpotifyProvider) Download(ctx context.Context, rawURL string, destDir string) ([]MediaFile, error) {
	title, err := spotifyOEmbedTitle(ctx, rawURL)
	if err != nil {
		return nil, fmt.Errorf("resolve spotify track: %w", err)
	}

	outTemplate := filepath.Join(destDir, "%(id)s.%(ext)s")
	args := []string{
		"--no-playlist",
		"-x", "--audio-format", "mp3",
		"--output", outTemplate,
		"--no-warnings",
		"--print", "after_move:filepath",
	}
	if p.maxSizeMB > 0 {
		args = append(args, "--max-filesize", fmt.Sprintf("%dM", p.maxSizeMB))
	}
	// "ytsearch1:" prefix makes yt-dlp search YouTube and take the top hit,
	// and guarantees the arg can't be mistaken for a yt-dlp flag.
	args = append(args, "ytsearch1:"+title)

	cmd := exec.CommandContext(ctx, p.binPath, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("yt-dlp failed: %w (%s)", err, truncate(string(out), 500))
	}

	paths := parsePrintedPaths(string(out))
	if len(paths) == 0 {
		return nil, fmt.Errorf("yt-dlp produced no output files")
	}
	files := make([]MediaFile, 0, len(paths))
	for _, path := range paths {
		files = append(files, MediaFile{Path: path, Kind: KindFromExt(path)})
	}
	return files, nil
}

type spotifyOEmbed struct {
	Title string `json:"title"`
}

func spotifyOEmbedTitle(ctx context.Context, rawURL string) (string, error) {
	endpoint := "https://open.spotify.com/oembed?url=" + url.QueryEscape(rawURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("oembed status %d", resp.StatusCode)
	}
	var body spotifyOEmbed
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", err
	}
	if body.Title == "" {
		return "", fmt.Errorf("no title in oembed response")
	}
	return body.Title, nil
}
