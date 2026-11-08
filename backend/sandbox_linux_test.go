package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// The sandbox can't be undone, so it is tested in a child process: the
// test binary runs itself with RM_BROWSER_SANDBOX_CHILD set.
func TestSandbox(t *testing.T) {
	if os.Getenv("RM_BROWSER_SANDBOX_CHILD") != "" {
		sandboxChild()
		return
	}
	dir := t.TempDir()
	if os.Geteuid() == 0 {
		_ = os.Chmod(dir, 0o755)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestSandbox$", "-test.v")
	cmd.Env = append(os.Environ(), "RM_BROWSER_SANDBOX_CHILD="+dir)
	out, err := cmd.CombinedOutput()
	t.Logf("child output:\n%s", out)
	if err != nil {
		t.Fatalf("child failed: %v", err)
	}
	if !strings.Contains(string(out), "SANDBOX-OK") {
		t.Fatal("child did not finish")
	}
}

func sandboxChild() {
	dir := os.Getenv("RM_BROWSER_SANDBOX_CHILD")
	cfg, cache := filepath.Join(dir, "cfg"), filepath.Join(dir, "cache")
	outside := filepath.Join(dir, "outside")
	_ = os.MkdirAll(outside, 0o777)
	wasRoot := os.Geteuid() == 0
	store, err := openStore(cfg, cache)
	if err != nil {
		fail("openStore: %v", err)
	}
	layers, err := enterSandbox(cfg, cache)
	if err != nil {
		fail("enterSandbox: %v", err)
	}
	println("layers:", layers)

	// allowed: own folders
	if err := store.writeConfig([]byte("{}")); err != nil {
		fail("config write: %v", err)
	}
	if err := store.cache.WriteFile("x", []byte("x"), 0o644); err != nil {
		fail("cache write: %v", err)
	}
	if _, err := os.ReadFile("/etc/hosts"); err != nil {
		fail("reading /etc/hosts (DNS) failed: %v", err)
	}
	// blocked: starting programs
	if err := exec.Command("/bin/true").Run(); err == nil {
		fail("exec of /bin/true succeeded")
	} else {
		println("exec blocked:", err.Error())
	}
	// blocked by seccomp at the system call itself: EPERM (Landlock would
	// answer EACCES later). If exec worked, this process would be replaced
	// and never print SANDBOX-OK.
	if err := syscall.Exec("/bin/true", []string{"true"}, nil); err != syscall.EPERM {
		fail("execve: want EPERM from seccomp, got %v", err)
	}
	// blocked: getting root back
	if wasRoot {
		if err := syscall.Setuid(0); err == nil {
			fail("setuid(0) succeeded")
		}
		if os.Geteuid() == 0 {
			fail("still root")
		}
	}
	// blocked: writing outside (Landlock, if the kernel has it)
	err = os.WriteFile(filepath.Join(outside, "f"), []byte("x"), 0o644)
	if strings.Contains(layers, "Landlock") && err == nil {
		fail("write outside the sandbox folders succeeded")
	}
	println("write outside:", errString(err))
	println("SANDBOX-OK")
	os.Exit(0)
}

func errString(err error) string {
	if err == nil {
		return "allowed"
	}
	return err.Error()
}

func fail(f string, args ...any) {
	println("FAIL: " + fmt.Sprintf(f, args...))
	os.Exit(1)
}
