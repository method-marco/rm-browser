package main

import (
	"context"
	"os"
	"testing"
	"time"
)

// Real books (development): RM_BOOKS_REAL=<a copy of xochitl's folder>
// opens every EPUB in its Library folder, checks each chapter's positions
// against the written text and round trips positions through xpointers.
// With RM_BOOKS_SYNC=<kosync-bridge config.json> it also resolves the
// position the sync server has for each book (e.g. from KOReader or the
// built-in reader) and shows the text there.
func TestRealBooks(t *testing.T) {
	dir := os.Getenv("RM_BOOKS_REAL")
	if dir == "" {
		t.Skip("RM_BOOKS_REAL not set")
	}
	lib, err := openLibrary(dir)
	if err != nil {
		t.Fatal(err)
	}
	books, found, err := lib.scan()
	if err != nil || !found {
		t.Fatal(err, found)
	}
	var login *SyncLogin
	if p := os.Getenv("RM_BOOKS_SYNC"); p != "" {
		login = readBridgeLogin(p)
	}
	for _, lb := range books {
		t0 := time.Now()
		b, err := loadBook(lib, lb.ID)
		if err != nil {
			t.Errorf("%s: %v", lb.Title, err)
			continue
		}
		open := time.Since(t0)
		trips, bad := 0, 0
		for ch := 0; ch < b.chapters(); ch++ {
			c := b.ep.renderChapter(ch, nil, 0, renderOptions{})
			st := b.ep.renderChapter(ch, nil, 0, renderOptions{bookStyle: true}) // the book's style: the same text
			if len(st.pos) != len(c.pos) || len(visible(st.html)) != len(st.pos) {
				t.Errorf("%s chapter %d: book style %d positions, plain %d", lb.Title, ch, len(st.pos), len(c.pos))
			}
			if os.Getenv("RM_BOOKS_SHOW") == lb.ID && ch == b.chapters()/2 {
				t.Logf("book style sample:\n%.1500s", st.html)
			}
			if v := visible(c.html); len(v) != len(c.pos) {
				t.Errorf("%s chapter %d: %d visible characters written, %d positions", lb.Title, ch, len(v), len(c.pos))
			} else {
				for i, p := range c.pos {
					if b.ep.texts[p.seq].text[p.off] != v[i] {
						t.Errorf("%s chapter %d: position %d points at the wrong character", lb.Title, ch, i)
						break
					}
				}
			}
			for k := 0; k < b.counts[ch]; k += 97 {
				xp := b.xpointer(ch, k)
				c2, k2, ok := b.fromXPointer(xp)
				trips++
				if !ok || c2 != ch || k2 != k {
					bad++
					if bad < 5 {
						t.Errorf("%s: (%d, %d) -> %s -> (%d, %d)", lb.Title, ch, k, xp, c2, k2)
					}
				}
			}
		}
		t.Logf("%-50.50s %3d chapters %8d chars %3d toc  open %v  %d round trips, %d wrong  digest %s",
			lb.Title, b.chapters(), b.total, len(b.toc), open.Round(time.Millisecond), trips, bad, b.digest)
		if login != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			p, err := syncGet(ctx, *login, b.digest)
			cancel()
			switch {
			case err != nil:
				t.Logf("    sync: %v", err)
			case p == nil:
				t.Logf("    sync: nothing stored")
			default:
				ch, k, ok := b.fromXPointer(p.Progress)
				text := ""
				if ok {
					c := b.dryChapter(ch)
					for i := k; i < len(c.pos) && i < k+60; i++ {
						text += string(b.ep.texts[c.pos[i].seq].text[c.pos[i].off])
					}
				}
				t.Logf("    sync: %s %.1f%% from %q -> ok=%v chapter %d char %d (%.1f%% here): %q",
					p.Progress, p.Percentage*100, p.Device, ok, ch, k, b.percent(ch, k)*100, text)
			}
		}
		b.close()
	}
}
