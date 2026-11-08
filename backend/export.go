package main

// "Save as PDF" works like printing: the PDF has the same pages as the
// reader, with the same lines on them, in real (selectable) text.
//
// The frontend doesn't send the text, only how Qt laid it out: for every
// line on screen, how many visible characters it holds, where it starts
// (x, y) and how tall it is, plus where each screen page begins. The
// backend walks the same page HTML it gave the frontend, hands out the
// characters line by line, and draws each line at Qt's position with the
// same fonts (Noto Sans or EB Garamond, embedded) and size. Pictures are
// the JPEGs the reader shows. Headings become the PDF's outline.
//
// The PDF is written under its document name ("Title (2).pdf": librarian
// names the document after the file; renaming afterwards would deadlock
// xochitl) into /run/rm-browser-export, a sticky world-writable folder in
// RAM like /tmp, and deleted right after the import. The frontend (root)
// only imports exactly the name it asked for, and the kernel's
// protected_symlinks/protected_hardlinks (checked at start) stop root
// from following a link the sandbox might plant there, so a hijacked
// backend still can't make root import another file.

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"io"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"codeberg.org/go-pdf/fpdf"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

const (
	maxExportPages = 1000
	maxExportLines = 50000
	maxPageSide    = 6000     // PDF page size limit (pixels = points)
	maxExportPDF   = 60 << 20 // RLIMIT_FSIZE is 64 MB
)

// Export is the backend's side of the export folder.
type Export struct {
	dir     string   // absolute path, for the frontend
	root    *os.Root // opened before the sandbox
	current string   // file of the export in progress
}

func exportDirPath() string {
	if p := os.Getenv("RM_BROWSER_EXPORT"); p != "" {
		return p
	}
	return "/run/rm-browser-export"
}

// linkProtection: root must never follow links or hard links that the
// sandbox user made in the shared folder.
var linkProtection = []string{"/proc/sys/fs/protected_symlinks", "/proc/sys/fs/protected_hardlinks"}

// openExport prepares the folder (owned by root, sticky, writable for the
// sandbox) and removes what an earlier run left behind.
func openExport(dir string) (*Export, error) {
	for _, f := range linkProtection {
		if b, err := os.ReadFile(f); err != nil || strings.TrimSpace(string(b)) == "0" {
			return nil, fmt.Errorf("%s is off", filepath.Base(f))
		}
	}
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
	if err := os.Chmod(dir, 0o777|os.ModeSticky); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		abs = dir
	}
	e := &Export{dir: abs, root: root}
	e.removeAll()
	return e, nil
}

func (e *Export) removeAll() {
	d, err := e.root.Open(".")
	if err != nil {
		return
	}
	names, _ := d.Readdirnames(-1)
	d.Close()
	for _, n := range names {
		_ = e.root.Remove(n)
	}
}

// path is the absolute path of a document's export file.
func (e *Export) path(name string) string { return filepath.Join(e.dir, name+".pdf") }

// clear deletes the export file once it is in the library (or saving
// failed).
func (e *Export) clear() {
	if e.current != "" {
		_ = e.root.Remove(e.current)
		e.current = ""
	}
}

