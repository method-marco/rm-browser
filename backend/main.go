// rm-browser backend: a distraction-free, reader-only web browser for
// reMarkable tablets. It fetches pages over HTTPS, extracts the article in
// reader mode and hands sanitized rich text to the QML frontend, which
// shows it page by page. It runs sandboxed (see sandbox_linux.go).
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var version = "dev"

// Message types frontend -> backend.
const (
	reqInit       = 1  // {width, height}: viewport for images; answers state, then the current entry
	reqOpen       = 2  // {input}: an address or a search query
	reqSearchMore = 3  // next result page of the current search
	reqSuggest    = 4  // {q}: autocomplete
	reqBack       = 5  // history back
	reqForward    = 6  // history forward
	reqPosition   = 7  // {page}: reading position of the current entry
	reqClearData  = 8  // delete history, cookies, cache
	reqSettings   = 9  // {fontSize, serif, safeSearch, noImages, greyImages}
	reqHome       = 10 // go to the start screen
	reqReload     = 11 // load the current entry again, bypassing the cache
	reqCancel     = 12 // stop loading
	reqViewport   = 13 // {width, height}
	reqClosed     = 14 // the app was closed: forget cookies and connections
)

// Message types backend -> frontend.
const (
	resState   = 101
	resError   = 102 // {message, retry}
	resPage    = 103
	resSearch  = 104
	resSuggest = 105
	resBusy    = 106
	resHome    = 107
	resInfo    = 108 // {message}
)

type Settings struct {
	FontSize   int    `json:"fontSize"` // reader text size in pixels
	Serif      bool   `json:"serif"`
	SafeSearch string `json:"safeSearch"` // moderate | strict | off
	NoImages   bool   `json:"noImages"`
	GreyImages bool   `json:"greyImages"` // default: colour
	AllowHTTP  bool   `json:"allowHTTP"`  // default: HTTPS only
}

// Entry is one step in the tab's history.
type Entry struct {
	Kind  string `json:"kind"` // home | page | search
	URL   string `json:"url,omitempty"`
	Query string `json:"query,omitempty"`
	Title string `json:"title,omitempty"`
	Page  int    `json:"page"` // reading position (screen index)
}

type HistoryItem struct {
	URL   string `json:"url,omitempty"`
	Query string `json:"query,omitempty"`
	Title string `json:"title,omitempty"`
	Time  int64  `json:"time"`
}

type Config struct {
	Settings Settings      `json:"settings"`
	History  []HistoryItem `json:"history"` // newest first
	Stack    []Entry       `json:"stack"`   // the tab: back/forward list
	Pos      int           `json:"pos"`
}

const (
	maxHistory = 300
	maxStack   = 50
	pageCache  = 12

	maxImageCache  = 150 << 20 // converted images on disk: prune above this ...
	keepImageCache = 100 << 20 // ... down to this, oldest first
)

type App struct {
	al    *AppLoad
	store *Store

	mu      sync.Mutex // guards everything below
	cfg     Config
	http    *http.Client
	vpW     int // viewport for images (pixels)
	vpH     int
	sandbox string

	cache     map[string]*cachedPage // built pages by URL+viewport
	cacheKeys []string

	loadCancel  context.CancelFunc
	loadGen     int
	search      *SearchPage // current search results (all pages loaded so far)
	searchQ     string
	suggestStop context.CancelFunc

	sendHook func(typ int, v any) // tests: receives messages instead of AppLoad
}

type cachedPage struct {
	page *Page
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: entry <appload-socket>")
		os.Exit(2)
	}
	al, err := ConnectAppLoad(os.Args[1])
	if err != nil {
		log.Fatal(err)
	}
	defer al.Close()

	cfgDir, cacheDir := configDir(), cacheDirPath()
	// Open our folders before the sandbox takes away access to /home/root.
	store, err := openStore(cfgDir, cacheDir)
	if err != nil {
		log.Fatal(err)
	}
	sandbox, err := enterSandbox(jailDir(cacheDir), cfgDir, cacheDir)
	if err != nil {
		// Never browse unsandboxed.
		log.Printf("sandbox: %v", err)
		_ = al.Send(resError, mustJSON(map[string]any{"message": "Cannot start the sandbox, so the browser stays off: " + err.Error()}))
		for {
			if _, _, err := al.Receive(); err != nil {
				return
			}
		}
	}
	log.Printf("rm-browser %s, sandbox: %s", version, sandbox)

	app := newApp(al, store)
	app.sandbox = sandbox
	for {
		typ, contents, err := al.Receive()
		if err != nil {
			log.Printf("receive: %v", err)
			return
		}
		switch typ {
		case msgSystemTerminate:
			return
		case msgSystemNewCoordinator, msgSystemLostCoordinator:
			continue
		}
		app.handle(int(typ), contents)
	}
}

