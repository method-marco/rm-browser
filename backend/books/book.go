package main

// An open book: the parsed EPUB, the number of visible characters of every
// chapter (for percentages), the table of contents with positions, and
// conversions between the reader's positions (chapter + visible
// character) and KOReader positions.

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
)

type tocItem struct {
	Title   string `json:"title"`
	Level   int    `json:"level"`
	Chapter int    `json:"chapter"`
	Char    int    `json:"char"`
}

type book struct {
	id     string
	title  string
	author string
	file   *os.File
	ep     *epubBook
	digest string // KOReader's partial MD5: the book's id on the sync server

	counts   []int // visible characters per chapter
	pictures []int // pictures per chapter
	prefix   []int // visible characters before each chapter
	total    int
	anchors  []map[string]int
	toc      []tocItem

	dry   map[int]*chapter // counting renders (positions only), a few kept
	order []int
}

func loadBook(lib *library, id string) (*book, error) {
	if !validID(id) {
		return nil, fmt.Errorf("unknown book")
	}
	f, err := lib.root.Open(id + ".epub")
	if err != nil {
		return nil, fmt.Errorf("the book's file is missing")
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	ep, err := openEPUB(f, st.Size())
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("this EPUB can't be read (%v)", err)
	}
	b := &book{id: id, file: f, ep: ep, digest: partialMD5(f), dry: map[int]*chapter{}, title: lib.name(id), author: ep.author}
	if b.title == "" {
		b.title = ep.title
	}
	n := len(ep.frags)
	b.counts, b.pictures, b.prefix, b.anchors = make([]int, n), make([]int, n), make([]int, n), make([]map[string]int, n)
	for i := 0; i < n; i++ {
		c := ep.renderChapter(i, nil, 0)
		b.counts[i], b.pictures[i], b.anchors[i] = len(c.pos), c.pictures, c.anchors
		b.prefix[i] = b.total
		b.total += len(c.pos)
	}
	for _, e := range ep.toc {
		if e.frag < 1 || e.frag > n || e.title == "" {
			continue
		}
		ch := e.frag - 1
		b.toc = append(b.toc, tocItem{Title: e.title, Level: max(1, e.level), Chapter: ch, Char: b.anchors[ch][e.anchor]})
	}
	return b, nil
}

func (b *book) close() {
	if b != nil && b.file != nil {
		b.file.Close()
	}
}

func (b *book) chapters() int { return len(b.counts) }

// empty: nothing to show (no text, no picture).
func (b *book) empty(ch int) bool { return b.counts[ch] == 0 && b.pictures[ch] == 0 }

// nextShown: the first chapter from ch on (step +1 or -1) that shows
// something, or -1.
func (b *book) nextShown(ch, step int) int {
	for ; ch >= 0 && ch < b.chapters(); ch += step {
		if !b.empty(ch) {
			return ch
		}
	}
	return -1
}

func (b *book) dryChapter(ch int) *chapter {
	if c, ok := b.dry[ch]; ok {
		return c
	}
	c := b.ep.renderChapter(ch, nil, 0)
	b.dry[ch] = c
	b.order = append(b.order, ch)
	if len(b.order) > 4 {
		delete(b.dry, b.order[0])
		b.order = b.order[1:]
	}
	return c
}

// imageBook: books with (almost) no text, e.g. comics: percentages by
// chapter instead of by text.
func (b *book) imageBook() bool { return b.total < 50*b.chapters() }

// percent of the book read at (chapter, visible character).
func (b *book) percent(ch, k int) float64 {
	if ch < 0 || b.chapters() == 0 {
		return 0
	}
	if b.imageBook() {
		frac := 0.0
		if b.counts[ch] > 0 {
			frac = float64(min(k, b.counts[ch])) / float64(b.counts[ch])
		}
		return (float64(ch) + frac) / float64(b.chapters())
	}
	return float64(b.prefix[ch]+min(k, b.counts[ch])) / float64(b.total)
}

// fromPercent: the position at a share of the book (apps that sync only a
// percentage).
func (b *book) fromPercent(p float64) (int, int) {
	p = max(0, min(1, p))
	if b.imageBook() {
		ch := min(b.chapters()-1, int(p*float64(b.chapters())))
		return ch, 0
	}
	target := int(p * float64(b.total))
	for ch := b.chapters() - 1; ch >= 0; ch-- {
		if b.prefix[ch] <= target && b.counts[ch] > 0 {
			return ch, min(target-b.prefix[ch], b.counts[ch]-1)
		}
	}
	return 0, 0
}

// xpointer: the KOReader position of (chapter, visible character).
func (b *book) xpointer(ch, k int) string {
	c := b.dryChapter(ch)
	if len(c.pos) == 0 {
		return fmt.Sprintf("/body/DocFragment[%d]/body", ch+1)
	}
	p := c.pos[max(0, min(k, len(c.pos)-1))]
	return b.ep.xpointerFor(b.ep.texts[p.seq], int(p.off))
}

var fragRe = regexp.MustCompile(`^/body/DocFragment\[(\d+)\]`)

// fromXPointer: the reader's position for a KOReader position.
func (b *book) fromXPointer(xp string) (int, int, bool) {
	m := fragRe.FindStringSubmatch(xp)
	if m == nil {
		return 0, 0, false
	}
	frag, _ := strconv.Atoi(m[1])
	if frag < 1 || frag > b.chapters() {
		return 0, 0, false
	}
	t, off, err := b.ep.resolve(xp)
	if err != nil || b.ep.fragIndex(t) != frag-1 {
		return frag - 1, 0, true // e.g. a picture page: its chapter's start
	}
	c := b.dryChapter(frag - 1)
	// the first visible character written at or after (t, off)
	lo, hi := 0, len(c.pos)
	for lo < hi {
		mid := (lo + hi) / 2
		p := c.pos[mid]
		if int(p.seq) < t.seq || (int(p.seq) == t.seq && int(p.off) < off) {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return frag - 1, min(lo, max(0, len(c.pos)-1)), true
}

// anchorChar: the visible character an element id of a chapter starts at.
func (b *book) anchorChar(ch int, anchor string) int {
	if ch < 0 || ch >= b.chapters() || anchor == "" {
		return 0
	}
	return b.anchors[ch][anchor]
}
