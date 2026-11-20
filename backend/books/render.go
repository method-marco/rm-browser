package main

// A chapter (one spine item) is written as the HTML subset Qt's rich text
// engine understands, like the browser's reader view (backend/reader.go):
// only elements the writer knows, all text escaped, pictures re-encoded.
// The book's own CSS is not used: the reader's font settings apply.
//
// Positions: the writer notes, for every visible character it writes,
// where it came from (text node and offset in crengine's DOM). The
// frontend counts visible characters in the laid-out text the same way
// (isCounted here, counted() in app/shared/ReaderView.qml), so "the
// 1234th visible character of chapter 7" is the same place on both
// sides, and from there the exact KOReader position follows.

import (
	"fmt"
	"html/template"
	"net/url"
	"path"
	"strings"
)

type textPos struct{ seq, off int32 } // text node (domNode.seq) and rune offset

type chapter struct {
	html     string
	pos      []textPos      // per visible character written
	anchors  map[string]int // element id -> index of the next visible character
	pictures int            // pictures in the chapter (shown or not)
}

// isCounted: characters that count as visible. Everything except what
// JavaScript's \s matches, the picture placeholder U+FFFC, Qt's table
// marks and invisible format characters (soft hyphen, zero width).
// Must match counted() in ReaderView.qml.
func isCounted(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0xA0, 0x1680, 0x2028, 0x2029, 0x202F, 0x205F, 0x3000, 0xFEFF,
		0xFFFC, 0xFDD0, 0xFDD1, 0xFDD2, 0xAD, 0x200B, 0x200C, 0x200D, 0x2060:
		return false
	}
	return !(r >= 0x2000 && r <= 0x200A)
}

// keepRune: characters written at all (no control characters, no
// non-characters).
func keepRune(r rune) bool {
	switch {
	case r < 0x20 && r != '\t' && r != '\n':
		return false
	case r >= 0x7F && r <= 0x9F:
		return false
	case r == 0xFFFE || r == 0xFFFF || (r >= 0xFDD0 && r <= 0xFDEF):
		return false
	case r >= 0xD800 && r <= 0xDFFF:
		return false
	}
	return true
}

// ImageFunc prepares a picture of the book (its path in the EPUB) for the
// screen: the converted file and its size, or ok=false to leave it out.
type ImageFunc func(zipPath string) (file string, w, h int, ok bool)

type chapterWriter struct {
	book     *epubBook
	frag     int // spine item, 0-based
	b        strings.Builder
	pos      []textPos
	anchors  map[string]int
	images   ImageFunc // nil: no pictures (counting only)
	maxW     int
	indent   int
	cells    int
	href     string // link around the text being written (link with blocks inside)
	pictures int
	// the book's style (css.go): nil = the reader's plain formatting
	sheet    styleSheet
	inAlign  string  // inherited from enclosing blocks
	inIndent float64 // em, -1 = none
	inChars  string
	hyph     *hyphenator // soft hyphens in words (nil: none)
	noHyph   int         // inside headings, preformatted text, code
}

// renderOptions: how a chapter is written. The text, and so every
// position, is the same either way.
type renderOptions struct {
	bookStyle bool // the book's own formatting (css.go)
	hyphenate bool // soft hyphens where words may break (hyphenate.go)
}

// renderChapter writes chapter frag (see renderOptions).
func (bk *epubBook) renderChapter(frag int, images ImageFunc, maxW int, opt renderOptions) *chapter {
	w := &chapterWriter{book: bk, frag: frag, anchors: map[string]int{}, images: images, maxW: maxW, inIndent: -1}
	if opt.hyphenate {
		w.hyph = hyphenatorFor(bk.lang)
	}
	if opt.bookStyle && frag >= 0 && frag < len(bk.frags) {
		w.sheet = bk.sheetFor(frag)
		if w.sheet == nil {
			w.sheet = styleSheet{} // no CSS: still the book's style attributes
		}
	}
	if frag >= 0 && frag < len(bk.frags) && bk.frags[frag] != nil {
		w.writeChildren(bk.frags[frag])
	}
	return &chapter{html: w.b.String(), pos: w.pos, anchors: w.anchors, pictures: w.pictures}
}

func (w *chapterWriter) text(n *domNode) {
	rs := make([]rune, 0, len(n.text))
	for i, r := range n.text {
		if !keepRune(r) {
			continue
		}
		if isCounted(r) {
			w.pos = append(w.pos, textPos{int32(n.seq), int32(i)})
		}
		rs = append(rs, r)
	}
	if w.hyph != nil && w.noHyph == 0 {
		rs = w.hyph.hyphenate(rs)
	}
	s := template.HTMLEscapeString(string(rs))
	if w.href != "" && strings.TrimSpace(s) != "" {
		w.b.WriteString(linkOpen(w.href) + s + "</a>")
		return
	}
	w.b.WriteString(s)
}

func linkOpen(href string) string {
	return `<a href="` + template.HTMLEscapeString(href) + `" style="color:#000000; text-decoration:underline">`
}

func (w *chapterWriter) writeChildren(n *domNode) {
	for _, c := range n.children {
		w.write(c)
	}
}

