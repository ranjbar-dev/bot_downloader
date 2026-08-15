package platform

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/dustin/go-humanize"
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
	// Qualities probes rawURL for the formats it actually has and returns a
	// ladder sized to that video (falls back to a generic static ladder if
	// probing fails). Empty means "no quality prompt, always download best".
	Qualities(ctx context.Context, rawURL string) []Quality
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

// Qualities returns nil if this provider has no quality ladder configured.
// Otherwise it probes rawURL for the formats/sizes actually available and
// builds a ladder sized to that video; if probing fails (network hiccup,
// yt-dlp error) it falls back to the generic static ladder so the bot still
// offers something rather than erroring out.
func (p *YtDlpProvider) Qualities(ctx context.Context, rawURL string) []Quality {
	if p.qualities == nil {
		return nil
	}
	if q, err := p.probeQualities(ctx, rawURL); err == nil {
		return q
	}
	return p.qualities
}

// ytFormat mirrors the subset of yt-dlp's per-format JSON fields (from
// `yt-dlp -j`) needed to size a quality ladder.
type ytFormat struct {
	Height         int     `json:"height"`
	VCodec         string  `json:"vcodec"`
	ACodec         string  `json:"acodec"`
	Filesize       int64   `json:"filesize"`
	FilesizeApprox int64   `json:"filesize_approx"`
	ABR            float64 `json:"abr"`
}

func (f ytFormat) size() int64 {
	if f.Filesize > 0 {
		return f.Filesize
	}
	return f.FilesizeApprox
}

type ytInfo struct {
	Duration float64    `json:"duration"`
	Formats  []ytFormat `json:"formats"`
}

// qualityHeights is the candidate ladder; probeQualities only offers the
// ones actually present in the video's formats.
var qualityHeights = []int{2160, 1440, 1080, 720, 480, 360, 240}

// probeQualities asks yt-dlp for rawURL's format list and builds a quality
// ladder (with human-readable approximate sizes) from what's actually
// available, instead of a fixed guess.
func (p *YtDlpProvider) probeQualities(ctx context.Context, rawURL string) ([]Quality, error) {
	args := []string{
		"--no-playlist", "--no-warnings", "-j",
		"--extractor-args", "youtube:player_client=android_vr",
		rawURL,
	}
	out, err := exec.CommandContext(ctx, p.binPath, args...).Output()
	if err != nil {
		return nil, fmt.Errorf("probe formats: %w", err)
	}
	var info ytInfo
	if err := json.Unmarshal(out, &info); err != nil {
		return nil, fmt.Errorf("parse formats: %w", err)
	}

	var bestAudioSize int64
	var bestAudioKbps float64
	byHeight := map[int]int64{}
	maxHeight := 0
	for _, f := range info.Formats {
		hasVideo := f.VCodec != "" && f.VCodec != "none"
		hasAudio := f.ACodec != "" && f.ACodec != "none"
		if hasAudio && !hasVideo {
			if s := f.size(); s > bestAudioSize {
				bestAudioSize = s
			}
			if f.ABR > bestAudioKbps {
				bestAudioKbps = f.ABR
			}
		}
		if hasVideo && f.Height > 0 {
			if s := f.size(); s > byHeight[f.Height] {
				byHeight[f.Height] = s
			}
			if f.Height > maxHeight {
				maxHeight = f.Height
			}
		}
	}
	if maxHeight == 0 {
		return nil, fmt.Errorf("no video formats found")
	}

	qualities := []Quality{
		{Label: sizedLabel("🎬 Best", byHeight[maxHeight]+bestAudioSize), Value: ""},
	}
	for _, h := range qualityHeights {
		size, ok := byHeight[h]
		if !ok || h == maxHeight {
			continue
		}
		qualities = append(qualities, Quality{
			Label: sizedLabel(fmt.Sprintf("%dp", h), size+bestAudioSize),
			Value: fmt.Sprintf("-f bv*[height<=%d]+ba/b[height<=%d]", h, h),
		})
	}
	audioSize := bestAudioSize
	if audioSize == 0 && info.Duration > 0 && bestAudioKbps > 0 {
		audioSize = int64(info.Duration * bestAudioKbps * 1000 / 8)
	}
	qualities = append(qualities, Quality{Label: sizedLabel("🎵 Audio only (mp3)", audioSize), Value: "-x --audio-format mp3"})

	return qualities, nil
}

