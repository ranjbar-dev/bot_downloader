package platform

// NewXProvider downloads public X/Twitter videos and animated GIFs using
// yt-dlp. Shared links (including tracking query parameters) pass through
// unchanged, and the best available format is downloaded automatically.
func NewXProvider(binPath string, maxSizeMB int) *YtDlpProvider {
	return NewYtDlpProvider("x", []string{
		"x.com", "www.x.com", "m.x.com", "mobile.x.com",
		"twitter.com", "www.twitter.com", "m.twitter.com", "mobile.twitter.com",
	}, binPath, maxSizeMB)
}
