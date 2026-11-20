// rm-books backend: an EPUB reader for reMarkable tablets (AppLoad app
// "Books"). It reads the EPUBs in the library folder "Library", writes
// each chapter as rich text for the shared reader view (the browser's:
// pages you turn, the same Font panel), and syncs the reading position
// with KOReader's sync server (kosync.go). Sandboxed like the browser
// (internal/sandbox): the library is readable, nothing else.
package main

import (
	"context"
	"crypto/md5"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image/jpeg"
	"log"
	"net"
	"os"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/method-marco/rm-browser/backend/internal/appload"
	"github.com/method-marco/rm-browser/backend/internal/imaging"
	"github.com/method-marco/rm-browser/backend/internal/livedns"
	"github.com/method-marco/rm-browser/backend/internal/sandbox"
	"github.com/method-marco/rm-browser/backend/internal/store"
	"github.com/method-marco/rm-browser/backend/internal/typography"
)

var version = "dev"

// Message types frontend -> backend (must match app/books/main.qml).
const (
	reqInit     = 1  // {width, height}: answers state, then the open book or the library
	reqOpen     = 2  // {id}: open a book at its saved position
	reqChapter  = 3  // {chapter, char, dir}: show a chapter (dir ±1: skip empty ones that way; char -1: its end)
	reqPosition = 4  // {chapter, char}: the reader's position (first visible character on screen)
	reqLink     = 5  // {href}: a link inside the book ("book:<chapter>#<id>")
	reqSettings = 6  // typography.Update + {noImages, greyImages, autoSync}
	reqLibrary  = 7  // back to the library (rescans it)
	reqSync     = 8  // sync now: newer position from elsewhere, else send ours
	reqLogin    = 9  // {server, username, password, register}
	reqLogout   = 10 // forget the sync login
	reqViewport = 11 // {width, height}
	reqClosed   = 12 // the app was closed: send the position now
	reqPull     = 13 // go to the server's position, whatever it is
	reqPush     = 14 // send this position, even if unchanged
	reqSearch   = 15 // {query}: answers resSearch
)

// Message types backend -> frontend.
const (
	resState   = 101
	resError   = 102 // {message}
	resChapter = 103 // {book, chapter, html, char, end}
	resBook    = 104 // {id, title, author, chapters, counts, toc}
	resLibrary = 105 // {books, found, folder}
	resBusy    = 106 // {busy, text}
	resToast   = 107 // {message}
	resSync    = 108 // {chapter, char, percent, device, manual}: a newer position from another device
	resSearch  = 109 // {book, query, hits: [{chapter, char, len, before, match, after, title, percent}], total}
)

const (
	memoryLimit    = 384 << 20
	maxImageCache  = 150 << 20
	keepImageCache = 100 << 20
	pushDelay      = 8 * time.Second // after the last page turn
)

// ebookDefaults: settings that suit books (the Font panel's "Defaults").
var ebookDefaults = typography.Settings{FontSize: 34, Font: "literata", LineSpacing: 125, Align: "justify", Margin: "normal", Paragraphs: "indent"}

// defaultsMap: the Font panel's "Defaults" (ebookDefaults and the book's style).
func defaultsMap() map[string]any {
	var m map[string]any
	b, _ := json.Marshal(ebookDefaults)
	_ = json.Unmarshal(b, &m)
	m["bookStyle"] = true
	m["hyphenate"] = true
	delete(m, "theme") // colours are not a book default
	return m
}

type Settings struct {
	typography.Settings
	NoImages   bool  `json:"noImages"`
	GreyImages bool  `json:"greyImages"`
	ManualSync bool  `json:"manualSync"` // false: sync on open, page turns and close
	BookStyle  *bool `json:"bookStyle"`  // the book's own formatting (css.go); nil = on
	Hyphenate  *bool `json:"hyphenate"`  // soft hyphens in words (hyphenate.go); nil = on
}

type BookState struct {
	Chapter  int     `json:"chapter"`
	Char     int     `json:"char"`
	Percent  float64 `json:"percent"`
	Read     int64   `json:"read"`               // when the position last changed here (unix)
	Pushed   string  `json:"pushed,omitempty"`   // last position sent to the sync server
	PushedAt int64   `json:"pushedAt,omitempty"` // its server timestamp
}

