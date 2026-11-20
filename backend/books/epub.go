package main

// EPUB reading with KOReader (crengine) compatible positions
// ("xpointers"), adapted from kosync-bridge (remarkable-mods,
// kosync-native-reader/bridge/epub.go): the DOM is built the way crengine
// builds it, so positions written here point at the same text in KOReader
// and every other kosync app.
//
// crengine's DOM for an EPUB: /body/DocFragment[i]/body/... where DocFragment i is
// the i-th spine item and the inner body is that file's <body>. Path steps are
// element names, with [n] (1-based, among siblings of the same name) only when
// there is more than one such sibling; text nodes are text() / text()[n]; the
// final .k is the character offset in that text node.

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/url"
	"path"
	"strconv"
	"strings"
	"unicode"
)

type domNode struct {
	name     string            // element name (lower case); "" for text nodes
	id       string            // id attribute (link targets, table of contents)
	attrs    map[string]string // the few attributes the reader uses (see keepAttr)
	text     []rune            // text nodes only
	seq      int               // text nodes: index in reading order (epubBook.texts)
	children []*domNode
	parent   *domNode
}

func (n *domNode) isText() bool { return n.name == "" }

func (n *domNode) attr(k string) string {
	if n.attrs == nil {
		return ""
	}
	return n.attrs[k]
}

// keepAttr: attributes the reader needs (local names, lower case).
var keepAttr = map[string]bool{"class": true, "rel": true, "href": true, "src": true, "alt": true, "align": true, "style": true,
	"colspan": true, "rowspan": true, "start": true, "type": true, "width": true, "height": true}

const maxEPUBFile = 64 << 20 // one file inside the EPUB (a chapter or a picture)

type epubBook struct {
	zip       *zip.Reader
	files     map[string]*zip.File
	title     string
	author    string
	lang      string     // the book's language (dc:language), for hyphenation
	frags     []*domNode // body element of each spine item (nil if missing)
	fragPaths []string   // path of each spine item in the EPUB
	fragOf    map[string]int
	toc       []tocEntry // the EPUB's own table of contents, in reading order
	texts     []*domNode // all text nodes in reading order
	fragCSS   [][]string // each chapter's stylesheets (see headCSS)
	sheets    map[int]styleSheet
	cssFiles  map[string]styleSheet
}

// blockish elements: whitespace-only text directly inside them is not content
// (crengine drops it while building the DOM).
var blockElems = map[string]bool{
	"html": true, "body": true, "div": true, "section": true, "article": true, "aside": true,
	"header": true, "footer": true, "nav": true, "main": true, "figure": true, "blockquote": true,
	"ul": true, "ol": true, "li": true, "dl": true, "dt": true, "dd": true, "table": true,
	"thead": true, "tbody": true, "tfoot": true, "tr": true, "td": true, "th": true,
	"p": true, "h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true,
	"hgroup": true, "figcaption": true, "pre": false, "address": true, "center": true,
	"caption": true, "colgroup": true, "col": true, "head": true,
}

func isSpaceOnly(r []rune) bool {
	for _, c := range r {
		if !unicode.IsSpace(c) || c == 0xA0 {
			return false
		}
	}
	return true
}

func parseXHTML(data []byte) (*domNode, error) {
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.Strict = false
	dec.AutoClose = xml.HTMLAutoClose
	dec.Entity = xml.HTMLEntity
	root := &domNode{name: "#document"}
	cur := root
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return root, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			n := &domNode{name: strings.ToLower(t.Name.Local), parent: cur}
			for _, a := range t.Attr {
				k := strings.ToLower(a.Name.Local)
				if k == "id" {
					n.id = a.Value
				} else if keepAttr[k] {
					if n.attrs == nil {
						n.attrs = map[string]string{}
					}
					n.attrs[k] = a.Value
				}
			}
			cur.children = append(cur.children, n)
			cur = n
		case xml.EndElement:
			name := strings.ToLower(t.Name.Local)
			for p := cur; p != nil && p != root; p = p.parent {
				if p.name == name {
					cur = p.parent
					break
				}
			}
		case xml.CharData:
			txt := []rune(string(t))
			if k := len(cur.children); k > 0 && cur.children[k-1].isText() {
				cur.children[k-1].text = append(cur.children[k-1].text, txt...)
			} else {
				cur.children = append(cur.children, &domNode{text: txt, parent: cur})
			}
		}
	}
	cleanDOM(root)
	return root, nil
}