func newApp(al *AppLoad, store *Store) *App {
	a := &App{al: al, store: store, http: newHTTPClient(), cache: map[string]*cachedPage{}, vpW: 860, vpH: 1400}
	a.loadConfig()
	return a
}

func homeDir() string {
	if h, err := os.UserHomeDir(); err == nil {
		return h
	}
	return "/home/root"
}

func configDir() string {
	if p := os.Getenv("RM_BROWSER_CONFIG"); p != "" {
		return p
	}
	return filepath.Join(homeDir(), ".config", "rm-browser")
}

func cacheDirPath() string {
	if p := os.Getenv("RM_BROWSER_CACHE"); p != "" {
		return p
	}
	return filepath.Join(homeDir(), ".cache", "rm-browser")
}

// jailDir is the empty folder the backend chroots into (next to the cache).
func jailDir(cacheDir string) string {
	if p := os.Getenv("RM_BROWSER_JAIL"); p != "" {
		return p
	}
	return filepath.Join(filepath.Dir(cacheDir), "rm-browser-jail")
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
		if errors.Is(err, ErrTooLarge) && typ == resPage {
			_ = a.al.Send(resError, mustJSON(map[string]any{"message": "This page is too large to show.", "retry": false}))
		}
	}
}

func (a *App) sendError(err error, retry bool) {
	if errors.Is(err, context.Canceled) {
		return
	}
	a.send(resError, map[string]any{"message": err.Error(), "retry": retry})
}

func (a *App) busy(on bool, text string) { a.send(resBusy, map[string]any{"busy": on, "text": text}) }

func (a *App) client() *http.Client {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.http
}

// ---------------------------------------------------------------- config

func (a *App) loadConfig() {
	if b, err := a.store.cfg.ReadFile("config.json"); err == nil {
		_ = json.Unmarshal(b, &a.cfg)
	}
	s := &a.cfg.Settings
	if s.FontSize < 20 || s.FontSize > 60 {
		s.FontSize = 32
	}
	if s.SafeSearch == "" {
		s.SafeSearch = "moderate"
	}
	if a.cfg.Pos < 0 || a.cfg.Pos >= len(a.cfg.Stack) {
		a.cfg.Pos = len(a.cfg.Stack) - 1
	}
	allowHTTP.Store(s.AllowHTTP)
}

// saveLocked writes the config; a.mu must be held.
func (a *App) saveLocked() {
	b, err := json.MarshalIndent(a.cfg, "", " ")
	if err != nil {
		return
	}
	if err := a.store.writeConfig(b); err != nil {
		log.Printf("save config: %v", err)
	}
}

func (a *App) stateLocked() map[string]any {
	hist := a.cfg.History
	if hist == nil {
		hist = []HistoryItem{}
	}
	return map[string]any{
		"settings":   a.cfg.Settings,
		"history":    hist,
		"canBack":    a.cfg.Pos > 0,
		"canForward": a.cfg.Pos >= 0 && a.cfg.Pos < len(a.cfg.Stack)-1,
		"version":    version,
		"sandbox":    a.sandbox,
		"imageDir":   a.store.cachePath("img"),
	}
}

func (a *App) sendState() {
	a.mu.Lock()
	st := a.stateLocked()
	a.mu.Unlock()
	a.send(resState, st)
}

func (a *App) current() (Entry, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cfg.Pos < 0 || a.cfg.Pos >= len(a.cfg.Stack) {
		return Entry{}, false
	}
	return a.cfg.Stack[a.cfg.Pos], true
}

// push adds an entry after the current one (dropping the forward list).
func (a *App) push(e Entry) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cfg.Pos >= 0 && a.cfg.Pos < len(a.cfg.Stack) {
		cur := a.cfg.Stack[a.cfg.Pos]
		if cur.Kind == e.Kind && cur.URL == e.URL && cur.Query == e.Query {
			a.cfg.Stack = a.cfg.Stack[:a.cfg.Pos+1]
			a.cfg.Stack[a.cfg.Pos].Page = 0
			a.saveLocked()
			return
		}
	}
	a.cfg.Stack = append(a.cfg.Stack[:a.cfg.Pos+1], e)
	if len(a.cfg.Stack) > maxStack {
		a.cfg.Stack = a.cfg.Stack[len(a.cfg.Stack)-maxStack:]
	}
	a.cfg.Pos = len(a.cfg.Stack) - 1
	a.saveLocked()
}

