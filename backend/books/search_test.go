package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/method-marco/rm-browser/backend/internal/store"
)

func TestSearch(t *testing.T) {
	lib := writeLibrary(t, t.TempDir())
	b, err := loadBook(lib, "book-1")
	if err != nil {
		t.Fatal(err)
	}
	defer b.close()
	// the visible characters of a match, from the book's text
	textAt := func(h searchHit) string {
		c := b.ep.renderChapter(h.Chapter, nil, 0, renderOptions{})
		s := ""
		for i := h.Char; i < h.Char+h.Len; i++ {
			s += string(b.ep.texts[c.pos[i].seq].text[c.pos[i].off])
		}
		return s
	}
	for _, tc := range []struct {
		q      string
		n      int
		match  string // as written in the book
		chars  string // its visible characters
		chap   int
		before string // the end of the text before it
	}{
		{"stormy", 1, "stormy", "stormy", 0, "It was a dark and "},
		{"DARK AND", 1, "dark and", "darkand", 0, "It was a "}, // over formatting, case
		{"ca va", 1, "Ça va", "Çava", 0, "Later, <nothing> happened. "},
		{"bucher", 1, "Bücher", "Bücher", 0, "Later, <nothing> happened. Ça va? 3 "},
		{"line", 2, "line", "line", 2, "First "},
		{"  first   line ", 1, "First line", "Firstline", 2, ""},
		{"line.second", 0, "", "", 0, ""}, // not across paragraphs
		{"nothing", 1, "nothing", "nothing", 0, "Later, <"},
		{"", 0, "", "", 0, ""},
		{"zebra", 0, "", "", 0, ""},
	} {
		hits, total := b.search(tc.q)
		if total != tc.n || len(hits) != tc.n {
			t.Errorf("%q: %d hits (%d in all), want %d", tc.q, len(hits), total, tc.n)
			continue
		}
		if tc.n == 0 {
			continue
		}
		h := hits[0]
		if h.Match != tc.match || textAt(h) != tc.chars || h.Chapter != tc.chap || !strings.HasSuffix(h.Before, tc.before) {
			t.Errorf("%q: %+v (text %q)", tc.q, h, textAt(h))
		}
	}
	hits, _ := b.search("line")
	if hits[0].Title != "Chapter Two" || hits[1].Char <= hits[0].Char || hits[0].Percent <= 0.5 {
		t.Errorf("second chapter: %+v", hits)
	}
	if h, _ := b.search("later"); len(h) != 1 || h[0].Title != "Later on" {
		t.Errorf("titles: %+v", h)
	}
	// a typographic apostrophe in the book, a plain one typed
	b.ep.texts[0].text = []rune("Don’t panic") // (the first heading)
	b.searchTexts = nil
	if h, _ := b.search("don't"); len(h) != 1 || h[0].Match != "Don’t" {
		t.Errorf("apostrophes: %+v", h)
	}
}

func TestSearchRequest(t *testing.T) {
	dir := t.TempDir()
	lib := writeLibrary(t, dir)
	st, err := store.Open(filepath.Join(dir, "cfg"), filepath.Join(dir, "cache"))
	if err != nil {
		t.Fatal(err)
	}
	rec := &recorder{}
	a := newApp(nil, st, lib)
	a.sendHook = rec.hook
	a.handle(reqInit, `{"width":800,"height":1200}`)
	a.handle(reqOpen, `{"id":"book-1"}`)
	a.handle(reqSearch, `{"query":"stormy"}`)
	m := rec.last(resSearch)
	if m == nil || m["query"] != "stormy" || m["total"] != float64(1) || m["book"] != "book-1" {
		t.Fatalf("search answer: %v", m)
	}
	hit := m["hits"].([]any)[0].(map[string]any)
	if hit["chapter"] != float64(0) || hit["match"] != "stormy" {
		t.Errorf("hit: %v", hit)
	}
}
