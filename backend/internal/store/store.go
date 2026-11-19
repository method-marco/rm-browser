// Package store holds an app backend's two folders (settings and cache).
package store

// Store holds handles to the backend's two folders, opened as root before
// the sandbox drops privileges (os.Root works relative to the open folder,
// so the sandbox user needs no access to the folders above them, and
// nothing can escape them through "..", absolute paths or symlinks).

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

type Store struct {
	Cfg      *os.Root // settings (config.json)
	Cache    *os.Root // converted images and other cached files
	cacheDir string
}

// Open creates and opens both folders.
func Open(cfgDir, cacheDir string) (*Store, error) {
	for _, d := range []string{cfgDir, cacheDir} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return nil, err
		}
	}
	c, err := os.OpenRoot(cfgDir)
	if err != nil {
		return nil, err
	}
	k, err := os.OpenRoot(cacheDir)
	if err != nil {
		c.Close()
		return nil, err
	}
	abs, err := filepath.Abs(cacheDir)
	if err != nil {
		abs = cacheDir
	}
	return &Store{Cfg: c, Cache: k, cacheDir: abs}, nil
}

// CachePath is the absolute path of a cache file, for the frontend.
func (s *Store) CachePath(name string) string { return filepath.Join(s.cacheDir, name) }

// WriteConfig replaces config.json atomically.
func (s *Store) WriteConfig(b []byte) error {
	if err := s.Cfg.WriteFile("config.json.tmp", b, 0o600); err != nil {
		return err
	}
	return s.Cfg.Rename("config.json.tmp", "config.json")
}

// ClearCache deletes everything in the cache folder.
func (s *Store) ClearCache() error {
	d, err := s.Cache.Open(".")
	if err != nil {
		return err
	}
	names, err := d.Readdirnames(-1)
	d.Close()
	if err != nil {
		return err
	}
	var errs []error
	for _, n := range names {
		if err := s.Cache.RemoveAll(n); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// PruneImages deletes the oldest converted images once they take more
// than max bytes, until they take at most keep.
func (s *Store) PruneImages(max, keep int64) error {
	d, err := s.Cache.Open("img")
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	entries, err := d.ReadDir(-1)
	d.Close()
	if err != nil {
		return err
	}
	type file struct {
		name string
		size int64
		mod  int64
	}
	var files []file
	var total int64
	for _, e := range entries {
		info, err := e.Info()
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		files = append(files, file{e.Name(), info.Size(), info.ModTime().UnixNano()})
		total += info.Size()
	}
	if total <= max {
		return nil
	}
	sort.Slice(files, func(i, j int) bool { return files[i].mod < files[j].mod })
	for _, f := range files {
		if total <= keep {
			break
		}
		if err := s.Cache.Remove("img/" + f.name); err == nil {
			total -= f.size
		}
	}
	return nil
}
