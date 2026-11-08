package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Real pages laid out by Qt (written by `make qmltest` from the fixtures of
// the live Sites test): the backend must place every character of the
// page on Qt's lines. The PDFs land in build/fixture/pdf/ to look at.
func TestFixtureLayouts(t *testing.T) {
	dir := "../build/fixture"
	layouts, _ := filepath.Glob(filepath.Join(dir, "*.layout"))
	if len(layouts) == 0 {
		t.Skip("no layouts: run the Sites test with RM_BROWSER_FIXTURE, then make qmltest")
	}
	_ = os.MkdirAll(filepath.Join(dir, "pdf"), 0o755)
	for _, lf := range layouts {
		name := filepath.Base(lf)
		pageFile := lf[:strings.Index(lf, ".json.")+len(".json")]
		var p Page
		var req ExportRequest
		pb, err := os.ReadFile(pageFile)
		if err != nil {
			t.Fatal(err)
		}
		lb, _ := os.ReadFile(lf)
		if err := json.Unmarshal(pb, &p); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(lb, &req); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		read := func(path string) ([]byte, error) {
			return os.ReadFile(filepath.Join(dir, "cache", strings.TrimPrefix(path, "/repo/build/fixture/cache/")))
		}
		var buf bytes.Buffer
		if err := buildPDF(&buf, &p, req, read); err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		out := filepath.Join(dir, "pdf", strings.TrimSuffix(name, ".layout")+".pdf")
		_ = os.WriteFile(out, buf.Bytes(), 0o644)
		t.Logf("%s: %d lines, %d pages, %d KB", name[:min(60, len(name))], len(req.Lines)/4, len(req.PageStarts), buf.Len()/1024)
	}
}