// cleanDOM applies crengine's text normalization: whitespace-only text inside a
// block element is dropped when it only separates block-level content (or sits at
// the edge of the block); between inline elements it is a real space and is kept.
// In remaining text, runs of whitespace (incl. new lines) become a single space.
// (Checked against crengine on real books, see the verify mode.)
func cleanDOM(n *domNode) {
	isBlock := func(c *domNode) bool { return c == nil || (!c.isText() && blockElems[c.name]) }
	var kept []*domNode
	for i, c := range n.children {
		if c.isText() {
			if isSpaceOnly(c.text) && blockElems[n.name] {
				var prev, next *domNode
				if i > 0 {
					prev = n.children[i-1]
				}
				if i+1 < len(n.children) {
					next = n.children[i+1]
				}
				if isBlock(prev) || isBlock(next) {
					continue
				}
			}
			c.text = collapseSpace(c.text)
			if len(c.text) == 0 {
				continue
			}
		} else {
			cleanDOM(c)
		}
		kept = append(kept, c)
	}
	n.children = kept
}

func collapseSpace(r []rune) []rune {
	out := make([]rune, 0, len(r))
	prevSpace := false
	for _, c := range r {
		if unicode.IsSpace(c) && c != 0xA0 {
			if prevSpace {
				continue
			}
			prevSpace = true
			out = append(out, ' ')
			continue
		}
		prevSpace = false
		out = append(out, c)
	}
	return out
}

func findElem(n *domNode, name string) *domNode {
	if n.name == name {
		return n
	}
	for _, c := range n.children {
		if !c.isText() {
			if f := findElem(c, name); f != nil {
				return f
			}
		}
	}
	return nil
}

type opfPackage struct {
	Metadata struct {
		Titles   []string `xml:"title"`
		Creators  []string `xml:"creator"`
		Languages []string `xml:"language"`
	} `xml:"metadata"`
	Manifest []struct {
		ID         string `xml:"id,attr"`
		Href       string `xml:"href,attr"`
		MediaType  string `xml:"media-type,attr"`
		Properties string `xml:"properties,attr"`
	} `xml:"manifest>item"`
	Spine struct {
		Toc      string `xml:"toc,attr"`
		Itemrefs []struct {
			IDRef string `xml:"idref,attr"`
		} `xml:"itemref"`
	} `xml:"spine"`
}

// tocEntry: one chapter of the EPUB's table of contents.
type tocEntry struct {
	title  string
	frag   int    // spine item (1-based, = DocFragment index); 0 if unknown
	anchor string // element id within that file, "" for the file start
	level  int    // nesting depth, 1 = top
}

func (b *epubBook) read(name string) ([]byte, error) {
	f, ok := b.files[name]
	if !ok {
		return nil, fmt.Errorf("missing %s", name)
	}
	if f.UncompressedSize64 > maxEPUBFile {
		return nil, fmt.Errorf("%s is too large", name)
	}
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(io.LimitReader(rc, maxEPUBFile))
}

func newEPUB(r io.ReaderAt, size int64) (*epubBook, opfPackage, string, error) {
	var pkg opfPackage
	zr, err := zip.NewReader(r, size)
	if err != nil {
		return nil, pkg, "", err
	}
	b := &epubBook{zip: zr, files: map[string]*zip.File{}, fragOf: map[string]int{}}
	for _, f := range zr.File {
		b.files[f.Name] = f
	}
	container, err := b.read("META-INF/container.xml")
	if err != nil {
		return nil, pkg, "", err
	}
	var c struct {
		Rootfiles []struct {
			FullPath string `xml:"full-path,attr"`
		} `xml:"rootfiles>rootfile"`
	}
	if err := xml.Unmarshal(container, &c); err != nil || len(c.Rootfiles) == 0 {
		return nil, pkg, "", errors.New("no rootfile in container.xml")
	}
	opfPath := c.Rootfiles[0].FullPath
	opfData, err := b.read(opfPath)
	if err != nil {
		return nil, pkg, "", err
	}
	dec := xml.NewDecoder(bytes.NewReader(opfData))
	dec.Strict = false
	if err := dec.Decode(&pkg); err != nil {
		return nil, pkg, "", fmt.Errorf("opf: %w", err)
	}
	if len(pkg.Metadata.Titles) > 0 {
		b.title = strings.Join(strings.Fields(pkg.Metadata.Titles[0]), " ")
	}
	if len(pkg.Metadata.Creators) > 0 {
		b.author = strings.Join(strings.Fields(pkg.Metadata.Creators[0]), " ")
	}
	if len(pkg.Metadata.Languages) > 0 {
		b.lang = strings.TrimSpace(pkg.Metadata.Languages[0])
	}
	return b, pkg, opfPath, nil
}

