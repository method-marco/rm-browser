package main

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestClassifyInput(t *testing.T) {
	cases := map[string]string{
		"example.com":                 "https://example.com",
		"en.wikipedia.org/wiki/E_ink": "https://en.wikipedia.org/wiki/E_ink",
		"https://example.com/a?b=1":   "https://example.com/a?b=1",
		"http://example.com":          "http://example.com",
		"localhost:8080":              "http://localhost:8080",
		"192.168.1.10/x":              "http://192.168.1.10/x",
		"remarkable paper pro":        "",
		"3.5":                         "",
		"node.js":                     "https://node.js", // a plausible domain; DuckDuckGo would be wrong here too
		"javascript:alert(1)":         "",
		"file:///etc/passwd":          "",
		"data:text/html,hi":           "",
		"what is e.ink?":              "",
		"":                            "",
	}
	for in, want := range cases {
		if got := classifyInput(in); got != want {
			t.Errorf("classifyInput(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseDDG(t *testing.T) {
	b, err := os.ReadFile("testdata/ddg.html")
	if err != nil {
		t.Fatal(err)
	}
	p, err := parseDDG(string(b))
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Results) != 10 {
		t.Fatalf("got %d results, want 10", len(p.Results))
	}
	r := p.Results[0]
	if r.URL != "https://www.eink.com/" || r.Host != "eink.com" || !strings.HasPrefix(r.Title, "E Ink") || !strings.Contains(r.Snippet, "electrophoretic") {
		t.Errorf("first result = %+v", r)
	}
	if p.Next == nil || p.Next["s"] != "10" || p.Next["vqd"] == "" {
		t.Errorf("next params = %v", p.Next)
	}
	if _, ok := p.Next["q"]; ok {
		t.Error("next params must not carry the query")
	}
}

func TestParseDDGCaptcha(t *testing.T) {
	_, err := parseDDG(`<html><body><div class="anomaly-modal__title">Unfortunately, bots use DuckDuckGo too.</div></body></html>`)
	if err == nil || !strings.Contains(err.Error(), "bot") {
		t.Fatalf("err = %v", err)
	}
}

func TestDDGTarget(t *testing.T) {
	cases := map[string]string{
		"//duckduckgo.com/l/?uddg=https%3A%2F%2Fexample.com%2Fa%3Fb%3D1&rut=x": "https://example.com/a?b=1",
		"https://example.org/":                    "https://example.org/",
		"//duckduckgo.com/l/?uddg=javascript%3Ax": "",
		"https://duckduckgo.com/y.js?ad_domain=x": "",
		"ftp://example.org/":                      "",
	}
	for in, want := range cases {
		if got := ddgTarget(in); got != want {
			t.Errorf("ddgTarget(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseSuggestions(t *testing.T) {
	got, err := parseSuggestions([]byte(`[{"phrase":"remarkable"},{"phrase":"remarkable 2"},{"phrase":""}]`))
	if err != nil || len(got) != 2 || got[1] != "remarkable 2" {
		t.Fatalf("got %v, %v", got, err)
	}
}

const articleHTML = `<!doctype html><html><head><title>The Article Title - Site</title>
<script>alert("head")</script></head><body>
<header><nav><a href="/">Home</a> <a href="/news">News</a> <a href="/news">News again</a>
<a href="javascript:evil()">Evil</a> <a href="#top">Top</a></nav></header>
<main><article>
<h1>The Article Title</h1>
<p class="byline">By Jane Writer</p>
<p onclick="steal()">First paragraph with a <a href="/other" onmouseover="x()">relative link</a>,
a <a href="javascript:alert(1)">script link</a> and a <a href="#part-two">jump</a>. ` + filler + `</p>
<script>document.cookie</script>
<img src="/pic.png" alt="A picture" onerror="hack()">
<h2 id="part-two">Part two</h2>
<p>Second paragraph <b>bold</b> <i>italic</i> <code>code</code> &lt;not a tag&gt; 😀 emoji. ` + filler + `</p>
<iframe src="https://ads.example/"></iframe>
<form action="/login"><input name="password"></form>
<h3>Details</h3>
<pre>line 1
    line 2</pre>
<ul><li>one</li><li>two</li></ul>
<table><tr><th>Name</th><th>Value</th></tr><tr><td colspan="2" style="x">cell</td></tr><tr><td>a</td><td>1</td></tr><tr><td>b</td><td>2</td></tr></table>
<p style="color:red">Third paragraph. ` + filler + `</p>
</article></main>
<footer><a href="/imprint">Imprint</a></footer>
</body></html>`

const filler = "This sentence is here to make the article long enough for reader mode, which wants real text. " +
	"Readability scores paragraphs by length and commas, so this is written like normal prose, with commas, clauses and words. "

func TestBuildPageSanitizes(t *testing.T) {
	u, _ := url.Parse("https://example.com/articles/1")
	var asked []string
	imgs := func(ctx context.Context, src string) (string, int, int, bool) {
		asked = append(asked, src)
		return "/cache/img/abc.jpg", 400, 300, true
	}
	pg, err := buildPage(context.Background(), &Fetched{URL: u, ContentType: "text/html", Body: []byte(articleHTML)}, imgs, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !pg.Reader {
		t.Error("expected reader mode")
	}
	if !strings.HasPrefix(pg.Title, "The Article Title") {
		t.Errorf("title = %q", pg.Title)
	}
	h := pg.HTML
	for _, bad := range []string{"<script", "alert", "steal", "onclick", "onmouseover", "onerror", "javascript:",
		"<iframe", "<form", "<input", "password", "style=\"color:red", "style=\"x", "😀", "document.cookie"} {
		if strings.Contains(h, bad) {
			t.Errorf("output contains %q:\n%s", bad, h)
		}
	}
	for _, good := range []string{`href="https://example.com/other"`, `href="#part-two"`, "<h2>Part two</h2>",
		"<b>bold</b>", "<i>italic</i>", "<code>code</code>", "&lt;not a tag&gt;", `<pre style="white-space:pre-wrap">`,
		"<li", `colspan="2"`, `<img src="file:///cache/img/abc.jpg" width="400" height="300"/>`, "line 2"} {
		if !strings.Contains(h, good) {
			t.Errorf("output lacks %q:\n%s", good, h)
		}
	}
	if strings.Count(h, "The Article Title") != 0 {
		t.Errorf("title heading should be dropped from the body (shown above it):\n%s", h)
	}
	if len(asked) != 1 || asked[0] != "https://example.com/pic.png" {
		t.Errorf("images asked = %v", asked)
	}
	if len(pg.Headings) != 2 || pg.Headings[0].Text != "Part two" || pg.Headings[0].ID != "part-two" || pg.Headings[1].Level != 3 {
		t.Errorf("headings = %+v", pg.Headings)
	}
	want := []Link{{"Home", "https://example.com/"}, {"News", "https://example.com/news"}}
	if len(pg.Links) != len(want) || pg.Links[0] != want[0] || pg.Links[1] != want[1] {
		t.Errorf("nav links = %+v", pg.Links)
	}
}

func TestBuildPageFallback(t *testing.T) {
	u, _ := url.Parse("https://example.com/")
	src := `<html><head><title>Portal</title></head><body><nav><a href="/a">Section A</a></nav>
<div><a href="/story1">Story one</a></div><div><a href="/story2">Story two</a></div>
<script>x()</script><form><button>Go</button></form></body></html>`
	pg, err := buildPage(context.Background(), &Fetched{URL: u, ContentType: "text/html", Body: []byte(src)}, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if pg.Reader {
		t.Error("expected the fallback view")
	}
	if pg.Title != "Portal" || !strings.Contains(pg.HTML, "https://example.com/story2") || strings.Contains(pg.HTML, "x()") ||
		strings.Contains(pg.HTML, "Go</") {
		t.Errorf("page = %+v", pg)
	}
	if len(pg.Links) != 1 || pg.Links[0].Text != "Section A" {
		t.Errorf("links = %+v", pg.Links)
	}
}

func TestBuildPageTextAndOther(t *testing.T) {
	u, _ := url.Parse("https://example.com/notes.txt")
	pg, err := buildPage(context.Background(), &Fetched{URL: u, ContentType: "text/plain", Body: []byte("a <b> & c")}, nil, 0)
	if err != nil || !strings.Contains(pg.HTML, "a &lt;b&gt; &amp; c") {
		t.Fatalf("text page = %+v, %v", pg, err)
	}
	if _, err := buildPage(context.Background(), &Fetched{URL: u, ContentType: "application/pdf"}, nil, 0); err == nil {
		t.Fatal("pdf should not be shown")
	}
}

func TestConvertImage(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 2000, 1000))
	for y := 0; y < 1000; y++ {
		for x := 0; x < 2000; x++ {
			src.Set(x, y, color.NRGBA{R: uint8(x), G: 100, B: 200, A: 255})
		}
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, src)
	out, w, h, err := convertImage(buf.Bytes(), 860, 1400)
	if err != nil {
		t.Fatal(err)
	}
	if w != 860 || h != 430 {
		t.Errorf("size %dx%d", w, h)
	}
	img, format, err := image.Decode(bytes.NewReader(out))
	if err != nil || format != "jpeg" {
		t.Fatalf("decode: %v %s", err, format)
	}
	if _, ok := img.(*image.Gray); !ok {
		t.Errorf("want grey JPEG, got %T", img)
	}

	tiny := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	buf.Reset()
	_ = png.Encode(&buf, tiny)
	if _, _, _, err := convertImage(buf.Bytes(), 860, 1400); err == nil {
		t.Error("tracking pixel should be dropped")
	}
	if _, _, _, err := convertImage([]byte("<svg/>"), 860, 1400); err == nil {
		t.Error("garbage should fail")
	}
}

func TestFitSize(t *testing.T) {
	if w, h := fitSize(200, 100, 860, 1400); w != 300 || h != 150 {
		t.Errorf("small image: %dx%d", w, h)
	}
	if w, h := fitSize(1000, 4000, 860, 1400); w != 350 || h != 1400 {
		t.Errorf("tall image: %dx%d", w, h)
	}
}

func TestStore(t *testing.T) {
	dir := t.TempDir()
	s, err := openStore(filepath.Join(dir, "cfg"), filepath.Join(dir, "cache"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.writeConfig([]byte(`{"a":1}`)); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "cfg", "config.json")); string(b) != `{"a":1}` {
		t.Errorf("config = %s", b)
	}
	_ = s.cache.MkdirAll("img/sub", 0o700)
	_ = s.cache.WriteFile("img/sub/x.jpg", []byte("x"), 0o644)
	_ = s.cache.WriteFile("y", []byte("y"), 0o644)
	if err := s.clearCache(); err != nil {
		t.Fatal(err)
	}
	if left, _ := os.ReadDir(filepath.Join(dir, "cache")); len(left) != 0 {
		t.Errorf("cache not empty: %v", left)
	}
	if _, err := os.Stat(filepath.Join(dir, "cfg", "config.json")); err != nil {
		t.Error("clearing the cache must not touch the config")
	}
	// os.Root keeps writes inside the folder
	if err := s.cache.WriteFile("../escape", []byte("x"), 0o644); err == nil {
		t.Error("write outside the cache folder succeeded")
	}
}

// End to end through the App with a local web server: page load, history,
// back/forward and clearing data.
func TestAppFlow(t *testing.T) {
	allowLocalForTests = true
	defer func() { allowLocalForTests = false }()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "track", Value: "1"})
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(strings.Replace(articleHTML, "The Article Title", "Title of "+r.URL.Path, -1)))
	}))
	defer srv.Close()

	dir := t.TempDir()
	store, err := openStore(filepath.Join(dir, "cfg"), filepath.Join(dir, "cache"))
	if err != nil {
		t.Fatal(err)
	}
	sent := make(chan [2]any, 100)
	a := newApp(nil, store)
	a.sendHook = func(typ int, v any) { sent <- [2]any{typ, v} }
	wait := func(typ int) any {
		for m := range sent {
			if m[0].(int) == resError {
				t.Fatalf("error: %v", m[1])
			}
			if m[0].(int) == typ {
				return m[1]
			}
		}
		return nil
	}
	a.handle(reqOpen, `{"input":"`+srv.URL+`/one"}`)
	p := wait(resPage).(map[string]any)["page"].(*Page)
	if !strings.HasPrefix(p.Title, "Title of /one") {
		t.Fatalf("title %q", p.Title)
	}
	a.handle(reqOpen, `{"input":"`+srv.URL+`/two"}`)
	wait(resPage)
	a.handle(reqPosition, `{"page":3}`)
	a.handle(reqBack, ``)
	if p := wait(resPage).(map[string]any)["page"].(*Page); !strings.HasPrefix(p.Title, "Title of /one") {
		t.Fatalf("back went to %q", p.Title)
	}
	a.handle(reqForward, ``)
	m := wait(resPage).(map[string]any)
	if !strings.HasPrefix(m["page"].(*Page).Title, "Title of /two") || m["position"].(int) != 3 {
		t.Fatalf("forward: %v", m)
	}
	u, _ := url.Parse(srv.URL)
	if len(a.client().Jar.Cookies(u)) == 0 {
		t.Fatal("expected a cookie")
	}
	if len(a.cfg.History) != 2 {
		t.Fatalf("history %v", a.cfg.History)
	}
	_ = store.cache.WriteFile("img-test", []byte("x"), 0o644)

	a.handle(reqClearData, ``)
	wait(resInfo)
	if len(a.cfg.History) != 0 || len(a.cfg.Stack) != 0 || len(a.cache) != 0 {
		t.Errorf("not cleared: %+v", a.cfg)
	}
	if len(a.client().Jar.Cookies(u)) != 0 {
		t.Error("cookies survived")
	}
	if left, _ := os.ReadDir(filepath.Join(dir, "cache")); len(left) != 0 {
		t.Errorf("cache files survived: %v", left)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "cfg", "config.json"))
	if strings.Contains(string(b), "/one") || strings.Contains(string(b), "/two") {
		t.Errorf("config still has history: %s", b)
	}
}

