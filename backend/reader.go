package main

// Reader mode. Every page goes through three steps:
//
//  1. Mozilla Readability (codeberg.org/readeck/go-readability, a port of
//     Firefox's Reader View) finds the article and drops menus, ads,
//     scripts and other clutter. Pages without an article (home pages,
//     link lists) fall back to the page body without scripts, forms and
//     menus.
//  2. bluemonday sanitizes the result with a strict allowlist: only
//     text-level tags survive, links only with http(s) / mailto addresses.
//  3. The sanitized tree is written out as the HTML subset Qt's rich text
//     engine understands, with images downloaded, re-encoded
//     and sized to fit one screen.
//
// The site's navigation menus are collected separately and shown in the
// Contents panel next to the article's headings.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"html/template"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"unicode"

	readability "codeberg.org/readeck/go-readability/v2"
	"github.com/microcosm-cc/bluemonday"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

type Heading struct {
	Level int    `json:"level"`
	Text  string `json:"text"`
	ID    string `json:"id,omitempty"`
}

type Link struct {
	Text string `json:"text"`
	URL  string `json:"url"`
}

type Page struct {
	URL      string    `json:"url"`
	Title    string    `json:"title"`
	Byline   string    `json:"byline,omitempty"`
	Site     string    `json:"site,omitempty"`
	HTML     string    `json:"html"`
	Headings []Heading `json:"headings"`
	Links    []Link    `json:"links"`
	Secure   bool      `json:"secure"`
	Reader   bool      `json:"reader"` // false: no article found, simplified full page
}

// ImageFunc downloads and prepares one image; it returns the local file and
// the size to show it at, or ok=false to leave the image out.
type ImageFunc func(ctx context.Context, src string) (path string, w, h int, ok bool)

const (
	maxImagesPerPage = 40
	minArticleChars  = 500
	maxNavLinks      = 120
)

var sanitizer = func() *bluemonday.Policy {
	p := bluemonday.NewPolicy()
	p.AllowElements("p", "br", "hr", "h1", "h2", "h3", "h4", "h5", "h6", "blockquote", "pre", "code",
		"b", "strong", "i", "em", "u", "s", "del", "ins", "sub", "sup", "small", "mark", "abbr", "cite",
		"q", "kbd", "samp", "var", "time", "span", "div", "section", "article", "main", "header",
		"footer", "ul", "ol", "li", "dl", "dt", "dd", "table", "thead", "tbody", "tfoot", "tr", "th",
		"td", "caption", "figure", "figcaption", "picture")
	p.AllowAttrs("href", "id", "name").OnElements("a")
	p.AllowAttrs("id").OnElements("h1", "h2", "h3", "h4", "h5", "h6", "section", "div", "p", "span")
	p.AllowAttrs("src", "alt", "width", "height", "srcset").OnElements("img")
	p.AllowAttrs("srcset").OnElements("source")
	p.AllowElements("source")
	p.AllowAttrs("colspan", "rowspan").OnElements("td", "th")
	p.AllowAttrs("start").OnElements("ol")
	p.AllowURLSchemes("http", "https", "mailto")
	p.RequireParseableURLs(true)
	p.AllowRelativeURLs(true)
	return p
}()

// buildPage turns a downloaded response into a reader page.
// maxW is the width of the reader area in pixels: images in indented
// blocks (lists, quotes, tables) are shrunk to fit; 0 = don't shrink.
func buildPage(ctx context.Context, f *Fetched, images ImageFunc, maxW int) (*Page, error) {
	pg := &Page{URL: f.URL.String(), Secure: f.URL.Scheme == "https", Headings: []Heading{}, Links: []Link{}}
	switch {
	case isHTML(f.ContentType):
		return buildHTMLPage(ctx, f, pg, images, maxW)
	case strings.HasPrefix(f.ContentType, "text/"):
		pg.Title = lastPathPart(f.URL)
		pg.Reader = true
		pg.HTML = `<pre style="white-space:pre-wrap">` + template.HTMLEscapeString(stripUnsupported(string(f.Body))) + `</pre>`
		return pg, nil
	case strings.HasPrefix(f.ContentType, "image/"):
		pg.Title = lastPathPart(f.URL)
		pg.Reader = true
		if images != nil {
			if p, w, h, ok := images(ctx, f.URL.String()); ok {
				pg.HTML = imgTag(p, w, h)
				return pg, nil
			}
		}
		return nil, errors.New("this image cannot be shown")
	}
	return nil, fmt.Errorf("this browser only shows web pages, not %s files", f.ContentType)
}

