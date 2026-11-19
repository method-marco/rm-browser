// Package typography: the reading settings both apps share (the Font
// panel, app/shared/FontPanel.qml): typeface, size, line spacing,
// alignment, margins and paragraph style.
package typography

// Text size in pixels (the Font panel's A− and A+).
const MinFontSize, MaxFontSize = 16, 96

// Fonts are the typeface keys of the Font panel (app/shared/fonts.js). The
// browser embeds the same faces for Save as PDF (pdfFonts in export.go).
var Fonts = []string{"sans", "inter", "plexsans", "atkinson", "garamond", "literata", "merriweather",
	"sourceserif", "lora", "crimson", "bitter", "plexmono"}

// Valid reports whether font is one of Fonts.
func Valid(font string) bool {
	for _, f := range Fonts {
		if f == font {
			return true
		}
	}
	return false
}

type Settings struct {
	FontSize    int    `json:"fontSize"`    // pixels
	Font        string `json:"font"`        // one of Fonts
	LineSpacing int    `json:"lineSpacing"` // percent of the font's line height
	Align       string `json:"align"`       // left | justify
	Margin      string `json:"margin"`      // narrow | normal | wide
	Paragraphs  string `json:"paragraphs"`  // spaced (gap between paragraphs) | indent (first line indented, like books)
	Theme       string `json:"theme"`       // colours: one of Themes (app/shared/Theme.qml)
	// earlier colour settings, read once into Theme
	Evening string `json:"evening,omitempty"`
	Night   string `json:"night,omitempty"`
	Warmth  int    `json:"warmth,omitempty"`
}

// Themes: paper and text colours (app/shared/Theme.qml). cream, warm, sepia
// and nightshift are warmer papers for the reMarkable's cool white front
// light; softdark is light grey on dark grey, dark white on black.
var Themes = []string{"default", "cream", "warm", "sepia", "nightshift", "softdark", "dark"}

// Update: the fields the frontend sends; nil = unchanged.
type Update struct {
	FontSize    *int    `json:"fontSize"`
	Font        *string `json:"font"`
	LineSpacing *int    `json:"lineSpacing"`
	Align       *string `json:"align"`
	Margin      *string `json:"margin"`
	Paragraphs  *string `json:"paragraphs"`
	Theme       *string `json:"theme"`
}

func oneOf(v string, allowed ...string) bool {
	for _, a := range allowed {
		if v == a {
			return true
		}
	}
	return false
}

// Normalize replaces every missing or invalid value with the one in def.
func (s *Settings) Normalize(def Settings) {
	if s.FontSize < MinFontSize || s.FontSize > MaxFontSize {
		s.FontSize = def.FontSize
	}
	if !Valid(s.Font) {
		s.Font = def.Font
	}
	if s.LineSpacing < 100 || s.LineSpacing > 200 {
		s.LineSpacing = def.LineSpacing
	}
	if !oneOf(s.Align, "left", "justify") {
		s.Align = def.Align
	}
	if !oneOf(s.Margin, "narrow", "normal", "wide") {
		s.Margin = def.Margin
	}
	if !oneOf(s.Paragraphs, "spaced", "indent") {
		s.Paragraphs = def.Paragraphs
	}
	// earlier colour settings: "paper" was the default, the night light's
	// warmth a warm theme
	if s.Theme == "" && s.Night == "on" {
		s.Theme = map[int]string{1: "cream", 2: "warm", 3: "sepia"}[s.Warmth]
	}
	if !oneOf(s.Theme, Themes...) {
		s.Theme = "default"
	}
	s.Evening, s.Night, s.Warmth = "", "", 0
}

// Apply takes the valid values of u and reports whether anything changed.
func (s *Settings) Apply(u Update) bool {
	old := *s
	if u.FontSize != nil && *u.FontSize >= MinFontSize && *u.FontSize <= MaxFontSize {
		s.FontSize = *u.FontSize
	}
	if u.Font != nil && Valid(*u.Font) {
		s.Font = *u.Font
	}
	if u.LineSpacing != nil && *u.LineSpacing >= 100 && *u.LineSpacing <= 200 {
		s.LineSpacing = *u.LineSpacing
	}
	if u.Align != nil && oneOf(*u.Align, "left", "justify") {
		s.Align = *u.Align
	}
	if u.Margin != nil && oneOf(*u.Margin, "narrow", "normal", "wide") {
		s.Margin = *u.Margin
	}
	if u.Paragraphs != nil && oneOf(*u.Paragraphs, "spaced", "indent") {
		s.Paragraphs = *u.Paragraphs
	}
	if u.Theme != nil && oneOf(*u.Theme, Themes...) {
		s.Theme = *u.Theme
	}
	return *s != old
}
