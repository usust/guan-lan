//go:build darwin || linux

package storage

import (
	"fmt"
	"golang.org/x/sys/unix"
	"math"
)

// AvailableBytes reports space available to the calling account, not root.
func AvailableBytes(path string) (uint64, error) {
	var s unix.Statfs_t
	if err := unix.Statfs(path, &s); err != nil {
		return 0, err
	}
	if s.Bsize <= 0 {
		return 0, fmt.Errorf("invalid filesystem block size")
	}
	blocks, size := uint64(s.Bavail), uint64(s.Bsize)
	if blocks > math.MaxUint64/size {
		return 0, fmt.Errorf("filesystem capacity overflow")
	}
	return blocks * size, nil
}

func DiskUsage(path string) (uint64, uint64, error) {
	var s unix.Statfs_t
	if err := unix.Statfs(path, &s); err != nil {
		return 0, 0, err
	}
	if s.Bsize <= 0 {
		return 0, 0, fmt.Errorf("invalid filesystem block size")
	}
	return (uint64(s.Blocks) - uint64(s.Bfree)) * uint64(s.Bsize), uint64(s.Blocks) * uint64(s.Bsize), nil
}
