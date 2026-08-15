package platform

import "testing"

// Shape trimmed from a real video page: the initials blob carries far more
// keys, and the page continues after the closing brace.
const xhPage = `<html><body><script id='initials-script'>window.initials={` +
	`"videoModel":{"id":28671844},` +
	`"downloadDropdownComponent":{"videoId":28671844,"sources":{` +
	`"mp4":{"144p":"https://video7.xhcdn.com/key=aaa,end=1,limit=3/144p.h264.mp4",` +
	`"720p":"https://video7.xhcdn.com/key=bbb,end=1,limit=3/720p.h264.mp4",` +
	`"480p":"https://video7.xhcdn.com/key=ccc,end=1,limit=3/480p.h264.mp4"},` +
	`"download":{"144p":{"size":30513561.6},"480p":{"size":145437491.2},"720p":{"size":361528033.28}}}}` +
	`};</script><div>rest of page</div></body></html>`

func TestParseXHamsterSources(t *testing.T) {
	sources, err := parseXHamsterSources(xhPage)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	wantHeights := []int{720, 480, 144} // best first
	if len(sources) != len(wantHeights) {
		t.Fatalf("got %d sources, want %d", len(sources), len(wantHeights))
	}
	for i, want := range wantHeights {
		if sources[i].Height != want {
			t.Errorf("source %d height = %d, want %d", i, sources[i].Height, want)
		}
	}
	if sources[0].Size != 361528033 {
		t.Errorf("720p size = %d, want 361528033", sources[0].Size)
	}
	if sources[0].URL == "" {
		t.Error("720p URL is empty")
	}
}

func TestParseXHamsterSourcesErrors(t *testing.T) {
	if _, err := parseXHamsterSources("<html>no initials here</html>"); err == nil {
		t.Error("want error when the initials blob is missing")
	}
	if _, err := parseXHamsterSources(`window.initials={"downloadDropdownComponent":{"sources":{"mp4":{}}}}`); err == nil {
		t.Error("want error when the page carries no mp4 sources")
	}
}