func buildHTMLPage(ctx context.Context, f *Fetched, pg *Page, images ImageFunc, maxW int) (*Page, error) {
	doc, err := html.Parse(bytes.NewReader(f.Body))
	if err != nil {
		return nil, err
	}
	pg.Links = navLinks(doc, f.URL)
	docTitle := ""
	if t := findFirst(doc, func(n *html.Node) bool { return n.Type == html.ElementNode && n.Data == "title" }); t != nil {
		docTitle = collapseSpace(textOf(t))
	}

	unwrapHeadings(doc)
	markVideos(doc, f.URL)
	var content string
	// A site's front page is a list of stories, not one article: show it
	// whole (simplified). So are pages Readability itself doesn't consider
	// articles (the check Firefox uses to offer Reader View).
	front := strings.Trim(f.URL.Path, "/") == "" && f.URL.RawQuery == ""
	if !front && readability.CheckDocument(doc) {
		art, err := readability.FromDocument(doc, f.URL)
		if err == nil && art.Node != nil && len(collapseSpace(textOf(art.Node))) >= minArticleChars {
			var b strings.Builder
			if err := html.Render(&b, art.Node); err == nil {
				content = b.String()
				pg.Reader = true
				pg.Title = collapseSpace(art.Title())
				pg.Byline = collapseSpace(art.Byline())
				pg.Site = collapseSpace(art.SiteName())
			}
		}
	}
	if !pg.Reader {
		// No article: the page's main area (or the whole body) without
		// scripts, forms, menus, pop-ups and other clutter. Readability
		// changed the tree, so parse again.
		doc, _ = html.Parse(bytes.NewReader(f.Body))
		markVideos(doc, f.URL)
		body := simplifiedRoot(doc)
		var b strings.Builder
		for c := body.FirstChild; c != nil; c = c.NextSibling {
			_ = html.Render(&b, c)
		}
		content = b.String()
	}
	if pg.Title == "" {
		pg.Title = docTitle
	}
	if pg.Title == "" {
		pg.Title = f.URL.Hostname()
	}
	if pg.Site == "" {
		pg.Site = strings.TrimPrefix(f.URL.Hostname(), "www.")
	}

	clean := sanitizer.Sanitize(content)
	frag, err := html.ParseFragment(strings.NewReader(clean), &html.Node{Type: html.ElementNode, Data: "div", DataAtom: atom.Div})
	if err != nil {
		return nil, err
	}
	root := &html.Node{Type: html.ElementNode, Data: "div"}
	for _, n := range frag {
		root.AppendChild(n)
	}
	w := &qtWriter{base: f.URL, title: pg.Title, images: map[string]imgResult{}, maxW: maxW}
	w.prepareImages(ctx, root, images)
	w.writeChildren(root)
	pg.HTML = w.b.String()
	pg.Headings = w.headings
	if strings.TrimSpace(textOf(root)) == "" && len(w.images) == 0 {
		if len(pg.Links) > 0 {
			pg.HTML = "<p><i>This page has no readable text. Its links are in Contents.</i></p>"
		} else {
			pg.HTML = "<p><i>This page has no readable text. It probably needs JavaScript, which this browser does not run.</i></p>"
		}
	}
	return pg, nil
}

type imgResult struct {
	path string
	w, h int
}

