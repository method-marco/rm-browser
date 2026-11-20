package main

import (
	"archive/zip"
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"html"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/method-marco/rm-browser/backend/internal/store"
)

func testPNG(w, h int) []byte {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.NRGBA{uint8(x), 80, 160, 255})
		}
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

// testEPUB: three chapters (one empty), a nav table of contents, a link
// between chapters, a picture, whitespace the way books have it.
func testEPUB(t *testing.T) []byte {
	t.Helper()
	files := map[string]string{
		"META-INF/container.xml": `<?xml version="1.0"?><container xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`,
		"OEBPS/content.opf": `<?xml version="1.0"?><package xmlns="http://www.idpf.org/2007/opf" version="3.0"><metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>The  Test Book</dc:title><dc:creator>Ada Writer</dc:creator></metadata>
<manifest><item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/><item id="c1" href="text/one.xhtml" media-type="application/xhtml+xml"/><item id="c2" href="text/empty.xhtml" media-type="application/xhtml+xml"/><item id="c3" href="text/two.xhtml" media-type="application/xhtml+xml"/><item id="pic" href="img/pic.png" media-type="image/png"/></manifest>
<spine><itemref idref="c1"/><itemref idref="c2"/><itemref idref="c3"/></spine></package>`,
		"OEBPS/nav.xhtml": `<html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops"><body><nav epub:type="toc"><ol><li><a href="text/one.xhtml">Chapter One</a><ol><li><a href="text/one.xhtml#later">Later on</a></li></ol></li><li><a href="text/two.xhtml">Chapter Two</a></li></ol></nav></body></html>`,
		"OEBPS/text/one.xhtml": `<?xml version="1.0" encoding="utf-8"?>
<html xmlns="http://www.w3.org/1999/xhtml"><head><title>One</title><link rel="stylesheet" type="text/css" href="../css/book.css"/><style>p{}</style></head>
<body>
  <h1>Chapter One</h1>
  <p>It was a <i>dark</i> and stormy night; the rain fell in torrents &amp; more.</p>
  <p class="center" style="text-align: center">* * *</p>
  <p>Read <a href="two.xhtml#end">the ending</a> or   look <b>here</b>.</p>
  <div><img src="../img/pic.png" alt="a picture"/></div>
  <p id="later">Later, &lt;nothing&gt; happened. Ça va? 3 Bücher.</p>
  <script>alert("no")</script>
</body></html>`,
		"OEBPS/css/book.css": `/* the book's style */ @charset "utf-8";
p { text-indent: 1.5em; margin: 0 0 0.5em 0 }
.center { text-align: center; text-indent: 0 }
h1 { font-size: 150%; text-align: center }
i, em { font-style: italic }
div.later p, p#later { font-variant: small-caps; color: red }
b { font-weight: normal; font-size: 0.8em }
@media print { p { text-indent: 9em } }
a:hover { font-weight: bold }`,
		"OEBPS/text/empty.xhtml": `<html xmlns="http://www.w3.org/1999/xhtml"><body>  <div> </div> </body></html>`,
		"OEBPS/text/two.xhtml":   `<html xmlns="http://www.w3.org/1999/xhtml"><body><h2>Chapter Two</h2><p>First line.</p><p>Second line with <a href="https://example.org/x">a web link</a>.</p><p id="end">The end.</p></body></html>`,
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.CreateHeader(&zip.FileHeader{Name: "mimetype", Method: zip.Store})
	_, _ = w.Write([]byte("application/epub+zip"))
	for name, body := range files {
		w, _ := zw.Create(name)
		_, _ = w.Write([]byte(body))
	}
	w, _ = zw.Create("OEBPS/img/pic.png")
	_, _ = w.Write(testPNG(300, 200))
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// visible: what the frontend counts in Qt's text (every character except
// whitespace and invisible ones), from the HTML the writer made.
func visible(h string) []rune {
	txt := html.UnescapeString(regexp.MustCompile(`<[^>]*>`).ReplaceAllString(h, " "))
	var out []rune
	for _, r := range txt {
		if isCounted(r) {
			out = append(out, r)
		}
	}
	return out
}

func openTestBook(t *testing.T) *epubBook {
	data := testEPUB(t)
	b, err := openEPUB(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestEPUBAndChapters(t *testing.T) {
	ep := openTestBook(t)
	if ep.title != "The Test Book" || ep.author != "Ada Writer" || len(ep.frags) != 3 {
		t.Fatalf("book: %q %q %d", ep.title, ep.author, len(ep.frags))
	}
	if len(ep.toc) != 3 || ep.toc[1].anchor != "later" || ep.toc[1].level != 2 || ep.toc[2].frag != 3 {
		t.Fatalf("toc: %+v", ep.toc)
	}
	var asked []string
	c := ep.renderChapter(0, func(p string) (string, int, int, bool) {
		asked = append(asked, p)
		return "/cache/img/x.jpg", 1200, 800, true
	}, 600, renderOptions{})
	for _, want := range []string{
		"<h2>Chapter One</h2>", "<i>dark</i>", "torrents &amp; more", `<p align="center" style="line-height:125%">* * *</p>`,
		`href="book:2#end"`, `<img src="file:///cache/img/x.jpg" width="590" height="393"/>`, "&lt;nothing&gt;",
	} {
		if !strings.Contains(c.html, want) {
			t.Errorf("missing %q in\n%s", want, c.html)
		}
	}
	if strings.Contains(c.html, "alert") || strings.Contains(c.html, "One</title>") || strings.Contains(c.html, "p{}") {
		t.Error("script, title or style written")
	}
	if len(asked) != 1 || asked[0] != "OEBPS/img/pic.png" {
		t.Errorf("pictures: %v", asked)
	}
	if v := visible(c.html); len(v) != len(c.pos) {
		t.Fatalf("visible characters: html %d, positions %d", len(v), len(c.pos))
	}
	// every position points at the character written there
	v := visible(c.html)
	for i, p := range c.pos {
		if r := ep.texts[p.seq].text[p.off]; r != v[i] {
			t.Fatalf("position %d: %q in the DOM, %q written", i, r, v[i])
		}
	}
	if k := c.anchors["later"]; v[k] != 'L' {
		t.Errorf("anchor 'later' at %d: %q", k, v[k])
	}
	if e := ep.renderChapter(1, nil, 0, renderOptions{}); e.html != "" || len(e.pos) != 0 {
		t.Errorf("empty chapter: %q", e.html)
	}
	if two := ep.renderChapter(2, nil, 0, renderOptions{}); !strings.Contains(two.html, `href="https://example.org/x"`) {
		t.Errorf("web link: %s", two.html)
	}
}

func TestXPointers(t *testing.T) {
	dir := t.TempDir()
	lib := writeLibrary(t, dir)
	b, err := loadBook(lib, "book-1")
	if err != nil {
		t.Fatal(err)
	}
	defer b.close()
	if b.title != "My Book" || b.counts[1] != 0 || b.counts[0] == 0 || !b.empty(1) || b.nextShown(1, 1) != 2 || b.nextShown(1, -1) != 0 {
		t.Fatalf("book %q counts %v", b.title, b.counts)
	}
	// crengine's paths: element names, [n] only among same-named siblings
	for xp, want := range map[string][2]int{
		"/body/DocFragment[1]/body/h1/text().0":      {0, 0},
		"/body/DocFragment[1]/body/p[1]/text()[1].0": {0, 10},
		"/body/DocFragment[1]/body/p[1]/i/text().0":  {0, 16},
		"/body/DocFragment[3]/body/p[3]/text().4":    {2, 46},
		"/body/DocFragment[2]/body":                  {1, 0},
	} {
		ch, k, ok := b.fromXPointer(xp)
		if !ok || ch != want[0] || k != want[1] {
			t.Errorf("%s -> chapter %d char %d (%v), want %v", xp, ch, k, ok, want)
		}
	}
	if xp := b.xpointer(0, 16); xp != "/body/DocFragment[1]/body/p[1]/i/text().0" {
		t.Errorf("xpointer(0, 16) = %s", xp)
	}
	if xp := b.xpointer(0, 10); xp != "/body/DocFragment[1]/body/p[1]/text()[1].0" {
		t.Errorf("xpointer(0, 10) = %s", xp)
	}
	// every position survives the round trip
	for ch := 0; ch < b.chapters(); ch++ {
		for k := 0; k < b.counts[ch]; k++ {
			xp := b.xpointer(ch, k)
			c2, k2, ok := b.fromXPointer(xp)
			if !ok || c2 != ch || k2 != k {
				t.Fatalf("(%d, %d) -> %s -> (%d, %d)", ch, k, xp, c2, k2)
			}
		}
	}
	if p := b.percent(2, 0); p <= 0.5 || p >= 1 {
		t.Errorf("percent at chapter 3: %v", p)
	}
	if ch, k := b.fromPercent(b.percent(2, 10)); ch != 2 || k != 10 {
		t.Errorf("fromPercent: %d %d", ch, k)
	}
	if len(b.toc) != 3 || b.toc[1].Chapter != 0 || b.toc[1].Char == 0 || b.toc[2].Chapter != 2 {
		t.Errorf("toc: %+v", b.toc)
	}
}

// writeLibrary: a xochitl folder with a Library folder (and a subfolder),
// books elsewhere, deleted and in the trash.
func writeLibrary(t *testing.T, dir string) *library {
	t.Helper()
	epub := testEPUB(t)
	meta := func(id, name, typ, parent string, deleted bool) {
		b, _ := json.Marshal(map[string]any{"visibleName": name, "type": typ, "parent": parent, "deleted": deleted})
		if err := os.WriteFile(filepath.Join(dir, id+".metadata"), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	meta("lib", "Library", "CollectionType", "", false)
	meta("sub", "Novels", "CollectionType", "lib", false)
	meta("other", "Work", "CollectionType", "", false)
	meta("oldlib", "Library", "CollectionType", "trash", false)
	for id, m := range map[string][2]string{
		"book-1": {"My Book", "lib"}, "book-2": {"Another", "sub"}, "book-3": {"Not here", "other"},
		"book-4": {"Trashed", "trash"}, "book-5": {"In old library", "oldlib"},
	} {
		meta(id, m[0], "DocumentType", m[1], false)
		_ = os.WriteFile(filepath.Join(dir, id+".epub"), epub, 0o644)
	}
	meta("book-6", "Deleted", "DocumentType", "lib", true)
	_ = os.WriteFile(filepath.Join(dir, "book-6.epub"), epub, 0o644)
	meta("pdf-1", "A PDF", "DocumentType", "lib", false)
	_ = os.WriteFile(filepath.Join(dir, "pdf-1.pdf"), []byte("%PDF"), 0o644)
	lib, err := openLibrary(dir)
	if err != nil {
		t.Fatal(err)
	}
	return lib
}

func TestLibraryScan(t *testing.T) {
	lib := writeLibrary(t, t.TempDir())
	books, found, err := lib.scan()
	if err != nil || !found {
		t.Fatal(err, found)
	}
	var got []string
	for _, b := range books {
		got = append(got, b.ID+":"+b.Title+":"+b.Folder+":"+b.Author)
	}
	want := "book-1:My Book::Ada Writer book-2:Another:Novels:Ada Writer"
	if strings.Join(got, " ") != want {
		t.Errorf("library:\n got %s\nwant %s", strings.Join(got, " "), want)
	}
	for _, id := range []string{"../x", "a/b", "", "x.epub"} {
		if validID(id) {
			t.Errorf("%q accepted as id", id)
		}
	}
}

func TestPartialMD5(t *testing.T) {
	small := []byte("short file")
	s := md5.Sum(small)
	if got := partialMD5(bytes.NewReader(small)); got != hex.EncodeToString(s[:]) {
		t.Errorf("small file: %s", got)
	}
	// samples at 0, 1024, 4096, 16384, ...: a change at 3000 doesn't count, at 4096 it does
	big := make([]byte, 100000)
	d1 := partialMD5(bytes.NewReader(big))
	big[3000] = 1
	if partialMD5(bytes.NewReader(big)) != d1 {
		t.Error("byte outside the samples changed the digest")
	}
	big[4096] = 1
	if partialMD5(bytes.NewReader(big)) == d1 {
		t.Error("sampled byte didn't change the digest")
	}
}

// fakeSync: a kosync server in memory.
type fakeSync struct {
	mu       sync.Mutex
	users    map[string]string
	progress map[string]progress
	puts     int
}

func (f *fakeSync) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	auth := f.users[r.Header.Get("x-auth-user")] != "" && f.users[r.Header.Get("x-auth-user")] == r.Header.Get("x-auth-key")
	switch {
	case r.Method == "POST" && r.URL.Path == "/users/create":
		var b map[string]string
		_ = json.NewDecoder(r.Body).Decode(&b)
		if f.users[b["username"]] != "" {
			w.WriteHeader(402)
			return
		}
		f.users[b["username"]] = b["password"]
		w.WriteHeader(201)
	case !auth:
		w.WriteHeader(401)
	case r.URL.Path == "/users/auth":
		_, _ = w.Write([]byte(`{"authorized":"OK"}`))
	case r.Method == "PUT" && r.URL.Path == "/syncs/progress":
		var p progress
		_ = json.NewDecoder(r.Body).Decode(&p)
		p.Timestamp = time.Now().Unix()
		f.progress[p.Document] = p
		f.puts++
		_ = json.NewEncoder(w).Encode(map[string]any{"document": p.Document, "timestamp": p.Timestamp})
	case r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/syncs/progress/"):
		p, ok := f.progress[strings.TrimPrefix(r.URL.Path, "/syncs/progress/")]
		if !ok {
			_, _ = w.Write([]byte(`{}`))
			return
		}
		_ = json.NewEncoder(w).Encode(p)
	default:
		w.WriteHeader(404)
	}
}

type recorder struct {
	mu   sync.Mutex
	msgs []struct {
		typ int
		v   map[string]any
	}
}

func (r *recorder) hook(typ int, v any) {
	b, _ := json.Marshal(v)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	r.mu.Lock()
	r.msgs = append(r.msgs, struct {
		typ int
		v   map[string]any
	}{typ, m})
	r.mu.Unlock()
}

func (r *recorder) last(typ int) map[string]any {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := len(r.msgs) - 1; i >= 0; i-- {
		if r.msgs[i].typ == typ {
			return r.msgs[i].v
		}
	}
	return nil
}

func (r *recorder) wait(t *testing.T, typ int, ok func(map[string]any) bool) map[string]any {
	t.Helper()
	for i := 0; i < 200; i++ {
		if m := r.last(typ); m != nil && (ok == nil || ok(m)) {
			return m
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("no message %d", typ)
	return nil
}

func TestAppFlowAndSync(t *testing.T) {
	dir := t.TempDir()
	xochitl := filepath.Join(dir, "xochitl")
	_ = os.MkdirAll(xochitl, 0o755)
	lib := writeLibrary(t, xochitl)
	st, err := store.Open(filepath.Join(dir, "cfg"), filepath.Join(dir, "cache"))
	if err != nil {
		t.Fatal(err)
	}
	fs := &fakeSync{users: map[string]string{}, progress: map[string]progress{}}
	srv := httptest.NewServer(fs)
	defer srv.Close()
	fs.users["ada"] = passwordKey("secret")

	rec := &recorder{}
	a := newApp(nil, st, lib)
	a.sendHook = rec.hook
	// the kosync-bridge login is taken over once
	bridgeFile := filepath.Join(dir, "bridge.json")
	_ = os.WriteFile(bridgeFile, []byte(`{"server":"`+srv.URL+`","username":"ada","userkey":"`+passwordKey("secret")+`"}`), 0o600)
	a.importLogin(readBridgeLogin(bridgeFile))
	if !a.cfg.Sync.loggedIn() || a.cfg.Sync.Server != srv.URL {
		t.Fatalf("login not imported: %+v", a.cfg.Sync)
	}

	a.handle(reqInit, `{"width":800,"height":1200}`)
	libMsg := rec.wait(t, resLibrary, nil)
	if books := libMsg["books"].([]any); len(books) != 2 {
		t.Fatalf("library: %v", libMsg)
	}
	if s := rec.last(resState); s["defaults"].(map[string]any)["font"] != "literata" {
		t.Errorf("state: %v", s)
	}

	a.handle(reqOpen, `{"id":"book-1"}`)
	bk := rec.wait(t, resBook, nil)
	if bk["title"] != "My Book" || bk["chapters"].(float64) != 3 {
		t.Fatalf("book: %v", bk)
	}
	ch := rec.wait(t, resChapter, nil)
	if ch["chapter"].(float64) != 0 || !strings.Contains(ch["html"].(string), "stormy") || !strings.Contains(ch["html"].(string), "<img") {
		t.Fatalf("chapter: %v", ch)
	}
	// the picture was converted into the cache
	if m := regexp.MustCompile(`file://([^"]+)`).FindStringSubmatch(ch["html"].(string)); m == nil {
		t.Fatal("no picture")
	} else if _, err := os.Stat(m[1]); err != nil {
		t.Fatalf("picture file: %v", err)
	}

	// next chapter after the end of the first skips the empty one
	a.handle(reqChapter, `{"chapter":1,"char":0,"dir":1}`)
	if c := rec.last(resChapter); c["chapter"].(float64) != 2 {
		t.Fatalf("empty chapter not skipped: %v", c["chapter"])
	}
	a.handle(reqChapter, `{"chapter":1,"char":0,"dir":-1}`)
	if c := rec.last(resChapter); c["chapter"].(float64) != 0 || c["end"] != true {
		t.Fatalf("back over the empty chapter: %v", c)
	}
	// links inside the book
	a.handle(reqLink, `{"href":"book:2#end"}`)
	if c := rec.last(resChapter); c["chapter"].(float64) != 2 || c["char"].(float64) == 0 {
		t.Fatalf("link: %v", c)
	}

	// reading position -> pushed as a KOReader position
	a.handle(reqPosition, `{"chapter":2,"char":46}`)
	a.pushNow(false)
	fs.mu.Lock()
	var pushed progress
	for _, p := range fs.progress {
		pushed = p
	}
	fs.mu.Unlock()
	if pushed.Progress != "/body/DocFragment[3]/body/p[3]/text().4" || pushed.Device != syncDeviceName || pushed.Percentage < 0.5 {
		t.Fatalf("pushed: %+v", pushed)
	}
	a.pushNow(false) // unchanged: not sent again
	if fs.puts != 1 {
		t.Errorf("%d pushes", fs.puts)
	}

	// another device read on: opening the book jumps there
	fs.mu.Lock()
	fs.progress[pushed.Document] = progress{Document: pushed.Document, Progress: "/body/DocFragment[1]/body/p[3]/text().0",
		Percentage: 0.2, Device: "Kobo", DeviceID: "OTHER", Timestamp: time.Now().Unix() + 5}
	fs.mu.Unlock()
	a.pull(false, false)
	s := rec.wait(t, resSync, nil)
	if s["chapter"].(float64) != 0 || s["device"] != "Kobo" {
		t.Fatalf("sync: %v", s)
	}
	// percentage-only positions (other apps)
	fs.mu.Lock()
	fs.progress[pushed.Document] = progress{Document: pushed.Document, Progress: "epubcfi(/6/4!/4/2)", Percentage: 0.3, DeviceID: "OTHER2", Timestamp: time.Now().Unix() + 10}
	fs.mu.Unlock()
	a.pull(false, false)
	s = rec.wait(t, resSync, func(m map[string]any) bool { return m["device"] == "another device" })
	if s["chapter"].(float64) != 0 || s["char"].(float64) < 40 {
		t.Fatalf("percentage sync: %v", s)
	}

	// forced pull: the server's position even if it is this tablet's own
	a.handle(reqPosition, `{"chapter":0,"char":5}`)
	a.pushNow(true)
	a.pull(true, true)
	s = rec.wait(t, resSync, func(m map[string]any) bool { return m["device"] == "this tablet" })
	if s["chapter"].(float64) != 0 || s["char"].(float64) != 5 || s["manual"] != true {
		t.Fatalf("forced pull: %v", s)
	}
	if !strings.Contains(rec.last(resToast)["message"].(string), "Position sent") {
		t.Errorf("push: %v", rec.last(resToast))
	}
	a.handle(reqPosition, `{"chapter":2,"char":46}`) // back where the reader was

	// settings: typography and images
	a.handle(reqSettings, `{"fontSize":40,"paragraphs":"spaced","greyImages":true}`)
	if a.cfg.Settings.FontSize != 40 || a.cfg.Settings.Paragraphs != "spaced" || !a.cfg.Settings.GreyImages {
		t.Errorf("settings: %+v", a.cfg.Settings)
	}
	// back to the library: the book is listed with its progress
	a.handle(reqLibrary, "")
	lm := rec.wait(t, resLibrary, func(m map[string]any) bool {
		b := m["books"].([]any)[0].(map[string]any)
		return b["id"] == "book-1" && b["percent"].(float64) > 0.5
	})
	_ = lm
	// logging in with a wrong password fails, with the right one works
	a.login(srv.URL, "ada", "wrong", false, false)
	if !strings.Contains(rec.last(resToast)["message"].(string), "didn't accept") {
		t.Errorf("wrong password: %v", rec.last(resToast))
	}
	a.login(srv.URL, "bob", "pw", false, true)
	if a.cfg.Sync.Username != "bob" || fs.users["bob"] != passwordKey("pw") {
		t.Errorf("register: %+v", a.cfg.Sync)
	}
	// restart: the config has the book open, the position and the login
	b2 := newApp(nil, st, lib)
	if b2.cfg.Open != "" || b2.cfg.Books["book-1"].Chapter != 2 || b2.cfg.Sync.Username != "bob" {
		t.Errorf("saved config: %+v", b2.cfg)
	}
	// and the bridge login is not imported again over a logout
	b2.cfg.Sync = SyncLogin{}
	b2.importLogin(readBridgeLogin(bridgeFile))
	if b2.cfg.Sync.loggedIn() {
		t.Error("bridge login imported a second time")
	}
}

func TestBookStyle(t *testing.T) {
	ep := openTestBook(t)
	plain := ep.renderChapter(0, nil, 0, renderOptions{})
	styled := ep.renderChapter(0, nil, 0, renderOptions{bookStyle: true})
	for _, want := range []string{
		`<h2 align="center" style="line-height:125%; font-size:1.5em">Chapter One</h2>`,
		`<p style="line-height:125%; text-indent:1.5em; margin-top:0em; margin-bottom:0.5em">It was a `,
		`<p align="center" style="line-height:125%; text-indent:0em; margin-top:0em; margin-bottom:0.5em">* * *</p>`,
		`<span style="font-weight:normal; font-size:0.8em"><b>here</b></span>`,
		`text-indent:1.5em; margin-top:0em; margin-bottom:0.5em; font-variant:small-caps">Later,`,
	} {
		if !strings.Contains(styled.html, want) {
			t.Errorf("book style: missing %q in\n%s", want, styled.html)
		}
	}
	if strings.Contains(styled.html, "9em") || strings.Contains(styled.html, "red") {
		t.Error("@media or colours used")
	}
	if strings.Contains(plain.html, "text-indent") || strings.Contains(plain.html, "font-size") {
		t.Errorf("plain style has the book's CSS: %s", plain.html)
	}
	// the text, and so every position, is the same in both styles
	if len(plain.pos) != len(styled.pos) {
		t.Fatalf("positions: %d plain, %d styled", len(plain.pos), len(styled.pos))
	}
	for i := range plain.pos {
		if plain.pos[i] != styled.pos[i] {
			t.Fatalf("position %d differs", i)
		}
	}
	if v := visible(styled.html); len(v) != len(styled.pos) {
		t.Fatalf("styled: %d visible, %d positions", len(v), len(styled.pos))
	}
}

func TestParseCSS(t *testing.T) {
	sh := parseCSS(`@import url(x.css); @font-face { font-family: X; src: url(x.ttf) }
		div  >  p.a.b , #n { margin: 1em 2em } p:first-child { color: red } * { font-style: normal }`, 0)
	if len(sh) != 3 || len(sh[0].sel.parts) != 2 || sh[0].sel.combs[0] != '>' || sh[0].sel.parts[1].tag != "p" ||
		len(sh[0].sel.parts[1].classes) != 2 || sh[0].sel.spec != 22 || sh[1].sel.parts[0].id != "n" || sh[2].sel.parts[0].tag != "" {
		t.Fatalf("rules: %+v", sh)
	}
	// descendant and child selectors look at the ancestors
	doc, _ := parseXHTML([]byte(`<html><body><div class="poem"><blockquote><p id="a">x</p></blockquote></div><p id="b">y</p></body></html>`))
	a, b := findID(doc, "a"), findID(doc, "b")
	for sel, want := range map[string][2]bool{
		"div.poem p": {true, false}, "div > p": {false, false}, "blockquote > p": {true, false}, "body > p": {false, true},
		"p": {true, true}, ".poem blockquote p#a": {true, false}, "div p + p": {false, false},
	} {
		cs, ok := parseSelector(sel)
		got := [2]bool{ok && cs.matches(a), ok && cs.matches(b)}
		if got != want {
			t.Errorf("%q: %v, want %v", sel, got, want)
		}
	}
	for v, want := range map[string]float64{"1.5em": 1.5, "24px": 1.5, "12pt": 1, "0": 0, "2rem": 2} {
		if got, ok := emLength(v); !ok || got != want {
			t.Errorf("%s -> %v %v", v, got, ok)
		}
	}
	for _, v := range []string{"auto", "10%", "1e999em", "x"} {
		if _, ok := emLength(v); ok {
			t.Errorf("%s accepted", v)
		}
	}
}
