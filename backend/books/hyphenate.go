package main

import (
	"bufio"
	"bytes"
	"embed"
	"strings"
	"sync"
	"unicode"
)

// Hyphenation: soft hyphens (U+00AD) where a word may break, from TeX's
// hyphenation patterns (Liang's algorithm, the hyph-utf8 patterns in hyph/,
// see hyph/NOTICES.txt). Qt breaks a line there and shows a hyphen; soft
// hyphens are not counted (isCounted), so positions and sync stay the same.

//go:embed hyph/*.txt
var hyphFiles embed.FS

// hyphLangs: a book's language (its first subtag, or the whole tag) -> the
// patterns and the fewest letters before and after a break.
var hyphLangs = map[string]struct {
	file        string
	left, right int
}{
	"en": {"en-us", 2, 3}, "en-us": {"en-us", 2, 3}, "en-gb": {"en-gb", 2, 3}, "en-au": {"en-gb", 2, 3},
	"en-nz": {"en-gb", 2, 3}, "en-ie": {"en-gb", 2, 3}, "en-za": {"en-gb", 2, 3}, "en-in": {"en-gb", 2, 3},
	"de": {"de-1996", 2, 2}, "fr": {"fr", 2, 3}, "es": {"es", 2, 2}, "it": {"it", 2, 2}, "nl": {"nl", 2, 2},
}

// books without a language (or "und"): most are English
const defaultHyphLang = "en"

type hyphenator struct {
	patterns   map[string][]uint8 // letters -> the values between them (len+1)
	exceptions map[string][]bool  // word -> breaks before each letter
	maxLen     int
	left       int
	right      int
}

var (
	hyphMu    sync.Mutex
	hyphCache = map[string]*hyphenator{}
)

// hyphenatorFor returns the hyphenator for a language tag ("en-US", "de"),
// or nil when there are no patterns for it.
func hyphenatorFor(lang string) *hyphenator {
	tag := strings.ToLower(strings.TrimSpace(strings.ReplaceAll(lang, "_", "-")))
	if tag == "" || tag == "und" || tag == "mul" || tag == "zxx" {
		tag = defaultHyphLang
	}
	l, ok := hyphLangs[tag]
	if !ok {
		main, _, _ := strings.Cut(tag, "-")
		if l, ok = hyphLangs[main]; !ok {
			return nil
		}
	}
	hyphMu.Lock()
	defer hyphMu.Unlock()
	if h, ok := hyphCache[l.file]; ok {
		return h
	}
	h := loadHyphenator(l.file, l.left, l.right)
	hyphCache[l.file] = h
	return h
}

func loadHyphenator(name string, left, right int) *hyphenator {
	h := &hyphenator{patterns: map[string][]uint8{}, exceptions: map[string][]bool{}, left: left, right: right}
	if data, err := hyphFiles.ReadFile("hyph/hyph-" + name + ".pat.txt"); err == nil {
		sc := bufio.NewScanner(bytes.NewReader(data))
		for sc.Scan() {
			for _, p := range strings.Fields(sc.Text()) {
				h.addPattern(p)
			}
		}
	}
	if data, err := hyphFiles.ReadFile("hyph/hyph-" + name + ".hyp.txt"); err == nil {
		sc := bufio.NewScanner(bytes.NewReader(data))
		for sc.Scan() {
			for _, e := range strings.Fields(sc.Text()) {
				var letters []rune
				var br []bool // a break before each letter
				pending := false
				for _, r := range e {
					if r == '-' {
						pending = len(letters) > 0
						continue
					}
					letters = append(letters, r)
					br = append(br, pending)
					pending = false
				}
				h.exceptions[string(letters)] = br
			}
		}
	}
	return h
}

// addPattern: "a1b4c" -> letters "abc", values [0 1 4 0].
func (h *hyphenator) addPattern(p string) {
	var letters []rune
	vals := []uint8{0}
	for _, r := range p {
		if r >= '0' && r <= '9' {
			vals[len(vals)-1] = uint8(r - '0')
			continue
		}
		letters = append(letters, r)
		vals = append(vals, 0)
	}
	if len(letters) == 0 {
		return
	}
	h.patterns[string(letters)] = vals
	h.maxLen = max(h.maxLen, len(letters))
}

// breaks returns, for each letter of word, whether the word may break
// before it (nil: nowhere). word is letters only.
func (h *hyphenator) breaks(word []rune) []bool {
	n := len(word)
	if n < h.left+h.right {
		return nil
	}
	lower := make([]rune, n)
	upper := 0
	for i, r := range word {
		lower[i] = unicode.ToLower(r)
		if lower[i] != r {
			upper++
		}
	}
	if upper == n {
		return nil // all capitals: an abbreviation (or a shouted word)
	}
	if e, ok := h.exceptions[string(lower)]; ok {
		out := make([]bool, n)
		for i := h.left; i <= n-h.right; i++ {
			out[i] = e[i]
		}
		return out
	}
	s := make([]rune, 0, n+2)
	s = append(s, '.')
	s = append(s, lower...)
	s = append(s, '.')
	points := make([]uint8, len(s)+1)
	for i := range s {
		for j := i + 1; j <= len(s) && j-i <= h.maxLen; j++ {
			if v, ok := h.patterns[string(s[i:j])]; ok {
				for k, x := range v {
					if x > points[i+k] {
						points[i+k] = x
					}
				}
			}
		}
	}
	out := make([]bool, n)
	found := false
	// the gap before word[i] is the gap before s[i+1]
	for i := h.left; i <= n-h.right; i++ {
		if points[i+1]%2 == 1 {
			out[i], found = true, true
		}
	}
	if !found {
		return nil
	}
	return out
}

func isWordRune(r rune) bool { return unicode.IsLetter(r) || unicode.Is(unicode.Mn, r) }

// hyphenate writes rs with soft hyphens where its words may break.
func (h *hyphenator) hyphenate(rs []rune) []rune {
	out := make([]rune, 0, len(rs)+len(rs)/8)
	for i := 0; i < len(rs); {
		if !isWordRune(rs[i]) {
			out = append(out, rs[i])
			i++
			continue
		}
		j := i
		for j < len(rs) && isWordRune(rs[j]) {
			j++
		}
		word := rs[i:j]
		br := h.breaks(word)
		for k, r := range word {
			if br != nil && br[k] {
				out = append(out, 0xAD)
			}
			out = append(out, r)
		}
		i = j
	}
	return out
}