func (a *App) addHistory(it HistoryItem) {
	a.mu.Lock()
	defer a.mu.Unlock()
	it.Time = time.Now().Unix()
	out := []HistoryItem{it}
	for _, h := range a.cfg.History {
		if (it.URL != "" && h.URL == it.URL) || (it.Query != "" && h.Query == it.Query) {
			continue
		}
		out = append(out, h)
		if len(out) >= maxHistory {
			break
		}
	}
	a.cfg.History = out
	a.saveLocked()
}

// ---------------------------------------------------------------- messages

func (a *App) handle(typ int, contents string) {
	var req struct {
		Input      string  `json:"input"`
		Q          string  `json:"q"`
		Page       int     `json:"page"`
		Width      int     `json:"width"`
		Height     int     `json:"height"`
		FontSize   *int    `json:"fontSize"`
		Serif      *bool   `json:"serif"`
		SafeSearch *string `json:"safeSearch"`
		NoImages   *bool   `json:"noImages"`
		GreyImages *bool   `json:"greyImages"`
		AllowHTTP  *bool   `json:"allowHTTP"`
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
		if req.Width > 0 && req.Height > 0 {
			a.mu.Lock()
			changed = a.vpW != req.Width || a.vpH != req.Height
			a.vpW, a.vpH = req.Width, req.Height
			a.mu.Unlock()
		}
		if typ == reqInit {
			a.newSession() // in case the frontend went away without saying so
			a.sendState()
			a.showCurrent(false)
		} else if e, ok := a.current(); changed && ok && e.Kind == "page" {
			a.showCurrent(false) // images are sized for the screen: rotate -> new sizes
		}
	case reqOpen:
		in := strings.TrimSpace(req.Input)
		if in == "" {
			return
		}
		if u := classifyInput(in); u != "" {
			a.push(Entry{Kind: "page", URL: u})
		} else {
			a.push(Entry{Kind: "search", Query: in})
		}
		a.sendState()
		a.showCurrent(false)
	case reqSearchMore:
		a.searchMore()
	case reqSuggest:
		a.suggest(req.Q)
	case reqBack, reqForward:
		a.mu.Lock()
		np := a.cfg.Pos - 1
		if typ == reqForward {
			np = a.cfg.Pos + 1
		}
		ok := np >= 0 && np < len(a.cfg.Stack)
		if ok {
			a.cfg.Pos = np
			a.saveLocked()
		}
		a.mu.Unlock()
		if ok {
			a.sendState()
			a.showCurrent(false)
		}
	case reqPosition:
		a.mu.Lock()
		if a.cfg.Pos >= 0 && a.cfg.Pos < len(a.cfg.Stack) && a.cfg.Stack[a.cfg.Pos].Page != req.Page {
			a.cfg.Stack[a.cfg.Pos].Page = req.Page
			a.saveLocked()
		}
		a.mu.Unlock()
	case reqClearData:
		a.clearData()
	case reqClosed:
		a.mu.Lock()
		if a.loadCancel != nil {
			a.loadCancel()
		}
		a.mu.Unlock()
		a.newSession()
	case reqSettings:
		a.mu.Lock()
		s := &a.cfg.Settings
		if req.FontSize != nil && *req.FontSize >= 20 && *req.FontSize <= 60 {
			s.FontSize = *req.FontSize
		}
		if req.Serif != nil {
			s.Serif = *req.Serif
		}
		if req.SafeSearch != nil {
			switch *req.SafeSearch {
			case "moderate", "strict", "off":
				s.SafeSearch = *req.SafeSearch
			}
		}
		imagesChanged := req.NoImages != nil && *req.NoImages != s.NoImages
		if req.NoImages != nil {
			s.NoImages = *req.NoImages
		}
		if req.GreyImages != nil && *req.GreyImages != s.GreyImages {
			s.GreyImages = *req.GreyImages
			imagesChanged = true
		}
		reload := false
		if req.AllowHTTP != nil && *req.AllowHTTP != s.AllowHTTP {
			s.AllowHTTP = *req.AllowHTTP
			allowHTTP.Store(s.AllowHTTP)
			reload = true
		}
		a.saveLocked()
		a.mu.Unlock()
		a.sendState()
		if reload {
			a.mu.Lock() // pages were built under the other rule
			a.cache, a.cacheKeys = map[string]*cachedPage{}, nil
			a.mu.Unlock()
		}
		if imagesChanged {
			a.showCurrent(false)
		}
	case reqHome:
		a.push(Entry{Kind: "home"})
		a.sendState()
		a.showCurrent(false)
	case reqReload:
		a.showCurrent(true)
	case reqCancel:
		a.mu.Lock()
		if a.loadCancel != nil {
			a.loadCancel()
		}
		a.mu.Unlock()
		a.busy(false, "")
	}
}

