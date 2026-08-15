package platform

import (
	"context"
	"fmt"
	"net/url"
	"os/exec"
	"path/filepath"
	"strings"
)

// Quality is one selectable download option shown to the user as a button.
// Value is appended (whitespace-split) to the yt-dlp argument list verbatim
// — e.g. "-f bv*[height<=720]+ba/b[height<=720]" or "-x --audio-format mp3".
// An empty Value means yt-dlp's own default (best).
type Quality struct {
	Label string
	Value string
}

// VideoQualities is the generic quality ladder for video sites.
var VideoQualities = []Quality{
	{Label: "🎬 Best", Value: ""},
	{Label: "1080p", Value: "-f bv*[height<=1080]+ba/b[height<=1080]"},
	{Label: "720p", Value: "-f bv*[height<=720]+ba/b[height<=720]"},
	{Label: "360p", Value: "-f bv*[height<=360]+ba/b[height<=360]"},
}

// YouTubeQualities adds an audio-only option on top of VideoQualities.
var YouTubeQualities = []Quality{
	{Label: "🎬 Best (video)", Value: ""},
	{Label: "1080p", Value: "-f bv*[height<=1080]+ba/b[height<=1080]"},
	{Label: "720p", Value: "-f bv*[height<=720]+ba/b[height<=720]"},
	{Label: "360p", Value: "-f bv*[height<=360]+ba/b[height<=360]"},
	{Label: "🎵 Audio only (mp3)", Value: "-x --audio-format mp3"},
}

// QualityProvider is implemented by providers that offer more than one
// download option. The bot shows Qualities() as buttons and calls
// DownloadWithQuality with the chosen Value once the user picks one.
type QualityProvider interface {
	Provider
	Qualities() []Quality
	DownloadWithQuality(ctx context.Context, rawURL, destDir, quality string) ([]MediaFile, error)
}

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
	qualities []Quality // nil = no quality prompt, always download best
}

func NewYtDlpProvider(name string, hosts []string, binPath string, maxSizeMB int) *YtDlpProvider {
	return newYtDlpProvider(name, hosts, binPath, maxSizeMB, nil)
}

// NewYtDlpProviderWithQuality is NewYtDlpProvider plus a set of selectable
// qualities (see Quality/QualityProvider) — the bot prompts for one of these
// before downloading instead of always grabbing yt-dlp's default best.
func NewYtDlpProviderWithQuality(name string, hosts []string, binPath string, maxSizeMB int, qualities []Quality) *YtDlpProvider {
	return newYtDlpProvider(name, hosts, binPath, maxSizeMB, qualities)
}

func newYtDlpProvider(name string, hosts []string, binPath string, maxSizeMB int, qualities []Quality) *YtDlpProvider {
	h := make(map[string]bool, len(hosts))
	for _, host := range hosts {
		h[strings.ToLower(host)] = true
	}
	if binPath == "" {
		binPath = "yt-dlp"
	}
	return &YtDlpProvider{name: name, hosts: h, binPath: binPath, maxSizeMB: maxSizeMB, qualities: qualities}
}

func (p *YtDlpProvider) Name() string { return p.name }

func (p *YtDlpProvider) Match(u *url.URL) bool {
	return p.hosts[strings.ToLower(u.Hostname())]
}

func (p *YtDlpProvider) Qualities() []Quality { return p.qualities }

func (p *YtDlpProvider) Download(ctx context.Context, rawURL string, destDir string) ([]MediaFile, error) {
	return p.download(ctx, rawURL, destDir, "")
}

func (p *YtDlpProvider) DownloadWithQuality(ctx context.Context, rawURL, destDir, quality string) ([]MediaFile, error) {
	return p.download(ctx, rawURL, destDir, quality)
}

func (p *YtDlpProvider) baseArgs(outTemplate, quality string) []string {
	args := []string{
		"--no-playlist",
		"--output", outTemplate,
		"--no-warnings",
		"--print", "after_move:filepath",
	}
	if p.maxSizeMB > 0 {
		args = append(args, "--max-filesize", fmt.Sprintf("%dM", p.maxSizeMB))
	}
	if quality != "" {
		args = append(args, strings.Fields(quality)...)
	}
	return args
}

// download shells out to yt-dlp with an argument slice — never a shell
// string — so nothing in rawURL can break out into a shell command.
func (p *YtDlpProvider) download(ctx context.Context, rawURL, destDir, quality string) ([]MediaFile, error) {
	outTemplate := filepath.Join(destDir, "%(id)s.%(ext)s")
	args := append(p.baseArgs(outTemplate, quality), rawURL)

	cmd := exec.CommandContext(ctx, p.binPath, args...)
	out, err := cmd.CombinedOutput()

	// ponytail: image-only Instagram carousels have no video formats, and
	// yt-dlp errors instead of just grabbing the images (yt-dlp/yt-dlp#17077).
	// Retry asking for thumbnails only; yt-dlp still exits non-zero here even
	// on success, so fall back to checking destDir for what actually landed.
	if err != nil && strings.Contains(string(out), "No video formats found") {
		retryArgs := append(p.baseArgs(outTemplate, quality),
			"--write-thumbnail", "--skip-download", "--ignore-no-formats-error", rawURL)
		cmd = exec.CommandContext(ctx, p.binPath, retryArgs...)
		retryOut, _ := cmd.CombinedOutput()
		// The thumbnail-only path never fires the after_move print hook, so
		// go straight to listing what actually landed in destDir.
		paths, _ := filepath.Glob(filepath.Join(destDir, "*"))
		if len(paths) == 0 {
			return nil, fmt.Errorf("yt-dlp failed: %s", truncate(string(retryOut), 500))
		}
		return toMediaFiles(paths), nil
	}
	if err != nil {
		return nil, fmt.Errorf("yt-dlp failed: %w (%s)", err, truncate(string(out), 500))
	}

	paths := parsePrintedPaths(string(out))
	if len(paths) == 0 {
		return nil, fmt.Errorf("yt-dlp produced no output files")
	}
	return toMediaFiles(paths), nil
}

func toMediaFiles(paths []string) []MediaFile {
	files := make([]MediaFile, 0, len(paths))
	for _, path := range paths {
		files = append(files, MediaFile{Path: path, Kind: KindFromExt(path)})
	}
	return files
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
