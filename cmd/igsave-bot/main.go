package main

import (
	"log"
	"os"
	"path/filepath"

	"igsave-bot/internal/bot"
	"igsave-bot/internal/config"
	"igsave-bot/internal/platform"
	"igsave-bot/internal/store"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}

	if err := os.MkdirAll(cfg.CacheDir, 0o755); err != nil {
		log.Fatal("create cache dir:", err)
	}
	if err := os.MkdirAll(filepath.Dir(cfg.DBPath), 0o755); err != nil {
		log.Fatal("create db dir:", err)
	}

	members, err := store.Open(cfg.DBPath)
	if err != nil {
		log.Fatal("open member store:", err)
	}
	defer members.Close()

	// New sources go here: one more NewYtDlpProvider (or a custom Provider
	// for non-yt-dlp sites like Spotify) plus a line in this list.
	registry := platform.NewRegistry(
		platform.NewYtDlpProvider("instagram",
			[]string{"instagram.com", "www.instagram.com", "instagr.am"},
			cfg.YtDlpPath, cfg.MaxUploadMB),
	)

	b := bot.New(cfg, registry, members)
	if err := b.Run(); err != nil {
		log.Fatal(err)
	}
}