// qtWriter writes the sanitized tree as Qt rich text.
type qtWriter struct {
	b         bytes.Buffer
	base      *url.URL
	title     string
	headings  []Heading
	images    map[string]imgResult
	pre       int
	skipTitle bool
	maxW      int    // reader width; 0 = unknown
	indent    int    // pixels taken by enclosing lists and quotes
	cells     int    // inside table cells
	href      string // link pushed down from an <a> around blocks
}

func (w *qtWriter) prepareImages(ctx context.Context, root *html.Node, images ImageFunc) {
	if images == nil {
		return
	}
	var srcs []string
	seen := map[string]bool{}
	walk(root, func(n *html.Node) bool {
		if n.Type == html.ElementNode && n.Data == "img" {
			if s := w.imageSource(n); s != "" && !seen[s] && len(srcs) < maxImagesPerPage {
				seen[s] = true
				srcs = append(srcs, s)
			}
		}
		return true
	})
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4)
	for _, s := range srcs {
		wg.Add(1)
		go func(s string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			if p, iw, ih, ok := images(ctx, s); ok {
				mu.Lock()
				w.images[s] = imgResult{p, iw, ih}
				mu.Unlock()
			}
		}(s)
	}
	wg.Wait()
}

// imageSource picks the address of an <img>: src, or the largest srcset
// candidate when src is missing or a placeholder.
func (w *qtWriter) imageSource(n *html.Node) string {
	src := attr(n, "src")
	if src == "" || strings.HasPrefix(src, "data:") {
		src = bestSrcset(attr(n, "srcset"))
		if src == "" && n.Parent != nil && n.Parent.Data == "picture" {
			for c := n.Parent.FirstChild; c != nil && src == ""; c = c.NextSibling {
				if c.Type == html.ElementNode && c.Data == "source" {
					src = bestSrcset(attr(c, "srcset"))
				}
			}
		}
	}
	if src == "" {
		return ""
	}
	u, err := w.base.Parse(src)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return ""
	}
	if strings.HasSuffix(strings.ToLower(u.Path), ".svg") {
		return ""
	}
	if isTrackerImage(n, u.String()) {
		return "" // not even downloaded
	}
	return u.String()
}

func bestSrcset(s string) string {
	best, bestW := "", -1.0
	for _, cand := range strings.Split(s, ",") {
		f := strings.Fields(strings.TrimSpace(cand))
		if len(f) == 0 {
			continue
		}
		wv := 1.0
		if len(f) > 1 {
			var v float64
			var unit string
			if _, err := fmt.Sscanf(f[1], "%f%s", &v, &unit); err == nil {
				if unit == "x" {
					v *= 1000
				}
				wv = v
			}
		}
		// prefer the candidate closest to ~1000 px: big enough, not huge
		score := wv
		if wv > 1600 {
			score = 1600 - (wv-1600)/10
		}
		if score > bestW {
			best, bestW = f[0], score
		}
	}
	return best
}

func (w *qtWriter) text(s string) {
	s = stripUnsupported(s)
	if w.href != "" && strings.TrimSpace(s) != "" {
		w.b.WriteString(w.linkOpen(w.href) + template.HTMLEscapeString(s) + "</a>")
		return
	}
	w.b.WriteString(template.HTMLEscapeString(s))
}

func (w *qtWriter) linkOpen(href string) string {
	return `<a href="` + template.HTMLEscapeString(href) + `" style="color:#000000; text-decoration:underline">`
}

// containsBlock: n has block content inside, which Qt cannot lay out
// inside a list item or a link.
func containsBlock(n *html.Node) bool {
	return findFirst(n, func(m *html.Node) bool {
		if m == n || m.Type != html.ElementNode {
			return false
		}
		switch m.Data {
		case "p", "div", "section", "article", "header", "footer", "figure", "picture", "img", "h1", "h2",
			"h3", "h4", "h5", "h6", "ul", "ol", "dl", "table", "blockquote", "pre", "hr":
			return true
		}
		return false
	}) != nil
}