type Config struct {
	Settings Settings              `json:"settings"`
	Sync     SyncLogin             `json:"sync"`
	Imported bool                  `json:"imported,omitempty"` // the kosync-bridge login was offered once
	DeviceID string                `json:"deviceId"`
	Open     string                `json:"open,omitempty"` // book open when the app was left
	Books    map[string]*BookState `json:"books"`
}

type App struct {
	al      *appload.AppLoad
	store   *store.Store
	lib     *library
	sandbox string

	mu         sync.Mutex // guards everything below
	cfg        Config
	vpW, vpH   int
	cur        *book
	shown      map[string]*chapter // rendered chapters (with pictures), by chapter + layout
	shownKeys  []string
	pushTimer  *time.Timer
	syncStatus string // last sync problem, shown in Settings

	sendHook func(typ int, v any) // tests
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: entry <appload-socket>")
		os.Exit(2)
	}
	debug.SetMemoryLimit(memoryLimit)
	al, err := appload.Connect(os.Args[1])
	if err != nil {
		log.Fatal(err)
	}
	defer al.Close()

	cfgDir, cacheDir := envPath("RM_BOOKS_CONFIG", ".config/rm-books"), envPath("RM_BOOKS_CACHE", ".cache/rm-books")
	st, err := store.Open(cfgDir, cacheDir)
	if err != nil {
		log.Fatal(err)
	}
	// Everything outside our folders is opened now, before the sandbox:
	// the library (read-only) and, once, the kosync-bridge login.
	libDir := xochitlDirPath()
	lib, libErr := openLibrary(libDir)
	bridge := readBridgeLogin(envPath("RM_BOOKS_KOSYNC_IMPORT", ".local/share/kosync-bridge/config.json"))
	if live := livedns.Open("/etc/resolv.conf"); live != nil {
		net.DefaultResolver = live.Resolver()
	}
	jail := os.Getenv("RM_BOOKS_JAIL")
	if jail == "" {
		jail = filepath.Join(filepath.Dir(cacheDir), "rm-books-jail")
	}
	// No network ports for Landlock: the only server is the sync server
	// the user typed in, which may use any port.
	layers, err := sandbox.Enter(sandbox.Options{Jail: jail, Dirs: []string{cfgDir, cacheDir}, ReadOnly: []string{libDir}})
	if err != nil {
		log.Printf("sandbox: %v", err)
		_ = al.Send(resError, mustJSON(map[string]any{"message": "Cannot start the sandbox, so Books stays off: " + err.Error()}))
		for {
			if _, _, err := al.Receive(); err != nil {
				return
			}
		}
	}
	log.Printf("rm-books %s, sandbox: %s", version, layers)
	if libErr != nil {
		log.Printf("library: %v", libErr)
	}
	app := newApp(al, st, lib)
	app.sandbox = layers
	app.importLogin(bridge)
	for {
		typ, contents, err := al.Receive()
		if err != nil {
			log.Printf("receive: %v", err)
			return
		}
		switch typ {
		case appload.MsgSystemTerminate:
			app.pushNow(false)
			return
		case appload.MsgSystemNewCoordinator, appload.MsgSystemLostCoordinator:
			continue
		}
		app.handle(int(typ), contents)
	}
}

func envPath(env, rel string) string {
	if p := os.Getenv(env); p != "" {
		return p
	}
	h, err := os.UserHomeDir()
	if err != nil {
		h = "/home/root"
	}
	return filepath.Join(h, rel)
}

// readBridgeLogin reads kosync-native-reader's login (remarkable-mods),
// so that Books syncs with the same account without logging in again.
func readBridgeLogin(path string) *SyncLogin {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var l SyncLogin
	if json.Unmarshal(b, &l) != nil || !l.loggedIn() {
		return nil
	}
	if s, err := checkServer(l.Server); err == nil {
		l.Server = s
	} else {
		return nil
	}
	return &l
}

