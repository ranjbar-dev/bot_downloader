package config

import (
	"fmt"
	"os"
	"strconv"
)

type Config struct {
	BotToken          string
	GateChannel       int64
	GateInviteLink    string
	DBPath            string
	CacheDir          string
	CacheTTLSeconds   int
	CacheMaxMB        int
	YtDlpPath         string
	MaxUploadMB       int
	WorkerCount       int
	JobTimeoutSeconds int
}

func Load() (*Config, error) {
	gateChannel, err := strconv.ParseInt(os.Getenv("GATE_CHANNEL"), 10, 64)
	if err != nil {
		return nil, fmt.Errorf("GATE_CHANNEL must be a numeric channel id (e.g. -1001234567890): %w", err)
	}

	c := &Config{
		BotToken:          os.Getenv("BOT_TOKEN"),
		GateChannel:       gateChannel,
		GateInviteLink:    os.Getenv("GATE_CHANNEL_INVITE_LINK"),
		DBPath:            getEnvDefault("DB_PATH", "/var/lib/igsave-bot/members.db"),
		CacheDir:          getEnvDefault("CACHE_DIR", "/var/lib/igsave-bot/cache"),
		CacheTTLSeconds:   getEnvIntDefault("CACHE_TTL_SECONDS", 3600),
		CacheMaxMB:        getEnvIntDefault("CACHE_MAX_MB", 10000),
		YtDlpPath:         getEnvDefault("YT_DLP_PATH", "yt-dlp"),
		MaxUploadMB:       getEnvIntDefault("TELEGRAM_MAX_UPLOAD_MB", 50),
		WorkerCount:       getEnvIntDefault("WORKER_COUNT", 2),
		JobTimeoutSeconds: getEnvIntDefault("JOB_TIMEOUT_SECONDS", 120),
	}
	if c.BotToken == "" {
		return nil, fmt.Errorf("BOT_TOKEN required")
	}
	return c, nil
}

func getEnvDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func getEnvIntDefault(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}
