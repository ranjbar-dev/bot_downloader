// Package cache persists downloaded media on disk for a configurable TTL, so
// repeat requests for the same link (from any user) are served from disk
// instead of re-downloading.
package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"log"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"igsave-bot/internal/platform"
)

type Cache struct {
	dir      string
	ttl      time.Duration
	maxBytes int64 // 0 = unlimited
	keys     keyedLock
}

// New creates a Cache rooted at dir. maxBytes caps total cache size (0 =
// unlimited) - enforced by the sweeper, oldest entries evicted first, so
// actual usage can transiently exceed the cap between sweeps.
func New(dir string, ttl time.Duration, maxBytes int64) *Cache {
	return &Cache{dir: dir, ttl: ttl, maxBytes: maxBytes}
}

// Key derives a cache key from a raw URL and the requested quality (empty
// for providers with no quality choice). Same pair -> same key; differing
// query params/tracking junk, or a different quality pick, are distinct
// entries.
func Key(rawURL, quality string) string {
	sum := sha256.Sum256([]byte(rawURL + "|" + quality))
	return hex.EncodeToString(sum[:])
}

func (c *Cache) dirFor(key string) string {
	return filepath.Join(c.dir, key)
}

func (c *Cache) doneMarker(key string) string {
	return filepath.Join(c.dirFor(key), ".done")
}

// Lock serializes access to one cache key so concurrent requests for the
// same link don't download it twice. Call the returned func to release.
func (c *Cache) Lock(key string) func() {
	return c.keys.Lock(key)
}

// Lookup returns cached files for key if a completed, non-expired entry
// exists. Caller must hold the key's lock.
func (c *Cache) Lookup(key string) ([]platform.MediaFile, bool) {
	info, err := os.Stat(c.doneMarker(key))
	if err != nil || time.Since(info.ModTime()) > c.ttl {
		return nil, false
	}
	entries, err := os.ReadDir(c.dirFor(key))
	if err != nil {
		return nil, false
	}
	var files []platform.MediaFile
	for _, e := range entries {
		if e.IsDir() || e.Name() == ".done" {
			continue
		}
		path := filepath.Join(c.dirFor(key), e.Name())
		files = append(files, platform.MediaFile{Path: path, Kind: platform.KindFromExt(path)})
	}
	if len(files) == 0 {
		return nil, false
	}
	return files, true
}

// PrepareDir clears any stale/partial contents and returns a fresh
// directory for key to download into. Caller must hold the key's lock.
func (c *Cache) PrepareDir(key string) (string, error) {
	dir := c.dirFor(key)
	if err := os.RemoveAll(dir); err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

// MarkDone records a successful download so Lookup treats the entry as
// valid until the TTL elapses. Caller must hold the key's lock.
func (c *Cache) MarkDone(key string) error {
	return os.WriteFile(c.doneMarker(key), nil, 0o644)
}

// Abandon removes a partially-downloaded entry after a failed download.
// Caller must hold the key's lock.
func (c *Cache) Abandon(key string) {
	if err := os.RemoveAll(c.dirFor(key)); err != nil {
		log.Println("cache: abandon cleanup failed:", err)
	}
}

// SweepLoop periodically deletes cache entries older than the TTL. Runs
// until ctx is cancelled by the caller (or process exit).
func (c *Cache) SweepLoop(stop <-chan struct{}) {
	interval := max(c.ttl/2, time.Minute)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			c.sweepOnce()
		case <-stop:
			return
		}
	}
}

func (c *Cache) sweepOnce() {
	entries, err := os.ReadDir(c.dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		key := e.Name()
		release := c.keys.Lock(key)
		info, err := os.Stat(c.doneMarker(key))
		expired := err != nil || time.Since(info.ModTime()) > c.ttl
		if expired {
			if err := os.RemoveAll(c.dirFor(key)); err != nil {
				log.Println("cache: sweep remove failed:", err)
			}
		}
		release()
	}

	if c.maxBytes > 0 {
		c.enforceCap()
	}
}

type cacheEntry struct {
	key   string
	mtime time.Time
	size  int64
}

// enforceCap evicts complete entries oldest-first (by .done mtime) until
// total size is back under maxBytes. In-progress downloads are skipped -
// Lock blocks until any active download for that key finishes or aborts.
func (c *Cache) enforceCap() {
	dirEntries, err := os.ReadDir(c.dir)
	if err != nil {
		return
	}

	var entries []cacheEntry
	var total int64
	for _, e := range dirEntries {
		if !e.IsDir() {
			continue
		}
		key := e.Name()
		info, err := os.Stat(c.doneMarker(key))
		if err != nil {
			continue // not a complete entry, leave it for the TTL pass
		}
		size := dirSize(c.dirFor(key))
		entries = append(entries, cacheEntry{key: key, mtime: info.ModTime(), size: size})
		total += size
	}
	if total <= c.maxBytes {
		return
	}

	sort.Slice(entries, func(i, j int) bool { return entries[i].mtime.Before(entries[j].mtime) })
	for _, ce := range entries {
		if total <= c.maxBytes {
			break
		}
		release := c.keys.Lock(ce.key)
		if err := os.RemoveAll(c.dirFor(ce.key)); err != nil {
			log.Println("cache: cap eviction failed:", err)
		} else {
			total -= ce.size
		}
		release()
	}
}

func dirSize(dir string) int64 {
	var total int64
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	for _, e := range entries {
		if info, err := e.Info(); err == nil {
			total += info.Size()
		}
	}
	return total
}

// keyedLock hands out one mutex per key, reference-counted so the map
// doesn't grow unbounded as distinct URLs accumulate over the bot's uptime.
type keyedLock struct {
	mu    sync.Mutex
	locks map[string]*lockEntry
}

type lockEntry struct {
	mu   sync.Mutex
	refs int
}

func (k *keyedLock) Lock(key string) func() {
	k.mu.Lock()
	if k.locks == nil {
		k.locks = make(map[string]*lockEntry)
	}
	e, ok := k.locks[key]
	if !ok {
		e = &lockEntry{}
		k.locks[key] = e
	}
	e.refs++
	k.mu.Unlock()

	e.mu.Lock()
	return func() {
		e.mu.Unlock()
		k.mu.Lock()
		e.refs--
		if e.refs == 0 {
			delete(k.locks, key)
		}
		k.mu.Unlock()
	}
}
