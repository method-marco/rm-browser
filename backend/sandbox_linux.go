package main

// The backend is the only part of the browser that talks to the network
// and handles untrusted data, so it locks itself in before loading
// anything. AppLoad starts it as root; then, in this order:
//
//  1. Its two data folders are handed to the user nobody, and handles to
//     them are kept (os.Root, see store.go): from then on they are the
//     only way to its files.
//  2. Landlock (if the kernel has it): file access only to the jail and
//     the data folders; network only outgoing to web ports and DNS; no
//     signals to or abstract sockets of processes outside the sandbox.
//  3. chroot into an empty jail folder that holds nothing but copies of
//     resolv.conf and hosts (for DNS). The rest of the tablet's files no
//     longer exist for the process.
//  4. Drop root: user and group nobody (65534), no supplementary groups,
//     no capabilities, not dumpable (no core dumps; other processes of
//     nobody can't read its memory), limits on file size and open files.
//  5. no_new_privs and a seccomp filter: system calls a browser never
//     needs fail with EPERM: starting programs, other processes' memory,
//     signals to other processes, mounting, kernel modules, BPF,
//     io_uring, namespaces, changing user ids or directories, and every
//     socket except IPv4/IPv6 (no Unix sockets to D-Bus or systemd, no
//     netlink, no raw sockets).
//
// The HTTP client adds its own rules (fetch.go): no connections to the
// local network or the tablet itself, web ports only.
//
// The root xochitl process that shows the pages never gets untrusted
// bytes: only HTML that reader.go writes itself from an allowlist (all
// page text escaped) and JPEG files the backend encoded.

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	seccomp "github.com/elastic/go-seccomp-bpf"
	"github.com/landlock-lsm/go-landlock/landlock"
)

const sandboxUID = 65534 // nobody

// useLandlock: tests turn it off to check the other layers alone, as on
// kernels without Landlock (the reMarkable Paper Pro's).
var useLandlock = true

// blockedSyscalls fail inside the sandbox. On arm64 (Paper Pro, Paper Pro
// Move) the filter is an allowlist (seccomp_linux.go) and none of these
// may ever be on it; on 32-bit ARM (reMarkable 1/2), where the allowlist
// hasn't been traced, these are blocked one by one.
var blockedSyscalls = []string{
	// starting other programs
	"execve", "execveat", "fork", "vfork", "memfd_create",
	// other processes: memory, signals
	"ptrace", "process_vm_readv", "process_vm_writev", "kcmp", "pidfd_getfd", "pidfd_open",
	"pidfd_send_signal", "tkill", "rt_sigqueueinfo", "process_madvise",
	// file systems, devices, kernel
	"mount", "umount", "umount2", "pivot_root", "chroot", "chdir", "fchdir", "swapon", "swapoff",
	"mknod", "mknodat", "open_by_handle_at", "name_to_handle_at", "fsopen", "fsmount", "fsconfig",
	"fspick", "move_mount", "open_tree", "mount_setattr", "init_module", "finit_module", "delete_module",
	"kexec_load", "kexec_file_load", "reboot", "acct", "quotactl", "quotactl_fd", "syslog", "bpf",
	"perf_event_open", "userfaultfd", "lookup_dcookie", "keyctl", "add_key", "request_key", "uselib",
	"personality", "vhangup", "nfsservctl", "fanotify_init",
	// io_uring runs operations past seccomp
	"io_uring_setup", "io_uring_enter", "io_uring_register",
	// namespaces
	"unshare", "setns",
	// identities and capabilities
	"setuid", "setgid", "setreuid", "setregid", "setresuid", "setresgid", "setgroups", "setfsuid",
	"setfsgid", "capset", "setuid32", "setgid32", "setreuid32", "setregid32", "setresuid32",
	"setresgid32", "setgroups32", "setfsuid32", "setfsgid32",
	// clock and host
	"settimeofday", "clock_settime", "clock_settime64", "clock_adjtime", "clock_adjtime64", "adjtimex",
	"sethostname", "setdomainname",
	// sockets other than socket(AF_INET/AF_INET6) (see below)
	"socketpair",
}

// jailFiles are copied into the jail's /etc so that DNS keeps working.
var jailFiles = []string{"/etc/resolv.conf", "/etc/hosts"}

