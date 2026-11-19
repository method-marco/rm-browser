package sandbox

import (
	"testing"

	"github.com/elastic/go-seccomp-bpf/arch"
)

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
