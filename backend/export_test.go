package main

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

func writePNG(t *testing.T, path string, w, h int, c color.Color) {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if (x/10+y/10)%2 == 0 {
				img.Set(x, y, c)
			} // the rest stays transparent: must turn white
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestExportPDF(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "export")
	e, err := openExport(dir)
	if err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(filepath.Join(dir, exportPDFName)); fi.Mode().Perm() != 0o600 {
		t.Errorf("pdf mode %v", fi.Mode())
	}
	writePNG(t, filepath.Join(dir, "p0.png"), 300, 400, color.Black)
	writePNG(t, filepath.Join(dir, "p1.png"), 300, 400, color.RGBA{200, 0, 0, 255})
	writePNG(t, filepath.Join(dir, "p2.png"), 300, 400, color.Black)
	req := ExportRequest{Pages: 3, Title: "Bikes, tyres/wheels – Über", URL: "https://road.cc/x", Width: 412, Height: 540, X: 56, Y: 48,
		Headings: []ExportHeading{{"First", 2, 0}, {"Deep", 4, 1}, {"Second", 2, 2}}}
	if err := e.writePDF(req); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(e.pdfPath())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(b, []byte("%PDF-")) {
		t.Fatalf("not a PDF: %q", b[:20])
	}
	if n := len(regexp.MustCompile(`/Type /Page\b`).FindAll(b, -1)); n != 3 {
		t.Errorf("%d pages", n)
	}
	if n := bytes.Count(b, []byte("/Title ")); n < 5 { // document title + 4 bookmarks
		t.Errorf("%d titles/bookmarks", n)
	}
	if !bytes.Contains(b, []byte("/MediaBox [0 0 412.00 540.00]")) {
		t.Errorf("page size not the screen's")
	}
	// a second, smaller export must not leave the end of the first behind
	big := len(b)
	req.Pages = 1
	if err := e.writePDF(req); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(e.pdfPath()); len(b) >= big || !bytes.HasSuffix(bytes.TrimSpace(b), []byte("%%EOF")) {
		t.Errorf("rewritten PDF is %d bytes (first %d)", len(b), big)
	}
	if err := e.clear(); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(e.pdfPath()); fi.Size() != 0 {
		t.Error("not emptied")
	}
	// missing or bogus pages fail cleanly
	if err := e.writePDF(ExportRequest{Pages: 5, Width: 412, Height: 540}); err == nil {
		t.Error("missing page accepted")
	}
	_ = os.WriteFile(filepath.Join(dir, "p0.png"), []byte("not a png"), 0o644)
	if err := e.writePDF(ExportRequest{Pages: 1, Width: 412, Height: 540}); err == nil {
		t.Error("bogus page accepted")
	}
	if err := e.writePDF(ExportRequest{Pages: 1, Width: 1e6, Height: 540}); err == nil {
		t.Error("huge page accepted")
	}
	// a link planted in the folder is not followed for page images
	_ = os.Remove(filepath.Join(dir, "p0.png"))
	secret := filepath.Join(t.TempDir(), "secret.png")
	writePNG(t, secret, 10, 10, color.Black)
	_ = os.Symlink(secret, filepath.Join(dir, "p0.png"))
	if err := e.writePDF(ExportRequest{Pages: 1, Width: 412, Height: 540}); err == nil {
		t.Error("followed a symlink out of the export folder")
	}
}

func TestPageImageGrey(t *testing.T) {
	for _, c := range []struct {
		col  color.Color
		grey bool
	}{{color.Black, true}, {color.RGBA{200, 0, 0, 255}, false}} {
		var b bytes.Buffer
		img := image.NewNRGBA(image.Rect(0, 0, 20, 20))
		img.Set(3, 3, c.col)
		_ = png.Encode(&b, img)
		out, err := pageImage(b.Bytes())
		if err != nil {
			t.Fatal(err)
		}
		_, isGrey := out.(*image.Gray)
		if isGrey != c.grey {
			t.Errorf("%v: grey=%v", c.col, isGrey)
		}
		r, g, bl, _ := out.At(10, 10).RGBA()
		if r != 0xffff || g != 0xffff || bl != 0xffff {
			t.Errorf("transparent not white: %v", fmt.Sprint(out.At(10, 10)))
		}
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
	long := cleanTitle(string(bytes.Repeat([]byte("x"), 300)))
	if len([]rune(long)) != 121 {
		t.Errorf("long title: %d runes", len([]rune(long)))
	}
}