// readEPUBInfo reads only the title and author (the library list).
func readEPUBInfo(r io.ReaderAt, size int64) (title, author string, err error) {
	b, _, _, err := newEPUB(r, size)
	if err != nil {
		return "", "", err
	}
	return b.title, b.author, nil
}

// openEPUB reads the book: every spine item is parsed into crengine's DOM.
// r must stay open while the book is used (pictures are read later).
func openEPUB(r io.ReaderAt, size int64) (*epubBook, error) {
	b, pkg, opfPath, err := newEPUB(r, size)
	if err != nil {
		return nil, err
	}
	hrefs := map[string]string{}
	base := path.Dir(opfPath)
	for _, it := range pkg.Manifest {
		h, _ := url.PathUnescape(it.Href)
		if base != "." {
			h = path.Join(base, h)
		}
		hrefs[it.ID] = path.Clean(h)
	}
	for i, it := range pkg.Spine.Itemrefs {
		if _, seen := b.fragOf[hrefs[it.IDRef]]; !seen {
			b.fragOf[hrefs[it.IDRef]] = i + 1
		}
	}
	for _, it := range pkg.Spine.Itemrefs {
		var body *domNode
		var css []string
		if data, err := b.read(hrefs[it.IDRef]); err == nil {
			if doc, err := parseXHTML(data); err == nil || doc != nil {
				body = findElem(doc, "body")
				css = headCSS(doc, hrefs[it.IDRef])
			}
		}
		b.frags = append(b.frags, body)
		b.fragCSS = append(b.fragCSS, css)
		b.fragPaths = append(b.fragPaths, hrefs[it.IDRef])
		if body == nil {
			continue
		}
		var walk func(n *domNode)
		walk = func(n *domNode) {
			for _, ch := range n.children {
				if ch.isText() {
					ch.seq = len(b.texts)
					b.texts = append(b.texts, ch)
				} else {
					walk(ch)
				}
			}
		}
		walk(body)
	}
	if len(b.frags) == 0 {
		return nil, errors.New("the book has no chapters")
	}
	b.toc = readTOC(pkg, hrefs, b.fragOf, b.read)
	return b, nil
}

// fragIndex returns the spine item (0-based) that holds n, or -1.
func (b *epubBook) fragIndex(n *domNode) int {
	for n != nil && n.name != "body" {
		n = n.parent
	}
	for i, f := range b.frags {
		if f != nil && f == n {
			return i
		}
	}
	return -1
}

// target resolves a link inside the book (relative to the spine item
// fromPath) to a spine item (0-based) and anchor; ok=false if it leads
// outside the book.
func (b *epubBook) target(fromPath, href string) (int, string, bool) {
	h, anchor, _ := strings.Cut(href, "#")
	if h == "" {
		h = fromPath
	} else {
		h, _ = url.PathUnescape(h)
		h = path.Join(path.Dir(fromPath), h)
	}
	f, ok := b.fragOf[path.Clean(h)]
	if !ok {
		return 0, "", false
	}
	return f - 1, anchor, true
}

// ---- xpointers ----------------------------------------------------------------

