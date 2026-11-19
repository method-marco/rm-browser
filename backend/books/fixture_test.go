package main

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"
)

// The frontend must count visible characters exactly as the writer does
// (positions are "the n-th visible character of a chapter"). This writes a
// chapter with the tricky cases to tests/qml/books-chapter.json, which the
// frontend test (tests/qml/tst_books.qml) checks against Qt's layout.
// RM_BOOKS_WRITE_FIXTURE=1 rewrites it; otherwise it must be up to date.
const fixtureXHTML = `<html xmlns="http://www.w3.org/1999/xhtml"><body>
<h1 id="top">Tricky&#160;characters</h1>
<p>Soft&#173;hyphen, zero&#8203;width, non&#160;breaking&#8239;space, emoji 📚 and 𝔘nicode, 漢字かな, Ça&nbsp;va?</p>
<p>Line<br/>break and   collapsed     spaces &amp; entities &lt;tag&gt; “quotes” — dashes…</p>
<ul><li>First item</li><li>Second <b>bold</b> item</li></ul>
<ol start="3"><li>Numbered</li></ol>
<table><tr><th>Head</th><th>Other</th></tr><tr><td>Cell one</td><td>Cell two</td></tr></table>
<blockquote><p>A quote, indented.</p></blockquote>
<div><img src="pic.png"/></div>
<p id="end">The end. <a href="#top">Back to the top</a></p>
<pre>pre    formatted
text</pre>
</body></html>`

type fixtureSample struct {
	K  int    `json:"k"`
	Ch string `json:"ch"`
}

func TestQtFixture(t *testing.T) {
	doc, err := parseXHTML([]byte(fixtureXHTML))
	if err != nil {
		t.Fatal(err)
	}
	body := findElem(doc, "body")
	ep := &epubBook{frags: []*domNode{body}, fragPaths: []string{"text/c.xhtml"}, fragOf: map[string]int{"text/c.xhtml": 1}}
	var walk func(n *domNode)
	walk = func(n *domNode) {
		for _, c := range n.children {
			if c.isText() {
				c.seq = len(ep.texts)
				ep.texts = append(ep.texts, c)
			} else {
				walk(c)
			}
		}
	}
	walk(body)
	c := ep.renderChapter(0, func(p string) (string, int, int, bool) {
		return "/repo/app/books/icon.png", 128, 128, true
	}, 800)
	var samples []fixtureSample
	for k := range c.pos {
		p := c.pos[k]
		samples = append(samples, fixtureSample{K: k, Ch: string(ep.texts[p.seq].text[p.off])})
	}
	out, _ := json.MarshalIndent(map[string]any{"html": c.html, "count": len(c.pos), "samples": samples,
		"anchors": c.anchors, "imageDir": "/repo/app/books"}, "", " ")
	out = append(out, '\n')
	const path = "../../tests/qml/books-chapter.json"
	if os.Getenv("RM_BOOKS_WRITE_FIXTURE") != "" {
		if err := os.WriteFile(path, out, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	old, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(old, out) {
		t.Fatalf("%s is out of date: run RM_BOOKS_WRITE_FIXTURE=1 go test ./books/ -run TestQtFixture", path)
	}
}
