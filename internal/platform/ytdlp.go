package platform

import (
	"context"
	"fmt"
	"net/url"
	"os/exec"
	"path/filepath"
	"strings"
)

// YtDlpProvider is a generic Provider backed by the yt-dlp binary. Most
// video/photo sites (Instagram, YouTube, TikTok, Twitter/X, ...) work through
// yt-dlp out of the box — adding one of those later is just another
// NewYtDlpProvider() call in main.go, no new code. Sites yt-dlp can't reach
// (e.g. Spotify) need a real new Provider implementation instead.
type YtDlpProvider struct {
	name      string
	hosts     map[string]bool
	binPath   string
	maxSizeMB int
}

func NewYtDlpProvider(name string, hosts []string, binPath string, maxSizeMB int) *YtDlpProvider {
	h := make(map[string]bool, len(hosts))
	for _, host := range hosts {
		h[strings.ToLower(host)] = true
	}
	if binPath == "" {
		binPath = "yt-dlp"
	}
	return &YtDlpProvider{name: name, hosts: h, binPath: binPath, maxSizeMB: maxSizeMB}
}

func (p *YtDlpProvider) Name() string { return p.name }

func (p *YtDlpProvider) Match(u *url.URL) bool {
	return p.hosts[strings.ToLower(u.Hostname())]
}

// Download shells out to yt-dlp with an argument slice — never a shell
// string — so nothing in rawURL can break out into a shell command.
func (p *YtDlpProvider) Download(ctx context.Context, rawURL string, destDir string) ([]MediaFile, error) {
	outTemplate := filepath.Join(destDir, "%(id)s.%(ext)s")
	args := []string{
		"--no-playlist",
		"--output", outTemplate,
		"--no-warnings",
		"--print", "after_move:filepath",
	}
	if p.maxSizeMB > 0 {
		args = append(args, "--max-filesize", fmt.Sprintf("%dM", p.maxSizeMB))
	}
	args = append(args, rawURL)

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

func parsePrintedPaths(out string) []string {
	var paths []string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "[") {
			paths = append(paths, line)
		}
	}
	return paths
}

// KindFromExt infers MediaKind from a file's extension. Exported so callers
// that already have a file on disk (e.g. the cache, on a hit) can classify
// it without re-running a Provider.
func KindFromExt(path string) MediaKind {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".jpg", ".jpeg", ".png", ".webp":
		return KindPhoto
	case ".mp3", ".m4a", ".opus", ".flac":
		return KindAudio
	default:
		return KindVideo
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
