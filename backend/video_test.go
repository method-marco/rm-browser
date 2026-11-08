package main

import (
	"context"
	"net/url"
	"strings"
	"testing"
)

func TestVideosBecomeLinks(t *testing.T) {
	u, _ := url.Parse("https://example.com/news/story")
	src := `<html><head><title>Story</title></head><body><article><h1>Story</h1><p>` + filler + `</p>
<iframe src="https://www.youtube.com/embed/dQw4w9WgXcQ?rel=0" title="Never Gonna Give You Up"></iframe>
<p>` + filler + `</p>
<figure><iframe data-src="https://player.vimeo.com/video/76979871?h=8272103f6e"></iframe></figure>
<lite-youtube videoid="abc123XYZ"></lite-youtube>
<video controls poster="/p.jpg"><source src="/media/clip.mp4" type="video/mp4"></video>
<iframe src="https://ads.example.net/frame.html"></iframe>
<iframe src="https://docs.google.com/forms/d/e/x/viewform"></iframe>
<p>` + filler + `</p></article></body></html>`
	pg, err := buildPage(context.Background(), &Fetched{URL: u, ContentType: "text/html", Body: []byte(src)}, nil, 842)
	if err != nil {
		t.Fatal(err)
	}
	h := pg.HTML
	for _, want := range []string{
		`<i>Video: </i><a href="https://www.youtube.com/watch?v=dQw4w9WgXcQ"`, ">Never Gonna Give You Up</a>",
		`href="https://vimeo.com/76979871"`, ">Vimeo video</a>",
		`href="https://www.youtube.com/watch?v=abc123XYZ"`,
		`href="https://example.com/media/clip.mp4"`, ">clip.mp4</a>",
	} {
		if !strings.Contains(h, want) {
			t.Errorf("missing %q in\n%s", want, h)
		}
	}
	for _, bad := range []string{"ads.example.net", "docs.google.com", "<iframe", "<video"} {
		if strings.Contains(h, bad) {
			t.Errorf("%q in output", bad)
		}
	}
	// the video line sits where the video was: between the paragraphs
	if i, j := strings.Index(h, "dQw4w9WgXcQ"), strings.Index(h, "vimeo.com"); i < 0 || j < 0 || i > j {
		t.Error("videos out of order")
	}
}