// cardList: a list whose items hold paragraphs, headings or pictures (the
// story lists of front pages). Qt cannot nest such blocks in list items,
// so the items are written as plain blocks with a rule between them.
func (w *qtWriter) cardList(n *html.Node) bool {
	cards := false
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && c.Data == "li" && containsBlock(c) {
			cards = true
			break
		}
	}
	if !cards {
		return false
	}
	first := true
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type != html.ElementNode || c.Data != "li" {
			w.write(c)
			continue
		}
		start := w.b.Len()
		if !first {
			w.b.WriteString("<hr/>")
		}
		mark := w.b.Len()
		if containsBlock(c) {
			w.writeChildren(c)
		} else {
			w.block(`<p style="line-height:125%">`, "</p>", c)
		}
		if w.b.Len() == mark {
			w.b.Truncate(start) // empty item: no rule either
			continue
		}
		first = false
	}
	return true
}

func (w *qtWriter) writeChildren(n *html.Node) {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		w.write(c)
	}
}

func (w *qtWriter) wrap(open, close string, n *html.Node) {
	w.b.WriteString(open)
	w.writeChildren(n)
	w.b.WriteString(close)
}

var tagRe = regexp.MustCompile(`<[^>]*>`)

// block writes a block element and takes it out again if it shows nothing
// (no text, image or rule): empty paragraphs and list items would leave
// gaps on the page.
func (w *qtWriter) block(open, close string, n *html.Node) {
	start := w.b.Len()
	w.wrap(open, close, n)
	seg := w.b.Bytes()[start:]
	if bytes.Contains(seg, []byte("<img")) || bytes.Contains(seg, []byte("<hr")) {
		return
	}
	txt := strings.ReplaceAll(tagRe.ReplaceAllString(string(seg), ""), "&nbsp;", "")
	if strings.TrimSpace(txt) == "" {
		w.b.Truncate(start)
	}
}

// indented writes n with extra indentation (lists, quotes): images inside
// get correspondingly less room.
func (w *qtWriter) indented(px int, open, close string, n *html.Node) {
	w.indent += px
	w.block(open, close, n)
	w.indent -= px
}

