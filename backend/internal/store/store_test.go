package store

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStore(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "cfg"), filepath.Join(dir, "cache"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.WriteConfig([]byte(`{"a":1}`)); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "cfg", "config.json")); string(b) != `{"a":1}` {
		t.Errorf("config = %s", b)
	}
	_ = s.Cache.MkdirAll("img/sub", 0o700)
	_ = s.Cache.WriteFile("img/sub/x.jpg", []byte("x"), 0o644)
	_ = s.Cache.WriteFile("y", []byte("y"), 0o644)
	if err := s.ClearCache(); err != nil {
		t.Fatal(err)
	}
	if left, _ := os.ReadDir(filepath.Join(dir, "cache")); len(left) != 0 {
		t.Errorf("cache not empty: %v", left)
	}
	if _, err := os.Stat(filepath.Join(dir, "cfg", "config.json")); err != nil {
		t.Error("clearing the cache must not touch the config")
	}
	// os.Root keeps writes inside the folder
	if err := s.Cache.WriteFile("../escape", []byte("x"), 0o644); err == nil {
		t.Error("write outside the cache folder succeeded")
	}
}

func TestPruneImages(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "cfg"), filepath.Join(dir, "cache"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.PruneImages(10, 5); err != nil { // no img folder yet
		t.Fatal(err)
	}
	_ = s.Cache.MkdirAll("img", 0o700)
	base := time.Now().Add(-time.Hour)
	for i := 0; i < 10; i++ {
		name := fmt.Sprintf("img/%d.jpg", i)
		_ = s.Cache.WriteFile(name, make([]byte, 100), 0o644)
		_ = s.Cache.Chtimes(name, base.Add(time.Duration(i)*time.Minute), base.Add(time.Duration(i)*time.Minute))
	}
	if err := s.PruneImages(900, 500); err != nil {
		t.Fatal(err)
	}
	left, _ := os.ReadDir(filepath.Join(dir, "cache", "img"))
	if len(left) != 5 || left[0].Name() != "5.jpg" {
		t.Errorf("left: %v", left)
	}
}
