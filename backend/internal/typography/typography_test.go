package typography

import (
	"encoding/json"
	"testing"
)

func TestNormalizeAndApply(t *testing.T) {
	def := Settings{FontSize: 34, Font: "literata", LineSpacing: 125, Align: "justify", Margin: "normal", Paragraphs: "indent"}
	var s Settings
	_ = json.Unmarshal([]byte(`{"fontSize":200,"font":"comic","lineSpacing":150,"align":"left","margin":"huge"}`), &s)
	s.Normalize(def)
	want := Settings{FontSize: 34, Font: "literata", LineSpacing: 150, Align: "left", Margin: "normal", Paragraphs: "indent", Theme: "default"}
	if s != want {
		t.Fatalf("normalized %+v", s)
	}
	var u Update
	_ = json.Unmarshal([]byte(`{"fontSize":40,"font":"nope","paragraphs":"spaced","align":"center"}`), &u)
	if !s.Apply(u) || s.FontSize != 40 || s.Font != "literata" || s.Paragraphs != "spaced" || s.Align != "left" {
		t.Fatalf("applied %+v", s)
	}
	if s.Apply(Update{}) {
		t.Error("empty update changed something")
	}
	u = Update{}
	_ = json.Unmarshal([]byte(`{"theme":"nightshift"}`), &u)
	if !s.Apply(u) || s.Theme != "nightshift" {
		t.Errorf("themes: %+v", s)
	}
	u = Update{}
	_ = json.Unmarshal([]byte(`{"theme":"neon"}`), &u)
	if s.Apply(u) {
		t.Errorf("invalid theme accepted: %+v", s)
	}
	// earlier colour settings
	for in, want := range map[string]string{
		`{"night":"on","warmth":3}`: "sepia", `{"night":"on","warmth":1}`: "cream", `{"night":"evening","warmth":1}`: "default",
		`{"theme":"sepia","evening":"dark"}`: "sepia", `{"theme":"dark"}`: "dark", `{"theme":"softdark"}`: "softdark",
		`{"theme":"paper"}`: "default", `{"theme":"neon"}`: "default", `{}`: "default",
	} {
		var m Settings
		_ = json.Unmarshal([]byte(in), &m)
		m.Normalize(def)
		if m.Theme != want || m.Evening != "" || m.Night != "" || m.Warmth != 0 {
			t.Errorf("%s -> %+v", in, m)
		}
	}
}