func TestWriterShrinksIndentedImagesAndDropsEmptyBlocks(t *testing.T) {
	u, _ := url.Parse("https://example.com/")
	src := `<html><body><main><p></p><p> &nbsp; </p><h2>A long enough heading to count as text for this page</h2><p>` + filler + `</p>
<blockquote><img src="/a.jpg"><p>Quoted text.</p><blockquote><img src="/b.jpg"></blockquote></blockquote>
<ul><li>item</li><li></li></ul>
<div class="newsletter-signup">Sign up for our newsletter</div><div style="display: none">Hidden modal</div>
<div class="share-tools">Share this</div></main><footer>© footer</footer></body></html>`
	imgs := func(ctx context.Context, s string) (string, int, int, bool) {
		return "/c/" + s[len(s)-5:], 842, 473, true
	}
	pg, err := buildPage(context.Background(), &Fetched{URL: u, ContentType: "text/html", Body: []byte(src)}, imgs, 842)
	if err != nil {
		t.Fatal(err)
	}
	h := pg.HTML
	if !strings.Contains(h, `a.jpg" width="752" height="422"`) {
		t.Errorf("image in quote not shrunk to 842-80-10: %s", h)
	}
	if !strings.Contains(h, `b.jpg" width="672" height="377"`) {
		t.Errorf("image in nested quote not shrunk: %s", h)
	}
	for _, bad := range []string{"<p style=\"line-height:125%\"></p>", "newsletter", "Hidden modal", "Share this", "footer", "<li style=\"line-height:125%\"></li>"} {
		if strings.Contains(h, bad) {
			t.Errorf("output contains %q: %s", bad, h)
		}
	}
}

