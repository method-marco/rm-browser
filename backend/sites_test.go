package main

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"
)

// RM_BROWSER_LIVE=1 go test -run Sites -v ./...
// The user's regular sites: each home page, then the first article linked from it.
func TestSites(t *testing.T) {
	if os.Getenv("RM_BROWSER_LIVE") == "" {
		t.Skip("set RM_BROWSER_LIVE=1")
	}
	c := newHTTPClient()
	hrefRe := regexp.MustCompile(`href="([^"]+)"`)
	for _, home := range []string{"https://www.macrumors.com/", "https://www.engadget.com/", "https://www.cyclingweekly.com/", "https://road.cc/"} {
		pg := livePage(t, c, home)
		if pg == nil {
			continue
		}
		// first link on the same site with a long path: most likely an article
		hu, _ := url.Parse(home)
		article := ""
		for _, m := range hrefRe.FindAllStringSubmatch(pg.HTML, -1) {
			u, err := url.Parse(strings.ReplaceAll(m[1], "&amp;", "&"))
			if err == nil && strings.TrimPrefix(u.Hostname(), "www.") == strings.TrimPrefix(hu.Hostname(), "www.") && strings.Count(strings.Trim(u.Path, "/"), "-") >= 4 {
				article = u.String()
				break
			}
		}
		if article == "" {
			t.Errorf("%s: no article link found", home)
			continue
		}
		livePage(t, c, article)
	}
}

func livePage(t *testing.T, c *http.Client, u string) *Page {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	req, _ := newRequest(ctx, http.MethodGet, u, nil)
	f, err := fetch(ctx, c, req, maxPageBytes)
	if err != nil {
		t.Errorf("%s: %v", u, err)
		return nil
	}
	n := 0
	images := func(ctx context.Context, src string) (string, int, int, bool) {
		n++
		return "/x.jpg", 100, 100, true
	}
	// RM_BROWSER_FIXTURE=dir: real images, and the page as JSON for the QML test
	fixture := os.Getenv("RM_BROWSER_FIXTURE")
	if fixture != "" {
		store, err := openStore(fixture+"/cfg", fixture+"/cache")
		if err != nil {
			t.Fatal(err)
		}
		app := &App{store: store, http: c}
		images = func(ctx context.Context, src string) (string, int, int, bool) {
			n++
			return app.prepareImage(ctx, src, 842, 1257) // Paper Pro Move reader area
		}
	}
	pg, err := buildPage(ctx, f, images, 842)
	if err != nil {
		t.Errorf("%s: %v", u, err)
		return nil
	}
	links := strings.Count(pg.HTML, "<a href")
	t.Logf("%s\n    reader=%v title=%q headings=%d navlinks=%d inlinelinks=%d images=%d text=%d chars",
		u, pg.Reader, pg.Title, len(pg.Headings), len(pg.Links), links, n, len(stripTags(pg.HTML)))
	if d := os.Getenv("RM_BROWSER_DUMP"); d != "" {
		_ = os.WriteFile(d+"/"+strings.NewReplacer("/", "_", ":", "", "?", "_").Replace(u)+".html", []byte(pg.HTML), 0o644)
	}
	if fixture != "" {
		name := strings.NewReplacer("/", "_", ":", "", "?", "_").Replace(u)
		_ = os.WriteFile(fixture+"/"+name+".json", []byte(mustJSON(pg)), 0o644)
	}
	return pg
}

func stripTags(s string) string { return regexp.MustCompile(`<[^>]*>`).ReplaceAllString(s, "") }