func newApp(al *appload.AppLoad, st *store.Store, lib *library) *App {
	a := &App{al: al, store: st, lib: lib, vpW: 860, vpH: 1300, shown: map[string]*chapter{}}
	if b, err := st.Cfg.ReadFile("config.json"); err == nil {
		_ = json.Unmarshal(b, &a.cfg)
	}
	a.cfg.Settings.Settings.Normalize(ebookDefaults)
	if a.cfg.Settings.BookStyle == nil {
		on := true
		a.cfg.Settings.BookStyle = &on
	}
	if a.cfg.Settings.Hyphenate == nil {
		on := true
		a.cfg.Settings.Hyphenate = &on
	}
	if a.cfg.Books == nil {
		a.cfg.Books = map[string]*BookState{}
	}
	if a.cfg.DeviceID == "" {
		r := make([]byte, 16)
		_, _ = rand.Read(r)
		s := md5.Sum(r)
		a.cfg.DeviceID = strings.ToUpper(hex.EncodeToString(s[:]))
	}
	if a.cfg.Sync.Server == "" {
		a.cfg.Sync.Server = defaultSyncServer
	}
	a.saveLocked()
	return a
}

// importLogin takes over the kosync-bridge login once (not again after
// the user logged out here).
func (a *App) importLogin(l *SyncLogin) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if l == nil || a.cfg.Imported || a.cfg.Sync.loggedIn() {
		return
	}
	a.cfg.Sync = *l
	a.cfg.Imported = true
	a.saveLocked()
	log.Printf("sync: took over the kosync-bridge login (%s)", l.Username)
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func (a *App) send(typ int, v any) {
	if a.sendHook != nil {
		a.sendHook(typ, v)
		return
	}
	if err := a.al.Send(int32(typ), mustJSON(v)); err != nil {
		log.Printf("send %d: %v", typ, err)
		if errors.Is(err, appload.ErrTooLarge) && typ == resChapter {
			_ = a.al.Send(resError, mustJSON(map[string]any{"message": "This chapter is too large to show."}))
		}
	}
}

func (a *App) toast(msg string)          { a.send(resToast, map[string]any{"message": msg}) }
func (a *App) busy(on bool, text string) { a.send(resBusy, map[string]any{"busy": on, "text": text}) }
func (a *App) sendError(msg string)      { a.send(resError, map[string]any{"message": msg}) }

func (a *App) saveLocked() {
	b, err := json.MarshalIndent(a.cfg, "", " ")
	if err == nil {
		err = a.store.WriteConfig(b)
	}
	if err != nil {
		log.Printf("save config: %v", err)
	}
}

func (a *App) stateLocked(id string) *BookState {
	s, ok := a.cfg.Books[id]
	if !ok {
		s = &BookState{}
		a.cfg.Books[id] = s
	}
	return s
}

func (a *App) sendState() {
	a.mu.Lock()
	st := map[string]any{
		"settings": a.cfg.Settings,
		"defaults": defaultsMap(),
		"sync": map[string]any{
			"server": a.cfg.Sync.Server, "username": a.cfg.Sync.Username, "loggedIn": a.cfg.Sync.loggedIn(),
			"auto": !a.cfg.Settings.ManualSync, "device": syncDeviceName, "status": a.syncStatus,
		},
		"version":  version,
		"sandbox":  a.sandbox,
		"imageDir": a.store.CachePath("img"),
		"folder":   libraryFolder,
	}
	a.mu.Unlock()
	a.send(resState, st)
}

// ---------------------------------------------------------------- messages