func TestCardListsAndBlockLinks(t *testing.T) {
	u, _ := url.Parse("https://example.com/")
	src := `<html><body><main><ul>
<li><a href="/s1"><img src="/a.jpg"><h2>First story headline</h2></a><p>` + filler + `</p></li>
<li><a href="/s2"><div><h3>Second story</h3></div></a><p>Teaser two.</p></li>
<li></li></ul>
<ul><li>plain item</li><li>another <a href="/x">link</a></li></ul></main></body></html>`
	imgs := func(ctx context.Context, s string) (string, int, int, bool) { return "/c/img.jpg", 400, 200, true }
	pg, err := buildPage(context.Background(), &Fetched{URL: u, ContentType: "text/html", Body: []byte(src)}, imgs, 842)
	if err != nil {
		t.Fatal(err)
	}
	h := pg.HTML
	if strings.Count(h, "<hr/>") != 1 {
		t.Errorf("want one rule between the two stories: %s", h)
	}
	for _, want := range []string{
		`<p align="center"><a href="https://example.com/s1" style="color:#000000; text-decoration:underline"><img src="file:///c/img.jpg"`,
		`<h2><a href="https://example.com/s1" style="color:#000000; text-decoration:underline">First story headline</a></h2>`,
		`<h3><a href="https://example.com/s2"`,
		`<ul><li style="line-height:125%">plain item</li>`,
	} {
		if !strings.Contains(h, want) {
			t.Errorf("missing %q in %s", want, h)
		}
	}
	// no block element may sit inside a link
	if regexp.MustCompile(`<a [^>]*>[^<]*<(p|h\d|div)`).MatchString(h) {
		t.Errorf("block inside a link: %s", h)
	}
}

