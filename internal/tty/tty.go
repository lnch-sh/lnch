// Package tty detects interactive terminals.
package tty

import "os"

// Is reports whether f is a character device.
func Is(f *os.File) bool {
	if f == nil {
		return false
	}
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}