func (w *chapterWriter) wrap(open, close string, n *domNode) {
	w.b.WriteString(open)
	w.writeChildren(n)
	w.b.WriteString(close)
}

// block writes a block element and takes it out again if it shows nothing
// (no visible character, picture or rule): empty paragraphs would leave
// gaps on the page.
func (w *chapterWriter) block(open, close string, n *domNode) {
	start, npos := w.b.Len(), len(w.pos)
	w.wrap(open, close, n)
	seg := w.b.String()[start:]
	if len(w.pos) > npos || strings.Contains(seg, "<img") || strings.Contains(seg, "<hr") {
		return
	}
	s := w.b.String()[:start]
	w.b.Reset()
	w.b.WriteString(s)
}

func (w *chapterWriter) indented(px int, open, close string, n *domNode) {
	w.indent += px
	w.container(n, func() { w.block(open, close, n) })
	w.indent -= px
}

// alignOf: the alignment the book asks for on this element, from the align
// attribute or an inline style (classes from the book's CSS are not read).
func alignOf(n *domNode) string {
	a := strings.ToLower(strings.TrimSpace(n.attr("align")))
	if st := strings.ToLower(n.attr("style")); strings.Contains(st, "text-align") {
		for _, v := range []string{"center", "right"} {
			if strings.Contains(st, "text-align:"+v) || strings.Contains(st, "text-align: "+v) {
				a = v
			}
		}
	}
	if n.name == "center" {
		a = "center"
	}
	if a == "center" || a == "right" {
		return a
	}
	return ""
}

// paragraph: the frontend restyles `<p style="line-height:125%">` (line
// spacing, justified, indented first lines); aligned ones keep their
// alignment.
func (w *chapterWriter) paragraphOpen(n *domNode) string {
	if w.sheet != nil {
		f := w.blockFormatOf(n)
		if a := alignOf(n); a != "" {
			f.align = a
		}
		return f.open("p")
	}
	if a := alignOf(n); a != "" {
		return `<p align="` + a + `" style="line-height:125%">`
	}
	return `<p style="line-height:125%">`
}

func hasBlockChild(n *domNode) bool {
	for _, c := range n.children {
		if c.isText() {
			continue
		}
		switch c.name {
		case "p", "div", "section", "article", "main", "header", "footer", "aside", "nav", "figure", "h1", "h2",
			"h3", "h4", "h5", "h6", "ul", "ol", "dl", "table", "blockquote", "pre", "hr", "img", "svg",
			"figcaption", "center", "hgroup", "address":
			return true
		}
	}
	return false
}

// containsBlock: n has block content inside, which Qt can't lay out inside
// a link.
func containsBlock(n *domNode) bool {
	for _, c := range n.children {
		if c.isText() {
			continue
		}
		if hasBlockChild(c) || c.name == "p" || c.name == "div" || c.name == "img" || c.name == "svg" {
			return true
		}
		if containsBlock(c) {
			return true
		}
	}
	return false
}

// linkTarget: a link inside the book becomes "book:<spine item>#<id>",
// web links stay; anything else is not a link.
func (w *chapterWriter) linkTarget(href string) string {
	href = strings.TrimSpace(href)
	if href == "" {
		return ""
	}
	if u, err := url.Parse(href); err == nil && u.Scheme != "" {
		if u.Scheme == "http" || u.Scheme == "https" {
			return u.String()
		}
		return ""
	}
	f, anchor, ok := w.book.target(w.book.fragPaths[w.frag], href)
	if !ok {
		return ""
	}
	return fmt.Sprintf("book:%d#%s", f, anchor)
}

func (w *chapterWriter) image(src string) {
	if src == "" || strings.HasPrefix(src, "data:") {
		return
	}
	w.pictures++
	if w.images == nil {
		return
	}
	if u, err := url.Parse(src); err != nil || u.Scheme != "" {
		return
	}
	p, _ := url.PathUnescape(strings.SplitN(src, "#", 2)[0])
	p = path.Clean(path.Join(path.Dir(w.book.fragPaths[w.frag]), p))
	file, iw, ih, ok := w.images(p)
	if !ok {
		return
	}
	if w.maxW > 0 {
		avail := w.maxW - w.indent - 10
		if w.cells > 0 {
			avail /= 2
		}
		avail = max(avail, 120)
		if iw > avail {
			ih = ih * avail / iw
			iw = avail
		}
	}
	img := fmt.Sprintf(`<img src="file://%s" width="%d" height="%d"/>`, template.HTMLEscapeString(file), iw, ih)
	if w.href != "" {
		img = linkOpen(w.href) + img + "</a>"
	}
	w.b.WriteString(`<p align="center">` + img + `</p>`)
}