func TestNoLocalNetwork(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("secret")) }))
	defer srv.Close()
	c := newHTTPClient()
	ctx := context.Background()
	for _, u := range []string{srv.URL, "http://localhost:80/", "http://[::1]:443/", "http://192.168.1.1/", "http://10.11.99.1/",
		"http://169.254.169.254/latest/meta-data/", "http://[::ffff:127.0.0.1]:80/", "http://0.0.0.0/"} {
		req, _ := newRequest(ctx, http.MethodGet, u, nil)
		if _, err := fetch(ctx, c, req, 1<<20); err == nil {
			t.Errorf("%s: local address was fetched", u)
		} else if !strings.Contains(err.Error(), "local network") && !strings.Contains(err.Error(), "web ports") {
			t.Errorf("%s: unexpected error %v", u, err)
		}
	}
	for ip, local := range map[string]bool{"8.8.8.8": false, "172.15.0.1": false, "172.16.0.1": true, "100.64.1.1": true,
		"2001:4860::8888": false, "fd00::1": true, "fe80::1": true} {
		if isLocalIP(net.ParseIP(ip)) != local {
			t.Errorf("isLocalIP(%s) != %v", ip, local)
		}
	}
}

func TestPruneImages(t *testing.T) {
	dir := t.TempDir()
	s, err := openStore(filepath.Join(dir, "cfg"), filepath.Join(dir, "cache"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.pruneImages(10, 5); err != nil { // no img folder yet
		t.Fatal(err)
	}
	_ = s.cache.MkdirAll("img", 0o700)
	base := time.Now().Add(-time.Hour)
	for i := 0; i < 10; i++ {
		name := fmt.Sprintf("img/%d.jpg", i)
		_ = s.cache.WriteFile(name, make([]byte, 100), 0o644)
		_ = s.cache.Chtimes(name, base.Add(time.Duration(i)*time.Minute), base.Add(time.Duration(i)*time.Minute))
	}
	if err := s.pruneImages(900, 500); err != nil {
		t.Fatal(err)
	}
	left, _ := os.ReadDir(filepath.Join(dir, "cache", "img"))
	if len(left) != 5 || left[0].Name() != "5.jpg" {
		t.Errorf("left: %v", left)
	}
}
