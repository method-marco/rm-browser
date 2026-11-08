package main

// The seccomp filter. On arm64 it is an allowlist: only the system calls
// the backend was seen to make (scripts/trace-syscalls.sh, plus all calls
// Go's runtime can make) reach the kernel. Every other call fails with
// ENOSYS ("not implemented", so code with a fallback uses it) and never
// reaches the kernel code behind it; kernel bugs there can't be used.
//
// On top, for allowed calls:
//   - socket: IPv4 and IPv6 only (no Unix sockets to D-Bus or systemd,
//     no netlink, no packet sockets)
//   - kill, tgkill: only to its own process (Go signals its own threads)

import (
	"fmt"
	"runtime"
	"syscall"
	"unsafe"

	seccomp "github.com/elastic/go-seccomp-bpf"
	"github.com/elastic/go-seccomp-bpf/arch"
	"golang.org/x/net/bpf"
	"golang.org/x/sys/unix"
)

// allowedSyscalls (arm64), named as in go-seccomp-bpf's table (e.g.
// "fstatat", which strace shows as newfstatat).
var allowedSyscalls = []string{
	// Go runtime: memory, threads, scheduling, signals, timers
	"brk", "clock_gettime", "clone", "exit", "exit_group", "faccessat", "futex", "getpid", "gettid",
	"kill", "madvise", "mincore", "mmap", "munmap", "nanosleep", "pipe2", "rt_sigaction",
	"rt_sigprocmask", "rt_sigreturn", "sched_getaffinity", "sched_yield", "setitimer", "sigaltstack",
	"tgkill", "timer_create", "timer_delete", "timer_settime",
	// traced: files (through the os.Root folder handles)
	"openat", "close", "read", "pread64", "write", "fstat", "fstatat", "fcntl", "getdents64",
	"mkdirat", "renameat", "unlinkat", "utimensat", "readlinkat",
	// traced: network (sockets limited to IPv4/IPv6 by argumentRules)
	"socket", "connect", "setsockopt", "getsockopt", "getsockname", "getpeername", "epoll_ctl",
	"epoll_pwait", "uname", "getrandom",
	// rare paths: restarted calls, netpoll setup, I/O variants, fallbacks
	"restart_syscall", "epoll_create1", "eventfd2", "readv", "writev", "lseek", "shutdown", "sendto",
	"recvfrom", "sendmsg", "recvmsg", "renameat2", "faccessat2", "clock_nanosleep", "clock_getres",
	"getuid", "geteuid", "getgid", "getegid",
}

func argumentRules(pid uint64) seccomp.SyscallGroup {
	return seccomp.SyscallGroup{Action: seccomp.ActionErrno, NamesWithCondtions: []seccomp.NameWithConditions{
		{Name: "socket", Conditions: seccomp.ArgumentConditions{
			{Argument: 0, Operation: seccomp.NotEqual, Value: syscall.AF_INET},
			{Argument: 0, Operation: seccomp.NotEqual, Value: syscall.AF_INET6},
		}},
		{Name: "kill", Conditions: seccomp.ArgumentConditions{
			{Argument: 0, Operation: seccomp.NotEqual, Value: pid},
		}},
		{Name: "tgkill", Conditions: seccomp.ArgumentConditions{
			{Argument: 0, Operation: seccomp.NotEqual, Value: pid},
		}},
	}}
}

// loadSeccomp installs the filter for all threads and returns its name.
func loadSeccomp(pid uint64) (string, error) {
	info, err := arch.GetInfo("")
	if err != nil {
		return "", err
	}
	known := func(list []string) []string {
		var out []string
		for _, n := range list {
			if _, ok := info.SyscallNames[n]; ok {
				out = append(out, n)
			}
		}
		return out
	}
	if runtime.GOARCH != "arm64" {
		// 32-bit ARM: block the dangerous calls one by one.
		err := seccomp.LoadFilter(seccomp.Filter{
			NoNewPrivs: true,
			Flag:       seccomp.FilterFlagTSync,
			Policy: seccomp.Policy{
				DefaultAction: seccomp.ActionAllow,
				Syscalls: []seccomp.SyscallGroup{
					{Action: seccomp.ActionErrno, Names: known(blockedSyscalls)},
					argumentRules(pid),
				},
			},
		})
		return "seccomp", err
	}

	allowed, err := allowList()
	if err != nil {
		return "", err
	}
	nrs := make([]uint32, 0, len(allowed))
	for _, n := range allowed {
		if n == "socket" || n == "kill" || n == "tgkill" {
			continue // allowed only with the right arguments, below
		}
		nr, ok := info.SyscallNames[n]
		if !ok {
			// never drop a name silently: the call would be refused
			return "", fmt.Errorf("unknown system call %q in the allowlist", n)
		}
		nrs = append(nrs, uint32(nr))
	}
	insts, err := allowlistProgram(nrs, uint32(info.SyscallNames["socket"]), uint32(info.SyscallNames["kill"]),
		uint32(info.SyscallNames["tgkill"]), uint32(pid))
	if err != nil {
		return "", err
	}
	raw, err := bpf.Assemble(insts)
	if err != nil {
		return "", err
	}
	filter := make([]unix.SockFilter, len(raw))
	for i, r := range raw {
		filter[i] = unix.SockFilter{Code: r.Op, Jt: r.Jt, Jf: r.Jf, K: r.K}
	}
	prog := unix.SockFprog{Len: uint16(len(filter)), Filter: &filter[0]}
	if err := seccomp.SetNoNewPrivs(); err != nil {
		return "", err
	}
	if _, _, e := unix.Syscall(unix.SYS_SECCOMP, unix.SECCOMP_SET_MODE_FILTER,
		unix.SECCOMP_FILTER_FLAG_TSYNC, uintptr(unsafe.Pointer(&prog))); e != 0 {
		return "", e
	}
	runtime.KeepAlive(filter)
	return fmt.Sprintf("seccomp allowlist (%d calls)", len(nrs)+3), nil
}