// write stores the PDF as "<name>.pdf".
func (e *Export) write(name string, b []byte) error {
	if err := checkDocName(name); err != nil {
		return err
	}
	if len(b) > maxExportPDF {
		return errors.New("the PDF would be too large")
	}
	e.clear()
	file := name + ".pdf"
	_ = e.root.Remove(file) // a leftover (or something planted): never written through
	f, err := e.root.OpenFile(file, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	e.current = file
	if _, err := f.Write(b); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// checkDocName: the frontend picks the document name (it knows which
// names the library already has); it must be a plain file name.
func checkDocName(name string) error {
	switch {
	case name == "" || len(name) > 200 || strings.HasPrefix(name, ".") || strings.TrimSpace(name) != name:
		return errors.New("bad document name")
	case strings.ContainsAny(name, "/\\"):
		return errors.New("bad document name")
	}
	for _, r := range name {
		if unicode.IsControl(r) || r == '\u2028' || r == '\u2029' || r == utf8.RuneError {
			return errors.New("bad document name")
		}
	}
	return nil
}

// ExportRequest is the reader's layout of the current page.
type ExportRequest struct {
	Name        string    `json:"name"` // document name, also the file name
	URL         string    `json:"url"`
	FontSize    float64   `json:"fontSize"`
	Serif       bool      `json:"serif"`
	Width       float64   `json:"width"`  // screen size: the PDF page
	Height      float64   `json:"height"` //
	X           float64   `json:"x"`      // where the reader area sits on the screen
	Y           float64   `json:"y"`
	Lines       []float64 `json:"lines"` // per line: visible characters, y, height, x (in the text)
	PageStarts  []float64 `json:"pageStarts"`
	PageHeights []float64 `json:"pageHeights"`
}

func (r *ExportRequest) check() error {
	switch {
	case checkDocName(r.Name) != nil:
		return errors.New("bad document name")
	case r.Width < 100 || r.Height < 100 || r.Width > maxPageSide || r.Height > maxPageSide:
		return errors.New("bad page size")
	case r.FontSize < 10 || r.FontSize > 100:
		return errors.New("bad font size")
	case len(r.Lines)%4 != 0 || len(r.Lines)/4 > maxExportLines:
		return errors.New("bad layout")
	case len(r.PageStarts) == 0 || len(r.PageStarts) > maxExportPages || len(r.PageHeights) != len(r.PageStarts):
		return errors.New("bad pages")
	}
	for _, v := range r.Lines {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return errors.New("bad layout")
		}
	}
	return nil
}

// ---------------------------------------------------------------- text stream

// style of a run of text, as Qt shows it
type textStyle struct {
	bold, italic, under, strike, mono bool
	scale                             float64 // relative to the reader's font size
	link                              string  // http(s) URL or #fragment
}

// item is one character, picture or rule of the page, in reading order.
type item struct {
	r        rune // 0: picture or rule
	st       textStyle
	img      string // picture: the JPEG's path
	imgW     float64
	imgH     float64
	rule     bool
	heading  int    // first character of a heading: its level (1-6)
	headText string // ... and its text
	marker   string // first character of a list item: "•" or "3."
}

func (it *item) space() bool { return it.r != 0 && unicode.IsSpace(it.r) }

// pageStream turns what the reader shows (title, meta line, page HTML)
// into items. It must count visible characters exactly as Qt does: every
// non-space character is one, every picture one.
func pageStream(p *Page) []item {
	var meta []string
	if p.Site != "" {
		meta = append(meta, html.EscapeString(p.Site))
	}
	if p.Byline != "" {
		meta = append(meta, html.EscapeString(p.Byline))
	}
	if !p.Secure {
		meta = append(meta, "<b>not secure (http)</b>")
	}
	if !p.Reader {
		meta = append(meta, "simplified page")
	}
	src := "<h1>" + html.EscapeString(p.Title) + "</h1><p><small>" + strings.Join(meta, " · ") + "</small></p>" + p.HTML
	nodes, err := html.ParseFragment(strings.NewReader(src), &html.Node{Type: html.ElementNode, Data: "div", DataAtom: atom.Div})
	if err != nil {
		return nil
	}
	s := &streamer{}
	for _, n := range nodes {
		s.walk(n, textStyle{scale: 1})
	}
	return s.items
}

type streamer struct {
	items   []item
	pre     int
	lists   []int // counters of the open lists (-1: bullets)
	pending *item // marker/heading waiting for the next visible character
}

var headingScale = map[int]float64{1: 2, 2: 1.5, 3: 1.2, 4: 1, 5: 0.8, 6: 1}

func (s *streamer) brk() { s.items = append(s.items, item{r: ' '}) }

func (s *streamer) add(it item) {
	if s.pending != nil && (it.r == 0 || !unicode.IsSpace(it.r)) {
		if s.pending.heading > 0 {
			it.heading, it.headText = s.pending.heading, s.pending.headText
		}
		if s.pending.marker != "" {
			it.marker = s.pending.marker
		}
		s.pending = nil
	}
	s.items = append(s.items, it)
}

func (s *streamer) walk(n *html.Node, st textStyle) {
	switch n.Type {
	case html.TextNode:
		for _, r := range n.Data {
			if s.pre == 0 && unicode.IsSpace(r) {
				r = ' '
			}
			s.add(item{r: r, st: st})
		}
		return
	case html.ElementNode:
	default:
		return
	}
	block := false
	switch n.Data {
	case "h1", "h2", "h3", "h4", "h5", "h6":
		lvl := int(n.Data[1] - '0')
		st.bold = true
		st.scale = headingScale[lvl]
		block = true
		s.brk()
		s.pending = &item{heading: lvl, headText: strings.Join(strings.Fields(textOf(n)), " ")}
	case "p", "div", "blockquote", "dl", "dt", "dd", "table", "tr", "caption", "figcaption":
		block = true
	case "pre":
		block = true
		st.mono = true
		s.pre++
		defer func() { s.pre-- }()
	case "ul", "ol":
		block = true
		c := -1
		if n.Data == "ol" {
			c = 1
			if v, err := strconv.Atoi(attr(n, "start")); err == nil {
				c = v
			}
		}
		s.lists = append(s.lists, c)
		defer func() { s.lists = s.lists[:len(s.lists)-1] }()
	case "li":
		block = true
		s.brk()
		m := "•"
		if k := len(s.lists); k > 0 && s.lists[k-1] >= 0 {
			m = strconv.Itoa(s.lists[k-1]) + "."
			s.lists[k-1]++
		}
		s.pending = &item{marker: m}
	case "td", "th":
		s.brk()
		if n.Data == "th" {
			st.bold = true
		}
	case "br":
		s.add(item{r: '\n', st: st})
		return
	case "hr":
		s.brk()
		s.add(item{rule: true})
		s.brk()
		return
	case "img":
		path := strings.TrimPrefix(attr(n, "src"), "file://")
		if p, err := url.PathUnescape(path); err == nil {
			path = p
		}
		w, _ := strconv.ParseFloat(attr(n, "width"), 64)
		h, _ := strconv.ParseFloat(attr(n, "height"), 64)
		s.add(item{st: st, img: path, imgW: w, imgH: h})
		return
	case "b", "strong":
		st.bold = true
	case "i", "em":
		st.italic = true
	case "u":
		st.under = true
	case "s", "del":
		st.strike = true
	case "small", "sub", "sup":
		st.scale *= 0.8
	case "code":
		st.mono = true
	case "a":
		if h := attr(n, "href"); strings.HasPrefix(h, "http://") || strings.HasPrefix(h, "https://") || strings.HasPrefix(h, "#") {
			st.link = h
			st.under = true
		}
	case "script", "style", "head", "title":
		return
	}
	if block {
		s.brk()
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		s.walk(c, st)
	}
	if block {
		s.brk()
	}
}

// ---------------------------------------------------------------- PDF

//go:embed fonts/*.ttf
var fontFS embed.FS

type fontMetrics struct{ asc, desc float64 } // per em

var (
	sansMetrics  = fontMetrics{1.069, 0.293} // Noto Sans
	serifMetrics = fontMetrics{1.007, 0.298} // EB Garamond
)

// ImageReader reads one of the reader's pictures (an absolute path from
// the page HTML).
type ImageReader func(path string) ([]byte, error)

// buildPDF draws the page as the reader laid it out.
func buildPDF(w io.Writer, p *Page, req ExportRequest, readImage ImageReader) (err error) {
	defer func() { // a bug in the PDF library must not take the browser down
		if r := recover(); r != nil {
			err = fmt.Errorf("PDF library: %v", r)
		}
	}()
	if err = req.check(); err != nil {
		return err
	}
	pdf := fpdf.NewCustom(&fpdf.InitType{UnitStr: "pt", Size: fpdf.SizeType{Wd: req.Width, Ht: req.Height}})
	pdf.SetMargins(0, 0, 0)
	pdf.SetAutoPageBreak(false, 0)
	pdf.SetCompression(true)
	family, faces := "NotoSans", map[string]string{"": "NotoSans-Regular", "B": "NotoSans-Bold", "I": "NotoSans-Italic", "BI": "NotoSans-BoldItalic"}
	metrics := sansMetrics
	if req.Serif {
		family, faces = "EBGaramond", map[string]string{"": "EBGaramond-Regular", "B": "EBGaramond-Bold", "I": "EBGaramond-Italic", "BI": "EBGaramond-BoldItalic"}
		metrics = serifMetrics
	}
	for style, name := range faces {
		b, err := fontFS.ReadFile("fonts/" + name + ".ttf")
		if err != nil {
			return err
		}
		pdf.AddUTF8FontFromBytes(family, style, b)
	}
	if family != "NotoSans" { // page numbers
		b, _ := fontFS.ReadFile("fonts/NotoSans-Regular.ttf")
		pdf.AddUTF8FontFromBytes("NotoSans", "", b)
	}
	title := strings.Join(strings.Fields(p.Title), " ")
	if title == "" {
		title = "Web page"
	}
	pdf.SetTitle(title, true)
	pdf.SetSubject(p.URL, true)
	pdf.SetAuthor(strings.Join(strings.Fields(p.Byline), " "), true)
	pdf.SetCreator("rm-browser", true)
	pdf.SetCreationDate(time.Now())

	type op struct {
		y     float64 // in the text
		h     float64
		x     float64
		items []item
	}
	nPages := len(req.PageStarts)
	ops := make([][]op, nPages)
	pageOf := func(y float64) int {
		i := 0
		for i+1 < nPages && req.PageStarts[i+1] <= y+0.5 {
			i++
		}
		return i
	}

	// hand out the characters line by line
	items := pageStream(p)
	pos := 0
	for l := 0; l+3 < len(req.Lines); l += 4 {
		n, y, h, x := int(req.Lines[l]), req.Lines[l+1], req.Lines[l+2], req.Lines[l+3]
		for pos < len(items) && items[pos].space() {
			pos++
		}
		start := pos
		if n == 0 {
			if pos < len(items) && items[pos].rule {
				pos++
			} else {
				continue
			}
		}
		for count := 0; count < n && pos < len(items); pos++ {
			if !items[pos].space() && !items[pos].rule {
				count++
			}
		}
		if pos > start {
			pg := pageOf(y)
			ops[pg] = append(ops[pg], op{y, h, x, items[start:pos]})
		}
	}
	for pos < len(items) && (items[pos].space() || items[pos].rule) {
		pos++
	}
	if pos < len(items) {
		return fmt.Errorf("layout and text don't match (%d of %d characters placed)", pos, len(items))
	}

	// links to places in the page (#fragment): to the heading's page
	fragLinks := map[string]int{}
	headingIDs := map[string][]string{} // heading text -> fragments
	for _, hd := range p.Headings {
		if hd.ID != "" {
			t := strings.Join(strings.Fields(hd.Text), " ")
			headingIDs[t] = append(headingIDs[t], hd.ID)
		}
	}
	linkFor := func(frag string) int {
		if id, ok := fragLinks[frag]; ok {
			return id
		}
		id := pdf.AddLink()
		fragLinks[frag] = id
		return id
	}

	base := req.FontSize
	setFont := func(st textStyle) float64 {
		size := math.Round(base * st.scale)
		style := ""
		if st.bold {
			style += "B"
		}
		if st.italic {
			style += "I"
		}
		size2 := size
		if st.mono {
			size2 = math.Round(size * 0.85)
		}
		pdf.SetFont(family, style, size2)
		return size2
	}
	minLevel := 9
	for _, ls := range ops {
		for _, o := range ls {
			for _, it := range o.items {
				if it.heading > 1 && it.heading < minLevel {
					minLevel = it.heading
				}
			}
		}
	}

	for pg := 0; pg < nPages; pg++ {
		pdf.AddPage()
		top, height := req.PageStarts[pg], req.PageHeights[pg]
		pdf.ClipRect(0, req.Y, req.Width, height, false)
		lastLevel := 0
		for _, o := range ops[pg] {
			y := req.Y + (o.y - top)
			// the line's baseline: below the tallest font's ascent
			maxScale := 0.0
			for _, it := range o.items {
				if it.r != 0 && !it.space() && it.st.scale > maxScale {
					maxScale = it.st.scale
				}
			}
			if maxScale == 0 {
				maxScale = 1
			}
			baseline := y + metrics.asc*math.Round(base*maxScale)
			x := req.X + o.x
			for i := 0; i < len(o.items); {
				it := o.items[i]
				switch {
				case it.rule:
					pdf.SetDrawColor(0, 0, 0)
					pdf.SetLineWidth(1)
					pdf.Line(req.X, y+o.h/2, req.X+(req.Width-2*req.X), y+o.h/2)
					i++
					continue
				case it.img != "":
					if b, err := readImage(it.img); err == nil {
						name := it.img
						info := pdf.RegisterImageOptionsReader(name, fpdf.ImageOptions{ImageType: "JPG"}, bytes.NewReader(b))
						if info != nil && !pdf.Err() {
							iw, ih := it.imgW, it.imgH
							if iw <= 0 || ih <= 0 {
								iw, ih = info.Width(), info.Height()
							}
							link, linkStr := 0, ""
							if strings.HasPrefix(it.st.link, "#") {
								link = linkFor(it.st.link[1:])
							} else {
								linkStr = it.st.link
							}
							pdf.ImageOptions(name, x, y, iw, ih, false, fpdf.ImageOptions{ImageType: "JPG"}, link, linkStr)
							x += iw
						}
						if pdf.Err() { // a bad picture is left out, not the whole PDF
							pdf.ClearError()
						}
					}
					i++
					continue
				}
				if it.heading > 0 {
					lvl := 0
					if it.heading > 1 {
						lvl = 1 + it.heading - minLevel
						if lvl > lastLevel+1 {
							lvl = lastLevel + 1
						}
					}
					text := it.headText
					if text == "" {
						text = title
					}
					pdf.Bookmark(text, lvl, y)
					lastLevel = lvl
					for _, frag := range headingIDs[text] {
						pdf.SetLink(linkFor(frag), y, -1)
					}
				}
				if it.marker != "" {
					setFont(textStyle{scale: it.st.scale})
					mw := pdf.GetStringWidth(it.marker)
					gap := 0.35 * base
					pdf.SetTextColor(0, 0, 0)
					pdf.Text(x-gap-mw, baseline, it.marker)
				}
				// a run of characters in one style
				j := i
				var run strings.Builder
				for j < len(o.items) && o.items[j].r != 0 && o.items[j].st == it.st && (j == i || (o.items[j].heading == 0 && o.items[j].marker == "")) {
					r := o.items[j].r
					if r == '\n' {
						r = ' '
					}
					if r > 0xFFFF {
						r = '?' // fpdf's fonts cover the Basic Multilingual Plane only
					}
					run.WriteRune(r)
					j++
				}
				txt := run.String()
				size := setFont(it.st)
				wd := pdf.GetStringWidth(txt)
				pdf.SetTextColor(0, 0, 0)
				pdf.Text(x, baseline, txt)
				if it.st.under {
					pdf.SetLineWidth(math.Max(1, size/16))
					pdf.SetDrawColor(0, 0, 0)
					uy := baseline + size*0.12
					pdf.Line(x, uy, x+wd, uy)
				}
				if it.st.strike {
					pdf.SetLineWidth(math.Max(1, size/16))
					sy := baseline - size*0.3
					pdf.Line(x, sy, x+wd, sy)
				}
				if it.st.link != "" {
					lh := o.h
					if strings.HasPrefix(it.st.link, "#") {
						pdf.Link(x, y, wd, lh, linkFor(it.st.link[1:]))
					} else {
						pdf.LinkString(x, y, wd, lh, it.st.link)
					}
				}
				x += wd
				i = j
			}
		}
		pdf.ClipEnd()
		// page number, where the reader shows it
		pdf.SetFont("NotoSans", "", 22)
		pdf.SetTextColor(0x55, 0x55, 0x55)
		num := fmt.Sprintf("%d / %d", pg+1, nPages)
		pdf.Text((req.Width-pdf.GetStringWidth(num))/2, req.Height-22-0.293*22, num)
		if pdf.Err() {
			return pdf.Error()
		}
	}
	return pdf.Output(w)
}
