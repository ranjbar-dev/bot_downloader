package cache

import (
	"os"
	"testing"
	"time"

	"igsave-bot/internal/platform"
)

func TestLookupMissThenHitThenExpiry(t *testing.T) {
	dir := t.TempDir()
	c := New(dir, 50*time.Millisecond, 0)
	key := Key("https://example.com/x")

	release := c.Lock(key)
	if _, ok := c.Lookup(key); ok {
		t.Fatal("expected miss before any download")
	}

	cacheDir, err := c.PrepareDir(key)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cacheDir+"/a.mp4", []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := c.MarkDone(key); err != nil {
		t.Fatal(err)
	}
	release()

	release = c.Lock(key)
	files, ok := c.Lookup(key)
	if !ok || len(files) != 1 || files[0].Kind != platform.KindVideo {
		t.Fatalf("expected 1 cached video file, got %v ok=%v", files, ok)
	}
	release()

	time.Sleep(80 * time.Millisecond)

	release = c.Lock(key)
	if _, ok := c.Lookup(key); ok {
		t.Fatal("expected miss after ttl expiry")
	}
	release()
}

func TestEnforceCapEvictsOldestFirst(t *testing.T) {
	dir := t.TempDir()
	c := New(dir, time.Hour, 10) // 10 byte cap

	put := func(key, data string) {
		release := c.Lock(key)
		defer release()
		d, err := c.PrepareDir(key)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(d+"/f", []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := c.MarkDone(key); err != nil {
			t.Fatal(err)
		}
	}

	put("old", "12345") // 5 bytes, written first -> oldest mtime
	time.Sleep(10 * time.Millisecond)
	put("new", "12345") // 5 bytes, total 10 bytes, still within cap

	c.enforceCap()
	if _, ok := c.Lookup("old"); !ok {
		t.Fatal("expected 'old' to survive while under cap")
	}

	time.Sleep(10 * time.Millisecond)
	put("newest", "12345") // pushes total to 15 bytes, over the 10 byte cap

	c.enforceCap()
	if _, ok := c.Lookup("old"); ok {
		t.Fatal("expected 'old' evicted first once over cap")
	}
	if _, ok := c.Lookup("newest"); !ok {
		t.Fatal("expected 'newest' to survive eviction")
	}
}

func TestKeyedLockSerializesSameKey(t *testing.T) {
	dir := t.TempDir()
	c := New(dir, time.Hour, 0)
	key := Key("https://example.com/y")

	release := c.Lock(key)
	unlocked := make(chan struct{})
	go func() {
		second := c.Lock(key)
		close(unlocked)
		second()
	}()

	select {
	case <-unlocked:
		t.Fatal("second Lock acquired while first still held")
	case <-time.After(30 * time.Millisecond):
	}

	release()
	select {
	case <-unlocked:
	case <-time.After(time.Second):
		t.Fatal("second Lock never acquired after release")
	}
}
