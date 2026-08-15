package main

import (
	"log"
	"os"

	"igsave-bot/internal/bot"
	"igsave-bot/internal/config"
	"igsave-bot/internal/platform"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}

	if err := os.MkdirAll(cfg.CacheDir, 0o755); err != nil {
		log.Fatal("create cache dir:", err)
	}

	// New sources go here: one more NewYtDlpProvider (or a custom Provider
	// for non-yt-dlp sites like Spotify) plus a line in this list.
	registry := platform.NewRegistry(
		platform.NewYtDlpProvider("instagram",
			[]string{"instagram.com", "www.instagram.com", "instagr.am"},
			cfg.YtDlpPath, cfg.MaxUploadMB),
	)

	b := bot.New(cfg, registry)
	if err := b.Run(); err != nil {
		log.Fatal(err)
	}
}
