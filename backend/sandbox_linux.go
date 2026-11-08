package main

// The backend is the only part of the browser that talks to the network
// and handles untrusted data, so it locks itself in before loading
// anything. AppLoad starts it as root; the sandbox has four layers:
//
//  1. Drop root: switch to the unprivileged user nobody (uid/gid 65534)
//     without supplementary groups. Its own folders are handed over first;
//     everything else on the tablet (notebooks, xochitl, system files) is
//     out of reach of an unprivileged user.
//  2. no_new_privs: nothing the process does can ever regain privileges.
//  3. Landlock (Linux security module, used if the kernel has it): file
//     access only to /etc (read-only: DNS, certificates) and its own two
//     folders; network only outgoing connections to web ports and DNS, no
//     listening sockets; no signals to or abstract sockets of processes
//     outside the sandbox.
//  4. seccomp: system calls that a browser backend never needs fail with
//     EPERM: starting programs (execve), debugging other processes,
//     mounting, kernel modules, BPF, namespaces, changing user ids, ...
//
// In addition, the root xochitl process that shows the pages never gets
// untrusted bytes: only HTML that reader.go writes itself from an
// allowlist (all page text escaped) and JPEG files the backend encoded.

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	seccomp "github.com/elastic/go-seccomp-bpf"
	"github.com/elastic/go-seccomp-bpf/arch"
	"github.com/landlock-lsm/go-landlock/landlock"
)

const sandboxUID = 65534 // nobody

// blockedSyscalls fail with EPERM inside the sandbox. Names that don't
// exist on the running architecture are skipped.
var blockedSyscalls = []string{
	// starting other programs
	"execve", "execveat", "fork", "vfork",
	// other processes' memory
	"ptrace", "process_vm_readv", "process_vm_writev", "kcmp", "pidfd_getfd",
	// file systems, devices, kernel
	"mount", "umount", "umount2", "pivot_root", "chroot", "swapon", "swapoff", "mknod", "mknodat",
	"open_by_handle_at", "name_to_handle_at", "fsopen", "fsmount", "fsconfig", "move_mount", "open_tree",
	"mount_setattr", "init_module", "finit_module", "delete_module", "kexec_load", "kexec_file_load",
	"reboot", "acct", "quotactl", "syslog", "bpf", "perf_event_open", "userfaultfd", "lookup_dcookie",
	"keyctl", "add_key", "request_key", "uselib", "personality", "vhangup", "nfsservctl",
	// namespaces
	"unshare", "setns",
	// identities and capabilities
	"setuid", "setgid", "setreuid", "setregid", "setresuid", "setresgid", "setgroups", "setfsuid",
	"setfsgid", "capset", "setuid32", "setgid32", "setreuid32", "setregid32", "setresuid32",
	"setresgid32", "setgroups32", "setfsuid32", "setfsgid32",
	// clock and host
	"settimeofday", "clock_settime", "clock_settime64", "clock_adjtime", "clock_adjtime64", "adjtimex",
	"sethostname", "setdomainname",
}

// enterSandbox applies all layers. dirs are the folders the backend may
// write to; they are created and handed to the sandbox user first. It
// returns a short description of the active layers.
func enterSandbox(dirs ...string) (string, error) {
	var layers []string
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return "", err
		}
	}

	if os.Geteuid() == 0 {
		for _, d := range dirs {
			if err := chownTree(d, sandboxUID, sandboxUID); err != nil {
				return "", fmt.Errorf("hand over %s: %w", d, err)
			}
		}
	}

	if err := seccomp.SetNoNewPrivs(); err != nil {
		return "", fmt.Errorf("no_new_privs: %w", err)
	}

	// Landlock. Files resolv.conf may point to (systemd-resolved, ...)
	// need to be readable too.
	ro := []string{"/etc", "/usr/share/ca-certificates", "/usr/share/zoneinfo", "/usr/lib/ssl"}
	if t, err := filepath.EvalSymlinks("/etc/resolv.conf"); err == nil && !strings.HasPrefix(t, "/etc/") {
		ro = append(ro, filepath.Dir(t))
	}
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

	// Drop root after Landlock: its rules open the folders by path, and
	// the sandbox user may not be allowed to look into /home/root. The
	// backend keeps using them through handles opened as root (os.Root).
	if os.Geteuid() == 0 {
		if err := syscall.Setgroups([]int{}); err != nil {
			return "", fmt.Errorf("setgroups: %w", err)
		}
		if err := syscall.Setgid(sandboxUID); err != nil {
			return "", fmt.Errorf("setgid: %w", err)
		}
		if err := syscall.Setuid(sandboxUID); err != nil {
			return "", fmt.Errorf("setuid: %w", err)
		}
		if os.Getuid() != sandboxUID || os.Geteuid() != sandboxUID {
			return "", fmt.Errorf("still running as uid %d", os.Geteuid())
		}
		layers = append([]string{"unprivileged user"}, layers...)
	}

	if !seccomp.Supported() {
		return strings.Join(layers, ", "), nil
	}
	info, err := arch.GetInfo("")
	if err != nil {
		return "", fmt.Errorf("seccomp: %w", err)
	}
	var names []string
	for _, n := range blockedSyscalls {
		if _, ok := info.SyscallNames[n]; ok {
			names = append(names, n)
		}
	}
	err = seccomp.LoadFilter(seccomp.Filter{
		NoNewPrivs: true,
		Flag:       seccomp.FilterFlagTSync,
		Policy: seccomp.Policy{
			DefaultAction: seccomp.ActionAllow,
			Syscalls:      []seccomp.SyscallGroup{{Action: seccomp.ActionErrno, Names: names}},
		},
	})
	if err != nil {
		return "", fmt.Errorf("seccomp: %w", err)
	}
	layers = append(layers, "seccomp")
	return strings.Join(layers, ", "), nil
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
