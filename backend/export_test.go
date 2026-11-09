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
	req := ExportRequest{Name: "Bikes, tyres", URL: "https://example.com/a", FontSize: 32, Width: 954, Height: 1696, X: 56, Y: 48,
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
	if !bytes.Contains(b, []byte("/URI (https://example.com/a)")) {
		t.Error("the title doesn't link to the page")
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
	// every typeface embeds its own font files
	for font, pf := range pdfFonts {
		for _, f := range pf.files {
			if _, err := fontFS.ReadFile("fonts/" + f + ".ttf"); err != nil {
				t.Errorf("font %q: %v", font, err)
			}
		}
		req.FontSize, req.Font = 96, font
		buf.Reset()
		want := "/BaseFont /utf8" + strings.ToLower(pf.family)
		if err := buildPDF(&buf, testPage(), req, read); err != nil || !bytes.Contains(buf.Bytes(), []byte(want)) {
			t.Errorf("font %q: %v (no %s)", font, err, want)
		}
	}
	req.FontSize, req.Font = 32, ""
	if err := buildPDF(&buf, testPage(), req, read); err != nil || !bytes.Contains(buf.Bytes(), []byte("/BaseFont /utf8notosans")) {
		t.Errorf("default font: %v", err)
	}
	req.Font = "comic"
	if err := buildPDF(&buf, testPage(), req, read); err == nil {
		t.Error("unknown font accepted")
	}
}

// Justified: the blanks of every line but a paragraph's last are stretched
// to the column's right edge.
func TestBuildPDFJustified(t *testing.T) {
	p := &Page{URL: "https://example.com/j", Title: "J", Secure: true, Reader: true,
		HTML: `<p style="line-height:125%">Typography with justified lines stretches the blanks.</p><h2>Not justified here</h2>`}
	// Qt's layout: title, empty meta, paragraph on two lines, heading
	lines := []float64{1, 0, 88, 0, 0, 100, 36, 0, 40, 148, 44, 0, 7, 203, 44, 0, 16, 260, 66, 0}
	req := ExportRequest{Name: "J", URL: p.URL, FontSize: 28, Width: 954, Height: 1696, X: 56, Y: 48, AreaWidth: 842,
		Lines: lines, PageStarts: []float64{0}, PageHeights: []float64{1500}}
	draw := func(justify bool) string {
		req.Justify = justify
		var buf bytes.Buffer
		pdfCompress = false
		defer func() { pdfCompress = true }()
		if err := buildPDF(&buf, p, req, func(string) ([]byte, error) { return nil, os.ErrNotExist }); err != nil {
			t.Fatal(err)
		}
		return buf.String()
	}
	// x of the text drawn on the first line of the paragraph ("hh" ends it)
	xs := func(doc string) []string {
		return regexp.MustCompile(`BT ([0-9.]+) [0-9.]+ Td`).FindAllString(doc, -1)
	}
	left, just := xs(draw(false)), xs(draw(true))
	if len(just) <= len(left) {
		t.Errorf("justified line not drawn word by word: %d vs %d runs", len(just), len(left))
	}
}

func TestExportFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "export")
	_ = os.MkdirAll(dir, 0o755)
	_ = os.WriteFile(filepath.Join(dir, "old.pdf"), []byte("x"), 0o644)
	e, err := openExport(dir)
	if err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(dir); fi.Mode()&os.ModeSticky == 0 || fi.Mode().Perm() != 0o777 {
		t.Errorf("folder mode %v", fi.Mode())
	}
	if _, err := os.Stat(filepath.Join(dir, "old.pdf")); err == nil {
		t.Error("leftover not removed")
	}
	if err := e.write("Bikes, tyres – Über (2)", []byte("%PDF-x")); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(e.path("Bikes, tyres – Über (2)")); string(b) != "%PDF-x" {
		t.Errorf("written %q", b)
	}
	// a planted file or link under the name is replaced, never written through
	target := filepath.Join(t.TempDir(), "target")
	_ = os.WriteFile(target, []byte("keep"), 0o644)
	_ = os.Symlink(target, filepath.Join(dir, "Next.pdf"))
	if err := e.write("Next", []byte("%PDF-y")); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(target); string(b) != "keep" {
		t.Error("wrote through a link")
	}
	if _, err := os.Stat(e.path("Bikes, tyres – Über (2)")); err == nil {
		t.Error("previous export not removed")
	}
	e.clear()
	if _, err := os.Lstat(e.path("Next")); err == nil {
		t.Error("export not removed")
	}
	for _, bad := range []string{"", "../x", "a/b", ".hidden", " pad", "a\nb", "a\\b", strings.Repeat("x", 201)} {
		if e.write(bad, []byte("x")) == nil {
			t.Errorf("name %q accepted", bad)
		}
	}
	l := filepath.Join(t.TempDir(), "link")
	_ = os.Symlink(t.TempDir(), l)
	if _, err := openExport(l); err == nil {
		t.Error("followed a link for the export folder")
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

// The reader and the PDF use the same typefaces: every bundled one is in
// the app's resources (the frontend loads them from there) and here.
func TestBundledFonts(t *testing.T) {
	qrc, err := os.ReadFile("../app/application.qrc")
	if err != nil {
		t.Fatal(err)
	}
	for key, pf := range pdfFonts {
		if key == "sans" || key == "garamond" { // on the tablet
			continue
		}
		for _, f := range pf.files {
			if !bytes.Contains(qrc, []byte("<file>fonts/"+f+".ttf</file>")) {
				t.Errorf("%s.ttf is not in app/application.qrc", f)
			}
			a, err1 := os.ReadFile("../app/fonts/" + f + ".ttf")
			b, err2 := fontFS.ReadFile("fonts/" + f + ".ttf")
			if err1 != nil || err2 != nil || !bytes.Equal(a, b) {
				t.Errorf("%s.ttf differs between app/fonts and backend/fonts", f)
			}
		}
	}
}
