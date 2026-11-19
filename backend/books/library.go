package main

// The books come from the reMarkable's own library: every EPUB in the
// folder "Library" (and its subfolders). xochitl keeps each document as
// <id>.epub next to <id>.metadata (name, folder, deleted flag); folders
// are metadata files of type CollectionType. The backend reads them
// through a read-only handle opened before the sandbox (os.Root): it can
// read the library, never change it.

import (
	"encoding/json"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
)

const libraryFolder = "Library"

type metadata struct {
	VisibleName string `json:"visibleName"`
	Type        string `json:"type"`
	Parent      string `json:"parent"`
	Deleted     bool   `json:"deleted"`
}

type libBook struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Author string `json:"author,omitempty"`
	Folder string `json:"folder,omitempty"` // subfolder inside Library, "" = Library itself
}

type epubInfo struct {
	stamp         string
	title, author string
}

type library struct {
	root *os.Root // xochitl's document folder, read-only

	mu    sync.Mutex
	infos map[string]epubInfo // by document id
}

func xochitlDirPath() string {
	if p := os.Getenv("RM_BOOKS_XOCHITL"); p != "" {
		return p
	}
	return "/home/root/.local/share/remarkable/xochitl"
}

func openLibrary(dir string) (*library, error) {
	r, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	return &library{root: r, infos: map[string]epubInfo{}}, nil
}

// validID: a document id as xochitl makes them (no path tricks).
func validID(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	for _, c := range id {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
			return false
		}
	}
	return true
}

// scan lists the EPUBs in the Library folder; found=false if there is no
// such folder.
func (l *library) scan() (books []libBook, found bool, err error) {
	d, err := l.root.Open(".")
	if err != nil {
		return nil, false, err
	}
	names, err := d.Readdirnames(-1)
	d.Close()
	if err != nil {
		return nil, false, err
	}
	meta := map[string]metadata{}
	hasEPUB := map[string]bool{}
	for _, n := range names {
		switch {
		case strings.HasSuffix(n, ".metadata"):
			id := strings.TrimSuffix(n, ".metadata")
			if !validID(id) {
				continue
			}
			b, err := l.root.ReadFile(n)
			if err != nil {
				continue
			}
			var m metadata
			if json.Unmarshal(b, &m) == nil {
				meta[id] = m
			}
		case strings.HasSuffix(n, ".epub"):
			hasEPUB[strings.TrimSuffix(n, ".epub")] = true
		}
	}
	libs := map[string]bool{}
	for id, m := range meta {
		if m.Type == "CollectionType" && !m.Deleted && m.Parent != "trash" && strings.EqualFold(strings.TrimSpace(m.VisibleName), libraryFolder) {
			libs[id] = true
		}
	}
	// the folder path below Library, or ok=false if not inside it
	inLibrary := func(id string) (string, bool) {
		var path []string
		p := meta[id].Parent
		for depth := 0; depth < 32 && p != "" && p != "trash"; depth++ {
			if libs[p] {
				for i, j := 0, len(path)-1; i < j; i, j = i+1, j-1 {
					path[i], path[j] = path[j], path[i]
				}
				return strings.Join(path, " / "), true
			}
			m, ok := meta[p]
			if !ok || m.Deleted {
				return "", false
			}
			path = append(path, m.VisibleName)
			p = m.Parent
		}
		return "", false
	}
	for id, m := range meta {
		if m.Type != "DocumentType" || m.Deleted || !hasEPUB[id] {
			continue
		}
		folder, ok := inLibrary(id)
		if !ok {
			continue
		}
		info := l.info(id)
		title := strings.TrimSpace(m.VisibleName)
		if title == "" {
			title = info.title
		}
		books = append(books, libBook{ID: id, Title: title, Author: info.author, Folder: folder})
	}
	sort.Slice(books, func(i, j int) bool {
		if books[i].Folder != books[j].Folder {
			return books[i].Folder < books[j].Folder
		}
		return strings.ToLower(books[i].Title) < strings.ToLower(books[j].Title)
	})
	return books, len(libs) > 0, nil
}

// info: title and author from the EPUB itself (cached until the file changes).
func (l *library) info(id string) epubInfo {
	f, err := l.root.Open(id + ".epub")
	if err != nil {
		return epubInfo{}
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return epubInfo{}
	}
	stamp := st.ModTime().String() + "|" + strconv.FormatInt(st.Size(), 10)
	l.mu.Lock()
	if c, ok := l.infos[id]; ok && c.stamp == stamp {
		l.mu.Unlock()
		return c
	}
	l.mu.Unlock()
	t, a, _ := readEPUBInfo(f, st.Size())
	c := epubInfo{stamp: stamp, title: t, author: a}
	l.mu.Lock()
	l.infos[id] = c
	l.mu.Unlock()
	return c
}

// name: the document's name in the library.
func (l *library) name(id string) string {
	b, err := l.root.ReadFile(id + ".metadata")
	if err != nil {
		return ""
	}
	var m metadata
	_ = json.Unmarshal(b, &m)
	return strings.TrimSpace(m.VisibleName)
}
