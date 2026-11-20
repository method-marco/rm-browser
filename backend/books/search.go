package main

import (
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// Search in the open book: case, accents and the kind of quotes and dashes
// are ignored ("cafe" finds "Café", "don't" finds "don’t"); words may be
// split over formatting ("<i>wo</i>rd").
// Matches are given as visible characters (the reader's positions).

const (
	maxSearchHits = 500
	snippetRunes  = 50 // around a match
)

type searchHit struct {
	Chapter int     `json:"chapter"`
	Char    int     `json:"char"` // first visible character of the match
	Len     int     `json:"len"`  // visible characters in it
	Before  string  `json:"before"`
	Match   string  `json:"match"`
	After   string  `json:"after"`
	Title   string  `json:"title"` // the table of contents' entry there
	Percent float64 `json:"percent"`
}

// searchText: a chapter's text for searching: folded runes, the visible
// character each comes from (-1: a space between words) and the original.
type searchText struct {
	norm []rune
	k    []int32
	orig []rune
}

// foldRune appends r as searched: lower case, without accents.
func foldRune(out []rune, r rune) []rune {
	if r < 0x80 {
		return append(out, unicode.ToLower(r))
	}
	switch r {
	case '‘', '’', 'ʼ', '′':
		return append(out, '\'')
	case '“', '”', '„', '″', '«', '»':
		return append(out, '"')
	case '‐', '‑', '‒', '–', '—':
		return append(out, '-')
	case 'ß', 'ẞ':
		return append(out, 's', 's')
	}
	for _, d := range norm.NFKD.String(string(r)) {
		if !unicode.Is(unicode.Mn, d) {
			out = append(out, unicode.ToLower(d))
		}
	}
	return out
}

func foldQuery(q string) []rune {
	var out []rune
	for _, f := range strings.Fields(q) {
		if len(out) > 0 {
			out = append(out, ' ')
		}
		for _, r := range f {
			out = foldRune(out, r)
		}
	}
	return out
}

// blockOf: the nearest block element around a text node.
func blockOf(n *domNode) *domNode {
	for p := n.parent; p != nil; p = p.parent {
		if blockElems[p.name] || p.name == "pre" {
			return p
		}
	}
	return nil
}

func hasSpace(rs []rune) bool {
	for _, r := range rs {
		if unicode.IsSpace(r) {
			return true
		}
	}
	return false
}

// spaceBetween: whether two visible characters are in different words.
func (bk *epubBook) spaceBetween(a, b textPos) bool {
	ta, tb := bk.texts[a.seq], bk.texts[b.seq]
	if a.seq == b.seq {
		return hasSpace(ta.text[a.off+1 : b.off])
	}
	if hasSpace(ta.text[a.off+1:]) || hasSpace(tb.text[:b.off]) {
		return true
	}
	for s := a.seq + 1; s < b.seq; s++ {
		if hasSpace(bk.texts[s].text) {
			return true
		}
	}
	return blockOf(ta) != blockOf(tb)
}

func (b *book) searchTextOf(ch int) *searchText {
	if b.searchTexts == nil {
		b.searchTexts = make([]*searchText, b.chapters())
	}
	if st := b.searchTexts[ch]; st != nil {
		return st
	}
	c := b.ep.renderChapter(ch, nil, 0, renderOptions{})
	st := &searchText{}
	for i, p := range c.pos {
		if i > 0 && b.ep.spaceBetween(c.pos[i-1], p) {
			st.norm, st.k, st.orig = append(st.norm, ' '), append(st.k, -1), append(st.orig, ' ')
		}
		r := b.ep.texts[p.seq].text[p.off]
		n := len(st.norm)
		st.norm = foldRune(st.norm, r)
		for j := n; j < len(st.norm); j++ {
			st.k, st.orig = append(st.k, int32(i)), append(st.orig, r)
		}
	}
	b.searchTexts[ch] = st
	return st
}

// text of entries [from, to) as written (a character folded into several
// runes once)
func (st *searchText) text(from, to int) string {
	var sb strings.Builder
	for i := max(0, from); i < min(to, len(st.orig)); i++ {
		if i > 0 && st.k[i] >= 0 && st.k[i] == st.k[i-1] {
			continue
		}
		sb.WriteRune(st.orig[i])
	}
	return sb.String()
}

// search returns the first matches of q (at most maxSearchHits) and how
// many there are in all.
func (b *book) search(q string) ([]searchHit, int) {
	query := foldQuery(q)
	hits := []searchHit{}
	if len(query) == 0 {
		return hits, 0
	}
	total := 0
	for ch := 0; ch < b.chapters(); ch++ {
		if b.counts[ch] == 0 {
			continue
		}
		st := b.searchTextOf(ch)
		n := len(st.norm)
	scan:
		for s := 0; s+len(query) <= n; s++ {
			for j, r := range query {
				if st.norm[s+j] != r {
					continue scan
				}
			}
			e := s + len(query) // match: entries [s, e)
			if st.k[s] < 0 || st.k[e-1] < 0 || (s > 0 && st.k[s-1] == st.k[s]) || (e < n && st.k[e] == st.k[e-1]) {
				continue // starts or ends at a space, or in the middle of a folded character
			}
			total++
			if len(hits) < maxSearchHits {
				k := int(st.k[s])
				before := st.text(s-snippetRunes, s)
				if i := strings.IndexByte(before, ' '); i >= 0 && s > snippetRunes {
					before = "…" + before[i:]
				}
				after := st.text(e, e+snippetRunes)
				if i := strings.LastIndexByte(after, ' '); i >= 0 && e+snippetRunes < n {
					after = after[:i] + " …"
				}
				hits = append(hits, searchHit{Chapter: ch, Char: k, Len: int(st.k[e-1]) - k + 1,
					Before: strings.TrimLeft(before, " "), Match: st.text(s, e), After: after,
					Title: b.titleAt(ch, k), Percent: b.percent(ch, k)})
			}
			s = e - 1
		}
	}
	return hits, total
}

// titleAt: the table of contents' entry at or before a position.
func (b *book) titleAt(ch, k int) string {
	t := ""
	for _, e := range b.toc {
		if e.Chapter < ch || (e.Chapter == ch && e.Char <= k) {
			t = e.Title
		}
	}
	return t
}
