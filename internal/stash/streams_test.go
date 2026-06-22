package stash

import "testing"

func TestPublicURLRewritesOnlyOrigin(t *testing.T) {
	client := NewClient("http://stash:9999", "http://stash.example.test:8085", "key", 0)
	got := client.PublicURL("http://stash:9999/scene/62/stream?apikey=secret&resolution=ORIGINAL")
	want := "http://stash.example.test:8085/scene/62/stream?apikey=secret&resolution=ORIGINAL"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestBestStreamPrefersDirect(t *testing.T) {
	scene := Scene{SceneStreams: []StreamEndpoint{
		{URL: "http://stash/scene/1/stream.mp4", MIMEType: "video/mp4", Label: "MP4"},
		{URL: "http://stash/scene/1/stream", MIMEType: "video/mp4", Label: "Direct stream"},
	}}
	if got := BestStream(scene); got != "http://stash/scene/1/stream" {
		t.Fatalf("got %q", got)
	}
}
