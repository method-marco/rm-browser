package main

import (
	"context"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// RM_BROWSER_LIVE=1 go test -run Live -v ./...   checks against the real web.
func TestLive(t *testing.T) {
	if os.Getenv("RM_BROWSER_LIVE") == "" {
		t.Skip("set RM_BROWSER_LIVE=1")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	c := newHTTPClient()

	sugg, err := ddgSuggest(ctx, c, "remarkable")
	if err != nil || len(sugg) == 0 {
		t.Errorf("suggest: %v %v", sugg, err)
	}
	t.Logf("suggestions: %v", sugg)

	if sp, err := ddgSearch(ctx, c, "e-ink display", nil, "moderate"); err != nil {
		t.Logf("search skipped: %v", err) // e.g. DuckDuckGo's bot check after many test runs
	} else if len(sp.Results) < 5 {
		t.Errorf("search: only %d results", len(sp.Results))
	} else {
		t.Logf("first result: %+v, more: %v", sp.Results[0], sp.Next != nil)
	}

	for _, u := range []string{"https://en.wikipedia.org/wiki/E_Ink", "https://go.dev/blog/go1.22", "https://news.ycombinator.com/"} {
		req, _ := newRequest(ctx, http.MethodGet, u, nil)
		f, err := fetch(ctx, c, req, maxPageBytes)
		if err != nil {
			t.Errorf("%s: %v", u, err)
			continue
		}
		n := 0
		pg, err := buildPage(ctx, f, func(ctx context.Context, src string) (string, int, int, bool) {
			n++
			return "/x.jpg", 100, 100, true
		}, 842)
		if err != nil {
			t.Errorf("%s: %v", u, err)
			continue
		}
		t.Logf("%s: reader=%v title=%q headings=%d links=%d images=%d html=%d bytes", u, pg.Reader, pg.Title, len(pg.Headings), len(pg.Links), n, len(pg.HTML))
		if len(pg.HTML) < 500 {
			t.Errorf("%s: too little content", u)
		}
		if strings.Contains(pg.HTML, "<script") {
			t.Errorf("%s: script in output", u)
		}
		if os.Getenv("RM_BROWSER_DUMP") != "" {
			_ = os.WriteFile(os.Getenv("RM_BROWSER_DUMP")+"/"+strings.NewReplacer("/", "_", ":", "").Replace(u)+".html", []byte(pg.HTML), 0o644)
		}
	}

	req, _ := newRequest(ctx, http.MethodGet, "https://expired.badssl.com/", nil)
	if _, err := fetch(ctx, c, req, maxPageBytes); err == nil {
		t.Errorf("expired certificate: %v", err)
	} else {
		t.Logf("bad cert: %v", err)
	}
}
