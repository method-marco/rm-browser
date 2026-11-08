package main

// "Save as PDF" works like printing: the frontend captures every reader
// page exactly as it is shown (same fonts, size and page breaks) into
// /run/rm-browser-export/pN.png, the backend puts them together into one
// PDF, and the frontend hands that to rm-librarian, which files it in the
// library folder "Web".
//
// The export folder lives in RAM (/run), belongs to root and the sandbox
// can't write it: it reads the page images through a handle opened
// before it dropped root, and writes the PDF through a file descriptor
// opened at the same time. So even a hijacked backend can't put a link
// to another file there for root to import into the library.

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unicode"

	"codeberg.org/go-pdf/fpdf"
)

const (
	exportPDFName  = "Web page.pdf" // librarian names the document after it; renamed afterwards
	maxExportPages = 400
	maxPageImage   = 20 << 20 // bytes of one captured page
	maxPageSide    = 6000     // pixels
	maxExportPDF   = 60 << 20 // RLIMIT_FSIZE is 64 MB
)

// Export is the backend's side of the export folder.
type Export struct {
	dir  string   // absolute path, for the frontend
	root *os.Root // page images (read only for the sandbox)
	pdf  *os.File // the PDF, root-owned, opened before the sandbox
}

func exportDirPath() string {
	if p := os.Getenv("RM_BROWSER_EXPORT"); p != "" {
		return p
	}
	return "/run/rm-browser-export"
}

// openExport prepares the folder and the PDF file. As root, both are
// root's: the folder 0755 (the sandbox may read the page images), the
// PDF 0600 (written through the descriptor only).
func openExport(dir string) (*Export, error) {
	if fi, err := os.Lstat(dir); err == nil && !fi.IsDir() {
		return nil, fmt.Errorf("%s is not a folder", dir)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	if os.Geteuid() == 0 {
		if err := os.Chown(dir, 0, 0); err != nil {
			return nil, err
		}
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, exportPDFName), os.O_RDWR|os.O_CREATE|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		root.Close()
		return nil, err
	}
	if os.Geteuid() == 0 {
		if err := f.Chown(0, 0); err != nil {
			return nil, err
		}
	}
	if err := f.Chmod(0o600); err != nil {
		return nil, err
	}
	_ = f.Truncate(0)
	abs, err := filepath.Abs(dir)
	if err != nil {
		abs = dir
	}
	return &Export{dir: abs, root: root, pdf: f}, nil
}

func (e *Export) pdfPath() string { return filepath.Join(e.dir, exportPDFName) }

// clear empties the PDF (the page images are blanked by the frontend).
func (e *Export) clear() error { return e.pdf.Truncate(0) }

// ExportRequest describes the captured pages.
type ExportRequest struct {
	Pages    int             `json:"pages"`
	Title    string          `json:"title"`
	URL      string          `json:"url"`
	Width    float64         `json:"width"` // screen size: the PDF page
	Height   float64         `json:"height"`
	X        float64         `json:"x"` // where the captured reader area sits on the screen
	Y        float64         `json:"y"`
	Headings []ExportHeading `json:"headings"`
}

type ExportHeading struct {
	Text  string `json:"text"`
	Level int    `json:"level"`
	Page  int    `json:"page"`
}

//go:embed fonts/NotoSans-Regular.ttf
var fontFS embed.FS

// writePDF builds the PDF from the captured pages and writes it to the
// export file.
func (e *Export) writePDF(req ExportRequest) error {
	if req.Pages < 1 || req.Pages > maxExportPages {
		return fmt.Errorf("%d pages", req.Pages)
	}
	if req.Width < 100 || req.Height < 100 || req.Width > maxPageSide || req.Height > maxPageSide {
		return errors.New("bad page size")
	}
	pages := make([][]byte, req.Pages)
	for i := range pages {
		b, err := e.readPage(fmt.Sprintf("p%d.png", i))
		if err != nil {
			return fmt.Errorf("page %d: %w", i+1, err)
		}
		pages[i] = b
	}
	var buf bytes.Buffer
	if err := buildPDF(&buf, req, pages); err != nil {
		return err
	}
	if buf.Len() > maxExportPDF {
		return errors.New("the PDF would be too large")
	}
	if _, err := e.pdf.Seek(0, io.SeekStart); err != nil {
		return err
	}
	if _, err := e.pdf.Write(buf.Bytes()); err != nil {
		return err
	}
	return e.pdf.Truncate(int64(buf.Len()))
}

