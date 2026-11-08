package main

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func testPage() *Page {
	return &Page{URL: "https://example.com/a", Title: "Bikes, tyres/wheels – Über", Site: "example.com", Byline: "A Writer",
		Secure: true, Reader: true,
		HTML: `<p style="line-height:125%">Hello <b>bold</b> <a href="https://example.org/x" style="color:#000000; text-decoration:underline">link</a> and <a href="#sec">jump</a>.</p>` +
			`<h2>Section</h2><ul><li style="line-height:125%">one</li><li style="line-height:125%">two</li></ul>` +
			`<hr/><p align="center"><img src="file:///cache/img/a.jpg" width="200" height="100"/></p>` +
			`<h3>Deeper</h3><p style="line-height:125%">End € 𝒜.</p>`,
		Headings: []Heading{{Level: 2, Text: "Section", ID: "sec"}, {Level: 3, Text: "Deeper"}}}
}

// visible characters per block of testPage, as Qt counts them
func TestPageStreamCounts(t *testing.T) {
	items := pageStream(testPage())
	n := 0
	var markers, headings []string
	for _, it := range items {
		if !it.space() && !it.rule {
			n++
		}
		if it.marker != "" {
			markers = append(markers, it.marker)
		}
		if it.heading > 0 {
			headings = append(headings, it.headText)
		}
	}
	want := len([]rune(strings.Join(strings.Fields("Bikes, tyres/wheels – Über"+"example.com · A Writer"+
		"Hello bold link and jump."+"Section"+"one"+"two"+"X"+"Deeper"+"End € 𝒜."), "")))
	if n != want {
		t.Errorf("%d visible characters, want %d", n, want)
	}
	if strings.Join(markers, ",") != "•,•" {
		t.Errorf("markers %v", markers)
	}
	if strings.Join(headings, "|") != "Bikes, tyres/wheels – Über|Section|Deeper" {
		t.Errorf("headings %v", headings)
	}
}

func TestBuildPDF(t *testing.T) {
	jpg, _, _, err := convertImage(testPNG(t, 300, 150), 300, 150, false)
	if err != nil {
		t.Fatal(err)
	}
	read := func(p string) ([]byte, error) {
		if p != "/cache/img/a.jpg" {
			t.Errorf("asked for %s", p)
		}
		return jpg, nil
	}
	// Qt's lines: visible characters, y, height, x
	lines := []float64{
		23, 0, 88, 0, // title
		19, 100, 36, 0, // meta (example.com·AWriter)
		21, 148, 44, 0, // Hello bold link and jump.
		7, 220, 66, 0, // Section
		3, 300, 44, 40, // one
		3, 355, 44, 40, // two
		0, 420, 44, 0, // rule
		1, 476, 100, 321, // picture
		6, 600, 52, 0, // Deeper
		6, 664, 44, 0, // End € 𝒜.
	}
	req := ExportRequest{URL: "https://example.com/a", FontSize: 32, Width: 954, Height: 1696, X: 56, Y: 48,
		Lines: lines, PageStarts: []float64{0, 476}, PageHeights: []float64{476, 300}}
	var buf bytes.Buffer
	if err := buildPDF(&buf, testPage(), req, read); err != nil {
		t.Fatal(err)
	}
	b := buf.Bytes()
	if n := len(regexp.MustCompile(`/Type /Page\b`).FindAll(b, -1)); n != 2 {
		t.Errorf("%d pages, want the reader's 2", n)
	}
	if !bytes.Contains(b, []byte("/MediaBox [0 0 954.00 1696.00]")) {
		t.Error("page size not the screen's")
	}
	if !bytes.Contains(b, []byte("/URI (https://example.org/x)")) {
		t.Error("web link missing")
	}
	if !bytes.Contains(b, []byte("/Subtype /Image")) {
		t.Error("picture missing")
	}
	if n := bytes.Count(b, []byte("/Title ")); n < 4 { // document + 3 bookmarks
		t.Errorf("%d titles/bookmarks", n)
	}
	for _, f := range []string{"/BaseFont /utf8notosans", "/BaseFont /utf8notosansB"} {
		if !bytes.Contains(b, []byte(f)) {
			t.Errorf("font %s not embedded", f)
		}
	}
	// layout that doesn't fit the text is refused, not drawn wrongly
	req.Lines = lines[:8]
	if err := buildPDF(&buf, testPage(), req, read); err == nil || !strings.Contains(err.Error(), "don't match") {
		t.Errorf("short layout: %v", err)
	}
	req.Lines, req.FontSize = lines, 5000
	if err := buildPDF(&buf, testPage(), req, read); err == nil {
		t.Error("absurd font size accepted")
	}
	// serif
	req.FontSize, req.Serif = 32, true
	buf.Reset()
	if err := buildPDF(&buf, testPage(), req, read); err != nil || !bytes.Contains(buf.Bytes(), []byte("/BaseFont /utf8ebgaramond")) {
		t.Errorf("serif: %v", err)
	}
}

func TestExportFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "export")
	e, err := openExport(dir)
	if err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(e.pdfPath()); fi.Mode().Perm() != 0o600 {
		t.Errorf("pdf mode %v", fi.Mode())
	}
	if err := e.write([]byte("%PDF-long-content")); err != nil {
		t.Fatal(err)
	}
	if err := e.write([]byte("%PDF-x")); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(e.pdfPath()); string(b) != "%PDF-x" {
		t.Errorf("rewrite left %q", b)
	}
	if err := e.clear(); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(e.pdfPath()); fi.Size() != 0 {
		t.Error("not emptied")
	}
	// a link in place of the folder is refused
	l := filepath.Join(t.TempDir(), "link")
	_ = os.Symlink(t.TempDir(), l)
	if _, err := openExport(filepath.Join(l)); err == nil {
		t.Error("followed a link for the export folder")
	}
}

func TestCleanTitle(t *testing.T) {
	for in, want := range map[string]string{
		"Apple, Google/Meta\n news": "Apple‚ Google-Meta news",
		"  ":                        "Web page",
		"a\\b":                      "a-b",
	} {
		if got := cleanTitle(in); got != want {
			t.Errorf("cleanTitle(%q) = %q, want %q", in, got, want)
		}
	}
	if long := cleanTitle(strings.Repeat("x", 300)); len([]rune(long)) != 121 {
		t.Errorf("long title: %d runes", len([]rune(long)))
	}
}

func testPNG(t *testing.T, w, h int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := range img.Pix {
		img.Pix[i] = uint8(i)
	}
	img.Set(0, 0, color.RGBA{255, 0, 0, 255})
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}