// xpointerFor builds the crengine position of rune offset off in text node t.
func (b *epubBook) xpointerFor(t *domNode, off int) string {
	var steps []string
	// text step
	same, idx := 0, 0
	for _, s := range t.parent.children {
		if s.isText() {
			same++
			if s == t {
				idx = same
			}
		}
	}
	if same > 1 {
		steps = append(steps, fmt.Sprintf("text()[%d].%d", idx, off))
	} else {
		steps = append(steps, fmt.Sprintf("text().%d", off))
	}
	n := t.parent
	frag := 0
	for n != nil && n.name != "body" {
		same, idx = 0, 0
		for _, s := range n.parent.children {
			if s.name == n.name {
				same++
				if s == n {
					idx = same
				}
			}
		}
		if same > 1 {
			steps = append(steps, fmt.Sprintf("%s[%d]", n.name, idx))
		} else {
			steps = append(steps, n.name)
		}
		n = n.parent
	}
	for i, f := range b.frags {
		if f == n {
			frag = i + 1
		}
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "/body/DocFragment[%d]/body", frag)
	for i := len(steps) - 1; i >= 0; i-- {
		sb.WriteString("/")
		sb.WriteString(steps[i])
	}
	return sb.String()
}

// resolve finds the text node and rune offset for a crengine xpointer. For an
// element xpointer (no text step) it returns the element's first text node.
func (b *epubBook) resolve(xp string) (*domNode, int, error) {
	if !strings.HasPrefix(xp, "/body/DocFragment[") {
		return nil, 0, errors.New("not a KOReader position")
	}
	rest := strings.TrimPrefix(xp, "/body/DocFragment[")
	end := strings.Index(rest, "]")
	if end < 0 {
		return nil, 0, errors.New("bad xpointer")
	}
	fi, err := strconv.Atoi(rest[:end])
	if err != nil || fi < 1 || fi > len(b.frags) || b.frags[fi-1] == nil {
		return nil, 0, errors.New("no such DocFragment")
	}
	rest = strings.TrimPrefix(rest[end+1:], "/body")
	n := b.frags[fi-1]
	off := 0
	for _, step := range strings.Split(strings.Trim(rest, "/"), "/") {
		if step == "" {
			continue
		}
		name, idx := step, 1
		if dot := strings.LastIndex(step, "."); dot >= 0 && !strings.Contains(step[dot:], "]") {
			off, _ = strconv.Atoi(step[dot+1:])
			name = step[:dot]
		}
		if lb := strings.Index(name, "["); lb >= 0 && strings.HasSuffix(name, "]") {
			idx, _ = strconv.Atoi(name[lb+1 : len(name)-1])
			name = name[:lb]
		}
		var next *domNode
		count := 0
		for _, c := range n.children {
			if (name == "text()" && c.isText()) || (name != "text()" && c.name == name) {
				count++
				if count == idx {
					next = c
					break
				}
			}
		}
		if next == nil {
			var kids []string
			for _, c := range n.children {
				if c.isText() {
					kids = append(kids, fmt.Sprintf("text(%q)", string(c.text[:min(len(c.text), 25)])))
				} else {
					kids = append(kids, c.name)
				}
			}
			return nil, 0, fmt.Errorf("step %q not found in %s: %s", step, n.name, strings.Join(kids, ", "))
		}
		n = next
	}
	if !n.isText() {
		// element position (e.g. an image): the first text at or after it
		t := firstText(n)
		if t == nil {
			t = b.textAfter(n)
		}
		if t == nil {
			return nil, 0, errors.New("no text at position")
		}
		return t, 0, nil
	}
	if off > len(n.text) {
		off = len(n.text)
	}
	return n, off, nil
}

// textAfter returns the first text node following element n in reading order.
func (b *epubBook) textAfter(n *domNode) *domNode {
	for p := n; p != nil && p.parent != nil; p = p.parent {
		seen := false
		for _, c := range p.parent.children {
			if c == p {
				seen = true
				continue
			}
			if !seen {
				continue
			}
			if c.isText() {
				return c
			}
			if t := firstText(c); t != nil {
				return t
			}
		}
	}
	return nil
}

func firstText(n *domNode) *domNode {
	for _, c := range n.children {
		if c.isText() {
			return c
		}
		if t := firstText(c); t != nil {
			return t
		}
	}
	return nil
}

// ---- table of contents ------------------------------------------------------------