func (a *App) handle(typ int, contents string) {
	var req struct {
		typography.Update
		Width      int     `json:"width"`
		Height     int     `json:"height"`
		ID         string  `json:"id"`
		Chapter    int     `json:"chapter"`
		Char       int     `json:"char"`
		Dir        int     `json:"dir"`
		Href       string  `json:"href"`
		NoImages   *bool   `json:"noImages"`
		GreyImages *bool   `json:"greyImages"`
		AutoSync   *bool   `json:"autoSync"`
		BookStyle  *bool   `json:"bookStyle"`
		Hyphenate  *bool   `json:"hyphenate"`
		Server     string  `json:"server"`
		Query      string  `json:"query"`
		Username   string  `json:"username"`
		Password   *string `json:"password"`
		Register   bool    `json:"register"`
	}
	if contents != "" {
		if err := json.Unmarshal([]byte(contents), &req); err != nil {
			log.Printf("bad message %d: %v", typ, err)
			return
		}
	}
	switch typ {
	case reqInit, reqViewport:
		changed := false
		if req.Width >= 200 && req.Height >= 200 {
			a.mu.Lock()
			changed = a.vpW != req.Width || a.vpH != req.Height
			a.vpW, a.vpH = req.Width, req.Height
			a.mu.Unlock()
		}
		if typ == reqInit {
			a.sendState()
			a.mu.Lock()
			open := a.cfg.Open
			a.mu.Unlock()
			if open != "" {
				a.openBook(open)
			} else {
				a.sendLibrary()
			}
		} else if changed {
			a.resendCurrent() // pictures are sized for the screen: rotated -> new sizes
		}
	case reqOpen:
		a.openBook(req.ID)
	case reqChapter:
		a.showChapter(req.Chapter, req.Char, req.Dir)
	case reqPosition:
		a.setPosition(req.Chapter, req.Char)
	case reqLink:
		a.followLink(req.Href)
	case reqLibrary:
		go a.pushNow(false)
		a.mu.Lock()
		a.cfg.Open = ""
		a.saveLocked()
		a.mu.Unlock()
		a.sendLibrary()
	case reqSync:
		go a.pull(true, false)
	case reqPull:
		go a.pull(true, true)
	case reqPush:
		go a.pushNow(true)
	case reqLogin:
		pw := ""
		if req.Password != nil {
			pw = *req.Password
		}
		go a.login(req.Server, req.Username, pw, req.Password == nil, req.Register)
	case reqLogout:
		a.mu.Lock()
		a.cfg.Sync = SyncLogin{Server: a.cfg.Sync.Server}
		a.syncStatus = ""
		a.saveLocked()
		a.mu.Unlock()
		a.sendState()
	case reqClosed:
		go a.pushNow(false)
	case reqSearch:
		a.search(req.Query)
	case reqSettings:
		a.mu.Lock()
		s := &a.cfg.Settings
		s.Settings.Apply(req.Update)
		images := false
		if req.NoImages != nil && *req.NoImages != s.NoImages {
			s.NoImages, images = *req.NoImages, true
		}
		if req.GreyImages != nil && *req.GreyImages != s.GreyImages {
			s.GreyImages, images = *req.GreyImages, true
		}
		if req.BookStyle != nil && (s.BookStyle == nil || *req.BookStyle != *s.BookStyle) {
			v := *req.BookStyle
			s.BookStyle, images = &v, true // a new chapter text
		}
		if req.Hyphenate != nil && (s.Hyphenate == nil || *req.Hyphenate != *s.Hyphenate) {
			v := *req.Hyphenate
			s.Hyphenate, images = &v, true
		}
		if req.AutoSync != nil {
			s.ManualSync = !*req.AutoSync
		}
		a.saveLocked()
		a.mu.Unlock()
		a.sendState()
		if images {
			a.resendCurrent()
		}
	}
}

// search answers the matches of query in the open book.
func (a *App) search(query string) {
	a.mu.Lock()
	b := a.cur
	if b == nil {
		a.mu.Unlock()
		return
	}
	first := b.searchTexts == nil
	a.mu.Unlock()
	if first {
		a.busy(true, "Searching…")
	}
	a.mu.Lock()
	t0 := time.Now()
	hits, total := b.search(query)
	a.mu.Unlock()
	log.Printf("search: %d matches in %v", total, time.Since(t0).Round(time.Millisecond))
	if first {
		a.busy(false, "")
	}
	a.send(resSearch, map[string]any{"book": b.id, "query": query, "hits": hits, "total": total})
}

// ---------------------------------------------------------------- library

func (a *App) sendLibrary() {
	if a.lib == nil {
		a.send(resLibrary, map[string]any{"books": []any{}, "found": false, "folder": libraryFolder,
			"error": "The tablet's library can't be read."})
		return
	}
	books, found, err := a.lib.scan()
	if err != nil {
		log.Printf("library: %v", err)
	}
	type item struct {
		libBook
		Percent float64 `json:"percent"`
		Read    int64   `json:"read"`
	}
	out := []item{}
	a.mu.Lock()
	for _, b := range books {
		it := item{libBook: b}
		if s, ok := a.cfg.Books[b.ID]; ok {
			it.Percent, it.Read = s.Percent, s.Read
		}
		out = append(out, it)
	}
	a.mu.Unlock()
	// books being read first (most recent first), then the rest by folder and title
	sort.SliceStable(out, func(i, j int) bool { return out[i].Read > out[j].Read })
	a.send(resLibrary, map[string]any{"books": out, "found": found, "folder": libraryFolder})
}

