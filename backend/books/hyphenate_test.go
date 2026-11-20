package main

import (
	"strings"
	"testing"
)

func hyph(h *hyphenator, s string) string {
	return strings.ReplaceAll(string(h.hyphenate([]rune(s))), "­", "-")
}

func TestHyphenation(t *testing.T) {
	en := hyphenatorFor("en-US")
	if en == nil || hyphenatorFor("en") != en || hyphenatorFor("und") != en || hyphenatorFor("") != en {
		t.Fatal("English patterns")
	}
	if hyphenatorFor("ja") != nil {
		t.Error("no patterns for Japanese: no hyphenation")
	}
	if hyphenatorFor("en-GB") == en || hyphenatorFor("de-AT") == nil || hyphenatorFor("fr_CA") == nil {
		t.Error("language tags")
	}
	for in, want := range map[string]string{
		"hyphenation":           "hy-phen-ation",
		"associate":             "as-so-ciate", // an exception
		"Hyphenation, please.":  "Hy-phen-ation, please.",
		"NASA and UNESCO":       "NASA and UNESCO", // abbreviations stay whole
		"a cat":                 "a cat",
		"don’t":            "don’t",
		"Unbelievable results.": "Un-be-liev-able re-sults.",
	} {
		if got := hyph(en, in); got != want {
			t.Errorf("en %q: %q, want %q", in, got, want)
		}
	}
	// at least two letters before a break and three after (English)
	for _, w := range strings.Fields(hyph(en, "extraordinarily international communication possibilities")) {
		parts := strings.Split(w, "-")
		if len(parts) < 2 || len([]rune(parts[0])) < 2 || len([]rune(parts[len(parts)-1])) < 3 {
			t.Errorf("en breaks %q", w)
		}
	}
	de := hyphenatorFor("de")
	if got := hyph(de, "Silbentrennung für Bücher"); got != "Sil-ben-tren-nung für Bü-cher" {
		t.Errorf("de: %q", got)
	}
}

// Soft hyphens change neither the text nor any position; headings and
// code keep whole words.
func TestHyphenatedChapter(t *testing.T) {
	ep := openTestBook(t)
	plain := ep.renderChapter(0, nil, 0, renderOptions{})
	hy := ep.renderChapter(0, nil, 0, renderOptions{hyphenate: true, bookStyle: true})
	if !strings.Contains(hy.html, "­") {
		t.Fatalf("no soft hyphens:\n%s", hy.html)
	}
	if string(visible(hy.html)) != string(visible(plain.html)) || len(hy.pos) != len(plain.pos) {
		t.Fatal("the hyphenated text differs")
	}
	for i := range plain.pos {
		if hy.pos[i] != plain.pos[i] {
			t.Fatalf("position %d moved", i)
		}
	}
	for _, h := range []string{"<h2", "<h3", "<h4"} {
		for _, part := range strings.Split(hy.html, h)[1:] {
			head, _, _ := strings.Cut(part, "</h")
			if strings.Contains(head, "­") {
				t.Errorf("a hyphenated heading: %q", head)
			}
		}
	}
}