func (w *qtWriter) write(n *html.Node) {
	switch n.Type {
	case html.TextNode:
		w.text(n.Data)
		return
	case html.ElementNode:
	default:
		return
	}
	switch n.Data {
	case "h1", "h2", "h3", "h4", "h5", "h6":
		t := collapseSpace(stripUnsupported(textOf(n)))
		if t == "" {
			return
		}
		if !w.skipTitle && len(w.headings) == 0 && strings.EqualFold(t, w.title) {
			w.skipTitle = true // the title is shown above the article already
			return
		}
		level := int(n.Data[1] - '0')
		id := attr(n, "id")
		if id == "" {
			if a := findFirst(n, func(m *html.Node) bool {
				return m.Type == html.ElementNode && m.Data == "a" && (attr(m, "id") != "" || attr(m, "name") != "")
			}); a != nil {
				id = attr(a, "id")
				if id == "" {
					id = attr(a, "name")
				}
			}
		}
		w.headings = append(w.headings, Heading{Level: level, Text: t, ID: id})
		// Article headings start at h2 so that the page title stays the largest.
		if level < 2 {
			level = 2
		}
		href := w.href
		if href == "" { // a headline that is a link (front pages)
			if a := findFirst(n, func(m *html.Node) bool { return m.Type == html.ElementNode && m.Data == "a" }); a != nil &&
				collapseSpace(textOf(a)) == collapseSpace(textOf(n)) {
				href = w.linkTarget(attr(a, "href"))
			}
		}
		fmt.Fprintf(&w.b, "<h%d>", level)
		if href != "" {
			w.b.WriteString(w.linkOpen(href) + template.HTMLEscapeString(t) + "</a>")
		} else {
			w.b.WriteString(template.HTMLEscapeString(t))
		}
		fmt.Fprintf(&w.b, "</h%d>", level)
	case "p":
		w.block(`<p style="line-height:125%">`, "</p>", n)
	case "li":
		w.block(`<li style="line-height:125%">`, "</li>", n)
	case "br":
		w.b.WriteString("<br/>")
	case "hr":
		w.b.WriteString("<hr/>")
	case "blockquote":
		w.indented(80, "<blockquote>", "</blockquote>", n)
	case "ul":
		if w.cardList(n) {
			return
		}
		w.indented(40, "<ul>", "</ul>", n)
	case "ol":
		if w.cardList(n) {
			return
		}
		if s := attr(n, "start"); s != "" {
			w.indented(40, `<ol start="`+template.HTMLEscapeString(s)+`">`, "</ol>", n)
			return
		}
		w.indented(40, "<ol>", "</ol>", n)
	case "dd":
		w.indented(40, "<dd>", "</dd>", n)
	case "dl", "dt":
		w.block("<"+n.Data+">", "</"+n.Data+">", n)
	case "table":
		w.block(`<table border="1" cellspacing="0" cellpadding="6">`, "</table>", n)
	case "thead", "tbody", "tfoot", "tr", "sub", "sup", "small", "code":
		w.wrap("<"+n.Data+">", "</"+n.Data+">", n)
	case "th", "td":
		open := "<" + n.Data
		for _, k := range []string{"colspan", "rowspan"} {
			if v := attr(n, k); v != "" {
				open += " " + k + `="` + template.HTMLEscapeString(v) + `"`
			}
		}
		w.cells++
		w.wrap(open+">", "</"+n.Data+">", n)
		w.cells--
	case "caption", "figcaption":
		w.block(`<p align="center"><i>`, "</i></p>", n)
	case "pre":
		w.pre++
		w.wrap(`<pre style="white-space:pre-wrap">`, "</pre>", n)
		w.pre--
	case "kbd", "samp", "var":
		w.wrap("<code>", "</code>", n)
	case "b", "strong":
		w.wrap("<b>", "</b>", n)
	case "i", "em", "cite", "var2":
		w.wrap("<i>", "</i>", n)
	case "u", "ins":
		w.wrap("<u>", "</u>", n)
	case "s", "del":
		w.wrap("<s>", "</s>", n)
	case "q":
		w.wrap("“", "”", n)
	case "a":
		href := w.linkTarget(attr(n, "href"))
		prev := w.href
		switch {
		case href == "":
			w.writeChildren(n)
		case containsBlock(n):
			// Qt can't put blocks inside a link: link the text and
			// pictures inside instead.
			w.href = href
			w.writeChildren(n)
		default:
			w.href = ""
			w.wrap(w.linkOpen(href), "</a>", n)
		}
		w.href = prev
	case "img":
		src := w.imageSource(n)
		if r, ok := w.images[src]; ok {
			iw, ih := r.w, r.h
			if w.maxW > 0 {
				avail := w.maxW - w.indent - 10
				if w.cells > 0 {
					avail /= 2
				}
				if avail < 120 {
					avail = 120
				}
				if iw > avail {
					ih = ih * avail / iw
					iw = avail
				}
			}
			if w.href != "" {
				w.b.WriteString(`<p align="center">` + w.linkOpen(w.href) + imgTag(r.path, iw, ih) + `</a></p>`)
			} else {
				w.b.WriteString(`<p align="center">` + imgTag(r.path, iw, ih) + `</p>`)
			}
		}
	case "div", "section", "article", "main", "header", "footer", "figure", "picture":
		if hasBlockChild(n) {
			w.writeChildren(n)
		} else {
			w.block(`<p style="line-height:125%">`, "</p>", n)
		}
	case "source":
	default:
		w.writeChildren(n)
	}
}

func hasBlockChild(n *html.Node) bool {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type != html.ElementNode {
			continue
		}
		switch c.Data {
		case "p", "div", "section", "article", "main", "header", "footer", "figure", "h1", "h2", "h3",
			"h4", "h5", "h6", "ul", "ol", "dl", "table", "blockquote", "pre", "hr", "img", "picture", "figcaption":
			return true
		}
	}
	return false
}

