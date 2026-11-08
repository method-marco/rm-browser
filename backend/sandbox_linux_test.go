package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/elastic/go-seccomp-bpf/arch"
)

// The sandbox can't be undone, so it is tested in a child process: the
// test binary runs itself with RM_BROWSER_SANDBOX_CHILD set. Run as root
// (e.g. in Docker, as `make docker` does) to test every layer.
func TestSandbox(t *testing.T) {
	if os.Getenv("RM_BROWSER_SANDBOX_CHILD") != "" {
		sandboxChild()
		return
	}
	if os.Geteuid() != 0 {
		t.Log("not root: chroot and the user switch are not tested")
	}
	for _, landlock := range []string{"1", "0"} {
		dir := t.TempDir()
		_ = os.Chmod(dir, 0o755)
		cmd := exec.Command(os.Args[0], "-test.run=^TestSandbox$", "-test.v")
		cmd.Env = append(os.Environ(), "RM_BROWSER_SANDBOX_CHILD="+dir, "RM_BROWSER_SANDBOX_LANDLOCK="+landlock)
		out, err := cmd.CombinedOutput()
		t.Logf("child output (Landlock %s):\n%s", landlock, out)
		if err != nil {
			t.Fatalf("child failed: %v", err)
		}
		if !strings.Contains(string(out), "SANDBOX-OK") {
			t.Fatal("child did not finish")
		}
	}
}

func sandboxChild() {
	dir := os.Getenv("RM_BROWSER_SANDBOX_CHILD")
	cfg, cache, jail := filepath.Join(dir, "cfg"), filepath.Join(dir, "cache"), filepath.Join(dir, "jail")
	outside := filepath.Join(dir, "outside")
	_ = os.MkdirAll(outside, 0o777)
	_ = os.WriteFile(filepath.Join(outside, "secret"), []byte("x"), 0o644)
	wasRoot := os.Geteuid() == 0
	useLandlock = os.Getenv("RM_BROWSER_SANDBOX_LANDLOCK") != "0"
	store, err := openStore(cfg, cache)
	if err != nil {
		fail("openStore: %v", err)
	}
	layers, err := enterSandbox(jail, cfg, cache)
	if err != nil {
		fail("enterSandbox: %v", err)
	}
	println("layers:", layers)

	// allowed: own folders (through the handles), DNS files, IP sockets
	if err := store.writeConfig([]byte("{}")); err != nil {
		fail("config write: %v", err)
	}
	if err := store.cache.WriteFile("x", []byte("x"), 0o644); err != nil {
		fail("cache write: %v", err)
	}
	if _, err := os.ReadFile("/etc/resolv.conf"); err != nil {
		fail("reading /etc/resolv.conf (DNS) failed: %v", err)
	}
	if fd, err := syscall.Socket(syscall.AF_INET, syscall.SOCK_STREAM, 0); err != nil {
		fail("IPv4 socket: %v", err)
	} else {
		syscall.Close(fd)
	}
	if fd, err := syscall.Socket(syscall.AF_INET6, syscall.SOCK_DGRAM, 0); err != nil {
		fail("IPv6 socket: %v", err)
	} else {
		syscall.Close(fd)
	}

	// blocked by seccomp: EPERM at the system call itself. If exec
	// worked, this process would be replaced and never print SANDBOX-OK.
	if err := syscall.Exec("/bin/true", []string{"true"}, nil); !blockedErr(err) {
		fail("execve: want EPERM/ENOSYS, got %v", err)
	}
	for name, dom := range map[string]int{"unix": syscall.AF_UNIX, "netlink": syscall.AF_NETLINK, "packet": syscall.AF_PACKET} {
		if _, err := syscall.Socket(dom, syscall.SOCK_DGRAM, 0); err != syscall.EPERM {
			fail("%s socket: want EPERM, got %v", name, err)
		}
	}
	if _, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0); !blockedErr(err) {
		fail("socketpair: want EPERM, got %v", err)
	}
	if err := syscall.Kill(1, 0); err != syscall.EPERM {
		fail("kill(1): want EPERM, got %v", err)
	}
	if err := syscall.Kill(os.Getpid(), 0); err != nil {
		fail("kill(self, 0) must work (Go runtime): %v", err)
	}
	if err := syscall.Chdir("/"); !blockedErr(err) {
		fail("chdir: want EPERM, got %v", err)
	}
	if err := syscall.Chroot("/"); !blockedErr(err) {
		fail("chroot: want EPERM, got %v", err)
	}
	// (with the allowlist, prctl itself is blocked)
	if d, _, e := syscall.RawSyscall(syscall.SYS_PRCTL, syscall.PR_GET_DUMPABLE, 0, 0); e == 0 && d != 0 {
		fail("still dumpable")
	}
	if _, _, e := syscall.RawSyscall(425 /* io_uring_setup */, 1, 0, 0); !blockedErr(e) {
		fail("io_uring_setup: want EPERM, got %v", e)
	}

	// anything not on the allowlist never reaches the kernel (arm64)
	if strings.Contains(layers, "allowlist") {
		if _, _, e := syscall.RawSyscall(syscall.SYS_UNAME+1000, 0, 0, 0); e != syscall.ENOSYS {
			fail("unknown call: want ENOSYS, got %v", e)
		}
		if _, _, e := syscall.RawSyscall(syscall.SYS_SETXATTR, 0, 0, 0); e != syscall.ENOSYS {
			fail("setxattr (not allowed): want ENOSYS, got %v", e)
		}
		if _, _, e := syscall.RawSyscall(syscall.SYS_PERSONALITY, 0xffffffff, 0, 0); e != syscall.ENOSYS {
			fail("personality: want ENOSYS, got %v", e)
		}
	}

	if wasRoot {
		if !strings.Contains(layers, "chroot") || !strings.Contains(layers, "unprivileged user") {
			fail("layers: %s", layers)
		}
		// the rest of the file system is gone
		for _, p := range []string{"/etc/passwd", "/bin/true", outside + "/secret", "/proc/self/status"} {
			if _, err := os.Stat(p); err == nil {
				fail("%s is visible in the jail", p)
			}
		}
		if err := os.WriteFile("/tmp/x", []byte("x"), 0o644); err == nil {
			fail("could write /tmp/x")
		}
		if err := os.WriteFile("/etc/hosts", []byte("x"), 0o644); err == nil {
			fail("could change the jail's /etc/hosts")
		}
		if err := syscall.Setuid(0); err == nil || os.Geteuid() == 0 {
			fail("got root back")
		}
		// open folder handles must not lead out of the jail
		if _, err := store.cfg.Open("../../outside/secret"); err == nil {
			fail("escaped the config folder through its handle")
		}
	}
	println("SANDBOX-OK")
	os.Exit(0)
}

func blockedErr(err error) bool { return err == syscall.EPERM || err == syscall.ENOSYS }

func fail(f string, args ...any) {
	println("FAIL: " + fmt.Sprintf(f, args...))
	os.Exit(1)
}

func TestAllowlistNames(t *testing.T) {
	for _, n := range allowedSyscalls {
		if _, ok := arch.AARCH64.SyscallNames[n]; !ok {
			t.Errorf("allowlist: %q is not an arm64 system call name", n)
		}
	}
	if _, err := allowList(); err != nil {
		t.Error(err)
	}
}