// Offsets in struct seccomp_data (little endian).
const (
	offNr      = 0
	offArch    = 4
	offArg0Low = 16
)

// allowlistProgram builds the filter (arm64):
//
//	wrong architecture          -> kill the process
//	call in nrs                 -> allow
//	socket(AF_INET/AF_INET6)    -> allow, other socket families -> EPERM
//	kill/tgkill(own pid, ...)   -> allow, other processes -> EPERM
//	anything else               -> ENOSYS
//
// Written by hand rather than with go-seccomp-bpf's assembler: that one
// loads the call number only once, so rules after an argument check
// compare the argument instead.
func allowlistProgram(nrs []uint32, socketNr, killNr, tgkillNr, pid uint32) ([]bpf.Instruction, error) {
	const (
		retAllow  = unix.SECCOMP_RET_ALLOW
		retEPERM  = unix.SECCOMP_RET_ERRNO | uint32(unix.EPERM)
		retENOSYS = unix.SECCOMP_RET_ERRNO | uint32(unix.ENOSYS)
		retKill   = unix.SECCOMP_RET_KILL_PROCESS
	)
	// Layout:
	//   0 ld arch; 1 jeq AARCH64 ? 3 : 2; 2 ret kill
	//   3 ld nr
	//   4.. jeq nr_i -> ALLOW                (len(nrs))
	//   jeq socket -> SOCK; jeq kill -> KILL; jeq tgkill -> KILL
	//   ret ENOSYS
	//   SOCK: ld arg0; jeq AF_INET -> ALLOW; jeq AF_INET6 -> ALLOW; ret EPERM
	//   KILL: ld arg0; jeq pid -> ALLOW; ret EPERM
	//   ALLOW: ret allow
	n := len(nrs)
	sock := 4 + n + 3 + 1
	kill := sock + 4
	allow := kill + 3
	if allow-4 > 255 {
		return nil, fmt.Errorf("allowlist too long for short jumps")
	}
	p := []bpf.Instruction{
		bpf.LoadAbsolute{Off: offArch, Size: 4},
		bpf.JumpIf{Cond: bpf.JumpEqual, Val: unix.AUDIT_ARCH_AARCH64, SkipTrue: 1},
		bpf.RetConstant{Val: retKill},
		bpf.LoadAbsolute{Off: offNr, Size: 4},
	}
	to := func(target int) uint8 { return uint8(target - len(p) - 1) }
	for _, nr := range nrs {
		p = append(p, bpf.JumpIf{Cond: bpf.JumpEqual, Val: nr, SkipTrue: to(allow)})
	}
	p = append(p, bpf.JumpIf{Cond: bpf.JumpEqual, Val: socketNr, SkipTrue: to(sock)})
	p = append(p, bpf.JumpIf{Cond: bpf.JumpEqual, Val: killNr, SkipTrue: to(kill)})
	p = append(p, bpf.JumpIf{Cond: bpf.JumpEqual, Val: tgkillNr, SkipTrue: to(kill)})
	p = append(p, bpf.RetConstant{Val: retENOSYS})
	if len(p) != sock {
		return nil, fmt.Errorf("filter layout")
	}
	p = append(p, bpf.LoadAbsolute{Off: offArg0Low, Size: 4})
	p = append(p, bpf.JumpIf{Cond: bpf.JumpEqual, Val: unix.AF_INET, SkipTrue: to(allow)})
	p = append(p, bpf.JumpIf{Cond: bpf.JumpEqual, Val: unix.AF_INET6, SkipTrue: to(allow)})
	p = append(p, bpf.RetConstant{Val: retEPERM})
	if len(p) != kill {
		return nil, fmt.Errorf("filter layout")
	}
	p = append(p, bpf.LoadAbsolute{Off: offArg0Low, Size: 4})
	p = append(p, bpf.JumpIf{Cond: bpf.JumpEqual, Val: pid, SkipTrue: to(allow)})
	p = append(p, bpf.RetConstant{Val: retEPERM})
	if len(p) != allow {
		return nil, fmt.Errorf("filter layout")
	}
	p = append(p, bpf.RetConstant{Val: retAllow})
	return p, nil
}

// allowList checks that no blocked call slipped onto the allowlist.
func allowList() ([]string, error) {
	blocked := map[string]bool{}
	for _, n := range blockedSyscalls {
		blocked[n] = true
	}
	for _, n := range allowedSyscalls {
		if blocked[n] {
			return nil, fmt.Errorf("%s is both allowed and blocked", n)
		}
	}
	return allowedSyscalls, nil
}
