//go:build seccomptrace

package main

// Build with -tags seccomptrace to record the system calls the backend
// makes (scripts/trace-syscalls.sh): Landlock's all-threads restriction
// hangs under strace, so this build skips it. Never used for releases.
func init() { useLandlock = false }
