package livedns

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// DNS follows the tablet's network: the resolver asks the server the live
// resolv.conf names now, not the one in the copy made at start.
func TestLiveDNS(t *testing.T) {
	dir := t.TempDir()
	live := filepath.Join(dir, "run", "resolve")
	if err := os.MkdirAll(live, 0o755); err != nil {
		t.Fatal(err)
	}
	conf := filepath.Join(live, "resolv.conf")
	link := filepath.Join(dir, "etc-resolv.conf")
	_ = os.WriteFile(conf, []byte("# comment\nnameserver 192.168.1.1\nsearch home\n"), 0o644)
	if err := os.Symlink(conf, link); err != nil {
		t.Fatal(err)
	}
	l := Open(link)
	if l == nil {
		t.Fatal("no live DNS")
	}
	if got := l.target("192.168.1.1:53"); got != "192.168.1.1:53" {
		t.Errorf("same server: %s", got)
	}
	// another network: systemd-resolved replaces the file (a new inode)
	tmp := conf + ".new"
	_ = os.WriteFile(tmp, []byte("nameserver 10.0.0.1\nnameserver 10.0.0.2\n"), 0o644)
	if err := os.Rename(tmp, conf); err != nil {
		t.Fatal(err)
	}
	l.read = time.Time{}
	if got := l.target("192.168.1.1:53"); got != "10.0.0.1:53" {
		t.Errorf("old server kept: %s", got)
	}
	if got := l.target("10.0.0.2:53"); got != "10.0.0.2:53" {
		t.Errorf("listed server replaced: %s", got)
	}
	// file gone for a moment: the last good list stays
	_ = os.Remove(conf)
	l.read = time.Time{}
	if got := l.target("192.168.1.1:53"); got != "10.0.0.1:53" {
		t.Errorf("lost the servers: %s", got)
	}
	// a plain /etc/resolv.conf (no link): nothing to follow
	plain := filepath.Join(dir, "resolv.conf")
	_ = os.WriteFile(plain, []byte("nameserver 1.1.1.1\n"), 0o644)
	if Open(plain) != nil {
		t.Error("followed a plain file")
	}
}