// ---------------------------------------------------------------- reading

func (a *App) openBook(id string) {
	a.mu.Lock()
	cur := a.cur
	a.mu.Unlock()
	if cur == nil || cur.id != id {
		if a.lib == nil {
			a.sendError("The tablet's library can't be read.")
			return
		}
		name := a.lib.name(id)
		if name == "" {
			name = "the book"
		}
		a.busy(true, "Opening "+name+" …")
		t0 := time.Now()
		b, err := loadBook(a.lib, id)
		a.busy(false, "")
		if err != nil {
			a.mu.Lock()
			if a.cfg.Open == id {
				a.cfg.Open = ""
				a.saveLocked()
			}
			a.mu.Unlock()
			a.sendError("Could not open " + name + ": " + err.Error())
			a.sendLibrary()
			return
		}
		log.Printf("opened %s (%d chapters, %d characters) in %v", id, b.chapters(), b.total, time.Since(t0).Round(time.Millisecond))
		a.mu.Lock()
		if a.cur != nil {
			a.cur.close()
		}
		a.cur = b
		a.shown, a.shownKeys = map[string]*chapter{}, nil
		a.mu.Unlock()
		cur = b
	}
	a.mu.Lock()
	a.cfg.Open = id
	st := a.stateLocked(id)
	ch, k := st.Chapter, st.Char
	a.saveLocked()
	a.mu.Unlock()
	a.send(resBook, map[string]any{"id": cur.id, "title": cur.title, "author": cur.author, "chapters": cur.chapters(),
		"counts": cur.counts, "toc": cur.toc, "imageBook": cur.imageBook()})
	if ch < 0 || ch >= cur.chapters() {
		ch, k = 0, 0
	}
	a.showChapter(ch, k, 1)
	go a.pull(false, false)
}

func (a *App) resendCurrent() {
	a.mu.Lock()
	b := a.cur
	open := a.cfg.Open
	var ch, k int
	if b != nil {
		st := a.stateLocked(b.id)
		ch, k = st.Chapter, st.Char
	}
	a.mu.Unlock()
	if b != nil && open == b.id {
		a.showChapter(ch, k, 1)
	}
}

// showChapter sends chapter ch (or the next one in direction dir that
// shows something) at visible character k (-1: its end).
func (a *App) showChapter(ch, k, dir int) {
	a.mu.Lock()
	b := a.cur
	if b == nil {
		a.mu.Unlock()
		return
	}
	if dir == 0 {
		dir = 1
	}
	if ch < 0 || ch >= b.chapters() {
		a.mu.Unlock()
		return // before the first or past the last chapter: stay
	}
	n := b.nextShown(ch, dir)
	if n < 0 {
		n = b.nextShown(ch, -dir)
	}
	if n < 0 {
		a.mu.Unlock()
		a.sendError("This book has nothing to show.")
		return
	}
	if n != ch {
		k = map[bool]int{true: 0, false: -1}[dir > 0]
	}
	c := a.renderShownLocked(b, n)
	a.mu.Unlock()
	a.send(resChapter, map[string]any{"book": b.id, "chapter": n, "html": c.html, "char": k, "end": k < 0})
}

