package bot

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"igsave-bot/internal/config"
)

// mediaInput has to send an absolute path (never file contents) to a local Bot
// API server, and stream contents (never a path) to the public one. Getting it
// backwards fails silently: api.telegram.org would read the path string as a
// file_id.
func TestMediaInputLocalSendsPathPublicSendsContents(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "clip.mp4")
	if err := os.WriteFile(path, []byte("video"), 0o644); err != nil {
		t.Fatal(err)
	}

	local := &Bot{cfg: &config.Config{BotAPIURL: "http://127.0.0.1:8081"}}
	input, cleanup, err := local.mediaInput(path)
	if err != nil {
		t.Fatal(err)
	}
	cleanup()
	// The field value is what goes on the wire; marshalling is how gotgbot gets it there.
	// A bare path here gets parsed as a URL by the server and rejected.
	got := marshalValue(t, input)
	want := "file://" + filepath.ToSlash(path)
	if got != want {
		t.Errorf("local server: want file URI %q on the wire, got %q", want, got)
	}

	public := &Bot{cfg: &config.Config{}}
	input, cleanup, err = public.mediaInput(path)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if got := marshalValue(t, input); strings.Contains(got, dir) {
		t.Errorf("public API: leaked a local path on the wire (%q); file must be uploaded instead", got)
	}
}

func marshalValue(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		t.Fatal(err)
	}
	return s
}
