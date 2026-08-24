// Package platform defines the pluggable download-source abstraction.
// Add a new source (YouTube, TikTok, Spotify, ...) by implementing Provider
// and registering it in cmd/igsave-bot/main.go — nothing else in the bot
// needs to change.
package platform

import (
	"context"
	"fmt"
	"net/url"
)

type MediaKind int

const (
	KindVideo MediaKind = iota
	KindPhoto
	KindAudio
)

type MediaFile struct {
	Path string
	Kind MediaKind
	// Caption overrides the bot's default "via @bot / link" caption when
	// non-empty — used for content (e.g. Instagram profile info) that needs
	// its own client-facing text instead.
	Caption string
}

// Provider handles one source site. Match decides ownership of a URL;
// Download fetches the media into destDir and returns the resulting files.
type Provider interface {
	Name() string
	Match(u *url.URL) bool
	Download(ctx context.Context, rawURL string, destDir string) ([]MediaFile, error)
}

type Registry struct {
	providers []Provider
}

func NewRegistry(providers ...Provider) *Registry {
	return &Registry{providers: providers}
}

var ErrNoProvider = fmt.Errorf("no provider matches this link")

func (r *Registry) Find(rawURL string) (Provider, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("invalid url: %w", err)
	}
	for _, p := range r.providers {
		if p.Match(u) {
			return p, nil
		}
	}
	return nil, ErrNoProvider
}