func sizedLabel(label string, size int64) string {
	if size <= 0 {
		return label
	}
	return fmt.Sprintf("%s (~%s)", label, humanize.Bytes(uint64(size)))
}

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
		// ponytail: YouTube's web player now serves SABR-only formats that
		// 403 on direct download (yt-dlp/yt-dlp#16729). tv/android clients
		// dodge the 403 too but as of 2026-07 only expose progressive 360p;
		// android_vr still gets full separate-stream DASH (144p-1440p+) and
		// actually completes the download. Harmless no-op on non-YouTube hosts.
		"--extractor-args", "youtube:player_client=android_vr",
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

	var out []byte
	var err error
	// ponytail: android_vr-client format URLs 403 intermittently (signed URL
	// hiccup/edge blip, confirmed by hand: a failed download succeeds outright
	// on an immediate retry with no code change) - a few retries clear it
	// without needing a smarter fix.
	for range 3 {
		cmd := exec.CommandContext(ctx, p.binPath, args...)
		out, err = cmd.CombinedOutput()
		if err == nil || !strings.Contains(string(out), "HTTP Error 403") {
			break
		}
	}

	// ponytail: image-only Instagram carousels have no video formats, and
	// yt-dlp errors instead of just grabbing the images (yt-dlp/yt-dlp#17077).
	// Retry asking for thumbnails only; yt-dlp still exits non-zero here even
	// on success, so fall back to checking destDir for what actually landed.
	if err != nil && strings.Contains(string(out), "No video formats found") {
		retryArgs := append(p.baseArgs(outTemplate, quality),
			"--write-thumbnail", "--skip-download", "--ignore-no-formats-error", rawURL)
		retryCmd := exec.CommandContext(ctx, p.binPath, retryArgs...)
		retryOut, _ := retryCmd.CombinedOutput()
		// The thumbnail-only path never fires the after_move print hook, so
		// go straight to listing what actually landed in destDir.
		paths, _ := filepath.Glob(filepath.Join(destDir, "*"))
		if len(paths) == 0 {
			return nil, fmt.Errorf("yt-dlp failed: %s", truncate(string(retryOut), 500))
		}
		return toMediaFiles(paths), nil
	}
	if err != nil {
		// ponytail: yt-dlp can exit non-zero on a harmless post-merge cleanup
		// hiccup (e.g. a temp file still locked by AV/indexing on Windows)
		// after the real output file was already written and printed - trust
		// that print over the exit code if the file is actually there.
		if paths := existingPaths(parsePrintedPaths(string(out))); len(paths) > 0 {
			return toMediaFiles(paths), nil
		}
		return nil, fmt.Errorf("yt-dlp failed: %w (%s)", err, truncate(string(out), 500))
	}

	paths := parsePrintedPaths(string(out))
	if len(paths) == 0 {
		// ponytail: yt-dlp aborts silently (exit 0, no file) when the real
		// filesize — only known mid-download on sites that don't report it
		// upfront — exceeds --max-filesize. It only logs why at -v, so the
		// generic message is otherwise a dead end to debug.
		if p.maxSizeMB > 0 {
			return nil, fmt.Errorf("yt-dlp produced no output files (likely exceeds --max-filesize %dM)", p.maxSizeMB)
		}
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

// existingPaths filters paths down to the ones that actually exist on disk.
func existingPaths(paths []string) []string {
	var found []string
	for _, path := range paths {
		if _, err := os.Stat(path); err == nil {
			found = append(found, path)
		}
	}
	return found
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