// readTOC reads the EPUB 3 nav document (or the EPUB 2 NCX) into chapters in
// reading order, each pointing at a spine item and optional anchor.
func readTOC(pkg opfPackage, hrefs map[string]string, fragOf map[string]int, read func(string) ([]byte, error)) []tocEntry {
	target := func(base, href string) (int, string) {
		h, frag, _ := strings.Cut(href, "#")
		h, _ = url.PathUnescape(h)
		if base != "." && base != "" {
			h = path.Join(base, h)
		}
		return fragOf[path.Clean(h)], frag
	}
	navPath, ncxPath := "", ""
	for _, it := range pkg.Manifest {
		if strings.Contains(" "+it.Properties+" ", " nav ") {
			navPath = hrefs[it.ID]
		}
		if it.MediaType == "application/x-dtbncx+xml" || it.ID == pkg.Spine.Toc {
			ncxPath = hrefs[it.ID]
		}
	}
	var out []tocEntry
	if navPath != "" {
		if data, err := read(navPath); err == nil {
			out = navEntries(data, path.Dir(navPath), target)
		}
	}
	if len(out) == 0 && ncxPath != "" {
		if data, err := read(ncxPath); err == nil {
			out = ncxEntries(data, path.Dir(ncxPath), target)
		}
	}
	return out
}

// navEntries: <a href> links inside the <nav epub:type="toc"> (or the first <nav>).
func navEntries(data []byte, base string, target func(string, string) (int, string)) []tocEntry {
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.Strict, dec.AutoClose, dec.Entity = false, xml.HTMLAutoClose, xml.HTMLEntity
	var out []tocEntry
	navDepth, inToc, sawToc, olDepth := 0, false, false, 0
	var cur *tocEntry
	var title strings.Builder
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			name := strings.ToLower(t.Name.Local)
			if name == "nav" {
				navDepth++
				isToc := false
				for _, a := range t.Attr {
					if a.Name.Local == "type" && strings.Contains(a.Value, "toc") {
						isToc = true
					}
				}
				if navDepth == 1 && (isToc || !sawToc) {
					inToc, sawToc = true, sawToc || isToc
					if isToc {
						out = out[:0] // prefer the real toc nav over an earlier one
					}
				}
			}
			if (name == "ol" || name == "ul") && inToc {
				olDepth++
			}
			if name == "a" && inToc {
				for _, a := range t.Attr {
					if a.Name.Local == "href" {
						f, anchor := target(base, a.Value)
						cur = &tocEntry{frag: f, anchor: anchor, level: max(1, olDepth)}
						title.Reset()
					}
				}
			}
		case xml.CharData:
			if cur != nil {
				title.Write(t)
			}
		case xml.EndElement:
			name := strings.ToLower(t.Name.Local)
			if name == "a" && cur != nil {
				cur.title = strings.Join(strings.Fields(title.String()), " ")
				out = append(out, *cur)
				cur = nil
			}
			if (name == "ol" || name == "ul") && inToc && olDepth > 0 {
				olDepth--
			}
			if name == "nav" {
				navDepth--
				if navDepth == 0 {
					inToc = false
				}
			}
		}
	}
	return out
}

// ncxEntries: navPoints in document order (navLabel/text + content/@src).
func ncxEntries(data []byte, base string, target func(string, string) (int, string)) []tocEntry {
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.Strict = false
	var out []tocEntry
	var stack []int // indexes into out of the open navPoints
	inText := false
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch strings.ToLower(t.Name.Local) {
			case "navpoint":
				out = append(out, tocEntry{level: len(stack) + 1})
				stack = append(stack, len(out)-1)
			case "text":
				inText = len(stack) > 0
			case "content":
				if len(stack) > 0 {
					for _, a := range t.Attr {
						if a.Name.Local == "src" {
							e := &out[stack[len(stack)-1]]
							e.frag, e.anchor = target(base, a.Value)
						}
					}
				}
			}
		case xml.CharData:
			if inText && len(stack) > 0 {
				if e := &out[stack[len(stack)-1]]; e.title == "" {
					e.title = strings.Join(strings.Fields(string(t)), " ")
				}
			}
		case xml.EndElement:
			switch strings.ToLower(t.Name.Local) {
			case "text":
				inText = false
			case "navpoint":
				if len(stack) > 0 {
					stack = stack[:len(stack)-1]
				}
			}
		}
	}
	return out
}

// findID returns the element with the given id below n.
func findID(n *domNode, id string) *domNode {
	if n == nil {
		return nil
	}
	if n.id == id {
		return n
	}
	for _, c := range n.children {
		if !c.isText() {
			if f := findID(c, id); f != nil {
				return f
			}
		}
	}
	return nil
}