// startLoad cancels any running load and returns a context for a new one.
func (a *App) startLoad() (context.Context, int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.loadCancel != nil {
		a.loadCancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.loadCancel = cancel
	a.loadGen++
	return ctx, a.loadGen
}

func (a *App) isCurrentLoad(gen int) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return gen == a.loadGen
}

// showCurrent loads and shows the current history entry.
func (a *App) showCurrent(reload bool) {
	e, ok := a.current()
	if !ok || e.Kind == "home" {
		a.startLoad()
		a.busy(false, "")
		a.send(resHome, map[string]any{})
		return
	}
	ctx, gen := a.startLoad()
	go func() {
		defer func() {
			if a.isCurrentLoad(gen) {
				a.busy(false, "")
			}
		}()
		switch e.Kind {
		case "search":
			a.runSearch(ctx, gen, e, reload)
		case "page":
			a.loadPage(ctx, gen, e, reload)
		}
	}()
}

func (a *App) runSearch(ctx context.Context, gen int, e Entry, reload bool) {
	a.mu.Lock()
	cached := a.search != nil && a.searchQ == e.Query && !reload
	sp := a.search
	safe := a.cfg.Settings.SafeSearch
	a.mu.Unlock()
	if !cached {
		a.busy(true, "Searching for "+e.Query+" …")
		var err error
		sp, err = ddgSearch(ctx, a.client(), e.Query, nil, safe)
		if !a.isCurrentLoad(gen) {
			return
		}
		if err != nil {
			a.sendError(err, true)
			return
		}
		a.mu.Lock()
		a.search, a.searchQ = sp, e.Query
		a.mu.Unlock()
		a.addHistory(HistoryItem{Query: e.Query})
		a.sendState()
	}
	a.send(resSearch, map[string]any{"query": e.Query, "results": sp.Results, "hasMore": sp.Next != nil,
		"append": false, "page": e.Page})
}

func (a *App) searchMore() {
	a.mu.Lock()
	sp := a.search
	safe := a.cfg.Settings.SafeSearch
	a.mu.Unlock()
	if sp == nil || sp.Next == nil {
		return
	}
	ctx, gen := a.startLoad()
	go func() {
		defer func() {
			if a.isCurrentLoad(gen) {
				a.busy(false, "")
			}
		}()
		a.busy(true, "Loading more results …")
		more, err := ddgSearch(ctx, a.client(), sp.Query, sp.Next, safe)
		if !a.isCurrentLoad(gen) {
			return
		}
		if err != nil {
			a.sendError(err, false)
			return
		}
		a.mu.Lock()
		if a.search != sp {
			a.mu.Unlock()
			return
		}
		seen := map[string]bool{}
		for _, r := range sp.Results {
			seen[r.URL] = true
		}
		var added []SearchResult
		for _, r := range more.Results {
			if !seen[r.URL] {
				added = append(added, r)
			}
		}
		merged := &SearchPage{Query: sp.Query, Results: append(append([]SearchResult{}, sp.Results...), added...), Next: more.Next}
		if len(added) == 0 {
			merged.Next = nil
		}
		a.search = merged
		a.mu.Unlock()
		if added == nil {
			added = []SearchResult{}
		}
		a.send(resSearch, map[string]any{"query": sp.Query, "results": added, "hasMore": merged.Next != nil, "append": true})
	}()
}