// enterSandbox applies all layers. jail is an empty folder to chroot into
// (created, owned by root); dirs are the data folders, already opened as
// os.Root and handed to the sandbox user. It returns a short description
// of the active layers.
func enterSandbox(jail string, dirs ...string) (string, error) {
	var layers []string
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return "", err
		}
	}
	root := os.Geteuid() == 0

	if root {
		for _, d := range dirs {
			if err := chownTree(d, sandboxUID, sandboxUID); err != nil {
				return "", fmt.Errorf("hand over %s: %w", d, err)
			}
		}
		if err := buildJail(jail); err != nil {
			return "", fmt.Errorf("jail: %w", err)
		}
	}

	if err := seccomp.SetNoNewPrivs(); err != nil {
		return "", fmt.Errorf("no_new_privs: %w", err)
	}

	// Landlock rules are set by path, so before the chroot.
	ro := []string{jail, "/etc", "/usr/share/ca-certificates", "/usr/lib/ssl"}
	if t, err := filepath.EvalSymlinks("/etc/resolv.conf"); err == nil && !strings.HasPrefix(t, "/etc/") {
		ro = append(ro, filepath.Dir(t))
	}
	if useLandlock {
		if err := landlock.V11.BestEffort().Restrict(
			landlock.RODirs(ro...).IgnoreIfMissing(),
			landlock.RWDirs(dirs...),
			landlock.ConnectTCP(80), landlock.ConnectTCP(443), landlock.ConnectTCP(8080), landlock.ConnectTCP(8443),
			landlock.ConnectTCP(53), landlock.BindUDP(0), landlock.ConnectSendUDP(53),
		); err != nil {
			return "", fmt.Errorf("landlock: %w", err)
		}
		if v := landlockABI(); v > 0 {
			layers = append(layers, fmt.Sprintf("Landlock v%d", v))
		}
	}

	if root {
		if err := syscall.Chroot(jail); err != nil {
			return "", fmt.Errorf("chroot: %w", err)
		}
		if err := syscall.Chdir("/"); err != nil {
			return "", fmt.Errorf("chdir: %w", err)
		}
		layers = append(layers, "chroot")

		if err := syscall.Setgroups([]int{}); err != nil {
			return "", fmt.Errorf("setgroups: %w", err)
		}
		if err := syscall.Setgid(sandboxUID); err != nil {
			return "", fmt.Errorf("setgid: %w", err)
		}
		if err := syscall.Setuid(sandboxUID); err != nil {
			return "", fmt.Errorf("setuid: %w", err)
		}
		if os.Getuid() != sandboxUID || os.Geteuid() != sandboxUID || os.Getgid() != sandboxUID {
			return "", fmt.Errorf("still running as uid %d", os.Geteuid())
		}
		layers = append([]string{"unprivileged user"}, layers...)
	}

	// Not dumpable: no core dumps with page data, and other processes of
	// the same user can't attach to it or read /proc/<pid>/mem.
	if _, _, e := syscall.RawSyscall(syscall.SYS_PRCTL, syscall.PR_SET_DUMPABLE, 0, 0); e != 0 {
		return "", fmt.Errorf("prctl dumpable: %w", e)
	}
	for _, l := range []struct {
		res int
		max uint64
	}{
		{syscall.RLIMIT_CORE, 0},
		{syscall.RLIMIT_FSIZE, 64 << 20}, // config and images are small
		{syscall.RLIMIT_NOFILE, 512},
	} {
		if err := syscall.Setrlimit(l.res, &syscall.Rlimit{Cur: l.max, Max: l.max}); err != nil {
			return "", fmt.Errorf("setrlimit %d: %w", l.res, err)
		}
	}

	if !seccomp.Supported() {
		return "", fmt.Errorf("the kernel has no seccomp")
	}
	mode, err := loadSeccomp(uint64(os.Getpid()))
	if err != nil {
		return "", fmt.Errorf("seccomp: %w", err)
	}
	layers = append(layers, mode)
	return strings.Join(layers, ", "), nil
}

// buildJail (re)creates the chroot folder: root-owned, read-only for the
// sandbox, with only what DNS needs.
func buildJail(jail string) error {
	if err := os.MkdirAll(filepath.Join(jail, "etc"), 0o755); err != nil {
		return err
	}
	for _, d := range []string{jail, filepath.Join(jail, "etc")} {
		if err := os.Chown(d, 0, 0); err != nil {
			return err
		}
		if err := os.Chmod(d, 0o755); err != nil {
			return err
		}
	}
	for _, f := range jailFiles {
		b, err := os.ReadFile(f)
		if err != nil {
			b = nil // e.g. no /etc/hosts: Go's resolver copes
		}
		dst := filepath.Join(jail, f)
		if err := os.WriteFile(dst, b, 0o644); err != nil {
			return err
		}
		if err := os.Chown(dst, 0, 0); err != nil {
			return err
		}
	}
	return nil
}

func chownTree(dir string, uid, gid int) error {
	return filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		return os.Lchown(p, uid, gid)
	})
}

// landlockABI returns the Landlock version the kernel supports (0 = none).
func landlockABI() int {
	const sysLandlockCreateRuleset = 444 // same number on arm and arm64
	const createRulesetVersion = 1
	v, _, errno := syscall.Syscall(sysLandlockCreateRuleset, 0, 0, createRulesetVersion)
	if errno != 0 {
		return 0
	}
	return int(v)
}
