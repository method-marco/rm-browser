//go:build !linux

package sandbox

import "os"

// UseLandlock: see sandbox_linux.go.
var UseLandlock = true

// Options: see sandbox_linux.go.
type Options struct {
	Jail     string
	Dirs     []string
	Shared   []string
	ReadOnly []string
	TCPPorts []uint16
}

// Enter: the tablet runs Linux; elsewhere (tests on a desktop) only
// the folders are created.
func Enter(o Options) (string, error) {
	for _, d := range o.Dirs {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return "", err
		}
	}
	return "none", nil
}
