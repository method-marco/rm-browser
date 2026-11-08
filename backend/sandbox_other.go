//go:build !linux

package main

import "os"

// enterSandbox: the tablet runs Linux; elsewhere (tests on a desktop) only
// the folders are created.
func enterSandbox(jail string, readOnly []string, dirs ...string) (string, error) {
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return "", err
		}
	}
	return "none", nil
}
