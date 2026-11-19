package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/method-marco/rm-browser/backend/internal/sandbox"
	"github.com/method-marco/rm-browser/backend/internal/store"
)

// Books' sandbox: the library (xochitl's documents, owned by root) is
// readable through its handle and nothing in it can be changed. Run as
// root (e.g. in Docker, as `make docker` does) to test every layer; the
// sandbox can't be undone, so it runs in a child process.
func TestBooksSandbox(t *testing.T) {
	if os.Getenv("RM_BOOKS_SANDBOX_CHILD") != "" {
		booksSandboxChild()
		return
	}
	if os.Geteuid() != 0 {
		t.Skip("needs root (chroot, user switch, a root-owned library)")
	}
	for _, landlock := range []string{"1", "0"} {
		dir := t.TempDir()
		_ = os.Chmod(dir, 0o755)
		cmd := exec.Command(os.Args[0], "-test.run=^TestBooksSandbox$", "-test.v")
		cmd.Env = append(os.Environ(), "RM_BOOKS_SANDBOX_CHILD="+dir, "RM_BOOKS_SANDBOX_LANDLOCK="+landlock)
		out, err := cmd.CombinedOutput()
		t.Logf("child output (Landlock %s):\n%s", landlock, out)
		if err != nil || !strings.Contains(string(out), "SANDBOX-OK") {
			t.Fatalf("child failed: %v", err)
		}
	}
}

func booksSandboxChild() {
	dir := os.Getenv("RM_BOOKS_SANDBOX_CHILD")
	sandbox.UseLandlock = os.Getenv("RM_BOOKS_SANDBOX_LANDLOCK") != "0"
	xochitl := filepath.Join(dir, "xochitl")
	_ = os.MkdirAll(xochitl, 0o755)
	_ = os.WriteFile(filepath.Join(xochitl, "doc.metadata"), []byte(`{"visibleName":"Library","type":"CollectionType","parent":""}`), 0o644)
	_ = os.WriteFile(filepath.Join(xochitl, "notes.rm"), []byte("a notebook"), 0o644)
	cfg, cache := filepath.Join(dir, "cfg"), filepath.Join(dir, "cache")
	st, err := store.Open(cfg, cache)
	if err != nil {
		fail("store: %v", err)
	}
	lib, err := openLibrary(xochitl)
	if err != nil {
		fail("library: %v", err)
	}
	layers, err := sandbox.Enter(sandbox.Options{Jail: filepath.Join(dir, "jail"), Dirs: []string{cfg, cache}, ReadOnly: []string{xochitl}})
	if err != nil {
		fail("sandbox: %v", err)
	}
	println("layers:", layers)
	if _, found, err := lib.scan(); err != nil || !found {
		fail("scan inside the sandbox: %v %v", found, err)
	}
	if b, err := lib.root.ReadFile("notes.rm"); err != nil || string(b) != "a notebook" {
		fail("read the library: %v", err)
	}
	// the library can't be changed
	if err := lib.root.WriteFile("notes.rm", []byte("x"), 0o644); err == nil {
		fail("overwrote a file in the library")
	}
	if err := lib.root.WriteFile("new.epub", []byte("x"), 0o644); err == nil {
		fail("created a file in the library")
	}
	if err := lib.root.Remove("notes.rm"); err == nil {
		fail("deleted a file in the library")
	}
	if err := lib.root.Rename("notes.rm", "moved.rm"); err == nil {
		fail("renamed a file in the library")
	}
	if _, err := lib.root.Open("../cfg"); err == nil {
		fail("left the library through its handle")
	}
	// its own folders still work
	if err := st.WriteConfig([]byte("{}")); err != nil {
		fail("config: %v", err)
	}
	if err := st.Cache.WriteFile("img-x", []byte("x"), 0o644); err != nil {
		fail("cache: %v", err)
	}
	for _, p := range []string{xochitl + "/notes.rm", "/etc/passwd", "/proc/self/status"} {
		if _, err := os.Stat(p); err == nil {
			fail("%s is visible in the jail", p)
		}
	}
	println("SANDBOX-OK")
	os.Exit(0)
}

func fail(f string, args ...any) {
	println("FAIL: " + fmt.Sprintf(f, args...))
	os.Exit(1)
}