func (e *Export) readPage(name string) ([]byte, error) {
	f, err := e.root.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() || fi.Size() > maxPageImage {
		return nil, errors.New("not a page image")
	}
	return io.ReadAll(io.LimitReader(f, maxPageImage))
}

// buildPDF puts one captured page on each PDF page, at the screen's size
// and position, so it looks and pages exactly like the reader.
func buildPDF(w io.Writer, req ExportRequest, pages [][]byte) error {
	pdf := fpdf.NewCustom(&fpdf.InitType{UnitStr: "pt", Size: fpdf.SizeType{Wd: req.Width, Ht: req.Height}})
	pdf.SetMargins(0, 0, 0)
	pdf.SetAutoPageBreak(false, 0)
	pdf.SetCompression(true)
	if b, err := fontFS.ReadFile("fonts/NotoSans-Regular.ttf"); err == nil {
		pdf.AddUTF8FontFromBytes("sans", "", b) // for the outline (bookmarks)
		pdf.SetFont("sans", "", 12)
	}
	title := strings.Join(strings.Fields(req.Title), " ") // metadata and outline keep commas and slashes
	if title == "" {
		title = cleanTitle("")
	}
	pdf.SetTitle(title, true)
	pdf.SetSubject(req.URL, true)
	pdf.SetCreator("rm-browser", true)
	pdf.SetCreationDate(time.Now())

	// bookmark levels must start at 0 and go down one at a time
	minLevel := 99
	for _, h := range req.Headings {
		if h.Level > 0 && h.Level < minLevel {
			minLevel = h.Level
		}
	}
	for i, data := range pages {
		img, err := pageImage(data)
		if err != nil {
			return fmt.Errorf("page %d: %w", i+1, err)
		}
		pdf.AddPage()
		name := fmt.Sprintf("p%d", i)
		var enc bytes.Buffer
		if err := (&png.Encoder{CompressionLevel: png.BestSpeed}).Encode(&enc, img); err != nil {
			return err
		}
		pdf.RegisterImageOptionsReader(name, fpdf.ImageOptions{ImageType: "PNG"}, &enc)
		b := img.Bounds()
		pdf.ImageOptions(name, req.X, req.Y, float64(b.Dx()), float64(b.Dy()), false, fpdf.ImageOptions{ImageType: "PNG"}, 0, "")
		if i == 0 {
			pdf.Bookmark(title, 0, 0)
		}
		last := 0
		for _, h := range req.Headings {
			if h.Page != i || strings.TrimSpace(h.Text) == "" {
				continue
			}
			lvl := 1 + h.Level - minLevel
			if lvl > last+1 {
				lvl = last + 1
			}
			if lvl < 1 {
				lvl = 1
			}
			pdf.Bookmark(strings.Join(strings.Fields(h.Text), " "), lvl, 0)
			last = lvl
		}
		if pdf.Err() {
			return pdf.Error()
		}
	}
	return pdf.Output(w)
}

// pageImage decodes a captured page and flattens it onto white; pages
// without colour become grey scale (a third of the size).
func pageImage(data []byte) (image.Image, error) {
	cfg, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	if cfg.Width < 1 || cfg.Height < 1 || cfg.Width > maxPageSide || cfg.Height > maxPageSide {
		return nil, errors.New("bad page image size")
	}
	src, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	b := src.Bounds()
	rgba := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(rgba, rgba.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	draw.Draw(rgba, rgba.Bounds(), src, b.Min, draw.Over)
	grey := true
	for i := 0; i < len(rgba.Pix) && grey; i += 4 {
		p := rgba.Pix[i : i+3 : i+3]
		grey = p[0] == p[1] && p[1] == p[2]
	}
	if !grey {
		return rgba, nil
	}
	g := image.NewGray(rgba.Bounds())
	for i, j := 0, 0; i < len(rgba.Pix); i, j = i+4, j+1 {
		g.Pix[j] = rgba.Pix[i]
	}
	return g, nil
}

// cleanTitle makes a page title usable as a document name: one line, no
// "/" (a path separator for librarian) or "," (its argument separator).
func cleanTitle(s string) string {
	s = strings.Map(func(r rune) rune {
		switch {
		case r == '/' || r == '\\':
			return '-'
		case r == ',':
			return '‚' // U+201A, looks like a comma
		case unicode.IsControl(r) || r == ' ' || r == ' ':
			return ' '
		}
		return r
	}, s)
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > 120 {
		s = strings.TrimSpace(string(r[:120])) + "…"
	}
	if s == "" {
		s = "Web page"
	}
	return s
}
