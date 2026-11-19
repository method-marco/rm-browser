package main

// The book's own style ("Book's style" in the Font panel): the formatting a
// book sets in its CSS, as far as it makes sense on e-ink and Qt's rich
// text can show it. Read: the stylesheets a chapter links to and its
// <style> blocks, plus style attributes. Matched: simple selectors (tag,
// .class, #id and combinations; for descendant selectors the last part).
// Kept, within bounds:
//
//   - text-align, text-indent (inherited, as in CSS)
//   - margin-top / margin-bottom (paragraph spacing)
//   - font-style, font-weight, font-variant: small-caps, font-size
//     (relative to the reader's size), monospace font families
//
// Lengths are written in em; the frontend turns them into pixels for the
// text size (Qt's rich text ignores em). Everything else (colours,
// backgrounds, positions, the book's font files) is not used: the reader's
// typeface, size, line spacing and margins stay.

import (
	"fmt"
	"math"
	"path"
	"sort"
	"strconv"
	"strings"
)

// cssCompound: one part of a selector (tag, #id, .classes).
type cssCompound struct {
	tag     string // "" = any
	id      string
	classes []string
}

// cssSelector: compounds from the outermost to the element itself, joined
// by descendant (' ') or child ('>') combinators.
type cssSelector struct {
	parts []cssCompound
	combs []byte // combs[i] joins parts[i] and parts[i+1]
	spec  int    // specificity: ids*100 + classes*10 + tags
}

type cssRule struct {
	sel   cssSelector
	order int
	decls [][2]string // property, value (lower case)
}

type styleSheet []cssRule

const maxCSS = 1 << 20

// parseCSS reads the rules of a stylesheet (@media blocks and other
// at-rules are skipped).
func parseCSS(src string, order int) styleSheet {
	// comments out
	var b strings.Builder
	for {
		i := strings.Index(src, "/*")
		if i < 0 {
			b.WriteString(src)
			break
		}
		b.WriteString(src[:i])
		j := strings.Index(src[i+2:], "*/")
		if j < 0 {
			break
		}
		src = src[i+2+j+2:]
	}
	s := b.String()
	var out styleSheet
	for len(s) > 0 {
		s = strings.TrimSpace(s)
		if s == "" {
			break
		}
		if s[0] == '@' {
			// at-rule: up to ';' or a balanced {...} block
			semi, brace := strings.IndexByte(s, ';'), strings.IndexByte(s, '{')
			if brace < 0 || (semi >= 0 && semi < brace) {
				if semi < 0 {
					break
				}
				s = s[semi+1:]
				continue
			}
			depth, i := 0, brace
			for ; i < len(s); i++ {
				if s[i] == '{' {
					depth++
				} else if s[i] == '}' {
					depth--
					if depth == 0 {
						break
					}
				}
			}
			if i >= len(s) {
				break
			}
			s = s[i+1:]
			continue
		}
		open := strings.IndexByte(s, '{')
		if open < 0 {
			break
		}
		close := strings.IndexByte(s[open:], '}')
		if close < 0 {
			break
		}
		sels, body := s[:open], s[open+1:open+close]
		s = s[open+close+1:]
		var decls [][2]string
		for _, d := range strings.Split(body, ";") {
			k, v, ok := strings.Cut(d, ":")
			if !ok {
				continue
			}
			k = strings.ToLower(strings.TrimSpace(k))
			v = strings.ToLower(strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(v), "!important")))
			if k != "" && v != "" {
				decls = append(decls, [2]string{k, v})
			}
		}
		if len(decls) == 0 {
			continue
		}
		for _, sel := range strings.Split(sels, ",") {
			if cs, ok := parseSelector(sel); ok {
				out = append(out, cssRule{sel: cs, order: order, decls: decls})
				order++
			}
		}
	}
	return out
}

// parseSelector: a selector of tags, classes and ids joined by descendant
// or child combinators. Selectors with pseudo-classes, attributes or
// sibling combinators (+ ~) are not used.
func parseSelector(sel string) (cssSelector, bool) {
	sel = strings.TrimSpace(sel)
	if sel == "" || strings.ContainsAny(sel, ":[+~") {
		return cssSelector{}, false
	}
	sel = strings.ReplaceAll(sel, ">", " > ")
	var cs cssSelector
	pending := byte(0)
	for _, tok := range strings.Fields(sel) {
		if tok == ">" {
			if len(cs.parts) == 0 {
				return cssSelector{}, false
			}
			pending = '>'
			continue
		}
		var c cssCompound
		rest := tok
		for len(rest) > 0 {
			kind := byte(0)
			if rest[0] == '.' || rest[0] == '#' {
				kind, rest = rest[0], rest[1:]
			}
			end := strings.IndexAny(rest, ".#")
			if end < 0 {
				end = len(rest)
			}
			name := strings.ToLower(rest[:end])
			rest = rest[end:]
			switch kind {
			case '.':
				c.classes = append(c.classes, name)
				cs.spec += 10
			case '#':
				c.id = name
				cs.spec += 100
			default:
				if name != "*" && name != "" {
					c.tag = name
					cs.spec++
				}
			}
		}
		if len(cs.parts) > 0 {
			if pending == 0 {
				pending = ' '
			}
			cs.combs = append(cs.combs, pending)
		}
		pending = 0
		cs.parts = append(cs.parts, c)
	}
	return cs, len(cs.parts) > 0 && pending == 0
}

