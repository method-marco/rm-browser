// Package livedns keeps DNS working inside the sandbox when the tablet
// changes networks.
package livedns

// The backend runs for days (AppLoad keeps it when the app closes), and
// inside its chroot /etc/resolv.conf is a copy made at start. When the
// tablet joins another network the DNS server changes, and the copy kept
// sending every lookup to the old one: each page waited for DNS timeouts.
//
// So the folder that holds the tablet's live resolv.conf (systemd-resolved
// rewrites it there) is opened before the sandbox, read-only, and every
// DNS query goes to the server it names now.

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

// Live reads the tablet's current resolv.conf through a folder handle.
type Live struct {
	Dir  *os.Root
	Name string

	mu      sync.Mutex
	read    time.Time
	servers []string
}

// Open opens the folder of the file /etc/resolv.conf points to. A
// plain /etc/resolv.conf (no link) gets nil: the copy is all there is, and
// the sandbox shouldn't hold a handle on all of /etc.
func Open(path string) *Live {
	t, err := filepath.EvalSymlinks(path)
	if err != nil || filepath.Dir(t) == filepath.Dir(path) {
		return nil
	}
	r, err := os.OpenRoot(filepath.Dir(t))
	if err != nil {
		return nil
	}
	return &Live{Dir: r, Name: filepath.Base(t)}
}

// Nameservers are the servers in the live file, read at most every 2 s.
func (l *Live) Nameservers() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	if time.Since(l.read) < 2*time.Second {
		return l.servers
	}
	l.read = time.Now()
	b, err := l.Dir.ReadFile(l.Name)
	if err != nil {
		return l.servers // keep the last good list
	}
	var s []string
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) >= 2 && f[0] == "nameserver" && net.ParseIP(f[1]) != nil {
			s = append(s, f[1])
		}
	}
	if len(s) > 0 {
		l.servers = s
	}
	return l.servers
}

// target: where a query Go's resolver meant for address (a server from
// its copy of resolv.conf) goes: the same server if it is still listed,
// else the current first one.
func (l *Live) target(address string) string {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return address
	}
	s := l.Nameservers()
	if len(s) == 0 || slices.Contains(s, host) {
		return address
	}
	return net.JoinHostPort(s[0], port)
}

// Resolver uses the live servers.
func (l *Live) Resolver() *net.Resolver {
	return &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, network, l.target(address))
	}}
}