// linkTarget makes a link absolute; links to a place on the same page
// become "#fragment".
func (w *qtWriter) linkTarget(href string) string {
	href = strings.TrimSpace(href)
	if href == "" {
		return ""
	}
	u, err := w.base.Parse(href)
	if err != nil {
		return ""
	}
	switch u.Scheme {
	case "http", "https":
	default:
		return ""
	}
	if u.Fragment != "" && sameDocument(u, w.base) {
		return "#" + u.Fragment
	}
	return u.String()
}

func sameDocument(a, b *url.URL) bool {
	return a.Scheme == b.Scheme && strings.EqualFold(a.Host, b.Host) && a.Path == b.Path && a.RawQuery == b.RawQuery
}

func imgTag(path string, w, h int) string {
	return fmt.Sprintf(`<img src="%s" width="%d" height="%d"/>`, template.HTMLEscapeString((&url.URL{Scheme: "file", Path: path}).String()), w, h)
}

// navLinks collects the links of the site's menus (<nav>, role=navigation,
// <header>), in page order, without duplicates.
func navLinks(doc *html.Node, base *url.URL) []Link {
	out := []Link{}
	seen := map[string]bool{}
	var collect func(n *html.Node)
	collect = func(n *html.Node) {
		walk(n, func(m *html.Node) bool {
			if len(out) >= maxNavLinks {
				return false
			}
			if m.Type != html.ElementNode || m.Data != "a" {
				return true
			}
			t := collapseSpace(stripUnsupported(textOf(m)))
			if t == "" {
				t = collapseSpace(attr(m, "aria-label"))
			}
			if t == "" {
				t = collapseSpace(attr(m, "title"))
			}
			u, err := base.Parse(strings.TrimSpace(attr(m, "href")))
			if t == "" || err != nil || (u.Scheme != "http" && u.Scheme != "https") {
				return false
			}
			if sameDocument(u, base) {
				return false
			}
			u.Fragment = ""
			key := u.String()
			if seen[key] {
				return false
			}
			seen[key] = true
			if len([]rune(t)) > 80 {
				t = string([]rune(t)[:79]) + "…"
			}
			out = append(out, Link{Text: t, URL: key})
			return false
		})
	}
	walk(doc, func(n *html.Node) bool {
		if n.Type != html.ElementNode {
			return true
		}
		if n.Data == "script" || n.Data == "style" || n.Data == "template" {
			return false
		}
		if n.Data == "nav" || n.Data == "header" || attr(n, "role") == "navigation" {
			collect(n)
			return false
		}
		return true
	})
	return out
}

// unwrapHeadings replaces wrappers that hold a heading and little else
// (Wikipedia: <div class="mw-heading"><h2>..</h2><span>[edit]</span></div>)
// by the bare heading. Readability otherwise drops such wrappers for their
// high link density, and the article loses its section headings.
func unwrapHeadings(doc *html.Node) {
	var wrappers []*html.Node
	walk(doc, func(n *html.Node) bool {
		if n.Type == html.ElementNode && n.Data == "div" && n.Parent != nil {
			var h *html.Node
			count := 0
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				if c.Type == html.ElementNode && len(c.Data) == 2 && c.Data[0] == 'h' && c.Data[1] >= '1' && c.Data[1] <= '6' {
					h = c
					count++
				}
			}
			if count == 1 && len(collapseSpace(textOf(n)))-len(collapseSpace(textOf(h))) <= 12 {
				wrappers = append(wrappers, n)
				return false
			}
		}
		return true
	})
	for _, w := range wrappers {
		for c := w.FirstChild; c != nil; c = c.NextSibling {
			if c.Type == html.ElementNode && len(c.Data) == 2 && c.Data[0] == 'h' {
				w.RemoveChild(c)
				w.Parent.InsertBefore(c, w)
				w.Parent.RemoveChild(w)
				break
			}
		}
	}
}