func (w *chapterWriter) write(n *domNode) {
	if n.isText() {
		w.text(n)
		return
	}
	if n.id != "" {
		if _, ok := w.anchors[n.id]; !ok {
			w.anchors[n.id] = len(w.pos)
		}
	}
	switch n.name {
	case "script", "style", "head", "title", "noscript", "template", "rt", "rp", "math":
	case "h1", "h2", "h3", "h4", "h5", "h6":
		// sizes that suit a book page: chapter titles h2, the rest smaller
		level := map[string]int{"h1": 2, "h2": 3}[n.name]
		if level == 0 {
			level = 4
		}
		open := fmt.Sprintf("<h%d>", level)
		if a := alignOf(n); a != "" {
			open = fmt.Sprintf(`<h%d align="%s">`, level, a)
		}
		if w.sheet != nil {
			f := w.blockFormatOf(n)
			f.indent = -1 // headings: no first-line indent
			if a := alignOf(n); a != "" {
				f.align = a
			}
			open = f.open(fmt.Sprintf("h%d", level))
		}
		w.noHyph++ // headings stay whole words
		w.block(open, fmt.Sprintf("</h%d>", level), n)
		w.noHyph--
	case "p":
		w.block(w.paragraphOpen(n), "</p>", n)
	case "li":
		if w.sheet != nil {
			f := w.blockFormatOf(n)
			f.indent = -1
			w.block(f.open("li"), "</li>", n)
			return
		}
		w.block(`<li style="line-height:125%">`, "</li>", n)
	case "br":
		w.b.WriteString("<br/>")
	case "hr":
		w.b.WriteString("<hr/>")
	case "blockquote":
		w.indented(80, "<blockquote>", "</blockquote>", n)
	case "ul":
		w.indented(40, "<ul>", "</ul>", n)
	case "ol":
		if s := n.attr("start"); s != "" {
			w.indented(40, `<ol start="`+template.HTMLEscapeString(s)+`">`, "</ol>", n)
			return
		}
		w.indented(40, "<ol>", "</ol>", n)
	case "dd":
		w.indented(40, "<dd>", "</dd>", n)
	case "dl", "dt":
		w.block("<"+n.name+">", "</"+n.name+">", n)
	case "table":
		w.block(`<table border="1" cellspacing="0" cellpadding="6">`, "</table>", n)
	case "code":
		w.noHyph++
		w.wrap("<code>", "</code>", n)
		w.noHyph--
	case "thead", "tbody", "tfoot", "tr", "sub", "sup", "small":
		w.wrap("<"+n.name+">", "</"+n.name+">", n)
	case "th", "td":
		open := "<" + n.name
		for _, k := range []string{"colspan", "rowspan"} {
			if v := n.attr(k); v != "" {
				open += " " + k + `="` + template.HTMLEscapeString(v) + `"`
			}
		}
		w.cells++
		w.wrap(open+">", "</"+n.name+">", n)
		w.cells--
	case "caption", "figcaption":
		w.block(`<p align="center"><i>`, "</i></p>", n)
	case "pre":
		w.noHyph++
		w.wrap(`<pre style="white-space:pre-wrap">`, "</pre>", n)
		w.noHyph--
	case "kbd", "samp", "tt":
		w.noHyph++
		w.wrap("<code>", "</code>", n)
		w.noHyph--
	case "b", "strong":
		w.inline(n, func() { w.wrap("<b>", "</b>", n) })
	case "i", "em", "cite", "dfn", "var":
		w.inline(n, func() { w.wrap("<i>", "</i>", n) })
	case "u", "ins":
		w.wrap("<u>", "</u>", n)
	case "s", "del", "strike":
		w.wrap("<s>", "</s>", n)
	case "a":
		href := w.linkTarget(n.attr("href"))
		prev := w.href
		switch {
		case href == "":
			w.writeChildren(n)
		case containsBlock(n):
			w.href = href // Qt can't put blocks inside a link: link what's inside
			w.writeChildren(n)
		default:
			w.href = ""
			w.wrap(linkOpen(href), "</a>", n)
		}
		w.href = prev
	case "img":
		w.image(n.attr("src"))
	case "image": // inside <svg> (cover pages)
		w.image(n.attr("href"))
	case "div", "section", "article", "main", "header", "footer", "aside", "nav", "figure", "body",
		"center", "hgroup", "address", "svg":
		if hasBlockChild(n) || n.name == "svg" {
			w.container(n, func() { w.writeChildren(n) })
		} else {
			w.block(w.paragraphOpen(n), "</p>", n)
		}
	default:
		w.inline(n, func() { w.writeChildren(n) })
	}
}

// container: a block holding blocks passes the book's alignment, indent
// and character formatting on to them (inherited, as in CSS).
func (w *chapterWriter) container(n *domNode, body func()) {
	if w.sheet == nil {
		body()
		return
	}
	f := w.blockFormatOf(n)
	a, i, c := w.inAlign, w.inIndent, w.inChars
	w.inAlign, w.inIndent, w.inChars = f.align, f.indent, f.chars
	body()
	w.inAlign, w.inIndent, w.inChars = a, i, c
}

// inline: an inline element with the book's character formatting.
func (w *chapterWriter) inline(n *domNode, body func()) {
	if w.sheet == nil {
		body()
		return
	}
	cs := charStyle(w.sheet.computed(n))
	if cs == "" {
		body()
		return
	}
	w.b.WriteString(`<span style="` + cs + `">`)
	body()
	w.b.WriteString("</span>")
}
