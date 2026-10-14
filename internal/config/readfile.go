package config

import (
	"fmt"
	"os"
	"syscall"
)

// ReadFile parses a committed config file from disk. It refuses anything that
// is not a plain regular file: a committed symlink to /dev/zero, a FIFO or
// /etc/passwd must never be read, let alone block a hook. A missing file
// returns (nil, nil).
func ReadFile(path string, kind Kind) (*File, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		// ELOOP (macOS) / EMLINK: O_NOFOLLOW hit a symlink.
		return &File{Source: path, Keys: map[string]string{}, Warnings: []Warning{{
			Source: path, Message: fmt.Sprintf("not read: %v (committed config must be a regular file, not a symlink)", err)}}}, nil
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return &File{Source: path, Keys: map[string]string{}, Warnings: []Warning{{
			Source: path, Message: "not read: committed config must be a regular file"}}}, nil
	}
	return ParseFile(f, path, kind), nil
}