func (a *App) renderShownLocked(b *book, ch int) *chapter {
	s := a.cfg.Settings
	opt := renderOptions{bookStyle: s.BookStyle == nil || *s.BookStyle, hyphenate: s.Hyphenate == nil || *s.Hyphenate}
	key := fmt.Sprintf("%s|%d|%d|%d|%v|%v|%+v", b.id, ch, a.vpW, a.vpH, s.NoImages, s.GreyImages, opt)
	if c, ok := a.shown[key]; ok {
		return c
	}
	var images ImageFunc
	if !s.NoImages {
		maxW, maxH, grey := a.vpW, a.vpH, s.GreyImages
		images = func(p string) (string, int, int, bool) { return a.bookImage(b, p, maxW, maxH, grey) }
	}
	c := b.ep.renderChapter(ch, images, a.vpW, opt)
	a.shown[key] = c
	a.shownKeys = append(a.shownKeys, key)
	if len(a.shownKeys) > 3 {
		delete(a.shown, a.shownKeys[0])
		a.shownKeys = a.shownKeys[1:]
	}
	if err := a.store.PruneImages(maxImageCache, keepImageCache); err != nil {
		log.Printf("prune images: %v", err)
	}
	return c
}

// bookImage converts a picture of the book for the screen (cached).
func (a *App) bookImage(b *book, zipPath string, maxW, maxH int, grey bool) (string, int, int, bool) {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%s|%s|%d|%d|%v", b.id, b.digest, zipPath, maxW, maxH, grey)))
	name := "img/" + hex.EncodeToString(sum[:12]) + ".jpg"
	if data, err := a.store.Cache.ReadFile(name); err == nil {
		if cfg, err := jpeg.DecodeConfig(strings.NewReader(string(data))); err == nil {
			now := time.Now()
			_ = a.store.Cache.Chtimes(name, now, now)
			return a.store.CachePath(name), cfg.Width, cfg.Height, true
		}
	}
	data, err := b.ep.read(zipPath)
	if err != nil {
		return "", 0, 0, false
	}
	out, w, h, err := imaging.Convert(data, maxW, maxH, grey)
	if err != nil {
		return "", 0, 0, false
	}
	if err := a.store.Cache.MkdirAll("img", 0o700); err != nil {
		return "", 0, 0, false
	}
	if err := a.store.Cache.WriteFile(name, out, 0o644); err != nil {
		return "", 0, 0, false
	}
	return a.store.CachePath(name), w, h, true
}

func (a *App) followLink(href string) {
	rest, ok := strings.CutPrefix(href, "book:")
	if !ok {
		return
	}
	num, anchor, _ := strings.Cut(rest, "#")
	var ch int
	if _, err := fmt.Sscanf(num, "%d", &ch); err != nil {
		return
	}
	a.mu.Lock()
	b := a.cur
	k := 0
	if b != nil {
		k = b.anchorChar(ch, anchor)
	}
	a.mu.Unlock()
	if b != nil {
		a.showChapter(ch, k, 1)
	}
}

func (a *App) setPosition(ch, k int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	b := a.cur
	if b == nil || ch < 0 || ch >= b.chapters() || k < 0 {
		return
	}
	st := a.stateLocked(b.id)
	if st.Chapter == ch && st.Char == k {
		return
	}
	st.Chapter, st.Char, st.Percent, st.Read = ch, k, b.percent(ch, k), time.Now().Unix()
	a.saveLocked()
	if a.cfg.Sync.loggedIn() && !a.cfg.Settings.ManualSync {
		if a.pushTimer != nil {
			a.pushTimer.Stop()
		}
		a.pushTimer = time.AfterFunc(pushDelay, func() { a.pushNow(false) })
	}
}

// ---------------------------------------------------------------- sync

func (a *App) setSyncStatus(err error) {
	a.mu.Lock()
	old := a.syncStatus
	a.syncStatus = ""
	if err != nil {
		a.syncStatus = err.Error()
	}
	changed := old != a.syncStatus
	a.mu.Unlock()
	if changed {
		a.sendState()
	}
}

