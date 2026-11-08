#!/bin/sh
# Record the system calls the backend makes once its sandbox is active,
# to maintain the seccomp allowlist (backend/seccomp_linux.go).
#
#   scripts/trace-syscalls.sh            -> calls made (strace names)
#   FAILED=1 scripts/trace-syscalls.sh   -> calls that failed, with errno
#
# Builds with -tags seccomptrace (no Landlock: it hangs under strace) and
# runs scripts/scenarios.py in Docker. Note: strace says "newfstatat"
# where go-seccomp-bpf's table says "fstatat".
set -e
cd "$(dirname "$0")/.."
mkdir -p build/trace
docker run --rm -v "$PWD":/repo -w /repo/backend golang:1.26-bookworm \
	sh -c 'CGO_ENABLED=0 go build -tags seccomptrace -o /repo/build/trace/entry .'
flags="-f -qq"
[ -n "$FAILED" ] && flags="$flags -Z"
docker run --rm --cap-add SYS_PTRACE --security-opt seccomp=unconfined -v "$PWD":/repo -w /repo golang:1.26-bookworm sh -c "
	apt-get update -qq >/dev/null && apt-get install -y -qq python3 strace >/dev/null 2>&1
	timeout 1200 python3 -u scripts/scenarios.py strace $flags -o build/trace/trace.txt build/trace/entry"
python3 - <<'PY'
import re, collections
lines = open("build/trace/trace.txt").read().splitlines()
start = next(i for i, l in enumerate(lines) if "seccomp(SECCOMP_SET_MODE_FILTER" in l)
calls = collections.Counter()
for l in lines[start + 1:]:
    m = re.match(r"\d+\s+(?:<\.\.\. )?([a-z_0-9]+)[( ]", l)
    if m:
        err = re.search(r"= -1 (E[A-Z]+)", l)
        calls[m.group(1) + (" -> " + err.group(1) if err else "")] += 1
for name, n in sorted(calls.items()):
    print(f"{n:7} {name}")
PY