func (c cssCompound) matches(n *domNode) bool {
	if n == nil || n.isText() {
		return false
	}
	if c.tag != "" && c.tag != n.name {
		return false
	}
	if c.id != "" && c.id != strings.ToLower(n.id) {
		return false
	}
	if len(c.classes) > 0 {
		have := strings.Fields(strings.ToLower(n.attr("class")))
		for _, want := range c.classes {
			found := false
			for _, h := range have {
				if h == want {
					found = true
					break
				}
			}
			if !found {
				return false
			}
		}
	}
	return true
}

func (cs cssSelector) matches(n *domNode) bool {
	return cs.matchFrom(len(cs.parts)-1, n)
}

// matchFrom: parts[i] matches n and the parts before it match its ancestors.
func (cs cssSelector) matchFrom(i int, n *domNode) bool {
	if !cs.parts[i].matches(n) {
		return false
	}
	if i == 0 {
		return true
	}
	if cs.combs[i-1] == '>' {
		return cs.matchFrom(i-1, n.parent)
	}
	for p := n.parent; p != nil; p = p.parent {
		if cs.matchFrom(i-1, p) {
			return true
		}
	}
	return false
}

// computed: the book's declarations for n (rules by specificity and
// order, then the style attribute).
func (sh styleSheet) computed(n *domNode) map[string]string {
	var hits []cssRule
	for _, r := range sh {
		if r.sel.matches(n) {
			hits = append(hits, r)
		}
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].sel.spec != hits[j].sel.spec {
			return hits[i].sel.spec < hits[j].sel.spec
		}
		return hits[i].order < hits[j].order
	})
	out := map[string]string{}
	set := func(k, v string) {
		if k == "margin" { // shorthand: top right bottom left
			f := strings.Fields(v)
			switch len(f) {
			case 1:
				out["margin-top"], out["margin-bottom"] = f[0], f[0]
			case 2, 3, 4:
				out["margin-top"] = f[0]
				out["margin-bottom"] = f[0]
				if len(f) >= 3 {
					out["margin-bottom"] = f[2]
				}
			}
			return
		}
		if k == "font" { // shorthand: only the keywords we use
			for _, w := range strings.Fields(v) {
				switch w {
				case "italic", "oblique":
					out["font-style"] = "italic"
				case "bold", "bolder", "600", "700", "800", "900":
					out["font-weight"] = "bold"
				case "small-caps":
					out["font-variant"] = "small-caps"
				}
			}
			return
		}
		out[k] = v
	}
	for _, r := range hits {
		for _, d := range r.decls {
			set(d[0], d[1])
		}
	}
	if st := n.attr("style"); st != "" {
		for _, d := range strings.Split(st, ";") {
			if k, v, ok := strings.Cut(d, ":"); ok {
				set(strings.ToLower(strings.TrimSpace(k)), strings.ToLower(strings.TrimSpace(v)))
			}
		}
	}
	return out
}

// emLength: a CSS length in em (px at 16 per em, pt at 12), ok=false for
// anything else (percentages of the page width, auto, ...).
func emLength(v string) (float64, bool) {
	v = strings.TrimSpace(v)
	if v == "0" {
		return 0, true
	}
	for unit, per := range map[string]float64{"rem": 1, "em": 1, "px": 1.0 / 16, "pt": 1.0 / 12} {
		if num, ok := strings.CutSuffix(v, unit); ok {
			if unit == "em" && strings.HasSuffix(num, "r") {
				continue // rem
			}
			f, err := strconv.ParseFloat(strings.TrimSpace(num), 64)
			if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
				return 0, false
			}
			return f * per, true
		}
	}
	return 0, false
}

// fontScale: a font-size relative to the surrounding text.
func fontScale(v string) (float64, bool) {
	switch v {
	case "xx-small":
		return 0.6, true
	case "x-small":
		return 0.75, true
	case "small", "smaller":
		return 0.85, true
	case "medium", "inherit", "initial":
		return 1, true
	case "large", "larger":
		return 1.2, true
	case "x-large":
		return 1.5, true
	case "xx-large":
		return 2, true
	}
	if p, ok := strings.CutSuffix(v, "%"); ok {
		f, err := strconv.ParseFloat(strings.TrimSpace(p), 64)
		if err != nil {
			return 0, false
		}
		return f / 100, true
	}
	if f, ok := emLength(v); ok && !strings.HasSuffix(v, "px") && !strings.HasSuffix(v, "pt") {
		return f, true
	}
	return 0, false
}

func clamp(x, lo, hi float64) float64 { return math.Max(lo, math.Min(hi, x)) }

func em(x float64) string { return strconv.FormatFloat(math.Round(x*100)/100, 'f', -1, 64) + "em" }