// pushNow sends the open book's position (manual: also when unchanged, and
// say so).
func (a *App) pushNow(manual bool) {
	a.mu.Lock()
	b := a.cur
	if a.pushTimer != nil {
		a.pushTimer.Stop()
	}
	if b == nil || !a.cfg.Sync.loggedIn() || (a.cfg.Settings.ManualSync && !manual) {
		a.mu.Unlock()
		if manual && b == nil {
			a.toast("Open a book first.")
		}
		return
	}
	st := a.stateLocked(b.id)
	xp, pct := b.xpointer(st.Chapter, st.Char), b.percent(st.Chapter, st.Char)
	if xp == st.Pushed && !manual {
		a.mu.Unlock()
		return
	}
	login, dev := a.cfg.Sync, a.cfg.DeviceID
	a.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	ts, err := syncPut(ctx, login, progress{Document: b.digest, Progress: xp, Percentage: pct, Device: syncDeviceName, DeviceID: dev})
	a.setSyncStatus(err)
	if err != nil {
		log.Printf("sync push %s: %v", b.id, err)
		if manual {
			a.toast(err.Error())
		}
		return
	}
	log.Printf("sync push %s: %s (%.1f%%)", b.id, xp, pct*100)
	a.mu.Lock()
	st.Pushed, st.PushedAt = xp, ts
	a.saveLocked()
	a.mu.Unlock()
	if manual {
		a.toast(fmt.Sprintf("Position sent to the sync server (%d%%).", int(pct*100+0.5)))
	}
}

// pull asks the sync server for the open book's position. A newer one from
// another device is sent to the frontend; manual: otherwise ours is sent.
// force: the server's position whatever it is (older, or this device's).
func (a *App) pull(manual, force bool) {
	a.mu.Lock()
	b := a.cur
	if b == nil || !a.cfg.Sync.loggedIn() || (a.cfg.Settings.ManualSync && !manual) {
		a.mu.Unlock()
		switch {
		case manual && b == nil:
			a.toast("Open a book first.")
		case manual:
			a.toast("Log in to a sync server in Settings first.")
		}
		return
	}
	login, dev := a.cfg.Sync, a.cfg.DeviceID
	st := *a.stateLocked(b.id)
	a.mu.Unlock()
	if manual {
		a.busy(true, "Syncing …")
		defer a.busy(false, "")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	p, err := syncGet(ctx, login, b.digest)
	a.setSyncStatus(err)
	if err != nil {
		log.Printf("sync pull %s: %v", b.id, err)
		if manual {
			a.toast(err.Error())
		}
		return
	}
	if force && p == nil {
		a.toast("The sync server has no position for this book yet.")
		return
	}
	newer := p != nil && p.DeviceID != dev && p.Timestamp > st.Read && p.Progress != st.Pushed
	if !newer && !force {
		if manual {
			a.pushNow(true)
		}
		return
	}
	a.mu.Lock()
	if a.cur != b {
		a.mu.Unlock()
		return
	}
	ch, k, ok := b.fromXPointer(p.Progress)
	how := "position"
	if !ok {
		ch, k = b.fromPercent(p.Percentage)
		how = "percentage"
	}
	a.mu.Unlock()
	log.Printf("sync pull %s: %s from %s (%s) -> chapter %d char %d", b.id, p.Progress, p.Device, how, ch, k)
	if !force && ch == st.Chapter && abs(k-st.Char) < 40 {
		if manual {
			a.toast("Already at the latest position.")
		}
		return
	}
	dev2 := p.Device
	if p.DeviceID == dev {
		dev2 = "this tablet"
	} else if dev2 == "" {
		dev2 = "another device"
	}
	a.send(resSync, map[string]any{"chapter": ch, "char": k, "percent": b.percent(ch, k), "device": dev2, "manual": manual})
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

func (a *App) login(server, user, pw string, keep, register bool) {
	srv, err := checkServer(server)
	user = strings.TrimSpace(user)
	if err == nil && user == "" {
		err = errors.New("Enter a user name.")
	}
	a.mu.Lock()
	l := SyncLogin{Server: srv, Username: user}
	if keep && a.cfg.Sync.Username == user && a.cfg.Sync.loggedIn() {
		l.Userkey = a.cfg.Sync.Userkey // the saved login, password not typed again
	} else if pw != "" {
		l.Userkey = passwordKey(pw)
	} else if err == nil {
		err = errors.New("Enter the password.")
	}
	a.mu.Unlock()
	if err == nil {
		a.busy(true, "Checking the login …")
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		err = syncAuth(ctx, l, register)
		cancel()
		a.busy(false, "")
	}
	if err != nil {
		a.toast(err.Error())
		return
	}
	a.mu.Lock()
	a.cfg.Sync = l
	a.syncStatus = ""
	a.saveLocked()
	a.mu.Unlock()
	a.sendState()
	a.toast("Logged in as " + user + ".")
	go a.pull(false, false)
}