func (a *App) loadPage(ctx context.Context, gen int, e Entry, reload bool) {
	a.mu.Lock()
	vw, vh := a.vpW, a.vpH
	noImages := a.cfg.Settings.NoImages
	grey := a.cfg.Settings.GreyImages
	key := fmt.Sprintf("%s|%d|%d|%v|%v", e.URL, vw, vh, noImages, grey)
	cp := a.cache[key]
	a.mu.Unlock()

	if cp == nil || reload {
		u, err := url.Parse(e.URL)
		if err != nil {
			a.sendError(fmt.Errorf("invalid address"), false)
			return
		}
		a.busy(true, "Loading "+u.Hostname()+" …")
		target := e.URL
		upgraded := false
		if u.Scheme == "http" && !allowHTTP.Load() {
			target, upgraded = httpsURL(e.URL), true // HTTPS-only: try the secure version
		}
		req, err := newRequest(ctx, http.MethodGet, target, nil)
		if err != nil {
			a.sendError(err, false)
			return
		}
		f, err := fetch(ctx, a.client(), req, maxPageBytes)
		if !a.isCurrentLoad(gen) {
			return
		}
		if err != nil {
			if upgraded && !errors.Is(err, context.Canceled) && !errors.Is(err, errLocalNetwork) {
				log.Printf("https upgrade of %s: %v", u.Hostname(), err)
				a.sendError(errInsecure, false)
				return
			}
			a.sendError(err, !errors.Is(err, errInsecure))
			return
		}
		var imgs ImageFunc
		if !noImages {
			imgs = func(ctx context.Context, src string) (string, int, int, bool) {
				return a.prepareImage(ctx, src, vw, vh, grey)
			}
		}
		a.busy(true, "Preparing "+f.URL.Hostname()+" …")
		pg, err := buildPage(ctx, f, imgs, vw)
		if !a.isCurrentLoad(gen) {
			return
		}
		if err != nil {
			a.sendError(err, true)
			return
		}
		cp = &cachedPage{page: pg}
		if err := a.store.pruneImages(maxImageCache, keepImageCache); err != nil {
			log.Printf("prune images: %v", err)
		}
		a.mu.Lock()
		if _, ok := a.cache[key]; !ok {
			a.cacheKeys = append(a.cacheKeys, key)
		}
		a.cache[key] = cp
		for len(a.cacheKeys) > pageCache {
			delete(a.cache, a.cacheKeys[0])
			a.cacheKeys = a.cacheKeys[1:]
		}
		// remember the title (and the final address after redirects)
		if a.cfg.Pos >= 0 && a.cfg.Pos < len(a.cfg.Stack) && a.cfg.Stack[a.cfg.Pos].URL == e.URL {
			a.cfg.Stack[a.cfg.Pos].Title = pg.Title
		}
		a.mu.Unlock()
		a.addHistory(HistoryItem{URL: pg.URL, Title: pg.Title})
		a.sendState()
	}
	if !a.isCurrentLoad(gen) {
		return
	}
	a.send(resPage, map[string]any{"page": cp.page, "position": e.Page})
}

func (a *App) suggest(q string) {
	q = strings.TrimSpace(q)
	a.mu.Lock()
	if a.suggestStop != nil {
		a.suggestStop()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	a.suggestStop = cancel
	a.mu.Unlock()
	if q == "" || classifyInput(q) != "" && strings.Contains(q, "/") {
		a.send(resSuggest, map[string]any{"q": q, "items": []string{}})
		return
	}
	go func() {
		defer cancel()
		items, err := ddgSuggest(ctx, a.client(), q)
		if err != nil {
			return
		}
		a.send(resSuggest, map[string]any{"q": q, "items": items})
	}()
}

// newSession forgets cookies and open connections; it runs when the app
// is opened and closed, so sites can't recognise the tablet from one use
// of the browser to the next (AppLoad keeps the backend running).
func (a *App) newSession() {
	a.mu.Lock()
	old := a.http
	a.http = newHTTPClient()
	a.mu.Unlock()
	if t, ok := old.Transport.(*http.Transport); ok {
		t.CloseIdleConnections()
	}
}

// clearData forgets everything the browser stored: history, the tab's
// back/forward list, search results, cookies, open connections, cached
// pages and images. Settings (text size, ...) stay.
func (a *App) clearData() {
	a.mu.Lock()
	if a.loadCancel != nil {
		a.loadCancel()
	}
	a.loadGen++
	old := a.http
	a.http = newHTTPClient()
	a.cfg.History = nil
	a.cfg.Stack = nil
	a.cfg.Pos = -1
	a.search, a.searchQ = nil, ""
	a.cache = map[string]*cachedPage{}
	a.cacheKeys = nil
	a.saveLocked()
	a.mu.Unlock()
	if t, ok := old.Transport.(*http.Transport); ok {
		t.CloseIdleConnections()
	}
	err := a.store.clearCache()
	a.busy(false, "")
	a.sendState()
	a.send(resHome, map[string]any{})
	if err != nil {
		a.sendError(fmt.Errorf("browsing data cleared, but some cached files could not be deleted: %w", err), false)
		return
	}
	a.send(resInfo, map[string]any{"message": "Browsing data cleared: history, cookies and cache."})
}