// junkRe matches class names and ids of page clutter: pop-ups, sign-up
// and cookie banners, menus, sharing buttons, ads. Used only for pages
// without an article (Readability has its own, finer rules).
var junkRe = regexp.MustCompile(`(?i)(^|[\s_-])(modal|popup|pop-up|newsletter|subscribe|subscription|signup|sign-up|cookies?|consent|gdpr|banner|promo|advert|advertisement|ads?|adslot|sponsored|sidebar|menu|navbar|nav|navigation|social|share|sharing|login|signin|account|skip|paywall|overlay|drawer|offcanvas|masthead|breadcrumbs?|toolbar|copyright|legal|tip|tips)([\s_-]|$)`)

var hiddenStyleRe = regexp.MustCompile(`(?i)display\s*:\s*none|visibility\s*:\s*hidden`)

// simplifiedRoot returns the part of a page without an article that is
// worth showing: its main area if it has one with enough text, else the
// body; with scripts, forms, menus, hidden parts and clutter removed.
func simplifiedRoot(doc *html.Node) *html.Node {
	body := findFirst(doc, func(n *html.Node) bool { return n.Type == html.ElementNode && n.Data == "body" })
	if body == nil {
		body = doc
	}
	clean := func(n *html.Node) {
		removeElements(n, "script", "style", "noscript", "template", "svg", "canvas", "iframe", "frame",
			"object", "embed", "video", "audio", "form", "button", "input", "select", "textarea", "nav",
			"footer", "aside", "dialog")
		removeMatching(n, func(m *html.Node) bool {
			switch attr(m, "role") {
			case "navigation", "banner", "contentinfo", "dialog", "alertdialog", "search", "complementary":
				return true
			}
			if attr(m, "aria-modal") == "true" || hiddenStyleRe.MatchString(attr(m, "style")) {
				return true
			}
			if m.Data == "body" || m.Data == "main" || m.Data == "article" {
				return false
			}
			return junkRe.MatchString(attr(m, "class")) || junkRe.MatchString(attr(m, "id"))
		})
	}
	main := findFirst(body, func(n *html.Node) bool {
		if n.Type != html.ElementNode {
			return false
		}
		id := strings.ToLower(attr(n, "id"))
		return n.Data == "main" || attr(n, "role") == "main" || id == "main" || id == "main-content" || id == "content"
	})
	if main != nil {
		clean(main)
		if len(collapseSpace(textOf(main))) >= minArticleChars {
			return main
		}
	}
	clean(body)
	return body
}

func removeMatching(n *html.Node, pred func(*html.Node) bool) {
	for c := n.FirstChild; c != nil; {
		next := c.NextSibling
		if c.Type == html.ElementNode && pred(c) {
			n.RemoveChild(c)
		} else {
			removeMatching(c, pred)
		}
		c = next
	}
}

func removeElements(n *html.Node, tags ...string) {
	drop := map[string]bool{}
	for _, t := range tags {
		drop[t] = true
	}
	var rec func(*html.Node)
	rec = func(p *html.Node) {
		for c := p.FirstChild; c != nil; {
			next := c.NextSibling
			if c.Type == html.CommentNode || (c.Type == html.ElementNode && (drop[c.Data] || attr(c, "hidden") != "" || attr(c, "aria-hidden") == "true")) {
				p.RemoveChild(c)
			} else {
				rec(c)
			}
			c = next
		}
	}
	rec(n)
}

// stripUnsupported removes characters the tablet cannot draw (it only has
// Noto Sans, Noto CJK and EB Garamond: no emoji) and invisible controls.
func stripUnsupported(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\n' || r == '\t':
			return r
		case r >= 0x1F000 && r <= 0x1FAFF, r >= 0x2600 && r <= 0x27BF && r != 0x2713 && r != 0x2717,
			r == 0xFE0F, r == 0xFE0E, r == 0x200D, r >= 0xE000 && r <= 0xF8FF:
			return -1
		case unicode.IsControl(r):
			return -1
		}
		return r
	}, s)
}

func lastPathPart(u *url.URL) string {
	p := strings.TrimSuffix(u.Path, "/")
	if i := strings.LastIndex(p, "/"); i >= 0 && i < len(p)-1 {
		return p[i+1:]
	}
	return u.Hostname()
}