// charStyle: the character formatting the book sets on an element, as
// Qt CSS ("" if none).
func charStyle(c map[string]string) string {
	var parts []string
	switch c["font-style"] {
	case "italic", "oblique":
		parts = append(parts, "font-style:italic")
	case "normal":
		parts = append(parts, "font-style:normal")
	}
	switch c["font-weight"] {
	case "bold", "bolder", "600", "700", "800", "900":
		parts = append(parts, "font-weight:bold")
	case "normal", "lighter", "100", "200", "300", "400":
		parts = append(parts, "font-weight:normal")
	}
	if c["font-variant"] == "small-caps" {
		parts = append(parts, "font-variant:small-caps")
	}
	if v, ok := c["font-size"]; ok {
		if f, ok := fontScale(v); ok && math.Abs(f-1) > 0.01 {
			parts = append(parts, "font-size:"+em(clamp(f, 0.5, 2.5)))
		}
	}
	if ff := c["font-family"]; strings.Contains(ff, "mono") || strings.Contains(ff, "courier") {
		parts = append(parts, "font-family:'IBM Plex Mono'")
	}
	return strings.Join(parts, "; ")
}

// blockFormat is what a block takes from the book: alignment, first-line
// indent and spacing (with what it inherits).
type blockFormat struct {
	align   string  // left | right | center | justify | ""
	indent  float64 // em, -1 = not set
	spacing string  // margin-top/bottom as Qt CSS
	chars   string  // charStyle
}

func (w *chapterWriter) blockFormatOf(n *domNode) blockFormat {
	f := blockFormat{align: w.inAlign, indent: w.inIndent}
	if w.sheet == nil {
		return f
	}
	c := w.sheet.computed(n)
	switch c["text-align"] {
	case "left", "start":
		f.align = "left"
	case "right", "end":
		f.align = "right"
	case "center", "justify":
		f.align = c["text-align"]
	}
	if v, ok := c["text-indent"]; ok {
		if x, ok := emLength(v); ok {
			f.indent = clamp(x, 0, 5)
		}
	}
	var sp []string
	for _, k := range []string{"margin-top", "margin-bottom"} {
		if v, ok := c[k]; ok {
			if x, ok := emLength(v); ok {
				sp = append(sp, k+":"+em(clamp(x, 0, 4)))
			}
		}
	}
	f.spacing = strings.Join(sp, "; ")
	f.chars = charStyle(c)
	return f
}

// open writes the start tag of a block with the book's format.
func (f blockFormat) open(tag string) string {
	style := "line-height:125%"
	if f.indent >= 0 {
		style += "; text-indent:" + em(f.indent)
	}
	if f.spacing != "" {
		style += "; " + f.spacing
	}
	if f.chars != "" {
		style += "; " + f.chars
	}
	if f.align != "" {
		return fmt.Sprintf(`<%s align="%s" style="%s">`, tag, f.align, style)
	}
	return fmt.Sprintf(`<%s style="%s">`, tag, style)
}

// sheetFor: the book's rules for a chapter (its linked stylesheets and
// <style> blocks), cached per stylesheet.
func (b *epubBook) sheetFor(frag int) styleSheet {
	if b.sheets == nil {
		b.sheets = map[int]styleSheet{}
		b.cssFiles = map[string]styleSheet{}
	}
	if sh, ok := b.sheets[frag]; ok {
		return sh
	}
	var sh styleSheet
	order := 0
	for _, src := range b.fragCSS[frag] {
		if strings.HasPrefix(src, "inline:") {
			sh = append(sh, parseCSS(strings.TrimPrefix(src, "inline:"), order)...)
		} else {
			cs, ok := b.cssFiles[src]
			if !ok {
				if data, err := b.read(src); err == nil && len(data) <= maxCSS {
					cs = parseCSS(string(data), 0)
				}
				b.cssFiles[src] = cs
			}
			for _, r := range cs {
				r.order += order
				sh = append(sh, r)
			}
		}
		order += 100000
	}
	b.sheets[frag] = sh
	return sh
}

// headCSS lists a chapter's stylesheets in order: paths in the EPUB, or
// "inline:<css>" for <style> blocks.
func headCSS(doc *domNode, docPath string) []string {
	var out []string
	var walk func(n *domNode)
	walk = func(n *domNode) {
		for _, c := range n.children {
			if c.isText() {
				continue
			}
			switch c.name {
			case "link":
				if strings.Contains(strings.ToLower(c.attr("rel")), "stylesheet") && c.attr("href") != "" {
					h := strings.SplitN(c.attr("href"), "#", 2)[0]
					if !strings.Contains(h, "://") {
						out = append(out, path.Clean(path.Join(path.Dir(docPath), h)))
					}
				}
			case "style":
				var sb strings.Builder
				for _, t := range c.children {
					if t.isText() {
						sb.WriteString(string(t.text))
					}
				}
				out = append(out, "inline:"+sb.String())
			default:
				walk(c)
			}
		}
	}
	walk(doc)
	return out
}
